// Command winsetup is Stockroom-Setup.exe, the graphical installer for
// Windows. It is the wizard page `stockroom setup --gui` shows on Linux and
// macOS, in front of scripts/get.ps1: the page asks for the failsafe admin,
// Windows asks for permission, and get.ps1 installs Ubuntu under WSL 2 and
// Stockroom inside it.
//
// It is an installer, not a Windows build of Stockroom. Stockroom itself
// still runs only inside WSL (docs/decisions.md, 2026-10-06).
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"stockroom/internal/setup"
	"stockroom/internal/wizard"
	"stockroom/scripts"
)

const (
	// appURL is where a Windows browser reaches the server inside WSL.
	appURL = "http://localhost:8080"
	// exitRestart is get.ps1 saying Windows must restart before it can go on.
	exitRestart = 3
	// sentinel starts the last line of the log: the script's exit code.
	sentinel = "STOCKROOM-EXIT "

	batchName    = "run.cmd"
	scriptName   = "get.ps1"
	logName      = "install.log"
	passwordName = "admin.pw"
)

// steps are get.ps1's, as its Say announces them.
var steps = []string{"Ubuntu", "systemd", "Stockroom", "Startup task"}

// newWizard is the installer's page. elevate runs a batch file as
// administrator without waiting for it, and open opens a URL.
func newWizard(elevate func(batch string) error, open func(url string) error) *wizard.Wizard {
	return &wizard.Wizard{
		Platform: "Windows",
		Steps:    steps,
		Notes: []string{
			"Installs Ubuntu 24.04 under WSL 2, Windows's own Linux layer, and Stockroom inside it. Stockroom has no native Windows version.",
			"Windows asks for permission to make changes, and near the end for your Windows password, so Stockroom starts at boot with nobody logged in. Look for that dialog behind this window if the install seems to wait.",
			"A PC that has never had WSL needs a restart partway. This page says so; open Stockroom-Setup.exe again afterwards and it carries on.",
			"Needs the internet, and takes several minutes. Running it again later is the upgrade.",
		},
		AskAdmin: true,
		Check: func(a wizard.Answers) error {
			return setup.CheckFailsafe(a.AdminNumber, a.AdminPassword)
		},
		Run: func(ctx context.Context, a wizard.Answers, out io.Writer) (wizard.Result, error) {
			dir, err := os.MkdirTemp("", "stockroom-setup-")
			if err != nil {
				return wizard.Result{}, err
			}
			// The folder holds the failsafe admin's password until now.
			defer os.RemoveAll(dir)
			if err := prepare(dir, a); err != nil {
				return wizard.Result{}, err
			}
			if err := elevate(filepath.Join(dir, batchName)); err != nil {
				return wizard.Result{}, err
			}
			code, err := follow(ctx, filepath.Join(dir, logName), out, 300*time.Millisecond)
			if err != nil {
				return wizard.Result{}, err
			}
			return outcome(code)
		},
		Open: open,
	}
}

// prepare writes what the elevated run needs into dir: get.ps1, the password
// file when there is a failsafe admin, and the batch file that runs them.
func prepare(dir string, a wizard.Answers) error {
	if err := os.WriteFile(filepath.Join(dir, scriptName), scripts.GetPS1, 0o600); err != nil {
		return err
	}
	if a.AdminNumber != "" {
		if err := os.WriteFile(filepath.Join(dir, passwordName), []byte(a.AdminPassword+"\r\n"), 0o600); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(dir, batchName), []byte(batch(a)), 0o600)
}

// batch is the batch file. A batch file rather than a PowerShell wrapper,
// because cmd redirects the script's output at the handle: Windows PowerShell
// turns a native program's stderr into a terminating error when PowerShell
// itself does the redirecting, and apt writes plenty to stderr.
//
// Every path is %~dp0, the batch file's own folder, so no path is ever
// quoted into the file. The number is digits only (setup.CheckFailsafe), and
// the password stays in its own file.
func batch(a wizard.Answers) string {
	args := " -Unattended"
	if a.AdminNumber != "" {
		args += " -AdminNumber " + a.AdminNumber + ` -AdminPasswordFile "%~dp0` + passwordName + `"`
	}
	lines := []string{
		"@echo off",
		// wsl.exe's own messages are UTF-16 without this.
		"set WSL_UTF8=1",
		`powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0` + scriptName + `"` + args + ` > "%~dp0` + logName + `" 2>&1`,
		// The redirect comes first: "echo … 3>> file" would redirect handle 3.
		`>> "%~dp0` + logName + `" echo ` + sentinel + "%errorlevel%",
	}
	return strings.Join(lines, "\r\n") + "\r\n"
}

// follow copies the log to out as it grows and returns the exit code on its
// last line. The elevated process is another process tree that ShellExecute
// gives no handle to, so the log is the only thing to wait on.
func follow(ctx context.Context, path string, out io.Writer, poll time.Duration) (int, error) {
	var offset int64
	var partial string
	for {
		chunk, err := readFrom(path, offset)
		if err != nil {
			return 0, err
		}
		offset += int64(len(chunk))
		text := partial + string(chunk)
		for {
			i := strings.IndexByte(text, '\n')
			if i < 0 {
				break
			}
			line := clean(text[:i])
			text = text[i+1:]
			if rest, ok := strings.CutPrefix(line, sentinel); ok {
				code, err := strconv.Atoi(strings.TrimSpace(rest))
				if err != nil {
					return 0, fmt.Errorf("the install ended without saying how: %q", line)
				}
				return code, nil
			}
			fmt.Fprintln(out, line)
		}
		partial = text
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(poll):
		}
	}
}

// readFrom is the file from offset on, or nothing while it doesn't exist yet.
func readFrom(path string, offset int64) ([]byte, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	return io.ReadAll(f)
}

// clean makes a log line safe to show: the log mixes PowerShell's console
// code page, UTF-8 from Ubuntu, and the odd UTF-16 line from wsl.exe.
func clean(line string) string {
	line = strings.ReplaceAll(line, "\x00", "")
	line = strings.TrimRight(line, "\r")
	return strings.ToValidUTF8(line, "?")
}

// outcome turns get.ps1's exit code into the page's last screen.
func outcome(code int) (wizard.Result, error) {
	switch code {
	case 0:
		return wizard.Result{
			URL:     appURL,
			Message: `Stockroom is running inside Ubuntu and starts when this PC does. Backups go to Documents\Stockroom Backups until you choose another folder. Set this PC never to sleep (Settings, System, Power), then open Stockroom and follow the setup guide.`,
		}, nil
	case exitRestart:
		return wizard.Result{
			Message: "Windows needs a restart to finish installing WSL. Restart this PC, then open Stockroom-Setup.exe again. It carries on from here.",
		}, nil
	}
	return wizard.Result{}, fmt.Errorf("the install stopped (exit code %d). Details, below, says where", code)
}

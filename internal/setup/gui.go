package setup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"stockroom/internal/wizard"
)

const (
	// caskName is the Homebrew cask the macOS installer app installs.
	caskName     = "kathir-d/tap/stockroom"
	packagesStep = "Homebrew packages"
	// homebrewPkg is where Homebrew publishes its own graphical installer.
	homebrewPkg = "https://github.com/Homebrew/brew/releases/latest"
)

// GUI is `stockroom setup --gui`: the same setup, asked and shown in a
// browser page instead of a terminal. It is what the Stockroom Setup launcher
// in the .deb and the macOS installer app run.
//
// The page replaces the prompts and nothing else. Its Install button calls
// the steps Setup runs, with the flags Setup takes.
func GUI(ctx context.Context, env *Env, opts Options) error {
	if err := useConfig(env, opts.ConfigFile); err != nil {
		return err
	}
	switch env.GOOS {
	case "linux":
		// Checked before the page opens. Otherwise the first thing the page
		// could say is that it should have been started another way.
		if !env.Root {
			return errors.New("the graphical setup needs root too: open Stockroom Setup from the applications menu, or run sudo stockroom setup --gui")
		}
	case "darwin":
	default:
		return fmt.Errorf("setup supports Linux and macOS, not %s. On Windows, run Stockroom-Setup.exe (docs/INSTALL.md)", env.GOOS)
	}

	_, err := os.Stat(env.Paths.ConfigFile)
	hasConfig := err == nil
	fromFlags := opts.AdminNumber != "" || opts.AdminPasswordFile != ""
	packages := env.GOOS == "darwin" && opts.InstallPackages

	w := &wizard.Wizard{
		Platform: map[string]string{"linux": "Linux", "darwin": "macOS"}[env.GOOS],
		Steps:    stepNames,
		Notes:    guiNotes(env, packages),
		AskAdmin: !hasConfig && !fromFlags,
		Camera:   !env.WSL && !opts.WithCamera,
		Check: func(a wizard.Answers) error {
			return checkFailsafe(failsafe{number: a.AdminNumber, password: a.AdminPassword})
		},
		Log: env.Out,
	}
	switch {
	case hasConfig:
		w.AdminNote = fmt.Sprintf("This computer already has a Stockroom config (%s). Setup keeps it, the failsafe admin in it included, and repairs whatever is missing.", env.Paths.ConfigFile)
	case fromFlags:
		w.AdminNote = "The failsafe admin was given on the command line."
	}
	if env.WSL {
		w.Platform = "Linux under WSL"
	}
	if packages {
		w.Steps = append([]string{packagesStep}, stepNames...)
		w.Need = func() *wizard.Need {
			if _, err := os.Stat(brewBin(env)); err == nil {
				return nil
			}
			return &wizard.Need{
				Message:  "Stockroom on a Mac installs through Homebrew, which supplies its database, and this Mac doesn't have Homebrew yet. Download Homebrew's installer (the .pkg file under Assets), run it, then come back here and press Check again.",
				LinkText: "Get Homebrew",
				LinkURL:  homebrewPkg,
			}
		}
	}
	if !opts.NoOpen {
		w.Open = env.Open
	}

	w.Run = func(ctx context.Context, a wizard.Answers, out io.Writer) (wizard.Result, error) {
		// A copy: a second try after a failure starts from the same machine.
		e := *env
		e.Out = out
		o := opts
		o.NoOpen, o.NonInteractive = true, true
		o.WithCamera = opts.WithCamera || a.WithCamera
		if w.AskAdmin {
			o.AdminNumber, o.adminPassword = a.AdminNumber, a.AdminPassword
		}

		if e.GOOS == "darwin" && !e.Root {
			// macOS setup runs as the installing user and reaches for sudo
			// to write the LaunchDaemons. Started from Finder there is no
			// terminal for sudo to ask on, so it asks in a dialog.
			if r, ok := e.Run.(ExecRunner); ok && r.Askpass == "" {
				askpass, err := writeAskpass()
				if err != nil {
					return wizard.Result{}, err
				}
				defer os.Remove(askpass)
				e.Run = ExecRunner{Askpass: askpass}
			}
		}
		if packages {
			if err := installPackages(ctx, &e); err != nil {
				return wizard.Result{}, fmt.Errorf("setup stopped at homebrew packages: %w", err)
			}
		}
		addr, err := install(ctx, &e, o)
		if err != nil {
			return wizard.Result{}, fmt.Errorf("setup stopped at %w", err)
		}
		return wizard.Result{
			URL:     "http://" + addr,
			Message: "Stockroom is running and starts by itself when this computer does. Open it and follow the setup guide: lending rules, categories, equipment, the roster and backups.",
		}, nil
	}
	return w.Serve(ctx)
}

// guiNotes says what Install is about to do, on the page's first card.
func guiNotes(e *Env, packages bool) []string {
	var notes []string
	if packages {
		notes = append(notes, "Installs Stockroom, PostgreSQL 17 and rclone with Homebrew. This is the slow part: a few minutes on a school connection.")
	}
	notes = append(notes,
		"Creates a stockroom database in this computer's own PostgreSQL, reachable from this computer only.",
		fmt.Sprintf("Writes the config to %s and keeps photos and backups in %s.", e.Paths.ConfigFile, e.Paths.DataDir),
	)
	if e.GOOS == "darwin" {
		notes = append(notes, "Asks for your Mac password once, to install the two background services that start PostgreSQL and Stockroom at boot with nobody logged in.")
	} else {
		notes = append(notes, "Installs a service that starts Stockroom at boot, with nobody logged in.")
	}
	return append(notes, "Safe to run again. It repairs what is missing and never overwrites the config or the database.")
}

func brewBin(e *Env) string {
	return filepath.Join(e.Paths.BrewPrefix, "bin", "brew")
}

// installPackages installs the cask, which brings postgresql@17 and rclone
// with it, then points setup at the binary Homebrew linked. The installer app
// runs a copy of stockroom from its own bundle, and a LaunchDaemon must not
// name a path inside an app someone will drag to the Trash.
func installPackages(ctx context.Context, e *Env) error {
	e.say("== %s ==", packagesStep)
	prefix := e.Paths.BrewPrefix
	linked := filepath.Join(prefix, "bin", "stockroom")
	missing := false
	for _, p := range []string{
		linked,
		filepath.Join(prefix, "opt", brewFormula, "bin", "postgres"),
		filepath.Join(prefix, "bin", "rclone"),
	} {
		if _, err := os.Stat(p); err != nil {
			missing = true
		}
	}
	if missing {
		e.say("  installing %s with Homebrew, which takes a few minutes ...", caskName)
		// Homebrew refuses to run as root.
		asUser := ""
		if e.Root {
			asUser = e.SudoUser
		}
		if _, err := e.Run.Run(ctx, Cmd{
			Name: brewBin(e), Args: []string{"install", "--cask", caskName},
			User: asUser, Env: []string{"NONINTERACTIVE=1"}, Stream: e.Out,
		}); err != nil {
			return err
		}
	}
	if _, err := os.Stat(linked); err != nil {
		return fmt.Errorf("Homebrew finished, but %s is not there. Run this in Terminal and read what it says: brew install --cask %s", linked, caskName)
	}
	e.Paths.Binary = linked
	e.Paths.CameraDir = brewCameraDir(prefix, linked)
	e.ok("Stockroom, %s and rclone are installed; the service will run %s", brewFormula, linked)
	return nil
}

// askpassScript asks for the password in a dialog and prints it, which is
// what sudo -A expects of the program in SUDO_ASKPASS.
const askpassScript = `#!/bin/sh
exec /usr/bin/osascript \
  -e 'display dialog "Stockroom Setup needs your Mac password to install the services that start it at boot." with title "Stockroom Setup" default answer "" with hidden answer' \
  -e 'text returned of result'
`

func writeAskpass() (string, error) {
	f, err := os.CreateTemp("", "stockroom-askpass-*.sh")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.WriteString(askpassScript); err != nil {
		return "", err
	}
	return f.Name(), f.Chmod(0o700)
}

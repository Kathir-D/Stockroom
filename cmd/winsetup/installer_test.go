package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"stockroom/internal/wizard"
	"stockroom/scripts"
)

// TestBatch: the password is never in the batch file, the number is, and the
// exit code is appended in the one form cmd doesn't misread.
func TestBatch(t *testing.T) {
	with := batch(wizard.Answers{AdminNumber: "900100", AdminPassword: "hunter2hunter2"})
	for _, want := range []string{
		"@echo off\r\n",
		`-File "%~dp0get.ps1" -Unattended -AdminNumber 900100 -AdminPasswordFile "%~dp0admin.pw" > "%~dp0install.log" 2>&1` + "\r\n",
		`>> "%~dp0install.log" echo STOCKROOM-EXIT %errorlevel%` + "\r\n",
	} {
		if !strings.Contains(with, want) {
			t.Errorf("batch lacks %q:\n%s", want, with)
		}
	}
	if strings.Contains(with, "hunter2") {
		t.Error("the password is in the batch file")
	}
	without := batch(wizard.Answers{})
	if strings.Contains(without, "-Admin") || !strings.Contains(without, " -Unattended > ") {
		t.Errorf("batch with no failsafe admin:\n%s", without)
	}
}

// TestGetPS1TakesWhatTheBatchPasses holds the batch file and the script to
// each other: a parameter renamed in one and not the other would only show
// on a real Windows machine.
func TestGetPS1TakesWhatTheBatchPasses(t *testing.T) {
	script := string(scripts.GetPS1)
	for _, want := range []string{
		"[switch]$Unattended", "[string]$AdminNumber", "[string]$AdminPasswordFile",
		"exit 3", "if ($Unattended) { exit 0 }",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("get.ps1 lacks %q", want)
		}
	}
	for _, step := range steps {
		if !strings.Contains(script, `Say "`+step+`"`) {
			t.Errorf("get.ps1 never announces the %q step", step)
		}
	}
}

func TestPrepare(t *testing.T) {
	dir := t.TempDir()
	if err := prepare(dir, wizard.Answers{AdminNumber: "900100", AdminPassword: "pässword123"}); err != nil {
		t.Fatal(err)
	}
	pw, _ := os.ReadFile(filepath.Join(dir, passwordName))
	if string(pw) != "pässword123\r\n" {
		t.Errorf("password file = %q", pw)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, scriptName)); !bytes.Equal(got, scripts.GetPS1) {
		t.Error("get.ps1 was not written whole")
	}

	bare := t.TempDir()
	if err := prepare(bare, wizard.Answers{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(bare, passwordName)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a password file was written with no failsafe admin: %v", err)
	}
}

// TestFollow reads a log that arrives in pieces, the way the elevated
// script writes it, and stops at the exit code.
func TestFollow(t *testing.T) {
	path := filepath.Join(t.TempDir(), logName)
	go func() {
		time.Sleep(20 * time.Millisecond) // the file doesn't exist at first
		f, _ := os.Create(path)
		defer f.Close()
		for _, piece := range []string{"== Ubuntu ==\r\nInstall", "ing\r\nw\x00s\x00l\x00\r\n", "bad \xff byte\r\n", sentinel + "3\r\n", "after\r\n"} {
			f.WriteString(piece)
			time.Sleep(10 * time.Millisecond)
		}
	}()
	var out bytes.Buffer
	code, err := follow(context.Background(), path, &out, 5*time.Millisecond)
	if err != nil || code != 3 {
		t.Fatalf("follow = %d, %v", code, err)
	}
	if got, want := out.String(), "== Ubuntu ==\nInstalling\nwsl\nbad ? byte\n"; got != want {
		t.Errorf("out = %q, want %q", got, want)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := follow(ctx, filepath.Join(t.TempDir(), "never"), &out, 5*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("follow on a log that never comes = %v", err)
	}
}

func TestOutcome(t *testing.T) {
	if res, err := outcome(0); err != nil || res.URL != appURL {
		t.Errorf("outcome(0) = %+v, %v", res, err)
	}
	if res, err := outcome(exitRestart); err != nil || res.URL != "" || !strings.Contains(res.Message, "Restart") {
		t.Errorf("outcome(restart) = %+v, %v", res, err)
	}
	if _, err := outcome(1); err == nil {
		t.Error("outcome(1) is not an error")
	}
}

// TestRunEndToEnd stands in for Windows: "elevating" writes the log the
// script would, and the page gets its lines and its ending. The temp folder,
// which held the password, is gone afterwards.
func TestRunEndToEnd(t *testing.T) {
	var dir string
	w := newWizard(func(batchPath string) error {
		dir = filepath.Dir(batchPath)
		if pw, _ := os.ReadFile(filepath.Join(dir, passwordName)); string(pw) != "password123\r\n" {
			t.Errorf("password file at elevation = %q", pw)
		}
		return os.WriteFile(filepath.Join(dir, logName), []byte("== Stockroom ==\r\n"+sentinel+"0\r\n"), 0o600)
	}, nil)

	if err := w.Check(wizard.Answers{AdminNumber: "12ab", AdminPassword: "password123"}); err == nil {
		t.Error("a failsafe admin number with letters passed Check")
	}
	var out bytes.Buffer
	res, err := w.Run(context.Background(), wizard.Answers{AdminNumber: "900100", AdminPassword: "password123"}, &out)
	if err != nil || res.URL != appURL || out.String() != "== Stockroom ==\n" {
		t.Fatalf("Run = %+v, %v, out %q", res, err, out.String())
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the temp folder outlived the install: %v", err)
	}

	refused := newWizard(func(string) error { return errors.New("no permission") }, nil)
	if _, err := refused.Run(context.Background(), wizard.Answers{}, &out); err == nil || err.Error() != "no permission" {
		t.Errorf("Run after a refused elevation = %v", err)
	}
}

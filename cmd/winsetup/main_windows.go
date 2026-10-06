package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"golang.org/x/sys/windows"
)

const title = "Stockroom Setup"

func main() {
	w := newWizard(elevate, func(url string) error {
		if err := openBrowser(url); err != nil {
			// The exe is built without a console, so a box is the only
			// place left to say where the page is.
			alert(fmt.Sprintf("Stockroom Setup could not open your browser (%v).\n\nOpen this address yourself:\n%s", err, url))
		}
		return nil
	})
	if err := w.Serve(context.Background()); err != nil {
		alert("Stockroom Setup could not start: " + err.Error())
		os.Exit(1)
	}
}

// elevate starts the batch file as administrator, hidden, and returns once
// Windows has agreed to. The UAC prompt is the one moment this installer
// asks for more than the person's own rights.
func elevate(batch string) error {
	verb, _ := windows.UTF16PtrFromString("runas")
	file, err := windows.UTF16PtrFromString(filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe"))
	if err != nil {
		return err
	}
	// /s with doubled quotes is the one form of /c that survives a space or
	// an ampersand in the path.
	args, err := windows.UTF16PtrFromString(`/s /c ""` + batch + `""`)
	if err != nil {
		return err
	}
	err = windows.ShellExecute(0, verb, file, args, nil, windows.SW_HIDE)
	if errors.Is(err, windows.ERROR_CANCELLED) {
		return errors.New("Windows wasn't given permission to make changes, so nothing was installed. Press Try again and choose Yes when Windows asks")
	}
	return err
}

func openBrowser(url string) error {
	return exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "rundll32.exe"), "url.dll,FileProtocolHandler", url).Start()
}

func alert(text string) {
	t, _ := windows.UTF16PtrFromString(text)
	c, _ := windows.UTF16PtrFromString(title)
	_, _ = windows.MessageBox(0, t, c, windows.MB_OK|windows.MB_ICONWARNING)
}

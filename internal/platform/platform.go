// Package platform answers the few questions about the machine that setup,
// doctor and open share: is this WSL, is systemd running, how to open a URL.
package platform

import (
	"errors"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strings"
)

// IsWSL reports whether this is Linux running under Windows Subsystem for
// Linux, from WSL_DISTRO_NAME or the interop file WSL registers. Under sudo
// the variable is usually gone, so the file is the check that matters.
func IsWSL() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	if os.Getenv("WSL_DISTRO_NAME") != "" {
		return true
	}
	_, err := os.Stat("/proc/sys/fs/binfmt_misc/WSLInterop")
	return err == nil
}

// SystemdRunning reports whether systemd is PID 1. WSL runs without it unless
// /etc/wsl.conf turns it on.
func SystemdRunning() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	st, err := os.Stat("/run/systemd/system")
	return err == nil && st.IsDir()
}

// OpenCommand is the command line that opens url in the desktop's browser:
// xdg-open on Linux, open on macOS, and under WSL wslview when present, else
// Windows's own start through cmd.exe.
func OpenCommand(url string) ([]string, error) {
	switch {
	case runtime.GOOS == "darwin":
		return []string{"open", url}, nil
	case IsWSL():
		if p, err := exec.LookPath("wslview"); err == nil {
			return []string{p, url}, nil
		}
		// start treats its first quoted argument as a window title, hence "".
		return []string{"cmd.exe", "/c", "start", `""`, url}, nil
	case runtime.GOOS == "linux":
		return []string{"xdg-open", url}, nil
	}
	return nil, errors.New("opening a browser is only supported on Linux and macOS")
}

// OpenURL opens url in a browser. Under sudo it runs as the user who ran
// sudo, since root has no desktop session and xdg-open as root opens a
// browser nobody can see, if any.
func OpenURL(url string) error {
	argv, err := OpenCommand(url)
	if err != nil {
		return err
	}
	if os.Geteuid() == 0 {
		if u := SudoUser(); u != "" && u != "root" {
			argv = append([]string{"sudo", "-u", u, "--"}, argv...)
		}
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	return cmd.Start()
}

// SudoUser is the account that asked for root: sudo's SUDO_USER, or the
// owner of pkexec's PKEXEC_UID, which is how the Stockroom Setup launcher in
// the applications menu gets root. Empty when neither is set.
func SudoUser() string {
	if name := os.Getenv("SUDO_USER"); name != "" {
		return name
	}
	if uid := os.Getenv("PKEXEC_UID"); uid != "" {
		if u, err := user.LookupId(uid); err == nil {
			return u.Username
		}
	}
	return ""
}

// InvokingUser is the account that ran sudo or pkexec, or the current one
// without either.
func InvokingUser() (*user.User, error) {
	if name := SudoUser(); name != "" && os.Geteuid() == 0 {
		return user.Lookup(name)
	}
	return user.Current()
}

// HasDesktop is a rough check that a graphical session exists to open a
// browser in.
func HasDesktop() bool {
	switch {
	case runtime.GOOS == "darwin", IsWSL():
		return true
	}
	if os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != "" {
		return true
	}
	// sudo drops DISPLAY. A logind graphical session for the invoking user is
	// the next best sign.
	out, err := exec.Command("loginctl", "list-sessions", "--no-legend").Output()
	return err == nil && strings.Contains(string(out), "seat")
}

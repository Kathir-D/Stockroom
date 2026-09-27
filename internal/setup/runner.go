package setup

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// Cmd is one command setup runs. Everything setup does to the machine beyond
// writing files goes through a Runner, so the tests can record the commands
// instead of running them as root.
type Cmd struct {
	Name  string
	Args  []string
	Stdin string
	// User runs the command as that account (postgres for psql on Linux,
	// the installing user for Homebrew on macOS). Empty runs as whoever
	// setup is running as.
	User string
	// Root runs the command with root privileges, through sudo when setup
	// isn't root already. macOS setup runs as the installing user and needs
	// root only to write LaunchDaemons.
	Root bool
	// Env is added to the inherited environment.
	Env []string
}

func (c Cmd) String() string {
	s := c.Name
	if len(c.Args) > 0 {
		s += " " + strings.Join(c.Args, " ")
	}
	if c.User != "" {
		s = "[as " + c.User + "] " + s
	}
	if c.Root {
		s = "[root] " + s
	}
	return s
}

// Runner runs a Cmd and returns its standard output. A failure's error
// carries the command's standard error.
type Runner interface {
	Run(ctx context.Context, c Cmd) (string, error)
}

// ExecRunner runs commands for real.
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, c Cmd) (string, error) {
	argv := append([]string{c.Name}, c.Args...)
	euid := os.Geteuid()
	switch {
	case c.User != "" && euid == 0 && runtime.GOOS == "linux":
		argv = append([]string{"runuser", "-u", c.User, "--"}, argv...)
	case c.User != "" && euid == 0:
		argv = append([]string{"sudo", "-u", c.User, "--"}, argv...)
	case c.User != "" && c.User != currentUserName():
		argv = append([]string{"sudo", "-u", c.User, "--"}, argv...)
	case c.Root && euid != 0:
		argv = append([]string{"sudo", "--"}, argv...)
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if c.User != "" {
		// Another account's commands start in /, not in a home directory
		// it may not be able to read (psql warns about that as postgres).
		cmd.Dir = "/"
	}
	cmd.Env = append(os.Environ(), c.Env...)
	if c.Stdin != "" {
		cmd.Stdin = strings.NewReader(c.Stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			return stdout.String(), fmt.Errorf("%s: %w", c.Name, err)
		}
		return stdout.String(), fmt.Errorf("%s: %w: %s", c.Name, err, msg)
	}
	return stdout.String(), nil
}

func currentUserName() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return ""
}

package setup

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"stockroom/internal/stockroom"
)

// Prompter asks the person running setup a question.
type Prompter interface {
	Line(prompt string) (string, error)
	Secret(prompt string) (string, error)
}

// ttyPrompter reads from /dev/tty rather than stdin. Under
// `curl … | sudo bash`, stdin is the script itself, and reading it would eat
// the rest of the installer.
type ttyPrompter struct{}

var errNoTTY = errors.New("no terminal to ask on; run setup from a terminal, or pass --non-interactive with --admin-number and --admin-password-file")

func (ttyPrompter) Line(prompt string) (string, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return "", errNoTTY
	}
	defer tty.Close()
	fmt.Fprint(tty, prompt)
	line, err := bufio.NewReader(tty).ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// Secret turns echo off with stty for the length of one line. stty rather
// than a terminal library keeps the module's dependencies as they are.
func (p ttyPrompter) Secret(prompt string) (string, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return "", errNoTTY
	}
	defer tty.Close()
	stty := func(arg string) error {
		cmd := exec.Command("stty", arg)
		cmd.Stdin = tty
		return cmd.Run()
	}
	if err := stty("-echo"); err != nil {
		return "", fmt.Errorf("could not turn off echo: %w", err)
	}
	defer func() {
		_ = stty("echo")
		fmt.Fprintln(tty)
	}()
	fmt.Fprint(tty, prompt)
	line, err := bufio.NewReader(tty).ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// failsafe is the failsafe admin setup writes into the config.
type failsafe struct {
	number, password string
}

// CheckFailsafe is checkFailsafe for an installer outside this package:
// Stockroom-Setup.exe refuses a bad failsafe admin before it installs
// anything, by the rule setup will apply inside WSL.
func CheckFailsafe(number, password string) error {
	return checkFailsafe(failsafe{number: number, password: password})
}

// checkFailsafe applies install.sh's rules: both or neither, digits only,
// 8 to 72 bytes, and no single quote, which the config's quoting can't hold.
func checkFailsafe(f failsafe) error {
	switch {
	case f.number == "" && f.password == "":
		return nil
	case f.number == "":
		return errors.New("a failsafe admin password needs a student number too")
	case f.password == "":
		return errors.New("a failsafe admin number needs a password too")
	}
	for _, r := range f.number {
		if r < '0' || r > '9' {
			return errors.New("the failsafe admin number must be digits only")
		}
	}
	if len(f.number) > 32 {
		return errors.New("the failsafe admin number can be at most 32 digits")
	}
	if n := len(f.password); n < 8 || n > 72 {
		return errors.New("the failsafe admin password must be 8 to 72 characters")
	}
	if !stockroom.EnvValueWritable(f.password) {
		return errors.New("the failsafe admin password cannot contain a single quote (') or a line break")
	}
	return nil
}

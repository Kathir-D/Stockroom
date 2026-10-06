//go:build !windows

package main

import (
	"fmt"
	"os"
)

// The installer only means anything on Windows. This main keeps
// `go build ./...` and the tests working everywhere else.
func main() {
	fmt.Fprintln(os.Stderr, "Stockroom-Setup.exe is the Windows installer. On Linux and macOS, run: stockroom setup --gui")
	os.Exit(1)
}

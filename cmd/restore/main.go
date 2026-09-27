// Command restore is `stockroom restore` for a working copy, so the docs'
// `go run ./cmd/restore` keeps working. The body lives in internal/cli.
package main

import (
	"os"

	"stockroom/internal/cli"
)

func main() {
	os.Exit(cli.Restore("go run ./cmd/restore", os.Args[1:]))
}

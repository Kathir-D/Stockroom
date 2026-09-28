package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"stockroom/internal/cli"
	"stockroom/internal/platform"
	"stockroom/internal/stockroom"
	"stockroom/supabase"
)

const configFlagHelp = "config file to read (default: STOCKROOM_CONFIG, a .env above the working directory, then the system path)"

// run dispatches on the first argument. No argument, or one that starts with
// a dash, means serve, so `go run ./server` and dev.sh keep working.
func run(args []string) int {
	name := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name, args = args[0], args[1:]
	}
	switch name {
	case "serve":
		return serve(args)
	case "setup":
		return cmdSetup(args)
	case "service":
		return cmdService(args)
	case "doctor":
		return cmdDoctor(args)
	case "support-bundle":
		return cmdSupportBundle(args)
	case "restore":
		return cli.Restore("stockroom restore", args)
	case "version":
		return cmdVersion(args)
	case "open":
		return cmdOpen(args)
	case "help":
		usage(os.Stdout)
		return 0
	}
	fmt.Fprintf(os.Stderr, "stockroom: unknown command %q\n\n", name)
	usage(os.Stderr)
	return 2
}

func usage(w io.Writer) {
	fmt.Fprint(w, `Usage: stockroom <command> [flags]

  serve     run the server (the default with no command)
  setup     install on this machine: database, config, service (run with sudo)
  service   install|uninstall|start|stop|status the background service
  doctor    check this install and say how to fix what is wrong
  support-bundle
            write doctor's report, the config and the recent log, secrets
            removed, into one zip to attach to an issue (sudo on Linux)
  restore   load a backup archive into the database
  version   print the version, commit and schema version
  open      open Stockroom in the browser

Commands that read the config take --config <file>. "stockroom <command> -h" lists its flags.
`)
}

func versionString() string {
	if commit == "" {
		return version
	}
	return version + " (" + commit + ")"
}

func cmdVersion(args []string) int {
	fs := flag.NewFlagSet("stockroom version", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	schema, err := stockroom.EmbeddedSchemaVersion(supabase.Migrations)
	if err != nil {
		schema = "unknown (" + err.Error() + ")"
	}
	fmt.Printf("stockroom %s\ncommit    %s\nschema    %s\n", version, orNone(commit), schema)
	return 0
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// cmdOpen opens the configured address in a browser.
func cmdOpen(args []string) int {
	fs := flag.NewFlagSet("stockroom open", flag.ContinueOnError)
	configPath := fs.String("config", "", configFlagHelp)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, err := stockroom.LoadConfigFrom(*configPath)
	if err != nil {
		log.Printf("config: %v", err)
		return 1
	}
	log.Print(cfg.Describe())
	url := "http://" + cfg.ServerAddr
	if err := platform.OpenURL(url); err != nil {
		log.Printf("could not open a browser (%v). Open %s yourself.", err, url)
		return 1
	}
	fmt.Println(url)
	return 0
}

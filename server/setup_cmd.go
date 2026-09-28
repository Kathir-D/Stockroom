package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"stockroom/internal/setup"
	"stockroom/internal/stockroom"
	"stockroom/supabase"
)

func cmdSetup(args []string) int {
	fs := flag.NewFlagSet("stockroom setup", flag.ContinueOnError)
	var o setup.Options
	fs.BoolVar(&o.NonInteractive, "non-interactive", false, "fail instead of asking anything")
	fs.BoolVar(&o.NoService, "no-service", false, "don't install or start the service (for a machine without systemd)")
	fs.BoolVar(&o.NoOpen, "no-open", false, "don't open a browser at the end")
	fs.BoolVar(&o.WithCamera, "with-camera", false, "also start the closet camera's detector (needs Docker)")
	fs.StringVar(&o.ServiceUser, "service-user", "", "account the service runs as (Linux; default: whoever ran sudo; \"stockroom\" creates a system account)")
	fs.StringVar(&o.AdminNumber, "admin-number", "", "failsafe admin student number")
	fs.StringVar(&o.AdminPasswordFile, "admin-password-file", "", "file holding the failsafe admin password, or - for stdin")
	fs.StringVar(&o.Addr, "addr", "", "address the server listens on (default 127.0.0.1:8080)")
	fs.StringVar(&o.ConfigFile, "config", "", "config file to write and point the service at (default: the service's current one, else the system path)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "stockroom setup takes no arguments, got %q. A password goes in --admin-password-file, never on the command line\n", fs.Args())
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	env := setup.NewEnv()
	if err := setup.Setup(ctx, env, o); err != nil {
		fmt.Fprintf(os.Stderr, "\nsetup stopped at %v\n", err)
		return 1
	}
	return 0
}

func cmdService(args []string) int {
	fs := flag.NewFlagSet("stockroom service", flag.ContinueOnError)
	user := fs.String("user", "", "account the service runs as, for install (default: the current service user, or whoever ran sudo)")
	configPath := fs.String("config", "", "config file the service reads, for install (default: the service's current one, else the system path)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: stockroom service install|uninstall|start|stop|restart|status [--user name] [--config file]")
		fs.PrintDefaults()
	}
	if len(args) == 0 {
		fs.Usage()
		return 2
	}
	action := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := setup.Service(ctx, setup.NewEnv(), action, *user, *configPath); err != nil {
		fmt.Fprintf(os.Stderr, "stockroom service %s: %v\n", action, err)
		return 1
	}
	return 0
}

func cmdDoctor(args []string) int {
	fs := flag.NewFlagSet("stockroom doctor", flag.ContinueOnError)
	configPath := fs.String("config", "", configFlagHelp)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if setup.Doctor(ctx, setup.NewEnv(), *configPath) > 0 {
		return 1
	}
	return 0
}

func cmdSupportBundle(args []string) int {
	fs := flag.NewFlagSet("stockroom support-bundle", flag.ContinueOnError)
	configPath := fs.String("config", "", configFlagHelp)
	out := fs.String("out", "", "zip to write (default: stockroom-support-<date>.zip here)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	schema, err := stockroom.EmbeddedSchemaVersion(supabase.Migrations)
	if err != nil {
		schema = "unknown (" + err.Error() + ")"
	}
	path, err := setup.SupportBundle(ctx, setup.NewEnv(), *configPath, setup.BundleInfo{
		Version: fmt.Sprintf("stockroom %s\ncommit    %s\nschema    %s\n", version, orNone(commit), schema),
		Out:     *out,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "stockroom support-bundle: %v\n", err)
		return 1
	}
	fmt.Printf("Wrote %s. Read it before you send it: secrets are removed, but check.\n", path)
	return 0
}

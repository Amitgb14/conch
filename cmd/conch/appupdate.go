package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/update"
)

const appUpdateUsage = `usage: conch app update check [-json] [-version V]
       conch app update install [-app PATH] VERSION
       conch app update rollback [-app PATH]`

// appUpdate is Conch.app's own update (#204), which the app runs through
// the conch inside it: check asks for a newer release than the app's
// version, install replaces the bundle with a verified one, rollback puts
// back the one kept from the last update. The app relaunches itself.
func appUpdate(args []string) error {
	if len(args) == 0 {
		return errors.New(appUpdateUsage)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	fs := flag.NewFlagSet("app update "+args[0], flag.ContinueOnError)
	switch args[0] {
	case "check":
		asJSON := fs.Bool("json", false, "say it as JSON, for Conch.app")
		version := fs.String("version", proto.Version, "the version to compare with: the app's")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		c, err := update.CheckApp(ctx, *version)
		if err != nil {
			return err
		}
		if *asJSON {
			return json.NewEncoder(os.Stdout).Encode(c)
		}
		switch {
		case c.Dev:
			fmt.Printf("Conch %s is built from source: it updates from source, not from releases\n", c.Current)
		case c.Newer:
			fmt.Printf("Conch %s is available (this is %s): %s\n", c.Latest, c.Current, c.Notes)
			if c.Unavailable != "" {
				fmt.Println(c.Unavailable)
			}
		default:
			fmt.Printf("Conch %s is up to date\n", c.Current)
		}
		return nil
	case "install", "rollback":
		app := fs.String("app", "", "the Conch.app to replace: the one this conch is in")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *app == "" {
			if exe, err := os.Executable(); err == nil {
				*app = appBundleOf(exe)
			}
			if *app == "" {
				return errors.New("this conch is not inside Conch.app: say which with -app PATH")
			}
		}
		if args[0] == "rollback" {
			if fs.NArg() != 0 {
				return errors.New(appUpdateUsage)
			}
			if err := update.RollbackApp(*app); err != nil {
				return err
			}
			v, _ := update.PreviousApp(*app)
			now := "the one before"
			if b, ok := update.AppVersion(*app); ok && b != "" {
				now = b
			}
			fmt.Printf("Conch.app is back to %s (%s is kept to go forward again); relaunch it to run it\n", now, v)
			return nil
		}
		if fs.NArg() != 1 {
			return errors.New(appUpdateUsage)
		}
		version := fs.Arg(0)
		say := func(s string) { fmt.Fprintln(os.Stderr, s) }
		if err := update.InstallApp(ctx, version, *app, say); err != nil {
			return err
		}
		fmt.Printf("Conch %s is installed at %s; it runs from the next launch\n", version, *app)
		return nil
	}
	return fmt.Errorf("conch app update has no %q; %s", args[0], appUpdateUsage)
}

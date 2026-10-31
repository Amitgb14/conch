package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/configweb"
	"github.com/Amitgb14/conch/internal/tui"
)

const configUsage = `usage: conch config web [-port N] [-no-open]
       conch config path`

// configWait holds conch config web open until it is interrupted; a test
// puts its own here to look at the page and then let it go.
var configWait = func(ctx context.Context, url string) { <-ctx.Done() }

func runConfig(args []string) error {
	if len(args) == 0 {
		return errors.New(configUsage)
	}
	switch args[0] {
	case "path":
		if len(args) > 1 {
			return errors.New(configUsage)
		}
		fmt.Println(config.File())
		return nil
	case "web":
		return configWeb(args[1:])
	}
	return fmt.Errorf("unknown config subcommand %q\n%s", args[0], configUsage)
}

// configWeb serves the settings page until ctrl+c. The TUI serves the same
// page itself (⚙ → e), which is the better way while one is open: it takes
// each save at once, where this one is read by a TUI only when it starts.
func configWeb(args []string) error {
	fs := flag.NewFlagSet("config web", flag.ContinueOnError)
	port := fs.Int("port", 0, "the port to listen on, on 127.0.0.1; 0 picks a free one")
	noOpen := fs.Bool("no-open", false, "print the address rather than opening it")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 || *port < 0 || *port > 65535 {
		return errors.New(configUsage)
	}
	srv, err := configweb.New(tui.SettingsChoices(nil))
	if err != nil {
		return err
	}
	run, err := srv.Listen(*port)
	if err != nil {
		return fmt.Errorf("listening: %w", err)
	}
	defer run.Close()

	fmt.Printf("conch settings: %s\n", run.URL)
	fmt.Println("Editing " + config.File() + ". A conch that is open reads it when it next starts —")
	fmt.Println("while one is, its own Settings (, then e) edits it live instead. ctrl+c stops this.")
	if !*noOpen {
		if err := openInBrowser(run.URL); err != nil {
			fmt.Fprintln(os.Stderr, "couldn't open a browser:", err, "— open the address above")
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	configWait(ctx, run.URL)
	return nil
}

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/Amitgb14/conch/internal/brain"
	"github.com/Amitgb14/conch/internal/config"
)

// `conch commit-message` writes a commit message for the diff on its stdin
// with the brain set in config.toml, and prints it: Conch.app's "Write from
// the changes". It runs on this computer — where the model's key is — for
// a branch on any machine, since the app hands it the diff it is showing.
// Nothing is committed.

const commitMessageUsage = "usage: git diff --cached | conch commit-message [-context TEXT]"

// stdinIn is where commit-message reads the diff; a test gives its own.
var stdinIn io.Reader = os.Stdin

func runCommitMessage(args []string) error {
	fs := flag.NewFlagSet("commit-message", flag.ContinueOnError)
	about := fs.String("context", "", "what the agent that made the change said it was doing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return errors.New(commitMessageUsage)
	}
	diff, err := io.ReadAll(io.LimitReader(stdinIn, 8<<20))
	if err != nil {
		return err
	}
	cfg, cerr := config.Load()
	if cerr != nil {
		fmt.Fprintln(os.Stderr, "conch: settings not read ("+cerr.Error()+"); using the defaults")
	}
	provider, err := brain.New(cfg.Brain)
	if err != nil {
		return err
	}
	if err := provider.Check(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	m, err := brain.WriteCommitMessage(ctx, provider, string(diff), *about)
	if err != nil {
		return err
	}
	fmt.Println(m.String())
	return nil
}

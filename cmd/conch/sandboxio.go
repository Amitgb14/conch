package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/charmbracelet/x/term"

	"github.com/Amitgb14/conch/internal/remote"
	"github.com/Amitgb14/conch/internal/sandbox"
)

// runSandboxIO is how conch reaches a sandbox whose provider has no ssh:
// what ssh would be in its place, run with the sandbox and a script.
//
//	conch sandbox-io -provider P [-t] ID SCRIPT      runs SCRIPT there
//	conch sandbox-io -provider P -bridge ID SCRIPT   carries conch's connection
//
// It is not for people; conch puts it on the command lines it runs.
func runSandboxIO(args []string) error {
	fs := flag.NewFlagSet("sandbox-io", flag.ContinueOnError)
	provider := fs.String("provider", "", "the sandbox's provider")
	tty := fs.Bool("t", false, "run it on a terminal")
	bridge := fs.Bool("bridge", false, "carry conch's connection")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 || *provider == "" {
		return errors.New("usage: conch sandbox-io -provider P [-t | -bridge] ID SCRIPT")
	}
	id, script := fs.Arg(0), fs.Arg(1)
	p, err := remote.OpenProvider(*provider)
	if err != nil {
		return err
	}
	st, ok := p.(sandbox.Streamer)
	if !ok {
		return fmt.Errorf("%s sandboxes are reached over ssh", *provider)
	}
	ctx := context.Background()
	if *bridge {
		return st.Bridge(ctx, id, script, os.Stdin, os.Stdout)
	}
	var t *sandbox.Term
	if *tty {
		t = &sandbox.Term{Rows: 24, Cols: 80}
		fd := os.Stdin.Fd()
		if term.IsTerminal(fd) {
			if w, h, err := term.GetSize(fd); err == nil {
				t.Rows, t.Cols = uint16(h), uint16(w)
			}
			if old, err := term.MakeRaw(fd); err == nil {
				defer term.Restore(fd, old)
			}
			resize := make(chan [2]uint16, 1)
			winch := make(chan os.Signal, 1)
			signal.Notify(winch, syscall.SIGWINCH)
			defer signal.Stop(winch)
			go func() {
				for range winch {
					if w, h, err := term.GetSize(fd); err == nil {
						select {
						case resize <- [2]uint16{uint16(h), uint16(w)}:
						default:
						}
					}
				}
			}()
			t.Resize = resize
		}
	}
	code, err := st.Exec(ctx, id, script, t, os.Stdin, os.Stdout, os.Stderr)
	if err != nil {
		return err
	}
	if code != 0 {
		return exitStatus(code)
	}
	return nil
}

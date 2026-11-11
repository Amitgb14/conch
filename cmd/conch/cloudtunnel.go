package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/x/term"

	"github.com/Amitgb14/conch/internal/cloud"
	"github.com/Amitgb14/conch/internal/relay"
	"github.com/Amitgb14/conch/internal/remote"
)

// conch cloud bridge and exec: a cloud machine's transport (what ssh is to
// other machines). bridge carries conch's protocol on stdin and stdout,
// as `conch machine bridge` does — it is what remote.BridgeCommand runs
// for a cloud machine, so the TUI and Conch.app reach one like any
// other — and exec runs a script there. Both wake an asleep machine and
// wait for it.

// cloudExit ends `conch cloud exec` with the script's status; a variable
// for tests.
var cloudExit = os.Exit

// cloudSay is where the transport says what it waits on: stderr, which
// remote keeps the end of for an error.
func cloudSay(msg string) { fmt.Fprintf(os.Stderr, "conch: %s…\n", msg) }

// dialCloud opens a tunnel to the machine named ref: its catalog name,
// its cloud name or its id.
func dialCloud(ctx context.Context, ref string) (*relay.Session, error) {
	c, err := cloud.Open()
	if err != nil {
		return nil, err
	}
	id := ref
	if m, ok := remote.LookupMachine(ref); ok {
		if cid, ok := remote.ParseCloudTarget(m.Target); ok {
			id = cid
		}
	}
	if !cloud.IsMachineID(id) {
		m, err := findCloudMachine(ctx, c, ref)
		if err != nil {
			return nil, err
		}
		id = m.ID
	}
	return c.Dial(ctx, id, cloudSay)
}

// cloudExitFor is the status a failure to reach the machine exits with:
// one trying again may mend, or one it cannot.
func cloudExitFor(err error) int {
	var kc *cloud.KeyChangedError
	switch {
	case errors.Is(err, cloud.ErrNotSignedIn), errors.Is(err, cloud.ErrSignedOut), errors.As(err, &kc),
		errors.Is(err, cloud.ErrNotFound), errors.Is(err, cloud.ErrSuspended):
		return remote.CloudExitRefused
	}
	return remote.CloudExitConnection
}

func runCloudBridge(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: conch cloud bridge NAME")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	dctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	s, err := dialCloud(dctx, args[0])
	cancel()
	if err != nil {
		fmt.Fprintln(os.Stderr, "conch:", err)
		cloudExit(cloudExitFor(err))
		return nil
	}
	defer s.Close()
	if err := cloud.Bridge(ctx, s, os.Stdin, os.Stdout); err != nil && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, "conch:", err)
		cloudExit(cloudExitFor(err))
	}
	return nil
}

func runCloudExec(args []string) error {
	fs := flag.NewFlagSet("cloud exec", flag.ContinueOnError)
	tty := fs.Bool("t", false, "run it on a terminal (for a program that draws a screen)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 1 {
		return errors.New("usage: conch cloud exec [-t] NAME [-- SCRIPT…]")
	}
	ref, words := rest[0], rest[1:]
	if len(words) > 0 && words[0] == "--" {
		words = words[1:]
	}
	script := strings.Join(words, " ")
	if script == "" {
		script = "exec \"$SHELL\" -l"
		*tty = true
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	dctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	s, err := dialCloud(dctx, ref)
	cancel()
	if err != nil {
		fmt.Fprintln(os.Stderr, "conch:", err)
		cloudExit(cloudExitFor(err))
		return nil
	}
	defer s.Close()
	o := cloud.ExecOptions{Script: script, TTY: *tty, Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}
	if *tty && term.IsTerminal(os.Stdin.Fd()) {
		if cols, rows, err := term.GetSize(os.Stdout.Fd()); err == nil {
			o.Cols, o.Rows = cols, rows
		}
		old, err := term.MakeRaw(os.Stdin.Fd())
		if err == nil {
			defer term.Restore(os.Stdin.Fd(), old)
		}
		resize := make(chan [2]int, 1)
		winch := make(chan os.Signal, 1)
		signal.Notify(winch, syscall.SIGWINCH)
		defer signal.Stop(winch)
		go func() {
			for range winch {
				if c, r, err := term.GetSize(os.Stdout.Fd()); err == nil {
					select {
					case resize <- [2]int{c, r}:
					default:
					}
				}
			}
		}()
		o.Resize = resize
	}
	code, err := cloud.Exec(ctx, s, o)
	if err != nil {
		fmt.Fprintln(os.Stderr, "conch:", err)
		code = cloudExitFor(err)
	}
	s.Close()
	cloudExit(code)
	return nil
}

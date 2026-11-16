package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/desktop"
	"github.com/Amitgb14/conch/internal/fleet"
	"github.com/Amitgb14/conch/internal/proto"
)

// `conch desktop` is the bridge a desktop app runs and talks to over this
// process's stdin and stdout (internal/desktop): the Linux and Windows
// apps, and in time Conch.app. It is a command rather than a server of its
// own because the app is what decides it is needed, and because a pipe
// crosses into WSL (`wsl.exe -d Ubuntu conch desktop`) and over ssh with
// nothing listening and nothing to authenticate. Like `conch bridge`,
// stdout carries only the protocol: anything to say goes to stderr.

const desktopUsage = "usage: conch desktop [-every DURATION]"

func runDesktop(args []string) error {
	return runDesktopOn(args, os.Stdin, os.Stdout)
}

func runDesktopOn(args []string, in io.Reader, out io.Writer) error {
	fs := flag.NewFlagSet("desktop", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	every := fs.Duration("every", time.Second, "how often the servers are read for changes")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return errors.New(desktopUsage)
	}
	if *every <= 0 {
		return fmt.Errorf("-every must be more than 0, not %s", *every)
	}
	sock := config.SocketPath()
	if err := client.EnsureServer(sock, config.ServerLogPath()); err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}

	// This computer's connection, made again if it drops (the server
	// reloading closes it), as the phone gateway's is.
	var mu sync.Mutex
	var shared *client.Client
	local := func() (*client.Client, error) {
		mu.Lock()
		defer mu.Unlock()
		if shared != nil && shared.Err() == nil {
			return shared, nil
		}
		c, err := client.Dial(sock, "conch-desktop")
		if err != nil {
			return nil, err
		}
		shared = c
		return c, nil
	}
	pool := fleet.New(fleet.Options{
		Local:     local,
		DialLocal: func() (*client.Client, error) { return client.Dial(sock, "conch-desktop") },
	})
	defer pool.Close()
	defer func() {
		mu.Lock()
		if shared != nil {
			shared.Close()
		}
		mu.Unlock()
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return desktop.Serve(ctx, in, out, desktop.Options{
		Machines: pool, Conch: self, Version: proto.Version, Every: *every,
	})
}

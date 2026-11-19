package main

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/sandbox"
)

// maxKey bounds what is read as a key: a key is a line, and a pipe that
// never ends is not one.
const maxKey = 64 << 10

// key set PROVIDER keeps a provider's API key in config.toml, under
// [sandbox.PROVIDER] api_key, where conch reads it too: for somebody who
// starts a tool from the Dock and has no shell variables. The key is read
// from stdin, never from the command line, which other processes can see.
func (a *app) key(args []string) error {
	c := a.command("key", "conch-sandbox key set PROVIDER   (the key on stdin)")
	rest, err := c.parse(args)
	if err != nil {
		return err
	}
	if len(rest) != 2 || rest[0] != "set" {
		return c.wrong("give key set PROVIDER")
	}
	name := rest[1]
	if why, gone := sandbox.Dropped(name); gone {
		return fmt.Errorf("%s", why)
	}
	if !sandbox.Known(name) {
		return fmt.Errorf("unknown sandbox provider %q; %s", name, installedWords())
	}
	var key string
	if a.terminal {
		fmt.Fprintf(a.stderr, "%s API key (not shown): ", sandbox.ProviderLabel(name))
		key, err = a.readSecret()
		fmt.Fprintln(a.stderr)
	} else {
		key, err = bufio.NewReader(io.LimitReader(a.stdin, maxKey)).ReadString('\n')
		if err == io.EOF {
			err = nil
		}
	}
	if err != nil {
		return fmt.Errorf("read the key: %w", err)
	}
	key = strings.TrimSpace(key)
	switch {
	case key == "":
		return fmt.Errorf("no key given: pipe it in, e.g. printf %%s \"$KEY\" | conch-sandbox key set %s", name)
	case len(key) >= maxKey:
		return fmt.Errorf("that is not an API key: it is %d bytes or more", maxKey)
	case strings.ContainsAny(key, " \t\r\n"):
		return fmt.Errorf("that is not an API key: it has spaces in it")
	}
	cfg, err := config.Load()
	if err != nil {
		// Saving over a file conch cannot read would lose it.
		return fmt.Errorf("read %s: %w; put it right first", config.File(), err)
	}
	if cfg.Sandbox == nil {
		cfg.Sandbox = config.SandboxCfg{}
	}
	pc := cfg.Sandbox[name]
	pc.APIKey = key
	cfg.Sandbox[name] = pc
	if err := config.Save(cfg); err != nil {
		return err
	}
	fmt.Fprintf(a.stderr, "Saved the %s key in %s.\n", sandbox.ProviderLabel(name), config.File())
	return nil
}

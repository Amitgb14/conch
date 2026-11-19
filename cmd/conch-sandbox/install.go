package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/Amitgb14/conch/internal/integration"
	"github.com/Amitgb14/conch/internal/sandbox"
)

// Somebody without conch has no provider files, so a verb naming a
// provider that is not installed fetches its file from the registry — the
// signed index only, the file checked against the hash the index gives
// and its hosts and key variable against the index's — and goes on.
// conch asks a person before it installs, and shows them the hosts; a
// tool running conch-sandbox has nobody to ask, so the signature is what
// stands in for that look, and an index without one is never read.

// installer installs a provider from the registry for openProvider; run
// sets it, so a provider is fetched only by a command being run.
var installer func(name string) error

// ensureInstalled installs name from the registry when it is not here
// at all; a file that is here, even a broken one, is left for the caller
// to say about.
func ensureInstalled(name string) error {
	if sandbox.Known(name) || sandbox.CheckName(name) != nil {
		return nil
	}
	if _, err := os.Stat(sandbox.ProviderFilePath(name)); err == nil {
		return nil
	}
	if installer == nil {
		return nil
	}
	return installer(name)
}

// registryTimeout bounds one look at the registry.
const registryTimeout = time.Minute

// install fetches name's file from the registry's signed index and
// installs it.
func (a *app) install(ctx context.Context, name string) error {
	ctx, cancel := context.WithTimeout(ctx, registryTimeout)
	defer cancel()
	ix, err := sandbox.FetchSignedIndex(ctx)
	if err != nil {
		return fmt.Errorf("unknown sandbox provider %q, and its file could not be fetched: %s", name, registryWords(err))
	}
	e, ok := ix.Find(name)
	if !ok {
		if off, ok := ix.FindDisabled(name); ok {
			return fmt.Errorf("%s is in the provider registry, but its file has no ssh way in, so conch-sandbox cannot use it", firstOf(off.Label, off.Name))
		}
		return fmt.Errorf("unknown sandbox provider %q: the provider registry has none of that name (conch-sandbox providers lists those it has)", name)
	}
	return a.installEntry(ctx, e)
}

// installEntry fetches e's file, checks it and keeps it.
func (a *app) installEntry(ctx context.Context, e sandbox.RegistryEntry) error {
	if err := sandbox.CheckName(e.Name); err != nil {
		return err
	}
	if e.Format > sandbox.ProviderFormat {
		return fmt.Errorf("%s's file needs a newer conch-sandbox (format %d; this one reads up to %d)", e.Name, e.Format, sandbox.ProviderFormat)
	}
	fmt.Fprintf(a.stderr, "Fetching the %s provider from the registry…\n", firstOf(e.Label, e.Name))
	data, err := sandbox.FetchProviderFile(ctx, e)
	if err != nil {
		return fmt.Errorf("%s: %s", e.Name, registryWords(err))
	}
	// What the signed index says and what the file says must agree: the
	// file's key_env is what is read and sent, and its api host where.
	f, problems, err := sandbox.ParseProviderFile(e.Name, data)
	if err != nil {
		return fmt.Errorf("%s's file from the registry: %w", e.Name, err)
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s's file from the registry cannot be used by this conch-sandbox: not installed", e.Name)
	}
	if api, ssh := sandbox.FileHosts(f); api != e.APIHost || ssh != e.SSHHost {
		return fmt.Errorf("%s's file names %s and %s, not the hosts the registry's index lists: not installed", e.Name, api, ssh)
	}
	if f.KeyEnv != e.KeyEnv {
		return fmt.Errorf("%s's file reads the key from $%s, not the $%s the registry's index lists: not installed", e.Name, f.KeyEnv, e.KeyEnv)
	}
	if _, err := sandbox.InstallRegistryFile(e.Name, data, e.Capabilities); err != nil {
		return err
	}
	fmt.Fprintf(a.stderr, "  installed %s: your key ($%s, or conch-sandbox key set %s) goes to %s", sandbox.ProviderFilePath(e.Name), e.KeyEnv, e.Name, e.APIHost)
	if e.SSHHost != "" {
		fmt.Fprintf(a.stderr, ", and its sandboxes are reached at %s", e.SSHHost)
	}
	fmt.Fprintln(a.stderr)
	return nil
}

// registryWords says a registry failure without sending anybody to conch.
func registryWords(err error) string {
	switch {
	case errors.Is(err, sandbox.ErrUnsigned):
		return "the provider registry's index is not signed with a key conch-sandbox knows, so nothing is installed from it"
	case errors.Is(err, sandbox.ErrIndexTooNew):
		return "the provider registry's index needs a newer conch-sandbox"
	}
	return err.Error()
}

// provider install NAME fetches a provider's file from the registry.
func (a *app) provider(ctx context.Context, args []string) error {
	c := a.command("provider", "conch-sandbox provider install NAME   (from the provider registry's signed index)")
	rest, err := c.parse(args)
	if err != nil {
		return err
	}
	if len(rest) != 2 || rest[0] != "install" {
		return c.wrong("give provider install NAME")
	}
	name := rest[1]
	if err := sandbox.CheckName(name); err != nil {
		return errUsage{err.Error()}
	}
	if why, gone := sandbox.Dropped(name); gone {
		return errors.New(why)
	}
	if _, err := os.Stat(sandbox.ProviderFilePath(name)); err == nil {
		inst, err := sandbox.ReadInstalled()
		if err != nil {
			return err
		}
		if _, ok := inst[name]; ok {
			fmt.Fprintf(a.stderr, "%s is installed already.\n", name)
			return nil
		}
		return fmt.Errorf("%s is a provider file of your own here (%s): it is left as it is", name, sandbox.ProviderFilePath(name))
	}
	return a.install(ctx, name)
}

// registryProviders are the providers in the signed index not installed
// here that have a way in over ssh, for the tools' pickers: each says
// installed false, and is installed when first named.
func registryProviders(ctx context.Context, here map[string]bool) ([]providerJSON, error) {
	ctx, cancel := context.WithTimeout(ctx, registryTimeout)
	defer cancel()
	ix, err := sandbox.FetchSignedIndex(ctx)
	if err != nil {
		return nil, errors.New(registryWords(err))
	}
	var out []providerJSON
	for _, e := range ix.Providers {
		if here[e.Name] || e.Format > sandbox.ProviderFormat {
			continue
		}
		kind := entrySSH(e.Capabilities)
		if kind == "" {
			continue // its way in is not said: nothing to offer a tool
		}
		out = append(out, providerJSON{Name: e.Name, Label: firstOf(e.Label, e.Name), KeyEnv: e.KeyEnv, SSH: kind, Note: e.Note,
			Why: "not installed: it is fetched from the provider registry when first used", Capabilities: e.Capabilities})
	}
	return out, nil
}

// entrySSH is the index's word for how a provider's sandboxes are
// reached, as sshKind says it for an installed one.
func entrySSH(c *integration.Capabilities) string {
	switch {
	case c == nil || c.SSH == nil:
		return ""
	case c.SSH.Key == "none":
		return "token"
	}
	return "key"
}

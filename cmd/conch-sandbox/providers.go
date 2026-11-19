package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/integration"
	"github.com/Amitgb14/conch/internal/sandbox"
)

// schema is the version of every JSON object conch-sandbox prints. A field
// may be added under the same number; one renamed or dropped moves it.
const schema = 1

// openProvider is the named provider, set up from config.toml. A provider
// conch dropped says why; one not installed says which are.
func openProvider(name string) (sandbox.Provider, config.Config, error) {
	if name == "" {
		return nil, config.Config{}, errUsage{"give --provider NAME (" + installedWords() + ")"}
	}
	if why, gone := sandbox.Dropped(name); gone {
		return nil, config.Config{}, errors.New(why)
	}
	if !sandbox.Known(name) {
		// A file that is there but wrong — no [ssh], say — is not unknown:
		// what is wrong with it is the answer.
		if sandbox.CheckName(name) == nil {
			path := sandbox.ProviderFilePath(name)
			if _, problems, err := sandbox.ReadProviderFile(path); err == nil && len(problems) > 0 {
				return nil, config.Config{}, fmt.Errorf("%s cannot be used: %s", path, strings.Join(problems, "; "))
			}
		}
		return nil, config.Config{}, fmt.Errorf("unknown sandbox provider %q; %s", name, installedWords())
	}
	cfg, err := config.Load()
	if err != nil {
		return nil, cfg, fmt.Errorf("read %s: %w", config.File(), err)
	}
	p, err := sandbox.Open(name, cfg.Sandbox)
	return p, cfg, err
}

// ready opens the provider and checks it has a key, saying how to give it
// one in this program's words rather than conch's settings screen.
func ready(name string) (sandbox.Provider, config.Config, error) {
	p, cfg, err := openProvider(name)
	if err != nil {
		return nil, cfg, err
	}
	if err := p.Check(); err != nil {
		return nil, cfg, errors.New(notReady(p, err))
	}
	return p, cfg, nil
}

// notReady says what a provider lacks.
func notReady(p sandbox.Provider, err error) string {
	if !errors.Is(err, sandbox.ErrNotConfigured) {
		return err.Error()
	}
	if c, ok := p.(*sandbox.Custom); ok && c.File().KeyEnv != "" {
		return fmt.Sprintf("no API key: set $%s, or run conch-sandbox key set %s", c.File().KeyEnv, p.Name())
	}
	return "no API key: run conch-sandbox key set " + p.Name()
}

// installedWords names the providers installed here, or says how to get one.
func installedWords() string {
	if names := sandbox.Names(); len(names) > 0 {
		return "installed: " + strings.Join(names, ", ")
	}
	return "none is installed: conch sandbox provider install NAME installs one, or put its file in " + sandbox.ProvidersDir()
}

// sshKind is how a provider's sandboxes are reached: "key" (a key of the
// person's own, registered with the account or handed over by the API),
// "token" (a secret the provider hands out as the user name), or "" when
// its file says no way in at all.
func sshKind(p sandbox.Provider) string {
	c, ok := p.(*sandbox.Custom)
	if !ok {
		return ""
	}
	s := sandbox.FileCapabilities(c.File()).SSH
	switch {
	case s == nil:
		return ""
	case s.Key == "none":
		return "token"
	}
	return "key"
}

type settingJSON struct {
	Key     string           `json:"key"`
	Label   string           `json:"label,omitempty"`
	Help    string           `json:"help,omitempty"`
	Choices []sandbox.Choice `json:"choices,omitempty"`
	// FromAPI says the choices are read from the provider, which
	// providers does not do: it would call every provider's API.
	FromAPI bool   `json:"from_api,omitempty"`
	Free    bool   `json:"free,omitempty"`
	Default string `json:"default,omitempty"`
}

type providerJSON struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	Ready bool   `json:"ready"`
	Why   string `json:"why,omitempty"`
	// KeyEnv is the variable the key may be in; key set writes config.toml.
	KeyEnv   string            `json:"key_env,omitempty"`
	SSH      string            `json:"ssh"`
	Note     string            `json:"note,omitempty"`
	Settings []settingJSON     `json:"settings,omitempty"`
	Sizes    *sandbox.SizeSpec `json:"sizes,omitempty"`
	// Capabilities are the registry's for the file, as conch reads them.
	Capabilities *integration.Capabilities `json:"capabilities,omitempty"`
}

type providersJSON struct {
	Schema    int            `json:"schema"`
	Providers []providerJSON `json:"providers"`
}

func (a *app) providers(_ context.Context, args []string) error {
	c := a.command("providers", "conch-sandbox providers [--json]")
	asJSON := c.Bool("json", false, "print JSON on stdout")
	rest, err := c.parse(args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return c.wrong("providers takes no arguments")
	}
	out := providersJSON{Schema: schema, Providers: []providerJSON{}}
	for _, name := range sandbox.Offered() {
		j := providerJSON{Name: name, Label: sandbox.ProviderLabel(name), Capabilities: sandbox.CapabilitiesOf(name)}
		p, _, err := openProvider(name)
		if err == nil {
			if err = p.Check(); err != nil {
				err = errors.New(notReady(p, err))
			}
		}
		if err != nil {
			j.Why = err.Error()
		} else {
			j.Ready = true
		}
		if p != nil {
			j.SSH = sshKind(p)
			if n, ok := p.(sandbox.Noted); ok {
				j.Note = n.Note()
			}
		}
		if cp, ok := p.(*sandbox.Custom); ok {
			j.KeyEnv = cp.File().KeyEnv
			for _, s := range cp.Settings() {
				sj := settingJSON{Key: s.Key, Label: s.Label, Help: s.Help, Free: s.Free, Default: s.Default, FromAPI: s.ChoicesCall != ""}
				for _, v := range s.Choices {
					sj.Choices = append(sj.Choices, sandbox.Choice{Value: v})
				}
				j.Settings = append(j.Settings, sj)
			}
			if sz := cp.Sizes(); len(sz.Names)+len(sz.CPU)+len(sz.MemoryGiB)+len(sz.DiskGiB) > 0 {
				j.Sizes = &sz
			}
		}
		out.Providers = append(out.Providers, j)
	}
	if *asJSON {
		return a.printJSON(out)
	}
	if len(out.Providers) == 0 {
		fmt.Fprintln(a.stderr, "No providers: "+installedWords())
		return nil
	}
	tw := tabwriter.NewWriter(a.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "PROVIDER\tNAME\tSSH\tREADY")
	for _, j := range out.Providers {
		state := "yes"
		if !j.Ready {
			state = "no: " + j.Why
		}
		way := j.SSH
		if way == "" {
			way = "none"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", j.Name, j.Label, way, state)
	}
	return tw.Flush()
}

// printJSON prints v as one line on stdout.
func (a *app) printJSON(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(a.stdout, string(b))
	return err
}

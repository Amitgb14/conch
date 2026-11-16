package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/remote"
	"github.com/Amitgb14/conch/internal/sandbox"
)

// sandboxSettingsJSON is what a provider's file lets be chosen for its new
// sandboxes, and what config.toml chose: Conch.app's Settings → Sandboxes
// and New Sandbox sheet draw it.
type sandboxSettingsJSON struct {
	Name     string                `json:"name"`
	Label    string                `json:"label"`
	KeyEnv   string                `json:"key_env"`
	Ready    bool                  `json:"ready"`
	Why      string                `json:"why,omitempty"`
	Settings []sandboxSettingJSON  `json:"settings"`
	Size     sandbox.SizeSpec      `json:"size"`
	Life     int                   `json:"life_mins,omitempty"`
	Chosen   sandboxChosenJSON     `json:"chosen"`
	Can      sandboxSettingsCanFor `json:"can"`
}

type sandboxSettingJSON struct {
	Key     string           `json:"key"`
	Label   string           `json:"label"`
	Help    string           `json:"help,omitempty"`
	Default string           `json:"default,omitempty"`
	Free    bool             `json:"free,omitempty"`
	Choices []sandbox.Choice `json:"choices"`
	// Error is why the choices could not be read: the setting still takes
	// a value typed in.
	Error string `json:"error,omitempty"`
}

type sandboxChosenJSON struct {
	Region string `json:"region,omitempty"`
	Image  string `json:"image,omitempty"`
	Size   string `json:"size,omitempty"`
	CPU    int    `json:"cpu,omitempty"`
	Memory int    `json:"memory,omitempty"`
	Disk   int    `json:"disk,omitempty"`
}

// sandboxSettingsCanFor is what else the file describes.
type sandboxSettingsCanFor struct {
	Snapshots bool `json:"snapshots,omitempty"`
	Preview   bool `json:"preview,omitempty"`
	Usage     bool `json:"usage,omitempty"`
}

// choicesWait bounds reading one setting's choices from the provider.
const choicesWait = 10 * time.Second

// sandboxSettings is conch sandbox -provider P settings [-json].
func sandboxSettings(args []string) error {
	asJSON := len(args) == 1 && args[0] == "-json"
	if len(args) > 0 && !asJSON {
		return errors.New("usage: conch sandbox -provider P settings [-json]")
	}
	p, err := remote.OpenProvider(sandboxProvider)
	if err != nil {
		return err
	}
	c, ok := p.(*sandbox.Custom)
	if !ok {
		return fmt.Errorf("%s is not a provider file", sandboxProvider)
	}
	cfg, _ := config.Load()
	pc := cfg.Sandbox.Of(sandboxProvider)
	out := sandboxSettingsJSON{Name: sandboxProvider, Label: sandbox.ProviderLabel(sandboxProvider),
		KeyEnv: firstSetStr(pc.APIKeyEnv, c.File().KeyEnv), Settings: []sandboxSettingJSON{}, Size: c.Sizes(),
		Chosen: sandboxChosenJSON{Region: pc.RegionOr(), Image: pc.ImageOr(), Size: pc.Size, CPU: pc.CPU, Memory: pc.Memory, Disk: pc.Disk}}
	if err := c.Check(); err != nil {
		out.Why = strings.TrimPrefix(err.Error(), sandbox.ErrNotConfigured.Error()+": ")
	} else {
		out.Ready = true
	}
	if l, ok := sandbox.As[sandbox.Lifetime](p); ok {
		out.Life = int(l.Life().Minutes())
	}
	_, out.Can.Snapshots = sandbox.As[sandbox.Snapshotter](p)
	_, out.Can.Preview = sandbox.As[sandbox.Previewer](p)
	_, out.Can.Usage = sandbox.As[sandbox.Metered](p)
	for _, s := range c.Settings() {
		j := sandboxSettingJSON{Key: s.Key, Label: firstSetStr(s.Label, s.Key), Help: s.Help, Default: s.Default, Free: s.Free, Choices: []sandbox.Choice{}}
		// A list read from the provider needs the key; without it the
		// setting is still offered, to type into.
		if s.ChoicesCall == "" || out.Ready {
			ctx, cancel := context.WithTimeout(context.Background(), choicesWait)
			ch, err := c.Choices(ctx, s.Key)
			cancel()
			if err != nil {
				j.Error = err.Error()
			} else {
				j.Choices = ch
			}
		}
		out.Settings = append(out.Settings, j)
	}
	if asJSON {
		return json.NewEncoder(os.Stdout).Encode(out)
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "SETTING\tCHOICES\tNOW")
	chosen := map[string]string{"region": out.Chosen.Region, "image": out.Chosen.Image}
	for _, s := range out.Settings {
		var vals []string
		for _, ch := range s.Choices {
			vals = append(vals, ch.Value)
		}
		list := strings.Join(vals, ", ")
		switch {
		case s.Error != "":
			list = "(couldn't read them: " + s.Error + ")"
		case list == "" && s.Free:
			list = "any"
		}
		now := firstSetStr(chosen[s.Key], "the default")
		if chosen[s.Key] != "" {
			now += " (chosen)"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", s.Label, list, now)
	}
	switch z := out.Size; {
	case len(z.Names) > 0:
		fmt.Fprintf(tw, "Size\t%s\t%s\n", strings.Join(z.Names, ", "), firstSetStr(out.Chosen.Size, "the default"))
	case len(z.CPU)+len(z.MemoryGiB)+len(z.DiskGiB) > 0:
		fmt.Fprintf(tw, "Size\t%s\t%s\n", sizeLimits(z), sizeNow(out.Chosen))
	}
	return tw.Flush()
}

func sizeLimits(z sandbox.SizeSpec) string {
	var parts []string
	for _, l := range []struct {
		v    []int
		unit string
	}{{z.CPU, " vCPU"}, {z.MemoryGiB, " GiB"}, {z.DiskGiB, " GiB disk"}} {
		if len(l.v) == 2 {
			parts = append(parts, fmt.Sprintf("%d–%d%s", l.v[0], l.v[1], l.unit))
		}
	}
	return strings.Join(parts, ", ")
}

func sizeNow(c sandboxChosenJSON) string {
	var parts []string
	for _, p := range []struct {
		n    int
		unit string
	}{{c.CPU, " vCPU"}, {c.Memory, " GiB"}, {c.Disk, " GiB disk"}} {
		if p.n > 0 {
			parts = append(parts, fmt.Sprintf("%d%s", p.n, p.unit))
		}
	}
	if len(parts) == 0 {
		return "the default"
	}
	return strings.Join(parts, ", ")
}

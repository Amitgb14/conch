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
	"github.com/Amitgb14/conch/internal/integration"
	"github.com/Amitgb14/conch/internal/remote"
	"github.com/Amitgb14/conch/internal/sandbox"
)

// sandboxSettingsJSON is what a provider's file lets be chosen for its new
// sandboxes, and what config.toml chose: Conch.app's Settings → Sandboxes
// and New Sandbox sheet draw it.
type sandboxSettingsJSON struct {
	Name     string               `json:"name"`
	Label    string               `json:"label"`
	KeyEnv   string               `json:"key_env"`
	Ready    bool                 `json:"ready"`
	Why      string               `json:"why,omitempty"`
	Settings []sandboxSettingJSON `json:"settings"`
	Size     sandbox.SizeSpec     `json:"size"`
	// Sizes are what the account may make, read from the provider; SizeError
	// why they could not be.
	Sizes     []sandbox.SizeOption `json:"sizes,omitempty"`
	SizeError string               `json:"size_error,omitempty"`
	// Takes are the parts of a size the provider's create sends.
	Takes  []string              `json:"takes"`
	Life   int                   `json:"life_mins,omitempty"`
	Chosen sandboxChosenJSON     `json:"chosen"`
	Can    sandboxSettingsCanFor `json:"can"`
	// Capabilities are what the provider can do, and what its create
	// takes: the New Sandbox sheet asks for those and nothing else.
	Capabilities *integration.Capabilities `json:"capabilities,omitempty"`
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
	Region   string `json:"region,omitempty"`
	Template string `json:"template,omitempty"`
	Image    string `json:"image,omitempty"`
	Size     string `json:"size,omitempty"`
	CPU      int    `json:"cpu,omitempty"`
	Memory   int    `json:"memory,omitempty"`
	Disk     int    `json:"disk,omitempty"`
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
		Chosen: sandboxChosenJSON{Region: pc.RegionOr(), Template: pc.Template, Image: pc.ImageOr(), Size: pc.Size, CPU: pc.CPU, Memory: pc.Memory, Disk: pc.Disk},
		Takes:  c.Takes()}
	if err := c.Check(); err != nil {
		out.Why = strings.TrimPrefix(err.Error(), sandbox.ErrNotConfigured.Error()+": ")
	} else {
		out.Ready = true
	}
	if l, ok := sandbox.As[sandbox.Lifetime](p); ok {
		out.Life = int(l.Life().Minutes())
	}
	caps := sandbox.CapabilitiesOf(sandboxProvider)
	out.Capabilities = caps
	_, out.Can.Snapshots = sandbox.As[sandbox.Snapshotter](p)
	_, out.Can.Preview = sandbox.As[sandbox.Previewer](p)
	_, out.Can.Usage = sandbox.As[sandbox.Metered](p)
	out.Can.Snapshots = out.Can.Snapshots && caps.Can("snapshot_save")
	out.Can.Preview = out.Can.Preview && caps.Can("preview")
	out.Can.Usage = out.Can.Usage && caps.Can("usage")
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
			} else if ch != nil { // a nil list stays [] for Conch.app
				j.Choices = ch
			}
		}
		out.Settings = append(out.Settings, j)
	}
	if out.Ready && c.Sizes().Choices.Call != "" {
		ctx, cancel := context.WithTimeout(context.Background(), choicesWait)
		out.Sizes, err = c.SizeOptions(ctx)
		cancel()
		if err != nil {
			out.SizeError = err.Error()
		}
	}
	if asJSON {
		return json.NewEncoder(os.Stdout).Encode(out)
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "SETTING\tCHOICES\tNOW")
	chosen := map[string]string{"region": out.Chosen.Region, "image": out.Chosen.Image, "template": out.Chosen.Template}
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
	case len(out.Sizes) > 0 || out.SizeError != "":
		var opts []string
		for _, o := range out.Sizes {
			opts = append(opts, sizeOptionWords(o))
		}
		list := strings.Join(opts, ", ")
		if out.SizeError != "" {
			list = "(couldn't read them: " + out.SizeError + ")"
		}
		fmt.Fprintf(tw, "Size\t%s\t%s\n", list, sizeNow(out.Chosen))
	case len(z.Names) > 0:
		fmt.Fprintf(tw, "Size\t%s\t%s\n", strings.Join(z.Names, ", "), firstSetStr(out.Chosen.Size, "the default"))
	case len(z.CPU)+len(z.MemoryGiB)+len(z.DiskGiB) > 0:
		fmt.Fprintf(tw, "Size\t%s\t%s\n", sizeLimits(z), sizeNow(out.Chosen))
	}
	return tw.Flush()
}

// sizeOptionWords is a size as people read it: "4 vCPU · 8 GiB".
func sizeOptionWords(o sandbox.SizeOption) string {
	s := fmt.Sprintf("%d vCPU", o.CPU)
	if o.Memory > 0 {
		s += fmt.Sprintf(" · %d GiB", o.Memory)
	}
	if o.Disk > 0 {
		s += fmt.Sprintf(" · %d GiB disk", o.Disk)
	}
	return s
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

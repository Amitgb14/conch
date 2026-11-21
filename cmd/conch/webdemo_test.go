package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/Amitgb14/conch/internal/adapter"
	"github.com/Amitgb14/conch/internal/config"
)

// The website's sandbox demo (web/src/components/landing/sandbox-demo.tsx)
// lets somebody pick a provider, its size and an agent, and prints the
// commands conch would run. What it offers is read from sandbox-demo.json,
// and this test holds that file to conch itself: the providers' sizes and
// regions to their registry files, the agents to the supported ones, and
// every flag the page prints to the CLI's usage. A page that offers a size
// conch refuses, or a flag that was renamed, is a promise nobody checks.
type webDemo struct {
	Providers []struct {
		Name    string           `json:"name"`
		Label   string           `json:"label"`
		KeyEnv  string           `json:"keyEnv"`
		Regions []string         `json:"regions"`
		Sizes   []string         `json:"sizes"`
		Limits  map[string][]int `json:"limits"`
	} `json:"providers"`
	Agents []struct {
		Name  string `json:"name"`
		Label string `json:"label"`
	} `json:"agents"`
	Flags      map[string][]string `json:"flags"`
	ConfigKeys []string            `json:"configKeys"`
}

func readWebDemo(t *testing.T) webDemo {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "web", "src", "components", "landing", "sandbox-demo.json"))
	if err != nil {
		t.Fatal(err)
	}
	var d webDemo
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatalf("sandbox-demo.json: %v", err)
	}
	return d
}

func TestWebSandboxDemoProviders(t *testing.T) {
	d := readWebDemo(t)
	if len(d.Providers) == 0 {
		t.Fatal("the demo offers no provider")
	}
	for _, p := range d.Providers {
		var f struct {
			Label  string `toml:"label"`
			KeyEnv string `toml:"key_env"`
			Size   struct {
				Names     []string `toml:"names"`
				CPU       []int    `toml:"cpu"`
				MemoryGiB []int    `toml:"memory_gib"`
				DiskGiB   []int    `toml:"disk_gib"`
			} `toml:"size"`
			Setting []struct {
				Key     string   `toml:"key"`
				Choices []string `toml:"choices"`
			} `toml:"setting"`
		}
		path := filepath.Join("..", "..", "internal", "sandbox", "testdata", "registry", p.Name+".toml")
		if _, err := toml.DecodeFile(path, &f); err != nil {
			t.Errorf("%s: no provider file conch knows (%v)", p.Name, err)
			continue
		}
		if p.Label != f.Label || p.KeyEnv != f.KeyEnv {
			t.Errorf("%s: demo says %q/%s, its file %q/%s", p.Name, p.Label, p.KeyEnv, f.Label, f.KeyEnv)
		}
		if !slicesEq(p.Sizes, f.Size.Names) {
			t.Errorf("%s: sizes %v, its file %v", p.Name, p.Sizes, f.Size.Names)
		}
		limits := map[string][]int{}
		for k, v := range map[string][]int{"cpu": f.Size.CPU, "memory": f.Size.MemoryGiB, "disk": f.Size.DiskGiB} {
			if len(v) > 0 {
				limits[k] = v
			}
		}
		if len(p.Limits) == 0 {
			p.Limits = map[string][]int{}
		}
		if !reflect.DeepEqual(p.Limits, limits) {
			t.Errorf("%s: limits %v, its file %v", p.Name, p.Limits, limits)
		}
		var regions []string
		for _, s := range f.Setting {
			if s.Key == "region" {
				regions = s.Choices
			}
		}
		if !slicesEq(p.Regions, regions) {
			t.Errorf("%s: regions %v, its file %v", p.Name, p.Regions, regions)
		}
	}
}

func TestWebSandboxDemoAgents(t *testing.T) {
	d := readWebDemo(t)
	t.Setenv("HOME", t.TempDir())
	reg, err := adapter.New("/bin/conch", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var want, got []string
	for _, a := range reg {
		if a.Tier() == adapter.TierSupported {
			want = append(want, a.Name()+"="+a.Label())
		}
	}
	for _, a := range d.Agents {
		got = append(got, a.Name+"="+a.Label)
	}
	if !slicesEq(got, want) {
		t.Errorf("demo agents %v, supported %v", got, want)
	}
}

// Each command the page prints names its flags in "flags"; every one has
// to be on that command's line of the usage.
func TestWebSandboxDemoFlags(t *testing.T) {
	d := readWebDemo(t)
	if len(d.Flags) == 0 {
		t.Fatal("the demo names no command")
	}
	lines := strings.Split(usage, "\n")
	for cmd, flags := range d.Flags {
		var line string
		for _, l := range lines {
			if strings.HasPrefix(strings.TrimSpace(l), cmd+" ") {
				line = l
				break
			}
		}
		if line == "" {
			t.Errorf("no usage line starts %q", cmd)
			continue
		}
		for _, f := range flags {
			if !regexp.MustCompile(`\[` + regexp.QuoteMeta(f) + `[ \]]`).MatchString(line) {
				t.Errorf("%s: usage has no %s (%s)", cmd, f, strings.TrimSpace(line))
			}
		}
	}
}

// The page writes a [sandbox.NAME] table for the size a task's sandbox is
// made with; each key it writes has to be one config.toml reads.
func TestWebSandboxDemoConfigKeys(t *testing.T) {
	d := readWebDemo(t)
	if len(d.ConfigKeys) == 0 {
		t.Fatal("the demo names no config key")
	}
	tags := map[string]bool{}
	ty := reflect.TypeOf(config.ProviderCfg{})
	for i := 0; i < ty.NumField(); i++ {
		tags[strings.Split(ty.Field(i).Tag.Get("toml"), ",")[0]] = true
	}
	for _, k := range d.ConfigKeys {
		if !tags[k] {
			t.Errorf("[sandbox.NAME] has no key %q", k)
		}
	}
}

func slicesEq(a, b []string) bool {
	return len(a) == len(b) && (len(a) == 0 || reflect.DeepEqual(a, b))
}

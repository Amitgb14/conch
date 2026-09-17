package config

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

// a3Isolate points every location conch reads at fresh temp directories.
func a3Isolate(t *testing.T) (home, conchHome string) {
	t.Helper()
	t.Setenv("CONCH_SOCKET", "")
	t.Setenv("CONCH_PANE_ID", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	home, conchHome = t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CONCH_HOME", conchHome)
	return home, conchHome
}

func TestA3Defaults(t *testing.T) {
	d := Default()
	want := Config{
		Keys:   Keys{Prefix: "ctrl+b"},
		Notify: NotifyCfg{Enabled: true, Desktop: true, Waiting: true, Done: true, Limits: true, LimitAt: []int{80, 95}},
		UI:     UICfg{Mouse: true, Theme: "conch"},
		Agents: AgentsCfg{Default: "claude"},
		Brain:  BrainCfg{Provider: "claude"},
		Update: UpdateCfg{CheckReleases: true},
		Remote: RemoteCfg{UploadDrops: true, UploadMaxMB: 25},
	}
	if !reflect.DeepEqual(d, want) {
		t.Fatalf("Default = %+v", d)
	}
	// Each call is independent: mutating one doesn't leak into the next.
	d.Keys.Prefix = "ctrl+a"
	if Default().Keys.Prefix != "ctrl+b" {
		t.Fatal("Default shares state")
	}
}

func TestA3DirOrder(t *testing.T) {
	home, _ := a3Isolate(t)

	t.Setenv("CONCH_HOME", "/custom/conch")
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if got := Dir(); got != "/custom/conch" {
		t.Fatalf("CONCH_HOME: %q", got)
	}
	t.Setenv("CONCH_HOME", "")
	if got := Dir(); got != filepath.Join("/xdg", "conch") {
		t.Fatalf("XDG_CONFIG_HOME: %q", got)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	if got := Dir(); got != filepath.Join(home, ".config", "conch") {
		t.Fatalf("home: %q", got)
	}
}

func TestA3DirWithoutHome(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "plan9" {
		t.Skip("home comes from other variables")
	}
	a3Isolate(t)
	t.Setenv("CONCH_HOME", "")
	t.Setenv("HOME", "")
	if _, err := os.UserHomeDir(); err == nil {
		t.Skip("home directory still resolvable")
	}
	if got := Dir(); got != filepath.Join(os.TempDir(), "conch") {
		t.Fatalf("no home: %q", got)
	}
}

func TestA3SocketAndLogPaths(t *testing.T) {
	_, conchHome := a3Isolate(t)
	if got := SocketPath(); got != filepath.Join(conchHome, "conch.sock") {
		t.Fatalf("SocketPath = %q", got)
	}
	if got := ServerLogPath(); got != filepath.Join(conchHome, "server.log") {
		t.Fatalf("ServerLogPath = %q", got)
	}
	t.Setenv("CONCH_SOCKET", "/run/other.sock")
	if got := SocketPath(); got != "/run/other.sock" {
		t.Fatalf("CONCH_SOCKET override = %q", got)
	}
	if got := ServerLogPath(); got != filepath.Join(conchHome, "server.log") {
		t.Fatalf("CONCH_SOCKET must not move the log: %q", got)
	}
}

func TestA3LoadMissingFile(t *testing.T) {
	_, conchHome := a3Isolate(t)
	cfg, err := Load()
	if err != nil || !reflect.DeepEqual(cfg, Default()) {
		t.Fatalf("no file: %+v %v", cfg, err)
	}
	// A CONCH_HOME that doesn't exist yet is fine too.
	t.Setenv("CONCH_HOME", filepath.Join(conchHome, "does", "not", "exist"))
	if cfg, err := Load(); err != nil || !reflect.DeepEqual(cfg, Default()) {
		t.Fatalf("no dir: %+v %v", cfg, err)
	}
}

func a3WriteConfig(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestA3LoadPartialFileKeepsDefaults(t *testing.T) {
	_, conchHome := a3Isolate(t)
	a3WriteConfig(t, conchHome, `
[ui]
theme = "dracula"

[notify]
done = false
quiet_start = "22:00"
quiet_end = "07:30"

[brain]
provider = "openai"
base_url = "http://localhost:11434/v1"
summaries = true

[future_section]
anything = 1
`)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := Default()
	want.UI.Theme = "dracula"
	want.Notify.Done = false
	want.Notify.QuietStart, want.Notify.QuietEnd = "22:00", "07:30"
	want.Brain.Provider, want.Brain.BaseURL, want.Brain.Summaries = "openai", "http://localhost:11434/v1", true
	if !reflect.DeepEqual(cfg, want) {
		t.Fatalf("got  %+v\nwant %+v", cfg, want)
	}
}

func TestA3LoadEmptyPrefixFallsBack(t *testing.T) {
	_, conchHome := a3Isolate(t)
	a3WriteConfig(t, conchHome, "[keys]\nprefix = \"\"\n[ui]\nmouse = false\n")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Keys.Prefix != "ctrl+b" {
		t.Fatalf("empty prefix must fall back: %q", cfg.Keys.Prefix)
	}
	if cfg.UI.Mouse {
		t.Fatal("explicit false overrides a true default")
	}
}

func TestA3LoadInvalidFiles(t *testing.T) {
	_, conchHome := a3Isolate(t)
	for name, body := range map[string]string{
		"syntax":     "[ui\ntheme = \"x\"\n",
		"type":       "[ui]\nmouse = \"yes\"\n",
		"duplicate":  "[ui]\ntheme = \"a\"\ntheme = \"b\"\n",
		"bad string": "[ui]\ntheme = \"unterminated\n",
	} {
		a3WriteConfig(t, conchHome, body)
		cfg, err := Load()
		if err == nil {
			t.Errorf("%s: no error", name)
			continue
		}
		if !reflect.DeepEqual(cfg, Default()) {
			t.Errorf("%s: invalid file must yield pure defaults, got %+v", name, cfg)
		}
	}

	// config.toml that is a directory.
	if err := os.Remove(filepath.Join(conchHome, "config.toml")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(conchHome, "config.toml"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Error("directory as config.toml: no error")
	}
}

func TestA3SaveFullRoundTrip(t *testing.T) {
	_, conchHome := a3Isolate(t)
	nested := filepath.Join(conchHome, "a", "b")
	t.Setenv("CONCH_HOME", nested)

	cfg := Config{
		Keys:   Keys{Prefix: "ctrl+space"},
		Pane:   PaneCfg{DefaultCommand: "fish -l"},
		Notify: NotifyCfg{Enabled: true, Desktop: false, Sound: true, Bell: true, Waiting: false, Done: true, QuietStart: "23:00", QuietEnd: "06:00", Limits: false, LimitAt: []int{50, 90}},
		UI:     UICfg{Mouse: false, Theme: "tokyo-night", Accent: "#ff00aa"},
		Shell:  ShellCfg{OMZTheme: "robbyrussell"},
		Agents: AgentsCfg{Default: "codex"},
		Brain:  BrainCfg{Provider: "anthropic", Model: "m", SummaryModel: "s", Summaries: true, BaseURL: "https://x", APIKeyEnv: "MY_KEY", Command: "/opt/claude"},
		Update: UpdateCfg{CheckReleases: false},
	}
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil || !reflect.DeepEqual(got, cfg) {
		t.Fatalf("round trip:\n got  %+v\n want %+v\n err %v", got, cfg, err)
	}

	path := filepath.Join(nested, "config.toml")
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode: %v %v", info, err)
	}
	if dinfo, _ := os.Stat(nested); dinfo.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode: %v", dinfo.Mode())
	}
	b, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(b), "# conch settings.") {
		t.Fatalf("header missing: %q", b)
	}
	for _, key := range []string{"[keys]", "prefix = \"ctrl+space\"", "check_releases = false", "api_key_env = \"MY_KEY\""} {
		if !strings.Contains(string(b), key) {
			t.Errorf("saved file lacks %q:\n%s", key, b)
		}
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temp file left behind: %v", err)
	}

	// Saving again overwrites (comments in a hand-edited file are lost).
	if err := os.WriteFile(path, []byte("# my notes\n[ui]\ntheme = \"nord\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Save(Default()); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	if strings.Contains(string(b), "my notes") {
		t.Fatal("old content survived Save")
	}
	if got, err := Load(); err != nil || !reflect.DeepEqual(got, Default()) {
		t.Fatalf("after overwrite: %+v %v", got, err)
	}
}

func TestA3SaveErrors(t *testing.T) {
	_, conchHome := a3Isolate(t)
	file := filepath.Join(conchHome, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONCH_HOME", filepath.Join(file, "sub"))
	if err := Save(Default()); err == nil {
		t.Fatal("CONCH_HOME under a file: no error")
	}

	// The temp file's name is taken by a directory.
	dir := filepath.Join(conchHome, "d")
	if err := os.MkdirAll(filepath.Join(dir, "config.toml.tmp"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONCH_HOME", dir)
	if err := Save(Default()); err == nil {
		t.Fatal("unwritable temp file: no error")
	}

	// The destination is a non-empty directory, so the rename fails.
	dir2 := filepath.Join(conchHome, "d2")
	if err := os.MkdirAll(filepath.Join(dir2, "config.toml", "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONCH_HOME", dir2)
	if err := Save(Default()); err == nil {
		t.Fatal("rename over a directory: no error")
	}
}

func TestA3DefaultShell(t *testing.T) {
	t.Setenv("SHELL", "/usr/local/bin/fish")
	if got := DefaultShell(); got != "/usr/local/bin/fish" {
		t.Fatalf("SHELL set: %q", got)
	}
	t.Setenv("SHELL", "")
	if got := DefaultShell(); got != "/bin/sh" {
		t.Fatalf("SHELL empty: %q", got)
	}
}

func TestA3MergeEnvEdgeCases(t *testing.T) {
	if got := MergeEnv(nil); len(got) != 0 {
		t.Fatalf("nothing: %v", got)
	}
	base := []string{"A=1", "B=2"}
	got := MergeEnv(base)
	if !reflect.DeepEqual(got, base) {
		t.Fatalf("no overrides: %v", got)
	}
	got[0] = "changed"
	if base[0] != "A=1" {
		t.Fatal("MergeEnv aliased its input")
	}
	// Values containing '=' keep everything after the first one; keys are
	// case-sensitive; entries without '=' are treated as bare keys.
	got = MergeEnv([]string{"URL=a=b", "path=/x", "BARE", "KEEP=1"}, "URL=c=d", "PATH=/y", "BARE=now")
	want := []string{"path=/x", "KEEP=1", "URL=c=d", "PATH=/y", "BARE=now"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	// An empty value still replaces.
	if got := MergeEnv([]string{"X=1"}, "X="); !reflect.DeepEqual(got, []string{"X="}) {
		t.Fatalf("empty value: %v", got)
	}
}

func TestA3QuietEdgeCases(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 1, 2, h, m, 30, 0, time.UTC) }
	for _, c := range []struct {
		start, end string
		t          time.Time
		want       bool
	}{
		{"22:00", "22:00", at(22, 0), false},  // empty range
		{" 01:00 ", "02:00", at(1, 30), true}, // surrounding spaces
		{"1:5", "2:0", at(1, 5), true},        // unpadded
		{"00:00", "23:59", at(23, 58), true},  // almost all day
		{"00:00", "23:59", at(23, 59), false}, // end is exclusive
		{"23:59", "00:00", at(23, 59), true},  // one minute wrapping midnight
		{"23:59", "00:00", at(0, 0), false},
		{"12:60", "13:00", at(12, 30), false}, // bad minute
		{"-1:00", "13:00", at(12, 30), false}, // negative hour
		{"noon", "13:00", at(12, 30), false},  // not a clock
		{"12:00", "", at(12, 30), false},      // one side missing
	} {
		n := NotifyCfg{QuietStart: c.start, QuietEnd: c.end}
		if got := n.Quiet(c.t); got != c.want {
			t.Errorf("%q-%q at %s: %v, want %v", c.start, c.end, c.t.Format("15:04"), got, c.want)
		}
	}
}

// Files saved before [remote] existed upload drops by default; the table
// can turn it off, and a nonsense limit falls back to the default.
func TestRemoteUploadSettings(t *testing.T) {
	_, conchHome := a3Isolate(t)
	write := func(s string) Config {
		t.Helper()
		if err := os.WriteFile(filepath.Join(conchHome, "config.toml"), []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		return cfg
	}
	if cfg := write("[ui]\ntheme = \"nord\"\n"); !cfg.Remote.UploadDrops || cfg.Remote.UploadLimit() != 25<<20 {
		t.Fatalf("old file: %+v", cfg.Remote)
	}
	if cfg := write("[remote]\nupload_drops = false\nupload_max_mb = 100\n"); cfg.Remote.UploadDrops || cfg.Remote.UploadLimit() != 100<<20 {
		t.Fatalf("set: %+v", cfg.Remote)
	}
	for _, mb := range []int{0, -5} {
		if got := (RemoteCfg{UploadMaxMB: mb}).UploadLimit(); got != DefaultUploadMaxMB<<20 {
			t.Fatalf("%d MB: %d", mb, got)
		}
	}
	cfg := Default()
	cfg.Remote.UploadDrops = false
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	if got, _ := Load(); got.Remote.UploadDrops || got.Remote.UploadMaxMB != 25 {
		t.Fatalf("round trip: %+v", got.Remote)
	}
}

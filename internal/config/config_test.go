package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMergeEnv(t *testing.T) {
	got := MergeEnv([]string{"PATH=/bin", "CONCH_SOCKET=/outer.sock", "HOME=/h", "CONCH_SOCKET=/dup.sock"},
		"CONCH_SOCKET=/inner.sock", "TERM=xterm")
	if want := "PATH=/bin,HOME=/h,CONCH_SOCKET=/inner.sock,TERM=xterm"; strings.Join(got, ",") != want {
		t.Fatalf("got %v, want %s", got, want)
	}
}

func TestSaveLoadAndOldFiles(t *testing.T) {
	t.Setenv("CONCH_HOME", t.TempDir())
	cfg := Default()
	cfg.UI.Theme, cfg.Notify.Sound, cfg.Notify.Done, cfg.Shell.OMZTheme = "nord", true, false, "agnoster"
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil || got.UI.Theme != "nord" || !got.Notify.Sound || got.Notify.Done || got.Shell.OMZTheme != "agnoster" || got.Keys.Prefix != "ctrl+b" {
		t.Fatalf("round trip: %+v %v", got, err)
	}
}

func TestSandboxSettings(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CONCH_HOME", dir)
	// A file from before [sandbox] existed loads with Daytona unset, which
	// means never auto-stopping.
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[ui]\ntheme = \"nord\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil || got.Sandbox.Of("daytona").AutoStop != 0 || got.Sandbox.Of("daytona").APIKeyEnv != "" {
		t.Fatalf("old file: %+v %v", got.Sandbox, err)
	}
	toml := "[sandbox.daytona]\napi_key_env = \"DT_KEY\"\ntarget = \"eu\"\nsnapshot = \"daytona-medium\"\nauto_stop = 60\nenv = [\"CLAUDE_CODE_OAUTH_TOKEN\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = Load()
	d := got.Sandbox.Of("daytona")
	if err != nil || d.APIKeyEnv != "DT_KEY" || d.Target != "eu" || d.Snapshot != "daytona-medium" || d.AutoStop != 60 ||
		len(d.Env) != 1 || d.Env[0] != "CLAUDE_CODE_OAUTH_TOKEN" {
		t.Fatalf("loaded %+v %v", d, err)
	}
	if err := Save(got); err != nil {
		t.Fatal(err)
	}
	again, err := Load()
	if err != nil || again.Sandbox.Of("daytona").AutoStop != 60 || again.Sandbox.Of("daytona").Env[0] != "CLAUDE_CODE_OAUTH_TOKEN" {
		t.Fatalf("round trip %+v %v", again.Sandbox, err)
	}

	// Every provider takes the same settings under its own name, and one
	// this build doesn't know is kept rather than dropped when the
	// settings are saved.
	toml = "[sandbox.daytona]\nsnapshot = \"dt\"\n\n[sandbox.e2b]\napi_key_env = \"E2B_KEY\"\nsnapshot = \"base\"\n\n[sandbox.later]\ntarget = \"eu\"\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = Load()
	if err != nil || got.Sandbox.Of("e2b").APIKeyEnv != "E2B_KEY" || got.Sandbox.Of("e2b").Snapshot != "base" ||
		got.Sandbox.Of("daytona").Snapshot != "dt" || got.Sandbox.Of("later").Target != "eu" {
		t.Fatalf("several providers: %+v %v", got.Sandbox, err)
	}
	got.Sandbox.Set("e2b", ProviderCfg{Snapshot: "other"})
	if err := Save(got); err != nil {
		t.Fatal(err)
	}
	again, err = Load()
	if err != nil || again.Sandbox.Of("e2b").Snapshot != "other" || again.Sandbox.Of("later").Target != "eu" ||
		again.Sandbox.Of("daytona").Snapshot != "dt" {
		t.Fatalf("saved %+v %v", again.Sandbox, err)
	}
	// A key kept in the settings round-trips, and the file it lands in is
	// readable by nobody else.
	got.Sandbox.Set("daytona", ProviderCfg{APIKey: "dtn_secret", Snapshot: "dt"})
	if err := Save(got); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(filepath.Join(dir, "config.toml")); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("config.toml is %v (%v)", st.Mode().Perm(), err)
	}
	again, err = Load()
	if err != nil || again.Sandbox.Of("daytona").APIKey != "dtn_secret" {
		t.Fatalf("the key did not survive: %+v %v", again.Sandbox, err)
	}

	// A provider with nothing set answers with zeroes rather than panicking.
	var empty SandboxCfg
	if got := empty.Of("nope"); got.Snapshot != "" || got.AutoStop != 0 {
		t.Fatalf("empty: %+v", got)
	}
	empty.Set("nope", ProviderCfg{Target: "us"}) // makes the map
	if empty.Of("nope").Target != "us" {
		t.Fatalf("set on a nil map: %+v", empty)
	}
}

func TestQuietHours(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 9, 13, h, m, 0, 0, time.Local) }
	night := NotifyCfg{QuietStart: "22:00", QuietEnd: "08:00"}
	day := NotifyCfg{QuietStart: "09:00", QuietEnd: "18:30"}
	for _, tc := range []struct {
		cfg  NotifyCfg
		t    time.Time
		want bool
	}{
		{night, at(23, 0), true}, {night, at(3, 0), true}, {night, at(8, 0), false}, {night, at(21, 59), false},
		{day, at(9, 0), true}, {day, at(18, 29), true}, {day, at(18, 30), false},
		{NotifyCfg{}, at(3, 0), false}, {NotifyCfg{QuietStart: "25:00", QuietEnd: "08:00"}, at(3, 0), false},
	} {
		if got := tc.cfg.Quiet(tc.t); got != tc.want {
			t.Errorf("%s-%s at %s: %v", tc.cfg.QuietStart, tc.cfg.QuietEnd, tc.t.Format("15:04"), got)
		}
	}
}

// A config.toml with a typo in it must not take conch's settings with it:
// Load falls back to the defaults and says why, and saving keeps the file
// that could not be read instead of writing over it.
func TestLoadAndSaveWithABrokenConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CONCH_HOME", dir)
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("this is not = valid toml [[[\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err == nil {
		t.Fatal("want an error for a config that cannot be parsed")
	}
	if cfg.Keys.Prefix != Default().Keys.Prefix || cfg.UI.Theme != Default().UI.Theme {
		t.Fatalf("want the defaults back, got %+v", cfg)
	}

	// Saving keeps the unreadable file and writes a good one.
	cfg.UI.Theme = "light"
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	kept, err := os.ReadFile(path + ".invalid")
	if err != nil || !strings.Contains(string(kept), "not = valid toml") {
		t.Fatalf("the unreadable config was not kept: %v", err)
	}
	again, err := Load()
	if err != nil || again.UI.Theme != "light" {
		t.Fatalf("after saving: %+v %v", again, err)
	}

	// A config that reads fine is written over as before, with no copy.
	if err := Save(again); err != nil {
		t.Fatal(err)
	}
	if entries, _ := filepath.Glob(filepath.Join(dir, "*.invalid")); len(entries) != 1 {
		t.Fatalf("a readable config should not be copied aside: %v", entries)
	}
	// And a config that isn't there at all is no trouble either.
	os.Remove(path)
	if err := Save(Default()); err != nil {
		t.Fatal(err)
	}
	// Something that isn't a file of ours — a directory in its place — is
	// left where it is, and saving fails as it always did.
	other := t.TempDir()
	t.Setenv("CONCH_HOME", other)
	if err := os.MkdirAll(filepath.Join(other, "config.toml", "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Save(Default()); err == nil {
		t.Fatal("want an error when config.toml is a directory")
	}
	if _, err := os.Stat(filepath.Join(other, "config.toml", "x")); err != nil {
		t.Fatalf("the directory was moved aside: %v", err)
	}
}

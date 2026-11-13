package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCloudEnabled(t *testing.T) {
	on, off := Config{Cloud: CloudCfg{Enabled: true}}, Config{}
	for _, tc := range []struct {
		env  string
		cfg  Config
		want bool
	}{
		{"", off, false}, // the release: hidden
		{"", on, true},
		{"1", off, true},
		{"true", off, true},
		{" YES ", off, true},
		{"on", off, true},
		{"0", on, false}, // the variable wins either way
		{"false", on, false},
		{"off", on, false},
		{"2", off, false}, // only the words for on are on
		{"enabled", on, false},
		{"   ", on, true}, // blank is unset
	} {
		t.Setenv(CloudEnv, tc.env)
		if got := CloudEnabled(tc.cfg); got != tc.want {
			t.Errorf("CONCH_CLOUD=%q enabled=%v: got %v, want %v", tc.env, tc.cfg.Cloud.Enabled, got, tc.want)
		}
	}
}

func TestCloudOnReadsTheFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CONCH_HOME", dir)
	t.Setenv(CloudEnv, "")
	file := filepath.Join(dir, "config.toml")
	if CloudOn() {
		t.Fatal("no config.toml: Cloud is on")
	}
	// A file from before the switch, with only the server, keeps Cloud off.
	if err := os.WriteFile(file, []byte("[cloud]\napi = \"https://example.test\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if CloudOn() {
		t.Fatal("[cloud] api alone turned Cloud on")
	}
	if err := os.WriteFile(file, []byte("[cloud]\nenabled = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !CloudOn() {
		t.Fatal("[cloud] enabled = true: Cloud is off")
	}
	t.Setenv(CloudEnv, "0")
	if CloudOn() {
		t.Fatal("CONCH_CLOUD=0 did not win over the file")
	}
	// A file conch cannot read is Cloud off, unless the variable says on.
	if err := os.WriteFile(file, []byte("[cloud\nenabled = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(CloudEnv, "")
	if CloudOn() {
		t.Fatal("an unreadable file turned Cloud on")
	}
	t.Setenv(CloudEnv, "1")
	if !CloudOn() {
		t.Fatal("CONCH_CLOUD=1 with an unreadable file: Cloud is off")
	}
	// Saved and loaded again, the switch survives.
	if err := Save(Config{Cloud: CloudCfg{Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	t.Setenv(CloudEnv, "")
	if !CloudOn() {
		t.Fatal("[cloud] enabled lost in a save")
	}
}

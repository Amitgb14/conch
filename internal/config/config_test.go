package config

import (
	"strings"
	"testing"
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

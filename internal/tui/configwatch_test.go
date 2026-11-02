package tui

import (
	"os"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/config"
)

// waitConfigStamp waits until a write now would change config.toml's
// stamp, on a file system that keeps coarse times.
func waitConfigStamp(t *testing.T) {
	t.Helper()
	if st, err := os.Stat(config.File()); err == nil {
		for time.Since(st.ModTime()) < 10*time.Millisecond {
			time.Sleep(time.Millisecond)
		}
	}
}

// A setting changed outside the TUI — the browser page run on its own,
// Conch.app, an editor — is taken in, so the TUI's next save does not
// write its older copy back over it. That is how a theme chosen elsewhere
// went back to the old one.
func TestConfigChangedElsewhereIsTakenIn(t *testing.T) {
	t.Setenv("CONCH_HOME", t.TempDir())
	cfg := config.Default()
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	m, _ := a1Fixture(t, false)
	m.cfg = cfg
	w := newConfigWatch()
	if msg := pollConfig(w); msg.changed {
		t.Fatal("an unchanged file read as changed")
	}

	other := cfg
	other.UI.Theme = "tokyo-night"
	waitConfigStamp(t)
	if err := config.Save(other); err != nil {
		t.Fatal(err)
	}
	msg := pollConfig(w)
	if !msg.changed || msg.cfg.UI.Theme != "tokyo-night" {
		t.Fatalf("not seen: %+v", msg)
	}
	m.takeConfigPoll(msg)
	if m.cfg.UI.Theme != "tokyo-night" || m.flash == "" {
		t.Fatalf("not taken in: theme %q flash %q", m.cfg.UI.Theme, m.flash)
	}
	// Seen once.
	if pollConfig(w).changed {
		t.Fatal("read twice")
	}

	// The TUI's own save reads back as what it holds: nothing to take.
	m.flash = ""
	m.cfg.UI.Cost = !m.cfg.UI.Cost
	waitConfigStamp(t)
	if done := saveConfig(m.cfg)(); done != nil {
		t.Fatalf("save: %v", done)
	}
	own := pollConfig(w)
	if !own.changed {
		t.Fatal("the save was not seen")
	}
	if m.takeConfigPoll(own); m.flash != "" {
		t.Fatalf("its own save was taken as another's: %q", m.flash)
	}
}

// While one of this process's saves is being written, the file is not
// read: it is between two of the TUI's own states.
func TestConfigWatchWaitsForOwnSave(t *testing.T) {
	t.Setenv("CONCH_HOME", t.TempDir())
	w := newConfigWatch()
	waitConfigStamp(t)
	if err := config.Save(config.Default()); err != nil {
		t.Fatal(err)
	}
	configSaving.Add(1)
	if pollConfig(w).changed {
		configSaving.Add(-1)
		t.Fatal("read while a save was in flight")
	}
	configSaving.Add(-1)
	if !pollConfig(w).changed {
		t.Fatal("not read once the save landed")
	}
}

// A file that does not parse is not taken: the TUI keeps what it has, and
// looks again when it changes.
func TestConfigWatchSkipsABrokenFile(t *testing.T) {
	t.Setenv("CONCH_HOME", t.TempDir())
	w := newConfigWatch()
	if err := os.MkdirAll(config.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	waitConfigStamp(t)
	if err := os.WriteFile(config.File(), []byte("[ui\ntheme = "), 0o600); err != nil {
		t.Fatal(err)
	}
	if pollConfig(w).changed {
		t.Fatal("a broken file was taken")
	}
	waitConfigStamp(t)
	if err := config.Save(config.Default()); err != nil {
		t.Fatal(err)
	}
	if !pollConfig(w).changed {
		t.Fatal("the fixed file was not seen")
	}
	// Missing altogether: nothing to take.
	os.Remove(config.File())
	if msg := pollConfig(w); msg.changed && msg.cfg.UI.Theme != config.Default().UI.Theme {
		t.Fatalf("missing file: %+v", msg)
	}
}

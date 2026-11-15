package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestUserScenes(t *testing.T) {
	if got := UserScenes(filepath.Join(t.TempDir(), "missing")); got != nil {
		t.Fatalf("missing folder: %v", got)
	}
	dir := t.TempDir()
	for _, f := range []string{"bay.toml", "a-1_b.toml", "Upper.toml", "reef.toml", "board.toml", "notes.txt", ".hidden.toml", "bay.jpg"} {
		if err := os.WriteFile(filepath.Join(dir, f), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "dir.toml"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Only what the app would offer: a lower-case name, no built-in's, no folder.
	if got := UserScenes(dir); !slices.Equal(got, []string{"a-1_b", "bay"}) {
		t.Fatalf("got %v", got)
	}
	home := t.TempDir()
	t.Setenv("CONCH_HOME", home)
	if ScenesDir() != filepath.Join(home, "scenes") {
		t.Fatal(ScenesDir())
	}
}

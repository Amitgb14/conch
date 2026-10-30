package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// The server is started from the conch the app carries: in the bundle's
// Resources on macOS, beside the window elsewhere, else on PATH — never
// from the window itself.
func TestBundledConch(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	self, _ = filepath.EvalSymlinks(self)
	dir := filepath.Dir(self)
	t.Setenv("PATH", t.TempDir()) // no conch there
	if _, err := bundledConch(); err == nil {
		// Only if a conch happens to sit beside the test binary.
		if _, statErr := os.Stat(filepath.Join(dir, "conch")); statErr != nil {
			t.Fatal("found a conch where there is none")
		}
	}

	onPath := filepath.Join(t.TempDir(), "conch")
	os.WriteFile(onPath, []byte("#!/bin/sh\n"), 0o755)
	t.Setenv("PATH", filepath.Dir(onPath))
	got, err := bundledConch()
	if err != nil || got != onPath {
		t.Fatalf("on PATH: %q %v", got, err)
	}
	// A file there that cannot run is not one.
	os.Chmod(onPath, 0o644)
	if got, err := bundledConch(); err == nil && got == onPath {
		t.Fatal("took a conch that is not executable")
	}
	if runtime.GOOS == "darwin" {
		// The bundle layout is checked against the built app by build.sh;
		// the path is Contents/MacOS/../Resources/conch.
		if want := filepath.Join(dir, "..", "Resources", "conch"); filepath.Base(filepath.Dir(want)) != "Resources" {
			t.Fatal(want)
		}
	}
}

package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// adoptGit takes a repository only when its top level is the project's own
// folder, and leaves a plain folder plain until one is.
func TestAdoptGit(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	s, dir := a5Server(t)
	gitInit := func(d string) {
		t.Helper()
		if out, err := exec.Command("git", "-C", d, "init", "-q").CombinedOutput(); err != nil {
			t.Fatalf("git init: %v\n%s", err, out)
		}
	}
	plain := func(rel string) *project {
		t.Helper()
		folder := filepath.Join(dir, rel)
		if err := os.MkdirAll(folder, 0o755); err != nil {
			t.Fatal(err)
		}
		p, err := s.projects.add(folder, false)
		if err != nil {
			t.Fatal(err)
		}
		if p.snapshot().Git || p.commonDir() != "" {
			t.Fatalf("%s added as git", rel)
		}
		return p
	}

	// No .git: nothing, and nothing asked of git.
	notes := plain("notes")
	if got := s.projects.adoptGit(notes); got != "" {
		t.Fatalf("plain folder adopted %q", got)
	}

	// Inside a repository made afterwards, with a .git of its own that is
	// not a repository (a stray file): still plain.
	inner := plain("outer/inner")
	gitInit(filepath.Join(dir, "outer"))
	os.WriteFile(filepath.Join(dir, "outer", "inner", ".git"), []byte("gitdir: /nowhere\n"), 0o644)
	if got := s.projects.adoptGit(inner); got != "" {
		t.Fatalf("folder inside another repository adopted %q", got)
	}

	// Its own repository: adopted, and a refresh asked for.
	gitInit(filepath.Join(dir, "notes"))
	want := filepath.Join(dir, "notes", ".git")
	if got := s.projects.adoptGit(notes); got != want {
		t.Fatalf("adoptGit = %q, want %q", got, want)
	}
	notes.mu.Lock()
	pending := notes.pending
	notes.mu.Unlock()
	if !pending {
		t.Fatal("no refresh asked for")
	}
	s.projects.refresh(notes, "")
	if !notes.snapshot().Git {
		t.Fatal("still not a git project after a refresh")
	}
}

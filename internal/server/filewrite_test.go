package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

// A file read whole carries its fingerprint, and a save of an edit made
// from it goes through; a save from a file that changed since — an agent
// wrote it — is refused and changes nothing, unless forced.
func TestWriteFileFromWhatWasRead(t *testing.T) {
	a5IsolateEnv(t)
	s, dir := a5Server(t)
	repo := filesRepo(t, s, dir)
	c := &client{}

	r, perr := s.readFile(proto.FSReadParams{Root: repo, Path: "cmd/main.go"})
	if perr != nil || r.Hash != contentHash([]byte("package main\n")) {
		t.Fatalf("read: %+v %v", r, perr)
	}
	w, perr := s.writeFile(c, proto.FSWriteParams{Root: repo, Path: "cmd/main.go", Data: "package main\n\nfunc main() {}\n", Base: r.Hash})
	if perr != nil || w.Hash != contentHash([]byte("package main\n\nfunc main() {}\n")) || w.Size != 29 || w.Path != filepath.Join(repo, "cmd", "main.go") {
		t.Fatalf("write: %+v %v", w, perr)
	}
	if got, _ := os.ReadFile(filepath.Join(repo, "cmd/main.go")); string(got) != "package main\n\nfunc main() {}\n" {
		t.Fatalf("on disk: %q", got)
	}
	// The same save again: its base is the old content now.
	if _, perr := s.writeFile(c, proto.FSWriteParams{Root: repo, Path: "cmd/main.go", Data: "x", Base: r.Hash}); perr == nil || perr.Code != proto.ErrFileChanged {
		t.Fatalf("stale base: %v", perr)
	}
	// Changed underneath (an agent): refused, the file left as the agent made it.
	filesWrite(t, repo, "cmd/main.go", "package main // agent\n")
	if _, perr := s.writeFile(c, proto.FSWriteParams{Root: repo, Path: "cmd/main.go", Data: "mine", Base: w.Hash}); perr == nil || perr.Code != proto.ErrFileChanged {
		t.Fatalf("changed on disk: %v", perr)
	}
	if got, _ := os.ReadFile(filepath.Join(repo, "cmd/main.go")); string(got) != "package main // agent\n" {
		t.Fatalf("a refused save wrote: %q", got)
	}
	// No base at all is no proof either; forced, it goes.
	if _, perr := s.writeFile(c, proto.FSWriteParams{Root: repo, Path: "cmd/main.go", Data: "mine"}); perr == nil || perr.Code != proto.ErrFileChanged {
		t.Fatalf("no base: %v", perr)
	}
	if _, perr := s.writeFile(c, proto.FSWriteParams{Root: repo, Path: "cmd/main.go", Data: "mine\n", Force: true}); perr != nil {
		t.Fatal(perr)
	}
	if got, _ := os.ReadFile(filepath.Join(repo, "cmd/main.go")); string(got) != "mine\n" {
		t.Fatalf("forced: %q", got)
	}
	// Nothing left behind beside it.
	ents, _ := os.ReadDir(filepath.Join(repo, "cmd"))
	if len(ents) != 1 {
		t.Fatalf("left in cmd/: %v", ents)
	}
}

// A file deleted since it was opened is a change; a new file is made only
// when asked, its folders with it; a file truncated in the read has no
// fingerprint, so it cannot be saved from that read.
func TestWriteFileNewDeletedAndPartial(t *testing.T) {
	a5IsolateEnv(t)
	s, dir := a5Server(t)
	repo := filesRepo(t, s, dir)
	c := &client{}
	r, _ := s.readFile(proto.FSReadParams{Root: repo, Path: "README.md"})
	os.Remove(filepath.Join(repo, "README.md"))
	if _, perr := s.writeFile(c, proto.FSWriteParams{Root: repo, Path: "README.md", Data: "x", Base: r.Hash}); perr == nil || perr.Code != proto.ErrFileChanged || !strings.Contains(perr.Message, "deleted") {
		t.Fatalf("deleted: %v", perr)
	}
	if _, perr := s.writeFile(c, proto.FSWriteParams{Root: repo, Path: "docs/new/notes.md", Data: "# notes\n", Create: true}); perr != nil {
		t.Fatal(perr)
	}
	if got, _ := os.ReadFile(filepath.Join(repo, "docs/new/notes.md")); string(got) != "# notes\n" {
		t.Fatalf("created: %q", got)
	}
	big := strings.Repeat("a", proto.FSReadMax+10)
	filesWrite(t, repo, "big.txt", big)
	if r, _ := s.readFile(proto.FSReadParams{Root: repo, Path: "big.txt"}); !r.Truncated || r.Hash != "" {
		t.Fatalf("a partial read has a fingerprint: %+v", r.Hash)
	}
	if r, _ := s.readFile(proto.FSReadParams{Root: repo, Path: "big.txt", Offset: 1}); r.Hash != "" {
		t.Fatal("a read from an offset has a fingerprint")
	}
}

// Writes stay inside a checkout of a project: not outside it by ../ or a
// link, not in a folder that is no checkout, not over a folder, not past
// the cap; a worktree is a checkout too. A link inside it is written
// through and stays a link, and a file keeps its permissions.
func TestWriteFileConfined(t *testing.T) {
	a5IsolateEnv(t)
	s, dir := a5Server(t)
	repo := filesRepo(t, s, dir)
	c := &client{}
	outside := filepath.Join(dir, "outside.txt")
	os.WriteFile(outside, []byte("keep\n"), 0o644)
	os.Symlink(outside, filepath.Join(repo, "out-link"))
	for _, wp := range []proto.FSWriteParams{
		{Root: repo, Path: "../outside.txt", Data: "x", Force: true},
		{Root: repo, Path: "out-link", Data: "x", Force: true},
		{Root: dir, Path: "outside.txt", Data: "x", Force: true},
		{Root: "", Path: outside, Data: "x", Force: true},
		{Root: repo, Path: "cmd", Data: "x", Force: true},
		{Root: repo, Path: "", Data: "x", Force: true},
		{Root: repo, Path: "huge.txt", Data: strings.Repeat("a", proto.FSWriteMax+1), Create: true},
	} {
		if _, perr := s.writeFile(c, wp); perr == nil {
			t.Errorf("%s in %s was written", wp.Path, wp.Root)
		}
	}
	if got, _ := os.ReadFile(outside); string(got) != "keep\n" {
		t.Fatalf("outside written: %q", got)
	}

	// A link inside the checkout: written through, still a link.
	filesWrite(t, repo, "real.txt", "one\n")
	os.Symlink("real.txt", filepath.Join(repo, "alias.txt"))
	r, _ := s.readFile(proto.FSReadParams{Root: repo, Path: "alias.txt"})
	if _, perr := s.writeFile(c, proto.FSWriteParams{Root: repo, Path: "alias.txt", Data: "two\n", Base: r.Hash}); perr != nil {
		t.Fatal(perr)
	}
	if fi, _ := os.Lstat(filepath.Join(repo, "alias.txt")); fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the link was replaced by a file")
	}
	if got, _ := os.ReadFile(filepath.Join(repo, "real.txt")); string(got) != "two\n" {
		t.Fatalf("through the link: %q", got)
	}
	// Permissions kept: a script stays executable.
	filesWrite(t, repo, "run.sh", "#!/bin/sh\n")
	os.Chmod(filepath.Join(repo, "run.sh"), 0o755)
	r, _ = s.readFile(proto.FSReadParams{Root: repo, Path: "run.sh"})
	if _, perr := s.writeFile(c, proto.FSWriteParams{Root: repo, Path: "run.sh", Data: "#!/bin/sh\necho hi\n", Base: r.Hash}); perr != nil {
		t.Fatal(perr)
	}
	if fi, _ := os.Stat(filepath.Join(repo, "run.sh")); fi.Mode().Perm() != 0o755 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}

	// A worktree of the project is a checkout to write in.
	id := s.projectOfCheckout(repo)
	raw, perr := s.dispatch(&client{}, proto.Message{Method: proto.MethodWorktreeAdd, Params: proto.Marshal(proto.WorktreeAddParams{ProjectID: id, Branch: "feat"})})
	if perr != nil {
		t.Fatal(perr)
	}
	wt := raw.(proto.WorktreeResult).Path
	a5WaitFor(t, "the worktree listed", func() bool { return s.projectOfCheckout(wt) == id })
	r, _ = s.readFile(proto.FSReadParams{Root: wt, Path: "README.md"})
	if _, perr := s.writeFile(c, proto.FSWriteParams{Root: wt, Path: "README.md", Data: "# wt\n", Base: r.Hash}); perr != nil {
		t.Fatal(perr)
	}
	if got, _ := os.ReadFile(filepath.Join(wt, "README.md")); string(got) != "# wt\n" {
		t.Fatalf("worktree: %q", got)
	}
}

// An agent writes through conch only in its own project's checkouts.
func TestWriteFileScoped(t *testing.T) {
	s, _, work := shareFixture(t)
	mine, err := s.projects.add(work, false)
	if err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "other")
	a5GitRepo(t, other)
	filesWrite(t, other, "a.txt", "theirs\n")
	if _, err := s.projects.add(other, false); err != nil {
		t.Fatal(err)
	}
	agent := agentPane(t, s, "p1", "codex", work, "stty -echo; exec cat")
	agent.mu.Lock()
	agent.project = mine
	agent.mu.Unlock()
	c := &client{pane: "p1"}
	if _, ok := s.scopedCaller(c); !ok {
		t.Skip("the pane is not scoped here")
	}
	if _, perr := s.writeFile(c, proto.FSWriteParams{Root: other, Path: "a.txt", Data: "x", Force: true}); perr == nil || perr.Code != proto.ErrOutOfScope {
		t.Fatalf("another project's checkout: %v", perr)
	}
	if got, _ := os.ReadFile(filepath.Join(other, "a.txt")); string(got) != "theirs\n" {
		t.Fatalf("written: %q", got)
	}
	filesWrite(t, work, "mine.txt", "a\n")
	if _, perr := s.writeFile(c, proto.FSWriteParams{Root: work, Path: "mine.txt", Data: "b\n", Force: true}); perr != nil {
		t.Fatalf("its own: %v", perr)
	}
}

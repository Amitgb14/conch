package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

// A file is renamed or moved into a folder that is made on the way; a
// folder is renamed whole; nothing is written over, moved into itself,
// taken outside the checkout or out of .git.
func TestRenamePath(t *testing.T) {
	a5IsolateEnv(t)
	s, dir := a5Server(t)
	repo := filesRepo(t, s, dir)
	c := &client{}
	r, perr := s.renamePath(c, proto.FSPathParams{Root: repo, Path: "cmd/main.go", To: "cmd/conch/app.go"})
	if perr != nil || r.Path != filepath.Join(repo, "cmd", "conch", "app.go") {
		t.Fatalf("rename: %+v %v", r, perr)
	}
	if got, _ := os.ReadFile(r.Path); string(got) != "package main\n" {
		t.Fatalf("moved: %q", got)
	}
	if _, err := os.Stat(filepath.Join(repo, "cmd/main.go")); !os.IsNotExist(err) {
		t.Fatal("the old name is still there")
	}
	if _, perr := s.renamePath(c, proto.FSPathParams{Root: repo, Path: "internal", To: "pkg"}); perr != nil {
		t.Fatal(perr)
	}
	if _, err := os.Stat(filepath.Join(repo, "pkg/tui/model.go")); err != nil {
		t.Fatal("the folder did not move whole")
	}
	for name, p := range map[string]proto.FSPathParams{
		"taken":       {Root: repo, Path: "README.md", To: "pkg/tui/model.go"},
		"into itself": {Root: repo, Path: "pkg", To: "pkg/tui/inner"},
		"outside":     {Root: repo, Path: "README.md", To: "../README.md"},
		"from out":    {Root: repo, Path: "../x", To: "x"},
		"git":         {Root: repo, Path: ".git/HEAD", To: "HEAD"},
		"into git":    {Root: repo, Path: "README.md", To: ".git/README.md"},
		"the root":    {Root: repo, Path: ".", To: "x"},
		"no target":   {Root: repo, Path: "README.md"},
		"missing":     {Root: repo, Path: "nothing.txt", To: "x.txt"},
		"elsewhere":   {Root: dir, Path: "repo/README.md", To: "x"},
	} {
		if _, perr := s.renamePath(c, p); perr == nil {
			t.Errorf("%s: renamed", name)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(repo, "README.md")); string(got) != "# hi\n" {
		t.Fatalf("a refused rename changed README.md: %q", got)
	}
}

// A file is copied with its mode; a folder with what is in it, a link in
// it as a link; never over something there.
func TestCopyPath(t *testing.T) {
	a5IsolateEnv(t)
	s, dir := a5Server(t)
	repo := filesRepo(t, s, dir)
	c := &client{}
	filesWrite(t, repo, "run.sh", "#!/bin/sh\n")
	os.Chmod(filepath.Join(repo, "run.sh"), 0o755)
	if _, perr := s.copyPath(c, proto.FSPathParams{Root: repo, Path: "run.sh", To: "run copy.sh"}); perr != nil {
		t.Fatal(perr)
	}
	if info, err := os.Stat(filepath.Join(repo, "run copy.sh")); err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("copy: %v %v", info, err)
	}
	os.Symlink("model.go", filepath.Join(repo, "internal/tui/link.go"))
	if _, perr := s.copyPath(c, proto.FSPathParams{Root: repo, Path: "internal/tui", To: "internal/tui2"}); perr != nil {
		t.Fatal(perr)
	}
	if got, _ := os.ReadFile(filepath.Join(repo, "internal/tui2/model.go")); string(got) != "package tui\n\nvar x = 1\n" {
		t.Fatalf("folder copy: %q", got)
	}
	if target, err := os.Readlink(filepath.Join(repo, "internal/tui2/link.go")); err != nil || target != "model.go" {
		t.Fatalf("link: %q %v", target, err)
	}
	if _, perr := s.copyPath(c, proto.FSPathParams{Root: repo, Path: "run.sh", To: "README.md"}); perr == nil {
		t.Fatal("copied over README.md")
	}
	if _, perr := s.copyPath(c, proto.FSPathParams{Root: repo, Path: "internal", To: "internal/again"}); perr == nil {
		t.Fatal("copied into itself")
	}
}

// A file or a folder is deleted; a link is deleted as itself, even one to
// outside the checkout, and what it points at stays.
func TestDeletePath(t *testing.T) {
	a5IsolateEnv(t)
	s, dir := a5Server(t)
	repo := filesRepo(t, s, dir)
	c := &client{}
	if _, perr := s.deletePath(c, proto.FSPathParams{Root: repo, Path: "README.md"}); perr != nil {
		t.Fatal(perr)
	}
	if _, perr := s.deletePath(c, proto.FSPathParams{Root: repo, Path: "internal"}); perr != nil {
		t.Fatal(perr)
	}
	for _, p := range []string{"README.md", "internal"} {
		if _, err := os.Lstat(filepath.Join(repo, p)); !os.IsNotExist(err) {
			t.Fatalf("%s is still there", p)
		}
	}
	outside := filepath.Join(dir, "precious.txt")
	os.WriteFile(outside, []byte("keep"), 0o644)
	os.Symlink(outside, filepath.Join(repo, "out"))
	if _, perr := s.deletePath(c, proto.FSPathParams{Root: repo, Path: "out"}); perr != nil {
		t.Fatal(perr)
	}
	if got, _ := os.ReadFile(outside); string(got) != "keep" {
		t.Fatal("deleted through a link")
	}
	for name, p := range map[string]string{"root": "", "dot": ".", "git": ".git", "up": "../precious.txt", "missing": "nope"} {
		if _, perr := s.deletePath(c, proto.FSPathParams{Root: repo, Path: p}); perr == nil {
			t.Errorf("%s: deleted", name)
		}
	}
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		t.Fatal(".git went")
	}
}

// fs.mkdir with a Root makes a folder in that checkout, and the folders on
// the way; a name taken, a path outside or over ssh is refused.
func TestMkdirInCheckout(t *testing.T) {
	a5IsolateEnv(t)
	s, dir := a5Server(t)
	repo := filesRepo(t, s, dir)
	c := &client{}
	r, perr := s.mkdirIn(c, proto.FSMkdirParams{Root: repo, Path: "docs/plans/new"})
	if perr != nil || r.Path != filepath.Join(repo, "docs", "plans", "new") {
		t.Fatalf("mkdir: %+v %v", r, perr)
	}
	if info, err := os.Stat(r.Path); err != nil || !info.IsDir() {
		t.Fatal("not made")
	}
	for name, mp := range map[string]proto.FSMkdirParams{
		"taken":   {Root: repo, Path: "docs/plans"},
		"a file":  {Root: repo, Path: "README.md/x"},
		"outside": {Root: repo, Path: "../out"},
		"ssh":     {Root: repo, Path: "x", SSH: "p1"},
	} {
		if _, perr := s.mkdirIn(c, mp); perr == nil {
			t.Errorf("%s: made", name)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "out")); !os.IsNotExist(err) {
		t.Fatal("made outside")
	}
}

// The new methods are dispatched, and fs.mkdir with a Root goes to the
// checkout, never to the home folder an older client meant.
func TestFSOpsDispatch(t *testing.T) {
	a5IsolateEnv(t)
	s, dir := a5Server(t)
	repo := filesRepo(t, s, dir)
	c := &client{}
	call := func(method string, params any) (any, *proto.Error) {
		t.Helper()
		return s.dispatch(c, proto.Message{Method: method, Params: proto.Marshal(params)})
	}
	if _, perr := call(proto.MethodFSMkdir, proto.FSMkdirParams{Root: repo, Path: "made"}); perr != nil {
		t.Fatal(perr)
	}
	if _, err := os.Stat(filepath.Join(repo, "made")); err != nil {
		t.Fatal("fs.mkdir with a root did not make it in the checkout")
	}
	if _, perr := call(proto.MethodFSCopy, proto.FSPathParams{Root: repo, Path: "README.md", To: "R2.md"}); perr != nil {
		t.Fatal(perr)
	}
	if _, perr := call(proto.MethodFSRename, proto.FSPathParams{Root: repo, Path: "R2.md", To: "R3.md"}); perr != nil {
		t.Fatal(perr)
	}
	if _, perr := call(proto.MethodFSDelete, proto.FSPathParams{Root: repo, Path: "R3.md"}); perr != nil {
		t.Fatal(perr)
	}
	out, perr := call(proto.MethodFSSearch, proto.FSSearchParams{Root: repo, FSPattern: proto.FSPattern{Query: "hi"}, Text: true})
	if perr != nil || !strings.Contains(searchPaths(out.(proto.FSSearchResult)), "README.md:1") {
		t.Fatalf("search: %+v %v", out, perr)
	}
	if _, perr := call(proto.MethodFSReplace, proto.FSReplaceParams{Root: repo, FSPattern: proto.FSPattern{Query: "hi"}, Replace: "ho", Files: []proto.FSReplaceFile{{Path: "README.md", Hash: contentHash([]byte("# hi\n"))}}}); perr != nil {
		t.Fatal(perr)
	}
	l, perr := call(proto.MethodFSLines, proto.FSLinesParams{Root: repo, Path: "README.md"})
	if perr != nil || l.(proto.FSLinesResult).Data != "# ho\n" {
		t.Fatalf("lines: %+v %v", l, perr)
	}
	if _, perr := call(proto.MethodFSWriteRange, proto.FSWriteRangeParams{Root: repo, Path: "README.md", Offset: 0, Length: 5, Base: l.(proto.FSLinesResult).Hash, Data: "# hey\n"}); perr != nil {
		t.Fatal(perr)
	}
	if got, _ := os.ReadFile(filepath.Join(repo, "README.md")); string(got) != "# hey\n" {
		t.Fatalf("range: %q", got)
	}
	for _, m := range []string{proto.MethodFSSearch, proto.MethodFSReplace, proto.MethodFSRename, proto.MethodFSCopy, proto.MethodFSDelete, proto.MethodFSLines, proto.MethodFSWriteRange} {
		if !slowMethods[m] {
			t.Errorf("%s runs on the connection's own goroutine", m)
		}
	}
}

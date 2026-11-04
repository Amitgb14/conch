package server

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

// fs.find lists what git would: tracked and untracked files, not what it
// ignores, and not .git; the name containing the letters first.
func TestFindFilesInACheckout(t *testing.T) {
	a5IsolateEnv(t)
	s, dir := a5Server(t)
	repo := filesRepo(t, s, dir)

	got, perr := s.findFiles(proto.FSFindParams{Root: repo, Query: "mod"})
	if perr != nil {
		t.Fatal(perr)
	}
	if !slices.Equal(got.Paths, []string{"internal/tui/model.go"}) || got.Root != repo || got.Truncated {
		t.Fatalf("mod: %+v", got)
	}
	// Untracked and new are found; ignored and git's own are not.
	if got, _ := s.findFiles(proto.FSFindParams{Root: repo, Query: "app"}); !slices.Equal(got.Paths, []string{"internal/tui/app.tsx"}) {
		t.Fatalf("untracked: %+v", got)
	}
	for _, q := range []string{"index.js", "debug", "HEAD"} {
		if got, _ := s.findFiles(proto.FSFindParams{Root: repo, Query: q}); len(got.Paths) != 0 {
			t.Fatalf("%s found: %+v", q, got)
		}
	}
	// A dotfile git does not ignore is a file like any other.
	if got, _ := s.findFiles(proto.FSFindParams{Root: repo, Query: ".env"}); !slices.Equal(got.Paths, []string{".env"}) {
		t.Fatalf(".env: %+v", got)
	}
	// Ranked: the name before the path before scattered letters; case ignored.
	if got, _ := s.findFiles(proto.FSFindParams{Root: repo, Query: "TUI"}); len(got.Paths) != 2 || !strings.HasPrefix(got.Paths[0], "internal/tui/") {
		t.Fatalf("tui: %+v", got)
	}
	if got, _ := s.findFiles(proto.FSFindParams{Root: repo, Query: "cmg"}); !slices.Equal(got.Paths, []string{"cmd/main.go"}) {
		t.Fatalf("scattered: %+v", got)
	}
	// A file deleted in the worktree is gone from the answer.
	os.Remove(filepath.Join(repo, "README.md"))
	if got, _ := s.findFiles(proto.FSFindParams{Root: repo, Query: "readme"}); len(got.Paths) != 0 {
		t.Fatalf("deleted: %+v", got)
	}
	// Nothing asked, nothing given.
	if got, _ := s.findFiles(proto.FSFindParams{Root: repo, Query: "  "}); got.Paths == nil || len(got.Paths) != 0 {
		t.Fatalf("empty query: %+v", got)
	}
}

// Only a project or one of its worktrees, and no further.
func TestFindFilesConfined(t *testing.T) {
	a5IsolateEnv(t)
	s, dir := a5Server(t)
	filesRepo(t, s, dir)
	for _, root := range []string{"", "relative", dir, "/"} {
		if _, perr := s.findFiles(proto.FSFindParams{Root: root, Query: "a"}); perr == nil {
			t.Fatalf("root %q was searched", root)
		}
	}
}

// A folder git does not know is walked, leaving out hidden folders and
// node_modules.
func TestFindFilesInAPlainFolder(t *testing.T) {
	a5IsolateEnv(t)
	s, dir := a5Server(t)
	plain := filepath.Join(dir, "plain")
	for _, f := range []string{"notes/todo.md", "node_modules/x/todo.js", ".cache/todo.txt", "todo.txt"} {
		filesWrite(t, plain, f, "x\n")
	}
	if _, err := s.projects.add(plain, false); err != nil {
		t.Fatal(err)
	}
	got, perr := s.findFiles(proto.FSFindParams{Root: plain, Query: "todo"})
	if perr != nil {
		t.Fatal(perr)
	}
	if !slices.Equal(got.Paths, []string{"todo.txt", "notes/todo.md"}) {
		t.Fatalf("plain: %+v", got)
	}
}

func TestMatchFilesLimitsAndRanks(t *testing.T) {
	files := []string{"b/zz.go", "a/zz.go", "zz/long/path.go", "x.go", "z_z.txt"}
	got, more := matchFiles(files, "zz", 2)
	if !slices.Equal(got, []string{"a/zz.go", "b/zz.go"}) || !more {
		t.Fatalf("limited: %v %v", got, more)
	}
	got, more = matchFiles(files, "zz", 10)
	if !slices.Equal(got, []string{"a/zz.go", "b/zz.go", "zz/long/path.go", "z_z.txt"}) || more {
		t.Fatalf("all: %v %v", got, more)
	}
	if got, _ := matchFiles(nil, "x", 5); len(got) != 0 {
		t.Fatalf("none: %v", got)
	}
	if !inOrder("internal/tui/model.go", "itm") || inOrder("abc", "cb") || !inOrder("é-file", "éf") {
		t.Fatal("inOrder")
	}
}

// The limit is the server's to cap, and a default when none is asked.
func TestFindFilesLimit(t *testing.T) {
	a5IsolateEnv(t)
	s, dir := a5Server(t)
	plain := filepath.Join(dir, "many")
	for i := range proto.FSFindMax + 30 {
		filesWrite(t, plain, filepath.Join("d", "f"+strconv.Itoa(i)), "x")
	}
	if _, err := s.projects.add(plain, false); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.findFiles(proto.FSFindParams{Root: plain, Query: "f"}); len(got.Paths) != proto.FSFindDefault || !got.Truncated {
		t.Fatalf("default: %d %v", len(got.Paths), got.Truncated)
	}
	if got, _ := s.findFiles(proto.FSFindParams{Root: plain, Query: "f", Limit: 10_000}); len(got.Paths) != proto.FSFindMax {
		t.Fatalf("max: %d", len(got.Paths))
	}
}

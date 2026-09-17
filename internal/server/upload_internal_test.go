package server

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

func TestUploadNameEdges(t *testing.T) {
	for in, want := range map[string]string{
		"shot.png":               "shot.png",
		"  spaced.png  ":         "spaced.png",
		"dir/":                   "file",
		"a\\b.png":               "a\\b.png", // a backslash is part of a unix name
		"\x7f\x01":               "file",
		".hidden":                ".hidden",
		strings.Repeat("a", 200): strings.Repeat("a", 200),
		strings.Repeat("a", 201): strings.Repeat("a", 200),
		strings.Repeat("a", 250) + "." + strings.Repeat("x", 30): strings.Repeat("a", 200), // not an extension worth keeping
		strings.Repeat("b", 300) + ".tar.gz":                     strings.Repeat("b", 197) + ".gz",
	} {
		if got := uploadName(in); got != want {
			t.Errorf("uploadName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUploadSweepAndPrune(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.Local)
	u := newUploads(dir)
	u.now = func() time.Time { return now }
	owner := &client{}

	// A missing uploads folder is fine.
	u.prune()
	u.sweep()

	idle, perr := u.chunk(owner, proto.FSUploadParams{Name: "idle.png", Size: 10, Data: []byte("1")})
	if perr != nil {
		t.Fatal(perr)
	}
	now = now.Add(uploadIdle - time.Second)
	busy, _ := u.chunk(owner, proto.FSUploadParams{Name: "busy.png", Size: 10, Data: []byte("1")})
	u.sweep()
	if len(u.active) != 2 {
		t.Fatalf("swept too early: %d active", len(u.active))
	}
	now = now.Add(time.Second)
	u.sweep()
	if _, ok := u.active[idle.Upload]; ok || u.active[busy.Upload] == nil {
		t.Fatalf("sweep: %v", u.active)
	}
	if _, perr := u.chunk(owner, proto.FSUploadParams{Upload: idle.Upload, Data: []byte("2")}); perr == nil || perr.Code != proto.ErrNotFound {
		t.Fatalf("chunk after abandonment: %v", perr)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "uploads", "*", "*", "idle.png.part"))
	if len(matches) != 0 {
		t.Fatalf("abandoned part kept: %v", matches)
	}

	// Pruning keeps the last week, removes older days and ignores other names.
	for _, name := range []string{"2026-09-01", "2026-09-08", "2026-09-09", "2026-09-10", "notes", "2026-13-01"} {
		if err := os.MkdirAll(filepath.Join(dir, "uploads", name, "abcd"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(dir, "uploads", "2026-09-02"), []byte("a file"), 0o600)
	u.prune()
	entries, _ := os.ReadDir(filepath.Join(dir, "uploads"))
	var left []string
	for _, e := range entries {
		left = append(left, e.Name())
	}
	if want := []string{"2026-09-02", "2026-09-09", "2026-09-10", "2026-09-16", "2026-13-01", "notes"}; !slices.Equal(left, want) {
		t.Fatalf("after prune: %v, want %v", left, want)
	}
	if u.active[busy.Upload] == nil {
		t.Fatal("prune dropped today's upload")
	}

	// A closed connection drops only its own uploads.
	other := &client{}
	theirs, _ := u.chunk(other, proto.FSUploadParams{Name: "theirs.png", Size: 2, Data: []byte("1")})
	u.dropClient(owner)
	if u.active[busy.Upload] != nil || u.active[theirs.Upload] == nil {
		t.Fatalf("dropClient: %v", u.active)
	}
}

func TestUploadRunStops(t *testing.T) {
	u := newUploads(t.TempDir())
	quit := make(chan struct{})
	done := make(chan struct{})
	go func() { u.run(quit); close(done) }()
	close(quit)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("run didn't stop")
	}
}

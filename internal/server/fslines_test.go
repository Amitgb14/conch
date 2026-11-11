package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// numbered is n lines "line 1" … "line n", each ending in a newline.
func numbered(from, n int) string {
	var b strings.Builder
	for i := from; i < from+n; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	return b.String()
}

// Lines are read from anywhere in a file — past several of the index's
// marks, on one, from the end — with where they start in it, how many
// the file has, and their fingerprint.
func TestReadLines(t *testing.T) {
	a5IsolateEnv(t)
	s, dir := a5Server(t)
	repo := filesRepo(t, s, dir)
	body := numbered(1, 5000)
	filesWrite(t, repo, "big.log", body)

	for _, first := range []int{1, 2, 1024, 1025, 1026, 3000, 4999} {
		r, perr := s.readLines(proto.FSLinesParams{Root: repo, Path: "big.log", Line: first, Count: 3})
		if perr != nil {
			t.Fatal(perr)
		}
		want := numbered(first, min(3, 5000-first+1))
		if r.Data != want || r.Line != first || r.Lines != 5000 || r.Size != int64(len(body)) {
			t.Fatalf("from %d: %+v", first, r)
		}
		if body[r.Offset:r.Offset+r.Length] != want || r.Hash != contentHash([]byte(want)) {
			t.Fatalf("from %d: offset %d length %d", first, r.Offset, r.Length)
		}
	}
	r, _ := s.readLines(proto.FSLinesParams{Root: repo, Path: "big.log", Count: 2, Tail: true})
	if r.Data != "line 4999\nline 5000\n" || r.Line != 4999 {
		t.Fatalf("tail: %+v", r)
	}
	r, _ = s.readLines(proto.FSLinesParams{Root: repo, Path: "big.log", Line: 6000})
	if r.Data != "" || r.Lines != 5000 || r.Offset != int64(len(body)) {
		t.Fatalf("past the end: %+v", r)
	}
	r, _ = s.readLines(proto.FSLinesParams{Root: repo, Path: "big.log", Count: proto.FSLinesMax + 10})
	if r.Data != numbered(1, proto.FSLinesMax) {
		t.Fatalf("count capped: %d bytes", len(r.Data))
	}

	// The last line without a newline counts; an empty file has none.
	filesWrite(t, repo, "short.txt", "a\nb")
	if r, _ := s.readLines(proto.FSLinesParams{Root: repo, Path: "short.txt", Tail: true, Count: 1}); r.Data != "b" || r.Lines != 2 || r.Line != 2 {
		t.Fatalf("no newline: %+v", r)
	}
	filesWrite(t, repo, "empty.txt", "")
	if r, perr := s.readLines(proto.FSLinesParams{Root: repo, Path: "empty.txt"}); perr != nil || r.Lines != 0 || r.Data != "" {
		t.Fatalf("empty: %+v %v", r, perr)
	}
	for name, lp := range map[string]proto.FSLinesParams{
		"no file": {Root: repo}, "folder": {Root: repo, Path: "internal"}, "missing": {Root: repo, Path: "x"},
		"outside": {Root: repo, Path: "../x"}, "not a checkout": {Root: dir, Path: "repo/README.md"},
	} {
		if _, perr := s.readLines(lp); perr == nil {
			t.Errorf("%s: read", name)
		}
	}
}

// A log that grows is indexed on from where it was; a file rewritten is
// indexed again, so its line numbers are never the old file's.
func TestReadLinesFollowsChanges(t *testing.T) {
	a5IsolateEnv(t)
	s, dir := a5Server(t)
	repo := filesRepo(t, s, dir)
	path := filepath.Join(repo, "app.log")
	filesWrite(t, repo, "app.log", numbered(1, 2000))
	if r, _ := s.readLines(proto.FSLinesParams{Root: repo, Path: "app.log", Tail: true, Count: 1}); r.Data != "line 2000\n" {
		t.Fatalf("first: %+v", r)
	}
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(numbered(2001, 1500))
	f.Close()
	os.Chtimes(path, time.Now().Add(time.Second), time.Now().Add(time.Second))
	r, _ := s.readLines(proto.FSLinesParams{Root: repo, Path: "app.log", Line: 3100, Count: 1})
	if r.Data != "line 3100\n" || r.Lines != 3500 {
		t.Fatalf("appended: %+v", r)
	}
	s.lineIdx.mu.Lock()
	marks := len(s.lineIdx.m[path].marks)
	s.lineIdx.mu.Unlock()
	if marks != 4 { // lines 1, 1025, 2049, 3073
		t.Fatalf("marks after appending: %d", marks)
	}

	// Rewritten longer, with other lines: not taken for an append.
	filesWrite(t, repo, "app.log", strings.Replace(numbered(1, 4000), "line 1\n", "first\nline 1\n", 1))
	os.Chtimes(path, time.Now().Add(2*time.Second), time.Now().Add(2*time.Second))
	r, _ = s.readLines(proto.FSLinesParams{Root: repo, Path: "app.log", Line: 3000, Count: 1})
	if r.Data != "line 2999\n" || r.Lines != 4001 {
		t.Fatalf("rewritten: %+v", r)
	}
}

// A file with a NUL is binary and shows nothing; one that is not UTF-8 is
// shown with replacement characters and can't be saved back; a line too
// long for one answer is cut on a whole character.
func TestReadLinesBinaryLossyLong(t *testing.T) {
	a5IsolateEnv(t)
	s, dir := a5Server(t)
	repo := filesRepo(t, s, dir)
	filesWrite(t, repo, "bin", "a\x00b\n")
	if r, _ := s.readLines(proto.FSLinesParams{Root: repo, Path: "bin"}); !r.Binary || r.Data != "" || r.Hash != "" {
		t.Fatalf("binary: %+v", r)
	}
	filesWrite(t, repo, "latin1.txt", "caf\xe9\n")
	if r, _ := s.readLines(proto.FSLinesParams{Root: repo, Path: "latin1.txt"}); !r.Lossy || r.Data != "caf�\n" || r.Hash != "" || r.Length != 5 {
		t.Fatalf("lossy: %+v", r)
	}
	long := strings.Repeat("é", proto.FSLinesBytesMax) + "\nnext\n"
	filesWrite(t, repo, "long.txt", long)
	r, _ := s.readLines(proto.FSLinesParams{Root: repo, Path: "long.txt"})
	if r.Lossy || r.Hash == "" || r.Length > proto.FSLinesBytesMax || r.Length%2 != 0 || strings.Contains(r.Data, "next") {
		t.Fatalf("long: length %d lossy %v", r.Length, r.Lossy)
	}
}

// A range of lines is saved on its own, the rest of the file kept; one
// that changed since it was read is refused and nothing written; after a
// save the lines are read as they are now.
func TestWriteRange(t *testing.T) {
	a5IsolateEnv(t)
	s, dir := a5Server(t)
	repo := filesRepo(t, s, dir)
	c := &client{}
	body := numbered(1, 3000)
	filesWrite(t, repo, "big.log", body)
	path := filepath.Join(repo, "big.log")
	os.Chmod(path, 0o600)
	r, _ := s.readLines(proto.FSLinesParams{Root: repo, Path: "big.log", Line: 2000, Count: 2})
	w, perr := s.writeRange(c, proto.FSWriteRangeParams{Root: repo, Path: "big.log", Offset: r.Offset, Length: r.Length, Base: r.Hash, Data: "edited\n"})
	if perr != nil {
		t.Fatal(perr)
	}
	want := numbered(1, 1999) + "edited\n" + numbered(2002, 999)
	if got, _ := os.ReadFile(path); string(got) != want {
		t.Fatal("the file is not the edit with the rest kept")
	}
	if w.Size != int64(len(want)) || w.Hash != contentHash([]byte("edited\n")) {
		t.Fatalf("result: %+v", w)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode: %v", info.Mode())
	}
	if r2, _ := s.readLines(proto.FSLinesParams{Root: repo, Path: "big.log", Line: 2000, Count: 1}); r2.Data != "edited\n" || r2.Lines != 2999 {
		t.Fatalf("read after: %+v", r2)
	}
	// The same save again: those bytes are something else now.
	if _, perr := s.writeRange(c, proto.FSWriteRangeParams{Root: repo, Path: "big.log", Offset: r.Offset, Length: r.Length, Base: r.Hash, Data: "x"}); perr == nil || perr.Code != proto.ErrFileChanged {
		t.Fatalf("stale: %v", perr)
	}
	if got, _ := os.ReadFile(path); string(got) != want {
		t.Fatal("a refused save wrote")
	}
	for name, wp := range map[string]proto.FSWriteRangeParams{
		"past the end": {Root: repo, Path: "big.log", Offset: int64(len(want)), Length: 1, Base: "h"},
		"negative":     {Root: repo, Path: "big.log", Offset: -1, Base: "h"},
		"no base":      {Root: repo, Path: "big.log"},
		"too big":      {Root: repo, Path: "big.log", Base: "h", Data: strings.Repeat("x", proto.FSWriteMax+1)},
		"no file":      {Root: repo, Base: "h"},
		"outside":      {Root: repo, Path: "../x", Base: "h"},
		"folder":       {Root: repo, Path: "internal", Base: "h"},
	} {
		if _, perr := s.writeRange(c, wp); perr == nil {
			t.Errorf("%s: written", name)
		}
	}
	// An insertion: a range of nothing.
	if _, perr := s.writeRange(c, proto.FSWriteRangeParams{Root: repo, Path: "big.log", Offset: 0, Length: 0, Base: contentHash(nil), Data: "top\n"}); perr != nil {
		t.Fatal(perr)
	}
	if got, _ := os.ReadFile(path); !strings.HasPrefix(string(got), "top\nline 1\n") {
		t.Fatal("insert at the top")
	}
	ents, _ := os.ReadDir(repo)
	for _, e := range ents {
		if strings.Contains(e.Name(), ".conch-") {
			t.Fatalf("left behind: %s", e.Name())
		}
	}
}

// fs.read with Whole gives a file up to FSWriteMax in one piece, with its
// fingerprint, so it can be edited; without, it stops at FSReadMax.
func TestReadFileWhole(t *testing.T) {
	a5IsolateEnv(t)
	s, dir := a5Server(t)
	repo := filesRepo(t, s, dir)
	body := strings.Repeat("0123456789abcde\n", proto.FSReadMax/8)
	filesWrite(t, repo, "mid.txt", body)
	if r, _ := s.readFile(proto.FSReadParams{Root: repo, Path: "mid.txt"}); !r.Truncated || r.Hash != "" {
		t.Fatal("without Whole: the whole file")
	}
	r, perr := s.readFile(proto.FSReadParams{Root: repo, Path: "mid.txt", Whole: true})
	if perr != nil || r.Truncated || r.Data != body || r.Hash != contentHash([]byte(body)) {
		t.Fatalf("whole: truncated %v len %d %v", r.Truncated, len(r.Data), perr)
	}
	filesWrite(t, repo, "huge.txt", strings.Repeat("x", proto.FSWriteMax+1))
	if r, _ := s.readFile(proto.FSReadParams{Root: repo, Path: "huge.txt", Whole: true}); !r.Truncated || len(r.Data) != proto.FSWriteMax || r.Hash != "" {
		t.Fatalf("past FSWriteMax: truncated %v len %d", r.Truncated, len(r.Data))
	}
}

// Indexes are kept for a few files, the least used let go first.
func TestLineIndexesBounded(t *testing.T) {
	a5IsolateEnv(t)
	s, dir := a5Server(t)
	repo := filesRepo(t, s, dir)
	for i := range lineIndexMax + 3 {
		name := fmt.Sprintf("f%d.txt", i)
		filesWrite(t, repo, name, "a\n")
		if _, perr := s.readLines(proto.FSLinesParams{Root: repo, Path: name}); perr != nil {
			t.Fatal(perr)
		}
	}
	s.lineIdx.mu.Lock()
	n := len(s.lineIdx.m)
	s.lineIdx.mu.Unlock()
	if n != lineIndexMax {
		t.Fatalf("%d indexes kept", n)
	}
	s.lineIdx.drop(repo)
	if len(s.lineIdx.m) != 0 {
		t.Fatal("dropping a folder keeps what is in it")
	}
}

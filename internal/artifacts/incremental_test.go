package artifacts

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// publishLines is one publish of url: the tool call and its result.
func publishLines(id, at, url string) string {
	return `{"type":"assistant","timestamp":"` + at + `","message":{"content":[{"type":"tool_use","id":"` + id + `","name":"Artifact","input":{"title":"T ` + id + `"}}]}}` + "\n" +
		`{"type":"user","timestamp":"` + at + `","message":{"content":[{"type":"tool_result","tool_use_id":"` + id + `","content":"Updated the Artifact at ` + url + `"}]}}` + "\n"
}

// filler is a line of conversation with nothing to do with artifacts, the
// bulk of a real transcript.
var filler = `{"type":"assistant","timestamp":"2026-11-13T07:00:00Z","message":{"content":[{"type":"text","text":"` + strings.Repeat("x", 4000) + `"}]}}` + "\n"

func appendTo(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
	f.Close()
}

// TestReadOnlyWhatWasAppended: a live session's transcript runs to
// hundreds of megabytes and grows with every turn; reading it whole each
// time it grew was most of a listing's cost. What was appended is read,
// and a publish whose call came before the append and whose result after
// it is still found.
func TestReadOnlyWhatWasAppended(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	first := strings.Repeat(filler, 50) + publishLines("a", "2026-11-13T07:00:00Z", "https://claude.ai/artifact/AAAA")
	appendTo(t, path, first)
	c := NewCache()
	if arts, err := c.Read(path); err != nil || len(arts) != 1 {
		t.Fatalf("first read = %+v, %v", arts, err)
	}
	if got := c.read.Load(); got != int64(len(first)) {
		t.Fatalf("first read took %d bytes, want the whole %d", got, len(first))
	}

	// A call now, its result in the next append.
	call, result, _ := strings.Cut(publishLines("b", "2026-11-13T08:00:00Z", "https://claude.ai/artifact/BBBB"), "\n")
	appendTo(t, path, call+"\n")
	if arts, _ := c.Read(path); len(arts) != 1 {
		t.Fatalf("after the call alone: %+v, want still one", arts)
	}
	appendTo(t, path, result)
	arts, err := c.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(arts) != 2 || arts[0].URL != "https://claude.ai/artifact/BBBB" || arts[0].Title != "T b" {
		t.Fatalf("after the result: %+v, want BBBB found with its call's title", arts)
	}
	if got, want := c.read.Load(), int64(len(first)+len(call)+1+len(result)); got != want {
		t.Errorf("read %d bytes in all, want %d: only what was appended each time", got, want)
	}
	// Unchanged: nothing read.
	before := c.read.Load()
	if _, err := c.Read(path); err != nil {
		t.Fatal(err)
	}
	if c.read.Load() != before {
		t.Errorf("an unchanged transcript was read again")
	}
}

// TestHalfWrittenLineReadAgain: a line being written as the transcript is
// read is taken once it is whole, not lost and not counted twice.
func TestHalfWrittenLineReadAgain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	pub := publishLines("a", "2026-11-13T07:00:00Z", "https://claude.ai/artifact/AAAA")
	cut := len(pub) - 30
	appendTo(t, path, pub[:cut])
	c := NewCache()
	if arts, _ := c.Read(path); len(arts) != 0 {
		t.Fatalf("half a result counted: %+v", arts)
	}
	appendTo(t, path, pub[cut:])
	arts, err := c.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(arts) != 1 || arts[0].Versions != 1 {
		t.Fatalf("got %+v, want the publish once it was whole", arts)
	}
	// A whole last line with no newline is taken, and the newline that
	// follows it adds nothing.
	path2 := filepath.Join(t.TempDir(), "s.jsonl")
	appendTo(t, path2, strings.TrimSuffix(pub, "\n"))
	c2 := NewCache()
	if arts, _ := c2.Read(path2); len(arts) != 1 {
		t.Fatalf("whole last line without a newline: %+v", arts)
	}
	appendTo(t, path2, "\n")
	if arts, _ := c2.Read(path2); len(arts) != 1 || arts[0].Versions != 1 {
		t.Fatalf("after its newline: %+v, want one version", arts)
	}
}

// TestRewrittenTranscriptReadWhole: a transcript that shrank, or a new
// file in its place, is read from the start, so what it no longer says
// is gone.
func TestRewrittenTranscriptReadWhole(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	a := publishLines("a", "2026-11-13T07:00:00Z", "https://claude.ai/artifact/AAAA")
	appendTo(t, path, strings.Repeat(filler, 3)+a)
	c := NewCache()
	if arts, _ := c.Read(path); len(arts) != 1 {
		t.Fatalf("first read: %+v", arts)
	}

	// Shorter.
	b := publishLines("b", "2026-11-13T08:00:00Z", "https://claude.ai/artifact/BBBB")
	if err := os.WriteFile(path, []byte(b), 0o600); err != nil {
		t.Fatal(err)
	}
	before := c.read.Load()
	arts, err := c.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(arts) != 1 || arts[0].URL != "https://claude.ai/artifact/BBBB" {
		t.Fatalf("after shrinking: %+v, want BBBB alone", arts)
	}
	if got := c.read.Load() - before; got != int64(len(b)) {
		t.Errorf("read %d bytes, want the whole %d", got, len(b))
	}

	// Replaced by a longer file: another file at the same path.
	cc := publishLines("c", "2026-11-13T09:00:00Z", "https://claude.ai/artifact/CCCC")
	repl := filepath.Join(dir, "new")
	if err := os.WriteFile(repl, []byte(strings.Repeat(filler, 2)+cc), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(repl, path); err != nil {
		t.Fatal(err)
	}
	arts, err = c.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(arts) != 1 || arts[0].URL != "https://claude.ai/artifact/CCCC" {
		t.Fatalf("after replacing: %+v, want CCCC alone", arts)
	}
}

// TestConcurrentReadsShareOne: every window of Conch.app lists the
// artifacts as it connects, and on a cold cache each one read every
// transcript. Callers asking at once wait for the one read.
func TestConcurrentReadsShareOne(t *testing.T) {
	dir := t.TempDir()
	var paths []string
	var total int64
	for i := range 3 {
		p := filepath.Join(dir, fmt.Sprintf("s%d.jsonl", i))
		s := strings.Repeat(filler, 5) + publishLines("x", "2026-11-13T07:00:00Z", fmt.Sprintf("https://claude.ai/artifact/A%d", i))
		appendTo(t, p, s)
		paths = append(paths, p)
		total += int64(len(s))
	}
	const callers = 4
	c := NewCache()
	release := make(chan struct{})
	waiting := make(chan string, 100)
	c.testReading = func(path string) {
		if path == paths[0] {
			<-release
		}
	}
	c.testWaiting = func(path string) { waiting <- path }

	var wg sync.WaitGroup
	results := make([][]Found, callers)
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], _ = c.ReadAll(paths)
		}()
	}
	// Hold the first read until the others are waiting for it, or long
	// enough that they would have been.
	deadline := time.After(2 * time.Second)
	for n := 0; n < callers-1; {
		select {
		case <-waiting:
			n++
		case <-deadline:
			n = callers
		}
	}
	close(release)
	wg.Wait()
	if got := c.read.Load(); got != total {
		t.Errorf("%d callers read %d bytes, want each transcript once (%d)", callers, got, total)
	}
	for i, r := range results {
		if len(r) != 3 {
			t.Errorf("caller %d got %d artifacts, want 3", i, len(r))
		}
	}
}

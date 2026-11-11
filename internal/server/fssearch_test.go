package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

func searchPaths(r proto.FSSearchResult) string {
	var out []string
	for _, f := range r.Files {
		for _, l := range f.Lines {
			out = append(out, fmt.Sprintf("%s:%d", f.Path, l.Line))
		}
	}
	return strings.Join(out, " ")
}

// Text is found in what git lists — not in node_modules or a log it
// ignores, unless asked — with each line's number, where it starts, the
// matches in it, and the file's fingerprint for a replace.
func TestSearchText(t *testing.T) {
	a5IsolateEnv(t)
	s, dir := a5Server(t)
	repo := filesRepo(t, s, dir)
	filesWrite(t, repo, "internal/tui/view.go", "package tui\n\n// Pane is drawn\nfunc pane() {} // pane\r\nvar paned = 1\n")

	r, perr := s.searchFiles(proto.FSSearchParams{Root: repo, FSPattern: proto.FSPattern{Query: "pane"}, Text: true})
	if perr != nil {
		t.Fatal(perr)
	}
	if got := searchPaths(r); got != "internal/tui/view.go:3 internal/tui/view.go:4 internal/tui/view.go:5" {
		t.Fatalf("found: %s", got)
	}
	f := r.Files[0]
	data, _ := os.ReadFile(filepath.Join(repo, "internal/tui/view.go"))
	if f.Hash != contentHash(data) {
		t.Fatal("no fingerprint")
	}
	l := f.Lines[1]
	if l.Text != "func pane() {} // pane" || !reflect.DeepEqual(l.Ranges, [][2]int{{5, 9}, {18, 22}}) || l.Offset != int64(strings.Index(string(data), "func")) {
		t.Fatalf("line: %+v", l)
	}
	if r.Searched == 0 || r.Truncated {
		t.Fatalf("counts: %+v", r)
	}

	// Case, whole words, a regular expression.
	r, _ = s.searchFiles(proto.FSSearchParams{Root: repo, FSPattern: proto.FSPattern{Query: "Pane", Case: true}, Text: true})
	if got := searchPaths(r); got != "internal/tui/view.go:3" {
		t.Fatalf("case: %s", got)
	}
	r, _ = s.searchFiles(proto.FSSearchParams{Root: repo, FSPattern: proto.FSPattern{Query: "pane", Word: true}, Text: true})
	if got := searchPaths(r); got != "internal/tui/view.go:3 internal/tui/view.go:4" {
		t.Fatalf("word: %s", got)
	}
	r, _ = s.searchFiles(proto.FSSearchParams{Root: repo, FSPattern: proto.FSPattern{Query: `^package \w+$`, Regex: true}, Text: true})
	if got := searchPaths(r); got != "cmd/main.go:1 internal/tui/model.go:1 internal/tui/view.go:1" {
		t.Fatalf("regex: %s", got)
	}

	// Ignored files only when asked; .git never.
	r, _ = s.searchFiles(proto.FSSearchParams{Root: repo, FSPattern: proto.FSPattern{Query: "x"}, Text: true})
	if strings.Contains(searchPaths(r), "node_modules") || strings.Contains(searchPaths(r), "debug.log") {
		t.Fatalf("ignored files searched: %s", searchPaths(r))
	}
	r, _ = s.searchFiles(proto.FSSearchParams{Root: repo, FSPattern: proto.FSPattern{Query: "x"}, Text: true, Ignored: true})
	if got := searchPaths(r); !strings.Contains(got, "node_modules/pkg/index.js:1") || !strings.Contains(got, "debug.log:1") || strings.Contains(got, ".git/") {
		t.Fatalf("with ignored: %s", got)
	}
}

// File names are found as fs.find finds them, alone or beside text, and
// Include and Exclude narrow both.
func TestSearchNamesAndGlobs(t *testing.T) {
	a5IsolateEnv(t)
	s, dir := a5Server(t)
	repo := filesRepo(t, s, dir)
	filesWrite(t, repo, "internal/tui/model_test.go", "package tui // model\n")

	r, perr := s.searchFiles(proto.FSSearchParams{Root: repo, FSPattern: proto.FSPattern{Query: "model"}, Names: true})
	if perr != nil {
		t.Fatal(perr)
	}
	if strings.Join(r.Names, " ") != "internal/tui/model.go internal/tui/model_test.go" || len(r.Files) != 0 {
		t.Fatalf("names: %+v", r)
	}
	r, _ = s.searchFiles(proto.FSSearchParams{Root: repo, FSPattern: proto.FSPattern{Query: "model"}, Names: true, Text: true, Exclude: []string{"*_test.go"}})
	if strings.Join(r.Names, " ") != "internal/tui/model.go" || searchPaths(r) != "" {
		t.Fatalf("excluded: %+v", r)
	}
	r, _ = s.searchFiles(proto.FSSearchParams{Root: repo, FSPattern: proto.FSPattern{Query: "package"}, Text: true, Include: []string{"cmd/", " "}})
	if got := searchPaths(r); got != "cmd/main.go:1" {
		t.Fatalf("included: %s", got)
	}

	for _, c := range []struct {
		glob, path string
		want       bool
	}{
		{"*.go", "internal/tui/model.go", true},
		{"*.go", "README.md", false},
		{"node_modules", "web/node_modules/x/y.js", true},
		{"internal/", "internal/tui/model.go", true},
		{"internal/", "cmd/internal.go", false},
		{"/cmd", "cmd/main.go", true},
		{"cmd/**/main.go", "cmd/conch/deep/main.go", true},
		{"cmd/**/main.go", "cmd/main.go", true},
		{"cmd/*.go", "cmd/conch/main.go", false},
		{"model?.go", "x/model2.go", true},
		{"a.b", "axb", false},
	} {
		re, err := globRegexp(c.glob)
		if err != nil {
			t.Fatal(err)
		}
		if got := re.MatchString(c.path); got != c.want {
			t.Errorf("%q on %q: %v", c.glob, c.path, got)
		}
	}
	if _, err := globRegexp(" / "); err == nil {
		t.Fatal("an empty glob")
	}
}

// What a search can't do is said: nothing to look for, a pattern that
// matches every line, a bad expression, not a checkout, neither kind.
func TestSearchRefusals(t *testing.T) {
	a5IsolateEnv(t)
	s, dir := a5Server(t)
	repo := filesRepo(t, s, dir)
	for name, sp := range map[string]proto.FSSearchParams{
		"empty":      {Root: repo, Text: true},
		"everywhere": {Root: repo, FSPattern: proto.FSPattern{Query: "a*", Regex: true}, Text: true},
		"bad regex":  {Root: repo, FSPattern: proto.FSPattern{Query: "(", Regex: true}, Text: true},
		"neither":    {Root: repo, FSPattern: proto.FSPattern{Query: "x"}},
		"elsewhere":  {Root: dir, FSPattern: proto.FSPattern{Query: "x"}, Text: true},
		"bad glob":   {Root: repo, FSPattern: proto.FSPattern{Query: "x"}, Text: true, Include: []string{"/"}},
	} {
		if _, perr := s.searchFiles(sp); perr == nil || perr.Code != proto.ErrBadRequest {
			t.Errorf("%s: %v", name, perr)
		}
	}
	// Names alone with no query finds nothing rather than everything.
	if r, perr := s.searchFiles(proto.FSSearchParams{Root: repo, Names: true}); perr != nil || len(r.Names) != 0 {
		t.Fatalf("no query: %+v %v", r, perr)
	}
}

// Binary files and ones too big to read whole are passed over and
// counted; the limit stops a search and says so.
func TestSearchSkipsAndLimits(t *testing.T) {
	a5IsolateEnv(t)
	s, dir := a5Server(t)
	repo := filesRepo(t, s, dir)
	filesWrite(t, repo, "bin.dat", "needle\x00\x01")
	filesWrite(t, repo, "huge.txt", "needle\n"+strings.Repeat("x", proto.FSSearchFileMax))
	filesWrite(t, repo, "many.txt", strings.Repeat("needle\n", 30))
	r, perr := s.searchFiles(proto.FSSearchParams{Root: repo, FSPattern: proto.FSPattern{Query: "needle"}, Text: true})
	if perr != nil {
		t.Fatal(perr)
	}
	if r.Skipped != 2 || searchPaths(r) == "" || strings.Contains(searchPaths(r), "bin.dat") || strings.Contains(searchPaths(r), "huge") {
		t.Fatalf("skipped: %+v", r)
	}
	r, _ = s.searchFiles(proto.FSSearchParams{Root: repo, FSPattern: proto.FSPattern{Query: "needle"}, Text: true, Limit: 10})
	if !r.Truncated || len(r.Files) != 1 || len(r.Files[0].Lines) != 10 {
		t.Fatalf("limit: %+v", r)
	}
}

// A line too long to send whole comes as the part around its first
// match, cut on whole characters, the ranges moved with it.
func TestSearchLineWindow(t *testing.T) {
	line := strings.Repeat("é", 300) + "needle" + strings.Repeat("ü", 300)
	at := strings.Index(line, "needle")
	l := searchLine(7, 100, line, [][]int{{at, at + 6}})
	if l.Line != 7 || l.Offset != 100 || len(l.Text) > proto.FSSearchLineMax || !strings.Contains(l.Text, "needle") {
		t.Fatalf("window: %+v", l)
	}
	if l.Col != at-80 || l.Text[l.Ranges[0][0]:l.Ranges[0][1]] != "needle" {
		t.Fatalf("ranges: col %d %v", l.Col, l.Ranges)
	}
	if !strings.HasPrefix(line[l.Col:], l.Text) {
		t.Fatal("text is not the line from col")
	}
	short := searchLine(1, 0, "a needle", [][]int{{2, 8}})
	if short.Col != 0 || short.Text != "a needle" {
		t.Fatalf("short: %+v", short)
	}
}

// One big file is searched a piece at a time: each answer says where the
// next goes on from, with the line numbers carried across, until Done.
func TestSearchOneFileInPieces(t *testing.T) {
	a5IsolateEnv(t)
	s, dir := a5Server(t)
	repo := filesRepo(t, s, dir)
	var b strings.Builder
	for i := 1; i <= 3000; i++ {
		if i%100 == 0 {
			fmt.Fprintf(&b, "%d level=error broke\n", i)
		} else {
			fmt.Fprintf(&b, "%d level=info fine\n", i)
		}
	}
	b.WriteString("3001 level=error no newline")
	filesWrite(t, repo, "server.log", b.String())

	sp := proto.FSSearchParams{Root: repo, FSPattern: proto.FSPattern{Query: "level=error"}, Path: "server.log", Limit: 7}
	var lines []int
	for calls := 0; ; calls++ {
		if calls > 20 {
			t.Fatal("never done")
		}
		r, perr := s.searchFiles(sp)
		if perr != nil {
			t.Fatal(perr)
		}
		if r.Size != int64(b.Len()) {
			t.Fatalf("size %d", r.Size)
		}
		for _, f := range r.Files {
			for _, l := range f.Lines {
				if !strings.HasPrefix(l.Text, fmt.Sprint(l.Line)+" ") {
					t.Fatalf("line %d is %q", l.Line, l.Text)
				}
				if !strings.HasPrefix(b.String()[l.Offset:], l.Text) {
					t.Fatalf("offset %d for line %d", l.Offset, l.Line)
				}
				lines = append(lines, l.Line)
			}
		}
		if r.Done {
			break
		}
		sp.Offset, sp.Line = r.Next, r.NextLine
	}
	if len(lines) != 31 || lines[0] != 100 || lines[30] != 3001 {
		t.Fatalf("found %d lines: %v", len(lines), lines)
	}
	for _, bad := range []proto.FSSearchParams{
		{Root: repo, FSPattern: proto.FSPattern{Query: "x"}, Path: "server.log", Offset: int64(b.Len()) + 1},
		{Root: repo, FSPattern: proto.FSPattern{Query: "x"}, Path: "internal"},
		{Root: repo, FSPattern: proto.FSPattern{Query: "x"}, Path: "../outside"},
		{Root: repo, FSPattern: proto.FSPattern{Query: "x"}, Path: "gone.log"},
	} {
		if _, perr := s.searchFiles(bad); perr == nil {
			t.Errorf("%+v: no error", bad)
		}
	}
	// An empty file is done at once.
	filesWrite(t, repo, "empty.log", "")
	if r, perr := s.searchFiles(proto.FSSearchParams{Root: repo, FSPattern: proto.FSPattern{Query: "x"}, Path: "empty.log"}); perr != nil || !r.Done || len(r.Files) != 0 {
		t.Fatalf("empty: %+v %v", r, perr)
	}
}

// fs.search's pattern travels as fields of its own, beside the rest.
func TestSearchParamsWire(t *testing.T) {
	b, _ := json.Marshal(proto.FSSearchParams{Root: "/r", FSPattern: proto.FSPattern{Query: "q", Regex: true}, Text: true})
	if string(b) != `{"root":"/r","query":"q","regex":true,"text":true}` {
		t.Fatalf("wire: %s", b)
	}
}

// What was searched is replaced where the file is still what was searched:
// line endings kept, groups expanded, only the lines asked for; a file an
// agent changed since is skipped and left alone.
func TestReplaceFiles(t *testing.T) {
	a5IsolateEnv(t)
	s, dir := a5Server(t)
	repo := filesRepo(t, s, dir)
	c := &client{}
	filesWrite(t, repo, "a.go", "call(MethodFSRead)\r\nx := MethodFSRead\nMethodFSRead()\n")
	filesWrite(t, repo, "b.go", "MethodFSRead\n")
	filesWrite(t, repo, "c.go", "nothing\n")
	pat := proto.FSPattern{Query: "FSRead", Case: true}
	r, _ := s.searchFiles(proto.FSSearchParams{Root: repo, FSPattern: pat, Text: true})
	hash := map[string]string{}
	for _, f := range r.Files {
		hash[f.Path] = f.Hash
	}
	filesWrite(t, repo, "b.go", "MethodFSRead // agent\n") // changed since

	res, perr := s.replaceFiles(c, proto.FSReplaceParams{Root: repo, FSPattern: pat, Replace: "FSOpen", Files: []proto.FSReplaceFile{
		{Path: "a.go", Hash: hash["a.go"], Lines: []int{1, 3}},
		{Path: "b.go", Hash: hash["b.go"]},
		{Path: "c.go", Hash: contentHash([]byte("nothing\n"))},
		{Path: "../x.go", Hash: "h"},
	}})
	if perr != nil {
		t.Fatal(perr)
	}
	if got, _ := os.ReadFile(filepath.Join(repo, "a.go")); string(got) != "call(MethodFSOpen)\r\nx := MethodFSRead\nMethodFSOpen()\n" {
		t.Fatalf("a.go: %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(repo, "b.go")); string(got) != "MethodFSRead // agent\n" {
		t.Fatalf("b.go written over: %q", got)
	}
	if len(res.Files) != 1 || res.Files[0].Count != 2 || res.Files[0].Hash == hash["a.go"] {
		t.Fatalf("replaced: %+v", res.Files)
	}
	why := map[string]string{}
	for _, sk := range res.Skipped {
		why[sk.Path] = sk.Reason
	}
	if !strings.Contains(why["b.go"], "changed") || why["c.go"] != "nothing matched" || why["../x.go"] == "" {
		t.Fatalf("skipped: %+v", res.Skipped)
	}

	// Groups, with a regular expression; a file deleted meanwhile is said.
	filesWrite(t, repo, "d.go", "foo(1) foo(22)\n")
	data, _ := os.ReadFile(filepath.Join(repo, "d.go"))
	res, _ = s.replaceFiles(c, proto.FSReplaceParams{Root: repo, FSPattern: proto.FSPattern{Query: `foo\((\d+)\)`, Regex: true}, Replace: "bar[$1]", Files: []proto.FSReplaceFile{
		{Path: "d.go", Hash: contentHash(data)}, {Path: "gone.go", Hash: "h"},
	}})
	if got, _ := os.ReadFile(filepath.Join(repo, "d.go")); string(got) != "bar[1] bar[22]\n" {
		t.Fatalf("groups: %q", got)
	}
	if len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0].Reason, "deleted") {
		t.Fatalf("gone: %+v", res.Skipped)
	}
	// Without Regex a $ is a $.
	data, _ = os.ReadFile(filepath.Join(repo, "d.go"))
	s.replaceFiles(c, proto.FSReplaceParams{Root: repo, FSPattern: proto.FSPattern{Query: "bar"}, Replace: "$1", Files: []proto.FSReplaceFile{{Path: "d.go", Hash: contentHash(data)}}})
	if got, _ := os.ReadFile(filepath.Join(repo, "d.go")); string(got) != "$1[1] $1[22]\n" {
		t.Fatalf("literal: %q", got)
	}
	if _, perr := s.replaceFiles(c, proto.FSReplaceParams{Root: repo, FSPattern: pat}); perr == nil {
		t.Fatal("no files")
	}
	if _, perr := s.replaceFiles(c, proto.FSReplaceParams{Root: repo, Files: []proto.FSReplaceFile{{Path: "a.go"}}}); perr == nil {
		t.Fatal("no pattern")
	}
}

// An agent replaces in its own project's checkouts, and nowhere else.
func TestReplaceScoped(t *testing.T) {
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
	rp := proto.FSReplaceParams{Root: other, FSPattern: proto.FSPattern{Query: "theirs"}, Replace: "mine", Files: []proto.FSReplaceFile{{Path: "a.txt", Hash: contentHash([]byte("theirs\n"))}}}
	if _, perr := s.replaceFiles(c, rp); perr == nil || perr.Code != proto.ErrOutOfScope {
		t.Fatalf("another project: %v", perr)
	}
	for name, call := range map[string]func() *proto.Error{
		"rename": func() *proto.Error {
			_, p := s.renamePath(c, proto.FSPathParams{Root: other, Path: "a.txt", To: "b.txt"})
			return p
		},
		"copy": func() *proto.Error {
			_, p := s.copyPath(c, proto.FSPathParams{Root: other, Path: "a.txt", To: "b.txt"})
			return p
		},
		"delete": func() *proto.Error { _, p := s.deletePath(c, proto.FSPathParams{Root: other, Path: "a.txt"}); return p },
		"mkdir":  func() *proto.Error { _, p := s.mkdirIn(c, proto.FSMkdirParams{Root: other, Path: "d"}); return p },
		"range": func() *proto.Error {
			_, p := s.writeRange(c, proto.FSWriteRangeParams{Root: other, Path: "a.txt", Length: 6, Base: contentHash([]byte("theirs")), Data: "mine"})
			return p
		},
	} {
		if perr := call(); perr == nil || perr.Code != proto.ErrOutOfScope {
			t.Errorf("%s in another project: %v", name, perr)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(other, "a.txt")); string(got) != "theirs\n" {
		t.Fatalf("written: %q", got)
	}
	filesWrite(t, work, "mine.txt", "a\n")
	if _, perr := s.renamePath(c, proto.FSPathParams{Root: work, Path: "mine.txt", To: "ours.txt"}); perr != nil {
		t.Fatalf("its own: %v", perr)
	}
}

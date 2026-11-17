package integration

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// conductorFile is import/conductor/integration.toml as the registry
// publishes it (conch-integrations#16).
const conductorFile = `format = 3
kind = "import"
name = "conductor"
label = "Conductor"
docs = "https://www.conductor.build/docs"
adapted_by = "claude"
adapted = "2026-11-16"
note = "Conductor's conversations and workspaces, from its own database on this Mac. Conductor Cloud workspaces must be moved to the Mac in Conductor first."

[source]
platforms = ["darwin"]
store = "sqlite"
path = "~/Library/Application Support/com.conductor.app/conductor.db"
reader = "conductor"          # the reader in conch that understands it
min_reader = 1                # the reader version this file was checked with

[detect]
files = ["~/Library/Application Support/com.conductor.app/conductor.db"]
`

func TestImportFileParses(t *testing.T) {
	f, problems, err := Parse([]byte(conductorFile))
	if err != nil || len(problems) > 0 {
		t.Fatalf("the registry's Conductor file: %v %v", err, problems)
	}
	if f.Kind != KindImport || f.Source.Reader != "conductor" || f.Source.MinReader != 1 || f.Source.Store != "sqlite" {
		t.Fatalf("read as %+v", f)
	}
	if len(f.Hosts()) != 0 || len(f.Operations()) != 0 {
		t.Fatalf("an import reaches nothing: %v %v", f.Hosts(), f.Operations())
	}
	if !Reads(KindImport) || Reads("bogus") {
		t.Fatal("Reads")
	}
}

func TestImportFileProblems(t *testing.T) {
	const path = `path = "~/Library/Application Support/com.conductor.app/conductor.db"`
	for _, tc := range []struct{ name, from, to, want string }{
		{"key_env", `label =`, "key_env = \"X_TOKEN\"\nlabel =", "key_env: an import file reaches no service"},
		{"api", `label =`, "api = \"https://api.example.com\"\nlabel =", "api: an import file reaches no service"},
		{"absolute path", path, `path = "/Users/someone/conductor.db"`, "[source] path"},
		{"dotdot path", path, `path = "~/../etc/passwd"`, "[source] path"},
		{"bare home", path, `path = "~/"`, "[source] path"},
		{"unknown store", `store = "sqlite"`, `store = "mongo"`, `[source] store "mongo"`},
		{"no platforms", `platforms = ["darwin"]`, `platforms = []`, "[source] platforms"},
		{"windows", `platforms = ["darwin"]`, `platforms = ["windows"]`, `[source] platforms "windows"`},
		{"no reader", `reader = "conductor"`, `reader = ""`, "[source] reader"},
		{"unknown reader", `reader = "conductor"`, `reader = "cursor"`, `reader "cursor": this conch has no reader by that name, so it needs a newer conch`},
		{"newer reader", `min_reader = 1`, `min_reader = 2`, `reader "conductor" version 2: this conch has version 1, so it needs a newer conch`},
		{"zero reader", `min_reader = 1`, `min_reader = 0`, "[source] min_reader 0"},
		{"detect outside", `files = ["~/Library`, `files = ["/etc/passwd", "~/Library`, `[detect] files "/etc/passwd"`},
		{"operations", "[detect]", "[send]\ncall = \"POST /x\"\n\n[detect]", "[send]: not an operation of kind import"},
		{"remotes", "files = [", "remotes = [\"github.com\"]\nfiles = [", "[detect] remotes or checks"},
	} {
		src := strings.Replace(conductorFile, tc.from, tc.to, 1)
		if src == conductorFile {
			t.Fatalf("%s: %q is not in the file", tc.name, tc.from)
		}
		_, problems, err := Parse([]byte(src))
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if !strings.Contains(strings.Join(problems, "\n"), tc.want) {
			t.Errorf("%s: want %q in %q", tc.name, tc.want, problems)
		}
	}
}

func TestSourceOnlyForImport(t *testing.T) {
	src := good + "\n[source]\nreader = \"conductor\"\n"
	_, problems, err := Parse([]byte(src))
	if err != nil || !strings.Contains(strings.Join(problems, "\n"), "[source]: only an import file has one") {
		t.Fatalf("%v %v", err, problems)
	}
}

func TestEntryNeedsNewer(t *testing.T) {
	for _, tc := range []struct {
		e    Entry
		want string
	}{
		{Entry{Kind: KindImport, Reader: "conductor", MinReader: 1}, ""},
		{Entry{Kind: KindImport, Reader: "conductor", MinReader: 2}, "version 2"},
		{Entry{Kind: KindImport, Reader: "cursor", MinReader: 1}, `no reader by that name`},
		{Entry{Kind: KindImport}, `reader ""`},
		{Entry{Kind: "calendar"}, `kind "calendar"`},
		{Entry{Kind: KindCI}, ""},
		{Entry{Kind: KindSandbox}, ""},
		{Entry{Kind: KindHost, BuiltIn: true}, ""},
	} {
		got := tc.e.NeedsNewer()
		if tc.want == "" && got != "" || tc.want != "" && (!strings.Contains(got, tc.want) || !strings.HasSuffix(got, "needs a newer conch")) {
			t.Errorf("%+v: %q", tc.e, got)
		}
	}
}

// The issue (#271): the registry lists import/conductor and conch would
// not install it, whatever its reader.
func TestFetchAndInstallImport(t *testing.T) {
	isolate(t)
	fr := newFakeRegistry(t, "")
	r := Registry{Name: "r", URL: fr.srv.URL}
	ctx := context.Background()
	e := fr.publish(t, conductorFile, 1, Hosts{})
	if e.Reader != "conductor" || e.MinReader != 1 {
		t.Fatalf("entry %+v", e)
	}
	data, f, err := FetchFile(ctx, r, e)
	if err != nil {
		t.Fatalf("an import this conch reads: %v", err)
	}
	if err := Install(e, data); err != nil {
		t.Fatal(err)
	}
	all, bad := All()
	if !hasFile(all, "conductor") || len(bad) > 0 {
		t.Fatalf("installed: %v %v", names(all), bad)
	}
	if f.Uses(t.TempDir(), Remote{Host: "github.com", Path: "a/b"}) {
		t.Fatal("an import is about this machine, never a project")
	}

	// A reader this conch lacks, or a newer version of one, says so before
	// anything is fetched; an index that disagrees with its file refuses.
	for name, tc := range map[string]struct {
		change func(*Entry)
		want   string
	}{
		"newer reader":   {func(e *Entry) { e.MinReader = 2 }, "Conductor needs a newer conch"},
		"unknown reader": {func(e *Entry) { e.Reader = "cursor" }, "Conductor needs a newer conch"},
		"index differs":  {func(e *Entry) { e.Reader, e.MinReader = "conductor", 0 }, `not the "conductor" version 0 the index lists`},
	} {
		x := e
		tc.change(&x)
		if _, _, err := FetchFile(ctx, r, x); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestImportFound(t *testing.T) {
	home := t.TempDir()
	f, _, _ := Parse([]byte(conductorFile))
	f.Source.Platforms = []string{runtime.GOOS}
	if got := f.Found(home); len(got) != 0 {
		t.Fatalf("nothing there yet: %v", got)
	}
	db := filepath.Join(home, "Library", "Application Support", "com.conductor.app", "conductor.db")
	if err := os.MkdirAll(filepath.Dir(db), 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(db, nil, 0o600)
	if got := f.Found(home); len(got) != 1 || got[0] != db {
		t.Fatalf("found under home: %v", got)
	}
	if got := f.Found(""); len(got) != 0 {
		t.Fatalf("no home: %v", got)
	}
	f.Source.Platforms = []string{"plan9"}
	if got := f.Found(home); len(got) != 0 {
		t.Fatalf("another platform's app: %v", got)
	}
	// Detected is about a project: an import's home paths are never it,
	// even with a checkout that has the same relative path.
	f.Source.Platforms = []string{runtime.GOOS}
	if d := Detected(home, Remote{}, nil, []*File{f}); len(d) != 0 {
		t.Fatalf("detected in a project: %+v", d)
	}
}

func TestHomePath(t *testing.T) {
	for in, want := range map[string]string{
		"~/a/b":    "/h/a/b",
		"~/":       "",
		"~":        "",
		"/abs":     "",
		"~/../x":   "",
		"~/a/../b": "",
		"a/b":      "",
		"~/a b/c":  "/h/a b/c",
	} {
		if got := HomePath(in, "/h"); got != filepath.FromSlash(want) {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func TestImportCapabilitiesAndDoes(t *testing.T) {
	f, _, _ := Parse([]byte(conductorFile))
	c := FileCapabilities(f)
	if c.Auth != nil || len(c.Needs) != 0 || len(c.Operations) != 0 || c.Follow {
		t.Fatalf("an import has no key and reaches nothing: %+v", c)
	}
	if len(c.Detect) != 1 || c.Detect[0] != "files" {
		t.Fatalf("detect: %v", c.Detect)
	}
	if d := Does(KindImport, nil); len(d) == 0 || d[0].Level != "yes" {
		t.Fatalf("does: %+v", d)
	}
}

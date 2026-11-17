package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// KindImport is another app whose conversations conch brings in. Its file
// reaches no service and runs nothing: it says where the app keeps them on
// this machine ([source]) and which of conch's readers reads that. The
// file cannot add a reader, only name one conch has, the way a ci file's
// operations name conch's contract.
const KindImport = "import"

// ImportSource is an import file's [source]: the platforms the app ran on, how
// it keeps its conversations (store), where (path, under the person's
// home), and the reader in conch that understands it, at least version
// MinReader — the one the file was checked with.
type ImportSource struct {
	Platforms []string `toml:"platforms"`
	Store     string   `toml:"store"`
	Path      string   `toml:"path"`
	Reader    string   `toml:"reader"`
	MinReader int      `toml:"min_reader"`
}

// Readers are the readers this conch has for kind import, by name, and
// each one's version: a reader's version goes up when what it reads or
// writes changes in a way an import file has to ask for.
var Readers = map[string]int{
	"conductor": 1,
}

var (
	importStores    = []string{"sqlite", "jsonl", "json"}
	importPlatforms = []string{"darwin", "linux"}
)

// ReaderProblem says why this conch cannot read with reader at version
// min, in words ending "needs a newer conch"; "" when it can.
func ReaderProblem(reader string, min int) string {
	have, ok := Readers[reader]
	switch {
	case !ok:
		return fmt.Sprintf("reader %q: this conch has no reader by that name, so it needs a newer conch", reader)
	case min > have:
		return fmt.Sprintf("reader %q version %d: this conch has version %d, so it needs a newer conch", reader, min, have)
	}
	return ""
}

// Reads says whether this conch reads integration files of kind.
func Reads(kind string) bool {
	_, ok := contracts[kind]
	return ok || kind == KindImport
}

// NeedsNewer says why this conch cannot install or use e, in words ending
// "needs a newer conch"; "" when it can. A kind it has no code for, or an
// import whose reader it lacks or has an older version of.
func (e Entry) NeedsNewer() string {
	switch {
	case e.BuiltIn || e.Kind == KindSandbox:
		return ""
	case !Reads(e.Kind):
		return fmt.Sprintf("kind %q: this conch does not read it, so it needs a newer conch", e.Kind)
	case e.Kind == KindImport:
		return ReaderProblem(e.Reader, e.MinReader)
	}
	return ""
}

// HomePath is p, a ~/ path, under home; "" for anything else, which an
// import file's paths never are.
func HomePath(p, home string) string {
	rest, ok := strings.CutPrefix(p, "~/")
	if !ok || home == "" || !homeRelative(p) {
		return ""
	}
	return filepath.Join(home, filepath.FromSlash(rest))
}

// homeRelative is a ~/ path that stays under the home: no .., nothing
// after ~/ empty.
func homeRelative(p string) bool {
	rest, ok := strings.CutPrefix(p, "~/")
	if !ok || strings.Trim(rest, "/") == "" {
		return false
	}
	for _, part := range strings.Split(rest, "/") {
		if part == ".." {
			return false
		}
	}
	return true
}

// Found is an import file's [detect] files that are on this machine,
// under home, as the paths they are there: nothing on a platform the app
// never ran on.
func (f *File) Found(home string) []string {
	if f.Kind != KindImport || !f.RunsHere() {
		return nil
	}
	var out []string
	for _, p := range f.Detect.Files {
		if full := HomePath(p, home); full != "" {
			if _, err := os.Stat(full); err == nil {
				out = append(out, full)
			}
		}
	}
	return out
}

// RunsHere says whether the app an import file reads ran on this
// platform.
func (f *File) RunsHere() bool { return slices.Contains(f.Source.Platforms, runtime.GOOS) }

// importProblems is what is wrong with a file of kind import: the source,
// and anything of an integration that reaches a service, which it is not.
func (f *File) importProblems(add func(string, ...any)) {
	none := func(set bool, what string) {
		if set {
			add("%s: an import file reaches no service, so it has none", what)
		}
	}
	none(f.API != "", "api")
	none(f.BaseURL != "", "base_url")
	none(f.KeyEnv != "", "key_env")
	none(f.KeyHelp != "", "key_help")
	none(f.AuthHeader != "" || f.AuthValue != "" || f.Auth.Type != "" || f.Auth.Header != "" || f.Auth.Value != "" ||
		len(f.Auth.Scopes) > 0 || f.Auth.UserEnv != "", "[auth]")
	none(len(f.FollowHosts) > 0, "follow_hosts")
	none(len(f.States) > 0, "[states]")
	none(f.Project.ID != "", "[project]")
	none(len(f.Detect.Remotes)+len(f.Detect.Checks) > 0, "[detect] remotes or checks")
	for _, op := range f.Operations() {
		add("[%s]: not an operation of kind import, which has none", op)
	}
	s := f.Source
	if len(s.Platforms) == 0 {
		add("[source] platforms: where the app ran, from %s", strings.Join(importPlatforms, ", "))
	}
	for _, p := range s.Platforms {
		if !slices.Contains(importPlatforms, p) {
			add("[source] platforms %q: one of %s", p, strings.Join(importPlatforms, ", "))
		}
	}
	if !slices.Contains(importStores, s.Store) {
		add("[source] store %q: one of %s", s.Store, strings.Join(importStores, ", "))
	}
	if !homeRelative(s.Path) {
		add("[source] path %q: a ~/ path in the person's home, without ..", s.Path)
	}
	switch {
	case s.Reader == "":
		add("[source] reader: the reader in conch that understands it")
	case s.MinReader < 1:
		add("[source] min_reader %d: the reader version the file was checked with, from 1", s.MinReader)
	default:
		if why := ReaderProblem(s.Reader, s.MinReader); why != "" {
			add("[source] %s", why)
		}
	}
	for _, p := range f.Detect.Files {
		if !homeRelative(p) {
			add("[detect] files %q: a ~/ path in the person's home, without ..", p)
		}
	}
}

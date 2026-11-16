// Package integration reads integration files: descriptions of a
// service's HTTP API for one kind of job conch already does, so a service
// conch has no code for can still be used. The first kind is CI — where a
// branch's failed run is and what it printed — for the CI services that
// report to a pull request but are not GitHub Actions (CircleCI, GitLab
// CI, …), whose failures conch could see and not read.
//
// It is the sandbox provider file (internal/sandbox/custom.go) grown to a
// second kind, and a description for the same reason: a file runs
// nothing. Its calls go to its own api with the person's key, or — for a
// URL a reply hands back, such as CircleCI's presigned log — to a host
// the file lists, without the key. Everything else is conch's code.
package integration

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/Amitgb14/conch/internal/config"
)

var nameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}$`)
var envRE = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// CheckName says whether name can be an integration's: it becomes a path.
func CheckName(name string) error {
	if !nameRE.MatchString(name) {
		return fmt.Errorf("%q is not an integration name: lowercase letters, digits and -, starting with a letter, 2 to 31 long", name)
	}
	return nil
}

// Dir is where installed integration files are kept.
func Dir() string { return filepath.Join(config.Dir(), "integrations") }

// Parse reads an integration file and lists what is wrong with it —
// everything, since whoever fixes it wants the list.
func Parse(data []byte) (*File, []string, error) {
	if len(data) > 256<<10 {
		return nil, nil, errors.New("over 256 KB: not an integration file")
	}
	var f File
	md, err := toml.Decode(string(data), &f)
	if err != nil {
		return nil, nil, err
	}
	var problems []string
	for _, k := range md.Undecoded() {
		// A body is free-form: what is inside it is the service's.
		if slicesHas(k, "body") {
			continue
		}
		problems = append(problems, fmt.Sprintf("unknown key %s", k))
	}
	problems = append(problems, f.Problems()...)
	return &f, problems, nil
}

// Read reads and checks the file at path.
func Read(path string) (*File, []string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	f, problems, err := Parse(data)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	return f, problems, nil
}

func webURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return false
	}
	return u.Scheme == "https" || u.Scheme == "http" && loopback(u.Hostname())
}

// hostOf is a URL's host, lowercased; "" for none.
func hostOf(s string) string {
	u, err := url.Parse(s)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Shipped reports whether conch carries the file rather than the person
// having installed it.
func (f *File) Shipped() bool { return f.shipped }

// DisplayName is how people write it.
func (f *File) DisplayName() string {
	if f.Label != "" {
		return f.Label
	}
	return f.Name
}

//go:embed files/*.toml
var shippedFS embed.FS

// Guide is the format, as a person or an agent writing a file is told it.
//
//go:embed format.md
var Guide string

// All is every integration this conch can use: the ones it ships, each
// replaced by an installed file of the same name, and the installed ones
// besides, by name. A file that is wrong is left out and said in bad.
func All() (files []*File, bad []string) {
	byName := map[string]*File{}
	ents, _ := fs.ReadDir(shippedFS, "files")
	for _, e := range ents {
		data, _ := shippedFS.ReadFile("files/" + e.Name())
		f, problems, err := Parse(data)
		if err != nil || len(problems) > 0 {
			// A test keeps every shipped file clean; this is a guard.
			bad = append(bad, fmt.Sprintf("%s (shipped): %v%v", e.Name(), err, problems))
			continue
		}
		f.shipped = true
		byName[f.Name] = f
	}
	ents, _ = os.ReadDir(Dir())
	for _, e := range ents {
		name, ok := strings.CutSuffix(e.Name(), ".toml")
		if !ok || e.IsDir() {
			continue
		}
		f, problems, err := Read(filepath.Join(Dir(), e.Name()))
		switch {
		case err != nil:
			bad = append(bad, err.Error())
			continue
		case len(problems) > 0:
			bad = append(bad, fmt.Sprintf("%s: %s", e.Name(), strings.Join(problems, "; ")))
			continue
		case f.Name != name:
			bad = append(bad, fmt.Sprintf("%s names itself %q", e.Name(), f.Name))
			continue
		}
		byName[f.Name] = f
	}
	for _, f := range byName {
		files = append(files, f)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	return files, bad
}

// Remote is a repository's remote, parsed: its host and its path on the
// host (owner/repo, or group/sub/repo).
type Remote struct {
	Host string
	Path string
}

// ParseRemote reads a git remote URL: https://host/owner/repo(.git),
// ssh://git@host[:port]/owner/repo, or git@host:owner/repo.
func ParseRemote(raw string) (Remote, bool) {
	raw = strings.TrimSpace(raw)
	var host, path string
	if u, err := url.Parse(raw); err == nil && u.Scheme != "" && u.Host != "" {
		host, path = u.Hostname(), u.Path
	} else if at, rest, ok := strings.Cut(raw, ":"); ok && !strings.Contains(at, "/") {
		_, host, _ = strings.Cut(at, "@")
		if host == "" {
			host = at
		}
		path = rest
	} else {
		return Remote{}, false
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	if host == "" || !strings.Contains(path, "/") || strings.Contains(path, "..") {
		return Remote{}, false
	}
	return Remote{Host: strings.ToLower(host), Path: path}, true
}

// Uses says whether the project checked out at root, whose remote is rem,
// uses the service.
func (f *File) Uses(root string, rem Remote) bool {
	for _, name := range f.Detect.Files {
		if root != "" {
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(name))); err == nil {
				return true
			}
		}
	}
	for _, h := range f.Detect.Remotes {
		h = strings.ToLower(h)
		if rem.Host == h || strings.HasSuffix(rem.Host, "."+h) {
			return true
		}
	}
	return false
}

// For lists the integrations, of kind, that the project at root uses.
func For(kind, root string, rem Remote) []*File {
	all, _ := All()
	var out []*File
	for _, f := range all {
		if f.Kind == kind && f.Uses(root, rem) {
			out = append(out, f)
		}
	}
	return out
}

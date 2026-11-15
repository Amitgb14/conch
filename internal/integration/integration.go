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

// Format is the newest integration file this conch reads.
const Format = 1

// KindCI is a CI service: its failed runs and their logs.
const KindCI = "ci"

// File is the shape of an integration file.
type File struct {
	Format int    `toml:"format"`
	Kind   string `toml:"kind"`
	Name   string `toml:"name"`
	Label  string `toml:"label"`
	// API is the base every call's path is put after; the key goes only
	// there.
	API    string `toml:"api"`
	KeyEnv string `toml:"key_env"`
	// AuthHeader and AuthValue send the key: "Authorization" and
	// "Bearer {key}" when left out.
	AuthHeader string `toml:"auth_header"`
	AuthValue  string `toml:"auth_value"`
	// Follow are the hosts a URL found in a reply may be fetched from,
	// never with the key.
	Follow    []string `toml:"follow"`
	Docs      string   `toml:"docs"`
	AdaptedBy string   `toml:"adapted_by"`
	Adapted   string   `toml:"adapted"`
	Note      string   `toml:"note"`

	Detect    Detect    `toml:"detect"`
	Project   Project   `toml:"project"`
	FailedLog Operation `toml:"failed_log"`

	// shipped is set for a file conch carries rather than one installed.
	shipped bool
}

// Detect says which projects use the service: a file in the checkout, a
// host in the remote's URL.
type Detect struct {
	Files   []string `toml:"files"`
	Remotes []string `toml:"remotes"`
}

// Project makes the service's name for a project out of its remote:
// {host} {owner} {repo} {path} (owner/repo, or group/sub/repo) and
// {path_escaped} (that, URL-escaped as one segment).
type Project struct {
	ID string `toml:"id"`
}

// Operation is one of the kind's jobs, done as a short chain of calls.
type Operation struct {
	Steps []Step `toml:"steps"`
	// Workflow and URL say which run answered, as templates over the
	// steps' values.
	Workflow string `toml:"workflow"`
	URL      string `toml:"url"`
}

// Step is one call, or one call per item of an earlier step's list.
type Step struct {
	// As names the step's value for those after it: {as.PATH}.
	As string `toml:"as"`
	// Call is "METHOD /path" on api, with the key; Fetch is a URL found
	// in a reply, fetched from a follow host without it. One of them.
	Call  string `toml:"call"`
	Fetch string `toml:"fetch"`
	// Each names an earlier step whose value is a list: the call is made
	// once per item, which is {item}, and the texts are put together.
	Each string `toml:"each"`
	// Pick is the path to the value kept, in a JSON reply; the whole reply
	// when empty. A step that picks nothing ends the operation: there is
	// no such run.
	Pick string `toml:"pick"`
	// Text takes the reply as text — a log. Lines takes a JSON list's
	// items' field at that path as the text, one item a line.
	Text  bool   `toml:"text"`
	Lines string `toml:"lines"`
}

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

// Problems lists what is wrong with the file, or nothing.
func (f *File) Problems() []string {
	var p []string
	add := func(format string, a ...any) { p = append(p, fmt.Sprintf(format, a...)) }
	if f.Format < 0 || f.Format > Format {
		add("format %d: this conch reads up to format %d; update conch to use it", f.Format, Format)
	}
	if f.Kind != KindCI {
		add("kind %q: this conch knows ci", f.Kind)
	}
	if !nameRE.MatchString(f.Name) {
		add("name %q: lowercase letters, digits and -, starting with a letter", f.Name)
	}
	if !webURL(f.API) {
		add("api %q: an https:// URL", f.API)
	}
	if !envRE.MatchString(f.KeyEnv) {
		add("key_env %q: the name of an environment variable, e.g. ACME_TOKEN", f.KeyEnv)
	}
	if f.AuthValue != "" && !strings.Contains(f.AuthValue, "{key}") {
		add("auth_value %q never sends {key}", f.AuthValue)
	}
	for _, h := range f.Follow {
		if h == "" || strings.ContainsAny(h, "/:@ ") {
			add("follow %q: a host name, nothing else", h)
		}
	}
	if len(f.Detect.Files) == 0 && len(f.Detect.Remotes) == 0 {
		add("[detect] files or remotes: how conch knows a project uses it")
	}
	for _, name := range f.Detect.Files {
		if name == "" || filepath.IsAbs(name) || strings.Contains(name, "..") {
			add("[detect] files %q: a path inside the checkout", name)
		}
	}
	if f.Project.ID == "" {
		add("[project] id: the service's name for a project, e.g. gh/{owner}/{repo}")
	}
	f.checkOp("failed_log", f.FailedLog, &p)
	sort.Strings(p)
	return p
}

func (f *File) checkOp(what string, op Operation, p *[]string) {
	add := func(format string, a ...any) { *p = append(*p, fmt.Sprintf(format, a...)) }
	if len(op.Steps) == 0 {
		add("[%s] steps: none", what)
		return
	}
	names := map[string]bool{}
	for i, s := range op.Steps {
		at := fmt.Sprintf("[%s] step %d", what, i+1)
		switch {
		case (s.Call == "") == (s.Fetch == ""):
			add("%s: call or fetch, one of them", at)
		case s.Call != "":
			if err := checkCall(s.Call); err != nil {
				add("%s call: %v", at, err)
			}
		case len(f.Follow) == 0:
			add("%s fetch: no follow hosts it may reach", at)
		}
		if s.Each != "" && !names[s.Each] {
			add("%s each %q: no earlier step is called that", at, s.Each)
		}
		if s.Text && (s.Pick != "" || s.Lines != "") {
			add("%s: text takes the whole reply, so no pick or lines", at)
		}
		if s.As != "" {
			if s.As == "item" || reserved[s.As] || !regexp.MustCompile(`^[a-z_][a-z0-9_]*$`).MatchString(s.As) {
				add("%s as %q: a lowercase name of its own", at, s.As)
			}
			names[s.As] = true
		}
		for _, ref := range placeholders(s.Call + " " + s.Fetch + " " + s.Pick) {
			root, _, _ := strings.Cut(ref, ".")
			if !(reserved[root] || names[root] && root != s.As || root == "item" && s.Each != "") {
				add("%s: {%s} is nothing known before it", at, ref)
			}
		}
	}
	for _, ref := range placeholders(op.Workflow + " " + op.URL) {
		if root, _, _ := strings.Cut(ref, "."); !reserved[root] && !names[root] {
			add("[%s] workflow or url: {%s} is no step's", what, ref)
		}
	}
	if last := op.Steps[len(op.Steps)-1]; !last.Text && last.Lines == "" {
		add("[%s] the last step is the log: text = true, or lines", what)
	}
}

// reserved are the placeholders every call may use.
var reserved = map[string]bool{"project": true, "branch": true, "head": true, "host": true, "owner": true, "repo": true, "path": true, "path_escaped": true}

func checkCall(c string) error {
	method, path, ok := strings.Cut(c, " ")
	switch {
	case !ok:
		return fmt.Errorf("%q: METHOD /path", c)
	case method != "GET" && method != "POST" && method != "PUT":
		return fmt.Errorf("%q: GET, POST or PUT", c)
	case !strings.HasPrefix(path, "/"):
		return fmt.Errorf("%q: the path starts with /", c)
	}
	return nil
}

var placeholderRE = regexp.MustCompile(`\{([a-z_][a-z0-9_]*(?:\.[^{}]*)?)\}`)

func placeholders(s string) []string {
	var out []string
	for _, m := range placeholderRE.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}

func webURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return false
	}
	return u.Scheme == "https" || u.Scheme == "http" && loopback(u.Hostname())
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

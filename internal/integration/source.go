package integration

import (
	"net/url"
	"path"
	"strings"
	"sync"
	"time"
)

// SourceActions is a check GitHub Actions ran, which conch reads through gh.
const SourceActions = "github-actions"

// known are services conch recognises in a check with nothing installed,
// so a red one can say whose it is and what would read it.
var known = []struct {
	name, label string
	hosts       []string // the link's host, or a host under it
	prefixes    []string // the check's name starts with one
	pathHas     string   // the link's path holds it
}{
	{name: "circleci", label: "CircleCI", hosts: []string{"circleci.com"}, prefixes: []string{"ci/circleci"}},
	{name: "gitlab-ci", label: "GitLab CI", hosts: []string{"gitlab.com"}},
	{name: "buildkite", label: "Buildkite", hosts: []string{"buildkite.com"}, prefixes: []string{"buildkite/"}},
	{name: "jenkins", label: "Jenkins", prefixes: []string{"continuous-integration/jenkins"}, pathHas: "/job/"},
	{name: "bitbucket-pipelines", label: "Bitbucket Pipelines", hosts: []string{"bitbucket.org"}},
	{name: "azure-pipelines", label: "Azure Pipelines", hosts: []string{"dev.azure.com", "visualstudio.com"}, prefixes: []string{"Azure Pipelines"}},
	{name: "travis-ci", label: "Travis CI", hosts: []string{"travis-ci.com", "travis-ci.org"}, prefixes: []string{"continuous-integration/travis-ci"}},
}

// Label is how people write a source: an integration's label, a known
// service's, else the name.
func Label(source string, files []*File) string {
	if source == SourceActions {
		return "GitHub Actions"
	}
	for _, f := range files {
		if f.Name == source {
			return f.DisplayName()
		}
	}
	for _, k := range known {
		if k.name == source {
			return k.label
		}
	}
	if b, ok := builtins[source]; ok {
		return b.label
	}
	return source
}

// CheckSource names the service a pull request check came from, by its
// name and link, and says whether conch can read its log: GitHub Actions
// through gh, an integration of files whose file reads a failed log. A
// service conch only recognises is named, unreadable; "" is one it does
// not know.
func CheckSource(name, link string, files []*File) (source string, readable bool) {
	u, _ := url.Parse(link)
	host, p := "", ""
	if u != nil {
		host, p = strings.ToLower(u.Hostname()), u.Path
	}
	if host == "github.com" && strings.Contains(p, "/actions/runs/") {
		return SourceActions, true
	}
	for _, f := range files {
		if f.Kind != KindCI {
			continue
		}
		hit := under(host, site(hostOf(f.API)))
		for _, g := range f.Detect.Checks {
			if ok, _ := path.Match(g, name); ok {
				hit = true
			}
		}
		if hit {
			return f.Name, f.FailedLog.Has()
		}
	}
	for _, k := range known {
		hit := k.pathHas != "" && strings.Contains(p, k.pathHas)
		for _, h := range k.hosts {
			hit = hit || under(host, h)
		}
		for _, pre := range k.prefixes {
			hit = hit || strings.HasPrefix(name, pre)
		}
		if hit {
			return k.name, false
		}
	}
	return "", false
}

// CanRerun is whether conch can run a check of source again: GitHub
// Actions, through gh, or an integration whose file has [rerun].
func CanRerun(source string, files []*File) bool {
	if source == SourceActions {
		return true
	}
	for _, f := range files {
		if f.Name == source && f.Kind == KindCI {
			return f.Rerun.Has()
		}
	}
	return false
}

// site drops an API's own subdomain: a check links to app.circleci.com,
// the API is circleci.com; Buildkite's is api.buildkite.com.
func site(host string) string {
	return strings.TrimPrefix(host, "api.")
}

func under(host, h string) bool {
	return h != "" && host != "" && (host == h || strings.HasSuffix(host, "."+h))
}

// Cached is All, read again at most every few seconds: a project's checks
// are named at every refresh, and the files change only when somebody
// installs one.
func Cached() []*File {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.files == nil || time.Since(cache.at) > cacheFor {
		cache.files, _ = All()
		cache.at = time.Now()
	}
	return cache.files
}

// Forget drops what Cached read, after an install or a removal.
func Forget() {
	cache.mu.Lock()
	cache.files = nil
	cache.mu.Unlock()
}

const cacheFor = 10 * time.Second

var cache struct {
	mu    sync.Mutex
	files []*File
	at    time.Time
}

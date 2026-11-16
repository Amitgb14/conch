package integration

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Detection is a service a project uses, and why conch thinks so.
// Installed is whether something here reads it — a file, or conch's own
// GitHub support; one conch only recognises is not.
type Detection struct {
	Name      string `json:"name"`
	Label     string `json:"label"`
	Kind      string `json:"kind"`
	Why       string `json:"why"`
	Installed bool   `json:"installed,omitempty"`
}

// CheckRef is a pull request check, by its name and link.
type CheckRef struct {
	Name string
	URL  string
}

// markers are files that say a service is used, for services conch
// reads without a file (Actions) or has none for yet.
var markers = []struct{ path, name, kind string }{
	{".github/workflows", SourceActions, KindCI},
	{"bitbucket-pipelines.yml", "bitbucket-pipelines", KindCI},
	{"azure-pipelines.yml", "azure-pipelines", KindCI},
	{".travis.yml", "travis-ci", KindCI},
}

// Detected lists the services the project at root uses: its code host from
// the remote, and every CI its files, remote or pull request checks show
// — all of them, since a project uses every CI it has. Nothing runs: a
// file is looked for with stat, and checks are what the server already
// has.
func Detected(root string, rem Remote, checks []CheckRef, files []*File) []Detection {
	seen := map[string]bool{}
	var out []Detection
	add := func(d Detection) {
		if !seen[d.Name] {
			seen[d.Name] = true
			out = append(out, d)
		}
	}
	if rem.Host == "github.com" {
		add(Detection{Name: "github", Label: "GitHub", Kind: KindHost, Why: "github.com remote", Installed: true})
	}
	exists := func(p string) bool {
		if root == "" {
			return false
		}
		_, err := os.Stat(filepath.Join(root, filepath.FromSlash(p)))
		return err == nil
	}
	for _, f := range files {
		for _, p := range f.Detect.Files {
			if exists(p) {
				add(Detection{Name: f.Name, Label: f.DisplayName(), Kind: f.Kind, Why: p, Installed: true})
			}
		}
		for _, h := range f.Detect.Remotes {
			if under(rem.Host, strings.ToLower(h)) {
				add(Detection{Name: f.Name, Label: f.DisplayName(), Kind: f.Kind, Why: rem.Host + " remote", Installed: true})
			}
		}
	}
	for _, m := range markers {
		if exists(m.path) {
			add(Detection{Name: m.name, Label: Label(m.name, files), Kind: m.kind, Why: m.path, Installed: m.name == SourceActions || hasName(files, m.name)})
		}
	}
	for _, c := range checks {
		if src, _ := CheckSource(c.Name, c.URL, files); src != "" {
			add(Detection{Name: src, Label: Label(src, files), Kind: KindCI, Why: "check " + c.Name,
				Installed: src == SourceActions || hasName(files, src)})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Kind == KindHost && out[j].Kind != KindHost })
	return out
}

func hasName(files []*File, name string) bool {
	for _, f := range files {
		if f.Name == name {
			return true
		}
	}
	return false
}

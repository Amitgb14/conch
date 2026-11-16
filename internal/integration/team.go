package integration

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// TeamFile is where a repository says which integrations it uses, for
// everybody who clones it: names only. A key never goes in it — it is
// committed — and conch never reads one from it.
const TeamFile = ".conch/integrations.toml"

// teamFile is the file's shape. Anything else in it is ignored, and never
// written back.
type teamFile struct {
	Uses []string `toml:"uses"`
}

// builtins are the integrations conch reads without a file, by kind.
var builtins = map[string]struct{ label, kind string }{
	"github":        {"GitHub", KindHost},
	SourceActions:   {"GitHub Actions", KindCI},
	"github-issues": {"GitHub Issues", KindTickets},
	"linear":        {"Linear", KindTickets},
	"jira":          {"Jira", KindTickets},
}

// ReadTeam is the names a checkout's team file lists, sorted and once
// each; nil, nil with no file. A name that cannot be an integration's is
// an error rather than dropped: whoever wrote it wants to know.
func ReadTeam(root string) ([]string, error) {
	if root == "" {
		return nil, nil
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(TeamFile)))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(data) > 64<<10 {
		return nil, fmt.Errorf("%s: over 64 KB", TeamFile)
	}
	var f teamFile
	if _, err := toml.Decode(string(data), &f); err != nil {
		return nil, fmt.Errorf("%s: %v", TeamFile, err)
	}
	return cleanNames(f.Uses)
}

// WriteTeam writes the team file with names and nothing else.
func WriteTeam(root string, names []string) error {
	if root == "" {
		return errors.New("not a checkout")
	}
	names, err := cleanNames(names)
	if err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("# The integrations this repository uses, for conch (https://github.com/Amitgb14/conch).\n")
	b.WriteString("# Names only: every key stays in its owner's Keychain or environment, never here.\n")
	b.WriteString("uses = [")
	for i, n := range names {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%q", n)
	}
	b.WriteString("]\n")
	path := filepath.Join(root, filepath.FromSlash(TeamFile))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// RemoveTeam deletes the team file; none there is not an error.
func RemoveTeam(root string) error {
	if root == "" {
		return errors.New("not a checkout")
	}
	err := os.Remove(filepath.Join(root, filepath.FromSlash(TeamFile)))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func cleanNames(in []string) ([]string, error) {
	var out []string
	for _, n := range in {
		n = strings.TrimSpace(n)
		if err := CheckName(n); err != nil {
			return nil, fmt.Errorf("%s: %v", TeamFile, err)
		}
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out, nil
}

// WithTeam adds to ds what the team file names that nothing else showed,
// each with the file as its reason. A name conch knows neither as a file
// nor as one of its own is left out: there is no kind to show it under.
func WithTeam(ds []Detection, team []string, files []*File) []Detection {
	for _, n := range team {
		if slices.ContainsFunc(ds, func(d Detection) bool { return d.Name == n }) {
			continue
		}
		if b, ok := builtins[n]; ok {
			ds = append(ds, Detection{Name: n, Label: b.label, Kind: b.kind, Why: TeamFile, Installed: true})
			continue
		}
		for _, f := range files {
			if f.Name == n {
				ds = append(ds, Detection{Name: n, Label: f.DisplayName(), Kind: f.Kind, Why: TeamFile, Installed: true})
				break
			}
		}
	}
	return ds
}

// Subset says whether every name in a is in b.
func Subset(a, b []string) bool {
	for _, n := range a {
		if !slices.Contains(b, n) {
			return false
		}
	}
	return true
}

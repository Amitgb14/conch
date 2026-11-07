package config

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// OMZDir finds Oh My Zsh: the ZSH exported in ~/.zshrc, else ~/.oh-my-zsh.
func OMZDir(home string) string {
	dir := filepath.Join(home, ".oh-my-zsh")
	if v := ZshrcVar(home, "ZSH"); v != "" {
		v = strings.ReplaceAll(strings.ReplaceAll(v, "${HOME}", home), "$HOME", home)
		if strings.HasPrefix(v, "~/") {
			v = filepath.Join(home, v[2:])
		}
		dir = v
	}
	return dir
}

var zshrcAssign = regexp.MustCompile(`^\s*(?:export\s+)?([A-Z_]+)=["']?([^"'#\s]*)["']?`)

// ZshrcVar returns the first assignment of name in ~/.zshrc.
func ZshrcVar(home, name string) string {
	f, err := os.Open(filepath.Join(home, ".zshrc"))
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if m := zshrcAssign.FindStringSubmatch(sc.Text()); m != nil && m[1] == name {
			return m[2]
		}
	}
	return ""
}

// OMZThemes lists the Oh My Zsh themes installed under dir — its own and
// those in custom/ — sorted, each once. ok is false when dir is no Oh My
// Zsh at all.
func OMZThemes(dir string) (themes []string, ok bool) {
	if _, err := os.Stat(filepath.Join(dir, "oh-my-zsh.sh")); err != nil {
		return nil, false
	}
	custom := filepath.Join(dir, "custom")
	seen := map[string]bool{}
	for _, pattern := range []string{
		filepath.Join(dir, "themes", "*.zsh-theme"),
		filepath.Join(custom, "themes", "*.zsh-theme"),
		filepath.Join(custom, "*.zsh-theme"),
	} {
		matches, _ := filepath.Glob(pattern)
		for _, m := range matches {
			if name := strings.TrimSuffix(filepath.Base(m), ".zsh-theme"); !seen[name] {
				seen[name] = true
				themes = append(themes, name)
			}
		}
	}
	sort.Strings(themes)
	return themes, true
}

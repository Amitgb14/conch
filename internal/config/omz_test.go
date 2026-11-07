package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The themes Oh My Zsh has, its own and custom ones, each once, sorted.
func TestOMZThemes(t *testing.T) {
	dir := t.TempDir()
	if themes, ok := OMZThemes(dir); ok || themes != nil {
		t.Fatalf("no Oh My Zsh: %v %v", themes, ok)
	}
	if themes, ok := OMZThemes(filepath.Join(dir, "missing")); ok || themes != nil {
		t.Fatalf("a missing folder: %v %v", themes, ok)
	}
	os.WriteFile(filepath.Join(dir, "oh-my-zsh.sh"), nil, 0o644)
	if themes, ok := OMZThemes(dir); !ok || len(themes) != 0 {
		t.Fatalf("installed, no themes: %v %v", themes, ok)
	}
	for _, f := range []string{"themes/robbyrussell.zsh-theme", "themes/agnoster.zsh-theme", "themes/README.md",
		"custom/themes/mine.zsh-theme", "custom/agnoster.zsh-theme", "custom/loose.zsh-theme"} {
		p := filepath.Join(dir, f)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, nil, 0o644)
	}
	themes, ok := OMZThemes(dir)
	if got := strings.Join(themes, ","); !ok || got != "agnoster,loose,mine,robbyrussell" {
		t.Fatalf("themes: %s", got)
	}
}

// Where Oh My Zsh is: ~/.oh-my-zsh, or the ZSH .zshrc exports.
func TestOMZDir(t *testing.T) {
	home := t.TempDir()
	if got := OMZDir(home); got != filepath.Join(home, ".oh-my-zsh") {
		t.Fatalf("no .zshrc: %s", got)
	}
	for zshrc, want := range map[string]string{
		"export ZSH=\"$HOME/.omz\"\n":        filepath.Join(home, ".omz"),
		"ZSH=~/src/omz # mine\n":             filepath.Join(home, "src/omz"),
		"# ZSH=/nope\nexport ZSH=/opt/omz\n": "/opt/omz",
	} {
		os.WriteFile(filepath.Join(home, ".zshrc"), []byte(zshrc), 0o644)
		if got := OMZDir(home); got != want {
			t.Errorf("%q: %s, want %s", zshrc, got, want)
		}
	}
}

package server

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/amitghadge/conch/internal/config"
	"github.com/amitghadge/conch/internal/proto"
)

// omzDir finds Oh My Zsh: the ZSH exported in ~/.zshrc, else ~/.oh-my-zsh.
func omzDir(home string) string {
	dir := filepath.Join(home, ".oh-my-zsh")
	if v := zshrcVar(home, "ZSH"); v != "" {
		v = strings.ReplaceAll(strings.ReplaceAll(v, "${HOME}", home), "$HOME", home)
		if strings.HasPrefix(v, "~/") {
			v = filepath.Join(home, v[2:])
		}
		dir = v
	}
	return dir
}

var zshrcAssign = regexp.MustCompile(`^\s*(?:export\s+)?([A-Z_]+)=["']?([^"'#\s]*)["']?`)

// zshrcVar returns the first assignment of name in ~/.zshrc.
func zshrcVar(home, name string) string {
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

func (s *Server) shellThemes() proto.ShellThemes {
	home, _ := os.UserHomeDir()
	res := proto.ShellThemes{Shell: filepath.Base(config.DefaultShell())}
	dir := omzDir(home)
	if _, err := os.Stat(filepath.Join(dir, "oh-my-zsh.sh")); err != nil {
		return res
	}
	res.OMZ = true
	res.Current = zshrcVar(home, "ZSH_THEME")
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
				res.Themes = append(res.Themes, name)
			}
		}
	}
	sort.Strings(res.Themes)
	return res
}

// zshWrapper is a ZDOTDIR whose startup files load the user's own files
// (from their real ZDOTDIR) and then switch the Oh My Zsh theme for this
// shell only. ZDOTDIR points back at the user's directory once startup is
// over, so shells started from the pane behave normally.
var zshWrapper = map[string]string{
	".zshenv": `_conch_wrap="$ZDOTDIR"; ZDOTDIR="$CONCH_USER_ZDOTDIR"
[[ -f "$ZDOTDIR/.zshenv" ]] && source "$ZDOTDIR/.zshenv"
CONCH_USER_ZDOTDIR="$ZDOTDIR"; ZDOTDIR="$_conch_wrap"
`,
	".zprofile": `_conch_wrap="$ZDOTDIR"; ZDOTDIR="$CONCH_USER_ZDOTDIR"
[[ -f "$ZDOTDIR/.zprofile" ]] && source "$ZDOTDIR/.zprofile"
ZDOTDIR="$_conch_wrap"
`,
	".zshrc": `_conch_wrap="$ZDOTDIR"; ZDOTDIR="$CONCH_USER_ZDOTDIR"
[[ -f "$ZDOTDIR/.zshrc" ]] && source "$ZDOTDIR/.zshrc"
if [[ -n "$CONCH_OMZ_THEME" ]]; then
  if (( $+functions[omz] )); then
    omz theme use "$CONCH_OMZ_THEME" >/dev/null 2>&1
  else
    for _conch_f in "$ZSH_CUSTOM/$CONCH_OMZ_THEME.zsh-theme" "$ZSH_CUSTOM/themes/$CONCH_OMZ_THEME.zsh-theme" "$ZSH/themes/$CONCH_OMZ_THEME.zsh-theme"; do
      [[ -f "$_conch_f" ]] && { source "$_conch_f"; break; }
    done
    unset _conch_f
  fi
fi
ZDOTDIR="$_conch_wrap"
`,
	".zlogin": `ZDOTDIR="$CONCH_USER_ZDOTDIR"
[[ -f "$ZDOTDIR/.zlogin" ]] && source "$ZDOTDIR/.zlogin"
unset _conch_wrap CONCH_USER_ZDOTDIR CONCH_OMZ_THEME
`,
}

// shellThemeEnv returns the environment that applies an Oh My Zsh theme to
// a new zsh pane, or nil when it doesn't apply.
func (s *Server) shellThemeEnv(theme string) []string {
	if theme == "" || strings.ContainsAny(theme, "/\\ \t\n") || filepath.Base(config.DefaultShell()) != "zsh" {
		return nil
	}
	dir := filepath.Join(s.configDir, "zsh")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil
	}
	for name, content := range zshWrapper {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			return nil
		}
	}
	user := os.Getenv("ZDOTDIR")
	if user == "" {
		user, _ = os.UserHomeDir()
	}
	return []string{"ZDOTDIR=" + dir, "CONCH_USER_ZDOTDIR=" + user, "CONCH_OMZ_THEME=" + theme}
}

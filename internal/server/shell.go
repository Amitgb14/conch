package server

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

// omzDir and zshrcVar are config's, kept by these names for this file.
func omzDir(home string) string         { return config.OMZDir(home) }
func zshrcVar(home, name string) string { return config.ZshrcVar(home, name) }

func (s *Server) shellThemes() proto.ShellThemes {
	home, _ := os.UserHomeDir()
	res := proto.ShellThemes{Shell: filepath.Base(config.DefaultShell())}
	dir := omzDir(home)
	themes, ok := config.OMZThemes(dir)
	if !ok {
		return res
	}
	res.OMZ = true
	res.Current = zshrcVar(home, "ZSH_THEME")
	res.Themes = themes
	res.Samples = themeSamples(dir, res.Themes)
	return res
}

// themeSamples is what each theme's prompt looks like: zsh is asked to
// expand it, since a theme is a shell script and nothing else can say what
// it prints. One zsh does the lot, in a folder that is no repository, so a
// git-aware theme shows its plain form rather than this checkout's branch.
//
// Anything that goes wrong — no zsh, a theme that hangs, a shell that
// takes too long — simply leaves the samples out: they are a nicety beside
// the names, which are what a person chooses by.
func themeSamples(omz string, names []string) map[string]string {
	if len(names) == 0 {
		return nil
	}
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		return nil
	}
	dir, err := os.MkdirTemp("", "conch-prompt")
	if err != nil {
		return nil
	}
	defer os.RemoveAll(dir)

	// Each theme in a subshell, so one that dies takes none of the others
	// with it; the name and its prompt on one line, tab separated.
	var b strings.Builder
	b.WriteString("emulate -L zsh\nautoload -Uz colors && colors 2>/dev/null\nZSH=" + shellQuote(omz) + "\n")
	for _, name := range names {
		file := filepath.Join(omz, "themes", name+".zsh-theme")
		if _, err := os.Stat(file); err != nil {
			continue
		}
		// ${(e)…} runs what the prompt substitutes — a theme's git or
		// hostname helper — and ${(%%)…} then expands the prompt escapes
		// that came back, which is how the shell itself draws it.
		b.WriteString("(PROMPT=''; RPROMPT=''; source " + shellQuote(file) + " >/dev/null 2>&1; " +
			"p=\"${(%%)${(e)PROMPT}}\"; print -rn -- " + shellQuote(name) + "$'\\t'; " +
			"print -r -- \"${p//$'\\n'/ }\") 2>/dev/null\n")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, zsh, "-f", "-c", b.String())
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HOME="+dir, "PROMPT_EOL_MARK=")
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil
	}
	samples := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		name, prompt, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		if prompt = strings.TrimSpace(prompt); prompt != "" {
			samples[name] = prompt
		}
	}
	if len(samples) == 0 {
		return nil
	}
	return samples
}

// shellQuote wraps a path for the script above.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

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

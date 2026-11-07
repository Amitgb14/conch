package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// A prompt as Settings shows it under a theme's name: the lines of its
// PROMPT and its RPROMPT, each a run of styled text.
type Prompt struct {
	Theme string   `json:"theme"`
	Lines [][]Span `json:"lines"`
	Right []Span   `json:"right,omitempty"`
}

// Span is text in one style. Colours are #rrggbb, "" for the terminal's
// own.
type Span struct {
	Text string `json:"text"`
	FG   string `json:"fg,omitempty"`
	BG   string `json:"bg,omitempty"`
	Bold bool   `json:"bold,omitempty"`
}

// PromptBG and PromptFG are the terminal a preview is drawn on: the
// palette below is drawn for them.
const (
	PromptBG = "#1d1f21"
	PromptFG = "#c5c8c6"
)

// promptCols is how wide the terminal the theme is told about is: themes
// that fill a line (af-magic's dashes, jonathan's bar) fill this much, and
// one that reads COLUMNS as 0 outside a terminal never finishes.
const promptCols = 64

// promptTimeout bounds one theme: a theme is somebody's zsh, and one
// that waits on something never answers.
var promptTimeout = 3 * time.Second

// omzPromptScript loads what themes lean on — git's prompt functions
// (synchronously: an async prompt answers after the print), the
// *_prompt_info stubs, colours — then the theme, runs its precmd hooks as
// a prompt would, and prints PROMPT, a NUL and RPROMPT as zsh shows them.
// Not oh-my-zsh.sh itself: that runs compinit, plugins and updates.
const omzPromptScript = `
ZSH=$1
setopt prompt_subst
autoload -U colors && colors
autoload -U add-zsh-hook
zstyle ':omz:alpha:lib:git' async-prompt no
for f in git prompt_info_functions spectrum theme-and-appearance; do
  [[ -r $ZSH/lib/$f.zsh ]] && source $ZSH/lib/$f.zsh
done
source $2
for fn in $precmd_functions; do $fn; done
print -rnP -- "$PROMPT"
print -rn -- $'\0'
print -rnP -- "$RPROMPT"
`

// OMZThemeFile is where Oh My Zsh would load theme name from: custom/
// first, as it does, so a theme of your own shadows its namesake.
func OMZThemeFile(dir, name string) (string, error) {
	if name == "" || strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, ".") {
		return "", fmt.Errorf("no theme %q", name)
	}
	for _, p := range []string{
		filepath.Join(dir, "custom", "themes", name+".zsh-theme"),
		filepath.Join(dir, "custom", name+".zsh-theme"),
		filepath.Join(dir, "themes", name+".zsh-theme"),
	} {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("no theme %q in %s", name, dir)
}

// OMZPrompt renders theme's prompt as a new terminal would draw it: zsh
// runs the theme in a home of its own, in a git checkout ~/my-project on
// main with a change, so what a theme says about git shows. Nothing of
// the person's own — their .zshrc, their git config — is read; the theme
// itself is their code, run as opening a terminal runs it.
func OMZPrompt(ctx context.Context, dir, theme string) (Prompt, error) {
	file, err := OMZThemeFile(dir, theme)
	if err != nil {
		return Prompt{}, err
	}
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		return Prompt{}, errors.New("zsh is not installed")
	}
	tmp, err := os.MkdirTemp("", "conch-prompt")
	if err != nil {
		return Prompt{}, err
	}
	defer os.RemoveAll(tmp)
	// The real path, or ~ would not be recognised in a home under /var.
	if real, err := filepath.EvalSymlinks(tmp); err == nil {
		tmp = real
	}
	project := filepath.Join(tmp, "my-project")
	env := promptEnv(tmp)
	sampleRepo(ctx, project, env)

	ctx, cancel := context.WithTimeout(ctx, promptTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, zsh, "-f", "-c", omzPromptScript, "zsh", dir, file)
	cmd.Dir = project
	cmd.Env = env
	cmd.Stdin = nil
	// A theme may start programs of its own; the whole group goes.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	var out bytes.Buffer
	cmd.Stdout = &out
	runErr := cmd.Run()
	if ctx.Err() != nil {
		return Prompt{}, fmt.Errorf("%s took over %s to draw", theme, promptTimeout)
	}
	if out.Len() == 0 && runErr != nil {
		return Prompt{}, fmt.Errorf("%s: %v", theme, runErr)
	}
	left, right, _ := strings.Cut(out.String(), "\x00")
	p := Prompt{Theme: theme, Lines: trimLines(ParseSGRLines(left))}
	if rs := ParseSGRLines(right); len(rs) > 0 {
		p.Right = rs[len(rs)-1]
		if spansText(p.Right) == "" {
			p.Right = nil
		}
	}
	if len(p.Lines) == 0 {
		return Prompt{}, fmt.Errorf("%s draws an empty prompt here", theme)
	}
	return p, nil
}

func promptEnv(home string) []string {
	env := []string{
		"HOME=" + home,
		"PATH=" + os.Getenv("PATH"),
		"TERM=xterm-256color",
		"COLUMNS=" + strconv.Itoa(promptCols),
		"LINES=24",
		"LANG=en_US.UTF-8",
		"LC_ALL=en_US.UTF-8",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + filepath.Join(home, ".gitconfig"),
		"GIT_AUTHOR_NAME=conch", "GIT_AUTHOR_EMAIL=conch@localhost",
		"GIT_COMMITTER_NAME=conch", "GIT_COMMITTER_EMAIL=conch@localhost",
	}
	for _, k := range []string{"USER", "LOGNAME"} {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// sampleRepo makes the checkout a prompt is drawn in. Without git it is a
// folder, and the prompt says nothing about git, which is still true.
func sampleRepo(ctx context.Context, dir string, env []string) {
	_ = os.MkdirAll(dir, 0o755)
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "maintenance.auto=false", "-c", "gc.auto=0", "commit", "-q", "--allow-empty", "-m", "start"},
	} {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir, cmd.Env = dir, env
		if cmd.Run() != nil {
			return
		}
	}
	_ = os.WriteFile(filepath.Join(dir, "README.md"), []byte("changed\n"), 0o644)
}

func spansText(ss []Span) string {
	var b strings.Builder
	for _, s := range ss {
		b.WriteString(s.Text)
	}
	return b.String()
}

// trimLines drops blank lines before and after: several themes start
// with a newline to leave a gap above the prompt.
func trimLines(ls [][]Span) [][]Span {
	for len(ls) > 0 && strings.TrimSpace(spansText(ls[0])) == "" {
		ls = ls[1:]
	}
	for len(ls) > 0 && strings.TrimSpace(spansText(ls[len(ls)-1])) == "" {
		ls = ls[:len(ls)-1]
	}
	return ls
}

// ansi16 is the palette the first sixteen colours are drawn in
// (Tomorrow Night, which Ghostty draws by default).
var ansi16 = [16]string{
	"#1d1f21", "#cc6666", "#b5bd68", "#f0c674", "#81a2be", "#b294bb", "#8abeb7", "#c5c8c6",
	"#666666", "#d54e53", "#b9ca4a", "#e7c547", "#7aa6da", "#c397d8", "#70c0b1", "#eaeaea",
}

// color256 is xterm's colour n as #rrggbb.
func color256(n int) string {
	switch {
	case n < 0 || n > 255:
		return ""
	case n < 16:
		return ansi16[n]
	case n < 232:
		n -= 16
		level := func(v int) int {
			if v == 0 {
				return 0
			}
			return 55 + v*40
		}
		return fmt.Sprintf("#%02x%02x%02x", level(n/36), level(n/6%6), level(n%6))
	default:
		g := 8 + (n-232)*10
		return fmt.Sprintf("#%02x%02x%02x", g, g, g)
	}
}

// ParseSGRLines splits terminal output into lines of styled spans: SGR
// colours and bold are kept, every other escape — a cursor move, a title —
// is dropped, as are carriage returns and other control characters.
func ParseSGRLines(s string) [][]Span {
	var lines [][]Span
	var line []Span
	var cur Span
	var text strings.Builder
	// Line drawing: ESC ( 0 switches G0 to DEC's graphics set, ESC ( B
	// back, as jonathan's bar is drawn.
	graphics := false
	flush := func() {
		if text.Len() > 0 {
			sp := cur
			sp.Text = text.String()
			if n := len(line); n > 0 && line[n-1].FG == sp.FG && line[n-1].BG == sp.BG && line[n-1].Bold == sp.Bold {
				line[n-1].Text += sp.Text
			} else {
				line = append(line, sp)
			}
			text.Reset()
		}
	}
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == 0x1b && i+1 < len(s) && s[i+1] == '[':
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			if j >= len(s) {
				i = len(s)
				continue
			}
			if s[j] == 'm' {
				flush()
				cur = applySGR(cur, s[i+2:j])
			}
			i = j + 1
		case c == 0x1b && i+1 < len(s) && s[i+1] == ']':
			// OSC: to BEL or ST.
			j := i + 2
			for j < len(s) && s[j] != 0x07 && !(s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\') {
				j++
			}
			switch {
			case j >= len(s):
				i = len(s)
			case s[j] == 0x07:
				i = j + 1
			default:
				i = j + 2
			}
		case c == 0x1b && i+2 < len(s) && strings.IndexByte("()*+", s[i+1]) >= 0:
			if s[i+1] == '(' {
				graphics = s[i+2] == '0'
			}
			i += 3
		case c == 0x1b:
			i += 2
		case c == '\n':
			flush()
			lines = append(lines, line)
			line = nil
			i++
		case c == '\t':
			text.WriteString("    ")
			i++
		case c < 0x20 || c == 0x7f:
			i++
		default:
			if r, ok := decGraphics[c]; ok && graphics {
				text.WriteRune(r)
			} else {
				text.WriteByte(c)
			}
			i++
		}
	}
	flush()
	return append(lines, line)
}

// decGraphics is DEC's line-drawing set, by the letter that stands for
// each.
var decGraphics = map[byte]rune{
	'j': '┘', 'k': '┐', 'l': '┌', 'm': '└', 'n': '┼', 'q': '─',
	't': '├', 'u': '┤', 'v': '┴', 'w': '┬', 'x': '│', 'a': '▒', '`': '◆',
}

func applySGR(sp Span, params string) Span {
	if params == "" {
		return Span{}
	}
	ps := strings.FieldsFunc(params, func(r rune) bool { return r == ';' || r == ':' })
	num := func(k int) int {
		if k >= len(ps) {
			return -1
		}
		n, err := strconv.Atoi(ps[k])
		if err != nil {
			return -1
		}
		return n
	}
	for k := 0; k < len(ps); k++ {
		n := num(k)
		switch {
		case n == 0:
			sp = Span{}
		case n == 1:
			sp.Bold = true
		case n == 22:
			sp.Bold = false
		case n >= 30 && n <= 37:
			sp.FG = ansi16[n-30]
		case n >= 90 && n <= 97:
			sp.FG = ansi16[n-90+8]
		case n >= 40 && n <= 47:
			sp.BG = ansi16[n-40]
		case n >= 100 && n <= 107:
			sp.BG = ansi16[n-100+8]
		case n == 39:
			sp.FG = ""
		case n == 49:
			sp.BG = ""
		case n == 38 || n == 48:
			var col string
			switch num(k + 1) {
			case 5:
				col = color256(num(k + 2))
				k += 2
			case 2:
				r, g, b := num(k+2), num(k+3), num(k+4)
				if r >= 0 && g >= 0 && b >= 0 && r < 256 && g < 256 && b < 256 {
					col = fmt.Sprintf("#%02x%02x%02x", r, g, b)
				}
				k += 4
			default:
				k = len(ps)
			}
			if n == 38 {
				sp.FG = col
			} else {
				sp.BG = col
			}
		}
	}
	return sp
}

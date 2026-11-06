package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
	"github.com/charmbracelet/lipgloss"

	"github.com/Amitgb14/conch/internal/config"
)

// Themes of your own: a file each in $CONCH_HOME/themes, NAME.toml, its
// name the theme's (theme = "NAME"). A file starts from a built-in theme
// (based_on, conch's own by default) and sets only the colours it wants
// changed. Conch.app reads the same files, so a theme is one thing in both.
//
//	name = "My Dark"            # what the lists call it
//	based_on = "vscode-dark-modern"
//	bg = "#16161e"
//	accent = "#7aa2f7"
//
// A file that does not read, a colour that is not #rrggbb, a name a
// built-in theme has, or a based_on nobody has is left out and said why.

// maxUserThemes keeps a folder of junk from filling every list.
const maxUserThemes = 64

// userThemeFile is a theme file's keys; unset ones keep based_on's colour.
type userThemeFile struct {
	Name    string `toml:"name"`
	BasedOn string `toml:"based_on"`
	Light   *bool  `toml:"light"` // the app's: drawn as a light window; else from bg
	BG      string `toml:"bg"`
	FG      string `toml:"fg"`
	Accent  string `toml:"accent"`
	SelFG   string `toml:"sel_fg"`
	Border  string `toml:"border"`
	Muted   string `toml:"muted"`
	OK      string `toml:"ok"`
	Warn    string `toml:"warn"`
	Err     string `toml:"err"`
	Work    string `toml:"work"`
	SelDim  string `toml:"sel_dim"`
	Merged  string `toml:"merged"`
}

var (
	hexColor    = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
	themeIDText = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

	userMu       sync.Mutex
	userThemes   []palette
	userProblems []string
)

// UserThemesDir is where conch looks for themes of your own.
func UserThemesDir() string { return filepath.Join(config.Dir(), "themes") }

// loadUserThemes reads the theme files in dir: the themes, and what was
// wrong with the ones left out. A missing folder is no themes and no fault.
func loadUserThemes(dir string) ([]palette, []string) {
	paths, _ := filepath.Glob(filepath.Join(dir, "*.toml"))
	sort.Strings(paths)
	var out []palette
	var problems []string
	seen := map[string]bool{}
	for _, p := range paths {
		id := strings.ToLower(strings.TrimSuffix(filepath.Base(p), ".toml"))
		file := filepath.Base(p)
		switch {
		case !themeIDText.MatchString(id):
			problems = append(problems, fmt.Sprintf("%s: a theme's file name is its name, in lower case letters, digits, - and _", file))
			continue
		case builtinTheme(id):
			problems = append(problems, fmt.Sprintf("%s: %q is a built-in theme's name; call the file something else", file, id))
			continue
		case seen[id]:
			continue
		case len(out) == maxUserThemes:
			problems = append(problems, fmt.Sprintf("%s: more than %d themes of your own; the rest are left out", file, maxUserThemes))
			continue
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", file, err))
			continue
		}
		var f userThemeFile
		if md, err := toml.Decode(string(raw), &f); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", file, err))
			continue
		} else if extra := md.Undecoded(); len(extra) > 0 {
			problems = append(problems, fmt.Sprintf("%s: %q is not a theme key (bg, fg, accent, sel_fg, border, muted, ok, warn, err, work, sel_dim, merged)", file, extra[0].String()))
			continue
		}
		t, err := f.palette(id)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", file, err))
			continue
		}
		seen[id] = true
		out = append(out, t)
	}
	return out, problems
}

// palette is the file's theme: based_on's colours with the file's over them.
func (f userThemeFile) palette(id string) (palette, error) {
	base := "conch"
	if f.BasedOn != "" {
		base = strings.ToLower(strings.TrimSpace(f.BasedOn))
		if !builtinTheme(base) {
			return palette{}, fmt.Errorf("based_on %q is no built-in theme", f.BasedOn)
		}
	}
	t := themeByName(base)
	t.name = id
	t.label = strings.TrimSpace(f.Name)
	if t.label == "" {
		t.label = id
	}
	for _, c := range []struct {
		key, value string
		into       *lipgloss.Color
	}{
		{"bg", f.BG, &t.bg}, {"fg", f.FG, &t.fg}, {"accent", f.Accent, &t.accent}, {"sel_fg", f.SelFG, &t.selFG},
		{"border", f.Border, &t.border}, {"muted", f.Muted, &t.muted}, {"ok", f.OK, &t.ok}, {"warn", f.Warn, &t.warn},
		{"err", f.Err, &t.err}, {"work", f.Work, &t.work}, {"sel_dim", f.SelDim, &t.selDim}, {"merged", f.Merged, &t.merged},
	} {
		v := strings.TrimSpace(c.value)
		if v == "" {
			continue
		}
		if !hexColor.MatchString(v) {
			return palette{}, fmt.Errorf("%s = %q is not a colour; write it as \"#rrggbb\"", c.key, c.value)
		}
		*c.into = lipgloss.Color(strings.ToUpper(v))
	}
	return t, nil
}

func builtinTheme(id string) bool {
	for _, t := range themes {
		if t.name == id {
			return true
		}
	}
	return false
}

// reloadUserThemes reads the folder again, for the lists and the lookups.
func reloadUserThemes() []string {
	ts, problems := loadUserThemes(UserThemesDir())
	userMu.Lock()
	userThemes, userProblems = ts, problems
	userMu.Unlock()
	return problems
}

// allThemes is every theme there is: the built-in ones, then your own.
func allThemes() []palette {
	userMu.Lock()
	defer userMu.Unlock()
	return append(append([]palette{}, themes...), userThemes...)
}

// UserThemeProblems says what was wrong with the theme files left out at
// the last reading, for the status bar and conch doctor.
func UserThemeProblems() []string {
	userMu.Lock()
	defer userMu.Unlock()
	return append([]string(nil), userProblems...)
}

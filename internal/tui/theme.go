package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// palette is a colour scheme. The terminal's own background stays; themes
// colour text, borders and the selection.
type palette struct {
	name   string
	label  string
	accent lipgloss.Color // selection bar, focused borders, dialogs
	selFG  lipgloss.Color // text on the accent
	border lipgloss.Color
	muted  lipgloss.Color
	ok     lipgloss.Color
	warn   lipgloss.Color
	err    lipgloss.Color
	work   lipgloss.Color // working agents, hunk headers, hashes
	selDim lipgloss.Color // selection while focus is elsewhere
	merged lipgloss.Color // merged pull requests
	live   lipgloss.Color // wash behind a file being written; from warn
	// bg and fg are the theme's own background and text. conch never
	// paints them — the terminal's background stays — but the window
	// `conch app` opens is a terminal conch configures, so it is given
	// these (TerminalColors).
	bg, fg lipgloss.Color
}

// themes in the order the settings screen lists them.
var themes = []palette{
	{name: "conch", label: "Conch", accent: "#0F7B8A", selFG: "#FFFFFF", border: "#444C56", muted: "#8B949E",
		ok: "#3FB950", warn: "#D29922", err: "#F85149", work: "#58A6FF", selDim: "#2D333B", merged: "#A371F7", bg: "#15191E", fg: "#C9D1D9"},
	{name: "dracula", label: "Dracula", accent: "#BD93F9", selFG: "#282A36", border: "#6272A4", muted: "#6272A4",
		ok: "#50FA7B", warn: "#F1FA8C", err: "#FF5555", work: "#8BE9FD", selDim: "#44475A", merged: "#FF79C6", bg: "#282A36", fg: "#F8F8F2"},
	{name: "catppuccin", label: "Catppuccin Mocha", accent: "#89B4FA", selFG: "#1E1E2E", border: "#585B70", muted: "#7F849C",
		ok: "#A6E3A1", warn: "#F9E2AF", err: "#F38BA8", work: "#89DCEB", selDim: "#313244", merged: "#CBA6F7", bg: "#1E1E2E", fg: "#CDD6F4"},
	{name: "nord", label: "Nord", accent: "#88C0D0", selFG: "#2E3440", border: "#4C566A", muted: "#7B88A1",
		ok: "#A3BE8C", warn: "#EBCB8B", err: "#BF616A", work: "#81A1C1", selDim: "#3B4252", merged: "#B48EAD", bg: "#2E3440", fg: "#D8DEE9"},
	{name: "gruvbox", label: "Gruvbox Dark", accent: "#FE8019", selFG: "#282828", border: "#504945", muted: "#928374",
		ok: "#B8BB26", warn: "#FABD2F", err: "#FB4934", work: "#83A598", selDim: "#3C3836", merged: "#D3869B", bg: "#282828", fg: "#EBDBB2"},
	{name: "tokyo-night", label: "Tokyo Night", accent: "#7AA2F7", selFG: "#1A1B26", border: "#3B4261", muted: "#737AA2",
		ok: "#9ECE6A", warn: "#E0AF68", err: "#F7768E", work: "#7DCFFF", selDim: "#292E42", merged: "#BB9AF7", bg: "#1A1B26", fg: "#C0CAF5"},
	{name: "one-dark", label: "One Dark", accent: "#61AFEF", selFG: "#282C34", border: "#4B5263", muted: "#7F848E",
		ok: "#98C379", warn: "#E5C07B", err: "#E06C75", work: "#56B6C2", selDim: "#3E4451", merged: "#C678DD", bg: "#282C34", fg: "#ABB2BF"},
	{name: "solarized", label: "Solarized Dark", accent: "#268BD2", selFG: "#FDF6E3", border: "#586E75", muted: "#839496",
		ok: "#859900", warn: "#B58900", err: "#DC322F", work: "#2AA198", selDim: "#073642", merged: "#6C71C4", bg: "#002B36", fg: "#839496"},
	{name: "rose-pine", label: "Rosé Pine", accent: "#C4A7E7", selFG: "#191724", border: "#524F67", muted: "#6E6A86",
		ok: "#9CCFD8", warn: "#F6C177", err: "#EB6F92", work: "#31748F", selDim: "#26233A", merged: "#EBBCBA", bg: "#191724", fg: "#E0DEF4"},
	{name: "kanagawa", label: "Kanagawa", accent: "#7E9CD8", selFG: "#1F1F28", border: "#54546D", muted: "#727169",
		ok: "#98BB6C", warn: "#E6C384", err: "#E82424", work: "#7FB4CA", selDim: "#2D4F67", merged: "#957FB8", bg: "#1F1F28", fg: "#DCD7BA"},
	{name: "everforest", label: "Everforest Dark", accent: "#A7C080", selFG: "#2D353B", border: "#475258", muted: "#859289",
		ok: "#A7C080", warn: "#DBBC7F", err: "#E67E80", work: "#7FBBB3", selDim: "#3D484D", merged: "#D699B6", bg: "#2D353B", fg: "#D3C6AA"},
	{name: "monokai", label: "Monokai", accent: "#A6E22E", selFG: "#272822", border: "#49483E", muted: "#75715E",
		ok: "#A6E22E", warn: "#E6DB74", err: "#F92672", work: "#66D9EF", selDim: "#3E3D32", merged: "#AE81FF", bg: "#272822", fg: "#F8F8F2"},
	{name: "github-dark", label: "GitHub Dark", accent: "#1F6FEB", selFG: "#FFFFFF", border: "#30363D", muted: "#8B949E",
		ok: "#3FB950", warn: "#D29922", err: "#F85149", work: "#79C0FF", selDim: "#21262D", merged: "#A371F7", bg: "#0D1117", fg: "#C9D1D9"},
	{name: "ayu", label: "Ayu Dark", accent: "#E6B450", selFG: "#0B0E14", border: "#3D424D", muted: "#565B66",
		ok: "#AAD94C", warn: "#FFB454", err: "#F07178", work: "#59C2FF", selDim: "#1E232B", merged: "#D2A6FF", bg: "#0B0E14", fg: "#BFBDB6"},
	{name: "night-owl", label: "Night Owl", accent: "#82AAFF", selFG: "#011627", border: "#1D3B53", muted: "#637777",
		ok: "#22DA6E", warn: "#ADDB67", err: "#EF5350", work: "#7FDBCA", selDim: "#1D3B53", merged: "#C792EA", bg: "#011627", fg: "#D6DEEB"},
	// conch's own, drawn for the desktop styles: Firefly is a meadow at
	// night, Cel flat cel-shaded colour. Accent, working, ok and warning
	// differ in each, since a commit graph's first lanes take them in turn.
	{name: "firefly", label: "Firefly", accent: "#9CC77A", selFG: "#1D2333", border: "#3A4560", muted: "#8189A0",
		ok: "#7FD1AE", warn: "#F2D46B", err: "#EF8A73", work: "#8EC5E6", selDim: "#2C3650", merged: "#C3A6E8", bg: "#1D2333", fg: "#E6E1CF"},
	{name: "cel", label: "Cel", accent: "#FF5C8A", selFG: "#1A1C2C", border: "#4A4D78", muted: "#9A9CC4",
		ok: "#7AE582", warn: "#FFD166", err: "#FF6B6B", work: "#4CC9F0", selDim: "#2E3152", merged: "#B892FF", bg: "#1A1C2C", fg: "#F1F0FF"},
	// Light ones, last: for a light terminal, and for Conch.app under the
	// light appearance, which takes the same [ui] theme.
	{name: "github-light", label: "GitHub Light", accent: "#0969DA", selFG: "#FFFFFF", border: "#D0D7DE", muted: "#656D76",
		ok: "#1A7F37", warn: "#9A6700", err: "#CF222E", work: "#0969DA", selDim: "#DDF4FF", merged: "#8250DF", bg: "#FFFFFF", fg: "#1F2328"},
	{name: "solarized-light", label: "Solarized Light", accent: "#268BD2", selFG: "#FDF6E3", border: "#D3CBB7", muted: "#93A1A1",
		ok: "#859900", warn: "#B58900", err: "#DC322F", work: "#2AA198", selDim: "#EEE8D5", merged: "#6C71C4", bg: "#FDF6E3", fg: "#586E75"},
	{name: "catppuccin-latte", label: "Catppuccin Latte", accent: "#1E66F5", selFG: "#EFF1F5", border: "#BCC0CC", muted: "#8C8FA1",
		ok: "#40A02B", warn: "#DF8E1D", err: "#D20F39", work: "#04A5E5", selDim: "#CCD0DA", merged: "#8839EF", bg: "#EFF1F5", fg: "#4C4F69"},
	// Ink is black ink on paper with one spot red; Meadow is sky, grass
	// and sun on warm paper, Firefly's daytime.
	{name: "ink", label: "Ink", accent: "#E0353D", selFG: "#FFFFFF", border: "#111111", muted: "#6B6860",
		ok: "#2F7D32", warn: "#B26A00", err: "#9B1C1C", work: "#1F5FA8", selDim: "#E4E1D8", merged: "#6A3FA0", bg: "#FBFAF6", fg: "#111111"},
	{name: "meadow", label: "Meadow", accent: "#4F8A3C", selFG: "#FFFFFF", border: "#D9CFB4", muted: "#7E8B80",
		ok: "#2E8B6E", warn: "#C9822A", err: "#C15A3F", work: "#3F84AD", selDim: "#E6EDD8", merged: "#8A6BB0", bg: "#F7F1DF", fg: "#34453A"},
	// Terminal is conch's teal on a terminal's ground: ink black, dark
	// hairline borders and terminal state colours, after herdr — the
	// desktop's Terminal style puts it on, with monospace for the window.
	{name: "terminal", label: "Terminal", accent: "#2AA5B5", selFG: "#FFFFFF", border: "#1E232A", muted: "#8B949E",
		ok: "#9ECE6A", warn: "#E0AF68", err: "#F7768E", work: "#7AA2F7", selDim: "#171B20", merged: "#BB9AF7", bg: "#0C0E11", fg: "#D4D9DF"},
	// Terminal Light is the same on paper: off-white, grey hairlines, the
	// teal deepened so a selection reads on white, GitHub-light states.
	{name: "terminal-light", label: "Terminal Light", accent: "#0F7B8A", selFG: "#FFFFFF", border: "#DADDE2", muted: "#6E7781",
		ok: "#1A7F37", warn: "#9A6700", err: "#CF222E", work: "#0969DA", selDim: "#E9EBEE", merged: "#8250DF", bg: "#FBFBFC", fg: "#1F2328"},
}

// accentColors are the named choices for [ui] accent. Each is dark enough
// for white selection text to stay readable.
var accentColors = map[string]lipgloss.Color{
	"teal":   "#0F7B8A",
	"blue":   "#2F6FDB",
	"green":  "#2E7D4F",
	"orange": "#C2571A",
	"pink":   "#C2407D",
	"red":    "#C0392B",
	"gray":   "#5A6472",
	"purple": "#7D56F4",
}

// Colours and styles of the active theme, set by applyTheme.
var (
	colorAccent, colorInput, colorWarn, colorErr, colorMuted, colorBorder lipgloss.Color

	styleMuted, styleBold, styleSel, styleSelDim, styleOK, styleErr lipgloss.Style
	styleHover, styleThumb, styleLink                               lipgloss.Style
	styleTrack, styleBarOK, styleBarWarn, styleBarErr               lipgloss.Style
	styleWarn, styleWork, styleAccent, styleChip, stylePRMerged     lipgloss.Style
	styleLive                                                       lipgloss.Style
	styleTabOn                                                      lipgloss.Style
	tabUnderline                                                    string
	// colorChrome is the band along the top of conch's own window, the
	// shade of a dark title bar so the two read as one; colorWell is the
	// search field set into it, in the window's own background.
	colorChrome, colorWell           lipgloss.Color
	styleDiffAdded, styleDiffRemoved lipgloss.Style
)

func init() { applyTheme("conch", "") }

// TerminalColors are what a terminal conch configures for itself should
// draw with under a theme (and an accent override, as applyTheme takes
// it): its background and text, the cursor in the accent, and a selection
// in the theme's dim selection colour. `conch app` writes them into the
// window it opens, so that window is the theme rather than a terminal's
// stock black with conch's colours on it.
func TerminalColors(theme, accent string) (bg, fg, cursor, selection string) {
	t := themeByName(theme)
	cur := t.accent
	if c, ok := accentColors[strings.ToLower(strings.TrimSpace(accent))]; ok {
		cur = c
	} else if len(accent) == 7 && accent[0] == '#' {
		cur = lipgloss.Color(accent)
	}
	return string(t.bg), string(t.fg), string(cur), string(t.selDim)
}

func themeByName(name string) palette {
	for _, t := range themes {
		if t.name == strings.ToLower(strings.TrimSpace(name)) {
			return t
		}
	}
	return themes[0]
}

// applyTheme switches every colour to theme name, with an optional accent
// override (a name from accentColors or #rrggbb).
func applyTheme(name, accent string) {
	t := themeByName(name)
	if c, ok := accentColors[strings.ToLower(strings.TrimSpace(accent))]; ok {
		t.accent, t.selFG = c, "#FFFFFF"
	} else if len(accent) == 7 && accent[0] == '#' {
		t.accent, t.selFG = lipgloss.Color(accent), "#FFFFFF"
	}
	colorAccent, colorInput, colorWarn, colorErr, colorMuted, colorBorder = t.accent, t.ok, t.warn, t.err, t.muted, t.border

	// A wash for a file an agent is writing: the theme's warn colour taken
	// right down, so it reads as a tint rather than a highlight.
	t.live = darken(t.warn, 0.26)
	styleLive = lipgloss.NewStyle().Background(t.live).Foreground(textOn(t.live))
	styleMuted = lipgloss.NewStyle().Foreground(t.muted)
	// The tab on screen: a cell of the dim selection colour, bold, with
	// the accent drawn under it (tabUnderline) — a tab rather than a
	// selected row, which is what the full accent bar used to make it.
	styleTabOn = lipgloss.NewStyle().Bold(true).Underline(true).Background(t.selDim).Foreground(textOn(t.selDim))
	tabUnderline = underlineColor(t.accent)
	colorChrome, colorWell = chromeOn(t.bg), t.bg
	styleBold = lipgloss.NewStyle().Bold(true)
	styleSel = lipgloss.NewStyle().Bold(true).Foreground(t.selFG).Background(t.accent)
	styleSelDim = lipgloss.NewStyle().Background(t.selDim).Foreground(textOn(t.selDim))
	// Hover is fainter than the dim selection: it says the pointer is here,
	// not that anything is chosen.
	styleHover = lipgloss.NewStyle().Background(darken(t.selDim, 0.5))
	// A link under the pointer: underlined and in the accent, so it reads
	// as the one thing on that screen a click would act on. The program's
	// own colours are the ones it is drawn over, which is why this is a
	// foreground and a line rather than a block of background.
	styleLink = lipgloss.NewStyle().Foreground(t.accent).Underline(true)
	// The scrollbar's thumb: a coloured cell, never a glyph whose width a
	// font may disagree about.
	styleThumb = lipgloss.NewStyle().Background(t.accent)
	// Usage bars, drawn the same way and for the same reason.
	styleTrack = lipgloss.NewStyle().Background(t.selDim)
	styleBarOK = lipgloss.NewStyle().Background(t.ok)
	styleBarWarn = lipgloss.NewStyle().Background(t.warn)
	styleBarErr = lipgloss.NewStyle().Background(t.err)
	styleOK = lipgloss.NewStyle().Foreground(t.ok)
	styleErr = lipgloss.NewStyle().Foreground(t.err)
	styleWarn = lipgloss.NewStyle().Foreground(t.warn).Bold(true)
	styleSearchMatch = lipgloss.NewStyle().Background(t.warn).Foreground(textOn(t.warn))
	// What changed *inside* a changed line: the line's own colour on a
	// washed background, so the eye lands on it without the line ceasing
	// to read as an addition or a removal.
	styleDiffAdded = lipgloss.NewStyle().Foreground(textOn(t.ok)).Background(t.ok)
	styleDiffRemoved = lipgloss.NewStyle().Foreground(textOn(t.err)).Background(t.err)
	styleWork = lipgloss.NewStyle().Foreground(t.work)
	styleAccent = lipgloss.NewStyle().Foreground(t.accent).Bold(true)
	styleChip = lipgloss.NewStyle().Bold(true).Foreground(t.selFG).Padding(0, 1)
	stylePRMerged = lipgloss.NewStyle().Foreground(t.merged)
	applyIconTheme(t)
}

// textOn is a readable text colour for a background. A style that sets only
// a background keeps the terminal's own foreground, which may be nearly the
// same colour — in a light terminal the unfocused selection bar (a dark
// selDim) swallowed the row's name.
func textOn(bg lipgloss.Color) lipgloss.Color {
	if luminance(bg) < 0.45 {
		return "#F0F6FC"
	}
	return "#11151A"
}

// underlineColor is the escape that colours underlines c until the next
// reset, which every styled run ends with. It is written with colons
// (SGR 58:2::r:g:b), the form a terminal that has no underline colour
// throws away whole: with semicolons the same numbers would be read as
// separate attributes there — 2 dim, 5 blink, 7 reverse — by whichever
// value the colour happened to have. No colour at all for a profile
// with none, or for a colour that is not #rrggbb.
func underlineColor(c lipgloss.Color) string {
	s := string(c)
	if lipgloss.ColorProfile() == termenv.Ascii || len(s) != 7 || s[0] != '#' {
		return ""
	}
	v, err := strconv.ParseUint(s[1:], 16, 32)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("\x1b[58:2::%d:%d:%dm", v>>16&0xFF, v>>8&0xFF, v&0xFF)
}

// lighten moves a #rrggbb colour toward white by f. A native macOS title
// bar over a dark window is that window's background lifted by about 5%
// (#15191E under #20242A, measured), so the band conch draws under one
// takes the same step to read as part of it. A colour it cannot parse
// comes back as it was.
func lighten(c lipgloss.Color, f float64) lipgloss.Color {
	s := string(c)
	if len(s) != 7 || s[0] != '#' {
		return c
	}
	v, err := strconv.ParseUint(s[1:], 16, 32)
	if err != nil {
		return c
	}
	up := func(x uint64) int { return int(float64(x) + (255-float64(x))*f + 0.5) }
	return lipgloss.Color(fmt.Sprintf("#%02X%02X%02X", up(v>>16&0xFF), up(v>>8&0xFF), up(v&0xFF)))
}

// chromeOn is the band along the top of conch's own window over a
// background: lifted 5% on a dark one, as a native title bar is, and
// taken 4% down on a light one, where lifting white changes nothing.
func chromeOn(bg lipgloss.Color) lipgloss.Color {
	if luminance(bg) > 0.5 {
		return darken(bg, 0.96)
	}
	return lighten(bg, 0.05)
}

// darken scales a #rrggbb colour toward black by f, for a background tint
// the row's text still reads on. A colour it cannot parse comes back as it
// was, which only costs the tint.
func darken(c lipgloss.Color, f float64) lipgloss.Color {
	s := string(c)
	if len(s) != 7 || s[0] != '#' {
		return c
	}
	v, err := strconv.ParseUint(s[1:], 16, 32)
	if err != nil {
		return c
	}
	r, g, b := float64(v>>16&0xFF)*f, float64(v>>8&0xFF)*f, float64(v&0xFF)*f
	return lipgloss.Color(fmt.Sprintf("#%02X%02X%02X", int(r), int(g), int(b)))
}

// luminance is the relative brightness of a #rrggbb colour, 0 (black) to 1
// (white). An unparseable colour counts as dark, which keeps light text.
func luminance(c lipgloss.Color) float64 {
	s := string(c)
	if len(s) != 7 || s[0] != '#' {
		return 0
	}
	v, err := strconv.ParseUint(s[1:], 16, 32)
	if err != nil {
		return 0
	}
	r, g, b := float64(v>>16&0xFF)/255, float64(v>>8&0xFF)/255, float64(v&0xFF)/255
	return 0.2126*r + 0.7152*g + 0.0722*b
}

// swatch previews a palette as a row of coloured blocks.
func (t palette) swatch() string {
	var b strings.Builder
	for _, c := range []lipgloss.Color{t.accent, t.ok, t.warn, t.err, t.work, t.merged} {
		b.WriteString(lipgloss.NewStyle().Foreground(c).Render("●"))
	}
	return b.String()
}

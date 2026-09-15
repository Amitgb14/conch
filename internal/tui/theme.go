package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
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
}

// themes in the order the settings screen lists them.
var themes = []palette{
	{name: "conch", label: "Conch", accent: "#0F7B8A", selFG: "#FFFFFF", border: "#444C56", muted: "#8B949E",
		ok: "#3FB950", warn: "#D29922", err: "#F85149", work: "#58A6FF", selDim: "#2D333B", merged: "#A371F7"},
	{name: "dracula", label: "Dracula", accent: "#BD93F9", selFG: "#282A36", border: "#6272A4", muted: "#6272A4",
		ok: "#50FA7B", warn: "#F1FA8C", err: "#FF5555", work: "#8BE9FD", selDim: "#44475A", merged: "#FF79C6"},
	{name: "catppuccin", label: "Catppuccin Mocha", accent: "#89B4FA", selFG: "#1E1E2E", border: "#585B70", muted: "#7F849C",
		ok: "#A6E3A1", warn: "#F9E2AF", err: "#F38BA8", work: "#89DCEB", selDim: "#313244", merged: "#CBA6F7"},
	{name: "nord", label: "Nord", accent: "#88C0D0", selFG: "#2E3440", border: "#4C566A", muted: "#7B88A1",
		ok: "#A3BE8C", warn: "#EBCB8B", err: "#BF616A", work: "#81A1C1", selDim: "#3B4252", merged: "#B48EAD"},
	{name: "gruvbox", label: "Gruvbox Dark", accent: "#FE8019", selFG: "#282828", border: "#504945", muted: "#928374",
		ok: "#B8BB26", warn: "#FABD2F", err: "#FB4934", work: "#83A598", selDim: "#3C3836", merged: "#D3869B"},
	{name: "tokyo-night", label: "Tokyo Night", accent: "#7AA2F7", selFG: "#1A1B26", border: "#3B4261", muted: "#737AA2",
		ok: "#9ECE6A", warn: "#E0AF68", err: "#F7768E", work: "#7DCFFF", selDim: "#292E42", merged: "#BB9AF7"},
	{name: "one-dark", label: "One Dark", accent: "#61AFEF", selFG: "#282C34", border: "#4B5263", muted: "#7F848E",
		ok: "#98C379", warn: "#E5C07B", err: "#E06C75", work: "#56B6C2", selDim: "#3E4451", merged: "#C678DD"},
	{name: "solarized", label: "Solarized Dark", accent: "#268BD2", selFG: "#FDF6E3", border: "#586E75", muted: "#839496",
		ok: "#859900", warn: "#B58900", err: "#DC322F", work: "#2AA198", selDim: "#073642", merged: "#6C71C4"},
	{name: "rose-pine", label: "Rosé Pine", accent: "#C4A7E7", selFG: "#191724", border: "#524F67", muted: "#6E6A86",
		ok: "#9CCFD8", warn: "#F6C177", err: "#EB6F92", work: "#31748F", selDim: "#26233A", merged: "#EBBCBA"},
	{name: "kanagawa", label: "Kanagawa", accent: "#7E9CD8", selFG: "#1F1F28", border: "#54546D", muted: "#727169",
		ok: "#98BB6C", warn: "#E6C384", err: "#E82424", work: "#7FB4CA", selDim: "#2D4F67", merged: "#957FB8"},
	{name: "everforest", label: "Everforest Dark", accent: "#A7C080", selFG: "#2D353B", border: "#475258", muted: "#859289",
		ok: "#A7C080", warn: "#DBBC7F", err: "#E67E80", work: "#7FBBB3", selDim: "#3D484D", merged: "#D699B6"},
	{name: "monokai", label: "Monokai", accent: "#A6E22E", selFG: "#272822", border: "#49483E", muted: "#75715E",
		ok: "#A6E22E", warn: "#E6DB74", err: "#F92672", work: "#66D9EF", selDim: "#3E3D32", merged: "#AE81FF"},
	{name: "github-dark", label: "GitHub Dark", accent: "#1F6FEB", selFG: "#FFFFFF", border: "#30363D", muted: "#8B949E",
		ok: "#3FB950", warn: "#D29922", err: "#F85149", work: "#79C0FF", selDim: "#21262D", merged: "#A371F7"},
	{name: "ayu", label: "Ayu Dark", accent: "#E6B450", selFG: "#0B0E14", border: "#3D424D", muted: "#565B66",
		ok: "#AAD94C", warn: "#FFB454", err: "#F07178", work: "#59C2FF", selDim: "#1E232B", merged: "#D2A6FF"},
	{name: "night-owl", label: "Night Owl", accent: "#82AAFF", selFG: "#011627", border: "#1D3B53", muted: "#637777",
		ok: "#22DA6E", warn: "#ADDB67", err: "#EF5350", work: "#7FDBCA", selDim: "#1D3B53", merged: "#C792EA"},
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
	styleWarn, styleWork, styleAccent, styleChip, stylePRMerged     lipgloss.Style
)

func init() { applyTheme("conch", "") }

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

	styleMuted = lipgloss.NewStyle().Foreground(t.muted)
	styleBold = lipgloss.NewStyle().Bold(true)
	styleSel = lipgloss.NewStyle().Bold(true).Foreground(t.selFG).Background(t.accent)
	styleSelDim = lipgloss.NewStyle().Background(t.selDim)
	styleOK = lipgloss.NewStyle().Foreground(t.ok)
	styleErr = lipgloss.NewStyle().Foreground(t.err)
	styleWarn = lipgloss.NewStyle().Foreground(t.warn).Bold(true)
	styleWork = lipgloss.NewStyle().Foreground(t.work)
	styleAccent = lipgloss.NewStyle().Foreground(t.accent).Bold(true)
	styleChip = lipgloss.NewStyle().Bold(true).Foreground(t.selFG).Padding(0, 1)
	stylePRMerged = lipgloss.NewStyle().Foreground(t.merged)
}

// swatch previews a palette as a row of coloured blocks.
func (t palette) swatch() string {
	var b strings.Builder
	for _, c := range []lipgloss.Color{t.accent, t.ok, t.warn, t.err, t.work, t.merged} {
		b.WriteString(lipgloss.NewStyle().Foreground(c).Render("●"))
	}
	return b.String()
}

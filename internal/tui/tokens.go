package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// ---- access tokens ----

// tokensPanel lists the access tokens the person made for agents outside
// conch (#350), from Settings → Web, and revokes them. They are the
// server's, so they are asked for and revoked through it; making one is
// `conch token create`, which prints the secret once where it can be
// copied — a dialog would put it on a screen that is shared and captured.
type tokensPanel struct {
	back    overlay
	tokens  []proto.TokenInfo
	err     string
	loading bool
	sel     int
	scroll  int
	confirm string // the token x was pressed on once: a second x revokes it
}

type tokensLoadedMsg struct {
	tokens []proto.TokenInfo
	err    error
}

type tokenRevokedMsg struct {
	info proto.TokenInfo
	err  error
}

func newTokensPanel(m *Model, back overlay) (*tokensPanel, tea.Cmd) {
	p := &tokensPanel{back: back}
	return p, p.load(m)
}

// tokenClient is this computer's server, if it can hold tokens.
func tokenClient(m *Model) (*client.Client, string) {
	c := m.clientOf(localMachine)
	if c == nil {
		return nil, "conch's server here is not connected"
	}
	if len(c.MissingCapabilities([]string{proto.CapToken})) > 0 {
		return nil, "this computer's conch server predates access tokens: reload it (m on its row)"
	}
	return c, ""
}

func (p *tokensPanel) load(m *Model) tea.Cmd {
	c, why := tokenClient(m)
	if why != "" {
		p.err, p.loading = why, false
		return nil
	}
	p.loading = true
	return func() tea.Msg {
		var list proto.TokenListResult
		err := callCtx(c, proto.MethodTokenList, nil, &list)
		return tokensLoadedMsg{tokens: list.Tokens, err: err}
	}
}

func (p *tokensPanel) revoke(m *Model) tea.Cmd {
	if p.sel >= len(p.tokens) {
		return nil
	}
	tk := p.tokens[p.sel]
	if p.confirm != tk.ID {
		p.confirm = tk.ID
		return nil
	}
	p.confirm = ""
	c, why := tokenClient(m)
	if why != "" {
		m.setFlash(why, true)
		return nil
	}
	return func() tea.Msg {
		var info proto.TokenInfo
		err := callCtx(c, proto.MethodTokenRevoke, proto.TokenRevokeParams{Name: tk.ID}, &info)
		if err == nil && info.Name == "" {
			info = tk
		}
		return tokenRevokedMsg{info: info, err: err}
	}
}

func (p *tokensPanel) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	switch msg := msg.(type) {
	case tokensLoadedMsg:
		p.loading, p.tokens, p.err = false, msg.tokens, ""
		if msg.err != nil {
			p.err = msg.err.Error()
		}
		p.sel = min(p.sel, max(len(p.tokens)-1, 0))
		return false, nil
	case tokenRevokedMsg:
		if msg.err != nil {
			m.setFlash(msg.err.Error(), true)
		} else {
			m.setFlash(fmt.Sprintf("revoked %s (%s): it is refused from now on, also where it is in use", msg.info.Name, msg.info.ID), false)
		}
		return false, p.load(m)
	}
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return false, nil
	}
	key := k.String()
	if key != "x" && key != "delete" {
		p.confirm = ""
	}
	switch key {
	case "up", "k":
		p.sel = max(p.sel-1, 0)
	case "down", "j":
		p.sel = min(p.sel+1, max(len(p.tokens)-1, 0))
	case "x", "delete":
		return false, p.revoke(m)
	case "c":
		return false, copyText(tokenCreateExample)
	case "R":
		return false, p.load(m)
	case "esc", "q", "left", "h":
		m.overlay = p.back
	}
	return false, nil
}

// tokenCreateExample is how one is made, for c to copy.
const tokenCreateExample = "conch token create -level prompt -project PROJECT -expires 30d NAME"

func tokenRow(tk proto.TokenInfo, now time.Time, w int) string {
	nameW := max(w-58, 6)
	name := ansi.Truncate(tk.Name, nameW, "…")
	projects := "all"
	if len(tk.Projects) > 0 {
		var names []string
		for _, p := range tk.Projects {
			names = append(names, p.Name)
		}
		projects = strings.Join(names, ",")
	}
	expires := "never"
	switch {
	case tk.Expires.IsZero():
	case !now.Before(tk.Expires):
		expires = "expired"
	default:
		expires = tk.Expires.Local().Format("2 Jan 2006")
	}
	used := "never"
	if !tk.Used.IsZero() {
		used = tk.Used.Local().Format("Jan 2 15:04")
	}
	return fit(fmt.Sprintf(" %-*s  %-6s  %-14s  %-11s  %s", nameW, name, tk.Level, ansi.Truncate(projects, 14, "…"), expires, used), w)
}

func (p *tokensPanel) render(m Model) box {
	w := min(84, max(m.width-4, 24))
	var lines []string
	add := func(s string) { lines = append(lines, wrap(s, w-2)...) }
	switch {
	case p.err != "":
		add(styleErr.Render("Can't list the tokens: " + p.err))
	case p.loading && len(p.tokens) == 0:
		add(styleMuted.Render("Asking the server…"))
	case len(p.tokens) == 0:
		add("No access tokens. An agent outside conch — an MCP client elsewhere, a script — gets one from conch token create, printed once.")
	default:
		lines = append(lines, styleMuted.Render(fit(fmt.Sprintf(" %-*s  %-6s  %-14s  %-11s  %s", max(w-58, 6), "NAME", "LEVEL", "PROJECTS", "EXPIRES", "USED"), w)))
		room := max(m.height-9, 1)
		if p.sel < p.scroll {
			p.scroll = p.sel
		}
		if p.sel >= p.scroll+room {
			p.scroll = p.sel - room + 1
		}
		now := time.Now()
		for i := p.scroll; i < len(p.tokens) && i < p.scroll+room; i++ {
			row := tokenRow(p.tokens[i], now, w)
			if i == p.sel {
				row = styleSel.Render(row)
			}
			lines = append(lines, row)
		}
	}
	lines = append(lines, "")
	if p.sel < len(p.tokens) && p.confirm == p.tokens[p.sel].ID {
		add(styleWarn.Render(fmt.Sprintf("x again revokes %s: it is refused at once, also where it is in use", p.tokens[p.sel].Name)))
	} else {
		add(styleMuted.Render("Make one: " + tokenCreateExample))
	}
	add(styleMuted.Render("x revoke · c copy the command · R refresh · ↑↓ · esc back"))
	b := box{lines: frameLines(" Web · access tokens ", lines, w, colorAccent)}
	b.x = max((m.width-b.width())/2, 0)
	b.y = max((m.height-len(b.lines))/3, 0)
	return b
}

// mouse: a click on a token selects it; a click outside goes back; the
// wheel moves the selection.
func (p *tokensPanel) mouse(m *Model, msg tea.MouseMsg, b box) tea.Cmd {
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		p.sel = max(p.sel-1, 0)
		return nil
	case tea.MouseButtonWheelDown:
		p.sel = min(p.sel+1, max(len(p.tokens)-1, 0))
		return nil
	}
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return nil
	}
	if !b.contains(msg.X, msg.Y) {
		m.overlay = p.back
		return nil
	}
	// Row 1 is the column titles, under the frame's top line.
	if i := p.scroll + msg.Y - b.y - 2; msg.Y-b.y >= 2 && i < len(p.tokens) && i < p.scroll+max(m.height-9, 1) {
		p.sel, p.confirm = i, ""
	}
	return nil
}

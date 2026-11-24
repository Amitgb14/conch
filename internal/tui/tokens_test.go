package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/proto"
)

func tokensModel(t *testing.T, caps ...string) (*Model, *a1Peer) {
	t.Helper()
	m := a2Model()
	c, peer := a1FakeClient(t, caps...)
	m.machines[0].c = c
	return m, peer
}

// runTokenCmd runs a command the panel returned and hands its message
// back to the panel, as the program would.
func runTokenCmd(t *testing.T, m *Model, p *tokensPanel, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("no command")
	}
	p.update(m, cmd())
}

func openTokens(t *testing.T, m *Model) (*tokensPanel, tea.Cmd) {
	t.Helper()
	s := &settings{}
	s.setTab(webTab)
	m.overlay = s
	for _, it := range s.items(m) {
		if strings.HasPrefix(it.label, "Access tokens") {
			if !it.page {
				t.Error("the row does not say it opens a page")
			}
			cmd := it.run(m)
			p, ok := m.overlay.(*tokensPanel)
			if !ok {
				t.Fatalf("overlay %T", m.overlay)
			}
			return p, cmd
		}
	}
	t.Fatal("no Access tokens row on the Web tab")
	return nil, nil
}

// Settings → Web → Access tokens: every token the person made, revoked
// with a second x, and back to the settings.
func TestTokensPanel(t *testing.T) {
	m, peer := tokensModel(t, proto.CapToken)
	now := time.Now()
	peer.setResult(proto.MethodTokenList, proto.TokenListResult{Tokens: []proto.TokenInfo{
		{ID: "aaaa1111", Name: "reviewer", Level: proto.TokenPrompt, Projects: []proto.TokenProject{{ID: "r1", Name: "api"}}, Created: now, Expires: now.Add(48 * time.Hour)},
		{ID: "bbbb2222", Name: "ci", Level: proto.TokenRead, Created: now, Used: now},
		{ID: "cccc3333", Name: "old", Level: proto.TokenFull, Created: now, Expires: now.Add(-time.Hour)},
	}})
	p, cmd := openTokens(t, m)
	if out := a2Plain(p.render(*m).lines); !strings.Contains(out, "Asking the server") {
		t.Errorf("before the answer:\n%s", out)
	}
	runTokenCmd(t, m, p, cmd)
	b := p.render(*m)
	a2CheckBox(t, b, *m)
	out := a2Plain(b.lines)
	for _, want := range []string{"reviewer", "prompt", "api", "ci", "all", "never", "expired", "x revoke", "conch token create"} {
		if !strings.Contains(out, want) {
			t.Errorf("panel lacks %q:\n%s", want, out)
		}
	}

	p.update(m, tea.KeyMsg{Type: tea.KeyDown})
	if p.sel != 1 {
		t.Fatalf("sel %d", p.sel)
	}
	if cmd := p.revoke(m); cmd != nil || p.confirm != "bbbb2222" {
		t.Fatalf("the first x revoked, or did not ask: %v %q", cmd != nil, p.confirm)
	}
	if out := a2Plain(p.render(*m).lines); !strings.Contains(out, "x again revokes ci") {
		t.Errorf("no confirmation:\n%s", out)
	}
	peer.setResult(proto.MethodTokenRevoke, proto.TokenInfo{ID: "bbbb2222", Name: "ci"})
	_, cmd = p.update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if cmd == nil {
		t.Fatal("the second x did nothing")
	}
	msg := cmd()
	if r, ok := msg.(tokenRevokedMsg); !ok || r.err != nil || r.info.Name != "ci" {
		t.Fatalf("revoked %+v", msg)
	}
	var revoked proto.TokenRevokeParams
	for _, sent := range peer.snapshot() {
		if sent.Method == proto.MethodTokenRevoke {
			_ = json.Unmarshal(sent.Params, &revoked)
		}
	}
	if revoked.Name != "bbbb2222" {
		t.Errorf("revoked by %q, want the id", revoked.Name)
	}
	_, reload := p.update(m, msg)
	if !strings.Contains(m.flash, "revoked ci") || reload == nil {
		t.Errorf("flash %q, reload %v", m.flash, reload != nil)
	}

	// Any other key calls the confirmation off.
	p.update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	p.update(m, tea.KeyMsg{Type: tea.KeyUp})
	if p.confirm != "" {
		t.Error("the confirmation outlived another key")
	}

	if _, cmd := p.update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}}); cmd == nil {
		t.Error("c copies nothing")
	}
	p.update(m, tea.KeyMsg{Type: tea.KeyEsc})
	if _, ok := m.overlay.(*settings); !ok {
		t.Errorf("esc went to %T", m.overlay)
	}
}

func TestTokensPanelEmptyAndFailing(t *testing.T) {
	m, peer := tokensModel(t, proto.CapToken)
	peer.setResult(proto.MethodTokenList, proto.TokenListResult{})
	p, cmd := openTokens(t, m)
	runTokenCmd(t, m, p, cmd)
	if out := a2Plain(p.render(*m).lines); !strings.Contains(out, "No access tokens") {
		t.Errorf("empty:\n%s", out)
	}
	if cmd := p.revoke(m); cmd != nil {
		t.Error("revoked from an empty list")
	}
	p.update(m, tea.KeyMsg{Type: tea.KeyDown})
	if p.sel != 0 {
		t.Errorf("sel %d in an empty list", p.sel)
	}

	peer.setError(proto.MethodTokenList, "the server is busy")
	_, cmd = p.update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}})
	runTokenCmd(t, m, p, cmd)
	if out := a2Plain(p.render(*m).lines); !strings.Contains(out, "the server is busy") {
		t.Errorf("failing:\n%s", out)
	}
}

// An older server cannot list them: the panel says so rather than ask.
func TestTokensPanelOlderServer(t *testing.T) {
	m, peer := tokensModel(t) // no token.v1
	p, cmd := openTokens(t, m)
	if cmd != nil {
		t.Error("asked a server that cannot answer")
	}
	if out := a2Plain(p.render(*m).lines); !strings.Contains(out, "predates access tokens") {
		t.Errorf("older server:\n%s", out)
	}
	for _, method := range peer.methods() {
		if strings.HasPrefix(method, "token.") {
			t.Errorf("sent %s", method)
		}
	}
	m.machines[0].c = nil // and none at all
	p, _ = openTokens(t, m)
	if !strings.Contains(p.err, "not connected") {
		t.Errorf("no server: %q", p.err)
	}
}

// Every size draws inside the screen.
func TestTokensPanelSizes(t *testing.T) {
	m, _ := tokensModel(t, proto.CapToken)
	long := strings.Repeat("n", 64)
	p := &tokensPanel{tokens: []proto.TokenInfo{{ID: "a", Name: long, Level: proto.TokenDrive,
		Projects: []proto.TokenProject{{Name: "a-very-long-project-name"}, {Name: "b"}}}}}
	for _, size := range [][2]int{{1, 1}, {20, 5}, {39, 12}, {80, 24}, {250, 80}} {
		m.width, m.height = size[0], size[1]
		a2CheckBox(t, p.render(*m), *m)
	}
}

// The mouse picks a row, the wheel moves, and a click outside goes back.
func TestTokensPanelMouse(t *testing.T) {
	m, _ := tokensModel(t, proto.CapToken)
	back := &settings{}
	p := &tokensPanel{back: back, tokens: []proto.TokenInfo{{ID: "a", Name: "one"}, {ID: "b", Name: "two"}}}
	m.overlay = p
	b := p.render(*m)
	p.mouse(m, tea.MouseMsg{X: b.x + 3, Y: b.y + 3, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, b)
	if p.sel != 1 {
		t.Errorf("click on the second row: sel %d", p.sel)
	}
	p.mouse(m, tea.MouseMsg{Button: tea.MouseButtonWheelUp}, b)
	if p.sel != 0 {
		t.Errorf("wheel up: sel %d", p.sel)
	}
	p.mouse(m, tea.MouseMsg{X: 0, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, b)
	if m.overlay != back {
		t.Errorf("click outside went to %T", m.overlay)
	}
}

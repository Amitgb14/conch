package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// forwardKey sends a keypress to a pane. Printable input goes as text; every
// other key goes by name so the server can encode it for the pane's current
// terminal modes.
func forwardKey(c *client.Client, id string, k tea.KeyMsg) {
	switch {
	case k.Paste:
		c.Notify(proto.MethodPaneSendText, proto.PaneSendTextParams{ID: id, Text: string(k.Runes), Paste: true})
	case k.Type == tea.KeyRunes && k.Alt && len(k.Runes) == 1:
		c.Notify(proto.MethodPaneSendKeys, proto.PaneSendKeysParams{ID: id, Keys: []string{"alt+" + string(k.Runes)}})
	case k.Type == tea.KeyRunes:
		c.Notify(proto.MethodPaneSendText, proto.PaneSendTextParams{ID: id, Text: string(k.Runes)})
	case k.Type == tea.KeySpace && !k.Alt:
		c.Notify(proto.MethodPaneSendText, proto.PaneSendTextParams{ID: id, Text: " "})
	case k.Type == tea.KeySpace:
		c.Notify(proto.MethodPaneSendKeys, proto.PaneSendKeysParams{ID: id, Keys: []string{"alt+space"}})
	default:
		c.Notify(proto.MethodPaneSendKeys, proto.PaneSendKeysParams{ID: id, Keys: []string{k.String()}})
	}
}

func decodeInto(msg proto.Message, v any) bool {
	return json.Unmarshal(msg.Data, v) == nil
}

// callCtx makes a request with the TUI's standard timeout.
func callCtx(c *client.Client, method string, params, out any) error {
	return callCtxFor(c, method, params, out, 30*time.Second)
}

// callCtxFor is callCtx with a longer wait, for work that reaches the
// network or touches many files.
func callCtxFor(c *client.Client, method string, params, out any, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return c.Call(ctx, method, params, out)
}

// Keys typed into a pane while its machine reconnects — a server reload
// takes a moment — are held and sent once the pane is back, rather than
// lost or taken as tree commands.
const (
	maxHeldKeys = 4096
	heldKeyAge  = 15 * time.Second // older keys are dropped, not replayed
)

type heldKey struct {
	pane    string
	created time.Time // the pane's start, so a restarted server's same ID doesn't get them
	key     tea.KeyMsg
	at      time.Time
}

// sendKey types k into pane id on machine mid, or holds it while that
// machine is reconnecting. Held keys go first, so order is kept.
func (m *Model) sendKey(mid, id string, k tea.KeyMsg) {
	mach := m.machine(mid)
	if mach == nil {
		return
	}
	if mach.c != nil && len(mach.held) > 0 && time.Since(mach.held[0].at) > heldKeyAge {
		m.flushHeld(mach) // the pane list that would have sent them never came
	}
	if mach.c != nil && len(mach.held) == 0 {
		forwardKey(mach.c, id, k)
		return
	}
	p := mach.pane(id)
	if p == nil {
		return
	}
	switch {
	case len(mach.held) >= maxHeldKeys:
		m.setFlash("still reconnecting to "+mach.label+" · typing dropped", true)
		return
	case len(mach.held) == 0:
		m.setFlash("reconnecting to "+mach.label+" · typing is held until it's back", false)
	}
	mach.held = append(mach.held, heldKey{pane: id, created: p.Created, key: k, at: time.Now()})
}

// flushHeld sends the keys held for a machine once its panes are listed
// again: to the same panes, still running, if they aren't too old.
func (m *Model) flushHeld(mach *machine) {
	if mach.c == nil || len(mach.held) == 0 {
		return
	}
	dropped := 0
	for _, h := range mach.held {
		p := mach.pane(h.pane)
		if p == nil || p.State != proto.PaneRunning || !p.Created.Equal(h.created) || time.Since(h.at) > heldKeyAge {
			dropped++
			continue
		}
		forwardKey(mach.c, h.pane, h.key)
	}
	mach.held = nil
	if dropped > 0 {
		m.setFlash(fmt.Sprintf("dropped %d keys typed while %s was offline", dropped, mach.label), true)
	}
}

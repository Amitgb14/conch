package tui

import (
	"context"
	"encoding/json"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/amitghadge/conch/internal/client"
	"github.com/amitghadge/conch/internal/proto"
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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return c.Call(ctx, method, params, out)
}

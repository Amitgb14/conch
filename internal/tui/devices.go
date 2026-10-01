package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/phone"
)

// ---- paired devices ----

// devicesPanel lists the phones and browsers paired with conch web, from
// Settings → Web: what each may do, changed here, and revoking one. The
// gateway reads the file on each request, so a change applies at once.
// The TUI is the person, so none of this is held back as an agent is.
type devicesPanel struct {
	back    overlay // the settings it was opened from
	devices []phone.Device
	err     string
	sel     int
	scroll  int
	confirm string // the device x was pressed on once: a second x revokes it
}

func newDevicesPanel(back overlay) *devicesPanel {
	p := &devicesPanel{back: back}
	p.load()
	return p
}

func (p *devicesPanel) load() {
	devs, err := phone.OpenStore(config.Dir()).Devices()
	p.devices, p.err = devs, ""
	if err != nil {
		p.err = err.Error()
	}
	p.sel = min(p.sel, max(len(p.devices)-1, 0))
}

func (p *devicesPanel) close(m *Model) {
	m.overlay = p.back
}

// set gives the selected device a permission.
func (p *devicesPanel) set(m *Model, permission string) {
	if p.sel >= len(p.devices) {
		return
	}
	d := p.devices[p.sel]
	if _, err := phone.OpenStore(config.Dir()).SetPermission(d.ID, permission); err != nil {
		m.setFlash(err.Error(), true)
		return
	}
	m.setFlash(fmt.Sprintf("%s (%s) may now %s", d.Name, d.ID, permissionWords[permission]), false)
	p.load()
}

// revoke asks once, then revokes the selected device.
func (p *devicesPanel) revoke(m *Model) {
	if p.sel >= len(p.devices) {
		return
	}
	d := p.devices[p.sel]
	if p.confirm != d.ID {
		p.confirm = d.ID
		return
	}
	p.confirm = ""
	if _, err := phone.OpenStore(config.Dir()).Revoke(d.ID); err != nil {
		m.setFlash(err.Error(), true)
		return
	}
	m.setFlash(fmt.Sprintf("revoked %s (%s): it is signed out at once", d.Name, d.ID), false)
	p.load()
}

var permissionWords = map[string]string{
	phone.PermView:  "look only",
	phone.PermReply: "reply and answer",
	phone.PermFull:  "do everything, typing into terminals too",
}

func (p *devicesPanel) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return false, nil
	}
	key := k.String()
	if key != "x" {
		p.confirm = ""
	}
	switch key {
	case "up", "k":
		p.sel = max(p.sel-1, 0)
	case "down", "j":
		p.sel = min(p.sel+1, max(len(p.devices)-1, 0))
	case "v":
		p.set(m, phone.PermView)
	case "r":
		p.set(m, phone.PermReply)
	case "f":
		p.set(m, phone.PermFull)
	case "x", "delete":
		p.revoke(m)
	case "R":
		p.load()
	case "esc", "q", "left", "h":
		p.close(m)
	}
	return false, nil
}

// devicesHead is the column titles, and devicesRow one device, as wide as
// the panel's content.
func devicesRow(d phone.Device, w int) string {
	when := d.Created.Local().Format("Jan 2 15:04")
	name := ansi.Truncate(d.Name, max(w-34, 6), "…")
	return fit(fmt.Sprintf(" %-*s  %-7s  %-6s  %s", max(w-34, 6), name, d.ID, d.Permission, when), w)
}

func (p *devicesPanel) render(m Model) box {
	w := min(76, max(m.width-4, 24))
	var lines []string
	add := func(s string) { lines = append(lines, wrap(s, w-2)...) }
	switch {
	case p.err != "":
		add(styleErr.Render("Can't read the devices: " + p.err))
	case len(p.devices) == 0:
		add("No phones or browsers are paired. 🌐 in the status bar, or Pair a phone in the settings, pairs one.")
	default:
		lines = append(lines, styleMuted.Render(fit(fmt.Sprintf(" %-*s  %-7s  %-6s  %s", max(w-34, 6), "NAME", "ID", "MAY", "PAIRED"), w)))
		// Leave room for the header, the footer and the frame.
		room := max(m.height-8, 1)
		if p.sel < p.scroll {
			p.scroll = p.sel
		}
		if p.sel >= p.scroll+room {
			p.scroll = p.sel - room + 1
		}
		for i := p.scroll; i < len(p.devices) && i < p.scroll+room; i++ {
			row := devicesRow(p.devices[i], w)
			if i == p.sel {
				row = styleSel.Render(row)
			}
			lines = append(lines, row)
		}
	}
	lines = append(lines, "")
	if len(p.devices) > 0 && p.sel < len(p.devices) {
		d := p.devices[p.sel]
		if p.confirm == d.ID {
			add(styleWarn.Render(fmt.Sprintf("x again revokes %s (%s): it is signed out at once", d.Name, d.ID)))
		} else {
			add(styleMuted.Render(fmt.Sprintf("%s may %s.", d.Name, permissionWords[d.Permission])))
		}
	}
	add(styleMuted.Render("v view · r reply · f full · x revoke · ↑↓ · esc back"))
	b := box{lines: frameLines(" Web · paired devices ", lines, w, colorAccent)}
	b.x = max((m.width-b.width())/2, 0)
	b.y = max((m.height-len(b.lines))/3, 0)
	return b
}

// mouse: a click on a device selects it; a click outside goes back; the
// wheel moves the selection.
func (p *devicesPanel) mouse(m *Model, msg tea.MouseMsg, b box) tea.Cmd {
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		p.sel = max(p.sel-1, 0)
		return nil
	case tea.MouseButtonWheelDown:
		p.sel = min(p.sel+1, max(len(p.devices)-1, 0))
		return nil
	}
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return nil
	}
	if !b.contains(msg.X, msg.Y) {
		p.close(m)
		return nil
	}
	// Row 1 is the column titles, under the frame's top line.
	if i := p.scroll + msg.Y - b.y - 2; msg.Y-b.y >= 2 && i < len(p.devices) && i < p.scroll+max(m.height-8, 1) {
		p.sel, p.confirm = i, ""
	}
	return nil
}

// devicesSummary is what the settings row says beside "Devices".
func devicesSummary() string {
	devs, err := phone.OpenStore(config.Dir()).Devices()
	switch {
	case err != nil:
		return styleErr.Render("can't be read")
	case len(devs) == 0:
		return "none paired"
	}
	counts := map[string]int{}
	for _, d := range devs {
		counts[d.Permission]++
	}
	var parts []string
	for _, p := range []string{phone.PermFull, phone.PermReply, phone.PermView} {
		if counts[p] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[p], p))
		}
	}
	return fmt.Sprintf("%d paired · %s", len(devs), strings.Join(parts, ", "))
}

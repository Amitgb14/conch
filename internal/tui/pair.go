package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/phone"
)

// ---- pairing a phone ----

// pairDialog pairs a phone with this computer, for `conch web`: a QR code
// that opens the gateway's page with a one-time code filled in, and the
// code for typing. Opening it issues the code, as `conch web pair` does;
// it is this computer's gateway whatever machine the tree has selected.
type pairDialog struct {
	permission string
	code       string
	url        string // where the gateway said phones open it; "" if it never ran
	expires    time.Time
	qr         []string
	err        string
}

func newPairDialog(permission string) *pairDialog {
	d := &pairDialog{permission: permission}
	store := phone.OpenStore(config.Dir())
	now := time.Now()
	code, err := store.NewCode(permission, now)
	if err != nil {
		d.err = err.Error()
		return d
	}
	d.code, d.expires, d.url = code, now.Add(phone.PairTTL), store.URL()
	if d.url != "" {
		d.qr, _ = phone.QRLines(phone.PairLink(d.url, code)) // without one, the code is still there to type
	}
	return d
}

func (d *pairDialog) link() string {
	if d.url == "" || d.code == "" {
		return ""
	}
	return phone.PairLink(d.url, d.code)
}

func (d *pairDialog) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return false, nil
	}
	switch k.String() {
	case "v":
		m.overlay = newPairDialog(phone.PermView)
	case "r":
		m.overlay = newPairDialog(phone.PermReply)
	case "f":
		m.overlay = newPairDialog(phone.PermFull)
	case "y":
		if l := d.link(); l != "" {
			return false, copyText(l)
		}
	default:
		m.overlay = nil
		return true, nil
	}
	return false, nil
}

// render lays the dialog out within the screen. The QR code is shown only
// when all of it fits with the words round it — a code cut short doesn't
// scan — and the words are few enough that an 80×24 terminal has room.
func (d *pairDialog) render(m Model) box {
	if len(d.qr) > 0 {
		if b := d.layout(m, true); b.width() <= m.width && len(b.lines) <= m.height {
			return b
		}
	}
	return d.layout(m, false)
}

func (d *pairDialog) layout(m Model, withQR bool) box {
	const title = " Pair a phone with this computer "
	w := min(64, max(m.width-4, 20))
	if withQR {
		w = max(w, ansi.StringWidth(d.qr[0])+2)
	}
	var text []string
	add := func(s string) { text = append(text, wrap(s, w-2)...) }
	switch {
	case d.err != "":
		add(styleErr.Render("No pairing code: " + d.err))
	default:
		add(fmt.Sprintf("Code %s · %s · until %s, once", styleBold.Render(d.code), d.permission, d.expires.Format("15:04")))
		switch {
		case d.url == "":
			add("conch web has never run here. Start it (conch web, with -url for the address tailscale serve gives), then press P again.")
		case withQR:
			add("Scan it with the phone's camera, or open " + d.url + " and type the code (conch web has to be running).")
		default:
			add("Open " + d.url + " on the phone and type the code. conch web has to be running.")
			if len(d.qr) > 0 {
				add(styleMuted.Render(fmt.Sprintf("A larger terminal shows a QR code to scan (%d wide, %d high).", ansi.StringWidth(d.qr[0])+4, len(d.qr)+len(text)+3)))
			}
		}
	}
	// v, r and f name the permissions the code line shows.
	keys := "v/r/f new code as view/reply/full · other keys close"
	if d.link() != "" {
		keys = "v/r/f new code · y copy link · other keys close"
	}
	add(styleMuted.Render(keys))

	var lines []string
	if withQR {
		pad := (w - ansi.StringWidth(d.qr[0])) / 2
		for _, l := range d.qr {
			lines = append(lines, fmt.Sprintf("%*s%s", pad, "", l))
		}
	}
	for _, l := range text {
		lines = append(lines, " "+l)
	}
	b := box{lines: frameLines(title, lines, w, colorAccent)}
	b.x = max((m.width-b.width())/2, 0)
	b.y = max((m.height-len(b.lines))/3, 0)
	return b
}

func (d *pairDialog) mouse(m *Model, msg tea.MouseMsg, b box) tea.Cmd {
	if msg.Action == tea.MouseActionPress && !b.contains(msg.X, msg.Y) {
		m.overlay = nil
	}
	return nil
}

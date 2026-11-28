package pane

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
)

// A raw client — Conch.app's Ghostty — is given a replay and then the
// program's own output, and must end on the same screen as conch's copy.
// When the replay leaves out state the program relies on, the two part:
// a program that repaints by moving the cursor relative to where it is
// (Claude Code) then writes over the wrong rows on the client only, and
// its screen fills with doubled lines and letters written over letters,
// while conch's own copy stays right. Seen on a real Mac after an install
// and a server reload, each of which attaches afresh.

// clientLines is a stand-in terminal's screen as plain text.
func clientLines(e *vt.Emulator) []string {
	cols, rows := e.Width(), e.Height()
	out := make([]string, rows)
	for y := 0; y < rows; y++ {
		var b strings.Builder
		for x := 0; x < cols; x++ {
			c := e.CellAt(x, y)
			switch {
			case c == nil:
				b.WriteByte(' ')
			case c.Content != "":
				b.WriteString(c.Content)
			case !c.IsZero():
				b.WriteByte(' ')
			}
		}
		out[y] = strings.TrimRight(b.String(), " ")
	}
	return out
}

func trimmed(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = strings.TrimRight(l, " ")
	}
	return out
}

// attachMidway starts script, waits for ready on screen, attaches a raw
// client then, lets the program go on (a line typed into its read), and
// says what each side shows once marker has reached the client.
func attachMidway(t *testing.T, script, ready, marker string, cols, rows int) (conch, client []string) {
	t.Helper()
	p := a5Script(t, script, cols, rows)
	waitScreen(t, p, ready)
	replay, c, r, sub := p.SubscribeRaw()
	defer p.UnsubscribeRaw(sub)
	term := vt.NewEmulator(c, r)
	go func() { // a terminal answers what it is asked; nobody reads it here
		buf := make([]byte, 256)
		for {
			if _, err := term.Read(buf); err != nil {
				return
			}
		}
	}()
	_, _ = term.Write([]byte(replay))
	_ = p.SendText("\n", false)
	got := rawUntil(t, sub, marker)
	_, _ = term.Write([]byte(got))
	time.Sleep(100 * time.Millisecond) // anything after the marker, on both
	for {
		select {
		case b := <-sub.C:
			_, _ = term.Write(b)
			continue
		default:
		}
		break
	}
	return trimmed(p.PlainLines()), clientLines(term)
}

func sameScreen(t *testing.T, conch, client []string) {
	t.Helper()
	if strings.Join(conch, "\n") != strings.Join(client, "\n") {
		t.Fatalf("the raw client's screen parted from conch's:\nconch:\n%s\nclient:\n%s",
			strings.Join(conch, "\n"), strings.Join(client, "\n"))
	}
}

// A line exactly as wide as the pane leaves the cursor waiting to wrap:
// the next character goes on the next row. A replay that only put the
// cursor in the last column put it there on the client, one row short,
// and every repaint counted up from the wrong row after that.
func TestRawClientKeepsAPendingWrap(t *testing.T) {
	conch, client := attachMidway(t,
		`stty -echo; printf '%40s' '' | tr ' ' '='; read -r _; printf 'X\033[1A\rY'; printf '\033[3;1Hdone-%s' $((1+1)); sleep 30`,
		"========================================", "done-2", 40, 6)
	sameScreen(t, conch, client)
}

// A cursor saved with ESC 7 before the client came is where ESC 8 goes
// back to after: the replay has to say where that is.
func TestRawClientKeepsASavedCursor(t *testing.T) {
	conch, client := attachMidway(t,
		`stty -echo; printf 'abc\0337\r\n\r\nready'; read -r _; printf '\0338Z'; printf '\033[5;1Hdone-%s' $((1+1)); sleep 30`,
		"ready", "done-2", 40, 6)
	sameScreen(t, conch, client)
}

// Repainting in place, as Claude Code does: up to the top of what it
// drew, clear down, draw again — with full-width rules among the lines.
func TestRawClientRepaintsInPlace(t *testing.T) {
	conch, client := attachMidway(t,
		`stty -echo; rule=$(printf '%40s' '' | tr ' ' '-'); printf 'history\r\n'; printf '%s' "$rule"; printf 'box one\r\n%s' "$rule"; read -r _; `+
			`i=0; while [ $i -lt 3 ]; do printf '\033[2A\r\033[J%s' "$rule"; printf 'box %s\r\n%s' $i "$rule"; i=$((i+1)); done; printf '\r\ndone-%s' $((1+1)); sleep 30`,
		"box one", "done-2", 40, 8)
	sameScreen(t, conch, client)
}

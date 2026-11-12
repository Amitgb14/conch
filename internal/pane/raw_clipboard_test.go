package pane

import (
	"bytes"
	"strings"
	"testing"
)

// OSC 52 never reaches a raw client: not setting the clipboard, not asking
// for it, however it is ended and however the reads cut it.
func TestQueryFilterTakesOutTheClipboard(t *testing.T) {
	payload := strings.Repeat("aGVsbG8=", 2000) // far longer than maxHeld
	for _, tc := range []struct{ name, in, want string }{
		{"set, BEL", "a\x1b]52;c;" + payload + "\x07b", "ab"},
		{"set, ST", "a\x1b]52;c;" + payload + "\x1b\\b", "ab"},
		{"read", "a\x1b]52;c;?\x07b", "ab"},
		{"empty selection", "\x1b]52;;aGk=\x07", ""},
		{"ended by another sequence", "\x1b]52;c;aGk=\x1b[31mred", "\x1b[31mred"},
		{"OSC 5 is not 52", "\x1b]5;x\x07", "\x1b]5;x\x07"},
		{"OSC 520 is not 52", "\x1b]520;x\x07", "\x1b]520;x\x07"},
		{"between queries", "\x1b[c\x1b]52;c;?\x07\x1b[6nok", "ok"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var f queryFilter
			if got := string(f.filter([]byte(tc.in))); got != tc.want {
				t.Fatalf("filter = %.80q, want %q", got, tc.want)
			}
			if f.dropping || len(f.held) != 0 {
				t.Fatalf("left dropping=%v held=%q", f.dropping, f.held)
			}
		})
	}
}

func TestQueryFilterTakesOutTheClipboardAcrossReads(t *testing.T) {
	in := "before\x1b]52;c;" + strings.Repeat("QUJD", 500) + "\x1b\\after\x1b]52;c;?\x07end"
	want := "beforeafterend"
	// Every way of cutting it in two, and one byte at a time.
	for cut := 0; cut <= len(in); cut++ {
		var f queryFilter
		got := string(f.filter([]byte(in[:cut]))) + string(f.filter([]byte(in[cut:])))
		if got != want {
			t.Fatalf("cut at %d: %.80q", cut, got)
		}
	}
	var f queryFilter
	var out bytes.Buffer
	for i := range len(in) {
		out.Write(f.filter([]byte{in[i]}))
	}
	if out.String() != want {
		t.Fatalf("a byte at a time: %.80q", out.String())
	}
}

// The window title a replay writes back is the program's, so it cannot end
// conch's sequence and start one of its own.
func TestPrintableTitle(t *testing.T) {
	if got := printableTitle("ok\x07\x1b]52;c;aGk=\x07\u009b\x7f!"); got != "ok]52;c;aGk=!" {
		t.Fatalf("%q", got)
	}
}

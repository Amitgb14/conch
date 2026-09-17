package pane

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// Claude Code's titles start with "✳" (E2 9C B3). The emulator's parser
// took the 0x9C inside it for the C1 string terminator, so the title ended
// there and the rest was printed at the cursor — in the agent's input box.
func TestStringFilterKeepsUTF8InsideEscapeStrings(t *testing.T) {
	for _, c := range []struct {
		name, in, want string
	}{
		{"title with BEL", "a\x1b]0;✳ Terminals SSH support\x07b", "a\x1b]0;? Terminals SSH support\x07b"},
		{"title with ST", "a\x1b]2;✳ plan\x1b\\b", "a\x1b]2;? plan\x1b\\b"},
		{"four-byte character", "\x1b]0;😀x\x07", "\x1b]0;?x\x07"},
		{"several characters", "\x1b]0;héllo ✳\x07", "\x1b]0;h?llo ?\x07"},
		{"DCS", "\x1bPq✳\x1b\\", "\x1bPq?\x1b\\"},
		{"APC", "\x1b_✳\x1b\\", "\x1b_?\x1b\\"},
		{"a lone C1 ST still ends the string", "\x1b]0;x\x9cafter", "\x1b]0;x\x9cafter"},
		{"CAN abandons the string", "\x1b]0;x\x18✳", "\x1b]0;x\x18✳"},
		{"text outside strings is left alone", "héllo ✳ \x1b[1mbold\x1b[m", "héllo ✳ \x1b[1mbold\x1b[m"},
		{"an escape inside a string that isn't ST", "\x1b]0;a\x1bx✳\x07", "\x1b]0;a\x1bx?\x07"},
		{"empty", "", ""},
	} {
		var f stringFilter
		if got := string(f.filter([]byte(c.in))); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// A sequence can arrive in pieces: split anywhere, the result is the same.
func TestStringFilterAcrossWrites(t *testing.T) {
	in := []byte("before\x1b]0;✳ Drag-and-drop screenshots plan\x07after ✳ \x1b]2;😀\x1b\\end")
	var whole stringFilter
	want := whole.filter(append([]byte(nil), in...))
	for cut := 0; cut <= len(in); cut++ {
		for cut2 := cut; cut2 <= len(in); cut2 += 7 {
			var f stringFilter
			var got []byte
			got = append(got, f.filter(in[:cut])...)
			got = append(got, f.filter(in[cut:cut2])...)
			got = append(got, f.filter(in[cut2:])...)
			if !bytes.Equal(got, want) {
				t.Fatalf("split at %d,%d: %q, want %q", cut, cut2, got, want)
			}
		}
	}
	// Inside the strings the characters are replaced; between them, text
	// keeps its UTF-8.
	if !bytes.Contains(want, []byte("\x1b]0;? Drag-and-drop screenshots plan\x07")) ||
		!bytes.Contains(want, []byte("\x1b]2;?\x1b\\")) || !bytes.Contains(want, []byte("after ✳ ")) {
		t.Fatalf("filtered: %q", want)
	}
}

// Nothing to change: the same slice comes back, with no copy.
func TestStringFilterFastPath(t *testing.T) {
	var f stringFilter
	b := []byte("plain output with ✳ and \x1b[31mcolour\x1b[m")
	if got := f.filter(b); &got[0] != &b[0] {
		t.Fatal("unchanged output was copied")
	}
}

// Through a real pane: the title stays out of the screen and keeps its text.
func TestTitleWithUTF8DoesNotLeakOntoTheScreen(t *testing.T) {
	p, err := Start(Options{
		ID: "t3", Cols: 60, Rows: 5,
		Command: []string{"/bin/sh", "-c", `printf 'box>\033]0;\342\234\263 Terminals SSH support\007|MARK'; sleep 30`},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	waitScreen(t, p, "|MARK")
	screen := strings.Join(p.PlainLines(), "\n")
	if strings.Contains(screen, "SSH support") {
		t.Fatalf("the title leaked onto the screen:\n%s", screen)
	}
	if !strings.Contains(screen, "box>|MARK") {
		t.Fatalf("screen:\n%s", screen)
	}
	deadline := time.Now().Add(3 * time.Second)
	for p.Title() != "✳ Terminals SSH support" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := p.Title(); got != "✳ Terminals SSH support" {
		t.Fatalf("title %q, want the full UTF-8 title", got)
	}
}

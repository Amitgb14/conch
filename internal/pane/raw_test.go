package pane

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestQueryFilterTakesOutWhatConchAnswers(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"plain text", "hello\r\n", "hello\r\n"},
		{"styled text", "\x1b[31mred\x1b[0m", "\x1b[31mred\x1b[0m"},
		{"primary DA", "a\x1b[cb", "ab"},
		{"primary DA with 0", "\x1b[0c", ""},
		{"secondary DA", "\x1b[>c", ""},
		{"tertiary DA", "\x1b[=c", ""},
		{"cursor position report", "x\x1b[6ny", "xy"},
		{"status report", "\x1b[5n", ""},
		{"private DSR", "\x1b[?6n", ""},
		{"DECRQM", "\x1b[?2026$p", ""},
		{"ANSI DECRQM", "\x1b[4$p", ""},
		{"soft reset is not DECRQM", "\x1b[!p", "\x1b[!p"},
		{"background colour, BEL", "\x1b]11;?\x07", ""},
		{"foreground colour, ST", "\x1b]10;?\x1b\\", ""},
		{"cursor colour", "\x1b]12;?\x07", ""},
		{"program status probe", "\x1b]7501;?\x07", ""},
		{"program status report", "\x1b]7501;state=working\x07", "\x1b]7501;state=working\x07"},
		{"setting a colour", "\x1b]11;#000000\x07", "\x1b]11;#000000\x07"},
		{"title", "\x1b]0;✳ claude\x07", "\x1b]0;✳ claude\x07"},
		{"XTVERSION is the client's", "\x1b[>q", "\x1b[>q"},
		{"kitty keyboard query is the client's", "\x1b[?u", "\x1b[?u"},
		{"kitty graphics query is the client's", "\x1b_Gi=1,a=q;AAAA\x1b\\", "\x1b_Gi=1,a=q;AAAA\x1b\\"},
		{"alternate screen", "\x1b[?1049h", "\x1b[?1049h"},
		{"two queries and text", "\x1b[c\x1b[6nok", "ok"},
		{"not a CSI after all", "\x1b[\x01x", "\x1b[\x01x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var f queryFilter
			if got := string(f.filter([]byte(tc.in))); got != tc.want {
				t.Fatalf("filter(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if len(f.held) != 0 {
				t.Fatalf("held %q after a whole input", f.held)
			}
		})
	}
}

func TestQueryFilterAcrossReads(t *testing.T) {
	var f queryFilter
	var out bytes.Buffer
	for _, piece := range []string{"before", "\x1b", "[", "6", "n", "after", "\x1b]11", ";?", "\x1b", "\\", "end\x1b[3", "1m!"} {
		out.Write(f.filter([]byte(piece)))
	}
	if got := out.String(); got != "beforeafterend\x1b[31m!" {
		t.Fatalf("got %q", got)
	}
}

func TestQueryFilterLetsLongSequencesThrough(t *testing.T) {
	var f queryFilter
	long := "\x1b]0;" + strings.Repeat("t", 3*maxHeld) // a title, not yet ended
	got := string(f.filter([]byte(long)))
	got += string(f.filter([]byte("\x07")))
	if got != long+"\x07" {
		t.Fatalf("a long title came out as %q", got)
	}
}

func TestQueryFilterPlainTextIsNotCopied(t *testing.T) {
	var f queryFilter
	b := []byte("no escapes here")
	if got := f.filter(b); &got[0] != &b[0] {
		t.Fatal("plain text was copied")
	}
}

func TestSendRawMarksSlowSubscriberLost(t *testing.T) {
	p := &Pane{}
	slow := newRawSub(2)
	p.raws = map[*RawSub]struct{}{slow: {}}
	buf := []byte("x")
	p.sendRaw(buf)
	buf[0] = 'y' // the piece sent must be a copy
	if got := <-slow.C; string(got) != "x" {
		t.Fatalf("got %q", got)
	}
	p.sendRaw([]byte("1"))
	p.sendRaw([]byte("2"))
	if slow.Lost() {
		t.Fatal("lost with room left")
	}
	p.sendRaw([]byte("3"))
	select {
	case <-slow.LostC():
	default:
		t.Fatal("LostC not closed")
	}
	if !slow.Lost() || len(p.raws) != 0 {
		t.Fatalf("lost=%v, still subscribed=%d", slow.Lost(), len(p.raws))
	}
	p.sendRaw(nil) // nobody left, and nothing to send: no panic
}

// rawUntil reads raw output until it holds want.
func rawUntil(t *testing.T, sub *RawSub, want string) string {
	t.Helper()
	var got strings.Builder
	deadline := time.After(5 * time.Second)
	for !strings.Contains(got.String(), want) {
		select {
		case b := <-sub.C:
			got.Write(b)
		case <-deadline:
			t.Fatalf("raw output never held %q; got %q", want, got.String())
		}
	}
	return got.String()
}

func TestSubscribeRawReplaysThenStreams(t *testing.T) {
	p := startShell(t)
	_ = p.SendText("echo before-$((1+1))\n", false)
	waitScreen(t, p, "before-2")

	replay, cols, rows, sub := p.SubscribeRaw()
	defer p.UnsubscribeRaw(sub)
	if cols != 60 || rows != 10 {
		t.Fatalf("size %dx%d, want 60x10", cols, rows)
	}
	if !strings.Contains(replay, "before-2") {
		t.Fatalf("replay lacks what was printed: %q", replay)
	}
	// What comes after is streamed, queries taken out. The marker is
	// built by the shell so the typed command line cannot match it.
	// conch's emulator answers the query as if typed: read swallows it.
	_ = p.SendText(`printf 'q\033[cafter-%s\n' $((2+3)); read -r reply`+"\n", false)
	got := rawUntil(t, sub, "after-5")
	time.Sleep(100 * time.Millisecond)
	_ = p.SendText("\n", false)
	if strings.Contains(got, "\x1b[c") {
		t.Fatalf("the device attributes query reached the raw client: %q", got)
	}
	if strings.Contains(got, "before-2\r\n$") {
		t.Fatalf("output before the replay was streamed again: %q", got)
	}

	p.UnsubscribeRaw(sub)
	_ = p.SendText("echo gone-$((3+4))\n", false)
	waitScreen(t, p, "gone-7")
	select {
	case b := <-sub.C:
		if strings.Contains(string(b), "gone-7") {
			t.Fatalf("output after unsubscribing: %q", b)
		}
	default:
	}
}

func TestRawReplayPutsAltHistoryFirst(t *testing.T) {
	p := a5Script(t, `printf '\033[?1049hon the alternate screen'; sleep 30`, 40, 6)
	a5Wait(t, "the alternate screen", func() bool { return strings.Contains(a5Screen(p), "alternate screen") })
	p.emuMu.Lock()
	p.alt.lines = []string{"kept-line-one", "kept-line-two"}
	p.emuMu.Unlock()

	replay, _, _, sub := p.SubscribeRaw()
	p.UnsubscribeRaw(sub)
	one, alt, screen := strings.Index(replay, "kept-line-one"), strings.Index(replay, "\x1b[?1049h"), strings.Index(replay, "alternate screen")
	if one < 0 || alt < 0 || screen < 0 || !(one < alt && alt < screen) {
		t.Fatalf("want kept lines, then the alternate screen, then its text; got %q", replay)
	}
}

func TestRawReplayCarriesTitle(t *testing.T) {
	p := a5Script(t, `printf '\033]0;my-title\007ready'; sleep 30`, 40, 6)
	a5Wait(t, "ready", func() bool { return strings.Contains(a5Screen(p), "ready") })
	a5Wait(t, "the title", func() bool { return p.Title() == "my-title" })
	replay, _, _, sub := p.SubscribeRaw()
	p.UnsubscribeRaw(sub)
	if !strings.Contains(replay, "\x1b]2;my-title\x07") {
		t.Fatalf("replay lacks the title: %q", replay)
	}
}

// A pane that changes size starts its raw subscribers again from a replay
// at the new size: their terminals reflowed what they showed, and conch's
// copy did not. The same size is no change, and starts nothing again.
func TestResizeStartsRawAgain(t *testing.T) {
	p := startShell(t)
	waitScreen(t, p, "$")
	_, cols, rows, sub := p.SubscribeRaw()
	if err := p.Resize(cols, rows); err != nil {
		t.Fatal(err)
	}
	if sub.Lost() {
		t.Fatal("a resize to the same size started the subscriber again")
	}
	if err := p.Resize(cols+10, rows+2); err != nil {
		t.Fatal(err)
	}
	select {
	case <-sub.LostC():
	case <-time.After(time.Second):
		t.Fatal("a resize left the subscriber going on a reflowed screen")
	}
	p.emuMu.Lock()
	n := len(p.raws)
	p.emuMu.Unlock()
	if n != 0 {
		t.Fatalf("%d subscribers still held", n)
	}
	_, c2, r2, again := p.SubscribeRaw()
	defer p.UnsubscribeRaw(again)
	if c2 != cols+10 || r2 != rows+2 {
		t.Fatalf("the new replay is for %dx%d, want %dx%d", c2, r2, cols+10, rows+2)
	}
	// Two subscribers, both started again; one left alone afterwards.
	other := newRawSub(rawBacklog)
	p.emuMu.Lock()
	p.raws[other] = struct{}{}
	p.emuMu.Unlock()
	if err := p.Resize(cols, rows); err != nil {
		t.Fatal(err)
	}
	if !again.Lost() || !other.Lost() {
		t.Fatalf("lost: %v %v, want both", again.Lost(), other.Lost())
	}
}

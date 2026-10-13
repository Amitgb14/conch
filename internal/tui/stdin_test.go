package tui

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestUnfinishedSeq(t *testing.T) {
	for _, c := range []struct {
		what string
		in   string
		full bool
		want int // -1: len(in)
	}{
		{"empty", "", true, -1},
		{"plain text", "hello", true, -1},
		{"a whole wheel report", "\x1b[<65;130;43M", true, -1},
		{"a report cut in its numbers", "ab\x1b[<65;13", true, 2},
		{"a report cut in its numbers, short read", "ab\x1b[<65;13", false, 2},
		{"a report cut after <", "ab\x1b[<", false, 2},
		{"cut after ESC [", "ab\x1b[", true, 2},
		{"cut after ESC", "ab\x1b", true, 2},
		{"cut after ESC O", "ab\x1bO", true, 2},
		// A short read ends where the terminal's writes did: these are keys.
		{"Escape pressed", "\x1b", false, -1},
		{"alt+[ pressed", "\x1b[", false, -1},
		{"alt+O pressed", "\x1bO", false, -1},
		{"an unfinished CSI that is not a mouse report, short read", "\x1b[1;5", false, -1},
		{"an unfinished CSI on a full read", "x\x1b[1;5", true, 1},
		{"alt+a", "\x1ba", true, -1},
		{"a finished key after a finished report", "\x1b[<65;1;1M\x1b[A", true, -1},
		{"ESC then a control byte", "\x1b[1\r", true, -1},
		{"too long to be a report", "\x1b[<" + strings.Repeat("1", seqHoldMax), true, -1},
		{"the last ESC is the one that counts", "\x1b[<65;1;1M\x1b[<6", true, 10},
	} {
		want := c.want
		if want < 0 {
			want = len(c.in)
		}
		if got := unfinishedSeq([]byte(c.in), c.full); got != want {
			t.Errorf("%s: %q full=%v: got %d, want %d", c.what, c.in, c.full, got, want)
		}
	}
}

// chunks hands out one prepared read at a time, as a terminal does, then
// an error (or blocks, for a program that keeps reading).
type chunks struct {
	reads [][]byte
	err   error
	block bool
}

func (c *chunks) Read(p []byte) (int, error) {
	if len(c.reads) == 0 {
		if c.block {
			select {}
		}
		return 0, c.err
	}
	n := copy(p, c.reads[0])
	if n < len(c.reads[0]) {
		c.reads[0] = c.reads[0][n:]
	} else {
		c.reads = c.reads[1:]
	}
	return n, nil
}

// splitAt makes the reads a terminal would give a reader whose buffer
// holds size bytes: full ones while there is more, then the rest.
func splitAt(b []byte, size int) [][]byte {
	var out [][]byte
	for len(b) > 0 {
		n := min(size, len(b))
		out = append(out, b[:n])
		b = b[n:]
	}
	return out
}

func readAll(t *testing.T, r io.Reader, size int) [][]byte {
	t.Helper()
	var got [][]byte
	for {
		buf := make([]byte, size)
		n, err := r.Read(buf)
		if n > 0 {
			got = append(got, buf[:n])
		}
		if err != nil {
			return got
		}
	}
}

func TestWholeSequencesHoldsACutReport(t *testing.T) {
	stream := bytes.Repeat([]byte("\x1b[<65;130;43M"), 40)
	r := &seqReader{src: &chunks{reads: splitAt(stream, 256), err: io.EOF}}
	got := readAll(t, r, 256)
	if j := bytes.Join(got, nil); !bytes.Equal(j, stream) {
		t.Fatalf("bytes lost or reordered:\n got %q\nwant %q", j, stream)
	}
	for i, b := range got[:len(got)-1] {
		if unfinishedSeq(b, true) != len(b) {
			t.Errorf("read %d ends partway through a report: %q", i, b[max(len(b)-16, 0):])
		}
	}
}

func TestWholeSequencesWaitsForTheRest(t *testing.T) {
	// A read that is nothing but the start of a report is not handed over
	// empty: the reader waits for the rest and gives the two together.
	r := &seqReader{src: &chunks{reads: [][]byte{[]byte("\x1b[<65;1"), []byte("30;43M")}, err: io.EOF}}
	buf := make([]byte, 256)
	n, err := r.Read(buf)
	if err != nil || string(buf[:n]) != "\x1b[<65;130;43M" {
		t.Fatalf("got %q, %v", buf[:n], err)
	}
}

func TestWholeSequencesKeysAreNotKeptWaiting(t *testing.T) {
	// Escape alone, and alt+[ alone, are keys: a reader that held them
	// would make them wait for the next keypress.
	for _, key := range []string{"\x1b", "\x1b[", "\x1bO"} {
		r := &seqReader{src: &chunks{reads: [][]byte{[]byte(key)}, block: true}}
		done := make(chan string, 1)
		go func() {
			buf := make([]byte, 256)
			n, _ := r.Read(buf)
			done <- string(buf[:n])
		}()
		select {
		case got := <-done:
			if got != key {
				t.Errorf("%q: got %q", key, got)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%q was held back waiting for more", key)
		}
	}
}

func TestWholeSequencesGivesUpWhatItHeldOnError(t *testing.T) {
	boom := errors.New("closed")
	r := &seqReader{src: &chunks{reads: [][]byte{[]byte("ab\x1b[<65;1")}, err: boom}}
	buf := make([]byte, 256)
	n, err := r.Read(buf)
	if err != nil || string(buf[:n]) != "ab" {
		t.Fatalf("first read: %q, %v", buf[:n], err)
	}
	n, err = r.Read(buf)
	if !errors.Is(err, boom) || string(buf[:n]) != "\x1b[<65;1" {
		t.Fatalf("on error the held bytes go with it: %q, %v", buf[:n], err)
	}
}

func TestWholeSequencesTinyBuffer(t *testing.T) {
	// A caller with a buffer smaller than what is held still gets all of
	// it, in order.
	r := &seqReader{src: &chunks{reads: [][]byte{[]byte("ab\x1b[<65;1"), []byte("30;43M")}, err: io.EOF}}
	got := bytes.Join(readAll(t, r, 4), nil)
	if string(got) != "ab\x1b[<65;130;43M" {
		t.Fatalf("got %q", got)
	}
	if n, err := r.Read(nil); n != 0 || err != nil {
		t.Fatalf("an empty read: %d, %v", n, err)
	}
}

// recorder keeps every key and mouse message Bubble Tea parsed.
type recorder struct{ got chan tea.Msg }

func (r recorder) Init() tea.Cmd { return nil }
func (r recorder) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
	case tea.KeyMsg, tea.MouseMsg:
		r.got <- msg
	}
	return r, nil
}
func (r recorder) View() string { return "" }

// parse runs Bubble Tea's own input parser over reads and returns what it
// made of them.
func parse(t *testing.T, in io.Reader, want int) []tea.Msg {
	t.Helper()
	rec := recorder{got: make(chan tea.Msg, 1024)}
	// A plain reader, not the file seqReader embeds (nil here): Bubble Tea
	// would take that for a terminal.
	in = struct{ io.Reader }{in}
	p := tea.NewProgram(rec, tea.WithInput(in), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithoutSignals())
	done := make(chan struct{})
	go func() { _, _ = p.Run(); close(done) }()
	var got []tea.Msg
	timeout := time.After(5 * time.Second)
	for len(got) < want {
		select {
		case m := <-rec.got:
			got = append(got, m)
		case <-timeout:
			p.Quit()
			<-done
			t.Fatalf("parsed %d of %d messages: %v", len(got), want, got)
		}
	}
	p.Quit()
	<-done
	return got
}

// The report from use: wheel reports split across Bubble Tea's 256-byte
// reads came out as alt+[ and `<65;130;43M`, which conch typed into the
// focused shell. Through WholeSequences every one is a wheel event.
func TestWholeSequencesThroughBubbleTea(t *testing.T) {
	report := []byte("\x1b[<65;130;43M")
	stream := bytes.Repeat(report, 40)

	// Without it, the split is there to be seen — or this test proves
	// nothing about the reader.
	raw := parse(t, &chunks{reads: splitAt(stream, 256), block: true}, 40)
	keys := 0
	for _, m := range raw {
		if _, ok := m.(tea.KeyMsg); ok {
			keys++
		}
	}
	if keys == 0 {
		t.Fatal("Bubble Tea no longer splits a report at its buffer's edge; this reader may not be needed")
	}

	got := parse(t, &seqReader{src: &chunks{reads: splitAt(stream, 256), block: true}}, 40)
	for i, m := range got {
		mm, ok := m.(tea.MouseMsg)
		if !ok || mm.Button != tea.MouseButtonWheelDown {
			t.Fatalf("message %d: %T %v, want a wheel down (65)", i, m, m)
		}
	}
}

// A paste longer than one read still arrives as one paste, and keys typed
// around it are untouched.
func TestWholeSequencesPasteAndKeys(t *testing.T) {
	text := strings.Repeat("pasted text ", 60)
	stream := []byte("a\x1b[200~" + text + "\x1b[201~b")
	got := parse(t, &seqReader{src: &chunks{reads: splitAt(stream, 256), block: true}}, 3)
	want := []string{"a", text, "b"}
	for i, m := range got {
		k, ok := m.(tea.KeyMsg)
		if !ok || string(k.Runes) != want[i] {
			t.Fatalf("message %d: %v, want %q", i, m, want[i])
		}
	}
}

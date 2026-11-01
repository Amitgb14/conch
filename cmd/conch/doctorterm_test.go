package main

import (
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/doctor"
)

func TestParseTermAnswers(t *testing.T) {
	for _, c := range []struct {
		what   string
		said   string
		name   string
		modes  map[int]int
		marked bool
	}{
		{what: "nothing"},
		{what: "a terminal with the lot",
			said:   "\x1b[?1006;2$y\x1b[?2004;2$y\x1b[?2026;2$y\x1bP>|ghostty 1.2.0\x1b\\\x1b[?62;22c",
			name:   "ghostty 1.2.0",
			modes:  map[int]int{1006: 2, 2004: 2, 2026: 2},
			marked: true},
		{what: "one that knows the modes but not 2026",
			said:   "\x1b[?1006;1$y\x1b[?2004;2$y\x1b[?2026;0$y\x1b[?6c",
			modes:  map[int]int{1006: 1, 2004: 2, 2026: 0},
			marked: true},
		{what: "a name ended with BEL, as some terminals do",
			said:   "\x1bP>|WezTerm 20240203\a\x1b[?65;4c",
			name:   "WezTerm 20240203",
			marked: true},
		{what: "device attributes alone: it answers, and says nothing of modes",
			said:   "\x1b[?62;c",
			marked: true},
		{what: "a keystroke and a half-finished escape mixed in with the replies",
			said:   "q\x1b[?1006;2$y\x1b[\x1bP>|foot(1.16)\x1b\\A\x1b[?62c",
			name:   "foot(1.16)",
			modes:  map[int]int{1006: 2},
			marked: true},
		{what: "a mode number nothing could read", said: "\x1b[?99999999999999999999;2$y"},
		{what: "junk only", said: "hello\x1b[31m"},
	} {
		got := parseTermAnswers(c.said)
		if got.name != c.name || got.sentinel != c.marked {
			t.Errorf("%s: name %q sentinel %v, want %q %v", c.what, got.name, got.sentinel, c.name, c.marked)
		}
		if len(got.modes) != len(c.modes) {
			t.Errorf("%s: modes %v, want %v", c.what, got.modes, c.modes)
			continue
		}
		for mode, want := range c.modes {
			if got.modes[mode] != want {
				t.Errorf("%s: mode %d is %d, want %d", c.what, mode, got.modes[mode], want)
			}
		}
		// spoke is "it answered at all", which the empty cases are not.
		if spoke := got.spoke(); spoke != (c.said != "" && (c.marked || c.name != "" || len(c.modes) > 0)) {
			t.Errorf("%s: spoke() = %v", c.what, spoke)
		}
	}
}

// A mode is implemented whether it is on (1), off (2) or fixed either way
// (3, 4); 0 is the one answer that means the terminal has never heard of
// it. And a terminal that answers no mode query at all implements none of
// DECRQM, which says nothing about the modes themselves — the difference
// between "it said no" and "it did not say", which is what lacks() keeps.
func TestTermAnswersReadModes(t *testing.T) {
	all := parseTermAnswers("\x1b[?1006;1$y\x1b[?2004;4$y\x1b[?2026;0$y\x1b[?6c")
	for _, mode := range []int{modeSGRMouse, modeBracketedPaste} {
		if !all.has(mode) || all.lacks(mode) {
			t.Errorf("mode %d: has %v lacks %v", mode, all.has(mode), all.lacks(mode))
		}
	}
	if all.has(modeSyncUpdate) || !all.lacks(modeSyncUpdate) {
		t.Error("2026 answered 0 and should be read as missing")
	}

	quiet := parseTermAnswers("\x1b[?62;22c") // answers, but not about modes
	if quiet.knowsModes() {
		t.Error("a terminal that answered no mode query knows modes")
	}
	for _, mode := range []int{modeSGRMouse, modeBracketedPaste, modeSyncUpdate} {
		if quiet.lacks(mode) {
			t.Errorf("silence about mode %d was read as missing", mode)
		}
	}
	var never termAnswers // nobody asked
	for _, mode := range []int{modeSGRMouse, modeBracketedPaste, modeSyncUpdate} {
		if never.lacks(mode) || never.has(mode) {
			t.Errorf("mode %d claimed about a terminal nobody asked", mode)
		}
	}
}

func TestTerminfoName(t *testing.T) {
	for said, want := range map[string]string{
		"ghostty 1.2.0":      "ghostty",
		"kitty(0.33.1)":      "", // the version is not cut by a space here
		"kitty 0.33.1":       "kitty",
		"WezTerm 20240203":   "wezterm",
		"foot(1.16.2)":       "",
		"foot 1.16.2":        "foot",
		"iTerm2 3.5.0":       "", // runs as xterm-256color by design
		"XTerm(389)":         "",
		"":                   "",
		"   ghostty   1.2.0": "ghostty",
	} {
		if got := terminfoName(said); got != want {
			t.Errorf("terminfoName(%q) = %q, want %q", said, got, want)
		}
	}
}

// What the terminal said turns into findings — and only what it said.
func TestDoctorTerminalWhatTheTerminalSaid(t *testing.T) {
	doctorEnv(t)
	t.Setenv("TERM", "xterm-ghostty")
	bin := t.TempDir()
	doctorScript(t, bin, "infocmp", `exit 0`) // the entry is installed
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	// A terminal with everything conch wants: nothing to say but what it
	// is, and that frames arrive whole.
	good := parseTermAnswers("\x1b[?1006;2$y\x1b[?2004;2$y\x1b[?2026;2$y\x1bP>|ghostty 1.2.0\x1b\\\x1b[?62c")
	c := doctorTerminal(config.Default(), good)
	if c.Status != doctor.OK {
		t.Fatalf("a terminal with the lot: %+v", c)
	}
	if !strings.Contains(c.Detail, "says it is ghostty 1.2.0") || !strings.Contains(c.Detail, "synchronized updates") {
		t.Errorf("detail %q", c.Detail)
	}

	// The Ghostty-on-Linux case: it is Ghostty, TERM says plain xterm, so
	// its own keys and colours are described nowhere.
	t.Setenv("TERM", "xterm-256color")
	c = doctorTerminal(config.Default(), good)
	if c.Status != doctor.Warn || !strings.Contains(c.Detail, "TERM does not name ghostty") {
		t.Fatalf("a Ghostty run as xterm: %+v", c)
	}
	if !strings.Contains(c.Fix, "xterm-ghostty") {
		t.Errorf("fix %q", c.Fix)
	}

	// Inside tmux TERM is tmux's to set, and naming the outer terminal
	// would be wrong advice.
	t.Setenv("TMUX", "/tmp/tmux-501/default,1,0")
	cfg := config.Default()
	cfg.Keys.Prefix = "ctrl+z" // not tmux's, so no prefix finding either
	if c = doctorTerminal(cfg, good); c.Status != doctor.OK {
		t.Fatalf("inside tmux: %+v", c)
	}
	t.Setenv("TMUX", "")

	// A mouse conch cannot read, and a paste that is typed. Both are said;
	// the fix offered is the first.
	t.Setenv("TERM", "xterm-ghostty")
	blunt := parseTermAnswers("\x1b[?1006;0$y\x1b[?2004;0$y\x1b[?2026;0$y\x1b[?62c")
	c = doctorTerminal(config.Default(), blunt)
	if c.Status != doctor.Warn {
		t.Fatalf("no mouse, no paste: %+v", c)
	}
	if !strings.Contains(c.Detail, "no SGR mouse reports") || !strings.Contains(c.Detail, "no bracketed paste") {
		t.Errorf("both should be said: %q", c.Detail)
	}
	if !strings.Contains(c.Fix, "mouse") {
		t.Errorf("fix %q", c.Fix)
	}

	// A terminal that does not answer mode queries is not a terminal
	// without the modes: nothing is claimed about it.
	if c = doctorTerminal(config.Default(), parseTermAnswers("\x1b[?62;22c")); c.Status != doctor.OK {
		t.Fatalf("silence read as a fault: %+v", c)
	}
	// Nor is one that was never asked, which is how the TUI runs them.
	if c = doctorTerminal(config.Default(), termAnswers{}); c.Status != doctor.OK || strings.Contains(c.Detail, "says it is") {
		t.Fatalf("nobody asked: %+v", c)
	}

	// A conch pane says so, since the terminal answering there is conch's
	// own emulator and not the window somebody is looking at.
	t.Setenv("CONCH_PANE_ID", "p3")
	if c = doctorTerminal(config.Default(), good); !strings.Contains(c.Detail, "in a conch pane") {
		t.Errorf("detail %q", c.Detail)
	}
}

// TERM naming an entry this computer does not have is the quiet one: conch
// draws anyway, and everything curses draws is a guess.
func TestDoctorTerminalTerminfo(t *testing.T) {
	doctorEnv(t)
	t.Setenv("TERM", "xterm-ghostty")
	bin := t.TempDir()
	t.Setenv("PATH", bin) // nothing but the fakes: no real infocmp, no tmux

	// No infocmp at all: nothing here can say, so nothing is said.
	if c := doctorTerminal(config.Default(), termAnswers{}); c.Status != doctor.OK || strings.Contains(c.Detail, "terminfo") {
		t.Fatalf("without infocmp: %+v", c)
	}

	// An infocmp that cannot find the entry.
	doctorScript(t, bin, "infocmp", `echo "infocmp: couldn't open terminfo file" >&2; exit 1`)
	c := doctorTerminal(config.Default(), termAnswers{})
	if c.Status != doctor.Warn || !strings.Contains(c.Detail, "no terminfo entry for xterm-ghostty here") {
		t.Fatalf("missing entry: %+v", c)
	}
	if !strings.Contains(c.Fix, "tic") {
		t.Errorf("fix %q", c.Fix)
	}

	// It is the first finding, so its fix is the one offered even when the
	// terminal is also short of a mouse: a terminfo nothing can read is
	// the one that breaks other programs too.
	blunt := parseTermAnswers("\x1b[?1006;0$y\x1b[?62c")
	if c = doctorTerminal(config.Default(), blunt); !strings.Contains(c.Fix, "tic") || !strings.Contains(c.Detail, "no SGR mouse") {
		t.Fatalf("both findings: %+v", c)
	}

	// The entry is there: no finding.
	doctorScript(t, bin, "infocmp", `exit 0`)
	if c = doctorTerminal(config.Default(), termAnswers{}); c.Status != doctor.OK {
		t.Fatalf("entry installed: %+v", c)
	}
}

// askTerminalOn against a pty of the test's own: the queries go out, the
// replies come back, and the sentinel ends the wait. Never the terminal
// `go test` runs in — raw mode there would eat somebody's keys.
func TestAskTerminalOnAPty(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Skipf("no pty here: %v", err)
	}
	defer master.Close()
	defer slave.Close()

	// The far end answers as a terminal with the lot would.
	done := make(chan string, 1)
	go func() {
		buf := make([]byte, 512)
		n, err := master.Read(buf)
		if err != nil {
			done <- ""
			return
		}
		master.WriteString("\x1b[?1006;2$y\x1b[?2004;2$y\x1b[?2026;2$y\x1bP>|ghostty 1.2.0\x1b\\\x1b[?62;22c")
		done <- string(buf[:n])
	}()

	start := time.Now()
	said := askTerminalOn(slave, slave, 5*time.Second)
	asked := <-done
	if !said.sentinel || said.name != "ghostty 1.2.0" || !said.has(modeSGRMouse) {
		t.Fatalf("answers %+v", said)
	}
	// The sentinel ended it, so it did not sit out the timeout.
	if took := time.Since(start); took > 4*time.Second {
		t.Errorf("waited %v for a terminal that had answered", took)
	}
	for _, want := range []string{"\x1b[?1006$p", "\x1b[?2026$p", "\x1b[>0q", "\x1b[c"} {
		if !strings.Contains(asked, want) {
			t.Errorf("%q was not asked; sent %q", want, asked)
		}
	}
	// Device attributes go last: everything before it has been answered
	// by the time it comes back.
	if !strings.HasSuffix(asked, "\x1b[c") {
		t.Errorf("the sentinel is not last: %q", asked)
	}
}

// A terminal that says nothing holds nobody up, and answers nothing.
func TestAskTerminalSilence(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Skipf("no pty here: %v", err)
	}
	defer master.Close()
	defer slave.Close()
	start := time.Now()
	said := askTerminalOn(slave, slave, 150*time.Millisecond)
	if said.spoke() {
		t.Fatalf("a silent terminal answered %+v", said)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("waited %v on a terminal that never answers", took)
	}
}

// `conch doctor > file` from a terminal: stdin is a terminal and stdout is
// not, which is the case that would write escape sequences into somebody's
// file. Both ends have to be the terminal, not just the one being read.
func TestAskTerminalNotIntoARedirect(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Skipf("no pty here: %v", err)
	}
	defer master.Close()
	defer slave.Close()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	if said := askTerminalOn(slave, w, 500*time.Millisecond); said.spoke() {
		t.Fatalf("asked through a redirect: %+v", said)
	}
	w.Close()
	buf := make([]byte, 64)
	if n, err := r.Read(buf); n != 0 || err != io.EOF {
		t.Errorf("%d bytes went into the redirect: %q, %v", n, buf[:n], err)
	}
}

// Not a terminal, nothing asked: a pipe would carry the queries into
// whatever conch's output is being redirected to.
func TestAskTerminalNeedsATerminal(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if said := askTerminalOn(r, w, time.Second); said.spoke() {
		t.Fatalf("a pipe answered %+v", said)
	}
	// And nothing was written into it. Closing the writing end is what
	// makes the read answer at once — the bytes if there were any, EOF
	// if not. A deadline would not do it: asking for the descriptor
	// (term.IsTerminal does) puts the file back in blocking mode.
	w.Close()
	if n, err := r.Read(make([]byte, 64)); n != 0 || err != io.EOF {
		t.Errorf("the queries were written to a pipe: %d bytes, %v", n, err)
	}
}

package main

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
)

// What the terminal says about itself, as against what the environment
// claims. TERM and TERM_PROGRAM are set by whatever started the shell and
// are wrong often enough to be worth checking against the terminal itself:
// a Ghostty whose terminfo was never installed still says it is Ghostty
// while TERM says plain xterm, and then its keys and colours are described
// nowhere. So conch asks — once, in `conch doctor`, never in the TUI.
//
// **Only the command may ask.** A reply lands on conch's own stdin, and
// Bubble Tea v1 does not parse OSC or DECRQM on input, so a reply arriving
// while the TUI is running would reach it as an escape and then as the
// digits of the mode number — keys that switch tabs. That is the same
// reason internal/tui/hoststatus.go is write-only (see AGENTS.md). The TUI
// is handed these checks and runs them with nothing asked.

// The modes worth asking about: the ones conch itself turns on.
const (
	modeSGRMouse       = 1006 // mouse reports conch can read past column 223
	modeBracketedPaste = 2004 // a paste arrives as a paste, not as typing
	modeSyncUpdate     = 2026 // a frame drawn whole, and what altscroll reads
)

// termAskTime is how long the terminal has to answer. A terminal replies
// in microseconds — this is the wait for one that never will.
const termAskTime = 300 * time.Millisecond

// termQueries asks what the terminal is and which modes it knows, and ends
// with Primary Device Attributes: every terminal answers that one, so its
// reply is the mark that the ones before it have been answered or ignored,
// and conch stops waiting without the full timeout.
var termQueries = strings.Join([]string{
	"\x1b[?1006$p", "\x1b[?2004$p", "\x1b[?2026$p", // DECRQM
	"\x1b[>0q", // XTVERSION: the terminal's own name
	"\x1b[c",   // DA1, the sentinel
}, "")

var (
	termModeReply = regexp.MustCompile(`\x1b\[\?(\d+);(\d+)\$y`)
	termNameReply = regexp.MustCompile(`\x1bP>\|([^\x1b\a]*)(?:\x1b\\|\a)`)
	termDAReply   = regexp.MustCompile(`\x1b\[\?[\d;]*c`)
)

// termAnswers is what came back. The zero value is a terminal that was
// never asked, which is not the same as one that said no: every field is
// read through the methods below, so "nothing was asked" can never be
// reported as "your terminal cannot do this".
type termAnswers struct {
	name     string      // XTVERSION, e.g. "ghostty 1.2.0"
	modes    map[int]int // mode → DECRQM value; 0 is "not recognised"
	sentinel bool        // DA1 came back, so the terminal had its say
}

// spoke reports whether the terminal answered at all.
func (a termAnswers) spoke() bool {
	return a.sentinel || a.name != "" || len(a.modes) > 0
}

// knowsModes reports whether the terminal answers mode queries. One that
// answers none of them does not implement DECRQM, and then its silence
// about a mode says nothing about whether it has it — which is why every
// claim below is guarded by this rather than by the mode alone.
func (a termAnswers) knowsModes() bool { return len(a.modes) > 0 }

// has reports that the terminal said it knows a mode (set, reset or
// permanently either, all of which mean it is implemented).
func (a termAnswers) has(mode int) bool { return a.modes[mode] != 0 }

// lacks reports that the terminal was asked, answers mode queries, and
// said it does not know this one.
func (a termAnswers) lacks(mode int) bool { return a.knowsModes() && !a.has(mode) }

// parseTermAnswers reads the replies out of whatever came back, which is
// not only replies: a keystroke pressed while conch was asking arrives in
// the same bytes, and a terminal may split a reply across reads. Anything
// that is not one of the three shapes is passed over.
func parseTermAnswers(said string) termAnswers {
	a := termAnswers{sentinel: termDAReply.MatchString(said)}
	if m := termNameReply.FindStringSubmatch(said); m != nil {
		a.name = strings.TrimSpace(m[1])
	}
	for _, m := range termModeReply.FindAllStringSubmatch(said, -1) {
		mode, err := strconv.Atoi(m[1])
		value, err2 := strconv.Atoi(m[2])
		if err != nil || err2 != nil {
			continue
		}
		if a.modes == nil {
			a.modes = map[int]int{}
		}
		a.modes[mode] = value
	}
	return a
}

// askTerminal asks the terminal the queries above and reads what it says,
// for at most d. It answers the zero value — nothing asked — unless both
// ends are the terminal and raw mode is taken, since the replies would
// otherwise be echoed, line-buffered, or written into a file somebody is
// redirecting conch's output to.
func askTerminal(d time.Duration) termAnswers {
	return askTerminalOn(os.Stdin, os.Stdout, d)
}

// askTerminalOn is askTerminal on a given pair, so a test can hold both
// ends of a pty of its own and never the terminal `go test` runs in.
func askTerminalOn(in, out *os.File, d time.Duration) termAnswers {
	if !term.IsTerminal(in.Fd()) || !term.IsTerminal(out.Fd()) {
		return termAnswers{}
	}
	state, err := term.MakeRaw(in.Fd())
	if err != nil {
		return termAnswers{}
	}
	defer term.Restore(in.Fd(), state)
	if _, err := out.WriteString(termQueries); err != nil {
		return termAnswers{}
	}
	// The reading is a goroutine because a tty takes no read deadline on
	// macOS (see AGENTS.md), so a terminal that never answers would hold
	// `conch doctor` for ever. It is left blocked on the read when that
	// happens: the command prints and exits moments later, which ends it,
	// and nothing else in this process wants stdin.
	said := make(chan string, 16)
	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := in.Read(buf)
			if n > 0 {
				said <- string(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	var all strings.Builder
	for stop := time.After(d); ; {
		select {
		case s := <-said:
			all.WriteString(s)
			// The sentinel is the end: everything asked before it has
			// been answered, or this terminal was never going to.
			if a := parseTermAnswers(all.String()); a.sentinel {
				return a
			}
		case <-stop:
			return parseTermAnswers(all.String())
		}
	}
}

// ownTerminfo are terminals that ship a terminfo entry of their own and
// expect TERM to name it: their keys, colours and extras are described
// there and nowhere else, so running one as plain xterm quietly loses
// them. iTerm2 and Terminal.app are deliberately absent — they run as
// xterm-256color by design, and saying otherwise would be a warning
// nobody can act on.
var ownTerminfo = map[string]string{
	"ghostty":   "ghostty",
	"kitty":     "kitty",
	"wezterm":   "wezterm",
	"foot":      "foot",
	"alacritty": "alacritty",
	"rio":       "rio",
	"contour":   "contour",
}

// terminfoName is the terminfo an answered name expects TERM to hold, or
// "". The name is a word and a version ("ghostty 1.2.0"), and the word is
// matched case-insensitively, since the terminals spell themselves as they
// like ("WezTerm", "foot").
func terminfoName(said string) string {
	word, _, _ := strings.Cut(strings.TrimSpace(said), " ")
	return ownTerminfo[strings.ToLower(word)]
}

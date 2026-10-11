package tui

import (
	"fmt"
	"os"

	"github.com/charmbracelet/x/term"

	"github.com/Amitgb14/conch/internal/pane"
	"github.com/Amitgb14/conch/internal/proto"
)

// conch reads the Program Status Protocol (OSC 7501) off the ptys it owns
// — that is internal/pane/status.go, and it is how an agent says what it
// is doing instead of conch guessing from a spinner. This is the other
// direction: conch is itself a program on somebody's terminal, and it has
// the same thing to say. A terminal that takes the report can put it on
// the tab, so a Ghostty or kitty window says an agent is waiting while
// conch is not on screen — which is the one state nobody can see, because
// seeing it means looking at conch.
//
// **conch does not ask first, and that is deliberate.** A program is
// supposed to query the terminal (`OSC 7501 ; ?`) and stay quiet if
// nothing answers; conch answers that query for the programs in its own
// panes. It cannot ask one itself, because the answer arrives on stdin and
// Bubble Tea v1 has no OSC parsing on input: `ESC ] 7 5 0 1 ; …` would be
// handed to the TUI as an escape and then as the keys `7`, `5`, `0`, `1`,
// `;` — digits that switch tabs. Asking would mean taking the terminal out
// of Bubble Tea's hands, reading with a deadline, and swallowing whatever
// the user typed in that window. So conch writes and does not listen,
// which is safe in the one direction that matters: never asking means
// nothing ever arrives on stdin to be misread.
//
// What it costs is a sequence a terminal without support throws away —
// an unknown OSC is swallowed, not drawn — and these are written on
// change, a handful a minute, not once a cell like the pointer shape.
// `$CONCH_STATUS` settles it either way for a terminal that does
// something ugly with it, or one that wants it under tmux.

// hostStatusWorks reports whether conch should tell the terminal what it
// is doing. tmux and screen are out: they parse OSC themselves, pass on
// what they choose to, and the window the report would reach belongs to
// the multiplexer rather than to conch. A conch whose output is not a
// terminal has nobody to tell.
func hostStatusWorks() bool {
	switch os.Getenv("CONCH_STATUS") {
	case "0", "off", "false":
		return false
	case "1", "on", "true":
		return true
	}
	if os.Getenv("TMUX") != "" || os.Getenv("STY") != "" {
		return false
	}
	return stdoutIsTerminal()
}

// stdoutIsTerminal and sendHostStatus are the reaching-out itself, so the
// test suite can replace them (main_test.go): a test that wrote for real
// would leave a report on the developer's own terminal, which — conch
// being a terminal that reads these — is a report conch would then show
// about itself.
var (
	stdoutIsTerminal = func() bool { return term.IsTerminal(os.Stdout.Fd()) }
	sendHostStatus   = writeHostStatus
)

func writeHostStatus(body string) {
	_, _ = os.Stdout.WriteString("\x1b]7501;" + body + "\x1b\\")
}

// hostApp is what conch calls itself in its reports, as an agent calls
// itself claude-code.
const hostApp = "conch"

// hostStatus is what conch would say about itself now: one record, for
// every agent conch knows of rather than only the workspace on screen.
// The status bar's ⚑ counter deliberately counts only what the workspace
// shows (spaces.go); this does not, because the terminal's tab is outside
// workspaces altogether and an agent waiting in a workspace you have
// switched away from is exactly the one you cannot see.
//
// The order is the tree's own: waiting first, because it is the only state
// that needs you this second; then failed, because it is finished and
// wrong and the next state will not mention it; then working, done, idle.
func (m Model) hostStatus() pane.ProgramStatus {
	rec := pane.ProgramStatus{App: hostApp, Progress: -1}
	var waiting, failed, working, done int
	// The first of each, not the first of either: a failure further down
	// the list would otherwise name itself in a report about an agent
	// waiting, which is what the test for this caught.
	var firstWaiting, firstFailed scopedPane
	for _, mach := range m.machines {
		for _, p := range mach.panes {
			a := p.Agent
			// Not this conch's own pane. A conch run inside a conch pane
			// (`CONCH_PANE_ID= conch`) is detected as whatever its command
			// is named, and the state of that pane is this very report
			// read back: counting it made conch say "2 agents waiting"
			// about one waiting agent and itself, which a live run showed
			// at once. The same `ownPane` the tree uses to avoid drawing
			// itself.
			if a == nil || m.isOwnPane(mach.id, p.ID) {
				continue
			}
			switch {
			case a.State == proto.AgentBlocked:
				if waiting++; waiting == 1 {
					firstWaiting = scopedPane{machine: mach.id, PaneInfo: p}
				}
			case a.Failed && (a.State == proto.AgentDone || a.State == proto.AgentIdle):
				if failed++; failed == 1 {
					firstFailed = scopedPane{machine: mach.id, PaneInfo: p}
				}
			case a.State == proto.AgentWorking:
				working++
			case a.State == proto.AgentDone:
				done++
			}
		}
	}
	switch {
	case waiting > 0:
		rec.State, rec.Kind = pane.StatusBlocked, pane.BlockedQuestion
		rec.Msg = hostMsg(waiting, "waiting", firstWaiting)
	case failed > 0:
		rec.State = pane.StatusError
		rec.Msg = hostMsg(failed, "failed", firstFailed)
	case working > 0:
		rec.State = pane.StatusWorking
		rec.Msg = fmt.Sprintf("%d agent%s working", working, plural(working))
	case done > 0:
		rec.State = pane.StatusDone
		rec.Msg = fmt.Sprintf("%d agent%s done", done, plural(done))
	default:
		rec.State = pane.StatusIdle
	}
	return rec
}

// hostMsg names the agent when there is one of it — the useful line is
// which work wants you, not how many — and counts them when there are
// more. The branch is what tells one agent from another; a pane with no
// branch (a machine's own) is named alone.
func hostMsg(n int, what string, p scopedPane) string {
	if n > 1 {
		return fmt.Sprintf("%d agents %s", n, what)
	}
	name := p.DisplayName()
	if p.Branch != "" {
		name += " on " + p.Branch
	}
	return name + " " + what
}

// reportToHost tells the terminal what changed, and nothing when nothing
// has: a program speaks on change, which is the rule conch relies on when
// it is the one reading.
func (m *Model) reportToHost() {
	if !m.hostStatusOn {
		return
	}
	body := pane.FormatStatus(m.hostStatus())
	if body == m.hostSaid {
		return
	}
	m.hostSaid = body
	sendHostStatus(body)
}

// clearHostStatus takes conch's report away, for leaving the screen to
// somebody else. A tab left saying an agent is waiting would outlive the
// conch that said so — the same debt the pointer's hand owes, and every
// way out goes through here for the same reason.
func (m *Model) clearHostStatus() {
	if !m.hostStatusOn || m.hostSaid == "" {
		return
	}
	m.hostSaid = ""
	sendHostStatus(pane.FormatStatus(pane.StatusClear("")))
}

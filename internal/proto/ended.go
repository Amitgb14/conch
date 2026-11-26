package proto

import (
	"fmt"
	"time"
)

// CapPaneEnded is an agent whose pane ended without anybody closing it —
// it exited, it was killed, the server went — kept as an EndedAgent where
// it was, with why, until it is resumed in its place (pane.ended.resume)
// or dismissed (pane.ended.dismiss). A pane the person closed is not kept:
// closing is the asking. The records survive reloads and restarts, since
// that is when an agent goes missing most.
const CapPaneEnded = "pane.ended.v1"

// Methods of pane.ended.v1.
const (
	MethodPaneEndedList    = "pane.ended.list"    // → EndedList
	MethodPaneEndedDismiss = "pane.ended.dismiss" // EndedRef
	MethodPaneEndedResume  = "pane.ended.resume"  // EndedResumeParams → PaneInfo
)

// Events of pane.ended.v1: one is kept (EndedAgent), and one is gone
// (dismissed or resumed).
const (
	EventPaneEnded     = "pane.ended"
	EventPaneEndedGone = "pane.ended.gone" // EndedGone
)

// Why an agent ended.
const (
	EndedExited  = "exited"  // the program exited, with ExitCode
	EndedKilled  = "killed"  // a signal ended it (Signal)
	EndedServer  = "server"  // the server it ran under went
	EndedMachine = "machine" // the machine could not be reached (said by a client, never stored)
)

// EndedAgent is an agent that ended without being closed. ID is the
// record's own: a pane id is reused by the next server after a restart,
// so it cannot name the record, though Pane says which pane it was — the
// one a client may still hold, exited, in a tab.
type EndedAgent struct {
	ID        string    `json:"id"`
	Pane      string    `json:"pane"`
	Agent     string    `json:"agent"`
	Session   string    `json:"session,omitempty"`
	Dir       string    `json:"dir"`
	Account   string    `json:"account,omitempty"`
	Name      string    `json:"name,omitempty"`
	ProjectID string    `json:"project_id,omitempty"`
	Branch    string    `json:"branch,omitempty"`
	Why       string    `json:"why"`
	ExitCode  int       `json:"exit_code,omitempty"`
	Signal    string    `json:"signal,omitempty"`
	Screen    []string  `json:"screen,omitempty"` // its last screen, as text
	Started   time.Time `json:"started"`
	Ended     time.Time `json:"ended"`
}

// Failed says whether it ended badly enough to ask for the person: anything
// but its own clean exit.
func (e EndedAgent) Failed() bool {
	return e.Why != EndedExited || e.ExitCode != 0
}

// Reason is why it ended, in a few words.
func (e EndedAgent) Reason() string {
	switch e.Why {
	case EndedExited:
		if e.ExitCode == 0 {
			return "exited"
		}
		return fmt.Sprintf("exited with code %d", e.ExitCode)
	case EndedKilled:
		if e.Signal != "" {
			return "killed (" + e.Signal + ")"
		}
		return "killed"
	case EndedServer:
		return "the server restarted"
	case EndedMachine:
		return "the machine went away"
	}
	return "ended"
}

// EndedList is every ended agent a server keeps, newest first.
type EndedList struct {
	Ended []EndedAgent `json:"ended"`
}

// EndedRef names an ended agent by its record's ID.
type EndedRef struct {
	ID string `json:"id"`
}

// EndedGone says a record is gone: dismissed, expired, or resumed — then
// Resumed is the pane it came back in, for a client to put where Pane was.
type EndedGone struct {
	ID      string `json:"id"`
	Pane    string `json:"pane"`
	Resumed string `json:"resumed,omitempty"`
}

// EndedResumeParams resumes an ended agent's conversation on its account,
// in its folder, under its name. Where the new pane goes is the client's
// to say — it knows the tab the ended one was in — and the ended pane, if
// the server still holds it, is closed.
type EndedResumeParams struct {
	ID   string `json:"id"`
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
}

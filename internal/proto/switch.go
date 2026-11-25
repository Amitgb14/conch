package proto

import "time"

// CapAgentSwitch is switching a running agent's conversation to another
// agent in the same pane (#352): agent.switch, agent.switch.cancel and
// agent.switch.undo, the agent.switching and agent.switched events, and
// PaneInfo's SwitchedFrom.
const CapAgentSwitch = "agent.switch.v1"

const (
	// MethodAgentSwitch switches pane ID's conversation to agent To: the
	// conversation written as a session of To's own and resumed in the
	// same pane — same id, tab, branch and tree row — or, for an agent
	// conch writes no sessions for, To started there with the
	// conversation as a Markdown document for its first message. Never
	// mid-turn: without Now it waits for the turn to end, and for the
	// agent's background commands up to ten minutes; with Preview it
	// changes nothing and says what would cross.
	MethodAgentSwitch = "agent.switch"
	// MethodAgentSwitchCancel calls off a switch that is waiting.
	MethodAgentSwitchCancel = "agent.switch.cancel"
	// MethodAgentSwitchUndo removes the session files a switch wrote,
	// those the agent has not written to since.
	MethodAgentSwitchUndo = "agent.switch.undo"

	// EventAgentSwitching says a switch waits for the turn to end or for
	// background commands (AgentSwitching).
	EventAgentSwitching = "agent.switching"
	// EventAgentSwitched says a switch was made (AgentSwitchResult), or a
	// waiting one failed when its time came (its Error set).
	EventAgentSwitched = "agent.switched"
)

// How a conversation crossed (AgentSwitchResult.Crossed).
const (
	// SwitchNative: written as a session of the target's own, which it
	// resumed with the history.
	SwitchNative = "native"
	// SwitchHandoff: handed over as a Markdown document, the target's
	// first message.
	SwitchHandoff = "handoff"
)

// AgentSwitchParams switches pane ID to agent To.
type AgentSwitchParams struct {
	// ID is the pane, as agent.prompt names it (so scoping finds it).
	ID string `json:"id"`
	To string `json:"to"`
	// Preview says what would cross and how, changing nothing.
	Preview bool `json:"preview,omitempty"`
	// Now switches at once rather than waiting: still refused mid-turn,
	// and the agent's background commands stop with it.
	Now bool `json:"now,omitempty"`
	// Handoff hands the Markdown document over even where conch could
	// write the target's own session.
	Handoff bool `json:"handoff,omitempty"`
	// NoContinue leaves the resumed agent waiting rather than sending it
	// "continue".
	NoContinue bool `json:"no_continue,omitempty"`
	// Reason is said in the log and the event: "hand" when empty.
	Reason string `json:"reason,omitempty"`
}

// AgentSwitchResult is a switch made, waiting, or previewed.
type AgentSwitchResult struct {
	// Pane is the pane as it is now: running To once switched.
	Pane PaneInfo `json:"pane"`
	From string   `json:"from"`
	To   string   `json:"to"`
	// FromSession is the conversation switched, as From keeps it; Session
	// is To's session of it, when written natively.
	FromSession string `json:"from_session,omitempty"`
	Session     string `json:"session,omitempty"`
	// Crossed is SwitchNative or SwitchHandoff — for a preview, what it
	// would be — and Why says why a handoff, when it is one.
	Crossed string `json:"crossed,omitempty"`
	Why     string `json:"why,omitempty"`
	// Via says who wrote the native session: "codex import" (Codex's own
	// import of a Claude Code session) or "conch".
	Via string `json:"via,omitempty"`
	// Crossing is what crosses and what is left behind.
	Crossing *SwitchCrossing `json:"crossing,omitempty"`
	// Pending says the switch waits (Waits says for what) and is made
	// later, announced by agent.switched.
	Pending bool     `json:"pending,omitempty"`
	Waits   []string `json:"waits,omitempty"`
	Preview bool     `json:"preview,omitempty"`
	// Record names what the switch wrote, for agent.switch.undo; Document
	// is the handoff document's path.
	Record   string `json:"record,omitempty"`
	Document string `json:"document,omitempty"`
	// Error is why a waiting switch failed when its time came (only in
	// the agent.switched event).
	Error string `json:"error,omitempty"`
}

// SwitchCrossing is what a switch carries and leaves behind.
type SwitchCrossing struct {
	Messages  int      `json:"messages"`
	Commands  int      `json:"commands"`
	Notes     int      `json:"notes"`
	Shortened int      `json:"shortened,omitempty"`
	Fits      bool     `json:"fits"`
	Bytes     int      `json:"bytes"`
	Plan      bool     `json:"plan,omitempty"`
	Left      []string `json:"left,omitempty"`
}

// SwitchOrigin is where a pane's conversation was switched from.
type SwitchOrigin struct {
	Agent   string    `json:"agent"`
	Session string    `json:"session,omitempty"`
	At      time.Time `json:"at"`
}

// AgentSwitching is a switch waiting for its moment.
type AgentSwitching struct {
	Pane  string   `json:"pane"`
	From  string   `json:"from"`
	To    string   `json:"to"`
	Waits []string `json:"waits,omitempty"`
}

// AgentSwitchCancelParams calls off pane ID's waiting switch.
type AgentSwitchCancelParams struct {
	ID string `json:"id"`
}

// AgentSwitchUndoParams undoes the switch Record names.
type AgentSwitchUndoParams struct {
	Record string `json:"record"`
}

// AgentSwitchUndoResult says which files went and which stayed, having
// been written to since.
type AgentSwitchUndoResult struct {
	Removed []string `json:"removed,omitempty"`
	Kept    []string `json:"kept,omitempty"`
}

package proto

import "time"

// CapAgentSwitch is switching a running agent's conversation to another
// agent in the same pane (#352): agent.switch, agent.switch.cancel and
// agent.switch.undo, the agent.switching and agent.switched events, and
// PaneInfo's SwitchedFrom.
const CapAgentSwitch = "agent.switch.v1"

// CapAgentSwitchLimit is a switch offered at a plan limit: when an agent
// stops at its limit and no account of that agent has room, the server
// says so with agent.switch.offered, and with [agents] switch_at_limit
// naming an agent it switches by itself once the offer's countdown runs
// out (AgentSwitchOffer.Auto). agent.switch.cancel calls the offer off.
// A server without it offers only the account move.
const CapAgentSwitchLimit = "agent.switch.limit.v1"

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
	// EventAgentSwitchOffered says an agent is out of plan on every
	// account and could carry on as another agent (AgentSwitchOffer), or
	// that an offer made earlier is gone (its Gone set).
	EventAgentSwitchOffered = "agent.switch.offered"
)

// What [agents] switch_at_limit says besides an agent's name.
const (
	// SwitchAtLimitAsk (empty) offers the switch and waits to be told.
	SwitchAtLimitAsk = ""
	// SwitchAtLimitWait offers nothing: the agent waits for its reset.
	SwitchAtLimitWait = "wait"
)

// How a conversation crossed (AgentSwitchResult.Crossed).
const (
	// SwitchNative: written as a session of the target's own, which it
	// resumed with the history.
	SwitchNative = "native"
	// SwitchHandoff: handed over as a Markdown document, the target's
	// first message.
	SwitchHandoff = "handoff"
	// SwitchNever: an agent a conversation cannot be switched to, since
	// it cannot be given a first message (AgentAvailability.SwitchTo).
	SwitchNever = "never"
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
	// Reason is why it was made: "limit" for one the server made by
	// itself at a plan limit (CapAgentSwitchLimit); empty by hand.
	Reason string `json:"reason,omitempty"`
}

// AgentSwitchOffer is a switch offered because an agent is out of plan on
// every account it has (CapAgentSwitchLimit).
type AgentSwitchOffer struct {
	Pane string `json:"pane"`
	// From is the agent that ran out; To the one offered, empty when no
	// agent here can take the conversation (Why says why).
	From string `json:"from"`
	To   string `json:"to,omitempty"`
	// Crossed is how it would cross: SwitchNative or SwitchHandoff.
	Crossed string `json:"crossed,omitempty"`
	// Accounts is From's accounts as conch weighs them, each full, for a
	// client to say how full; ResetsAt the soonest any of them turns over.
	Accounts []AccountRank `json:"accounts,omitempty"`
	ResetsAt time.Time     `json:"resets_at,omitempty"`
	// Auto says the server switches by itself at At unless called off
	// (agent.switch.cancel): switch_at_limit names To.
	Auto bool      `json:"auto,omitempty"`
	At   time.Time `json:"at,omitempty"`
	// Setting is switch_at_limit as the server read it, and Why says why
	// the switch is not made by itself though it names an agent (that
	// agent not installed, out of plan too, unable to take a first
	// message), or why nothing is offered.
	Setting string `json:"setting,omitempty"`
	Why     string `json:"why,omitempty"`
	// Gone says the offer is withdrawn: the agent worked again, switched,
	// or the offer was called off.
	Gone bool `json:"gone,omitempty"`
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
	// Crossing is what the switch carried, for a client to say beside
	// Switch back; absent from a server before it was kept.
	Crossing *SwitchCrossing `json:"crossing,omitempty"`
	// Back says the switch went back to the agent the conversation had
	// been switched from: the round trip is over, so a client offers no
	// Switch back for it.
	Back bool `json:"back,omitempty"`
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

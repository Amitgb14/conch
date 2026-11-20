package proto

import "time"

// CapAccounts is account.*: an agent's accounts besides its own — a work
// and a personal subscription — each a configuration folder of the
// agent's, pinned to projects, worktrees and tasks, with new agents moved
// to the next account when one passes its plan limit. It also means
// pane.create, task.create and group.task take Account, and PaneInfo,
// PlanLimits and SessionInfo say which account they are. A server without
// it starts every agent on its own account.
const CapAccounts = "agent.accounts.v1"

// CapAccountFailoverAgent is a failover line per agent: AccountFailoverParams
// with an Agent, and AccountList.FailoverFor. Without it a server keeps
// one line for every agent and would take an Agent's line as everyone's.
const CapAccountFailoverAgent = "account.failover.agent.v1"

// Account methods.
const (
	// MethodAccountList lists the accounts, the pins and the failover line.
	MethodAccountList = "account.list"
	// MethodAccountAdd makes an account and its folder.
	MethodAccountAdd = "account.add"
	// MethodAccountRemove forgets an account, leaving its folder.
	MethodAccountRemove = "account.remove"
	// MethodAccountPin pins a folder to an account, or unpins it.
	MethodAccountPin = "account.pin"
	// MethodAccountFailover sets the failover line.
	MethodAccountFailover = "account.failover"
)

// AccountInfo is one account. The agent's own is listed too, with Name ""
// and Default set, so its limits sit beside the others'.
type AccountInfo struct {
	Agent   string `json:"agent"`
	Name    string `json:"name"`
	Dir     string `json:"dir,omitempty"`
	Default bool   `json:"default,omitempty"`
	// Limits is what the account's agents last reported of its plan.
	Limits *PlanLimits `json:"limits,omitempty"`
	// Agents is how many agents run on it now.
	Agents int `json:"agents,omitempty"`
	// SignedIn says whether the account has been signed in to, from the
	// record the agent writes when it is (never its token); absent when
	// the server cannot tell, or is older.
	SignedIn *bool `json:"signed_in,omitempty"`
}

// AccountPin is a folder whose agents start on an account by that name.
type AccountPin struct {
	Path    string `json:"path"`
	Account string `json:"account"`
}

// AccountList is the result of account.list. Agents names the agents that
// can have accounts; the others keep their own.
type AccountList struct {
	Accounts   []AccountInfo `json:"accounts"`
	Pins       []AccountPin  `json:"pins,omitempty"`
	FailoverAt int           `json:"failover_at,omitempty"`
	Agents     []string      `json:"agents,omitempty"`
	// FailoverFor is each agent's own line, where one has one
	// (CapAccountFailoverAgent); the rest follow FailoverAt.
	FailoverFor map[string]int `json:"failover_for,omitempty"`
}

// AccountAddParams makes Agent an account called Name, in Dir or a folder
// of conch's.
type AccountAddParams struct {
	Agent string `json:"agent"`
	Name  string `json:"name"`
	Dir   string `json:"dir,omitempty"`
}

// AccountRef names an account.
type AccountRef struct {
	Agent string `json:"agent"`
	Name  string `json:"name"`
}

// AccountPinParams pins Path to Account; "" unpins it.
type AccountPinParams struct {
	Path    string `json:"path"`
	Account string `json:"account,omitempty"`
}

// AccountFailoverParams sets the failover line, in percent; 0 is off.
type AccountFailoverParams struct {
	At int `json:"at"`
	// Agent sets that agent's own line rather than every agent's
	// (CapAccountFailoverAgent); with Shared it takes the agent's own line
	// away, so it follows every agent's again, and At is not read.
	Agent  string `json:"agent,omitempty"`
	Shared bool   `json:"shared,omitempty"`
}

// CapAgentMove is moving a running agent to another of its accounts:
// agent.move, agent.move.cancel and account.suggest, the agent.moving and
// agent.moved events, and PaneInfo's MovedFrom and Replaces.
const CapAgentMove = "agent.move.v1"

const (
	// MethodAgentMove moves an agent to another account: its conversation
	// copied into that account's folder and resumed there, in a new pane
	// that replaces the old one.
	MethodAgentMove = "agent.move"
	// MethodAgentMoveCancel calls off a move the server has scheduled
	// (agent.moving), and does not schedule one for that agent again until
	// it has worked.
	MethodAgentMoveCancel = "agent.move.cancel"
	// MethodAccountSuggest ranks an agent's accounts by room, the one conch
	// would pick first.
	MethodAccountSuggest = "account.suggest"

	// EventAgentMoving says the server will move an agent at At unless
	// told not to (agent.move.cancel).
	EventAgentMoving = "agent.moving"
	// EventAgentMoved says an agent moved: Pane is the new pane, Replaces
	// the one it replaced.
	EventAgentMoved = "agent.moved"
)

// AgentMoveParams moves pane ID's agent to Account ("default" for the agent's
// own); with no Account, conch chooses. Continue sends "continue" once it
// is back, as for an agent its limit stopped.
type AgentMoveParams struct {
	// ID is the pane, as agent.prompt names it (so scoping finds it).
	ID       string `json:"id"`
	Account  string `json:"account,omitempty"`
	Continue bool   `json:"continue,omitempty"`
	// Reason is said in the log and the event: "hand", "limit", "line" or
	// "back"; "hand" when empty.
	Reason string `json:"reason,omitempty"`
	// Force moves it even with background commands running, which stop
	// with it; without it, such a move is refused and they are named.
	Force bool `json:"force,omitempty"`
}

// AgentMoveCancelParams calls off pane ID's scheduled move.
type AgentMoveCancelParams struct {
	ID string `json:"id"`
}

// AgentMoving is a move the server has scheduled.
type AgentMoving struct {
	Pane   string    `json:"pane"`
	Agent  string    `json:"agent"`
	From   string    `json:"from"`
	To     string    `json:"to"`
	At     time.Time `json:"at"`
	Reason string    `json:"reason"`
	// Stops is the background commands that stop with it, when the server
	// waited for them and they went on running.
	Stops []string `json:"stops,omitempty"`
}

// AgentMoved is a move done.
type AgentMoved struct {
	Pane     string `json:"pane"`
	Replaces string `json:"replaces"`
	Agent    string `json:"agent"`
	From     string `json:"from"`
	To       string `json:"to"`
	Reason   string `json:"reason"`
}

// AccountSuggestParams asks for Agent's accounts ranked; Exclude is the
// one it is on, left out.
type AccountSuggestParams struct {
	Agent   string `json:"agent"`
	Exclude string `json:"exclude,omitempty"`
}

// AccountRank is one account as conch weighs it for a move.
type AccountRank struct {
	// Account is "" for the agent's own.
	Account string `json:"account"`
	// Room is what is left of its fullest live window, 0–100; with
	// Reported false it has said nothing and counts as room.
	Room     float64 `json:"room"`
	Reported bool    `json:"reported,omitempty"`
	SignedIn *bool   `json:"signed_in,omitempty"`
	Agents   int     `json:"agents,omitempty"`
	// ResetsAt is when its fullest window turns over, when it said.
	ResetsAt time.Time `json:"resets_at,omitempty"`
	// Usable is whether a move to it would go: signed in (or not known
	// otherwise) and with room. Why says why not, or what it has.
	Usable bool   `json:"usable"`
	Why    string `json:"why,omitempty"`
}

// AccountSuggestion is an agent's accounts, the pick first.
type AccountSuggestion struct {
	Agent    string        `json:"agent"`
	Accounts []AccountRank `json:"accounts"`
}

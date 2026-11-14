package proto

// CapAccounts is account.*: an agent's accounts besides its own — a work
// and a personal subscription — each a configuration folder of the
// agent's, pinned to projects, worktrees and tasks, with new agents moved
// to the next account when one passes its plan limit. It also means
// pane.create, task.create and group.task take Account, and PaneInfo,
// PlanLimits and SessionInfo say which account they are. A server without
// it starts every agent on its own account.
const CapAccounts = "agent.accounts.v1"

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
}

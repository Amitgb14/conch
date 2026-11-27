package acp

import "encoding/json"

// The protocol's messages, as much of them as conch reads or writes.
// Field names are the protocol's (camelCase); anything conch does not
// read is left out, and unknown fields an agent sends are ignored.

// Methods conch calls on the agent.
const (
	MethodInitialize   = "initialize"
	MethodSessionNew   = "session/new"
	MethodSessionLoad  = "session/load"
	MethodSessionList  = "session/list"
	MethodPrompt       = "session/prompt"
	MethodCancel       = "session/cancel"
	MethodUpdate       = "session/update"
	MethodPermission   = "session/request_permission"
	MethodReadFile     = "fs/read_text_file"
	MethodWriteFile    = "fs/write_text_file"
	MethodTermCreate   = "terminal/create"
	MethodTermOutput   = "terminal/output"
	MethodTermWait     = "terminal/wait_for_exit"
	MethodTermKill     = "terminal/kill"
	MethodTermRelease  = "terminal/release"
	MethodAuthenticate = "authenticate"
)

// Stop reasons a prompt turn ends with.
const (
	StopEndTurn   = "end_turn"
	StopMaxTokens = "max_tokens"
	StopMaxTurns  = "max_turn_requests"
	StopRefusal   = "refusal"
	StopCancelled = "cancelled"
)

type Info struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version,omitempty"`
}

type FSCapabilities struct {
	ReadTextFile  bool `json:"readTextFile"`
	WriteTextFile bool `json:"writeTextFile"`
}

type ClientCapabilities struct {
	FS       FSCapabilities `json:"fs"`
	Terminal bool           `json:"terminal"`
}

type InitializeParams struct {
	ProtocolVersion    int                `json:"protocolVersion"`
	ClientCapabilities ClientCapabilities `json:"clientCapabilities"`
	ClientInfo         *Info              `json:"clientInfo,omitempty"`
}

type SessionCapabilities struct {
	// List is an object when the agent can list its sessions; absent,
	// it cannot — loadSession alone reopens an id somebody already has.
	List *json.RawMessage `json:"list,omitempty"`
}

type AgentCapabilities struct {
	LoadSession         bool                `json:"loadSession"`
	SessionCapabilities SessionCapabilities `json:"sessionCapabilities"`
}

type AuthMethod struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type InitializeResult struct {
	ProtocolVersion   int               `json:"protocolVersion"`
	AgentCapabilities AgentCapabilities `json:"agentCapabilities"`
	AgentInfo         *Info             `json:"agentInfo,omitempty"`
	AuthMethods       []AuthMethod      `json:"authMethods,omitempty"`
}

// CanList is whether the agent lists its sessions (session/list).
func (r InitializeResult) CanList() bool {
	return r.AgentCapabilities.SessionCapabilities.List != nil
}

type NewSessionParams struct {
	Cwd        string `json:"cwd"`
	MCPServers []any  `json:"mcpServers"`
}

type NewSessionResult struct {
	SessionID string `json:"sessionId"`
}

type LoadSessionParams struct {
	SessionID  string `json:"sessionId"`
	Cwd        string `json:"cwd"`
	MCPServers []any  `json:"mcpServers"`
}

type ListSessionsParams struct {
	Cwd    string `json:"cwd,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}

type SessionInfo struct {
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
	Title     string `json:"title,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

type ListSessionsResult struct {
	Sessions   []SessionInfo `json:"sessions"`
	NextCursor string        `json:"nextCursor,omitempty"`
}

// ContentBlock is text, or another kind conch shows by its type.
type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	URI  string `json:"uri,omitempty"`
	Name string `json:"name,omitempty"`
}

type PromptParams struct {
	SessionID string         `json:"sessionId"`
	Prompt    []ContentBlock `json:"prompt"`
}

type PromptResult struct {
	StopReason string `json:"stopReason"`
}

type CancelParams struct {
	SessionID string `json:"sessionId"`
}

// ToolCallContent is what a tool call shows: content, a diff or a
// terminal.
type ToolCallContent struct {
	Type       string        `json:"type"`
	Content    *ContentBlock `json:"content,omitempty"`
	Path       string        `json:"path,omitempty"`
	OldText    *string       `json:"oldText,omitempty"`
	NewText    string        `json:"newText,omitempty"`
	TerminalID string        `json:"terminalId,omitempty"`
}

type PlanEntry struct {
	Content  string `json:"content"`
	Priority string `json:"priority,omitempty"`
	Status   string `json:"status,omitempty"`
}

// Update is one session/update's update: SessionUpdate says which kind,
// and the fields that kind uses are set.
type Update struct {
	SessionUpdate string            `json:"sessionUpdate"`
	Content       json.RawMessage   `json:"content,omitempty"` // a block, or a tool call's list
	ToolCallID    string            `json:"toolCallId,omitempty"`
	Title         *string           `json:"title,omitempty"`
	Kind          string            `json:"kind,omitempty"`
	Status        string            `json:"status,omitempty"`
	Entries       []PlanEntry       `json:"entries,omitempty"`
	Locations     []json.RawMessage `json:"locations,omitempty"`
}

type UpdateParams struct {
	SessionID string `json:"sessionId"`
	Update    Update `json:"update"`
}

// PermissionOption is one answer to a permission request.
type PermissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"` // allow_once, allow_always, reject_once, reject_always
}

type PermissionToolCall struct {
	ToolCallID string            `json:"toolCallId"`
	Title      *string           `json:"title,omitempty"`
	Kind       string            `json:"kind,omitempty"`
	Content    []ToolCallContent `json:"content,omitempty"`
}

type PermissionParams struct {
	SessionID string             `json:"sessionId"`
	ToolCall  PermissionToolCall `json:"toolCall"`
	Options   []PermissionOption `json:"options"`
}

type PermissionOutcome struct {
	Outcome  string `json:"outcome"` // selected, cancelled
	OptionID string `json:"optionId,omitempty"`
}

type PermissionResult struct {
	Outcome PermissionOutcome `json:"outcome"`
}

type ReadFileParams struct {
	SessionID string `json:"sessionId"`
	Path      string `json:"path"`
	Line      int    `json:"line,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

type ReadFileResult struct {
	Content string `json:"content"`
}

type WriteFileParams struct {
	SessionID string `json:"sessionId"`
	Path      string `json:"path"`
	Content   string `json:"content"`
}

type EnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type TermCreateParams struct {
	SessionID       string   `json:"sessionId"`
	Command         string   `json:"command"`
	Args            []string `json:"args,omitempty"`
	Env             []EnvVar `json:"env,omitempty"`
	Cwd             string   `json:"cwd,omitempty"`
	OutputByteLimit int      `json:"outputByteLimit,omitempty"`
}

type TermCreateResult struct {
	TerminalID string `json:"terminalId"`
}

type TermParams struct {
	SessionID  string `json:"sessionId"`
	TerminalID string `json:"terminalId"`
}

type ExitStatus struct {
	ExitCode *int    `json:"exitCode"`
	Signal   *string `json:"signal"`
}

type TermOutputResult struct {
	Output     string      `json:"output"`
	Truncated  bool        `json:"truncated"`
	ExitStatus *ExitStatus `json:"exitStatus,omitempty"`
}

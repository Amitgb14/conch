// Package phone is the gateway `conch web` runs: HTTP and a WebSocket for a
// phone, in front of the local conch server, which it talks to as a client
// like the TUI does.
//
// The types in this file mirror the contract in docs/plans/phone-api.md one
// to one; change that file first when a shape here has to change.
package phone

import "time"

// APIVersion is what hello reports, so a UI cached from an older build can
// see that it is out of date. 2 is machines: a pane ID is machine:pane,
// which is not something an older app can read.
const APIVersion = 2

// CookieName carries the device token.
const CookieName = "conch_device"

// CSRFHeader carries hello's csrf_token on state-changing requests.
const CSRFHeader = "X-CSRF-Token"

// LocalMachine is this computer, which is always listed and always first.
const LocalMachine = "local"

// Machine states, as the phone sees them.
const (
	MachineOnline     = "online"
	MachineConnecting = "connecting"
	MachineOffline    = "offline"
)

// Machine is one machine the gateway reaches. Agents counts what the list
// carries for it, so the app can show a machine with none without counting
// them itself; Detail says why an offline one is offline, for people.
type Machine struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	State  string `json:"state"`
	Agents int    `json:"agents"`
	Detail string `json:"detail,omitempty"`
}

// MachineList is GET /api/machines and the socket's "machines".
type MachineList struct {
	Machines []Machine `json:"machines"`
}

// Permissions, lowest first. A device with a higher one can do everything
// a lower one can.
const (
	PermView  = "view"
	PermReply = "reply"
	PermFull  = "full"
)

// permRank orders the permissions; 0 is none.
func permRank(p string) int {
	switch p {
	case PermView:
		return 1
	case PermReply:
		return 2
	case PermFull:
		return 3
	}
	return 0
}

// ValidPermission reports whether p names a permission.
func ValidPermission(p string) bool { return permRank(p) > 0 }

// allows reports whether a device with permission have may do what needs
// need. An unknown permission on either side allows nothing.
func allows(have, need string) bool {
	return permRank(have) > 0 && permRank(need) > 0 && permRank(have) >= permRank(need)
}

// Error codes. Clients act on the code; the message is for people.
const (
	CodeBadRequest        = "bad_request"
	CodeUnauthorized      = "unauthorized"
	CodeForbidden         = "forbidden"
	CodeNotFound          = "not_found"
	CodeAgentBlocked      = "agent_blocked"
	CodeNotWaiting        = "not_waiting"
	CodeQuestionChanged   = "question_changed"
	CodeNoChoices         = "no_choices"
	CodePairExpired       = "pair_expired"
	CodeRateLimited       = "rate_limited"
	CodeServerUnavailable = "server_unavailable"
)

// httpStatus is the status each code travels with.
var httpStatus = map[string]int{
	CodeBadRequest:        400,
	CodeUnauthorized:      401,
	CodeForbidden:         403,
	CodeNotFound:          404,
	CodeAgentBlocked:      409,
	CodeNotWaiting:        409,
	CodeQuestionChanged:   409,
	CodeNoChoices:         422,
	CodePairExpired:       410,
	CodeRateLimited:       429,
	CodeServerUnavailable: 503,
}

// APIError is the one shape every error has, over HTTP and the socket.
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *APIError) Error() string { return e.Code + ": " + e.Message }

// ErrorBody wraps an APIError as it is sent.
type ErrorBody struct {
	Error *APIError `json:"error"`
}

// Agent states as the phone sees them: the server's "blocked" is sent as
// "waiting", the word the TUI uses.
const (
	StateWorking = "working"
	StateWaiting = "waiting"
	StateDone    = "done"
	StateIdle    = "idle"
)

// ProjectRef is the project an agent works in.
type ProjectRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Agent is one agent, as the list and its updates carry it.
type Agent struct {
	Machine   string      `json:"machine"`
	Pane      string      `json:"pane"`
	Name      string      `json:"name"`
	Project   *ProjectRef `json:"project,omitempty"`
	Branch    string      `json:"branch,omitempty"`
	Agent     string      `json:"agent"`
	State     string      `json:"state"`
	Since     time.Time   `json:"since"`
	Title     string      `json:"title,omitempty"`
	CreatedBy string      `json:"created_by,omitempty"`
	CostUSD   float64     `json:"cost_usd,omitempty"`
	Failed    bool        `json:"failed"`
	// Question is there only while State is waiting.
	Question *Question `json:"question,omitempty"`
}

// Question is what a waiting agent asks. Choices is empty when they
// couldn't be read off the screen; the phone then offers only the terminal.
type Question struct {
	ID      string   `json:"id"`
	Text    string   `json:"text"`
	Choices []Choice `json:"choices"`
}

// Choice is one answer to a question.
type Choice struct {
	Choice  string `json:"choice"`
	Label   string `json:"label"`
	Default bool   `json:"default,omitempty"`
}

// Kinds of pane.
const (
	KindAgent    = "agent"
	KindTerminal = "terminal"
)

// Pane is any pane, an agent's or a plain terminal's: the Agent object
// with its kind and directory. A terminal's agent is "", its state idle,
// and since is when it started.
type Pane struct {
	Agent
	Kind string `json:"kind"`
	Cwd  string `json:"cwd,omitempty"`
}

// PaneList is GET /api/panes and the socket's "panes".
type PaneList struct {
	Panes []Pane `json:"panes"`
}

// NewPaneRequest is POST /api/panes: a terminal, or an agent with an
// optional first message, in a project's folder or the home folder.
type NewPaneRequest struct {
	Kind string `json:"kind"`
	// Machine is where to start it; "" is this computer. A project ID is
	// that machine's own.
	Machine string `json:"machine,omitempty"`
	Project string `json:"project,omitempty"`
	Agent   string `json:"agent,omitempty"`
	Name    string `json:"name,omitempty"`
	Prompt  string `json:"prompt,omitempty"`
}

// NewPaneResponse is the pane started.
type NewPaneResponse struct {
	Pane string `json:"pane"`
}

// CloseRequest is POST /api/close.
type CloseRequest struct {
	Pane string `json:"pane"`
}

// CloseResponse says the pane is closed.
type CloseResponse struct {
	Pane   string `json:"pane"`
	Closed bool   `json:"closed"`
}

// RenameRequest is POST /api/rename, and its response.
type RenameRequest struct {
	Pane string `json:"pane"`
	Name string `json:"name"`
}

// Frame is a pane's screen as the server renders it: a snapshot, drawn
// whole each time.
type Frame struct {
	Pane      string   `json:"pane"`
	Cols      int      `json:"cols"`
	Rows      int      `json:"rows"`
	Lines     []string `json:"lines"`
	Offset    int      `json:"offset"`
	History   int      `json:"history"`
	AltScreen bool     `json:"alt_screen"`
	// Mouse says the program asked for mouse input: the wheel scrolls its
	// own view (an agent's conversation) rather than the pane's history.
	Mouse bool `json:"mouse"`
}

// Project is a project a task can be started in.
type Project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
	Base string `json:"base,omitempty"`
}

// PairRequest is POST /pair.
type PairRequest struct {
	Code       string `json:"code"`
	DeviceName string `json:"device_name"`
}

// PairResponse answers it, along with the cookie.
type PairResponse struct {
	DeviceID   string `json:"device_id"`
	Permission string `json:"permission"`
}

// Hello is GET /api/hello.
type Hello struct {
	APIVersion   int    `json:"api_version"`
	ConchVersion string `json:"conch_version"`
	DeviceID     string `json:"device_id"`
	Permission   string `json:"permission"`
	CSRFToken    string `json:"csrf_token"`
	// Machines is every machine the gateway reaches, local first, so the
	// app can draw them before asking for anything else.
	Machines []Machine `json:"machines"`
}

// AgentList is GET /api/agents and the socket's "agents".
type AgentList struct {
	Agents []Agent `json:"agents"`
}

// ProjectList is GET /api/projects.
type ProjectList struct {
	Projects []Project `json:"projects"`
}

// ReplyRequest is POST /api/reply.
type ReplyRequest struct {
	Pane string `json:"pane"`
	Text string `json:"text"`
}

// ReplyResponse says which turn of the agent answers the reply.
type ReplyResponse struct {
	Pane  string `json:"pane"`
	Agent string `json:"agent"`
	Turn  int    `json:"turn"`
}

// AnswerRequest is POST /api/answer: a choice, and the question it answers.
type AnswerRequest struct {
	Pane       string `json:"pane"`
	QuestionID string `json:"question_id"`
	Choice     string `json:"choice"`
}

// AnswerResponse says the choice's keys went in.
type AnswerResponse struct {
	Pane string `json:"pane"`
	Sent bool   `json:"sent"`
}

// TaskRequest is POST /api/task. Machine is where to start it; "" is this
// computer, and the project is that machine's own..
type TaskRequest struct {
	Machine string `json:"machine,omitempty"`
	Project string `json:"project"`
	Agent   string `json:"agent,omitempty"`
	Name    string `json:"name,omitempty"`
	Prompt  string `json:"prompt"`
}

// TaskResponse is the pane the task started in.
type TaskResponse struct {
	Pane     string `json:"pane"`
	Worktree string `json:"worktree,omitempty"`
	Branch   string `json:"branch,omitempty"`
}

// PushKey is GET /api/push/key.
type PushKey struct {
	VAPIDPublicKey string `json:"vapid_public_key"`
}

// PushKeys are a push subscription's keys, as the browser gives them.
type PushKeys struct {
	P256dh string `json:"p256dh"`
	Auth   string `json:"auth"`
}

// PushSubscription is POST /api/push/subscribe; DELETE names the endpoint
// alone.
type PushSubscription struct {
	Endpoint string   `json:"endpoint"`
	Keys     PushKeys `json:"keys"`
	On       []string `json:"on,omitempty"`
}

// Socket message types.
const (
	MsgAgentsWatch = "agents.watch"
	MsgFrameOpen   = "frame.open"
	MsgFrameClose  = "frame.close"
	MsgKeys        = "keys"
	MsgPing        = "ping"
	MsgPanesWatch  = "panes.watch"
	MsgText        = "text"
	MsgScroll      = "scroll"
	MsgResize      = "resize"
	MsgWheel       = "wheel"

	MsgAgents    = "agents"
	MsgAgent     = "agent"
	MsgAgentGone = "agent.gone"
	MsgFrame     = "frame"
	MsgError     = "error"
	MsgPong      = "pong"
	MsgBye       = "bye"

	MsgMachines    = "machines"
	MsgPanes       = "panes"
	MsgPaneChanged = "pane.changed"
	MsgPaneGone    = "pane.gone"
)

// Reasons a socket is closed for.
const (
	ByeRevoked      = "revoked"
	ByeServerGone   = "server_gone"
	ByeShuttingDown = "shutting_down"
)

// ClientMessage is what the phone sends on the socket. ID, when given, is
// repeated by the answer.
type ClientMessage struct {
	Type string   `json:"type"`
	ID   string   `json:"id,omitempty"`
	Pane string   `json:"pane,omitempty"`
	Keys []string `json:"keys,omitempty"`
	Text string   `json:"text,omitempty"`
	// Offset is how many lines back into history a scroll goes; 0 is live.
	Offset int `json:"offset,omitempty"`
	// Direction and Count are a wheel's: up or down, that many steps.
	Direction string `json:"direction,omitempty"`
	Count     int    `json:"count,omitempty"`
	// Cols and Rows resize the pane itself, for a window that can show
	// more than the laptop gave it.
	Cols int `json:"cols,omitempty"`
	Rows int `json:"rows,omitempty"`
}

// ServerMessage is what the gateway sends on the socket; Type says which
// of the other fields are there.
type ServerMessage struct {
	Type     string     `json:"type"`
	ID       string     `json:"id,omitempty"`
	Agents   *[]Agent   `json:"agents,omitempty"`
	Machines *[]Machine `json:"machines,omitempty"`
	Panes    *[]Pane    `json:"panes,omitempty"`
	Info     *Pane      `json:"info,omitempty"`
	Agent    *Agent     `json:"agent,omitempty"`
	Pane     string     `json:"pane,omitempty"`
	Frame    *Frame     `json:"frame,omitempty"`
	Error    *APIError  `json:"error,omitempty"`
	Reason   string     `json:"reason,omitempty"`
}

// socketNeeds is the permission each socket message needs. A type that
// isn't here is refused.
var socketNeeds = map[string]string{
	MsgAgentsWatch: PermView,
	MsgFrameOpen:   PermView,
	MsgFrameClose:  PermView,
	MsgKeys:        PermFull,
	MsgPing:        PermView,
	MsgPanesWatch:  PermView,
	MsgText:        PermFull,
	MsgScroll:      PermView,
	MsgResize:      PermReply,
	MsgWheel:       PermReply,
}

// Resize bounds. A pane may be made as small as a phone and as large as a
// wide screen, and no further: a program given a thousand columns draws a
// thousand columns, and every client watching pays for them.
const (
	MinPaneCols, MaxPaneCols = 20, 500
	MinPaneRows, MaxPaneRows = 5, 200
)

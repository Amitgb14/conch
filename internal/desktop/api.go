package desktop

import (
	"encoding/json"
	"time"
)

// The bridge's wire format: what `conch desktop` says to a desktop app
// and what the app says back, one JSON object a line on the bridge's
// stdin and stdout (docs/plans/desktop-windows-linux.md). Three apps are
// written against it — Conch.app, the Linux app, the Windows app — in
// three languages, so its shapes are a contract: TestBridgeContract
// pins them, a field once released stays, and anything new is optional
// and announced by a capability.

// Protocol is the bridge's version: raised only for a change an older
// app could not read. New messages and fields come with a capability.
const Protocol = 1

// Capabilities are what this bridge offers, said in its hello.
var Capabilities = []string{CapBridge}

// CapBridge is the first set: hello, snapshot, patch, notify, and the
// intents pane.new, pane.close and pane.attach.
const CapBridge = "desktop.v1"

// Message types.
const (
	TypeHello    = "hello"    // both ways, first
	TypeSnapshot = "snapshot" // bridge → app: the whole State
	TypePatch    = "patch"    // bridge → app: a Patch on the last state
	TypeNotify   = "notify"   // bridge → app: something to tell the person
	TypeIntent   = "intent"   // app → bridge: do something
	TypeResult   = "result"   // bridge → app: how an intent went
)

// Intents an app may send.
const (
	IntentPaneNew    = "pane.new"
	IntentPaneClose  = "pane.close"
	IntentPaneAttach = "pane.attach"
)

// Message is one line either way. Which fields are set depends on Type:
// an intent has ID, Intent and Data; its result the same ID, OK, and
// Error or Data; a snapshot or patch has Seq and Data. A result and the
// patch its intent causes come in either order, so an app goes by the
// patch for the state and by the result only for success or the error.
type Message struct {
	Type   string          `json:"type"`
	ID     string          `json:"id,omitempty"`
	Seq    int             `json:"seq,omitempty"`
	Intent string          `json:"intent,omitempty"`
	OK     bool            `json:"ok,omitempty"`
	Error  string          `json:"error,omitempty"`
	Data   json.RawMessage `json:"data,omitempty"`
}

// AppHello is what an app says first.
type AppHello struct {
	App     string `json:"app"`               // "conch-linux", "conch-windows", "conch-mac"
	Version string `json:"version,omitempty"` // the app's own
	OS      string `json:"os,omitempty"`
	// Raw says the app's terminal draws the program's own output, so
	// attach is given -raw (Ghostty does; VTE and most widgets do not).
	Raw bool `json:"raw,omitempty"`
	// Wrap goes in front of every command the bridge hands back to run,
	// because the app may not run where the bridge does: a Windows app
	// whose bridge is in WSL sends ["wsl.exe", "-d", "Ubuntu"].
	Wrap []string `json:"wrap,omitempty"`
}

// BridgeHello is the bridge's answer.
type BridgeHello struct {
	Protocol     int      `json:"protocol"`
	Conch        string   `json:"conch"` // the conch version the bridge is
	Capabilities []string `json:"capabilities"`
}

// State is everything an app draws its tree from.
type State struct {
	Machines []Machine `json:"machines"`
	Projects []Project `json:"projects"`
	Panes    []Pane    `json:"panes"`
}

// Machine is this computer or one of the catalog's.
type Machine struct {
	ID     string `json:"id"` // "local" for this computer
	Label  string `json:"label"`
	State  string `json:"state"`            // online, connecting, offline
	Detail string `json:"detail,omitempty"` // why it is not online
}

// Project is a project on a machine. Key is unique across machines.
type Project struct {
	Key     string `json:"key"` // machine:id
	Machine string `json:"machine"`
	ID      string `json:"id"`
	Name    string `json:"name"`
	Path    string `json:"path"`
}

// Pane is a terminal on a machine, with the agent in it if there is one.
// Key is unique across machines and is what intents name.
type Pane struct {
	Key      string `json:"key"` // machine:id
	Machine  string `json:"machine"`
	ID       string `json:"id"`
	Name     string `json:"name"`
	Title    string `json:"title,omitempty"`
	Project  string `json:"project,omitempty"` // a Project's Key
	Branch   string `json:"branch,omitempty"`
	State    string `json:"state"` // running, exited
	ExitCode int    `json:"exit_code,omitempty"`
	Agent    *Agent `json:"agent,omitempty"`
}

// Agent is the agent detected in a pane.
type Agent struct {
	Name   string    `json:"name"`
	State  string    `json:"state"` // idle, working, waiting, done
	Since  time.Time `json:"since"`
	Failed bool      `json:"failed,omitempty"`
}

// Agent states, in the app's words.
const (
	AgentIdle    = "idle"
	AgentWorking = "working"
	AgentWaiting = "waiting"
	AgentDone    = "done"
)

// Patch is what changed since the last snapshot or patch: items set
// (added or changed, whole) and keys removed, per list. An app applies
// it by replacing each item set where it already is, adding a new one at
// the end, then dropping the keys removed. A machine that goes offline
// takes its projects and panes with it, since they can no longer be
// known. testdata/session.jsonl is a recorded session and
// testdata/final.json the state it ends in, for an app's own tests.
type Patch struct {
	Machines *Changes[Machine] `json:"machines,omitempty"`
	Projects *Changes[Project] `json:"projects,omitempty"`
	Panes    *Changes[Pane]    `json:"panes,omitempty"`
}

// Changes is one list's part of a patch.
type Changes[T any] struct {
	Set    []T      `json:"set,omitempty"`
	Remove []string `json:"remove,omitempty"`
}

// Notify is something the person should hear about: an agent that
// started waiting, finished, or failed. The app decides how to say it.
type Notify struct {
	Kind  string `json:"kind"` // waiting, done, failed
	Pane  string `json:"pane"` // a Pane's Key
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
}

// PaneNew starts a pane: an agent, a command, or the machine's shell.
type PaneNew struct {
	Machine string   `json:"machine,omitempty"` // "" is local
	Agent   string   `json:"agent,omitempty"`
	Prompt  string   `json:"prompt,omitempty"`
	Command []string `json:"command,omitempty"`
	Cwd     string   `json:"cwd,omitempty"`
	Name    string   `json:"name,omitempty"`
}

// PaneRef names a pane by its Key.
type PaneRef struct {
	Pane string `json:"pane"`
}

// Attach is how an app's terminal shows a pane: the command to run in it.
type Attach struct {
	Argv []string `json:"argv"`
	Raw  bool     `json:"raw,omitempty"` // the pane is drawn at the server's size
}

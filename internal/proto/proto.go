// Package proto defines conch's client/server protocol: newline-delimited
// JSON messages over a stream (a unix socket locally, an SSH bridge remotely).
//
// A message with Method is a request; with an ID it expects a response
// carrying the same ID and either Result or Error, without an ID it is a
// fire-and-forget notification. A message with Event is a server push.
package proto

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// Version is the conch release version, set at release time with
// -ldflags "-X github.com/Amitgb14/conch/internal/proto.Version=0.2.0".
var Version = "0.1.0-dev"

// IsRelease reports whether Version names a published release rather than a
// development build.
func IsRelease() bool { return Version != "" && !strings.Contains(Version, "dev") }

// ProtocolVersion is bumped on incompatible wire changes. Additive changes
// are advertised through Capabilities instead.
const ProtocolVersion = 1

// Capabilities advertised by this build during the hello handshake.
// Add one whenever the server gains something clients rely on: clients use
// the list to detect a server started by an older build.
var Capabilities = []string{
	"pane.v1", "pane.frame.v1", "events.v1", "agent.v1",
	"project.v1", "pane.scroll.v1", "project.pr.v1", "pane.default_shell.v1",
	"agent.install.v1", "fs.v1", "shell.omz.v1", "agent.setup.v1", "worktree.files.v1", "session.v1", "agent.limits.v1", "server.reload.v1", "session.delete.v1",
}

// Methods.
const (
	MethodHello           = "hello"
	MethodPing            = "ping"
	MethodServerStop      = "server.stop"
	MethodServerReload    = "server.reload"
	MethodPaneList        = "pane.list"
	MethodPaneCreate      = "pane.create"
	MethodPaneClose       = "pane.close"
	MethodPaneResize      = "pane.resize"
	MethodPaneSendText    = "pane.send_text"
	MethodPaneSendKeys    = "pane.send_keys"
	MethodPaneRead        = "pane.read"
	MethodPaneSubscribe   = "pane.subscribe"
	MethodPaneUnsubscribe = "pane.unsubscribe"
	MethodPaneMarkSeen    = "pane.mark_seen"
	MethodPaneRename      = "pane.rename"
	MethodPaneSendMouse   = "pane.send_mouse"
	MethodPaneScroll      = "pane.scroll"
	MethodAgentReport     = "agent.report"
	MethodAgentExplain    = "agent.explain"
	MethodAgentStatus     = "agent.status"
	MethodAgentInstall    = "agent.install"

	MethodProjectList    = "project.list"
	MethodProjectAdd     = "project.add"
	MethodProjectRemove  = "project.remove"
	MethodProjectRefresh = "project.refresh"
	MethodProjectChanges = "project.changes"
	MethodProjectDiff    = "project.diff"
	MethodWorktreeAdd    = "worktree.add"
	MethodWorktreeRemove = "worktree.remove"
	MethodTaskCreate     = "task.create"
	MethodProjectCreate  = "project.create"
	MethodFSList         = "fs.list"
	MethodFSMkdir        = "fs.mkdir"
	MethodShellThemes    = "shell.themes"
	MethodAgentSetup     = "agent.setup"
	MethodProjectFiles   = "project.set_files"
	MethodWorktreeFiles  = "worktree.copy_files"
	MethodSessionList    = "session.list"
	MethodSessionResume  = "session.resume"
	MethodSessionDismiss = "session.dismiss"
	MethodSessionDelete  = "session.delete"
	MethodAgentLimits    = "agent.limits"
)

// Events.
const (
	EventPaneCreated = "pane.created"
	EventPaneExited  = "pane.exited"
	EventPaneClosed  = "pane.closed"
	EventPaneFrame   = "pane.frame"
	// EventPaneUpdated carries a PaneInfo whose agent status, title or
	// name changed.
	EventPaneUpdated = "pane.updated"
	// EventProjectUpdated carries a ProjectInfo (added or git state changed).
	EventProjectUpdated = "project.updated"
	EventProjectRemoved = "project.removed"
	// EventAgentLimits carries a PlanLimits that changed.
	EventAgentLimits = "agent.limits"
)

// Error codes.
const (
	ErrBadRequest = "bad_request"
	ErrNotFound   = "not_found"
	ErrInternal   = "internal"
	ErrUnknown    = "unknown_method"
)

// Message is the single envelope for requests, responses and events.
type Message struct {
	ID     string          `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
	Event  string          `json:"event,omitempty"`
	Data   json.RawMessage `json:"data,omitempty"`
}

// Error is a protocol-level error.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Errorf builds an *Error.
func Errorf(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// HelloParams opens a session.
type HelloParams struct {
	Client       string   `json:"client"`
	Version      string   `json:"version"`
	Protocol     int      `json:"protocol"`
	Capabilities []string `json:"capabilities"`
}

// ServerReloadParams replaces the server's program without stopping its
// panes. Binary defaults to the server's own executable path (where a new
// build was installed).
type ServerReloadParams struct {
	Binary string `json:"binary,omitempty"`
}

// ServerReloadResult names the program the server is reloading into. The
// connection drops as it does; clients reconnect.
type ServerReloadResult struct {
	Binary string `json:"binary"`
}

// HelloResult describes the server.
type HelloResult struct {
	Version      string    `json:"version"`
	Protocol     int       `json:"protocol"`
	Capabilities []string  `json:"capabilities"`
	PID          int       `json:"pid"`
	Started      time.Time `json:"started"`
	Build        string    `json:"build,omitempty"`    // hash of the server binary
	BuildID      string    `json:"build_id,omitempty"` // the same across platforms (see buildinfo.ID)
	Platform     string    `json:"platform,omitempty"` // e.g. linux/amd64
	Hostname     string    `json:"hostname,omitempty"`
	Home         string    `json:"home,omitempty"`
}

// Pane states.
const (
	PaneRunning = "running"
	PaneExited  = "exited"
)

// PaneInfo is the metadata for one pane.
type PaneInfo struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Command  []string  `json:"command"`
	Cwd      string    `json:"cwd"`
	Cols     int       `json:"cols"`
	Rows     int       `json:"rows"`
	PID      int       `json:"pid"`
	State    string    `json:"state"`
	ExitCode int       `json:"exit_code"`
	Created  time.Time `json:"created"`
	// Agent is set while an agent is detected in the pane.
	Agent *AgentStatus `json:"agent,omitempty"`
	// Title is the terminal title with decoration (spinners, the agent's
	// default title) stripped; agents set it to a summary of their task.
	Title string `json:"title,omitempty"`
	// CustomName is true when the user renamed the pane.
	CustomName bool `json:"custom_name,omitempty"`
	// ProjectID and Branch place the pane in the sidebar tree. Branch is
	// the branch checked out in the pane's directory, if any.
	ProjectID string `json:"project_id,omitempty"`
	Branch    string `json:"branch,omitempty"`
}

// DisplayName is how a pane is labelled: the user's name, else an agent's
// task title, else the command name.
func (p PaneInfo) DisplayName() string {
	if !p.CustomName && p.Agent != nil && p.Title != "" {
		return p.Title
	}
	return p.Name
}

// PaneRenameParams renames a pane; an empty name restores the default.
type PaneRenameParams struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Mouse actions.
const (
	MousePress   = "press"
	MouseRelease = "release"
	MouseMotion  = "motion"
	MouseWheel   = "wheel"
)

// PaneSendMouseParams forwards a mouse event to a pane at cell X, Y (0-based,
// relative to the pane). Button is left, middle, right, wheel_up or
// wheel_down. The server drops it unless the program enabled mouse reporting.
type PaneSendMouseParams struct {
	ID     string `json:"id"`
	X      int    `json:"x"`
	Y      int    `json:"y"`
	Button string `json:"button"`
	Action string `json:"action"`
	Shift  bool   `json:"shift,omitempty"`
	Alt    bool   `json:"alt,omitempty"`
	Ctrl   bool   `json:"ctrl,omitempty"`
}

// ProjectInfo is a project (a git repository or plain folder) and its git
// state.
type ProjectInfo struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Path      string         `json:"path"`
	Git       bool           `json:"git"`
	Base      string         `json:"base,omitempty"` // branch others are compared to
	Worktrees []WorktreeInfo `json:"worktrees,omitempty"`
	Branches  []BranchInfo   `json:"branches,omitempty"` // newest commit first
	Error     string         `json:"error,omitempty"`
	Refreshed time.Time      `json:"refreshed"`
	// PRStatus explains missing pull request data ("" when it loaded, or
	// e.g. "no GitHub remote").
	PRStatus string `json:"pr_status,omitempty"`
	// LocalFiles are git glob patterns of ignored files (.env, local agent
	// settings) copied from the main checkout into new worktrees.
	LocalFiles []string `json:"local_files,omitempty"`
	// LocalFilesDefault is set while LocalFiles is the built-in list.
	LocalFilesDefault bool `json:"local_files_default,omitempty"`
}

// PRInfo is the pull request for a branch.
type PRInfo struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	State  string `json:"state"` // OPEN, MERGED, CLOSED
	Draft  bool   `json:"draft,omitempty"`
	URL    string `json:"url"`
	Review string `json:"review,omitempty"` // APPROVED, CHANGES_REQUESTED, REVIEW_REQUIRED
	Checks string `json:"checks,omitempty"` // pass, fail, pending
	Passed int    `json:"passed,omitempty"`
	Total  int    `json:"total,omitempty"`
}

// WorktreeInfo is a checkout of the project.
type WorktreeInfo struct {
	Path     string     `json:"path"`
	Branch   string     `json:"branch,omitempty"`
	Head     string     `json:"head,omitempty"`
	Detached bool       `json:"detached,omitempty"`
	Main     bool       `json:"main,omitempty"`
	Status   *GitStatus `json:"status,omitempty"`
}

// GitStatus summarises uncommitted changes in a worktree.
type GitStatus struct {
	Staged    int `json:"staged"`
	Unstaged  int `json:"unstaged"`
	Untracked int `json:"untracked"`
	Conflicts int `json:"conflicts"`
	Files     int `json:"files"`
	Added     int `json:"added"`
	Deleted   int `json:"deleted"`
}

// Clean reports whether there is nothing uncommitted.
func (s *GitStatus) Clean() bool { return s == nil || s.Files == 0 }

// BranchInfo is a local branch.
type BranchInfo struct {
	Name       string    `json:"name"`
	Upstream   string    `json:"upstream,omitempty"`
	Gone       bool      `json:"gone,omitempty"`
	Ahead      int       `json:"ahead,omitempty"` // vs upstream
	Behind     int       `json:"behind,omitempty"`
	BaseAhead  int       `json:"base_ahead,omitempty"` // vs the project's base branch
	BaseBehind int       `json:"base_behind,omitempty"`
	Committed  time.Time `json:"committed"`
	Subject    string    `json:"subject,omitempty"`
	Worktree   string    `json:"worktree,omitempty"` // path, when checked out
	PR         *PRInfo   `json:"pr,omitempty"`
}

// ProjectRef addresses a project.
type ProjectRef struct {
	ID string `json:"id"`
}

// ProjectAddParams adds the project containing Path.
type ProjectAddParams struct {
	Path string `json:"path"`
}

// ProjectCreateParams makes a new folder at Path (which must not exist yet),
// optionally a git repository, and adds it as a project.
type ProjectCreateParams struct {
	Path string `json:"path"`
	Git  bool   `json:"git"`
}

// FSListParams lists the folders in Path on the server's machine. "" and
// "~" mean the home directory.
type FSListParams struct {
	Path   string `json:"path"`
	Hidden bool   `json:"hidden,omitempty"`
}

// FSList is the result of fs.list.
type FSList struct {
	Path    string    `json:"path"` // absolute, cleaned
	Parent  string    `json:"parent,omitempty"`
	Home    string    `json:"home"`
	Entries []FSEntry `json:"entries"`
	// Truncated is set when the folder had more entries than were returned.
	Truncated bool `json:"truncated,omitempty"`
}

// FSEntry is a folder inside a listed folder.
type FSEntry struct {
	Name    string `json:"name"`
	Git     bool   `json:"git,omitempty"`     // a git repository (has .git)
	Project bool   `json:"project,omitempty"` // already a project
}

// FSMkdirParams creates one folder.
type FSMkdirParams struct {
	Path string `json:"path"`
}

// ProjectList is the result of project.list.
type ProjectList struct {
	Projects []ProjectInfo `json:"projects"`
}

// ChangesParams asks for the changes on a branch: uncommitted work when it
// is checked out, plus commits (and for other branches, files) since the base.
type ChangesParams struct {
	ProjectID string `json:"project_id"`
	Branch    string `json:"branch"`
}

// Changes is the result of project.changes.
type Changes struct {
	ProjectID string       `json:"project_id"`
	Branch    string       `json:"branch"`
	Base      string       `json:"base"`
	Worktree  string       `json:"worktree,omitempty"` // set when the files are uncommitted work
	Files     []FileChange `json:"files"`
	Commits   []CommitInfo `json:"commits"`
}

// FileChange is one changed file.
type FileChange struct {
	Path     string `json:"path"`
	OrigPath string `json:"orig_path,omitempty"`
	Code     string `json:"code"` // M A D R C T U(conflict) ?(untracked)
	Staged   bool   `json:"staged,omitempty"`
	Unstaged bool   `json:"unstaged,omitempty"`
	Added    int    `json:"added"`
	Deleted  int    `json:"deleted"`
	Binary   bool   `json:"binary,omitempty"`
}

// CommitInfo is one commit.
type CommitInfo struct {
	Hash    string    `json:"hash"`
	Subject string    `json:"subject"`
	Author  string    `json:"author"`
	Time    time.Time `json:"time"`
}

// DiffParams asks for the diff of one file within project.changes' scope.
type DiffParams struct {
	ProjectID string `json:"project_id"`
	Branch    string `json:"branch"`
	File      string `json:"file"`
}

// DiffResult is a unified diff.
type DiffResult struct {
	Diff string `json:"diff"`
}

// WorktreeAddParams checks Branch out into a new worktree, creating the
// branch from Base when it doesn't exist.
type WorktreeAddParams struct {
	ProjectID string `json:"project_id"`
	Branch    string `json:"branch"`
	Base      string `json:"base,omitempty"`
}

// ProjectFilesParams sets a project's local file patterns; Reset restores
// the built-in list.
type ProjectFilesParams struct {
	ProjectID string   `json:"project_id"`
	Patterns  []string `json:"patterns,omitempty"`
	Reset     bool     `json:"reset,omitempty"`
}

// WorktreeFilesParams copies the project's local files from the main
// checkout into a linked worktree. Existing files are kept unless Overwrite.
type WorktreeFilesParams struct {
	ProjectID string `json:"project_id"`
	Path      string `json:"path"`
	Overwrite bool   `json:"overwrite,omitempty"`
}

// WorktreeFilesResult lists what worktree.copy_files did.
type WorktreeFilesResult struct {
	Copied  []string `json:"copied,omitempty"`
	Skipped []string `json:"skipped,omitempty"` // with a reason, e.g. ".env (exists)"
}

// WorktreeRemoveParams removes a (clean) linked worktree.
type WorktreeRemoveParams struct {
	ProjectID string `json:"project_id"`
	Path      string `json:"path"`
}

// WorktreeResult is the path of a created worktree and the local files
// copied into it from the main checkout.
type WorktreeResult struct {
	Path   string   `json:"path"`
	Copied []string `json:"copied,omitempty"`
}

// TaskCreateParams starts an agent on a new branch in its own worktree.
// Branch defaults to one derived from the prompt, Base to the project's.
type TaskCreateParams struct {
	ProjectID string `json:"project_id"`
	Prompt    string `json:"prompt"`
	Branch    string `json:"branch,omitempty"`
	Base      string `json:"base,omitempty"`
	Agent     string `json:"agent,omitempty"` // default: the server's first agent (claude)
	Cols      int    `json:"cols,omitempty"`
	Rows      int    `json:"rows,omitempty"`
}

// Agent states.
const (
	AgentIdle    = "idle"
	AgentWorking = "working"
	AgentBlocked = "blocked"
	AgentDone    = "done" // idle, and nobody has looked since it finished
)

// AgentStatus is what the server believes an agent is doing.
type AgentStatus struct {
	Name      string    `json:"name"`
	State     string    `json:"state"`
	Source    string    `json:"source,omitempty"` // hook or screen
	Reason    string    `json:"reason,omitempty"`
	Message   string    `json:"message,omitempty"`
	SessionID string    `json:"session_id,omitempty"`
	Since     time.Time `json:"since"`
	Tokens    *Tokens   `json:"tokens,omitempty"`
}

// Tokens is an agent session's token usage, from its transcript.
type Tokens struct {
	Input      int    `json:"input"`
	CacheWrite int    `json:"cache_write"`
	CacheRead  int    `json:"cache_read"`
	Output     int    `json:"output"`
	Context    int    `json:"context"` // size of the latest request's context
	Model      string `json:"model,omitempty"`
	// CostUSD is set when the agent reports what the session cost.
	CostUSD float64 `json:"cost_usd,omitempty"`
	// ContextSize is the model's context window, when the agent reports it.
	ContextSize int `json:"context_size,omitempty"`
}

// LimitWindow is one usage limit window of an agent's plan.
type LimitWindow struct {
	UsedPct  float64   `json:"used_pct"` // 0-100 (above 100 once exceeded)
	ResetsAt time.Time `json:"resets_at,omitempty"`
}

// PlanLimits is how much of an agent account's plan limits is used, as the
// agent last reported it: Claude's 5-hour and weekly windows, Codex's
// primary and secondary windows, or a gateway spend limit.
type PlanLimits struct {
	Agent    string       `json:"agent"`
	FiveHour *LimitWindow `json:"five_hour,omitempty"`
	Week     *LimitWindow `json:"week,omitempty"`
	Spend    *LimitWindow `json:"spend,omitempty"`
	At       time.Time    `json:"at"` // when it was reported
}

// AgentLimitsResult is the result of agent.limits.
type AgentLimitsResult struct {
	Limits []PlanLimits `json:"limits"`
}

// NeedsAttention reports whether the agent is waiting on the user.
func (a *AgentStatus) NeedsAttention() bool {
	return a != nil && (a.State == AgentBlocked || a.State == AgentDone)
}

// AgentAvailability is whether an agent conch can launch is installed on
// the server's machine.
type AgentAvailability struct {
	Name      string `json:"name"`
	Label     string `json:"label,omitempty"` // e.g. "Claude Code"
	Installed bool   `json:"installed"`
	Path      string `json:"path,omitempty"`
	Version   string `json:"version,omitempty"`
}

// SessionListParams asks for the agent sessions saved for a project (its
// checkout and worktrees) or a directory.
type SessionListParams struct {
	ProjectID string `json:"project_id,omitempty"`
	Dir       string `json:"dir,omitempty"`
	Limit     int    `json:"limit,omitempty"` // default 100
}

// SessionInfo is a saved agent conversation.
type SessionInfo struct {
	Agent   string    `json:"agent"`
	ID      string    `json:"id,omitempty"` // "" for an interrupted run whose conversation wasn't found
	Dir     string    `json:"dir"`
	Branch  string    `json:"branch,omitempty"`
	Title   string    `json:"title"`
	Started time.Time `json:"started,omitempty"`
	Updated time.Time `json:"updated"`
	// PaneID is set while the session is open in a pane.
	PaneID string `json:"pane_id,omitempty"`
	// Interrupted means the agent was running in conch when its server
	// stopped (a restart or reboot) and hasn't been resumed.
	Interrupted bool `json:"interrupted,omitempty"`
}

// SessionList is the result of session.list, newest first.
type SessionList struct {
	Sessions []SessionInfo `json:"sessions"`
}

// SessionRef names a session to resume or dismiss.
type SessionRef struct {
	Agent string `json:"agent"`
	ID    string `json:"id,omitempty"`
	Dir   string `json:"dir"`
	Cols  int    `json:"cols,omitempty"`
	Rows  int    `json:"rows,omitempty"`
}

// AgentSetupParams asks what agents load when started in Dir. Agent limits
// the answer to one agent; "" means every agent conch knows.
type AgentSetupParams struct {
	Dir   string `json:"dir"`
	Agent string `json:"agent,omitempty"`
}

// AgentSetupResult describes the instructions, skills and tools agents pick
// up in a directory. For a linked worktree, items the main checkout has but
// the worktree lacks are included with Missing set.
type AgentSetupResult struct {
	Dir        string       `json:"dir"`
	ProjectID  string       `json:"project_id,omitempty"`
	Main       string       `json:"main,omitempty"`     // main checkout, when Dir is in a linked worktree
	Worktree   string       `json:"worktree,omitempty"` // that linked worktree's root
	LocalFiles []LocalFile  `json:"local_files,omitempty"`
	Agents     []AgentSetup `json:"agents"`
}

// Local file states in a worktree compared to the main checkout.
const (
	FileMissing = "missing"
	FileDiffers = "differs"
	FileSame    = "same"
	// FileNotIgnored is an untracked file git doesn't ignore: never copied,
	// so an agent can't commit it by accident.
	FileNotIgnored = "not_ignored"
)

// LocalFile is an untracked file of the main checkout matched by the
// project's local file patterns, and its state in the worktree.
type LocalFile struct {
	Path  string `json:"path"`
	State string `json:"state"`
}

// AgentSetup is what one agent loads.
type AgentSetup struct {
	Agent  string       `json:"agent"`
	Label  string       `json:"label"`
	Groups []SetupGroup `json:"groups"`
	Notes  []string     `json:"notes,omitempty"`
}

// SetupGroup is a kind of setup: instructions, skills, MCP servers…
type SetupGroup struct {
	Title string      `json:"title"`
	Items []SetupItem `json:"items"`
}

// SetupItem is one instruction file, skill, command, server or plugin.
// Scope is where it comes from: user, project, local, plugin, extension,
// system or conch.
type SetupItem struct {
	Name    string `json:"name"`
	Scope   string `json:"scope"`
	Path    string `json:"path,omitempty"`
	Detail  string `json:"detail,omitempty"`
	Missing bool   `json:"missing,omitempty"`
}

// AgentStatusResult is the result of agent.status.
type AgentStatusResult struct {
	Agents []AgentAvailability `json:"agents"`
}

// AgentInstallParams installs an agent for the server's user. The installer
// runs in a new pane (returned) so its progress and errors are visible.
type AgentInstallParams struct {
	Agent string `json:"agent"`
	Cols  int    `json:"cols,omitempty"`
	Rows  int    `json:"rows,omitempty"`
}

// AgentReportParams is a lifecycle event from an agent integration (hook).
type AgentReportParams struct {
	ID               string `json:"id"`
	Agent            string `json:"agent"`
	Event            string `json:"event"`
	NotificationType string `json:"notification_type,omitempty"`
	Message          string `json:"message,omitempty"`
	SessionID        string `json:"session_id,omitempty"`
	TranscriptPath   string `json:"transcript_path,omitempty"`
	// From Claude's status line input: plan limits and the context window.
	Limits      *PlanLimits `json:"limits,omitempty"`
	ContextUsed int         `json:"context_used,omitempty"`
	ContextSize int         `json:"context_size,omitempty"`
}

// PaneCreateParams creates a pane running Command in Cwd. When Agent names
// a known agent and Command is empty, the server builds the launch command
// itself (adding its integration, e.g. hooks) with AgentArgs appended.
type PaneCreateParams struct {
	Name      string `json:"name,omitempty"`
	Agent     string `json:"agent,omitempty"`
	AgentArgs string `json:"agent_args,omitempty"` // shell words, e.g. "--model opus"
	// Prompt is a first message for the agent, passed the way it expects.
	Prompt  string   `json:"prompt,omitempty"`
	Command []string `json:"command,omitempty"`
	Cwd     string   `json:"cwd,omitempty"`
	Env     []string `json:"env,omitempty"`
	Cols    int      `json:"cols,omitempty"`
	Rows    int      `json:"rows,omitempty"`
	// ShellTheme is an Oh My Zsh theme for a default-shell pane when that
	// shell is zsh; the user's dotfiles still load first.
	ShellTheme string `json:"shell_theme,omitempty"`
}

// ShellThemes describes the prompt themes available on a machine.
type ShellThemes struct {
	Shell   string   `json:"shell"`             // login shell, e.g. zsh
	OMZ     bool     `json:"omz"`               // Oh My Zsh is installed
	Current string   `json:"current,omitempty"` // ZSH_THEME set in .zshrc
	Themes  []string `json:"themes,omitempty"`  // sorted
}

// PaneRef addresses a pane.
type PaneRef struct {
	ID string `json:"id"`
}

// PaneResizeParams resizes a pane.
type PaneResizeParams struct {
	ID   string `json:"id"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

// PaneSendTextParams types text into a pane. Paste wraps it in bracketed
// paste sequences when the program asked for them.
type PaneSendTextParams struct {
	ID    string `json:"id"`
	Text  string `json:"text"`
	Paste bool   `json:"paste,omitempty"`
}

// PaneSendKeysParams sends named keys ("enter", "ctrl+c", "alt+up") to a
// pane; the server encodes them for the pane's current terminal modes.
type PaneSendKeysParams struct {
	ID   string   `json:"id"`
	Keys []string `json:"keys"`
}

// PaneReadResult is the plain-text visible screen of a pane.
type PaneReadResult struct {
	Lines []string `json:"lines"`
}

// PaneList is the result of pane.list.
type PaneList struct {
	Panes []PaneInfo `json:"panes"`
}

// Frame is a rendered snapshot of a pane's screen, one ANSI-styled string
// per row, with the cursor already drawn in.
type Frame struct {
	ID    string   `json:"id"`
	Cols  int      `json:"cols"`
	Rows  int      `json:"rows"`
	Lines []string `json:"lines"`
	Title string   `json:"title,omitempty"`
	// Offset is how many lines back into history the view is scrolled;
	// History is how many lines of history exist (0 on the alternate screen).
	Offset  int `json:"offset,omitempty"`
	History int `json:"history,omitempty"`
	// Mouse reports that the program asked for mouse events; AltScreen that
	// it is a full-screen program.
	Mouse     bool `json:"mouse,omitempty"`
	AltScreen bool `json:"alt_screen,omitempty"`
}

// PaneScrollParams scrolls the requesting client's view of a pane Offset
// lines back into history (0 = live). The view stays anchored to the same
// text while new output arrives.
type PaneScrollParams struct {
	ID     string `json:"id"`
	Offset int    `json:"offset"`
}

// Conn is a message stream. Writes are safe for concurrent use; reads must
// come from a single goroutine.
type Conn struct {
	rw  io.ReadWriteCloser
	sc  *bufio.Scanner
	wmu sync.Mutex
	enc *json.Encoder
}

// NewConn wraps a byte stream.
func NewConn(rw io.ReadWriteCloser) *Conn {
	sc := bufio.NewScanner(rw)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	return &Conn{rw: rw, sc: sc, enc: json.NewEncoder(rw)}
}

// Read returns the next message.
func (c *Conn) Read() (Message, error) {
	for c.sc.Scan() {
		line := c.sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var m Message
		if err := json.Unmarshal(line, &m); err != nil {
			return Message{}, fmt.Errorf("decode message: %w", err)
		}
		return m, nil
	}
	if err := c.sc.Err(); err != nil {
		return Message{}, err
	}
	return Message{}, io.EOF
}

// Write sends one message.
func (c *Conn) Write(m Message) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.enc.Encode(m) // Encode appends the newline delimiter.
}

// Close closes the underlying stream.
func (c *Conn) Close() error { return c.rw.Close() }

// Marshal encodes v, panicking only on programmer error (unencodable types).
func Marshal(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("proto: marshal %T: %v", v, err))
	}
	return b
}

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
var Version = "0.1.4-dev"

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
	"agent.install.v1", "fs.v1", "shell.omz.v1", "agent.setup.v1", "worktree.files.v1", "session.v1", "agent.limits.v1", "server.reload.v1", "session.delete.v1", "session.search.v1", "session.share.v1", "agent.broadcast.v1", "agent.broadcast.shells.v1", "pane.redraw.v1", "fs.upload.v1", "branch.harvest.v1", "worktree.cleanup.v1", "branch.hunks.v1", "project.resolve.v1", CapSessionHandoff, CapWorktreeWatch, CapPaneSearch, CapPaneMonitor, CapWorktreeMove, CapFSFiles, CapFSRead,
}

// CapSessionHandoff is session.export and session.share taking a Doc: a
// conversation handed to an agent on another machine.
const CapSessionHandoff = "session.handoff.v1"

// CapWorktreeWatch is announced only by a server that really got its file
// watches: without it clients poll instead.
const CapWorktreeWatch = "worktree.watch.v1"

// CapWorktreeMove is moving a worktree to another machine: worktree.have,
// worktree.describe, worktree.pack, worktree.pack_read, worktree.unpack and
// project.clone.
const CapWorktreeMove = "worktree.move.v1"

// CapPaneSearch is pane.search: finding text in a pane's history.
const CapPaneSearch = "pane.search.v1"

// CapPaneMonitor is pane.monitor and the Monitor and Alert of PaneInfo.
const CapPaneMonitor = "pane.monitor.v1"

// CapFSFiles is fs.list taking Root and Files: a checkout's files, confined
// to it, with git status. An older server answers with folders only.
const CapFSFiles = "fs.files.v1"

// CapFSRead is fs.read: the start of a file in a checkout, for a preview.
const CapFSRead = "fs.read.v1"

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
	MethodPaneRedraw      = "pane.redraw"
	MethodPaneSendMouse   = "pane.send_mouse"
	MethodPaneScroll      = "pane.scroll"
	MethodPaneSearch      = "pane.search"
	MethodPaneMonitor     = "pane.monitor"
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
	MethodProjectResolve = "project.resolve"
	MethodFSList         = "fs.list"
	MethodFSMkdir        = "fs.mkdir"
	MethodFSUpload       = "fs.upload"
	MethodFSRead         = "fs.read"
	MethodShellThemes    = "shell.themes"
	MethodAgentSetup     = "agent.setup"
	MethodProjectFiles   = "project.set_files"
	MethodWorktreeFiles  = "worktree.copy_files"
	MethodSessionList    = "session.list"
	MethodSessionResume  = "session.resume"
	MethodSessionDismiss = "session.dismiss"
	MethodSessionSearch  = "session.search"
	MethodSessionShare   = "session.share"
	MethodSessionExport  = "session.export"
	MethodSessionDelete  = "session.delete"
	MethodAgentLimits    = "agent.limits"
	// MethodAgentBroadcast types one message into several agents.
	MethodAgentBroadcast = "agent.broadcast"

	// Finishing a branch's work: commit, push, open a pull request, merge
	// into the base, or throw the branch away.
	MethodBranchCommit  = "branch.commit"
	MethodBranchPush    = "branch.push"
	MethodBranchPR      = "branch.pr"
	MethodBranchMerge   = "branch.merge"
	MethodBranchDiscard = "branch.discard"
	// Leftover worktrees: list what each would lose, and remove them.
	MethodWorktreeStale   = "worktree.stale"
	MethodWorktreeCleanup = "worktree.cleanup"
	// Moving a worktree to another machine (CapWorktreeMove).
	MethodWorktreeDescribe = "worktree.describe"
	MethodWorktreeHave     = "worktree.have"
	MethodWorktreePack     = "worktree.pack"
	MethodWorktreePackRead = "worktree.pack_read"
	MethodWorktreeUnpack   = "worktree.unpack"
	MethodProjectClone     = "project.clone"
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
	// EventWorktreeChanged carries a WorktreeChanged: files in a worktree
	// were written, moved or removed. It says nothing about what git makes
	// of them — the reader re-reads what it is showing.
	EventWorktreeChanged = "worktree.changed"
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
	Started      time.Time `json:"started"` // the first server of a chain of reloads
	// LoadedAt is when the running program started: a reload changes it but
	// not Started. Zero from servers that predate it.
	LoadedAt time.Time `json:"loaded_at,omitempty"`
	Build    string    `json:"build,omitempty"`    // hash of the server binary
	BuildID  string    `json:"build_id,omitempty"` // the same across platforms (see buildinfo.ID)
	Platform string    `json:"platform,omitempty"` // e.g. linux/amd64
	Hostname string    `json:"hostname,omitempty"`
	Home     string    `json:"home,omitempty"`
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
	// Monitor is what the user asked to be told about this pane, and Alert
	// what has happened since someone last looked at it (AlertActivity or
	// AlertSilence).
	Monitor *PaneMonitor `json:"monitor,omitempty"`
	Alert   string       `json:"alert,omitempty"`
}

// PaneMonitor asks to be told about a pane's output, as tmux's
// monitor-activity and monitor-silence do: Activity for any output while
// nobody is looking at it, Silence for that many seconds without output
// after some. A zero PaneMonitor stops monitoring.
type PaneMonitor struct {
	Activity bool `json:"activity,omitempty"`
	Silence  int  `json:"silence,omitempty"`
}

// PaneMonitorParams sets a pane's monitoring.
type PaneMonitorParams struct {
	ID string `json:"id"`
	PaneMonitor
}

// Alerts a monitored pane raises.
const (
	AlertActivity = "activity"
	AlertSilence  = "silence"
)

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
	// Remote is the URL of the repository's origin, so the same project can
	// be recognised on another machine. Absent from older servers.
	Remote string `json:"remote,omitempty"`
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
	Head   string `json:"head,omitempty"` // head commit
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

// ProjectPlace says which project and checkout a directory is in, read from
// git rather than the last refresh.
type ProjectPlace struct {
	ProjectID string `json:"project_id"`
	Root      string `json:"root"` // the project's main checkout
	Git       bool   `json:"git,omitempty"`
	Base      string `json:"base,omitempty"`
	Worktree  string `json:"worktree,omitempty"` // the checkout holding the directory
	Branch    string `json:"branch,omitempty"`   // its branch, "" when detached
}

// ProjectCreateParams makes a new folder at Path (which must not exist yet),
// optionally a git repository, and adds it as a project.
type ProjectCreateParams struct {
	Path string `json:"path"`
	Git  bool   `json:"git"`
}

// FSListParams lists the folders in Path on the server's machine. "" and
// "~" mean the home directory.
//
// With Root set the listing is of a checkout instead: Root must be a
// project's folder or one of its worktrees, Path is relative to it ("" for
// Root itself), and nothing outside Root is listed. Files adds the files
// beside the folders, with their size, time and git status (fs.files.v1).
type FSListParams struct {
	Path   string `json:"path"`
	Hidden bool   `json:"hidden,omitempty"`
	Root   string `json:"root,omitempty"`
	Files  bool   `json:"files,omitempty"`
	// Ignored keeps what git ignores in a checkout listing; it is left out
	// otherwise, which is what keeps node_modules out of the way.
	Ignored bool `json:"ignored,omitempty"`
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

// FSEntry is a folder inside a listed folder, or with Files a file.
type FSEntry struct {
	Name    string `json:"name"`
	Git     bool   `json:"git,omitempty"`     // a git repository (has .git)
	Project bool   `json:"project,omitempty"` // already a project

	// The rest are only filled in a checkout listing (fs.files.v1).
	Dir     bool      `json:"dir,omitempty"`
	Size    int64     `json:"size,omitempty"`
	ModTime time.Time `json:"mod_time,omitzero"`
	Symlink bool      `json:"symlink,omitempty"`
	Broken  bool      `json:"broken,omitempty"` // a symlink to nothing
	Ignored bool      `json:"ignored,omitempty"`
	// Status is the file's git status in the changes view's alphabet (M, A,
	// D, R, U, ?); a folder carries the most telling one of what is in it.
	Status string `json:"status,omitempty"`
}

// FSReadMax is the most fs.read returns at once: enough for a preview.
const FSReadMax = 256 << 10

// FSReadParams reads the start of a file in a checkout. Root and Path are as
// in FSListParams; Max is capped at FSReadMax, and 0 means FSReadMax.
type FSReadParams struct {
	Root   string `json:"root"`
	Path   string `json:"path"`
	Offset int64  `json:"offset,omitempty"`
	Max    int    `json:"max,omitempty"`
}

// FSReadResult is the result of fs.read. A binary file has no Data: it is
// described rather than shown.
type FSReadResult struct {
	Path      string    `json:"path"` // absolute
	Size      int64     `json:"size"`
	ModTime   time.Time `json:"mod_time,omitzero"`
	Data      string    `json:"data,omitempty"`
	Truncated bool      `json:"truncated,omitempty"` // the file goes on past Data
	Binary    bool      `json:"binary,omitempty"`
	MIME      string    `json:"mime,omitempty"`
}

// FSMkdirParams creates one folder.
type FSMkdirParams struct {
	Path string `json:"path"`
}

// UploadChunkSize is how much of a file one fs.upload request carries. Small
// chunks keep pane output flowing over the same connection during an upload.
const UploadChunkSize = 512 << 10

// FSUploadParams sends one chunk of a file for the server to store under its
// uploads folder. The first chunk has no Upload ID and names the file and its
// size; the server returns an ID for the chunks after it.
type FSUploadParams struct {
	Upload string `json:"upload,omitempty"`
	Name   string `json:"name,omitempty"` // base name, first chunk only
	Size   int64  `json:"size,omitempty"` // the whole file's size, first chunk only
	Data   []byte `json:"data,omitempty"` // base64 in JSON
	Final  bool   `json:"final,omitempty"`
}

// FSUploadResult answers each chunk.
type FSUploadResult struct {
	Upload string `json:"upload"`
	Path   string `json:"path,omitempty"` // the stored file's absolute path, once Final
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
	ProjectID string `json:"project_id"`
	Branch    string `json:"branch"`
	Base      string `json:"base"`
	Worktree  string `json:"worktree,omitempty"` // set when the files are uncommitted work
	// Watched says the server announces this worktree's edits as they
	// happen. Without it a reader has to keep polling: a worktree can be
	// too big for the server's watch budget even when the server watches
	// others, so this is per worktree rather than a capability.
	Watched bool         `json:"watched,omitempty"`
	Files   []FileChange `json:"files"`
	Commits []CommitInfo `json:"commits"`
}

// WorktreeChanged says a worktree's files changed on disk. Paths are
// relative to Worktree, slash-separated, and capped: More says some were
// left out, and a reader that cares about a particular file re-reads it
// anyway rather than trusting the list to be complete.
type WorktreeChanged struct {
	ProjectID string   `json:"project_id"`
	Worktree  string   `json:"worktree"`
	Paths     []string `json:"paths,omitempty"`
	More      bool     `json:"more,omitempty"`
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

// BranchRef addresses a branch of a project.
type BranchRef struct {
	ProjectID string `json:"project_id"`
	Branch    string `json:"branch"`
}

// BranchCommitParams commits the uncommitted changes where Branch is checked
// out: every change, or only Files (paths as in Changes; a rename needs both
// its paths).
//
// With Patch — a unified diff, as project.diff returns, cut down to the
// chosen hunks — those hunks are staged and what is staged is committed,
// Files included. A Patch that no longer applies commits nothing. It needs
// capability branch.hunks.v1; an older server would ignore it and commit
// everything.
type BranchCommitParams struct {
	ProjectID string   `json:"project_id"`
	Branch    string   `json:"branch"`
	Message   string   `json:"message"`
	Files     []string `json:"files,omitempty"`
	Patch     string   `json:"patch,omitempty"`
}

// CommitResult is a commit a branch method made.
type CommitResult struct {
	Hash string `json:"hash"`
	Into string `json:"into,omitempty"` // the branch merged into
}

// BranchPRParams pushes Branch and opens a pull request into the project's
// base. An empty Title fills title and body from the commits.
type BranchPRParams struct {
	ProjectID string `json:"project_id"`
	Branch    string `json:"branch"`
	Title     string `json:"title,omitempty"`
	Body      string `json:"body,omitempty"`
	Draft     bool   `json:"draft,omitempty"`
}

// BranchPRResult is the opened pull request.
type BranchPRResult struct {
	URL string `json:"url"`
}

// BranchMergeParams merges Branch's commits into the project's base where
// the base is checked out. A merge that conflicts is undone and refused.
type BranchMergeParams struct {
	ProjectID string `json:"project_id"`
	Branch    string `json:"branch"`
	Squash    bool   `json:"squash,omitempty"`
	Message   string `json:"message,omitempty"`
}

// BranchDiscardParams removes Branch's linked worktree and deletes the
// branch. Without Force it refuses when that loses work; DryRun only reports
// what would be lost.
type BranchDiscardParams struct {
	ProjectID string `json:"project_id"`
	Branch    string `json:"branch"`
	DryRun    bool   `json:"dry_run,omitempty"`
	Force     bool   `json:"force,omitempty"`
}

// BranchDiscardResult says what discarding a branch removes or would lose.
type BranchDiscardResult struct {
	Worktree    string   `json:"worktree,omitempty"`
	Uncommitted []string `json:"uncommitted,omitempty"`
	// Unmerged counts commits in neither the base nor the branch's upstream.
	Unmerged int  `json:"unmerged,omitempty"`
	Done     bool `json:"done,omitempty"`
}

// WorktreeStale lists a project's linked worktrees for cleaning up.
type WorktreeStale struct {
	Worktrees []StaleWorktree `json:"worktrees"`
}

// StaleWorktree is a linked worktree and what removing it would lose.
type StaleWorktree struct {
	Path    string `json:"path"`
	Branch  string `json:"branch,omitempty"`  // "" when detached
	Base    bool   `json:"base,omitempty"`    // the base branch, which cleanup keeps
	Missing bool   `json:"missing,omitempty"` // its folder is gone
	Locked  bool   `json:"locked,omitempty"`
	Panes   bool   `json:"panes,omitempty"` // panes run in it
	// Uncommitted and Unmerged are what removing it loses (see
	// BranchDiscardResult); a detached worktree's commits are not counted.
	Uncommitted int       `json:"uncommitted,omitempty"`
	Unmerged    int       `json:"unmerged,omitempty"`
	Merged      bool      `json:"merged,omitempty"` // the base has all of the branch
	Gone        bool      `json:"gone,omitempty"`   // its upstream was deleted
	PR          *PRInfo   `json:"pr,omitempty"`
	Committed   time.Time `json:"committed,omitempty"` // the branch's last commit
	// Reasons say why it looks finished ("merged", "folder gone");
	// Suggested means it can go without losing anything.
	Reasons   []string `json:"reasons,omitempty"`
	Suggested bool     `json:"suggested,omitempty"`
}

// WorktreeCleanupParams removes linked worktrees, deleting their branches
// (except the base) and pruning those whose folder is gone. Each is checked
// again first: without Force, one that would lose work is skipped.
type WorktreeCleanupParams struct {
	ProjectID string            `json:"project_id"`
	Remove    []CleanupWorktree `json:"remove"`
}

// CleanupWorktree is one worktree to remove.
type CleanupWorktree struct {
	Path  string `json:"path"`
	Force bool   `json:"force,omitempty"`
}

// WorktreeCleanupResult says what cleanup removed and what it didn't.
type WorktreeCleanupResult struct {
	Removed []string         `json:"removed,omitempty"`
	Failed  []CleanupFailure `json:"failed,omitempty"`
}

// CleanupFailure is a worktree cleanup left, and why.
type CleanupFailure struct {
	Path  string `json:"path"`
	Error string `json:"error"`
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

// WorktreeRef names a worktree of a project.
type WorktreeRef struct {
	ProjectID string `json:"project_id"`
	Path      string `json:"path"`
}

// WorktreeMoveInfo is what moving a worktree would take, from
// worktree.describe: its branch and commits, the candidates another machine
// may already have (History, newest first) and its uncommitted work.
type WorktreeMoveInfo struct {
	Branch  string   `json:"branch"`
	Head    string   `json:"head"`
	Base    string   `json:"base,omitempty"`
	Remote  string   `json:"remote,omitempty"`
	History []string `json:"history,omitempty"`
	// Staged and Unstaged count changed files; Other the untracked files
	// that move along, Local the project's local files (.env and such).
	Staged   int      `json:"staged,omitempty"`
	Unstaged int      `json:"unstaged,omitempty"`
	Other    int      `json:"other,omitempty"`
	Local    []string `json:"local,omitempty"`
}

// WorktreeHaveParams asks which Commits a project's repository has.
type WorktreeHaveParams struct {
	ProjectID string   `json:"project_id"`
	Commits   []string `json:"commits"`
}

// WorktreeHaveResult lists the commits found, in the order asked.
type WorktreeHaveResult struct {
	Have []string `json:"have"`
}

// WorktreePackParams packs a worktree for another machine that already has
// commit Have ("" when it has none of the history).
type WorktreePackParams struct {
	ProjectID string `json:"project_id"`
	Path      string `json:"path"`
	Have      string `json:"have,omitempty"`
}

// WorktreePack is a packed worktree waiting to be read.
type WorktreePack struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// WorktreePackReadParams reads a pack from Offset; the server deletes the
// pack once its end has been read.
type WorktreePackReadParams struct {
	ID     string `json:"id"`
	Offset int64  `json:"offset"`
}

// WorktreePackChunk is one piece of a pack (at most UploadChunkSize).
type WorktreePackChunk struct {
	Data []byte `json:"data,omitempty"`
	EOF  bool   `json:"eof,omitempty"`
}

// WorktreeUnpackParams recreates a packed worktree in a project from a
// file uploaded with fs.upload.
type WorktreeUnpackParams struct {
	ProjectID string `json:"project_id"`
	Pack      string `json:"pack"`
}

// WorktreeUnpackResult is the new worktree.
type WorktreeUnpackResult struct {
	Path   string   `json:"path"`
	Branch string   `json:"branch"`
	Files  []string `json:"files,omitempty"` // untracked and local files written
}

// ProjectCloneParams clones URL into Path and adds it as a project.
type ProjectCloneParams struct {
	URL  string `json:"url"`
	Path string `json:"path"`
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
	// Failed means the last request ended with an error, not an answer.
	Failed bool `json:"failed,omitempty"`
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
	// Snippet is the text around a match inside the conversation, in
	// session.search results; empty when the title or other details matched.
	Snippet string `json:"snippet,omitempty"`
	// CostUSD is what the agent said the conversation cost and Output the
	// tokens it generated, when its store records them (Claude Code and
	// OpenCode report a cost, Codex tokens only).
	CostUSD float64 `json:"cost_usd,omitempty"`
	Output  int     `json:"output,omitempty"`
}

// SessionSearchParams asks for the sessions of a project (or directory)
// whose title, branch, agent, ID or conversation contains every word of
// Query. The result is a SessionList.
type SessionSearchParams struct {
	ProjectID string `json:"project_id,omitempty"`
	Dir       string `json:"dir,omitempty"`
	Query     string `json:"query"`
	Limit     int    `json:"limit,omitempty"`
}

// SessionShareParams hands a saved conversation to another agent: conch
// writes it to a Markdown file in the session's directory, then starts To
// there with a prompt to read it, or (with PaneID) types that prompt into a
// running agent.
type SessionShareParams struct {
	Agent  string `json:"agent"`
	ID     string `json:"id"`
	Dir    string `json:"dir"`
	To     string `json:"to,omitempty"`
	PaneID string `json:"pane_id,omitempty"`
	Cols   int    `json:"cols,omitempty"`
	Rows   int    `json:"rows,omitempty"`
	// Doc is a conversation exported (session.export) on another machine:
	// it is written to Name in Dir instead of reading the session here.
	// From is that machine's name, for the agent's prompt.
	Doc  string `json:"doc,omitempty"`
	Name string `json:"name,omitempty"`
	From string `json:"from,omitempty"`
}

// SessionExport is a saved conversation rendered as a handoff document,
// the answer to session.export (whose params are a SessionRef).
type SessionExport struct {
	Name string `json:"name"` // file name, e.g. claude-c1.md
	Doc  string `json:"doc"`
}

// AgentBroadcastParams sends Text to the running panes IDs and submits it:
// an agent gets it as its next message. A pane that isn't running an agent
// would run Text as a command, so it is skipped unless Shells is set.
type AgentBroadcastParams struct {
	IDs    []string `json:"ids"`
	Text   string   `json:"text"`
	Shells bool     `json:"shells,omitempty"`
}

// AgentBroadcastResult reports, per pane, whether the message was sent.
type AgentBroadcastResult struct {
	Results []BroadcastOutcome `json:"results"`
}

// BroadcastOutcome is one pane's part of a broadcast.
type BroadcastOutcome struct {
	ID    string `json:"id"`
	Sent  bool   `json:"sent"`
	Error string `json:"error,omitempty"`
}

// SessionShareResult is the pane that received the conversation and the
// file it was written to.
type SessionShareResult struct {
	Pane PaneInfo `json:"pane"`
	Path string   `json:"path"`
}

// SessionList is the result of session.list, newest first.
type SessionList struct {
	Sessions []SessionInfo `json:"sessions"`
	// Partial says an agent whose sessions come from another program had
	// not answered yet, so the list is missing that agent's: ask again in
	// a moment. Absent from older servers, which always waited.
	Partial bool `json:"partial,omitempty"`
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
	// NoProject starts a machine-level pane: it belongs to no project even
	// when Cwd lies in one.
	NoProject bool `json:"no_project,omitempty"`
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

// PaneSearchParams looks for Query in a pane's history and screen, from
// just after Line and Col (just before them, Backward). Line counts from the
// oldest line of history; the screen starts at the result's History.
type PaneSearchParams struct {
	ID       string `json:"id"`
	Query    string `json:"query"`
	Line     int    `json:"line"`
	Col      int    `json:"col"`
	Backward bool   `json:"backward,omitempty"`
}

// PaneSearchResult is the next match, in the same lines as the params, and
// Width cells wide. Wrapped says the search went round an end to find it.
type PaneSearchResult struct {
	Found   bool `json:"found,omitempty"`
	Line    int  `json:"line,omitempty"`
	Col     int  `json:"col,omitempty"`
	Width   int  `json:"width,omitempty"`
	History int  `json:"history,omitempty"`
	Wrapped bool `json:"wrapped,omitempty"`
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

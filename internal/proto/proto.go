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
	"slices"
	"strings"
	"sync"
	"time"
)

// Version is the conch release version, set at release time with
// -ldflags "-X github.com/Amitgb14/conch/internal/proto.Version=0.2.0".
var Version = "0.2.1-dev"

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
	"agent.install.v1", "fs.v1", "shell.omz.v1", "agent.setup.v1", "worktree.files.v1", "session.v1", "agent.limits.v1", "server.reload.v1", "session.delete.v1", "session.search.v1", "session.share.v1", "agent.broadcast.v1", "agent.broadcast.shells.v1", "pane.redraw.v1", "fs.upload.v1", "branch.harvest.v1", "worktree.cleanup.v1", "branch.hunks.v1", "project.resolve.v1", CapSessionHandoff, CapWorktreeWatch, CapPaneSearch, CapPaneMonitor, CapWorktreeMove, CapProjectBundle, CapFSFiles, CapFSRead, CapFSTransfer, CapFSSSH, CapPaneSSH, CapBranchRebase, CapAgentSync, CapAgentSyncUser, CapAgentLibrary, CapLibraryKeychain, CapLibraryPlain, CapCloudAgent, CapAgentPrompt, CapTaskName, CapPaneScope, CapScopeRemote, CapAgentSkill, CapAgentSkillPick, CapLibraryFetch, CapBranchGit, CapAgentTiers, CapWorktreeSetup, CapBranchCILog, CapAgentReply, CapSchedule, CapIssues, CapPermissionRules, CapTaskAfter, CapReviewComments, CapSessionStar, CapView, CapPRMerge, CapAutopilot, CapFrameCursor, CapPaneSizeLatest, CapFSFind, CapProjectLog, CapPaneRaw, CapPaneListening, CapUsageHistory, CapCommitShow, CapPRReview, CapReviewSendTo, CapBranchStage, CapReviewThread, CapFSWrite, CapProjectGroups, CapGroupTask, CapGroupTaskMerge, CapFSSearch, CapFSReplace, CapFSOps, CapFSLines, CapAgentChat, CapProjectGitInit, CapAccounts,
}

// The tiers an agent can be in, as agent.list reports them. They are in
// the protocol because a client says them to a person: "runs here" is a
// promise conch is deliberately not making about sessions.
const (
	TierSupported = "supported"
	TierRunsHere  = "runs here"
)

// CapAgentTiers is agent.list saying each agent's tier, and the server
// offering agents described by a manifest as well as the five with
// adapters. A client without it sees the supported five and nothing it
// cannot explain; one with it can say which agents it only runs, and that
// their sessions are not read.
const CapAgentTiers = "agent.tiers.v1"

// CapSessionHandoff is session.export and session.share taking a Doc: a
// conversation handed to an agent on another machine.
const CapSessionHandoff = "session.handoff.v1"

// CapBranchRebase is branch.push taking Rebase: a push the remote is ahead
// of comes back as push_rejected, and asking again with Rebase takes those
// commits, puts the branch's own on top and pushes.
const CapBranchRebase = "branch.push.rebase.v1"

// CapAgentSync is agent.setup.sync: one agent's setup in a checkout given
// to the others — instructions, skills and MCP servers — planned, applied
// and undone.
const CapAgentSync = "agent.setup.sync.v1"

// CapAgentSyncUser is the same for the setup in your home, through the
// User field: a server without it would sync the checkout instead, so
// clients check before asking.
const CapAgentSyncUser = "agent.setup.sync.user.v1"

// CapAgentLibrary is agent.library and agent.library.apply: MCP servers
// and skills kept in conch and given to the agents chosen for each.
const CapAgentLibrary = "agent.library.v1"

// CapLibraryKeychain says this server gives every pane it starts the
// tokens the library's servers refer to (${VAR}) from this Mac's Keychain
// (service conch-mcp, account the variable), so a client may keep a token
// there rather than ask the person to export it. Only a server that can
// reach a Keychain claims it.
const CapLibraryKeychain = "agent.library.keychain.v1"

// CapLibraryPlain says agent.library takes a server marked plain: its
// environment and headers may hold a token as it is, written into each
// agent's file, for somebody who chose that over the Keychain. An older
// server refuses such a value, so a client offers it only here.
const CapLibraryPlain = "agent.library.plain.v1"

// CapAgentPrompt is agent.prompt: a message submitted to one agent, refused
// while it waits on a question, and the Turn of AgentStatus that says when
// the work it started has ended.
const CapAgentPrompt = "agent.prompt.v1"

// CapBranchGit is branch.git: git commands run in a branch's worktree for
// the TUI's git panel, and what they left unfinished.
const CapBranchGit = "branch.git.v1"

// CapProjectLog is project.log: a project's commits across its branches
// and tags, with parents and refs, for a commit graph.
const CapProjectLog = "project.log.v1"

// CapTaskName is task.create taking Name: the task's pane named as it
// starts. A server without it drops the field, so clients rename instead.
const CapTaskName = "task.name.v1"

// CapPaneScope is PaneInfo.CreatedBy, and the server refusing an agent's
// changes to panes and projects that aren't its own with ErrOutOfScope.
const CapPaneScope = "pane.scope.v1"

// CapScopeRemote is pane.caller and scope.act_for: a scoped agent's own
// server says who it is, and a connection to another machine asks to be
// held to what that agent started there.
const CapScopeRemote = "scope.remote.v1"

// CapAgentSkill is agent.skill: conch's own skill installed where each
// agent reads the person's skills, or taken away again.
const CapAgentSkill = "agent.skill.v1"

// CapAgentSkillPick is agent.skill taking Skills: some of conch's skills
// rather than all of them. A server without it installs every one, so a
// client asks for one skill only of a server that says it can.
const CapAgentSkillPick = "agent.skill.pick.v1"

// CapLibraryFetch is agent.library.fetch: a skill downloaded from GitHub
// into the library, on the machine whose agents will read it.
const CapLibraryFetch = "agent.library.fetch.v1"

// CapWorktreeWatch is announced only by a server that really got its file
// watches: without it clients poll instead.
const CapWorktreeWatch = "worktree.watch.v1"

// OptionalCapabilities are those a server of this very build may still
// leave out, because the machine it runs on cannot give them (no file
// watches, say). Their absence says nothing about the build, so it must not
// make the server look old: that server was shown as needing a reload for
// ever, and reloading it changes nothing. A client that relies on one asks
// for it by name, as it would any other.
var OptionalCapabilities = []string{CapWorktreeWatch, CapLibraryKeychain, CapCloudAgent}

// BuildCapabilities is what every server of this build announces: Capabilities
// without the optional ones. A server lacking any of these is from an older
// build.
func BuildCapabilities() []string {
	var out []string
	for _, c := range Capabilities {
		if !slices.Contains(OptionalCapabilities, c) {
			out = append(out, c)
		}
	}
	return out
}

// CapWorktreeMove is moving a worktree to another machine: worktree.have,
// worktree.describe, worktree.pack, worktree.pack_read, worktree.unpack and
// project.clone.
const CapWorktreeMove = "worktree.move.v1"

// CapProjectBundle is project.bundle and project.unbundle: a project with
// no origin moved by carrying its repository — every branch and tag in a
// git bundle — rather than cloning it there.
const CapProjectBundle = "project.bundle.v1"

// CapPaneSearch is pane.search: finding text in a pane's history.
const CapPaneSearch = "pane.search.v1"

// CapPaneMonitor is pane.monitor and the Monitor and Alert of PaneInfo.
const CapPaneMonitor = "pane.monitor.v1"

// CapFSFiles is fs.list taking Root and Files: a checkout's files, confined
// to it, with git status. An older server answers with folders only.
const CapFSFiles = "fs.files.v1"

// CapFSRead is fs.read: the start of a file in a checkout, for a preview.
const CapFSRead = "fs.read.v1"

// CapFSWrite is fs.write: a file in a checkout saved from an editor, and
// FSReadResult.Hash, the content's fingerprint a save is checked against
// so it never writes over what changed on disk since it was read (an
// agent at work in the same files).
const CapFSWrite = "fs.write.v1"

// CapFSFind is fs.find: the files of a checkout whose path matches a few
// letters, for a search box — git's list of them, so what it ignores stays
// out, as the files view leaves it out.
const CapFSFind = "fs.find.v1"

// CapFSSearch is fs.search: a checkout searched for text in its files and
// for files by name, or one file searched from an offset, a piece at a
// time, which is how a file too big to open whole is searched.
const CapFSSearch = "fs.search.v1"

// CapFSReplace is fs.replace: what fs.search found, replaced file by file,
// each checked against the content it was searched in.
const CapFSReplace = "fs.replace.v1"

// CapFSOps is fs.rename, fs.copy and fs.delete in a checkout, and fs.mkdir
// taking Root. An older server takes fs.mkdir's Path from the home folder
// and ignores Root, so a client asks first.
const CapFSOps = "fs.ops.v1"

// CapFSLines is a big file read by its lines (fs.lines) and edited a range
// at a time (fs.write_range), and fs.read taking Whole: a file up to
// FSWriteMax read in one piece, so it can be edited and saved.
const CapFSLines = "fs.lines.v1"

// CapFSTransfer is copying files and folders to and from the machine, and
// browsing all of it: fs.list with Files and fs.read without a Root reach
// anywhere, fs.upload takes Dir, Archive and Replace, and fs.download and
// fs.download_read bring a file or folder back. An older server ignores
// Dir and would put the file in its uploads folder instead, so a client
// asks first.
const CapFSTransfer = "fs.transfer.v1"

// CapWorktreeSetup is a project's setup command (project.set_setup), run
// in a terminal of its own in every new worktree, and the block of ports
// each worktree is given (CONCH_PORT, WorktreeInfo.Port). An older server
// runs no setup and gives no ports, so a client says so rather than wait
// for a terminal that will never start.
const CapWorktreeSetup = "worktree.setup.v1"

// CapBranchCILog is branch.ci_log: what a branch's failed CI run printed,
// read through gh on the machine the branch is on, for handing to its agent.
const CapBranchCILog = "branch.ci_log.v1"

// CapAgentReply is agent.reply: an agent's last answer, whole, from its
// saved conversation rather than off its screen.
const CapAgentReply = "agent.reply.v1"

// CapProjectGitInit is project.git_init: a project that is a plain
// folder made a git repository — git init and an empty first commit — so
// a task or a branch can be made in it.
const CapProjectGitInit = "project.git_init.v1"

// CapAgentChat is agent.chat: a pane's agent's conversation with the tools
// it used, from its saved transcript, for a chat drawn beside its terminal.
const CapAgentChat = "agent.chat.v1"

// CapSchedule is schedule.add, schedule.list and schedule.remove: a
// message for a running agent, or a task, kept by the server for a time
// to come — after a plan limit resets, overnight, every morning — so it
// happens whether or not anything is connected then.
const CapSchedule = "schedule.v1"

// CapIssues is project.issues and task.create's Issue: work started from a
// GitHub issue, read through gh on the machine the project is on.
const CapIssues = "project.issues.v1"

// CapPermissionRules is agent.permission: the server answering an agent's
// permission request by the person's rules, before it is asked on screen.
const CapPermissionRules = "agent.permission.v1"

// CapTaskAfter is ScheduledItem.After: a task held back until another —
// a branch, or a task still on the schedule — has finished, then started
// on top of that branch's commits. An older server would start it at once,
// so a client refuses -after without it.
const CapTaskAfter = "schedule.after.v1"

// CapReviewComments is review.*: comments on a branch's diff kept by the
// server, so every client of it — another TUI, the command line, the
// phone — sees the same ones, and review.send hands them to the branch's
// agent. An older server has none; a client says so rather than keep
// comments of its own nobody else can see. The list also carries the
// unresolved comments on the branch's pull request, read through gh.
const CapReviewComments = "review.comments.v1"

// CapSessionStar is session.star: a session kept as one to come back to,
// marked in the Sessions view and listed before the rest. The mark is
// conch's, kept beside the server's own state — an agent's saved
// conversations are the agent's files, and conch does not write in them.
const CapSessionStar = "session.star.v1"

// CapView is view.state, view.get and view.show: what a TUI is showing —
// its tabs, their splits and which pane has the keyboard — published by
// the TUI and read back by anything else, and a request to show a set of
// panes together. The server keeps no opinion of its own about layout; it
// carries the TUI's and passes requests back, because panes are the
// server's and the arrangement of them is the TUI's.
const CapView = "view.v1"

// CapPRMerge is branch.pr_merge: merging a branch's pull request on
// GitHub, through gh. An older server only merges into a local checkout,
// which leaves the pull request open; auto_merge needs this one.
const CapPRMerge = "branch.pr_merge.v1"

// CapAutopilot is autopilot.*: the server, not a TUI, sends a branch's
// failed CI and failed check back to its agent ([verify] auto_fix) and
// merges a pull request that turns green (auto_merge), so both carry on
// with no TUI open and through a reload. A TUI hands it the policy from
// its settings; against an older server the TUI does both itself, and
// only while it runs.
const CapAutopilot = "autopilot.v1"

// CapFrameCursor is Frame.Cursor: where the cursor is, as well as drawn
// in, for a client that places something at it — an input method's
// candidates. A frame from an older server has none.
const CapFrameCursor = "pane.frame.cursor.v1"

// CapPaneSizeLatest is a pane shown by several clients taking the size of
// the one the person last typed into, not of whichever resized last. A
// resize from another client is kept and applied when it is typed into.
const CapPaneSizeLatest = "pane.size.latest.v1"

// CapPaneRaw is pane.raw: a pane's output as the program wrote it, for a
// client with a terminal of its own to feed it to — the Mac app's Ghostty —
// rather than frames of the screen conch keeps. It starts with a replay
// that rebuilds the screen and history, then each piece of output as it
// arrives (EventPaneOutput), with the queries conch's emulator already
// answers taken out, so the program is not answered twice.
const CapPaneRaw = "pane.raw.v1"

// CapPaneListening is PaneInfo.Listening: the TCP ports the pane's
// programs listen on, looked for every few seconds.
const CapPaneListening = "pane.listening.v1"

// CapUsageHistory is usage.history: what each agent on the machine used
// over the last day, week and month, by model, read from the
// conversations it saved.
const CapUsageHistory = "usage.history.v1"

// CapCommitShow is project.commit: one commit whole (its message and the
// files it changed), what changed between two commits, or one file's diff
// in either — a commit graph's side panel.
const CapCommitShow = "project.commit.v1"

// CapPRReview is branch.review: submitting a review of a branch's pull
// request — approve, request changes or comment, with comments on lines —
// through gh.
const CapPRReview = "pr.review.v1"

// CapProjectGroups is group.list, group.save and group.remove: projects
// grouped by the person because they change together — an API, its web
// app, their specs — each still a project of its own.
const CapProjectGroups = "project.groups.v1"

// CapGroupTask is group.task and group.task.remove: one task across a
// group's repos — the same branch in a worktree of each, gathered in a
// task folder, with one agent given them all or an agent in each.
const CapGroupTask = "group.task.v1"

// CapGroupTaskMerge is group.task.merge: a task's pull requests merged
// once every one of them is green, one repo after another in the order
// given, by the server so it carries on with no client open. With it, a
// task's folder is also a root fs.read and fs.write take, for its
// CONCH-TASK.md.
const CapGroupTaskMerge = "group.task.merge.v1"

// CapCloudAgent says the server runs inside a Conch Cloud machine, with
// `conch cloud agent` beside it (cloud.agent.register): an agent it starts
// is given the account's env-mode secrets, which the cloud agent fetches
// for that start and nobody writes down. Only a server with a cloud agent
// registered announces it, so it is optional: every other server of this
// build leaves it out.
const CapCloudAgent = "cloud.agent.v1"

// CapReviewSendTo is review.send's Pane and New: the notes handed to the
// agent picked, or to a new one, rather than whichever is first on the
// branch — a branch like the base can have a dozen.
const CapReviewSendTo = "review.send.to.v1"

// CapBranchStage is branch.stage — files or hunks put in the index (git
// add) or taken out — and branch.commit's Staged, committing what is in
// the index and nothing else.
const CapBranchStage = "branch.stage.v1"

// CapReviewThread is a note that stays after it is sent — marked with
// whom it went to (SentTo) — and review.reply: the agent answering it,
// or the person answering back. review.list leaves sent notes out unless
// asked (ReviewRef.Sent), so a client from before still lists only what
// waits to be sent.
const CapReviewThread = "review.thread.v1"

// CapFSSSH is the files of a host a terminal is logged in to with ssh:
// PaneInfo.SSH names the host, and fs.list, fs.read, fs.mkdir, fs.upload
// and fs.download take SSH, the pane, to reach that host's files instead of
// this machine's — through sftp, so nothing of conch's is needed there.
const CapFSSSH = "fs.ssh.v1"

// CapPaneSSH is pane.create taking SSH, a pane logged in to a host: the
// command then runs on that host, through ssh with the pane's options and
// a terminal — how the Files view of a terminal's ssh host edits a file.
const CapPaneSSH = "pane.ssh.v1"

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
	// MethodPaneRaw starts the pane's raw output to this connection
	// (pane.raw.v1): the result is the replay, then EventPaneOutput
	// follows until pane.unsubscribe.
	MethodPaneRaw = "pane.raw"
	// MethodUsageHistory answers a UsageHistory (usage.history.v1).
	MethodUsageHistory = "usage.history"
	MethodAgentReport  = "agent.report"
	MethodAgentExplain = "agent.explain"
	MethodAgentStatus  = "agent.status"
	MethodAgentInstall = "agent.install"

	// Project groups (CapProjectGroups): GroupList, GroupSaveParams →
	// GroupInfo, GroupRef.
	MethodGroupList   = "group.list"
	MethodGroupSave   = "group.save"
	MethodGroupRemove = "group.remove"
	// Tasks across a group's repos (CapGroupTask): GroupTaskParams →
	// GroupTaskResult; GroupTaskRef.
	MethodGroupTask       = "group.task"
	MethodGroupTaskRemove = "group.task.remove"
	// MethodGroupTaskMerge arms (or with Stop, disarms) Merge All When
	// Green on a task (CapGroupTaskMerge): GroupTaskMergeParams → GroupTask.
	MethodGroupTaskMerge = "group.task.merge"
	// MethodCloudAgentRegister is conch cloud agent telling the server
	// where to ask for an agent's secrets (CapCloudAgent):
	// CloudAgentRegisterParams → CloudAgentRegisterResult. It lasts as long
	// as the connection that made it.
	MethodCloudAgentRegister = "cloud.agent.register"
	MethodProjectList        = "project.list"
	MethodProjectAdd         = "project.add"
	MethodProjectRemove      = "project.remove"
	MethodProjectRefresh     = "project.refresh"
	MethodProjectChanges     = "project.changes"
	MethodProjectDiff        = "project.diff"
	// MethodProjectLog is a project's history for a commit graph
	// (CapProjectLog).
	MethodProjectLog = "project.log"
	// MethodProjectCommit answers a CommitDetail (project.commit.v1).
	MethodProjectCommit  = "project.commit"
	MethodWorktreeAdd    = "worktree.add"
	MethodWorktreeRemove = "worktree.remove"
	MethodTaskCreate     = "task.create"
	MethodProjectCreate  = "project.create"
	MethodProjectResolve = "project.resolve"
	// MethodProjectGitInit makes a plain-folder project a git repository
	// (ProjectRef; answers its ProjectInfo; project.git_init.v1).
	MethodProjectGitInit = "project.git_init"
	MethodFSList         = "fs.list"
	MethodFSMkdir        = "fs.mkdir"
	MethodFSUpload       = "fs.upload"
	MethodFSRead         = "fs.read"
	MethodFSWrite        = "fs.write"
	MethodFSFind         = "fs.find"
	MethodFSSearch       = "fs.search"
	MethodFSReplace      = "fs.replace"
	MethodFSRename       = "fs.rename"
	MethodFSCopy         = "fs.copy"
	MethodFSDelete       = "fs.delete"
	MethodFSLines        = "fs.lines"
	MethodFSWriteRange   = "fs.write_range"
	// Copying a file or folder off the machine (CapFSTransfer).
	MethodFSDownload     = "fs.download"
	MethodFSDownloadRead = "fs.download_read"
	MethodShellThemes    = "shell.themes"
	MethodAgentSetup     = "agent.setup"
	MethodAgentSync      = "agent.setup.sync"
	MethodAgentLibrary   = "agent.library"
	MethodLibraryFetch   = "agent.library.fetch"
	MethodLibraryApply   = "agent.library.apply"
	MethodProjectFiles   = "project.set_files"
	MethodProjectSetup   = "project.set_setup"
	MethodWorktreeFiles  = "worktree.copy_files"
	MethodSessionList    = "session.list"
	MethodSessionResume  = "session.resume"
	MethodSessionDismiss = "session.dismiss"
	MethodSessionSearch  = "session.search"
	MethodSessionShare   = "session.share"
	MethodSessionExport  = "session.export"
	MethodSessionDelete  = "session.delete"
	// MethodSessionStar keeps a session as one to come back to, or stops.
	MethodSessionStar = "session.star"
	MethodAgentLimits = "agent.limits"
	// MethodAgentBroadcast types one message into several agents.
	MethodAgentBroadcast = "agent.broadcast"
	// MethodAgentPrompt submits a message to one agent, unless it is
	// waiting on a question.
	MethodAgentPrompt = "agent.prompt"

	// MethodViewState is a TUI telling the server what it is showing, so
	// anything else can read it (view.get). A notification: nothing waits
	// on it, and a server that predates it ignores it.
	MethodViewState = "view.state"
	// MethodViewGet answers with the last view a TUI published.
	MethodViewGet = "view.get"
	// MethodViewShow asks the TUIs to show a set of panes together. It is
	// a request, not an order: a client that is not showing that machine,
	// or has no room, does what it can.
	MethodViewShow = "view.show"
	// MethodAgentReply is the last thing a pane's agent said, read from
	// its saved conversation (CapAgentReply).
	MethodAgentReply = "agent.reply"
	// MethodAgentChat is a pane's agent's conversation, tools and all,
	// read from its saved transcript (CapAgentChat).
	MethodAgentChat = "agent.chat"

	MethodProjectIssues = "project.issues"

	MethodAgentPermission = "agent.permission"

	MethodScheduleAdd    = "schedule.add"
	MethodScheduleList   = "schedule.list"
	MethodScheduleRemove = "schedule.remove"

	MethodReviewList = "review.list"
	MethodReviewSet  = "review.set"
	MethodReviewSend = "review.send"
	// MethodReviewReply answers a note (review.thread.v1).
	MethodReviewReply = "review.reply"
	// MethodPaneCaller says which pane the connection comes from and
	// whether it is scoped; MethodActFor asks a connection from another
	// machine to be scoped as that caller.
	MethodPaneCaller = "pane.caller"
	MethodActFor     = "scope.act_for"
	// MethodAgentSkill installs or removes conch's skill for agents.
	MethodAgentSkill = "agent.skill"

	// Finishing a branch's work: commit, push, open a pull request, merge
	// into the base, or throw the branch away.
	MethodBranchCommit = "branch.commit"
	// MethodBranchStage is git add, or taking changes back out of the
	// index (branch.stage.v1).
	MethodBranchStage = "branch.stage"
	MethodBranchPush  = "branch.push"
	MethodBranchPR    = "branch.pr"
	// MethodBranchReview submits a review of the branch's pull request
	// (pr.review.v1).
	MethodBranchReview = "branch.review"
	MethodBranchMerge  = "branch.merge"
	// MethodBranchPRMerge merges a branch's pull request on GitHub
	// (CapPRMerge).
	MethodBranchPRMerge = "branch.pr_merge"
	MethodBranchDiscard = "branch.discard"
	// MethodAutopilotSet hands the server what to do on its own
	// (AutopilotPolicy); MethodAutopilotGet says what it is doing; and
	// MethodAutopilotCheck tells it how a project's own check on a branch
	// ended (CapAutopilot).
	MethodAutopilotSet   = "autopilot.set"
	MethodAutopilotGet   = "autopilot.get"
	MethodAutopilotCheck = "autopilot.check"
	// MethodBranchGit runs git commands in a branch's worktree
	// (CapBranchGit).
	MethodBranchGit = "branch.git"
	// MethodBranchCILog is the log of the failed steps of a branch's
	// newest failed GitHub Actions run (CapBranchCILog).
	MethodBranchCILog = "branch.ci_log"
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
	MethodProjectBundle    = "project.bundle"
	MethodProjectUnbundle  = "project.unbundle"
)

// Events.
const (
	EventPaneCreated = "pane.created"
	EventPaneExited  = "pane.exited"
	EventPaneClosed  = "pane.closed"
	EventPaneFrame   = "pane.frame"
	// EventPaneOutput is a piece of a pane's raw output (PaneOutput), to a
	// connection that asked for it with pane.raw.
	EventPaneOutput = "pane.output"
	// EventPaneUpdated carries a PaneInfo whose agent status, title or
	// name changed.
	EventPaneUpdated = "pane.updated"
	// EventProjectUpdated carries a ProjectInfo (added or git state changed).
	EventProjectUpdated = "project.updated"
	EventProjectRemoved = "project.removed"
	// EventGroups carries the whole GroupList whenever a group changes,
	// or a project leaves one by being removed.
	EventGroups = "group.changed"
	// EventAgentLimits carries a PlanLimits that changed.
	EventAgentLimits = "agent.limits"
	// EventWorktreeChanged carries a WorktreeChanged: files in a worktree
	// were written, moved or removed. It says nothing about what git makes
	// of them — the reader re-reads what it is showing.
	EventWorktreeChanged = "worktree.changed"
	// EventScheduleChanged carries the whole ScheduleList whenever an item
	// is added, fired, moved or removed.
	EventScheduleChanged = "schedule.changed"
	// EventReviewChanged carries the ReviewList of the branch whose
	// comments were added, changed, sent or dropped.
	EventReviewChanged = "review.changed"

	// EventAutopilotNote carries an AutopilotNote: something the server
	// did, or would not do, on its own.
	EventAutopilotNote = "autopilot.note"

	// EventViewShow carries a view.show request to every client.
	EventViewShow = "view.show"
)

// Error codes.
const (
	ErrBadRequest = "bad_request"
	// ErrPushRejected is a push the remote is ahead of: taking its commits
	// first (branch.push with Rebase) is what mends it.
	ErrPushRejected = "push_rejected"
	// ErrRebaseConflict is a rebase that stopped on conflicts and was
	// undone: the branch is as it was and nothing was pushed, so what is
	// left is to sort the conflict out in the worktree by hand.
	ErrRebaseConflict = "rebase_conflict"
	// ErrAgentBlocked is a message refused because the agent is waiting
	// on a question: typed in, its Enter could answer the question.
	ErrAgentBlocked = "agent_blocked"
	// ErrOutOfScope is a change an agent asked for, from inside its pane,
	// to a pane or project that isn't its own.
	ErrOutOfScope = "out_of_scope"
	// ErrCloudAgentHeld: cloud.agent.register while another connection
	// holds the registration.
	ErrCloudAgentHeld = "cloud_agent_held"
	// ErrFileChanged is fs.write refusing to write over a file that changed
	// on disk since it was read (fs.write.v1).
	ErrFileChanged = "file_changed"
	ErrNotFound    = "not_found"
	ErrInternal    = "internal"
	ErrUnknown     = "unknown_method"
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
	// Account is the account its agent was started on, "" for the agent's
	// own (CapAccounts).
	Account string `json:"account,omitempty"`
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
	// LastActive is when the pane last printed, or when it started if it
	// never has. It says whether anything is happening in a machine at
	// all, which is how conch decides a sandbox is idle. Zero from
	// servers that predate it.
	LastActive time.Time `json:"last_active,omitempty"`
	// CreatedBy is the pane this one was started from, if any: an agent
	// there may change it, and what it starts in turn (see pane.scope.v1).
	CreatedBy string `json:"created_by,omitempty"`
	// SSH is the host the program in front is logged in to when it is ssh
	// — `ssh me@box` typed at the shell — as it was typed (fs.ssh.v1).
	SSH string `json:"ssh,omitempty"`
	// Listening is the TCP ports the pane's program and its children
	// listen on, lowest first (pane.listening.v1): a dev server's address.
	Listening []int `json:"listening,omitempty"`
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

// IsPaneID reports whether ref has the shape of a pane ID ("p" and a
// number). Commands take a pane's name in place of its ID, so a name of
// that shape is refused: it would stand for another pane.
func IsPaneID(ref string) bool {
	if len(ref) < 2 || ref[0] != 'p' {
		return false
	}
	for _, c := range ref[1:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
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
	// Setup is the command run in every new worktree of the project, in a
	// terminal of its own: installing dependencies, copying a database,
	// whatever makes a fresh checkout runnable. "" runs nothing.
	Setup string `json:"setup,omitempty"`
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
	// Conflicting: GitHub says it cannot merge into its base as it stands.
	Conflicting bool `json:"conflicting,omitempty"`
	// Runs are its checks one by one — name, state, link — the first few
	// dozen; absent from older servers, which say only the counts.
	Runs []PRCheck `json:"runs,omitempty"`
}

// PRCheck is one check of a pull request: State is pass, fail or pending.
type PRCheck struct {
	Name  string `json:"name"`
	State string `json:"state"`
	URL   string `json:"url,omitempty"`
}

// WorktreeInfo is a checkout of the project.
type WorktreeInfo struct {
	Path     string     `json:"path"`
	Branch   string     `json:"branch,omitempty"`
	Head     string     `json:"head,omitempty"`
	Detached bool       `json:"detached,omitempty"`
	Main     bool       `json:"main,omitempty"`
	Status   *GitStatus `json:"status,omitempty"`
	// Port is the first of the PortBlock ports given to this checkout, as
	// CONCH_PORT in every pane started in it; 0 until one has been.
	Port int `json:"port,omitempty"`
	// Overlaps are the other checkouts of the project that have touched
	// files this one has too: two agents heading for the same merge
	// conflict. Conflicts are the files merging this branch into the base
	// would conflict in, tried without merging. Both are absent from older
	// servers, and Conflicts where git is older than 2.38.
	Overlaps  []Overlap `json:"overlaps,omitempty"`
	Conflicts []string  `json:"conflicts,omitempty"`
}

// Overlap is another branch's checkout touching the same files.
type Overlap struct {
	Branch string   `json:"branch"`
	Files  []string `json:"files"` // the first few
}

// PortBlock is how many ports a worktree is given, from CONCH_PORT on:
// enough for a web server, an API and a database of its own.
const PortBlock = 10

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

// GroupInfo is a project group: its name, its projects in the person's
// order (the order a task across them merges in), and notes for the
// agents working across them.
type GroupInfo struct {
	Name     string   `json:"name"`
	Projects []string `json:"projects"`
	Notes    string   `json:"notes,omitempty"`
	// Tasks across its repos (CapGroupTask), the newest last.
	Tasks []GroupTask `json:"tasks,omitempty"`
}

// GroupTask is one task across a group's repos: the branch made in each
// of its projects, the folder holding a link to each worktree (where its
// agent works), and the panes it started — one agent, or one per repo.
type GroupTask struct {
	Branch   string    `json:"branch"`
	Folder   string    `json:"folder"`
	Projects []string  `json:"projects"`
	Panes    []string  `json:"panes,omitempty"`
	Each     bool      `json:"each,omitempty"`
	Created  time.Time `json:"created"`
	// Merge is Merge All When Green on this task, once armed
	// (CapGroupTaskMerge); nil when it never was.
	Merge *GroupTaskMerge `json:"merge,omitempty"`
}

// The states of a task's Merge All When Green.
const (
	MergeWaiting = "waiting" // armed: not every pull request is green yet
	MergeMerging = "merging" // every one was green; merging in order
	MergeMerged  = "merged"  // all of them merged
	MergeStopped = "stopped" // one could not be merged, or it was stopped; Why says which
)

// GroupTaskMerge is where a task's Merge All When Green stands: the
// projects in the order they merge, those merged so far, and — while it
// waits or once it stopped — why, in a sentence.
type GroupTaskMerge struct {
	Order      []string  `json:"order"`
	Merged     []string  `json:"merged,omitempty"`
	State      string    `json:"state"`
	Why        string    `json:"why,omitempty"`
	RemoveTask bool      `json:"remove_task,omitempty"`
	Armed      time.Time `json:"armed"`
}

// GroupTaskMergeParams arms Merge All When Green on Group's task on
// Branch: Order is every one of the task's projects (default the task's
// own order), RemoveTask takes the task, its worktrees and its folder away
// once all are merged. Stop disarms it instead.
type GroupTaskMergeParams struct {
	Group      string   `json:"group"`
	Branch     string   `json:"branch"`
	Order      []string `json:"order,omitempty"`
	RemoveTask bool     `json:"remove_task,omitempty"`
	Stop       bool     `json:"stop,omitempty"`
}

// GroupTaskParams starts a task across Group's repos: Projects (default
// all of them, in the group's order), each on Branch (default named from
// the prompt). One agent at the task folder with every worktree, or with
// Each an agent in each worktree.
type GroupTaskParams struct {
	Group    string   `json:"group"`
	Prompt   string   `json:"prompt"`
	Branch   string   `json:"branch,omitempty"`
	Agent    string   `json:"agent,omitempty"`
	Account  string   `json:"account,omitempty"` // as PaneCreateParams has it
	Projects []string `json:"projects,omitempty"`
	Each     bool     `json:"each,omitempty"`
	Cols     int      `json:"cols,omitempty"`
	Rows     int      `json:"rows,omitempty"`
}

type GroupTaskResult struct {
	Task  GroupTask  `json:"task"`
	Panes []PaneInfo `json:"panes"`
}

// GroupTaskRef names a task by its group and branch.
type GroupTaskRef struct {
	Group  string `json:"group"`
	Branch string `json:"branch"`
}

type GroupList struct {
	Groups []GroupInfo `json:"groups"`
}

// GroupSaveParams makes a group, or replaces the one called Old (or Name)
// — a rename when Old differs.
type GroupSaveParams struct {
	Name     string   `json:"name"`
	Old      string   `json:"old,omitempty"`
	Projects []string `json:"projects"`
	Notes    string   `json:"notes,omitempty"`
}

type GroupRef struct {
	Name string `json:"name"`
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
	// SSH is a pane logged in to a host with ssh: Path is then on that
	// host (fs.ssh.v1), and Root is not taken.
	SSH string `json:"ssh,omitempty"`
}

// FSFindParams asks for the files of a checkout (a project or one of its
// worktrees, as fs.list's Root) whose path holds Query's letters in order,
// best first: the name containing it, then the path, then the letters
// scattered. Limit is at most FSFindMax; 0 means FSFindDefault.
type FSFindParams struct {
	Root  string `json:"root"`
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
}

// FSFindResult is what fs.find found: paths relative to Root, with slashes.
// Truncated says more matched than were returned.
type FSFindResult struct {
	Root      string   `json:"root"`
	Paths     []string `json:"paths"`
	Truncated bool     `json:"truncated,omitempty"`
}

const (
	FSFindDefault = 20
	FSFindMax     = 200
)

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
	SSH    string `json:"ssh,omitempty"` // as in FSListParams
	// Whole raises Max's cap to FSWriteMax, so a file that can be saved
	// can be read in one piece (fs.lines.v1).
	Whole bool `json:"whole,omitempty"`
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
	// Hash is the SHA-256 of the whole file, hex, when Data is all of it:
	// what an fs.write of an edit is checked against (fs.write.v1).
	Hash string `json:"hash,omitempty"`
}

// FSWriteMax is the most an fs.write takes: a file worth editing in a
// window, not a dump.
const FSWriteMax = 4 << 20

// FSWriteParams saves Data as the file at Path inside the checkout Root
// (a project or one of its worktrees). Base is the Hash it was read as:
// the save is refused with ErrFileChanged when the file is not that any
// more, unless Force. Create makes a file that is not there; without it
// a file gone since it was read is a change too.
type FSWriteParams struct {
	Root   string `json:"root"`
	Path   string `json:"path"`
	Data   string `json:"data"`
	Base   string `json:"base,omitempty"`
	Create bool   `json:"create,omitempty"`
	Force  bool   `json:"force,omitempty"`
}

// FSWriteResult is the file as saved.
type FSWriteResult struct {
	Path    string    `json:"path"` // absolute
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time,omitzero"`
	Hash    string    `json:"hash"`
}

// FSMkdirParams creates one folder. With Root (fs.ops.v1) Path is inside
// that checkout, as in FSListParams, and the folders on the way are made.
type FSMkdirParams struct {
	Path string `json:"path"`
	SSH  string `json:"ssh,omitempty"` // as in FSListParams
	Root string `json:"root,omitempty"`
}

// FSPattern is what fs.search looks for and fs.replace replaces: Query as
// written, or a regular expression (Go's syntax) with Regex; letter case
// ignored unless Case; with Word, only where it is a whole word. A
// pattern matches within a line, never across one.
type FSPattern struct {
	Query string `json:"query"`
	Regex bool   `json:"regex,omitempty"`
	Case  bool   `json:"case,omitempty"`
	Word  bool   `json:"word,omitempty"`
}

// FSSearchParams searches the checkout Root (as fs.list's): with Text, the
// lines of its files that match; with Names, the files whose path holds
// Query's letters, as fs.find finds them. Include and Exclude are globs
// (`*.go`, `internal/`, `cmd/**/main.go`), a path taken when it matches any
// Include (or there are none) and no Exclude. What git ignores is left out
// unless Ignored. Limit counts lines, at most FSSearchMax.
//
// With Path, that one file alone is searched, from byte Offset — the
// start of line Line — for FSSearchBudget at most, and the
// result says where to go on from: a big file searched a piece at a time.
type FSSearchParams struct {
	Root string `json:"root"`
	FSPattern
	Names   bool     `json:"names,omitempty"`
	Text    bool     `json:"text,omitempty"`
	Include []string `json:"include,omitempty"`
	Exclude []string `json:"exclude,omitempty"`
	Ignored bool     `json:"ignored,omitempty"`
	Limit   int      `json:"limit,omitempty"`
	Path    string   `json:"path,omitempty"`
	Offset  int64    `json:"offset,omitempty"`
	Line    int      `json:"line,omitempty"`
}

const (
	FSSearchDefault = 500
	FSSearchMax     = 5000
	// FSSearchFileMax is the biggest file a search of a whole checkout
	// reads; a bigger one is counted in Skipped and searched by its Path.
	FSSearchFileMax = 8 << 20
	// FSSearchLineMax is how much of a matching line comes back: the part
	// around its first match.
	FSSearchLineMax = 400
)

// FSSearchBudget is how long a search of one Path goes on before it
// answers with what it has and where it got to.
const FSSearchBudget = time.Second

// FSSearchResult is what fs.search found. Names are paths relative to
// Root; Files hold the matching lines, in path order. Searched counts
// the files read, Skipped those left out as binary or too big.
type FSSearchResult struct {
	Root      string         `json:"root"`
	Names     []string       `json:"names,omitempty"`
	Files     []FSSearchFile `json:"files,omitempty"`
	Searched  int            `json:"searched,omitempty"`
	Skipped   int            `json:"skipped,omitempty"`
	Truncated bool           `json:"truncated,omitempty"`
	// For a search of one Path: the file's size, where to go on from
	// (Next, the start of line NextLine), and Done once it reached the end.
	Size     int64 `json:"size,omitempty"`
	Next     int64 `json:"next,omitempty"`
	NextLine int   `json:"next_line,omitempty"`
	Done     bool  `json:"done,omitempty"`
}

// FSSearchFile is one file's matching lines, and its content's Hash
// (as fs.read's) that an fs.replace of them is checked against.
type FSSearchFile struct {
	Path  string         `json:"path"`
	Hash  string         `json:"hash,omitempty"`
	Lines []FSSearchLine `json:"lines"`
}

// FSSearchLine is a matching line: its number (from 1), where it starts in
// the file, Text — the line, or FSSearchLineMax of it from byte Col — and
// the matches in Text as byte ranges [start, end).
type FSSearchLine struct {
	Line   int      `json:"line"`
	Offset int64    `json:"offset"`
	Col    int      `json:"col,omitempty"`
	Text   string   `json:"text"`
	Ranges [][2]int `json:"ranges"`
}

// FSReplaceParams replaces the pattern with Replace in each of Files of
// the checkout Root — on the Lines listed, or every line when none are —
// where the file is still the Hash it was searched as. With Regex, $1 and
// ${name} in Replace are the groups matched.
type FSReplaceParams struct {
	Root string `json:"root"`
	FSPattern
	Replace string          `json:"replace"`
	Files   []FSReplaceFile `json:"files"`
}

type FSReplaceFile struct {
	Path  string `json:"path"`
	Hash  string `json:"hash"`
	Lines []int  `json:"lines,omitempty"`
}

// FSReplaceResult says what was replaced, and what was left and why: a
// file changed since the search is skipped, never written over.
type FSReplaceResult struct {
	Files   []FSReplaced `json:"files,omitempty"`
	Skipped []FSSkipped  `json:"skipped,omitempty"`
}

type FSReplaced struct {
	Path  string `json:"path"`
	Count int    `json:"count"`
	Hash  string `json:"hash"`
}

type FSSkipped struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// FSPathParams names a file or folder in the checkout Root, and for
// fs.rename and fs.copy where it goes (To, also inside Root). Neither
// writes over something already there. fs.delete takes a folder whole.
type FSPathParams struct {
	Root string `json:"root"`
	Path string `json:"path"`
	To   string `json:"to,omitempty"`
}

// FSPathResult is the path the file or folder now has, absolute.
type FSPathResult struct {
	Path string `json:"path"`
}

// FSLinesParams reads lines of a file in the checkout Root: Count of them
// (at most FSLinesMax; 0 means FSLinesDefault) from line Line (from 1),
// or with Tail the last Count. Lines are found through an index of the
// file kept by the server, so line ten million is no slower than line ten.
type FSLinesParams struct {
	Root  string `json:"root"`
	Path  string `json:"path"`
	Line  int    `json:"line,omitempty"`
	Count int    `json:"count,omitempty"`
	Tail  bool   `json:"tail,omitempty"`
}

const (
	FSLinesDefault = 200
	FSLinesMax     = 5000
	// FSLinesBytesMax bounds what one fs.lines returns, however long its
	// lines are.
	FSLinesBytesMax = 1 << 20
)

// FSLinesResult is the lines read: Data from byte Offset, Length bytes,
// starting at line Line; Lines is how many the file has. Hash is the
// fingerprint of those bytes, what an fs.write_range of them is checked
// against; it is left out when they are not UTF-8 (Lossy: shown with
// replacement characters, so they cannot be saved back) or Binary.
type FSLinesResult struct {
	Path    string    `json:"path"` // absolute
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time,omitzero"`
	Line    int       `json:"line"`
	Lines   int       `json:"lines"`
	Offset  int64     `json:"offset"`
	Length  int64     `json:"length"`
	Data    string    `json:"data,omitempty"`
	Hash    string    `json:"hash,omitempty"`
	Lossy   bool      `json:"lossy,omitempty"`
	Binary  bool      `json:"binary,omitempty"`
}

// FSWriteRangeParams replaces Length bytes from Offset of a file in the
// checkout Root with Data, if those bytes still have the fingerprint Base
// (FSLinesResult.Hash): a big file edited a few lines at a time. The rest
// of the file is kept as it is, the new file put in place in one step.
type FSWriteRangeParams struct {
	Root   string `json:"root"`
	Path   string `json:"path"`
	Offset int64  `json:"offset"`
	Length int64  `json:"length"`
	Base   string `json:"base"`
	Data   string `json:"data"`
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

	// The rest are CapFSTransfer, first chunk only. Dir is the folder to
	// put it in (absolute, or from ~) instead of the uploads folder.
	// Archive says the data is a folder packed by internal/transfer, to be
	// unpacked as Name. Without Replace a name already taken is not
	// written over: the copy is numbered ("ca 2.pem") and Path says so.
	Dir     string `json:"dir,omitempty"`
	Archive bool   `json:"archive,omitempty"`
	Replace bool   `json:"replace,omitempty"`
	// Mode is a file's permission bits, so a script stays runnable; 0
	// means 0644. A packed folder carries its own.
	Mode uint32 `json:"mode,omitempty"`
	// Parallel (first chunk, CapFSTransfer) says the client keeps several
	// chunks in flight, so they may arrive in any order and each says
	// where it goes: Offset, a multiple of UploadChunkSize, with a whole
	// chunk at each but the last. The Final chunk is sent alone once
	// every other one has been answered.
	Parallel bool  `json:"parallel,omitempty"`
	Offset   int64 `json:"offset,omitempty"`
	// SSH (first chunk) puts it on the host a pane is logged in to, as in
	// FSListParams; Dir is then a folder there.
	SSH string `json:"ssh,omitempty"`
}

// FSUploadResult answers each chunk.
type FSUploadResult struct {
	Upload string `json:"upload"`
	Path   string `json:"path,omitempty"` // the stored file's absolute path, once Final
}

// FSDownloadParams asks for a file or folder (absolute, or from ~) to be
// read back with fs.download_read.
type FSDownloadParams struct {
	Path string `json:"path"`
	SSH  string `json:"ssh,omitempty"` // as in FSListParams
}

// FSDownload is a download ready to read. A folder comes packed by
// internal/transfer, and Size is the pack's.
type FSDownload struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Size int64  `json:"size"`
	Dir  bool   `json:"dir,omitempty"`
}

// FSDownloadReadParams reads the chunk at Offset (at most UploadChunkSize).
type FSDownloadReadParams struct {
	ID     string `json:"id"`
	Offset int64  `json:"offset"`
}

// FSDownloadChunk is one piece of a download; EOF marks the last.
type FSDownloadChunk struct {
	Data []byte `json:"data,omitempty"`
	EOF  bool   `json:"eof,omitempty"`
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
	// Committed asks, for a branch with a worktree, for what its commits
	// change against the base as well (Changes.CommittedFiles): the files
	// are otherwise that worktree's uncommitted work, which is nothing
	// once an agent has committed. An older server ignores it.
	Committed bool `json:"committed,omitempty"`
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
	// CommittedFiles is what the branch's commits change against the
	// base, when Committed asked for it and Files are uncommitted work.
	CommittedFiles []FileChange `json:"committed_files,omitempty"`
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

// LogParams asks for a project's history: every commit reachable from its
// branches, tags and HEAD, newest first. Limit is how many (LogLimit when
// 0, at most LogLimitMax).
type LogParams struct {
	ProjectID string `json:"project_id"`
	Limit     int    `json:"limit,omitempty"`
}

// LogLimit and LogLimitMax bound project.log.
const (
	LogLimit    = 300
	LogLimitMax = 2000
)

// LogCommit is one commit for a graph: children always come before their
// parents in a LogResult, and Refs are as git decorates them ("HEAD ->
// main", "origin/main", "tag: v1").
type LogCommit struct {
	Hash    string    `json:"hash"`
	Parents []string  `json:"parents,omitempty"`
	Subject string    `json:"subject"`
	Author  string    `json:"author"`
	Time    time.Time `json:"time"`
	Refs    []string  `json:"refs,omitempty"`
}

// LogResult is a project's history. More says older commits were left out.
type LogResult struct {
	Commits []LogCommit `json:"commits"`
	More    bool        `json:"more,omitempty"`
	// Remotes are the repository's remotes, by which a client tells a
	// remote's branch (fork/main) from a local one with a slash. Absent
	// from an older server: clients then take origin and upstream.
	Remotes []string `json:"remotes,omitempty"`
}

// CommitParams asks project.commit about Hash: the commit itself; with
// From, what changed from From to Hash; with File, only that file's diff
// (in the commit, or from From).
type CommitParams struct {
	ProjectID string `json:"project_id"`
	Hash      string `json:"hash"`
	From      string `json:"from,omitempty"`
	File      string `json:"file,omitempty"`
}

// CommitDetail is project.commit's answer. For a commit: who, when, its
// message whole and the files it changed against its first parent. For a
// range (From): the files changed and how many commits that took. With
// File: Diff alone.
type CommitDetail struct {
	Hash    string       `json:"hash"`
	Parents []string     `json:"parents,omitempty"`
	Author  string       `json:"author,omitempty"`
	Email   string       `json:"email,omitempty"`
	Time    time.Time    `json:"time,omitempty"`
	Subject string       `json:"subject,omitempty"`
	Body    string       `json:"body,omitempty"`
	Files   []FileChange `json:"files,omitempty"`
	Commits int          `json:"commits,omitempty"`
	Diff    string       `json:"diff,omitempty"`
}

// ReviewEvent is what a review says about a pull request.
const (
	ReviewEventApprove        = "approve"
	ReviewEventRequestChanges = "request_changes"
	ReviewEventComment        = "comment"
)

// BranchReviewParams submits a review of the branch's pull request: Event
// (ReviewEventApprove, ReviewEventRequestChanges or ReviewEventComment), Body, and
// comments on lines of the pull request's diff, each on the new side.
type BranchReviewParams struct {
	ProjectID string          `json:"project_id"`
	Branch    string          `json:"branch"`
	Event     string          `json:"event"`
	Body      string          `json:"body,omitempty"`
	Comments  []ReviewLineNew `json:"comments,omitempty"`
}

// ReviewLineNew is a comment on one line of a pull request's diff.
type ReviewLineNew struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Note string `json:"note"`
}

// BranchReviewResult says where the review went.
type BranchReviewResult struct {
	PR  int    `json:"pr"`
	URL string `json:"url,omitempty"`
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

// BranchPushParams pushes a branch. With Rebase, a push the remote is ahead
// of takes the remote's commits first and puts the branch's own on top;
// without it such a push comes back as ErrPushRejected. It is a BranchRef
// with a field added, so an older server reads it as one.
type BranchPushParams struct {
	ProjectID string `json:"project_id"`
	Branch    string `json:"branch"`
	Rebase    bool   `json:"rebase,omitempty"`
}

// BranchPushResult says what a push did: Took counts the commits a rebase
// brought in from the remote, 0 when nothing was taken.
type BranchPushResult struct {
	Took int `json:"took,omitempty"`
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
	// Staged commits what is in the index and nothing else, as git commit
	// does; Files and Patch are left out (branch.stage.v1).
	Staged bool `json:"staged,omitempty"`
}

// BranchStageParams puts Files and the hunks in Patch (cut from
// project.diff, as BranchCommitParams' is) in the index where Branch is
// checked out — or, with Unstage, takes them back out, Patch then being
// as staged: a diff of the index. The files on disk are left as they are.
// A Patch that no longer applies changes nothing.
type BranchStageParams struct {
	ProjectID string   `json:"project_id"`
	Branch    string   `json:"branch"`
	Files     []string `json:"files,omitempty"`
	Patch     string   `json:"patch,omitempty"`
	Unstage   bool     `json:"unstage,omitempty"`
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

// BranchPRMergeParams merges Branch's pull request Number on GitHub, the
// way the repository allows. A non-empty Head is the commit it must still
// be at, so a push after its checks passed is not merged unchecked.
type BranchPRMergeParams struct {
	ProjectID string `json:"project_id"`
	Branch    string `json:"branch"`
	Number    int    `json:"number"`
	Head      string `json:"head,omitempty"`
}

// BranchPRMergeResult says how the pull request was merged: merge,
// squash or rebase.
type BranchPRMergeResult struct {
	Method string `json:"method"`
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

// BranchCILogParams names the branch whose failed CI run to read.
type BranchCILogParams struct {
	ProjectID string `json:"project_id"`
	Branch    string `json:"branch"`
}

// BranchCILog is a branch's newest failed Actions run — at its pull
// request's head commit, when it has one — and the end of what its failed
// steps printed.
type BranchCILog struct {
	Workflow string `json:"workflow,omitempty"`
	URL      string `json:"url,omitempty"`
	Log      string `json:"log"`
}

// BranchGitParams runs Commands, one after another, in the worktree where
// Branch is checked out, stopping at the first that fails. Each is git's
// arguments without "git", starting with the subcommand. With none it only
// reports the worktree's state.
type BranchGitParams struct {
	ProjectID string     `json:"project_id"`
	Branch    string     `json:"branch"`
	Commands  [][]string `json:"commands,omitempty"`
}

// BranchGitResult is what the commands printed, each under a "$ git …"
// line, and how the last one run ended. InProgress names an operation left
// unfinished in the worktree ("rebase", "merge", "cherry-pick", "revert",
// "am") so the panel can offer to continue or abort it.
type BranchGitResult struct {
	Worktree   string `json:"worktree"`
	Output     string `json:"output,omitempty"`
	ExitCode   int    `json:"exit_code,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
	InProgress string `json:"in_progress,omitempty"`
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

// ProjectSetupParams sets the command run in a project's new worktrees;
// an empty Command runs nothing.
type ProjectSetupParams struct {
	ProjectID string `json:"project_id"`
	Command   string `json:"command,omitempty"`
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

// ProjectBundle is a project's repository bundled for another machine:
// read it back with worktree.pack_read. Branch is the one its main
// checkout is on, for the other machine to check out.
type ProjectBundle struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Branch string `json:"branch,omitempty"`
}

// ProjectUnbundleParams makes a project at Path from a bundle uploaded
// here (Pack, an fs.upload's path), with Branch checked out.
type ProjectUnbundleParams struct {
	Pack   string `json:"pack"`
	Path   string `json:"path"`
	Branch string `json:"branch,omitempty"`
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
	// Setup is the pane the project's setup command runs in; "" when the
	// project has none.
	Setup string `json:"setup,omitempty"`
}

// TaskCreateParams starts an agent on a new branch in its own worktree.
// Branch defaults to one derived from the prompt, Base to the project's.
type TaskCreateParams struct {
	ProjectID string `json:"project_id"`
	Prompt    string `json:"prompt"`
	Branch    string `json:"branch,omitempty"`
	Base      string `json:"base,omitempty"`
	Agent     string `json:"agent,omitempty"` // default: the server's first agent (claude)
	// Account is the agent's account, as PaneCreateParams has it.
	Account string `json:"account,omitempty"`
	// Name names the pane, as pane.rename does, so it can be addressed
	// by it; see IsPaneID.
	Name string `json:"name,omitempty"`
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
	// Issue is a GitHub issue of the project to work on (CapIssues): the
	// server reads it through gh, and its title and body become the
	// prompt — Prompt, when given too, is added as further instructions —
	// and its number starts the branch's name.
	Issue int `json:"issue,omitempty"`
}

// PermissionAsk is an agent asking to use a tool, as its hook saw it:
// Subject is what the tool acts on — a command, a file, a URL.
type PermissionAsk struct {
	ID      string `json:"id"` // the pane
	Agent   string `json:"agent"`
	Tool    string `json:"tool"`
	Subject string `json:"subject,omitempty"`
}

// Permission decisions.
const (
	PermissionAllow = "allow"
	PermissionDeny  = "deny"
)

// PermissionAnswer is what the rules say: allow, deny, or "" to ask the
// person as usual. DryRun says what a rule would have answered while
// [agents] permission_mode is dry-run, when nothing is answered.
type PermissionAnswer struct {
	Decision string `json:"decision,omitempty"`
	Rule     string `json:"rule,omitempty"` // the rule that decided, in words
	DryRun   bool   `json:"dry_run,omitempty"`
}

// PermissionRecord is one answer the rules gave, kept on the pane and
// shown by agent.explain.
type PermissionRecord struct {
	At       time.Time `json:"at"`
	Tool     string    `json:"tool"`
	Subject  string    `json:"subject,omitempty"`
	Decision string    `json:"decision"`
	Rule     string    `json:"rule"`
	DryRun   bool      `json:"dry_run,omitempty"`
}

// IssuesParams lists a project's open GitHub issues (CapIssues).
type IssuesParams struct {
	ProjectID string `json:"project_id"`
	Limit     int    `json:"limit,omitempty"`
}

// IssueInfo is one open issue.
type IssueInfo struct {
	Number int      `json:"number"`
	Title  string   `json:"title"`
	URL    string   `json:"url,omitempty"`
	Labels []string `json:"labels,omitempty"`
}

// IssueList is a project's open issues, newest first.
type IssueList struct {
	Issues []IssueInfo `json:"issues"`
}

// ScheduledItem is something the server does at a time to come: a
// message to the agent running in Pane (Text), or a task to start (Task).
// EveryMinutes repeats it; 0 runs it once.
type ScheduledItem struct {
	ID string    `json:"id"`
	At time.Time `json:"at"`
	// EveryMinutes repeats the item that often, from At on.
	EveryMinutes int `json:"every_minutes,omitempty"`
	// A message: Pane and the Agent expected there, so a pane that has
	// since run something else is not typed into.
	Pane  string `json:"pane,omitempty"`
	Agent string `json:"agent,omitempty"`
	Text  string `json:"text,omitempty"`
	// PaneCreated is when that pane started, set by the server: a server
	// started afresh numbers its panes from p1 again, and a message must
	// not reach a stranger that happens to share the ID.
	PaneCreated time.Time `json:"pane_created,omitempty"`
	// Or a task, as task.create takes it. A repeating one gets a branch of
	// its own each time.
	Task *TaskCreateParams `json:"task,omitempty"`
	// Queue holds a task back, past At, until fewer than WIP agents are
	// running in its project: a backlog that starts work as the person
	// finishes looking at the last. WIP 0 means DefaultWIP.
	Queue bool `json:"queue,omitempty"`
	WIP   int  `json:"wip,omitempty"`
	// After holds a task back until other work has finished (CapTaskAfter):
	// a branch of the project, or the ID of a task still on the schedule.
	// The task then starts from that branch, stacked on it, unless it
	// names a Base of its own — or from the project's base, when the branch
	// has been merged and deleted meanwhile.
	After string `json:"after,omitempty"`
	// AfterBranch is the branch waited for, set by the server: After itself,
	// or the branch the task it names started on. AfterHead is that
	// branch's last commit seen, which tells a merged branch from a
	// discarded one once it is gone, and AfterSeen that its agent has been
	// seen at work — so an agent idle because it has not begun is not
	// taken for one that has finished.
	AfterBranch string `json:"after_branch,omitempty"`
	AfterHead   string `json:"after_head,omitempty"`
	AfterSeen   bool   `json:"after_seen,omitempty"`
	// Note says why, for lists: "after the 5-hour limit resets".
	Note    string    `json:"note,omitempty"`
	Created time.Time `json:"created"`
	// LastRun and LastError are how its last run went.
	LastRun   time.Time `json:"last_run,omitempty"`
	LastError string    `json:"last_error,omitempty"`
	// Waiting says why a due message has not gone yet: the agent is
	// working, or waiting on a question of its own.
	Waiting string `json:"waiting,omitempty"`
}

// DefaultWIP is how many agents a project runs before queued tasks wait:
// about as many as one person can review at once.
const DefaultWIP = 3

// ScheduleList is every item the server is keeping, soonest first.
type ScheduleList struct {
	Items []ScheduledItem `json:"items"`
}

// ReviewComment is one comment on a branch's diff, for its agent. Hunk is
// the hunk's text as it was when the comment was written, so the comment
// still says what it was about once the agent has rewritten those lines;
// without one, Line (1-based, in the new file) says where, and without
// either it is about the whole file.
//
// review.set takes one: with an ID it changes that comment, without one it
// changes the comment already on the same place or adds it, and an empty
// note removes it.
type ReviewComment struct {
	ID        string    `json:"id,omitempty"`
	ProjectID string    `json:"project_id"`
	Branch    string    `json:"branch"`
	File      string    `json:"file"`
	Hunk      string    `json:"hunk,omitempty"`
	Line      int       `json:"line,omitempty"`
	Note      string    `json:"note"`
	By        string    `json:"by,omitempty"` // an agent's pane that wrote it; "" for a person
	At        time.Time `json:"at,omitempty"`

	// SentTo is the agent a conch note was handed to (its pane's name),
	// SentPane that pane and SentAt when; SentAt is nil while it waits to
	// be sent, so a waiting note carries no date (review.thread.v1).
	// Replies then hold the answers.
	SentTo   string     `json:"sent_to,omitempty"`
	SentPane string     `json:"sent_pane,omitempty"`
	SentAt   *time.Time `json:"sent_at,omitempty"`

	// A comment from the branch's pull request (Source ReviewGitHub) is
	// read from GitHub and can't be changed here: By is the reviewer's
	// login, URL the comment, Outdated says the lines have changed since
	// it was written, and Replies are the rest of its thread. One with no
	// File is a review's own words about the pull request as a whole.
	Source   string        `json:"source,omitempty"`
	URL      string        `json:"url,omitempty"`
	Outdated bool          `json:"outdated,omitempty"`
	Replies  []ReviewReply `json:"replies,omitempty"`
}

// ReviewGitHub is the Source of a pull request's comment.
const ReviewGitHub = "github"

// ReviewReply is a later comment in a pull request's thread.
type ReviewReply struct {
	By   string    `json:"by,omitempty"`
	Note string    `json:"note"`
	At   time.Time `json:"at,omitempty"`
}

// ReviewRef names a branch whose comments to list.
type ReviewRef struct {
	ProjectID string `json:"project_id,omitempty"` // "" lists every project's
	Branch    string `json:"branch,omitempty"`
	// Sent lists the notes already sent as well, with their replies
	// (review.thread.v1); without it only those waiting to be sent.
	Sent bool `json:"sent,omitempty"`
}

// ReviewReplyParams answers note ID on a branch of ProjectID: an agent
// saying what it did about it, or the person answering back, which puts
// the note back among those waiting to be sent with its thread.
type ReviewReplyParams struct {
	ProjectID string `json:"project_id"`
	ID        string `json:"id"`
	Note      string `json:"note"`
}

// ReviewList is a branch's comments in the order they were written, or
// with no branch named every branch's.
type ReviewList struct {
	ProjectID string          `json:"project_id,omitempty"`
	Branch    string          `json:"branch,omitempty"`
	Comments  []ReviewComment `json:"comments"`
}

// ReviewSendParams hands a branch's comments to the agent working on it.
// With none there, Agent names one to start in its worktree; without it
// the send is refused. Pane names the agent to give them to instead of
// choosing one; New starts Agent even beside the agents already there.
type ReviewSendParams struct {
	ProjectID string `json:"project_id"`
	Branch    string `json:"branch"`
	Agent     string `json:"agent,omitempty"`
	Pane      string `json:"pane,omitempty"`
	New       bool   `json:"new,omitempty"`
	Cols      int    `json:"cols,omitempty"`
	Rows      int    `json:"rows,omitempty"`
}

// ReviewSendResult says where the comments went: an agent that was there,
// or one started with them (Started).
type ReviewSendResult struct {
	Pane    PaneInfo `json:"pane"`
	Started bool     `json:"started,omitempty"`
	Sent    int      `json:"sent"`
	Turn    int      `json:"turn,omitempty"`
}

// Agent states.
const (
	AgentIdle    = "idle"
	AgentWorking = "working"
	AgentBlocked = "blocked"
	AgentDone    = "done" // idle, and nobody has looked since it finished
)

// ReviewerPrefix names a reviewer's pane — the agent [verify] reviewer
// starts on a finished branch to read its diff and report — which is how
// conch knows one again, after a restart too: it is not the branch's
// writer, and it is closed once its report has gone where it was for.
const ReviewerPrefix = "review · "

// IsReviewer reports whether p is a reviewer's pane.
func IsReviewer(p PaneInfo) bool { return strings.HasPrefix(p.Name, ReviewerPrefix) }

// ReviewerDone reports whether p is a reviewer that has finished its
// report: running, its agent idle or done, and not stopped by an error —
// one still reading, asking, or failed is left for somebody to look at.
func ReviewerDone(p PaneInfo) bool {
	return IsReviewer(p) && p.State == PaneRunning && p.Agent != nil && !p.Agent.Failed &&
		(p.Agent.State == AgentDone || p.Agent.State == AgentIdle)
}

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
	// Turn counts the times the agent started working in this pane, so a
	// client that prompted it can tell the end of that work from a state
	// left over from before. It starts again from zero when the server
	// reloads, and servers without agent.prompt.v1 leave it zero.
	Turn int `json:"turn,omitempty"`
	// Subagents are the agents this one has running inside its own
	// process — Claude Code's Agent tool — which have no pane of their
	// own. Servers from before them send none.
	Subagents []Subagent `json:"subagents,omitempty"`
}

// Subagent is an agent running inside another agent's process, known from
// the hooks that say it started and stopped.
type Subagent struct {
	ID          string    `json:"id"`
	Type        string    `json:"type,omitempty"`        // e.g. general-purpose, Explore
	Description string    `json:"description,omitempty"` // the task it was given, when known
	Since       time.Time `json:"since"`
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
	Agent string `json:"agent"`
	// Account is which of the agent's accounts, "" for its own
	// (CapAccounts): each has a plan of its own.
	Account  string       `json:"account,omitempty"`
	FiveHour *LimitWindow `json:"five_hour,omitempty"`
	Week     *LimitWindow `json:"week,omitempty"`
	Spend    *LimitWindow `json:"spend,omitempty"`
	// Windows is every window the agent named, in the agent's own words.
	// An account with a per-model allowance reports more than the three
	// above — a weekly window for one model beside the weekly window for
	// all of them — and a new one appears whenever the plans change, so
	// what is passed on is the list rather than the three conch happens to
	// know. The three stay for servers and clients of older builds.
	Windows []NamedWindow `json:"windows,omitempty"`
	At      time.Time     `json:"at"` // when it was reported
}

// NamedWindow is one plan window under the name its agent gave it
// (five_hour, seven_day, seven_day_fable, spend_limit…).
type NamedWindow struct {
	Key      string    `json:"key"`
	UsedPct  float64   `json:"used_pct"`
	ResetsAt time.Time `json:"resets_at,omitempty"`
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
	// Tier is how far conch's support goes: "supported" for an agent with
	// an adapter, detection taken from the real thing and sessions that
	// list and resume, or "runs here" for one described by a manifest,
	// which conch starts and detects and no more. Empty from a server too
	// old to have tiers, where every agent it offers is supported.
	Tier string `json:"tier,omitempty"`
	// NoInstaller says conch cannot install this one: a manifest that
	// named no install script. Written the negative way round so that a
	// server too old to say it reads as "conch can install it", which is
	// what every agent such a server offers.
	NoInstaller bool `json:"no_installer,omitempty"`
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
	Agent string `json:"agent"`
	// Account is the account whose folder holds the conversation, "" for
	// the agent's own (CapAccounts); resuming it starts on that account.
	Account string    `json:"account,omitempty"`
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
	// Starred is a session somebody kept, to find again among the
	// hundreds a project collects. conch's own mark, not the agent's:
	// their files are theirs (session.star.v1).
	Starred bool `json:"starred,omitempty"`
}

// SessionStarParams marks a session as one to come back to, or stops.
// Agent and ID name it, as every session reference does.
type SessionStarParams struct {
	Agent string `json:"agent"`
	ID    string `json:"id"`
	On    bool   `json:"on"`
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
	// PaneName is what to call the pane that receives it: the name of the
	// session it continues, so the tree shows the same work rather than a
	// bare agent name. Empty leaves conch's default (the agent's name),
	// and a server that predates this ignores it.
	PaneName string `json:"pane_name,omitempty"`
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

// AgentPromptParams submits Text to the agent in pane ID as its next
// message.
type AgentPromptParams struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// ViewState is what a TUI is showing: its tabs in the order the bar
// lists them, and the pane the keyboard is on. A client publishes it
// when it changes; the server keeps the last one and answers view.get
// with it. Nothing of the layout is the server's own — it is carrying a
// TUI's answer to anything that asks.
type ViewState struct {
	// Client names the TUI, for telling two apart when somebody is
	// looking at the same machine from a phone and a terminal.
	Client string `json:"client,omitempty"`
	// Focus is the pane with the keyboard, "" when the tree has it.
	Focus string    `json:"focus,omitempty"`
	Tabs  []ViewTab `json:"tabs,omitempty"`
	// At is when the TUI published it.
	At time.Time `json:"at,omitempty"`
}

// ViewTab is one tab: what it is called and the panes it shows, left to
// right then top to bottom. A split showing a diff or a page rather than
// a pane contributes no id, since there is nothing an agent could
// address.
type ViewTab struct {
	Name   string   `json:"name,omitempty"`
	Panes  []string `json:"panes,omitempty"`
	Active bool     `json:"active,omitempty"`
}

// ViewShowParams asks the TUIs to show these panes together. One pane is
// a request to reveal it; several are a request to put them side by side.
type ViewShowParams struct {
	Panes []string `json:"panes"`
	// Name is what to call the tab, when one is opened.
	Name string `json:"name,omitempty"`
	// Why names the agent asking, for the line conch shows the person:
	// a screen that rearranges itself has to say who asked.
	Why string `json:"why,omitempty"`
}

// ViewShowResult is the panes the request was passed on for, in order
// and without the ones named twice.
type ViewShowResult struct {
	Panes []string `json:"panes,omitempty"`
}

// ChatTurn is one entry of an agent's conversation: what the person typed
// (user), what the agent said (assistant), or a tool it used (tool, Text
// its name and Detail what it was used on).
type ChatTurn struct {
	// Seq is its place in the whole conversation: stable as turns are
	// added, so an update replaces the turn it names.
	Seq    int       `json:"seq"`
	Role   string    `json:"role"`
	Text   string    `json:"text,omitempty"`
	Detail string    `json:"detail,omitempty"`
	Time   time.Time `json:"time,omitempty"`
	// For a tool: what it was given (an edit as - and + lines, InputKind
	// "diff"; a command or a file as "code"), what came back, whether it
	// failed, and whether its result has come yet.
	Input     string `json:"input,omitempty"`
	InputKind string `json:"input_kind,omitempty"`
	Output    string `json:"output,omitempty"`
	Error     bool   `json:"error,omitempty"`
	Done      bool   `json:"done,omitempty"`
}

// AgentChatParams asks for a pane's chat. A chat that has some sends the
// log's Epoch and Ver it has, and is sent only what is new or changed
// since; without them (or from another log) it is sent the newest turns
// with Reset.
type AgentChatParams struct {
	ID    string `json:"id"`
	Epoch string `json:"epoch,omitempty"`
	Ver   int64  `json:"ver,omitempty"`
}

// AgentChat is a pane's agent's conversation, newest last. Session is
// empty, and Turns with it, until the agent says which conversation it is
// in; Note says why a chat has nothing in it when it is not merely new.
type AgentChat struct {
	Agent   string     `json:"agent"`
	Session string     `json:"session,omitempty"`
	Turns   []ChatTurn `json:"turns"`
	Note    string     `json:"note,omitempty"`
	// Epoch and Ver name what the chat now has, to send next time; Reset
	// says Turns are the whole of it rather than what changed.
	Epoch string `json:"epoch,omitempty"`
	Ver   int64  `json:"ver,omitempty"`
	Reset bool   `json:"reset,omitempty"`
}

// AgentReply is the last answer of a pane's agent and when it was given.
type AgentReply struct {
	Agent string    `json:"agent"`
	Text  string    `json:"text"`
	Time  time.Time `json:"time,omitempty"`
}

// AgentPromptResult says which work answers the message: the agent's Turn
// reaching Turn. An agent already working when it was sent is given the
// message when that work ends, so its current Turn counts.
type AgentPromptResult struct {
	ID    string `json:"id"`
	Agent string `json:"agent"`
	Turn  int    `json:"turn"`
}

// CallerInfo is who a connection comes from, as its server sees it: the
// pane, and whether an agent runs there, so the server scopes it. ID names
// that agent's pane to other machines — the host, the pane and when it
// started, so a pane ID used again later is someone else — and Label says
// it to people.
type CallerInfo struct {
	Pane   string `json:"pane,omitempty"`
	Agent  string `json:"agent,omitempty"`
	Scoped bool   `json:"scoped,omitempty"`
	ID     string `json:"id,omitempty"`
	Label  string `json:"label,omitempty"`
}

// ActForParams holds a connection to what the agent CallerInfo names
// started on this machine. It can only narrow what the connection may do,
// so the server takes it at its word; it can't be changed once given.
type ActForParams struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
	Agent string `json:"agent,omitempty"`
}

// AgentSkillParams installs conch's skill for Agents (all it knows, when
// empty), or with Remove takes conch's copy away. Without Apply nothing is
// written: the result says what would be.
type AgentSkillParams struct {
	Agents []string `json:"agents,omitempty"`
	// Skills names which of conch's skills (conch, conch-review, …); all
	// of them when empty (agent.skill.pick.v1).
	Skills []string `json:"skills,omitempty"`
	Remove bool     `json:"remove,omitempty"`
	Apply  bool     `json:"apply,omitempty"`
}

// AgentSkillResult is what happens to each SKILL.md.
type AgentSkillResult struct {
	Changes []SkillChange `json:"changes"`
	Applied bool          `json:"applied,omitempty"`
}

// SkillChange is one SKILL.md and the agents that read it. Action is
// create, update, same, remove or skip (a skill of that name conch didn't
// write, left alone). Skill names which of conch's skills it is; a server
// from before conch had more than one leaves it out and means "conch".
type SkillChange struct {
	Skill  string   `json:"skill,omitempty"`
	Path   string   `json:"path"`
	Agents []string `json:"agents"`
	Action string   `json:"action"`
	Detail string   `json:"detail,omitempty"`
	Error  string   `json:"error,omitempty"`
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
	// Account is the session's own (SessionInfo.Account). The server finds
	// it when it is not given, so an older client resumes on the right one.
	Account string `json:"account,omitempty"`
	ID      string `json:"id,omitempty"`
	Dir     string `json:"dir"`
	Cols    int    `json:"cols,omitempty"`
	Rows    int    `json:"rows,omitempty"`
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

// AgentSyncParams gives the agents in To the setup From has in the
// checkout at Dir. With Apply false nothing is written: the changes are
// what it would do. Undo puts a sync back instead — Stamp names which, and
// "" is the last one.
type AgentSyncParams struct {
	Dir string `json:"dir"`
	// User syncs the setup in your home rather than a checkout's: the
	// instructions, skills and servers that follow you from project to
	// project. Dir is then only what a message calls the place.
	User  bool     `json:"user,omitempty"`
	From  string   `json:"from,omitempty"`
	To    []string `json:"to,omitempty"`
	Apply bool     `json:"apply,omitempty"`
	Undo  bool     `json:"undo,omitempty"`
	Stamp string   `json:"stamp,omitempty"`
}

// AgentSyncResult is what a sync would do, did, or put back.
type AgentSyncResult struct {
	Dir     string       `json:"dir"`
	From    string       `json:"from,omitempty"`
	To      []string     `json:"to,omitempty"`
	Applied bool         `json:"applied,omitempty"`
	Undone  bool         `json:"undone,omitempty"`
	Changes []SyncChange `json:"changes,omitempty"`
	Notes   []string     `json:"notes,omitempty"`
	// Undo is the record this apply left behind, to put it back with.
	Undo string `json:"undo,omitempty"`
	// Undos are the syncs of this checkout that can still be undone,
	// newest first.
	Undos []string `json:"undos,omitempty"`
}

// LibraryServer is an MCP server in conch's library. References to
// variables are written ${VAR}; conch writes each agent's own form.
type LibraryServer struct {
	Name      string            `json:"name"`
	Command   string            `json:"command,omitempty"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	URL       string            `json:"url,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	Transport string            `json:"transport,omitempty"` // "sse", or "" for streamable HTTP
	Agents    []string          `json:"agents"`
	// Plain: the person chose to keep its token in plain text, so Env and
	// Headers may hold values as they are rather than only ${VAR}
	// (CapLibraryPlain). They are written into each agent's file as given.
	Plain bool `json:"plain,omitempty"`
}

// LibrarySkill is a skill in the library: a folder holding a SKILL.md,
// linked into each agent's folder.
type LibrarySkill struct {
	Name   string   `json:"name"`
	Path   string   `json:"path"` // ~ is the home directory there
	Agents []string `json:"agents"`
	// Source is where on GitHub a fetched skill came from, to fetch it
	// again; empty for a folder of the person's own.
	Source string `json:"source,omitempty"`
}

// LibraryFetchParams asks for a skill on GitHub — owner/repo,
// owner/repo/path/to/skill, @ref at the end, or a github.com link — to be
// downloaded into the library under Name (its folder's name when empty).
type LibraryFetchParams struct {
	Source string `json:"source"`
	Name   string `json:"name,omitempty"`
}

// LibraryFetchResult is the skill fetched, ready to be given to agents as a
// library skill at Path. Found lists the skills a repository holds when
// Source named none of several; the person picks one.
type LibraryFetchResult struct {
	Name   string   `json:"name,omitempty"`
	Path   string   `json:"path,omitempty"`
	Source string   `json:"source,omitempty"`
	Found  []string `json:"found,omitempty"`
}

// Library is the MCP servers and skills conch keeps for the agents.
type Library struct {
	Servers []LibraryServer `json:"servers"`
	Skills  []LibrarySkill  `json:"skills"`
}

// LibraryCell is where one server or skill stands with one agent: on,
// pending, remove, off, theirs, differs or cannot.
type LibraryCell struct {
	Kind   string `json:"kind"` // "mcp server" or "skill"
	Name   string `json:"name"`
	Agent  string `json:"agent"`
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

// AgentLibraryParams asks for the library and where it stands. Set saves
// a new library first, and Import takes an agent's servers and skills into
// it first; neither writes to any agent.
type AgentLibraryParams struct {
	Set    *Library `json:"set,omitempty"`
	Import string   `json:"import,omitempty"`
}

// AgentLibraryResult is the library, where each item stands with each
// agent, and what an import took and left out.
type AgentLibraryResult struct {
	Library  Library       `json:"library"`
	Cells    []LibraryCell `json:"cells,omitempty"`
	Agents   []string      `json:"agents"`
	Undos    []string      `json:"undos,omitempty"`
	Imported []string      `json:"imported,omitempty"`
	Skipped  []string      `json:"skipped,omitempty"`
}

// LibraryApplyParams gives each agent what the library says. With Apply
// false nothing is written; Undo puts an apply back (Stamp, "" the last).
type LibraryApplyParams struct {
	Apply bool   `json:"apply,omitempty"`
	Undo  bool   `json:"undo,omitempty"`
	Stamp string `json:"stamp,omitempty"`
}

// What a sync change does: writing ones first.
const (
	SyncCreate = "create"
	SyncUpdate = "update"
	SyncLink   = "link"
	SyncRemove = "remove"
	SyncSame   = "same"
	SyncSkip   = "skip"
)

// Kinds of setup a sync carries.
const (
	SyncInstructions = "instructions"
	SyncSkill        = "skill"
	SyncMCP          = "mcp server"
)

// SyncChange is one thing a sync would do, or did: Action is create,
// update, link, same, skip or remove, and Path is inside the checkout.
type SyncChange struct {
	Agent  string `json:"agent,omitempty"`
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Path   string `json:"path,omitempty"`
	Action string `json:"action"`
	Detail string `json:"detail,omitempty"`
	Done   bool   `json:"done,omitempty"`
	Error  string `json:"error,omitempty"`
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
	// SubagentStart and SubagentStop name the subagent they are about.
	AgentID   string `json:"agent_id,omitempty"`
	AgentType string `json:"agent_type,omitempty"`
	// TaskDescription is what a SubagentStart's subagent was asked to do,
	// from Claude builds that say.
	TaskDescription string `json:"task_description,omitempty"`
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
	// Account is which of the agent's accounts it starts on (CapAccounts):
	// "" is the folder's pin, else the agent's own; "default" is its own.
	Account string `json:"account,omitempty"`
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
	// SSH runs Command on the host the pane SSH is logged in to, through
	// ssh with that pane's options and a terminal (pane.ssh.v1). Cwd is
	// where ssh runs here; "" is that pane's folder.
	SSH string `json:"ssh,omitempty"`
}

// ShellThemes describes the prompt themes available on a machine.
type ShellThemes struct {
	Shell   string   `json:"shell"`             // login shell, e.g. zsh
	OMZ     bool     `json:"omz"`               // Oh My Zsh is installed
	Current string   `json:"current,omitempty"` // ZSH_THEME set in .zshrc
	Themes  []string `json:"themes,omitempty"`  // sorted
	// Samples is each theme's prompt as zsh expands it, so a person can
	// see what they are choosing. A server that cannot run zsh, or one
	// from an older build, sends none and the names stand alone.
	Samples map[string]string `json:"samples,omitempty"`
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
	// Reply says the text is not the person typing: a terminal answering
	// the program (pane.raw's client), or the pointer moving over it. It
	// reaches the program the same, but takes the pane's size for nobody
	// and is not input that tells an agent's state (pane.raw.v1; an
	// older server takes it as typed).
	Reply bool `json:"reply,omitempty"`
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
	// Cursor is where the cursor is drawn, when it is: the program is
	// running, has not hidden it, and the view is not scrolled back.
	Cursor *FrameCursor `json:"cursor,omitempty"`
}

// UsageHistory is what agents on a machine used, as of At: each agent's
// periods (the last 24 hours, 7 days and 30 days), with what each model
// used within them. An agent with nothing saved in the last 30 days is
// left out.
type UsageHistory struct {
	At     time.Time    `json:"at"`
	Agents []AgentUsage `json:"agents,omitempty"`
}

// AgentUsage is one agent's periods, shortest first.
type AgentUsage struct {
	Agent   string        `json:"agent"`
	Periods []UsagePeriod `json:"periods"`
}

// UsagePeriod is what was used in the last Hours hours, in all and by
// model, the model that used most first.
type UsagePeriod struct {
	Hours int `json:"hours"`
	UsageTokens
	Models []ModelUsage `json:"models,omitempty"`
}

// ModelUsage is what one model used in a period.
type ModelUsage struct {
	Model string `json:"model"`
	UsageTokens
}

// UsageTokens counts tokens: input not read from the cache, input written
// to it and read from it, output, and how many responses they came from.
type UsageTokens struct {
	Input      int `json:"input"`
	CacheWrite int `json:"cache_write,omitempty"`
	CacheRead  int `json:"cache_read,omitempty"`
	Output     int `json:"output"`
	Messages   int `json:"messages,omitempty"`
}

// Add adds another's counts to these.
func (u *UsageTokens) Add(o UsageTokens) {
	u.Input += o.Input
	u.CacheWrite += o.CacheWrite
	u.CacheRead += o.CacheRead
	u.Output += o.Output
	u.Messages += o.Messages
}

// Total is every token counted.
func (u UsageTokens) Total() int { return u.Input + u.CacheWrite + u.CacheRead + u.Output }

// PaneRawStart is what pane.raw answers: output that rebuilds the pane's
// history and screen in a fresh terminal of Cols×Rows, after which every
// EventPaneOutput carries what the program wrote next.
type PaneRawStart struct {
	ID     string `json:"id"`
	Cols   int    `json:"cols"`
	Rows   int    `json:"rows"`
	Replay []byte `json:"replay"`
}

// PaneOutput is a piece of raw output. Reset says the connection fell
// behind and pieces were lost: Data is then a fresh replay (as
// PaneRawStart's) for a terminal cleared first, at Cols×Rows.
type PaneOutput struct {
	ID    string `json:"id"`
	Data  []byte `json:"data"`
	Reset bool   `json:"reset,omitempty"`
	Cols  int    `json:"cols,omitempty"`
	Rows  int    `json:"rows,omitempty"`
}

// FrameCursor is a cell of a frame, counted from 0 at the top left.
type FrameCursor struct {
	Row int `json:"row"`
	Col int `json:"col"`
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

// CloudAgentRegisterParams names the cloud agent's own socket, where the
// server asks for secrets with a CloudAgentAsk.
type CloudAgentRegisterParams struct {
	Socket string `json:"socket"`
	// Key is what the server sends back with each CloudAgentAsk for env,
	// so the agent's socket gives secrets to this server alone.
	Key string `json:"key,omitempty"`
}

// CloudAgentRegisterResult is the server's capabilities once the cloud
// agent is registered, which the agent reports to the control plane.
type CloudAgentRegisterResult struct {
	Capabilities []string `json:"capabilities"`
}

// CloudAgentAsk is one line asked of the cloud agent's socket, answered by
// one CloudAgentAnswer line.
type CloudAgentAsk struct {
	// Op is "env" (the env-mode secrets, for an agent being started) or
	// "git-credential" (the machine token, for git's credential helper).
	Op string `json:"op"`
	// Key is the registration's (CloudAgentRegisterParams.Key), which env
	// needs.
	Key string `json:"key,omitempty"`
}

// What a CloudAgentAsk may ask.
const (
	CloudAskEnv           = "env"
	CloudAskGitCredential = "git-credential"
)

// CloudAgentAnswer is the cloud agent's answer: Env for env, Username and
// Password for git-credential, Error when it has none to give.
type CloudAgentAnswer struct {
	Env      map[string]string `json:"env,omitempty"`
	Username string            `json:"username,omitempty"`
	Password string            `json:"password,omitempty"`
	Error    string            `json:"error,omitempty"`
}

package server

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/Amitgb14/conch/internal/detect"
	"github.com/Amitgb14/conch/internal/proto"
)

// Scoping keeps an agent that drives conch from inside a pane to its own
// work: the panes it started (and theirs), and the project it is in. It is
// a guard against a confused model closing the wrong session, not a
// sandbox: a program set on escaping can leave the pane's process tree.
//
// Which pane a connection comes from is asked of the kernel — the peer's
// process and its parents — never taken from what the caller says, so
// unsetting CONCH_PANE_ID changes nothing. Only a caller in a pane running
// an agent is scoped: a terminal pane, the TUI and anything outside conch
// act as the person they belong to.

// maxAncestors bounds the walk up from a caller to its pane.
const maxAncestors = 64

// callerPane finds the pane a process runs in: the pane whose program is
// the process or one of its ancestors. "" for a process in no pane.
func (s *Server) callerPane(pid int) string {
	if pid <= 0 {
		return ""
	}
	byPID := map[int]string{}
	for _, info := range s.list() {
		if info.State == proto.PaneRunning && info.PID > 0 {
			byPID[info.PID] = info.ID
		}
	}
	for i := 0; i < maxAncestors && pid > 1; i++ {
		if id, ok := byPID[pid]; ok {
			return id
		}
		parent, err := detect.ParentPID(pid)
		if err != nil || parent == pid {
			return ""
		}
		pid = parent
	}
	return ""
}

// scopeKind says what a method acts on, for the check.
type scopeKind int

const (
	scopePane    scopeKind = iota + 1 // params.id is a pane
	scopePanes                        // params.ids are panes
	scopeProject                      // params.project_id is a project
	scopeProjID                       // params.id is a project
	scopeServer                       // the whole server
)

// scoped lists the methods that change something an agent could get wrong:
// typing into, closing or renaming a pane, marking its work seen (which
// hides it from the person), reporting its state, and a project's branches
// and worktrees. Reading and creating are left open: a reviewer has to
// read, and what an agent starts is its own.
var scoped = map[string]scopeKind{
	proto.MethodPaneClose: scopePane, proto.MethodPaneSendText: scopePane, proto.MethodPaneSendKeys: scopePane,
	proto.MethodPaneSendMouse: scopePane, proto.MethodPaneResize: scopePane, proto.MethodPaneRename: scopePane,
	proto.MethodPaneMarkSeen: scopePane, proto.MethodPaneMonitor: scopePane, proto.MethodAgentReport: scopePane,
	proto.MethodAgentPrompt:    scopePane,
	proto.MethodAgentBroadcast: scopePanes,

	proto.MethodProjectRemove:  scopeProjID,
	proto.MethodWorktreeRemove: scopeProject, proto.MethodWorktreeCleanup: scopeProject,
	proto.MethodBranchCommit: scopeProject, proto.MethodBranchPush: scopeProject, proto.MethodBranchPR: scopeProject,
	proto.MethodBranchMerge: scopeProject, proto.MethodBranchDiscard: scopeProject,

	proto.MethodServerStop: scopeServer, proto.MethodServerReload: scopeServer,
}

// scopeRef is every field scoped methods name their target by.
type scopeRef struct {
	ID        string   `json:"id"`
	IDs       []string `json:"ids"`
	PaneID    string   `json:"pane_id"`
	ProjectID string   `json:"project_id"`
}

// inScope refuses a scoped method from an agent's pane when its target is
// not the agent's to change. A target that doesn't exist is let through,
// for the method's own not-found.
func (s *Server) inScope(c *client, msg proto.Message) *proto.Error {
	kind := scoped[msg.Method]
	if msg.Method == proto.MethodSessionShare {
		kind = scopePane // only when handing to a running pane; see below
	}
	if kind == 0 {
		return nil
	}
	caller, ok := s.scopedCaller(c)
	if !ok {
		return nil
	}
	var ref scopeRef
	if len(msg.Params) > 0 && json.Unmarshal(msg.Params, &ref) != nil {
		return nil // the method reports its own bad params
	}
	switch kind {
	case scopeServer:
		return outOfScope(caller, "%s the server: it runs every pane, not only this agent's", verb(msg.Method))
	case scopeProjID:
		return s.projectInScope(caller, msg.Method, ref.ID)
	case scopeProject:
		return s.projectInScope(caller, msg.Method, ref.ProjectID)
	case scopePanes:
		for _, id := range ref.IDs {
			if perr := s.paneInScope(caller, msg.Method, id); perr != nil {
				return perr
			}
		}
		return nil
	}
	id := ref.ID
	if msg.Method == proto.MethodSessionShare {
		if ref.PaneID == "" {
			return nil // a new pane: created, not changed
		}
		id = ref.PaneID
	}
	return s.paneInScope(caller, msg.Method, id)
}

// scopeCaller is an agent a connection is scoped to: one in a pane here,
// or one on another machine that the connection acts for (remote), which
// has no pane or project here, only what it started.
type scopeCaller struct {
	id, label, agent, project string
	remote                    bool
}

// scopedCaller is who the connection is held to, if anyone: the agent it
// acts for, else the agent running in its pane now.
func (s *Server) scopedCaller(c *client) (scopeCaller, bool) {
	c.mu.Lock()
	actFor := c.actFor
	c.mu.Unlock()
	if actFor.ID != "" {
		label := actFor.Label
		if label == "" {
			label = actFor.ID
		}
		return scopeCaller{id: actFor.ID, label: label, agent: actFor.Agent, remote: true}, true
	}
	if c.pane == "" {
		return scopeCaller{}, false
	}
	e, perr := s.get(c.pane)
	if perr != nil {
		return scopeCaller{}, false
	}
	info := e.info()
	if info.State != proto.PaneRunning || info.Agent == nil {
		return scopeCaller{}, false
	}
	return scopeCaller{id: info.ID, label: info.ID, agent: info.Agent.Name, project: info.ProjectID}, true
}

// startedBy reports whether caller started e, or started what started it.
func startedBy(e *entry, caller scopeCaller) bool {
	for _, by := range e.creators() {
		if by == caller.id {
			return true
		}
	}
	return false
}

func (s *Server) paneInScope(caller scopeCaller, method, id string) *proto.Error {
	e, perr := s.get(id)
	if perr != nil || (!caller.remote && id == caller.id) {
		return nil
	}
	if caller.project != "" && e.info().ProjectID == caller.project {
		return nil
	}
	if startedBy(e, caller) {
		return nil
	}
	if caller.remote {
		return outOfScope(caller, "%s %s: it did not start it", verb(method), id)
	}
	return outOfScope(caller, "%s %s: it did not start it, and it is in another project", verb(method), id)
}

// projectInScope: a project is the agent's when it works in it — for an
// agent on another machine, when it started a pane there.
func (s *Server) projectInScope(caller scopeCaller, method, id string) *proto.Error {
	if id == "" || id == caller.project {
		return nil
	}
	if _, perr := s.projects.get(id); perr != nil {
		return nil
	}
	if caller.remote {
		s.mu.Lock()
		entries := make([]*entry, 0, len(s.panes))
		for _, e := range s.panes {
			entries = append(entries, e)
		}
		s.mu.Unlock()
		for _, e := range entries {
			if startedBy(e, caller) && e.info().ProjectID == id {
				return nil
			}
		}
		return outOfScope(caller, "%s in project %s: it started nothing there", verb(method), id)
	}
	where := "no project"
	if caller.project != "" {
		where = "project " + caller.project
	}
	return outOfScope(caller, "%s in project %s: it works in %s", verb(method), id, where)
}

func outOfScope(caller scopeCaller, format string, args ...any) *proto.Error {
	who := "the agent"
	if caller.agent != "" {
		who = "the " + caller.agent + " agent"
	}
	return proto.Errorf(proto.ErrOutOfScope, "%s in %s may not %s; do it from the TUI or a terminal pane",
		who, caller.label, fmt.Sprintf(format, args...))
}

// verb says what a method does, for a refusal.
func verb(method string) string {
	if v, ok := verbs[method]; ok {
		return v
	}
	return "use " + method + " on"
}

var verbs = map[string]string{
	proto.MethodPaneClose: "close", proto.MethodPaneSendText: "type into", proto.MethodPaneSendKeys: "send keys to",
	proto.MethodPaneSendMouse: "click in", proto.MethodPaneResize: "resize", proto.MethodPaneRename: "rename",
	proto.MethodPaneMarkSeen: "mark as seen", proto.MethodPaneMonitor: "set monitoring on",
	proto.MethodAgentReport: "report the state of", proto.MethodAgentPrompt: "prompt",
	proto.MethodAgentBroadcast: "send a message to", proto.MethodSessionShare: "hand a session to",
	proto.MethodServerStop: "stop", proto.MethodServerReload: "reload",
	proto.MethodProjectRemove: "remove a project", proto.MethodWorktreeRemove: "remove a worktree",
	proto.MethodWorktreeCleanup: "clean up worktrees", proto.MethodBranchCommit: "commit",
	proto.MethodBranchPush: "push", proto.MethodBranchPR: "open a pull request",
	proto.MethodBranchMerge: "merge", proto.MethodBranchDiscard: "discard a branch",
}

// madeBy records that the connection's pane started pane id: it and the
// panes that started it may then act on the new one. Panes started from
// outside any pane have no creator.
func (s *Server) madeBy(c *client, id string) {
	c.mu.Lock()
	actFor := c.actFor.ID
	c.mu.Unlock()
	var lineage []string
	switch {
	case id == "":
		return
	case actFor != "": // an agent on another machine
		lineage = []string{actFor}
	case c.pane == "" || id == c.pane:
		return
	default:
		lineage = []string{c.pane}
		if by, perr := s.get(c.pane); perr == nil {
			lineage = append(lineage, by.creators()...)
		}
	}
	e, perr := s.get(id)
	if perr != nil {
		return
	}
	e.mu.Lock()
	if e.lineage == nil {
		e.lineage = lineage
	}
	e.mu.Unlock()
}

// creators is the pane that started e, then the one that started that,
// and so on.
func (e *entry) creators() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.lineage...)
}

// made records the connection's pane as the creator of a pane just
// started, and answers with the pane as it now is.
//
//	return s.made(c)(s.create(cp))
func (s *Server) made(c *client) func(proto.PaneInfo, *proto.Error) (proto.PaneInfo, *proto.Error) {
	return func(info proto.PaneInfo, perr *proto.Error) (proto.PaneInfo, *proto.Error) {
		if perr != nil {
			return info, perr
		}
		s.madeBy(c, info.ID)
		return s.infoOf(info), nil
	}
}

// infoOf is info refreshed from its pane, when the pane is still there.
func (s *Server) infoOf(info proto.PaneInfo) proto.PaneInfo {
	if e, perr := s.get(info.ID); perr == nil {
		return e.info()
	}
	return info
}

// callerInfo answers pane.caller: the connection's pane, and the name its
// agent goes by on other machines.
func (s *Server) callerInfo(c *client) proto.CallerInfo {
	if c.pane == "" {
		return proto.CallerInfo{}
	}
	e, perr := s.get(c.pane)
	if perr != nil {
		return proto.CallerInfo{}
	}
	info := e.info()
	ci := proto.CallerInfo{Pane: info.ID}
	if info.State == proto.PaneRunning && info.Agent != nil {
		host, _ := os.Hostname()
		ci.Agent, ci.Scoped = info.Agent.Name, true
		ci.ID = fmt.Sprintf("%s/%s@%d", host, info.ID, info.Created.Unix())
		ci.Label = info.ID + " on " + host
	}
	return ci
}

// actFor holds the connection to an agent on another machine. Only a
// connection from outside this machine's panes may ask — one from inside
// is already its own pane — and only once.
func (s *Server) actFor(c *client, p proto.ActForParams) *proto.Error {
	if p.ID == "" {
		return proto.Errorf(proto.ErrBadRequest, "scope.act_for needs the caller's id")
	}
	if c.pane != "" {
		return proto.Errorf(proto.ErrBadRequest, "this connection comes from pane %s here, and is scoped as that", c.pane)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.actFor.ID != "" && c.actFor.ID != p.ID {
		return proto.Errorf(proto.ErrBadRequest, "this connection already acts for %s", c.actFor.ID)
	}
	c.actFor = p
	return nil
}

package server

import (
	"encoding/json"
	"fmt"

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
	if kind == 0 || c.pane == "" {
		return nil
	}
	caller, ok := s.agentCaller(c)
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

// agentCaller is the connection's pane when an agent runs in it now.
func (s *Server) agentCaller(c *client) (proto.PaneInfo, bool) {
	e, perr := s.get(c.pane)
	if perr != nil {
		return proto.PaneInfo{}, false
	}
	info := e.info()
	return info, info.State == proto.PaneRunning && info.Agent != nil
}

func (s *Server) paneInScope(caller proto.PaneInfo, method, id string) *proto.Error {
	e, perr := s.get(id)
	if perr != nil || id == caller.ID {
		return nil
	}
	target := e.info()
	if caller.ProjectID != "" && target.ProjectID == caller.ProjectID {
		return nil
	}
	for _, by := range e.creators() {
		if by == caller.ID {
			return nil
		}
	}
	return outOfScope(caller, "%s %s: it did not start it, and it is in another project", verb(method), id)
}

func (s *Server) projectInScope(caller proto.PaneInfo, method, id string) *proto.Error {
	if id == "" || id == caller.ProjectID {
		return nil
	}
	if _, perr := s.projects.get(id); perr != nil {
		return nil
	}
	where := "no project"
	if caller.ProjectID != "" {
		where = "project " + caller.ProjectID
	}
	return outOfScope(caller, "%s in project %s: it works in %s", verb(method), id, where)
}

func outOfScope(caller proto.PaneInfo, format string, args ...any) *proto.Error {
	return proto.Errorf(proto.ErrOutOfScope, "the %s agent in %s may not %s; do it from the TUI or a terminal pane",
		caller.Agent.Name, caller.ID, fmt.Sprintf(format, args...))
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
	if c.pane == "" || id == "" || id == c.pane {
		return
	}
	e, perr := s.get(id)
	if perr != nil {
		return
	}
	lineage := []string{c.pane}
	if by, perr := s.get(c.pane); perr == nil {
		lineage = append(lineage, by.creators()...)
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

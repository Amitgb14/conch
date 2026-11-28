package server

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/sysstat"
)

// system.stats (system.stats.v1): the whole machine, for the window beside
// the resource monitor. The reading is sysstat's; what the server adds is
// which processes are conch's — each pane's program and everything under
// it, named by the pane, its project, branch and agent, and the server
// itself — since a heavy process is only half an answer without where it
// came from.

// systemSource is where the machine is read from; tests give their own.
var systemSource = sysstat.Local

func (s *Server) systemStats(p proto.SystemStatsParams) proto.SystemStats {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.system.Stats(ctx, p, s.tagProcesses)
}

// paneOwner is what a process under a pane is said to belong to.
type paneOwner struct {
	pid                              int
	id, name, project, branch, agent string
	state                            string // the agent's, when there is one
	since                            time.Time
}

// conchNames are the names conch's own programs run under: a client
// outside any pane — conch attach, conch web, a machine's bridge, a TUI —
// is one of these, and the server cannot otherwise tell it from anything
// else on the machine.
func conchNames() map[string]bool {
	names := map[string]bool{"conch": true}
	if exe, err := os.Executable(); err == nil {
		names[filepath.Base(exe)] = true
	}
	return names
}

// tagProcesses names every process conch started: a pane's tree first,
// then the server's own — the panes are its children, so it would
// otherwise claim them.
func (s *Server) tagProcesses(all []proto.SystemProcess) {
	s.mu.Lock()
	entries := make([]*entry, 0, len(s.order))
	for _, id := range s.order {
		entries = append(entries, s.panes[id])
	}
	s.mu.Unlock()
	var owners []paneOwner
	for _, e := range entries {
		info := e.info()
		if info.State != proto.PaneRunning || info.PID <= 0 {
			continue
		}
		o := paneOwner{pid: info.PID, id: info.ID, name: info.Name, branch: info.Branch}
		if info.Title != "" && !info.CustomName {
			o.name = info.Title
		}
		if info.Agent != nil {
			o.agent, o.state, o.since = info.Agent.Name, info.Agent.State, info.Agent.Since
		}
		if info.ProjectID != "" {
			if proj, perr := s.projects.get(info.ProjectID); perr == nil {
				proj.mu.Lock()
				o.project = proj.info.Name
				proj.mu.Unlock()
			}
		}
		owners = append(owners, o)
	}
	tagOwned(all, owners, os.Getpid(), conchNames())
}

// tagOwned marks all by owners — each pane's tree, the first root to reach
// a process keeping it — then the server's tree, then any other process of
// conch's own (by its name) outside a pane as a client.
func tagOwned(all []proto.SystemProcess, owners []paneOwner, me int, names map[string]bool) {
	roots := make([]int, 0, len(owners)+1)
	for _, o := range owners {
		roots = append(roots, o.pid)
	}
	roots = append(roots, me)
	trees := sysstat.Trees(all, roots)
	at := make(map[int]int, len(all))
	for i, p := range all {
		at[p.PID] = i
	}
	for _, o := range owners {
		for _, pid := range trees[o.pid] {
			p := &all[at[pid]]
			p.Pane, p.PaneName, p.Project, p.Branch, p.Agent = o.id, o.name, o.project, o.branch, o.agent
			if o.agent != "" {
				p.AgentState, p.AgentSince = o.state, o.since
			}
		}
	}
	for _, pid := range trees[me] {
		all[at[pid]].Conch = "server"
	}
	for i := range all {
		if all[i].Pane == "" && all[i].Conch == "" && names[all[i].Name] {
			all[i].Conch = "client"
		}
	}
}

package server

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/sessions"
)

// keepInterrupted is how long an interrupted run is offered for resuming.
const keepInterrupted = 7 * 24 * time.Hour

// agentRun is an agent conch ran in a pane.
type agentRun struct {
	Pane    string    `json:"pane"`
	Agent   string    `json:"agent"`
	Dir     string    `json:"dir"`
	Session string    `json:"session,omitempty"` // when the agent reported it (Claude's hooks)
	Started time.Time `json:"started"`
	Seen    time.Time `json:"seen"`
}

// runLog remembers the agents running in panes, on disk, so that after the
// server stops (a restart, a reboot) they can be offered for resuming.
type runLog struct {
	path string

	mu          sync.Mutex
	active      map[string]agentRun // by pane id
	interrupted []agentRun
}

type runLogFile struct {
	Active      []agentRun `json:"active"`
	Interrupted []agentRun `json:"interrupted"`
}

// loadRunLog reads the log. Runs still marked active belong to a server that
// is gone: they become interrupted.
func loadRunLog(configDir string, keepActive bool) *runLog {
	l := &runLog{path: filepath.Join(configDir, "agent-runs.json"), active: map[string]agentRun{}}
	b, err := os.ReadFile(l.path)
	if err != nil {
		return l
	}
	var f runLogFile
	if err := json.Unmarshal(b, &f); err != nil {
		log.Printf("agent runs: %v", err)
		return l
	}
	if keepActive {
		for _, r := range f.Active {
			l.active[r.Pane] = r
		}
		f.Active = nil
	}
	for _, r := range append(f.Interrupted, f.Active...) {
		if time.Since(r.Seen) < keepInterrupted {
			l.interrupted = append(l.interrupted, r)
		}
	}
	if len(f.Active) > 0 {
		log.Printf("%d agent(s) were running when the previous server stopped", len(f.Active))
	}
	l.saveLocked()
	return l
}

func (l *runLog) saveLocked() {
	f := runLogFile{Active: []agentRun{}, Interrupted: l.interrupted}
	for _, r := range l.active {
		f.Active = append(f.Active, r)
	}
	if f.Interrupted == nil {
		f.Interrupted = []agentRun{}
	}
	b, _ := json.MarshalIndent(f, "", "  ")
	tmp := l.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, l.path)
}

// note records what a pane runs; it writes only when something changed, or
// once a minute to keep Seen roughly current.
func (l *runLog) note(info proto.PaneInfo, dir string, created time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	cur, had := l.active[info.ID]
	if info.Agent == nil || info.State != proto.PaneRunning {
		if had {
			delete(l.active, info.ID)
			l.saveLocked()
		}
		return
	}
	next := agentRun{Pane: info.ID, Agent: info.Agent.Name, Dir: dir, Session: info.Agent.SessionID, Started: created, Seen: time.Now()}
	if next.Session == "" && cur.Agent == next.Agent {
		next.Session = cur.Session
	}
	if had && cur.Agent == next.Agent && cur.Session == next.Session && cur.Dir == next.Dir && time.Since(cur.Seen) < time.Minute {
		return
	}
	l.active[info.ID] = next
	l.saveLocked()
}

// forget drops a pane that was closed or whose agent exited on its own.
func (l *runLog) forget(pane string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.active[pane]; ok {
		delete(l.active, pane)
		l.saveLocked()
	}
}

// resolve removes the interrupted runs a session accounts for.
func (l *runLog) resolve(agent, id, dir string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	kept := l.interrupted[:0]
	for _, r := range l.interrupted {
		if r.Agent == agent && r.Dir == dir && (id == "" || r.Session == "" || r.Session == id) {
			continue
		}
		kept = append(kept, r)
	}
	l.interrupted = kept
	l.saveLocked()
}

func (l *runLog) interruptedRuns() []agentRun {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]agentRun(nil), l.interrupted...)
}

// ---- session.list / resume ----

func (s *Server) listSessions(p proto.SessionListParams) (proto.SessionList, *proto.Error) {
	var dirs []string
	var proj *project
	switch {
	case p.ProjectID != "":
		var perr *proto.Error
		if proj, perr = s.projects.get(p.ProjectID); perr != nil {
			return proto.SessionList{}, perr
		}
		dirs = proj.roots()
	case p.Dir != "":
		dirs = []string{p.Dir}
		proj = s.projects.containing(p.Dir)
	default:
		return proto.SessionList{}, proto.Errorf(proto.ErrBadRequest, "session.list needs a project or a directory")
	}
	limit := p.Limit
	if limit <= 0 {
		limit = 100
	}
	found := sessions.List(sessions.CurrentEnv(), dirs, limit)

	out := make([]proto.SessionInfo, 0, len(found))
	for _, f := range found {
		info := proto.SessionInfo{Agent: f.Agent, ID: f.ID, Dir: f.Dir, Branch: f.Branch, Title: f.Title, Started: f.Started, Updated: f.Updated}
		out = append(out, info)
	}

	// Sessions open in panes: Claude names its session; for the others, the
	// newest session of that agent in the pane's directory since it started.
	s.mu.Lock()
	entries := make([]*entry, 0, len(s.panes))
	for _, e := range s.panes {
		entries = append(entries, e)
	}
	s.mu.Unlock()
	claimed := map[int]bool{}
	for _, e := range entries {
		pi := e.info()
		if pi.Agent == nil || pi.State != proto.PaneRunning {
			continue
		}
		best := -1
		for i, si := range out {
			if claimed[i] || si.Agent != pi.Agent.Name {
				continue
			}
			if pi.Agent.SessionID != "" {
				if si.ID == pi.Agent.SessionID {
					best = i
					break
				}
				continue
			}
			if si.Dir == e.dir && !si.Updated.Before(pi.Created.Add(-time.Minute)) && (best < 0 || si.Updated.After(out[best].Updated)) {
				best = i
			}
		}
		if best >= 0 {
			claimed[best] = true
			out[best].PaneID = pi.ID
		}
	}

	// Interrupted runs: mark their sessions, or list the run itself when the
	// agent saved nothing findable.
	for _, r := range s.runs.interruptedRuns() {
		if !withinAny(r.Dir, dirs) {
			continue
		}
		best := -1
		for i, si := range out {
			if si.Agent != r.Agent || si.PaneID != "" {
				continue
			}
			if r.Session != "" {
				if si.ID == r.Session {
					best = i
					break
				}
				continue
			}
			if si.Dir == r.Dir && !si.Updated.Before(r.Started.Add(-time.Minute)) && !si.Updated.After(r.Seen.Add(10*time.Minute)) &&
				(best < 0 || si.Updated.After(out[best].Updated)) {
				best = i
			}
		}
		if best >= 0 {
			out[best].Interrupted = true
			continue
		}
		out = append(out, proto.SessionInfo{Agent: r.Agent, ID: r.Session, Dir: r.Dir, Title: "interrupted run · resumes the latest session here",
			Started: r.Started, Updated: r.Seen, Interrupted: true})
	}

	for i := range out {
		if out[i].Branch == "HEAD" {
			out[i].Branch = ""
		}
		if proj != nil {
			if wt, ok := proj.worktreeFor(out[i].Dir); ok && wt.Branch != "" {
				out[i].Branch = wt.Branch
			}
		}
	}
	sortSessions(out)
	return proto.SessionList{Sessions: out}, nil
}

// sortSessions puts interrupted sessions first, then newest first.
func sortSessions(out []proto.SessionInfo) {
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && lessSession(out[j], out[j-1]); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
}

func lessSession(a, b proto.SessionInfo) bool {
	if a.Interrupted != b.Interrupted {
		return a.Interrupted
	}
	return a.Updated.After(b.Updated)
}

func withinAny(path string, dirs []string) bool {
	for _, d := range dirs {
		if within(path, d) {
			return true
		}
	}
	return false
}

// resumeSession starts the agent in the session's directory, reopening it.
func (s *Server) resumeSession(r proto.SessionRef) (proto.PaneInfo, *proto.Error) {
	ad, ok := s.adapters.Get(r.Agent)
	if !ok {
		return proto.PaneInfo{}, proto.Errorf(proto.ErrBadRequest, "conch can't launch %q", r.Agent)
	}
	if st, err := os.Stat(r.Dir); err != nil || !st.IsDir() {
		return proto.PaneInfo{}, proto.Errorf(proto.ErrBadRequest, "%s no longer exists (a removed worktree?)", r.Dir)
	}
	info, perr := s.create(proto.PaneCreateParams{Agent: r.Agent, AgentArgs: ad.ResumeArgs(r.ID), Cwd: r.Dir, Cols: r.Cols, Rows: r.Rows})
	if perr != nil {
		return info, perr
	}
	s.runs.resolve(r.Agent, r.ID, r.Dir)
	log.Printf("pane %s resumed %s session %q in %s", info.ID, r.Agent, r.ID, r.Dir)
	return info, nil
}

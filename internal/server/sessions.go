package server

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/Amitgb14/conch/internal/accounts"
	"github.com/Amitgb14/conch/internal/adapter"
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
	Account string    `json:"account,omitempty"` // which of the agent's accounts: it resumes there and nowhere else
	Name    string    `json:"name,omitempty"`    // the pane's, which other agents address it by
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
	// fresh are the runs the previous server was in the middle of when it
	// went without being asked to — a reboot, a crash, a kill — which this
	// one resumes by itself (autoResume). stopped is that server having
	// been asked to stop: what it ran is offered, not restarted.
	fresh   []agentRun
	stopped bool
	// unsettled is a log read and not yet written back (settle).
	unsettled bool
	// ended are agents whose panes ended without being closed (ended.go).
	ended []proto.EndedAgent
}

type runLogFile struct {
	Active      []agentRun `json:"active"`
	Interrupted []agentRun `json:"interrupted"`
	// Stopped says the server writing this was stopped on purpose
	// (conch server stop), so its active runs are not resumed by the next.
	Stopped bool `json:"stopped,omitempty"`
	// Ended is pane.ended.v1's records (ended.go); older servers drop it.
	Ended []proto.EndedAgent `json:"ended,omitempty"`
}

// loadRunLog reads the log. Runs still marked active belong to a server that
// is gone: they become interrupted, and — unless that server was stopped on
// purpose — fresh, for this one to resume.
func loadRunLog(configDir string, keepActive bool) *runLog {
	l := readRunLog(configDir, keepActive)
	l.settle()
	return l
}

// readRunLog is loadRunLog without the writing back, which waits until
// this server owns the socket (settle): a second server that finds one
// running reads the live server's log too, and must leave it as it was.
func readRunLog(configDir string, keepActive bool) *runLog {
	l := &runLog{path: filepath.Join(configDir, "agent-runs.json"), active: map[string]agentRun{}}
	b, err := os.ReadFile(l.path)
	if err != nil {
		return l
	}
	var f runLogFile
	if err := json.Unmarshal(b, &f); err != nil {
		log.Printf("agent runs: %v", err) // and never overwritten
		return l
	}
	l.unsettled = true
	l.ended = f.Ended
	l.pruneEndedLocked(time.Now())
	// Logs written before triagers were left out (note) may hold one.
	f.Active, f.Interrupted = dropTriagers(f.Active), dropTriagers(f.Interrupted)
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
	for _, r := range f.Active {
		if time.Since(r.Seen) >= keepInterrupted {
			continue
		}
		if f.Stopped {
			// Not resumed by itself: kept where it was, to resume there.
			l.addEndedLocked(endedFromRun(r, proto.EndedServer))
		} else {
			l.fresh = append(l.fresh, r)
		}
	}
	if len(f.Active) > 0 {
		log.Printf("%d agent(s) were running when the previous server stopped", len(f.Active))
	}
	return l
}

// settle writes the log read as this server has it, once: the previous
// server's active runs moved to interrupted, and its stop forgotten.
func (l *runLog) settle() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.unsettled {
		l.unsettled = false
		l.saveLocked()
	}
}

func dropTriagers(runs []agentRun) []agentRun {
	var out []agentRun
	for _, r := range runs {
		if !proto.IsTriager(proto.PaneInfo{Name: r.Name}) {
			out = append(out, r)
		}
	}
	return out
}

func (l *runLog) saveLocked() {
	f := runLogFile{Active: []agentRun{}, Interrupted: l.interrupted, Stopped: l.stopped, Ended: l.ended}
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
	// An agent's pane that has just ended is the exit's to account for
	// (forget), which keeps it as ended: dropped here, between the
	// program ending and the exit being seen, it would vanish.
	if info.State == proto.PaneExited {
		return
	}
	// A triage agent is the server's errand, not the person's work: its
	// triage ends with the server, so it is neither resumed nor offered.
	if info.Agent == nil || info.State != proto.PaneRunning || proto.IsTriager(info) {
		if had {
			delete(l.active, info.ID)
			l.saveLocked()
		}
		return
	}
	next := agentRun{Pane: info.ID, Agent: info.Agent.Name, Dir: dir, Session: info.Agent.SessionID, Account: info.Account, Name: info.Name, Started: created, Seen: time.Now()}
	if next.Session == "" && cur.Agent == next.Agent {
		next.Session = cur.Session
	}
	if had && cur.Agent == next.Agent && cur.Session == next.Session && cur.Dir == next.Dir && cur.Account == next.Account && cur.Name == next.Name && time.Since(cur.Seen) < time.Minute {
		return
	}
	l.active[info.ID] = next
	l.saveLocked()
}

// forget drops a pane that was closed or whose agent exited on its own,
// and hands back the run it was, if an agent ran there.
func (l *runLog) forget(pane string) (agentRun, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	r, ok := l.active[pane]
	if ok {
		delete(l.active, pane)
		l.saveLocked()
	}
	return r, ok
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

// markStopped records that this server was asked to stop, so the next one
// offers its agents for resuming rather than starting them again.
func (l *runLog) markStopped() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.stopped = true
	l.saveLocked()
}

// takeFresh hands over the runs to resume by themselves, once.
func (l *runLog) takeFresh() []agentRun {
	l.mu.Lock()
	defer l.mu.Unlock()
	fresh := l.fresh
	l.fresh = nil
	return fresh
}

func (l *runLog) interruptedRuns() []agentRun {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]agentRun(nil), l.interrupted...)
}

// ---- session.list / resume ----

// realDir resolves symlinks in a directory a client names (/tmp is
// /private/tmp on macOS): agents record the resolved path.
func realDir(dir string) string {
	if dir == "" {
		return ""
	}
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		return r
	}
	return dir
}

// sessionDirs resolves where to look for a project's or directory's
// sessions.
func (s *Server) sessionDirs(method, projectID, dir string) ([]string, *project, *proto.Error) {
	switch {
	case projectID != "":
		proj, perr := s.projects.get(projectID)
		if perr != nil {
			return nil, nil, perr
		}
		return proj.roots(), proj, nil
	case dir != "":
		return []string{dir}, s.projects.containing(dir), nil
	}
	return nil, nil, proto.Errorf(proto.ErrBadRequest, "%s needs a project or a directory", method)
}

func (s *Server) listSessions(p proto.SessionListParams) (proto.SessionList, *proto.Error) {
	dirs, proj, perr := s.sessionDirs("session.list", p.ProjectID, p.Dir)
	if perr != nil {
		return proto.SessionList{}, perr
	}
	limit := p.Limit
	if limit <= 0 {
		limit = 100
	}
	found, complete := sessions.ListStatus(s.sessionEnv(), dirs, limit)

	out := make([]proto.SessionInfo, 0, len(found))
	for _, f := range found {
		info := proto.SessionInfo{Agent: f.Agent, Account: f.Account, ID: f.ID, Dir: f.Dir, Branch: f.Branch, Title: f.Title,
			Started: f.Started, Updated: f.Updated, CostUSD: f.Cost, Output: f.Output}
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
			if claimed[i] || si.Agent != pi.Agent.Name || si.Account != pi.Account {
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
			// The pane reads the whole transcript as it grows, so its
			// usage is ahead of anything the store scan found.
			if t := pi.Agent.Tokens; t != nil {
				if t.CostUSD > 0 {
					out[best].CostUSD = t.CostUSD
				}
				if t.Output > 0 {
					out[best].Output = t.Output
				}
			}
		}
	}

	var runs []agentRun
	for _, r := range s.runs.interruptedRuns() {
		if withinAny(r.Dir, dirs) {
			runs = append(runs, r)
		}
	}
	out = dedupeSessions(markInterrupted(out, runs))

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
	s.stars.mark(out)
	sortSessions(out)
	return proto.SessionList{Sessions: out, Partial: !complete}, nil
}

// markInterrupted marks the sessions interrupted runs were in, or lists a
// run itself when the agent saved nothing findable. A run whose session is
// open in a pane again has been picked up, so it is neither marked nor
// listed a second time beside that session (#346).
func markInterrupted(out []proto.SessionInfo, runs []agentRun) []proto.SessionInfo {
	for _, r := range runs {
		best, open := -1, false
		for i, si := range out {
			if si.Agent != r.Agent {
				continue
			}
			if r.Session != "" {
				if si.ID == r.Session && (r.Account == "" || si.Account == r.Account) {
					if si.PaneID != "" {
						open = true
						break
					}
					best = i
					break
				}
				continue
			}
			if si.PaneID != "" {
				continue
			}
			if si.Dir == r.Dir && !si.Updated.Before(r.Started.Add(-time.Minute)) && !si.Updated.After(r.Seen.Add(10*time.Minute)) &&
				(best < 0 || si.Updated.After(out[best].Updated)) {
				best = i
			}
		}
		if open {
			continue
		}
		if best >= 0 {
			out[best].Interrupted = true
			continue
		}
		out = append(out, proto.SessionInfo{Agent: r.Agent, Account: r.Account, ID: r.Session, Dir: r.Dir, Title: "interrupted run · resumes the latest session here",
			Started: r.Started, Updated: r.Seen, Interrupted: true})
	}
	return out
}

// dedupeSessions keeps one entry per agent, account and session id: a
// client keys its rows on those, and two entries with one key drew as a
// blank gap in Conch.app's list. The one open in a pane is kept, and an
// interrupted mark carries over only when neither is open.
func dedupeSessions(out []proto.SessionInfo) []proto.SessionInfo {
	type key struct{ agent, account, id string }
	at := map[key]int{}
	kept := out[:0]
	for _, si := range out {
		if si.ID == "" {
			kept = append(kept, si)
			continue
		}
		k := key{si.Agent, si.Account, si.ID}
		i, seen := at[k]
		if !seen {
			at[k] = len(kept)
			kept = append(kept, si)
			continue
		}
		if kept[i].PaneID == "" && si.PaneID != "" {
			kept[i] = si
		} else if kept[i].PaneID == "" && si.Interrupted {
			kept[i].Interrupted = true
		}
		if kept[i].PaneID != "" {
			kept[i].Interrupted = false
		}
	}
	return kept
}

// sortSessions puts interrupted sessions first, then starred, then
// newest first.
func sortSessions(out []proto.SessionInfo) {
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && lessSession(out[j], out[j-1]); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
}

func lessSession(a, b proto.SessionInfo) bool {
	// Interrupted first: that is work waiting to be picked up, where a
	// star is a bookmark somebody left. Then starred, which is what
	// starring is for — finding it among a project's hundreds without
	// remembering its first line. Recency decides within each.
	if a.Interrupted != b.Interrupted {
		return a.Interrupted
	}
	if a.Starred != b.Starred {
		return a.Starred
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

// sessionAccount is the account whose folder holds agent's session id.
func (s *Server) sessionAccount(agent, id, dir string) string {
	for _, f := range sessions.List(s.sessionEnv(), []string{dir}, 0) {
		if f.Agent == agent && f.ID == id {
			return f.Account
		}
	}
	return ""
}

// deleteSession removes a saved session the user chose, unless it is open
// in a pane.
func (s *Server) deleteSession(r proto.SessionRef) *proto.Error {
	if r.ID == "" {
		return proto.Errorf(proto.ErrBadRequest, "this run has no saved conversation to delete; dismiss it instead")
	}
	list, perr := s.listSessions(proto.SessionListParams{Dir: r.Dir, Limit: 1000})
	if perr != nil {
		return perr
	}
	for _, si := range list.Sessions {
		if si.Agent == r.Agent && si.ID == r.ID && si.PaneID != "" {
			return proto.Errorf(proto.ErrBadRequest, "the session is open in pane %s; close it first", si.PaneID)
		}
	}
	for _, found := range sessions.List(s.sessionEnv(), []string{r.Dir}, 0) {
		if found.Agent != r.Agent || found.ID != r.ID {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := sessions.Delete(ctx, s.sessionEnv(), found, trashDir(s.configDir)); err != nil {
			return proto.Errorf(proto.ErrBadRequest, "%v", err)
		}
		s.runs.resolve(r.Agent, r.ID, r.Dir)
		log.Printf("deleted %s session %s in %s", r.Agent, r.ID, r.Dir)
		return nil
	}
	return proto.Errorf(proto.ErrNotFound, "no %s session %s in %s", r.Agent, r.ID, r.Dir)
}

// trashDir is where deleted session files go: the user's Trash on macOS,
// else a folder in conch's config directory.
func trashDir(configDir string) string {
	if home, err := os.UserHomeDir(); err == nil && runtime.GOOS == "darwin" {
		if st, err := os.Stat(filepath.Join(home, ".Trash")); err == nil && st.IsDir() {
			return filepath.Join(home, ".Trash")
		}
	}
	return filepath.Join(configDir, "trash")
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
	if !adapter.SessionIDOK(r.ID) {
		return proto.PaneInfo{}, proto.Errorf(proto.ErrBadRequest, "%q is not a session id", r.ID)
	}
	// A conversation lives in its account's folder and resumes nowhere
	// else, whatever the folder is pinned to now or how full that account
	// is: found here when the client did not say, as an older one won't.
	// With no id, the latest conversation here is the pinned account's.
	account := r.Account
	if account == "" && r.ID != "" {
		account = accounts.Label(s.sessionAccount(r.Agent, r.ID, r.Dir))
	}
	info, perr := s.create(proto.PaneCreateParams{Agent: r.Agent, Account: account, AgentArgs: ad.ResumeArgs(r.ID), Cwd: r.Dir, Cols: r.Cols, Rows: r.Rows})
	if perr != nil {
		return info, perr
	}
	s.runs.resolve(r.Agent, r.ID, r.Dir)
	s.endedResumed(r.Agent, r.ID, r.Dir, info.ID)
	log.Printf("pane %s resumed %s session %q in %s", info.ID, r.Agent, r.ID, r.Dir)
	return info, nil
}

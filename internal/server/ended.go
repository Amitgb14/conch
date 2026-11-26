package server

import (
	"log"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// An agent whose pane ends without anybody closing it — it crashed, it was
// killed, the server went — used to vanish from every client: its pane
// closed, its row left the tree, and the way back was finding it in
// Sessions. Now it is kept as an ended record (pane.ended.v1) beside the
// run log, in the same file, so a restart keeps it too: where it was,
// why it ended and what it needs to come back. A pane the person closed
// is never kept; closing is the asking.

// keepEnded is how long an ended agent is kept for resuming, as long as
// an interrupted run is offered in Sessions.
const keepEnded = keepInterrupted

// maxEnded caps the records, oldest dropped: a loop that keeps crashing
// an agent must not grow the file and every client's tree without end.
const maxEnded = 50

// endedScreenLines is how much of the last screen a record keeps.
const endedScreenLines = 200

// endedID names a record: a pane id is the next server's again after a
// restart, so the pane and the moment it ended together.
func endedID(pane string, at time.Time) string {
	return pane + "." + strconv.FormatInt(at.UnixMilli(), 36)
}

// lastScreen is a pane's screen without the blank rows below what it said.
func lastScreen(lines []string) []string {
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	start := max(0, end-endedScreenLines)
	out := make([]string, 0, end-start)
	for _, l := range lines[start:end] {
		out = append(out, strings.TrimRight(l, " "))
	}
	return out
}

// endedFromRun is the record of a run whose server went before it ended.
func endedFromRun(r agentRun, why string) proto.EndedAgent {
	return proto.EndedAgent{ID: endedID(r.Pane, r.Seen), Pane: r.Pane, Agent: r.Agent, Session: r.Session, Dir: r.Dir,
		Account: r.Account, Name: r.Name, Why: why, Started: r.Started, Ended: r.Seen}
}

// addEnded keeps a record, and drops those past keepEnded or maxEnded.
// The caller holds l.mu.
func (l *runLog) addEndedLocked(rec proto.EndedAgent) {
	l.ended = slices.DeleteFunc(l.ended, func(e proto.EndedAgent) bool { return e.ID == rec.ID })
	l.ended = append(l.ended, rec)
	l.pruneEndedLocked(time.Now())
}

func (l *runLog) pruneEndedLocked(now time.Time) {
	l.ended = slices.DeleteFunc(l.ended, func(e proto.EndedAgent) bool { return now.Sub(e.Ended) >= keepEnded })
	slices.SortStableFunc(l.ended, func(a, b proto.EndedAgent) int { return a.Ended.Compare(b.Ended) })
	if over := len(l.ended) - maxEnded; over > 0 {
		l.ended = slices.Delete(l.ended, 0, over)
	}
}

// addEnded keeps rec and writes the log.
func (l *runLog) addEnded(rec proto.EndedAgent) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.addEndedLocked(rec)
	l.saveLocked()
}

// endedList is the records still kept, newest first.
func (l *runLog) endedList() []proto.EndedAgent {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneEndedLocked(time.Now())
	out := slices.Clone(l.ended)
	slices.Reverse(out)
	return out
}

func (l *runLog) endedByID(id string) (proto.EndedAgent, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, e := range l.ended {
		if e.ID == id {
			return e, true
		}
	}
	return proto.EndedAgent{}, false
}

// dropEnded forgets a record, saying whether there was one.
func (l *runLog) dropEnded(id string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := len(l.ended)
	l.ended = slices.DeleteFunc(l.ended, func(e proto.EndedAgent) bool { return e.ID == id })
	if len(l.ended) == n {
		return false
	}
	l.saveLocked()
	return true
}

// agentEnded keeps the agent of a pane that ended by itself, when one ran
// there: run is what the run log last knew of it.
func (s *Server) agentEnded(e *entry, info proto.PaneInfo, run agentRun) {
	rec := proto.EndedAgent{Pane: info.ID, Agent: run.Agent, Session: run.Session, Dir: run.Dir, Account: run.Account,
		Name: info.Name, ProjectID: info.ProjectID, Branch: info.Branch, Why: proto.EndedExited, ExitCode: info.ExitCode,
		Screen: lastScreen(e.p.PlainLines()), Started: run.Started, Ended: time.Now()}
	if sig := e.p.ExitSignal(); sig != "" {
		rec.Why, rec.Signal, rec.ExitCode = proto.EndedKilled, sig, 0
	}
	rec.ID = endedID(rec.Pane, rec.Ended)
	s.runs.addEnded(rec)
	log.Printf("pane %s: %s agent %s, kept to resume", info.ID, run.Agent, rec.Reason())
	s.broadcast(proto.EventPaneEnded, s.placeEnded(rec))
}

// serverEnded keeps the runs a previous server took with it and nobody is
// bringing back: they were offered in Sessions, and are now where they
// were too.
func (s *Server) serverEnded(runs []agentRun) {
	if len(runs) == 0 {
		return
	}
	for _, r := range runs {
		if proto.IsTriager(proto.PaneInfo{Name: r.Name}) {
			continue
		}
		rec := s.placeEnded(endedFromRun(r, proto.EndedServer))
		s.runs.addEnded(rec)
		s.broadcast(proto.EventPaneEnded, rec)
	}
}

// placeEnded puts a record under the project and branch its folder is in
// now: a record from before a restart was written before the catalog was
// read.
func (s *Server) placeEnded(rec proto.EndedAgent) proto.EndedAgent {
	if rec.ProjectID != "" {
		return rec
	}
	if proj := s.projects.containing(rec.Dir); proj != nil {
		rec.ProjectID = proj.id
		if wt, ok := proj.worktreeFor(rec.Dir); ok {
			rec.Branch = wt.Branch
		}
	}
	return rec
}

// markEnded says, on each session whose agent ended without being closed,
// why — by its session id, or, for an agent that never reported one, the
// newest session of that agent in its folder from while it ran. A session
// open in a pane again is not marked: it is back.
func markEnded(out []proto.SessionInfo, ended []proto.EndedAgent) {
	for _, r := range ended {
		best := -1
		for i, si := range out {
			if si.Agent != r.Agent || si.PaneID != "" || si.EndedID != "" {
				continue
			}
			if r.Session != "" {
				if si.ID == r.Session {
					best = i
					break
				}
				continue
			}
			if si.Dir == r.Dir && !si.Updated.Before(r.Started.Add(-time.Minute)) && !si.Updated.After(r.Ended.Add(10*time.Minute)) &&
				(best < 0 || si.Updated.After(out[best].Updated)) {
				best = i
			}
		}
		if best >= 0 {
			out[best].Ended, out[best].EndedID, out[best].EndedFailed = r.Reason(), r.ID, r.Failed()
		}
	}
}

func (s *Server) listEnded() proto.EndedList {
	list := s.runs.endedList()
	for i := range list {
		list[i] = s.placeEnded(list[i])
	}
	return proto.EndedList{Ended: list}
}

// dismissEnded forgets an ended agent; its conversation stays in Sessions.
func (s *Server) dismissEnded(ref proto.EndedRef) *proto.Error {
	rec, ok := s.runs.endedByID(ref.ID)
	if !ok || !s.runs.dropEnded(ref.ID) {
		return proto.Errorf(proto.ErrNotFound, "no ended agent %q", ref.ID)
	}
	s.broadcast(proto.EventPaneEndedGone, proto.EndedGone{ID: rec.ID, Pane: rec.Pane})
	s.closeEndedPane(rec)
	return nil
}

// resumeEnded starts an ended agent's conversation again, on its account
// and under its name. The gone event names the new pane before the old
// one is closed, so a client puts it where the old one was.
func (s *Server) resumeEnded(c *client, p proto.EndedResumeParams) (proto.PaneInfo, *proto.Error) {
	rec, ok := s.runs.endedByID(p.ID)
	if !ok {
		return proto.PaneInfo{}, proto.Errorf(proto.ErrNotFound, "no ended agent %q", p.ID)
	}
	info, perr := s.made(c)(s.resumeSession(proto.SessionRef{Agent: rec.Agent, ID: rec.Session, Dir: rec.Dir,
		Account: rec.Account, Cols: p.Cols, Rows: p.Rows}))
	if perr != nil {
		return info, perr
	}
	if rec.Name != "" && rec.Name != info.Name && !proto.IsPaneID(rec.Name) {
		if e, gerr := s.get(info.ID); gerr == nil {
			s.rename(e, rec.Name)
			info = s.infoOf(e.info())
		}
	}
	// resumeSession has said it is gone, by its session; one it could not
	// match by session is said here.
	if s.runs.dropEnded(rec.ID) {
		s.broadcast(proto.EventPaneEndedGone, proto.EndedGone{ID: rec.ID, Pane: rec.Pane, Resumed: info.ID})
		s.closeEndedPane(rec)
	}
	return info, nil
}

// endedResumed forgets the ended agents a resumed session brings back —
// from Sessions as much as from their own row — naming the pane each is
// back in, for a client to put it where the ended one was.
func (s *Server) endedResumed(agent, session, dir, pane string) {
	for _, rec := range s.runs.endedList() {
		if rec.Agent != agent || rec.Dir != dir || rec.Session != session {
			continue
		}
		if s.runs.dropEnded(rec.ID) {
			s.broadcast(proto.EventPaneEndedGone, proto.EndedGone{ID: rec.ID, Pane: rec.Pane, Resumed: pane})
			s.closeEndedPane(rec)
		}
	}
}

// closeEndedPane closes the pane a record was kept from, when the server
// still holds it ended — and not a live pane that took its id after a
// restart.
func (s *Server) closeEndedPane(rec proto.EndedAgent) {
	e, perr := s.get(rec.Pane)
	if perr != nil {
		return
	}
	if info := e.info(); info.State == proto.PaneExited && !info.Created.After(rec.Ended) {
		_ = s.close(rec.Pane)
	}
}

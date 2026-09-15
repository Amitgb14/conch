package server

import (
	"log"
	"sync"
	"time"

	"github.com/Amitgb14/conch/internal/adapter"
	"github.com/Amitgb14/conch/internal/detect"
	"github.com/Amitgb14/conch/internal/pane"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/usage"
)

// detectInterval is how often each pane's agent status, title and branch
// are re-evaluated. Hook reports and input trigger an evaluation immediately.
const detectInterval = 500 * time.Millisecond

// entry is a pane plus the server's view of the agent inside it.
type entry struct {
	p   *pane.Pane
	dir string // working directory, symlinks resolved

	// evalMu serialises evaluate+broadcast so update events go out in order.
	evalMu sync.Mutex

	mu       sync.Mutex
	tracker  *detect.Tracker
	watchers int      // subscribed clients
	project  *project // nil when the pane is outside any project

	screen        []string // cached plain screen
	screenVersion uint64

	// sent is what the last pane.updated told clients, so a change made
	// outside this pane — a checkout moving its branch, say — is noticed
	// too. Comparing only within a call would miss it: both samples
	// already have the new value.
	sent     shown
	sentOnce bool

	transcript *usage.Transcript // the agent session's, once a hook names it
	usage      *usageSource      // for agents without such hooks; watch goroutine only
	tokens     *proto.Tokens
}

func newEntry(p *pane.Pane, dir string, t *detect.Tracker, proj *project) *entry {
	return &entry{p: p, dir: dir, tracker: t, project: proj, screenVersion: ^uint64(0)}
}

func (e *entry) info() proto.PaneInfo {
	info := e.p.Info()
	info.Title = adapter.CleanTitle(e.p.Title())
	e.mu.Lock()
	proj := e.project
	st := e.tracker.Status()
	e.mu.Unlock()
	if proj != nil {
		info.ProjectID = proj.id
		if wt, ok := proj.worktreeFor(e.dir); ok {
			info.Branch = wt.Branch
		}
	}
	if info.State == proto.PaneRunning && st.Agent != "" {
		e.mu.Lock()
		tokens := e.tokens
		e.mu.Unlock()
		info.Agent = &proto.AgentStatus{
			Name: st.Agent, State: st.State, Source: st.Source, Reason: st.Reason,
			Message: st.Message, SessionID: st.SessionID, Since: st.Since, Tokens: tokens, Failed: st.Failed,
		}
	}
	return info
}

func (e *entry) explain() detect.Explanation {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.tracker.Explain()
}

// watch evaluates the pane periodically until it exits.
func (s *Server) watch(e *entry) {
	t := time.NewTicker(detectInterval)
	defer t.Stop()
	s.observe(e)
	lastUsage := time.Now()
	for {
		select {
		case <-t.C:
			if !s.alive(e) {
				return
			}
			s.observe(e)
			if time.Since(lastUsage) >= usagePollEvery {
				lastUsage = time.Now()
				s.pollUsage(e)
			}
		case <-e.p.Done():
			if s.alive(e) {
				info := e.info()
				select {
				case <-s.quit: // stopping the server: keep the run to offer resuming
				default:
					s.runs.forget(info.ID)
				}
				log.Printf("pane %s exited with code %d", info.ID, info.ExitCode)
				s.broadcast(proto.EventPaneExited, info)
			}
			return
		case <-s.quit:
			return
		}
	}
}

// observe samples the pane and broadcasts pane.updated when what clients
// show for it changed.
func (s *Server) observe(e *entry) {
	e.evalMu.Lock()
	defer e.evalMu.Unlock()
	s.evaluate(e, func(t *detect.Tracker) {})
}

// shown is the part of PaneInfo that pane.updated exists to report.
type shown struct {
	agent, state, message, session string
	title, name, branch            string
	tokens                         proto.Tokens
	failed                         bool
}

func shownOf(info proto.PaneInfo) shown {
	sh := shown{title: info.Title, name: info.Name, branch: info.Branch}
	if a := info.Agent; a != nil {
		sh.agent, sh.state, sh.message, sh.session, sh.failed = a.Name, a.State, a.Message, a.SessionID, a.Failed
		if a.Tokens != nil {
			sh.tokens = *a.Tokens
		}
	}
	return sh
}

// evaluate applies fn to the tracker, re-observes the pane and broadcasts
// pane.updated if anything shown changed. The caller holds e.evalMu.
func (s *Server) evaluate(e *entry, fn func(*detect.Tracker)) {
	e.mu.Lock()
	before, had := e.sent, e.sentOnce
	e.mu.Unlock()
	if !had {
		before = shownOf(e.info())
	}
	proc, perr := e.p.Foreground()
	if v := e.p.Version(); v != e.screenVersion {
		e.screen, e.screenVersion = e.p.PlainLines(), v
	}
	e.mu.Lock()
	prevState := e.tracker.Status().State
	fn(e.tracker)
	e.tracker.Observe(detect.Observation{
		Now:        time.Now(),
		Process:    proc,
		ProcessErr: perr,
		Screen:     e.screen,
		Title:      e.p.Title(),
		Watched:    e.watchers > 0,
	})
	e.mu.Unlock()

	info := e.info()
	if s.alive(e) {
		s.runs.note(info, e.dir, info.Created)
	}
	now := shownOf(info)
	e.mu.Lock()
	e.sent, e.sentOnce = now, true
	e.mu.Unlock()
	if now == before || !s.alive(e) {
		return
	}
	if a := info.Agent; a != nil && a.State != prevState {
		log.Printf("pane %s agent %s: %s -> %s (%s)", info.ID, a.Name, prevState, a.State, a.Reason)
	}
	s.broadcast(proto.EventPaneUpdated, info)
}

// refreshEvents are agent events after which the project's git state has
// likely changed.
var refreshEvents = map[string]bool{"PostToolUse": true, "PostToolUseFailure": true, "Stop": true}

func (s *Server) report(e *entry, rp proto.AgentReportParams) {
	if rp.Event == "StatusLine" {
		s.statusLine(e, rp)
		return
	}
	e.evalMu.Lock()
	defer e.evalMu.Unlock()
	s.updateUsage(e, rp.TranscriptPath)
	s.evaluate(e, func(t *detect.Tracker) {
		t.Hook(detect.HookEvent{
			Agent: rp.Agent, Event: rp.Event, NotificationType: rp.NotificationType,
			Message: rp.Message, SessionID: rp.SessionID,
		}, time.Now())
	})
	if refreshEvents[rp.Event] && e.project != nil {
		s.projects.request(e.project)
	}
}

// updateUsage re-reads the session transcript's new lines. The caller holds
// e.evalMu.
func (s *Server) updateUsage(e *entry, path string) {
	if path == "" {
		return
	}
	if e.transcript == nil || e.transcript.Path() != path {
		e.transcript = usage.NewTranscript(path) // a new session (e.g. /clear)
	}
	tok, err := e.transcript.Update()
	if err != nil && tok.Output == 0 {
		return
	}
	pt := proto.Tokens(tok)
	e.mu.Lock()
	if e.tokens != nil && e.tokens.ContextSize > 0 {
		pt.ContextSize = e.tokens.ContextSize // from the status line
	}
	e.tokens = &pt
	e.mu.Unlock()
}

func (s *Server) userInput(e *entry) {
	e.evalMu.Lock()
	defer e.evalMu.Unlock()
	s.evaluate(e, func(t *detect.Tracker) { t.UserInput() })
}

func (s *Server) markSeen(e *entry) {
	e.evalMu.Lock()
	defer e.evalMu.Unlock()
	s.evaluate(e, func(t *detect.Tracker) { t.MarkSeen(time.Now()) })
}

func (s *Server) rename(e *entry, name string) {
	e.evalMu.Lock()
	defer e.evalMu.Unlock()
	s.evaluate(e, func(*detect.Tracker) { e.p.Rename(name) })
}

func (s *Server) setWatchers(e *entry, delta int) {
	e.evalMu.Lock()
	defer e.evalMu.Unlock()
	e.mu.Lock()
	e.watchers += delta
	watched := e.watchers > 0
	e.mu.Unlock()
	if watched {
		s.evaluate(e, func(t *detect.Tracker) { t.MarkSeen(time.Now()) })
	}
}

// projectHasPanes reports whether running panes work in the project.
func (s *Server) projectHasPanes(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.panes {
		if e.project != nil && e.project.id == id {
			return true
		}
	}
	return false
}

// panesIn reports whether any pane works inside dir.
func (s *Server) panesIn(dir string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.panes {
		if within(e.dir, dir) {
			return true
		}
	}
	return false
}

func (s *Server) projectRegistered(p *project) bool {
	_, perr := s.projects.get(p.id)
	return perr == nil
}

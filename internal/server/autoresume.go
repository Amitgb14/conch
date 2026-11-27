package server

import (
	"log"
	"time"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

// A server that went without being asked to — the machine rebooted, the
// process crashed or was killed — left its agents' conversations saved and
// their panes gone. The next server starts each of them again on its own
// conversation, as I in Sessions would, so coming back after a reboot is
// the work as it was rather than a list to pick up. A server stopped on
// purpose (conch server stop) is not undone: its agents wait for I, and
// [agents] no_auto_resume leaves every restart to the person.
//
// Only what the previous server was running comes back — runs interrupted
// earlier and left alone stay offered, not restarted — and a run that
// cannot be resumed (its folder removed, its agent uninstalled) stays in
// Sessions, said in the log.

// loadAutoResumeConfig reads [agents]; a variable so tests can give one.
var loadAutoResumeConfig = config.Load

// autoResumeGap is the pause between one agent and the next: each starts a
// login shell, and a dozen at once is the slowest way to get them all.
var autoResumeGap = 300 * time.Millisecond

// Resumed panes start at this size; the first client to show one sizes it.
const autoResumeCols, autoResumeRows = 120, 40

func (s *Server) autoResume() {
	runs := s.runs.takeFresh()
	if len(runs) == 0 {
		return
	}
	if cfg, err := loadAutoResumeConfig(); err == nil && cfg.Agents.NoAutoResume {
		log.Printf("%d interrupted agent(s) left for Sessions ([agents] no_auto_resume)", len(runs))
		s.projects.waitLoaded()
		s.serverEnded(runs)
		return
	}
	// A pane joins the project its folder is in, which needs the catalog.
	s.projects.waitLoaded()
	seen := map[string]bool{}
	resumed := 0
	for i, r := range runs {
		// A triage agent belongs to its triage, which a restart ends
		// (triageFinish finds it gone): resumed, it would read on with
		// nobody to take its verdict, spending usage, in the checkout the
		// next triage reads in. The run log leaves them out; this is the
		// last word.
		if proto.IsTriager(proto.PaneInfo{Name: r.Name}) {
			continue
		}
		// Two runs of one agent in one folder with no session id would
		// both resume "the latest here": the same conversation twice.
		key := r.Agent + "\x00" + r.Dir + "\x00" + r.Session
		if seen[key] {
			continue
		}
		seen[key] = true
		if i > 0 {
			select {
			case <-time.After(autoResumeGap):
			case <-s.quit:
				return
			}
		}
		select {
		case <-s.quit:
			return
		default:
		}
		info, perr := s.resumeSession(proto.SessionRef{Agent: r.Agent, ID: r.Session, Dir: r.Dir, Account: r.Account,
			Cols: autoResumeCols, Rows: autoResumeRows})
		if perr != nil {
			log.Printf("resuming %s in %s after the restart: %s (left in Sessions)", r.Agent, r.Dir, perr.Message)
			s.serverEnded([]agentRun{r})
			continue
		}
		// Back under its name: another agent addresses it by that
		// (conch agent prompt reviewer), and a new name breaks the hand-off.
		if r.Name != "" && r.Name != info.Name {
			if e, perr := s.get(info.ID); perr == nil {
				s.rename(e, r.Name)
			}
		}
		// And back in the team it was put in by hand.
		if r.Team != "" {
			if e, perr := s.get(info.ID); perr == nil {
				s.setTeam(e, r.Team)
			}
		}
		resumed++
	}
	if resumed > 0 {
		log.Printf("resumed %d agent(s) the previous server was running", resumed)
	}
}

// setTeam puts pane e in team (by hand), tells clients, and writes it in
// the run log at once: a crash a moment later must not take it away.
func (s *Server) setTeam(e *entry, team string) {
	e.mu.Lock()
	e.team = team
	e.mu.Unlock()
	s.reshown(e)
	info := e.info()
	s.runs.note(info, e.dir, info.Created)
}

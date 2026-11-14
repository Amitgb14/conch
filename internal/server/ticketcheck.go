package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/ghx"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/tracker"
)

// A key that does not work used to be found out in the middle of New Task,
// as a list that would not load. ticket.check asks each tracker once, with
// what this server has — the same config.toml, environment and Keychain a
// real list reads — so Settings can say Connected, or what is wrong, beside
// the key itself. The trackers are asked side by side under one deadline,
// so one that does not answer costs the others nothing.

// checkTimeout bounds a whole check: Settings waits on it.
var checkTimeout = 20 * time.Second

func (s *Server) ticketCheck(cp proto.TicketCheckParams) (proto.TicketCheckResult, *proto.Error) {
	cfg, err := config.Load()
	if err != nil {
		return proto.TicketCheckResult{}, proto.Errorf(proto.ErrBadRequest, "config.toml: %v", err)
	}
	var p *project
	if cp.ProjectID != "" {
		var perr *proto.Error
		if p, perr = s.projects.get(cp.ProjectID); perr != nil {
			return proto.TicketCheckResult{}, perr
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
	defer cancel()

	out := proto.TicketCheckResult{Trackers: make([]proto.TrackerState, 3)}
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); out.Trackers[0] = s.checkGitHub(ctx, p) }()
	go func() { defer wg.Done(); out.Trackers[1] = s.checkTracker(ctx, cfg.Trackers, tracker.Linear, p) }()
	go func() { defer wg.Done(); out.Trackers[2] = s.checkTracker(ctx, cfg.Trackers, tracker.Jira, p) }()
	out.Projects = s.checkProjects(ctx, cfg.Trackers)
	wg.Wait()
	return out, nil
}

// checkGitHub is gh's login, and with a project its open issues.
func (s *Server) checkGitHub(ctx context.Context, p *project) proto.TrackerState {
	st := proto.TrackerState{Tracker: "github", KeyFrom: "gh"}
	dir := os.TempDir()
	if p != nil {
		dir = p.root
	}
	who, err := ghx.Whoami(ctx, dir)
	switch {
	case errors.Is(err, ghx.ErrNoGH):
		st.State, st.Error = proto.TrackerOff, "gh is not installed: GitHub issues are read through it (cli.github.com)"
		return st
	case err != nil:
		st.State, st.Error = proto.TrackerError, ghSays(err)
		return st
	}
	st.State, st.Who = proto.TrackerConnected, who
	if p != nil {
		st.Area = p.snapshot().Name
		if n, err := ghx.IssueCount(ctx, p.root, false, proto.TicketCountMax); err == nil {
			st.Open, st.More = n, n >= proto.TicketCountMax
			st.Mine, _ = ghx.IssueCount(ctx, p.root, true, proto.TicketCountMax)
		} else {
			// Signed in, but this checkout has no GitHub repository to ask.
			st.Error = ghSays(err)
		}
	}
	return st
}

// ghSays is gh's error, with the fix for the usual one.
func ghSays(err error) string {
	msg := err.Error()
	if strings.Contains(msg, "auth login") || strings.Contains(msg, "not logged") {
		return "gh is not signed in: run gh auth login"
	}
	return msg
}

// checkTracker is Linear or Jira: off when nothing is set up, else whose the
// key is, what it sees and how many are open where tickets are listed.
func (s *Server) checkTracker(ctx context.Context, cfg config.TrackersCfg, name string, p *project) proto.TrackerState {
	st := proto.TrackerState{Tracker: name}
	set := false
	for _, n := range tracker.Configured(cfg, s.trackerSecret) {
		set = set || n == name
	}
	if !set {
		st.State = proto.TrackerOff
		return st
	}
	st.KeyFrom = tracker.KeyFrom(name, cfg, func(v string) bool { return os.Getenv(v) != "" })
	tr, err := tracker.Open(cfg, name, s.trackerSecret)
	if err != nil {
		// Half set up: Jira with a site and no token, say.
		st.State, st.Error = proto.TrackerError, err.Error()
		return st
	}
	if ck, ok := tr.(tracker.Checker); ok {
		a, err := ck.Check(ctx)
		if err != nil {
			st.State, st.Error = proto.TrackerError, err.Error()
			return st
		}
		st.Who, st.Email, st.Workspace = a.Who, a.Email, a.Workspace
		for _, ar := range a.Scopes {
			st.Areas = append(st.Areas, proto.TrackerArea{Key: ar.Key, Name: ar.Name})
		}
	}
	st.State = proto.TrackerConnected
	pname, root := "", ""
	if p != nil {
		pname, root = p.snapshot().Name, p.root
	}
	st.Area = tracker.Scope(cfg, pname, root, name)
	if list, err := tr.List(ctx, tracker.ListOptions{Scope: st.Area, Limit: proto.TicketCountMax}); err == nil {
		st.Open, st.More = len(list), len(list) >= proto.TicketCountMax
		if mine, err := tr.List(ctx, tracker.ListOptions{Scope: st.Area, Assigned: true, Limit: proto.TicketCountMax}); err == nil {
			st.Mine = len(mine)
		}
	} else {
		// The key is good and the team or project is not: say which.
		st.Error = areaError(name, st.Area, err)
	}
	return st
}

// areaError is a list's failure in a check's words: a team or project the
// tracker has not got is named as such, not as a missing ticket.
func areaError(name, area string, err error) string {
	if errors.Is(err, tracker.ErrNotFound) && area != "" {
		kind := "team"
		if name == tracker.Jira {
			kind = "project"
		}
		return fmt.Sprintf("no %s %s in %s, or the key may not see it", kind, area, tracker.Title(name))
	}
	return err.Error()
}

// checkProjects counts the open tickets of each [[trackers.projects]]
// entry, side by side.
func (s *Server) checkProjects(ctx context.Context, cfg config.TrackersCfg) []proto.TrackerProjectState {
	out := make([]proto.TrackerProjectState, len(cfg.Projects))
	var wg sync.WaitGroup
	for i, e := range cfg.Projects {
		out[i] = proto.TrackerProjectState{Project: e.Project, Tracker: e.Tracker, Area: e.Key}
		wg.Add(1)
		go func(st *proto.TrackerProjectState) {
			defer wg.Done()
			name := strings.ToLower(strings.TrimSpace(st.Tracker))
			if st.Area == "" {
				st.Area = tracker.Scope(cfg, st.Project, st.Project, name)
			}
			tr, err := tracker.Open(cfg, name, s.trackerSecret)
			if err != nil {
				st.Error = err.Error()
				return
			}
			list, err := tr.List(ctx, tracker.ListOptions{Scope: st.Area, Limit: proto.TicketCountMax})
			if err != nil {
				st.Error = areaError(name, st.Area, err)
				return
			}
			st.Open, st.More = len(list), len(list) >= proto.TicketCountMax
		}(&out[i])
	}
	wg.Wait()
	return out
}

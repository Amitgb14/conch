package server

import (
	"path/filepath"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/sessions"
	"github.com/Amitgb14/conch/internal/usage"
)

// Agents without hooks that name their session (Codex, OpenCode) have their
// session found by directory and time, then their usage read from it.
const (
	usagePollEvery = 10 * time.Second
	usageRefind    = time.Minute // a new session (/new) may have started
)

type usageSource struct {
	agent   string
	id      string
	path    string
	found   time.Time
	rollout *usage.CodexRollout
}

// pollUsage refreshes the token usage of a Codex or OpenCode pane.
func (s *Server) pollUsage(e *entry) {
	info := e.info()
	if info.Agent == nil || (info.Agent.Name != "codex" && info.Agent.Name != "opencode") {
		return
	}
	agent := info.Agent.Name
	src := e.usage
	if src == nil || src.agent != agent || time.Since(src.found) > usageRefind {
		if found := findSession(agent, e.dir, info.Created); found != nil {
			if src == nil || src.id != found.ID || src.path != found.Path {
				src = &usageSource{agent: agent, id: found.ID, path: found.Path}
				if agent == "codex" {
					src.rollout = usage.NewCodexRollout(found.Path)
				}
			}
			src.found = time.Now()
			e.usage = src
		}
	}
	if src == nil {
		return
	}
	var tok usage.Tokens
	var err error
	switch agent {
	case "codex":
		tok, err = src.rollout.Update()
		if l := src.rollout.Limits(); l != nil {
			s.setLimits(*l)
		}
	case "opencode":
		tok, err = usage.OpenCodeSession(src.path, src.id)
	}
	if err != nil || tok.Input+tok.Output+tok.CacheRead == 0 {
		return
	}
	pt := proto.Tokens(tok)
	e.mu.Lock()
	e.tokens = &pt
	e.mu.Unlock()
	s.observe(e) // broadcasts the change
}

// findSession picks the newest session of agent in dir since the pane
// started.
func findSession(agent, dir string, since time.Time) *sessions.Session {
	var best *sessions.Session
	for _, ss := range sessions.List(sessions.CurrentEnv(), []string{dir}, 50) {
		if ss.Agent != agent || filepath.Clean(ss.Dir) != filepath.Clean(dir) || ss.Updated.Before(since.Add(-time.Minute)) {
			continue
		}
		if best == nil || ss.Updated.After(best.Updated) {
			c := ss
			best = &c
		}
	}
	return best
}

// statusLine takes what Claude's status line reports: the context window
// for the pane and the account's plan limits.
func (s *Server) statusLine(e *entry, rp proto.AgentReportParams) {
	if rp.ContextSize > 0 {
		e.mu.Lock()
		t := proto.Tokens{}
		if e.tokens != nil {
			t = *e.tokens
		}
		t.Context, t.ContextSize = rp.ContextUsed, rp.ContextSize
		e.tokens = &t
		e.mu.Unlock()
	}
	if rp.Limits != nil {
		rp.Limits.Agent = rp.Agent
		s.setLimits(*rp.Limits)
	}
	s.observe(e)
}

// setLimits records an agent's plan limits and tells clients when what
// they show changed.
func (s *Server) setLimits(l proto.PlanLimits) {
	if l.FiveHour == nil && l.Week == nil && l.Spend == nil {
		return
	}
	if l.At.IsZero() {
		l.At = time.Now()
	}
	s.limitsMu.Lock()
	if s.limits == nil {
		s.limits = map[string]proto.PlanLimits{}
	}
	old, had := s.limits[l.Agent]
	s.limits[l.Agent] = l
	s.limitsMu.Unlock()
	if had && sameWindow(old.FiveHour, l.FiveHour) && sameWindow(old.Week, l.Week) && sameWindow(old.Spend, l.Spend) {
		return
	}
	s.broadcast(proto.EventAgentLimits, l)
}

func sameWindow(a, b *proto.LimitWindow) bool {
	if a == nil || b == nil {
		return a == b
	}
	return int(a.UsedPct) == int(b.UsedPct) && a.ResetsAt.Equal(b.ResetsAt)
}

func (s *Server) allLimits() proto.AgentLimitsResult {
	s.limitsMu.Lock()
	defer s.limitsMu.Unlock()
	out := proto.AgentLimitsResult{Limits: []proto.PlanLimits{}}
	for _, l := range s.limits {
		out.Limits = append(out.Limits, l)
	}
	return out
}

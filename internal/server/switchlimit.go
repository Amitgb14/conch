package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/accounts"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/sessions"
	"github.com/BurntSushi/toml"
)

// Another agent at a plan limit (CapAgentSwitchLimit, #352 phase 3). An
// agent its plan stopped is first moved to another account of its own
// (accountmove.go); only when none has room is another agent offered —
// "No Claude account has room: continue in Codex?" — by the switch the
// menu makes (switch.go), so the conversation crosses the same way and
// Switch back works. Offered, never silent: with [agents] switch_at_limit
// naming an agent the server makes the switch itself after the same few
// seconds a move waits, the offer saying it will; otherwise it waits for
// somebody to take it. Here in the server, so it happens with no client
// open, as moves do.

// switchAtLimit is [agents] switch_at_limit, from the config.toml in the
// server's own folder; ok false when the file cannot be read, so nothing
// is switched on a guess.
func (s *Server) switchAtLimit() (setting string, ok bool) {
	var cfg config.Config
	if _, err := toml.DecodeFile(filepath.Join(s.configDir, "config.toml"), &cfg); err != nil {
		return "", errors.Is(err, fs.ErrNotExist)
	}
	return strings.ToLower(strings.TrimSpace(cfg.Agents.SwitchAtLimit)), true
}

// limitWatched says whether an agent's plan limit is looked for: Claude
// Code's, which moves between accounts, and any agent conch writes
// sessions for, which can be switched from at its limit.
func limitWatched(agent string) bool {
	return agent == "claude" || sessions.WriterFor(agent, sessions.Env{}) != nil
}

// cannotTake says why agent to cannot take a conversation stopped at a
// plan limit now, or "" when it can.
func (s *Server) cannotTake(to string, now time.Time) string {
	ad, ok := s.registry().Get(to)
	if !ok {
		return fmt.Sprintf("conch can't launch %q", to)
	}
	label := sessions.AgentName(to)
	if switchTo(ad) == proto.SwitchNever {
		return label + " cannot be given a first message"
	}
	if !s.installedHere(to) {
		return label + " is not installed here"
	}
	ranked := s.rankAccounts(to, "-")
	if len(ranked) == 0 || !ranked[0].Usable {
		why := "out of plan too"
		if len(ranked) > 0 && ranked[0].SignedIn != nil && !*ranked[0].SignedIn {
			why = "not signed in"
		}
		return label + " is " + why
	}
	return ""
}

// installedHere looks at whether an agent is installed, trusting a recent
// look that found it.
func (s *Server) installedHere(name string) bool {
	if s.agentsSeen.has(name) {
		return true
	}
	ad, ok := s.registry().Get(name)
	if !ok {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	in := ad.Detect(ctx, config.DefaultShell()).Installed
	s.agentsSeen.noteOne(name, in)
	return in
}

// switchCandidates is who could take from's conversation, the one
// switch_at_limit names first, then each agent conch writes sessions for
// in the order agents are listed.
func (s *Server) switchCandidates(from, setting string) []string {
	var out []string
	seen := map[string]bool{from: true, "": true}
	add := func(name string) {
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	if setting != proto.SwitchAtLimitAsk && setting != proto.SwitchAtLimitWait {
		add(setting)
	}
	for _, ad := range s.registry() {
		if sessions.WriterFor(ad.Name(), sessions.Env{}) != nil {
			add(ad.Name())
		}
	}
	return out
}

// offerSwitch says that the agent in info is out of plan on every account
// and which agent could carry on, scheduling the switch when
// switch_at_limit names that agent; false when nothing was offered.
func (s *Server) offerSwitch(info proto.PaneInfo, now time.Time) bool {
	setting, ok := s.switchAtLimit()
	if !ok {
		return false
	}
	from := info.Agent.Name
	if setting == proto.SwitchAtLimitWait {
		log.Printf("switch: %s is out of plan on every %s account, and waits for the reset (switch_at_limit = wait)", info.ID, from)
		return false
	}
	o := proto.AgentSwitchOffer{Pane: info.ID, From: from, Setting: setting}
	for _, r := range s.rankAccounts(from, "-") {
		o.Accounts = append(o.Accounts, r)
		if !r.ResetsAt.IsZero() && (o.ResetsAt.IsZero() || r.ResetsAt.Before(o.ResetsAt)) {
			o.ResetsAt = r.ResetsAt
		}
	}
	for _, to := range s.switchCandidates(from, setting) {
		why := s.cannotTake(to, now)
		if why == "" {
			o.To = to
			break
		}
		if to == setting {
			o.Why = "switch_at_limit names " + to + ", but " + why
		}
	}
	switch {
	case o.To == "" && o.Why == "":
		o.Why = "no other agent here can carry it on"
	case o.To != "":
		ad, _ := s.registry().Get(o.To)
		o.Crossed = switchTo(ad)
		if o.To == setting {
			o.Auto, o.At = true, now.Add(s.mover.delay)
		}
	}
	s.mover.setOffer(o)
	from = fmt.Sprintf("%s account (%s)", from, accountsFull(o.Accounts))
	switch {
	case o.Auto:
		log.Printf("switch: %s is out of plan on every %s account; switching to %s in %v (switch_at_limit)", info.ID, from, o.To, s.mover.delay)
	case o.To != "":
		log.Printf("switch: %s is out of plan on every %s account; offering %s%s", info.ID, from, o.To, map[bool]string{true: " (" + o.Why + ")", false: ""}[o.Why != ""])
	default:
		log.Printf("switch: %s is out of plan on every %s account; nothing to offer: %s", info.ID, from, o.Why)
	}
	s.broadcast(proto.EventAgentSwitchOffered, o)
	return true
}

// withdrawOffer forgets a pane's offer, telling the clients it is gone.
func (s *Server) withdrawOffer(pane, why string) {
	o, ok := s.mover.takeOffer(pane)
	if !ok {
		return
	}
	log.Printf("switch: %s: the offer of %s is gone (%s)", pane, o.To, why)
	s.broadcast(proto.EventAgentSwitchOffered, proto.AgentSwitchOffer{Pane: pane, From: o.From, To: o.To, Gone: true})
}

// switchOffered makes an offer's switch once its time has come, when it
// was to be made unasked; true when the pane has an offer, made or not.
func (s *Server) switchOffered(info proto.PaneInfo, now time.Time) bool {
	o, ok := s.mover.offerOf(info.ID)
	if !ok {
		return false
	}
	if !o.Auto || now.Before(o.At) {
		return true
	}
	if _, still := s.mover.takeOffer(info.ID); !still {
		return true // taken meanwhile: switched by hand, or called off
	}
	s.mover.hold(info.ID)
	// Not Now: background commands are waited for, as for any switch.
	if _, perr := s.switchAgent(proto.AgentSwitchParams{ID: info.ID, To: o.To, Reason: "limit"}); perr != nil {
		log.Printf("switch: %s to %s at its limit: %s", info.ID, o.To, perr.Message)
		s.broadcast(proto.EventAgentSwitched, proto.AgentSwitchResult{Pane: info, From: o.From, To: o.To, Reason: "limit", Error: perr.Message})
	}
	return true
}

func (m *mover) setOffer(o proto.AgentSwitchOffer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.offers[o.Pane] = o
}

func (m *mover) offerOf(pane string) (proto.AgentSwitchOffer, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.offers[pane]
	return o, ok
}

func (m *mover) takeOffer(pane string) (proto.AgentSwitchOffer, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.offers[pane]
	delete(m.offers, pane)
	return o, ok
}

// has reports whether name was last found installed, recently enough to
// go by.
func (a *agentsSeen) has(name string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	installed, known := a.installed[name]
	return known && installed && time.Since(a.at) <= agentsSeenFor
}

// accountsFull says how full each of an agent's accounts is, for a log
// line: "work 100%, personal 97%".
func accountsFull(rs []proto.AccountRank) string {
	var parts []string
	for _, r := range rs {
		parts = append(parts, fmt.Sprintf("%s %d%%", accounts.Label(r.Account), int(100-r.Room)))
	}
	return strings.Join(parts, ", ")
}

package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/adapter"
	"github.com/Amitgb14/conch/internal/proto"
)

// limitServer is a server whose Claude Code is out of plan on its only
// account, with Codex installed and signed in, and whose shell finds no
// agent by itself, so what is installed is what the test says.
func limitServer(t *testing.T) *Server {
	t.Helper()
	home := a5IsolateEnv(t)
	s, _ := a5Server(t)
	var err error
	if s.adapters, err = adapter.New("/usr/local/bin/conch", s.configDir); err != nil {
		t.Fatal(err)
	}
	sh := filepath.Join(t.TempDir(), "finds-nothing")
	os.WriteFile(sh, []byte("#!/bin/sh\nexit 1\n"), 0o755)
	t.Setenv("SHELL", sh)
	os.MkdirAll(filepath.Join(home, ".codex"), 0o700)
	os.WriteFile(filepath.Join(home, ".codex", "auth.json"), []byte(`{"tokens":{}}`), 0o600)
	s.agentsSeen.noteOne("codex", true)
	s.setLimits(proto.PlanLimits{Agent: "claude", FiveHour: &proto.LimitWindow{UsedPct: 100, ResetsAt: time.Now().Add(2 * time.Hour)}})
	return s
}

func setSwitchAtLimit(t *testing.T, s *Server, value string) {
	t.Helper()
	body := "[agents]\nswitch_at_limit = " + value + "\n"
	if value == "" {
		body = "[agents]\ndefault = \"claude\"\n" // a config of before switch_at_limit
	}
	if err := os.WriteFile(filepath.Join(s.configDir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

var atLimitInfo = proto.PaneInfo{ID: "p1", State: proto.PaneRunning, Agent: &proto.AgentStatus{Name: "claude", State: proto.AgentIdle}}

// TestSwitchOfferedAtALimit: out of plan on every Claude account, Codex is
// offered — only offered, unless switch_at_limit names it; an agent that
// cannot take the conversation now is never switched to by itself, and
// the offer says why.
func TestSwitchOfferedAtALimit(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		name, setting string
		prep          func(s *Server)
		offered       bool
		to            string
		auto          bool
		why           string
	}{
		{name: "no setting (an older config)", setting: "", offered: true, to: "codex"},
		{name: "ask", setting: `""`, offered: true, to: "codex"},
		{name: "codex, unasked", setting: `"codex"`, offered: true, to: "codex", auto: true},
		{name: "written in capitals", setting: `"Codex"`, offered: true, to: "codex", auto: true},
		{name: "wait", setting: `"wait"`, offered: false},
		{name: "codex not installed", setting: `"codex"`, prep: func(s *Server) { s.agentsSeen.noteOne("codex", false) },
			offered: true, why: "Codex is not installed here"},
		{name: "codex out of plan too", setting: `"codex"`,
			prep: func(s *Server) {
				s.setLimits(proto.PlanLimits{Agent: "codex", FiveHour: &proto.LimitWindow{UsedPct: 100, ResetsAt: now.Add(time.Hour)}})
			},
			offered: true, why: "Codex is out of plan too"},
		{name: "codex not signed in", setting: `"codex"`,
			prep: func(s *Server) { os.Remove(filepath.Join(os.Getenv("HOME"), ".codex", "auth.json")) },
			offered: true, why: "Codex is not signed in"},
		{name: "an agent that cannot take a first message", setting: `"aider"`, offered: true, to: "codex",
			why: "cannot be given a first message"},
		{name: "an agent conch does not know", setting: `"codx"`, offered: true, to: "codex", why: `can't launch "codx"`},
		{name: "itself", setting: `"claude"`, offered: true, to: "codex"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := limitServer(t)
			ev := a5Listen(t, s)
			setSwitchAtLimit(t, s, c.setting)
			if c.prep != nil {
				c.prep(s)
			}
			if got := s.offerSwitch(atLimitInfo, now); got != c.offered {
				t.Fatalf("offered: %v", got)
			}
			o, ok := s.mover.offerOf("p1")
			if ok != c.offered {
				t.Fatalf("kept: %v", ok)
			}
			if !c.offered {
				if n := ev.count(proto.EventAgentSwitchOffered); n != 0 {
					t.Fatalf("%d offers sent", n)
				}
				return
			}
			if o.To != c.to || o.Auto != c.auto || !strings.Contains(o.Why, c.why) || o.From != "claude" {
				t.Fatalf("offer: %+v", o)
			}
			if c.auto && !o.At.Equal(now.Add(s.mover.delay)) {
				t.Errorf("at %v, want %v", o.At, now.Add(s.mover.delay))
			}
			if !c.auto && !o.At.IsZero() {
				t.Errorf("an offer to ask about has a time: %v", o.At)
			}
			if o.To == "codex" && o.Crossed != proto.SwitchNative {
				t.Errorf("crossed %q", o.Crossed)
			}
			if len(o.Accounts) != 1 || o.Accounts[0].Room != 0 || o.ResetsAt.IsZero() {
				t.Errorf("accounts %+v, resets %v", o.Accounts, o.ResetsAt)
			}
			waitFor(t, func() bool { return ev.count(proto.EventAgentSwitchOffered) == 1 })
		})
	}
}

// An unreadable config switches nothing on a guess, and offers nothing.
func TestSwitchAtLimitUnreadable(t *testing.T) {
	s := limitServer(t)
	os.WriteFile(filepath.Join(s.configDir, "config.toml"), []byte("[agents\nswitch_at_limit = "), 0o600)
	if s.offerSwitch(atLimitInfo, time.Now()) {
		t.Fatal("offered with a config that cannot be read")
	}
}

// The countdown of an offer made unasked: nothing before its time; at it,
// the offer is taken and the agent held, and a failed switch is said.
// One made only to ask stays until somebody answers.
func TestSwitchOfferedRunsOut(t *testing.T) {
	s := limitServer(t)
	ev := a5Listen(t, s)
	now := time.Now()
	s.mover.setOffer(proto.AgentSwitchOffer{Pane: "p9", From: "claude", To: "codex"})
	info := proto.PaneInfo{ID: "p9", Agent: &proto.AgentStatus{Name: "claude"}}
	if !s.switchOffered(info, now.Add(time.Hour)) {
		t.Fatal("an offer to ask about was not seen")
	}
	if _, ok := s.mover.offerOf("p9"); !ok {
		t.Fatal("an offer to ask about was taken by itself")
	}
	s.mover.setOffer(proto.AgentSwitchOffer{Pane: "p9", From: "claude", To: "codex", Auto: true, At: now.Add(time.Second)})
	if !s.switchOffered(info, now) {
		t.Fatal("not seen")
	}
	if _, ok := s.mover.offerOf("p9"); !ok {
		t.Fatal("switched before its time")
	}
	// Its time: the pane is gone, so the switch fails and says so.
	if !s.switchOffered(info, now.Add(time.Second)) {
		t.Fatal("not seen")
	}
	if _, ok := s.mover.offerOf("p9"); ok || !s.mover.isHeld("p9") {
		t.Fatal("the offer stayed, or the agent is not held")
	}
	waitFor(t, func() bool { return ev.count(proto.EventAgentSwitched) == 1 })
	if s.switchOffered(info, now.Add(time.Hour)) {
		t.Fatal("an offer made is still there")
	}
}

// An offer withdrawn is said once, as gone; there is nothing to say for
// a pane with none.
func TestSwitchOfferWithdrawn(t *testing.T) {
	s := limitServer(t)
	ev := a5Listen(t, s)
	s.withdrawOffer("p1", "nothing")
	s.mover.setOffer(proto.AgentSwitchOffer{Pane: "p1", From: "claude", To: "codex"})
	s.withdrawOffer("p1", "worked again")
	s.withdrawOffer("p1", "twice")
	waitFor(t, func() bool { return ev.count(proto.EventAgentSwitchOffered) == 1 })
	time.Sleep(50 * time.Millisecond)
	if n := ev.count(proto.EventAgentSwitchOffered); n != 1 {
		t.Fatalf("%d said", n)
	}
	// A dropped pane forgets its offer too.
	s.mover.setOffer(proto.AgentSwitchOffer{Pane: "p2"})
	s.mover.drop("p2")
	if _, ok := s.mover.offerOf("p2"); ok {
		t.Fatal("drop kept the offer")
	}
}

func TestSwitchCandidates(t *testing.T) {
	s := limitServer(t)
	for _, c := range []struct {
		from, setting, want string
	}{
		{"claude", "", "codex"},
		{"codex", "", "claude"},
		{"claude", "wait", "codex"},
		{"claude", "gemini", "gemini codex"},
		{"claude", "codex", "codex"},
		{"claude", "claude", "codex"},
	} {
		if got := strings.Join(s.switchCandidates(c.from, c.setting), " "); got != c.want {
			t.Errorf("%s with %q: %q, want %q", c.from, c.setting, got, c.want)
		}
	}
}

func TestLimitWatched(t *testing.T) {
	for agent, want := range map[string]bool{"claude": true, "codex": true, "gemini": false, "": false} {
		if limitWatched(agent) != want {
			t.Errorf("%q: %v", agent, !want)
		}
	}
	// Codex's words for its limit are seen as well as Claude Code's.
	info := proto.PaneInfo{Agent: &proto.AgentStatus{Name: "codex", State: proto.AgentIdle}}
	if !atLimit(info, []string{"■ You've hit your usage limit. Upgrade to Pro or try again at 3:05 PM."}) {
		t.Error("Codex's limit not seen")
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

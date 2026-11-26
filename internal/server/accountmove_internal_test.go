package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// TestAtLimit: an agent its plan stopped says so at the end of its screen;
// one still working, or that merely mentions a limit higher up, is not.
func TestAtLimit(t *testing.T) {
	idle := proto.PaneInfo{Agent: &proto.AgentStatus{Name: "claude", State: proto.AgentIdle}}
	working := proto.PaneInfo{Agent: &proto.AgentStatus{Name: "claude", State: proto.AgentWorking}}
	screen := func(tail ...string) []string {
		lines := make([]string, 30)
		return append(lines, tail...)
	}
	for _, c := range []struct {
		name   string
		info   proto.PaneInfo
		screen []string
		want   bool
	}{
		{"today's words", idle, screen("⎿ You've hit your limit · resets 11pm (America/Los_Angeles)", "  /upgrade to increase your usage limit."), true},
		{"older words", idle, screen("Claude usage limit reached. Your limit will reset at 3pm."), true},
		{"blocked counts", proto.PaneInfo{Agent: &proto.AgentStatus{Name: "claude", State: proto.AgentBlocked}}, screen("You've hit your limit"), true},
		{"still working", working, screen("You've hit your limit"), false},
		{"no agent", proto.PaneInfo{}, screen("You've hit your limit"), false},
		{"talked about, long ago", idle, append([]string{"You've hit your limit"}, make([]string, 40)...), false},
		{"nothing of the kind", idle, screen("● All 42 packages ok."), false},
		{"an empty screen", idle, nil, false},
	} {
		if got := atLimit(c.info, c.screen); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// TestCopyConversation: the file and its subagents' folder land at the same
// place under the other account; a copy there that has gone on is kept;
// something that is not a conversation file is refused.
func TestCopyConversation(t *testing.T) {
	from, to := t.TempDir(), t.TempDir()
	id := "1b2c3d4e-0000-4000-8000-000000000001"
	src := filepath.Join(from, "projects", "-src-app", id+".jsonl")
	os.MkdirAll(filepath.Join(from, "projects", "-src-app", id, "subagents"), 0o700)
	os.WriteFile(src, []byte(`{"type":"user"}`+"\n"), 0o600)
	os.WriteFile(filepath.Join(from, "projects", "-src-app", id, "subagents", "a.jsonl"), []byte("sub\n"), 0o600)

	got, err := copyConversation(src, from, to)
	if err != nil || got != id {
		t.Fatalf("copy: %q %v", got, err)
	}
	dst := filepath.Join(to, "projects", "-src-app", id+".jsonl")
	if b, err := os.ReadFile(dst); err != nil || string(b) != `{"type":"user"}`+"\n" {
		t.Errorf("copied file: %q %v", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(to, "projects", "-src-app", id, "subagents", "a.jsonl")); err != nil || string(b) != "sub\n" {
		t.Errorf("subagent file: %q %v", b, err)
	}
	if _, err := os.Stat(dst + ".conch-tmp"); !os.IsNotExist(err) {
		t.Error("a temporary file was left")
	}

	// It went there before and has gone on since: that copy stays.
	os.WriteFile(dst, []byte(`{"type":"user"}`+"\n"+`{"type":"assistant"}`+"\n"), 0o600)
	later := time.Now().Add(time.Minute)
	os.Chtimes(dst, later, later)
	if _, err := copyConversation(src, from, to); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); !strings.Contains(string(b), "assistant") {
		t.Errorf("the copy that had gone on was overwritten: %q", b)
	}

	// A conversation kept outside the account it ran on still lands under
	// projects/ of the other, by its folder's name.
	odd := filepath.Join(t.TempDir(), "elsewhere", id+".jsonl")
	os.MkdirAll(filepath.Dir(odd), 0o700)
	os.WriteFile(odd, []byte("x\n"), 0o600)
	if _, err := copyConversation(odd, from, to); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(to, "projects", "elsewhere", id+".jsonl")); err != nil {
		t.Errorf("outside conversation: %v", err)
	}

	for _, bad := range []string{filepath.Join(from, "projects", "notes.txt"), filepath.Join(from, "projects", ".jsonl")} {
		if _, err := copyConversation(bad, from, to); err == nil {
			t.Errorf("%s copied as a conversation", bad)
		}
	}
	if _, err := copyConversation(filepath.Join(from, "projects", "-src-app", "missing.jsonl"), from, to); err == nil {
		t.Error("a missing file copied")
	}
}

func TestFullestWindowAndWords(t *testing.T) {
	now := time.Date(2026, 11, 19, 20, 0, 0, 0, time.UTC)
	l := proto.PlanLimits{FiveHour: &proto.LimitWindow{UsedPct: 100, ResetsAt: now.Add(-time.Minute)}, // turned over
		Week: &proto.LimitWindow{UsedPct: 77, ResetsAt: now.Add(26 * time.Hour)}}
	if used, at := fullestWindow(l, now); used != 77 || !at.Equal(now.Add(26*time.Hour)) {
		t.Errorf("fullest: %v %v", used, at)
	}
	if used, at := fullestWindow(proto.PlanLimits{}, now); used != 0 || !at.IsZero() {
		t.Errorf("nothing: %v %v", used, at)
	}
	yes, no := true, false
	for _, c := range []struct {
		r    proto.AccountRank
		want string
	}{
		{proto.AccountRank{SignedIn: &no, Reported: true, Room: 90}, "not signed in"},
		{proto.AccountRank{SignedIn: &yes}, "nothing reported yet"},
		{proto.AccountRank{Reported: true, Room: 0, ResetsAt: now.Add(2*time.Hour + 10*time.Minute)}, "full · resets in 2h 10m"},
		{proto.AccountRank{Reported: true, Room: 0}, "full"},
		{proto.AccountRank{Reported: true, Room: 96.4}, "96% room"},
	} {
		if got := rankWhy(c.r, now); got != c.want {
			t.Errorf("%+v: %q, want %q", c.r, got, c.want)
		}
	}
	if shortWait(30*time.Second) != "a minute" || shortWait(6*time.Minute) != "6m" || shortWait(26*time.Hour+5*time.Minute) != "26h 5m" {
		t.Error("shortWait")
	}
}

// TestMoveConfig: on by default, off with no_move_at_limit, at the line
// only when asked; an unreadable file moves nothing on a guess.
func TestMoveConfig(t *testing.T) {
	s := &Server{configDir: t.TempDir()}
	if lim, line := s.moveConfig(); !lim || line {
		t.Errorf("no file: %v %v", lim, line)
	}
	write := func(body string) {
		os.WriteFile(filepath.Join(s.configDir, "config.toml"), []byte(body), 0o600)
	}
	write("[agents]\nno_move_at_limit = true\nmove_at_line = true\n")
	if lim, line := s.moveConfig(); lim || !line {
		t.Errorf("both set: %v %v", lim, line)
	}
	write("[agents\nbroken")
	if lim, line := s.moveConfig(); lim || line {
		t.Errorf("unreadable: %v %v", lim, line)
	}
}

func TestMover(t *testing.T) {
	m := newMover()
	m.schedule(proto.AgentMoving{Pane: "p1", To: "work"})
	if mv, ok := m.get("p1"); !ok || mv.To != "work" {
		t.Errorf("scheduled: %+v %v", mv, ok)
	}
	m.forget("p1")
	if _, ok := m.get("p1"); ok {
		t.Error("forgotten move still there")
	}
	m.hold("p1")
	if !m.isHeld("p1") || m.isHeld("p2") {
		t.Error("hold")
	}
	m.release("p1")
	if m.isHeld("p1") {
		t.Error("release")
	}
}

// TestMoverFinishedTurn: an agent's first look is never a finished turn; after
// that, a turn counted since the last look is, once.
func TestMoverFinishedTurn(t *testing.T) {
	m := newMover()
	if m.finishedTurn("p1", 5) {
		t.Error("the first look at an idle agent counted as a finished turn")
	}
	if m.finishedTurn("p1", 5) {
		t.Error("no new turn, but finished")
	}
	if !m.finishedTurn("p1", 6) {
		t.Error("a new turn was not seen")
	}
	if m.finishedTurn("p1", 6) {
		t.Error("the same turn seen twice")
	}
	m.drop("p1")
	if m.finishedTurn("p1", 9) {
		t.Error("after drop, the next look is a first look")
	}
}

// TestMoverLookAtTurn: a look judges the turn by the snapshot it was given.
// A look whose snapshot is idle at turn 3 records 3, even if the agent has
// started turn 4 since — that turn ends as a finished one, which a look
// reading the count afresh recorded as already seen.
func TestMoverLookAtTurn(t *testing.T) {
	m := newMover()
	look := func(state string, turn int) (bool, bool) {
		return m.lookAtTurn(proto.PaneInfo{ID: "p1", Agent: &proto.AgentStatus{Name: "claude", State: state, Turn: turn}})
	}
	if idle, fin := look(proto.AgentIdle, 3); !idle || fin {
		t.Errorf("first look: idle %v finished %v", idle, fin)
	}
	if idle, fin := look(proto.AgentWorking, 4); idle || fin {
		t.Errorf("working: idle %v finished %v", idle, fin)
	}
	if idle, fin := look(proto.AgentDone, 4); !idle || !fin {
		t.Errorf("the turn that began after an idle look did not finish: idle %v finished %v", idle, fin)
	}
	if _, fin := look(proto.AgentIdle, 4); fin {
		t.Error("the same turn finished twice")
	}
	if idle, fin := m.lookAtTurn(proto.PaneInfo{ID: "p2"}); idle || fin {
		t.Error("no agent, but idle")
	}
	if _, fin := look(proto.AgentBlocked, 5); fin {
		t.Error("a blocked agent finished its turn")
	}
}

// TestJobCommands: what an agent left running, as the refusal and the card
// say it; long commands cut, none for no agent.
func TestJobCommands(t *testing.T) {
	if got := jobCommands(proto.PaneInfo{}); got != nil {
		t.Errorf("no agent: %v", got)
	}
	long := "go test -race -count=1 -timeout 20m ./internal/server/ ./cmd/conch/ ./internal/accounts/"
	info := proto.PaneInfo{Agent: &proto.AgentStatus{Name: "claude", Jobs: []proto.BackgroundJob{
		{PID: 1, Command: "  npm run dev  "}, {PID: 2, Command: long}}}}
	got := jobCommands(info)
	if len(got) != 2 || got[0] != "npm run dev" || len([]rune(got[1])) > 60 || !strings.HasSuffix(got[1], "…") {
		t.Errorf("jobs: %q", got)
	}
}

// TestMoverWaiting: the clock starts at the first ask, runs on, and stops
// (and starts over) once nothing waits.
func TestMoverWaiting(t *testing.T) {
	m := newMover()
	t0 := time.Date(2026, 11, 20, 4, 0, 0, 0, time.UTC)
	if w := m.waiting("p1", t0, false); w != 0 {
		t.Errorf("first ask: %v", w)
	}
	if w := m.waiting("p1", t0.Add(3*time.Minute), false); w != 3*time.Minute {
		t.Errorf("three minutes on: %v", w)
	}
	m.waiting("p1", t0.Add(4*time.Minute), true)
	if w := m.waiting("p1", t0.Add(20*time.Minute), false); w != 0 {
		t.Errorf("after stopping, a new first ask: %v", w)
	}
	m.drop("p1")
	if _, ok := m.waitFrom["p1"]; ok {
		t.Error("drop left the clock")
	}
}

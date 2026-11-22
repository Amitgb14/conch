package server_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/server"
)

// moveHome is a home with a stand-in claude on the PATH agents get: it
// writes the account folder it was given and its arguments, then waits.
func moveHome(t *testing.T) (home, log string) {
	t.Helper()
	home = a5Env(t)
	t.Setenv("SHELL", "/bin/sh")
	bin := filepath.Join(home, ".local", "bin")
	os.MkdirAll(bin, 0o755)
	log = filepath.Join(home, "launches")
	script := "#!/bin/sh\nprintf '%s|%s\\n' \"$CLAUDE_CONFIG_DIR\" \"$*\" >> " + log + "\nexec cat\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	// The agent's own account is signed in.
	os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"oauthAccount":{"accountUuid":"u-own"}}`), 0o600)
	return home, log
}

// launches waits for the stand-in to have started n times, and says how.
func launches(t *testing.T, log string, n int) []string {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		b, _ := os.ReadFile(log)
		lines := strings.Split(strings.TrimSpace(string(b)), "\n")
		if len(b) > 0 && len(lines) >= n {
			return lines
		}
		time.Sleep(50 * time.Millisecond)
	}
	b, _ := os.ReadFile(log)
	t.Fatalf("waited for %d launches, got %q", n, b)
	return nil
}

func panesNow(t *testing.T, c *client.Client) map[string]proto.PaneInfo {
	t.Helper()
	var l proto.PaneList
	if err := c.Call(t.Context(), proto.MethodPaneList, nil, &l); err != nil {
		t.Fatal(err)
	}
	out := map[string]proto.PaneInfo{}
	for _, p := range l.Panes {
		out[p.ID] = p
	}
	return out
}

// TestMoveAnAgentToAnotherAccount: by hand, an agent's conversation goes
// into the other account's folder and is resumed there in a new pane that
// says what it replaced and where it came from; moving it back forgets
// that. What cannot be done is refused before anything is stopped.
func TestMoveAnAgentToAnotherAccount(t *testing.T) {
	home, log := moveHome(t)
	c, dir := startServer(t)
	if !hasCap(c, proto.CapAgentMove) {
		t.Fatal("agent.move.v1 not announced")
	}
	ctx := t.Context()
	for _, name := range []string{"work", "cold"} {
		if err := c.Call(ctx, proto.MethodAccountAdd, proto.AccountAddParams{Agent: "claude", Name: name}, nil); err != nil {
			t.Fatal(err)
		}
	}
	work := filepath.Join(dir, "accounts", "claude", "work")
	os.WriteFile(filepath.Join(work, ".claude.json"), []byte(`{"oauthAccount":{"accountUuid":"u-work"}}`), 0o600)

	var old proto.PaneInfo
	if err := c.Call(ctx, proto.MethodPaneCreate, proto.PaneCreateParams{Agent: "claude", Cwd: dir, Cols: 80, Rows: 20}, &old); err != nil {
		t.Fatal(err)
	}
	if first := launches(t, log, 1)[0]; first != "|" && !strings.HasPrefix(first, "|") {
		t.Fatalf("the first launch was on an account: %q", first)
	}

	// Refused before its conversation is known.
	badRequest(t, c.Call(ctx, proto.MethodAgentMove, proto.AgentMoveParams{ID: old.ID, Account: "work"}, nil), "no conversation yet")

	id := "1b2c3d4e-0000-4000-8000-000000000001"
	conv := filepath.Join(home, ".claude", "projects", "-src", id+".jsonl")
	os.MkdirAll(filepath.Dir(conv), 0o700)
	os.WriteFile(conv, []byte(`{"type":"user","message":{"content":"ship it"}}`+"\n"), 0o600)
	if err := c.Call(ctx, proto.MethodAgentReport, proto.AgentReportParams{ID: old.ID, Agent: "claude", Event: "Stop", TranscriptPath: conv}, nil); err != nil {
		t.Fatal(err)
	}

	badRequest(t, c.Call(ctx, proto.MethodAgentMove, proto.AgentMoveParams{ID: old.ID, Account: "nope"}, nil), "an account that is not there")
	if err := c.Call(ctx, proto.MethodAgentReport, proto.AgentReportParams{ID: old.ID, Agent: "claude", Event: "UserPromptSubmit", TranscriptPath: conv}, nil); err != nil {
		t.Fatal(err)
	}
	badRequest(t, c.Call(ctx, proto.MethodAgentMove, proto.AgentMoveParams{ID: old.ID, Account: "work"}, nil), "mid-turn")
	if err := c.Call(ctx, proto.MethodAgentReport, proto.AgentReportParams{ID: old.ID, Agent: "claude", Event: "Stop", TranscriptPath: conv}, nil); err != nil {
		t.Fatal(err)
	}
	badRequest(t, c.Call(ctx, proto.MethodAgentMove, proto.AgentMoveParams{ID: old.ID, Account: "default"}, nil), "the account it is on")
	badRequest(t, c.Call(ctx, proto.MethodAgentMove, proto.AgentMoveParams{ID: old.ID, Account: "cold"}, nil), "an account not signed in")
	if _, ok := panesNow(t, c)[old.ID]; !ok {
		t.Fatal("a refused move stopped the agent")
	}

	var nw proto.PaneInfo
	if err := c.Call(ctx, proto.MethodAgentMove, proto.AgentMoveParams{ID: old.ID, Account: "work"}, &nw); err != nil {
		t.Fatal(err)
	}
	if nw.Account != "work" || nw.MovedFrom != "default" || nw.Replaces != old.ID || nw.ID == old.ID {
		t.Fatalf("moved: %+v", nw)
	}
	second := launches(t, log, 2)[1]
	if !strings.HasPrefix(second, work+"|") || !strings.Contains(second, id) {
		t.Errorf("resumed as %q, want on %s with %s", second, work, id)
	}
	if b, err := os.ReadFile(filepath.Join(work, "projects", "-src", id+".jsonl")); err != nil || !strings.Contains(string(b), "ship it") {
		t.Errorf("the conversation in work's folder: %q %v", b, err)
	}
	now := panesNow(t, c)
	if _, ok := now[old.ID]; ok {
		t.Error("the old pane is still there")
	}
	if p, ok := now[nw.ID]; !ok || p.MovedFrom != "default" || p.Account != "work" {
		t.Errorf("the new pane, listed: %+v", p)
	}
	waitEvent(t, c, func(m proto.Message) bool {
		var mv proto.AgentMoved
		return m.Event == proto.EventAgentMoved && json.Unmarshal(m.Data, &mv) == nil &&
			mv.Pane == nw.ID && mv.Replaces == old.ID && mv.From == "default" && mv.To == "work" && mv.Reason == "hand"
	})

	// Back: it starts on its own account again, and nothing says "moved".
	conv2 := filepath.Join(work, "projects", "-src", id+".jsonl")
	if err := c.Call(ctx, proto.MethodAgentReport, proto.AgentReportParams{ID: nw.ID, Agent: "claude", Event: "Stop", TranscriptPath: conv2}, nil); err != nil {
		t.Fatal(err)
	}
	var back proto.PaneInfo
	if err := c.Call(ctx, proto.MethodAgentMove, proto.AgentMoveParams{ID: nw.ID, Account: "default", Reason: "back"}, &back); err != nil {
		t.Fatal(err)
	}
	if back.Account != "" || back.MovedFrom != "" || back.Replaces != nw.ID {
		t.Errorf("moved back: %+v", back)
	}
	if third := launches(t, log, 3)[2]; !strings.HasPrefix(third, "|") || !strings.Contains(third, id) {
		t.Errorf("resumed back as %q", third)
	}
}

// TestSuggestAnAccount: the accounts ranked by room, a signed-out one last
// and said why; an agent that keeps one account has nothing to suggest.
func TestSuggestAnAccount(t *testing.T) {
	moveHome(t)
	c, dir := startServer(t)
	ctx := t.Context()
	for _, name := range []string{"work", "cold"} {
		if err := c.Call(ctx, proto.MethodAccountAdd, proto.AccountAddParams{Agent: "claude", Name: name}, nil); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(dir, "accounts", "claude", "work", ".claude.json"), []byte(`{"oauthAccount":{"accountUuid":"u"}}`), 0o600)
	own, _ := startOn(t, c, proto.PaneCreateParams{Agent: "claude", Cwd: dir})
	if err := c.Call(ctx, proto.MethodAgentReport, proto.AgentReportParams{ID: own.ID, Agent: "claude", Event: "StatusLine",
		Limits: &proto.PlanLimits{FiveHour: &proto.LimitWindow{UsedPct: 100, ResetsAt: time.Now().Add(2 * time.Hour)}}}, nil); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, c, func(m proto.Message) bool { return m.Event == proto.EventAgentLimits })

	var sg proto.AccountSuggestion
	if err := c.Call(ctx, proto.MethodAccountSuggest, proto.AccountSuggestParams{Agent: "claude", Exclude: "default"}, &sg); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, a := range sg.Accounts {
		names = append(names, a.Account)
	}
	if strings.Join(names, ",") != "work,cold" || !sg.Accounts[0].Usable || sg.Accounts[1].Usable || sg.Accounts[1].Why != "not signed in" {
		t.Errorf("suggested %+v", sg.Accounts)
	}
	// Nothing left out: the full own account last of the usable ones' kind.
	if err := c.Call(ctx, proto.MethodAccountSuggest, proto.AccountSuggestParams{Agent: "claude"}, &sg); err != nil {
		t.Fatal(err)
	}
	if len(sg.Accounts) != 3 || sg.Accounts[0].Account != "work" || !strings.HasPrefix(func() string {
		for _, a := range sg.Accounts {
			if a.Account == "" {
				return a.Why
			}
		}
		return ""
	}(), "full · resets in") {
		t.Errorf("all of them: %+v", sg.Accounts)
	}
	badRequest(t, c.Call(ctx, proto.MethodAccountSuggest, proto.AccountSuggestParams{Agent: "gemini"}, nil), "gemini")
}

// limitHome is moveHome whose claude really is one to conch (its foreground
// process is named claude: a copy of cat) and whose first start ends on
// Claude Code's words for a plan run out.
func limitHome(t *testing.T) (home, log string) {
	t.Helper()
	home, log = moveHome(t)
	// The repository's stand-in, built under the name claude: a copy of a
	// system binary such as cat is killed by macOS outside its own place.
	real := filepath.Join(home, "real")
	if out, err := exec.Command("go", "build", "-o", filepath.Join(real, "claude"), "../tools/standin").CombinedOutput(); err != nil {
		t.Fatalf("building the stand-in: %v\n%s", err, out)
	}
	script := "#!/bin/sh\nprintf '%s|%s\\n' \"$CLAUDE_CONFIG_DIR\" \"$*\" >> " + log + "\n" +
		"if [ \"$(wc -l < " + log + ")\" -le 1 ]; then echo \"You've hit your limit · resets 11pm\"; fi\n" +
		"exec " + filepath.Join(real, "claude") + "\n"
	if err := os.WriteFile(filepath.Join(home, ".local", "bin", "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return home, log
}

// startAtLimit starts a Claude agent on its own account whose plan has run
// out, with its conversation known, and the work account ready for it.
func startAtLimit(t *testing.T, c *client.Client, home, dir string) proto.PaneInfo {
	t.Helper()
	ctx := t.Context()
	if err := c.Call(ctx, proto.MethodAccountAdd, proto.AccountAddParams{Agent: "claude", Name: "work"}, nil); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "accounts", "claude", "work", ".claude.json"), []byte(`{"oauthAccount":{"accountUuid":"u"}}`), 0o600)
	var p proto.PaneInfo
	if err := c.Call(ctx, proto.MethodPaneCreate, proto.PaneCreateParams{Agent: "claude", Cwd: dir, Cols: 80, Rows: 20}, &p); err != nil {
		t.Fatal(err)
	}
	conv := filepath.Join(home, ".claude", "projects", "-src", "1b2c3d4e-0000-4000-8000-000000000002.jsonl")
	os.MkdirAll(filepath.Dir(conv), 0o700)
	os.WriteFile(conv, []byte(`{"type":"user"}`+"\n"), 0o600)
	if err := c.Call(ctx, proto.MethodAgentReport, proto.AgentReportParams{ID: p.ID, Agent: "claude", Event: "Stop", TranscriptPath: conv}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(ctx, proto.MethodAgentReport, proto.AgentReportParams{ID: p.ID, Agent: "claude", Event: "StatusLine",
		Limits: &proto.PlanLimits{FiveHour: &proto.LimitWindow{UsedPct: 100, ResetsAt: time.Now().Add(2 * time.Hour)}}}, nil); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestAnAgentAtItsLimitMovesByItself: the server sees Claude Code say its
// plan ran out, says where it will move it, and moves it there with the
// conversation, after the delay anyone could have cancelled in.
func TestAnAgentAtItsLimitMovesByItself(t *testing.T) {
	t.Cleanup(server.SetMoveTiming(100*time.Millisecond, 300*time.Millisecond))
	home, log := limitHome(t)
	c, dir := startServer(t)
	p := startAtLimit(t, c, home, dir)
	waitEvent(t, c, func(m proto.Message) bool {
		var mv proto.AgentMoving
		return m.Event == proto.EventAgentMoving && json.Unmarshal(m.Data, &mv) == nil &&
			mv.Pane == p.ID && mv.From == "default" && mv.To == "work" && mv.Reason == "limit"
	})
	var moved proto.AgentMoved
	waitEvent(t, c, func(m proto.Message) bool {
		return m.Event == proto.EventAgentMoved && json.Unmarshal(m.Data, &moved) == nil && moved.Replaces == p.ID
	})
	if moved.To != "work" || moved.Reason != "limit" {
		t.Errorf("moved: %+v", moved)
	}
	if second := launches(t, log, 2)[1]; !strings.HasPrefix(second, filepath.Join(dir, "accounts", "claude", "work")+"|") ||
		!strings.Contains(second, "1b2c3d4e-0000-4000-8000-000000000002") {
		t.Errorf("resumed as %q", second)
	}
	if n := panesNow(t, c)[moved.Pane]; n.Account != "work" || n.MovedFrom != "default" {
		t.Errorf("the new pane: %+v", n)
	}
}

// TestAMoveCalledOffStays: cancelled within the delay, the agent stays on
// its account, and is not scheduled again while it has not worked.
func TestAMoveCalledOffStays(t *testing.T) {
	t.Cleanup(server.SetMoveTiming(100*time.Millisecond, 600*time.Millisecond))
	home, log := limitHome(t)
	c, dir := startServer(t)
	p := startAtLimit(t, c, home, dir)
	waitEvent(t, c, func(m proto.Message) bool { return m.Event == proto.EventAgentMoving })
	if err := c.Call(t.Context(), proto.MethodAgentMoveCancel, proto.AgentMoveCancelParams{ID: p.ID}, nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond) // more than the delay and several looks
	if _, ok := panesNow(t, c)[p.ID]; !ok {
		t.Fatal("the agent moved after the move was called off")
	}
	if b, _ := os.ReadFile(log); strings.Count(string(b), "\n") != 1 {
		t.Errorf("started again: %q", b)
	}
	if err := c.Call(t.Context(), proto.MethodAgentMoveCancel, proto.AgentMoveCancelParams{ID: "p404"}, nil); err == nil {
		t.Error("cancelled the move of a pane that is not there")
	}
}

// TestNoMoveWhenTurnedOff: [agents] no_move_at_limit leaves it be.
func TestNoMoveWhenTurnedOff(t *testing.T) {
	t.Cleanup(server.SetMoveTiming(100*time.Millisecond, 200*time.Millisecond))
	home, log := limitHome(t)
	c, dir := startServer(t)
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[agents]\nno_move_at_limit = true\n"), 0o600)
	p := startAtLimit(t, c, home, dir)
	time.Sleep(1200 * time.Millisecond)
	if _, ok := panesNow(t, c)[p.ID]; !ok {
		t.Fatal("moved with no_move_at_limit")
	}
	if b, _ := os.ReadFile(log); strings.Count(string(b), "\n") != 1 {
		t.Errorf("started again: %q", b)
	}
}

// TestAtTheLineOnlyAnAgentInUseMoves: with move_at_line, an agent past the
// failover line that sits idle stays; once it has done a turn, it moves as
// that turn ends.
func TestAtTheLineOnlyAnAgentInUseMoves(t *testing.T) {
	t.Cleanup(server.SetMoveTiming(100*time.Millisecond, 200*time.Millisecond))
	home, log := limitHome(t)
	// No limit message this time: the agent is only past the line.
	script := "#!/bin/sh\nprintf '%s|%s\\n' \"$CLAUDE_CONFIG_DIR\" \"$*\" >> " + log + "\nexec " + filepath.Join(home, "real", "claude") + "\n"
	if err := os.WriteFile(filepath.Join(home, ".local", "bin", "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	c, dir := startServer(t)
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[agents]\nmove_at_line = true\n"), 0o600)
	ctx := t.Context()
	if err := c.Call(ctx, proto.MethodAccountFailover, proto.AccountFailoverParams{At: 70}, nil); err != nil {
		t.Fatal(err)
	}
	p := startAtLimit(t, c, home, dir) // done, its conversation known, its account at 100%
	if err := c.Call(ctx, proto.MethodAgentReport, proto.AgentReportParams{ID: p.ID, Agent: "claude", Event: "StatusLine",
		Limits: &proto.PlanLimits{FiveHour: &proto.LimitWindow{UsedPct: 80, ResetsAt: time.Now().Add(2 * time.Hour)}}}, nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second) // many looks
	if _, ok := panesNow(t, c)[p.ID]; !ok {
		t.Fatal("an idle agent past the line was moved")
	}

	// A turn: it starts work and finishes.
	conv := filepath.Join(home, ".claude", "projects", "-src", "1b2c3d4e-0000-4000-8000-000000000002.jsonl")
	for _, ev := range []string{"UserPromptSubmit", "Stop"} {
		if err := c.Call(ctx, proto.MethodAgentReport, proto.AgentReportParams{ID: p.ID, Agent: "claude", Event: ev, TranscriptPath: conv}, nil); err != nil {
			t.Fatal(err)
		}
		time.Sleep(300 * time.Millisecond)
	}
	var moved proto.AgentMoved
	waitEvent(t, c, func(m proto.Message) bool {
		return m.Event == proto.EventAgentMoved && json.Unmarshal(m.Data, &moved) == nil && moved.Replaces == p.ID
	})
	if moved.To != "work" || moved.Reason != "line" {
		t.Errorf("moved: %+v", moved)
	}
	if second := launches(t, log, 2)[1]; !strings.HasPrefix(second, filepath.Join(dir, "accounts", "claude", "work")+"|") {
		t.Errorf("resumed as %q", second)
	}
}

// TestAMovedAgentBringsBackNoProject: an agent running in a repository
// whose project was removed is moved without adding that project again.
func TestAMovedAgentBringsBackNoProject(t *testing.T) {
	home, _ := moveHome(t)
	c, dir := startServer(t)
	ctx := t.Context()
	if err := c.Call(ctx, proto.MethodAccountAdd, proto.AccountAddParams{Agent: "claude", Name: "work"}, nil); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "accounts", "claude", "work", ".claude.json"), []byte(`{"oauthAccount":{"accountUuid":"u"}}`), 0o600)
	repo := filepath.Join(dir, "removed-repo")
	os.MkdirAll(repo, 0o755)
	git(t, repo, "init", "-q")

	var p proto.PaneInfo
	if err := c.Call(ctx, proto.MethodPaneCreate, proto.PaneCreateParams{Agent: "claude", Cwd: repo, NoProject: true, Cols: 80, Rows: 20}, &p); err != nil {
		t.Fatal(err)
	}
	conv := filepath.Join(home, ".claude", "projects", "-repo", "1b2c3d4e-0000-4000-8000-000000000003.jsonl")
	os.MkdirAll(filepath.Dir(conv), 0o700)
	os.WriteFile(conv, []byte("{}\n"), 0o600)
	if err := c.Call(ctx, proto.MethodAgentReport, proto.AgentReportParams{ID: p.ID, Agent: "claude", Event: "Stop", TranscriptPath: conv}, nil); err != nil {
		t.Fatal(err)
	}
	var nw proto.PaneInfo
	if err := c.Call(ctx, proto.MethodAgentMove, proto.AgentMoveParams{ID: p.ID, Account: "work"}, &nw); err != nil {
		t.Fatal(err)
	}
	if nw.ProjectID != "" {
		t.Errorf("the moved agent joined project %q", nw.ProjectID)
	}
	var l proto.ProjectList
	if err := c.Call(ctx, proto.MethodProjectList, nil, &l); err != nil {
		t.Fatal(err)
	}
	for _, pr := range l.Projects {
		if pr.Path == repo {
			t.Errorf("the removed project came back: %+v", pr)
		}
	}
}

// TestLimitWordsOnScreenAloneMoveNothing: an agent whose screen ends on the
// limit message while its account's plan is far from full — it printed the
// words itself — stays where it is.
func TestLimitWordsOnScreenAloneMoveNothing(t *testing.T) {
	t.Cleanup(server.SetMoveTiming(100*time.Millisecond, 200*time.Millisecond))
	home, log := limitHome(t)
	c, dir := startServer(t)
	p := startAtLimit(t, c, home, dir)
	// The plan as it really is: 3% of five hours.
	if err := c.Call(t.Context(), proto.MethodAgentReport, proto.AgentReportParams{ID: p.ID, Agent: "claude", Event: "StatusLine",
		Limits: &proto.PlanLimits{FiveHour: &proto.LimitWindow{UsedPct: 3, ResetsAt: time.Now().Add(2 * time.Hour)}}}, nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1200 * time.Millisecond)
	if _, ok := panesNow(t, c)[p.ID]; !ok {
		t.Fatal("moved on the words alone")
	}
	if b, _ := os.ReadFile(log); strings.Count(string(b), "\n") != 1 {
		t.Errorf("started again: %q", b)
	}
}

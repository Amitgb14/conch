package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/detect"
	"github.com/Amitgb14/conch/internal/proto"
)

func withPermissionConfig(t *testing.T, mode string, rules ...config.PermissionRule) {
	t.Helper()
	old := loadPermissionConfig
	loadPermissionConfig = func() (config.Config, error) {
		cfg := config.Default()
		cfg.Agents.PermissionMode = mode
		cfg.Permissions = rules
		return cfg, nil
	}
	t.Cleanup(func() { loadPermissionConfig = old })
}

func TestDecidePermission(t *testing.T) {
	s, _, work := shareFixture(t)
	e := agentPane(t, s, "p1", "claude", work, "sleep 30")
	t.Cleanup(func() { e.p.Close() })
	ask := func(tool, subject string) proto.PermissionAnswer {
		t.Helper()
		ans, perr := s.decidePermission(proto.PermissionAsk{ID: "p1", Agent: "claude", Tool: tool, Subject: subject})
		if perr != nil {
			t.Fatal(perr)
		}
		return ans
	}
	// No rules: everything is asked as usual.
	withPermissionConfig(t, "")
	if a := ask("Bash", "go test ./..."); a.Decision != "" {
		t.Fatalf("no rules answered %+v", a)
	}
	withPermissionConfig(t, "",
		config.PermissionRule{Tool: "Bash", Match: "*rm -rf*", Action: "deny"},
		config.PermissionRule{Tool: "Bash", Match: "go test *", Action: "allow"},
		config.PermissionRule{Tool: "Edit", Project: "nope", Action: "allow"},
	)
	if a := ask("Bash", "go test ./..."); a.Decision != "allow" || a.Rule != `allow Bash "go test *"` {
		t.Fatalf("allowed: %+v", a)
	}
	if a := ask("Bash", "go test ./... && rm -rf ~"); a.Decision != "deny" {
		t.Fatalf("the deny rule first: %+v", a)
	}
	if a := ask("Bash", "go test ./... | sh"); a.Decision != "" {
		t.Fatalf("a chained command was answered: %+v", a)
	}
	if a := ask("Edit", "a.go"); a.Decision != "" {
		t.Fatalf("a rule for another project answered: %+v", a)
	}
	// The answers are kept on the pane, for explain.
	ex := e.explainAll()
	if len(ex.Permissions) != 2 || ex.Permissions[0].Decision != "allow" || ex.Permissions[1].Decision != "deny" {
		t.Fatalf("records %+v", ex.Permissions)
	}
	raw, _ := json.Marshal(ex)
	if !strings.Contains(string(raw), `"permissions":[`) || !strings.Contains(string(raw), `"status":`) {
		t.Fatalf("explain JSON lost a half: %s", raw)
	}
	// At most the last twenty.
	for range 30 {
		ask("Bash", "go test ./...")
	}
	if n := len(e.explainAll().Permissions); n != maxPermissionRecords {
		t.Fatalf("%d records kept", n)
	}

	// Dry run: nothing answered, what would have been is recorded.
	withPermissionConfig(t, "dry-run", config.PermissionRule{Tool: "Bash", Action: "allow"})
	if a := ask("Bash", "ls"); a.Decision != "" || !a.DryRun || a.Rule == "" {
		t.Fatalf("dry run: %+v", a)
	}
	recs := e.explainAll().Permissions
	if last := recs[len(recs)-1]; !last.DryRun || last.Decision != "allow" {
		t.Fatalf("dry-run record %+v", last)
	}
	// Off: ignored.
	withPermissionConfig(t, "off", config.PermissionRule{Tool: "Bash", Action: "allow"})
	if a := ask("Bash", "ls"); a.Decision != "" || a.DryRun {
		t.Fatalf("off: %+v", a)
	}
	// A pane that isn't there.
	if _, perr := s.decidePermission(proto.PermissionAsk{ID: "nope", Tool: "Bash"}); perr == nil {
		t.Fatal("an unknown pane was answered")
	}
}

func TestHeldBack(t *testing.T) {
	s := &Server{}
	e := &entry{}
	now := time.Now()
	block := &detect.ProgramReport{State: "blocked", Kind: "permission", At: now.Add(-100 * time.Millisecond)}

	// No rules: shown at once, as it always was.
	withPermissionConfig(t, "")
	if got := s.heldBack(e, block, now); got != block {
		t.Fatal("held back with no rules")
	}
	withPermissionConfig(t, "", config.PermissionRule{Tool: "Bash", Action: "allow"})
	s.rulesAt = time.Time{} // read the rules again
	// Fresh: held for the grace, then shown — a real question still is.
	if got := s.heldBack(e, block, now); got != nil {
		t.Fatal("a fresh permission block was shown at once")
	}
	if got := s.heldBack(e, block, now.Add(permissionGrace)); got != block {
		t.Fatal("a real question was never shown")
	}
	// Answered by a rule around then: never shown.
	e.answered = now
	if got := s.heldBack(e, block, now.Add(time.Minute)); got != nil {
		t.Fatal("a block a rule answered was shown")
	}
	e.answered = block.At.Add(-time.Minute) // answered before this question began
	if got := s.heldBack(e, block, now.Add(time.Minute)); got != block {
		t.Fatal("an earlier answer hid a later question")
	}
	e.answered = block.At.Add(2 * time.Hour) // a block from long before any answer
	if got := s.heldBack(e, block, now.Add(3*time.Hour)); got != block {
		t.Fatal("a record hours older than the answer was hidden")
	}
	// Other reports pass straight through.
	for _, p := range []*detect.ProgramReport{nil, {State: "working", At: now}, {State: "blocked", Kind: "question", At: now}} {
		if got := s.heldBack(e, p, now); got != p {
			t.Fatalf("%+v was changed", p)
		}
	}
	// Dry run and off don't hold anything.
	for _, mode := range []string{"dry-run", "off"} {
		withPermissionConfig(t, mode, config.PermissionRule{Tool: "Bash", Action: "allow"})
		s.rulesAt = time.Time{}
		e.answered = time.Time{}
		if got := s.heldBack(e, block, now); got != block {
			t.Fatalf("%s held a block back", mode)
		}
	}
}

// The live case: the agent reports it is blocked on a permission at the
// moment a rule answers it. The pane must never show it waiting.
func TestRuleAnsweredPermissionNeverShowsWaiting(t *testing.T) {
	s, _, work := shareFixture(t)
	withPermissionConfig(t, "", config.PermissionRule{Tool: "Bash", Match: "go test*", Action: "allow"})
	e := agentPane(t, s, "p1", "claude", work,
		`stty -echo; exec awk '{ printf "\033]7501;state=%s\007", $0; fflush() }'`)
	t.Cleanup(func() { e.p.Close() })
	e.p.SendText("working\r", false)
	a5WaitFor(t, "working", func() bool { s.observe(e); a := e.info().Agent; return a != nil && a.State == proto.AgentWorking })
	for i := range 20 {
		e.p.SendText("blocked:kind=permission\r", false)
		if ans, _ := s.decidePermission(proto.PermissionAsk{ID: "p1", Agent: "claude", Tool: "Bash", Subject: "go test ./..."}); ans.Decision != "allow" {
			t.Fatalf("not allowed: %+v", ans)
		}
		deadline := time.Now().Add(permissionGrace + 500*time.Millisecond)
		for time.Now().Before(deadline) {
			s.observe(e)
			if a := e.info().Agent; a != nil && a.State == proto.AgentBlocked {
				rec, _ := e.p.ProgramStatus()
				t.Fatalf("round %d: showed waiting for a request a rule answered (%s); report at %v, answered %v",
					i, a.Reason, rec.At.Format("15:04:05.000"), e.answered.Format("15:04:05.000"))
			}
			time.Sleep(5 * time.Millisecond)
		}
		e.p.SendText("working\r", false)
	}
}

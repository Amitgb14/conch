package server

import (
	"log"
	"time"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/detect"
	"github.com/Amitgb14/conch/internal/proto"
)

// An agent asking permission for the tenth `go test` of the hour is asking
// a question its person already answered. [[permissions]] rules answer it
// for them: the agent's hook (conch report) asks here before the agent
// shows its prompt, and an answer goes back through the hook — the agent's
// own channel for it — never as keys typed onto its screen. Every answer is
// kept on the pane, and agent.explain shows them, so what was allowed
// unseen can be read afterwards.

// maxPermissionRecords is how many answers a pane keeps.
const maxPermissionRecords = 20

// loadPermissionConfig reads the rules; a variable so tests can give some.
var loadPermissionConfig = config.Load

func (s *Server) decidePermission(ask proto.PermissionAsk) (proto.PermissionAnswer, *proto.Error) {
	e, perr := s.get(ask.ID)
	if perr != nil {
		return proto.PermissionAnswer{}, perr
	}
	cfg, err := loadPermissionConfig()
	if err != nil {
		log.Printf("permissions: %v", err)
		return proto.PermissionAnswer{}, nil // no rules readable: ask as usual
	}
	mode := cfg.Agents.PermissionModeOrDefault()
	if mode == config.PermissionsOff || len(cfg.Permissions) == 0 {
		return proto.PermissionAnswer{}, nil
	}
	project, root := "", ""
	e.mu.Lock()
	if p := e.project; p != nil {
		root = p.root
	}
	e.mu.Unlock()
	if root != "" {
		if p := s.projects.containing(root); p != nil {
			project = p.snapshot().Name
		}
	}
	for _, r := range cfg.Permissions {
		if !r.Matches(ask.Agent, ask.Tool, ask.Subject, project, root) {
			continue
		}
		ans := proto.PermissionAnswer{Decision: r.Action, Rule: r.String()}
		if mode == config.PermissionsDryRun {
			ans = proto.PermissionAnswer{Rule: r.String(), DryRun: true}
		}
		rec := proto.PermissionRecord{At: time.Now(), Tool: ask.Tool, Subject: ask.Subject, Decision: r.Action, Rule: r.String(), DryRun: ans.DryRun}
		e.mu.Lock()
		if !ans.DryRun {
			e.answered = rec.At
		}
		e.permissions = append(e.permissions, rec)
		if len(e.permissions) > maxPermissionRecords {
			e.permissions = e.permissions[len(e.permissions)-maxPermissionRecords:]
		}
		e.mu.Unlock()
		verb := map[string]string{"allow": "allowed", "deny": "denied"}[r.Action]
		if ans.DryRun {
			verb = "would have " + verb
		}
		log.Printf("pane %s: %s %s %q by rule %s", ask.ID, verb, ask.Tool, ask.Subject, r.String())
		return ans, nil
	}
	return proto.PermissionAnswer{}, nil
}

// explained is agent.explain's answer: what detection made of the pane, and
// what the permission rules answered there.
type explained struct {
	detect.Explanation
	Permissions []proto.PermissionRecord `json:"permissions,omitempty"`
}

func (e *entry) explainAll() explained {
	ex := explained{Explanation: e.explain()}
	e.mu.Lock()
	ex.Permissions = append([]proto.PermissionRecord(nil), e.permissions...)
	e.mu.Unlock()
	return ex
}

// permissionGrace is how long a program's own "blocked on a permission"
// is held back while rules are in use. The agent reports it as it asks,
// at the very moment its hook is being answered — so without the grace a
// request a rule answers in milliseconds still flashed "waiting", rang
// alerts and ended a `conch agent prompt -wait`, which is the false alarm
// that teaches people to ignore the real ones. Seen with Claude Code
// 2.1.299 (R55). A real question waits the grace longer to show.
const permissionGrace = 1500 * time.Millisecond

// heldBack is the program report detection should see: a permission block
// is dropped when a rule answered it, and held back for its grace while a
// rule still might.
func (s *Server) heldBack(e *entry, prog *detect.ProgramReport, now time.Time) *detect.ProgramReport {
	if prog == nil || prog.State != "blocked" || prog.Kind != "permission" || !s.rulesActive(now) {
		return prog
	}
	// A block that began before a rule's latest answer — or just after it,
	// as the agent reports and its hook runs at once — is one the rules
	// answered: an agent asks one thing at a time, so it cannot still be
	// waiting on an older request once a newer one was answered.
	answered := e.answered // e.mu is held by evaluate
	if !answered.IsZero() && prog.At.Before(answered.Add(permissionGrace)) && answered.Sub(prog.At) < time.Hour {
		return nil // never the person's to see
	}
	if now.Sub(prog.At) < permissionGrace {
		return nil // a rule may still answer it
	}
	return prog
}

// rulesCheck is how often whether any rules are in use is read again.
const rulesCheck = 5 * time.Second

// rulesActive reports whether permission rules are in use, read from
// config.toml at most every rulesCheck.
func (s *Server) rulesActive(now time.Time) bool {
	s.rulesMu.Lock()
	defer s.rulesMu.Unlock()
	if now.Sub(s.rulesAt) < rulesCheck && !s.rulesAt.IsZero() {
		return s.rulesOn
	}
	s.rulesAt = now
	cfg, err := loadPermissionConfig()
	s.rulesOn = err == nil && len(cfg.Permissions) > 0 && cfg.Agents.PermissionModeOrDefault() == config.PermissionsOn
	return s.rulesOn
}

package server

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Amitgb14/conch/internal/accounts"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/BurntSushi/toml"
)

// Moving a running agent to another of its accounts (CapAgentMove). When
// its plan runs out mid-task, people used to /login inside the session with
// another subscription — which, with accounts, signs the account's folder
// in as someone else. Instead conch stops the agent, copies its
// conversation into the other account's folder, and resumes it there in a
// new pane that clients put where the old one was. By hand at any time the
// agent is not mid-turn; by itself when the agent hits its limit (on unless
// [agents] no_move_at_limit), after a few seconds anyone can cancel.
//
// Claude Code only for now: its conversation is one file under the
// account's projects/, and --resume takes its id. Codex keeps its own.

// moveJobWait is how long a move the server would make waits for the
// agent's background commands to finish before it is scheduled anyway,
// naming them.
var moveJobWait = 10 * time.Minute

// jobCommands is what an agent left running, as it was started.
func jobCommands(info proto.PaneInfo) []string {
	if info.Agent == nil {
		return nil
	}
	var out []string
	for _, j := range info.Agent.Jobs {
		c := strings.TrimSpace(j.Command)
		if len(c) > 60 {
			c = c[:57] + "…"
		}
		out = append(out, c)
	}
	return out
}

// moveDelay is how long a scheduled move waits, for somebody to call it off,
// and moveTick how often agents are looked at for a limit: each server
// takes them as it starts (newMover), so a test changes them before.
var (
	moveDelay = 5 * time.Second
	moveTick  = 3 * time.Second
)

// mover holds the moves the server has scheduled, and the agents not to be
// moved again until they have worked (a move called off, or nowhere to go).
type mover struct {
	tick, delay time.Duration

	mu      sync.Mutex
	pending map[string]proto.AgentMoving
	held    map[string]bool
	// turns is each agent's count of turns at the last look, so a move at
	// the failover line follows a turn just finished and never an agent
	// left idle.
	turns map[string]int
	// jobWait, and when each agent needing a move was first found with
	// background commands running.
	jobWait  time.Duration
	waitFrom map[string]time.Time
}

// finishedTurn says whether the agent has finished a turn since the last
// look, counting turns now; the first look at an agent says no, so one
// found idle (a server just started, a reload) is not moved for it.
func (m *mover) finishedTurn(pane string, turns int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	seen, ok := m.turns[pane]
	m.turns[pane] = turns
	return ok && turns > seen
}

func newMover() *mover {
	return &mover{tick: moveTick, delay: moveDelay, pending: map[string]proto.AgentMoving{}, held: map[string]bool{}, turns: map[string]int{},
		jobWait: moveJobWait, waitFrom: map[string]time.Time{}}
}

// rankAccounts is agent's accounts other than exclude ("" its own; "-"
// leaves none out), the one a move would pick first.
func (s *Server) rankAccounts(agent, exclude string) []proto.AccountRank {
	b := s.accountBook()
	home, _ := os.UserHomeDir()
	running := s.agentsByAccount()
	now := time.Now()
	type acc struct{ name, dir string }
	all := []acc{{"", ""}}
	for _, a := range b.Of(agent) {
		all = append(all, acc{a.Name, a.Dir})
	}
	var cands []accounts.Cand
	ranks := map[string]proto.AccountRank{}
	for i, a := range all {
		if a.name == exclude {
			continue
		}
		r := proto.AccountRank{Account: a.name, Room: 100, Agents: running[limitKey(agent, a.name)]}
		c := accounts.Cand{Name: a.name, Room: 100, Agents: r.Agents, Order: i}
		if l, ok := s.limitsOf(agent, a.name); ok {
			used, resets := fullestWindow(l, now)
			r.Room, r.Reported, r.ResetsAt = 100-used, true, resets
			c.Room, c.Reported = r.Room, true
		}
		if in, known := accounts.SignedIn(agent, a.dir, home, os.Getenv); known {
			r.SignedIn, c.Signed = &in, &in
		}
		r.Usable = c.Usable()
		r.Why = rankWhy(r, now)
		ranks[a.name] = r
		cands = append(cands, c)
	}
	var out []proto.AccountRank
	for _, c := range accounts.Rank(cands, b.LineFor(agent)) {
		out = append(out, ranks[c.Name])
	}
	return out
}

// rankWhy is what a rank says about an account, for a menu.
func rankWhy(r proto.AccountRank, now time.Time) string {
	switch {
	case r.SignedIn != nil && !*r.SignedIn:
		return "not signed in"
	case !r.Reported:
		return "nothing reported yet"
	case r.Room <= 0 && !r.ResetsAt.IsZero():
		return "full · resets in " + shortWait(r.ResetsAt.Sub(now))
	case r.Room <= 0:
		return "full"
	}
	return fmt.Sprintf("%d%% room", int(r.Room))
}

// shortWait is a wait as people say it: "2h 10m", "6m".
func shortWait(d time.Duration) string {
	if d < time.Minute {
		return "a minute"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
}

// fullestWindow is how much of an account's fullest live window is used,
// and when that window turns over.
func fullestWindow(l proto.PlanLimits, now time.Time) (float64, time.Time) {
	most, at := 0.0, time.Time{}
	live := func(pct float64, resets time.Time) {
		if (resets.IsZero() || now.Before(resets)) && pct >= most {
			most, at = pct, resets
		}
	}
	for _, w := range []*proto.LimitWindow{l.FiveHour, l.Week, l.Spend} {
		if w != nil {
			live(w.UsedPct, w.ResetsAt)
		}
	}
	for _, w := range l.Windows {
		live(w.UsedPct, w.ResetsAt)
	}
	return most, at
}

// agentsByAccount counts the running agents on each account.
func (s *Server) agentsByAccount() map[string]int {
	s.mu.Lock()
	entries := make([]*entry, 0, len(s.panes))
	for _, e := range s.panes {
		entries = append(entries, e)
	}
	s.mu.Unlock()
	out := map[string]int{}
	for _, e := range entries {
		info := e.info()
		agent := e.launched
		if info.Agent != nil {
			agent = info.Agent.Name
		}
		if info.State == proto.PaneRunning && agent != "" {
			out[limitKey(agent, info.Account)]++
		}
	}
	return out
}

// suggestAccount is account.suggest.
func (s *Server) suggestAccount(p proto.AccountSuggestParams) (proto.AccountSuggestion, *proto.Error) {
	if !accounts.Supports(p.Agent) {
		return proto.AccountSuggestion{}, proto.Errorf(proto.ErrBadRequest, "%s keeps one account", p.Agent)
	}
	exclude := "-"
	if p.Exclude != "" {
		exclude = accountName(p.Exclude)
	}
	return proto.AccountSuggestion{Agent: p.Agent, Accounts: s.rankAccounts(p.Agent, exclude)}, nil
}

// accountName is an account as the book names it: "" for the agent's own.
func accountName(name string) string {
	if strings.EqualFold(name, accounts.Default) {
		return ""
	}
	return name
}

// claudeRoot is the folder of a Claude account: dir, or for the agent's own
// (dir "") where Claude keeps it here.
func claudeRoot(dir string) string {
	if dir != "" {
		return dir
	}
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

// moveAgent is agent.move.
func (s *Server) moveAgent(p proto.AgentMoveParams) (proto.PaneInfo, *proto.Error) {
	e, perr := s.get(p.ID)
	if perr != nil {
		return proto.PaneInfo{}, perr
	}
	info := e.info()
	agent := e.launched
	if info.Agent != nil {
		agent = info.Agent.Name
	}
	if agent != "claude" {
		return proto.PaneInfo{}, proto.Errorf(proto.ErrBadRequest, "conch moves Claude Code agents between accounts; %s is not one", p.ID)
	}
	if info.Agent != nil && info.Agent.State == proto.AgentWorking {
		return proto.PaneInfo{}, proto.Errorf(proto.ErrBadRequest, "%s is working: move it when its turn ends", p.ID)
	}
	// What the agent left running is its process's children and stops with
	// it; the resumed agent has no way back to them.
	if jobs := jobCommands(info); len(jobs) > 0 && !p.Force {
		return proto.PaneInfo{}, proto.Errorf(proto.ErrBadRequest, "%s has %d background command(s) running, which would stop: %s (move it anyway with force)",
			p.ID, len(jobs), strings.Join(jobs, "; "))
	}
	from := e.accountName()
	to := ""
	if p.Account == "" {
		ranked := s.rankAccounts(agent, from)
		if len(ranked) == 0 || !ranked[0].Usable {
			return proto.PaneInfo{}, proto.Errorf(proto.ErrBadRequest, "no other %s account has room", agent)
		}
		to = ranked[0].Account
	} else {
		to = accountName(p.Account)
	}
	if to == from {
		return proto.PaneInfo{}, proto.Errorf(proto.ErrBadRequest, "%s is already on %s", p.ID, accounts.Label(to))
	}
	b := s.accountBook()
	toDir := ""
	if to != "" {
		a, ok := b.Get(agent, to)
		if !ok {
			return proto.PaneInfo{}, proto.Errorf(proto.ErrBadRequest, "no %s account called %s (conch account list)", agent, to)
		}
		toDir = a.Dir
	}
	home, _ := os.UserHomeDir()
	if in, known := accounts.SignedIn(agent, toDir, home, os.Getenv); known && !in {
		return proto.PaneInfo{}, proto.Errorf(proto.ErrBadRequest, "%s is not signed in: sign in to it first", accounts.Label(to))
	}
	fromDir := ""
	if from != "" {
		if a, ok := b.Get(agent, from); ok {
			fromDir = a.Dir
		}
	}
	ad, ok := s.adapters.Get(agent)
	if !ok {
		return proto.PaneInfo{}, proto.Errorf(proto.ErrBadRequest, "conch can't launch %q", agent)
	}

	e.evalMu.Lock() // the transcript is the reports', which hold evalMu
	tr := e.transcript
	e.evalMu.Unlock()
	e.mu.Lock()
	movedFrom, team, lineage := e.movedFrom, e.team, append([]string(nil), e.lineage...)
	e.mu.Unlock()
	if tr == nil {
		return proto.PaneInfo{}, proto.Errorf(proto.ErrBadRequest, "conch has not seen %s's conversation yet: try again after its next reply", p.ID)
	}
	id, err := copyConversation(tr.Path(), claudeRoot(fromDir), claudeRoot(toDir))
	if err != nil {
		return proto.PaneInfo{}, proto.Errorf(proto.ErrInternal, "copying the conversation: %v", err)
	}

	// The account it was first moved off is remembered across moves, so
	// "back" is where it started; moving there forgets it.
	origin := movedFrom
	if origin == "" {
		origin = accounts.Label(from)
	}
	if accountName(origin) == to {
		origin = ""
	}
	name := ""
	if info.CustomName {
		name = info.Name
	}
	s.mover.forget(p.ID)
	s.mover.release(p.ID)
	if cerr := s.close(p.ID); cerr != nil {
		return proto.PaneInfo{}, cerr
	}
	// Outside any project it stays outside: creating a pane in a folder
	// adds that folder's project, which brought back projects people had
	// removed while their agents ran on.
	noProject := info.ProjectID == ""
	nw, perr := s.create(proto.PaneCreateParams{Agent: agent, Account: accounts.Label(to), AgentArgs: ad.ResumeArgs(id),
		Cwd: e.dir, Name: name, Cols: info.Cols, Rows: info.Rows, NoProject: noProject})
	if perr != nil {
		// Back where it was, so the conversation is not left without a pane.
		if back, berr := s.create(proto.PaneCreateParams{Agent: agent, Account: accounts.Label(from), AgentArgs: ad.ResumeArgs(id),
			Cwd: e.dir, Name: name, Cols: info.Cols, Rows: info.Rows, NoProject: noProject}); berr == nil {
			log.Printf("accounts: moving %s to %s failed (%s); resumed on %s in %s", p.ID, accounts.Label(to), perr.Message, accounts.Label(from), back.ID)
		}
		return proto.PaneInfo{}, perr
	}
	if ne, gerr := s.get(nw.ID); gerr == nil {
		ne.mu.Lock()
		ne.movedFrom, ne.replaces, ne.team, ne.lineage = origin, p.ID, team, lineage
		ne.mu.Unlock()
		nw = ne.info()
	}
	reason := p.Reason
	if reason == "" {
		reason = "hand"
	}
	log.Printf("accounts: moved %s from %s to %s (%s), now %s", p.ID, accounts.Label(from), accounts.Label(to), reason, nw.ID)
	s.broadcast(proto.EventPaneUpdated, nw)
	s.broadcast(proto.EventAgentMoved, proto.AgentMoved{Pane: nw.ID, Replaces: p.ID, Agent: agent,
		From: accounts.Label(from), To: accounts.Label(to), Reason: reason})
	if p.Continue {
		goSafe("continue after a move", func() { s.continueWhenReady(nw.ID) })
	}
	return nw, nil
}

// continueWhenReady sends "continue" to a resumed agent once it is ready
// for a message, within a minute.
func (s *Server) continueWhenReady(id string) {
	deadline := time.Now().Add(time.Minute)
	for time.Now().Before(deadline) {
		select {
		case <-s.quit:
			return
		case <-time.After(time.Second):
		}
		e, perr := s.get(id)
		if perr != nil {
			return
		}
		s.observe(e)
		if a := e.info().Agent; a != nil && (a.State == proto.AgentIdle || a.State == proto.AgentDone) {
			if _, perr := s.promptAgent(proto.AgentPromptParams{ID: id, Text: "continue"}); perr != nil {
				log.Printf("accounts: %s moved, but continue was not sent: %s", id, perr.Message)
			}
			return
		}
	}
	log.Printf("accounts: %s moved, but was not ready for continue within a minute", id)
}

// copyConversation copies a Claude conversation file, and the folder of the
// same name beside it (its subagents' runs), from one account's folder to
// the same place in another's, and says its id. A copy already there and
// as new stays as it is.
func copyConversation(path, fromRoot, toRoot string) (string, error) {
	id := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	if id == filepath.Base(path) || id == "" {
		return "", fmt.Errorf("%s is not a conversation file", path)
	}
	rel, err := filepath.Rel(fromRoot, path)
	if err != nil || strings.HasPrefix(rel, "..") || !strings.HasPrefix(filepath.ToSlash(rel), "projects/") {
		// Not under the account it ran on: put it where that account
		// keeps a project's conversations, under its folder's name.
		rel = filepath.Join("projects", filepath.Base(filepath.Dir(path)), filepath.Base(path))
	}
	dst := filepath.Join(toRoot, rel)
	if err := copyIfNewer(path, dst); err != nil {
		return "", err
	}
	side := strings.TrimSuffix(path, ".jsonl")
	if st, err := os.Stat(side); err == nil && st.IsDir() {
		werr := filepath.WalkDir(side, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			r, rerr := filepath.Rel(side, p)
			if rerr != nil {
				return rerr
			}
			return copyIfNewer(p, filepath.Join(strings.TrimSuffix(dst, ".jsonl"), r))
		})
		if werr != nil {
			return "", werr
		}
	}
	return id, nil
}

// copyIfNewer copies src to dst, making its folder, unless dst is already
// as large and as new; written beside and renamed, so a reader never sees
// half a file.
func copyIfNewer(src, dst string) error {
	sst, err := os.Stat(src)
	if err != nil {
		return err
	}
	if dstat, err := os.Stat(dst); err == nil && dstat.Size() >= sst.Size() && !dstat.ModTime().Before(sst.ModTime()) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".conch-tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// atLimit says whether an agent stopped because its plan ran out: not
// working, and the end of its screen says so.
func atLimit(info proto.PaneInfo, screen []string) bool {
	if info.Agent == nil || info.Agent.State == proto.AgentWorking {
		return false
	}
	start := len(screen) - 20
	if start < 0 {
		start = 0
	}
	for _, l := range screen[start:] {
		low := strings.ToLower(l)
		if strings.Contains(low, "hit your limit") || strings.Contains(low, "usage limit reached") {
			return true
		}
	}
	return false
}

// moveConfig is whether agents move at their limit (on unless
// no_move_at_limit) and at the failover line (move_at_line), from the
// config.toml in the server's own folder — read every few seconds, so a
// test's server never reads the developer's.
func (s *Server) moveConfig() (atLimit, atLine bool) {
	var cfg config.Config
	if _, err := toml.DecodeFile(filepath.Join(s.configDir, "config.toml"), &cfg); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return false, false // unreadable: move nothing on a guess
		}
		return true, false
	}
	return !cfg.Agents.NoMoveAtLimit, cfg.Agents.MoveAtLine
}

// watchMoves looks at every agent for one its limit stopped (or, with
// move_at_line, one past its account's failover line that finished its
// turn), schedules its move, and makes the moves whose time has come.
func (s *Server) watchMoves() {
	t := time.NewTicker(s.mover.tick)
	defer t.Stop()
	for {
		select {
		case <-s.quit:
			return
		case <-t.C:
		}
		s.lookForMoves(time.Now())
	}
}

func (s *Server) lookForMoves(now time.Time) {
	byLimit, byLine := s.moveConfig()
	s.mu.Lock()
	entries := make([]*entry, 0, len(s.panes))
	for _, e := range s.panes {
		entries = append(entries, e)
	}
	s.mu.Unlock()
	for _, e := range entries {
		info := e.info()
		if info.Agent == nil || info.Agent.Name != "claude" || info.State != proto.PaneRunning {
			s.mover.drop(info.ID)
			continue
		}
		if info.Agent.State == proto.AgentWorking {
			// Working again: a move called off may be scheduled anew.
			s.mover.release(info.ID)
		}
		// At the failover line only an agent in use moves, as its turn ends:
		// one that finished a turn since the last look (or whose move is
		// already set). One sitting idle stays where it is.
		idle := info.Agent.State == proto.AgentIdle || info.Agent.State == proto.AgentDone
		finished := false
		if idle {
			e.mu.Lock()
			turns := e.turns
			e.mu.Unlock()
			finished = s.mover.finishedTurn(info.ID, turns)
		}
		pendingLine := false
		if m, ok := s.mover.get(info.ID); ok && m.Reason == "line" {
			pendingLine = true
		}
		reason := ""
		switch {
		case byLimit && atLimit(info, e.p.PlainLines()) && s.limitReached("claude", info.Account, now):
			reason = "limit"
		case byLine && idle && (finished || pendingLine) && s.pastLine("claude", info.Account, now):
			reason = "line"
		}
		if reason == "" {
			s.mover.waiting(info.ID, now, true)
			if m, ok := s.mover.get(info.ID); ok {
				s.mover.forget(info.ID)
				log.Printf("accounts: %s no longer needs moving off %s", info.ID, m.From)
			}
			continue
		}
		if s.mover.isHeld(info.ID) {
			continue
		}
		if m, ok := s.mover.get(info.ID); ok {
			if !now.Before(m.At) {
				s.mover.forget(info.ID)
				if _, perr := s.moveAgent(proto.AgentMoveParams{ID: info.ID, Account: m.To, Continue: m.Reason == "limit", Reason: m.Reason, Force: true}); perr != nil {
					log.Printf("accounts: moving %s off %s: %s", info.ID, m.From, perr.Message)
					s.mover.hold(info.ID)
				}
			}
			continue
		}
		// Background commands stop with the agent: wait for them, and only
		// past the wait schedule it, saying which.
		jobs := jobCommands(info)
		if len(jobs) > 0 {
			if waited := s.mover.waiting(info.ID, now, false); waited < s.mover.jobWait {
				if waited == 0 {
					log.Printf("accounts: %s would move (%s), and waits for its %d background command(s)", info.ID, reason, len(jobs))
				}
				continue
			}
		}
		s.mover.waiting(info.ID, now, true)
		ranked := s.rankAccounts("claude", info.Account)
		if len(ranked) == 0 || !ranked[0].Usable {
			s.mover.hold(info.ID) // nowhere to go: said once, not every tick
			log.Printf("accounts: %s is at its %s on %s, and no other account has room", info.ID, reason, accounts.Label(info.Account))
			continue
		}
		m := proto.AgentMoving{Pane: info.ID, Agent: "claude", From: accounts.Label(info.Account), To: accounts.Label(ranked[0].Account),
			At: now.Add(s.mover.delay), Reason: reason, Stops: jobs}
		s.mover.schedule(m)
		log.Printf("accounts: %s will move from %s to %s in %v (%s)", info.ID, m.From, m.To, s.mover.delay, reason)
		s.broadcast(proto.EventAgentMoving, m)
	}
}

// limitReached says whether the account's own plan report has a window
// full. The screen alone is not enough: an agent can print Claude Code's
// words itself — this one did, while writing the tests for them, and was
// moved off an account at 3% of its five hours.
func (s *Server) limitReached(agent, account string, now time.Time) bool {
	l, ok := s.limitsOf(agent, account)
	if !ok {
		return false
	}
	used, _ := fullestWindow(l, now)
	return used >= 100
}

// pastLine says whether an agent's account is past its failover line.
func (s *Server) pastLine(agent, account string, now time.Time) bool {
	line := s.accountBook().LineFor(agent)
	if line <= 0 {
		return false
	}
	l, ok := s.limitsOf(agent, account)
	if !ok {
		return false
	}
	used, _ := fullestWindow(l, now)
	return used >= float64(line)
}

// cancelMove is agent.move.cancel.
func (s *Server) cancelMove(p proto.AgentMoveCancelParams) *proto.Error {
	if _, perr := s.get(p.ID); perr != nil {
		return perr
	}
	m, had := s.mover.get(p.ID)
	s.mover.forget(p.ID)
	s.mover.hold(p.ID)
	if had {
		log.Printf("accounts: %s stays on %s (move called off)", p.ID, m.From)
	}
	return nil
}

func (m *mover) schedule(mv proto.AgentMoving) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pending[mv.Pane] = mv
}

func (m *mover) get(pane string) (proto.AgentMoving, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	mv, ok := m.pending[pane]
	return mv, ok
}

func (m *mover) forget(pane string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.pending, pane)
}

// drop forgets everything about a pane that is no longer a running Claude
// agent.
func (m *mover) drop(pane string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.pending, pane)
	delete(m.held, pane)
	delete(m.turns, pane)
	delete(m.waitFrom, pane)
}

// waiting says how long the agent has been waiting on its background
// commands for a move, starting the clock at the first ask; done stops it.
func (m *mover) waiting(pane string, now time.Time, done bool) time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	if done {
		delete(m.waitFrom, pane)
		return 0
	}
	from, ok := m.waitFrom[pane]
	if !ok {
		m.waitFrom[pane] = now
		return 0
	}
	return now.Sub(from)
}

func (m *mover) hold(pane string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.held[pane] = true
}

func (m *mover) release(pane string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.held, pane)
}

func (m *mover) isHeld(pane string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.held[pane]
}

package server

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Amitgb14/conch/internal/accounts"
	"github.com/Amitgb14/conch/internal/adapter"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/sessions"
)

// Switching a running agent's conversation to another agent, in the same
// pane (CapAgentSwitch, #352). It is the account move's way (accountmove.go)
// with a translation in the middle: never mid-turn, waiting for the turn
// to end and for background commands; then the conversation is written as
// a session of the target's own (sessions.ClaudeWriter, CodexWriter) —
// tried in a scratch home and read back first — the target started in the
// pane's place resuming it, and sent "continue". Where conch writes no
// sessions for the target, or the trial reads back wrong, the target gets
// the Markdown handoff for its first message instead, and the result says
// why. The source session is only read, so switching back always works.
//
// Unlike a move, the pane stays: the new program takes the same id
// (start, takeOver), so every client keeps it in its tab and row.

// switchesDir is where the records of what each switch wrote are kept,
// for agent.switch.undo.
func (s *Server) switchesDir() string { return filepath.Join(s.configDir, "switches") }

// pendingSwitch is a switch waiting for its moment.
type pendingSwitch struct {
	params proto.AgentSwitchParams
	from   string
	// jobsFrom is when the agent was first seen done with its turn but
	// with background commands running.
	jobsFrom time.Time
}

// switcher holds the switches waiting for a turn to end, or for
// background commands.
type switcher struct {
	tick, jobWait time.Duration

	mu      sync.Mutex
	pending map[string]*pendingSwitch
}

func newSwitcher() *switcher {
	return &switcher{tick: moveTick, jobWait: moveJobWait, pending: map[string]*pendingSwitch{}}
}

func (w *switcher) set(p proto.AgentSwitchParams, from string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending[p.ID] = &pendingSwitch{params: p, from: from}
}

// restore takes back a switch that was waiting when the server reloaded.
func (w *switcher) restore(p proto.AgentSwitchParams) { w.set(p, "") }

func (w *switcher) get(id string) (pendingSwitch, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	sw, ok := w.pending[id]
	if !ok {
		return pendingSwitch{}, false
	}
	return *sw, true
}

func (w *switcher) forget(id string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, ok := w.pending[id]
	delete(w.pending, id)
	return ok
}

// waitedForJobs says how long the switch has waited on background
// commands, starting the clock at the first ask.
func (w *switcher) waitedForJobs(id string, now time.Time) time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	sw, ok := w.pending[id]
	if !ok {
		return 0
	}
	if sw.jobsFrom.IsZero() {
		sw.jobsFrom = now
	}
	return now.Sub(sw.jobsFrom)
}

func (w *switcher) all() []pendingSwitch {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]pendingSwitch, 0, len(w.pending))
	for _, sw := range w.pending {
		out = append(out, *sw)
	}
	return out
}

// paneAgent is the agent running in a pane, or the one it was started to
// run before it is seen.
func paneAgent(e *entry, info proto.PaneInfo) string {
	if info.Agent != nil && info.Agent.Name != "" {
		return info.Agent.Name
	}
	return e.launched
}

// midTurn is why the agent cannot be switched now, or "".
func midTurn(info proto.PaneInfo) string {
	if info.Agent == nil {
		return ""
	}
	switch info.Agent.State {
	case proto.AgentWorking:
		return "it is working"
	case proto.AgentBlocked:
		return "it is waiting on a question: answer it first"
	}
	return ""
}

// switchPlan is what a switch would carry, read from the session now.
type switchPlan struct {
	session  sessions.Session
	conv     sessions.Conversation // whole, for the handoff
	fit      sessions.Conversation // fitted to the target's context
	crossing sessions.Crossing
	native   bool
	why      string // why not native
}

// accountEnv is env with the variables that start agent on account.
func (s *Server) accountEnv(env sessions.Env, agent, account string) sessions.Env {
	ch := accounts.Choice{Account: account}
	if account != "" {
		a, ok := s.accountBook().Get(agent, account)
		if !ok {
			return env
		}
		ch.Dir, ch.KeyEnv = a.Dir, a.KeyEnv
	}
	over := map[string]string{}
	for _, kv := range ch.Env(agent) {
		k, v, _ := strings.Cut(kv, "=")
		over[k] = v
	}
	if len(over) == 0 {
		return env
	}
	base := env.Getenv
	env.Getenv = func(k string) string {
		if v, ok := over[k]; ok {
			return v
		}
		if base == nil {
			return ""
		}
		return base(k)
	}
	return env
}

// paneSession is the saved session of the agent running in a pane: the
// one its hooks named, found in the account it runs on.
func (s *Server) paneSession(e *entry, info proto.PaneInfo, agent string) (sessions.Session, *proto.Error) {
	id := ""
	if info.Agent != nil {
		id = info.Agent.SessionID
	}
	env := s.accountEnv(s.sessionEnv(), agent, e.accountName())
	e.evalMu.Lock()
	tr := e.transcript
	e.evalMu.Unlock()
	switch {
	case agent == "claude" && tr != nil:
		sid := strings.TrimSuffix(filepath.Base(tr.Path()), ".jsonl")
		return sessions.Session{Agent: agent, ID: sid, Path: tr.Path(), Dir: e.dir, Branch: info.Branch, Title: info.Title}, nil
	case agent == "codex" && id != "":
		if path := sessions.CodexRollout(env, id); path != "" {
			return sessions.Session{Agent: agent, ID: id, Path: path, Dir: e.dir, Branch: info.Branch, Title: info.Title}, nil
		}
	}
	if id != "" {
		for _, f := range sessions.List(env, []string{e.dir}, 0) {
			if f.Agent == agent && f.ID == id {
				return f, nil
			}
		}
	}
	return sessions.Session{}, proto.Errorf(proto.ErrBadRequest,
		"conch has not seen %s's conversation in %s yet: try again after its next reply", sessions.AgentName(agent), info.ID)
}

// planSwitch reads the pane's conversation and says how it would cross.
func (s *Server) planSwitch(e *entry, info proto.PaneInfo, from string, p proto.AgentSwitchParams) (switchPlan, *proto.Error) {
	var pl switchPlan
	sess, perr := s.paneSession(e, info, from)
	if perr != nil {
		return pl, perr
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conv, err := sessions.ReadConversation(ctx, s.sessionEnv(), sess)
	if err != nil {
		return pl, proto.Errorf(proto.ErrBadRequest, "reading %s's conversation: %v", sessions.AgentName(from), err)
	}
	if !conv.HasMessages() {
		return pl, proto.Errorf(proto.ErrBadRequest, "nothing has been said in %s yet: there is no conversation to switch", info.ID)
	}
	if conv.Session.Title == "" {
		conv.Session.Title = info.Title
	}
	pl.session, pl.conv = sess, conv
	pl.fit, pl.crossing = sessions.Measure(conv, sessions.SwitchBudget)
	switch {
	case p.Handoff:
		pl.why = "a handoff was asked for"
	case sessions.WriterFor(p.To, sessions.Env{}) == nil:
		pl.why = fmt.Sprintf("conch does not write %s's sessions", sessions.AgentName(p.To))
	default:
		pl.native = true
	}
	return pl, nil
}

// switchTo is how a conversation would be switched to ad: as a session of
// its own, as the handoff document, or not at all — what planSwitch and
// switchAgent decide, said ahead for a client's menu.
func switchTo(ad adapter.Adapter) string {
	switch {
	case sessions.WriterFor(ad.Name(), sessions.Env{}) != nil:
		return proto.SwitchNative
	case !canPrompt(ad):
		return proto.SwitchNever
	}
	return proto.SwitchHandoff
}

func canPrompt(ad adapter.Adapter) bool {
	cp, ok := ad.(interface{ CanPrompt() bool })
	return !ok || cp.CanPrompt()
}

func crossingOf(c sessions.Crossing) *proto.SwitchCrossing {
	return &proto.SwitchCrossing{Messages: c.Messages, Commands: c.Commands, Notes: c.Notes, Shortened: c.Shortened,
		Fits: c.Fits, Bytes: c.Bytes, Plan: c.Plan, Left: c.Left}
}

// switchAgent is agent.switch.
func (s *Server) switchAgent(p proto.AgentSwitchParams) (proto.AgentSwitchResult, *proto.Error) {
	e, perr := s.get(p.ID)
	if perr != nil {
		return proto.AgentSwitchResult{}, perr
	}
	s.observe(e)
	info := e.info()
	from := paneAgent(e, info)
	res := proto.AgentSwitchResult{Pane: info, From: from, To: p.To}
	switch {
	case info.State != proto.PaneRunning:
		return res, proto.Errorf(proto.ErrBadRequest, "%s has exited: resume its session instead", p.ID)
	case from == "":
		return res, proto.Errorf(proto.ErrBadRequest, "%s is not running an agent", p.ID)
	case p.To == "":
		return res, proto.Errorf(proto.ErrBadRequest, "agent.switch needs the agent to switch to")
	case p.To == from:
		return res, proto.Errorf(proto.ErrBadRequest, "%s is running %s already", p.ID, sessions.AgentName(from))
	}
	ad, ok := s.adapters.Get(p.To)
	if !ok {
		return res, proto.Errorf(proto.ErrBadRequest, "conch can't launch %q", p.To)
	}
	if perr := s.notInstalled(ad); perr != nil {
		return res, perr
	}
	pl, perr := s.planSwitch(e, info, from, p)
	if perr != nil {
		return res, perr
	}
	res.FromSession, res.Crossing = pl.session.ID, crossingOf(pl.crossing)
	res.Crossed, res.Why = proto.SwitchNative, pl.why
	if !pl.native {
		res.Crossed = proto.SwitchHandoff
		if cp, ok := ad.(interface{ CanPrompt() bool }); ok && !cp.CanPrompt() {
			// The handoff is a first message, which this agent cannot be
			// given: refused rather than started with nothing.
			return res, proto.Errorf(proto.ErrBadRequest,
				"%s cannot be given a first message, so a conversation cannot be handed to it", ad.Label())
		}
	}
	if p.Preview {
		res.Preview = true
		return res, nil
	}
	// A switch asked for by hand takes the place of one offered at a plan
	// limit, so the offer's countdown cannot make a second.
	if p.Reason != "limit" {
		if _, offered := s.mover.takeOffer(p.ID); offered {
			s.mover.hold(p.ID)
		}
	}
	var waits []string
	if why := midTurn(info); why != "" {
		if p.Now {
			return res, proto.Errorf(proto.ErrBadRequest, "%s is mid-turn (%s): switch it when its turn ends, or leave out -now to wait for that", p.ID, why)
		}
		waits = append(waits, "the turn to end")
	}
	if jobs := jobCommands(info); len(jobs) > 0 && !p.Now {
		waits = append(waits, fmt.Sprintf("%d background command(s), up to %v: %s", len(jobs), s.switcher.jobWait, strings.Join(jobs, "; ")))
	}
	if len(waits) > 0 {
		s.switcher.set(p, from)
		log.Printf("switch: %s will switch from %s to %s once it has %s", p.ID, from, p.To, strings.Join(waits, " and "))
		s.broadcast(proto.EventAgentSwitching, proto.AgentSwitching{Pane: p.ID, From: from, To: p.To, Waits: waits})
		res.Pending, res.Waits = true, waits
		return res, nil
	}
	s.switcher.forget(p.ID)
	return s.makeSwitch(e, p)
}

// makeSwitch switches the pane now: read, written, restarted.
func (s *Server) makeSwitch(e *entry, p proto.AgentSwitchParams) (proto.AgentSwitchResult, *proto.Error) {
	info := e.info()
	from := paneAgent(e, info)
	res := proto.AgentSwitchResult{Pane: info, From: from, To: p.To}
	if why := midTurn(info); why != "" {
		return res, proto.Errorf(proto.ErrBadRequest, "%s is mid-turn (%s)", p.ID, why)
	}
	ad, ok := s.adapters.Get(p.To)
	if !ok {
		return res, proto.Errorf(proto.ErrBadRequest, "conch can't launch %q", p.To)
	}
	pl, perr := s.planSwitch(e, info, from, p)
	if perr != nil {
		return res, perr
	}
	res.FromSession, res.Crossing, res.Why = pl.session.ID, crossingOf(pl.crossing), pl.why
	acct, perr := s.chooseAccount(p.To, "", e.dir)
	if perr != nil {
		return res, perr
	}

	var args, prompt string
	var record *sessions.SwitchRecord
	stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	stamp = strings.ReplaceAll(stamp, ".", "-")
	if pl.native {
		env := s.accountEnv(s.sessionEnv(), p.To, acct.Account)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		wr, via, err := sessions.SwitchWrite(ctx, pl.conv, pl.fit, p.To, env, e.dir)
		cancel()
		res.Via = via
		if err != nil {
			pl.native, res.Why = false, err.Error()
			log.Printf("switch: %s: writing %s's session: %v; handing the document over instead", p.ID, sessions.AgentName(p.To), err)
		} else {
			rec := sessions.Record(stamp, p.ID, pl.conv, wr)
			if err := rec.Save(s.switchesDir()); err != nil {
				log.Printf("switch: %s: keeping the record of what was written: %v", p.ID, err)
			} else {
				res.Record = stamp
			}
			record = &rec
			args, res.Session = ad.ResumeArgs(wr.ID), wr.ID
		}
	}
	if !pl.native {
		if cp, ok := ad.(interface{ CanPrompt() bool }); ok && !cp.CanPrompt() {
			return res, proto.Errorf(proto.ErrBadRequest,
				"%s cannot be given a first message, so a conversation cannot be handed to it (%s)", ad.Label(), res.Why)
		}
		path, err := writeHandoff(e.dir, pl.session, sessions.Handoff(pl.conv))
		if err != nil {
			return res, proto.Errorf(proto.ErrInternal, "write the conversation: %v", err)
		}
		res.Document = path
		prompt = handoffPrompt(from, "", filepath.Join(handoffDir, filepath.Base(path)))
	}
	res.Crossed = proto.SwitchNative
	if !pl.native {
		res.Crossed = proto.SwitchHandoff
	}

	// A name the pane was given stays; the agent's own name goes with it.
	name := ""
	if info.CustomName || info.Name != from {
		name = info.Name
	}
	origin := &proto.SwitchOrigin{Agent: from, Session: pl.session.ID, At: time.Now(), Crossing: res.Crossing}
	// Back to the agent it came from, by the round trip's own way: a
	// switch made after that one is a new trip, not a way back.
	if was := info.SwitchedFrom; was != nil && !was.Back && was.Agent == p.To {
		origin.Back = true
	}
	nw, perr := s.start(proto.PaneCreateParams{Agent: p.To, Account: accounts.Label(acct.Account), AgentArgs: args, Prompt: prompt,
		Cwd: e.dir, Name: name, Cols: info.Cols, Rows: info.Rows, NoProject: info.ProjectID == ""}, e,
		func(ne *entry) { ne.switchedFrom = origin })
	if perr != nil {
		// The old agent runs on, untouched; what was written for the new
		// one goes.
		if record != nil {
			_, _, _ = record.Undo()
			os.Remove(filepath.Join(s.switchesDir(), res.Record+".json"))
		}
		return res, perr
	}
	res.Pane = nw
	if p.Reason == "limit" {
		res.Reason = p.Reason
	}
	reason := p.Reason
	if reason == "" {
		reason = "hand"
	}
	log.Printf("switch: %s switched from %s to %s (%s, %s%s)", p.ID, from, p.To, reason, res.Crossed, map[bool]string{true: ": " + res.Why, false: ""}[res.Why != ""])
	s.broadcast(proto.EventAgentSwitched, res)
	if pl.native && !p.NoContinue {
		goSafe("continue after a switch", func() { s.continueWhenReady(p.ID) })
	}
	return res, nil
}

// takeOver puts ne in old's place: the same id, old's clients' views moved
// to it, and old's program ended.
func (s *Server) takeOver(old, ne *entry, token string) proto.PaneInfo {
	id := old.p.ID()
	old.mu.Lock()
	lineage, team := append([]string(nil), old.lineage...), old.team
	old.mu.Unlock()
	ne.mu.Lock()
	ne.lineage, ne.team = lineage, team
	ne.mu.Unlock()

	s.mu.Lock()
	clients := make([]*client, 0, len(s.clients))
	for c := range s.clients {
		clients = append(clients, c)
	}
	s.mu.Unlock()
	var watching []*client
	for _, c := range clients {
		c.mu.Lock()
		_, ok := c.subs[id]
		c.mu.Unlock()
		if ok {
			watching = append(watching, c)
			s.unsubscribe(c, id)
		}
	}
	s.mu.Lock()
	s.panes[id] = ne
	s.mu.Unlock()
	s.sshFiles.drop(id)
	s.tokens.dropPaneExcept(id, token, "")
	old.p.Close()
	for _, c := range watching {
		s.subscribe(c, ne)
	}
	info := ne.info()
	s.broadcast(proto.EventPaneUpdated, info)
	goSafe("pane watch "+id, func() { s.watch(ne) })
	return info
}

// cancelSwitch is agent.switch.cancel.
func (s *Server) cancelSwitch(p proto.AgentSwitchCancelParams) *proto.Error {
	if _, perr := s.get(p.ID); perr != nil {
		return perr
	}
	// An offer made at a plan limit is called off the same way, and the
	// agent is not offered one again until it has worked.
	if _, offered := s.mover.offerOf(p.ID); offered {
		s.withdrawOffer(p.ID, "called off")
		s.mover.hold(p.ID)
		s.switcher.forget(p.ID)
		return nil
	}
	if !s.switcher.forget(p.ID) {
		return proto.Errorf(proto.ErrBadRequest, "%s has no switch waiting", p.ID)
	}
	log.Printf("switch: %s stays as it is (switch called off)", p.ID)
	return nil
}

// undoSwitch is agent.switch.undo.
func (s *Server) undoSwitch(p proto.AgentSwitchUndoParams) (proto.AgentSwitchUndoResult, *proto.Error) {
	if p.Record == "" {
		return proto.AgentSwitchUndoResult{}, proto.Errorf(proto.ErrBadRequest, "agent.switch.undo needs the record a switch named")
	}
	rec, err := sessions.LoadSwitchRecord(s.switchesDir(), p.Record)
	if err != nil {
		if os.IsNotExist(err) {
			return proto.AgentSwitchUndoResult{}, proto.Errorf(proto.ErrNotFound, "no switch %s", p.Record)
		}
		return proto.AgentSwitchUndoResult{}, proto.Errorf(proto.ErrInternal, "%v", err)
	}
	removed, kept, err := rec.Undo()
	res := proto.AgentSwitchUndoResult{Removed: removed, Kept: kept}
	if err != nil {
		return res, proto.Errorf(proto.ErrInternal, "%v", err)
	}
	if len(kept) == 0 {
		os.Remove(filepath.Join(s.switchesDir(), p.Record+".json"))
	}
	log.Printf("switch: undid %s: %d file(s) removed, %d kept", p.Record, len(removed), len(kept))
	return res, nil
}

// watchSwitches makes the waiting switches whose moment has come.
func (s *Server) watchSwitches() {
	t := time.NewTicker(s.switcher.tick)
	defer t.Stop()
	for {
		select {
		case <-s.quit:
			return
		case <-t.C:
		}
		s.lookForSwitches(time.Now())
	}
}

func (s *Server) lookForSwitches(now time.Time) {
	for _, sw := range s.switcher.all() {
		p := sw.params
		e, perr := s.get(p.ID)
		if perr != nil {
			s.switcher.forget(p.ID)
			continue
		}
		s.observe(e)
		info := e.info()
		if info.State != proto.PaneRunning || paneAgent(e, info) == "" || paneAgent(e, info) == p.To {
			s.switcher.forget(p.ID)
			s.broadcast(proto.EventAgentSwitched, proto.AgentSwitchResult{Pane: info, From: sw.from, To: p.To,
				Error: "the agent is no longer running there"})
			continue
		}
		if midTurn(info) != "" {
			continue
		}
		if jobs := jobCommands(info); len(jobs) > 0 && s.switcher.waitedForJobs(p.ID, now) < s.switcher.jobWait {
			continue
		}
		s.switcher.forget(p.ID)
		p.Now = true // waited enough: what still runs stops with it
		if _, perr := s.makeSwitch(e, p); perr != nil {
			log.Printf("switch: %s to %s: %s", p.ID, p.To, perr.Message)
			s.broadcast(proto.EventAgentSwitched, proto.AgentSwitchResult{Pane: info, From: paneAgent(e, info), To: p.To, Error: perr.Message})
		}
	}
}

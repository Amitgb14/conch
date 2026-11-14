package server

import (
	"log"
	"path/filepath"
	"time"

	"github.com/Amitgb14/conch/internal/accounts"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/sessions"
)

// An agent's accounts (internal/accounts): the book is read at each launch
// rather than held, so a change made by hand takes effect at the next one,
// and written under accountsMu so two changes at once both land.

func (s *Server) accountsFile() string { return accounts.File(s.configDir) }

// accountBook is the book as the file has it now. One that cannot be read
// is said in the log and treated as empty, so a broken file costs the
// accounts and not every launch.
func (s *Server) accountBook() accounts.Book {
	b, err := accounts.Load(s.accountsFile())
	if err != nil {
		log.Printf("accounts: %v", err)
	}
	return b
}

// changeAccounts loads the book, lets change alter it and saves it.
func (s *Server) changeAccounts(change func(*accounts.Book) error) *proto.Error {
	s.accountsMu.Lock()
	defer s.accountsMu.Unlock()
	b, err := accounts.Load(s.accountsFile())
	if err != nil {
		return proto.Errorf(proto.ErrInternal, "%v", err)
	}
	if err := change(&b); err != nil {
		return proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	if err := b.Save(s.accountsFile()); err != nil {
		return proto.Errorf(proto.ErrInternal, "%v", err)
	}
	return nil
}

// accountName is the account e's agent was started on.
func (e *entry) accountName() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.account
}

// limitKey is where an account's limits are kept.
func limitKey(agent, account string) string { return agent + "\x00" + account }

// limitsOf is what agent's account last reported, if anything.
func (s *Server) limitsOf(agent, account string) (proto.PlanLimits, bool) {
	s.limitsMu.Lock()
	defer s.limitsMu.Unlock()
	l, ok := s.limits[limitKey(agent, account)]
	return l, ok
}

// usedPct is the fullest of an account's plan windows that has not yet
// turned over, in percent: what failover compares with its line. An
// account that has reported nothing is taken as having room.
func (s *Server) usedPct(agent, account string) float64 {
	l, ok := s.limitsOf(agent, account)
	if !ok {
		return 0
	}
	return planUsed(l, time.Now())
}

func planUsed(l proto.PlanLimits, now time.Time) float64 {
	most := 0.0
	live := func(pct float64, resets time.Time) {
		if (resets.IsZero() || now.Before(resets)) && pct > most {
			most = pct
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
	return most
}

// chooseAccount is the account an agent started in dir runs on.
func (s *Server) chooseAccount(agent, asked, dir string) (accounts.Choice, *proto.Error) {
	if agent == "" {
		if asked != "" {
			return accounts.Choice{}, proto.Errorf(proto.ErrBadRequest, "an account is an agent's: name the agent to start on %s", asked)
		}
		return accounts.Choice{}, nil
	}
	ch, err := s.accountBook().Choose(agent, asked, dir, s.usedPct)
	if err != nil {
		return ch, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	if ch.PassedOver != "" {
		log.Printf("accounts: %s's %s account is past its failover line; starting on %s", agent, ch.PassedOver, accounts.Label(ch.Account))
	}
	return ch, nil
}

// listAccounts is account.list: every agent's own account and the ones
// added, each with its limits and how many agents run on it.
func (s *Server) listAccounts() proto.AccountList {
	b := s.accountBook()
	running := map[string]int{}
	s.mu.Lock()
	entries := make([]*entry, 0, len(s.panes))
	for _, e := range s.panes {
		entries = append(entries, e)
	}
	s.mu.Unlock()
	for _, e := range entries {
		// An agent started and not yet seen counts, as it does for a
		// queue: it is about to spend the account.
		info := e.info()
		agent := e.launched
		if info.Agent != nil {
			agent = info.Agent.Name
		}
		if info.State == proto.PaneRunning && agent != "" {
			running[limitKey(agent, info.Account)]++
		}
	}
	out := proto.AccountList{Accounts: []proto.AccountInfo{}, FailoverAt: b.FailoverAt, Agents: accounts.Agents()}
	add := func(a proto.AccountInfo) {
		if l, ok := s.limitsOf(a.Agent, a.Name); ok {
			a.Limits = &l
		}
		a.Agents = running[limitKey(a.Agent, a.Name)]
		out.Accounts = append(out.Accounts, a)
	}
	for _, agent := range accounts.Agents() {
		add(proto.AccountInfo{Agent: agent, Default: true})
		for _, a := range b.Of(agent) {
			add(proto.AccountInfo{Agent: a.Agent, Name: a.Name, Dir: a.Dir})
		}
	}
	for _, p := range b.Pins {
		out.Pins = append(out.Pins, proto.AccountPin(p))
	}
	return out
}

func (s *Server) addAccount(p proto.AccountAddParams) (proto.AccountInfo, *proto.Error) {
	var a accounts.Account
	perr := s.changeAccounts(func(b *accounts.Book) error {
		var err error
		a, err = b.Add(p.Agent, p.Name, p.Dir, s.configDir)
		return err
	})
	if perr != nil {
		return proto.AccountInfo{}, perr
	}
	log.Printf("accounts: added %s account %s in %s", a.Agent, a.Name, a.Dir)
	return proto.AccountInfo{Agent: a.Agent, Name: a.Name, Dir: a.Dir}, nil
}

func (s *Server) removeAccount(r proto.AccountRef) *proto.Error {
	return s.changeAccounts(func(b *accounts.Book) error { return b.Remove(r.Agent, r.Name) })
}

func (s *Server) pinAccount(p proto.AccountPinParams) *proto.Error {
	path := p.Path
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real // panes are placed by the resolved folder
	}
	return s.changeAccounts(func(b *accounts.Book) error { return b.Pin(path, p.Account) })
}

func (s *Server) setFailover(p proto.AccountFailoverParams) *proto.Error {
	if p.At < 0 || p.At > 100 {
		return proto.Errorf(proto.ErrBadRequest, "failover is a percentage, 0 (off) to 100: %d", p.At)
	}
	return s.changeAccounts(func(b *accounts.Book) error { b.FailoverAt = p.At; return nil })
}

// sessionEnv is where agents keep their conversations here: this
// process's environment and every account's folder.
func (s *Server) sessionEnv() sessions.Env {
	e := sessions.CurrentEnv()
	for _, a := range s.accountBook().Accounts {
		e.Accounts = append(e.Accounts, sessions.Account{Agent: a.Agent, Name: a.Name, Dir: a.Dir})
	}
	return e
}

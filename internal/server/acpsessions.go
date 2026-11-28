package server

import (
	"context"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Amitgb14/conch/internal/acp"
	"github.com/Amitgb14/conch/internal/adapter"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

// An agent run over the Agent Client Protocol keeps its sessions where
// only it knows, and says them through session/list — when it offers
// that at all. Listing one means starting it, initializing and asking,
// which takes a node agent a second or two, so it is done as the slow
// stores in internal/sessions are: in the background, a list waiting a
// grace for it and otherwise served the last answer and marked partial.
//
// Whether an agent lists is its own answer to initialize, kept here from
// the last time it was asked: loadSession alone reopens an id somebody
// already has and lists nothing, so an agent that offers it without
// session/list is said to have sessions conch does not read.

// acpCapsFor is how long an agent's answer to initialize is trusted.
const acpCapsFor = 10 * time.Minute

type acpAgents struct {
	mu    sync.Mutex
	caps  map[string]acpCaps
	lists map[string]*acpList
	// probing is the asks under way, closed when each lands, so lists
	// asked for together start the agent once; failed is when an agent
	// last could not be asked, so one that never answers is not started
	// again — and waited on — at every list.
	probing map[string]chan struct{}
	failed  map[string]time.Time
}

// acpFailFor is how long an agent that could not be asked is left alone.
const acpFailFor = 5 * time.Minute

type acpCaps struct {
	init acp.InitializeResult
	at   time.Time
}

type acpList struct {
	val  []proto.SessionInfo
	at   time.Time     // when val was answered
	done chan struct{} // closed when the running ask lands; nil when idle
}

// acpListFresh is how long an answer stands before the agent is started
// again to ask: the Sessions view asks every few seconds while open.
const acpListFresh = 30 * time.Second

func (a *acpAgents) known(name string) (acp.InitializeResult, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	c, ok := a.caps[name]
	if !ok || time.Since(c.at) > acpCapsFor {
		return acp.InitializeResult{}, false
	}
	return c.init, true
}

// acpKey names an ACP agent by everything that decides what it answers —
// its name, command, folders and environment — so a look that began
// before it was changed cannot leave the old answer standing for the new.
func acpKey(ad adapter.ACPAgent) string {
	m := ad.Spec()
	return strings.Join(append(append([]string{m.Name, m.Command}, m.Dirs...), m.Env...), "\x01")
}

// forget drops what name said and listed: its manifest changed, and the
// command may be another program now.
func (a *acpAgents) forget(name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for k := range a.caps {
		if strings.HasPrefix(k, name+"\x01") {
			delete(a.caps, k)
		}
	}
	for k := range a.lists {
		if strings.HasPrefix(k, name+"\x01") {
			delete(a.lists, k)
		}
	}
	for k := range a.failed {
		if strings.HasPrefix(k, name+"\x01") {
			delete(a.failed, k)
		}
	}
}

func (a *acpAgents) note(name string, init acp.InitializeResult) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.caps == nil {
		a.caps = map[string]acpCaps{}
	}
	a.caps[name] = acpCaps{init: init, at: time.Now()}
}

// acpCapabilities asks an installed ACP agent what it can do, or says
// what it answered lately. ok is false when it could not be asked.
func (s *Server) acpCapabilities(ad adapter.ACPAgent) (acp.InitializeResult, bool) {
	return s.acpCapabilitiesWithin(ad, adapter.ProbeTimeout()+5*time.Second)
}

// acpCapabilitiesWithin is acpCapabilities waiting wait at most: the ask
// goes on past it, and what it learns — or that it failed — is kept for
// the next look. agent.status waits a moment only, since an agent that
// never answers (or one taking 14 s to load, as claude-agent-acp can) once
// held every agent list for the probe's whole minute.
func (s *Server) acpCapabilitiesWithin(ad adapter.ACPAgent, wait time.Duration) (acp.InitializeResult, bool) {
	key := acpKey(ad)
	if init, ok := s.acp.known(key); ok {
		return init, true
	}
	a := &s.acp
	a.mu.Lock()
	if at, ok := a.failed[key]; ok && time.Since(at) < acpFailFor {
		a.mu.Unlock()
		return acp.InitializeResult{}, false
	}
	done, running := a.probing[key]
	if !running {
		if a.probing == nil {
			a.probing, a.failed = map[string]chan struct{}{}, map[string]time.Time{}
		}
		done = make(chan struct{})
		a.probing[key] = done
		goSafe("acp capabilities", func() {
			home, _ := os.UserHomeDir()
			init, _, err := ad.Probe(context.Background(), config.DefaultShell(), home, false)
			a.mu.Lock()
			if err != nil {
				a.failed[key] = time.Now()
			} else {
				delete(a.failed, key)
			}
			delete(a.probing, key)
			a.mu.Unlock()
			if err == nil {
				a.note(key, init)
			}
			close(done)
		})
	}
	a.mu.Unlock()
	select {
	case <-done:
		return a.known(key)
	case <-time.After(wait):
		return acp.InitializeResult{}, false
	}
}

// acpGrace is how long a list waits for an ACP agent to answer; the
// sessions package's CONCH_SESSION_GRACE shortens it for tests.
func acpGrace() time.Duration {
	if v := os.Getenv("CONCH_SESSION_GRACE"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 1500 * time.Millisecond
}

// acpSessions is every ACP agent's sessions in dirs, and whether each
// answered in time.
func (s *Server) acpSessions(dirs []string) ([]proto.SessionInfo, bool) {
	var out []proto.SessionInfo
	complete := true
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, ad := range s.registry() {
		aa, ok := adapter.AsACP(ad)
		if !ok {
			continue
		}
		if init, known := s.acp.known(acpKey(aa)); known && !init.CanList() {
			continue // it said it does not list: nothing to ask
		}
		for _, dir := range dirs {
			wg.Add(1)
			goSafe("acp sessions", func() {
				defer wg.Done()
				found, done := s.acpList(aa, dir)
				mu.Lock()
				out = append(out, found...)
				complete = complete && done
				mu.Unlock()
			})
		}
	}
	wg.Wait()
	return out, complete
}

// acpList is one agent's sessions in one folder: a fresh answer if it
// comes within the grace, else the last one it gave.
func (s *Server) acpList(ad adapter.ACPAgent, dir string) ([]proto.SessionInfo, bool) {
	key := acpKey(ad) + "\x00" + dir
	s.acp.mu.Lock()
	if s.acp.lists == nil {
		s.acp.lists = map[string]*acpList{}
	}
	en := s.acp.lists[key]
	if en == nil {
		en = &acpList{}
		s.acp.lists[key] = en
	}
	if en.done == nil && !en.at.IsZero() && time.Since(en.at) < acpListFresh {
		val := append([]proto.SessionInfo(nil), en.val...)
		s.acp.mu.Unlock()
		return val, true
	}
	if en.done == nil {
		en.done = make(chan struct{})
		done := en.done
		goSafe("acp list", func() {
			val, ok := s.askACPSessions(ad, dir)
			s.acp.mu.Lock()
			if ok {
				en.val, en.at = val, time.Now()
			}
			en.done = nil
			s.acp.mu.Unlock()
			close(done)
		})
	}
	done := en.done
	s.acp.mu.Unlock()

	answered := true
	select {
	case <-done:
	case <-time.After(acpGrace()):
		answered = false
	}
	s.acp.mu.Lock()
	defer s.acp.mu.Unlock()
	return append([]proto.SessionInfo(nil), en.val...), answered
}

// askACPSessions starts the agent in dir and asks for its sessions there.
// ok is false when it could not be asked, which keeps the last answer
// rather than emptying the list.
func (s *Server) askACPSessions(ad adapter.ACPAgent, dir string) ([]proto.SessionInfo, bool) {
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return nil, true
	}
	init, list, err := ad.Probe(context.Background(), config.DefaultShell(), dir, true)
	if err != nil && init.ProtocolVersion == 0 && init.AgentInfo == nil {
		return nil, false
	}
	s.acp.note(acpKey(ad), init)
	if err != nil {
		return nil, false
	}
	out := make([]proto.SessionInfo, 0, len(list))
	for _, si := range list {
		if si.SessionID == "" || !adapter.SessionIDOK(si.SessionID) {
			continue
		}
		info := proto.SessionInfo{Agent: ad.Name(), ID: si.SessionID, Dir: si.Cwd, Title: si.Title}
		if info.Dir == "" {
			info.Dir = dir
		}
		if t, err := time.Parse(time.RFC3339Nano, si.UpdatedAt); err == nil {
			info.Updated = t
		}
		if info.Title == "" {
			info.Title = "(untitled)"
		}
		out = append(out, info)
	}
	return out, true
}

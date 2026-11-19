package server

import (
	"log"
	"runtime/debug"
	"sync"
	"time"
)

// A panic on any goroutine ends the whole process: every pane and every
// agent in it, for one bad request or one job of the server's own. So the
// server's goroutines run under safely: a panic is logged with its stack
// and ends that goroutine (or that request) alone.
//
// Not unconditionally. A panic between a Lock and its Unlock, with no
// defer, leaves the lock held, and a server carrying on past that hangs
// on the next caller instead — no reply, panes unreachable, and no crash
// to start a fresh server. So after a panic the server's own locks are
// tried (wedged); if one stays held, the panic goes on and ends the
// process as before. A lock of a pane's or a project's own is not looked
// at: that leaves at worst the one pane or project stuck, not the server.
//
// What no recover reaches: Go's fatal errors (a map written from two
// goroutines, out of memory, a stack overflow) end the process whatever is
// deferred. After those the next client starts a server again
// (client.EnsureServer) and the agents that were running are resumed
// (autoResume).

// safely runs fn, and reports whether it panicked: the panic logged, and
// raised again in a test (repanic) or when it left the server wedged.
func safely(what string, fn func()) (panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			recovered(what, r)
			panicked = true
		}
	}()
	fn()
	return false
}

// goSafe is go fn() under safely.
func goSafe(what string, fn func()) {
	go safely(what, fn)
}

// keepAgain is how long a loop of the server's own waits after a panic
// before it starts again: long enough that a panic on every pass is a log
// line every few seconds, not a spinning core.
var keepAgain = 5 * time.Second

// keep runs one of the server's long-lived loops on a goroutine of its
// own, and starts it again after a panic until the server stops: the
// autopilot, the scheduler and the rest would otherwise stop for good,
// silently, at the first bad project.
func (s *Server) keep(what string, run func()) {
	again := keepAgain
	go func() {
		for safely(what, run) {
			select {
			case <-s.quit:
				return
			case <-time.After(again):
				log.Printf("%s: starting again after its panic", what)
			}
		}
	}()
}

func recovered(what string, r any) {
	log.Printf("%s: panic: %v\n%s", what, r, debug.Stack())
	if repanic.Load() {
		panic(r)
	}
	if lock := wedged(); lock != "" {
		log.Printf("%s: the panic left %s held; ending the server rather than hanging", what, lock)
		panic(r)
	}
	tookPanic(what)
}

// tookPanic is told each panic taken, last thing: a test waits on it to
// know a goroutine's recover is over before it puts repanic back.
var tookPanic = func(string) {}

// lockWait is how long a lock may stay held after a panic before the
// server is taken to be wedged. Every one of them is held for moments.
var lockWait = 3 * time.Second

// wedged names a lock of a running server's own that stays held, or "".
func wedged() string {
	live.Lock()
	servers := make([]*Server, 0, len(live.servers))
	for s := range live.servers {
		servers = append(servers, s)
	}
	live.Unlock()
	for _, s := range servers {
		for name, mu := range s.vitalLocks() {
			if !lockable(mu, lockWait) {
				return name
			}
		}
	}
	return ""
}

func (s *Server) vitalLocks() map[string]*sync.Mutex {
	m := map[string]*sync.Mutex{
		"the server's lock": &s.mu, "the background lock": &s.bgMu, "the accounts lock": &s.accountsMu,
		"the limits lock": &s.limitsMu, "the rules lock": &s.rulesMu,
	}
	if s.projects != nil {
		m["the projects lock"] = &s.projects.mu
		m["the projects' background lock"] = &s.projects.bgMu
	}
	return m
}

func lockable(mu *sync.Mutex, wait time.Duration) bool {
	deadline := time.Now().Add(wait)
	for {
		if mu.TryLock() {
			mu.Unlock()
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// live is the servers running in this process — one, outside tests — whose
// locks wedged tries.
var live = struct {
	sync.Mutex
	servers map[*Server]struct{}
}{servers: map[*Server]struct{}{}}

func (s *Server) noteLive(on bool) {
	live.Lock()
	defer live.Unlock()
	if on {
		live.servers[s] = struct{}{}
	} else {
		delete(live.servers, s)
	}
}

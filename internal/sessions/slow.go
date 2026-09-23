package sessions

import (
	"strings"
	"sync"
	"time"
)

// Most agents keep their sessions in files this process reads directly, but
// Devin's are behind its CLI and OpenCode's behind a database program. Those
// take hundreds of milliseconds at best, and up to their own timeout when the
// program is slow, offline or waiting on a network call — and a list waits
// for the slowest store, so one of them held up every other agent's sessions
// for as long as ten seconds.
//
// Such a store now runs in the background: a list waits slowGrace for it and
// no longer. Past that it is served the last answer that store gave, or none
// at all, and says it is incomplete, so the caller can ask again in a moment
// rather than show that agent's sessions as absent. A program that answers
// promptly — the usual case — still has its answer read fresh every time.
const slowGrace = 1500 * time.Millisecond

// graceOf is slowGrace, or what CONCH_SESSION_GRACE says: a hook so tests
// need not wait out a whole grace. Nothing sets it in normal use.
func graceOf(e Env) time.Duration {
	if v := e.get("CONCH_SESSION_GRACE"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return slowGrace
}

type slowEntry struct {
	val  []Session     // the last answer the store gave
	done chan struct{} // closed when the running refresh lands; nil when idle
}

var (
	slowMu    sync.Mutex
	slowCache = map[string]*slowEntry{}
)

// slowKey names one store's answer for one set of directories.
func slowKey(name string, e Env, dirs []string) string {
	return name + "\x00" + e.Home + "\x00" + strings.Join(dirs, "\x00")
}

// slowList runs fn in the background and waits slowGrace for it. complete is
// false when it did not answer in time: list is then the answer it gave last,
// if it ever gave one.
func slowList(e Env, key string, fn func() []Session) (list []Session, complete bool) {
	slowMu.Lock()
	en := slowCache[key]
	if en == nil {
		en = &slowEntry{}
		slowCache[key] = en
	}
	if en.done == nil {
		en.done = make(chan struct{})
		go refreshSlow(en, fn)
	}
	done := en.done
	slowMu.Unlock()

	select {
	case <-done:
	case <-time.After(graceOf(e)):
		slowMu.Lock()
		val := en.val // the answer it gave last, or none yet
		slowMu.Unlock()
		return val, false
	}
	slowMu.Lock()
	val := en.val
	slowMu.Unlock()
	return val, true
}

func refreshSlow(e *slowEntry, fn func() []Session) {
	val := fn()
	slowMu.Lock()
	done := e.done
	e.val, e.done = val, nil
	slowMu.Unlock()
	close(done)
}

// forgetSlow drops every kept answer, for tests.
func forgetSlow() {
	slowMu.Lock()
	slowCache = map[string]*slowEntry{}
	slowMu.Unlock()
}

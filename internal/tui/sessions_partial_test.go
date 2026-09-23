package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// A list that came back without a slow agent's sessions is still shown, and
// the TUI asks for it again — backing off, and giving up rather than polling.
func TestA2SessionsPartialAsksAgain(t *testing.T) {
	m := a2Model()
	key := sessionsKey(localMachine, "r1")
	m.sessions = map[string]*sessionsData{key: {loading: true}}
	one := []proto.SessionInfo{{Agent: "claude", ID: "s1", Dir: "/src/api", Title: "Refactor auth"}}

	var waits []time.Duration
	for i := 1; i <= sessionsRetries+1; i++ {
		m.sessions[key].loading = true
		cmd := m.receiveSessions(sessionsMsg{key: key, list: one, partial: true})
		if len(m.sessions[key].list) != 1 {
			t.Fatalf("attempt %d: the sessions that did arrive should be kept", i)
		}
		if i > sessionsRetries {
			if cmd != nil {
				t.Fatalf("attempt %d: should stop asking after %d tries", i, sessionsRetries)
			}
			break
		}
		if cmd == nil {
			t.Fatalf("attempt %d: want another try", i)
		}
		start := time.Now()
		msg := cmd()
		waits = append(waits, time.Since(start))
		r, ok := msg.(sessionsRetryMsg)
		if !ok || r.machine != localMachine || r.project != "r1" {
			t.Fatalf("attempt %d: got %#v", i, msg)
		}
	}
	if len(waits) != sessionsRetries {
		t.Fatalf("tried %d times, want %d", len(waits), sessionsRetries)
	}
	for i := 1; i < len(waits); i++ {
		if waits[i] <= waits[i-1] {
			t.Fatalf("waits do not back off: %v", waits)
		}
	}

	// A complete list ends it, and lets a later partial one start over.
	m.sessions[key].loading = true
	if cmd := m.receiveSessions(sessionsMsg{key: key, list: one}); cmd != nil {
		t.Fatal("a complete list should not ask again")
	}
	if m.sessions[key].tries != 0 {
		t.Fatalf("tries not reset: %d", m.sessions[key].tries)
	}
	m.sessions[key].loading = true
	if cmd := m.receiveSessions(sessionsMsg{key: key, list: one, partial: true}); cmd == nil {
		t.Fatal("want a try after a complete list")
	}

	// An error is reported, and never asked about again.
	m.sessions[key].loading = true
	if cmd := m.receiveSessions(sessionsMsg{key: key, partial: true, err: errFake}); cmd != nil {
		t.Fatal("an error should not schedule a try")
	}
	if m.sessions[key].err == "" {
		t.Fatal("want the error kept")
	}
	// A list for a project the TUI no longer holds is dropped.
	if cmd := m.receiveSessions(sessionsMsg{key: "gone|x", partial: true}); cmd != nil {
		t.Fatal("unknown key should do nothing")
	}
}

// The retry message reloads that project's sessions, ignoring the TTL.
func TestA2SessionsRetryReloads(t *testing.T) {
	m := a2Model()
	// The command is never run, so the client is only asked what it can do.
	m.machines[0].c = a2Client("session.v1")
	m.sessions = map[string]*sessionsData{sessionsKey(localMachine, "r1"): {at: time.Now()}}
	_, cmd := m.Update(sessionsRetryMsg{machine: localMachine, project: "r1"})
	if cmd == nil {
		t.Fatal("want the list asked for again")
	}
	if !m.sessions[sessionsKey(localMachine, "r1")].loading {
		t.Fatal("want it loading again although the list is fresh")
	}
	// A machine that is gone: nothing to do, and no panic.
	if _, cmd := m.Update(sessionsRetryMsg{machine: "nope", project: "r1"}); cmd != nil {
		t.Fatal("unknown machine should do nothing")
	}
}

var errFake = fakeErr{}

type fakeErr struct{}

func (fakeErr) Error() string { return "no" }

// The flag has to survive the wire: a list the server marked partial must
// reach the model as one, and an ordinary list must not.
func TestA2SessionsListCarriesPartial(t *testing.T) {
	for _, partial := range []bool{true, false} {
		c, peer := a1FakeClient(t, "session.v1")
		m := a2Model()
		m.machines[0].c = c
		m.sessions = map[string]*sessionsData{}
		peer.setResult(proto.MethodSessionList, proto.SessionList{
			Sessions: []proto.SessionInfo{{Agent: "claude", ID: "s1", Dir: "/src/api", Title: "Refactor auth"}},
			Partial:  partial,
		})
		cmd := m.loadSessions(localMachine, "r1", true)
		if cmd == nil {
			t.Fatal("no request made")
		}
		msg, ok := cmd().(sessionsMsg)
		if !ok {
			t.Fatalf("got %#v", msg)
		}
		if msg.err != nil {
			t.Fatalf("partial=%v: %v", partial, msg.err)
		}
		if msg.partial != partial || len(msg.list) != 1 {
			t.Fatalf("partial=%v: got partial=%v with %d sessions", partial, msg.partial, len(msg.list))
		}
		// And it lands in the view, with a retry only when it is partial.
		retry := m.receiveSessions(msg)
		if (retry != nil) != partial {
			t.Fatalf("partial=%v: retry scheduled %v", partial, retry != nil)
		}
	}
}

// An older server has no say in the matter: no field means a whole list.
func TestA2SessionsListFromAnOlderServer(t *testing.T) {
	var out proto.SessionList
	if err := json.Unmarshal([]byte(`{"sessions":[{"agent":"claude","id":"s1"}]}`), &out); err != nil {
		t.Fatal(err)
	}
	if out.Partial || len(out.Sessions) != 1 {
		t.Fatalf("a list without the field: partial=%v, %d sessions", out.Partial, len(out.Sessions))
	}
	b, err := json.Marshal(proto.SessionList{Sessions: out.Sessions})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "partial") {
		t.Fatalf("a whole list should not mention partial: %s", b)
	}
}

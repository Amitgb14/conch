package cloudagent

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

// TestRefusedFirstHelloWaitsForInject: the grace for an inject's token is
// hello's, so the very first hello waits for it too.
func TestRefusedFirstHelloWaitsForInject(t *testing.T) {
	r := newRig(t)
	refusedGrace = 10 * time.Second
	r.cloud.setToken("ccm_second")
	r.startServer()
	r.run()
	r.cloud.waitFor("POST /v1/vm/hello", 1, 10*time.Second) // refused
	r.writeBoot("ccm_second")
	hellos := r.cloud.waitFor("POST /v1/vm/hello", 2, 10*time.Second)
	if got := hellos[len(hellos)-1].Header.Get("Authorization"); got != "Bearer ccm_second" {
		t.Fatalf("hello again with %q", got)
	}
	select {
	case err := <-r.done:
		r.done <- err
		t.Fatalf("the agent stopped: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
}

// TestHelloAfterAPauseWaitsForInject: the hello said after a pause, or for
// the control plane's hello command, is refused while an inject is under
// way like any other, and waits for boot.json rather than ending the agent.
func TestHelloAfterAPauseWaitsForInject(t *testing.T) {
	r := newRig(t)
	refusedGrace = 10 * time.Second
	r.startServer()
	r.run()
	r.cloud.waitFor("POST /v1/vm/hello", 1, 10*time.Second)
	r.cloud.setToken("ccm_second")
	go func() {
		time.Sleep(300 * time.Millisecond)
		r.writeBoot("ccm_second")
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := r.agent.hello(ctx, WokeMemory); err != nil {
		t.Fatalf("hello: %v", err)
	}
}

// TestRefusalOfAReplacedToken: a refusal of a token another loop has
// already replaced is not waited out — the token now in use is newer.
func TestRefusalOfAReplacedToken(t *testing.T) {
	r := newRig(t)
	refusedGrace = 10 * time.Second
	r.startServer()
	r.run()
	r.cloud.waitFor("POST /v1/vm/hello", 1, 10*time.Second)
	start := time.Now()
	if got := r.agent.awaitNewToken(context.Background(), "ccm_older"); got != tokenTaken {
		t.Fatalf("got %v", got)
	}
	if took := time.Since(start); took > time.Second {
		t.Fatalf("waited %s", took)
	}
}

// TestRetryAfterIsCapped: a control plane's Retry-After is honoured up to
// retryAfterMax, not for a day.
func TestRetryAfterIsCapped(t *testing.T) {
	if got := retryAfter(86400); got != retryAfterMax {
		t.Fatalf("a day: %s", got)
	}
	if got := retryAfter(3); got != 3*time.Second {
		t.Fatalf("3 s: %s", got)
	}
	fastHello(t)
	was := retryAfterMax
	retryAfterMax = 100 * time.Millisecond
	t.Cleanup(func() { retryAfterMax = was })
	f := newFakeCloud(t, "tok")
	var n atomic.Int32
	f.handle("POST /v1/vm/hello", func(w http.ResponseWriter, _ *http.Request) {
		if n.Add(1) == 1 {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(503)
			json.NewEncoder(w).Encode(map[string]any{"type": "about:blank", "title": "busy", "status": 503, "code": "unavailable", "retry_after": 86400})
			return
		}
		writeJSON(w, 200, map[string]any{"machine_id": testMachine, "relay_url": "ws://127.0.0.1:1", "room_secret": "s", "sleep": map[string]any{}})
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := sayHello(ctx, NewAPI(f.URL(), "tok", nil), VMHello{ConchVersion: "1", Capabilities: []string{}, ServerKey: "k"}, t.Logf); err != nil {
		t.Fatal(err)
	}
}

// TestActivityHonoursRetryAfter: while the control plane has asked for a
// wait, changes do not each send a report; the retry sends them at once.
func TestActivityHonoursRetryAfter(t *testing.T) {
	// Restored only once the agent has stopped: cleanups run last first,
	// and the rig's stops the agent.
	was := retryAfterMax
	retryAfterMax = 800 * time.Millisecond
	t.Cleanup(func() { retryAfterMax = was })
	r := newRig(t)
	var n atomic.Int32
	r.cloud.handle("POST /v1/vm/activity", func(w http.ResponseWriter, _ *http.Request) {
		if n.Add(1) == 1 {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(429)
			json.NewEncoder(w).Encode(map[string]any{"type": "about:blank", "title": "slow", "status": 429, "code": "rate_limited", "retry_after": 3600})
			return
		}
		writeJSON(w, 200, map[string]any{"commands": []any{}})
	})
	r.startServer()
	r.run()
	got := r.cloud.waitFor("POST /v1/vm/activity", 1, 10*time.Second)
	refusedAt := got[0].At
	for range 10 {
		r.agent.kick()
		time.Sleep(30 * time.Millisecond)
	}
	got = r.cloud.waitFor("POST /v1/vm/activity", 2, 10*time.Second)
	if gap := got[1].At.Sub(refusedAt); gap < 600*time.Millisecond {
		t.Fatalf("reported again after %s, inside the wait asked for", gap)
	}
}

// TestStoppedWhileWaitingForBootJSON: stopping the agent before the host
// has written boot.json is not a failure (systemctl stop would otherwise
// mark the unit failed).
func TestStoppedWhileWaitingForBootJSON(t *testing.T) {
	r := newRig(t)
	os.Remove(r.opts.BootPath)
	r.run()
	time.Sleep(100 * time.Millisecond)
	r.stop()
	if err := r.exited(5 * time.Second); err != nil {
		t.Fatalf("exited with %v", err)
	}
}

// TestStoppedDuringTheFirstHello: the same while the control plane is
// still being asked.
func TestStoppedDuringTheFirstHello(t *testing.T) {
	r := newRig(t)
	r.cloud.handle("POST /v1/vm/hello", func(w http.ResponseWriter, _ *http.Request) {
		writeProblem(w, 503, "unavailable", "starting")
	})
	r.startServer()
	r.run()
	r.cloud.waitFor("POST /v1/vm/hello", 1, 10*time.Second)
	r.stop()
	if err := r.exited(5 * time.Second); err != nil {
		t.Fatalf("exited with %v", err)
	}
}

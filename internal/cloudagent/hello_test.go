package cloudagent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fastHello(t *testing.T) {
	t.Helper()
	f, m := helloFirstWait, helloMaxWait
	helloFirstWait, helloMaxWait = 10*time.Millisecond, 40*time.Millisecond
	t.Cleanup(func() { helloFirstWait, helloMaxWait = f, m })
}

func TestHelloPayload(t *testing.T) {
	f := newFakeCloud(t, "tok")
	api := NewAPI(f.URL()+"/", "tok", nil)
	h := VMHello{ConchVersion: "0.1.12", Capabilities: []string{"pane.v1", ContractCapability}, ServerKey: "key", WokeFrom: WokeBoot}
	cfg, err := sayHello(context.Background(), api, h, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MachineID != testMachine || cfg.RoomSecret != "secret" || cfg.RelayURL == "" || cfg.Sleep.IdleSeconds != 600 {
		t.Fatalf("config %+v", cfg)
	}
	got := f.requests("POST /v1/vm/hello")
	if len(got) != 1 {
		t.Fatalf("%d hellos", len(got))
	}
	r := got[0]
	if r.Header.Get("Conch-Contract") != "1.3.0" || r.Header.Get("Authorization") != "Bearer tok" {
		t.Fatalf("headers %v", r.Header)
	}
	var sent map[string]any
	json.Unmarshal(r.Body, &sent)
	if sent["conch_version"] != "0.1.12" || sent["server_key"] != "key" || sent["woke_from"] != "boot" {
		t.Fatalf("sent %s", r.Body)
	}
	if caps, _ := sent["capabilities"].([]any); len(caps) != 2 || caps[1] != "cloud.contract.1.3.0" {
		t.Fatalf("capabilities %v", sent["capabilities"])
	}
}

// TestHelloRetries: a control plane that is down, failing or busy is asked
// again, waiting longer each time, and Retry-After is honoured.
func TestHelloRetries(t *testing.T) {
	fastHello(t)
	f := newFakeCloud(t, "tok")
	var n atomic.Int32
	f.handle("POST /v1/vm/hello", func(w http.ResponseWriter, r *http.Request) {
		switch n.Add(1) {
		case 1:
			writeProblem(w, 503, "unavailable", "starting")
		case 2:
			writeProblem(w, 500, "unavailable", "oops")
		case 3:
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(429)
			json.NewEncoder(w).Encode(map[string]any{"type": "about:blank", "title": "slow", "status": 429, "code": "rate_limited", "retry_after": 0})
		default:
			writeJSON(w, 200, map[string]any{"machine_id": testMachine, "relay_url": "ws://127.0.0.1:1", "room_secret": "s", "sleep": map[string]any{}})
		}
	})
	var logs []string
	cfg, err := sayHello(context.Background(), NewAPI(f.URL(), "tok", nil), VMHello{ConchVersion: "1", Capabilities: []string{}, ServerKey: "k"},
		func(format string, a ...any) { logs = append(logs, format) })
	if err != nil || cfg.RoomSecret != "s" {
		t.Fatalf("cfg %+v err %v", cfg, err)
	}
	if n.Load() != 4 || len(logs) != 3 {
		t.Fatalf("%d calls, %d logs", n.Load(), len(logs))
	}
	got := f.requests("POST /v1/vm/hello")
	if gap := got[2].At.Sub(got[1].At); gap < 10*time.Millisecond {
		t.Fatalf("the second retry came after %s: no backoff", gap)
	}
}

func TestHelloRetriesUnreachable(t *testing.T) {
	fastHello(t)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	var tries int
	_, err := sayHello(ctx, NewAPI("http://127.0.0.1:1", "tok", nil), VMHello{}, func(string, ...any) { tries++ })
	if !errors.Is(err, context.DeadlineExceeded) || tries < 2 {
		t.Fatalf("err %v after %d tries, want retries until the context ends", err, tries)
	}
}

func TestHelloUnauthorized(t *testing.T) {
	f := newFakeCloud(t, "the-new-token")
	_, err := sayHello(context.Background(), NewAPI(f.URL(), "old", nil), VMHello{ConchVersion: "1", Capabilities: []string{}, ServerKey: "k"}, t.Logf)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err %v, want ErrUnauthorized", err)
	}
	if len(f.requests("POST /v1/vm/hello")) != 1 {
		t.Fatal("a refused token was tried again")
	}
}

func TestHelloFatal(t *testing.T) {
	for _, c := range []struct {
		status int
		code   string
		want   string
	}{
		{409, "conflict", "different server key"},
		{426, "contract_unsupported", "does not speak contract 1.3.0"},
		{400, "invalid_request", "invalid_request"},
		{403, "suspended", "suspended"},
	} {
		f := newFakeCloud(t, "tok")
		f.handle("POST /v1/vm/hello", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(c.status)
			p := map[string]any{"type": "about:blank", "title": "no", "status": c.status, "code": c.code}
			if c.status == 426 {
				p["contract_supported"] = ">=2.0.0 <3.0.0"
			}
			json.NewEncoder(w).Encode(p)
		})
		_, err := sayHello(context.Background(), NewAPI(f.URL(), "tok", nil), VMHello{ConchVersion: "1", Capabilities: []string{}, ServerKey: "k"}, t.Logf)
		if !errors.Is(err, ErrFatal) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%d: %v, want ErrFatal saying %q", c.status, err, c.want)
		}
		if n := len(f.requests("POST /v1/vm/hello")); n != 1 {
			t.Errorf("%d: asked %d times", c.status, n)
		}
	}
}

// TestHelloAnswerWithoutRoom: a config the gateway cannot use is an error,
// not a machine that says it runs and is unreachable.
func TestHelloAnswerWithoutRoom(t *testing.T) {
	api := NewAPI("http://x", "t", &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"machine_id":"`+testMachine+`","relay_url":"","room_secret":"","sleep":{}}`), nil
	})})
	if _, err := api.Hello(context.Background(), VMHello{}); err == nil {
		t.Fatal("want an error")
	}
}

func TestProblemDecoding(t *testing.T) {
	for _, c := range []struct {
		res       *http.Response
		wantText  string
		retryable bool
	}{
		{jsonResponse(503, `{"status":503,"code":"unavailable","detail":"down"}`), "503 unavailable: down", true},
		{&http.Response{StatusCode: 502, Header: http.Header{"Content-Type": {"text/html"}}, Body: body("<html>")}, "502: Bad Gateway", true},
		{&http.Response{StatusCode: 429, Header: http.Header{"Retry-After": {"7"}}, Body: body("")}, "429", true},
		{jsonResponse(404, `{"status":404,"code":"not_found","title":"Not here"}`), "404 not_found: Not here", false},
	} {
		api := NewAPI("http://x", "t", &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) { return c.res, nil })})
		_, err := api.Env(context.Background())
		var p *Problem
		if !errors.As(err, &p) || !strings.Contains(err.Error(), c.wantText) || p.retryable() != c.retryable {
			t.Errorf("%d: %v (retryable %v)", c.res.StatusCode, err, p != nil && p.retryable())
		}
		if c.res.StatusCode == 429 && p.RetryAfter != 7 {
			t.Errorf("Retry-After: %d", p.RetryAfter)
		}
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func jsonResponse(status int, s string) *http.Response {
	ct := "application/json"
	if status >= 400 {
		ct = "application/problem+json"
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {ct}}, Body: body(s)}
}

func body(s string) *readCloser { return &readCloser{strings.NewReader(s)} }

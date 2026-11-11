package cloudagent

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/cloudcontract"
	"github.com/Amitgb14/conch/internal/update"
)

// The control plane is conch-cloud's, and its contract (openapi.yaml) is
// owned there. These tests check every request this agent makes, and every
// answer the fake control plane gives it, against that file — so a test
// cannot pass on a field the contract does not have. The file is read from
// a conch-cloud checkout beside this one (or CONCH_CLOUD_CONTRACT); conch's
// own CI has none, and there the checks are skipped, said once, and the
// agent's behaviour is still tested against the fake.

// loadContract is the contract, or nil with why it is not there.
func loadContract() (*cloudcontract.Spec, string) { return cloudcontract.Load(Contract) }

func TestContractFound(t *testing.T) {
	s, why := loadContract()
	if s == nil {
		t.Skip("contract checks skipped: " + why)
	}
	if v := s.Version(); update.Compare(v, Contract) < 0 {
		t.Fatalf("%s is contract %s, older than the %s this agent speaks", s.Path, v, Contract)
	}
	for _, p := range []string{"/v1/vm/hello", "/v1/vm/activity", "/v1/vm/push", "/v1/vm/push/targets", "/v1/vm/env"} {
		if !s.HasPath(p) {
			t.Errorf("%s has no %s", s.Path, p)
		}
	}
	t.Logf("checking against %s", s.Path)
}

// TestSchemaCheckCatches: the checker is what makes the fake honest, so it
// is checked against requests and answers the contract does not allow.
func TestSchemaCheckCatches(t *testing.T) {
	s, why := loadContract()
	if s == nil {
		t.Skip("contract checks skipped: " + why)
	}
	h := http.Header{"Conch-Contract": {Contract}, "Authorization": {"Bearer x"}, "Content-Type": {"application/json"}}
	for name, c := range map[string]struct {
		path, body string
		bad        bool
	}{
		"good hello":        {"/v1/vm/hello", `{"conch_version":"1","capabilities":["a"],"server_key":"k","woke_from":"boot"}`, false},
		"hello no key":      {"/v1/vm/hello", `{"conch_version":"1","capabilities":[]}`, true},
		"hello extra field": {"/v1/vm/hello", `{"conch_version":"1","capabilities":[],"server_key":"k","token":"t"}`, true},
		"hello bad woke":    {"/v1/vm/hello", `{"conch_version":"1","capabilities":[],"server_key":"k","woke_from":"nap"}`, true},
		"good activity":     {"/v1/vm/activity", `{"agents":[{"agent":"claude","state":"working","project":"api"}],"clients_attached":0,"processes_busy":false}`, false},
		"activity state":    {"/v1/vm/activity", `{"agents":[{"agent":"claude","state":"blocked"}],"clients_attached":0}`, true},
		"activity float":    {"/v1/vm/activity", `{"agents":[],"clients_attached":1.5}`, true},
		"push no payloads":  {"/v1/vm/push", `{"urgency":"high"}`, true},
		"push good":         {"/v1/vm/push", `{"urgency":"high","payloads":[{"device_id":"d","ciphertext":"c"}]}`, false},
	} {
		errs := s.CheckRequest("POST", c.path, h, []byte(c.body))
		if (len(errs) > 0) != c.bad {
			t.Errorf("%s: errors %v, want bad=%v", name, errs, c.bad)
		}
	}
	if errs := s.CheckRequest("GET", "/v1/vm/nope", h, nil); len(errs) == 0 {
		t.Error("an unknown path passed")
	}
	jh := http.Header{"Content-Type": {"application/json"}}
	if errs := s.CheckResponse("POST", "/v1/vm/activity", 200, jh, []byte(`{"commands":[{"type":"reboot"}]}`)); len(errs) == 0 {
		t.Error("an unknown command type passed")
	}
	if errs := s.CheckResponse("POST", "/v1/vm/activity", 201, jh, []byte(`{}`)); len(errs) == 0 {
		t.Error("an unlisted status passed")
	}
	if errs := s.CheckResponse("POST", "/v1/vm/hello", 401, jh, []byte(`{"type":"about:blank","title":"x","status":401,"code":"unauthorized"}`)); len(errs) == 0 {
		t.Error("a problem as application/json passed")
	}
	if errs := s.CheckResponse("GET", "/v1/vm/env", 200, jh, []byte(`{"env":{"A":1}}`)); len(errs) == 0 {
		t.Error("a number in env passed")
	}
}

// ---- the fake control plane ----

// fakeCloud is the control plane's VM endpoints on httptest. Each handler
// may be replaced; every request and answer is checked against the
// contract when it is there, and a test fails on any that breaks it.
type fakeCloud struct {
	t    *testing.T
	srv  *httptest.Server
	spec *cloudcontract.Spec

	mu       sync.Mutex
	token    string
	handlers map[string]http.HandlerFunc // "POST /v1/vm/hello"
	calls    map[string][]recorded
	notify   chan string
}

type recorded struct {
	Header http.Header
	Body   []byte
	At     time.Time
}

func newFakeCloud(t *testing.T, token string) *fakeCloud {
	t.Helper()
	f := &fakeCloud{t: t, token: token, handlers: map[string]http.HandlerFunc{}, calls: map[string][]recorded{}, notify: make(chan string, 1024)}
	f.spec, _ = loadContract()
	f.handle("POST /v1/vm/hello", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"machine_id": testMachine, "relay_url": "ws://127.0.0.1:1", "room_secret": "secret",
			"sleep": map[string]any{"idle_seconds": 600, "unattended_max_seconds": 28800, "archive_after_seconds": 1209600}})
	})
	f.handle("POST /v1/vm/activity", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"commands": []any{}})
	})
	f.handle("GET /v1/vm/env", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"env": map[string]string{}})
	})
	f.handle("GET /v1/vm/push/targets", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"targets": []any{}})
	})
	f.handle("POST /v1/vm/push", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(202) })
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeCloud) URL() string { return f.srv.URL }

func (f *fakeCloud) handle(route string, h http.HandlerFunc) {
	f.mu.Lock()
	f.handlers[route] = h
	f.mu.Unlock()
}

func (f *fakeCloud) setToken(tok string) {
	f.mu.Lock()
	f.token = tok
	f.mu.Unlock()
}

// requests is what came to route so far.
func (f *fakeCloud) requests(route string) []recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls[route])
}

// waitFor waits until route has been called n times, and returns them.
func (f *fakeCloud) waitFor(route string, n int, within time.Duration) []recorded {
	f.t.Helper()
	deadline := time.Now().Add(within)
	for {
		if got := f.requests(route); len(got) >= n {
			return got
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("%s called %d times within %s, want %d", route, len(f.requests(route)), within, n)
		}
		select {
		case <-f.notify:
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func (f *fakeCloud) serve(w http.ResponseWriter, r *http.Request) {
	body := new(bytes.Buffer)
	body.ReadFrom(r.Body)
	route := r.Method + " " + r.URL.Path
	if f.spec != nil {
		for _, e := range f.spec.CheckRequest(r.Method, r.URL.Path, r.Header, body.Bytes()) {
			f.t.Errorf("request breaks the contract: %s", e)
		}
	}
	f.mu.Lock()
	h := f.handlers[route]
	tok := f.token
	f.calls[route] = append(f.calls[route], recorded{Header: r.Header.Clone(), Body: body.Bytes(), At: time.Now()})
	f.mu.Unlock()
	select {
	case f.notify <- route:
	default:
	}
	rec := httptest.NewRecorder()
	switch {
	case h == nil:
		writeProblem(rec, 404, "not_found", "no such route in the fake")
	case r.Header.Get("Authorization") != "Bearer "+tok:
		writeProblem(rec, 401, "unauthorized", "This machine token is not valid.")
	default:
		r.Body = readCloser{bytes.NewReader(body.Bytes())}
		h(rec, r)
	}
	if f.spec != nil {
		for _, e := range f.spec.CheckResponse(r.Method, r.URL.Path, rec.Code, rec.Header(), rec.Body.Bytes()) {
			f.t.Errorf("the fake's answer breaks the contract: %s", e)
		}
	}
	for k, v := range rec.Header() {
		w.Header()[k] = v
	}
	w.WriteHeader(rec.Code)
	w.Write(rec.Body.Bytes())
}

type readCloser struct{ io.Reader }

func (readCloser) Close() error { return nil }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeProblem(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"type": "about:blank", "title": http.StatusText(status), "status": status, "code": code, "detail": detail})
}

package cloudagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/Amitgb14/conch/internal/update"
)

// The control plane is conch-cloud's, and its contract (openapi.yaml) is
// owned there. These tests check every request this agent makes, and every
// answer the fake control plane gives it, against that file — so a test
// cannot pass on a field the contract does not have. The file is read from
// a conch-cloud checkout beside this one (or CONCH_CLOUD_CONTRACT); conch's
// own CI has none, and there the checks are skipped, said once, and the
// agent's behaviour is still tested against the fake.

// contractEnv names an openapi.yaml to check against.
const contractEnv = "CONCH_CLOUD_CONTRACT"

type spec struct {
	doc  map[string]any
	path string
}

var (
	specOnce   sync.Once
	loadedSpec *spec
	specWhy    string
)

// loadContract is the contract, or nil with why it is not there.
func loadContract() (*spec, string) {
	specOnce.Do(func() {
		path := os.Getenv(contractEnv)
		if path == "" {
			path = findContract()
		}
		if path == "" {
			specWhy = "no conch-cloud checkout with contract " + Contract + " beside this one (set " + contractEnv + " to its openapi.yaml)"
			return
		}
		b, err := os.ReadFile(path)
		if err != nil {
			specWhy = err.Error()
			return
		}
		var doc map[string]any
		if err := yaml.Unmarshal(b, &doc); err != nil {
			specWhy = path + ": " + err.Error()
			return
		}
		loadedSpec = &spec{doc: doc, path: path}
	})
	return loadedSpec, specWhy
}

// findContract looks up from here for conch-cloud/contract/openapi.yaml, or
// a conch-cloud worktree's, of this agent's contract version or later in
// the same major.
func findContract() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	best, bestV := "", ""
	for ; ; dir = filepath.Dir(dir) {
		cands := []string{filepath.Join(dir, "conch-cloud", "contract", "openapi.yaml")}
		more, _ := filepath.Glob(filepath.Join(dir, "conch-cloud.worktrees", "*", "contract", "openapi.yaml"))
		cands = append(cands, more...)
		for _, c := range cands {
			v := specVersion(c)
			if v == "" || strings.Split(v, ".")[0] != strings.Split(Contract, ".")[0] || update.Compare(v, Contract) < 0 {
				continue
			}
			if best == "" || update.Compare(v, bestV) > 0 {
				best, bestV = c, v
			}
		}
		if filepath.Dir(dir) == dir {
			return best
		}
	}
}

var specVersionLine = regexp.MustCompile(`(?m)^info:\n(?:  .*\n)*?  version: *([0-9.]+)`)

func specVersion(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	m := specVersionLine.FindSubmatch(b)
	if m == nil {
		return ""
	}
	return string(m[1])
}

func TestContractFound(t *testing.T) {
	s, why := loadContract()
	if s == nil {
		t.Skip("contract checks skipped: " + why)
	}
	info, _ := s.doc["info"].(map[string]any)
	if v, _ := info["version"].(string); update.Compare(v, Contract) < 0 {
		t.Fatalf("%s is contract %s, older than the %s this agent speaks", s.path, v, Contract)
	}
	for _, p := range []string{"/v1/vm/hello", "/v1/vm/activity", "/v1/vm/push", "/v1/vm/push/targets", "/v1/vm/env"} {
		if s.pathItem(p) == nil {
			t.Errorf("%s has no %s", s.path, p)
		}
	}
	t.Logf("checking against %s", s.path)
}

// ---- the schema subset the contract uses ----

func (s *spec) pathItem(path string) map[string]any {
	paths, _ := s.doc["paths"].(map[string]any)
	if it, ok := paths[path].(map[string]any); ok {
		return it
	}
	for tmpl, it := range paths {
		re := "^" + regexp.MustCompile(`\\\{[^}]+\\\}`).ReplaceAllString(regexp.QuoteMeta(tmpl), `[^/]+`) + "$"
		if regexp.MustCompile(re).MatchString(path) {
			m, _ := it.(map[string]any)
			return m
		}
	}
	return nil
}

func (s *spec) op(path, method string) (map[string]any, bool) {
	it := s.pathItem(path)
	if it == nil {
		return nil, false
	}
	op, ok := it[strings.ToLower(method)].(map[string]any)
	return op, ok
}

func (s *spec) resolve(sch any) map[string]any {
	m, _ := sch.(map[string]any)
	for m != nil {
		ref, ok := m["$ref"].(string)
		if !ok {
			return m
		}
		var cur any = s.doc
		for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
			cm, _ := cur.(map[string]any)
			cur = cm[part]
		}
		m, _ = cur.(map[string]any)
	}
	return m
}

// mode says which way a body goes: a readOnly property may not be sent,
// a writeOnly one may not come back.
type mode int

const (
	inRequest mode = iota
	inResponse
)

// check is every way v breaks the schema.
func (s *spec) check(sch any, v any, at string, m mode) []string {
	sc := s.resolve(sch)
	if sc == nil {
		return nil
	}
	var errs []string
	fail := func(f string, a ...any) { errs = append(errs, at+": "+fmt.Sprintf(f, a...)) }

	if all, ok := sc["allOf"].([]any); ok {
		merged := map[string]any{}
		for k, v := range sc {
			if k != "allOf" {
				merged[k] = v
			}
		}
		props := map[string]any{}
		var req []any
		for _, part := range append(all, map[string]any{"properties": sc["properties"], "required": sc["required"]}) {
			p := s.resolve(part)
			if pp, ok := p["properties"].(map[string]any); ok {
				for k, v := range pp {
					props[k] = v
				}
			}
			if r, ok := p["required"].([]any); ok {
				req = append(req, r...)
			}
			if t, ok := p["type"]; ok && merged["type"] == nil {
				merged["type"] = t
			}
		}
		merged["properties"], merged["required"] = props, req
		return s.check(merged, v, at, m)
	}
	if one, ok := sc["oneOf"].([]any); ok {
		if d, ok := sc["discriminator"].(map[string]any); ok {
			obj, _ := v.(map[string]any)
			name, _ := d["propertyName"].(string)
			kind, _ := obj[name].(string)
			mapping, _ := d["mapping"].(map[string]any)
			if ref, ok := mapping[kind].(string); ok {
				return s.check(map[string]any{"$ref": ref}, v, at, m)
			}
			return []string{fmt.Sprintf("%s: %s %q is none of the oneOf", at, name, kind)}
		}
		passed := 0
		for _, o := range one {
			if len(s.check(o, v, at, m)) == 0 {
				passed++
			}
		}
		if passed != 1 {
			fail("matches %d of oneOf, want exactly 1", passed)
		}
		return errs
	}
	if c, ok := sc["const"]; ok && fmt.Sprint(c) != fmt.Sprint(v) {
		fail("%v is not the const %v", v, c)
	}
	if enum, ok := sc["enum"].([]any); ok {
		found := false
		for _, e := range enum {
			if fmt.Sprint(e) == fmt.Sprint(v) {
				found = true
			}
		}
		if !found {
			fail("%v is not one of %v", v, enum)
		}
	}
	typ, _ := sc["type"].(string)
	switch typ {
	case "string":
		str, ok := v.(string)
		if !ok {
			fail("%T, want a string", v)
			break
		}
		if p, ok := sc["pattern"].(string); ok && !regexp.MustCompile(p).MatchString(str) {
			fail("%q does not match %s", str, p)
		}
		if ml, ok := sc["maxLength"].(int); ok && len(str) > ml {
			fail("longer than %d", ml)
		}
		switch sc["format"] {
		case "date-time":
			if _, err := time.Parse(time.RFC3339, str); err != nil {
				fail("%q is not a date-time", str)
			}
		case "uri":
			if u, err := url.Parse(str); err != nil || u.Scheme == "" {
				fail("%q is not a uri", str)
			}
		}
	case "integer", "number":
		n, ok := v.(float64)
		if !ok {
			fail("%T, want a %s", v, typ)
			break
		}
		if typ == "integer" && n != math.Trunc(n) {
			fail("%v is not an integer", n)
		}
		if mn, ok := sc["minimum"].(int); ok && n < float64(mn) {
			fail("%v is under the minimum %d", n, mn)
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			fail("%T, want a boolean", v)
		}
	case "array":
		arr, ok := v.([]any)
		if !ok {
			fail("%T, want an array", v)
			break
		}
		for i, it := range arr {
			errs = append(errs, s.check(sc["items"], it, fmt.Sprintf("%s[%d]", at, i), m)...)
		}
	case "object", "":
		obj, ok := v.(map[string]any)
		if !ok {
			if typ == "object" {
				fail("%T, want an object", v)
			}
			break
		}
		props, _ := sc["properties"].(map[string]any)
		req, _ := sc["required"].([]any)
		for _, r := range req {
			if _, ok := obj[r.(string)]; !ok {
				fail("missing required %q", r)
			}
		}
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			p, known := props[k]
			if !known {
				switch ap := sc["additionalProperties"].(type) {
				case map[string]any:
					errs = append(errs, s.check(ap, obj[k], at+"."+k, m)...)
				case bool:
					if !ap {
						fail("%q is not in the contract", k)
					}
				default:
					fail("%q is not in the contract", k)
				}
				continue
			}
			ps := s.resolve(p)
			if m == inRequest && ps["readOnly"] == true {
				fail("%q is read-only and was sent", k)
			}
			if m == inResponse && ps["writeOnly"] == true {
				fail("%q is write-only and came back", k)
			}
			errs = append(errs, s.check(p, obj[k], at+"."+k, m)...)
		}
	}
	return errs
}

// checkRequest is every way a request to path breaks the contract.
func (s *spec) checkRequest(method, path string, h http.Header, body []byte) []string {
	op, ok := s.op(path, method)
	if !ok {
		return []string{fmt.Sprintf("%s %s is not in the contract", method, path)}
	}
	var errs []string
	if h.Get("Conch-Contract") == "" {
		errs = append(errs, method+" "+path+": no Conch-Contract header")
	}
	if !strings.HasPrefix(h.Get("Authorization"), "Bearer ") {
		errs = append(errs, method+" "+path+": no bearer token")
	}
	rb, _ := op["requestBody"].(map[string]any)
	if rb == nil {
		if len(bytes.TrimSpace(body)) > 0 {
			errs = append(errs, method+" "+path+": a body the contract does not take")
		}
		return errs
	}
	content, _ := rb["content"].(map[string]any)
	js, _ := content["application/json"].(map[string]any)
	if js == nil {
		return errs
	}
	if ct := h.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		errs = append(errs, method+" "+path+": Content-Type "+ct)
	}
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return append(errs, method+" "+path+": body is not JSON: "+err.Error())
	}
	return append(errs, s.check(js["schema"], v, method+" "+path+" body", inRequest)...)
}

// checkResponse is every way an answer breaks the contract.
func (s *spec) checkResponse(method, path string, status int, h http.Header, body []byte) []string {
	op, ok := s.op(path, method)
	if !ok {
		return nil // said by checkRequest
	}
	responses, _ := op["responses"].(map[string]any)
	r, ok := responses[fmt.Sprint(status)]
	if !ok {
		if status < 400 {
			return []string{fmt.Sprintf("%s %s answered %d, which the contract does not list", method, path, status)}
		}
		r = responses["default"]
	}
	rm := s.resolve(r)
	if rm == nil {
		return []string{fmt.Sprintf("%s %s %d: no response in the contract", method, path, status)}
	}
	content, _ := rm["content"].(map[string]any)
	if len(content) == 0 {
		if len(bytes.TrimSpace(body)) > 0 {
			return []string{fmt.Sprintf("%s %s %d: a body where the contract has none", method, path, status)}
		}
		return nil
	}
	ct := strings.Split(h.Get("Content-Type"), ";")[0]
	media, ok := content[ct].(map[string]any)
	if !ok {
		return []string{fmt.Sprintf("%s %s %d: Content-Type %q, the contract says %v", method, path, status, ct, mapKeys(content))}
	}
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return []string{fmt.Sprintf("%s %s %d: not JSON: %v", method, path, status, err)}
	}
	return s.check(media["schema"], v, fmt.Sprintf("%s %s %d", method, path, status), inResponse)
}

func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
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
		errs := s.checkRequest("POST", c.path, h, []byte(c.body))
		if (len(errs) > 0) != c.bad {
			t.Errorf("%s: errors %v, want bad=%v", name, errs, c.bad)
		}
	}
	if errs := s.checkRequest("GET", "/v1/vm/nope", h, nil); len(errs) == 0 {
		t.Error("an unknown path passed")
	}
	jh := http.Header{"Content-Type": {"application/json"}}
	if errs := s.checkResponse("POST", "/v1/vm/activity", 200, jh, []byte(`{"commands":[{"type":"reboot"}]}`)); len(errs) == 0 {
		t.Error("an unknown command type passed")
	}
	if errs := s.checkResponse("POST", "/v1/vm/activity", 201, jh, []byte(`{}`)); len(errs) == 0 {
		t.Error("an unlisted status passed")
	}
	if errs := s.checkResponse("POST", "/v1/vm/hello", 401, jh, []byte(`{"type":"about:blank","title":"x","status":401,"code":"unauthorized"}`)); len(errs) == 0 {
		t.Error("a problem as application/json passed")
	}
	if errs := s.checkResponse("GET", "/v1/vm/env", 200, jh, []byte(`{"env":{"A":1}}`)); len(errs) == 0 {
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
	spec *spec

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
		for _, e := range f.spec.checkRequest(r.Method, r.URL.Path, r.Header, body.Bytes()) {
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
		for _, e := range f.spec.checkResponse(r.Method, r.URL.Path, rec.Code, rec.Header(), rec.Body.Bytes()) {
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

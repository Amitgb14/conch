package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/config"
)

// A provider shaped as instabox is: every change needs an Idempotency-Key
// (a key seen before on another request is refused), a delete must name
// the machine in X-Confirm-Delete, and a create answers with an operation.
type fakeInstabox struct {
	mu       sync.Mutex
	next     int
	machines map[string]map[string]any
	keys     map[string]string // Idempotency-Key → the request it came with
	seen     []http.Header
}

func newFakeInstabox(t *testing.T) (*fakeInstabox, string) {
	t.Helper()
	f := &fakeInstabox{machines: map[string]map[string]any{}, keys: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, srv.URL
}

func (f *fakeInstabox) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen = append(f.seen, r.Header.Clone())
	reply := func(code int, v any) {
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	fail := func(code int, c string) { reply(code, map[string]any{"error": map[string]any{"code": c}}) }
	if r.Header.Get("X-API-Key") != "ibk_test" {
		fail(401, "invalid_key")
		return
	}
	if r.Method != "GET" {
		k := r.Header.Get("Idempotency-Key")
		if k == "" {
			fail(400, "bad_request")
			return
		}
		if prev, ok := f.keys[k]; ok && prev != r.Method+r.URL.Path {
			fail(409, "idempotency_conflict")
			return
		}
		f.keys[k] = r.Method + r.URL.Path
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/") // v1 machines [id [verb]]
	if len(parts) == 2 {
		switch r.Method {
		case "POST":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.next++
			id := fmt.Sprintf("m_%026d", f.next)
			f.machines[id] = map[string]any{"id": id, "name": body["name"], "state": "creating"}
			reply(202, map[string]any{"id": "op_1", "machineId": id, "state": "queued"})
		case "GET":
			var all []any
			for _, m := range f.machines {
				all = append(all, m)
			}
			reply(200, map[string]any{"machines": all, "nextCursor": nil})
		}
		return
	}
	m, ok := f.machines[parts[2]]
	if !ok {
		fail(404, "not_found")
		return
	}
	switch {
	case r.Method == "GET" && len(parts) == 3:
		if m["state"] == "creating" {
			defer func() { m["state"] = "running" }()
		}
		reply(200, m)
	case r.Method == "DELETE":
		if r.Header.Get("X-Confirm-Delete") != parts[2] {
			fail(409, "delete_confirmation_required")
			return
		}
		delete(f.machines, parts[2])
		reply(202, map[string]any{"id": "op_2", "state": "queued"})
	case r.Method == "POST" && parts[3] == "stop":
		m["state"] = "stopped"
		reply(202, map[string]any{"id": "op_3"})
	case r.Method == "POST" && parts[3] == "start":
		m["state"] = "running"
		reply(202, map[string]any{"id": "op_4"})
	case r.Method == "POST" && parts[3] == "ssh-tokens":
		reply(201, map[string]any{"token": "ibssh_abc", "host": "ssh.box.test", "port": 22})
	default:
		fail(404, "not_found")
	}
}

func boxFile(api string) string {
	return `format = 3
name = "box"
api = "` + api + `"
key_env = "BOX_KEY"
auth_header = "X-API-Key"
auth_value = "{key}"
headers = { Idempotency-Key = "{uuid}", X-Client = "conch" }

[create]
call = "POST /v1/machines"
body = { name = "{name}" }
id = "machineId"

[get]
call = "GET /v1/machines/{id}"
state = "state"
name = "name"

[list]
call = "GET /v1/machines"
items = "machines"
id = "id"
mine = "conch-"

[start]
call = "POST /v1/machines/{id}/start"
[stop]
call = "POST /v1/machines/{id}/stop"
[delete]
call = "DELETE /v1/machines/{id}"
headers = { X-Confirm-Delete = "{id}", X-Note = "for {name}" }

[states]
creating = "starting"
running = "started"
stopped = "stopped"
deleted = "gone"

[ssh]
host = "{reply.host}"
user = "{reply.token}"
key = "none"
[ssh.access]
call = "POST /v1/machines/{id}/ssh-tokens"
headers = { idempotency-key = "access-{id}-{uuid}", X-Client = "" }
`
}

func TestFormatThreeSendsHeaders(t *testing.T) {
	old := pollEvery
	pollEvery = time.Millisecond
	t.Cleanup(func() { pollEvery = old })
	f, api := newFakeInstabox(t)
	t.Setenv("BOX_KEY", "ibk_test")
	path := customHome(t, "box", boxFile(api))
	if _, problems, err := ReadProviderFile(path); err != nil || len(problems) != 0 {
		t.Fatalf("file: %v %v", problems, err)
	}
	p, err := Open("box", config.SandboxCfg{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	s, err := p.Create(ctx, Spec{Name: "hull"})
	if err != nil || s.State != StateStarted {
		t.Fatalf("create: %+v %v", s, err)
	}
	if err := p.Stop(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Start(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	a, err := p.SSHAccess(ctx, s.ID)
	if err != nil || a.User != "ibssh_abc" || a.Host != "ssh.box.test" {
		t.Fatalf("ssh: %+v %v", a, err)
	}
	if err := p.Delete(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Get(ctx, s.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("gone: %v", err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	keys := map[string]bool{}
	for _, h := range f.seen {
		k := h.Get("Idempotency-Key")
		if k == "" {
			t.Fatalf("a request without its key: %v", h)
		}
		// A new key for every request: the provider would answer a second
		// use with the first one's reply.
		if keys[k] {
			t.Fatalf("Idempotency-Key %q sent twice", k)
		}
		keys[k] = true
		switch {
		case strings.HasPrefix(k, "access-"):
			// The call's own header, named in other letters, replaces the
			// file's; an empty one leaves the file's out.
			if !strings.HasPrefix(k, "access-"+s.ID+"-") || len(h.Values("Idempotency-Key")) != 1 || h.Get("X-Client") != "" {
				t.Fatalf("ssh access headers: %v", h)
			}
		case len(k) != 36 || k[14] != '4':
			t.Fatalf("not a v4 uuid: %q", k)
		default:
			if h.Get("X-Client") != "conch" {
				t.Fatalf("the file's header missing: %v", h)
			}
		}
		if h.Get("X-API-Key") != "ibk_test" {
			t.Fatalf("the key: %v", h)
		}
		// {name} is not known to a delete, so that header is left out
		// rather than sent as "for {name}".
		if h.Get("X-Note") != "" {
			t.Fatalf("an unfilled header was sent: %q", h.Get("X-Note"))
		}
	}
}

func TestFormatThreeProblems(t *testing.T) {
	good := boxFile("https://api.example.test")
	for _, c := range []struct{ from, to, want string }{
		{"format = 3\n", "", "headers needs format = 3"},
		{"format = 3\n", "format = 2\n", "headers needs format = 3"},
		{`X-Client = "conch"`, `"X Client" = "conch"`, `"X Client" is not a header name`},
		{`X-Client = "conch"`, `x-api-key = "{uuid}"`, "x-api-key carries the key"},
		{`X-Client = "conch"`, `Content-Type = "text/plain"`, "Content-Type is conch's to set"},
		{`X-Client = "conch"`, `host = "evil.test"`, "host is conch's to set"},
		{`X-Client = "conch"`, `X-Secret = "{key}"`, "unknown placeholder {key}"},
		{`X-Client = "conch"`, `X-Client = "a\r\nX-Evil: 1"`, "line break"},
		{`X-Confirm-Delete = "{id}"`, `X-Confirm-Delete = "{snapshot}"`, "[delete] headers: X-Confirm-Delete: unknown placeholder {snapshot}"},
	} {
		changed := strings.Replace(good, c.from, c.to, 1)
		if changed == good {
			t.Fatalf("%q is not in the file", c.from)
		}
		_, problems, err := ReadProviderFile(customHome(t, "box", changed))
		if err != nil || !strings.Contains(strings.Join(problems, "\n"), c.want) {
			t.Errorf("%q → %q: %v %v", c.from, c.to, problems, err)
		}
	}
	// Each place a header may go asks for format 3 alone.
	tl := tensorlakeFile("https://api.example.test")
	for _, where := range []string{"create", "get", "list", "delete"} {
		changed := strings.Replace(tl, "["+where+"]\n", "["+where+"]\nheaders = { X-A = \"b\" }\n", 1)
		if changed == tl {
			t.Fatalf("[%s] is not in the file", where)
		}
		_, problems, _ := ReadProviderFile(customHome(t, "tlake", changed))
		if got := strings.Join(problems, "\n"); !strings.Contains(got, "["+where+"] headers needs format = 3") {
			t.Errorf("[%s]: %v", where, problems)
		}
	}
	// A header value that is not a string is the file's error, said.
	if _, _, err := ReadProviderFile(customHome(t, "box", strings.Replace(good, `X-Client = "conch"`, `X-Client = 1`, 1))); err == nil {
		t.Error("a number for a header value was taken")
	}
}

func TestFormatThreeDescribesHeaders(t *testing.T) {
	f, problems, err := ReadProviderFile(customHome(t, "box", boxFile("https://api.example.test")))
	if err != nil || len(problems) != 0 {
		t.Fatal(problems, err)
	}
	d := f.Describe()
	want := "DELETE /v1/machines/{id} [Idempotency-Key: {uuid}] [X-Client: conch] [X-Confirm-Delete: {id}] [X-Note: for {name}]"
	if d[5].Does != "Delete" || d[5].Call != want {
		t.Fatalf("%q", d[5].Call)
	}
	// A file without headers reads as it did.
	tf, _, _ := ReadProviderFile(customHome(t, "tlake", tensorlakeFile("https://api.example.test")))
	for _, m := range tf.Describe()[:6] {
		if strings.Contains(m.Call, " [") {
			t.Fatalf("headers where there are none: %q", m.Call)
		}
	}
}

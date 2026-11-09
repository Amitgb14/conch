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

// A cloud shaped as DigitalOcean's is: one droplet wrapped in
// {"droplet": ...}, a key put in through cloud-init, the public address
// one of several in a list, and pages by number.
type fakeCloud struct {
	mu      sync.Mutex
	next    int
	items   map[string]map[string]any
	created []map[string]any
	actions []string
}

func newFakeCloud(t *testing.T) (*fakeCloud, string) {
	t.Helper()
	f := &fakeCloud{next: 100, items: map[string]map[string]any{}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, srv.URL
}

func (f *fakeCloud) add(name, status string) string {
	f.next++
	id := fmt.Sprint(f.next)
	f.items[id] = map[string]any{"id": f.next, "name": name, "status": status, "vcpus": 2, "memory": 4096,
		"created_at": "2026-11-08T10:00:00Z",
		"networks": map[string]any{"v4": []any{
			map[string]any{"type": "private", "ip_address": "10.0.0." + fmt.Sprint(f.next%200)},
			map[string]any{"type": "public", "ip_address": "203.0.113." + fmt.Sprint(f.next%200)},
		}}}
	return id
}

func (f *fakeCloud) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	reply := func(code int, v any) {
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/") // v2 droplets [id [actions]]
	switch {
	case r.Method == "POST" && len(parts) == 2:
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.created = append(f.created, body)
		id := f.add(fmt.Sprint(body["name"]), "new")
		reply(202, map[string]any{"droplet": f.items[id]})
	case r.Method == "GET" && len(parts) == 2:
		// Two droplets a page, as DigitalOcean's pages are numbered.
		var all []any
		for i := 101; i <= f.next; i++ {
			if d, ok := f.items[fmt.Sprint(i)]; ok {
				all = append(all, d)
			}
		}
		page := 1
		fmt.Sscan(r.URL.Query().Get("page"), &page)
		lo, hi := (page-1)*2, page*2
		lo, hi = min(lo, len(all)), min(hi, len(all))
		out := map[string]any{"droplets": all[lo:hi], "meta": map[string]any{"total": len(all)}}
		if hi < len(all) {
			out["next_page"] = page + 1
		}
		reply(200, out)
	case len(parts) >= 3:
		d, ok := f.items[parts[2]]
		if !ok {
			reply(404, map[string]any{"id": "not_found", "message": "The resource you were accessing could not be found."})
			return
		}
		switch {
		case r.Method == "GET":
			// A new droplet is active the next time it is looked at.
			if d["status"] == "new" {
				defer func() { d["status"] = "active" }()
			}
			reply(200, map[string]any{"droplet": d})
		case r.Method == "DELETE":
			delete(f.items, parts[2])
			w.WriteHeader(204)
		case r.Method == "POST" && len(parts) == 4:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.actions = append(f.actions, fmt.Sprint(body["type"]))
			d["status"] = map[string]string{"power_off": "off", "power_on": "active"}[fmt.Sprint(body["type"])]
			reply(201, map[string]any{"action": map[string]any{"id": 1}})
		}
	default:
		reply(404, map[string]any{"message": "no such route"})
	}
}

func cloudFile(api string) string {
	return `format = 2
name = "cloud"
label = "Cloud"
api = "` + api + `"
key_env = "CLOUD_TOKEN"

[create]
call = "POST /v2/droplets"
body = { name = "{name}", size = "s-1vcpu-1gb", image = "ubuntu-24-04-x64", user_data = """#cloud-config
ssh_authorized_keys:
  - {public_key}
""" }
id = "droplet.id"

[get]
call = "GET /v2/droplets/{id}"
item = "droplet"
state = "status"
name = "name"
cpu = "vcpus"
memory_mb = "memory"
created = "created_at"

[list]
call = "GET /v2/droplets"
items = "droplets"
id = "id"
next = "next_page"
cursor_param = "page"
mine = "conch-"

[start]
call = "POST /v2/droplets/{id}/actions"
body = { type = "power_on" }
[stop]
call = "POST /v2/droplets/{id}/actions"
body = { type = "power_off" }
[delete]
call = "DELETE /v2/droplets/{id}"

[states]
new = "starting"
active = "started"
off = "stopped"
archive = "gone"

[ssh]
host = "{reply.droplet.networks.v4[type=public].ip_address}"
user = "root"
key = "account"
[ssh.access]
call = "GET /v2/droplets/{id}"
`
}

func TestFormatTwoRunsAWrappedCloud(t *testing.T) {
	old := pollEvery
	pollEvery = time.Millisecond
	oldKey := PublicKey
	t.Cleanup(func() { pollEvery, PublicKey = old, oldKey })
	PublicKey = func(context.Context) (string, error) { return "ssh-ed25519 AAAAkey me@here\n", nil }
	f, api := newFakeCloud(t)
	t.Setenv("CLOUD_TOKEN", "tok")
	path := customHome(t, "cloud", cloudFile(api))
	if _, problems, err := ReadProviderFile(path); err != nil || len(problems) != 0 {
		t.Fatalf("file: %v %v", problems, err)
	}
	p, err := Open("cloud", config.SandboxCfg{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	s, err := p.Create(ctx, Spec{Name: "hull"})
	if err != nil || s.State != StateStarted || s.CPU != 2 || s.Memory != 4 || s.Created.IsZero() {
		t.Fatalf("create: %+v %v", s, err)
	}
	// The key is put inside a longer string, trimmed of its newline.
	if ud := f.created[0]["user_data"]; ud != "#cloud-config\nssh_authorized_keys:\n  - ssh-ed25519 AAAAkey me@here\n" {
		t.Fatalf("user_data %q", ud)
	}
	a, err := p.SSHAccess(ctx, s.ID)
	if err != nil || !strings.HasPrefix(a.Host, "203.0.113.") || a.User != "root" {
		t.Fatalf("the public address, not the first: %+v %v", a, err)
	}
	// Three pages of two: conch's two are found among the account's five.
	f.mu.Lock()
	f.add("web", "active")
	f.add("db", "off")
	f.add("conch-other-abc123", "off")
	f.add("mail", "active")
	f.mu.Unlock()
	list, err := p.List(ctx)
	if err != nil || len(list) != 2 || list[1].State != StateStopped {
		t.Fatalf("list %+v %v", list, err)
	}
	if err := p.Stop(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	if s2, err := p.Start(ctx, s.ID); err != nil || s2.State != StateStarted || strings.Join(f.actions, ",") != "power_off,power_on" {
		t.Fatalf("start: %+v %v %v", s2, err, f.actions)
	}
	if err := p.Delete(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Get(ctx, s.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("gone: %v", err)
	}

	// No key to give, or none to be had: said, and nothing made.
	made := len(f.created)
	PublicKey = nil
	if _, err := p.Create(ctx, Spec{}); err == nil || !strings.Contains(err.Error(), "no") {
		t.Fatalf("no key hook: %v", err)
	}
	PublicKey = func(context.Context) (string, error) { return "", errors.New("ssh-keygen failed") }
	if _, err := p.Create(ctx, Spec{}); err == nil || !strings.Contains(err.Error(), "ssh-keygen failed") {
		t.Fatalf("key error: %v", err)
	}
	if len(f.created) != made {
		t.Fatal("a sandbox was made without its key")
	}
}

func TestFormatTwoProblems(t *testing.T) {
	good := cloudFile("https://api.example.test")
	for _, c := range []struct{ from, to, want string }{
		{"format = 2\n", "", "[get] item needs format = 2"},
		{"format = 2\n", "format = 1\n", "[get] item needs format = 2"},
		{"format = 2\n", "format = 3\n", "reads up to format 2"},
		{"format = 2\n", "format = -1\n", "reads up to format 2"},
	} {
		path := customHome(t, "cloud", strings.Replace(good, c.from, c.to, 1))
		_, problems, err := ReadProviderFile(path)
		if err != nil || !strings.Contains(strings.Join(problems, "\n"), c.want) {
			t.Errorf("%q → %q: %v %v", c.from, c.to, problems, err)
		}
	}
	// Each format-2 thing alone asks for format 2.
	tl := tensorlakeFile("https://api.example.test")
	for _, c := range []struct{ from, to, want string }{
		{`body = { name = "{name}"`, `body = { key = "{public_key}", name = "{name}"`, "{public_key} in [create] body"},
		{`host = "sandbox.tensorlake.ai"`, `host = "{reply.ips[kind=v4].ip}"`, "the selector in"},
	} {
		changed := strings.Replace(tl, c.from, c.to, 1)
		if changed == tl {
			t.Fatalf("%q is not in the file", c.from)
		}
		path := customHome(t, "tlake", changed)
		_, problems, _ := ReadProviderFile(path)
		if got := strings.Join(problems, "\n"); !strings.Contains(got, c.want) || !strings.Contains(got, "needs format = 2") {
			t.Errorf("%s: %v", c.want, problems)
		}
	}
	// A format-1 file still reads as it did.
	if _, problems, _ := ReadProviderFile(customHome(t, "tlake", tl)); len(problems) != 0 {
		t.Fatal(problems)
	}
}

func TestPickSelectors(t *testing.T) {
	var v any
	_ = json.Unmarshal([]byte(`{"n":{"v4":[{"type":"private","ip":"10.0.0.1"},{"type":"public","ip":"1.2.3.4"},{"type":"public","ip":"5.6.7.8"}],"s":"x"},
		"top":[{"k":1,"v":"one"}]}`), &v)
	for path, want := range map[string]any{
		"n.v4[type=public].ip":  "1.2.3.4", // the first that matches
		"n.v4[type=private].ip": "10.0.0.1",
		"n.v4[type=none].ip":    nil,
		"n.s[type=public]":      nil, // not a list
		"n.v4[type].ip":         nil, // no value to match
		"n.v4[=public].ip":      nil,
		"top[k=1].v":            "one", // numbers match as written
		"n.v4.1[type=x]":        nil,
		"x[type=public]":        nil,
	} {
		if got := Pick(v, path); got != want {
			t.Errorf("%q: %v, want %v", path, got, want)
		}
	}
	var list any
	_ = json.Unmarshal([]byte(`[{"a":"b","c":"d"}]`), &list)
	if got := Pick(list, "[a=b].c"); got != "d" {
		t.Errorf("a selector on the reply itself: %v", got)
	}
}

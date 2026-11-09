package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/remote"
	"github.com/Amitgb14/conch/internal/sandbox"
)

// adaptSite is a provider's docs and API on one test server: the docs at
// /openapi.yaml, the list call at /sandboxes behind a key.
func adaptSite(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/openapi.yaml":
			w.Header().Set("Content-Type", "application/yaml")
			w.Write([]byte("openapi: 3.1.0\n# " + strings.Repeat("the acme sandbox API ", 20)))
		case "/sandboxes":
			if r.Header.Get("Authorization") != "Bearer acme-key" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"sandboxes": []any{
				map[string]any{"id": "a", "name": "conch-x-1"}, map[string]any{"id": "b", "name": "theirs"}}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func adaptFile(api string) string {
	return `name = "acme"
label = "Acme"
api = "` + api + `"
key_env = "ACME_API_KEY"
adapted_by = "claude"
[create]
call = "POST /sandboxes"
body = { name = "{name}" }
id = "id"
[get]
call = "GET /sandboxes/{id}"
state = "status"
name = "name"
[list]
call = "GET /sandboxes"
items = "sandboxes"
id = "id"
mine = "conch-"
[start]
call = "POST /sandboxes/{id}/start"
[stop]
call = "POST /sandboxes/{id}/stop"
[delete]
call = "DELETE /sandboxes/{id}"
[states]
running = "started"
[ssh]
host = "ssh.acme.test"
user = "{id}"
key = "account"
`
}

// adaptAgent is a server whose "agent" writes what write says into the
// folder it is started in, works for one look and is then done.
type adaptAgent struct {
	mu      sync.Mutex
	created []proto.PaneCreateParams
	prompts []string
	looks   int
}

func startAdaptAgent(t *testing.T, write func(dir string)) *adaptAgent {
	t.Helper()
	a := &adaptAgent{}
	srv := startA4Server(t, config.SocketPath())
	srv.setHandle(func(msg proto.Message, conn *proto.Conn) (any, *proto.Error) {
		a.mu.Lock()
		defer a.mu.Unlock()
		switch msg.Method {
		case proto.MethodPaneCreate:
			var p proto.PaneCreateParams
			json.Unmarshal(msg.Params, &p)
			a.created = append(a.created, p)
			write(p.Cwd)
			return proto.PaneInfo{ID: "p7", Name: p.Name}, nil
		case proto.MethodPaneList:
			a.looks++
			state := proto.AgentDone
			if a.looks == 1 {
				state = proto.AgentWorking
			}
			return proto.PaneList{Panes: []proto.PaneInfo{a4Pane("p7", state)}}, nil
		case proto.MethodAgentPrompt:
			var p proto.AgentPromptParams
			json.Unmarshal(msg.Params, &p)
			a.prompts = append(a.prompts, p.Text)
			write(filepath.Join(sandbox.ProvidersDir(), "drafts", "acme"))
			go conn.Write(a4Updated(proto.PaneInfo{ID: "p7", Agent: &proto.AgentStatus{Name: "claude", State: proto.AgentDone, Turn: 2}}))
			return proto.AgentPromptResult{ID: "p7", Agent: "claude", Turn: 2}, nil
		}
		return nil, nil
	})
	return a
}

func TestAdaptWritesChecksAndSaves(t *testing.T) {
	a4Env(t)
	site := adaptSite(t)
	agent := startAdaptAgent(t, func(dir string) {
		os.WriteFile(filepath.Join(dir, "provider.toml"), []byte(adaptFile(site.URL)), 0o600)
	})
	t.Setenv("ACME_API_KEY", "acme-key")
	var err error
	out, _ := a4Capture(t, "", func() { err = runSandbox([]string{"adapt", "-name", "acme", site.URL + "/openapi.yaml"}) })
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{"docs: " + site.URL + "/openapi.yaml", "claude is writing the file in pane p7",
		"New sandbox", "POST /sandboxes", "signed in with $ACME_API_KEY: the list answered, 2 sandboxes (1 of them conch's)",
		"save it with: conch sandbox provider save acme"} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in\n%s", want, out)
		}
	}
	p := agent.created[0]
	dir := filepath.Join(sandbox.ProvidersDir(), "drafts", "acme")
	if p.Agent != "claude" || p.Cwd != dir || !p.NoProject || !strings.Contains(p.Prompt, `"acme"`) || strings.Contains(p.Prompt, "acme-key") {
		t.Fatalf("pane %+v", p)
	}
	// The key is emptied in the agent's pane, and is nowhere in its folder.
	if !strings.Contains(strings.Join(p.Env, " "), "ACME_API_KEY=") {
		t.Fatalf("env %v", p.Env)
	}
	filepath.Walk(dir, func(path string, info os.FileInfo, _ error) error {
		if b, _ := os.ReadFile(path); !info.IsDir() && strings.Contains(string(b), "acme-key") {
			t.Errorf("the key is in %s", path)
		}
		return nil
	})
	if b, _ := os.ReadFile(filepath.Join(dir, "PROVIDER-FILE.md")); string(b) != sandbox.ProviderFileGuide {
		t.Fatal("the guide is not in the folder")
	}
	if docs, _ := os.ReadDir(filepath.Join(dir, "docs")); len(docs) != 1 {
		t.Fatalf("docs %v", docs)
	}
	// Not a provider until saved.
	if sandbox.Known("acme") {
		t.Fatal("a draft is a provider")
	}
	out, _ = a4Capture(t, "", func() { err = runSandbox([]string{"provider", "save", "acme"}) })
	if err != nil || !sandbox.Known("acme") || !strings.Contains(out, "saved") {
		t.Fatalf("save: %v %q", err, out)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("the draft is left behind")
	}
	out, _ = a4Capture(t, "", func() { err = runSandbox([]string{"providers", "-json"}) })
	var ps []providerJSON
	json.Unmarshal([]byte(out), &ps)
	if last := ps[len(ps)-1]; err != nil || last.Name != "acme" || !last.Adapted || !last.Ready || last.Label != "Acme" ||
		!strings.Contains(last.Note, "adapted from its API docs by claude") {
		t.Fatalf("providers: %+v %v", ps, err)
	}
	// Adapting it again is refused while it is a provider, and so is a
	// built-in's name.
	a4Capture(t, "", func() { err = runSandbox([]string{"adapt", "-name", "acme", site.URL}) })
	if err == nil || !strings.Contains(err.Error(), "already a provider") {
		t.Fatalf("again: %v", err)
	}
	a4Capture(t, "", func() { err = runSandbox([]string{"adapt", "https://api.daytona.io/sandbox"}) })
	if err == nil || !strings.Contains(err.Error(), "built in") {
		t.Fatalf("built in: %v", err)
	}
	// A machine on it keeps it from being removed.
	if _, err := remote.SaveMachine(remote.Machine{Label: "hull", Target: "acme:sbx1"}); err != nil {
		t.Fatal(err)
	}
	a4Capture(t, "", func() { err = runSandbox([]string{"provider", "rm", "acme"}) })
	if err == nil || !strings.Contains(err.Error(), "hull") {
		t.Fatalf("rm with a machine: %v", err)
	}
}

func TestAdaptJSONAndProblems(t *testing.T) {
	a4Env(t)
	site := adaptSite(t)
	bad := strings.Replace(adaptFile(site.URL), `running = "started"`, `running = "up"`, 1)
	startAdaptAgent(t, func(dir string) {
		b := bad
		if _, err := os.Stat(filepath.Join(dir, "provider.toml")); err == nil {
			b = adaptFile(site.URL) // the revision fixes it
		}
		os.WriteFile(filepath.Join(dir, "provider.toml"), []byte(b), 0o600)
	})
	var err error
	out, _ := a4Capture(t, "", func() { err = runSandbox([]string{"adapt", "-json", "-name", "acme", site.URL + "/openapi.yaml"}) })
	if err == nil || !strings.Contains(err.Error(), "-revise") {
		t.Fatalf("a bad file passed: %v", err)
	}
	var events []adaptEvent
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var e adaptEvent
		if json.Unmarshal([]byte(line), &e) != nil {
			t.Fatalf("not JSON: %q", line)
		}
		events = append(events, e)
	}
	kinds := ""
	for _, e := range events {
		kinds += e.Event + " "
	}
	if kinds != "docs agent waiting file problems error " {
		t.Fatalf("events %q", kinds)
	}
	if f := events[3]; !strings.Contains(f.Text, "[ssh]") || len(f.Mapping) != 8 {
		t.Fatalf("file event %+v", f)
	}
	if !strings.Contains(strings.Join(events[4].Problems, " "), `running = "up"`) {
		t.Fatalf("problems %v", events[4].Problems)
	}
	// Saving a draft that is wrong is refused.
	a4Capture(t, "", func() { err = runSandbox([]string{"provider", "save", "acme"}) })
	if err == nil {
		t.Fatal("saved a wrong draft")
	}
	// Asked to change it, the agent fixes it; without a key the check
	// says so rather than failing.
	out, _ = a4Capture(t, "", func() { err = runSandbox([]string{"adapt", "-revise", "map running to started", "acme"}) })
	if err != nil || !strings.Contains(out, "not checked against the API: set $ACME_API_KEY") {
		t.Fatalf("revise: %v\n%s", err, out)
	}
	out, _ = a4Capture(t, "", func() { err = runSandbox([]string{"provider", "check", "-offline", "acme"}) })
	if err != nil || !strings.Contains(out, "no problems") {
		t.Fatalf("check: %v %q", err, out)
	}
	a4Capture(t, "", func() { err = runSandbox([]string{"adapt", "-revise", "x", "nope"}) })
	if err == nil || !strings.Contains(err.Error(), "no draft of nope") {
		t.Fatalf("revise nothing: %v", err)
	}
}

func TestAdaptRefusals(t *testing.T) {
	a4Env(t)
	site := adaptSite(t)
	old := adaptWait
	adaptWait = 5 * time.Second
	t.Cleanup(func() { adaptWait = old })
	// The agent finds no ssh.
	startAdaptAgent(t, func(dir string) {
		os.WriteFile(filepath.Join(dir, "NO_SSH.txt"), []byte("only an exec API\n"), 0o600)
	})
	var err error
	a4Capture(t, "", func() { err = runSandbox([]string{"adapt", "-name", "acme", site.URL + "/openapi.yaml"}) })
	if err == nil || !strings.Contains(err.Error(), "no ssh way in") || !strings.Contains(err.Error(), "only an exec API") {
		t.Fatalf("no ssh: %v", err)
	}
	// Nothing that is docs: an API that answers 401, a page too short.
	a4Capture(t, "", func() { err = runSandbox([]string{"adapt", "-name", "acme", site.URL + "/sandboxes"}) })
	if err == nil || !strings.Contains(err.Error(), "found no docs") {
		t.Fatalf("no docs: %v", err)
	}
	a4Capture(t, "", func() { err = runSandbox([]string{"adapt", "http://127.0.0.1:1/x"}) })
	if err == nil || !strings.Contains(err.Error(), "-name") {
		t.Fatalf("no name: %v", err)
	}
	a4Capture(t, "", func() { err = runSandbox([]string{"adapt"}) })
	if err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("no url: %v", err)
	}
	for _, args := range [][]string{{"provider"}, {"provider", "bogus"}, {"provider", "save"}, {"provider", "rm", "nope"}, {"provider", "rm", "daytona"}, {"provider", "show", "nope"}} {
		a4Capture(t, "", func() { err = runSandbox(args) })
		if err == nil {
			t.Errorf("%v was taken", args)
		}
	}
}

func TestAdaptNamesAndCandidates(t *testing.T) {
	for in, want := range map[string]string{
		"https://api.tensorlake.ai/sandboxes": "tensorlake",
		"https://docs.acme.dev/api":           "acme",
		"https://acme.co.uk/api":              "acme",
		"https://localhost/x":                 "",
		"not a url":                           "",
		"https://9lives.dev":                  "",
	} {
		if got := adaptName(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
	c := docsCandidates("https://api.tensorlake.ai/sandboxes")
	if c[0] != "https://docs.tensorlake.ai/llms.txt" || !slicesHas(c, "https://docs.tensorlake.ai/api-reference/openapi.yaml") ||
		!slicesHas(c, "https://api.tensorlake.ai/openapi.json") {
		t.Fatalf("candidates %v", c)
	}
	seen := map[string]bool{}
	for _, x := range c {
		if seen[x] {
			t.Fatalf("twice: %s", x)
		}
		seen[x] = true
	}
	if docsCandidates("::") != nil || docsCandidates("http://127.0.0.1:8080/x") != nil || docsCandidates("http://localhost/x") != nil {
		t.Fatal("candidates for nothing")
	}
	if n := docName(0, "https://docs.x.dev/api-reference/openapi.yaml"); n != "01-docs.x.dev-api-reference-openapi.yaml" {
		t.Fatal(n)
	}
	if env := strings.Join(keylessEnv("my-cloud"), " "); !strings.Contains(env, "MY_CLOUD_API_KEY=") || !strings.Contains(env, "DAYTONA_API_KEY=") {
		t.Fatal(env)
	}
}

package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/integration"
	"github.com/Amitgb14/conch/internal/proto"
)

// The key every test here holds, which must reach the service and
// nothing else: not the agent's prompt, its environment or its folder,
// nor anything published.
const semaphoreKey = "sekrit-semaphore-key"

// semaphoreSite is a CI service's docs and API on one test server: the
// docs at /openapi.yaml, a branch's runs at /runs behind the key.
func semaphoreSite(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/openapi.yaml":
			w.Header().Set("Content-Type", "application/yaml")
			w.Write([]byte("openapi: 3.1.0\n# " + strings.Repeat("the semaphore pipelines API ", 20)))
		case "/runs":
			mu.Lock()
			calls = append(calls, r.URL.RawQuery+" "+r.Header.Get("Authorization"))
			mu.Unlock()
			if r.Header.Get("Authorization") != "Bearer "+semaphoreKey {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"runs": []any{
				map[string]any{"id": "1", "status": "ok"}, map[string]any{"id": "2", "status": "broken"}, map[string]any{"id": "3", "status": "ok"}}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), calls...)
	}
}

func semaphoreFile(api string) string {
	return `format = 3
kind = "ci"
name = "semaphore"
label = "Semaphore"
api = "` + api + `"
key_env = "SEMAPHORE_TOKEN"
docs = "https://docs.semaphoreci.test/api"
adapted_by = "claude"
adapted = "2026-11-15"
note = "no stats endpoint"
[detect]
files = [".semaphore/semaphore.yml"]
[project]
id = "{owner}/{repo}"
[states]
ok = "pass"
broken = "fail"
[runs]
call = "GET /runs?branch={branch}"
items = "runs"
id = "id"
state = "status"
[failed_log]
call = "GET /runs/{run}/log"
text = "."
`
}

// gitProject is a checkout on github.com/me/proj, on main, for a live
// check to read the runs of.
func gitProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"remote", "add", "origin", "https://github.com/me/proj.git"}} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	return dir
}

// startIntegrationAgent is a server whose "agent" writes what write says
// into the folder it is started in, works for one look and is then done.
func startIntegrationAgent(t *testing.T, write func(dir string)) *adaptAgent {
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
			return proto.PaneInfo{ID: "p9", Name: p.Name}, nil
		case proto.MethodPaneList:
			a.looks++
			state := proto.AgentDone
			if a.looks == 1 {
				state = proto.AgentWorking
			}
			return proto.PaneList{Panes: []proto.PaneInfo{a4Pane("p9", state)}}, nil
		case proto.MethodAgentPrompt:
			var p proto.AgentPromptParams
			json.Unmarshal(msg.Params, &p)
			a.prompts = append(a.prompts, p.Text)
			write(integrationDraftDir("semaphore"))
			go conn.Write(a4Updated(proto.PaneInfo{ID: "p9", Agent: &proto.AgentStatus{Name: "claude", State: proto.AgentDone, Turn: 2}}))
			return proto.AgentPromptResult{ID: "p9", Agent: "claude", Turn: 2}, nil
		}
		return nil, nil
	})
	return a
}

func adaptEvents(t *testing.T, out string) []adaptEvent {
	t.Helper()
	var events []adaptEvent
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var e adaptEvent
		if json.Unmarshal([]byte(line), &e) != nil {
			t.Fatalf("not JSON: %q", line)
		}
		events = append(events, e)
	}
	return events
}

func eventKinds(events []adaptEvent) string {
	s := ""
	for _, e := range events {
		s += e.Event + " "
	}
	return s
}

// The agent never sees the key: not in its prompt, its pane's
// environment or its folder. conch checks the file with it, and only
// once the person has agreed to where it goes.
func TestIntegrationAdaptKeepsTheKeyFromTheAgent(t *testing.T) {
	_, stored := integrationEnv(t)
	site, calls := semaphoreSite(t)
	serveRegistry(t) // lists nothing
	project := gitProject(t)
	agent := startIntegrationAgent(t, func(dir string) {
		os.WriteFile(filepath.Join(dir, "semaphore.toml"), []byte(semaphoreFile(site.URL)), 0o600)
	})
	t.Setenv("SEMAPHORE_TOKEN", semaphoreKey)
	t.Setenv("CIRCLECI_TOKEN", "sekrit-circle")
	var asked []string
	adaptConfirm = func(q string) bool { asked = append(asked, q); return true }
	t.Cleanup(func() { adaptConfirm = askYes })

	var err error
	out, _ := a4Capture(t, "", func() {
		err = runIntegration([]string{"adapt", "-kind", "ci", "-agent", "codex", "-name", "semaphore", "-cwd", project, site.URL + "/openapi.yaml"})
	})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(agent.created) != 1 {
		t.Fatalf("panes %d", len(agent.created))
	}
	p := agent.created[0]
	dir := integrationDraftDir("semaphore")
	if p.Agent != "codex" || p.Cwd != dir || !p.NoProject || p.Name != "adapt semaphore" {
		t.Fatalf("pane %+v", p)
	}
	// The prompt holds the kind's contract and where the format is; no key.
	for _, want := range []string{`"semaphore"`, "kind = \"ci\"", "[runs] (required)", "[failed_log]", "pass, fail, pending, canceled",
		"INTEGRATION-FILE.md", "./semaphore.toml", "conch integration check ./semaphore.toml", site.URL + "/openapi.yaml"} {
		if !strings.Contains(p.Prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, p.Prompt)
		}
	}
	env := strings.Join(p.Env, "\n")
	for _, secret := range []string{semaphoreKey, "sekrit-circle"} {
		if strings.Contains(p.Prompt, secret) || strings.Contains(env, secret) {
			t.Fatalf("the key reached the agent: %q / %v", p.Prompt, p.Env)
		}
	}
	// The variables keys are in are emptied in its pane: this service's
	// likely one, and every integration's conch knows.
	for _, want := range []string{"SEMAPHORE_TOKEN=", "SEMAPHORE_API_KEY=", "CIRCLECI_TOKEN=", "GITLAB_TOKEN=", "GITHUB_TOKEN="} {
		if !slicesHas(p.Env, want) {
			t.Errorf("%s is not emptied: %v", want, p.Env)
		}
	}
	// ...and the Keychain is out of reach of conch run there.
	if !slicesHas(p.Env, "CONCH_SECURITY=false") {
		t.Errorf("the Keychain is reachable: %v", p.Env)
	}
	filepath.Walk(dir, func(path string, info os.FileInfo, _ error) error {
		if b, _ := os.ReadFile(path); info != nil && !info.IsDir() && strings.Contains(string(b), semaphoreKey) {
			t.Errorf("the key is in %s", path)
		}
		return nil
	})
	if b, _ := os.ReadFile(filepath.Join(dir, "INTEGRATION-FILE.md")); string(b) != integration.Guide {
		t.Fatal("the format is not in the folder")
	}
	if docs, _ := os.ReadDir(filepath.Join(dir, "docs")); len(docs) != 1 {
		t.Fatalf("docs %v", docs)
	}

	// The key went to the service once the person agreed to the pair.
	if len(asked) != 1 || !strings.Contains(asked[0], "$SEMAPHORE_TOKEN to "+site.URL) {
		t.Fatalf("asked %q", asked)
	}
	if c := calls(); len(c) != 1 || !strings.Contains(c[0], "branch=main") || !strings.Contains(c[0], semaphoreKey) {
		t.Fatalf("calls %q", c)
	}
	for _, want := range []string{"codex is writing the file in pane p9", "codex wrote", "runs, failed_log",
		"offline check: 0 problems", "live check: read 3 runs of me/proj on main", "conch integration adapt -save semaphore",
		"conch integration publish semaphore"} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, semaphoreKey) {
		t.Fatal("the key was printed")
	}
	if b, _ := os.ReadFile(stored); len(b) != 0 {
		t.Fatalf("adapting put something in the Keychain: %q", b)
	}
	// A draft is not an integration until it is kept.
	if _, _, err := integration.Source("semaphore"); err == nil {
		t.Fatal("a draft is an integration")
	}
	out, _ = a4Capture(t, "", func() { err = runIntegration([]string{"adapt", "-save", "semaphore"}) })
	if err != nil || !strings.Contains(out, "kept") {
		t.Fatalf("save: %v %q", err, out)
	}
	if _, _, err := integration.Source("semaphore"); err != nil {
		t.Fatalf("not kept: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("the draft is left behind")
	}
	// Adapting it again is refused while it is here.
	a4Capture(t, "", func() { err = runIntegration([]string{"adapt", "-name", "semaphore", site.URL}) })
	if err == nil || !strings.Contains(err.Error(), "already") {
		t.Fatalf("again: %v", err)
	}
}

// For the app nobody is asked, so nothing is sent: it says where the key
// would go, and the person's -check sends it.
func TestIntegrationAdaptJSONAsksBeforeTheKey(t *testing.T) {
	integrationEnv(t)
	site, calls := semaphoreSite(t)
	serveRegistry(t)
	project := gitProject(t)
	bad := strings.Replace(semaphoreFile(site.URL), `broken = "fail"`, `broken = "red"`, 1)
	startIntegrationAgent(t, func(dir string) {
		b := bad
		if _, err := os.Stat(filepath.Join(dir, "semaphore.toml")); err == nil {
			b = semaphoreFile(site.URL) // the revision fixes it
		}
		os.WriteFile(filepath.Join(dir, "semaphore.toml"), []byte(b), 0o600)
	})
	t.Setenv("SEMAPHORE_TOKEN", semaphoreKey)
	adaptConfirm = func(q string) bool { t.Errorf("asked in -json: %q", q); return false }
	t.Cleanup(func() { adaptConfirm = askYes })

	var err error
	out, _ := a4Capture(t, "", func() {
		err = runIntegration([]string{"adapt", "-json", "-name", "semaphore", site.URL + "/openapi.yaml"})
	})
	if err == nil || !strings.Contains(err.Error(), "-revise") {
		t.Fatalf("a bad file passed: %v\n%s", err, out)
	}
	events := adaptEvents(t, out)
	if k := eventKinds(events); k != "docs agent waiting file problems error " {
		t.Fatalf("events %q", k)
	}
	if f := events[3]; f.Kind != "ci" || strings.Join(f.Operations, ",") != "runs,failed_log" || !strings.Contains(f.Text, "[runs]") {
		t.Fatalf("file %+v", f)
	}
	if !strings.Contains(strings.Join(events[4].Problems, " "), "red") {
		t.Fatalf("problems %v", events[4].Problems)
	}
	// Keeping a wrong draft is refused.
	a4Capture(t, "", func() { err = runIntegration([]string{"adapt", "-save", "semaphore"}) })
	if err == nil {
		t.Fatal("kept a wrong draft")
	}

	// Revised, it is clean; the key is not sent, and where it would go is said.
	out, _ = a4Capture(t, "", func() {
		err = runIntegration([]string{"adapt", "-json", "-revise", "map broken to fail", "semaphore"})
	})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	events = adaptEvents(t, out)
	if k := eventKinds(events); k != "agent file offline consent done " {
		t.Fatalf("events %q", k)
	}
	if c := events[3]; !strings.Contains(c.Detail, "$SEMAPHORE_TOKEN to "+site.URL) {
		t.Fatalf("consent %+v", c)
	}
	if c := calls(); len(c) != 0 {
		t.Fatalf("the key went unasked: %q", c)
	}

	// -check is the person's yes.
	out, _ = a4Capture(t, "", func() {
		err = runIntegration([]string{"adapt", "-json", "-check", "-cwd", project, "semaphore"})
	})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	events = adaptEvents(t, out)
	if k := eventKinds(events); k != "file offline live done " {
		t.Fatalf("events %q", k)
	}
	if l := events[2].Live; l == nil || !l.OK || l.Runs != 3 || l.Project != "me/proj" || l.Branch != "main" {
		t.Fatalf("live %+v", events[2].Live)
	}
	if !events[3].OK || strings.Contains(out, semaphoreKey) {
		t.Fatalf("done %+v", events[3])
	}

	// No project to read: said, not failed.
	out, _ = a4Capture(t, "", func() {
		err = runIntegration([]string{"adapt", "-json", "-check", "-cwd", t.TempDir(), "semaphore"})
	})
	events = adaptEvents(t, out)
	if l := events[2].Live; err != nil || l == nil || l.OK || !strings.Contains(l.Why, "-cwd") {
		t.Fatalf("no project: %v %+v", err, l)
	}
	// A wrong key: what the service said.
	t.Setenv("SEMAPHORE_TOKEN", "wrong")
	out, _ = a4Capture(t, "", func() {
		runIntegration([]string{"adapt", "-json", "-check", "-cwd", project, "semaphore"})
	})
	if l := adaptEvents(t, out)[2].Live; l == nil || l.OK || !strings.Contains(l.Why, "401") {
		t.Fatalf("wrong key: %+v", l)
	}
	// No key at all.
	t.Setenv("SEMAPHORE_TOKEN", "")
	out, _ = a4Capture(t, "", func() {
		runIntegration([]string{"adapt", "-json", "-check", "-cwd", project, "semaphore"})
	})
	if l := adaptEvents(t, out)[2].Live; l == nil || l.OK || !strings.Contains(l.Why, "$SEMAPHORE_TOKEN") {
		t.Fatalf("no key: %+v", l)
	}
}

func TestIntegrationAdaptRefusals(t *testing.T) {
	integrationEnv(t)
	site, _ := semaphoreSite(t)
	reg := serveRegistry(t)
	reg.publish(t, testIntegration, 1, acme)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"adapt"}, "usage"},
		{[]string{"adapt", "-kind", "host", site.URL}, "no host integration files yet"},
		{[]string{"adapt", "-kind", "weird", site.URL}, "kind"},
		{[]string{"adapt", "-name", "Bad Name", site.URL}, "not an integration name"},
		{[]string{"adapt", "http://10.0.0.1:8080/x"}, "say it with -name"},
		// One conch ships: improving it is adapting anew, with -new.
		{[]string{"adapt", "-name", "circleci", site.URL}, "ships with conch"},
		// The registry has it: install, rather than spend an agent.
		{[]string{"adapt", "-name", "acme-ci", site.URL}, "conch integration install acme-ci"},
		{[]string{"adapt", "-revise", "x", "nope"}, "no draft of nope"},
		{[]string{"adapt", "-check", "nope"}, "no draft of nope"},
		{[]string{"adapt", "-save", "nope"}, "no draft of nope"},
		{[]string{"adapt", "-kind", "sandbox", "-check", "x"}, "conch sandbox provider check"},
	} {
		var err error
		out, _ := a4Capture(t, "", func() { err = runIntegration(c.args) })
		if c.want == "conch integration install acme-ci" {
			if err != nil || !strings.Contains(out, c.want) {
				t.Errorf("%v: %v %q", c.args, err, out)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: want %q, got %v", c.args, c.want, err)
		}
	}
	// No docs to read: nothing started.
	old := docsClient
	t.Cleanup(func() { docsClient = old })
	var err error
	a4Capture(t, "", func() { err = runIntegration([]string{"adapt", "-name", "nodocs", site.URL + "/nothing"}) })
	if err == nil || !strings.Contains(err.Error(), "found no docs") {
		t.Fatalf("no docs: %v", err)
	}
}

// -kind sandbox is conch sandbox adapt, which already does it.
func TestIntegrationAdaptSandboxIsSandboxAdapt(t *testing.T) {
	a4Env(t)
	site := adaptSite(t)
	agent := startAdaptAgent(t, func(dir string) {
		os.WriteFile(filepath.Join(dir, "provider.toml"), []byte(adaptFile(site.URL)), 0o600)
	})
	adaptConfirm = func(string) bool { return false }
	t.Cleanup(func() { adaptConfirm = askYes })
	var err error
	out, _ := a4Capture(t, "", func() {
		err = runIntegration([]string{"adapt", "-kind", "sandbox", "-name", "acme", "-new", site.URL + "/openapi.yaml"})
	})
	if err != nil || len(agent.created) != 1 || !strings.Contains(agent.created[0].Prompt, "sandbox provider") ||
		!strings.Contains(out, "conch sandbox provider save acme") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestIntegrationPublish(t *testing.T) {
	home, _ := integrationEnv(t)
	site, calls := semaphoreSite(t)
	reg := serveRegistry(t)
	reg.publish(t, testIntegration, 1, acme) // another integration only
	project := gitProject(t)
	old := publishNow
	publishNow = func() time.Time { return time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { publishNow = old })
	file := semaphoreFile(site.URL)
	path := filepath.Join(home, "integrations", "semaphore.toml")
	os.MkdirAll(filepath.Dir(path), 0o700)
	var asked []string
	adaptConfirm = func(q string) bool { asked = append(asked, q); return true }
	t.Cleanup(func() { adaptConfirm = askYes })

	for _, c := range []struct {
		setup func()
		args  []string
		want  string
	}{
		{nil, []string{"publish"}, "usage"},
		{nil, []string{"publish", "semaphore"}, "no integration semaphore"},
		{func() { os.WriteFile(path, []byte("format = 3\nname = \"semaphore\"\n"), 0o600) }, []string{"publish", "semaphore"}, "problems to fix first"},
		{func() {
			os.WriteFile(path, []byte(strings.Replace(file, "docs = \"https://docs.semaphoreci.test/api\"\n", "", 1)), 0o600)
		},
			[]string{"publish", "semaphore"}, "docs"},
		// Published files are ones that worked: no key, no proposal.
		{func() { os.WriteFile(path, []byte(file), 0o600) }, []string{"publish", "-cwd", project, "semaphore"}, "with your key first"},
	} {
		if c.setup != nil {
			c.setup()
		}
		var err error
		a4Capture(t, "", func() { err = runIntegration(c.args) })
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", c.want, err)
		}
	}

	t.Setenv("SEMAPHORE_TOKEN", semaphoreKey)
	// Saying no to where the key goes publishes nothing.
	adaptConfirm = func(q string) bool { asked = append(asked, q); return false }
	var err error
	a4Capture(t, "", func() { err = runIntegration([]string{"publish", "-cwd", project, "semaphore"}) })
	if err == nil || !strings.Contains(err.Error(), "nothing published") || len(calls()) != 0 {
		t.Fatalf("no: %v %q", err, calls())
	}
	if !strings.Contains(asked[len(asked)-1], "$SEMAPHORE_TOKEN to "+site.URL) {
		t.Fatalf("asked %q", asked)
	}
	adaptConfirm = func(q string) bool { asked = append(asked, q); return true }

	out, _ := a4Capture(t, "", func() { err = runIntegration([]string{"publish", "-dry-run", "-cwd", project, "semaphore"}) })
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{"would propose to Amitgb14/conch-integrations: Add Semaphore (ci)",
		"--- ci/semaphore/integration.toml", "--- ci/semaphore/meta.json", `"version": 1`, `"checked": null`, `"conch": "`,
		`"docs": "https://`, "- operations: runs, failed_log", "read 3 runs of me/proj on main", "with conch "} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, semaphoreKey) {
		t.Fatal("the key is in what would be published")
	}

	// Through a fork, for somebody who cannot push to the registry.
	log := fakeGH(t, false)
	out, _ = a4Capture(t, "", func() { err = runIntegration([]string{"publish", "-y", "-json", "-cwd", project, "semaphore"}) })
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var res struct {
		URL     string `json:"url"`
		Version int    `json:"version"`
	}
	if json.Unmarshal([]byte(out), &res) != nil || !strings.HasSuffix(res.URL, "/pull/7") || res.Version != 1 {
		t.Fatal(out)
	}
	b, _ := os.ReadFile(log)
	logged := string(b)
	for _, want := range []string{
		"api --method POST repos/Amitgb14/conch-integrations/forks",
		`"ref":"refs/heads/integration-semaphore-20261115-120000"`,
		"api --method PUT repos/me/conch-integrations/contents/ci/semaphore/integration.toml",
		"api --method PUT repos/me/conch-integrations/contents/ci/semaphore/meta.json",
		`"head":"me:integration-semaphore-20261115-120000"`,
		"api --method POST repos/Amitgb14/conch-integrations/pulls",
		"read 3 runs of me/proj on main", // the live check's result, in the body
	} {
		if !strings.Contains(logged, want) {
			t.Errorf("no %q in\n%s", want, logged)
		}
	}
	// The file goes as it is, and the key goes nowhere.
	if !strings.Contains(logged, base64.StdEncoding.EncodeToString([]byte(file))) || strings.Contains(logged, semaphoreKey) {
		t.Fatal("the file was changed, or the key sent")
	}

	// Listed already: the next version, and the same file is not proposed again.
	sum := sha256Hex([]byte(file))
	listed := integration.Entry{Kind: "ci", Name: "semaphore", Label: "Semaphore", Version: 4, Format: 3, KeyEnv: "SEMAPHORE_TOKEN",
		File: "integrations/semaphore.toml", SHA256: "old", Revoked: []integration.Revocation{{Version: 5, Reason: "bad"}}}
	ix, _ := json.Marshal(integration.Index{Format: 2, Entries: []integration.Entry{listed}})
	reg.mu.Lock()
	reg.files["index.json"] = ix
	reg.mu.Unlock()
	out, _ = a4Capture(t, "", func() { err = runIntegration([]string{"publish", "-y", "-dry-run", "-cwd", project, path}) })
	if err != nil || !strings.Contains(out, `"version": 6`) || !strings.Contains(out, "Update Semaphore to version 6") {
		t.Fatalf("next version: %v\n%s", err, out)
	}
	listed.SHA256 = sum
	ix, _ = json.Marshal(integration.Index{Format: 2, Entries: []integration.Entry{listed}})
	reg.mu.Lock()
	reg.files["index.json"] = ix
	reg.mu.Unlock()
	a4Capture(t, "", func() { err = runIntegration([]string{"publish", "-y", "-cwd", project, "semaphore"}) })
	if err == nil || !strings.Contains(err.Error(), "has this file already") {
		t.Fatalf("same file: %v", err)
	}
	// The registry cannot be read: no version can be numbered.
	t.Setenv("CONCH_PROVIDERS_URL", "http://127.0.0.1:1")
	a4Capture(t, "", func() { err = runIntegration([]string{"publish", "-y", "-cwd", project, "semaphore"}) })
	if err == nil || !strings.Contains(err.Error(), "could not read") {
		t.Fatalf("no registry: %v", err)
	}
	// A sandbox provider is published as one.
	a4Capture(t, "", func() { err = runIntegration([]string{"publish", "-kind", "sandbox"}) })
	if err == nil {
		t.Fatal("publish took -kind")
	}
}

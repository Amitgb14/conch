package cloudagent

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeGitProxy asks for basic auth as the git proxy does and records what
// it was given, by path.
type fakeGitProxy struct {
	*httptest.Server
	mu   sync.Mutex
	seen map[string]string // path → the password it was given
}

func newFakeGitProxy(t *testing.T) *fakeGitProxy {
	p := &fakeGitProxy{seen: map[string]string{}}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, pass, ok := r.BasicAuth()
		if !ok {
			w.Header().Set("WWW-Authenticate", `Basic realm="conch git proxy"`)
			w.WriteHeader(401)
			return
		}
		p.mu.Lock()
		p.seen[r.URL.Path] = pass
		p.mu.Unlock()
		w.WriteHeader(404) // nothing to clone: what was sent is the test
	}))
	t.Cleanup(p.Close)
	return p
}

func (p *fakeGitProxy) password(path string) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	v, ok := p.seen[path]
	return v, ok
}

func git(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "SSH_ASKPASS=")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestGitThroughTheProxy: github.com, by https or ssh name, goes to the git
// proxy, which is given the machine token by the agent — from memory, and
// to no other host.
func TestGitThroughTheProxy(t *testing.T) {
	r := newRig(t)
	proxy := newFakeGitProxy(t)
	exe, _ := os.Executable()
	r.opts.GitConfig = nil // the real git, on GIT_CONFIG_SYSTEM (isolate)
	r.opts.Executable = exe
	r.cloud.handle("POST /v1/vm/hello", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"machine_id": testMachine, "relay_url": "ws://127.0.0.1:1", "room_secret": "s",
			"sleep": map[string]any{}, "git_proxy": proxy.URL})
	})
	// What an earlier agent left for another proxy is replaced.
	sys := os.Getenv("GIT_CONFIG_SYSTEM")
	os.WriteFile(sys, []byte("[url \"http://old.example/github.com/\"]\n\tinsteadOf = https://github.com/\n[user]\n\tname = kept\n"), 0o644)
	r.startServer()
	r.run()
	r.cloud.waitFor("POST /v1/vm/hello", 1, 10*time.Second)
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(r.log.String(), "git reaches github.com") {
		if time.Now().After(deadline) {
			t.Fatalf("git not configured: %s", r.log)
		}
		time.Sleep(20 * time.Millisecond)
	}
	conf, _ := os.ReadFile(sys)
	if bytes.Contains(conf, []byte("old.example")) || !bytes.Contains(conf, []byte("name = kept")) {
		t.Fatalf("system config:\n%s", conf)
	}
	if bytes.Contains(conf, []byte("ccm_first")) {
		t.Fatal("the token is in git's configuration")
	}

	for _, remote := range []string{"https://github.com/acme/api.git", "git@github.com:acme/web.git", "ssh://git@github.com/acme/cli.git"} {
		out, _ := git(t, r.home, "ls-remote", remote)
		name := remote[strings.LastIndex(remote, "/")+1:]
		want := "/github.com/acme/" + name + "/info/refs"
		pass, ok := proxy.password(want)
		if !ok || pass != "ccm_first" {
			t.Errorf("%s: the proxy was given %q at %s (%v); git said:\n%s", remote, pass, want, ok, out)
		}
	}

	// The helper is the proxy's alone: another host is not given the token.
	other := newFakeGitProxy(t)
	git(t, r.home, "ls-remote", other.URL+"/acme/api.git")
	if pass, ok := other.password("/acme/api.git/info/refs"); ok {
		t.Fatalf("another host was given %q", pass)
	}
}

func TestGitCredentialHelper(t *testing.T) {
	r := newRig(t)
	r.startServer()
	r.run()
	r.cloud.waitFor("POST /v1/vm/hello", 1, 10*time.Second)
	var out bytes.Buffer
	if err := GitCredential([]string{"-socket", r.opts.AgentSocket, "get"}, strings.NewReader("protocol=http\nhost=proxy\n\n"), &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "username=conch\npassword=ccm_first\n" {
		t.Fatalf("answered %q", out.String())
	}
	out.Reset()
	if err := GitCredential([]string{"-socket", r.opts.AgentSocket, "store"}, strings.NewReader("password=x\n\n"), &out); err != nil || out.Len() != 0 {
		t.Fatalf("store: %q %v", out.String(), err)
	}
	// Never the default socket: on a cloud machine that is the real agent.
	if err := GitCredential([]string{"-socket", filepath.Join(t.TempDir(), "none.sock"), "get"}, strings.NewReader(""), &out); err == nil {
		t.Fatal("no agent on the socket, and no error")
	}
	if err := GitCredential(nil, strings.NewReader(""), &out); err == nil {
		t.Fatal("no operation: no error")
	}
}

// TestGitProxyWithAPath: a proxy whose URL has a path is still given the
// token (git matches a credential URL by its path as a prefix), a git:// name goes through it too, and a helper an earlier agent left for
// a proxy that has since moved is taken away.
func TestGitProxyWithAPath(t *testing.T) {
	r := newRig(t)
	proxy := newFakeGitProxy(t)
	exe, _ := os.Executable()
	r.opts.GitConfig = nil
	r.opts.Executable = exe
	r.cloud.handle("POST /v1/vm/hello", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"machine_id": testMachine, "relay_url": "ws://127.0.0.1:1", "room_secret": "s",
			"sleep": map[string]any{}, "git_proxy": proxy.URL + "/gp"})
	})
	sys := os.Getenv("GIT_CONFIG_SYSTEM")
	os.WriteFile(sys, []byte("[credential \"http://old.example\"]\n\thelper = \n\thelper = !'/usr/bin/conch' cloud git-credential -socket '/run/x'\n[credential \"https://mine.example\"]\n\thelper = store\n"), 0o644)
	r.startServer()
	r.run()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(r.log.String(), "git reaches github.com") {
		if time.Now().After(deadline) {
			t.Fatalf("git not configured: %s", r.log)
		}
		time.Sleep(20 * time.Millisecond)
	}
	conf, _ := os.ReadFile(sys)
	if bytes.Contains(conf, []byte("old.example")) || !bytes.Contains(conf, []byte("helper = store")) {
		t.Fatalf("system config:\n%s", conf)
	}
	for _, remote := range []string{"https://github.com/acme/api.git", "git://github.com/acme/old.git"} {
		out, _ := git(t, r.home, "ls-remote", remote)
		want := "/gp/github.com/acme/" + remote[strings.LastIndex(remote, "/")+1:] + "/info/refs"
		if pass, ok := proxy.password(want); !ok || pass != "ccm_first" {
			t.Errorf("%s: the proxy was given %q at %s (%v); git said:\n%s", remote, pass, want, ok, out)
		}
	}
}

func TestGitProxyAnswers(t *testing.T) {
	serve := func(status int, auth string) string {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "" {
				t.Errorf("the probe sent credentials: %q", r.Header.Get("Authorization"))
			}
			if auth != "" {
				w.Header().Set("WWW-Authenticate", auth)
			}
			w.WriteHeader(status)
		}))
		t.Cleanup(s.Close)
		return s.URL
	}
	gone := httptest.NewServer(http.NotFoundHandler())
	gone.Close()
	cases := []struct {
		name string
		url  string
		want bool
	}{
		{"asks for the token", serve(401, `Basic realm="conch-cloud git"`), true},
		{"asks, lower case", serve(401, `basic realm="x"`), true},
		{"with a path and a slash", serve(401, "Basic") + "/git/", true},
		{"gateway with nothing behind it", serve(502, ""), false},
		{"401 asking for something else", serve(401, "Bearer"), false},
		{"401 asking nothing", serve(401, ""), false},
		{"answers without asking", serve(200, ""), false},
		{"not found", serve(404, ""), false},
		{"nobody listening", gone.URL, false},
		{"not a URL", "://nope", false},
	}
	for _, c := range cases {
		ok, why := gitProxyAnswers(context.Background(), nil, c.url)
		if ok != c.want {
			t.Errorf("%s: %v (%s), want %v", c.name, ok, why, c.want)
		}
		if !ok && why == "" {
			t.Errorf("%s: refused with no reason", c.name)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if ok, _ := gitProxyAnswers(ctx, nil, serve(401, "Basic")); ok {
		t.Fatal("a cancelled context answered")
	}
}

// gitAgent is an agent with just enough to set git up: the real git, on
// the isolated GIT_CONFIG_SYSTEM.
func gitAgent(t *testing.T) (*Agent, string, *bytes.Buffer) {
	t.Helper()
	home := t.TempDir()
	sys := filepath.Join(home, "gitconfig-system")
	t.Setenv("GIT_CONFIG_SYSTEM", sys)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, "gitconfig-global"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "")
	var log bytes.Buffer
	var mu sync.Mutex
	a := &Agent{opts: Options{Executable: "/usr/bin/conch", AgentSocket: "/run/x.sock", Logf: func(f string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprintf(&log, f+"\n", args...)
	}}}
	return a, sys, &log
}

// resolved is where git would fetch remote from, asked without the network.
func resolved(t *testing.T, remote string) string {
	t.Helper()
	out, err := git(t, t.TempDir(), "ls-remote", "--get-url", remote)
	if err != nil {
		t.Fatalf("ls-remote --get-url: %v: %s", err, out)
	}
	return strings.TrimSpace(out)
}

// TestGitProxyDownLeavesGithubAlone: a control plane can name a git proxy
// it does not run (no GitHub App), behind a gateway answering 502.
// github.com must not be sent there — every clone failed, public
// repositories too — and what an earlier agent set for it is taken away.
func TestGitProxyDownLeavesGithubAlone(t *testing.T) {
	a, sys, log := gitAgent(t)
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(502) }))
	defer down.Close()
	// Set when the proxy last answered, plus somebody's own settings.
	os.WriteFile(sys, []byte("[url \""+down.URL+"/github.com/\"]\n\tinsteadOf = https://github.com/\n\tinsteadOf = git@github.com:\n"+
		"[url \"https://mirror.example/\"]\n\tinsteadOf = https://gitlab.example/\n"+
		"[credential \""+down.URL+"\"]\n\thelper = \n\thelper = !'/usr/bin/conch' cloud git-credential -socket '/run/x.sock'\n"+
		"[credential \"https://mine.example\"]\n\thelper = store\n[user]\n\tname = kept\n"), 0o644)

	a.applyGit(context.Background(), down.URL)

	if got := resolved(t, "https://github.com/acme/bot.git"); got != "https://github.com/acme/bot.git" {
		t.Fatalf("github.com still goes to %s", got)
	}
	if got := resolved(t, "git@github.com:acme/bot.git"); got != "git@github.com:acme/bot.git" {
		t.Fatalf("the ssh form still goes to %s", got)
	}
	conf, _ := os.ReadFile(sys)
	for _, gone := range []string{"cloud git-credential", down.URL} {
		if bytes.Contains(conf, []byte(gone)) {
			t.Errorf("%q is still in the system config:\n%s", gone, conf)
		}
	}
	for _, kept := range []string{"mirror.example", "helper = store", "name = kept"} {
		if !bytes.Contains(conf, []byte(kept)) {
			t.Errorf("%q was taken away:\n%s", kept, conf)
		}
	}
	if !strings.Contains(log.String(), "does not answer (502") {
		t.Errorf("log: %s", log)
	}
	if a.gitProxy != "" {
		t.Errorf("recorded as in use: %q", a.gitProxy)
	}
}

// TestGitProxyComesUpLater: the next hello asks again, and a proxy that
// answers now is used; once in use it is not asked again, and a hello
// that names none takes it away.
func TestGitProxyComesUpLater(t *testing.T) {
	a, sys, _ := gitAgent(t)
	var mu sync.Mutex
	up, asked := false, 0
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		asked++
		if !up {
			w.WriteHeader(502)
			return
		}
		w.Header().Set("WWW-Authenticate", "Basic")
		w.WriteHeader(401)
	}))
	defer proxy.Close()
	ctx := context.Background()

	a.applyGit(ctx, proxy.URL)
	if got := resolved(t, "https://github.com/acme/bot.git"); got != "https://github.com/acme/bot.git" {
		t.Fatalf("down, and github.com goes to %s", got)
	}
	mu.Lock()
	up = true
	mu.Unlock()
	a.applyGit(ctx, proxy.URL)
	if got, want := resolved(t, "https://github.com/acme/bot.git"), proxy.URL+"/github.com/acme/bot.git"; got != want {
		t.Fatalf("up, and github.com goes to %s, want %s", got, want)
	}
	mu.Lock()
	before := asked
	mu.Unlock()
	a.applyGit(ctx, proxy.URL)
	mu.Lock()
	if asked != before {
		t.Errorf("a proxy in use was asked again")
	}
	mu.Unlock()

	a.applyGit(ctx, "")
	if got := resolved(t, "https://github.com/acme/bot.git"); got != "https://github.com/acme/bot.git" {
		t.Fatalf("no proxy named, and github.com goes to %s", got)
	}
	if conf, _ := os.ReadFile(sys); bytes.Contains(conf, []byte("git-credential")) {
		t.Fatalf("the helper stayed:\n%s", conf)
	}
}

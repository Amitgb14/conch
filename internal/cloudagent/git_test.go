package cloudagent

import (
	"bytes"
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

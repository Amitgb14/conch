package sandbox_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/sandbox"
	"github.com/Amitgb14/conch/internal/sandbox/sandboxdtest"
)

// sandboxdEnv keeps a test away from any sandboxd of the developer's.
func sandboxdEnv(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, v := range []string{"XDG_RUNTIME_DIR", "XDG_CONFIG_HOME", "SANDBOX_CONTEXT", "SANDBOXD_TOKEN"} {
		t.Setenv(v, "")
	}
	return home
}

const testToken = "tok-0123456789abcdef"

// fakeSandboxd is a fake on TCP with the provider set up for it.
func fakeSandboxd(t *testing.T) (*sandboxdtest.Server, *sandbox.Sandboxd) {
	t.Helper()
	sandboxdEnv(t)
	f := sandboxdtest.New(t)
	f.Token = testToken
	t.Setenv("SANDBOXD_TOKEN", testToken)
	p := sandbox.NewSandboxd(config.ProviderCfg{APIURL: f.URL})
	if err := p.Check(); err != nil {
		t.Fatal(err)
	}
	sandbox.SandboxdPollForTest(t, time.Millisecond)
	return f, p
}

func TestSandboxdIsAProvider(t *testing.T) {
	sandboxdEnv(t)
	if !sandbox.Known("sandbox-cli") || sandbox.ProviderLabel("sandbox-cli") != "sandbox-cli" {
		t.Fatal("not known")
	}
	p, err := sandbox.Open("sandbox-cli", nil)
	if err != nil || p.Name() != "sandbox-cli" {
		t.Fatalf("open: %v %v", p, err)
	}
	for _, ok := range []bool{
		func() bool { _, ok := p.(sandbox.Execer); return ok }(),
		func() bool { _, ok := p.(sandbox.Streamer); return ok }(),
		func() bool { _, ok := p.(sandbox.Endpointed); return ok }(),
		func() bool { _, ok := p.(sandbox.Binder); return ok }(),
	} {
		if !ok {
			t.Fatalf("%T lacks an interface", p)
		}
	}
	if _, err := p.SSHAccess(context.Background(), "x"); err == nil {
		t.Fatal("ssh access to a sandbox with no ssh")
	}
}

func TestSandboxdEndpoint(t *testing.T) {
	home := sandboxdEnv(t)
	cfgDir := filepath.Join(home, ".config", "sandbox")
	os.MkdirAll(cfgDir, 0o755)

	// Nothing set: sandbox-cli's local socket, which isn't there.
	p := sandbox.NewSandboxd(config.ProviderCfg{})
	if p.Endpoint() != "unix://"+filepath.Join(cfgDir, "sandboxd.sock") || !p.CanBind() {
		t.Fatalf("default %q", p.Endpoint())
	}
	if err := p.Check(); !errors.Is(err, sandbox.ErrNotConfigured) || !strings.Contains(err.Error(), "sandboxd isn't running here") {
		t.Fatalf("no socket: %v", err)
	}
	// $XDG_RUNTIME_DIR moves it, as it does for sandbox-cli.
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/7")
	if p := sandbox.NewSandboxd(config.ProviderCfg{}); p.Endpoint() != "unix:///run/user/7/sandboxd.sock" {
		t.Fatalf("runtime dir %q", p.Endpoint())
	}
	t.Setenv("XDG_RUNTIME_DIR", "")

	// A context of sandbox-cli's: the current one, or the one named.
	tok := filepath.Join(home, "box.token")
	os.WriteFile(tok, []byte(" "+testToken+"\n"), 0o600)
	os.WriteFile(filepath.Join(cfgDir, "contexts.json"), []byte(`{"current":"box","contexts":{
		"box":{"endpoint":"https://box.test:7443","token_file":"`+tok+`"},
		"bad":{"endpoint":"https://bad.test","token_file":"/nope"}}}`), 0o600)
	p = sandbox.NewSandboxd(config.ProviderCfg{})
	if p.Endpoint() != "https://box.test:7443" || p.CanBind() || p.Check() != nil {
		t.Fatalf("current context: %q %v", p.Endpoint(), p.Check())
	}
	if p := sandbox.NewSandboxd(config.ProviderCfg{Target: "local"}); !strings.HasPrefix(p.Endpoint(), "unix://") {
		t.Fatalf("named local: %q", p.Endpoint())
	}
	t.Setenv("SANDBOX_CONTEXT", "local")
	if p := sandbox.NewSandboxd(config.ProviderCfg{}); !strings.HasPrefix(p.Endpoint(), "unix://") {
		t.Fatalf("$SANDBOX_CONTEXT: %q", p.Endpoint())
	}
	t.Setenv("SANDBOX_CONTEXT", "")
	for target, want := range map[string]string{"nope": `no context "nope"`, "bad": "/nope"} {
		if err := sandbox.NewSandboxd(config.ProviderCfg{Target: target}).Check(); !errors.Is(err, sandbox.ErrNotConfigured) || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", target, err)
		}
	}
	os.WriteFile(filepath.Join(cfgDir, "contexts.json"), []byte("{not json"), 0o600)
	if err := sandbox.NewSandboxd(config.ProviderCfg{}).Check(); err == nil || !strings.Contains(err.Error(), "contexts.json") {
		t.Fatalf("bad json: %v", err)
	}

	// An endpoint in the settings wins, and needs a token unless local.
	p = sandbox.NewSandboxd(config.ProviderCfg{APIURL: "https://mine.test"})
	if err := p.Check(); !errors.Is(err, sandbox.ErrNotConfigured) || !strings.Contains(err.Error(), "needs a token") {
		t.Fatalf("no token: %v", err)
	}
	t.Setenv("MY_TOKEN", testToken)
	if err := sandbox.NewSandboxd(config.ProviderCfg{APIURL: "https://mine.test", APIKeyEnv: "MY_TOKEN"}).Check(); err != nil {
		t.Fatalf("token from a variable: %v", err)
	}
	if err := sandbox.NewSandboxd(config.ProviderCfg{APIURL: "https://mine.test", APIKey: testToken}).Check(); err != nil {
		t.Fatalf("token kept: %v", err)
	}
	for url, want := range map[string]string{"ftp://x": "give http(s)", "unix://": "names no socket"} {
		if err := sandbox.NewSandboxd(config.ProviderCfg{APIURL: url}).Check(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", url, err)
		}
	}
	if err := sandbox.NewSandboxd(config.ProviderCfg{APIURL: "https://x", APIKey: testToken, CAFile: "/nope"}).Check(); err == nil || !strings.Contains(err.Error(), "ca_file") {
		t.Fatalf("missing CA: %v", err)
	}
	notPEM := filepath.Join(home, "ca.pem")
	os.WriteFile(notPEM, []byte("not a certificate"), 0o600)
	if err := sandbox.NewSandboxd(config.ProviderCfg{APIURL: "https://x", APIKey: testToken, CAFile: notPEM}).Check(); err == nil || !strings.Contains(err.Error(), "no certificates") {
		t.Fatalf("bad CA: %v", err)
	}
}

// A sandboxd on this computer is a unix socket, asked as localhost.
func TestSandboxdOverASocket(t *testing.T) {
	sandboxdEnv(t)
	dir, err := os.MkdirTemp("", "sbx")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	f := sandboxdtest.NewUnix(t, filepath.Join(dir, "d.sock"))
	p := sandbox.NewSandboxd(config.ProviderCfg{APIURL: f.URL})
	if err := p.Check(); err != nil {
		t.Fatal(err)
	}
	f.Add(sandboxdtest.Box{ID: "sbx_1", Labels: map[string]string{"conch": "1"}})
	list, err := p.List(context.Background())
	if err != nil || len(list) != 1 || list[0].ID != "sbx_1" {
		t.Fatalf("list %+v %v", list, err)
	}
}

func TestSandboxdCreate(t *testing.T) {
	f, p := fakeSandboxd(t)
	f.Pending = 2
	got, err := p.Create(context.Background(), sandbox.Spec{Snapshot: "my-image", CPU: 2, Memory: 4, Disk: 10,
		Env: map[string]string{"TOKEN": "v"}, Labels: map[string]string{"task": "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.State != sandbox.StateStarted || got.CPU != 2 || !strings.HasPrefix(got.ID, "sbx_") {
		t.Fatalf("created %+v", got)
	}
	body := f.Creates()[0]
	if body["image"] != "my-image" || body["cpus"] != 2.0 || body["memory_mb"] != 4096.0 || body["disk_mb"] != 10240.0 ||
		body["idle_timeout_secs"] != float64(7*24*3600) || body["network"] != nil || body["bind"] != nil {
		t.Fatalf("body %v", body)
	}
	if l := body["labels"].(map[string]any); l["conch"] != "1" || l["task"] != "x" {
		t.Fatalf("labels %v", l)
	}
	if env := body["env"].(map[string]any); env["TOKEN"] != "v" {
		t.Fatalf("env %v", env)
	}

	// The settings' image, network and idle; nothing more than asked.
	f2 := sandboxdtest.New(t)
	t.Setenv("SANDBOXD_TOKEN", "")
	p2 := sandbox.NewSandboxd(config.ProviderCfg{APIURL: f2.URL, APIKey: testToken, Snapshot: "from-settings",
		Network: "allowlist", Allow: []string{"api.anthropic.com"}})
	if _, err := p2.Create(context.Background(), sandbox.Spec{AutoStop: 90}); err != nil {
		t.Fatal(err)
	}
	body = f2.Creates()[0]
	if body["image"] != "from-settings" || body["idle_timeout_secs"] != 5400.0 || body["cpus"] != nil || body["env"] != nil {
		t.Fatalf("defaults %v", body)
	}
	if n := body["network"].(map[string]any); n["mode"] != "allowlist" || fmt.Sprint(n["allow"]) != "[api.anthropic.com]" {
		t.Fatalf("network %v", n)
	}
	// A mode with no names leaves the list to sandboxd, rather than an
	// empty one it would refuse.
	p3 := sandbox.NewSandboxd(config.ProviderCfg{APIURL: f2.URL, APIKey: testToken, Network: "open"})
	p3.Create(context.Background(), sandbox.Spec{})
	if n := f2.Creates()[1]["network"].(map[string]any); n["mode"] != "open" || n["allow"] != nil {
		t.Fatalf("open %v", n)
	}
}

func TestSandboxdCreateBindsAFolder(t *testing.T) {
	sandboxdEnv(t)
	dir, _ := os.MkdirTemp("", "sbx")
	t.Cleanup(func() { os.RemoveAll(dir) })
	f := sandboxdtest.NewUnix(t, filepath.Join(dir, "d.sock"))
	p := sandbox.NewSandboxd(config.ProviderCfg{APIURL: f.URL})
	work := t.TempDir()
	if _, err := p.Create(context.Background(), sandbox.Spec{Dir: work}); err != nil {
		t.Fatal(err)
	}
	if b := f.Creates()[0]["bind"].(map[string]any); b["host_path"] != work {
		t.Fatalf("bind %v", b)
	}
	if _, err := p.Create(context.Background(), sandbox.Spec{Dir: filepath.Join(work, "missing")}); err == nil || !strings.Contains(err.Error(), "is not a folder") {
		t.Fatalf("missing: %v", err)
	}
	// Over the network there is no folder of this computer to mount.
	_, tcp := fakeSandboxd(t)
	if _, err := tcp.Create(context.Background(), sandbox.Spec{Dir: work}); err == nil || !strings.Contains(err.Error(), "only be mounted when sandboxd runs on this computer") {
		t.Fatalf("remote bind: %v", err)
	}
}

func TestSandboxdCreateFails(t *testing.T) {
	f, p := fakeSandboxd(t)
	f.Fail["POST /v1/sandboxes"] = [2]string{"403", "refused"}
	if _, err := p.Create(context.Background(), sandbox.Spec{}); err == nil || !strings.Contains(err.Error(), "create sandbox: sandboxd: told to fail (403)") {
		t.Fatalf("refused: %v", err)
	}
	delete(f.Fail, "POST /v1/sandboxes")
	// One that never runs is given up on with its ID, so it can be deleted.
	f.Pending = 1 << 30
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	got, err := p.Create(ctx, sandbox.Spec{})
	if err == nil || got.ID == "" || !strings.Contains(err.Error(), "still starting") {
		t.Fatalf("slow: %+v %v", got, err)
	}
	// A wrong token.
	bad := sandbox.NewSandboxd(config.ProviderCfg{APIURL: f.URL, APIKey: "wrong-token-wrong-token"})
	if _, err := bad.List(context.Background()); err == nil || !strings.Contains(err.Error(), "(401)") {
		t.Fatalf("bad token: %v", err)
	}
	// Nothing listening.
	gone := sandbox.NewSandboxd(config.ProviderCfg{APIURL: "http://127.0.0.1:1", APIKey: testToken})
	if _, err := gone.List(context.Background()); err == nil || !strings.Contains(err.Error(), "sandboxd at http://127.0.0.1:1") {
		t.Fatalf("down: %v", err)
	}
}

func TestSandboxdGetListAndStates(t *testing.T) {
	f, p := fakeSandboxd(t)
	ours := map[string]string{"conch": "1"}
	f.Add(sandboxdtest.Box{ID: "sbx_run", State: "running", Labels: ours, CPUs: 1.5, MemoryMB: 2048})
	f.Add(sandboxdtest.Box{ID: "sbx_sus", State: "suspended", Labels: ours})
	f.Add(sandboxdtest.Box{ID: "sbx_new", State: "pending", Labels: ours})
	f.Add(sandboxdtest.Box{ID: "sbx_end", State: "terminated", Labels: ours})
	f.Add(sandboxdtest.Box{ID: "sbx_yours", State: "running"})
	ctx := context.Background()
	got, err := p.Get(ctx, "sbx_run")
	if err != nil || got.State != sandbox.StateStarted || got.CPU != 2 || got.Memory != 2 || got.Created.IsZero() {
		t.Fatalf("get %+v %v", got, err)
	}
	for id, want := range map[string]sandbox.State{"sbx_sus": sandbox.StateStopped, "sbx_end": sandbox.StateDestroyed} {
		if got, _ := p.Get(ctx, id); got.State != want {
			t.Errorf("%s: %s", id, got.State)
		}
	}
	for _, id := range []string{"sbx_yours", "sbx_missing", "", "../capabilities", "a?b"} {
		if _, err := p.Get(ctx, id); !errors.Is(err, sandbox.ErrNotFound) {
			t.Errorf("%q: %v", id, err)
		}
	}
	list, err := p.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, s := range list {
		ids = append(ids, s.ID)
	}
	// conch's own, newest first, and not the terminated one.
	if strings.Join(ids, ",") != "sbx_new,sbx_sus,sbx_run" {
		t.Fatalf("list %v", ids)
	}
	if !strings.Contains(f.Called(), "GET /v1/sandboxes?label=conch%3D1") {
		t.Fatalf("calls:\n%s", f.Called())
	}
}

func TestSandboxdStartStopDelete(t *testing.T) {
	f, p := fakeSandboxd(t)
	ctx := context.Background()
	f.Add(sandboxdtest.Box{ID: "sbx_1", Labels: map[string]string{"conch": "1"}})

	// A backend that can't suspend (a Mac's) can only delete.
	if err := p.Stop(ctx, "sbx_1"); !errors.Is(err, sandbox.ErrCannotStop) {
		t.Fatalf("no suspend: %v", err)
	}
	f.Caps["suspend"] = true
	p = sandbox.NewSandboxd(config.ProviderCfg{APIURL: f.URL}) // asks afresh
	if err := p.Stop(ctx, "sbx_1"); err != nil || f.State("sbx_1") != "suspended" {
		t.Fatalf("stop: %v %s", err, f.State("sbx_1"))
	}
	if err := p.Stop(ctx, "sbx_1"); err != nil || strings.Count(f.Called(), "/suspend") != 1 {
		t.Fatalf("stop again asked again:\n%s", f.Called())
	}
	got, err := p.Start(ctx, "sbx_1")
	if err != nil || got.State != sandbox.StateStarted || f.State("sbx_1") != "running" {
		t.Fatalf("start: %+v %v", got, err)
	}
	if _, err := p.Start(ctx, "sbx_1"); err != nil || strings.Count(f.Called(), "/resume") != 1 {
		t.Fatalf("start again resumed again:\n%s", f.Called())
	}
	// sandboxd won't suspend with a process running.
	f.Fail["POST /suspend"] = [2]string{"409", "conflict"}
	if err := p.Stop(ctx, "sbx_1"); err == nil || !strings.Contains(err.Error(), "still running there") {
		t.Fatalf("busy: %v", err)
	}
	delete(f.Fail, "POST /suspend")
	if err := p.Delete(ctx, "sbx_1"); err != nil || f.State("sbx_1") != "terminated" {
		t.Fatalf("delete: %v", err)
	}
	if err := p.Delete(ctx, "sbx_1"); !errors.Is(err, sandbox.ErrNotFound) {
		t.Fatalf("delete again: %v", err)
	}
	if _, err := p.Start(ctx, "sbx_1"); err == nil {
		t.Fatal("started a terminated sandbox")
	}
	// None of it touches a sandbox conch didn't make.
	f.Add(sandboxdtest.Box{ID: "sbx_yours"})
	if err := p.Delete(ctx, "sbx_yours"); !errors.Is(err, sandbox.ErrNotFound) || f.State("sbx_yours") != "running" {
		t.Fatalf("deleted yours: %v", err)
	}
	if err := p.Stop(ctx, "sbx_yours"); !errors.Is(err, sandbox.ErrNotFound) {
		t.Fatalf("stopped yours: %v", err)
	}
}

func TestSandboxdExecArgv(t *testing.T) {
	_, p := fakeSandboxd(t)
	sandbox.SandboxdSelfForTest(t, p, "/bin/conch")
	if got := strings.Join(p.ExecArgv("sbx_1", false), " "); got != "/bin/conch sandbox-io -provider sandbox-cli sbx_1" {
		t.Fatalf("exec %q", got)
	}
	if got := strings.Join(p.ExecArgv("sbx_1", true), " "); got != "/bin/conch sandbox-io -provider sandbox-cli -t sbx_1" {
		t.Fatalf("tty %q", got)
	}
	if got := strings.Join(p.BridgeArgv("sbx_1"), " "); got != "/bin/conch sandbox-io -provider sandbox-cli -bridge sbx_1" {
		t.Fatalf("bridge %q", got)
	}
}

func TestSandboxdExec(t *testing.T) {
	f, p := fakeSandboxd(t)
	f.Add(sandboxdtest.Box{ID: "sbx_1", Labels: map[string]string{"conch": "1"}})
	f.SetRun(func(pr sandboxdtest.Proc) int {
		in, _ := io.ReadAll(pr.Stdin)
		fmt.Fprintf(pr.Stdout, "argv=%q stdin=%d\n", pr.Argv, len(in))
		fmt.Fprint(pr.Stderr, "to stderr")
		return 3
	})
	// More than one frame of stdin: conch copies 16 MB in this way.
	big := bytes.Repeat([]byte("x"), 3<<20)
	var out, errOut bytes.Buffer
	code, err := p.Exec(context.Background(), "sbx_1", "uname -s", nil, bytes.NewReader(big), &out, &errOut)
	if err != nil || code != 3 {
		t.Fatalf("exec: %d %v", code, err)
	}
	if out.String() != fmt.Sprintf("argv=%q stdin=%d\n", []string{"sh", "-c", "uname -s"}, len(big)) || errOut.String() != "to stderr" {
		t.Fatalf("out %q err %q", out.String(), errOut.String())
	}
	// No stdin: it is closed at once rather than left waiting.
	out.Reset()
	if code, err := p.Exec(context.Background(), "sbx_1", "true", nil, nil, &out, &errOut); err != nil || code != 3 || !strings.Contains(out.String(), "stdin=0") {
		t.Fatalf("no stdin: %d %v %q", code, err, out.String())
	}

	// A terminal: asked for with its size, resized as it changes.
	resize := make(chan [2]uint16, 1)
	resize <- [2]uint16{50, 120}
	close(resize)
	f.SetRun(func(pr sandboxdtest.Proc) int {
		select {
		case size := <-pr.Resized:
			fmt.Fprintf(pr.Stdout, "tty=%v %dx%d", pr.Tty, size[0], size[1])
		case <-time.After(5 * time.Second):
			fmt.Fprint(pr.Stdout, "no resize")
		}
		return 0
	})
	out.Reset()
	if code, err := p.Exec(context.Background(), "sbx_1", "bash", &sandbox.Term{Rows: 24, Cols: 80, Resize: resize}, nil, &out, &errOut); err != nil || code != 0 || out.String() != "tty=true 50x120" {
		t.Fatalf("tty: %d %v %q", code, err, out.String())
	}

	// A sandbox that isn't there, and a process sandboxd won't start.
	if _, err := p.Exec(context.Background(), "sbx_missing", "true", nil, nil, &out, &errOut); !errors.Is(err, sandbox.ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	f.Fail["POST /processes"] = [2]string{"400", "invalid_request"}
	if _, err := p.Exec(context.Background(), "sbx_1", "true", nil, nil, &out, &errOut); err == nil || !strings.Contains(err.Error(), "(400)") {
		t.Fatalf("refused: %v", err)
	}
}

func TestSandboxdBridge(t *testing.T) {
	f, p := fakeSandboxd(t)
	f.Add(sandboxdtest.Box{ID: "sbx_1", Labels: map[string]string{"conch": "1"}})
	f.SetRun(func(pr sandboxdtest.Proc) int {
		if !strings.HasSuffix(pr.Argv[2], " bridge -listen 127.0.0.1:0") {
			fmt.Fprintln(pr.Stderr, "not a bridge:", pr.Argv)
			return 2
		}
		// conch's server, as far as the test can tell: it echoes, upper.
		return sandboxdtest.Listen(pr, func(c net.Conn) {
			defer c.Close()
			b := make([]byte, 5)
			io.ReadFull(c, b)
			c.Write(bytes.ToUpper(b))
		})
	})
	var out bytes.Buffer
	if err := p.Bridge(context.Background(), "sbx_1", "'/sandbox/home/.local/bin/conch' bridge", strings.NewReader("hello"), &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "HELLO" {
		t.Fatalf("through the tunnel: %q", out.String())
	}
	if !strings.Contains(f.Called(), "GET /v1/sandboxes/sbx_1/tunnel?port=") {
		t.Fatalf("no tunnel:\n%s", f.Called())
	}

	// An older conch there knows no -listen.
	f.SetRun(func(pr sandboxdtest.Proc) int {
		fmt.Fprintln(pr.Stderr, "flag provided but not defined: -listen")
		return 2
	})
	if err := p.Bridge(context.Background(), "sbx_1", "conch bridge", strings.NewReader(""), &out); err == nil || !strings.Contains(err.Error(), "too old") {
		t.Fatalf("old conch: %v", err)
	}
	// One that ends saying nothing.
	f.SetRun(func(pr sandboxdtest.Proc) int { return 1 })
	if err := p.Bridge(context.Background(), "sbx_1", "conch bridge", strings.NewReader(""), &out); err == nil || !strings.Contains(err.Error(), "exited without saying why") {
		t.Fatalf("silent: %v", err)
	}
	// One that never says where it listens.
	sandbox.SandboxdListenWaitForTest(t, 50*time.Millisecond)
	f.SetRun(func(pr sandboxdtest.Proc) int { time.Sleep(time.Second); return 0 })
	if err := p.Bridge(context.Background(), "sbx_1", "conch bridge", strings.NewReader(""), &out); err == nil || !strings.Contains(err.Error(), "never said") {
		t.Fatalf("mute: %v", err)
	}
	// A tunnel that can't reach the port.
	f.SetRun(func(pr sandboxdtest.Proc) int { fmt.Fprintln(pr.Stdout, "listening 1"); return 0 })
	if err := p.Bridge(context.Background(), "sbx_1", "conch bridge", strings.NewReader(""), &out); err == nil || !strings.Contains(err.Error(), "tunnel to conch in sbx_1") {
		t.Fatalf("no tunnel: %v", err)
	}
}

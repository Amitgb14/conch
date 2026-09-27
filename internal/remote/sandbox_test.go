package remote

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/sandbox"
)

// fakeProvider is a sandbox provider holding one sandbox's state.
type fakeProvider struct {
	state     sandbox.State
	getErr    error
	accessErr error
	accesses  int
}

func (f *fakeProvider) Name() string { return "daytona" }
func (f *fakeProvider) Check() error { return nil }
func (f *fakeProvider) Create(context.Context, sandbox.Spec) (sandbox.Sandbox, error) {
	return sandbox.Sandbox{}, errors.New("not here")
}
func (f *fakeProvider) Get(_ context.Context, id string) (sandbox.Sandbox, error) {
	return sandbox.Sandbox{ID: id, State: f.state}, f.getErr
}
func (f *fakeProvider) List(context.Context) ([]sandbox.Sandbox, error) { return nil, nil }
func (f *fakeProvider) Start(context.Context, string) (sandbox.Sandbox, error) {
	return sandbox.Sandbox{}, nil
}
func (f *fakeProvider) Stop(context.Context, string) error   { return nil }
func (f *fakeProvider) Delete(context.Context, string) error { return nil }
func (f *fakeProvider) SSHAccess(context.Context, string) (sandbox.Access, error) {
	f.accesses++
	return sandbox.Access{User: "tok-secret", Host: "gw.test", Port: 2222}, f.accessErr
}

func useProvider(t *testing.T, p sandbox.Provider, err error) {
	t.Helper()
	useNamedProvider(t, "daytona", p, err)
}

func useNamedProvider(t *testing.T, want string, p sandbox.Provider, err error) {
	t.Helper()
	old := openProvider
	openProvider = func(name string) (sandbox.Provider, error) {
		if name != want {
			t.Errorf("opened provider %q, want %q", name, want)
		}
		return p, err
	}
	t.Cleanup(func() { openProvider = old })
}

// keyProvider is a provider whose sandboxes have an sshd of their own, so
// conch's own public key is what lets it in (boat.dev's shape).
type keyProvider struct {
	fakeProvider
	keys   []string
	keyErr error
}

func (k *keyProvider) Name() string { return "boat" }

func (k *keyProvider) AuthorizeKey(_ context.Context, _, publicKey string) error {
	k.keys = append(k.keys, publicKey)
	return k.keyErr
}

func (k *keyProvider) SSHAccess(context.Context, string) (sandbox.Access, error) {
	k.accesses++
	return sandbox.Access{User: "user", PlainUser: true, Host: "203.0.113.5"}, k.accessErr
}

// ownKey puts a public key in HOME, so nothing has to generate one; it
// returns the key line and where it is.
func ownKey(t *testing.T) (string, string) {
	t.Helper()
	dir := filepath.Join(os.Getenv("HOME"), ".ssh")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	line := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample conch@test"
	path := filepath.Join(dir, "id_ed25519.pub")
	if err := os.WriteFile(path, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return line, path
}

// A provider that takes a key of your own is given conch's, and its
// ordinary user name is not treated as a secret.
func TestTransportForASandboxThatTakesAKey(t *testing.T) {
	a4Env(t)
	line, keyPath := ownKey(t)
	p := &keyProvider{fakeProvider: fakeProvider{state: sandbox.StateStarted}}
	useNamedProvider(t, "boat", p, nil)
	tr, err := TransportFor(context.Background(), "fix-login", "boat:bx_23456781", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.keys) != 1 || p.keys[0] != line {
		t.Fatalf("authorized %q", p.keys)
	}
	cmd, err := tr.Command(context.Background(), "uname -s")
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(cmd.Args, " ")
	if !strings.Contains(args, "user@203.0.113.5 uname -s") {
		t.Fatalf("ssh args: %s", args)
	}
	// ssh must offer the key conch authorized, and only that one: the
	// generated config names conch's own key, which stops ssh trying the
	// defaults, and the agent may hold nothing.
	key := strings.TrimSuffix(keyPath, ".pub")
	if !strings.Contains(args, "-i "+key) || !strings.Contains(args, "IdentitiesOnly=yes") {
		t.Fatalf("the authorized key is not offered: %s", args)
	}
	// A gateway that hands out a token instead is left as it was: there is
	// no key of ours in that login.
	useProvider(t, &fakeProvider{state: sandbox.StateStarted}, nil)
	gwCmd, err := TransportFor(context.Background(), "gw", "daytona:sb1", false)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := gwCmd.Command(context.Background(), "uname -s")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(c2.Args, " "); strings.Contains(got, "IdentitiesOnly") {
		t.Fatalf("a gateway login named an identity: %s", got)
	}
	// "user" is an account name, not a token, so nothing is struck out of
	// messages — a gateway that takes the token as the username keeps its
	// hiding.
	if st := tr.(*sandboxTransport); st.secret != "" {
		t.Fatalf("an ordinary user name is kept as a secret: %q", st.secret)
	}
	useProvider(t, &fakeProvider{state: sandbox.StateStarted}, nil)
	gw, err := TransportFor(context.Background(), "gw", "daytona:sb1", false)
	if err != nil {
		t.Fatal(err)
	}
	if st := gw.(*sandboxTransport); st.secret != "tok-secret" {
		t.Fatalf("a token as a username is not hidden: %q", st.secret)
	}
	// A key the provider won't take stops there, before any ssh.
	p2 := &keyProvider{fakeProvider: fakeProvider{state: sandbox.StateStarted}, keyErr: errors.New("boat: Forbidden (403)")}
	useNamedProvider(t, "boat", p2, nil)
	if _, err := TransportFor(context.Background(), "fix-login", "boat:bx_23456781", false); err == nil ||
		!strings.Contains(err.Error(), "403") {
		t.Fatalf("a key that was refused: %v", err)
	}
	if p2.accesses != 0 {
		t.Fatal("asked where to ssh although the key was refused")
	}
}

func TestParseSandboxTarget(t *testing.T) {
	for _, c := range []struct {
		target, provider, id string
		ok                   bool
	}{
		{"daytona:sb1", "daytona", "sb1", true},
		{"daytona:6f1c-4e2a", "daytona", "6f1c-4e2a", true},
		{"boat:bx_2345678abcdef", "boat", "bx_2345678abcdef", true},
		{"daytona:", "", "", false},
		{"daytona", "", "", false},
		{"", "", "", false},
		{"dev@gpu.lab", "", "", false},
		{"ssh://dev@gpu.lab:2222", "", "", false},
		{"gpu.lab:22", "", "", false},
		{"e2b:sb1", "", "", false}, // not a provider conch knows
		{"daytona:a@b", "", "", false},
		{"daytona:a/b", "", "", false},
		{"daytona:a:b", "", "", false},
		{"daytona:a b", "", "", false},
	} {
		provider, id, ok := ParseSandboxTarget(c.target)
		if provider != c.provider || id != c.id || ok != c.ok {
			t.Errorf("%q: %q %q %v, want %q %q %v", c.target, provider, id, ok, c.provider, c.id, c.ok)
		}
	}
	if SandboxTarget("daytona", "sb1") != "daytona:sb1" {
		t.Error("SandboxTarget")
	}
	if !(Machine{Target: "daytona:sb1"}).IsSandbox() || (Machine{Target: "dev@box"}).IsSandbox() || (Machine{}).IsSandbox() {
		t.Error("IsSandbox")
	}
}

func TestTransportForSSHNeverAsksAProvider(t *testing.T) {
	a4Env(t)
	useProvider(t, nil, errors.New("must not be opened"))
	tr, err := TransportFor(context.Background(), "gpu", "dev@gpu.lab", true)
	if err != nil || tr.Describe() != "dev@gpu.lab" || !tr.interactive() {
		t.Fatalf("ssh: %v %v", tr, err)
	}
}

func TestTransportForARunningSandbox(t *testing.T) {
	a4Env(t)
	p := &fakeProvider{state: sandbox.StateStarted}
	useProvider(t, p, nil)
	tr, err := TransportFor(context.Background(), "fix-login", "daytona:sb1", true)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Describe() != "fix-login" || tr.interactive() || tr.forBridge() != tr {
		t.Fatalf("describe %q interactive %v", tr.Describe(), tr.interactive())
	}
	cmd, err := tr.Command(context.Background(), "uname -s")
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(cmd.Args, " ")
	for _, want := range []string{"BatchMode=yes", "StrictHostKeyChecking=accept-new", "-- ssh://tok-secret@gw.test:2222 uname -s"} {
		if !strings.Contains(args, want) {
			t.Errorf("ssh args lack %q: %s", want, args)
		}
	}
	if p.accesses != 1 {
		t.Fatalf("minted %d tokens", p.accesses)
	}
}

func TestTransportForSandboxThatCantBeReached(t *testing.T) {
	a4Env(t)
	ctx := context.Background()

	for _, state := range []sandbox.State{sandbox.StateStopped, sandbox.StateArchived, sandbox.StateStarting, sandbox.StateError} {
		p := &fakeProvider{state: state}
		useProvider(t, p, nil)
		_, err := TransportFor(ctx, "box", "daytona:sb1", false)
		var stopped *SandboxStoppedError
		if !errors.As(err, &stopped) || stopped.State != state || err.Error() != "sandbox box is "+string(state) {
			t.Fatalf("%s: %v", state, err)
		}
		if p.accesses != 0 {
			t.Fatalf("%s: a token was minted for a sandbox that isn't running", state)
		}
	}

	useProvider(t, &fakeProvider{getErr: sandbox.ErrNotFound}, nil)
	if _, err := TransportFor(ctx, "box", "daytona:sb1", false); err == nil || !strings.Contains(err.Error(), "no longer exists") {
		t.Fatalf("deleted: %v", err)
	}
	useProvider(t, &fakeProvider{getErr: errors.New("daytona: Unauthorized (401)")}, nil)
	if _, err := TransportFor(ctx, "box", "daytona:sb1", false); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("refused: %v", err)
	}
	useProvider(t, &fakeProvider{state: sandbox.StateStarted, accessErr: errors.New("ssh access: boom")}, nil)
	if _, err := TransportFor(ctx, "box", "daytona:sb1", false); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("no access: %v", err)
	}
	useProvider(t, nil, sandbox.ErrNotConfigured)
	if _, err := TransportFor(ctx, "box", "daytona:sb1", false); !errors.Is(err, sandbox.ErrNotConfigured) {
		t.Fatalf("no provider: %v", err)
	}
}

func TestSandboxTransportKeepsTheTokenOutOfErrors(t *testing.T) {
	a4Env(t)
	tr := sandboxSSH("box", sandbox.Access{User: "tok-secret", Host: "gw.test"}, "")
	err := tr.failed(errors.New("exit status 255"), "tok-secret@gw.test: Permission denied (publickey).\n")
	if strings.Contains(err.Error(), "tok-secret") || !strings.Contains(err.Error(), "…@gw.test: Permission denied") {
		t.Fatalf("err %q", err)
	}
	if err := tr.failed(errors.New("exit status 1"), "no such file\n"); err.Error() != "no such file" {
		t.Fatalf("other errors pass through: %q", err)
	}
	if err := tr.failed(errors.New("exit status 1"), ""); err.Error() != "exit status 1" {
		t.Fatalf("no stderr: %q", err)
	}
}

func TestUnsavedWork(t *testing.T) {
	got := UnsavedWork([]proto.ProjectInfo{{Name: "api",
		Worktrees: []proto.WorktreeInfo{{Path: "/w/a", Status: &proto.GitStatus{Files: 1}}, {Path: "/w/clean", Status: &proto.GitStatus{}}},
		Branches: []proto.BranchInfo{
			{Name: "a", Upstream: "origin/a", Ahead: 1, Worktree: "/w/a"},
			{Name: "gone", Upstream: "origin/gone", Gone: true, BaseAhead: 4},
			{Name: "local", BaseAhead: 2, Worktree: "/w/b"},
			{Name: "pushed", Upstream: "origin/pushed"},
			{Name: "clean", Worktree: "/w/clean"},
			{Name: "nostatus", Worktree: "/w/unknown"},
		}}})
	want := []string{"api a: 1 commit not pushed, 1 file uncommitted", "api gone: 4 commits on no remote", "api local: 2 commits on no remote"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q", got)
	}
	// Found on a real sandbox: a new repository has no branches until its
	// first commit, and a detached worktree belongs to none, yet either can
	// hold uncommitted files.
	got = UnsavedWork([]proto.ProjectInfo{{Name: "demo", Worktrees: []proto.WorktreeInfo{
		{Path: "/home/daytona/demo", Branch: "main", Main: true, Status: &proto.GitStatus{Untracked: 1, Files: 1}},
		{Path: "/w/detached", Detached: true, Status: &proto.GitStatus{Unstaged: 2, Files: 2}},
		{Path: "/w/clean", Branch: "clean"},
	}}})
	want = []string{"demo main: 1 file uncommitted", "demo /w/detached: 2 files uncommitted"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("no branches: got %q", got)
	}
	if UnsavedWork(nil) != nil || UnsavedWork([]proto.ProjectInfo{{Name: "empty"}}) != nil {
		t.Fatal("nothing to lose")
	}
	if DefaultSandboxLabel("0123456789") != "sandbox-01234567" || DefaultSandboxLabel("ab") != "sandbox-ab" {
		t.Fatal("DefaultSandboxLabel")
	}
}

// The whole remote path to a sandbox runs through the gateway's ssh with
// the provider's token, and nothing it says names the token.
func TestConnectToASandbox(t *testing.T) {
	a4Env(t)
	useProvider(t, &fakeProvider{state: sandbox.StateStarted}, nil)
	f := newFakeSSH(t)
	f.setProbe(t, currentProbe("linux/amd64"))
	srv := startFakeServer(t, proto.HelloResult{Version: proto.Version, Protocol: proto.ProtocolVersion, Capabilities: proto.Capabilities, PID: 4242, Platform: "linux/amd64"})
	t.Setenv("A4_BRIDGE_SOCK", srv.sock)

	tr, err := TransportFor(context.Background(), "fix-login", "daytona:sb1", false)
	if err != nil {
		t.Fatal(err)
	}
	var said []string
	c, err := Connect(context.Background(), tr, Options{Progress: func(s string) { said = append(said, s) }})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	c.Close()
	if got := strings.Join(said, "; "); strings.Contains(got, "tok-secret") || !strings.Contains(got, "fix-login") {
		t.Fatalf("progress %q", got)
	}
	calls := f.calls(t)
	if len(calls) < 2 {
		t.Fatalf("ssh ran %d times", len(calls))
	}
	for _, argv := range calls { // probe and bridge alike
		joined := strings.Join(argv, " ")
		if !strings.Contains(joined, "ssh://tok-secret@gw.test:2222") || !strings.Contains(joined, "StrictHostKeyChecking=accept-new") {
			t.Fatalf("argv: %s", joined)
		}
	}
}

package remote

import (
	"context"
	"errors"
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
	old := openProvider
	openProvider = func(name string) (sandbox.Provider, error) {
		if name != "daytona" {
			t.Errorf("opened provider %q", name)
		}
		return p, err
	}
	t.Cleanup(func() { openProvider = old })
}

func TestParseSandboxTarget(t *testing.T) {
	for _, c := range []struct {
		target, provider, id string
		ok                   bool
	}{
		{"daytona:sb1", "daytona", "sb1", true},
		{"daytona:6f1c-4e2a", "daytona", "6f1c-4e2a", true},
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
	tr := sandboxSSH("box", sandbox.Access{User: "tok-secret", Host: "gw.test"})
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

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/buildinfo"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/remote"
)

// a4OtherPlatform is a supported platform other than this computer's, so
// installs use CONCH_REMOTE_BINARY instead of copying the test binary.
func a4OtherPlatform() (platform, unameS, unameM string) {
	if runtime.GOOS+"/"+runtime.GOARCH == "linux/amd64" {
		return "darwin/arm64", "Darwin", "arm64"
	}
	return "linux/amd64", "Linux", "x86_64"
}

// a4CurrentProbe is what a machine with this very build installed reports.
func a4CurrentProbe() string {
	_, s, m := a4OtherPlatform()
	info, _ := json.Marshal(buildinfo.Current())
	return s + "\n" + m + "\nbin=/home/dev/.local/bin/conch\n" + string(info) + "\n"
}

func TestA4MachineListRemoveHosts(t *testing.T) {
	a4Env(t)
	var err error
	out, _ := a4Capture(t, "", func() { err = runMachine(nil) })
	if err != nil || strings.Join(strings.Fields(out), " ") != "ID LABEL TARGET ENABLED local local - true" {
		t.Fatalf("empty ls: %q %v", out, err)
	}
	if _, err := remote.SaveMachine(remote.Machine{Target: "dev@gpu.lab", Label: "gpu"}); err != nil {
		t.Fatal(err)
	}
	out, _ = a4Capture(t, "", func() { err = runMachine([]string{"list"}) })
	if err != nil || !strings.Contains(strings.Join(strings.Fields(out), " "), "gpu gpu dev@gpu.lab true") {
		t.Fatalf("ls: %q %v", out, err)
	}
	if err := runMachine([]string{"rm", "nope"}); err == nil || !strings.Contains(err.Error(), `no machine "nope"`) {
		t.Fatalf("rm unknown: %v", err)
	}
	if err := runMachine([]string{"remove", "gpu"}); err != nil {
		t.Fatal(err)
	}
	if ms, _ := remote.Machines(); len(ms) != 0 {
		t.Fatalf("after rm %+v", ms)
	}

	os.WriteFile(filepath.Join(config.Dir(), "machines.json"), []byte("{"), 0o600)
	a4Capture(t, "", func() { err = runMachine([]string{"ls"}) })
	if err == nil {
		t.Fatal("corrupt catalog listed")
	}

	home := os.Getenv("HOME")
	os.MkdirAll(filepath.Join(home, ".ssh"), 0o700)
	os.WriteFile(filepath.Join(home, ".ssh", "config"), []byte("Host alpha beta\nHost *\n"), 0o600)
	out, _ = a4Capture(t, "", func() { err = runMachine([]string{"hosts"}) })
	if err != nil || out != "alpha\nbeta\n" {
		t.Fatalf("hosts: %q %v", out, err)
	}
}

func TestA4MachineAddInstallsAndSaves(t *testing.T) {
	a4Env(t)
	f := newA4SSH(t)
	platform, s, m := a4OtherPlatform()
	f.setProbe(t, s+"\n"+m+"\n")
	f.setProbeAfterInstall(t, a4CurrentProbe())
	remoteSrv := startA4Server(t, "")
	remoteSrv.setHello(func(n int) proto.HelloResult {
		h := currentHello(n)
		h.PID, h.Hostname, h.Platform = 31337, "gpu-host", platform
		return h
	})
	t.Setenv("A4_BRIDGE_SOCK", remoteSrv.sock)
	bin := filepath.Join(t.TempDir(), "conch-remote")
	os.WriteFile(bin, []byte("remote build"), 0o755)
	t.Setenv("CONCH_REMOTE_BINARY", bin)

	// Declined at the install prompt.
	var err error
	_, errOut := a4Capture(t, "n\n", func() { err = runMachine([]string{"add", "dev@gpu.lab"}) })
	if err == nil || err.Error() != "not installed" || !strings.Contains(errOut, "conch is not installed there. Install it to ~/.local/bin/conch? [Y/n]") {
		t.Fatalf("declined: %v %q", err, errOut)
	}
	if ms, _ := remote.Machines(); len(ms) != 0 {
		t.Fatal("saved although declined")
	}

	// Accepted by default.
	out, errOut := a4Capture(t, "\n", func() { err = runMachine([]string{"add", "-label", "GPU", "dev@gpu.lab"}) })
	if err != nil {
		t.Fatalf("add: %v\n%s", err, errOut)
	}
	if out != "added gpu (GPU): "+platform+", server pid 31337 on gpu-host\n" {
		t.Fatalf("output %q", out)
	}
	for _, want := range []string{"Checking dev@gpu.lab…", "platform " + platform, "copying conch to dev@gpu.lab", "installed /home/dev/.local/bin/conch"} {
		if !strings.Contains(errOut, want) {
			t.Fatalf("stderr lacks %q:\n%s", want, errOut)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(f.dir, "installed.bin")); string(b) != "remote build" {
		t.Fatalf("installed %q", b)
	}
	ms, _ := remote.Machines()
	if len(ms) != 1 || ms[0].ID != "gpu" || ms[0].Target != "dev@gpu.lab" {
		t.Fatalf("saved %+v", ms)
	}
	// The probe may prompt (interactive): no BatchMode.
	if first := strings.SplitN(f.argv(t), "===\n", 2)[0]; strings.Contains(first, "BatchMode=yes") {
		t.Fatalf("probe ran in batch mode:\n%s", first)
	}
}

func TestA4MachineAddAlreadyInstalled(t *testing.T) {
	a4Env(t)
	f := newA4SSH(t)
	f.setProbe(t, a4CurrentProbe())
	remoteSrv := startA4Server(t, "")
	t.Setenv("A4_BRIDGE_SOCK", remoteSrv.sock)

	var err error
	out, errOut := a4Capture(t, "", func() { err = runMachine([]string{"add", "-yes", "box"}) })
	if err != nil || !strings.HasPrefix(out, "added box (box): ") {
		t.Fatalf("add: %q %v %q", out, err, errOut)
	}
	if !strings.Contains(errOut, "found /home/dev/.local/bin/conch (build "+buildinfo.Build()+")") {
		t.Fatalf("stderr %q", errOut)
	}
	if strings.Contains(f.argv(t), "conch.new") {
		t.Fatal("installed over a current binary")
	}
}

func TestA4MachineAddOutdatedServerKept(t *testing.T) {
	a4Env(t)
	f := newA4SSH(t)
	f.setProbe(t, a4CurrentProbe())
	remoteSrv := startA4Server(t, "")
	remoteSrv.setHello(func(n int) proto.HelloResult {
		h := currentHello(n)
		h.Capabilities = []string{"pane.v1"}
		return h
	})
	t.Setenv("A4_BRIDGE_SOCK", remoteSrv.sock)

	var err error
	out, errOut := a4Capture(t, "n\n", func() { err = runMachine([]string{"add", "box"}) })
	if err != nil || !strings.HasPrefix(out, "added box") {
		t.Fatalf("add: %q %v", out, err)
	}
	if !strings.Contains(errOut, "from an older build") || !strings.Contains(errOut, "Restart the server there now? Its panes stop. [y/N]") {
		t.Fatalf("stderr %q", errOut)
	}
	if strings.Contains(strings.Join(remoteSrv.methods(), ","), proto.MethodServerStop) {
		t.Fatal("stopped although declined")
	}
}

func TestA4MachineAddFailures(t *testing.T) {
	a4Env(t)
	f := newA4SSH(t)
	t.Setenv("A4_PROBE_FAIL", "ssh: Could not resolve hostname nowhere")
	var err error
	// The probe is interactive, so ssh's own message goes to the terminal.
	_, errOut := a4Capture(t, "", func() { err = runMachine([]string{"add", "nowhere"}) })
	if err == nil || !strings.Contains(errOut, "Could not resolve hostname") {
		t.Fatalf("probe failure: %v %q", err, errOut)
	}

	t.Setenv("A4_PROBE_FAIL", "")
	_, s, m := a4OtherPlatform()
	f.setProbe(t, s+"\n"+m+"\n")
	t.Setenv("CONCH_REMOTE_BINARY", filepath.Join(t.TempDir(), "missing"))
	a4Capture(t, "", func() { err = runMachine([]string{"add", "-yes", "box"}) })
	if err == nil || !os.IsNotExist(err) {
		t.Fatalf("install failure: %v", err)
	}

	// Installed, but the bridge can't reach a server.
	f.setProbe(t, a4CurrentProbe())
	t.Setenv("A4_BRIDGE_SOCK", filepath.Join(t.TempDir(), "none.sock"))
	a4Capture(t, "", func() { err = runMachine([]string{"add", "box"}) })
	if err == nil || !strings.Contains(err.Error(), "bridge dial") {
		t.Fatalf("bridge failure: %v", err)
	}
	if ms, _ := remote.Machines(); len(ms) != 0 {
		t.Fatalf("saved a machine that failed: %+v", ms)
	}
}

// TestA4MachineAddRestartRemoteServer: answering yes to restarting an
// outdated remote server must stop that server and reconnect, whatever the
// local server is doing.
func TestA4MachineAddRestartRemoteServer(t *testing.T) {
	a4Env(t)
	startA4Server(t, config.SocketPath()) // the local server keeps running
	f := newA4SSH(t)
	f.setProbe(t, a4CurrentProbe())
	var stopped atomic.Bool
	remoteSrv := startA4Server(t, "")
	remoteSrv.setHello(func(n int) proto.HelloResult {
		h := currentHello(n)
		if !stopped.Load() {
			h.Capabilities = []string{"pane.v1"}
		}
		return h
	})
	remoteSrv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		if msg.Method == proto.MethodServerStop {
			stopped.Store(true)
		}
		return nil, nil
	})
	t.Setenv("A4_BRIDGE_SOCK", remoteSrv.sock)

	var err error
	start := time.Now()
	a4Capture(t, "y\n", func() { err = runMachine([]string{"add", "box"}) })
	if !stopped.Load() {
		t.Fatal("remote server not asked to stop")
	}
	if time.Since(start) > 5*time.Second || (err != nil && strings.Contains(err.Error(), "did not stop")) {
		t.Fatalf("waited on the local server: %v after %v", err, time.Since(start))
	}
}

func TestA4MachineUpgradeSameBuild(t *testing.T) {
	a4Env(t)
	f := newA4SSH(t)
	f.setProbe(t, a4CurrentProbe())
	t.Setenv("CONCH_REMOTE_BINARY", filepath.Join(t.TempDir(), "bin"))
	os.WriteFile(os.Getenv("CONCH_REMOTE_BINARY"), []byte("b"), 0o755)
	remoteSrv := startA4Server(t, "")
	remoteSrv.setHello(func(n int) proto.HelloResult {
		h := currentHello(n)
		h.Build = buildinfo.Build() // already runs the installed binary
		return h
	})
	t.Setenv("A4_BRIDGE_SOCK", remoteSrv.sock)
	remote.SaveMachine(remote.Machine{Target: "dev@box", Label: "box"})

	var err error
	out, errOut := a4Capture(t, "", func() { err = runMachine([]string{"upgrade", "box"}) })
	if err != nil || out != "box: server pid 1000, build "+buildinfo.Build()+"\n" || !strings.Contains(errOut, "installed /home/dev/.local/bin/conch") {
		t.Fatalf("upgrade: %q %q %v", out, errOut, err)
	}
	if strings.Contains(strings.Join(remoteSrv.methods(), ","), proto.MethodServerReload) {
		t.Fatal("reloaded a server already on the build")
	}
}

func TestA4MachineUpgradeReloads(t *testing.T) {
	if testing.Short() {
		t.Skip("waits a second for the reload")
	}
	a4Env(t)
	f := newA4SSH(t)
	f.setProbe(t, a4CurrentProbe())
	t.Setenv("CONCH_REMOTE_BINARY", filepath.Join(t.TempDir(), "bin"))
	os.WriteFile(os.Getenv("CONCH_REMOTE_BINARY"), []byte("b"), 0o755)
	var reloaded atomic.Bool
	remoteSrv := startA4Server(t, "")
	remoteSrv.setHello(func(n int) proto.HelloResult {
		h := currentHello(n)
		h.Build = "oldbuild"
		if reloaded.Load() {
			h.Build = buildinfo.Build()
		}
		return h
	})
	remoteSrv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		if msg.Method == proto.MethodServerReload {
			reloaded.Store(true)
		}
		return nil, nil
	})
	t.Setenv("A4_BRIDGE_SOCK", remoteSrv.sock)

	var err error
	out, errOut := a4Capture(t, "", func() { err = runMachine([]string{"upgrade", "dev@adhoc"}) })
	if err != nil || out != "adhoc: server pid 1000, build "+buildinfo.Build()+"\n" {
		t.Fatalf("upgrade: %q %q %v", out, errOut, err)
	}
	if !strings.Contains(errOut, "reloading the server on adhoc onto it (panes keep running)") {
		t.Fatalf("stderr %q", errOut)
	}
	var p proto.ServerReloadParams
	if remoteSrv.params(t, proto.MethodServerReload, &p); p.Binary != "/home/dev/.local/bin/conch" {
		t.Fatalf("reload params %+v", p)
	}
}

func TestA4MachineUpgradeOutdatedNoReload(t *testing.T) {
	a4Env(t)
	f := newA4SSH(t)
	f.setProbe(t, a4CurrentProbe())
	t.Setenv("CONCH_REMOTE_BINARY", filepath.Join(t.TempDir(), "bin"))
	os.WriteFile(os.Getenv("CONCH_REMOTE_BINARY"), []byte("b"), 0o755)
	remoteSrv := startA4Server(t, "")
	remoteSrv.setHello(func(n int) proto.HelloResult {
		h := currentHello(n)
		h.Capabilities = []string{"pane.v1"}
		return h
	})
	t.Setenv("A4_BRIDGE_SOCK", remoteSrv.sock)

	var err error
	out, errOut := a4Capture(t, "\n", func() { err = runMachine([]string{"upgrade", "box"}) })
	if err != nil || out != "" || !strings.Contains(errOut, "The server on box is still the old build. Restart it now? Its panes stop. [y/N]") {
		t.Fatalf("declined restart: %q %q %v", out, errOut, err)
	}
}

func TestA4MachineUpgradeFailures(t *testing.T) {
	a4Env(t)
	f := newA4SSH(t)
	t.Setenv("A4_PROBE_FAIL", "Connection refused")
	var err error
	_, errOut := a4Capture(t, "", func() { err = runMachine([]string{"upgrade", "box"}) })
	if err == nil || !strings.Contains(errOut, "Connection refused") {
		t.Fatalf("probe: %v %q", err, errOut)
	}
	t.Setenv("A4_PROBE_FAIL", "")
	f.setProbe(t, a4CurrentProbe())
	t.Setenv("CONCH_REMOTE_BINARY", filepath.Join(t.TempDir(), "missing"))
	a4Capture(t, "", func() { err = runMachine([]string{"upgrade", "box"}) })
	if err == nil || !os.IsNotExist(err) {
		t.Fatalf("install: %v", err)
	}
	t.Setenv("CONCH_REMOTE_BINARY", filepath.Join(t.TempDir(), "bin"))
	os.WriteFile(os.Getenv("CONCH_REMOTE_BINARY"), []byte("b"), 0o755)
	t.Setenv("A4_BRIDGE_SOCK", filepath.Join(t.TempDir(), "none.sock"))
	a4Capture(t, "", func() { err = runMachine([]string{"upgrade", "box"}) })
	if err == nil || !strings.Contains(err.Error(), "bridge dial") {
		t.Fatalf("connect: %v", err)
	}
}

func TestA4SameInstalled(t *testing.T) {
	a4Env(t)
	f := newA4SSH(t)
	remoteSrv := startA4Server(t, "")
	t.Setenv("A4_BRIDGE_SOCK", remoteSrv.sock)
	f.setProbe(t, a4CurrentProbe())
	c, err := remote.Bridge("box", "conch")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := t.Context()
	if sameInstalled(ctx, c, "box") { // server build "fakebuild" differs
		t.Fatal("different builds reported the same")
	}
	_, s, m := a4OtherPlatform()
	f.setProbe(t, s+"\n"+m+"\n") // no version info: can't tell
	if !sameInstalled(ctx, c, "box") {
		t.Fatal("unknown build must count as the same")
	}
	t.Setenv("A4_PROBE_FAIL", "down")
	if !sameInstalled(ctx, c, "box") {
		t.Fatal("probe failure must count as the same")
	}
}

func TestA4DashMMachine(t *testing.T) {
	a4Env(t)
	f := newA4SSH(t)
	remoteSrv := startA4Server(t, "")
	remoteSrv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		if msg.Method == proto.MethodPaneList {
			return proto.PaneList{Panes: []proto.PaneInfo{{ID: "r1", Name: "remote-pane", State: proto.PaneRunning}}}, nil
		}
		return nil, nil
	})
	t.Setenv("A4_BRIDGE_SOCK", remoteSrv.sock)
	remote.SaveMachine(remote.Machine{Target: "dev@gpu.lab", Label: "gpu"})

	// Not installed: never installs, points at machine add.
	_, s, m := a4OtherPlatform()
	f.setProbe(t, s+"\n"+m+"\n")
	machineFlag = "gpu"
	_, err := connect(true)
	if err == nil || !strings.Contains(err.Error(), "run: conch machine add dev@gpu.lab") {
		t.Fatalf("not installed: %v", err)
	}
	if strings.Contains(f.argv(t), "conch.new") {
		t.Fatal("-m installed conch")
	}

	// Installed and current: commands go to the remote server.
	f.setProbe(t, a4CurrentProbe())
	out, _ := a4Capture(t, "", func() { err = runStatus() })
	if err != nil || !strings.Contains(out, "remote-pane") {
		t.Fatalf("remote status: %q %v", out, err)
	}
	if !strings.Contains(f.argv(t), "dev@gpu.lab\n'/home/dev/.local/bin/conch' bridge") {
		t.Fatalf("bridge invocation:\n%s", f.argv(t))
	}

	// Outdated remote server: a warning, but the command still runs.
	remoteSrv.setHello(func(n int) proto.HelloResult {
		h := currentHello(n)
		h.Capabilities = []string{"pane.v1"}
		return h
	})
	out, errOut := a4Capture(t, "", func() { err = runStatus() })
	if err != nil || !strings.Contains(out, "remote-pane") || !strings.Contains(errOut, "conch: warning: gpu: the server there is from an older build") {
		t.Fatalf("outdated: %q %q %v", out, errOut, err)
	}

	// "local" means this computer.
	machineFlag = "local"
	startA4Server(t, config.SocketPath())
	c, err := connect(false)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
}

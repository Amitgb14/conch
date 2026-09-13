package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// TestServerReload builds conch twice, starts a server from the first build,
// reloads it into the second and checks the panes survived.
func TestServerReload(t *testing.T) {
	if testing.Short() {
		t.Skip("builds conch")
	}
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go toolchain")
	}
	dir, err := os.MkdirTemp("", "crl") // short: unix socket path limit
	if err != nil {
		t.Fatal(err)
	}
	dir, _ = filepath.EvalSymlinks(dir)
	defer os.RemoveAll(dir)
	bin := filepath.Join(dir, "conch")
	build := func(version string) {
		t.Helper()
		out, err := exec.Command(gobin, "build", "-ldflags", "-X github.com/Amitgb14/conch/internal/proto.Version="+version, "-o", bin+".new", ".").CombinedOutput()
		if err != nil {
			t.Fatalf("build: %v\n%s", err, out)
		}
		if err := os.Rename(bin+".new", bin); err != nil {
			t.Fatal(err)
		}
	}
	build("0.0.1-a")

	sock := filepath.Join(dir, "s.sock")
	srv := exec.Command(bin, "server")
	srv.Env = append(os.Environ(), "CONCH_HOME="+dir, "CONCH_SOCKET="+sock, "SHELL=/bin/sh")
	srv.Env = filterEnv(srv.Env, "CONCH_PANE_ID")
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		srv.Process.Kill()
		srv.Wait()
	}()

	dial := func() *client.Client {
		t.Helper()
		for i := 0; i < 200; i++ {
			if c, err := client.Dial(sock, "test"); err == nil {
				return c
			}
			time.Sleep(25 * time.Millisecond)
		}
		t.Fatal("server did not answer")
		return nil
	}
	c := dial()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var info proto.PaneInfo
	if err := c.Call(ctx, proto.MethodPaneCreate, proto.PaneCreateParams{Command: []string{"/bin/sh"}, Cwd: dir}, &info); err != nil {
		t.Fatal(err)
	}
	c.Call(ctx, proto.MethodPaneSendText, proto.PaneSendTextParams{ID: info.ID, Text: "echo before-$((6*7))\r"}, nil)
	waitFor := func(c *client.Client, want string) {
		t.Helper()
		for i := 0; i < 200; i++ {
			var r proto.PaneReadResult
			c.Call(ctx, proto.MethodPaneRead, proto.PaneRef{ID: info.ID}, &r)
			if strings.Contains(strings.Join(r.Lines, "\n"), want) {
				return
			}
			time.Sleep(25 * time.Millisecond)
		}
		t.Fatalf("screen never showed %q", want)
	}
	waitFor(c, "before-42")
	oldPID, oldStarted := c.Server.PID, c.Server.Started

	build("0.0.2-b")
	var res proto.ServerReloadResult
	if err := c.Call(ctx, proto.MethodServerReload, proto.ServerReloadParams{}, &res); err != nil {
		t.Fatal(err)
	}
	c.Close()
	var nc *client.Client
	for i := 0; i < 200; i++ {
		time.Sleep(50 * time.Millisecond)
		if cc, err := client.Dial(sock, "test"); err == nil {
			if cc.Server.Started.After(oldStarted) {
				nc = cc
				break
			}
			cc.Close()
		}
	}
	if nc == nil {
		t.Fatal("reloaded server did not answer")
	}
	defer nc.Close()
	if nc.Server.PID != oldPID || nc.Server.Version != "0.0.2-b" {
		t.Fatalf("reloaded server: pid %d (was %d) version %s", nc.Server.PID, oldPID, nc.Server.Version)
	}
	var list proto.PaneList
	nc.Call(ctx, proto.MethodPaneList, nil, &list)
	if len(list.Panes) != 1 || list.Panes[0].PID != info.PID || list.Panes[0].State != proto.PaneRunning {
		t.Fatalf("panes after reload: %+v", list.Panes)
	}
	waitFor(nc, "before-42") // the screen came across
	nc.Call(ctx, proto.MethodPaneSendText, proto.PaneSendTextParams{ID: info.ID, Text: "exit 3\r"}, nil)
	for i := 0; i < 200; i++ {
		nc.Call(ctx, proto.MethodPaneList, nil, &list)
		if list.Panes[0].State == proto.PaneExited {
			if list.Panes[0].ExitCode != 3 {
				t.Fatalf("exit code %d", list.Panes[0].ExitCode)
			}
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("pane never exited")
}

func filterEnv(env []string, drop string) []string {
	out := env[:0]
	for _, kv := range env {
		if !strings.HasPrefix(kv, drop+"=") {
			out = append(out, kv)
		}
	}
	return out
}

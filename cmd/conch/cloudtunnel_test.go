package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/cloud"
	"github.com/Amitgb14/conch/internal/cloud/cloudtest"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/remote"
	"github.com/Amitgb14/conch/internal/server"
)

// cloudMachineRig is a signed-in conch, a fake Conch Cloud with one
// running machine, and that machine's side: cloudagent's gateway on the
// fake's relay in front of a real conch server, and a conch in its user's
// ~/.local/bin for the probe to find (this test binary in disguise).
type cloudMachineRig struct {
	f     *cloudtest.Fake
	m     *cloudtest.Machine
	gw    *cloudtest.Gateway
	conch string // the client's conch: this test binary as main()
}

func newCloudMachineRig(t *testing.T) *cloudMachineRig {
	t.Helper()
	if testing.Short() {
		t.Skip("starts a server")
	}
	f, _ := cloudIsolated(t)
	if err := signIn(t, f); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", "/bin/sh")
	exe, _ := os.Executable()
	dir, err := os.MkdirTemp("", "cm")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	// The machine's server.
	sock := filepath.Join(dir, "s.sock")
	srv := server.New(sock, dir)
	go srv.Run()
	t.Cleanup(srv.StopAndWait)
	for range 300 {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	// The machine's conch, where the probe looks.
	bin := filepath.Join(os.Getenv("HOME"), ".local", "bin")
	os.MkdirAll(bin, 0o755)
	os.WriteFile(filepath.Join(bin, "conch"), []byte(fmt.Sprintf("#!/bin/sh\nexec env A4_HELPER_MODE=main CONCH_HOME=%s %s \"$@\"\n", filepath.Join(dir, "vm"), exe)), 0o755)
	// This device's conch.
	client := filepath.Join(dir, "client-conch")
	os.WriteFile(client, []byte(fmt.Sprintf("#!/bin/sh\nexec env A4_HELPER_MODE=main %s \"$@\"\n", exe)), 0o755)
	t.Cleanup(remote.CloudExecutableForTest(client))
	gw := f.StartGateway(t, sock)
	m := f.AddMachine("box", "running", gw.Key, gw.Room)
	return &cloudMachineRig{f: f, m: m, gw: gw, conch: client}
}

// TestCloudMachineIsAMachine: signed in, the account's machine is in
// conch's catalog, and it is reached the way every machine is —
// TransportFor, Connect (probe through `conch cloud exec`, the protocol
// through `conch cloud bridge`), and BridgeCommand, which is what
// `conch machine bridge` runs for Conch.app.
func TestCloudMachineIsAMachine(t *testing.T) {
	r := newCloudMachineRig(t)
	if err := runCloud([]string{"machine", "ls"}); err != nil {
		t.Fatal(err)
	}
	m, ok := remote.LookupMachine("box")
	if !ok || !m.IsCloud() || m.Target != "conch:"+r.m.ID {
		t.Fatalf("catalog has %+v %v", m, ok)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_, tr, err := transports(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	c, err := remote.Connect(ctx, tr, remote.Options{})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if c.Server.Version != proto.Version {
		t.Fatalf("server %+v", c.Server)
	}
	var panes json.RawMessage
	if err := c.Call(ctx, proto.MethodPaneList, nil, &panes); err != nil {
		t.Fatal(err)
	}
	c.Close()

	cmd, err := remote.BridgeCommand(ctx, tr)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(strings.Join(cmd.Args, " "), "cloud bridge "+r.m.ID) {
		t.Fatalf("bridge command %q", cmd.Args)
	}

	// Asleep: said, not woken, by a connection nobody asked for.
	r.f.SetMachine(r.m.ID, func(m *cloudtest.Machine) { m.State = "asleep" })
	_, _, err = transports(ctx, m)
	var asleep *remote.CloudAsleepError
	if !errors.As(err, &asleep) || !strings.Contains(err.Error(), "conch cloud machine wake box") {
		t.Fatalf("asleep: %v", err)
	}
	if n := r.f.Count("POST /v1/machines/" + r.m.ID + "/wake"); n != 0 {
		t.Fatalf("woken %d times by a background connection", n)
	}
}

// TestCloudExecCommand: conch cloud exec's stdin, stdout, stderr and exit
// status, and what a machine that cannot be reached exits with.
func TestCloudExecCommand(t *testing.T) {
	r := newCloudMachineRig(t)
	run := func(stdin string, args ...string) (string, string, int) {
		cmd := exec.Command(r.conch, append([]string{"cloud", "exec"}, args...)...)
		cmd.Stdin = strings.NewReader(stdin)
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		cmd.Run()
		return out.String(), errb.String(), cmd.ProcessState.ExitCode()
	}
	out, errs, code := run("hello\n", "box", "--", "tr a-z A-Z; echo bad >&2; exit 3")
	if out != "HELLO\n" || !strings.Contains(errs, "bad") || code != 3 {
		t.Fatalf("out %q err %q code %d", out, errs, code)
	}
	out, _, code = run("", r.m.ID, "--", "echo", "two words")
	if out != "two words\n" || code != 0 {
		t.Fatalf("by id: %q %d", out, code)
	}
	_, errs, code = run("", "nope", "--", "true")
	if code != remote.CloudExitConnection && code != remote.CloudExitRefused || !strings.Contains(errs, "nope") {
		t.Fatalf("unknown machine: %q %d", errs, code)
	}
	// Signed out: refused for good, not worth trying again.
	cloud.Forget()
	_, errs, code = run("", r.m.ID, "--", "true")
	if code != remote.CloudExitRefused || !strings.Contains(errs, "conch login") {
		t.Fatalf("signed out: %q %d", errs, code)
	}
}

// TestCloudBridgeCommand: conch cloud bridge carries the protocol on its
// stdin and stdout, and ends when stdin does.
func TestCloudBridgeCommand(t *testing.T) {
	r := newCloudMachineRig(t)
	cmd := exec.Command(r.conch, "cloud", "bridge", "box")
	in, _ := cmd.StdinPipe()
	outR, _ := cmd.StdoutPipe()
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(in, `{"id":"1","method":"hello","params":{"client":"t","protocol":%d}}`+"\n", proto.ProtocolVersion)
	buf := make([]byte, 64<<10)
	done := make(chan string, 1)
	go func() {
		var got []byte
		for {
			n, err := outR.Read(buf)
			got = append(got, buf[:n]...)
			if bytes.Contains(got, []byte("\n")) || err != nil {
				done <- string(got)
				io.Copy(io.Discard, outR)
				return
			}
		}
	}()
	select {
	case got := <-done:
		if !strings.Contains(got, `"id":"1"`) || !strings.Contains(got, `"capabilities"`) {
			t.Fatalf("hello answered %q (stderr %q)", got, errb.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("no answer; stderr %q", errb.String())
	}
	in.Close()
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	select {
	case err := <-exited:
		if err != nil {
			t.Fatalf("bridge exit: %v (%s)", err, errb.String())
		}
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		t.Fatal("the bridge outlived its stdin")
	}
}

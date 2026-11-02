package main

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/remote"
)

// runBridgeWith runs `conch machine bridge` with stdin and stdout the
// test's own pipes, and answers them; the result arrives on the channel.
func runBridgeWith(t *testing.T, args ...string) (io.WriteCloser, *bufio.Reader, chan error) {
	t.Helper()
	inR, inW, _ := os.Pipe()
	outR, outW, _ := os.Pipe()
	oldIn, oldOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = inR, outW
	done := make(chan error, 1)
	go func() {
		done <- runMachine(append([]string{"bridge"}, args...))
		outW.Close()
	}()
	t.Cleanup(func() {
		os.Stdin, os.Stdout = oldIn, oldOut
		inW.Close()
		inR.Close()
		outR.Close()
	})
	return inW, bufio.NewReader(outR), done
}

// The bytes reach the machine's server and its answers come back: the
// protocol, end to end, through the machine's own way in.
func TestMachineBridgeCarriesTheProtocol(t *testing.T) {
	a4Env(t)
	f := newA4SSH(t)
	f.setProbe(t, a4CurrentProbe())
	remoteSrv := startA4Server(t, "")
	remoteSrv.setHello(func(n int) proto.HelloResult {
		h := currentHello(n)
		h.Hostname = "gpu-host"
		return h
	})
	t.Setenv("A4_BRIDGE_SOCK", remoteSrv.sock)
	if _, err := remote.SaveMachine(remote.Machine{ID: "box", Label: "box", Target: "dev@box", Enabled: true}); err != nil {
		t.Fatal(err)
	}

	in, out, done := runBridgeWith(t, "box")
	hello, _ := json.Marshal(proto.Message{ID: "1", Method: proto.MethodHello, Params: proto.Marshal(proto.HelloParams{Client: "test", Protocol: proto.ProtocolVersion})})
	in.Write(append(hello, '\n')) //nolint:errcheck
	lineCh := make(chan string, 1)
	go func() { l, _ := out.ReadString('\n'); lineCh <- l }()
	select {
	case line := <-lineCh:
		var reply proto.Message
		var h proto.HelloResult
		if json.Unmarshal([]byte(line), &reply) != nil || reply.ID != "1" || json.Unmarshal(reply.Result, &h) != nil || h.Hostname != "gpu-host" {
			t.Fatalf("reply %q", line)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no reply through the bridge")
	}
	in.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("bridge ended with %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the bridge did not end with its input")
	}
	// It was the bridge that was run there, and nothing was installed.
	if argv := f.argv(t); !strings.Contains(argv, "bridge") || strings.Contains(argv, "conch.new") {
		t.Fatalf("ssh was asked to %q", argv)
	}
}

// A machine without conch is said, with what to do, and nothing installed.
func TestMachineBridgeWithoutConch(t *testing.T) {
	a4Env(t)
	f := newA4SSH(t)
	_, s, m := a4OtherPlatform()
	f.setProbe(t, s+"\n"+m+"\n")
	if _, err := remote.SaveMachine(remote.Machine{ID: "box", Label: "box", Target: "dev@box", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	err := runMachine([]string{"bridge", "box"})
	if err == nil || !strings.Contains(err.Error(), "not installed") || !strings.Contains(err.Error(), "conch machine upgrade box") {
		t.Fatalf("%v", err)
	}
	if argv := f.argv(t); strings.Contains(argv, "conch.new") || strings.Contains(argv, " bridge") {
		t.Fatalf("ssh was asked to %q", argv)
	}
	if err := runMachine([]string{"bridge"}); err == nil || !strings.Contains(err.Error(), "usage: conch machine bridge") {
		t.Fatalf("no name: %v", err)
	}
}

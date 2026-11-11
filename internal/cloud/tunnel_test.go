package cloud_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/relay"

	"github.com/Amitgb14/conch/internal/cloud/cloudtest"

	"github.com/Amitgb14/conch/internal/cloud"
)

// tunnelRig is a signed-in client, a fake control plane, its relay, and a
// machine whose gateway (cloudagent's) fronts a real conch server.
type tunnelRig struct {
	f    *cloudtest.Fake
	c    *cloud.Client
	gw   *cloudtest.Gateway
	m    *cloudtest.Machine
	sock string
}

func newTunnelRig(t *testing.T) *tunnelRig {
	t.Helper()
	isolate(t)
	f := cloudtest.New(t)
	c := login(t, f)
	dir, err := os.MkdirTemp("", "cc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")
	serveConch(t, sock)
	gw := f.StartGateway(t, sock)
	m := f.AddMachine("box", cloud.StateRunning, gw.Key, gw.Room)
	return &tunnelRig{f: f, c: c, gw: gw, m: m, sock: sock}
}

func TestBridgeCarriesConchsProtocol(t *testing.T) {
	r := newTunnelRig(t)
	ctx := ctxT(t)
	s, err := r.c.Dial(ctx, r.m.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// client.New over the bridge, exactly as remote.Bridge would through
	// `conch cloud bridge`'s stdin and stdout.
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- cloud.Bridge(ctx, s, inR, outW); outW.Close() }()
	c, err := client.New(rwc{outR, inW}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if c.Server.Version != proto.Version {
		t.Fatalf("hello %+v", c.Server)
	}
	var pong json.RawMessage
	if err := c.Call(ctx, proto.MethodPaneList, nil, &pong); err != nil {
		t.Fatal(err)
	}
	// stdin's end ends the bridge cleanly.
	inW.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("bridge: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the bridge outlived its stdin")
	}
}

type rwc struct {
	io.Reader
	io.WriteCloser
}

func TestExecExitCodeStdinStderr(t *testing.T) {
	r := newTunnelRig(t)
	ctx := ctxT(t)
	s, err := r.c.Dial(ctx, r.m.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var out, errb bytes.Buffer
	code, err := cloud.Exec(ctx, s, cloud.ExecOptions{Script: `cat; echo oops >&2; exit 7`, Stdin: strings.NewReader("from stdin\n"), Stdout: &out, Stderr: &errb})
	if err != nil {
		t.Fatal(err)
	}
	if code != 7 || out.String() != "from stdin\n" || errb.String() != "oops\n" {
		t.Fatalf("code %d out %q err %q", code, out.String(), errb.String())
	}
	// No stdin at all: EOF at once, so cat ends.
	out.Reset()
	code, err = cloud.Exec(ctx, s, cloud.ExecOptions{Script: `cat; echo done`, Stdout: &out})
	if err != nil || code != 0 || out.String() != "done\n" {
		t.Fatalf("no stdin: %d %q %v", code, out.String(), err)
	}
	// A terminal, when asked for.
	out.Reset()
	code, err = cloud.Exec(ctx, s, cloud.ExecOptions{Script: `[ -t 0 ] && stty size`, TTY: true, Cols: 91, Rows: 17, Stdout: &out})
	if err != nil || code != 0 || !strings.Contains(out.String(), "17 91") {
		t.Fatalf("tty: %d %q %v", code, out.String(), err)
	}
}

// TestDroppedMidStream: the relay going away in the middle of a script
// is an error, not an exit status.
func TestDroppedMidStream(t *testing.T) {
	r := newTunnelRig(t)
	ctx := ctxT(t)
	s, err := r.c.Dial(ctx, r.m.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	pr, pw := io.Pipe()
	got := make(chan struct{})
	var out lockedBuf
	out.on = func(b string) {
		if strings.Contains(b, "started") {
			select {
			case <-got:
			default:
				close(got)
			}
		}
	}
	res := make(chan error, 1)
	go func() {
		_, err := cloud.Exec(ctx, s, cloud.ExecOptions{Script: `echo started; cat`, Stdin: pr, Stdout: &out})
		res <- err
	}()
	<-got
	r.f.Relay.CloseClientConnections()
	r.gw.Stop()
	select {
	case err := <-res:
		if !errors.Is(err, cloud.ErrDropped) {
			t.Fatalf("err %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("exec did not notice the relay going")
	}
	pw.Close()

	// And a bridge.
	r2 := newTunnelRig(t)
	s2, err := r2.c.Dial(ctx, r2.m.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	inR, _ := io.Pipe()
	bres := make(chan error, 1)
	go func() { bres <- cloud.Bridge(ctx, s2, inR, io.Discard) }()
	time.Sleep(100 * time.Millisecond)
	r2.gw.Stop()
	select {
	case err := <-bres:
		if !errors.Is(err, cloud.ErrDropped) {
			t.Fatalf("bridge err %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the bridge did not notice the relay going")
	}
}

type lockedBuf struct {
	bytes.Buffer
	on func(string)
}

func (b *lockedBuf) Write(p []byte) (int, error) {
	n, err := b.Buffer.Write(p)
	if b.on != nil {
		b.on(b.String())
	}
	return n, err
}

// TestRefusedTicket: a ticket the relay refuses (4401) is asked for again
// once — it may have lapsed on the way — and then said.
func TestRefusedTicket(t *testing.T) {
	r := newTunnelRig(t)
	ctx := ctxT(t)
	// The relay takes no ticket the fake hands out: every one is wrong.
	r.f.Mu.Lock()
	r.f.BadTickets = true
	r.f.Mu.Unlock()
	_, err := r.c.Dial(ctx, r.m.ID, nil)
	if !errors.Is(err, relay.ErrTicketRefused) {
		t.Fatalf("err %v", err)
	}
	if n := r.f.Count("POST /v1/machines/" + r.m.ID + "/connect"); n != 2 {
		t.Fatalf("%d connects; want the first and one more", n)
	}
}

// TestWakesAndWaits: an asleep machine is woken and waited for; the
// tunnel opens once it is running.
func TestWakesAndWaits(t *testing.T) {
	r := newTunnelRig(t)
	ctx := ctxT(t)
	r.f.SetMachine(r.m.ID, func(m *cloudtest.Machine) { m.State = cloud.StateAsleep })
	go func() {
		for r.f.Count("POST /v1/machines/"+r.m.ID+"/wake") == 0 {
			time.Sleep(5 * time.Millisecond)
		}
		time.Sleep(50 * time.Millisecond)
		r.f.SetMachine(r.m.ID, func(m *cloudtest.Machine) { m.State = cloud.StateRunning })
	}()
	var said []string
	s, err := r.c.Dial(ctx, r.m.ID, func(m string) { said = append(said, m) })
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if len(said) == 0 || said[0] != "waking box" {
		t.Fatalf("said %q", said)
	}
	// A machine in error is said, with why.
	r.f.SetMachine(r.m.ID, func(m *cloudtest.Machine) { m.State, m.StateDetail = cloud.StateError, "its VM never said hello" })
	if _, err := r.c.Dial(ctx, r.m.ID, nil); err == nil || !strings.Contains(err.Error(), "never said hello") {
		t.Fatalf("error state: %v", err)
	}
}

// TestNoGatewayYet: running, but its agent has not joined the relay —
// tried again with a fresh ticket until it has.
func TestNoGatewayYet(t *testing.T) {
	r := newTunnelRig(t)
	ctx := ctxT(t)
	r.gw.Stop()
	go func() {
		for r.f.Count("POST /v1/machines/"+r.m.ID+"/connect") < 2 {
			time.Sleep(5 * time.Millisecond)
		}
		r.f.JoinGateway(t, r.sock, r.gw.DH, r.gw.Secret)
	}()
	var said []string
	s, err := r.c.Dial(ctx, r.m.ID, func(m string) { said = append(said, m) })
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if len(said) == 0 || said[0] != "box is starting up" {
		t.Fatalf("said %q", said)
	}
}

// TestDialRefusesAChangedKey: the control plane handing out a key the
// machine did not make is refused before the relay is dialled.
func TestDialRefusesAChangedKey(t *testing.T) {
	r := newTunnelRig(t)
	ctx := ctxT(t)
	s, err := r.c.Dial(ctx, r.m.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	other, _ := relay.NewKey()
	r.f.Mu.Lock()
	r.f.Keys[r.m.ID] = relay.EncodeKey(other.Public)
	r.f.Mu.Unlock()
	var kc *cloud.KeyChangedError
	if _, err := r.c.Dial(ctx, r.m.ID, nil); !errors.As(err, &kc) {
		t.Fatalf("err %v", err)
	}
}

// scanLines is for reading a bridge's output by line in tests.
func scanLines(r io.Reader) []string {
	var out []string
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		out = append(out, sc.Text())
	}
	return out
}

var _ = json.Marshal
var _ = context.Background
var _ = scanLines

// serveConch is a conch server as far as a bridge can tell: it answers
// hello and any other call, one JSON line each. (The real server cannot
// be imported here: it imports remote, which imports this package.)
func serveConch(t *testing.T, sock string) {
	t.Helper()
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				sc := bufio.NewScanner(c)
				sc.Buffer(make([]byte, 64<<10), 8<<20)
				for sc.Scan() {
					var m proto.Message
					if json.Unmarshal(sc.Bytes(), &m) != nil || m.ID == "" {
						continue
					}
					res := json.RawMessage(`[]`)
					if m.Method == proto.MethodHello {
						res = proto.Marshal(proto.HelloResult{Version: proto.Version, Protocol: proto.ProtocolVersion, Capabilities: proto.Capabilities})
					}
					b, _ := json.Marshal(proto.Message{ID: m.ID, Result: res})
					if _, err := c.Write(append(b, '\n')); err != nil {
						return
					}
				}
			}()
		}
	}()
}

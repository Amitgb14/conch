package cloud

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/relay"
	"github.com/Amitgb14/conch/internal/server"
)

// tunnelRig is a signed-in client, a fake control plane, its relay, and a
// machine whose gateway (cloudagent's) fronts a real conch server.
type tunnelRig struct {
	f    *fake
	c    *Client
	gw   *gateway
	m    *Machine
	sock string
}

func newTunnelRig(t *testing.T) *tunnelRig {
	t.Helper()
	isolate(t)
	f := newFake(t)
	c := login(t, f)
	dir, err := os.MkdirTemp("", "cc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")
	srv := server.New(sock, dir)
	go srv.Run()
	t.Cleanup(srv.StopAndWait)
	for range 200 {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	gw := f.startGateway(t, sock)
	m := f.addMachine("box", StateRunning, gw.key, gw.room)
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
	go func() { done <- Bridge(ctx, s, inR, outW); outW.Close() }()
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
	code, err := Exec(ctx, s, ExecOptions{Script: `cat; echo oops >&2; exit 7`, Stdin: strings.NewReader("from stdin\n"), Stdout: &out, Stderr: &errb})
	if err != nil {
		t.Fatal(err)
	}
	if code != 7 || out.String() != "from stdin\n" || errb.String() != "oops\n" {
		t.Fatalf("code %d out %q err %q", code, out.String(), errb.String())
	}
	// No stdin at all: EOF at once, so cat ends.
	out.Reset()
	code, err = Exec(ctx, s, ExecOptions{Script: `cat; echo done`, Stdout: &out})
	if err != nil || code != 0 || out.String() != "done\n" {
		t.Fatalf("no stdin: %d %q %v", code, out.String(), err)
	}
	// A terminal, when asked for.
	out.Reset()
	code, err = Exec(ctx, s, ExecOptions{Script: `[ -t 0 ] && stty size`, TTY: true, Cols: 91, Rows: 17, Stdout: &out})
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
		_, err := Exec(ctx, s, ExecOptions{Script: `echo started; cat`, Stdin: pr, Stdout: &out})
		res <- err
	}()
	<-got
	r.f.relay.CloseClientConnections()
	r.gw.stop()
	select {
	case err := <-res:
		if !errors.Is(err, ErrDropped) {
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
	go func() { bres <- Bridge(ctx, s2, inR, io.Discard) }()
	time.Sleep(100 * time.Millisecond)
	r2.gw.stop()
	select {
	case err := <-bres:
		if !errors.Is(err, ErrDropped) {
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
	r.f.mu.Lock()
	r.f.badTickets = true
	r.f.mu.Unlock()
	_, err := r.c.Dial(ctx, r.m.ID, nil)
	if !errors.Is(err, relay.ErrTicketRefused) {
		t.Fatalf("err %v", err)
	}
	if n := r.f.count("POST /v1/machines/" + r.m.ID + "/connect"); n != 2 {
		t.Fatalf("%d connects; want the first and one more", n)
	}
}

// TestWakesAndWaits: an asleep machine is woken and waited for; the
// tunnel opens once it is running.
func TestWakesAndWaits(t *testing.T) {
	r := newTunnelRig(t)
	ctx := ctxT(t)
	r.f.setMachine(r.m.ID, func(m *Machine) { m.State = StateAsleep })
	go func() {
		for r.f.count("POST /v1/machines/"+r.m.ID+"/wake") == 0 {
			time.Sleep(5 * time.Millisecond)
		}
		time.Sleep(50 * time.Millisecond)
		r.f.setMachine(r.m.ID, func(m *Machine) { m.State = StateRunning })
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
	r.f.setMachine(r.m.ID, func(m *Machine) { m.State, m.StateDetail = StateError, "its VM never said hello" })
	if _, err := r.c.Dial(ctx, r.m.ID, nil); err == nil || !strings.Contains(err.Error(), "never said hello") {
		t.Fatalf("error state: %v", err)
	}
}

// TestNoGatewayYet: running, but its agent has not joined the relay —
// tried again with a fresh ticket until it has.
func TestNoGatewayYet(t *testing.T) {
	r := newTunnelRig(t)
	ctx := ctxT(t)
	r.gw.stop()
	go func() {
		for r.f.count("POST /v1/machines/"+r.m.ID+"/connect") < 2 {
			time.Sleep(5 * time.Millisecond)
		}
		r.f.joinGateway(t, r.sock, r.gw.dh, r.gw.secret)
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

func TestKeyPinning(t *testing.T) {
	isolate(t)
	created := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	m := Machine{ID: "m_aaaaaaaaaaaaaaaa", Name: "box", CreatedAt: created}
	if err := checkPin(m, "key-one"); err != nil {
		t.Fatalf("first use: %v", err)
	}
	if err := checkPin(m, "key-one"); err != nil {
		t.Fatalf("same key: %v", err)
	}
	err := checkPin(m, "key-two")
	var kc *KeyChangedError
	if !errors.As(err, &kc) || !strings.Contains(err.Error(), "forget-key m_aaaaaaaaaaaaaaaa") {
		t.Fatalf("changed key: %v", err)
	}
	// Recreated: a new created_at, a new key, pinned again.
	m.CreatedAt = created.Add(time.Hour)
	if err := checkPin(m, "key-two"); err != nil {
		t.Fatalf("recreated: %v", err)
	}
	if err := checkPin(m, "key-one"); err == nil {
		t.Fatal("the old key passed after re-pinning")
	}
	// Forgotten: whatever comes next is pinned.
	if err := ForgetKey(m.ID); err != nil {
		t.Fatal(err)
	}
	if err := checkPin(m, "key-three"); err != nil {
		t.Fatal(err)
	}
	if err := ForgetKey("m_bbbbbbbbbbbbbbbb"); err == nil {
		t.Fatal("forgot a key never pinned")
	}
	st, _ := os.Stat(KeysPath())
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("cloud-keys.json is %04o", st.Mode().Perm())
	}
	if err := checkPin(m, ""); err == nil {
		t.Fatal("an empty key passed")
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
	r.f.mu.Lock()
	r.f.keys[r.m.ID] = relay.EncodeKey(other.Public)
	r.f.mu.Unlock()
	var kc *KeyChangedError
	if _, err := r.c.Dial(ctx, r.m.ID, nil); !errors.As(err, &kc) {
		t.Fatalf("err %v", err)
	}
}

func TestDecodeKey(t *testing.T) {
	k, _ := relay.NewKey()
	for _, s := range []string{relay.EncodeKey(k.Public), relay.EncodeKey(k.Public) + "="} {
		if b, err := decodeKey(s); err != nil || !bytes.Equal(b, k.Public) {
			t.Fatalf("%q: %v", s, err)
		}
	}
	if _, err := decodeKey("c2hvcnQ"); err == nil {
		t.Fatal("a short key decoded")
	}
	if _, err := decodeKey("!!"); err == nil {
		t.Fatal("garbage decoded")
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

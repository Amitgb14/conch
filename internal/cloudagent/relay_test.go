package cloudagent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/relay"
)

// relayRig is a rig whose hello names a real relay, run here.
func relayRig(t *testing.T) (*rig, *httptest.Server, string) {
	t.Helper()
	r := newRig(t)
	rs := httptest.NewServer(&relay.Server{})
	t.Cleanup(rs.Close)
	secret, _ := relay.NewSecret()
	wsURL := "ws" + strings.TrimPrefix(rs.URL, "http")
	r.cloud.handle("POST /v1/vm/hello", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"machine_id": testMachine, "relay_url": wsURL, "room_secret": secret, "sleep": map[string]any{"idle_seconds": 600}})
	})
	r.startServer()
	r.run()
	return r, rs, secret
}

// dialMachine is a client reaching the machine through the relay, as conch
// on a laptop will: the room, and the key hello reported.
func dialMachine(t *testing.T, r *rig, rs *httptest.Server, secret string) *relay.Session {
	t.Helper()
	hellos := r.cloud.waitFor("POST /v1/vm/hello", 1, 10*time.Second)
	var h VMHello
	json.Unmarshal(hellos[0].Body, &h)
	pub, err := base64.RawURLEncoding.DecodeString(h.ServerKey)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		s, err := relay.Dial(ctx, rs.URL, relay.RoomID(secret), pub)
		if err == nil {
			t.Cleanup(s.Close)
			return s
		}
		if ctx.Err() != nil {
			t.Fatalf("dial the machine: %v", err)
		}
		time.Sleep(30 * time.Millisecond) // until the gateway has joined
	}
}

// TestBridgeThroughTheRelay: conch's own protocol reaches the machine's
// server through the tunnel, and a client there counts as attached.
func TestBridgeThroughTheRelay(t *testing.T) {
	r, rs, secret := relayRig(t)
	s := dialMachine(t, r, rs, secret)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	k, err := s.Socket(ctx, "/bridge", nil)
	if err != nil {
		t.Fatal(err)
	}
	hello, _ := json.Marshal(proto.Message{ID: "1", Method: proto.MethodHello, Params: proto.Marshal(proto.HelloParams{Client: "test", Protocol: proto.ProtocolVersion})})
	if err := k.Send(ctx, string(hello)); err != nil {
		t.Fatal(err)
	}
	line, err := k.Recv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var msg proto.Message
	var res proto.HelloResult
	if json.Unmarshal([]byte(line), &msg) != nil || msg.ID != "1" || json.Unmarshal(msg.Result, &res) != nil {
		t.Fatalf("answer %s", line)
	}
	if res.Version != proto.Version || !containsStr(res.Capabilities, proto.CapCloudAgent) {
		t.Fatalf("hello result %+v", res)
	}
	r.activityWith("a client attached", func(a VMActivity) bool { return a.ClientsAttached == 1 })

	// Two lines in one message are refused: one message is one line.
	k2, err := s.Socket(ctx, "/bridge", nil)
	if err != nil {
		t.Fatal(err)
	}
	k2.Send(ctx, `{"id":"a","method":"ping"}`+"\n"+`{"id":"b","method":"ping"}`)
	if _, err := k2.Recv(ctx); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("two lines: %v", err)
	}

	s.Close()
	r.activityWith("no client attached", func(a VMActivity) bool { return a.ClientsAttached == 0 })
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

type execResult struct {
	out, err string
	exit     int
}

func runExec(t *testing.T, s *relay.Session, start string, inputs ...string) execResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	k, err := s.Socket(ctx, "/exec", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := k.Send(ctx, start); err != nil {
		t.Fatal(err)
	}
	for _, in := range inputs {
		k.Send(ctx, in)
	}
	var res execResult
	res.exit = -1
	for {
		m, err := k.Recv(ctx)
		if err != nil {
			if res.exit < 0 {
				t.Fatalf("no exit status: %v (out %q err %q)", err, res.out, res.err)
			}
			return res
		}
		var o struct {
			Out, Err string
			Exit     *int
		}
		json.Unmarshal([]byte(m), &o)
		b, _ := base64.StdEncoding.DecodeString(o.Out)
		res.out += string(b)
		b, _ = base64.StdEncoding.DecodeString(o.Err)
		res.err += string(b)
		if o.Exit != nil {
			res.exit = *o.Exit
		}
	}
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// TestExecThroughTheRelay: a script runs as the machine's user, in its
// home, with stdin carried in and stdout, stderr and the status out.
func TestExecThroughTheRelay(t *testing.T) {
	r, rs, secret := relayRig(t)
	s := dialMachine(t, r, rs, secret)

	res := runExec(t, s, `{"script":"echo out; echo err >&2; pwd; exit 3","tty":false}`)
	if res.out != "out\n"+r.home+"\n" || res.err != "err\n" || res.exit != 3 {
		t.Fatalf("%+v", res)
	}
	res = runExec(t, s, `{"script":"tr a-z A-Z","tty":false}`, `{"in":"`+b64("hello ")+`"}`, `{"in":"`+b64("there")+`"}`, `{"in":"","eof":true}`)
	if res.out != "HELLO THERE" || res.exit != 0 {
		t.Fatalf("stdin: %+v", res)
	}
	// A terminal of its own, with a size the client may change.
	res = runExec(t, s, `{"script":"sleep 0.2; stty size; test -t 0 && echo onatty","tty":true}`, `{"resize":[100,30]}`)
	if !strings.Contains(res.out, "30 100") || !strings.Contains(res.out, "onatty") || res.exit != 0 {
		t.Fatalf("tty: %+v", res)
	}
	// A killed script says so the way a shell does.
	res = runExec(t, s, `{"script":"kill -9 $$","tty":false}`)
	if res.exit != 137 {
		t.Fatalf("killed: %+v", res)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	k, err := s.Socket(ctx, "/exec", nil)
	if err != nil {
		t.Fatal(err)
	}
	k.Send(ctx, `{"tty":false}`)
	if _, err := k.Recv(ctx); err == nil {
		t.Fatal("a start with no script was run")
	}
	if _, err := s.Socket(ctx, "/nothing", nil); err == nil {
		t.Fatal("a socket nobody serves opened")
	}
}

// TestTunnelRoutesOnlyThroughTheTunnel: the gateway's handler refuses a
// request that did not come through the relay's session.
func TestTunnelRoutesOnlyThroughTheTunnel(t *testing.T) {
	r := newRig(t)
	a, _ := New(r.opts)
	ts := httptest.NewServer(a.tunnelHandler())
	defer ts.Close()
	for _, p := range []string{"/bridge", "/exec"} {
		res, err := http.Get(ts.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusForbidden {
			t.Errorf("%s over the network: %d", p, res.StatusCode)
		}
	}
}

// TestExecScriptIgnoringHangupIsKilled: a client that goes ends its script
// even when the script ignores SIGHUP — with or without a terminal — so
// the handler, its terminal and the tunnel's stream are not held for ever.
func TestExecScriptIgnoringHangupIsKilled(t *testing.T) {
	was := execKillAfter
	execKillAfter = 200 * time.Millisecond
	t.Cleanup(func() { execKillAfter = was })
	r, rs, secret := relayRig(t)
	s := dialMachine(t, r, rs, secret)
	for _, tty := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		k, err := s.Socket(ctx, "/exec", nil)
		if err != nil {
			t.Fatal(err)
		}
		start, _ := json.Marshal(map[string]any{"script": `trap '' HUP; echo "pid=$$."; sleep 60`, "tty": tty})
		k.Send(ctx, string(start))
		var out string
		pid := 0
		for pid == 0 {
			m, err := k.Recv(ctx)
			if err != nil {
				t.Fatalf("tty %v: %v (%q)", tty, err, out)
			}
			var o struct{ Out string }
			json.Unmarshal([]byte(m), &o)
			b, _ := base64.StdEncoding.DecodeString(o.Out)
			out += string(b)
			if i := strings.Index(out, "pid="); i >= 0 {
				if j := strings.Index(out[i:], "."); j > 0 {
					pid, _ = strconv.Atoi(out[i+4 : i+j])
				}
			}
		}
		k.Close()
		deadline := time.Now().Add(5 * time.Second)
		for syscall.Kill(pid, 0) == nil { // reaped only once the handler has waited for it
			if time.Now().After(deadline) {
				t.Fatalf("tty %v: the script (pid %d) outlived its client", tty, pid)
			}
			time.Sleep(20 * time.Millisecond)
		}
		cancel()
	}
}

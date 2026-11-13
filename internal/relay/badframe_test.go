package relay

import (
	"bytes"
	"compress/flate"
	"context"
	"crypto/rand"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/flynn/noise"
)

// A frame refused partway — longer than maxFrame, or not JSON with more
// behind it in the same message — leaves the rest of its message in the
// pipe the opener decrypts into. The decrypting goroutine waited in that
// pipe's Write for a reader that had gone, for ever: one goroutine and its
// message kept for each bad frame, and anybody holding the relay key (every
// pairing link, until `conch web relay reset`) could send as many as they
// liked. In review, twenty sessions left twenty.

// badFrames are the plaintexts of one Noise message each, every one a frame
// the reader refuses before reaching the end of the message.
var badFrames = map[string][]byte{
	"over maxFrame":           framed(maxFrame+1, []byte("and some of it")),
	"not JSON, more after it": append(framed(3, []byte("{{{")), framed(2, []byte("{}"))...),
}

// framed is a frame's length prefix saying n, and then body.
func framed(n uint32, body []byte) []byte {
	var head [4]byte
	binary.BigEndian.PutUint32(head[:], n)
	return append(head[:], body...)
}

// deflated is plain as a gateway's sealer sends it, with incompressible
// bytes behind it. The inflater reads ahead into its 32 KB window, so a
// message shorter than that is read whole whatever the frame in it; only
// one longer than the window — near a Noise message's 64 KB — is left
// half-read in the pipe.
func deflated(t *testing.T, plain []byte) []byte {
	t.Helper()
	noise := make([]byte, 60<<10)
	if _, err := rand.Read(noise); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	z, _ := flate.NewWriter(&b, flate.BestSpeed)
	if _, err := z.Write(append(append([]byte{}, plain...), noise...)); err != nil {
		t.Fatal(err)
	}
	if err := z.Flush(); err != nil {
		t.Fatal(err)
	}
	if b.Len() > maxPlain {
		t.Fatalf("%d bytes is over one Noise message", b.Len())
	}
	return b.Bytes()
}

// sendRaw seals plain into one Noise message and sends it, as a sealer
// would a frame it had made itself.
func sendRaw(ctx context.Context, c *websocket.Conn, cs *noise.CipherState, plain []byte) error {
	msg, err := cs.Encrypt(nil, nil, plain)
	if err != nil {
		return err
	}
	return c.Write(ctx, websocket.MessageBinary, msg)
}

// settledGoroutines is the goroutine count once it stops moving.
func settledGoroutines() int {
	n := runtime.NumGoroutine()
	for range 100 {
		time.Sleep(20 * time.Millisecond)
		m := runtime.NumGoroutine()
		if m == n {
			break
		}
		n = m
	}
	return n
}

// TestABadFrameFromAPhoneLeavesNothing: the gateway's end, through a real
// relay and link.
func TestABadFrameFromAPhoneLeavesNothing(t *testing.T) {
	for name, plain := range badFrames {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.dial(t).Close() // warms up what stays: the link's client and listener
			base := settledGoroutines()
			for i := range 20 {
				s := f.dial(t)
				s.out.mu.Lock()
				err := sendRaw(ctx10(t), s.c, s.out.cs, plain)
				s.out.mu.Unlock()
				if err != nil {
					t.Fatalf("session %d: %v", i, err)
				}
				// The gateway ends the session over it, which the phone hears.
				select {
				case <-s.ctx.Done():
				case <-time.After(10 * time.Second):
					t.Fatalf("session %d: the gateway kept a session that sent a bad frame", i)
				}
				s.Close()
			}
			waitFor(t, "the sessions' goroutines to end", func() bool { return runtime.NumGoroutine() <= base+3 })
			// And the gateway still takes a phone that behaves.
			if res, err := f.dial(t).Do(ctx10(t), "GET", "/api/echo", nil, ""); err != nil || res.S != 200 {
				t.Fatalf("a good session after the bad ones: %v %+v", err, res)
			}
		})
	}
}

// TestABadFrameFromAGatewayLeavesNothing: the Go phone's end, against a
// gateway that answers the handshake and then sends a bad frame. A relay
// cannot make one — it has no key — but a gateway with a bug can, and the
// phone's reader has the same pipe.
func TestABadFrameFromAGatewayLeavesNothing(t *testing.T) {
	key, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	for name, plain := range badFrames {
		t.Run(name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				c, err := websocket.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer c.CloseNow()
				c.SetReadLimit(maxMessage)
				_, send, err := respond(r.Context(), c, key)
				if err != nil {
					return
				}
				if sendRaw(r.Context(), c, send, deflated(t, plain)) != nil {
					return
				}
				// Stay, as a gateway would: the phone has to end it itself.
				_, _, _ = c.Read(r.Context())
			}))
			t.Cleanup(ts.Close)
			dial := func() *Session {
				s, err := Dial(ctx10(t), ts.URL, "room", key.Public)
				if err != nil {
					t.Fatalf("dial: %v", err)
				}
				return s
			}
			s := dial()
			<-s.ctx.Done()
			s.Close()
			base := settledGoroutines()
			for i := range 20 {
				s := dial()
				select {
				case <-s.ctx.Done():
				case <-time.After(10 * time.Second):
					t.Fatalf("session %d: the phone kept a session with a bad frame in it", i)
				}
				if _, err := s.Do(ctx10(t), "GET", "/api/echo", nil, ""); err == nil {
					t.Fatalf("session %d: a call on a session that has ended", i)
				}
				s.Close()
			}
			waitFor(t, "the sessions' goroutines to end", func() bool { return runtime.NumGoroutine() <= base+3 })
		})
	}
}

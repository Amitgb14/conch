package cloudagent

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Amitgb14/conch/internal/proto"
)

// device is a push subscription's own end: its key pair and auth secret,
// which it alone holds.
type device struct {
	id   string
	key  *ecdh.PrivateKey
	auth []byte
}

func newDevice(t *testing.T, id string) device {
	k, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	rand.Read(auth)
	return device{id: id, key: k, auth: auth}
}

func (d device) target(kind string, max int) map[string]any {
	return map[string]any{"device_id": d.id, "kind": kind, "max_ciphertext_bytes": max,
		"p256dh": base64.RawURLEncoding.EncodeToString(d.key.PublicKey().Bytes()), "auth": base64.RawURLEncoding.EncodeToString(d.auth)}
}

// decrypt is RFC 8291 from the device's side: what a browser or the
// app's notification extension does.
func (d device) decrypt(body []byte) ([]byte, error) {
	if len(body) < 21 {
		return nil, errors.New("short")
	}
	salt, rs, idlen := body[:16], binary.BigEndian.Uint32(body[16:20]), int(body[20])
	if len(body) < 21+idlen || rs < 18 {
		return nil, errors.New("bad header")
	}
	asPub, err := ecdh.P256().NewPublicKey(body[21 : 21+idlen])
	if err != nil {
		return nil, err
	}
	shared, err := d.key.ECDH(asPub)
	if err != nil {
		return nil, err
	}
	info := "WebPush: info\x00" + string(d.key.PublicKey().Bytes()) + string(asPub.Bytes())
	ikm, _ := hkdf.Key(sha256.New, shared, d.auth, info, 32)
	prk, _ := hkdf.Extract(sha256.New, ikm, salt)
	cek, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	block, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, nonce, body[21+idlen:], nil)
	if err != nil {
		return nil, err
	}
	plain = []byte(strings.TrimRight(string(plain), "\x00"))
	if len(plain) == 0 || plain[len(plain)-1] != 2 {
		return nil, errors.New("not the last record")
	}
	return plain[:len(plain)-1], nil
}

// TestPushWhenAnAgentWaits: an agent starting to wait is pushed to every
// device, encrypted to each device's key — and only the device can read
// it — within each one's limit.
func TestPushWhenAnAgentWaits(t *testing.T) {
	r := newRig(t)
	web, app := newDevice(t, "d_aaaaaaaaaaaaaaaa"), newDevice(t, "d_bbbbbbbbbbbbbbbb")
	r.cloud.handle("GET /v1/vm/push/targets", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"targets": []any{web.target("webpush", 4096), app.target("apns", 2600)}})
	})
	r.startServer()
	r.run()
	r.cloud.waitFor("POST /v1/vm/hello", 1, 10*time.Second)
	ag := r.pane(proto.PaneCreateParams{Agent: "claude", Name: "fix the login", Command: []string{"/bin/sh", "-c", "exec cat"}})
	r.report(ag.ID, "UserPromptSubmit")
	r.report(ag.ID, "PermissionRequest")
	pushes := r.cloud.waitFor("POST /v1/vm/push", 1, 10*time.Second)
	var vp VMPush
	json.Unmarshal(pushes[0].Body, &vp)
	if vp.Urgency != "high" || len(vp.Payloads) != 2 {
		t.Fatalf("push %s", pushes[0].Body)
	}
	for _, p := range vp.Payloads {
		d, max := web, 4096
		if p.DeviceID == app.id {
			d, max = app, 2600
		}
		ct, err := base64.RawURLEncoding.DecodeString(p.Ciphertext)
		if err != nil || len(ct) > max {
			t.Fatalf("%s: %d bytes (%v)", p.DeviceID, len(ct), err)
		}
		plain, err := d.decrypt(ct)
		if err != nil {
			t.Fatalf("%s cannot read its push: %v", p.DeviceID, err)
		}
		var m PushMessage
		json.Unmarshal(plain, &m)
		if m != (PushMessage{Type: "waiting", Machine: testMachine, Pane: ag.ID, Name: "fix the login"}) {
			t.Fatalf("%s read %s", p.DeviceID, plain)
		}
		other := web
		if d.id == web.id {
			other = app
		}
		if _, err := other.decrypt(ct); err == nil {
			t.Fatalf("%s's push opens with another device's key", p.DeviceID)
		}
	}

	// Waiting again after working: pushed again, with the targets kept.
	r.report(ag.ID, "UserPromptSubmit")
	r.report(ag.ID, "PermissionRequest")
	r.cloud.waitFor("POST /v1/vm/push", 2, 10*time.Second)
	if n := len(r.cloud.requests("GET /v1/vm/push/targets")); n != 1 {
		t.Fatalf("targets fetched %d times inside five minutes", n)
	}
}

func TestNoTargetsNoPush(t *testing.T) {
	r := newRig(t)
	r.startServer()
	r.run()
	r.cloud.waitFor("POST /v1/vm/hello", 1, 10*time.Second)
	ag := r.pane(proto.PaneCreateParams{Agent: "claude", Command: []string{"/bin/sh", "-c", "exec cat"}})
	r.report(ag.ID, "PermissionRequest")
	r.cloud.waitFor("GET /v1/vm/push/targets", 1, 10*time.Second)
	time.Sleep(100 * time.Millisecond)
	if n := len(r.cloud.requests("POST /v1/vm/push")); n != 0 {
		t.Fatalf("%d pushes with no devices", n)
	}
}

// TestPushFits: a notification over a device's limit loses words, never
// the machine or the pane.
func TestPushFits(t *testing.T) {
	d := newDevice(t, "d_cccccccccccccccc")
	msg := PushMessage{Type: "waiting", Machine: testMachine, Pane: "p12", Name: strings.Repeat("ü", 300), Project: strings.Repeat("p", 200)}
	tgt := func(max int) PushTarget {
		return PushTarget{DeviceID: d.id, Kind: "apns", MaxCiphertextBytes: max,
			P256dh: base64.RawURLEncoding.EncodeToString(d.key.PublicKey().Bytes()), Auth: base64.RawURLEncoding.EncodeToString(d.auth)}
	}
	for _, max := range []int{4096, 2600, 400, 200} {
		ct, err := encryptFor(msg, tgt(max))
		if err != nil {
			t.Fatalf("%d: %v", max, err)
		}
		if len(ct) > max {
			t.Fatalf("%d: %d bytes", max, len(ct))
		}
		plain, err := d.decrypt(ct)
		if err != nil {
			t.Fatal(err)
		}
		var m PushMessage
		if err := json.Unmarshal(plain, &m); err != nil || m.Pane != "p12" || m.Machine != testMachine || !utf8.ValidString(m.Name) {
			t.Fatalf("%d: %s", max, plain)
		}
		if max == 4096 && m.Name != msg.Name {
			t.Fatal("cut although it fitted")
		}
		if max == 200 && (m.Name != "" || len(m.Project) == 200) {
			t.Fatalf("200: %s", plain)
		}
	}
	if _, err := encryptFor(msg, tgt(120)); err == nil {
		t.Fatal("no room even for the pane, and no error")
	}
	bad := tgt(4096)
	bad.P256dh = "not-a-key"
	if _, err := encryptFor(msg, bad); err == nil {
		t.Fatal("a bad key, and no error")
	}
}

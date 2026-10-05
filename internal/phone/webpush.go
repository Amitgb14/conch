package phone

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Web Push, as a browser's push service takes it: the message encrypted
// for the one browser that subscribed (RFC 8291, aes128gcm), and the
// request signed with this gateway's VAPID key (RFC 8292), so the push
// service knows every message for a subscription comes from the gateway
// that made it. Only the standard library: the push service is the one
// other party that sees any of this, and it sees ciphertext.

// pushRecordSize is the record size written in the header. The payload
// is far smaller, so there is one record.
const pushRecordSize = 4096

// b64 decodes base64url with or without padding, as browsers hand out
// subscription keys either way.
func b64(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
}

// encryptPush encrypts payload for a subscription (RFC 8291). asKey and
// salt are fresh for every message; they are parameters so the RFC's
// worked example can be checked.
func encryptPush(payload []byte, keys PushKeys, asKey *ecdh.PrivateKey, salt []byte) ([]byte, error) {
	uaRaw, err := b64(keys.P256dh)
	if err != nil {
		return nil, fmt.Errorf("p256dh: %w", err)
	}
	uaPublic, err := ecdh.P256().NewPublicKey(uaRaw)
	if err != nil {
		return nil, fmt.Errorf("p256dh: %w", err)
	}
	auth, err := b64(keys.Auth)
	if err != nil || len(auth) != 16 {
		return nil, errors.New("auth: not 16 bytes of base64url")
	}
	if len(salt) != 16 {
		return nil, errors.New("salt: not 16 bytes")
	}
	shared, err := asKey.ECDH(uaPublic)
	if err != nil {
		return nil, err
	}
	asPublic := asKey.PublicKey().Bytes()

	// The shared secret mixed with the browser's auth secret, bound to
	// both public keys.
	info := "WebPush: info\x00" + string(uaRaw) + string(asPublic)
	ikm, err := hkdf.Key(sha256.New, shared, auth, info, 32)
	if err != nil {
		return nil, err
	}
	prk, err := hkdf.Extract(sha256.New, ikm, salt)
	if err != nil {
		return nil, err
	}
	cek, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(payload)+1+gcm.Overhead() > pushRecordSize {
		return nil, errors.New("payload too large for one record")
	}
	// The one record is the last: its delimiter is 2, with no padding.
	plain := append(append([]byte{}, payload...), 2)

	var body bytes.Buffer
	body.Write(salt)
	binary.Write(&body, binary.BigEndian, uint32(pushRecordSize))
	body.WriteByte(byte(len(asPublic)))
	body.Write(asPublic)
	body.Write(gcm.Seal(nil, nonce, plain, nil))
	return body.Bytes(), nil
}

// vapidSubject is who the push service can reach about this sender. There
// is no one person behind every conch, so it names the project; Apple
// requires one to be given.
const vapidSubject = "https://github.com/Amitgb14/conch"

// vapidAuth is the Authorization header for a push to endpoint: a JWT for
// the push service's origin, signed with the gateway's key (RFC 8292).
func vapidAuth(key *ecdh.PrivateKey, endpoint string, now time.Time) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	signer, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), key.Bytes())
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding
	header := enc.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims, _ := json.Marshal(map[string]any{
		"aud": u.Scheme + "://" + u.Host,
		"exp": now.Add(12 * time.Hour).Unix(), // the most push services accept is 24h
		"sub": vapidSubject,
	})
	signing := header + "." + enc.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signing))
	r, s, err := ecdsa.Sign(rand.Reader, signer, digest[:])
	if err != nil {
		return "", err
	}
	// JWS wants r and s as 32 bytes each, not DER.
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return "vapid t=" + signing + "." + enc.EncodeToString(sig) + ", k=" + enc.EncodeToString(key.PublicKey().Bytes()), nil
}

// pushHosts are the push services browsers subscribe with. Pushes go only
// to these: an endpoint is an address a phone gave the laptop, and the
// laptop POSTing to any address it is handed would let a paired device
// reach whatever the laptop can.
var pushHosts = []string{
	"fcm.googleapis.com",                // Chrome, Android
	"updates.push.services.mozilla.com", // Firefox
	"web.push.apple.com",                // Safari, iOS
	".notify.windows.com",               // Edge
}

// knownPushService reports whether an endpoint is https on a push
// service's host.
func knownPushService(endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, h := range pushHosts {
		if host == h || strings.HasPrefix(h, ".") && strings.HasSuffix(host, h) && len(host) > len(h) {
			return true
		}
	}
	return false
}

// PushMessage is what a notification carries: deliberately little, since
// it passes through Apple's or Google's servers and a question can quote
// a command. No screen text and no question text.
type PushMessage struct {
	Type    string `json:"type"` // waiting or done
	Pane    string `json:"pane"`
	Name    string `json:"name"`
	Project string `json:"project,omitempty"`
	URL     string `json:"url"`
}

// errGone is a subscription the push service says no longer exists: the
// browser unsubscribed, or the app was removed.
var errGone = errors.New("the subscription is gone")

// sendPush delivers one message to one subscription.
func sendPush(ctx context.Context, hc *http.Client, key *ecdh.PrivateKey, sub PushSubscription, msg PushMessage, now time.Time) error {
	payload, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	asKey, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	body, err := encryptPush(payload, sub.Keys, asKey, salt)
	if err != nil {
		return err
	}
	auth, err := vapidAuth(key, sub.Endpoint, now)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Authorization", auth)
	// A question is worth waking a phone for; a finished agent can wait
	// for the phone's next look. Either is stale after a day.
	req.Header.Set("TTL", "86400")
	if msg.Type == StateWaiting {
		req.Header.Set("Urgency", "high")
	} else {
		req.Header.Set("Urgency", "normal")
	}
	// One undelivered message per pane: a newer one replaces it.
	// A topic is a URL-safe token (RFC 8030), and a pane ID now holds a
	// colon, which is not: the machine and the pane are joined with a dash
	// instead, so a newer notification still replaces an older one for the
	// same pane.
	req.Header.Set("Topic", "conch-"+strings.ReplaceAll(msg.Pane, ":", "-"))
	res, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
	switch {
	case res.StatusCode == http.StatusNotFound || res.StatusCode == http.StatusGone:
		return errGone
	case res.StatusCode < 200 || res.StatusCode > 299:
		return fmt.Errorf("the push service answered %d", res.StatusCode)
	}
	return nil
}

package phone

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
	"time"
)

func mustB64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := b64(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// RFC 8291's own worked example (Appendix A): with its keys and salt, the
// message encrypts to exactly its bytes.
func TestEncryptPushRFC8291Example(t *testing.T) {
	asKey, err := ecdh.P256().NewPrivateKey(mustB64(t, "yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"))
	if err != nil {
		t.Fatal(err)
	}
	keys := PushKeys{
		P256dh: "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4",
		Auth:   "BTBZMqHH6r4Tts7J_aSIgg",
	}
	body, err := encryptPush([]byte("When I grow up, I want to be a watermelon"), keys, asKey, mustB64(t, "DGv6ra1nlYgDCS1FRnbzlw"))
	if err != nil {
		t.Fatal(err)
	}
	want := "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPTpK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN"
	if got := base64.RawURLEncoding.EncodeToString(body); got != want {
		t.Fatalf("encrypted:\n%s\nwant:\n%s", got, want)
	}
}

func TestEncryptPushRefusesBadKeys(t *testing.T) {
	asKey, _ := ecdh.P256().GenerateKey(rand.Reader)
	ua, _ := ecdh.P256().GenerateKey(rand.Reader)
	good := PushKeys{P256dh: base64.RawURLEncoding.EncodeToString(ua.PublicKey().Bytes()), Auth: "BTBZMqHH6r4Tts7J_aSIgg"}
	salt := make([]byte, 16)
	if _, err := encryptPush([]byte("{}"), good, asKey, salt); err != nil {
		t.Fatal(err)
	}
	// Padded base64, as some browsers give it, is the same key.
	padded := good
	padded.Auth += "=="
	if _, err := encryptPush([]byte("{}"), padded, asKey, salt); err != nil {
		t.Fatalf("padded: %v", err)
	}
	for name, keys := range map[string]PushKeys{
		"no key":          {Auth: good.Auth},
		"not base64":      {P256dh: "!!!", Auth: good.Auth},
		"not a point":     {P256dh: base64.RawURLEncoding.EncodeToString(make([]byte, 65)), Auth: good.Auth},
		"short auth":      {P256dh: good.P256dh, Auth: "AAAA"},
		"auth not base64": {P256dh: good.P256dh, Auth: "@@@@@@@@@@@@@@@@@@@@@@"},
	} {
		if _, err := encryptPush([]byte("{}"), keys, asKey, salt); err == nil {
			t.Errorf("%s: encrypted", name)
		}
	}
	if _, err := encryptPush([]byte("{}"), good, asKey, make([]byte, 8)); err == nil {
		t.Error("a short salt")
	}
	if _, err := encryptPush(make([]byte, pushRecordSize), good, asKey, salt); err == nil {
		t.Error("a payload larger than a record")
	}
}

// The VAPID header is a JWT for the push service's origin, signed with
// the gateway's key, and names that key.
func TestVAPIDAuth(t *testing.T) {
	key, _ := ecdh.P256().GenerateKey(rand.Reader)
	now := time.Date(2026, 9, 30, 9, 0, 0, 0, time.UTC)
	auth, err := vapidAuth(key, "https://fcm.googleapis.com/fcm/send/abc:def?x=1", now)
	if err != nil {
		t.Fatal(err)
	}
	rest, ok := strings.CutPrefix(auth, "vapid t=")
	if !ok {
		t.Fatalf("header %q", auth)
	}
	jwt, k, ok := strings.Cut(rest, ", k=")
	if !ok || k != base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()) {
		t.Fatalf("k in %q", auth)
	}
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("jwt %q", jwt)
	}
	var header, claims map[string]any
	json.Unmarshal(mustB64(t, parts[0]), &header)
	json.Unmarshal(mustB64(t, parts[1]), &claims)
	if header["alg"] != "ES256" || header["typ"] != "JWT" {
		t.Fatalf("header %v", header)
	}
	if claims["aud"] != "https://fcm.googleapis.com" || claims["sub"] != vapidSubject ||
		int64(claims["exp"].(float64)) != now.Add(12*time.Hour).Unix() {
		t.Fatalf("claims %v", claims)
	}
	sig := mustB64(t, parts[2])
	if len(sig) != 64 {
		t.Fatalf("signature of %d bytes", len(sig))
	}
	pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), key.PublicKey().Bytes())
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	r, s := new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])
	if !ecdsa.Verify(pub, digest[:], r, s) {
		t.Fatal("the signature doesn't verify with the gateway's key")
	}
	if _, err := vapidAuth(key, "://nope", now); err == nil {
		t.Fatal("a header for an address that isn't one")
	}
}

func TestKnownPushServices(t *testing.T) {
	for endpoint, want := range map[string]bool{
		"https://fcm.googleapis.com/fcm/send/abc":                  true,
		"https://FCM.googleapis.com/fcm/send/abc":                  true,
		"https://updates.push.services.mozilla.com/wpush/v2/abc":   true,
		"https://web.push.apple.com/QGd2...":                       true,
		"https://wns2-by3p.notify.windows.com/w/?token=abc":        true,
		"https://notify.windows.com/x":                             false, // the suffix, not the host
		"https://evilnotify.windows.com.example/x":                 false,
		"https://fcm.googleapis.com.evil.example/x":                false,
		"http://fcm.googleapis.com/fcm/send/abc":                   false,
		"https://fcm.googleapis.com:8443/fcm/send/abc":             false,
		"https://user@fcm.googleapis.com/fcm/send/abc":             false,
		"https://192.168.1.1/admin":                                false,
		"https://localhost/x":                                      false,
		"https://evil.example/?u=https://fcm.googleapis.com/fcm/x": false,
		"":           false,
		"not a url":  false,
		"https://":   false,
		"javascript": false,
	} {
		if got := knownPushService(endpoint); got != want {
			t.Errorf("%q: %v, want %v", endpoint, got, want)
		}
	}
}

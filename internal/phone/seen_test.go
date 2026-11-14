package phone

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// A phone paired with "name" rather than "device_name" keeps it, and one
// that only makes API calls — no socket — is still seen (#199: listed as
// "phone", seen "never").
func TestPairNameAndSeenByCalls(t *testing.T) {
	f := newFixture(t)
	f.forgetAttempts()
	code, err := f.store.NewCode(PermView, f.now())
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.Post(f.web.URL+"/pair", "application/json", strings.NewReader(`{"code":"`+code+`","name":"e2e-phone"}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("pair: %d %s", res.StatusCode, b)
	}
	devs, _ := f.store.Devices()
	if len(devs) != 1 || devs[0].Name != "e2e-phone" {
		t.Fatalf("devices %+v", devs)
	}
	if p, ok := f.store.Presence()[devs[0].ID]; ok && !p.LastSeen.IsZero() {
		t.Fatalf("seen before any call: %+v", p)
	}
	var token string
	for _, ck := range res.Cookies() {
		if ck.Name == CookieName {
			token = ck.Value
		}
	}
	ph := &fakePhone{t: t, base: f.web.URL, id: devs[0].ID, token: token}
	if status := ph.get("/api/hello", nil); status != 200 {
		t.Fatalf("hello: %d", status)
	}
	p := f.store.Presence()[devs[0].ID]
	if p.LastSeen.IsZero() || p.Connected {
		t.Fatalf("after a call: %+v", p)
	}
	if w := SeenWords(p, time.Now()); w != "just now" {
		t.Fatalf("seen %q", w)
	}
}

// device_name, which the app sends, wins over name when both are given.
func TestPairDeviceNameWins(t *testing.T) {
	f := newFixture(t)
	f.forgetAttempts()
	code, _ := f.store.NewCode(PermView, f.now())
	res, err := http.Post(f.web.URL+"/pair", "application/json", strings.NewReader(`{"code":"`+code+`","device_name":"app","name":"other"}`))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if devs, _ := f.store.Devices(); len(devs) != 1 || devs[0].Name != "app" {
		t.Fatalf("devices %+v", devs)
	}
}

// Calls are recorded at most once a minute a device, since each is a
// write of phone.json.
func TestSeenByCallsThrottled(t *testing.T) {
	f := newFixture(t)
	ph := f.pair(PermView)
	first := f.store.Presence()[ph.id].LastSeen
	if first.IsZero() {
		t.Fatal("not seen after pairing's hello")
	}
	f.clock.Add(int64(10 * time.Second))
	ph.get("/api/hello", nil)
	if got := f.store.Presence()[ph.id].LastSeen; !got.Equal(first) {
		t.Fatalf("written again within the minute: %v then %v", first, got)
	}
	f.clock.Add(int64(2 * time.Minute))
	ph.get("/api/hello", nil)
	if got := f.store.Presence()[ph.id].LastSeen; !got.After(first) {
		t.Fatalf("not written after a minute: %v then %v", first, got)
	}
}

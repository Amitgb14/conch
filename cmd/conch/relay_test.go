package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/phone"
	"github.com/Amitgb14/conch/internal/relay"
)

// With a relay set, pairing points at the relay — room and key in the
// link's fragment, never the room's secret — and says the key's words.
func TestWebPairThroughARelay(t *testing.T) {
	dir := a4Env(t)
	store := phone.OpenStore(dir)
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[web]\nrelay = \"wss://relay.example.com\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := stdoutIsTerminal
	t.Cleanup(func() { stdoutIsTerminal = old })
	stdoutIsTerminal = func() bool { return false }
	out, err := runWebCaptured(t, "pair")
	if err != nil {
		t.Fatal(err)
	}
	secret, key, err := store.Relay()
	if err != nil {
		t.Fatal(err)
	}
	words := relay.Fingerprint(key.Public)
	for _, want := range []string{"https://relay.example.com", words, "isn't connected to the relay yet"} {
		if !strings.Contains(out, want) {
			t.Errorf("pair through a relay doesn't say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, secret) {
		t.Fatal("the room's secret was printed")
	}

	out, err = runWebCaptured(t, "pair", "-json")
	if err != nil {
		t.Fatal(err)
	}
	var p pairJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &p); err != nil {
		t.Fatalf("%q: %v", out, err)
	}
	want := "https://relay.example.com/#code=" + p.Code + "&room=" + relay.RoomID(secret) + "&key=" + relay.EncodeKey(key.Public)
	if p.Link != want || p.Relay != "https://relay.example.com" || p.KeyWords != words || p.RelayConnected {
		t.Fatalf("pair -json through a relay: %+v\nwant link %s", p, want)
	}
	if strings.Contains(out, secret) {
		t.Fatal("the room's secret is in the JSON")
	}

	// Connected, once conch web says so.
	store.SetRelayState(&phone.RelayState{URL: "wss://relay.example.com", PID: os.Getpid(), Connected: true, Since: time.Now()}) //nolint:errcheck
	out, _ = runWebCaptured(t, "pair", "-json")
	p = pairJSON{}
	json.Unmarshal([]byte(strings.TrimSpace(out)), &p) //nolint:errcheck
	if !p.RelayConnected {
		t.Fatalf("connected, and pair doesn't say so: %+v", p)
	}
}

func TestWebRelay(t *testing.T) {
	dir := a4Env(t)
	store := phone.OpenStore(dir)
	out, err := runWebCaptured(t, "relay")
	if err != nil || !strings.Contains(out, "no relay") {
		t.Fatalf("no relay set: %q %v", out, err)
	}
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[web]\nrelay = \"wss://relay.example.com\"\n"), 0o600) //nolint:errcheck
	out, err = runWebCaptured(t, "relay")
	_, key, _ := store.Relay()
	if err != nil || !strings.Contains(out, relay.Fingerprint(key.Public)) || !strings.Contains(out, "conch web isn't running") {
		t.Fatalf("relay set, conch web not running: %q %v", out, err)
	}
	store.SetRelayState(&phone.RelayState{URL: "wss://relay.example.com", PID: os.Getpid(), Error: "connection refused"}) //nolint:errcheck
	if out, _ = runWebCaptured(t, "relay"); !strings.Contains(out, "not connected: connection refused") {
		t.Fatalf("not connected: %q", out)
	}
	store.SetRelayState(&phone.RelayState{URL: "wss://other.example.com", PID: os.Getpid(), Connected: true}) //nolint:errcheck
	if out, _ = runWebCaptured(t, "relay"); !strings.Contains(out, "not this one: restart it") {
		t.Fatalf("connected to another relay: %q", out)
	}
	store.SetRelayState(&phone.RelayState{URL: "wss://relay.example.com", PID: os.Getpid(), Connected: true, Since: time.Now()}) //nolint:errcheck
	if out, _ = runWebCaptured(t, "relay"); !regexp.MustCompile(`connected since \d\d:\d\d`).MatchString(out) {
		t.Fatalf("connected: %q", out)
	}

	out, err = runWebCaptured(t, "relay", "reset")
	_, again, _ := store.Relay()
	if err != nil || !strings.Contains(out, "pair again") || string(again.Public) == string(key.Public) {
		t.Fatalf("reset: %q %v", out, err)
	}
	if _, err := runWebCaptured(t, "relay", "bogus"); err == nil {
		t.Fatal("conch web relay bogus was taken")
	}
}

func TestWebServeRefusesABadRelay(t *testing.T) {
	a4Env(t)
	for _, bad := range []string{"relay.example.com", "ftp://x"} {
		if _, err := runWebCaptured(t, "-relay", bad); err == nil || !strings.Contains(err.Error(), "-relay") {
			t.Errorf("-relay %q: %v", bad, err)
		}
	}
}

func TestRelayArgs(t *testing.T) {
	for _, args := range [][]string{{"extra"}, {"-cert", "a"}, {"-key", "b"}, {"-listen", "not an address:"}} {
		if err := runRelay(args); err == nil {
			t.Errorf("conch relay %v was taken", args)
		}
	}
}

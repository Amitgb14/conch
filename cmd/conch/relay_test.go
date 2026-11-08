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
	if p.Link != want || p.Relay != "https://relay.example.com" || p.KeyWords != words || p.RelayConnected ||
		!strings.Contains(p.RelayStatus, "conch web isn't running") {
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
	if !p.RelayConnected || !regexp.MustCompile(`^connected since \d\d:\d\d$`).MatchString(p.RelayStatus) {
		t.Fatalf("connected, and pair doesn't say so: %+v", p)
	}

	// Running, and the relay refusing it: the window says why.
	store.SetRelayState(&phone.RelayState{URL: "wss://relay.example.com", PID: os.Getpid(), Error: "connection refused"}) //nolint:errcheck
	out, _ = runWebCaptured(t, "pair", "-json")
	p = pairJSON{}
	json.Unmarshal([]byte(strings.TrimSpace(out)), &p) //nolint:errcheck
	if p.RelayConnected || p.RelayStatus != "not connected: connection refused" {
		t.Fatalf("refused: %+v", p)
	}
	// Running, and not yet said either way.
	store.SetRelayState(&phone.RelayState{URL: "wss://relay.example.com", PID: os.Getpid()}) //nolint:errcheck
	out, _ = runWebCaptured(t, "pair", "-json")
	p = pairJSON{}
	json.Unmarshal([]byte(strings.TrimSpace(out)), &p) //nolint:errcheck
	if p.RelayConnected || p.RelayStatus != "connecting…" {
		t.Fatalf("connecting: %+v", p)
	}
}

// Without a relay, pair -json says nothing about one.
func TestWebPairJSONWithoutARelay(t *testing.T) {
	a4Env(t)
	out, err := runWebCaptured(t, "pair", "-json")
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"relay", "key_words", "relay_status", "relay_connected"} {
		if strings.Contains(out, `"`+k+`"`) {
			t.Errorf("pair -json without a relay has %q: %s", k, out)
		}
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

// conch web devices says which phones are on now and how; -json adds how
// the relay is, for Conch.app's phone icon to show in one call.
func TestWebDevicesPresence(t *testing.T) {
	dir := a4Env(t)
	store := phone.OpenStore(dir)
	out, err := runWebCaptured(t, "devices", "-json")
	if err != nil {
		t.Fatal(err)
	}
	var j devicesJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &j); err != nil || j.Devices == nil || len(j.Devices) != 0 || j.Running || j.Relay != "" {
		t.Fatalf("none paired: %q %+v %v", out, j, err)
	}
	now := time.Now()
	var ids []string
	for _, name := range []string{"iPhone", "Pixel", "iPad"} {
		code, _ := store.NewCode(phone.PermFull, now)
		d, _, err := store.Redeem(code, name, now)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, d.ID)
	}
	store.SocketOpened(os.Getpid(), ids[0], phone.RouteRelay, now)                                                 //nolint:errcheck
	store.SocketOpened(os.Getpid(), ids[1], phone.RouteDirect, now.Add(-time.Hour))                                //nolint:errcheck
	store.SocketClosed(os.Getpid(), ids[1], now.Add(-time.Hour))                                                   //nolint:errcheck
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[web]\nrelay = \"wss://relay.example.com\"\n"), 0o600) //nolint:errcheck

	out, err = runWebCaptured(t, "devices")
	if err != nil || !strings.Contains(out, "SEEN") || !strings.Contains(out, "on · relay") || !strings.Contains(out, "never") {
		t.Fatalf("devices: %q %v", out, err)
	}
	out, err = runWebCaptured(t, "devices", "-json")
	j = devicesJSON{}
	if err != nil || json.Unmarshal([]byte(strings.TrimSpace(out)), &j) != nil || len(j.Devices) != 3 {
		t.Fatalf("devices -json: %q %v", out, err)
	}
	on, off, never := j.Devices[0], j.Devices[1], j.Devices[2]
	if !on.Connected || on.Route != phone.RouteRelay || on.Since == nil || on.LastSeen == nil {
		t.Errorf("on through the relay: %+v", on)
	}
	if off.Connected || off.Route != phone.RouteDirect || off.Since != nil || off.LastSeen == nil {
		t.Errorf("seen an hour ago: %+v", off)
	}
	if never.Connected || never.LastSeen != nil || never.Route != "" {
		t.Errorf("never seen: %+v", never)
	}
	if j.Relay != "wss://relay.example.com" || j.RelayConnected || !strings.Contains(j.RelayStatus, "isn't running") {
		t.Errorf("relay: %+v", j)
	}
	if _, err := runWebCaptured(t, "devices", "-bogus"); err == nil {
		t.Error("devices -bogus was taken")
	}
}

// conch web rename takes the words after the ID as the name, unquoted.
func TestWebRename(t *testing.T) {
	dir := a4Env(t)
	store := phone.OpenStore(dir)
	code, _ := store.NewCode(phone.PermFull, time.Now())
	d, _, err := store.Redeem(code, "iPhone", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	out, err := runWebCaptured(t, "rename", d.ID, "Amit's", "iPhone")
	if err != nil || !strings.Contains(out, `is now "Amit's iPhone"`) {
		t.Fatalf("rename: %q %v", out, err)
	}
	if devs, _ := store.Devices(); devs[0].Name != "Amit's iPhone" {
		t.Fatalf("kept %q", devs[0].Name)
	}
	if out, _ := runWebCaptured(t, "devices"); !strings.Contains(out, "Amit's iPhone") {
		t.Errorf("devices doesn't list the new name: %q", out)
	}
	for _, bad := range [][]string{{"rename"}, {"rename", d.ID}, {"rename", "d_none", "x"}, {"rename", d.ID, " "}} {
		if _, err := runWebCaptured(t, bad...); err == nil {
			t.Errorf("%v was taken", bad)
		}
	}
	if devs, _ := store.Devices(); devs[0].Name != "Amit's iPhone" {
		t.Fatalf("a refused rename changed it to %q", devs[0].Name)
	}
}

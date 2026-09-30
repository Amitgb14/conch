package phone

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

func TestStoreEmptyMissingAndBroken(t *testing.T) {
	dir := t.TempDir()
	s := OpenStore(filepath.Join(dir, "not", "made", "yet"))
	if devs, err := s.Devices(); err != nil || len(devs) != 0 {
		t.Fatalf("missing file: %v %v", devs, err)
	}
	if s.URL() != "" {
		t.Fatal("a URL from nowhere")
	}
	if ok, err := s.Revoke("d_none"); ok || err != nil {
		t.Fatalf("revoke in an empty store: %v %v", ok, err)
	}
	// The directory is made when something is first written.
	if _, err := s.NewCode(PermView, time.Now()); err != nil {
		t.Fatal(err)
	}

	// An empty file is an empty store; a file from a build with fewer
	// fields still loads.
	s = OpenStore(dir)
	os.WriteFile(s.Path(), []byte("  \n"), 0o600)
	if devs, err := s.Devices(); err != nil || len(devs) != 0 {
		t.Fatalf("empty file: %v %v", devs, err)
	}
	os.WriteFile(s.Path(), []byte(`{"devices":[{"id":"d_old1","name":"old","permission":"view","token_hash":"ab","created":"2026-09-01T00:00:00Z"}],"later_field":1}`), 0o600)
	devs, err := s.Devices()
	if err != nil || len(devs) != 1 || devs[0].ID != "d_old1" || devs[0].Push != nil {
		t.Fatalf("older file: %+v %v", devs, err)
	}

	// A file that can't be read is left as it is, not written over, and
	// pairs nobody.
	broken := []byte(`{"devices":[{"id":`)
	os.WriteFile(s.Path(), broken, 0o600)
	if _, err := s.Devices(); err == nil {
		t.Fatal("a truncated file read without an error")
	}
	if _, err := s.NewCode(PermView, time.Now()); err == nil {
		t.Fatal("a code issued over a broken file")
	}
	if _, _, err := s.Redeem("123-456", "x", time.Now()); err == nil || err == ErrPairExpired {
		t.Fatalf("redeem over a broken file: %v", err)
	}
	if b, _ := os.ReadFile(s.Path()); string(b) != string(broken) {
		t.Fatalf("the broken file was written over: %s", b)
	}
	g := New(s, nil, nil)
	defer g.Close()
	if _, ok := g.deviceByHash("ab"); ok {
		t.Fatal("a device out of a broken file")
	}
}

func TestStoreCodes(t *testing.T) {
	s := OpenStore(t.TempDir())
	now := time.Now()
	for _, bad := range []string{"", "admin", "VIEW", "view "} {
		if _, err := s.NewCode(bad, now); err == nil {
			t.Errorf("a code for permission %q", bad)
		}
	}
	code, err := s.NewCode(PermFull, now)
	if err != nil {
		t.Fatal(err)
	}
	// However the code is typed, it is the same code.
	spaced := codeDigits(code)[:3] + " " + codeDigits(code)[3:]
	dev, token, err := s.Redeem(" "+spaced+" ", "", now.Add(PairTTL-time.Nanosecond))
	if err != nil || dev.Permission != PermFull || dev.Name != "phone" || len(token) != 43 || dev.TokenHash != hashSecret(token) {
		t.Fatalf("redeem: %+v %q %v", dev, token, err)
	}
	if _, _, err := s.Redeem(code, "", now); err != ErrPairExpired {
		t.Fatalf("a second use: %v", err)
	}

	// Exactly at its end a code is over.
	code, _ = s.NewCode(PermView, now)
	if _, _, err := s.Redeem(code, "", now.Add(PairTTL)); err != ErrPairExpired {
		t.Fatalf("at expiry: %v", err)
	}

	// Four wrong guesses leave the code; the fifth takes it away.
	code, _ = s.NewCode(PermView, now)
	wrong := "000000"
	if codeDigits(code) == wrong {
		wrong = "111111"
	}
	for range pairMaxFailures - 1 {
		s.Redeem(wrong, "", now)
	}
	if _, _, err := s.Redeem(code, "", now); err != nil {
		t.Fatalf("after %d wrong guesses: %v", pairMaxFailures-1, err)
	}
	code, _ = s.NewCode(PermView, now) // a new code starts the count again
	for range pairMaxFailures {
		s.Redeem(wrong, "", now)
	}
	if _, _, err := s.Redeem(code, "", now); err != ErrPairExpired {
		t.Fatalf("after %d wrong guesses: %v", pairMaxFailures, err)
	}
	if devs, _ := s.Devices(); len(devs) != 2 {
		t.Fatalf("devices: %+v", devs)
	}
}

func TestStoreDeviceLimitAndNames(t *testing.T) {
	s := OpenStore(t.TempDir())
	now := time.Now()
	ids := map[string]bool{}
	for range maxDevices {
		code, _ := s.NewCode(PermView, now)
		dev, _, err := s.Redeem(code, "x", now)
		if err != nil {
			t.Fatal(err)
		}
		ids[dev.ID] = true
	}
	if len(ids) != maxDevices {
		t.Fatalf("%d distinct IDs for %d devices", len(ids), maxDevices)
	}
	code, _ := s.NewCode(PermView, now)
	if _, _, err := s.Redeem(code, "x", now); err == nil || err == ErrPairExpired || !strings.Contains(err.Error(), "revoke") {
		t.Fatalf("one device too many: %v", err)
	}

	for in, want := range map[string]string{
		"":                       "phone",
		" \t\n":                  "phone",
		"\x1b]0;x\x07":           "]0;x",
		"a\nb":                   "a b",
		strings.Repeat("é", 100): strings.Repeat("é", 64),
	} {
		if got := cleanName(in); got != want {
			t.Errorf("cleanName(%q) = %q, want %q", in, got, want)
		}
	}
}

// `conch web pair` and the gateway write the file from two processes; here
// from many goroutines. Nothing is lost and the file is never half there.
func TestStoreConcurrentUpdates(t *testing.T) {
	s := OpenStore(t.TempDir())
	s.update(func(st *state) error {
		for i := range 8 {
			st.Devices = append(st.Devices, Device{ID: "d_" + string(rune('a'+i)), Permission: PermView, TokenHash: "h"})
		}
		return nil
	})
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(2)
		id := "d_" + string(rune('a'+i))
		go func() {
			defer wg.Done()
			if err := s.Subscribe(id, PushSubscription{Endpoint: "https://push.example/" + id}); err != nil {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			if _, err := s.Devices(); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	devs, err := s.Devices()
	if err != nil || len(devs) != 8 {
		t.Fatalf("%v %v", devs, err)
	}
	for _, d := range devs {
		if len(d.Push) != 1 {
			t.Fatalf("%s lost its subscription", d.ID)
		}
	}
	if err := s.Subscribe("d_none", PushSubscription{Endpoint: "https://push.example/x"}); err == nil {
		t.Fatal("subscribed a device that isn't there")
	}
	if err := s.Unsubscribe("d_none", "https://push.example/x"); err != nil {
		t.Fatal(err)
	}
	// A device keeps only its latest few subscriptions.
	for i := range maxPushPerDev + 3 {
		s.Subscribe("d_a", PushSubscription{Endpoint: "https://push.example/n" + string(rune('a'+i))})
	}
	devs, _ = s.Devices()
	if n := len(devs[0].Push); n != maxPushPerDev {
		t.Fatalf("%d subscriptions kept", n)
	}
	if entries, _ := os.ReadDir(filepath.Dir(s.Path())); len(entries) != 2 { // the file and its lock
		t.Fatalf("left behind: %v", entries)
	}
}

func TestStoreBrokenPushKey(t *testing.T) {
	s := OpenStore(t.TempDir())
	s.update(func(st *state) error { st.VAPIDKey = "not a key"; return nil })
	if _, err := s.VAPIDPublicKey(); err == nil {
		t.Fatal("a public key from a broken private one")
	}
}

func ipnet(s string) net.Addr {
	ip, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	n.IP = ip
	return n
}

// Without -listen the gateway listens on the Tailscale address and
// nowhere else; with none, it doesn't listen at all.
func TestListenAddr(t *testing.T) {
	lan := []net.Addr{ipnet("127.0.0.1/8"), ipnet("192.168.1.20/24"), ipnet("::1/128"), ipnet("fe80::1/64"), &net.UnixAddr{Name: "x"}}
	for _, c := range []struct {
		name  string
		addrs []net.Addr
	}{{"no addresses", nil}, {"no Tailscale", lan},
		{"just outside the range", []net.Addr{ipnet("100.63.255.255/32"), ipnet("100.128.0.0/32"), ipnet("fd7a:115c:a1e1::1/128")}}} {
		if addr, _, err := ListenAddr("", 8722, c.addrs); err != ErrNoTailscale || addr != "" {
			t.Errorf("%s: %q %v", c.name, addr, err)
		}
	}
	for _, c := range []struct {
		addrs []net.Addr
		want  string
	}{
		{append([]net.Addr{ipnet("100.101.102.103/32")}, lan...), "100.101.102.103:8722"},
		{append(lan, ipnet("fd7a:115c:a1e0::53/128"), ipnet("100.64.0.0/32")), "100.64.0.0:8722"}, // IPv4 first
		{[]net.Addr{ipnet("100.127.255.255/10")}, "100.127.255.255:8722"},
		{[]net.Addr{ipnet("fd7a:115c:a1e0::53/128")}, "[fd7a:115c:a1e0::53]:8722"},
		{[]net.Addr{ipnet("::ffff:100.100.1.1/128")}, "100.100.1.1:8722"},
	} {
		addr, warning, err := ListenAddr("", 8722, c.addrs)
		if err != nil || addr != c.want || warning != "" {
			t.Errorf("%v: %q %q %v, want %s", c.addrs, addr, warning, err, c.want)
		}
	}

	// -listen is taken at its word, and warned about unless it is a
	// Tailscale address itself.
	for _, c := range []struct {
		listen, want string
		warn         bool
	}{
		{"127.0.0.1:9000", "127.0.0.1:9000", true},
		{"0.0.0.0:9000", "0.0.0.0:9000", true},
		{":9000", ":9000", true},
		{"192.168.1.20", "192.168.1.20:8722", true},
		{"localhost:1", "localhost:1", true},
		{"[::1]:9000", "[::1]:9000", true},
		{"100.101.102.103:9000", "100.101.102.103:9000", false},
		{"100.101.102.103", "100.101.102.103:8722", false},
	} {
		addr, warning, err := ListenAddr(c.listen, 8722, lan)
		if err != nil || addr != c.want || (warning != "") != c.warn {
			t.Errorf("-listen %s: %q %q %v", c.listen, addr, warning, err)
		}
		if c.warn && !strings.Contains(warning, "not this machine's Tailscale address") {
			t.Errorf("warning %q", warning)
		}
	}
}

func menuScreen(lines ...string) []string { return lines }

func TestReadMenu(t *testing.T) {
	for _, c := range []struct {
		name   string
		screen []string
		labels string // joined with |, "" for no menu
		cursor int
	}{
		{"claude, boxed", menuScreen(
			"╭──────────────────────────────╮",
			"│ Bash command                 │",
			"│                              │",
			"│   go test ./...              │",
			"│                              │",
			"│ Do you want to proceed?      │",
			"│ ❯ 1. Yes                     │",
			"│   2. Yes, and don't ask again │",
			"│   3. No (esc)                │",
			"╰──────────────────────────────╯"), "Yes|Yes, and don't ask again|No (esc)", 0},
		{"cursor in the middle", menuScreen("Pick", "  1. a", "› 2. b", "  3. c"), "a|b|c", 1},
		{"a plain > for a cursor", menuScreen("> 1. Update now", "  2. Skip"), "Update now|Skip", 0},
		{"the last menu on the screen", menuScreen("❯ 1. old", "  2. older", "", "  1. new", "❯ 2. newer"), "new|newer", 1},
		{"ten choices", menuScreen("❯ 1. a", "  2. b", "  3. c", "  4. d", "  5. e", "  6. f", "  7. g", "  8. h", "  9. i", "  10. j"), "a|b|c|d|e|f|g|h|i|j", 0},
		{"no cursor", menuScreen("  1. Yes", "  2. No"), "", 0},
		{"two cursors", menuScreen("❯ 1. Yes", "❯ 2. No"), "", 0},
		{"one row", menuScreen("❯ 1. Yes"), "", 0},
		{"not from 1", menuScreen("❯ 2. Yes", "  3. No"), "", 0},
		{"a gap", menuScreen("❯ 1. Yes", "  3. No"), "", 0},
		{"a list in an answer", menuScreen("I did three things:", "1. read the file", "2. fixed the bug", "3. ran the tests"), "", 0},
		{"rows apart", menuScreen("❯ 1. Yes", "", "  2. No"), "", 0},
		{"nothing", nil, "", 0},
		{"blank", menuScreen("", "   "), "", 0},
	} {
		m, ok := readMenu(c.screen)
		if got := strings.Join(m.labels, "|"); ok != (c.labels != "") || ok && (got != c.labels || m.cursor != c.cursor) {
			t.Errorf("%s: %v %q cursor %d", c.name, ok, got, m.cursor)
		}
	}
}

func TestQuestionAndKeys(t *testing.T) {
	since := time.Date(2026, 9, 30, 9, 12, 4, 0, time.UTC)
	info := func(state, msg string) proto.PaneInfo {
		return proto.PaneInfo{ID: "p3", Agent: &proto.AgentStatus{Name: "claude", State: state, Message: msg, Since: since}}
	}
	boxed := menuScreen(
		"⏺ earlier output",
		"",
		"╭──────────────────────────────╮",
		"│ Bash command                 │",
		"│                              │",
		"│   go test ./...              │",
		"│                              │",
		"│ Do you want to proceed?      │",
		"│   1. Yes                     │",
		"│ ❯ 2. No                      │",
		"╰──────────────────────────────╯")

	for _, p := range []proto.PaneInfo{{ID: "p3"}, info(proto.AgentWorking, ""), info(proto.AgentIdle, "x"), info(proto.AgentDone, "")} {
		if q := readQuestion(p, boxed); q != nil {
			t.Errorf("a question from %+v: %+v", p.Agent, q)
		}
	}
	q := readQuestion(info(proto.AgentBlocked, ""), boxed)
	if q.Text != "Bash command go test ./... Do you want to proceed?" || len(q.Choices) != 2 || q.Choices[0].Default || !q.Choices[1].Default {
		t.Fatalf("question %+v", q)
	}
	if !strings.HasPrefix(q.ID, "q_") || len(q.ID) != 14 {
		t.Fatalf("id %q", q.ID)
	}
	// The hook's words win over the screen's.
	if hq := readQuestion(info(proto.AgentBlocked, " Claude needs your permission "), boxed); hq.Text != "Claude needs your permission" || hq.ID == q.ID {
		t.Fatalf("hook question %+v", hq)
	}
	// No menu: the last paragraph, and an empty list of choices.
	plain := readQuestion(info(proto.AgentBlocked, ""), menuScreen("old", "", "Do you trust the files", "in this folder?", "", ""))
	if plain.Text != "Do you trust the files in this folder?" || plain.Choices == nil || len(plain.Choices) != 0 {
		t.Fatalf("plain question %+v", plain)
	}
	if empty := readQuestion(info(proto.AgentBlocked, ""), nil); empty.Text != "" || empty.ID == "" {
		t.Fatalf("empty screen %+v", empty)
	}
	long := readQuestion(info(proto.AgentBlocked, ""), menuScreen(strings.Repeat("é", 2000)))
	if n := len([]rune(long.Text)); n != questionTextMax {
		t.Fatalf("text of %d runes", n)
	}

	// The ID follows the question: same again for the same one, different
	// for another pane, another time, another text, other choices or a
	// cursor that moved.
	same := readQuestion(info(proto.AgentBlocked, ""), boxed)
	if same.ID != q.ID {
		t.Fatal("the same question got another ID")
	}
	other := info(proto.AgentBlocked, "")
	other.ID = "p4"
	later := info(proto.AgentBlocked, "")
	later.Agent.Since = since.Add(time.Second)
	moved := append([]string{}, boxed...)
	moved[8], moved[9] = "│ ❯ 1. Yes                     │", "│   2. No                      │"
	relabelled := append([]string{}, boxed...)
	relabelled[8] = "│   1. Yes, always             │"
	reworded := append([]string{}, boxed...)
	reworded[5] = "│   rm -rf build               │"
	for name, id := range map[string]string{
		"another pane":   readQuestion(other, boxed).ID,
		"a later wait":   readQuestion(later, boxed).ID,
		"a moved cursor": readQuestion(info(proto.AgentBlocked, ""), moved).ID,
		"another label":  readQuestion(info(proto.AgentBlocked, ""), relabelled).ID,
		"another text":   readQuestion(info(proto.AgentBlocked, ""), reworded).ID,
	} {
		if id == q.ID {
			t.Errorf("%s has the same question ID", name)
		}
	}

	// Keys: the cursor moved to the row, then Enter.
	for choice, want := range map[string]string{"1": "up enter", "2": "enter"} {
		keys, ok := choiceKeys(boxed, choice)
		if !ok || strings.Join(keys, " ") != want {
			t.Errorf("choice %s: %v %v, want %s", choice, keys, ok, want)
		}
	}
	for _, bad := range []string{"", "0", "3", "-1", "1 ", "+1", "two", "99999999999999999999"} {
		if keys, ok := choiceKeys(boxed, bad); ok {
			t.Errorf("choice %q: %v", bad, keys)
		}
	}
	if _, ok := choiceKeys(menuScreen("  1. Yes", "  2. No"), "1"); ok {
		t.Error("keys for a menu with no cursor")
	}
}

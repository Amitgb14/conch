package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/phone"
	"github.com/Amitgb14/conch/internal/proto"
)

func runWebCaptured(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var err error
	out, _ := a4Capture(t, "", func() { err = runWeb(args) })
	return out, err
}

// pair, devices and revoke: the round a person makes from the laptop.
func TestWebPairDevicesRevoke(t *testing.T) {
	dir := a4Env(t)
	store := phone.OpenStore(dir)

	out, err := runWebCaptured(t, "devices")
	if err != nil || !strings.Contains(out, "no devices paired") {
		t.Fatalf("devices with none: %q %v", out, err)
	}

	out, err = runWebCaptured(t, "pair")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`pairing code: (\d{3}-\d{3})\n`).FindStringSubmatch(out)
	if m == nil || !strings.Contains(out, "reply permission") || !strings.Contains(out, "start the gateway with `conch web`") {
		t.Fatalf("pair printed %q", out)
	}
	// The code is not what is kept.
	if b, _ := os.ReadFile(store.Path()); bytes.Contains(b, []byte(m[1])) || bytes.Contains(b, []byte(strings.ReplaceAll(m[1], "-", ""))) {
		t.Fatalf("the code is in the file: %s", b)
	}
	dev, _, err := store.Redeem(m[1], "Pixel", time.Now())
	if err != nil || dev.Permission != phone.PermReply {
		t.Fatalf("redeem: %+v %v", dev, err)
	}

	// With a gateway's address on record, pair says where to go.
	store.SetURL("http://100.101.102.103:8722")
	out, err = runWebCaptured(t, "pair", "-permission", "full")
	if err != nil || !strings.Contains(out, "open http://100.101.102.103:8722 on the phone") || !strings.Contains(out, "full permission") {
		t.Fatalf("pair -permission full: %q %v", out, err)
	}

	out, err = runWebCaptured(t, "devices")
	if err != nil || !strings.Contains(out, dev.ID) || !strings.Contains(out, "Pixel") || !strings.Contains(out, "reply") {
		t.Fatalf("devices: %q %v", out, err)
	}
	if strings.Contains(out, dev.TokenHash) {
		t.Fatalf("devices prints the token's hash: %q", out)
	}

	if _, err := runWebCaptured(t, "revoke", "d_nope"); err == nil || !strings.Contains(err.Error(), `no device "d_nope"`) {
		t.Fatalf("revoking nobody: %v", err)
	}
	out, err = runWebCaptured(t, "revoke", dev.ID)
	if err != nil || !strings.Contains(out, "revoked "+dev.ID) {
		t.Fatalf("revoke: %q %v", out, err)
	}
	if devs, _ := store.Devices(); len(devs) != 0 {
		t.Fatalf("still paired: %+v", devs)
	}

	for _, bad := range [][]string{
		{"pair", "-permission", "admin"},
		{"pair", "-permission", ""},
		{"pair", "extra"},
		{"revoke"},
		{"revoke", "a", "b"},
		{"nonsense"},
		{"-cert", "c.pem"},
		{"-key", "k.pem"},
		{"-port", "0"},
		{"-port", "70000"},
	} {
		if _, err := runWebCaptured(t, bad...); err == nil {
			t.Errorf("conch web %v: no error", bad)
		}
	}

	// The gateway serves this machine; -m names another.
	machineFlag = "gpu-box"
	if _, err := runWebCaptured(t, "pair"); err == nil || !strings.Contains(err.Error(), "gpu-box") {
		t.Fatalf("with -m: %v", err)
	}
}

// Without -listen the gateway listens on the Tailscale address or not at
// all: no server is started, nothing is bound.
func TestWebNeedsTailscale(t *testing.T) {
	a4Env(t)
	old := interfaceAddrs
	t.Cleanup(func() { interfaceAddrs = old })
	_, lan, _ := net.ParseCIDR("192.168.1.0/24")
	lan.IP = net.ParseIP("192.168.1.20")
	interfaceAddrs = func() ([]net.Addr, error) { return []net.Addr{lan}, nil }
	if _, err := runWebCaptured(t); !errors.Is(err, phone.ErrNoTailscale) {
		t.Fatalf("no Tailscale address: %v", err)
	}
	interfaceAddrs = func() ([]net.Addr, error) { return nil, errors.New("no interfaces") }
	if _, err := runWebCaptured(t); err == nil || err.Error() != "no interfaces" {
		t.Fatalf("no interfaces: %v", err)
	}
}

// An agent the server keeps to its own work may not decide which phones
// act as the person.
func TestWebPairRefusedFromAnAgentsPane(t *testing.T) {
	dir := a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		if msg.Method == proto.MethodPaneCaller {
			return proto.CallerInfo{Pane: "p4", Agent: "codex", Scoped: true}, nil
		}
		return nil, nil
	})
	store := phone.OpenStore(dir)
	store.SetURL("x")
	before, _ := os.ReadFile(store.Path())
	for _, args := range [][]string{{"pair"}, {"pair", "-permission", "full"}, {"revoke", "d_abcd"}} {
		out, err := runWebCaptured(t, args...)
		if err == nil || !strings.Contains(err.Error(), "the codex agent in p4 may not") || strings.Contains(out, "pairing code") {
			t.Fatalf("conch web %v from an agent's pane: %q %v", args, out, err)
		}
	}
	if after, _ := os.ReadFile(store.Path()); !bytes.Equal(before, after) {
		t.Fatalf("the devices file changed: %s", after)
	}
	// Listing is reading; that it may do.
	if _, err := runWebCaptured(t, "devices"); err != nil {
		t.Fatal(err)
	}
}

// The command itself, in a process of its own: it says where it listens
// and warns about an address that isn't Tailscale's, serves the contract,
// records its address for `conch web pair`, and stops on a signal.
func TestWebServes(t *testing.T) {
	dir := a4Env(t)
	startA4Server(t, config.SocketPath())
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := a4Command(exe, "web", "-listen", "127.0.0.1:0")
	stdout, _ := cmd.StdoutPipe()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	t.Cleanup(func() { cmd.Process.Kill() })

	lines := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
		exited <- cmd.Wait()
	}()
	var url string
	timeout := time.After(15 * time.Second)
	for url == "" {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatalf("conch web ended before listening: %s", stderr.String())
			}
			if rest, found := strings.CutPrefix(line, "conch web: listening on "); found {
				url = rest
			}
		case <-timeout:
			t.Fatal("conch web never said where it listens")
		}
	}
	if !strings.HasPrefix(url, "http://127.0.0.1:") {
		t.Fatalf("listening on %q", url)
	}

	res, err := http.Get(url + "/api/hello")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatalf("hello unpaired: %d", res.StatusCode)
	}
	store := phone.OpenStore(dir)
	if store.URL() != url {
		t.Fatalf("recorded %q, listening on %q", store.URL(), url)
	}
	code, err := store.NewCode(phone.PermView, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(phone.PairRequest{Code: code, DeviceName: "test"})
	res, err = http.Post(url+"/pair", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 || len(res.Cookies()) != 1 {
		t.Fatalf("pair: %d %v", res.StatusCode, res.Cookies())
	}
	req, _ := http.NewRequest("GET", url+"/api/agents", nil)
	req.AddCookie(&http.Cookie{Name: phone.CookieName, Value: res.Cookies()[0].Value})
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var list phone.AgentList
	json.NewDecoder(res.Body).Decode(&list)
	res.Body.Close()
	if res.StatusCode != 200 || list.Agents == nil {
		t.Fatalf("agents through the fake server: %d %+v", res.StatusCode, list)
	}

	cmd.Process.Signal(syscall.SIGTERM)
	select {
	case err := <-exited:
		if err != nil {
			t.Fatalf("exit: %v\n%s", err, stderr.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("conch web did not stop on SIGTERM")
	}
	if !strings.Contains(stderr.String(), "warning: listening on 127.0.0.1:0, which is not this machine's Tailscale address") {
		t.Fatalf("no warning for -listen: %s", stderr.String())
	}
	if _, err := http.Get(url + "/api/hello"); err == nil {
		t.Fatal("still listening after it stopped")
	}
}

// The usage names the commands as they are.
func TestWebUsage(t *testing.T) {
	for _, want := range []string{"conch web [-listen ADDR]", "conch web pair [-permission view|reply|full]", "conch web devices | revoke ID"} {
		if !strings.Contains(usage, want) {
			t.Errorf("usage lacks %q", want)
		}
	}
}

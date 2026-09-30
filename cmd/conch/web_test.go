package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
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
	if m == nil || !strings.Contains(out, "full permission") || !strings.Contains(out, "start the gateway with `conch web`") {
		t.Fatalf("pair printed %q", out)
	}
	// The code is not what is kept.
	if b, _ := os.ReadFile(store.Path()); bytes.Contains(b, []byte(m[1])) || bytes.Contains(b, []byte(strings.ReplaceAll(m[1], "-", ""))) {
		t.Fatalf("the code is in the file: %s", b)
	}
	// Paired to type, unless asked for less.
	dev, _, err := store.Redeem(m[1], "Pixel", time.Now())
	if err != nil || dev.Permission != phone.PermFull {
		t.Fatalf("redeem: %+v %v", dev, err)
	}
	if ok, err := store.SetPermission(dev.ID, phone.PermReply); !ok || err != nil {
		t.Fatal(ok, err)
	}

	// With a gateway's address on record, pair says where to go — and on
	// a terminal draws the code that opens it.
	store.SetURL("https://laptop.tail1234.ts.net")
	oldTTY := stdoutIsTerminal
	t.Cleanup(func() { stdoutIsTerminal = oldTTY })
	stdoutIsTerminal = func() bool { return false }
	out, err = runWebCaptured(t, "pair", "-permission", "view")
	if err != nil || !strings.Contains(out, "open https://laptop.tail1234.ts.net on it") || !strings.Contains(out, "view permission") {
		t.Fatalf("pair -permission view: %q %v", out, err)
	}
	if strings.Contains(out, "\x1b[") {
		t.Fatalf("escape codes written to something that isn't a terminal: %q", out)
	}
	stdoutIsTerminal = func() bool { return true }
	out, err = runWebCaptured(t, "pair")
	code := regexp.MustCompile(`pairing code: (\d{3}-\d{3})\n`).FindStringSubmatch(out)
	if err != nil || code == nil {
		t.Fatalf("pair on a terminal: %q %v", out, err)
	}
	want, _ := phone.QRLines(phone.PairLink("https://laptop.tail1234.ts.net", code[1]))
	if !strings.HasPrefix(out, strings.Join(want, "\n")+"\n") {
		t.Fatalf("the QR code isn't the link to this code:\n%s", out)
	}

	out, err = runWebCaptured(t, "devices")
	if err != nil || !strings.Contains(out, dev.ID) || !strings.Contains(out, "Pixel") || !strings.Contains(out, "reply") {
		t.Fatalf("devices: %q %v", out, err)
	}
	if strings.Contains(out, dev.TokenHash) {
		t.Fatalf("devices prints the token's hash: %q", out)
	}

	// Raised to full without pairing again.
	out, err = runWebCaptured(t, "permission", dev.ID, "full")
	if err != nil || !strings.Contains(out, dev.ID+" may now reply, answer, type into terminals") {
		t.Fatalf("permission full: %q %v", out, err)
	}
	if devs, _ := store.Devices(); len(devs) != 1 || devs[0].Permission != phone.PermFull || devs[0].TokenHash != dev.TokenHash {
		t.Fatalf("after permission: %+v", devs)
	}
	for _, bad := range [][]string{{"permission"}, {"permission", dev.ID}, {"permission", dev.ID, "admin"}, {"permission", "d_nope", "full"}, {"permission", dev.ID, "full", "x"}} {
		if _, err := runWebCaptured(t, bad...); err == nil {
			t.Errorf("conch web %v: no error", bad)
		}
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
		{"-url", "laptop.ts.net"},
		{"-url", "ftp://laptop.ts.net"},
		{"-url", "https://laptop.ts.net/?x=1"},
		{"-url", "https://laptop.ts.net/#code=1"},
		{"-url", "https://me@laptop.ts.net"},
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
	for _, args := range [][]string{{"pair"}, {"pair", "-permission", "full"}, {"revoke", "d_abcd"}, {"permission", "d_abcd", "full"}} {
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
	cmd := a4Command(exe, "web", "-listen", "127.0.0.1:0", "-url", "https://laptop.tail1234.ts.net/")
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
	// What pairing points phones at is the address given, not the listener.
	store := phone.OpenStore(dir)
	if store.URL() != "https://laptop.tail1234.ts.net" {
		t.Fatalf("recorded %q", store.URL())
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
	for _, want := range []string{"conch web [-listen ADDR] [-port N] [-url URL]", "conch web pair [-permission view|reply|full]", "conch web devices | revoke ID | permission ID view|reply|full"} {
		if !strings.Contains(usage, want) {
			t.Errorf("usage lacks %q", want)
		}
	}
}

// Without -url or a certificate, pairing can't work, and conch web says
// what to run — with this machine's tailnet name when Tailscale knows it.
func TestWebSaysHowToGetHTTPS(t *testing.T) {
	dir := a4Env(t)
	startA4Server(t, config.SocketPath())
	fake := filepath.Join(dir, "tailscale")
	os.WriteFile(fake, []byte("#!/bin/sh\n[ \"$1 $2\" = \"status --json\" ] || exit 1\n"+
		"echo '{\"Self\":{\"DNSName\":\"laptop.tail1234.ts.net.\"}}'\n"), 0o755)
	t.Setenv("CONCH_TAILSCALE", fake)
	exe, _ := os.Executable()
	cmd := a4Command(exe, "web", "-listen", "127.0.0.1:0")
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Signal(syscall.SIGTERM); cmd.Wait() }()
	sc := bufio.NewScanner(stdout)
	var out []string
	for sc.Scan() {
		out = append(out, sc.Text())
		if strings.HasPrefix(sc.Text(), "pair a phone") {
			break
		}
	}
	text := strings.Join(out, "\n")
	for _, want := range []string{"phones can't pair over plain http", "  tailscale serve --bg http://127.0.0.1:8722\n", "  conch web -url https://laptop.tail1234.ts.net\n"} {
		if !strings.Contains(text+"\n", want) {
			t.Fatalf("lacks %q:\n%s", want, text)
		}
	}
}

// With no Tailscale to ask, the name is a placeholder, not a failure.
func TestTailnetNameWithoutTailscale(t *testing.T) {
	dir := a4Env(t)
	if n := tailnetName(); n != "" {
		t.Fatalf("name %q with no tailscale", n)
	}
	bad := filepath.Join(dir, "tailscale")
	os.WriteFile(bad, []byte("#!/bin/sh\necho not json\n"), 0o755)
	t.Setenv("CONCH_TAILSCALE", bad)
	if n := tailnetName(); n != "" {
		t.Fatalf("name %q from nonsense", n)
	}
}

// With -url, tailscale serve is in front, and it reaches the gateway on
// loopback: that is where it listens, and nowhere on the tailnet.
func TestWebWithURLListensOnLoopback(t *testing.T) {
	a4Env(t)
	startA4Server(t, config.SocketPath())
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	exe, _ := os.Executable()
	cmd := a4Command(exe, "web", "-port", fmt.Sprint(port), "-url", "https://laptop.tail1234.ts.net")
	stdout, _ := cmd.StdoutPipe()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill() })
	sc := bufio.NewScanner(stdout)
	var out []string
	for sc.Scan() {
		out = append(out, sc.Text())
		if strings.HasPrefix(sc.Text(), "pair a phone") {
			break
		}
	}
	text := strings.Join(out, "\n")
	want := fmt.Sprintf("conch web: listening on http://127.0.0.1:%d\nphones open https://laptop.tail1234.ts.net\n"+
		"with Tailscale's HTTPS in front of it: tailscale serve --bg http://127.0.0.1:%d", port, port)
	if !strings.HasPrefix(text, want) {
		t.Fatalf("printed:\n%s\nwant it to start:\n%s", text, want)
	}
	res, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/hello", port))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatalf("hello: %d", res.StatusCode)
	}
	cmd.Process.Signal(syscall.SIGTERM)
	cmd.Wait() // stderr is written until then
	if strings.Contains(stderr.String(), "warning") {
		t.Fatalf("warned about loopback: %s", stderr.String())
	}
}

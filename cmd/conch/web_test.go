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
	// And how it was started, with its process, for the TUI.
	run, running := store.Gateway()
	if !running || run.PID != cmd.Process.Pid || strings.Join(run.Args, " ") != "-listen 127.0.0.1:0 -url https://laptop.tail1234.ts.net/" {
		t.Fatalf("gateway record %+v %v", run, running)
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

	// conch web stop is what ends it, as a TUI-started one has no
	// terminal to press ctrl+c in.
	if out, err := runWebCaptured(t, "stop"); err != nil || !strings.Contains(out, fmt.Sprintf("stopped conch web (pid %d)", cmd.Process.Pid)) {
		t.Fatalf("web stop: %q %v", out, err)
	}
	select {
	case err := <-exited:
		if err != nil {
			t.Fatalf("exit: %v\n%s", err, stderr.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("conch web did not stop")
	}
	if run, running := store.Gateway(); running || run.PID != 0 || len(run.Args) != 4 {
		t.Fatalf("after stopping: %+v %v", run, running)
	}
	if out, err := runWebCaptured(t, "stop"); err != nil || !strings.Contains(out, "not running") {
		t.Fatalf("stop with none running: %q %v", out, err)
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
	for _, want := range []string{"conch web [-listen ADDR] [-port N] [-url URL]", "conch web pair [-permission view|reply|full]", "conch web devices | revoke ID | permission ID view|reply|full [-machine NAME] | stop",
		// where a device may act is part of what it may do, so the
		// usage says it rather than leaving it to the docs
		"machines it reaches"} {
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

// With [web] set in config.toml — as the TUI's settings set it — plain
// `conch web` listens on that port and pairs phones to that address; a
// flag still wins.
func TestWebTakesItsSettingsFromConfig(t *testing.T) {
	dir := a4Env(t)
	startA4Server(t, config.SocketPath())
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte(fmt.Sprintf("[web]\nurl = \"https://laptop.tail1234.ts.net\"\nport = %d\n", port)), 0o600)
	exe, _ := os.Executable()
	cmd := a4Command(exe, "web")
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	sc := bufio.NewScanner(stdout)
	var out []string
	for sc.Scan() {
		out = append(out, sc.Text())
		if strings.HasPrefix(sc.Text(), "pair a phone") {
			break
		}
	}
	text := strings.Join(out, "\n")
	if !strings.Contains(text, fmt.Sprintf("listening on http://127.0.0.1:%d", port)) || !strings.Contains(text, "phones open https://laptop.tail1234.ts.net") {
		t.Fatalf("printed:\n%s", text)
	}
	if run, _ := phone.OpenStore(dir).Gateway(); len(run.Args) != 0 {
		t.Fatalf("recorded args %v: started with none", run.Args)
	}
}

// A device reaches this computer until a machine is named for it, and
// `conch web permission -machine` is the only way to name one. The flag
// is where the decision is made, so it is also where the mistakes are:
// changing what a device may do must not quietly change where, and a
// name that could not be a machine must not be written down.
func TestWebPermissionMachines(t *testing.T) {
	dir := a4Env(t)
	store := phone.OpenStore(dir)
	code, err := store.NewCode(phone.PermReply, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	dev, _, err := store.Redeem(code, "Pixel", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	machines := func() []string {
		t.Helper()
		devs, err := store.Devices()
		if err != nil || len(devs) != 1 {
			t.Fatalf("devices %+v: %v", devs, err)
		}
		return devs[0].Machines
	}

	// Paired with none, and listed as reaching this computer.
	if len(machines()) != 0 {
		t.Fatalf("paired with machines %v", machines())
	}
	out, err := runWebCaptured(t, "devices")
	if err != nil || !strings.Contains(out, "MACHINES") || !strings.Contains(out, "this computer") {
		t.Fatalf("devices: %q %v", out, err)
	}

	// Given one, by name, with what it may do said in the same breath.
	out, err = runWebCaptured(t, "permission", dev.ID, "full", "-machine", "busybox")
	if err != nil || !strings.Contains(out, dev.ID+" also reaches busybox") {
		t.Fatalf("permission -machine: %q %v", out, err)
	}
	if !strings.Contains(out, "reached with your credentials") {
		t.Errorf("it does not say what granting a machine means: %q", out)
	}
	if got := machines(); len(got) != 1 || got[0] != "busybox" {
		t.Fatalf("machines %v", got)
	}
	if devs, _ := store.Devices(); devs[0].Permission != phone.PermFull {
		t.Errorf("permission %q", devs[0].Permission)
	}
	out, err = runWebCaptured(t, "devices")
	if err != nil || !strings.Contains(out, "this computer, busybox") {
		t.Fatalf("devices with a machine: %q %v", out, err)
	}

	// Several, repeatable and in one go; the record is sorted and deduped.
	if _, err := runWebCaptured(t, "permission", dev.ID, "full", "-machine", "vm2", "-machine", "busybox", "-machine", "vm2"); err != nil {
		t.Fatal(err)
	}
	if got := machines(); len(got) != 2 || got[0] != "busybox" || got[1] != "vm2" {
		t.Fatalf("machines %v", got)
	}

	// Changing what it may do leaves where alone: the two are separate
	// decisions, and a phone demoted to view should not silently lose its
	// machines (or keep them without anyone saying so).
	if _, err := runWebCaptured(t, "permission", dev.ID, "view"); err != nil {
		t.Fatal(err)
	}
	if got := machines(); len(got) != 2 {
		t.Fatalf("changing the permission changed the machines: %v", got)
	}

	// And `local` takes them away.
	out, err = runWebCaptured(t, "permission", dev.ID, "view", "-machine", "local")
	if err != nil || !strings.Contains(out, dev.ID+" reaches this computer only") {
		t.Fatalf("permission -machine local: %q %v", out, err)
	}
	if got := machines(); len(got) != 0 {
		t.Fatalf("machines after local: %v", got)
	}

	// A name that could not be a machine is refused, and the device is
	// left as it was — including the permission, which is set first.
	if _, err := runWebCaptured(t, "permission", dev.ID, "full", "-machine", "busybox"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"NOPE!", "has space", "a:b", "../etc"} {
		if _, err := runWebCaptured(t, "permission", dev.ID, "full", "-machine", bad); err == nil {
			t.Errorf("-machine %q was accepted", bad)
		}
		if got := machines(); len(got) != 1 || got[0] != "busybox" {
			t.Fatalf("-machine %q changed the record: %v", bad, got)
		}
	}
	// A device that isn't there is said so rather than invented.
	if _, err := runWebCaptured(t, "permission", "d_nope", "full", "-machine", "busybox"); err == nil {
		t.Error("a machine was given to a device that is not paired")
	}
}

// What `conch web pair` says and does about machines. A phone reaching
// this computer alone is the thing people are surprised by — twice over,
// in use — so pairing says where as well as what, and can pair a device
// for a machine in one command instead of two.
func TestWebPairSaysWhereAndPairsForAMachine(t *testing.T) {
	dir := a4Env(t)
	store := phone.OpenStore(dir)

	// Plain: it says this computer only, and how to change that.
	out, err := runWebCaptured(t, "pair")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"this computer only", "-machine NAME"} {
		if !strings.Contains(out, want) {
			t.Errorf("pairing does not say %q:\n%s", want, out)
		}
	}

	// For a machine: said, and carried onto the device that redeems it.
	out, err = runWebCaptured(t, "pair", "-permission", "reply", "-machine", "busybox")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "this computer and busybox") || !strings.Contains(out, "your own credentials") {
		t.Fatalf("pairing for a machine says:\n%s", out)
	}
	code := regexp.MustCompile(`pairing code: ([0-9]{3}-[0-9]{3})`).FindStringSubmatch(out)
	if code == nil {
		t.Fatalf("no code in:\n%s", out)
	}
	dev, _, err := store.Redeem(code[1], "Pixel", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(dev.Machines) != 1 || dev.Machines[0] != "busybox" {
		t.Fatalf("the device was paired with %v", dev.Machines)
	}
	if dev.Permission != phone.PermReply {
		t.Errorf("permission %q", dev.Permission)
	}
	out, err = runWebCaptured(t, "devices")
	if err != nil || !strings.Contains(out, "this computer, busybox") {
		t.Fatalf("devices: %q %v", out, err)
	}

	// A name that could not be a machine is refused, and no code is left
	// outstanding for it: a code that cannot do what was asked is worse
	// than none.
	if _, err := runWebCaptured(t, "pair", "-machine", "NOPE!"); err == nil {
		t.Error("a code was made for a machine that cannot exist")
	}
}

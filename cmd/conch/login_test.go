package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/cloud"
	"github.com/Amitgb14/conch/internal/cloud/cloudtest"
)

// syncBuf is cloudOut for a test: a command may write while the test reads.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// cloudIsolated is a scratch CONCH_HOME, a fake Conch Cloud, and the
// commands' output captured.
func cloudIsolated(t *testing.T) (*cloudtest.Fake, *syncBuf) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("CONCH_HOME", filepath.Join(dir, "conch"))
	t.Setenv("CONCH_SOCKET", "")
	t.Setenv("CONCH_PANE_ID", "")
	f := cloudtest.New(t)
	out := &syncBuf{}
	old := cloudOut
	cloudOut = out
	t.Cleanup(func() { cloudOut = old })
	return f, out
}

// signIn runs conch login against the fake, approving the code once
// asked for it.
func signIn(t *testing.T, f *cloudtest.Fake, args ...string) error {
	t.Helper()
	go func() {
		for f.Count("POST /v1/auth/device") == 0 {
			time.Sleep(5 * time.Millisecond)
		}
		f.Approve("approved")
	}()
	return runLogin(append([]string{"-no-browser"}, args...))
}

func TestLoginJSONAccountLogout(t *testing.T) {
	f, out := cloudIsolated(t)
	var opened []string
	old := openInBrowser
	openInBrowser = func(u string) error { opened = append(opened, u); return nil }
	t.Cleanup(func() { openInBrowser = old })
	go func() {
		for f.Count("POST /v1/auth/device") == 0 {
			time.Sleep(5 * time.Millisecond)
		}
		f.Approve("approved")
	}()
	if err := runLogin([]string{"-json", "-client", "mac_app"}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	var first, last loginEvent
	json.Unmarshal([]byte(lines[0]), &first)
	json.Unmarshal([]byte(lines[len(lines)-1]), &last)
	if first.Event != "code" || first.UserCode == "" || last.Event != "signed_in" || last.Handle != "ada" {
		t.Fatalf("events %q", lines)
	}
	if len(opened) != 1 || !strings.Contains(opened[0], first.UserCode) {
		t.Fatalf("opened %q", opened)
	}

	out.b.Reset()
	if err := runAccount([]string{"-json"}); err != nil {
		t.Fatal(err)
	}
	var acct accountJSON
	if err := json.Unmarshal([]byte(out.String()), &acct); err != nil || !acct.SignedIn || acct.Me.Plan != "pro" || len(acct.Devices) != 1 {
		t.Fatalf("account %s %v", out.String(), err)
	}
	out.b.Reset()
	if err := runAccount(nil); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"as ada", "Plan: pro", "2 h of 100 h", "12.5 of 100 GB", "(this one)"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("account says %q, lacking %q", out.String(), want)
		}
	}
	out.b.Reset()
	if err := runLogout(nil); err != nil {
		t.Fatal(err)
	}
	if cloud.SignedIn() || f.Count("POST /v1/auth/revoke") != 1 || !strings.Contains(out.String(), "Signed out of Conch Cloud (ada)") {
		t.Fatalf("logout: %q", out.String())
	}
}

func TestLoginDeniedSaysSo(t *testing.T) {
	f, out := cloudIsolated(t)
	go func() {
		for f.Count("POST /v1/auth/device") == 0 {
			time.Sleep(5 * time.Millisecond)
		}
		f.Approve("denied")
	}()
	err := runLogin([]string{"-json", "-no-browser"})
	if err == nil || !strings.Contains(out.String(), `"event":"error"`) || !strings.Contains(out.String(), "denied") {
		t.Fatalf("err %v out %q", err, out.String())
	}
}

// TestNoAccountCommandsAskNothing: account and logout with no sign-in
// say so without a request.
func TestNoAccountCommandsAskNothing(t *testing.T) {
	f, out := cloudIsolated(t)
	f.Forbidden = true
	if err := runAccount([]string{"-json"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"signed_in": false`) {
		t.Fatalf("account: %q", out.String())
	}
	if err := runLogout(nil); err != nil {
		t.Fatal(err)
	}
	if err := runCloud([]string{"machine", "ls"}); err == nil || !strings.Contains(err.Error(), "conch login") {
		t.Fatalf("machine ls: %v", err)
	}
	if n := f.Total(); n != 0 {
		t.Fatalf("%d requests", n)
	}
}

// TestLoginServerFromConfig: [cloud] api in config.toml is the server
// signed in to; CONCH_CLOUD_URL wins over it.
func TestLoginServerFromConfig(t *testing.T) {
	f, _ := cloudIsolated(t)
	t.Setenv(cloud.URLEnv, "")
	os.MkdirAll(os.Getenv("CONCH_HOME"), 0o700)
	os.WriteFile(filepath.Join(os.Getenv("CONCH_HOME"), "config.toml"), []byte("[cloud]\napi = \""+f.Srv.URL+"/\"\n"), 0o600)
	if cloud.APIURL() != f.Srv.URL {
		t.Fatalf("APIURL %q", cloud.APIURL())
	}
	if err := signIn(t, f); err != nil {
		t.Fatal(err)
	}
	a, _ := cloud.LoadAccount()
	if a.API != f.Srv.URL {
		t.Fatalf("signed in to %q", a.API)
	}
	t.Setenv(cloud.URLEnv, "http://127.0.0.1:9")
	if cloud.APIURL() != "http://127.0.0.1:9" {
		t.Fatalf("env did not win: %q", cloud.APIURL())
	}
}

func TestCloudMachineCommands(t *testing.T) {
	f, out := cloudIsolated(t)
	if err := signIn(t, f); err != nil {
		t.Fatal(err)
	}
	if err := runCloud([]string{"machine", "new", "box", "-size", "large"}); err != nil {
		t.Fatal(err)
	}
	out.b.Reset()
	if err := runCloud([]string{"machine", "ls", "-json"}); err != nil {
		t.Fatal(err)
	}
	var ls struct {
		Machines []cloudMachineJSON `json:"machines"`
	}
	if err := json.Unmarshal([]byte(out.String()), &ls); err != nil || len(ls.Machines) != 1 {
		t.Fatalf("ls %q %v", out.String(), err)
	}
	m := ls.Machines[0]
	if m.Name != "box" || m.Size != "large" || m.State != "creating" || m.WebURL != f.Srv.URL+"/machines/"+m.ID {
		t.Fatalf("machine %+v", m)
	}
	f.SetMachine(m.ID, func(m *cloudtest.Machine) { m.State = "running" })
	out.b.Reset()
	if err := runCloud([]string{"machine", "sleep", "box"}); err != nil || !strings.Contains(out.String(), "box is asleep") {
		t.Fatalf("sleep: %v %q", err, out.String())
	}
	out.b.Reset()
	if err := runCloud([]string{"machine", "wake", m.ID}); err != nil || !strings.Contains(out.String(), "box is waking") {
		t.Fatalf("wake: %v %q", err, out.String())
	}
	f.SetMachine(m.ID, func(m *cloudtest.Machine) {
		m.Activity = &cloudtest.Activity{Agents: []cloudtest.AgentActivity{{Agent: "claude", State: "working"}}, ReportedAt: time.Now().UTC()}
	})
	if err := runCloud([]string{"machine", "sleep", "box"}); err == nil || !strings.Contains(err.Error(), "-force") {
		t.Fatalf("sleep while working: %v", err)
	}
	if err := runCloud([]string{"machine", "wake", "nope"}); err == nil || !strings.Contains(err.Error(), `no cloud machine "nope"`) {
		t.Fatalf("unknown: %v", err)
	}
	if err := runCloud([]string{"machine", "rm", "-y", "box"}); err != nil {
		t.Fatal(err)
	}
	out.b.Reset()
	runCloud([]string{"machine", "ls"})
	if !strings.Contains(out.String(), "No cloud machines yet") {
		t.Fatalf("ls after rm: %q", out.String())
	}
}

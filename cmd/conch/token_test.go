package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/server"
)

// forge is tok with its secret's last digit changed.
func forge(tok string) string {
	last := tok[len(tok)-1]
	swap := byte('0')
	if last == '0' {
		swap = '1'
	}
	return tok[:len(tok)-1] + string(swap)
}

func TestParseTokenLife(t *testing.T) {
	for _, tc := range []struct {
		in    string
		want  time.Duration
		never bool
		bad   bool
	}{
		{"30d", 30 * 24 * time.Hour, false, false},
		{" 1D ", 24 * time.Hour, false, false},
		{"12h", 12 * time.Hour, false, false},
		{"90m", 90 * time.Minute, false, false},
		{"1h30m", 90 * time.Minute, false, false},
		{"never", 0, true, false},
		{"NEVER", 0, true, false},
		{"0d", 0, false, true},
		{"30s", 0, false, true}, // under a minute
		{"-5d", 0, false, true},
		{"d", 0, false, true},
		{"", 0, false, true},
		{"soon", 0, false, true},
		{"36501d", 0, false, true},
		{"36500d", 36500 * 24 * time.Hour, false, false},
	} {
		got, never, err := parseTokenLife(tc.in)
		if (err != nil) != tc.bad || got != tc.want || never != tc.never {
			t.Errorf("%q: %v %v %v", tc.in, got, never, err)
		}
	}
}

func TestTokenExpiryWords(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.Local)
	if got := tokenExpiry(proto.TokenInfo{}, now); got != "never" {
		t.Errorf("no date: %q", got)
	}
	if got := tokenExpiry(proto.TokenInfo{Expires: now}, now); got != "expired" {
		t.Errorf("at its date: %q", got)
	}
	if got := tokenExpiry(proto.TokenInfo{Expires: now.Add(48 * time.Hour)}, now); got != "25 Nov 2026" {
		t.Errorf("later: %q", got)
	}
	if got := tokenProjects(proto.TokenInfo{}); got != "all" {
		t.Errorf("no projects: %q", got)
	}
	if got := tokenProjects(proto.TokenInfo{Projects: []proto.TokenProject{{Name: "api"}, {Name: "web"}}}); got != "api,web" {
		t.Errorf("projects: %q", got)
	}
}

// tokenServer is a real server on the test's socket.
func tokenServer(t *testing.T) *client.Client {
	t.Helper()
	dir := a4Env(t)
	srv := server.New(config.SocketPath(), dir)
	go srv.Run()
	t.Cleanup(srv.StopAndWait)
	for i := 0; ; i++ {
		c, err := client.Dial(config.SocketPath(), "test")
		if err == nil {
			t.Cleanup(func() { c.Close() })
			return c
		}
		if i == 200 {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestTokenCommand(t *testing.T) {
	tokenServer(t)
	var err error
	out, errOut := a4Capture(t, "", func() { err = runToken([]string{"create", "-level", "prompt", "-expires", "2d", "reviewer"}) })
	if err != nil {
		t.Fatal(err)
	}
	tok := strings.TrimSpace(out)
	if !strings.HasPrefix(tok, "conch_t_") || strings.Contains(tok, "\n") {
		t.Fatalf("stdout is not the token alone: %q", out)
	}
	if !strings.Contains(errOut, "reviewer: a prompt token, in every project, until") || !strings.Contains(errOut, "shown once") {
		t.Errorf("stderr %q", errOut)
	}

	out, _ = a4Capture(t, "", func() { err = runToken([]string{"ls"}) })
	if err != nil || !strings.Contains(out, "reviewer") || !strings.Contains(out, "prompt") || strings.Contains(out, tok) {
		t.Errorf("ls: %v\n%s", err, out)
	}
	out, _ = a4Capture(t, "", func() { err = runToken([]string{"ls", "-json"}) })
	var list proto.TokenListResult
	if err != nil || json.Unmarshal([]byte(out), &list) != nil || len(list.Tokens) != 1 || list.Tokens[0].Level != proto.TokenPrompt {
		t.Errorf("ls -json: %v %q", err, out)
	}

	for _, args := range [][]string{
		{"create"}, {"create", "a", "b"}, {"create", "-expires", "soon", "x"}, {"create", "-level", "admin", "x"},
		{"create", "-project", "nowhere", "x"}, {"create", "reviewer"}, {"revoke"}, {"ls", "extra"}, {"frob"},
	} {
		a4Capture(t, "", func() { err = runToken(args) })
		if err == nil {
			t.Errorf("conch token %q worked", args)
		}
	}

	// Holding the prompt token, the command is that token's: it may not
	// list tokens.
	t.Setenv("CONCH_TOKEN", tok)
	a4Capture(t, "", func() { err = runToken([]string{"ls"}) })
	if err == nil || !strings.Contains(err.Error(), "may not") {
		t.Errorf("ls holding a prompt token: %v", err)
	}
	t.Setenv("CONCH_TOKEN", forge(tok))
	a4Capture(t, "", func() { err = runToken([]string{"ls"}) })
	if err == nil || !strings.Contains(err.Error(), "refused") {
		t.Errorf("ls holding a forged token: %v", err)
	}
	t.Setenv("CONCH_TOKEN", "")

	out, _ = a4Capture(t, "", func() { err = runToken([]string{"revoke", "reviewer"}) })
	if err != nil || !strings.Contains(out, "revoked reviewer") {
		t.Errorf("revoke: %v %q", err, out)
	}
	out, _ = a4Capture(t, "", func() { err = runToken(nil) }) // bare: ls
	if err != nil || !strings.Contains(out, "no access tokens") {
		t.Errorf("ls after revoking: %v %q", err, out)
	}
}

// mcpHTTPFixture is conch mcp -http in front of a real server, with a
// read token and a full one.
func mcpHTTPFixture(t *testing.T) (url, read, full string, person *client.Client) {
	t.Helper()
	person = tokenServer(t)
	mk := func(name, level string) string {
		var res proto.TokenCreateResult
		if err := person.Call(t.Context(), proto.MethodTokenCreate, proto.TokenCreateParams{Name: name, Level: level}, &res); err != nil {
			t.Fatal(err)
		}
		return res.Token
	}
	read, full = mk("reader", proto.TokenRead), mk("admin", proto.TokenFull)
	h := newMCPHTTP(func(token string) (*client.Client, error) {
		c, err := connect(false)
		if err != nil {
			return nil, err
		}
		if err := c.Present(context.Background(), token); err != nil {
			c.Close()
			return nil, err
		}
		return c, nil
	})
	t.Cleanup(h.close)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.URL + "/mcp", read, full, person
}

func mcpPost(t *testing.T, url, token, origin, body string) (*http.Response, a4MCPReply) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var r a4MCPReply
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
			t.Fatal(err)
		}
	}
	return resp, r
}

func TestMCPOverHTTP(t *testing.T) {
	url, read, full, person := mcpHTTPFixture(t)
	initialize := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`

	// No way in without a token conch accepts.
	for name, tok := range map[string]string{"none": "", "garbage": "hunter2", "forged": forge(read)} {
		resp, _ := mcpPost(t, url, tok, "", initialize)
		if resp.StatusCode != http.StatusUnauthorized || !strings.HasPrefix(resp.Header.Get("WWW-Authenticate"), "Bearer") {
			t.Errorf("%s token: %d %q", name, resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
		}
	}
	if resp, _ := mcpPost(t, url, read, "https://evil.example", initialize); resp.StatusCode != http.StatusForbidden {
		t.Errorf("another site's page: %d", resp.StatusCode)
	}
	if resp, r := mcpPost(t, url, read, "http://localhost:3000", initialize); resp.StatusCode != http.StatusOK || r.Result.ServerInfo.Name != "conch" {
		t.Errorf("a local page: %d %+v", resp.StatusCode, r)
	}
	if resp, err := http.Get(url); err != nil || resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET: %v %v", resp, err)
	} else {
		resp.Body.Close()
	}
	if resp, err := http.Post(strings.TrimSuffix(url, "/mcp")+"/other", "application/json", strings.NewReader(initialize)); err != nil || resp.StatusCode != http.StatusNotFound {
		t.Errorf("another path: %v %v", resp, err)
	} else {
		resp.Body.Close()
	}

	resp, r := mcpPost(t, url, read, "", initialize)
	if resp.StatusCode != http.StatusOK || r.Result.ProtocolVersion != mcpProtocol {
		t.Fatalf("initialize: %d %+v", resp.StatusCode, r)
	}
	if resp, _ := mcpPost(t, url, read, "", `{"jsonrpc":"2.0","method":"notifications/initialized"}`); resp.StatusCode != http.StatusAccepted {
		t.Errorf("a notification: %d", resp.StatusCode)
	}
	if _, r := mcpPost(t, url, read, "", `[{"jsonrpc":"2.0","id":1,"method":"ping"}]`); r.Error == nil || r.Error.Code != mcpInvalidRequest {
		t.Errorf("a batch: %+v", r)
	}
	if _, r := mcpPost(t, url, read, "", `{nope`); r.Error == nil || r.Error.Code != mcpParseError {
		t.Errorf("not JSON: %+v", r)
	}
	if resp, _ := mcpPost(t, url, read, "", strings.Repeat(" ", mcpMaxLine+1)); resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("too big: %d", resp.StatusCode)
	}

	// Each token is held to its own level by the server.
	var info proto.PaneInfo
	if err := person.Call(t.Context(), proto.MethodPaneCreate, proto.PaneCreateParams{Command: []string{"/bin/sleep", "60"}, NoProject: true, Cols: 20, Rows: 5}, &info); err != nil {
		t.Fatal(err)
	}
	list := a4MCPCall(2, "list", nil)
	if _, r := mcpPost(t, url, read, "", list); r.Result.IsError || !strings.Contains(r.text(), info.ID) {
		t.Errorf("read token lists: %s", r.text())
	}
	rename := a4MCPCall(3, "rename", map[string]any{"pane": info.ID, "name": "renamed"})
	if _, r := mcpPost(t, url, read, "", rename); !r.Result.IsError || !strings.Contains(r.text(), `"reader" may not`) {
		t.Errorf("read token renames: %s", r.text())
	}
	if _, r := mcpPost(t, url, full, "", rename); r.Result.IsError {
		t.Errorf("full token renames: %s", r.text())
	}

	// Revoked: its next call is refused, on the session already open.
	if err := person.Call(t.Context(), proto.MethodTokenRevoke, proto.TokenRevokeParams{Name: "reader"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, r := mcpPost(t, url, read, "", list); !r.Result.IsError || !strings.Contains(r.text(), "revoked") {
		t.Errorf("after revoking: %s", r.text())
	}
}

// The token a process inherited is not one of the tokens sent: conch mcp
// -http drops it before connecting.
func TestMCPHTTPDropsInheritedToken(t *testing.T) {
	a4Env(t)
	t.Setenv("CONCH_TOKEN", "conch_p_00000000_00000000_"+strings.Repeat("0", 64))
	done := make(chan error, 1)
	go func() { done <- runMCPHTTP("127.0.0.1:-1") }() // a bad address: returns at once
	select {
	case err := <-done:
		if err == nil {
			t.Error("a bad address served")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runMCPHTTP did not return")
	}
	if v := os.Getenv("CONCH_TOKEN"); v != "" {
		t.Errorf("inherited token kept: %q", v)
	}
}

func TestIsLoopback(t *testing.T) {
	for host, want := range map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true, "127.0.0.2": true,
		"0.0.0.0": false, "10.0.0.1": false, "evil.example": false, "": false} {
		if got := isLoopback(host); got != want {
			t.Errorf("%q: %v", host, got)
		}
	}
}

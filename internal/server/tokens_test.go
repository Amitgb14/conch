package server_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/server"
)

// dialToken is a connection from outside the panes holding tok.
func dialToken(t *testing.T, c *client.Client, tok string) *client.Client {
	t.Helper()
	sock := sockOf(t, c)
	tc, err := client.Dial(sock, "token-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tc.Close() })
	if err := tc.Present(t.Context(), tok); err != nil {
		t.Fatal(err)
	}
	return tc
}

// sockets remembers sockOf's answers, a pane each otherwise.
var sockets sync.Map // *client.Client → string

// sockOf is the socket a test's client reached; the panes are told it.
func sockOf(t *testing.T, c *client.Client) string {
	t.Helper()
	if s, ok := sockets.Load(c); ok {
		return s.(string)
	}
	var info proto.PaneInfo
	if err := c.Call(t.Context(), proto.MethodPaneCreate, proto.PaneCreateParams{
		Command: []string{"/bin/sh", "-c", `printf 'SOCK=%s\n' "$CONCH_SOCKET"; sleep 60`}, NoProject: true, Cols: 200, Rows: 5}, &info); err != nil {
		t.Fatal(err)
	}
	defer c.Call(t.Context(), proto.MethodPaneClose, proto.PaneRef{ID: info.ID}, nil)
	for range 500 {
		var screen proto.PaneReadResult
		c.Call(t.Context(), proto.MethodPaneRead, proto.PaneRef{ID: info.ID}, &screen)
		for _, l := range screen.Lines {
			if s, ok := strings.CutPrefix(strings.TrimSpace(l), "SOCK="); ok && s != "" {
				sockets.Store(c, s)
				return s
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no socket from the pane")
	return ""
}

func makeToken(t *testing.T, c *client.Client, p proto.TokenCreateParams) string {
	t.Helper()
	var res proto.TokenCreateResult
	if err := c.Call(t.Context(), proto.MethodTokenCreate, p, &res); err != nil {
		t.Fatalf("token.create %+v: %v", p, err)
	}
	return res.Token
}

func codeOf(err error) string {
	var perr *proto.Error
	switch {
	case err == nil:
		return "ok"
	case errors.As(err, &perr):
		return perr.Code
	}
	return "error: " + err.Error()
}

// Each level against what it may and may not do, held to project A.
func TestTokenLevels(t *testing.T) {
	c, a, b, projA, projB, inA, inB, loose := scopeFixture(t)
	agent := startHelper(t, c, a, "claude") // an agent in A, to prompt
	if err := os.WriteFile(filepath.Join(a, "notes.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(b, "secret.txt"), []byte("no"), 0o644)

	conns := map[string]*client.Client{}
	for _, level := range proto.TokenLevels {
		conns[level] = dialToken(t, c, makeToken(t, c, proto.TokenCreateParams{Name: level, Level: level, Projects: []string{projA.Name}}))
	}
	everywhere := dialToken(t, c, makeToken(t, c, proto.TokenCreateParams{Name: "everywhere", Level: proto.TokenRead}))

	type call struct {
		name   string
		method string
		params any
	}
	read := func(id string) call { return call{"read " + id, proto.MethodPaneRead, proto.PaneRef{ID: id}} }
	send := func(id string) call {
		return call{"type into " + id, proto.MethodPaneSendText, proto.PaneSendTextParams{ID: id, Text: "x"}}
	}
	shellIn := func(dir string) call {
		return call{"shell in " + dir, proto.MethodPaneCreate, proto.PaneCreateParams{Cwd: dir, Command: []string{"/bin/sleep", "60"}, Cols: 20, Rows: 5}}
	}
	fsRead := func(root, path string) call {
		return call{"fs.read " + path, proto.MethodFSRead, proto.FSReadParams{Root: root, Path: path}}
	}
	// Want, by level: read, prompt, drive, full.
	const ok, no = "ok", proto.ErrOutOfScope
	for _, tc := range []struct {
		call
		want [4]string
	}{
		{read(inA), [4]string{ok, ok, ok, ok}},
		{read(inB), [4]string{no, no, no, no}}, // another project, even to read
		{call{"list panes", proto.MethodPaneList, nil}, [4]string{ok, ok, ok, ok}},
		{fsRead(a, "notes.txt"), [4]string{ok, ok, ok, ok}},
		{fsRead(b, "secret.txt"), [4]string{no, no, no, no}},
		{fsRead(a, "../b/secret.txt"), [4]string{no, no, no, no}}, // judged where it lands
		{fsRead("", "notes.txt"), [4]string{no, no, no, no}},      // relative to nothing
		{send(agent.id), [4]string{no, ok, ok, ok}},
		{send(inA), [4]string{no, no, no, ok}}, // a shell: what is typed runs
		{send(inB), [4]string{no, no, no, no}},
		{call{"broadcast with shells", proto.MethodAgentBroadcast, proto.AgentBroadcastParams{IDs: []string{agent.id}, Text: "x", Shells: true}}, [4]string{no, no, no, ok}},
		{call{"agent pane with a command", proto.MethodPaneCreate, proto.PaneCreateParams{Agent: "claude", Command: []string{"/bin/sleep", "60"}, Cwd: a}}, [4]string{no, no, no, ok}},
		{shellIn(a), [4]string{no, no, no, ok}},
		{shellIn(b), [4]string{no, no, no, no}},
		{call{"pane in no project", proto.MethodPaneCreate, proto.PaneCreateParams{NoProject: true, Command: []string{"/bin/sleep", "60"}}}, [4]string{no, no, no, no}},
		{call{"close a shell", proto.MethodPaneClose, proto.PaneRef{ID: loose}}, [4]string{no, no, no, no}},
		{call{"discard in B", proto.MethodBranchDiscard, proto.BranchDiscardParams{ProjectID: projB.ID, Branch: "nope"}}, [4]string{no, no, no, no}},
		{call{"remove B", proto.MethodProjectRemove, proto.ProjectRef{ID: projB.ID}}, [4]string{no, no, no, no}},
		{call{"stop the server", proto.MethodServerStop, nil}, [4]string{no, no, no, no}},
		{call{"make a token", proto.MethodTokenCreate, proto.TokenCreateParams{Name: "more", Level: proto.TokenFull}}, [4]string{no, no, no, no}},
		{call{"schedule", proto.MethodScheduleAdd, proto.ScheduledItem{}}, [4]string{no, no, no, no}},
		// An agent's flags and environment are the person's to give: below
		// full, only which agent and its --model.
		{call{"agent with its model", proto.MethodPaneCreate, proto.PaneCreateParams{Agent: "claude", AgentArgs: "--model opus", Cwd: a}}, [4]string{no, no, ok, ok}},
		{call{"agent told to ask nobody", proto.MethodPaneCreate, proto.PaneCreateParams{Agent: "claude", AgentArgs: "--dangerously-skip-permissions", Cwd: a}}, [4]string{no, no, no, ok}},
		{call{"agent with an environment", proto.MethodPaneCreate, proto.PaneCreateParams{Agent: "claude", Env: []string{"X=1"}, Cwd: a}}, [4]string{no, no, no, ok}},
		// An ACP agent's command runs in the server: never a token's held
		// to projects, whatever its level.
		{call{"check an ACP command", proto.MethodAgentACPCheck, proto.AgentACPSpec{Command: "/bin/true"}}, [4]string{no, no, no, no}},
		{call{"add an ACP agent", proto.MethodAgentACPAdd, proto.AgentACPAddParams{AgentACPSpec: proto.AgentACPSpec{Name: "x", Command: "/bin/true"}}}, [4]string{no, no, no, no}},
	} {
		for i, level := range proto.TokenLevels {
			var out proto.PaneInfo
			got := codeOf(conns[level].Call(t.Context(), tc.method, tc.params, &out))
			if got != tc.want[i] {
				t.Errorf("%s token: %s: %s, want %s", level, tc.name, got, tc.want[i])
			}
			if tc.method == proto.MethodPaneCreate && got == ok {
				c.Call(t.Context(), proto.MethodPaneClose, proto.PaneRef{ID: out.ID}, nil)
			}
		}
	}

	// A drive token's agent flags never reach a shell: the call is
	// refused, and nothing the flags said ran.
	marker := filepath.Join(a, "MARKER")
	if got := codeOf(conns[proto.TokenDrive].Call(t.Context(), proto.MethodPaneCreate, proto.PaneCreateParams{Agent: "claude", AgentArgs: `--model "$(touch ` + marker + `)x"`, Cwd: a}, nil)); got != no {
		t.Errorf("drive token starts an agent with a shell in its flags: %s", got)
	}
	time.Sleep(300 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Error("a drive token's agent flags ran a command")
	}

	// Stopping an agent is driving: the drive token closes an agent's pane
	// in its project, which the prompt token may not.
	victim := startHelper(t, c, a, "claude")
	if got := codeOf(conns[proto.TokenPrompt].Call(t.Context(), proto.MethodPaneClose, proto.PaneRef{ID: victim.id}, nil)); got != no {
		t.Errorf("prompt token closes an agent: %s", got)
	}
	if got := codeOf(conns[proto.TokenDrive].Call(t.Context(), proto.MethodPaneClose, proto.PaneRef{ID: victim.id}, nil)); got != ok {
		t.Errorf("drive token closes an agent in its project: %s", got)
	}
	// ... and a shell in its project only at full.
	if got := codeOf(conns[proto.TokenDrive].Call(t.Context(), proto.MethodPaneClose, proto.PaneRef{ID: inA}, nil)); got != no {
		t.Errorf("drive token closes a shell: %s", got)
	}

	// A run is the project's it was made in: a full token held to A
	// removes or starts A's runs, and none of B's.
	for _, pr := range []proto.ProjectInfo{projA, projB} {
		var run proto.Run
		if err := c.Call(t.Context(), proto.MethodRunCreate, proto.RunCreateParams{Name: "run-" + pr.Name, ProjectID: pr.ID, Manual: true}, &run); err != nil {
			t.Fatal(err)
		}
		want := ok
		if pr.ID == projB.ID {
			want = no
		}
		if got := codeOf(conns[proto.TokenFull].Call(t.Context(), proto.MethodRunRemove, proto.RunRef{Run: run.ID}, nil)); got != want {
			t.Errorf("full token held to A removes a run in %s: %s, want %s", pr.Name, got, want)
		}
		if got := codeOf(conns[proto.TokenFull].Call(t.Context(), proto.MethodRunRemove, proto.RunRef{Run: run.Name}, nil)); pr.ID == projB.ID && got != no {
			t.Errorf("full token held to A removes B's run by name: %s", got)
		}
	}

	// A read token with no projects reads in every project, and no file
	// outside them: ~/.ssh is not a diff.
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "id_rsa"), []byte("key"), 0o600)
	for _, tc := range []struct {
		root, path, want string
	}{
		{b, "secret.txt", ok},
		{a, "notes.txt", ok},
		{outside, "id_rsa", no},
		{"", filepath.Join(outside, "id_rsa"), no},
	} {
		if got := codeOf(everywhere.Call(t.Context(), proto.MethodFSRead, proto.FSReadParams{Root: tc.root, Path: tc.path}, nil)); got != tc.want {
			t.Errorf("unlimited read token reads %s %s: %s, want %s", tc.root, tc.path, got, tc.want)
		}
	}

	// A read token with no projects reads anywhere and changes nothing.
	if got := codeOf(everywhere.Call(t.Context(), proto.MethodPaneRead, proto.PaneRef{ID: inB}, nil)); got != ok {
		t.Errorf("unlimited read token reads B: %s", got)
	}
	if got := codeOf(everywhere.Call(t.Context(), proto.MethodServerStop, nil, nil)); got != no {
		t.Errorf("read token stops the server: %s", got)
	}

	// Nothing refused was done.
	var list proto.PaneList
	c.Call(t.Context(), proto.MethodPaneList, nil, &list)
	running := map[string]bool{}
	for _, p := range list.Panes {
		running[p.ID] = p.State == proto.PaneRunning
	}
	for _, id := range []string{inA, inB, loose, agent.id} {
		if !running[id] {
			t.Errorf("%s is not running after refusals", id)
		}
	}
	var projects proto.ProjectList
	c.Call(t.Context(), proto.MethodProjectList, nil, &projects)
	if len(projects.Projects) != 2 {
		t.Errorf("%d projects after refusals", len(projects.Projects))
	}
}

// The person manages tokens; an unlimited full token may too; an agent in
// a pane may not.
func TestTokenManagement(t *testing.T) {
	c, a, _, _, _, _, _, _ := scopeFixture(t)
	full := dialToken(t, c, makeToken(t, c, proto.TokenCreateParams{Name: "admin", Level: proto.TokenFull, Never: true}))
	if got := codeOf(full.Call(t.Context(), proto.MethodTokenCreate, proto.TokenCreateParams{Name: "made-by-admin", Level: proto.TokenRead}, nil)); got != "ok" {
		t.Errorf("full token makes a token: %s", got)
	}
	var list proto.TokenListResult
	if err := c.Call(t.Context(), proto.MethodTokenList, nil, &list); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tk := range list.Tokens {
		names = append(names, tk.Name)
		if tk.Level == proto.TokenPane {
			t.Errorf("a pane's token is listed: %+v", tk)
		}
	}
	if !slices.Equal(names, []string{"admin", "made-by-admin"}) {
		t.Errorf("listed %q", names)
	}
	if got := codeOf(c.Call(t.Context(), proto.MethodTokenCreate, proto.TokenCreateParams{Name: "x", Level: proto.TokenRead, Projects: []string{"nope"}}, nil)); got != proto.ErrNotFound {
		t.Errorf("unknown project: %s", got)
	}
	if got := codeOf(c.Call(t.Context(), proto.MethodTokenCreate, proto.TokenCreateParams{Name: "x", Level: proto.TokenRead, Projects: []string{a}}, nil)); got != "ok" {
		t.Errorf("project by folder: %s", got)
	}

	h := startHelper(t, c, a, "claude")
	if code, _ := h.do(proto.MethodTokenRevoke, "admin"); code != proto.ErrOutOfScope {
		t.Errorf("an agent revokes a token: %s", code)
	}
}

// A token revoked while a connection holds it stops that connection's
// next call, not only the next connection.
func TestTokenRevokedMidConnection(t *testing.T) {
	c, _, _, _, _, inA, _, _ := scopeFixture(t)
	tc := dialToken(t, c, makeToken(t, c, proto.TokenCreateParams{Name: "ci", Level: proto.TokenRead}))
	if err := tc.Call(t.Context(), proto.MethodPaneRead, proto.PaneRef{ID: inA}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(t.Context(), proto.MethodTokenRevoke, proto.TokenRevokeParams{Name: "ci"}, nil); err != nil {
		t.Fatal(err)
	}
	if got := codeOf(tc.Call(t.Context(), proto.MethodPaneRead, proto.PaneRef{ID: inA}, nil)); got != proto.ErrUnauthorized {
		t.Errorf("after revoking: %s", got)
	}
}

// A forged token and another machine's are refused when presented; a
// connection holds one token only.
func TestTokenForgedAndAnotherMachine(t *testing.T) {
	c, _, _, _, _, _, _, _ := scopeFixture(t)
	other, _ := startServer(t)
	tok := makeToken(t, c, proto.TokenCreateParams{Name: "ci", Level: proto.TokenRead})
	theirs := makeToken(t, other, proto.TokenCreateParams{Name: "ci", Level: proto.TokenRead})
	sock := sockOf(t, c)
	try := func(tok string) error {
		tc, err := client.Dial(sock, "token-test")
		if err != nil {
			t.Fatal(err)
		}
		defer tc.Close()
		return tc.Present(t.Context(), tok)
	}
	forged := tok[:strings.LastIndex(tok, "_")+1] + strings.Repeat("a", 64)
	for name, bad := range map[string]string{"forged": forged, "another machine's": theirs, "garbage": "hunter2"} {
		err := try(bad)
		var perr *proto.Error
		if !errors.As(err, &perr) || perr.Code != proto.ErrUnauthorized {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := try(theirs); err == nil || !strings.Contains(err.Error(), "another machine") {
		t.Errorf("another machine's token says %v", err)
	}

	tc := dialToken(t, c, tok)
	second := makeToken(t, c, proto.TokenCreateParams{Name: "second", Level: proto.TokenFull})
	if got := codeOf(tc.Call(t.Context(), proto.MethodAuthToken, proto.AuthTokenParams{Token: second}, nil)); got != proto.ErrBadRequest {
		t.Errorf("a second token on one connection: %s", got)
	}
	if got := codeOf(tc.Call(t.Context(), proto.MethodAuthToken, proto.AuthTokenParams{Token: tok}, nil)); got != "ok" {
		t.Errorf("the same token again: %s", got)
	}
}

// The reparented helper: a process whose ancestors include no pane, which
// the walk takes for the person, holding its pane's token is held to that
// pane — the fail-open this is for.
func TestTokenReparentedKeepsPaneScope(t *testing.T) {
	c, a, _, _, _, inA, inB, _ := scopeFixture(t)
	h := startHelper(t, c, a, "claude")
	code, tok := h.do("token", "")
	if code != "ok" || !proto.IsPaneToken(tok) {
		t.Fatalf("the pane's token: %s %q", code, tok)
	}

	// Without it, this test process is the person, and may.
	sock := sockOf(t, c)
	plain, err := client.Dial(sock, "plain")
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	var who proto.CallerInfo
	plain.Call(t.Context(), proto.MethodPaneCaller, nil, &who)
	if who.Scoped {
		t.Fatal("the test process is scoped without a token")
	}

	held := dialToken(t, c, tok)
	if held.Token.Pane != h.id || held.Token.Level != proto.TokenPane {
		t.Errorf("holds %+v", held.Token)
	}
	held.Call(t.Context(), proto.MethodPaneCaller, nil, &who)
	if !who.Scoped || who.Pane != h.id {
		t.Errorf("caller %+v, want scoped as %s", who, h.id)
	}
	if got := codeOf(held.Call(t.Context(), proto.MethodPaneSendText, proto.PaneSendTextParams{ID: inB, Text: "x"}, nil)); got != proto.ErrOutOfScope {
		t.Errorf("type into another project's pane: %s", got)
	}
	if got := codeOf(held.Call(t.Context(), proto.MethodServerStop, nil, nil)); got != proto.ErrOutOfScope {
		t.Errorf("stop the server: %s", got)
	}
	if got := codeOf(held.Call(t.Context(), proto.MethodPaneSendText, proto.PaneSendTextParams{ID: h.id, Text: "hello\r"}, nil)); got != "ok" {
		t.Errorf("type into its own pane: %s", got)
	}
	if got := codeOf(held.Call(t.Context(), proto.MethodActFor, proto.ActForParams{ID: "elsewhere/p1@1"}, nil)); got != proto.ErrBadRequest {
		t.Errorf("act_for from a pane token's connection: %s", got)
	}

	// What it starts is its pane's, as for the walk.
	var made proto.PaneInfo // a plain shell: what the launch rule lets an agent start
	if err := held.Call(t.Context(), proto.MethodPaneCreate, proto.PaneCreateParams{Cwd: a, Cols: 20, Rows: 5}, &made); err != nil {
		t.Fatal(err)
	}
	if made.CreatedBy != h.id {
		t.Errorf("made %s, created by %q, want %s", made.ID, made.CreatedBy, h.id)
	}

	// Its pane closes: the token goes with it, and the connection still
	// holding it stays held — never the person.
	if err := c.Call(t.Context(), proto.MethodPaneClose, proto.PaneRef{ID: h.id}, nil); err != nil {
		t.Fatal(err)
	}
	if got := codeOf(held.Call(t.Context(), proto.MethodPaneSendText, proto.PaneSendTextParams{ID: inA, Text: "x"}, nil)); got != proto.ErrOutOfScope {
		t.Errorf("after its pane closed, type into a pane it never started: %s", got)
	}
	tc, err := client.Dial(sock, "late")
	if err != nil {
		t.Fatal(err)
	}
	defer tc.Close()
	if got := codeOf(tc.Present(t.Context(), tok)); !strings.Contains(got, "closed") && got != proto.ErrUnauthorized {
		t.Errorf("a closed pane's token presented: %s", got)
	}
}

// A pane's token presented from another pane leaves the connection where
// the kernel put it: a token is a string anybody reading that pane's
// environment could copy.
func TestTokenFromAnotherPaneKeepsOwn(t *testing.T) {
	c, a, b, _, _, _, _, _ := scopeFixture(t)
	first := startHelper(t, c, a, "claude")
	second := startHelper(t, c, b, "claude")
	_, tok := first.do("token", "")
	if code, pane := second.do("present", tok); code != "ok" || pane != second.id {
		t.Errorf("presented A's token from B's pane: %s, placed in %q, want %s", code, pane, second.id)
	}
	if code, id := second.do(proto.MethodPaneCaller, ""); code != "ok" || !strings.Contains(id, "/"+second.id+"@") {
		t.Errorf("caller after: %s %s", code, id)
	}
}

// Every pane is given a token of its own, and a new pane is never given
// the one the server inherited.
func TestTokenInEveryPane(t *testing.T) {
	a5Env(t)
	t.Setenv("CONCH_TOKEN", "conch_t_00000000_00000000_"+strings.Repeat("0", 64)) // the server's own, inherited
	sockDir, err := os.MkdirTemp("", "tk")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(sockDir)
	srv := server.New(filepath.Join(sockDir, "s.sock"), sockDir)
	go srv.Run()
	defer srv.StopAndWait()
	t.Setenv("CONCH_TOKEN", "")
	var c *client.Client
	for range 100 {
		if c, err = client.Dial(filepath.Join(sockDir, "s.sock"), "test"); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if !slices.Contains(c.Server.Capabilities, proto.CapToken) {
		t.Fatalf("no %s in %v", proto.CapToken, c.Server.Capabilities)
	}
	seen := map[string]bool{}
	for range 2 {
		var info proto.PaneInfo
		if err := c.Call(t.Context(), proto.MethodPaneCreate, proto.PaneCreateParams{
			Command: []string{"/bin/sh", "-c", `printf 'TOK=%s\n' "$CONCH_TOKEN"; sleep 60`}, NoProject: true, Cols: 200, Rows: 5}, &info); err != nil {
			t.Fatal(err)
		}
		var tok string
		for i := 0; tok == "" && i < 500; i++ {
			var screen proto.PaneReadResult
			c.Call(t.Context(), proto.MethodPaneRead, proto.PaneRef{ID: info.ID}, &screen)
			for _, l := range screen.Lines {
				if s, ok := strings.CutPrefix(strings.TrimSpace(l), "TOK="); ok && s != "" {
					tok = s
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		if !proto.IsPaneToken(tok) || seen[tok] {
			t.Errorf("pane %s was given %q", info.ID, tok)
		}
		seen[tok] = true
	}
}

// Tokens outlive the server that made them; a pane's does not outlive a
// restart, which ends every pane.
func TestTokenSurvivesRestart(t *testing.T) {
	a5Env(t)
	dir, err := os.MkdirTemp("", "tk")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "s.sock")
	start := func() (*server.Server, *client.Client) {
		srv := server.New(sock, dir)
		go srv.Run()
		for range 200 {
			if c, err := client.Dial(sock, "test"); err == nil {
				return srv, c
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("no server")
		return nil, nil
	}
	srv, c := start()
	tok := makeToken(t, c, proto.TokenCreateParams{Name: "ci", Level: proto.TokenRead})
	var info proto.PaneInfo
	if err := c.Call(t.Context(), proto.MethodPaneCreate, proto.PaneCreateParams{
		Command: []string{"/bin/sh", "-c", `printf 'TOK=%s\n' "$CONCH_TOKEN"; sleep 60`}, NoProject: true, Cols: 200, Rows: 5}, &info); err != nil {
		t.Fatal(err)
	}
	var paneTok string
	for i := 0; paneTok == "" && i < 500; i++ {
		var screen proto.PaneReadResult
		c.Call(t.Context(), proto.MethodPaneRead, proto.PaneRef{ID: info.ID}, &screen)
		for _, l := range screen.Lines {
			if s, ok := strings.CutPrefix(strings.TrimSpace(l), "TOK="); ok && s != "" {
				paneTok = s
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	c.Close()
	srv.StopAndWait()

	srv, c = start()
	defer func() { c.Close(); srv.StopAndWait() }()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := c.Present(ctx, tok); err != nil {
		t.Errorf("the person's token after a restart: %v", err)
	}
	c2, err := client.Dial(sock, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	if got := codeOf(c2.Present(ctx, paneTok)); got == "ok" {
		t.Errorf("a pane's token outlived a restart: %s", got)
	}
}

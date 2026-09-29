package server_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// TestScopeHelper is not a test: it is the program a scope test runs in a
// pane, a caller really inside it. Each line it reads is "N METHOD [ARG]";
// it makes that call on one connection and prints "=> N CODE [ID]", which
// the typed line never contains. Other lines — text a test typed into this
// pane as a target — are skipped. It drops CONCH_PANE_ID first: who is
// calling is the kernel's to say.
//
// "N reach SOCK" does what `conch -m` does: it asks its own server who it
// is, connects to the server at SOCK — another machine's, reached from
// outside its panes — and, when its own server says it is a scoped agent,
// asks to be held to that there. Later lines go to that server.
func TestScopeHelper(t *testing.T) {
	if os.Getenv("CONCH_SCOPE_HELPER") != "1" {
		t.Skip("run by the scope tests")
	}
	os.Unsetenv("CONCH_PANE_ID")
	c, err := client.Dial(os.Getenv("CONCH_SOCKET"), "scope-helper")
	if err != nil {
		fmt.Println("=> 0 dial", err)
		select {}
	}
	in := bufio.NewScanner(os.Stdin)
	for in.Scan() {
		f := strings.Fields(in.Text())
		if len(f) < 2 {
			continue
		}
		n, err := strconv.Atoi(f[0])
		if err != nil {
			continue
		}
		f = f[1:]
		arg := ""
		if len(f) > 1 {
			arg = f[1]
		}
		if f[0] == "reach" {
			var who proto.CallerInfo
			if err := c.Call(context.Background(), proto.MethodPaneCaller, nil, &who); err != nil {
				fmt.Printf("=> %d error\n", n)
				continue
			}
			there, err := client.Dial(arg, "scope-helper")
			if err != nil {
				fmt.Printf("=> %d dial\n", n)
				continue
			}
			if who.Scoped {
				err := there.Call(context.Background(), proto.MethodActFor,
					proto.ActForParams{ID: who.ID, Label: who.Label, Agent: who.Agent}, nil)
				var perr *proto.Error
				if errors.As(err, &perr) {
					fmt.Printf("=> %d %s\n", n, perr.Code)
					continue
				} else if err != nil {
					fmt.Printf("=> %d error\n", n)
					continue
				}
			}
			c = there
			fmt.Printf("=> %d ok %v\n", n, who.Scoped)
			continue
		}
		if f[0] == proto.MethodPaneCaller {
			var who proto.CallerInfo
			if err := c.Call(context.Background(), proto.MethodPaneCaller, nil, &who); err != nil {
				fmt.Printf("=> %d error\n", n)
			} else {
				fmt.Printf("=> %d ok %s\n", n, who.ID)
			}
			continue
		}
		var params any
		switch f[0] {
		case proto.MethodPaneSendText:
			params = proto.PaneSendTextParams{ID: arg, Text: "from-helper\r"}
		case proto.MethodAgentBroadcast:
			params = proto.AgentBroadcastParams{IDs: strings.Split(arg, ","), Text: "from-helper"}
		case proto.MethodPaneCreate:
			params = proto.PaneCreateParams{Command: []string{"/bin/sleep", "60"}, Cwd: arg, Cols: 40, Rows: 5}
		case proto.MethodProjectRemove:
			params = proto.ProjectRef{ID: arg}
		case proto.MethodBranchDiscard:
			params = proto.BranchDiscardParams{ProjectID: arg, Branch: "nope"}
		case proto.MethodServerStop, proto.MethodServerReload:
			params = nil
		default:
			params = proto.PaneRef{ID: arg}
		}
		var info proto.PaneInfo
		err = c.Call(context.Background(), f[0], params, &info)
		var perr *proto.Error
		switch {
		case err == nil:
			fmt.Printf("=> %d ok %s\n", n, info.ID)
		case errors.As(err, &perr):
			fmt.Printf("=> %d %s\n", n, perr.Code)
		default:
			fmt.Printf("=> %d error\n", n)
		}
	}
	select {}
}

// helperPane starts the helper in a pane in dir; with agent set, the
// server takes it for that agent.
type helperPane struct {
	t    *testing.T
	c    *client.Client
	id   string
	sent int
}

func startHelper(t *testing.T, c *client.Client, dir, agent string) *helperPane {
	t.Helper()
	var info proto.PaneInfo
	err := c.Call(t.Context(), proto.MethodPaneCreate, proto.PaneCreateParams{Agent: agent,
		Command: []string{os.Args[0], "-test.run=^TestScopeHelper$"}, Env: []string{"CONCH_SCOPE_HELPER=1"},
		Cwd: dir, Cols: 120, Rows: 40}, &info)
	if err != nil {
		t.Fatal(err)
	}
	h := &helperPane{t: t, c: c, id: info.ID}
	if agent != "" {
		h.until("the agent detected", func(p proto.PaneInfo) bool { return p.Agent != nil })
	}
	return h
}

func (h *helperPane) until(what string, ok func(proto.PaneInfo) bool) proto.PaneInfo {
	h.t.Helper()
	for i := 0; ; i++ {
		var list proto.PaneList
		h.c.Call(h.t.Context(), proto.MethodPaneList, nil, &list)
		for _, p := range list.Panes {
			if p.ID == h.id && ok(p) {
				return p
			}
		}
		if i == 500 {
			h.t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// do has the helper make one call and returns what it printed: the code,
// and the ID of a pane it made.
func (h *helperPane) do(method, arg string) (code, id string) {
	h.t.Helper()
	h.sent++
	n := h.sent
	if err := h.c.Call(h.t.Context(), proto.MethodPaneSendText, proto.PaneSendTextParams{ID: h.id, Text: fmt.Sprintf("%d %s %s\r", n, method, arg)}, nil); err != nil {
		h.t.Fatal(err)
	}
	re := regexp.MustCompile(fmt.Sprintf(`=> %d (\S+) ?(\S*)`, n))
	for i := 0; ; i++ {
		var screen proto.PaneReadResult
		h.c.Call(h.t.Context(), proto.MethodPaneRead, proto.PaneRef{ID: h.id}, &screen)
		if m := re.FindStringSubmatch(strings.Join(screen.Lines, "\n")); m != nil {
			return m[1], m[2]
		}
		if i == 500 {
			var list proto.PaneList
			h.c.Call(h.t.Context(), proto.MethodPaneList, nil, &list)
			h.t.Fatalf("no answer to %s %s:\n%s\npanes: %+v", method, arg, strings.Join(screen.Lines, "\n"), list.Panes)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// scopeFixture is a server with projects A and B, a pane in each and one
// in neither, all made from outside the panes (the test).
func scopeFixture(t *testing.T) (c *client.Client, a, b string, projA, projB proto.ProjectInfo, inA, inB, loose string) {
	t.Helper()
	a5Env(t)
	c, dir := startServer(t)
	events := a5Collect(c)
	a, b = filepath.Join(dir, "a"), filepath.Join(dir, "b")
	for i, repo := range []string{a, b} {
		os.MkdirAll(repo, 0o755)
		git(t, repo, "init", "-q", "-b", "main")
		git(t, repo, "config", "user.name", "t")
		git(t, repo, "config", "user.email", "t@example.com")
		git(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
		var p proto.ProjectInfo
		if err := c.Call(t.Context(), proto.MethodProjectAdd, proto.ProjectAddParams{Path: repo}, &p); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			projA = p
		} else {
			projB = p
		}
	}
	events.wait(t, "projects loaded", func(proto.Message) bool {
		var list proto.ProjectList
		c.Call(t.Context(), proto.MethodProjectList, nil, &list)
		return len(list.Projects) == 2
	})
	sleep := func(params proto.PaneCreateParams) string {
		params.Command, params.Cols, params.Rows = []string{"/bin/sleep", "60"}, 40, 5
		var info proto.PaneInfo
		if err := c.Call(t.Context(), proto.MethodPaneCreate, params, &info); err != nil {
			t.Fatal(err)
		}
		return info.ID
	}
	inA, inB, loose = sleep(proto.PaneCreateParams{Cwd: a}), sleep(proto.PaneCreateParams{Cwd: b}), sleep(proto.PaneCreateParams{NoProject: true})
	return
}

// An agent in project A, calling from inside its pane: what it may change
// and what it may not.
func TestScopeAgentInAPane(t *testing.T) {
	c, a, b, projA, projB, inA, inB, loose := scopeFixture(t)
	h := startHelper(t, c, a, "claude")

	for _, step := range []struct{ method, arg, want string }{
		{proto.MethodPaneSendText, h.id, "ok"},                             // itself
		{proto.MethodPaneSendText, inA, "ok"},                              // its project
		{proto.MethodPaneSendText, inB, proto.ErrOutOfScope},               // another project
		{proto.MethodPaneClose, loose, proto.ErrOutOfScope},                // in no project
		{proto.MethodPaneMarkSeen, inB, proto.ErrOutOfScope},               // hiding another's work
		{proto.MethodPaneRename, inB, proto.ErrOutOfScope},                 //
		{proto.MethodAgentBroadcast, inA + "," + inB, proto.ErrOutOfScope}, // one out is all out
		{proto.MethodPaneClose, "p999", proto.ErrNotFound},                 // nothing there: its own error
		{proto.MethodServerStop, "", proto.ErrOutOfScope},
		{proto.MethodServerReload, "", proto.ErrOutOfScope},
		{proto.MethodProjectRemove, projB.ID, proto.ErrOutOfScope},
		{proto.MethodBranchDiscard, projB.ID, proto.ErrOutOfScope},
		{proto.MethodProjectRemove, "r404", proto.ErrNotFound},
	} {
		if code, _ := h.do(step.method, step.arg); code != step.want {
			t.Errorf("%s %s: %s, want %s", step.method, step.arg, code, step.want)
		}
	}
	// Its own project's branches are its work; this branch just isn't there.
	if code, _ := h.do(proto.MethodBranchDiscard, projA.ID); code == proto.ErrOutOfScope || code == "ok" {
		t.Errorf("discard in its own project: %s", code)
	}

	// What it starts is its own, in any project; it is recorded as such.
	code, made := h.do(proto.MethodPaneCreate, b)
	if code != "ok" || made == "" {
		t.Fatalf("create: %s %s", code, made)
	}
	var list proto.PaneList
	c.Call(t.Context(), proto.MethodPaneList, nil, &list)
	for _, p := range list.Panes {
		want := ""
		if p.ID == made {
			want = h.id
		}
		if p.CreatedBy != want {
			t.Errorf("%s created by %q, want %q", p.ID, p.CreatedBy, want)
		}
	}
	if code, _ := h.do(proto.MethodPaneClose, made); code != "ok" {
		t.Errorf("close what it started: %s", code)
	}

	// Nothing refused was done: B's pane and the loose one still run, and
	// the server is up.
	c.Call(t.Context(), proto.MethodPaneList, nil, &list)
	running := map[string]bool{}
	for _, p := range list.Panes {
		running[p.ID] = p.State == proto.PaneRunning
	}
	if !running[inB] || !running[loose] || !running[inA] || running[made] {
		t.Fatalf("after: %v", running)
	}
	var screen proto.PaneReadResult
	c.Call(t.Context(), proto.MethodPaneRead, proto.PaneRef{ID: inB}, &screen)
	if strings.Contains(strings.Join(screen.Lines, "\n"), "from-helper") {
		t.Fatal("typed into another project's pane")
	}
}

// A terminal pane is the person's own: nothing it asks is scoped, and
// panes it starts have no creator.
func TestScopeTerminalPaneIsUnscoped(t *testing.T) {
	c, a, _, _, projB, _, inB, _ := scopeFixture(t)
	h := startHelper(t, c, a, "")
	time.Sleep(3 * detectTicks) // the server has looked, and found no agent
	if p := h.until("running", func(p proto.PaneInfo) bool { return p.State == proto.PaneRunning }); p.Agent != nil {
		t.Fatalf("taken for an agent: %+v", p.Agent)
	}
	if code, _ := h.do(proto.MethodPaneSendText, inB); code != "ok" {
		t.Fatalf("send: %s", code)
	}
	if code, _ := h.do(proto.MethodBranchDiscard, projB.ID); code == proto.ErrOutOfScope {
		t.Fatalf("discard: %s", code)
	}
	code, made := h.do(proto.MethodPaneCreate, a)
	var list proto.PaneList
	c.Call(t.Context(), proto.MethodPaneList, nil, &list)
	for _, p := range list.Panes {
		if p.ID == made && (code != "ok" || p.CreatedBy != h.id) {
			// A terminal pane records what it starts too: its agent may
			// come later, and the lineage is what reaches it then.
			t.Fatalf("created %s by %q (%s)", made, p.CreatedBy, code)
		}
	}
}

// The test itself is outside every pane: the TUI and scripts run as the
// person, whatever they ask.
func TestScopeOutsideThePanes(t *testing.T) {
	c, _, _, _, _, _, inB, loose := scopeFixture(t)
	for _, id := range []string{inB, loose} {
		if err := c.Call(t.Context(), proto.MethodPaneClose, proto.PaneRef{ID: id}, nil); err != nil {
			t.Fatalf("close %s: %v", id, err)
		}
	}
}

const detectTicks = 500 * time.Millisecond

// secondServer is another machine's server, for a helper to reach: its own
// socket, a project, and a pane nobody on the first machine started.
func secondServer(t *testing.T) (c *client.Client, sock, repo string, proj proto.ProjectInfo, theirs string) {
	t.Helper()
	c, dir := startServer(t)
	sock = filepath.Join(dir, "s.sock")
	repo = filepath.Join(dir, "gpu")
	os.MkdirAll(repo, 0o755)
	git(t, repo, "init", "-q", "-b", "main")
	git(t, repo, "config", "user.name", "t")
	git(t, repo, "config", "user.email", "t@example.com")
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
	if err := c.Call(t.Context(), proto.MethodProjectAdd, proto.ProjectAddParams{Path: repo}, &proj); err != nil {
		t.Fatal(err)
	}
	var info proto.PaneInfo
	if err := c.Call(t.Context(), proto.MethodPaneCreate, proto.PaneCreateParams{Command: []string{"/bin/sleep", "60"},
		Cwd: repo, Cols: 40, Rows: 5}, &info); err != nil {
		t.Fatal(err)
	}
	return c, sock, repo, proj, info.ID
}

// An agent on this machine reaching another, as `conch -m` does: held
// there to what it starts there.
func TestScopeAcrossMachines(t *testing.T) {
	c, a, _, _, _, _, _, _ := scopeFixture(t)
	far, sock, repo, proj, theirs := secondServer(t)
	h := startHelper(t, c, a, "claude")

	code, id := h.do(proto.MethodPaneCaller, "")
	host, _ := os.Hostname()
	if code != "ok" || !regexp.MustCompile("^"+regexp.QuoteMeta(host)+"/"+h.id+`@\d+$`).MatchString(id) {
		t.Fatalf("who it is: %s %q", code, id)
	}
	if code, scoped := h.do("reach", sock); code != "ok" || scoped != "true" {
		t.Fatalf("reach: %s %s", code, scoped)
	}
	for _, step := range []struct{ method, arg, want string }{
		{proto.MethodPaneClose, theirs, proto.ErrOutOfScope},
		{proto.MethodPaneSendText, theirs, proto.ErrOutOfScope},
		{proto.MethodBranchDiscard, proj.ID, proto.ErrOutOfScope}, // it has started nothing there yet
		{proto.MethodServerStop, "", proto.ErrOutOfScope},
	} {
		if code, _ := h.do(step.method, step.arg); code != step.want {
			t.Errorf("%s %s there: %s, want %s", step.method, step.arg, code, step.want)
		}
	}
	code, made := h.do(proto.MethodPaneCreate, repo)
	if code != "ok" {
		t.Fatalf("create there: %s", code)
	}
	var list proto.PaneList
	far.Call(t.Context(), proto.MethodPaneList, nil, &list)
	for _, p := range list.Panes {
		if p.ID == made && p.CreatedBy != id {
			t.Fatalf("created there by %q, want %q", p.CreatedBy, id)
		}
	}
	// Its own pane there is its own, and so now is that pane's project.
	if code, _ := h.do(proto.MethodPaneSendText, made); code != "ok" {
		t.Errorf("type into its own there: %s", code)
	}
	if code, _ := h.do(proto.MethodBranchDiscard, proj.ID); code == proto.ErrOutOfScope {
		t.Errorf("discard in the project it works in there: %s", code)
	}
	if code, _ := h.do(proto.MethodPaneClose, made); code != "ok" {
		t.Errorf("close its own there: %s", code)
	}
	// Nothing refused was done there.
	far.Call(t.Context(), proto.MethodPaneList, nil, &list)
	for _, p := range list.Panes {
		if p.ID == theirs && p.State != proto.PaneRunning {
			t.Fatalf("their pane: %+v", p)
		}
	}
}

// A terminal pane reaching another machine carries nothing: it is you.
func TestScopeAcrossMachinesFromATerminal(t *testing.T) {
	c, a, _, _, _, _, _, _ := scopeFixture(t)
	_, sock, _, _, theirs := secondServer(t)
	h := startHelper(t, c, a, "")
	time.Sleep(3 * detectTicks)
	if code, scoped := h.do("reach", sock); code != "ok" || scoped != "false" {
		t.Fatalf("reach: %s %s", code, scoped)
	}
	if code, _ := h.do(proto.MethodPaneClose, theirs); code != "ok" {
		t.Fatalf("close there: %s", code)
	}
}

// An agent pane declaring itself to its own server: it is already scoped
// as its pane there, and the declaration is refused rather than let it
// trade its pane's scope for another.
func TestScopeActForFromOwnPane(t *testing.T) {
	c, a, _, _, _, _, _, _ := scopeFixture(t)
	h := startHelper(t, c, a, "claude")
	sock := filepath.Join(filepath.Dir(a), "s.sock")
	if code, _ := h.do("reach", sock); code != proto.ErrBadRequest {
		t.Fatalf("declared to its own server: %s", code)
	}
}

package report

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

func fixedNow(t *testing.T) time.Time {
	t.Helper()
	now := time.Date(2026, 10, 4, 21, 0, 0, 0, time.UTC)
	old := Now
	Now = func() time.Time { return now }
	t.Cleanup(func() { Now = old })
	return now
}

func full(now time.Time) Facts {
	return Facts{
		Version: "0.1.7-dev", Build: "29945acfb71a", Platform: "darwin/arm64",
		Server: Server{Version: "0.1.7-dev", Build: "29945acfb71a", Platform: "darwin/arm64",
			PID: 28356, Started: now.Add(-134 * time.Hour), Panes: 31},
		Terminal: Terminal{Program: "iTerm.app", Term: "xterm-256color", Cols: 247, Rows: 48, Icons: "text"},
		Agents:   map[string]int{"claude": 12, "codex": 1},
		Machines: 2, MachinesOnline: 1,
		LastError: "out_of_scope: the claude agent in p1 may not reload the server",
	}
}

// TestTextIsFactsOnly: the rule the package exists for. A report carries
// versions, counts, sizes and conch's own words — never a path, a project,
// a branch, a pane's title or anything off a screen. It is what makes one
// keystroke safe: nothing has to be read through before it is sent.
func TestTextIsFactsOnly(t *testing.T) {
	now := fixedNow(t)
	f := full(now)
	// Everything a caller might wrongly hand over, offered and refused by
	// the shape of Facts: there is nowhere to put it.
	got := f.Text()
	for _, leak := range []string{"/Users/", "/home/", "~/", "api.worktrees", "feat/", "review it"} {
		if strings.Contains(got, leak) {
			t.Errorf("the report carries %q:\n%s", leak, got)
		}
	}
	for _, want := range []string{
		"conch 0.1.7-dev (build 29945acfb71a, darwin/arm64)",
		"server: 0.1.7-dev (build 29945acfb71a, darwin/arm64), pid 28356, up 5d",
		"panes: 31 · claude×12, codex",
		"machines: 2, 1 reachable",
		"terminal: iTerm.app, xterm-256color, 247×48, icons=text",
		"last error: out_of_scope: the claude agent in p1 may not reload the server",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the report does not say %q:\n%s", want, got)
		}
	}
	// Two reports of the same thing read the same: the agents are sorted,
	// not in map order.
	for range 20 {
		if f.Text() != got {
			t.Fatal("two reports of one conch differ")
		}
	}
}

// TestTextWithoutAServer: the report somebody makes when conch will not
// start is the one that matters most, so nothing may depend on a server.
func TestTextWithoutAServer(t *testing.T) {
	fixedNow(t)
	got := Facts{Version: "0.1.7-dev", Platform: "linux/amd64"}.Text()
	if !strings.Contains(got, "server: not running, or not reached") {
		t.Fatalf("no server:\n%s", got)
	}
	if !strings.Contains(got, "panes: 0") {
		t.Errorf("panes:\n%s", got)
	}
	// Nothing invented: no machines line, no terminal line, no error line.
	for _, absent := range []string{"machines:", "terminal:", "last error:", "reloaded"} {
		if strings.Contains(got, absent) {
			t.Errorf("it made up %q:\n%s", absent, got)
		}
	}
	// And an empty conch still says which conch it is.
	if bare := (Facts{}).Text(); !strings.Contains(bare, "conch unknown version") {
		t.Errorf("a report from nothing at all:\n%s", bare)
	}
}

// TestReloadAndMissingCapabilities: a stale server is the commonest thing a
// report has to show, so a reload and the capabilities it lacks are in it.
func TestReloadAndMissingCapabilities(t *testing.T) {
	now := fixedNow(t)
	f := full(now)
	f.Server.LoadedAt = now.Add(-90 * time.Minute)
	f.Server.Missing = []string{"agent.prompt.v1", "worktree.watch.v1"}
	got := f.Text()
	if !strings.Contains(got, "reloaded 1h30m ago") {
		t.Errorf("the reload:\n%s", got)
	}
	if !strings.Contains(got, "missing: agent.prompt.v1 worktree.watch.v1") {
		t.Errorf("what the server lacks:\n%s", got)
	}
	// A server that has never reloaded says nothing about it.
	f.Server.LoadedAt = f.Server.Started
	if strings.Contains(f.Text(), "reloaded") {
		t.Error("a server that never reloaded says it did")
	}
}

// TestLastErrorIsOneLine: it goes in a URL, and a wrapped error reads as
// several facts.
func TestLastErrorIsOneLine(t *testing.T) {
	fixedNow(t)
	f := Facts{Version: "x", LastError: "  first line\nsecond line\nthird  "}
	if got := f.Text(); !strings.Contains(got, "last error: first line\n") || strings.Contains(got, "second line") {
		t.Fatalf("a multi-line error:\n%s", got)
	}
	f.LastError = strings.Repeat("a", 400)
	line := ""
	for _, l := range strings.Split(f.Text(), "\n") {
		if strings.HasPrefix(l, "last error:") {
			line = l
		}
	}
	if len([]rune(line)) > 320 || !strings.HasSuffix(line, "…") {
		t.Fatalf("a long error is %d runes: %q", len([]rune(line)), line)
	}
}

// TestIssueURL: one keystroke ends in a browser with the words visible, not
// in a request conch made. The link carries them, and a browser has to be
// able to open it.
func TestIssueURL(t *testing.T) {
	now := fixedNow(t)
	f := full(now)
	f.What = "A terminal moved into a folder by itself after I closed its namesake."
	link := f.IssueURL("folders: a terminal moved into a folder by itself")
	if !strings.HasPrefix(link, Repo+"/issues/new?") {
		t.Fatalf("link %q", link)
	}
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("title") != "folders: a terminal moved into a folder by itself" {
		t.Errorf("title %q", q.Get("title"))
	}
	body := q.Get("body")
	if !strings.Contains(body, f.What) || !strings.Contains(body, "panes: 31") {
		t.Errorf("body:\n%s", body)
	}
	if !strings.Contains(body, "```") {
		t.Error("the facts are not fenced, so GitHub will reflow them")
	}
	// A link a browser will refuse is a report nobody can make.
	if len(link) > 8000 {
		t.Fatalf("the link is %d bytes", len(link))
	}

	// Nothing typed: the body says what to write, so an empty issue is
	// still a usable one.
	f.What = ""
	if body := mustQuery(t, f.IssueURL("")).Get("body"); !strings.Contains(body, "What happened") {
		t.Errorf("an empty report says nothing to the person:\n%s", body)
	}
	if title := mustQuery(t, f.IssueURL("")).Get("title"); title != "" {
		t.Errorf("it invented a title %q", title)
	}
}

// TestIssueURLStaysOpenable: somebody pastes a screenful into the box, and
// the link still has to open. The facts are what must survive.
func TestIssueURLStaysOpenable(t *testing.T) {
	fixedNow(t)
	f := full(Now())
	f.What = strings.Repeat("it broke and here is a great deal about it. ", 500)
	link := f.IssueURL("a long one")
	if len(link) > 8000 {
		t.Fatalf("the link is %d bytes", len(link))
	}
	body := mustQuery(t, link).Get("body")
	if !strings.Contains(body, "panes: 31") {
		t.Fatalf("the facts were the part that got cut:\n%s", body)
	}
	if strings.Count(body, "```")%2 != 0 {
		t.Errorf("the fence was cut in half:\n%s", body)
	}
}

func mustQuery(t *testing.T, link string) url.Values {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query()
}

// TestSince: a report wants the order of magnitude, not seconds, and a
// clock that has gone backwards must not print a negative age.
func TestSince(t *testing.T) {
	now := fixedNow(t)
	for _, c := range []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "0m"},
		{90 * time.Second, "1m"},
		{59 * time.Minute, "59m"},
		{90 * time.Minute, "1h30m"},
		{47 * time.Hour, "47h00m"},
		{134 * time.Hour, "5d"},
		{-time.Hour, "0m"}, // a clock that moved
	} {
		if got := since(now.Add(-c.d)); got != c.want {
			t.Errorf("%v ago reads %q, want %q", c.d, got, c.want)
		}
	}
}

// TestTerminalLine: what is drawn on, and nothing when there is no terminal
// — a report from a command is not a report from the TUI.
func TestTerminalLine(t *testing.T) {
	if got := (Terminal{}).line(); got != "" {
		t.Errorf("no terminal gave %q", got)
	}
	got := Terminal{Term: "screen-256color", Cols: 80, Rows: 24, Tmux: true, SSH: true}.line()
	if got != "screen-256color, 80×24, tmux, over ssh" {
		t.Errorf("terminal %q", got)
	}
}

// TestAgentsPart: the agents running, counted, in a fixed order; none at
// all says nothing rather than an empty list.
func TestAgentsPart(t *testing.T) {
	if got := agentsPart(nil); got != "" {
		t.Errorf("no agents gave %q", got)
	}
	if got := agentsPart(map[string]int{"claude": 0}); got != "" {
		t.Errorf("a count of zero gave %q", got)
	}
	if got := agentsPart(map[string]int{"codex": 1, "claude": 2}); got != " · claude×2, codex" {
		t.Errorf("agents %q", got)
	}
}

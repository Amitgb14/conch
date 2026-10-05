package main

import (
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/report"
)

// TestA4Bug: the command somebody runs when conch itself is the problem.
// It must work with no server at all — that is the report that matters
// most — and it must never send anything by itself.
func TestA4Bug(t *testing.T) {
	a4Env(t)
	t.Setenv("TERM_PROGRAM", "iTerm.app")
	t.Setenv("TERM", "xterm-256color")

	// No server: said plainly, and the binary's own facts still stand.
	out, _ := a4Capture(t, "", func() {
		if err := runBug(nil); err != nil {
			t.Fatalf("with no server: %v", err)
		}
	})
	for _, want := range []string{"conch " + proto.Version, "server: not running, or not reached",
		"terminal: iTerm.app, xterm-256color", "conch bug -open"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not say %q:\n%s", want, out)
		}
	}

	// With one: its version, pid and panes, and what it lacks.
	srv := startA4Server(t, config.SocketPath())
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		if msg.Method == proto.MethodPaneList {
			return proto.PaneList{Panes: []proto.PaneInfo{
				{ID: "p1", State: proto.PaneRunning, Agent: &proto.AgentStatus{Name: "claude"}},
				{ID: "p2", State: proto.PaneRunning, Agent: &proto.AgentStatus{Name: "claude"}},
				{ID: "p3", State: proto.PaneRunning},
				{ID: "p4", State: proto.PaneExited}, // an ended pane is not a running one
			}}, nil
		}
		return nil, nil
	})
	out, _ = a4Capture(t, "", func() {
		if err := runBug([]string{"the tree lost a terminal"}); err != nil {
			t.Fatalf("with a server: %v", err)
		}
	})
	if !strings.Contains(out, "pid 1000") || !strings.Contains(out, "panes: 3 · claude×2") {
		t.Errorf("the server's facts:\n%s", out)
	}

	// -log says where the log is and what is in it, and does not print it:
	// it holds paths, branch names and pane titles.
	out, _ = a4Capture(t, "", func() { _ = runBug([]string{"-log"}) })
	if !strings.Contains(out, config.ServerLogPath()) || !strings.Contains(out, "read it before you attach it") {
		t.Errorf("-log:\n%s", out)
	}

	// -open ends in a browser with the words visible. Nothing is sent by
	// conch, so what is checked is the link, not a request.
	opened := ""
	old := openInBrowser
	openInBrowser = func(url string) error { opened = url; return nil }
	t.Cleanup(func() { openInBrowser = old })
	out, _ = a4Capture(t, "", func() { _ = runBug([]string{"-open", "-title", "folders: a terminal moved by itself"}) })
	if !strings.HasPrefix(opened, report.Repo+"/issues/new?") {
		t.Fatalf("it opened %q", opened)
	}
	u, err := url.Parse(opened)
	if err != nil {
		t.Fatal(err)
	}
	if got := u.Query().Get("title"); got != "folders: a terminal moved by itself" {
		t.Errorf("title %q", got)
	}
	if !strings.Contains(u.Query().Get("body"), "pid 1000") {
		t.Errorf("body:\n%s", u.Query().Get("body"))
	}
	if !strings.Contains(out, "nothing is sent until you submit it") {
		t.Errorf("it does not say nothing was sent:\n%s", out)
	}

	// A desktop that will not open one hands over the link instead of
	// losing the report.
	openInBrowser = func(string) error { return errors.New("no display") }
	out, _ = a4Capture(t, "", func() {
		if err := runBug([]string{"-open"}); err == nil {
			t.Error("a browser that failed was reported as success")
		}
	})
	if !strings.Contains(out, report.Repo+"/issues/new?") {
		t.Errorf("the link was not printed:\n%s", out)
	}
}

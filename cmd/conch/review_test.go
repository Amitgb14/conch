package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

// reviewServer is a fake server keeping review comments as the real one
// does, for a worktree of branch feat.
func reviewServer(t *testing.T) (*a4Server, string, *a4Var[[]proto.Message]) {
	t.Helper()
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	wt := filepath.Join(root, "api.worktrees", "feat")
	os.MkdirAll(wt, 0o755)
	seen := newA4Var([]proto.Message(nil))
	var comments []proto.ReviewComment
	n := 0
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		seen.Set(append(seen.Get(), msg))
		switch msg.Method {
		case proto.MethodProjectResolve:
			return proto.ProjectPlace{ProjectID: "api", Root: root, Git: true, Base: "main", Worktree: wt, Branch: "feat"}, nil
		case proto.MethodReviewList:
			return proto.ReviewList{ProjectID: "api", Branch: "feat", Comments: comments}, nil
		case proto.MethodReviewSet:
			var rc proto.ReviewComment
			json.Unmarshal(msg.Params, &rc)
			if rc.ID != "" {
				for i, c := range comments {
					if c.ID == rc.ID {
						comments = append(comments[:i], comments[i+1:]...)
						return proto.ReviewList{Comments: comments}, nil
					}
				}
				return nil, proto.Errorf(proto.ErrNotFound, "no review comment %s; it may have been sent already", rc.ID)
			}
			n++
			rc.ID = "c" + strings.Repeat("0", 7) + string(rune('0'+n))
			comments = append(comments, rc)
			return proto.ReviewList{Comments: comments}, nil
		case proto.MethodReviewSend:
			var sp proto.ReviewSendParams
			json.Unmarshal(msg.Params, &sp)
			res := proto.ReviewSendResult{Pane: proto.PaneInfo{ID: "p3"}, Sent: len(comments)}
			switch {
			case len(comments) == 0:
				return nil, proto.Errorf(proto.ErrBadRequest, "no review comments on feat to send")
			case sp.Agent == "" && n < 3:
				return nil, proto.Errorf(proto.ErrNotFound, "no agent is working on feat; name one to start with the comments")
			case sp.Agent != "":
				res.Started, res.Pane.ID = true, "p4"
			}
			comments = nil
			return res, nil
		}
		return nil, proto.Errorf(proto.ErrUnknown, "?")
	})
	return srv, wt, seen
}

func TestReviewCommand(t *testing.T) {
	_, wt, seen := reviewServer(t)
	t.Chdir(wt)
	run := func(args ...string) (string, error) {
		t.Helper()
		out, _, err := a4Out(t, func() error { return runReview(args) })
		return out, err
	}

	if out, err := run(); err != nil || out != "no review comments on feat\n" {
		t.Fatalf("empty: %q %v", out, err)
	}
	// A line, then a whole file, the note taken from every word after it.
	if out, err := run("add", "api/model.go:12", "  call", "it Model "); err != nil || out != "1 comment on feat · conch review send hands them to its agent\n" {
		t.Fatalf("add: %q %v", out, err)
	}
	msgs := seen.Get()
	var rc proto.ReviewComment
	json.Unmarshal(msgs[len(msgs)-1].Params, &rc)
	if rc.ProjectID != "api" || rc.Branch != "feat" || rc.File != "api/model.go" || rc.Line != 12 || rc.Note != "call it Model" {
		t.Fatalf("sent %+v", rc)
	}
	if _, err := run("add", "-branch", "feat", "README.md", "say what it does"); err != nil {
		t.Fatal(err)
	}
	out, err := run("ls")
	want := "c00000001  api/model.go:12\n    call it Model\nc00000002  README.md\n    say what it does\nconch review send hands 2 comments to the agent on feat\n"
	if err != nil || out != want {
		t.Fatalf("ls:\n%s\nwant:\n%s (%v)", out, want, err)
	}

	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"add", "a.go"}, "conch review add FILE[:LINE] NOTE"},
		{[]string{"add", "a.go", "  "}, "an empty comment says nothing"},
		{[]string{"add", "a.go:0", "x"}, "lines count from 1"},
		{[]string{"rm"}, "conch review rm ID"},
		{[]string{"rm", "c99"}, "no review comment c99"},
		{[]string{"send", "extra"}, "takes no arguments"},
		{[]string{"frob"}, `unknown review subcommand "frob"; conch review -h lists them`},
		{[]string{"-branch", "feat", "add", "a.go", "x"}, "the command goes before the flags: conch review add -branch B"},
		{[]string{"-branch", "feat", "frob"}, `unknown review subcommand "frob"; conch review -h lists them`},
		{[]string{"send"}, "no agent is working on feat; name one to start with the comments: conch review send -agent claude"},
	} {
		if _, err := run(c.args...); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: %v, want %q", c.args, err, c.want)
		}
	}

	if out, err := run("rm", "c00000001"); err != nil || out != "removed; 1 comment left on feat\n" {
		t.Fatalf("rm: %q %v", out, err)
	}
	if out, err := run("send", "-agent", "codex"); err != nil || out != "started codex in p4 on feat with 1 review comment\n" {
		t.Fatalf("send, starting: %q %v", out, err)
	}
	run("add", "a.go", "one")
	run("add", "b.go", "two")
	if out, err := run("send"); err != nil || out != "sent 2 review comments to p3 on feat\n" {
		t.Fatalf("send: %q %v", out, err)
	}
}

// The name of a file with a colon of its own is kept whole.
func TestSplitFileLine(t *testing.T) {
	for _, c := range []struct {
		in   string
		file string
		line int
	}{
		{"a.go", "a.go", 0},
		{"a.go:7", "a.go", 7},
		{"C:notes", "C:notes", 0},
		{"time:12:30.txt", "time:12:30.txt", 0},
		{"log:2:9", "log:2", 9},
	} {
		file, line, err := splitFileLine(c.in)
		if err != nil || file != c.file || line != c.line {
			t.Errorf("%q: %q %d %v", c.in, file, line, err)
		}
	}
	for _, bad := range []string{"", "a.go:-1", "a.go:0"} {
		if _, _, err := splitFileLine(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// A server from before review comments says so rather than failing a call.
func TestReviewCommandOldServer(t *testing.T) {
	srv, wt, _ := reviewServer(t)
	t.Chdir(wt)
	srv.setHello(func(n int) proto.HelloResult {
		h := currentHello(n)
		var kept []string
		for _, c := range h.Capabilities {
			if c != proto.CapReviewComments {
				kept = append(kept, c)
			}
		}
		h.Capabilities = kept
		return h
	})
	if _, _, err := a4Out(t, func() error { return runReview(nil) }); err == nil || !strings.Contains(err.Error(), "predates review comments") {
		t.Fatalf("old server: %v", err)
	}
}

// A pull request's comments are listed with who wrote them on GitHub, the
// thread's replies and where to answer them; the server refuses removing
// one, and says why.
func TestReviewCommandGitHub(t *testing.T) {
	srv, wt, _ := reviewServer(t)
	t.Chdir(wt)
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		switch msg.Method {
		case proto.MethodProjectResolve:
			return proto.ProjectPlace{ProjectID: "api", Git: true, Base: "main", Worktree: wt, Branch: "feat"}, nil
		case proto.MethodReviewList:
			return proto.ReviewList{ProjectID: "api", Branch: "feat", Comments: []proto.ReviewComment{
				{ID: "c1", File: "a.go", Line: 3, Note: "mine"},
				{ID: "gh101", File: "model.go", Line: 12, Hunk: "@@ -8 +8 @@", Note: "this leaks", By: "alice", Source: proto.ReviewGitHub,
					URL: "https://github.com/o/r/pull/5#discussion_r101", Replies: []proto.ReviewReply{{By: "bob", Note: "and\nclose it"}, {Note: "?"}}},
				{ID: "gh300", File: "old.go", Line: 7, Note: "rename", By: "bob", Source: proto.ReviewGitHub, Outdated: true},
				{ID: "ghr7", Note: "Needs tests.", By: "alice", Source: proto.ReviewGitHub},
			}}, nil
		case proto.MethodReviewSet:
			return nil, proto.Errorf(proto.ErrBadRequest, "review comment gh101 is on GitHub (pull request #5); answer or resolve it there")
		}
		return nil, proto.Errorf(proto.ErrUnknown, "?")
	})
	out, _, err := a4Out(t, func() error { return runReview(nil) })
	want := `c1  a.go:3
    mine
gh101  model.go:12  (alice on GitHub)
    this leaks
    ↳ bob: and
      close it
    ↳ someone: ?
    https://github.com/o/r/pull/5#discussion_r101
gh300  old.go:7  (bob on GitHub, outdated)
    rename
ghr7  the pull request  (alice on GitHub)
    Needs tests.
conch review send hands 4 comments to the agent on feat
`
	if err != nil || out != want {
		t.Fatalf("ls:\n%s\nwant:\n%s (%v)", out, want, err)
	}
	if _, _, err := a4Out(t, func() error { return runReview([]string{"rm", "gh101"}) }); err == nil || !strings.Contains(err.Error(), "answer or resolve it there") {
		t.Fatalf("rm: %v", err)
	}
}

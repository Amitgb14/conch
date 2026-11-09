package gitx

import (
	"os/exec"
	"strings"
	"testing"
)

func TestPrintable(t *testing.T) {
	for in, want := range map[string]string{
		"":                      "",
		"plain subject":         "plain subject",
		"naïve — 日本語 ✓":         "naïve — 日本語 ✓",
		"a\x1b]52;c;aGk=\x07b":  "a·]52;c;aGk=·b",
		"x\u009d2;title\u009cy": "x·2;title·y",
		"\u0080\u009f ":         "·· ", // NBSP is printable
		"del\x7f":               "del·",
		"tab\there":             "tab here",
		"line\nbreak\r":         "line·break·",
		"\x1b[2J\x1b[H":         "·[2J·[H",
		"raw \x9b byte":         "raw � byte", // not UTF-8: no C1 survives
		"\x00":                  "·",
	} {
		if got := Printable(in); got != want {
			t.Errorf("Printable(%q) = %q, want %q", in, got, want)
		}
	}
	if got := PrintableText("one\ntwo\x1b]2;x\x07\n\tthree"); got != "one\ntwo·]2;x·\n three" {
		t.Errorf("PrintableText = %q", got)
	}
}

// hostile is a subject and author that, drawn raw, would write the
// clipboard (OSC 52), retitle the window through a C1 OSC, and clear the
// screen.
const (
	hostileSubject = "fix \x1b]52;c;cHduZWQ=\x07 it \u009d2;owned\u009c \x1b[2J done"
	hostileAuthor  = "Eve \x1b]2;title\x07 \u009b31m"
)

func hostileCommit(t *testing.T) string {
	t.Helper()
	root := newRepo(t)
	write(t, root, "h.txt", "h\n")
	git(t, root, "add", "h.txt")
	cmd := exec.Command("git", "-C", root, "-c", "user.name="+hostileAuthor, "-c", "user.email=eve\x1b]52;c;x\x07@example.com",
		"commit", "-q", "-m", hostileSubject, "-m", "body\x1b]52;c;eA==\x07\nsecond \u0085line")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("commit: %v\n%s", err, out)
	}
	// git must really have kept the bytes, or the test proves nothing.
	if raw := git(t, root, "log", "-1", "--format=%s%an"); !strings.Contains(raw, "\x1b]52") || !strings.Contains(raw, "\u009d") {
		t.Fatalf("git did not keep the controls: %q", raw)
	}
	return root
}

func noControls(t *testing.T, what, s string) {
	t.Helper()
	for _, r := range s {
		if r == '\n' && what == "body" {
			continue
		}
		if unsafeRune(r) {
			t.Errorf("%s keeps control %U: %q", what, r, s)
			return
		}
	}
}

func TestLogCleansControls(t *testing.T) {
	root := hostileCommit(t)
	commits, _, err := Log(ctx, root, 10)
	if err != nil || len(commits) != 2 {
		t.Fatalf("%v %+v", err, commits)
	}
	c := commits[0]
	noControls(t, "subject", c.Subject)
	noControls(t, "author", c.Author)
	if !strings.HasPrefix(c.Subject, "fix ·]52;c;") || !strings.HasSuffix(c.Subject, " done") {
		t.Errorf("subject %q: the text around the controls should stay", c.Subject)
	}
	if commits[1].Subject != "initial" || commits[1].Author != "Tester" {
		t.Errorf("an ordinary commit changed: %+v", commits[1])
	}
}

func TestShowCommitCleansControls(t *testing.T) {
	root := hostileCommit(t)
	d, err := ShowCommit(ctx, root, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	noControls(t, "subject", d.Subject)
	noControls(t, "author", d.Author)
	noControls(t, "email", d.Email)
	noControls(t, "body", d.Body)
	if !strings.Contains(d.Body, "\nsecond ·line") {
		t.Errorf("body %q: its lines should stay", d.Body)
	}
}

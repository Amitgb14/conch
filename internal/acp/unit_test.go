package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNextKey(t *testing.T) {
	cases := []struct {
		in   string
		name string
		text string
		n    int
	}{
		{"\x1b[A", "up", "", 3},
		{"\x1bOB", "down", "", 3},
		{"\x1b", "escape", "", 1},
		{"\r", "enter", "", 1},
		{"\x7f", "backspace", "", 1},
		{"\x03", "ctrl+c", "", 1},
		{"\x04", "ctrl+d", "", 1},
		{"é", "", "é", 2},
		{"\x1b[200~a\rb\x1b[201~x", "paste", "a\rb", 15},
		{"\x1b[200~unfinished", "paste", "unfinished", 16},
		{"\x1b[1;5C", "", "", 6}, // ctrl+right: skipped whole
		{"\x01", "", "", 1},
	}
	for _, c := range cases {
		k, n := nextKey([]byte(c.in))
		if k.name != c.name || k.text != c.text || n != c.n {
			t.Errorf("nextKey(%q) = %+v %d, want %q %q %d", c.in, k, n, c.name, c.text, c.n)
		}
	}
}

func TestSanitize(t *testing.T) {
	got := sanitize("a\x1b]7501;state=blocked\x07b\tc\nd\u009be\xff")
	if strings.ContainsAny(got, "\x1b\x07\u009b") || !strings.Contains(got, "b\tc\nd") || !strings.Contains(got, "�") {
		t.Errorf("sanitize = %q", got)
	}
}

func TestAppName(t *testing.T) {
	for in, want := range map[string]string{"gemini-acp": "gemini-acp", "": "acp", "é!": "acp", strings.Repeat("a", 40): "acp", "a b": "ab"} {
		if got := appName(in); got != want {
			t.Errorf("appName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReadLineDropsOverlong(t *testing.T) {
	long := strings.Repeat("x", maxLine+10)
	br := bufio.NewReaderSize(strings.NewReader(long+"\nnext\n"), 64<<10)
	if _, err := readLine(br); !errors.Is(err, errLineTooLong) {
		t.Fatalf("err = %v, want too long", err)
	}
	line, err := readLine(br)
	if err != nil || string(line) != "next\n" {
		t.Fatalf("after it: %q %v", line, err)
	}
	// Exactly at the limit is kept.
	br = bufio.NewReaderSize(strings.NewReader(strings.Repeat("y", maxLine-1)+"\n"), 64<<10)
	if line, err := readLine(br); err != nil || len(line) != maxLine {
		t.Fatalf("at the limit: %d %v", len(line), err)
	}
	// A last line without a newline is still read.
	br = bufio.NewReader(strings.NewReader("tail"))
	if line, err := readLine(br); string(line) != "tail" || err != io.EOF {
		t.Fatalf("tail: %q %v", line, err)
	}
}

// TestConnAnswersWhatItCannotRead drives the connection by hand: a bad
// line gets a parse error back, an unknown method "method not found",
// and a call cut short by the other side ending says so.
func TestConnAnswersWhatItCannotRead(t *testing.T) {
	agentOut, connIn := io.Pipe()
	connOut, agentIn := io.Pipe()
	var problems []string
	c := newConn(agentOut, agentIn, func(ctx context.Context, method string, p json.RawMessage, notify bool) (any, *Error) {
		return nil, &Error{Code: codeMethodNotFound, Message: "method not found: " + method}
	}, func(s string) { problems = append(problems, s) })
	replies := bufio.NewReader(connOut)

	io.WriteString(connIn, "not json\n")
	line, _ := replies.ReadString('\n')
	if !strings.Contains(line, `"code":-32700`) {
		t.Errorf("parse error answer = %s", line)
	}
	io.WriteString(connIn, `{"jsonrpc":"2.0","id":7,"method":"x/y"}`+"\n")
	line, _ = replies.ReadString('\n')
	if !strings.Contains(line, `"id":7`) || !strings.Contains(line, `-32601`) {
		t.Errorf("unknown method answer = %s", line)
	}

	errc := make(chan error, 1)
	go func() { errc <- c.Call(context.Background(), "session/new", struct{}{}, nil) }()
	replies.ReadString('\n') // the call went out
	connIn.Close()
	select {
	case err := <-errc:
		if !errors.Is(err, ErrClosed) {
			t.Errorf("err = %v, want ErrClosed", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a call outlived its connection")
	}
	if err := c.Call(context.Background(), "x", nil, nil); !errors.Is(err, ErrClosed) {
		t.Errorf("after the end: %v", err)
	}
	if len(problems) == 0 {
		t.Error("the bad line was not said")
	}
}

func TestCallHonoursContext(t *testing.T) {
	r, _ := io.Pipe()
	c := newConn(r, io.Discard, nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := c.Call(ctx, "slow", nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v", err)
	}
}

func TestResolve(t *testing.T) {
	root := t.TempDir()
	w, err := newWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	os.Symlink(outside, filepath.Join(root, "out"))
	os.Mkdir(filepath.Join(root, "sub"), 0o755)
	for path, ok := range map[string]bool{
		filepath.Join(root, "a.txt"):                  true,
		filepath.Join(root, "sub", "new", "deep.txt"): true,
		root:                            true,
		filepath.Join(root, "..", "x"):  false,
		filepath.Join(root, "out", "x"): false,
		filepath.Join(root, "out"):      false,
		outside:                         false,
		"relative.txt":                  false,
		"":                              false,
		root + "-sibling/x":             false,
	} {
		_, err := w.resolve(path)
		if (err == nil) != ok {
			t.Errorf("resolve(%q): err = %v, want ok %v", path, err, ok)
		}
	}
}

func TestReadFileLines(t *testing.T) {
	root := t.TempDir()
	w, _ := newWorkspace(root)
	p := filepath.Join(root, "f")
	os.WriteFile(p, []byte("1\n2\n3\n4\n"), 0o644)
	got, rerr := w.readFile(ReadFileParams{Path: p, Line: 2, Limit: 2})
	if rerr != nil || got.Content != "2\n3\n" {
		t.Errorf("lines 2-3 = %q %v", got.Content, rerr)
	}
	got, _ = w.readFile(ReadFileParams{Path: p, Line: 99})
	if got.Content != "" {
		t.Errorf("past the end = %q", got.Content)
	}
	if _, rerr := w.readFile(ReadFileParams{Path: filepath.Join(root, "missing")}); rerr == nil {
		t.Error("read a missing file")
	}
}

func TestTerminalKeepsTheEnd(t *testing.T) {
	root := t.TempDir()
	w, _ := newWorkspace(root)
	defer w.closeAll()
	res, rerr := w.createTerminal(TermCreateParams{Command: "printf 'aaaaéééé'", OutputByteLimit: 6})
	if rerr != nil {
		t.Fatal(rerr)
	}
	st, rerr := w.wait(res.TerminalID, nil)
	if rerr != nil || st.ExitCode == nil || *st.ExitCode != 0 {
		t.Fatalf("exit = %+v %v", st, rerr)
	}
	out, _ := w.output(res.TerminalID)
	if !out.Truncated || out.Output != "ééé" || out.ExitStatus == nil {
		t.Errorf("output = %+v", out)
	}
	// Killed: a signal, not a code.
	res, _ = w.createTerminal(TermCreateParams{Command: "sleep", Args: []string{"30"}})
	w.killTerminal(res.TerminalID)
	st, _ = w.wait(res.TerminalID, nil)
	if st.Signal == nil {
		t.Errorf("killed: %+v", st)
	}
	if rerr := w.release(res.TerminalID); rerr != nil {
		t.Error(rerr)
	}
	if _, rerr := w.output(res.TerminalID); rerr == nil {
		t.Error("a released terminal still answers")
	}
	if _, rerr := w.createTerminal(TermCreateParams{Command: "pwd", Cwd: "/"}); rerr == nil {
		t.Error("ran outside the folder")
	}
	if _, rerr := w.createTerminal(TermCreateParams{Command: " "}); rerr == nil {
		t.Error("ran nothing")
	}
}

func TestDiffText(t *testing.T) {
	d := diffText("/x/f.go", "a\nb\nc\n", "a\nB\nc\n", false)
	if !strings.Contains(d, "-1") || !strings.Contains(d, "+1") || !strings.Contains(d, "- b") || !strings.Contains(d, "+ B") || strings.Contains(d, "- a") {
		t.Errorf("diff = %q", d)
	}
	var many strings.Builder
	for i := range 100 {
		many.WriteString(strings.Repeat("z", i%3+1) + "\n")
	}
	if d := diffText("/x/g", "", many.String(), true); !strings.Contains(d, "new file") || !strings.Contains(d, "60 more lines") {
		t.Errorf("long diff = %q", d)
	}
}

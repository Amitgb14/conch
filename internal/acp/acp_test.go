package acp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/detect"
	"github.com/Amitgb14/conch/internal/phone"
	"github.com/Amitgb14/conch/internal/proto"
)

// fakeAgent is internal/tools/acpfake, built once for the package.
var fakeAgent string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "acpfake")
	if err != nil {
		panic(err)
	}
	fakeAgent = filepath.Join(dir, "acpfake")
	_, file, _, _ := runtime.Caller(0)
	src := filepath.Join(filepath.Dir(file), "..", "tools", "acpfake")
	cmd := exec.Command("go", "build", "-o", fakeAgent, ".")
	cmd.Dir = src
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build acpfake: %v\n%s", err, out)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// screen is what conch acp wrote, safe to read while it writes.
type screen struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *screen) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *screen) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// text is the output without escapes.
func (s *screen) text() string { return ansi.Strip(s.String()) }

type run struct {
	t    *testing.T
	out  *screen
	in   *io.PipeWriter
	done chan error
	dir  string // the agent's folder
	data string // ACPFAKE_DIR
}

type runOpts struct {
	mode, prompt, load, data string
}

func start(t *testing.T, o runOpts) *run {
	t.Helper()
	dir := t.TempDir()
	if o.data == "" {
		o.data = t.TempDir()
	}
	env := append(os.Environ(), "ACPFAKE_MODE="+o.mode, "ACPFAKE_DIR="+o.data)
	inR, inW := io.Pipe()
	r := &run{t: t, out: &screen{}, in: inW, done: make(chan error, 1), dir: dir, data: o.data}
	end := make(chan struct{})
	go func() {
		defer close(end)
		r.done <- Run(context.Background(), RunOptions{Agent: "fake", Label: "Fake", Argv: []string{fakeAgent},
			Env: env, Dir: dir, Prompt: o.prompt, Load: o.load, In: inR, Out: r.out})
	}()
	t.Cleanup(func() {
		inW.Close()
		r.send("\x04")
		select {
		case <-end:
		case <-time.After(10 * time.Second):
		}
	})
	return r
}

func (r *run) waitFor(want string) {
	r.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(r.out.text(), want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.t.Fatalf("never saw %q in:\n%s", want, r.out.text())
}

func (r *run) waitStatus(want string) {
	r.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		st := statuses(r.out.String())
		if len(st) > 0 && strings.HasPrefix(st[len(st)-1], want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.t.Fatalf("last status never %q: %q", want, statuses(r.out.String()))
}

func (r *run) send(s string) {
	r.t.Helper()
	_, _ = io.WriteString(r.in, s) // after the end, nobody reads
}

func (r *run) quit() error {
	r.t.Helper()
	r.send("\x04")
	select {
	case err := <-r.done:
		return err
	case <-time.After(10 * time.Second):
		r.t.Fatalf("conch acp did not end:\n%s", r.out.text())
	}
	return nil
}

var oscRe = regexp.MustCompile(`\x1b\]7501;([^\x1b]*)\x1b\\`)

// statuses are the OSC 7501 bodies written, in order.
func statuses(s string) []string {
	var out []string
	for _, m := range oscRe.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}

func TestPromptStreamsAndEnds(t *testing.T) {
	r := start(t, runOpts{prompt: "hello"})
	r.waitFor("Hello, world")
	r.waitStatus("state=done")
	if err := r.quit(); err != nil {
		t.Fatal(err)
	}
	st := statuses(r.out.String())
	if len(st) < 2 || !strings.HasPrefix(st[0], "state=working:app=fake") {
		t.Errorf("statuses = %q, want working first", st)
	}
}

func TestTypedPrompt(t *testing.T) {
	r := start(t, runOpts{})
	r.waitStatus("state=idle")
	r.send("what is up")
	r.waitFor("› what is up")
	r.send("\r")
	r.waitFor("you said: what is up")
	r.waitStatus("state=done")
	// Typed while it works: queued, sent when the turn ends.
	r.send("slow\r")
	r.waitFor("working")
	r.send("second\r")
	r.send("\x1b") // stop the slow one; the queue goes with it
	r.waitFor("(stopped)")
	r.waitStatus("state=idle")
	if strings.Contains(r.out.text(), "you said: second") {
		t.Error("a prompt queued before a stop was sent after it")
	}
	r.quit()
}

// TestStatesFromProtocolOnly feeds what conch acp reported to detect's
// tracker, with a manifest that has no screen rules: every state comes
// from the protocol.
func TestStatesFromProtocolOnly(t *testing.T) {
	r := start(t, runOpts{prompt: "perm"})
	r.waitFor("❯ 1. Allow once")
	r.waitStatus("state=blocked")
	r.send("1")
	r.waitFor("chose:allow")
	r.waitStatus("state=done")
	r.quit()

	m := map[string]*detect.Manifest{"fake": {Agent: "fake", ScreenLines: 30}}
	tr := detect.NewTracker(m, "fake")
	now := time.Now()
	var seen []string
	for i, body := range statuses(r.out.String()) {
		rep := &detect.ProgramReport{At: now.Add(time.Duration(i) * time.Second)}
		for _, kv := range strings.Split(body, ":") {
			k, v, _ := strings.Cut(kv, "=")
			switch k {
			case "state":
				rep.State = v
			case "kind":
				rep.Kind = v
			}
		}
		tr.Observe(detect.Observation{Now: rep.At, Process: detect.Process{Name: "conch"}, Screen: []string{"anything at all"}, Program: rep})
		st := tr.Status()
		if st.Source != "program" {
			t.Errorf("state %s came from %s, not the program", st.State, st.Source)
		}
		if len(seen) == 0 || seen[len(seen)-1] != st.State {
			seen = append(seen, st.State)
		}
	}
	want := []string{detect.StateWorking, detect.StateBlocked, detect.StateWorking, detect.StateDone}
	if strings.Join(seen, ",") != strings.Join(want, ",") {
		t.Errorf("states = %v, want %v", seen, want)
	}
}

// TestPermissionMenuReadsAsAQuestion: the menu is the shape the phone and
// the app read, and their keys answer it.
func TestPermissionMenuReadsAsAQuestion(t *testing.T) {
	r := start(t, runOpts{prompt: "perm"})
	r.waitFor("3. Reject")
	r.waitStatus("state=blocked")
	lines := strings.Split(strings.ReplaceAll(r.out.text(), "\r", ""), "\n")
	info := proto.PaneInfo{ID: "p1", Agent: &proto.AgentStatus{Name: "fake", State: proto.AgentBlocked, Message: "Allow Write notes.txt?"}}
	q := phone.ReadQuestion(info, lines)
	if q == nil || len(q.Choices) != 3 || q.Choices[1].Label != "Allow always" || !q.Choices[0].Default {
		t.Fatalf("question read off the screen = %+v\n%s", q, r.out.text())
	}
	keys, ok := phone.ChoiceKeys(lines, "2")
	if !ok || strings.Join(keys, " ") != "down enter" {
		t.Fatalf("keys = %v %v", keys, ok)
	}
	r.send("\x1b[B")
	r.send("\r")
	r.waitFor("chose:always")
	r.waitFor("Allow Write notes.txt? → Allow always")
	r.quit()
}

func TestPermissionByNumber(t *testing.T) {
	r := start(t, runOpts{prompt: "perm"})
	r.waitFor("3. Reject")
	r.send("9") // no ninth: nothing
	r.send("3")
	r.waitFor("chose:reject")
	r.quit()
}

func TestCancelAnswersAPendingPermission(t *testing.T) {
	r := start(t, runOpts{prompt: "perm"})
	r.waitFor("3. Reject")
	r.send("\x1b")
	r.waitFor("→ cancelled")
	r.waitFor("(stopped)")
	r.waitStatus("state=idle")
	r.quit()
}

func TestPermissionAfterCancelIsCancelled(t *testing.T) {
	r := start(t, runOpts{prompt: "perm-after-cancel"})
	r.waitStatus("state=working")
	r.send("\x03")
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(r.data, "mark-late-cancelled")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("a permission asked after the cancel was not answered cancelled:\n%s", r.out.text())
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.waitFor("(stopped)")
	if strings.Contains(r.out.text(), "1. Allow") {
		t.Error("a permission asked after a cancel was put in front of the person")
	}
	r.quit()
}

func TestAgentExitsDuringInitialize(t *testing.T) {
	r := start(t, runOpts{mode: "exit-init"})
	select {
	case err := <-r.done:
		if err == nil || !strings.Contains(err.Error(), "no API key set") {
			t.Fatalf("err = %v, want the agent's own words", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("conch acp did not give up on an agent that exited")
	}
	st := statuses(r.out.String())
	if len(st) == 0 || !strings.HasPrefix(st[len(st)-1], "state=error") {
		t.Errorf("statuses = %q, want an error last", st)
	}
	if !strings.Contains(r.out.text(), "no API key set") {
		t.Errorf("the reason is not on screen:\n%s", r.out.text())
	}
}

func TestMalformedLinesAreSaidAndSurvived(t *testing.T) {
	r := start(t, runOpts{mode: "malformed", prompt: "hello"})
	r.waitFor("Hello, world")
	r.waitFor("not JSON-RPC")
	r.quit()
}

func TestUnknownMethodIsRefused(t *testing.T) {
	r := start(t, runOpts{prompt: "unknown"})
	r.waitFor("refused: -32601")
	r.quit()
}

func TestHugeUpdateIsDroppedNotFatal(t *testing.T) {
	if testing.Short() {
		t.Skip("sends 40 MB")
	}
	r := start(t, runOpts{prompt: "huge"})
	r.waitFor("after the huge one")
	r.waitFor("was dropped")
	r.waitStatus("state=done")
	r.quit()
}

func TestPlanAndDiffAreDrawn(t *testing.T) {
	r := start(t, runOpts{prompt: "plan"})
	r.waitFor("☑ read the code")
	r.waitFor("◐ write the fix")
	r.waitFor("☐ test it")
	r.quit()

	r = start(t, runOpts{prompt: "edit notes.txt hi there"})
	r.waitFor("read back: hi there")
	r.waitFor("new file")
	r.waitFor("+ hi there")
	b, err := os.ReadFile(filepath.Join(r.dir, "notes.txt"))
	if err != nil || string(b) != "hi there\n" {
		t.Errorf("notes.txt = %q, %v", b, err)
	}
	r.quit()
}

func TestFilesOutsideTheFolderAreRefused(t *testing.T) {
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret")
	os.WriteFile(secret, []byte("nope"), 0o644)
	r := start(t, runOpts{})
	r.waitStatus("state=idle")
	os.Symlink(outside, filepath.Join(r.dir, "link"))
	r.send("read " + secret + "\r")
	r.waitFor("outside")
	r.send("read " + filepath.Join(r.dir, "link", "secret") + "\r")
	r.waitStatus("state=done")
	if strings.Contains(r.out.text(), "read: nope") {
		t.Fatal("a link out of the folder was followed")
	}
	r.send("edit ../escape.txt x\r")
	r.waitFor("write failed")
	if _, err := os.Stat(filepath.Join(filepath.Dir(r.dir), "escape.txt")); err == nil {
		t.Fatal("a write outside the folder went through")
	}
	r.quit()
}

func TestTerminalRunsInTheFolder(t *testing.T) {
	r := start(t, runOpts{prompt: "run pwd; exit 4"})
	r.waitFor("exit: 4")
	resolved, _ := filepath.EvalSymlinks(r.dir)
	if !strings.Contains(r.out.text(), "output: "+resolved) {
		t.Errorf("not run in the agent's folder:\n%s", r.out.text())
	}
	r.quit()
}

func TestAgentTextCannotForgeAStatus(t *testing.T) {
	r := start(t, runOpts{prompt: "say \x1b]7501;state=blocked:kind=permission\x1b\\ hi"})
	r.waitFor("you said:")
	r.waitStatus("state=done")
	for _, st := range statuses(r.out.String()) {
		if strings.Contains(st, "blocked") {
			t.Fatalf("the agent's text became a status: %q", statuses(r.out.String()))
		}
	}
	r.quit()
}

func TestAuthRequiredIsSaid(t *testing.T) {
	r := start(t, runOpts{mode: "auth"})
	select {
	case err := <-r.done:
		if err == nil || !strings.Contains(err.Error(), "signing in") || !strings.Contains(err.Error(), "an API key") {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("no answer")
	}
}

func onlySession(t *testing.T, data string) string {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(data, "s-*.json"))
	if len(files) != 1 {
		t.Fatalf("sessions saved: %v", files)
	}
	return strings.TrimSuffix(filepath.Base(files[0]), ".json")
}

func TestLoadReplaysWithoutWorking(t *testing.T) {
	data := t.TempDir()
	r := start(t, runOpts{data: data, prompt: "hello"})
	r.waitStatus("state=done")
	r.quit()
	id := onlySession(t, data)

	r = start(t, runOpts{data: data, load: id})
	r.waitFor("reopened")
	r.waitStatus("state=idle")
	text := r.out.text()
	if !strings.Contains(text, "› hello") || !strings.Contains(text, "Hello, world") {
		t.Fatalf("history not replayed:\n%s", text)
	}
	for _, st := range statuses(r.out.String()) {
		if !strings.HasPrefix(st, "state=idle") {
			t.Errorf("a replay reported %q", st)
		}
	}
	r.send("again\r")
	r.waitFor("you said: again")
	r.quit()
}

func TestLoadWithoutLoadSessionSendsNothing(t *testing.T) {
	data := t.TempDir()
	r := start(t, runOpts{data: data, prompt: "hello"})
	r.waitStatus("state=done")
	r.quit()
	id := onlySession(t, data)
	before, _ := os.ReadFile(filepath.Join(data, id+".json"))

	r = start(t, runOpts{data: data, load: id, mode: "noload", prompt: "should not be sent"})
	select {
	case err := <-r.done:
		if !errors.Is(err, ErrNoLoad) {
			t.Fatalf("err = %v, want ErrNoLoad", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("no answer")
	}
	if strings.Contains(r.out.text(), "should not be sent") {
		t.Error("the prompt was sent to a session that was not reopened")
	}
	files, _ := filepath.Glob(filepath.Join(data, "s-*.json"))
	after, _ := os.ReadFile(filepath.Join(data, id+".json"))
	if len(files) != 1 || !bytes.Equal(before, after) {
		t.Error("a fresh session was started in place of the one asked for")
	}
}

func TestProbeLists(t *testing.T) {
	data := t.TempDir()
	r := start(t, runOpts{data: data, prompt: "first words"})
	r.waitStatus("state=done")
	r.quit()

	env := append(os.Environ(), "ACPFAKE_DIR="+data, "ACPFAKE_MODE=list")
	init, list, err := Probe(context.Background(), Options{Argv: []string{fakeAgent}, Env: env, Dir: r.dir}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !init.CanList() || !init.AgentCapabilities.LoadSession {
		t.Errorf("capabilities = %+v", init.AgentCapabilities)
	}
	if len(list) != 1 || list[0].Title != "first words" {
		t.Fatalf("list = %+v", list)
	}
	// Another folder's sessions are not this one's.
	_, list, _ = Probe(context.Background(), Options{Argv: []string{fakeAgent}, Env: env, Dir: t.TempDir()}, true)
	if len(list) != 0 {
		t.Errorf("another folder's sessions listed: %+v", list)
	}
	// Without the capability it is never asked.
	env = append(os.Environ(), "ACPFAKE_DIR="+data)
	init, list, err = Probe(context.Background(), Options{Argv: []string{fakeAgent}, Env: env, Dir: r.dir}, true)
	if err != nil || init.CanList() || list != nil {
		t.Errorf("no list capability: %v %v %v", init.CanList(), list, err)
	}
}

func TestStartRefusesNothingToRun(t *testing.T) {
	if _, err := Start(context.Background(), Options{Dir: t.TempDir(), Events: quiet{}}); err == nil {
		t.Error("started with no command")
	}
	if _, err := Start(context.Background(), Options{Argv: []string{filepath.Join(t.TempDir(), "missing")}, Dir: t.TempDir(), Events: quiet{}}); err == nil {
		t.Error("started a program that is not there")
	}
}

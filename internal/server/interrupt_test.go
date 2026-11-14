package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

func TestInterruptAgentRefuses(t *testing.T) {
	s, _, work := shareFixture(t)
	agentPane(t, s, "p1", "", work, "stty -echo; exec cat")
	gone := agentPane(t, s, "p2", "claude", work, "stty -echo; exec cat")
	gone.p.Close()
	a5WaitFor(t, "p2 exits", func() bool { return gone.info().State == proto.PaneExited })
	for _, c := range []struct {
		id, code, msg string
	}{
		{"p404", proto.ErrNotFound, `no pane "p404"`},
		{"p1", proto.ErrBadRequest, "p1 is not running an agent"},
		{"p2", proto.ErrBadRequest, "p2 has exited"},
	} {
		if _, perr := s.interruptAgent(proto.AgentInterruptParams{ID: c.id}); perr == nil || perr.Code != c.code || !strings.Contains(perr.Message, c.msg) {
			t.Errorf("%s: %v, want %s %q", c.id, perr, c.code, c.msg)
		}
		if _, perr := s.attachFolder(proto.AgentAttachParams{ID: c.id}); perr == nil || perr.Code != c.code || !strings.Contains(perr.Message, c.msg) {
			t.Errorf("attach %s: %v, want %s %q", c.id, perr, c.code, c.msg)
		}
	}
}

// The case Stop exists for: Claude at work, by its hooks, interrupted.
// No hook says it stopped; the screen does, and conch believes it at
// once — told by its own looks after the interrupt, with no tick running —
// rather than after hook_working_stale.
func TestInterruptAgentStopsAtOnce(t *testing.T) {
	s, _, work := shareFixture(t)
	old := interruptLookEvery
	interruptLookEvery = 20 * time.Millisecond
	t.Cleanup(func() { interruptLookEvery = old })
	// Shows Claude's spinner until an escape comes, then what Claude
	// shows once interrupted. awk is not a shell, so the pane reads as the
	// agent it was started as; what came before the escape goes in key.
	e := agentPane(t, s, "p1", "claude", work, `stty -icanon -echo min 1; exec awk 'BEGIN { RS = "\033"; print "esc to interrupt"; fflush() }
		{ printf "\033[2J\033[HInterrupted by user\n"; fflush(); printf "[%s]", $0 > "key"; close("key") }'`)
	hook(s, e, "PreToolUse")
	s.observe(e)
	if st, _ := agentState(e); st != proto.AgentWorking {
		t.Fatalf("before: %s", st)
	}
	start := time.Now()
	res, perr := s.interruptAgent(proto.AgentInterruptParams{ID: "p1"})
	if perr != nil {
		t.Fatal(perr)
	}
	if res.ID != "p1" || strings.Join(res.Keys, ",") != "escape" {
		t.Fatalf("result %+v", res)
	}
	a5WaitFor(t, "idle", func() bool { st, _ := agentState(e); return st == proto.AgentIdle })
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("took %v to stop", took)
	}
	if r := e.info().Agent.Reason; r != "interrupted" {
		t.Errorf("reason %q", r)
	}
	b, _ := os.ReadFile(filepath.Join(work, "key"))
	if string(b) != "[]" {
		t.Errorf("the agent read %q before the escape, want nothing", b)
	}
}

// Stop harder is ctrl+c; an agent that asks for escape twice gets two.
func TestInterruptAgentKeys(t *testing.T) {
	s, _, work := shareFixture(t)
	old := interruptGap
	interruptGap = 5 * time.Millisecond
	t.Cleanup(func() { interruptGap = old })
	e := agentPane(t, s, "p1", "claude", work, "stty -echo -isig -icanon min 1; exec cat -v")
	res, perr := s.interruptAgent(proto.AgentInterruptParams{ID: "p1", Hard: true})
	if perr != nil || strings.Join(res.Keys, ",") != "ctrl+c" {
		t.Fatalf("hard: %+v %v", res, perr)
	}
	a5WaitFor(t, "^C", func() bool { return screenCount(e, "^C") == 1 })
	if res.State != proto.AgentIdle {
		t.Errorf("an idle agent interrupted: %s", res.State)
	}

	d := agentPane(t, s, "p2", "devin", work, "stty -echo -isig -icanon min 1; exec cat -v")
	res, perr = s.interruptAgent(proto.AgentInterruptParams{ID: "p2"})
	if perr != nil || strings.Join(res.Keys, ",") != "escape,escape" {
		t.Fatalf("devin: %+v %v", res, perr)
	}
	a5WaitFor(t, "two escapes", func() bool { return screenCount(d, "^[^[") == 1 })
}

func TestAttachFolder(t *testing.T) {
	s, _, work := shareFixture(t)
	agentPane(t, s, "p1", "claude", work, "stty -echo; exec cat")
	base := filepath.Join(realDir(work), ".conch", "attachments")
	os.MkdirAll(base, 0o755)
	stale, fresh, file := filepath.Join(base, "old1"), filepath.Join(base, "new1"), filepath.Join(base, "note.txt")
	os.MkdirAll(stale, 0o755)
	os.WriteFile(filepath.Join(stale, "a.png"), []byte("x"), 0o644)
	os.MkdirAll(fresh, 0o755)
	os.WriteFile(file, []byte("mine"), 0o644)
	long := time.Now().Add(-proto.AttachKeep - time.Hour)
	os.Chtimes(stale, long, long)
	os.Chtimes(file, long, long)
	nearly := time.Now().Add(-proto.AttachKeep + time.Hour)
	os.Chtimes(fresh, nearly, nearly)

	res, perr := s.attachFolder(proto.AgentAttachParams{ID: "p1"})
	if perr != nil {
		t.Fatal(perr)
	}
	if filepath.Dir(res.Dir) != base || len(filepath.Base(res.Dir)) != 8 {
		t.Fatalf("dir %q, want a new folder in %s", res.Dir, base)
	}
	if res.Root != realDir(work) || res.Cwd != realDir(work) || res.MaxSize != 20<<20 || res.MaxFiles != 10 {
		t.Fatalf("result %+v", res)
	}
	if st, err := os.Stat(res.Dir); err != nil || !st.IsDir() {
		t.Fatalf("folder: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("a week-old folder was kept")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("a six-day-old folder was removed")
	}
	if _, err := os.Stat(file); err != nil {
		t.Error("a file of somebody's was removed")
	}
	// Never committed: git sees nothing new, and no .gitignore was made.
	os.WriteFile(filepath.Join(res.Dir, "shot.png"), []byte("png"), 0o644)
	if out := gitOut(t, work, "status", "--porcelain"); out != "" {
		t.Fatalf("git status: %q", out)
	}
	if _, err := os.Stat(filepath.Join(work, ".gitignore")); err == nil {
		t.Fatal("made a .gitignore")
	}
	// A second message, a second folder; the exclude line once.
	again, perr := s.attachFolder(proto.AgentAttachParams{ID: "p1"})
	if perr != nil || again.Dir == res.Dir {
		t.Fatalf("again: %+v %v", again, perr)
	}
	ex, _ := os.ReadFile(filepath.Join(work, ".git", "info", "exclude"))
	if strings.Count(string(ex), ".conch/attachments/\n") != 1 {
		t.Fatalf("exclude: %q", ex)
	}
}

// An agent in a linked worktree: its attachments go in that worktree, and
// git reads the exclude from the repository's common folder.
func TestAttachFolderWorktreeAndSubfolder(t *testing.T) {
	s, _, work := shareFixture(t)
	wt := filepath.Join(filepath.Dir(work), "wt")
	cmd := exec.Command("git", "-C", work, "worktree", "add", "-q", "-b", "feat", wt)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	sub := filepath.Join(wt, "pkg", "a")
	os.MkdirAll(sub, 0o755)
	agentPane(t, s, "p1", "claude", sub, "stty -echo; exec cat")
	res, perr := s.attachFolder(proto.AgentAttachParams{ID: "p1"})
	if perr != nil {
		t.Fatal(perr)
	}
	if res.Root != realDir(wt) || res.Cwd != realDir(sub) || !strings.HasPrefix(res.Dir, filepath.Join(realDir(wt), ".conch", "attachments")+"/") {
		t.Fatalf("result %+v", res)
	}
	os.WriteFile(filepath.Join(res.Dir, "a.txt"), []byte("x"), 0o644)
	if out := gitOut(t, wt, "status", "--porcelain"); out != "" {
		t.Fatalf("worktree status: %q", out)
	}

	// Not a checkout at all: the folder itself, and nothing for git.
	plain := filepath.Join(filepath.Dir(work), "plain")
	os.MkdirAll(plain, 0o755)
	agentPane(t, s, "p2", "claude", plain, "stty -echo; exec cat")
	res, perr = s.attachFolder(proto.AgentAttachParams{ID: "p2"})
	if perr != nil || res.Root != realDir(plain) {
		t.Fatalf("plain: %+v %v", res, perr)
	}
	if _, err := os.Stat(filepath.Join(plain, ".git")); err == nil {
		t.Fatal("made a .git")
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

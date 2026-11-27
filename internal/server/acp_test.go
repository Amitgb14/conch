package server_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/phone"
	"github.com/Amitgb14/conch/internal/proto"
)

var (
	acpFakeOnce sync.Once
	acpFakeBin  string
	acpFakeErr  error
)

// acpFake builds internal/tools/acpfake, once.
func acpFake(t *testing.T) string {
	t.Helper()
	acpFakeOnce.Do(func() {
		dir, err := os.MkdirTemp("", "acpfake")
		if err != nil {
			acpFakeErr = err
			return
		}
		acpFakeBin = filepath.Join(dir, "acpfake")
		_, file, _, _ := runtime.Caller(0)
		cmd := exec.Command("go", "build", "-o", acpFakeBin, ".")
		cmd.Dir = filepath.Join(filepath.Dir(file), "..", "tools", "acpfake")
		if out, err := cmd.CombinedOutput(); err != nil {
			acpFakeErr = fmt.Errorf("%v: %s", err, out)
		}
	})
	if acpFakeErr != nil {
		t.Fatal(acpFakeErr)
	}
	return acpFakeBin
}

func paneScreen(t *testing.T, ctx context.Context, c interface {
	Call(context.Context, string, any, any) error
}, id string) []string {
	t.Helper()
	var r proto.PaneReadResult
	if err := c.Call(ctx, proto.MethodPaneRead, proto.PaneRef{ID: id}, &r); err != nil {
		t.Fatal(err)
	}
	return r.Lines
}

// TestAnACPAgentEndToEnd: an agent that speaks ACP, from its manifest to
// a pane — started with a first message, its permission request read off
// the screen and answered as the phone answers one, its state the
// protocol's, its session listed and reopened.
func TestAnACPAgentEndToEnd(t *testing.T) {
	t.Setenv("CONCH_SOCKET", "")
	t.Setenv("CONCH_PANE_ID", "")
	t.Setenv("CONCH_SESSION_GRACE", "10s")
	fake := acpFake(t)
	data := t.TempDir()
	c, _ := startServerWithAgents(t, map[string]string{
		"fake": fmt.Sprintf("agent = %q\nlabel = \"Fake ACP\"\n[acp]\ncommand = %q\nenv = [\"ACPFAKE_MODE=list\", \"ACPFAKE_DIR=%s\"]\n", "fake", fake, data),
	})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var status proto.AgentStatusResult
	if err := c.Call(ctx, proto.MethodAgentStatus, nil, &status); err != nil {
		t.Fatal(err)
	}
	var av *proto.AgentAvailability
	for i := range status.Agents {
		if status.Agents[i].Name == "fake" {
			av = &status.Agents[i]
		}
	}
	if av == nil || !av.Installed || av.Protocol != proto.ProtocolACP || av.Tier != proto.TierRunsHere || !av.Sessions {
		t.Fatalf("agent status = %+v", av)
	}
	for _, a := range status.Agents {
		if a.Name == "claude" && a.Protocol != "" {
			t.Error("claude is said to run over ACP")
		}
	}

	work := t.TempDir()
	work, _ = filepath.EvalSymlinks(work)
	var info proto.PaneInfo
	if err := c.Call(ctx, proto.MethodPaneCreate, proto.PaneCreateParams{
		Agent: "fake", Prompt: "perm", Cwd: work, Cols: 100, Rows: 30, NoProject: true}, &info); err != nil {
		t.Fatal(err)
	}
	waitAgentState(t, c, info.ID, proto.AgentBlocked)
	p := panesNow(t, c)[info.ID]
	if p.Agent.Name != "fake" || p.Agent.Message != "Allow Write notes.txt?" {
		t.Errorf("agent = %+v", p.Agent)
	}
	screen := paneScreen(t, ctx, c, info.ID)
	q := phone.ReadQuestion(p, screen)
	if q == nil || len(q.Choices) != 3 || q.Choices[2].Label != "Reject" {
		t.Fatalf("question = %+v\n%s", q, strings.Join(screen, "\n"))
	}
	keys, ok := phone.ChoiceKeys(screen, "2")
	if !ok {
		t.Fatal("no keys for choice 2")
	}
	if err := c.Call(ctx, proto.MethodPaneSendKeys, proto.PaneSendKeysParams{ID: info.ID, Keys: keys}, nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for !strings.Contains(strings.Join(paneScreen(t, ctx, c, info.ID), "\n"), "chose:always") {
		if time.Now().After(deadline) {
			t.Fatalf("the answer did not reach the agent:\n%s", strings.Join(paneScreen(t, ctx, c, info.ID), "\n"))
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Finished, as the protocol said: done, or idle when the keys that
	// answered are counted as somebody looking after the turn has already
	// ended — which on a loaded machine they can be.
	deadline = time.Now().Add(15 * time.Second)
	for {
		st := panesNow(t, c)[info.ID].Agent
		if st != nil && (st.State == proto.AgentDone || st.State == proto.AgentIdle) {
			if st.Source != "program" {
				t.Errorf("the turn's end came from %q, not the agent's report", st.Source)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the turn never ended: %+v", st)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Typed into, as conch send does.
	if err := c.Call(ctx, proto.MethodPaneSendText, proto.PaneSendTextParams{ID: info.ID, Text: "hello\r"}, nil); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(15 * time.Second)
	for !strings.Contains(strings.Join(paneScreen(t, ctx, c, info.ID), "\n"), "Hello, world") {
		if time.Now().After(deadline) {
			t.Fatalf("a typed prompt was not sent:\n%s", strings.Join(paneScreen(t, ctx, c, info.ID), "\n"))
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Its session is listed for the folder, and reopens.
	var list proto.SessionList
	var mine *proto.SessionInfo
	deadline = time.Now().Add(20 * time.Second)
	for mine == nil && time.Now().Before(deadline) {
		if err := c.Call(ctx, proto.MethodSessionList, proto.SessionListParams{Dir: work}, &list); err != nil {
			t.Fatal(err)
		}
		for i := range list.Sessions {
			if list.Sessions[i].Agent == "fake" {
				mine = &list.Sessions[i]
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if mine == nil || mine.Title != "perm" || mine.Dir != work {
		t.Fatalf("sessions = %+v", list.Sessions)
	}
	err := c.Call(ctx, proto.MethodSessionDelete, proto.SessionRef{Agent: "fake", ID: mine.ID, Dir: work}, nil)
	if err == nil || !strings.Contains(err.Error(), "over ACP") {
		t.Errorf("delete = %v, want a refusal", err)
	}
	var again proto.PaneInfo
	if err := c.Call(ctx, proto.MethodSessionResume, proto.SessionRef{Agent: "fake", ID: mine.ID, Dir: work, Cols: 100, Rows: 30}, &again); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(15 * time.Second)
	for {
		text := strings.Join(paneScreen(t, ctx, c, again.ID), "\n")
		if strings.Contains(text, "reopened") && strings.Contains(text, "chose:always") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the session was not reopened:\n%s", text)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

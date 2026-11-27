package server_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/adapter"
	"github.com/Amitgb14/conch/internal/proto"
)

// TestAddAnACPAgentFromSettings: what Settings › Agents › Add an ACP
// Agent does, through the server — the command checked, the agent added
// and offered without a reload, started, changed and removed.
func TestAddAnACPAgentFromSettings(t *testing.T) {
	t.Setenv("CONCH_SOCKET", "")
	t.Setenv("CONCH_PANE_ID", "")
	t.Cleanup(adapter.SetProbeTimeout(3 * time.Second))
	fake := acpFake(t)
	c, dir := startServerWithAgents(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if miss := c.MissingCapabilities([]string{proto.CapAgentACPAdd}); len(miss) > 0 {
		t.Fatalf("not announced: %v", miss)
	}
	spec := proto.AgentACPSpec{Name: "fake", Label: "Fake ACP", Command: fake, Env: []string{"ACPFAKE_MODE=list"}}

	// Check: it speaks ACP, and says what it offers.
	var ck proto.AgentACPCheck
	if err := c.Call(ctx, proto.MethodAgentACPCheck, spec, &ck); err != nil {
		t.Fatal(err)
	}
	if ck.Problem != "" || ck.Agent != "acpfake" || ck.ProtocolVersion != 1 || !ck.Load || !ck.List || ck.Path == "" {
		t.Fatalf("check = %+v", ck)
	}
	// What can go wrong, each said as itself.
	for _, tc := range []struct {
		spec proto.AgentACPSpec
		want string
		out  string
	}{
		{proto.AgentACPSpec{Command: "definitely-not-installed-acp --acp"}, proto.ACPNotFound, ""},
		{proto.AgentACPSpec{Command: fake, Env: []string{"ACPFAKE_MODE=exit-init"}}, proto.ACPExited, "no API key set"},
		{proto.AgentACPSpec{Command: "/bin/sh -c 'echo Welcome to a chat program; sleep 30'"}, proto.ACPNotACP, "Welcome to a chat program"},
	} {
		var got proto.AgentACPCheck
		if err := c.Call(ctx, proto.MethodAgentACPCheck, tc.spec, &got); err != nil {
			t.Fatal(err)
		}
		if got.Problem != tc.want || !strings.Contains(got.Output, tc.out) || got.Message == "" {
			t.Errorf("check %q = %+v, want %s", tc.spec.Command, got, tc.want)
		}
	}
	if err := c.Call(ctx, proto.MethodAgentACPCheck, proto.AgentACPSpec{Command: "  "}, nil); err == nil {
		t.Error("checked no command")
	}

	// Add: written, and offered at once.
	var av proto.AgentAvailability
	if err := c.Call(ctx, proto.MethodAgentACPAdd, proto.AgentACPAddParams{AgentACPSpec: spec}, &av); err != nil {
		t.Fatal(err)
	}
	if av.Name != "fake" || av.Label != "Fake ACP" || av.Protocol != proto.ProtocolACP || !av.Installed || !av.Removable || av.ACP == nil || av.ACP.Command != fake || strings.Join(av.ACP.Env, " ") != "ACPFAKE_MODE=list" {
		t.Errorf("added = %+v", av)
	}
	if _, err := os.Stat(filepath.Join(dir, "agents", "fake.toml")); err != nil {
		t.Errorf("no manifest: %v", err)
	}
	var status proto.AgentStatusResult
	if err := c.Call(ctx, proto.MethodAgentStatus, nil, &status); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range status.Agents {
		found = found || a.Name == "fake"
		if a.Name == "claude" && a.Removable {
			t.Error("claude is said to be removable")
		}
	}
	if !found {
		t.Fatal("agent.status does not offer it")
	}
	// It starts, and is recognised: detection read it too.
	var info proto.PaneInfo
	if err := c.Call(ctx, proto.MethodPaneCreate, proto.PaneCreateParams{Agent: "fake", Prompt: "hello", Cwd: t.TempDir(), Cols: 80, Rows: 24, NoProject: true}, &info); err != nil {
		t.Fatal(err)
	}
	waitAgentState(t, c, info.ID, proto.AgentDone)

	// Refused: the same name again, a supported agent's, a bad name, a
	// line break that would be a second line of the file.
	for _, bad := range []proto.AgentACPAddParams{
		{AgentACPSpec: spec},
		{AgentACPSpec: proto.AgentACPSpec{Name: "claude", Command: fake}},
		{AgentACPSpec: proto.AgentACPSpec{Name: "Not A Name", Command: fake}},
		{AgentACPSpec: proto.AgentACPSpec{Name: "two", Command: fake + "\n[run]"}},
		{AgentACPSpec: proto.AgentACPSpec{Name: "envy", Command: fake, Env: []string{"not an assignment"}}},
	} {
		if err := c.Call(ctx, proto.MethodAgentACPAdd, bad, nil); err == nil {
			t.Errorf("added %+v", bad)
		}
	}
	// Changed, with Replace. Decoded fresh: JSON leaves a field the
	// answer omits (Sessions false) as it was.
	av = proto.AgentAvailability{}
	if err := c.Call(ctx, proto.MethodAgentACPAdd, proto.AgentACPAddParams{AgentACPSpec: proto.AgentACPSpec{Name: "fake", Label: "Renamed", Command: fake}, Replace: true}, &av); err != nil {
		t.Fatal(err)
	}
	if av.Label != "Renamed" {
		t.Errorf("replaced = %+v", av)
	}
	// What it offers now is asked in the background: the next status says it.
	deadline := time.Now().Add(15 * time.Second)
	for {
		var st proto.AgentStatusResult
		if err := c.Call(ctx, proto.MethodAgentStatus, nil, &st); err != nil {
			t.Fatal(err)
		}
		var now proto.AgentAvailability
		for _, a := range st.Agents {
			if a.Name == "fake" {
				now = a
			}
		}
		if now.Label == "Renamed" && !now.Sessions {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("still listing sessions after the change: %+v", now)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Removed: gone from the list, the file with it.
	if err := c.Call(ctx, proto.MethodAgentACPRemove, proto.AgentACPRemoveParams{Name: "fake"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "agents", "fake.toml")); !os.IsNotExist(err) {
		t.Errorf("manifest still there: %v", err)
	}
	if err := c.Call(ctx, proto.MethodAgentStatus, nil, &status); err != nil {
		t.Fatal(err)
	}
	for _, a := range status.Agents {
		if a.Name == "fake" {
			t.Error("still offered after it was removed")
		}
	}
	// Never a shipped agent, a supported one, or one that is gone.
	for _, name := range []string{"pi", "claude", "fake", "../x"} {
		if err := c.Call(ctx, proto.MethodAgentACPRemove, proto.AgentACPRemoveParams{Name: name}, nil); err == nil {
			t.Errorf("removed %q", name)
		}
	}
}

// TestReplaceLeavesARunManifestAlone: a [run] manifest of the person's
// own is not overwritten by an ACP one of the same name.
func TestReplaceLeavesARunManifestAlone(t *testing.T) {
	t.Setenv("CONCH_SOCKET", "")
	t.Setenv("CONCH_PANE_ID", "")
	body := "agent = \"robo\"\n[run]\nbinary = \"sh\"\n"
	c, dir := startServerWithAgents(t, map[string]string{"robo": body})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := c.Call(ctx, proto.MethodAgentACPAdd, proto.AgentACPAddParams{AgentACPSpec: proto.AgentACPSpec{Name: "robo", Command: "robo --acp"}, Replace: true}, nil)
	if err == nil || !strings.Contains(err.Error(), "left alone") {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "agents", "robo.toml")); string(b) != body {
		t.Errorf("the file changed: %q", b)
	}
	if err := c.Call(ctx, proto.MethodAgentACPRemove, proto.AgentACPRemoveParams{Name: "robo"}, nil); err == nil {
		t.Error("removed a [run] agent")
	}
}

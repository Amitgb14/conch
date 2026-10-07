package server_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/server"
)

// startServerWithAgents is startServer with manifests already in its
// config directory, as a machine that has had `conch agent add` run on it.
// They have to be there before the server starts: the registry is built
// once, which is why adding one asks for a reload.
func startServerWithAgents(t *testing.T, manifests map[string]string) (*client.Client, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "conch")
	if err != nil {
		t.Fatal(err)
	}
	dir, _ = filepath.EvalSymlinks(dir)
	if err := os.MkdirAll(filepath.Join(dir, "agents"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range manifests {
		if err := os.WriteFile(filepath.Join(dir, "agents", name+".toml"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sock := filepath.Join(dir, "s.sock")
	srv := server.New(sock, dir)
	go srv.Run()
	t.Cleanup(func() { srv.Stop(); time.Sleep(100 * time.Millisecond); os.RemoveAll(dir) })
	var c *client.Client
	for range 100 {
		if c, err = client.Dial(sock, "test"); err == nil {
			t.Cleanup(func() { c.Close() })
			return c, dir
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal(err)
	return nil, ""
}

// TestAnAgentFromAManifestIsRunButNotPretendedFor: the second tier, end to
// end through the server. It starts and is reported with its tier; a first
// message it cannot be given is refused rather than dropped on the floor
// and the agent started as if nobody had asked.
//
// That last part is the tier's whole promise. Seen for real while testing
// this: `conch task -agent robo "fix the tests"` would otherwise have
// started a shell with no message and called it a task.
func TestAnAgentFromAManifestIsRunButNotPretendedFor(t *testing.T) {
	c, _ := startServerWithAgents(t, map[string]string{
		// A program that is certainly installed, so this tests conch and
		// not whether somebody's laptop has an agent on it.
		"quiet": "agent = \"quiet\"\nlabel = \"Quiet Agent\"\nprocess_names = [\"sh\"]\n\n[run]\nbinary = \"sh\"\n",
		"talky": "agent = \"talky\"\nlabel = \"Talky Agent\"\nprocess_names = [\"sh\"]\n\n[run]\nbinary = \"sh\"\nprompt = \"-c {prompt}\"\n",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// Both are offered, with the tier said rather than left to be guessed.
	var status proto.AgentStatusResult
	if err := c.Call(ctx, proto.MethodAgentStatus, nil, &status); err != nil {
		t.Fatal(err)
	}
	tiers := map[string]string{}
	for _, a := range status.Agents {
		tiers[a.Name] = a.Tier
	}
	for _, name := range []string{"quiet", "talky"} {
		if tiers[name] != proto.TierRunsHere {
			t.Errorf("%s has tier %q", name, tiers[name])
		}
	}
	if tiers["claude"] != proto.TierSupported {
		t.Errorf("claude has tier %q", tiers["claude"])
	}

	// It starts, and the pane says which agent it is.
	var info proto.PaneInfo
	if err := c.Call(ctx, proto.MethodPaneCreate, proto.PaneCreateParams{
		Agent: "quiet", Cols: 80, Rows: 24, NoProject: true}, &info); err != nil {
		t.Fatalf("starting it: %v", err)
	}
	// Detection is not instant: the tracker has to see the process, which
	// is the same wait a person's eye makes.
	deadline := time.Now().Add(15 * time.Second)
	var seen string
	for time.Now().Before(deadline) {
		var list proto.PaneList
		if err := c.Call(ctx, proto.MethodPaneList, nil, &list); err != nil {
			t.Fatal(err)
		}
		for _, p := range list.Panes {
			if p.ID == info.ID && p.Agent != nil {
				seen = p.Agent.Name
			}
		}
		if seen != "" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if seen != "quiet" {
		t.Fatalf("the pane is not recognised as the agent that was started: %q", seen)
	}

	// A first message it cannot be given is refused, with the one thing
	// that would fix it.
	err := c.Call(ctx, proto.MethodPaneCreate, proto.PaneCreateParams{
		Agent: "quiet", Prompt: "fix the tests", Cols: 80, Rows: 24, NoProject: true}, &proto.PaneInfo{})
	if err == nil {
		t.Fatal("a first message was accepted by an agent that cannot take one")
	}
	for _, want := range []string{"Quiet Agent", "cannot be given a first message", "quiet.toml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}

	// The one whose manifest says how takes it, and the words reach the
	// command line.
	var talky proto.PaneInfo
	if err := c.Call(ctx, proto.MethodPaneCreate, proto.PaneCreateParams{
		Agent: "talky", Prompt: "echo hello-from-the-prompt", Cols: 80, Rows: 24, NoProject: true}, &talky); err != nil {
		t.Fatalf("an agent whose manifest says how: %v", err)
	}
	if !strings.Contains(strings.Join(talky.Command, " "), "hello-from-the-prompt") {
		t.Errorf("the first message did not reach the command: %v", talky.Command)
	}
}

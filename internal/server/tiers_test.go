package server_test

import (
	"context"
	"encoding/json"
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

// An agent whose manifest named no installer must not be "installed".
// Running the empty script started a pane, exited 0, and conch said
// "installed · press c to start it" — reported from use as the install
// doing nothing.
func TestNoInstallerIsSaidAndRefused(t *testing.T) {
	c, _ := startServerWithAgents(t, map[string]string{
		"noinst": "agent = \"noinst\"\nlabel = \"No Installer\"\nprocess_names = [\"sh\"]\n\n[run]\nbinary = \"definitely-not-a-program-here\"\n",
		"hasinst": "agent = \"hasinst\"\nlabel = \"Has Installer\"\nprocess_names = [\"sh\"]\n\n[run]\nbinary = \"definitely-not-a-program-here\"\n" +
			"install = \"echo pretending\"\n",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var status proto.AgentStatusResult
	if err := c.Call(ctx, proto.MethodAgentStatus, nil, &status); err != nil {
		t.Fatal(err)
	}
	no := map[string]bool{}
	for _, a := range status.Agents {
		no[a.Name] = a.NoInstaller
	}
	if !no["noinst"] {
		t.Error("the agent with no install script does not say so")
	}
	if no["hasinst"] {
		t.Error("the agent with an install script says it has none")
	}
	if no["claude"] {
		t.Error("a supported agent says conch cannot install it")
	}

	// And the install is refused rather than running nothing.
	err := c.Call(ctx, proto.MethodAgentInstall, proto.AgentInstallParams{Agent: "noinst", Cols: 80, Rows: 24}, &proto.PaneInfo{})
	if err == nil {
		t.Fatal("installing an agent with no installer was accepted")
	}
	for _, want := range []string{"No Installer", "no installer", "PATH"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	// Nothing was started for it.
	var list proto.PaneList
	if err := c.Call(ctx, proto.MethodPaneList, nil, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Panes) != 0 {
		t.Errorf("it left %d panes behind", len(list.Panes))
	}
	// The one with a script still installs.
	var info proto.PaneInfo
	if err := c.Call(ctx, proto.MethodAgentInstall, proto.AgentInstallParams{Agent: "hasinst", Cols: 80, Rows: 24}, &info); err != nil {
		t.Fatalf("an agent with an installer: %v", err)
	}
	if !strings.Contains(strings.Join(info.Command, " "), "echo pretending") {
		t.Errorf("the installer that ran was %v", info.Command)
	}
}

// An agent that reports its own state through the Program Status
// Protocol, end to end on a real server and a real pty: conch answers
// the query it opens with, and the state in the pane comes from what the
// program said rather than from a screen rule nobody wrote for it.
//
// This is the second tier's weak spot closed: an agent conch only runs
// has no screen rules at all, so its state was a guess until now.
func TestAnAgentReportsItsOwnState(t *testing.T) {
	c, _ := startServerWithAgents(t, map[string]string{
		// No rules: whatever state this pane shows came from the program.
		"talker": "agent = \"talker\"\nlabel = \"Talker\"\nprocess_names = [\"sh\"]\n\n[run]\nbinary = \"sh\"\n",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	script := `stty -icanon -echo min 0 time 30 2>/dev/null
printf '\033]7501;?\033\\'
dd bs=1 count=10 >/dev/null 2>&1
printf '\033]7501;state=blocked:kind=permission:app=talker:msg=%s\033\\' "$(printf 'May I write the file?' | base64 | tr -d '\n')"
sleep 30`
	var info proto.PaneInfo
	if err := c.Call(ctx, proto.MethodPaneCreate, proto.PaneCreateParams{
		Agent: "talker", Command: []string{"/bin/sh", "-c", script},
		Cols: 80, Rows: 24, NoProject: true}, &info); err != nil {
		t.Fatalf("starting it: %v", err)
	}

	deadline := time.Now().Add(20 * time.Second)
	var agent *proto.AgentStatus
	for time.Now().Before(deadline) {
		var list proto.PaneList
		if err := c.Call(ctx, proto.MethodPaneList, nil, &list); err != nil {
			t.Fatal(err)
		}
		for _, p := range list.Panes {
			if p.ID == info.ID && p.Agent != nil && p.Agent.State == proto.AgentBlocked {
				agent = p.Agent
			}
		}
		if agent != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if agent == nil {
		var ex json.RawMessage
		_ = c.Call(ctx, proto.MethodAgentExplain, proto.PaneRef{ID: info.ID}, &ex)
		t.Fatalf("the agent never reported blocked; conch decided: %s", ex)
	}
	if agent.Message != "May I write the file?" {
		t.Errorf("the program's own words did not reach the pane: %q", agent.Message)
	}
	if agent.Source != "program" {
		t.Errorf("the state came from %q, not from the program", agent.Source)
	}
}

package server

import (
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

// What an agent may put in agent_args, and what conch passes on for it.
func TestAgentLaunchArgs(t *testing.T) {
	ok := map[string]string{
		"":                            "",
		"   ":                         "",
		"--model opus":                "--model opus",
		"  --model   opus  ":          "--model opus",
		"--model=opus":                "--model opus",
		"--model 'opus' ":             "--model opus", // as conch new quotes it
		`--model "gpt-5"`:             "--model gpt-5",
		"--model claude-opus-4-1[1m]": "--model 'claude-opus-4-1[1m]'",
		"--model openai/gpt-5":        "--model openai/gpt-5",
		"--model gemini-2.5-pro":      "--model gemini-2.5-pro",
		"--model x@y:z+1":             "--model x@y:z+1",
		"--model 'a'\"b\"":            "--model ab",
	}
	for in, want := range ok {
		got, why := agentLaunch(proto.PaneCreateParams{Agent: "claude", AgentArgs: in})
		if why != "" || got != want {
			t.Errorf("%q: %q %q, want %q", in, got, why, want)
		}
	}
	for _, in := range []string{
		"--dangerously-skip-permissions", "--yolo", "-m opus", "--model", "--model ''", "--model=",
		"--model -x", "--model --yolo", "--model opus --yolo", "--model opus extra", "--MODEL opus",
		"--model opus;id", "--model opus ; id", "--model $(id)", "--model `id`", "--model \"$HOME\"",
		"--model 'unclosed", "--model opus\nid", "--model opus|id", "--model opus&", "--model a>b",
		"--model ~", "--model *", "--model {a,b}", "--model opus #", "--model \\x", "--model 'a b'",
		"--model x\x00", "--model é",
	} {
		if got, why := agentLaunch(proto.PaneCreateParams{Agent: "claude", AgentArgs: in}); why == "" {
			t.Errorf("%q taken, as %q", in, got)
		}
	}
	for _, p := range []proto.PaneCreateParams{
		{Command: []string{"/bin/sh", "-c", "id"}},
		{Command: []string{"/bin/sh"}},
		{Command: []string{"/bin/sh", "-l", "-c", "id"}},
		{Env: []string{"A=B"}},
	} {
		if _, why := agentLaunch(p); why == "" {
			t.Errorf("%+v taken", p)
		}
	}
	t.Setenv("SHELL", "/bin/zsh")
	if _, why := agentLaunch(proto.PaneCreateParams{Command: []string{"/bin/zsh", "-l"}}); why != "" {
		t.Errorf("the login shell: %s", why)
	}
	if _, why := agentLaunch(proto.PaneCreateParams{Command: []string{"/bin/bash", "-l"}}); why == "" {
		t.Error("another shell than the server's is a command of its own")
	}
}

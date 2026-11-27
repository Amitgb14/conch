package proto

import "testing"

func TestACPBaseAgent(t *testing.T) {
	for cmd, want := range map[string]string{
		"claude-agent-acp":                   "claude",
		"/opt/homebrew/bin/claude-agent-acp": "claude",
		"claude-code-acp":                    "claude",
		"LANG=C codex-acp --x":               "codex",
		"gemini --acp":                       "gemini",
		"goose acp":                          "goose",
		"robo --acp":                         "",
		"":                                   "",
		"   ":                                "",
	} {
		if got := ACPBaseAgent(cmd); got != want {
			t.Errorf("ACPBaseAgent(%q) = %q, want %q", cmd, got, want)
		}
	}
}

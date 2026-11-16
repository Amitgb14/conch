package adapter

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// orcaHook is a hook another tool installs for Codex, in a person's
// ~/.codex/hooks.json; its trust hashes below are the ones Codex 0.158
// itself wrote to config.toml when the hook was trusted in /hooks. The
// command has quotes, & and $, so it also pins how conch escapes JSON as
// Codex's serde_json does (Go's would write & as &).
const orcaHook = `: orca-agent-hook-form=1; if [ -n "${ORCA_PANE_KEY-}" ] && [ -n "${ORCA_AGENT_HOOK_ROOT-}" ] && [ -f "${ORCA_AGENT_HOOK_ROOT-}/agent-hooks/codex-hook.sh" ]; then /bin/sh "${ORCA_AGENT_HOOK_ROOT-}/agent-hooks/codex-hook.sh" || :; elif [ -z "${ORCA_AGENT_HOOK_ROOT-}" ] && [ -n "${ORCA_PANE_KEY-}" ] && [ -n "${ORCA_AGENT_HOOK_PORT-}" ] && [ -f "${HOME-}/.orca/agent-hooks/codex-hook.sh" ]; then /bin/sh "${HOME-}/.orca/agent-hooks/codex-hook.sh" || :; else { command -p cat 2>/dev/null || cat; } >/dev/null 2>&1 || :; fi`

func TestCodexHookHashIsCodexs(t *testing.T) {
	for _, c := range []struct {
		event   string
		timeout int
		want    string
	}{
		{"Stop", 10, "sha256:1ae8ee19d95298f7bc4821368a7d4600b9f9d36c68695812c19419b28b5ad53e"},
		{"Interrupt", 3, "sha256:78b430d1d7794c49e6d83a36964d4082d05ec72023ccc38244e97b8e08d054d2"},
	} {
		if got := codexHookHash(c.event, orcaHook, c.timeout); got != c.want {
			t.Errorf("%s: %s, Codex trusts %s", c.event, got, c.want)
		}
	}
	// A different command or timeout is a different hook to Codex.
	if codexHookHash("Stop", orcaHook, 5) == codexHookHash("Stop", orcaHook, 10) ||
		codexHookHash("Stop", "a", 5) == codexHookHash("Stop", "b", 5) ||
		codexHookHash("Stop", "a", 5) == codexHookHash("Interrupt", "a", 5) {
		t.Fatal("hash ignores part of the hook")
	}
}

func TestCodexEventKey(t *testing.T) {
	for ev, want := range map[string]string{
		"PreToolUse": "pre_tool_use", "Stop": "stop", "SessionStart": "session_start",
		"UserPromptSubmit": "user_prompt_submit", "PermissionRequest": "permission_request", "": "",
	} {
		if got := codexEventKey(ev); got != want {
			t.Errorf("%q → %q, want %q", ev, got, want)
		}
	}
}

func TestTOMLString(t *testing.T) {
	for in, want := range map[string]string{
		"":          `""`,
		`a "b" \c`:  `"a \"b\" \\c"`,
		"tab\tnl\n": `"tab\u0009nl\u000A"`,
		"café ✓":    `"café ✓"`,
		"del\x7f":   `"del\u007F"`,
	} {
		got := tomlString(in)
		if got != want {
			t.Errorf("%q → %s, want %s", in, got, want)
		}
		var v struct{ S string }
		if _, err := toml.Decode("S = "+got, &v); err != nil || v.S != in {
			t.Errorf("%s reads back as %q (%v)", got, v.S, err)
		}
	}
}

// codexHooks reads the -c conch passes Codex the way Codex would: through
// the shell, then as the TOML value of the key "hooks".
type codexHandler struct {
	Type    string
	Command string
	Timeout int
}

type codexConfig struct {
	Hooks map[string]toml.Primitive
}

func codexHooks(t *testing.T, exe string) (map[string][]struct{ Hooks []codexHandler }, map[string]struct {
	TrustedHash string `toml:"trusted_hash"`
}) {
	t.Helper()
	flag := codexHooksFlag(exe)
	out, err := exec.Command("/bin/sh", "-c", `for a in `+flag+`; do printf '%s\n' "$a"; done`).Output()
	if err != nil {
		t.Fatal(err)
	}
	args := strings.SplitN(strings.TrimSuffix(string(out), "\n"), "\n", 2)
	if len(args) != 2 || args[0] != "-c" || !strings.HasPrefix(args[1], "hooks=") {
		t.Fatalf("the shell gives Codex %q", args)
	}
	var cfg codexConfig
	md, err := toml.Decode(args[1], &cfg)
	if err != nil {
		t.Fatalf("Codex could not read %s: %v", args[1], err)
	}
	events := map[string][]struct{ Hooks []codexHandler }{}
	var state map[string]struct {
		TrustedHash string `toml:"trusted_hash"`
	}
	for k, v := range cfg.Hooks {
		if k == "state" {
			err = md.PrimitiveDecode(v, &state)
		} else {
			var groups []struct{ Hooks []codexHandler }
			err = md.PrimitiveDecode(v, &groups)
			events[k] = groups
		}
		if err != nil {
			t.Fatalf("%s: %v", k, err)
		}
	}
	return events, state
}

func TestCodexHooksFlag(t *testing.T) {
	// A path a shell, TOML and JSON each have to be careful with.
	for _, exe := range []string{"/usr/local/bin/conch", `/Users/a b/it's "conch"/conch`} {
		hooks, state := codexHooks(t, exe)
		command := ShellQuote(exe) + " report codex-hook"
		if len(hooks) != len(CodexHookEvents) || len(state) != len(CodexHookEvents) {
			t.Fatalf("%s: hooks %d, trusted %d, want %d each", exe, len(hooks), len(state), len(CodexHookEvents))
		}
		for _, ev := range CodexHookEvents {
			groups := hooks[ev]
			if len(groups) != 1 || len(groups[0].Hooks) != 1 {
				t.Fatalf("%s %s: %+v", exe, ev, groups)
			}
			h := groups[0].Hooks[0]
			if h.Type != "command" || h.Command != command {
				t.Fatalf("%s %s: %+v", exe, ev, h)
			}
			if (ev == "SessionEnd" || ev == "Interrupt") && h.Timeout > 3 {
				t.Errorf("%s: timeout %d; Codex cuts these to 3, and the hash would not match", ev, h.Timeout)
			}
			// Trusted under the key Codex looks it up by, with the hash of
			// exactly the hook given.
			key := "/<session-flags>/config.toml:" + codexEventKey(ev) + ":0:0"
			if s, ok := state[key]; !ok || s.TrustedHash != codexHookHash(ev, command, h.Timeout) {
				t.Errorf("%s: trust %+v under %q", ev, state[key], key)
			}
		}
	}
}

func TestCodexLaunchCarriesHooks(t *testing.T) {
	a := newCodex("/opt/conch")
	for _, args := range []string{"", "'fix it'", "resume --last"} {
		cmd := a.Command("/bin/sh", args)
		line := cmd[2]
		i := strings.Index(line, "exec codex -c 'hooks={")
		if i < 0 {
			t.Fatalf("%q: no hooks in %s", args, line)
		}
		if args != "" && !strings.HasSuffix(line, "}}' "+args) {
			t.Fatalf("%q: conch's flag must come before the person's words: %s", args, line[len(line)-80:])
		}
	}
}

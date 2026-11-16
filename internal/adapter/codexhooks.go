package adapter

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

// CodexHookEvents are the Codex hook events conch listens to: the ones
// that say whether it is working, waiting on the person or done. Model
// requests, compaction and subagents are left out, as they are for
// Claude: they fire inside a turn that tool events already report.
var CodexHookEvents = []string{
	"SessionStart", "SessionEnd", "UserPromptSubmit",
	"PreToolUse", "PostToolUse", "PermissionRequest",
	"Stop", "Interrupt",
}

// codexSessionFlags is where Codex says hooks given with -c come from,
// and so the start of the key their trust is kept under
// (hooks/src/engine/discovery.rs, config_toml_source_path).
const codexSessionFlags = "/<session-flags>/config.toml"

// codexHooksFlag is conch's hooks for one launch of Codex, as a single -c:
// the hooks themselves, and the trust Codex would otherwise ask the person
// to give in /hooks before running them.
//
// Given with -c they sit in Codex's session layer, which adds to the
// person's own hooks rather than replacing them — no file of theirs is
// written. Codex runs a hook it has not been told to trust only with
// --dangerously-bypass-hook-trust, which would run every untrusted hook
// the person has too; so conch trusts its own, by the hash Codex keeps for
// a trusted hook, and nothing else. If Codex ever hashes differently, its
// hooks are skipped as changed and conch reads the screen, as it did
// before.
func codexHooksFlag(exe string) string {
	command := ShellQuote(exe) + " report codex-hook"
	var hooks, state []string
	for _, ev := range CodexHookEvents {
		timeout := codexTimeout(ev)
		hooks = append(hooks, fmt.Sprintf(`%s=[{hooks=[{type="command",command=%s,timeout=%d}]}]`,
			ev, tomlString(command), timeout))
		key := codexSessionFlags + ":" + codexEventKey(ev) + ":0:0"
		state = append(state, fmt.Sprintf(`%s={trusted_hash=%s}`,
			tomlString(key), tomlString(codexHookHash(ev, command, timeout))))
	}
	return "-c " + ShellQuote("hooks={"+strings.Join(hooks, ",")+",state={"+strings.Join(state, ",")+"}}")
}

// codexTimeout is the seconds a hook may take. Codex holds SessionEnd and
// Interrupt to three at most; conch report gives up after three anyway.
func codexTimeout(ev string) int {
	if ev == "SessionEnd" || ev == "Interrupt" {
		return 3
	}
	return 5
}

// codexEventKey is Codex's name for an event in a trust key: snake case,
// PreToolUse → pre_tool_use.
func codexEventKey(ev string) string {
	var b strings.Builder
	for i, r := range ev {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('_')
			}
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}

// codexHookHash is the hash Codex trusts a hook by (hook_hash in
// hooks/src/engine/discovery.rs): SHA-256 of its normalized identity —
// the event, the matcher group with only this handler, the handler with
// its timeout filled in — as JSON with sorted keys and no spaces. Fields
// that are unset are left out, as Codex's TOML step leaves them.
func codexHookHash(ev, command string, timeout int) string {
	identity := map[string]any{
		"event_name": codexEventKey(ev),
		"hooks": []any{map[string]any{
			"type": "command", "command": command, "timeout": timeout, "async": false,
		}},
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // serde_json leaves < > & alone
	if err := enc.Encode(identity); err != nil {
		return ""
	}
	s := strings.TrimSuffix(buf.String(), "\n")
	// Go escapes these two even then; serde_json writes them as they are.
	s = strings.NewReplacer(` `, " ", ` `, " ").Replace(s)
	sum := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// tomlString is s as a TOML basic string.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

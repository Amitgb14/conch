package adapter

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Amitgb14/conch/internal/config"
)

// varName is what a shell takes as a variable's name, and so all that may
// be put into a launch line from a setting.
var varName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// antigravityLaunch is what conch adds to each start of agy, read from
// config.toml then. Always its own title command (conch report
// agy-title), given through --settings, which lasts for that launch and is
// never written to agy's settings.json: agy names its conversation
// nowhere else, which is what the chat reads, and the command runs the
// person's own title command after it.
//
// And with [agents] antigravity_api_key_env set to a variable's name,
// agy is switched to the Gemini API ("modelProvider": "gemini") with
// GEMINI_API_KEY taken from that variable by the person's own login shell
// — conch never sees or keeps the key — and an empty variable stops the
// launch saying which one, rather than leaving agy to fail at its first
// request. Checked against agy 1.5.16: with that setting and no key it
// refuses, naming GEMINI_API_KEY.
func antigravityLaunch(exe, dir string) func() (string, string) {
	return func() (string, string) {
		settings := map[string]any{"title": map[string]string{"command": ShellQuote(exe) + " report agy-title"}}
		before := ""
		if name := AntigravityKeyEnv(dir); name != "" {
			before = `[ -n "$` + name + `" ] || { echo "conch: ` + name + ` is empty: it should hold your Gemini API key ([agents] antigravity_api_key_env)" >&2; exit 1; }; ` +
				`export GEMINI_API_KEY="$` + name + `"; `
			settings["modelProvider"] = "gemini"
		}
		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(settings); err != nil {
			return before, ""
		}
		return before, "--settings " + ShellQuote(strings.TrimSpace(b.String()))
	}
}

// AntigravityKeyEnv is the variable [agents] antigravity_api_key_env in
// dir's config.toml names, when it is one a launch line can use; "" when
// it is unset, unreadable or not a variable's name.
func AntigravityKeyEnv(dir string) string {
	cfg, err := config.LoadFile(filepath.Join(dir, "config.toml"))
	if err != nil || !varName.MatchString(cfg.Agents.AntigravityAPIKeyEnv) {
		return ""
	}
	return cfg.Agents.AntigravityAPIKeyEnv
}

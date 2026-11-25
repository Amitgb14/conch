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
		title := map[string]string{"command": ShellQuote(exe) + " report agy-title"}
		settings := map[string]any{"title": title}
		before := ""
		if name := AntigravityKeyEnv(dir); name != "" {
			before = `[ -n "$` + name + `" ] || { echo "conch: ` + name + ` is empty: it should hold your Gemini API key ([agents] antigravity_api_key_env)" >&2; exit 1; }; ` +
				`export GEMINI_API_KEY="$` + name + `"; `
			settings["modelProvider"] = "gemini"
		}
		base, err := agySettings(settings)
		if err != nil {
			return before, ""
		}
		keyed, err := agySettings(map[string]any{"title": title, "modelProvider": "gemini"})
		if err != nil {
			return before, ""
		}
		// An account of agy's (internal/accounts) is chosen after this line
		// is made, so it arrives as two variables of conch's and is read
		// here: its folder, and the name of the variable holding its key —
		// checked to be a name before the shell is asked for its value. They
		// are unset before agy starts: nothing it runs needs them.
		before += `conch_agy=` + ShellQuote(base) + `; conch_agy_dir="${CONCH_AGY_DIR-}"; ` +
			`if [ -n "${CONCH_AGY_KEY_ENV-}" ]; then ` +
			`case "$CONCH_AGY_KEY_ENV" in [0-9]*|*[!A-Za-z0-9_]*) echo "conch: this account's CONCH_AGY_KEY_ENV is not a variable's name" >&2; exit 1;; esac; ` +
			`eval "conch_agy_key=\"\${$CONCH_AGY_KEY_ENV-}\""; ` +
			`[ -n "$conch_agy_key" ] || { echo "conch: $CONCH_AGY_KEY_ENV is empty: it should hold this account's Gemini API key" >&2; exit 1; }; ` +
			`export GEMINI_API_KEY="$conch_agy_key"; conch_agy=` + ShellQuote(keyed) + `; fi; ` +
			`unset CONCH_AGY_DIR CONCH_AGY_KEY_ENV conch_agy_key; `
		return before, `${conch_agy_dir:+"--gemini_dir=$conch_agy_dir"} --settings "$conch_agy"`
	}
}

// agySettings is settings as --settings takes them: JSON on one line,
// with & < > left as they are.
func agySettings(settings map[string]any) (string, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(settings); err != nil {
		return "", err
	}
	return strings.TrimSpace(b.String()), nil
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

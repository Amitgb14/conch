package adapter

import (
	"path/filepath"
	"regexp"

	"github.com/Amitgb14/conch/internal/config"
)

// varName is what a shell takes as a variable's name, and so all that may
// be put into a launch line from a setting.
var varName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// antigravityModelProvider switches agy to the Gemini API for one launch;
// --settings is never saved to its settings.json (agy 1.5.16 --help, and
// checked: without the key it refuses and names GEMINI_API_KEY).
const antigravityModelProvider = `{"modelProvider":"gemini"}`

// antigravityLaunch reads [agents] antigravity_api_key_env at each start.
// Set to a variable's name, agy is started with GEMINI_API_KEY taken from
// it by the person's own login shell — conch never sees or keeps the key
// — and an empty variable stops the launch saying which one, rather than
// leaving agy to fail at its first request.
func antigravityLaunch(dir string) func() (string, string) {
	return func() (string, string) {
		cfg, err := config.LoadFile(filepath.Join(dir, "config.toml"))
		if err != nil {
			return "", ""
		}
		name := cfg.Agents.AntigravityAPIKeyEnv
		if !varName.MatchString(name) {
			return "", ""
		}
		before := `[ -n "$` + name + `" ] || { echo "conch: ` + name + ` is empty: it should hold your Gemini API key ([agents] antigravity_api_key_env)" >&2; exit 1; }; ` +
			`export GEMINI_API_KEY="$` + name + `"; `
		return before, "--settings " + ShellQuote(antigravityModelProvider)
	}
}

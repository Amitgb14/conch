package accounts

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
)

// SignedIn reports whether agent's account in dir has been signed in to,
// and whether conch could tell. dir is the account's folder, or "" for
// the agent's own, which is looked for where the agent keeps it in home
// (env says whether CODEX_HOME moves Codex's). It reads only what says an
// account exists, never a token: Claude Code keeps its login in the
// Keychain and records the account in .claude.json (its oauthAccount),
// beside the folder for its own account and inside it for one in
// CLAUDE_CONFIG_DIR; Codex writes auth.json into its folder.
func SignedIn(agent, dir, home string, env func(string) string) (signedIn, known bool) {
	switch agent {
	case "claude":
		file := filepath.Join(dir, ".claude.json")
		if dir == "" {
			if home == "" {
				return false, false
			}
			file = filepath.Join(home, ".claude.json")
		}
		return claudeSignedIn(file)
	case "codex":
		if dir == "" {
			dir = env("CODEX_HOME")
		}
		if dir == "" {
			if home == "" {
				return false, false
			}
			dir = filepath.Join(home, ".codex")
		}
		st, err := os.Stat(filepath.Join(dir, "auth.json"))
		if err != nil {
			return false, os.IsNotExist(err)
		}
		return st.Mode().IsRegular() && st.Size() > 0, true
	}
	return false, false
}

// claudeSignedIn reads .claude.json for an account: missing is not
// signed in; unreadable or not JSON is not known.
func claudeSignedIn(file string) (bool, bool) {
	f, err := os.Open(file)
	if err != nil {
		return false, os.IsNotExist(err)
	}
	defer f.Close()
	// The file holds every project's history too, so it can be large;
	// enough to find the account, and no more.
	var c struct {
		OAuthAccount *struct {
			AccountUUID string `json:"accountUuid"`
		} `json:"oauthAccount"`
	}
	if err := json.NewDecoder(io.LimitReader(f, 64<<20)).Decode(&c); err != nil {
		return false, false
	}
	return c.OAuthAccount != nil && c.OAuthAccount.AccountUUID != "", true
}

package config

import (
	"path/filepath"
	"strings"
)

// TrackersCfg is where a task's ticket can come from besides a GitHub
// issue: Linear and Jira Cloud, each with a personal key. A key is never
// required here — APIKeyEnv names the variable holding it, and on a Mac
// the Keychain (service conch-mcp, the variable's name as the account)
// is read when the environment lacks it.
type TrackersCfg struct {
	Linear LinearCfg `toml:"linear"`
	Jira   JiraCfg   `toml:"jira"`
	// Projects say which tracker, and which team or project in it, a
	// conch project's tickets come from. Last, as an array of tables.
	Projects []TrackerProject `toml:"projects,omitempty"`
}

// LinearCfg is Linear, reached with a personal API key.
type LinearCfg struct {
	// APIKey is the key itself, kept here as it is (config.toml is 0600);
	// empty reads the variable APIKeyEnv names.
	APIKey string `toml:"api_key,omitempty"`
	// APIKeyEnv names the variable holding the key; "" is LINEAR_API_KEY.
	APIKeyEnv string `toml:"api_key_env,omitempty"`
	// APIURL overrides Linear's GraphQL endpoint.
	APIURL string `toml:"api_url,omitempty"`
	// Team is the team key whose tickets are listed (ENG); "" is every
	// team the key can see.
	Team string `toml:"team,omitempty"`
}

// JiraCfg is Jira Cloud, reached with an Atlassian account's email and an
// API token.
type JiraCfg struct {
	// URL is the site, https://acme.atlassian.net.
	URL   string `toml:"url,omitempty"`
	Email string `toml:"email,omitempty"`
	// APIToken is the token itself; empty reads the variable APITokenEnv
	// names.
	APIToken string `toml:"api_token,omitempty"`
	// APITokenEnv names the variable holding the token; "" is
	// JIRA_API_TOKEN.
	APITokenEnv string `toml:"api_token_env,omitempty"`
	// Project is the project key whose tickets are listed (PROJ); "" is
	// every project the account can see.
	Project string `toml:"project,omitempty"`
}

// TrackerProject ties a conch project to a tracker.
type TrackerProject struct {
	// Project is the conch project, by name or path.
	Project string `toml:"project"`
	// Tracker is linear or jira.
	Tracker string `toml:"tracker"`
	// Key is the Linear team's key or the Jira project's key (ENG, PROJ);
	// "" is the tracker's own default above.
	Key string `toml:"key,omitempty"`
}

// ForProject is the setting for the project named name at root, the first
// that names it.
func (t TrackersCfg) ForProject(name, root string) (TrackerProject, bool) {
	for _, p := range t.Projects {
		want := strings.TrimSpace(p.Project)
		if want == "" {
			continue
		}
		if want == name || (root != "" && filepath.Clean(want) == filepath.Clean(root)) {
			return p, true
		}
	}
	return TrackerProject{}, false
}

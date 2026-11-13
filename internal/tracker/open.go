package tracker

import (
	"fmt"
	"strings"

	"github.com/Amitgb14/conch/internal/config"
)

// The variables a key is read from when config.toml names none.
const (
	LinearKeyEnv = "LINEAR_API_KEY"
	JiraTokenEnv = "JIRA_API_TOKEN"
)

// Lookup reads a variable's value: the environment, then wherever else the
// caller keeps secrets (a server on a Mac, the Keychain). "" is none.
type Lookup func(name string) string

func linearKey(c config.LinearCfg, look Lookup) (string, string) {
	env := firstOf(c.APIKeyEnv, LinearKeyEnv)
	if c.APIKey != "" {
		return c.APIKey, env
	}
	return look(env), env
}

func jiraToken(c config.JiraCfg, look Lookup) (string, string) {
	env := firstOf(c.APITokenEnv, JiraTokenEnv)
	if c.APIToken != "" {
		return c.APIToken, env
	}
	return look(env), env
}

// Configured are the trackers cfg has enough of to reach: a key for
// Linear, a site for Jira (whose email and token are asked for when it is
// opened, so a half-set Jira says what it lacks rather than vanishing).
func Configured(cfg config.TrackersCfg, look Lookup) []string {
	var out []string
	if k, _ := linearKey(cfg.Linear, look); k != "" {
		out = append(out, Linear)
	}
	if strings.TrimSpace(cfg.Jira.URL) != "" {
		out = append(out, Jira)
	}
	return out
}

// Open is the tracker named, as cfg sets it up.
func Open(cfg config.TrackersCfg, name string, look Lookup) (Tracker, error) {
	switch name {
	case Linear:
		k, env := linearKey(cfg.Linear, look)
		if k == "" {
			return nil, fmt.Errorf("Linear needs a personal API key (Linear → Settings → Security & access): put it in %s, or in the Keychain as conch-mcp %s, or set [trackers.linear] api_key", env, env)
		}
		return NewLinear(cfg.Linear.APIURL, k), nil
	case Jira:
		c := cfg.Jira
		if strings.TrimSpace(c.URL) == "" || strings.TrimSpace(c.Email) == "" {
			return nil, fmt.Errorf("Jira needs its site and your email: set [trackers.jira] url and email in config.toml")
		}
		tok, env := jiraToken(c, look)
		if tok == "" {
			return nil, fmt.Errorf("Jira needs an API token (id.atlassian.com → Security → API tokens): put it in %s, or in the Keychain as conch-mcp %s, or set [trackers.jira] api_token", env, env)
		}
		return NewJira(c.URL, c.Email, tok), nil
	}
	return nil, fmt.Errorf("conch knows the trackers %s, not %q", strings.Join(Names, " and "), name)
}

// Choose says which tracker a project's tickets come from, and the team or
// project in it: what ref names (linear:ENG-1, an address), else the
// project's [[trackers.projects]] entry, else the one tracker set up.
// ref may be "" when listing.
func Choose(cfg config.TrackersCfg, project, root, ref string, look Lookup) (name, scope string, err error) {
	if ref != "" {
		if name, _, err = ParseRef(ref); err != nil {
			return "", "", err
		}
	}
	p, ok := cfg.ForProject(project, root)
	if name == "" && ok {
		name = strings.ToLower(strings.TrimSpace(p.Tracker))
	}
	if name == "" {
		set := Configured(cfg, look)
		switch len(set) {
		case 0:
			return "", "", fmt.Errorf("no tracker is set up: give conch a Linear key (%s) or a Jira site ([trackers.jira] url) — see conch help task", LinearKeyEnv)
		case 1:
			name = set[0]
		default:
			return "", "", fmt.Errorf("both Linear and Jira are set up, so say which: linear:KEY-1 or jira:KEY-1, or tie %s to one with [[trackers.projects]]", firstOf(project, "the project"))
		}
	}
	if name != Linear && name != Jira {
		return "", "", fmt.Errorf("conch knows the trackers %s, not %q", strings.Join(Names, " and "), name)
	}
	return name, Scope(cfg, project, root, name), nil
}

// Scope is the team or project of the tracker named whose tickets a
// project lists: its [[trackers.projects]] key, else the tracker's own.
func Scope(cfg config.TrackersCfg, project, root, name string) string {
	if p, ok := cfg.ForProject(project, root); ok && strings.EqualFold(p.Tracker, name) && p.Key != "" {
		return p.Key
	}
	switch name {
	case Linear:
		return cfg.Linear.Team
	case Jira:
		return cfg.Jira.Project
	}
	return ""
}

// Title is the tracker's name as it calls itself.
func Title(name string) string {
	switch name {
	case Linear:
		return "Linear"
	case Jira:
		return "Jira"
	}
	return name
}

func firstOf(s ...string) string {
	for _, v := range s {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

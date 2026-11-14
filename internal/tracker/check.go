package tracker

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/Amitgb14/conch/internal/config"
)

// Account is who a tracker's key belongs to and what it can see: what
// Settings shows to say a key works before anything is asked of it.
type Account struct {
	Who       string `json:"who,omitempty"`
	Email     string `json:"email,omitempty"`
	Workspace string `json:"workspace,omitempty"`
	Scopes    []Area `json:"scopes,omitempty"`
}

// Area is a Linear team or a Jira project the key can see.
type Area struct {
	Key  string `json:"key"`
	Name string `json:"name,omitempty"`
}

// Checker is a tracker that can say whose its key is.
type Checker interface {
	Check(ctx context.Context) (Account, error)
}

// Check asks Linear whose the key is, its workspace and its teams.
func (l *linear) Check(ctx context.Context) (Account, error) {
	var out struct {
		Viewer struct {
			Name         string `json:"name"`
			Email        string `json:"email"`
			Organization *struct {
				Name string `json:"name"`
			} `json:"organization"`
		} `json:"viewer"`
		Teams struct {
			Nodes []Area `json:"nodes"`
		} `json:"teams"`
	}
	const q = `query { viewer { name email organization { name } } teams(first: 100) { nodes { key name } } }`
	if err := l.query(ctx, q, nil, &out); err != nil {
		return Account{}, err
	}
	a := Account{Who: out.Viewer.Name, Email: out.Viewer.Email, Scopes: out.Teams.Nodes}
	if out.Viewer.Organization != nil {
		a.Workspace = out.Viewer.Organization.Name
	}
	return a, nil
}

// Check asks Jira whose the token is and the projects it can see; the
// workspace is the site's own name.
func (j *jira) Check(ctx context.Context) (Account, error) {
	var me struct {
		DisplayName  string `json:"displayName"`
		EmailAddress string `json:"emailAddress"`
	}
	if err := j.call(ctx, http.MethodGet, "/rest/api/3/myself", nil, &me); err != nil {
		return Account{}, err
	}
	a := Account{Who: me.DisplayName, Email: me.EmailAddress, Workspace: j.site}
	if u, err := url.Parse(j.site); err == nil && u.Host != "" {
		a.Workspace = u.Host
	}
	var projects struct {
		Values []Area `json:"values"`
	}
	// Projects are a nicety beside the account: a token that may not list
	// them still works for the tickets it can see.
	if err := j.call(ctx, http.MethodGet, "/rest/api/3/project/search?maxResults=100", nil, &projects); err == nil {
		a.Scopes = projects.Values
	}
	return a, nil
}

// KeyFrom says where a tracker's key is read from — config.toml, a
// variable, or the Keychain — never the key itself. inEnv says whether a
// variable is set in this process's environment.
func KeyFrom(name string, cfg config.TrackersCfg, inEnv func(string) bool) string {
	var inFile bool
	var env string
	switch name {
	case Linear:
		inFile, env = cfg.Linear.APIKey != "", firstOf(cfg.Linear.APIKeyEnv, LinearKeyEnv)
	case Jira:
		inFile, env = cfg.Jira.APIToken != "", firstOf(cfg.Jira.APITokenEnv, JiraTokenEnv)
	default:
		return ""
	}
	switch {
	case inFile:
		return "config.toml"
	case inEnv(env):
		return "$" + env
	}
	return "Keychain (" + strings.TrimSpace(env) + ")"
}

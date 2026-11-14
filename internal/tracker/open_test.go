package tracker

import (
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/config"
)

func vars(m map[string]string) Lookup { return func(n string) string { return m[n] } }

func TestConfiguredAndOpen(t *testing.T) {
	none := vars(nil)
	if got := Configured(config.TrackersCfg{}, none); len(got) != 0 {
		t.Errorf("nothing set: %v", got)
	}
	cfg := config.TrackersCfg{Linear: config.LinearCfg{APIKeyEnv: "MY_KEY"}, Jira: config.JiraCfg{URL: "https://acme.atlassian.net"}}
	if got := strings.Join(Configured(cfg, vars(map[string]string{"MY_KEY": "k"})), ","); got != "linear,jira" {
		t.Errorf("configured = %s", got)
	}
	// The default variable is read when none is named.
	if got := Configured(config.TrackersCfg{}, vars(map[string]string{"LINEAR_API_KEY": "k"})); len(got) != 1 || got[0] != "linear" {
		t.Errorf("default variable: %v", got)
	}
	// A key in the file wins over the variable.
	tr, err := Open(config.TrackersCfg{Linear: config.LinearCfg{APIKey: "file", APIURL: "http://x"}}, "linear", vars(map[string]string{"LINEAR_API_KEY": "env"}))
	if err != nil || tr.(*linear).key != "file" || tr.(*linear).url != "http://x" {
		t.Errorf("Open linear: %+v, %v", tr, err)
	}
	if _, err := Open(config.TrackersCfg{}, "linear", none); err == nil || !strings.Contains(err.Error(), "LINEAR_API_KEY") {
		t.Errorf("no key: %v", err)
	}
	if _, err := Open(cfg, "jira", none); err == nil || !strings.Contains(err.Error(), "email") {
		t.Errorf("jira without email: %v", err)
	}
	cfg.Jira.Email = "me@x"
	if _, err := Open(cfg, "jira", none); err == nil || !strings.Contains(err.Error(), "JIRA_API_TOKEN") {
		t.Errorf("jira without token: %v", err)
	}
	tr, err = Open(cfg, "jira", vars(map[string]string{"JIRA_API_TOKEN": "t"}))
	if err != nil || tr.Name() != "jira" || tr.(*jira).token != "t" {
		t.Errorf("Open jira: %+v, %v", tr, err)
	}
	if _, err := Open(cfg, "github", none); err == nil {
		t.Error("an unknown tracker opened")
	}
}

func TestChoose(t *testing.T) {
	both := config.TrackersCfg{
		Linear: config.LinearCfg{APIKey: "k", Team: "ENG"},
		Jira:   config.JiraCfg{URL: "https://acme.atlassian.net", Project: "PROJ"},
		Projects: []config.TrackerProject{
			{Project: "web", Tracker: "jira", Key: "WEB"},
			{Project: "app", Tracker: "linear"},
		},
	}
	none := vars(nil)
	cases := []struct {
		cfg                 config.TrackersCfg
		project, ref        string
		name, scope, errHas string
	}{
		{both, "web", "WEB-1", "jira", "WEB", ""},
		{both, "web", "linear:ENG-1", "linear", "ENG", ""}, // the ref says otherwise: the tracker's own default
		{both, "app", "", "linear", "ENG", ""},             // no key in the project: the tracker's
		{both, "other", "X-1", "", "", "say which"},
		{both, "other", "jira:X-1", "jira", "PROJ", ""},
		{both, "other", "https://linear.app/a/issue/ENG-2", "linear", "ENG", ""},
		{config.TrackersCfg{Jira: config.JiraCfg{URL: "https://a.atlassian.net"}}, "x", "A-1", "jira", "", ""},
		{config.TrackersCfg{}, "x", "A-1", "", "", "no tracker is set up"},
		{config.TrackersCfg{}, "x", "nonsense", "", "", "not a ticket"},
	}
	for _, c := range cases {
		name, scope, err := Choose(c.cfg, c.project, "/src/"+c.project, c.ref, none)
		if c.errHas != "" {
			if err == nil || !strings.Contains(err.Error(), c.errHas) {
				t.Errorf("Choose(%s, %q): err %v, want %q", c.project, c.ref, err, c.errHas)
			}
			continue
		}
		if err != nil || name != c.name || scope != c.scope {
			t.Errorf("Choose(%s, %q) = %q, %q, %v; want %q, %q", c.project, c.ref, name, scope, err, c.name, c.scope)
		}
	}
}

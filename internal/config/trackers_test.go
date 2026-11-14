package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTrackersLoadAndSave(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CONCH_HOME", home)
	raw := `
[trackers.linear]
api_key_env = "MY_LINEAR"
team = "ENG"

[trackers.jira]
url = "https://acme.atlassian.net"
email = "me@acme.test"
project = "PROJ"

[[trackers.projects]]
project = "conch"
tracker = "linear"
key = "CON"
`
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	tr := cfg.Trackers
	if tr.Linear.APIKeyEnv != "MY_LINEAR" || tr.Linear.Team != "ENG" {
		t.Errorf("linear = %+v", tr.Linear)
	}
	if tr.Jira.URL != "https://acme.atlassian.net" || tr.Jira.Email != "me@acme.test" || tr.Jira.Project != "PROJ" {
		t.Errorf("jira = %+v", tr.Jira)
	}
	if len(tr.Projects) != 1 || tr.Projects[0] != (TrackerProject{Project: "conch", Tracker: "linear", Key: "CON"}) {
		t.Errorf("projects = %+v", tr.Projects)
	}
	// What the settings screen writes back loads the same.
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	again, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if again.Trackers.Jira != tr.Jira || again.Trackers.Linear != tr.Linear || len(again.Trackers.Projects) != 1 {
		t.Errorf("after save = %+v", again.Trackers)
	}
}

func TestTrackerForProject(t *testing.T) {
	tr := TrackersCfg{Projects: []TrackerProject{
		{Project: "web", Tracker: "jira", Key: "WEB"},
		{Project: "/src/api", Tracker: "linear", Key: "API"},
		{Project: "", Tracker: "linear"}, // no project: matches nothing
	}}
	cases := []struct {
		name, root string
		want       TrackerProject
		ok         bool
	}{
		{"web", "/src/web", TrackerProject{Project: "web", Tracker: "jira", Key: "WEB"}, true},
		{"api", "/src/api/", TrackerProject{Project: "/src/api", Tracker: "linear", Key: "API"}, true},
		{"other", "/src/other", TrackerProject{}, false},
		{"", "", TrackerProject{}, false},
	}
	for _, c := range cases {
		got, ok := tr.ForProject(c.name, c.root)
		if ok != c.ok || got != c.want {
			t.Errorf("ForProject(%q, %q) = %+v, %v; want %+v, %v", c.name, c.root, got, ok, c.want, c.ok)
		}
	}
}

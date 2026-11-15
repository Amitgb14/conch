package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestTeamFor(t *testing.T) {
	teams := []Team{
		{Name: "Backend", Projects: []string{"api", "worker"}, Branches: []string{"conch/cloud-*"}},
		{Name: "UI", Projects: []string{"web"}, Branches: []string{"ui/*", "*-redesign", "*desktop*"}},
		{Name: ""}, // a nameless team never takes anyone
		{Name: "Everything", Branches: []string{"*"}},
	}
	for _, c := range []struct{ set, project, branch, want string }{
		{"", "api", "main", "Backend"},
		{"", "API", "", "Backend"}, // a project name in any case
		{"", "conch", "conch/cloud-agent", "Backend"},
		{"", "conch", "ui/nav/dark", "UI"}, // * takes slashes too
		{"", "conch", "checkout-redesign", "UI"},
		{"", "conch", "conch/readme-desktop-shots", "UI"},
		{"", "web", "conch/cloud-x", "Backend"}, // the first team that matches, in order
		{"", "conch", "anything", "Everything"},
		{"", "", "", ""},                // nothing to match: no team, not "Everything"
		{"UX", "api", "main", "UX"},     // by hand wins over every rule
		{"Gone", "api", "main", "Gone"}, // even a team since removed
		{NoTeam, "api", "main", ""},     // kept out on purpose
	} {
		if got := TeamFor(teams, c.set, c.project, c.branch); got != c.want {
			t.Errorf("TeamFor(%q, %q, %q) = %q, want %q", c.set, c.project, c.branch, got, c.want)
		}
	}
	if TeamFor(nil, "", "api", "main") != "" {
		t.Fatal("no teams, no team")
	}
}

func TestGlobMatch(t *testing.T) {
	for _, c := range []struct {
		p, s string
		ok   bool
	}{
		{"main", "main", true}, {"main", "mainline", false},
		{"ui/*", "ui/", true}, {"ui/*", "ux/a", false},
		{"*-copy", "a-copy", true}, {"*-copy", "-copy", true}, {"*-copy", "copy", false},
		{"a*b*c", "abc", true}, {"a*b*c", "axxbyyc", true}, {"a*b*c", "acb", false},
		{"a*a", "a", false}, {"*", "", true}, {"**", "x/y", true},
	} {
		if globMatch(c.p, c.s) != c.ok {
			t.Errorf("globMatch(%q, %q) != %v", c.p, c.s, c.ok)
		}
	}
}

func TestTeamsRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CONCH_HOME", home)
	cfg := Default()
	cfg.Teams = []Team{{Name: "Backend", Color: "blue", Projects: []string{"api"}}, {Name: "UI", Branches: []string{"ui/*"}}}
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil || !reflect.DeepEqual(got.Teams, cfg.Teams) {
		t.Fatalf("%+v %v", got.Teams, err)
	}
	b, _ := os.ReadFile(filepath.Join(home, "config.toml"))
	if !strings.Contains(string(b), "[[teams]]") {
		t.Fatalf("saved:\n%s", b)
	}
}

package server_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/integration"
	"github.com/Amitgb14/conch/internal/proto"
)

// A repository's .conch/integrations.toml is asked about once — and again
// only when it names something new — before detection takes it; saving
// writes names and nothing else; Settings add the tracker and sandboxes.
func TestIntegrationTeam(t *testing.T) {
	r := newCIRig(t, true, circleCheck)
	if miss := r.c.MissingCapabilities([]string{proto.CapIntegrationTeam}); len(miss) > 0 {
		t.Fatalf("capability missing: %v", miss)
	}
	call := func(method string, params any) (proto.IntegrationDetect, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		var got proto.IntegrationDetect
		err := r.c.Call(ctx, method, params, &got)
		return got, err
	}
	detect := func() proto.IntegrationDetect {
		t.Helper()
		got, err := call(proto.MethodIntegrationDetect, proto.IntegrationDetectParams{ProjectID: r.proj.ID})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	team := func(action string, names ...string) proto.IntegrationDetect {
		t.Helper()
		got, err := call(proto.MethodIntegrationTeam, proto.IntegrationTeamParams{ProjectID: r.proj.ID, Action: action, Names: names})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	use := func(d proto.IntegrationDetect, name string) (proto.IntegrationUse, bool) {
		for _, u := range d.Integrations {
			if u.Name == name {
				return u, true
			}
		}
		return proto.IntegrationUse{}, false
	}
	file := filepath.Join(r.proj.Path, integration.TeamFile)

	if d := detect(); d.Team != nil {
		t.Fatalf("no file, a team: %+v", d.Team)
	}
	if _, err := call(proto.MethodIntegrationTeam, proto.IntegrationTeamParams{ProjectID: r.proj.ID, Action: proto.TeamUse}); err == nil {
		t.Fatal("used a file that is not there")
	}

	// The repository says so: asked, not taken.
	os.MkdirAll(filepath.Dir(file), 0o755)
	os.WriteFile(file, []byte(`uses = ["circleci", "jira"]`), 0o644)
	d := detect()
	if d.Team == nil || d.Team.State != "ask" || strings.Join(d.Team.Names, ",") != "circleci,jira" || d.Team.Path != ".conch/integrations.toml" {
		t.Fatalf("asked: %+v", d.Team)
	}
	if _, ok := use(d, "jira"); ok {
		t.Fatal("taken before it was asked")
	}
	// No, once: still no, until it names something new.
	if d := team(proto.TeamIgnore); d.Team.State != "ignored" {
		t.Fatalf("ignored: %+v", d.Team)
	}
	os.WriteFile(file, []byte(`uses = ["circleci"]`), 0o644)
	if d := detect(); d.Team.State != "ignored" {
		t.Fatalf("fewer names asked again: %+v", d.Team)
	}
	os.WriteFile(file, []byte(`uses = ["circleci", "jira", "gitlab-ci"]`), 0o644)
	if d := detect(); d.Team.State != "ask" {
		t.Fatalf("a new name not asked: %+v", d.Team)
	}
	d = team(proto.TeamUse)
	u, ok := use(d, "jira")
	if d.Team.State != "used" || !ok || !u.Team || u.Why != integration.TeamFile || u.Kind != "tickets" || u.Connected {
		t.Fatalf("used: %+v %+v", d.Team, u)
	}
	// What its checks already showed keeps its own reason.
	if u, _ := use(d, "circleci"); u.Team || !strings.HasPrefix(u.Why, "check ") {
		t.Fatalf("circleci: %+v", u)
	}

	// Saved from the sheet: names only, and taken without asking.
	d = team(proto.TeamSave, "github-actions", "circleci")
	data, _ := os.ReadFile(file)
	if !strings.Contains(string(data), `uses = ["circleci", "github-actions"]`) || d.Team.State != "used" {
		t.Fatalf("saved: %s %+v", data, d.Team)
	}
	if _, err := call(proto.MethodIntegrationTeam, proto.IntegrationTeamParams{ProjectID: r.proj.ID, Action: proto.TeamSave, Names: []string{"CIRCLECI_TOKEN=x"}}); err == nil {
		t.Fatal("saved something that is not a name")
	}

	// Damaged: said, not used.
	os.WriteFile(file, []byte(`uses = [`), 0o644)
	if d := detect(); d.Team == nil || d.Team.Problem == "" || d.Team.State != "ignored" {
		t.Fatalf("damaged: %+v", d.Team)
	}
	if d := team(proto.TeamRemove); d.Team != nil {
		t.Fatalf("removed: %+v", d.Team)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("still there: %v", err)
	}
	if _, err := call(proto.MethodIntegrationTeam, proto.IntegrationTeamParams{ProjectID: r.proj.ID, Action: "steal"}); err == nil {
		t.Fatal("an unknown action")
	}
	if _, err := call(proto.MethodIntegrationTeam, proto.IntegrationTeamParams{ProjectID: "nope", Action: proto.TeamRemove}); err == nil {
		t.Fatal("an unknown project")
	}

	// Settings: the project's tracker with its scope and site, and the
	// sandboxes set up.
	os.WriteFile(filepath.Join(os.Getenv("CONCH_HOME"), "config.toml"), []byte(`
[trackers.jira]
url = "https://tracker.example.com"
email = "a@example.com"
[[trackers.projects]]
project = "`+r.proj.Name+`"
tracker = "jira"
key = "RGW"
[sandbox.daytona]
snapshot = "x"
[sandbox.nobody-knows]
snapshot = "y"
`), 0o600)
	t.Setenv("JIRA_API_TOKEN", "")
	d = detect()
	j, _ := use(d, "jira")
	if j.Label != "Jira (RGW)" || j.Why != "chosen in Settings · tracker.example.com" || j.Kind != "tickets" || j.Connected || j.KeyEnv != "JIRA_API_TOKEN" {
		t.Fatalf("jira: %+v", j)
	}
	if b, ok := use(d, "daytona"); !ok || b.Kind != "sandbox" || b.Label != "Daytona" {
		t.Fatalf("daytona: %+v", d.Integrations)
	}
	if _, ok := use(d, "nobody-knows"); ok {
		t.Fatal("a sandbox conch does not know")
	}
	t.Setenv("JIRA_API_TOKEN", "tok")
	if j, _ := use(detect(), "jira"); !j.Connected {
		t.Fatalf("jira with its token: %+v", j)
	}
}

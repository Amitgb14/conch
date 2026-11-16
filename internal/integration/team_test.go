package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTeamFile(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	if got, err := ReadTeam(root); got != nil || err != nil {
		t.Fatalf("no file: %v %v", got, err)
	}
	if got, err := ReadTeam(""); got != nil || err != nil {
		t.Fatalf("no checkout: %v %v", got, err)
	}

	if err := WriteTeam(root, []string{"circleci", " github-actions", "circleci"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(root, TeamFile))
	if !strings.Contains(string(data), `uses = ["circleci", "github-actions"]`) || !strings.Contains(string(data), "never here") {
		t.Fatalf("written:\n%s", data)
	}
	got, err := ReadTeam(root)
	if err != nil || strings.Join(got, ",") != "circleci,github-actions" {
		t.Fatalf("read back: %v %v", got, err)
	}

	// Somebody put a key in it: it is not read, and a save drops it.
	os.WriteFile(filepath.Join(root, TeamFile), []byte("uses = [\"jira\"]\nCIRCLECI_TOKEN = \"sekret\"\n[keys]\nx = \"y\"\n"), 0o644)
	if got, err := ReadTeam(root); err != nil || strings.Join(got, ",") != "jira" {
		t.Fatalf("with extra keys: %v %v", got, err)
	}
	WriteTeam(root, got)
	if data, _ := os.ReadFile(filepath.Join(root, TeamFile)); strings.Contains(string(data), "sekret") {
		t.Fatalf("a key survived a save:\n%s", data)
	}

	// Malformed, a name that cannot be one, an empty list.
	for body, want := range map[string]string{
		"uses = [":             "integrations.toml",
		`uses = ["Circle CI"]`: "not an integration name",
		`uses = "circleci"`:    "integrations.toml",
	} {
		os.WriteFile(filepath.Join(root, TeamFile), []byte(body), 0o644)
		if _, err := ReadTeam(root); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v", body, err)
		}
	}
	os.WriteFile(filepath.Join(root, TeamFile), []byte("uses = []\n"), 0o644)
	if got, err := ReadTeam(root); got != nil || err != nil {
		t.Fatalf("empty: %v %v", got, err)
	}
	if err := WriteTeam(root, []string{"../../etc"}); err == nil {
		t.Fatal("a path written as a name")
	}

	if err := RemoveTeam(root); err != nil {
		t.Fatal(err)
	}
	if err := RemoveTeam(root); err != nil {
		t.Fatalf("removing nothing: %v", err)
	}
	if WriteTeam("", nil) == nil || RemoveTeam("") == nil {
		t.Fatal("no checkout")
	}
}

func TestWithTeam(t *testing.T) {
	isolate(t)
	files, _ := All()
	ds := []Detection{{Name: "github", Label: "GitHub", Kind: KindHost, Why: "github.com remote", Installed: true}}
	got := WithTeam(ds, []string{"github", "circleci", "jira", "nobody-knows"}, files)
	if s := detected(got); s != "github:github.com remote+ circleci:.conch/integrations.toml+ jira:.conch/integrations.toml+" {
		t.Fatalf("got %s", s)
	}
	if got[1].Kind != KindCI || got[2].Kind != KindTickets || got[1].Label != "CircleCI" {
		t.Fatalf("kinds: %+v", got)
	}
	if len(WithTeam(nil, nil, files)) != 0 {
		t.Fatal("nothing named")
	}
	if Label("jira", files) != "Jira" || Label("github-issues", nil) != "GitHub Issues" || Label("nobody-knows", files) != "nobody-knows" {
		t.Fatal("labels of conch's own")
	}
	if !Subset([]string{"a"}, []string{"a", "b"}) || Subset([]string{"c"}, []string{"a"}) || !Subset(nil, nil) {
		t.Fatal("subset")
	}
}

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeConfig puts a config.toml in a temporary conch home and loads it.
func writeConfig(t *testing.T, body string) Config {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CONCH_HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", "")
	if body != "" {
		if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return cfg
}

func TestActionsLoad(t *testing.T) {
	cfg := writeConfig(t, `
[[actions]]
name = "Run tests"
run = "go test ./..."
on = ["branch", "project"]

[[actions]]
name = "Open the folder"
run = "open ."
keep = false
`)
	if len(cfg.Actions) != 2 {
		t.Fatalf("read %d actions: %+v", len(cfg.Actions), cfg.Actions)
	}
	a := cfg.Actions[0]
	if a.Name != "Run tests" || a.Run != "go test ./..." {
		t.Errorf("first action: %+v", a)
	}
	if !a.Keeps() {
		t.Error("an action that said nothing about keep should keep its terminal")
	}
	if cfg.Actions[1].Keeps() {
		t.Error("keep = false was not read")
	}
	// A file with no actions at all, and no file at all.
	if got := writeConfig(t, "[ui]\ntheme = \"nord\"\n").Actions; got != nil {
		t.Errorf("actions out of nowhere: %+v", got)
	}
	if got := writeConfig(t, "").Actions; got != nil {
		t.Errorf("no config.toml, yet actions: %+v", got)
	}
}

func TestActionsOn(t *testing.T) {
	cfg := writeConfig(t, `
[[actions]]
name = "everywhere"
run = "true"

[[actions]]
name = "branches"
run = "true"
on = ["  BRANCH  "]

[[actions]]
name = "no command"
run = "   "

[[actions]]
run = "nameless"
`)
	// In the order written, and the half-written ones left out.
	if got := names(cfg.ActionsOn(ActionOnBranch)); !eq(got, []string{"everywhere", "branches"}) {
		t.Errorf("on a branch: %v", got)
	}
	// Case and spaces in `on` are somebody writing TOML, not a mistake.
	if got := names(cfg.ActionsOn(ActionOnProject)); !eq(got, []string{"everywhere"}) {
		t.Errorf("on a project: %v", got)
	}
	for _, place := range ActionPlaces {
		if len(cfg.ActionsOn(place)) == 0 {
			t.Errorf("%s offers nothing at all", place)
		}
	}
	// A place that is not a place, and the zero config.
	if got := cfg.ActionsOn("elsewhere"); len(got) != 1 || got[0].Name != "everywhere" {
		t.Errorf("an unknown place: %v", names(got))
	}
	if got := (Config{}).ActionsOn(ActionOnPane); got != nil {
		t.Errorf("nothing configured, yet %v", names(got))
	}
}

func TestActionProblems(t *testing.T) {
	cfg := writeConfig(t, `
[[actions]]
name = "Fine"
run = "true"

[[actions]]
name = "Nothing to run"

[[actions]]
run = "echo nameless"

[[actions]]

[[actions]]
name = "Fine"
run = "true"

[[actions]]
name = "Odd place"
run = "true"
on = ["worktree"]
`)
	got := strings.Join(cfg.ActionProblems(), "\n")
	for _, want := range []string{
		"Nothing to run has no command",
		"the third action has no name: echo nameless",
		"action 4 has neither a name nor a command",
		"two actions are called Fine",
		"Odd place is offered on worktree, which is not one of pane, branch, project, machine",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("no complaint about %q in:\n%s", want, got)
		}
	}
	// A clean file complains about nothing.
	clean := writeConfig(t, "[[actions]]\nname = \"Fine\"\nrun = \"true\"\non = [\"pane\"]\n")
	if got := clean.ActionProblems(); len(got) != 0 {
		t.Errorf("complaints about a good file: %v", got)
	}
	if got := (Config{}).ActionProblems(); len(got) != 0 {
		t.Errorf("complaints about nothing: %v", got)
	}
}

// The settings screen rewrites config.toml whole, so an action written by
// hand has to survive conch saving a setting.
func TestActionsSurviveSave(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CONCH_HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", "")
	keep := false
	cfg := Default()
	cfg.Actions = []Action{
		{Name: "Run tests", Run: "go test ./...", On: []string{"branch"}},
		{Name: "Open the folder", Run: "open .", Keep: &keep},
	}
	if err := Save(cfg); err != nil {
		t.Fatalf("save: %v", err)
	}
	back, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(back.Actions) != 2 {
		t.Fatalf("saving lost them: %+v", back.Actions)
	}
	if back.Actions[0].Name != "Run tests" || back.Actions[0].Run != "go test ./..." ||
		!eq(back.Actions[0].On, []string{"branch"}) || !back.Actions[0].Keeps() {
		t.Errorf("first action came back as %+v", back.Actions[0])
	}
	if back.Actions[1].Keeps() {
		t.Error("keep = false did not survive")
	}
	// And the file is still TOML conch can read: the array of tables has
	// to come after every plain table, or the decoder reads a table into
	// the wrong place.
	b, err := os.ReadFile(File())
	if err != nil {
		t.Fatal(err)
	}
	if i, j := strings.Index(string(b), "[[actions]]"), strings.LastIndex(string(b), "[ui]"); i < j {
		t.Errorf("[[actions]] was written before [ui]:\n%s", b)
	}
}

func names(as []Action) []string {
	var out []string
	for _, a := range as {
		out = append(out, a.Name)
	}
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

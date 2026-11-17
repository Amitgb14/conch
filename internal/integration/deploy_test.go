package integration

import (
	"os"
	"strings"
	"testing"
)

// fixture reads a deploy file from testdata: the registry's own Vercel
// and Netlify files, copied as they were written, so a change to the
// reader that refuses either is seen here.
func fixture(t *testing.T, name string) *File {
	t.Helper()
	f, problems, err := Read("testdata/deploy/" + name + ".toml")
	if err != nil || len(problems) > 0 {
		t.Fatalf("%s: %v %q", name, err, problems)
	}
	return f
}

func TestDeployFilesParse(t *testing.T) {
	for _, name := range []string{"vercel", "netlify"} {
		f := fixture(t, name)
		if f.Kind != KindDeploy {
			t.Fatalf("%s: kind %q", name, f.Kind)
		}
		if got := strings.Join(f.Operations(), ","); got != "failed_log,previews,redeploy,promote" {
			t.Errorf("%s: operations %s", name, got)
		}
	}
}

// deployFile is a small deploy file with its parts replaced.
func deployFile(t *testing.T, replace ...string) []string {
	t.Helper()
	src := `format = 3
kind = "deploy"
name = "acme-deploy"
api = "https://api.acme.dev"
key_env = "ACME_TOKEN"

[detect]
files = ["acme.json"]

[states]
ok = "ready"
busy = "building"
broken = "failed"

[previews]
call = "GET /deploys?branch={branch}&sha={head}"
items = "deploys"
id = "id"
state = "state"
url = "https://{item.host}"
environment = "target"
error = "error"
inspect = "inspector"

[failed_log]
call = "GET /deploys/{deploy}/log"
text = "."

[redeploy]
call = "POST /deploys/{deploy}/again"

[promote]
call = "POST /deploys/{deploy}/promote"
body = { target = "production", id = "{deploy}" }
`
	for i := 0; i+1 < len(replace); i += 2 {
		if !strings.Contains(src, replace[i]) {
			t.Fatalf("no %q to replace", replace[i])
		}
		src = strings.Replace(src, replace[i], replace[i+1], 1)
	}
	_, problems, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return problems
}

func TestDeployProblems(t *testing.T) {
	if p := deployFile(t); len(p) > 0 {
		t.Fatalf("a good file: %q", p)
	}
	cases := []struct {
		name    string
		replace []string
		want    string
	}{
		{"no previews", []string{"[previews]\ncall = \"GET /deploys?branch={branch}&sha={head}\"\nitems = \"deploys\"\nid = \"id\"\nstate = \"state\"\nurl = \"https://{item.host}\"\nenvironment = \"target\"\nerror = \"error\"\ninspect = \"inspector\"\n", ""},
			"[previews]: required for kind deploy"},
		{"a ci state", []string{`busy = "building"`, `busy = "pending"`}, `[states] busy = "pending": one of ready, building, failed, canceled`},
		{"nothing ready", []string{`ok = "ready"`, `ok = "building"`}, "[states] maps nothing to ready or to failed"},
		{"no id", []string{"id = \"id\"\n", ""}, "[previews] items, id and state"},
		{"a ci operation", []string{"[redeploy]", "[rerun]"}, "[rerun]: not an operation of kind deploy"},
		{"run is not deploy's", []string{"/deploys/{deploy}/log", "/runs/{run}/log"}, "{run} is nothing known"},
		{"an unknown field", []string{`inspect = "inspector"`, `inspect = "{nope.x}"`}, "[previews] inspect: {nope.x} is nothing known"},
		{"no detect", []string{"[detect]\nfiles = [\"acme.json\"]\n", ""}, "[detect] files, remotes or checks"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := deployFile(t, tc.replace...)
			if !strings.Contains(strings.Join(p, "\n"), tc.want) {
				t.Fatalf("want %q in\n%s", tc.want, strings.Join(p, "\n"))
			}
		})
	}
}

// A ci file may not have a deploy's operations or fields.
func TestCIFileHasNoDeployParts(t *testing.T) {
	data, err := os.ReadFile("files/circleci.toml")
	if err != nil {
		t.Fatal(err)
	}
	_, p, err := Parse(append(data, []byte("\n[promote]\ncall = \"POST /x\"\n")...))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(p, "\n"), "[promote]: not an operation of kind ci") {
		t.Fatalf("%q", p)
	}
	_, p, _ = Parse([]byte(strings.Replace(string(data), "[runs]\n", "[runs]\ninspect = \"x\"\n", 1)))
	if !strings.Contains(strings.Join(p, "\n"), "unknown key runs.inspect") {
		t.Fatalf("a deploy field on a ci file: %q", p)
	}
}

// A query may carry a URL — Vercel finds a project by its repository's —
// without the call being taken for a URL of its own.
func TestCallQueryMayHoldURL(t *testing.T) {
	if err := checkCall("GET /v10/projects?repoUrl=https://{host}/{path}&limit=1", false); err != nil {
		t.Fatal(err)
	}
	if err := checkCall("GET https://elsewhere.dev/x", false); err == nil {
		t.Fatal("a URL as the path")
	}
}

func TestDeployContract(t *testing.T) {
	c, ok := Contract(KindDeploy)
	if !ok || strings.Join(c.Required, ",") != "previews" || strings.Join(c.Optional, ",") != "failed_log,redeploy,promote" ||
		strings.Join(c.States, ",") != "ready,building,failed,canceled" {
		t.Fatalf("%+v %v", c, ok)
	}
	// failed_log is said as a deploy's, not a run's.
	for _, want := range []string{"[previews] (required)", "[failed_log]: what failed deploy {deploy} printed", "[redeploy]", "[promote]", "conch asks the person first", "ready, building, failed, canceled"} {
		if !strings.Contains(c.Text, want) {
			t.Errorf("no %q in\n%s", want, c.Text)
		}
	}
	if strings.Contains(c.Text, "{run}") {
		t.Errorf("a deploy's contract speaks of runs:\n%s", c.Text)
	}
}

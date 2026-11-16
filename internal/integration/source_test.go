package integration

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckSource(t *testing.T) {
	isolate(t)
	shippedOnly, _ := All()
	acme, problems, _ := Parse([]byte(good))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	withAcme := append(append([]*File{}, shippedOnly...), acme)
	for _, tc := range []struct {
		name, link string
		files      []*File
		source     string
		readable   bool
	}{
		{"test (ubuntu)", "https://github.com/acme/widgets/actions/runs/1/job/2", nil, SourceActions, true},
		{"ci/circleci: build-and-test", "https://app.circleci.com/pipelines/github/acme/widgets/8812/workflows/w-1", shippedOnly, "circleci", true},
		{"ci/circleci: build-and-test", "", shippedOnly, "circleci", true}, // by its name alone
		{"ci/circleci: build-and-test", "", nil, "circleci", false},        // known, nothing installed
		{"build", "https://app.circleci.com/x", nil, "circleci", false},    // by its link alone
		{"lint", "https://gitlab.com/grp/w/-/pipelines/41", shippedOnly, "gitlab-ci", true},
		{"buildkite/widgets", "https://buildkite.com/acme/widgets/builds/9", shippedOnly, "buildkite", true},
		{"buildkite/widgets", "", nil, "buildkite", false},
		{"Pipeline #12", "https://bitbucket.org/acme/widgets/pipelines/results/12", shippedOnly, "bitbucket-pipelines", true},
		{"Azure Pipelines", "https://dev.azure.com/acme/widgets/_build/results?buildId=12", shippedOnly, "azure-pipelines", false},
		{"continuous-integration/travis-ci/pr", "", shippedOnly, "travis-ci", false},
		{"continuous-integration/jenkins/pr-merge", "https://ci.corp/job/widgets/PR-4/1/", nil, "jenkins", false},
		{"continuous-integration/jenkins/pr-merge", "https://ci.corp/job/widgets/PR-4/1/", shippedOnly, "jenkins", true},
		{"acme/unit", "https://ci.acme.dev/r/1", withAcme, "acme-ci", true}, // a file's checks glob
		{"unit", "https://api.acme.dev/runs/1", withAcme, "acme-ci", true},  // its API's host
		{"something", "https://ci.notacme.dev/x", withAcme, "", false},      // a look-alike host
		{"license/cla", "https://cla-assistant.io/acme/widgets", shippedOnly, "", false},
		{"weird", "::", nil, "", false},
	} {
		source, readable := CheckSource(tc.name, tc.link, tc.files)
		if source != tc.source || readable != tc.readable {
			t.Errorf("%q %q: %q %v, want %q %v", tc.name, tc.link, source, readable, tc.source, tc.readable)
		}
	}
	// A file that cannot read a log names the source but cannot read it.
	noLog := *acme
	noLog.FailedLog = LogOp{}
	if s, r := CheckSource("acme/unit", "", []*File{&noLog}); s != "acme-ci" || r {
		t.Errorf("no failed_log: %q %v", s, r)
	}
}

func TestLabel(t *testing.T) {
	isolate(t)
	files, _ := All()
	for source, want := range map[string]string{
		SourceActions: "GitHub Actions", "circleci": "CircleCI", "buildkite": "Buildkite", "mystery": "mystery",
	} {
		if got := Label(source, files); got != want {
			t.Errorf("%s: %q, want %q", source, got, want)
		}
	}
}

func TestCachedAndForget(t *testing.T) {
	home := isolate(t)
	Forget()
	n := len(Cached())
	dir := filepath.Join(home, "integrations")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "acme-ci.toml"), []byte(good), 0o644)
	if len(Cached()) != n {
		t.Fatal("read again before the cache ran out")
	}
	Forget()
	if len(Cached()) != n+1 {
		t.Fatal("an installed file not seen after Forget")
	}
	Forget()
}

func TestSource(t *testing.T) {
	home := isolate(t)
	data, shipped, err := Source("circleci")
	if err != nil || !shipped || len(data) == 0 {
		t.Fatalf("shipped: %v %v", shipped, err)
	}
	os.MkdirAll(filepath.Join(home, "integrations"), 0o755)
	os.WriteFile(filepath.Join(home, "integrations", "circleci.toml"), []byte("mine"), 0o600)
	if data, shipped, err := Source("circleci"); err != nil || shipped || string(data) != "mine" {
		t.Fatalf("installed: %q %v %v", data, shipped, err)
	}
	if _, _, err := Source("nothing-here"); err == nil {
		t.Fatal("an unknown name")
	}
	if _, _, err := Source("../etc"); err == nil {
		t.Fatal("a path for a name")
	}
}

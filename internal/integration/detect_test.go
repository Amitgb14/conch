package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func detected(ds []Detection) string {
	var out []string
	for _, d := range ds {
		s := d.Name + ":" + d.Why
		if d.Installed {
			s += "+"
		}
		out = append(out, s)
	}
	return strings.Join(out, " ")
}

func TestDetect(t *testing.T) {
	isolate(t)
	files, _ := All()
	gh := Remote{Host: "github.com", Path: "acme/widgets"}

	root := t.TempDir()
	if got := detected(Detected(root, Remote{}, nil, files)); got != "" {
		t.Fatalf("a bare folder: %q", got)
	}
	if got := detected(Detected(root, gh, nil, files)); got != "github:github.com remote+" {
		t.Fatalf("on github: %q", got)
	}

	// Actions and CircleCI both: a project uses every CI it has.
	os.MkdirAll(filepath.Join(root, ".github", "workflows"), 0o755)
	os.MkdirAll(filepath.Join(root, ".circleci"), 0o755)
	os.WriteFile(filepath.Join(root, ".circleci", "config.yml"), nil, 0o644)
	os.WriteFile(filepath.Join(root, "bitbucket-pipelines.yml"), nil, 0o644)
	got := detected(Detected(root, gh, []CheckRef{{Name: "buildkite/widgets", URL: "https://buildkite.com/acme/widgets/builds/1"}}, files))
	for _, want := range []string{"github:github.com remote+", "github-actions:.github/workflows+", "circleci:.circleci/config.yml+",
		"buildkite:check buildkite/widgets+", "bitbucket-pipelines:bitbucket-pipelines.yml"} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q: %s", want, got)
		}
	}
	if strings.Contains(got, "bitbucket-pipelines:bitbucket-pipelines.yml+") {
		t.Error("a service with no file read as installed")
	}

	// A self-hosted GitLab is not gitlab.com: only an installed file whose
	// remotes name it suggests one.
	if got := detected(Detected(t.TempDir(), Remote{Host: "git.corp", Path: "a/b"}, nil, files)); got != "" {
		t.Fatalf("self-hosted: %q", got)
	}
	own, problems, _ := Parse([]byte(strings.Replace(good, `remotes = ["acme.dev"]`, `remotes = ["git.corp"]`, 1)))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	if got := detected(Detected(t.TempDir(), Remote{Host: "git.corp", Path: "a/b"}, nil, append(files, own))); got != "acme-ci:git.corp remote+" {
		t.Fatalf("self-hosted with a file: %q", got)
	}
	// A folder that is not a checkout at all.
	if got := detected(Detected("", Remote{}, nil, files)); got != "" {
		t.Fatalf("nothing: %q", got)
	}
}

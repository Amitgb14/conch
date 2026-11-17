package integration

import (
	"strings"
	"testing"
)

func TestDoes(t *testing.T) {
	levels := func(fs []Feature) string {
		s := ""
		for _, f := range fs {
			s += f.Level + " "
		}
		return s
	}
	if got := levels(Does(KindCI, []string{"runs", "failed_log", "rerun"})); got != "yes yes yes partly built-in " {
		t.Fatalf("circleci-like: %q", got)
	}
	if got := levels(Does(KindCI, []string{"runs", "failed_log", "stats"})); got != "yes no yes yes built-in " {
		t.Fatalf("jenkins-like with stats: %q", got)
	}
	if got := levels(Does(KindCI, nil)); got != "no no no no built-in " {
		t.Fatalf("nothing: %q", got)
	}
	if len(Does(KindHost, []string{"list"})) != 0 {
		t.Fatal("a kind this conch does not describe yet")
	}
}

// A sandbox provider says what it does — a machine always, and what its
// file describes beside — and no kind says null, which Conch.app read as
// a page it could not open.
func TestDoesSandboxAndNeverNull(t *testing.T) {
	got := Does(KindSandbox, []string{"snapshot", "preview"})
	if len(got) != 5 || got[0].Level != "yes" || got[1].Level != "yes" || got[2].Level != "yes" || got[3].Level != "no" {
		t.Fatalf("sandbox: %+v", got)
	}
	for _, k := range []string{KindSandbox, KindHost, "nothing"} {
		if Does(k, nil) == nil {
			t.Errorf("%s: nil", k)
		}
	}
}

// A deploy file says what it does from its operations: Netlify-like with
// everything, and one that only lists previews.
func TestDoesDeploy(t *testing.T) {
	levels := func(fs []Feature) string {
		s := ""
		for _, f := range fs {
			s += f.Level + " "
		}
		return s
	}
	all := Does(KindDeploy, []string{"previews", "failed_log", "redeploy", "promote"})
	if got := levels(all); got != "yes yes yes yes " {
		t.Fatalf("all: %q %+v", got, all)
	}
	if got := levels(Does(KindDeploy, []string{"previews"})); got != "yes no no no " {
		t.Fatalf("previews only: %q", got)
	}
	if all[3].Where == "" || !strings.Contains(all[3].Where, "asks first") {
		t.Fatalf("promote: %+v", all[3])
	}
}

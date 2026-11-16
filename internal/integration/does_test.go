package integration

import "testing"

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

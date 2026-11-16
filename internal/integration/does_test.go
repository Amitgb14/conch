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
	if Does("tickets", []string{"list"}) != nil {
		t.Fatal("a kind this conch does not describe yet")
	}
}

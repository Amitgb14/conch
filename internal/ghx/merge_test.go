package ghx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mergeGH answers `gh repo view` with allowed and records every call's
// arguments, a line each, so a test can see the merge that followed.
func mergeGH(t *testing.T, allowed, mergeScript string) string {
	t.Helper()
	log := filepath.Join(t.TempDir(), "calls")
	fakeGH(t, `echo "$*" >> `+log+`
if [ "$1" = repo ]; then printf '%s' '`+allowed+`'; exit 0; fi
`+mergeScript)
	return log
}

func TestMerge(t *testing.T) {
	for _, c := range []struct{ name, allowed, want string }{
		{"every way", `{"mergeCommitAllowed":true,"squashMergeAllowed":true,"rebaseMergeAllowed":true}`, "merge"},
		{"squash only", `{"mergeCommitAllowed":false,"squashMergeAllowed":true,"rebaseMergeAllowed":false}`, "squash"},
		{"rebase only", `{"mergeCommitAllowed":false,"squashMergeAllowed":false,"rebaseMergeAllowed":true}`, "rebase"},
	} {
		t.Run(c.name, func(t *testing.T) {
			log := mergeGH(t, c.allowed, "exit 0\n")
			method, err := Merge(context.Background(), t.TempDir(), 307, "abc123")
			if err != nil || method != c.want {
				t.Fatalf("got %q %v, want %q", method, err, c.want)
			}
			b, _ := os.ReadFile(log)
			want := "repo view --json mergeCommitAllowed,squashMergeAllowed,rebaseMergeAllowed\npr merge 307 --" + c.want + " --match-head-commit abc123\n"
			if string(b) != want {
				t.Fatalf("calls:\n%s", b)
			}
		})
	}

	// No head to hold it to: merged as it stands.
	log := mergeGH(t, `{"mergeCommitAllowed":true}`, "exit 0\n")
	if _, err := Merge(context.Background(), t.TempDir(), 9, ""); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(log); !strings.HasSuffix(string(b), "pr merge 9 --merge\n") {
		t.Fatalf("calls:\n%s", b)
	}
}

func TestMergeFailures(t *testing.T) {
	for _, c := range []struct{ name, allowed, script, want string }{
		{"nothing allowed", `{"mergeCommitAllowed":false,"squashMergeAllowed":false,"rebaseMergeAllowed":false}`, "exit 0\n", "allows no way"},
		{"bad json", `{"mergeCommit`, "exit 0\n", "parse gh output"},
		{"conflict", `{"squashMergeAllowed":true}`,
			"echo 'X Pull request Amitgb14/r#307 is not mergeable: the merge commit cannot be cleanly created.' >&2\nexit 1\n", "is not mergeable"},
		{"pushed since", `{"mergeCommitAllowed":true}`,
			"echo 'GraphQL: Head branch was modified. Review and try the merge again. (mergePullRequest)' >&2\nexit 1\n", "Head branch was modified"},
	} {
		t.Run(c.name, func(t *testing.T) {
			mergeGH(t, c.allowed, c.script)
			if _, err := Merge(context.Background(), t.TempDir(), 307, "abc"); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want %q", err, c.want)
			}
		})
	}

	// The repository can't be read: nothing is merged.
	fakeGH(t, "echo 'HTTP 504: Gateway Timeout' >&2; exit 1\n")
	if _, err := Merge(context.Background(), t.TempDir(), 1, ""); err == nil || !strings.Contains(err.Error(), "gh repo view: HTTP 504") {
		t.Fatalf("repo view failing: %v", err)
	}

	t.Setenv("CONCH_GH", "")
	t.Setenv("PATH", t.TempDir())
	if _, err := Merge(context.Background(), t.TempDir(), 1, ""); !errors.Is(err, ErrNoGH) {
		t.Fatalf("no gh: %v", err)
	}
}

func TestParseConflicting(t *testing.T) {
	prs, err := Parse([]byte(`[
 {"number":1,"state":"OPEN","headRefName":"a","mergeable":"CONFLICTING","statusCheckRollup":[]},
 {"number":2,"state":"OPEN","headRefName":"b","mergeable":"MERGEABLE","statusCheckRollup":[]},
 {"number":3,"state":"OPEN","headRefName":"c","mergeable":"UNKNOWN","statusCheckRollup":[]},
 {"number":4,"state":"OPEN","headRefName":"d","statusCheckRollup":[]}]`))
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []bool{true, false, false, false} {
		if prs[i].Conflicting != want {
			t.Errorf("#%d: conflicting %v, want %v", prs[i].Number, prs[i].Conflicting, want)
		}
	}
}

func TestReady(t *testing.T) {
	log := mergeGH(t, `{}`, "exit 0\n")
	if err := Ready(context.Background(), t.TempDir(), 12); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(log); string(b) != "pr ready 12\n" {
		t.Fatalf("calls:\n%s", b)
	}
	fakeGH(t, "echo 'X not allowed' >&2; exit 1\n")
	if err := Ready(context.Background(), t.TempDir(), 12); err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("refused: %v", err)
	}
}

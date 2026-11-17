package ghx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const runsJSON = `[
 {"databaseId": 30, "workflowName": "lint", "url": "https://x/runs/30", "headSha": "new", "conclusion": "success"},
 {"databaseId": 20, "workflowName": "test", "url": "https://x/runs/20", "headSha": "new", "conclusion": "failure"},
 {"databaseId": 10, "workflowName": "test", "url": "https://x/runs/10", "headSha": "old", "conclusion": "failure"}
]`

// runsGH answers run list with list and run view with the run's ID, so the
// test can see which run's log was asked for.
func runsGH(t *testing.T, list string) {
	t.Helper()
	fakeGH(t, `case "$1 $2" in
"run list") printf '%s' '`+list+`' ;;
"run view") printf 'test\tgo test\tFAIL run %s\n' "$3"; [ "$4" = "--log-failed" ] || exit 9 ;;
*) exit 7 ;;
esac
`)
}

func TestFailedLog(t *testing.T) {
	runsGH(t, runsJSON)
	got, err := FailedLog(context.Background(), t.TempDir(), "feat", "new")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != 20 || got.Workflow != "test" || got.URL != "https://x/runs/20" || !strings.Contains(got.Log, "FAIL run 20") {
		t.Fatalf("got %+v", got)
	}
	// Without a head, the newest failure is the one.
	if got, err := FailedLog(context.Background(), t.TempDir(), "feat", ""); err != nil || got.ID != 20 {
		t.Fatalf("no head: %+v %v", got, err)
	}
	// A head nothing failed on: an older run's failure isn't this work's.
	if _, err := FailedLog(context.Background(), t.TempDir(), "feat", "newer"); !errors.Is(err, ErrNoFailedRun) {
		t.Fatalf("other head: %v", err)
	}
}

// TestRerunFailedEveryRun: every workflow that failed at the head is run
// again, each with its own `gh run rerun --failed`; an older commit's
// failure, or an older failure of the same workflow, is not. One that gh
// refuses is said, beside those it took.
func TestRerunFailedEveryRun(t *testing.T) {
	list := `[
 {"databaseId": 40, "workflowName": "lint", "url": "https://x/runs/40", "headSha": "new", "conclusion": "failure"},
 {"databaseId": 30, "workflowName": "test", "url": "https://x/runs/30", "headSha": "new", "conclusion": "failure"},
 {"databaseId": 25, "workflowName": "test", "url": "https://x/runs/25", "headSha": "new", "conclusion": "failure"},
 {"databaseId": 20, "workflowName": "e2e", "url": "https://x/runs/20", "headSha": "new", "conclusion": "success"},
 {"databaseId": 10, "workflowName": "e2e", "url": "https://x/runs/10", "headSha": "old", "conclusion": "failure"}
]`
	log := filepath.Join(t.TempDir(), "calls")
	fakeGH(t, `echo "$*" >> `+log+`
case "$1 $2" in
"run list") printf '%s' '`+list+`' ;;
"run rerun") [ "$4" = "--failed" ] || exit 9; [ "$3" = "$REFUSE" ] && { echo 'HTTP 403' >&2; exit 1; }; exit 0 ;;
*) exit 7 ;;
esac
`)
	ids := func(runs []FailedRun) string {
		var s []string
		for _, r := range runs {
			s = append(s, strconv.FormatInt(r.ID, 10))
		}
		return strings.Join(s, ",")
	}
	for _, head := range []string{"new", ""} {
		os.Remove(log)
		runs, err := RerunFailed(context.Background(), t.TempDir(), "feat", head)
		if err != nil || ids(runs) != "40,30" || runs[1].URL != "https://x/runs/30" {
			t.Fatalf("head %q: %+v %v", head, runs, err)
		}
		b, _ := os.ReadFile(log)
		if n := strings.Count(string(b), "run rerun"); n != 2 {
			t.Fatalf("head %q: %d re-runs:\n%s", head, n, b)
		}
	}
	// At the old commit only its own failure.
	if runs, err := RerunFailed(context.Background(), t.TempDir(), "feat", "old"); err != nil || ids(runs) != "10" {
		t.Fatalf("old: %+v %v", runs, err)
	}
	t.Setenv("REFUSE", "30")
	runs, err := RerunFailed(context.Background(), t.TempDir(), "feat", "new")
	if ids(runs) != "40" || err == nil || !strings.Contains(err.Error(), "run 30 (test)") {
		t.Fatalf("one refused: %+v %v", runs, err)
	}
	if _, err := RerunFailed(context.Background(), t.TempDir(), "feat", "none"); !errors.Is(err, ErrNoFailedRun) {
		t.Fatalf("nothing failed there: %v", err)
	}
}

func TestFailedLogFailures(t *testing.T) {
	for name, tc := range map[string]struct{ list, want string }{
		"nothing ran":   {`[]`, ErrNoFailedRun.Error()},
		"all passed":    {`[{"databaseId": 1, "conclusion": "success"}]`, ErrNoFailedRun.Error()},
		"not json":      {`oops`, "gh run list"},
		"truncated":     {`[{"databaseId": 1,`, "gh run list"},
		"timed out too": {`[{"databaseId": 5, "conclusion": "timed_out"}]`, ""},
	} {
		t.Run(name, func(t *testing.T) {
			runsGH(t, tc.list)
			_, err := FailedLog(context.Background(), t.TempDir(), "feat", "")
			if tc.want == "" {
				if err != nil {
					t.Fatalf("a timed-out run is a failure: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
	// gh itself failing is said in its own words.
	fakeGH(t, "echo 'HTTP 404: Not Found' >&2; exit 1\n")
	if _, err := FailedLog(context.Background(), t.TempDir(), "feat", ""); err == nil || !strings.Contains(err.Error(), "gh run list: HTTP 404") {
		t.Fatalf("gh failed: %v", err)
	}
	// No gh at all.
	t.Setenv("CONCH_GH", "/nonexistent/gh")
	if _, err := FailedLog(context.Background(), t.TempDir(), "feat", ""); !errors.Is(err, ErrNoGH) {
		t.Fatalf("no gh: %v", err)
	}
}

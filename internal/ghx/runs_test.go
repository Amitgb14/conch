package ghx

import (
	"context"
	"errors"
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

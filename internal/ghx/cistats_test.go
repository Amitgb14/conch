package ghx

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

var statsNow = time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

// run is a finished run that started at the given hours before statsNow,
// queued q seconds and took d seconds.
func ciRun(id int64, wf, branch, sha, conclusion string, hoursAgo float64, q, d int) Run {
	created := statsNow.Add(-time.Duration(hoursAgo * float64(time.Hour)))
	started := created.Add(time.Duration(q) * time.Second)
	return Run{ID: id, Workflow: wf, Branch: branch, Sha: sha, Conclusion: conclusion, Status: "completed", Attempt: 1,
		Created: created, Started: started, Updated: started.Add(time.Duration(d) * time.Second)}
}

func job(name, conclusion string, secs int) Job {
	start := statsNow.Add(-48 * time.Hour)
	return Job{Name: name, Conclusion: conclusion, Started: start, Completed: start.Add(time.Duration(secs) * time.Second)}
}

func TestStatsCounts(t *testing.T) {
	runs := []Run{
		ciRun(6, "ci", "feat", "s3", "success", 2, 10, 300),
		ciRun(5, "ci", "feat", "s2", "failure", 5, 20, 600),
		ciRun(4, "ci", "main", "m1", "success", 30, 30, 400),
		ciRun(3, "ci", "feat", "s1", "cancelled", 40, 5, 50),
		// The prior period: older than the 7 days asked for.
		ciRun(2, "ci", "main", "m0", "failure", 24*9, 10, 100),
		ciRun(1, "ci", "main", "m0", "success", 24*10, 10, 100),
		// Still running: counted nowhere yet.
		{ID: 7, Workflow: "ci", Branch: "feat", Status: "in_progress", Created: statsNow.Add(-time.Hour)},
	}
	jobs := map[int64][]Job{
		6: {job("build", "success", 60), job("test", "success", 240)},
		5: {job("build", "success", 80), job("test", "failure", 500)},
		4: {job("build", "success", 70), job("test", "success", 300)},
	}
	s := ComputeStats(runs, jobs, statsNow, 7, time.UTC)
	// A cancelled run is neither a pass nor a failure.
	if s.Runs != 3 || s.Passed != 2 || s.Failed != 1 {
		t.Fatalf("counts: %+v", s)
	}
	if s.PrevRuns != 2 || s.PrevPassed != 1 {
		t.Errorf("prior period: %d runs, %d passed", s.PrevRuns, s.PrevPassed)
	}
	if s.MedianSeconds != 400 || s.QueueSeconds != 20 {
		t.Errorf("median %d, queue %d", s.MedianSeconds, s.QueueSeconds)
	}
	if s.JobsRead != 3 {
		t.Errorf("jobs read for %d runs", s.JobsRead)
	}
	// One entry a day, oldest first, today last.
	if len(s.Daily) != 7 || s.Daily[6].Date != "2026-11-14" || s.Daily[6].Passed != 1 || s.Daily[6].Failed != 1 {
		t.Errorf("daily: %+v", s.Daily)
	}
	if s.Daily[5].Date != "2026-11-13" || s.Daily[5].Passed != 1 {
		t.Errorf("yesterday: %+v", s.Daily[5])
	}
	// Jobs, slowest first, with their median and slowest tenth.
	if len(s.Jobs) != 2 || s.Jobs[0].Name != "test" || s.Jobs[0].Runs != 3 || s.Jobs[0].Failed != 1 ||
		s.Jobs[0].MedianSeconds != 300 || s.Jobs[0].P90Seconds != 500 {
		t.Errorf("jobs: %+v", s.Jobs)
	}
	// The failure on feat went green when the next run on it finished.
	if s.RedToGreenSeconds != 3*3600+310-620 || s.RedToGreenCount != 1 {
		t.Errorf("red to green %d over %d", s.RedToGreenSeconds, s.RedToGreenCount)
	}
}

// A job that failed and passed on the same commit is flaky; so is a run
// that passed only on its second attempt. A failure fixed by a new commit
// is not.
func TestStatsFlaky(t *testing.T) {
	runs := []Run{
		ciRun(4, "ci", "feat", "same", "success", 1, 1, 10),
		ciRun(3, "ci", "feat", "same", "failure", 2, 1, 10),
		ciRun(2, "ci", "feat", "fixed", "failure", 3, 1, 10),
		ciRun(1, "ci", "feat", "fixed2", "success", 4, 1, 10),
	}
	retried := ciRun(5, "lint", "main", "x", "success", 1, 1, 10)
	retried.Attempt = 2
	runs = append(runs, retried)
	jobs := map[int64][]Job{
		4: {job("test macos", "success", 9), job("build", "success", 1)},
		3: {job("test macos", "failure", 9), job("build", "success", 1)},
		2: {job("build", "failure", 1)},
		1: {job("build", "success", 1)},
	}
	s := ComputeStats(runs, jobs, statsNow, 7, time.UTC)
	if len(s.Flaky) != 2 || s.Flaky[0].Name != "test macos" || s.Flaky[0].Times != 1 {
		t.Fatalf("flaky: %+v", s.Flaky)
	}
	if s.Flaky[1].Name != "lint" || !strings.Contains(s.Flaky[1].How, "attempt") {
		t.Errorf("a run that passed on a retry: %+v", s.Flaky[1])
	}
}

// Failures that can still be re-run: the newest run of each workflow on a
// branch, when it failed — not one a later run already replaced.
func TestStatsFailing(t *testing.T) {
	runs := []Run{
		ciRun(5, "ci", "feat", "b", "failure", 1, 1, 10),
		ciRun(4, "ci", "feat", "a", "failure", 2, 1, 10),
		ciRun(3, "ci", "main", "m", "success", 1, 1, 10),
		ciRun(2, "ci", "main", "l", "failure", 2, 1, 10),
		ciRun(1, "lint", "feat", "b", "timed_out", 1, 1, 10),
	}
	s := ComputeStats(runs, nil, statsNow, 7, time.UTC)
	var ids []int64
	for _, f := range s.Failing {
		ids = append(ids, f.ID)
	}
	if len(ids) != 2 || ids[0] != 5 || ids[1] != 1 {
		t.Fatalf("failing: %+v", s.Failing)
	}
}

// Nothing ran: every number is zero and every day is there, empty.
func TestStatsEmpty(t *testing.T) {
	s := ComputeStats(nil, nil, statsNow, 30, time.UTC)
	if s.Runs != 0 || s.MedianSeconds != 0 || len(s.Daily) != 30 || len(s.Jobs) != 0 || s.Days != 30 {
		t.Fatalf("%+v", s)
	}
	if s := ComputeStats(nil, nil, statsNow, 0, time.UTC); s.Days != 1 || len(s.Daily) != 1 {
		t.Errorf("no days asked is one: %+v", s)
	}
}

func TestPercentile(t *testing.T) {
	for _, c := range []struct {
		xs   []int
		p    float64
		want int
	}{
		{nil, 0.5, 0},
		{[]int{7}, 0.9, 7},
		{[]int{3, 1, 2}, 0.5, 2},
		{[]int{1, 2, 3, 4}, 0.5, 2},
		{[]int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, 0.9, 9},
	} {
		if got := percentile(c.xs, c.p); got != c.want {
			t.Errorf("percentile(%v, %v) = %d, want %d", c.xs, c.p, got, c.want)
		}
	}
}

// gh's own JSON, as gh run list and gh run view print it.
func TestRunsAndJobsThroughGH(t *testing.T) {
	fakeGH(t, `case "$1 $2" in
"run list") cat <<'EOF'
[{"attempt":2,"conclusion":"success","createdAt":"2026-11-15T00:40:36Z","databaseId":43,"event":"pull_request","headBranch":"feat","headSha":"abc","startedAt":"2026-11-15T00:40:40Z","status":"completed","updatedAt":"2026-11-15T00:59:44Z","url":"https://x/runs/43","workflowName":"ci"}]
EOF
;;
"run view") [ "$3" = 43 ] && [ "$4" = --json ] || exit 9; cat <<'EOF'
{"jobs":[{"completedAt":"2026-11-15T00:41:14Z","conclusion":"success","databaseId":1,"name":"build","startedAt":"2026-11-15T00:40:38Z","status":"completed","url":"u"}]}
EOF
;;
"run rerun") [ "$3" = 43 ] && [ "$4" = --failed ] || exit 9 ;;
*) exit 7 ;;
esac
`)
	ctx := context.Background()
	runs, err := Runs(ctx, t.TempDir(), 50)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs %+v %v", runs, err)
	}
	r := runs[0]
	if r.ID != 43 || r.Workflow != "ci" || r.Branch != "feat" || r.Sha != "abc" || r.Attempt != 2 || r.Updated.Sub(r.Started) != 19*time.Minute+4*time.Second {
		t.Errorf("run %+v", r)
	}
	jobs, err := RunJobs(ctx, t.TempDir(), 43)
	if err != nil || len(jobs) != 1 || jobs[0].Name != "build" || jobs[0].Completed.Sub(jobs[0].Started) != 36*time.Second {
		t.Errorf("jobs %+v %v", jobs, err)
	}
	if err := Rerun(ctx, t.TempDir(), 43); err != nil {
		t.Errorf("rerun: %v", err)
	}
	if err := Rerun(ctx, t.TempDir(), 44); err == nil {
		t.Error("a rerun gh refused said nothing")
	}
}

func TestRunsFailures(t *testing.T) {
	fakeGH(t, `printf 'oops'`)
	if _, err := Runs(context.Background(), t.TempDir(), 10); err == nil || !strings.Contains(err.Error(), "gh run list") {
		t.Errorf("bad JSON: %v", err)
	}
	if _, err := RunJobs(context.Background(), t.TempDir(), 1); err == nil {
		t.Error("bad jobs JSON said nothing")
	}
	t.Setenv("CONCH_GH", "/nonexistent/gh")
	if _, err := Runs(context.Background(), t.TempDir(), 10); !errors.Is(err, ErrNoGH) {
		t.Errorf("no gh: %v", err)
	}
}

// The newest failed run on a branch, for a re-run asked by branch.
func TestFailedRunOnBranch(t *testing.T) {
	runsGH(t, runsJSON)
	id, err := FailedRunID(context.Background(), t.TempDir(), "feat", "new")
	if err != nil || id != 20 {
		t.Fatalf("got %d %v", id, err)
	}
	if _, err := FailedRunID(context.Background(), t.TempDir(), "feat", "newer"); !errors.Is(err, ErrNoFailedRun) {
		t.Errorf("nothing failed there: %v", err)
	}
}

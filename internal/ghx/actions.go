package ghx

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"
)

// A repository's GitHub Actions history, read through gh, and what conch
// says about it: how often it passes, how long it takes, which jobs are
// slow or flaky, and which failures can be run again.

// Run is one workflow run, at its latest attempt.
type Run struct {
	ID         int64
	Workflow   string
	Branch     string
	Sha        string
	Event      string
	Status     string // queued, in_progress, completed
	Conclusion string // success, failure, cancelled, timed_out, skipped… once completed
	Attempt    int
	URL        string
	Created    time.Time
	Started    time.Time
	Updated    time.Time
}

// Job is one job of a run.
type Job struct {
	Name       string
	Conclusion string
	Started    time.Time
	Completed  time.Time
}

// Runs lists the repository's newest workflow runs, up to limit.
func Runs(ctx context.Context, dir string, limit int) ([]Run, error) {
	out, err := run(ctx, dir, "run", "list", "--limit", strconv.Itoa(limit), "--json",
		"databaseId,workflowName,headBranch,headSha,event,status,conclusion,attempt,url,createdAt,startedAt,updatedAt")
	if err != nil {
		return nil, err
	}
	var raw []struct {
		ID         int64     `json:"databaseId"`
		Workflow   string    `json:"workflowName"`
		Branch     string    `json:"headBranch"`
		Sha        string    `json:"headSha"`
		Event      string    `json:"event"`
		Status     string    `json:"status"`
		Conclusion string    `json:"conclusion"`
		Attempt    int       `json:"attempt"`
		URL        string    `json:"url"`
		Created    time.Time `json:"createdAt"`
		Started    time.Time `json:"startedAt"`
		Updated    time.Time `json:"updatedAt"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("gh run list: %w", err)
	}
	runs := make([]Run, 0, len(raw))
	for _, r := range raw {
		runs = append(runs, Run{ID: r.ID, Workflow: r.Workflow, Branch: r.Branch, Sha: r.Sha, Event: r.Event, Status: r.Status,
			Conclusion: r.Conclusion, Attempt: r.Attempt, URL: r.URL, Created: r.Created, Started: r.Started, Updated: r.Updated})
	}
	return runs, nil
}

// RunJobs lists a run's jobs. A finished run's jobs never change, so a
// caller may keep them.
func RunJobs(ctx context.Context, dir string, id int64) ([]Job, error) {
	out, err := run(ctx, dir, "run", "view", strconv.FormatInt(id, 10), "--json", "jobs")
	if err != nil {
		return nil, err
	}
	var raw struct {
		Jobs []struct {
			Name       string    `json:"name"`
			Conclusion string    `json:"conclusion"`
			Started    time.Time `json:"startedAt"`
			Completed  time.Time `json:"completedAt"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("gh run view: %w", err)
	}
	jobs := make([]Job, 0, len(raw.Jobs))
	for _, j := range raw.Jobs {
		jobs = append(jobs, Job{Name: j.Name, Conclusion: j.Conclusion, Started: j.Started, Completed: j.Completed})
	}
	return jobs, nil
}

// Rerun runs a run's failed jobs again, as a new attempt of the same run.
func Rerun(ctx context.Context, dir string, id int64) error {
	_, err := run(ctx, dir, "run", "rerun", strconv.FormatInt(id, 10), "--failed")
	return err
}

// FailedRunID is the newest failed Actions run on branch — at commit head
// when given — for a re-run asked for by branch rather than by run.
func FailedRunID(ctx context.Context, dir, branch, head string) (int64, error) {
	runs, err := failedRuns(ctx, dir, branch, head)
	if err != nil {
		return 0, err
	}
	return runs[0].ID, nil
}

// failed says whether a finished run's conclusion is a failure: a timeout
// is one, a cancellation is somebody's choice.
func failed(conclusion string) bool { return conclusion == "failure" || conclusion == "timed_out" }

// CIStats is what the CI panel shows for a period of whole days.
type CIStats struct {
	Days int
	// Finished runs that passed or failed in the period (a cancelled or
	// skipped run is neither), and the same for the period before it.
	Runs, Passed, Failed int
	PrevRuns, PrevPassed int
	// Medians, in seconds, of a run's time from start to finish and of
	// its wait in the queue; the run time also for the period before.
	MedianSeconds, PrevMedianSeconds, QueueSeconds int
	// How long a branch stayed red: from a failed run to the next passing
	// run of the same workflow on the same branch, median, and how many.
	RedToGreenSeconds, RedToGreenCount int
	// One entry a day, the oldest first and today last.
	Daily []CIDay
	// Each job, the slowest (by median) first.
	Jobs []CIJob
	// Jobs and runs that failed and then passed without a change.
	Flaky []CIFlaky
	// The newest run of each workflow on each branch, where it failed:
	// what can be run again.
	Failing []Run
	// How many of the period's runs had their jobs read.
	JobsRead int
}

// CIDay is one day's finished runs.
type CIDay struct {
	Date           string // YYYY-MM-DD in the server's time zone
	Passed, Failed int
}

// CIJob is one job's record over the period.
type CIJob struct {
	Name                      string
	Runs, Failed              int
	MedianSeconds, P90Seconds int
}

// CIFlaky is a job (or a whole run) that failed and passed with nothing
// changed: on the same commit, or on a second attempt of the same run.
type CIFlaky struct {
	Name  string
	Times int
	How   string
	Last  time.Time
}

// ComputeStats works out a period's statistics: the days days up to and
// including now's (in loc), and the same number before them. jobs holds
// the jobs of whichever runs have been read.
func ComputeStats(runs []Run, jobs map[int64][]Job, now time.Time, days int, loc *time.Location) CIStats {
	if days < 1 {
		days = 1
	}
	y, m, d := now.In(loc).Date()
	start := time.Date(y, m, d, 0, 0, 0, 0, loc).AddDate(0, 0, -(days - 1))
	prevStart := start.AddDate(0, 0, -days)
	s := CIStats{Days: days}
	day := map[string]int{}
	for i := range days {
		date := start.AddDate(0, 0, i).Format("2006-01-02")
		day[date] = i
		s.Daily = append(s.Daily, CIDay{Date: date})
	}
	var durations, prevDurations, queues []int
	type jobAcc struct {
		runs, failed int
		secs         []int
	}
	jobAccs := map[string]*jobAcc{}
	var inPeriod []Run
	for _, r := range runs {
		finished := r.Status == "completed" && (r.Conclusion == "success" || failed(r.Conclusion))
		switch {
		case !finished:
			continue
		case !r.Created.Before(start):
			inPeriod = append(inPeriod, r)
			s.Runs++
			i := day[r.Created.In(loc).Format("2006-01-02")]
			if r.Conclusion == "success" {
				s.Passed++
				s.Daily[i].Passed++
			} else {
				s.Failed++
				s.Daily[i].Failed++
			}
			if !r.Started.IsZero() && r.Updated.After(r.Started) {
				durations = append(durations, int(r.Updated.Sub(r.Started).Seconds()))
			}
			if !r.Started.IsZero() && !r.Started.Before(r.Created) {
				queues = append(queues, int(r.Started.Sub(r.Created).Seconds()))
			}
			if js, ok := jobs[r.ID]; ok {
				s.JobsRead++
				for _, j := range js {
					a := jobAccs[j.Name]
					if a == nil {
						a = &jobAcc{}
						jobAccs[j.Name] = a
					}
					a.runs++
					if failed(j.Conclusion) {
						a.failed++
					}
					if !j.Started.IsZero() && j.Completed.After(j.Started) {
						a.secs = append(a.secs, int(j.Completed.Sub(j.Started).Seconds()))
					}
				}
			}
		case !r.Created.Before(prevStart):
			s.PrevRuns++
			if r.Conclusion == "success" {
				s.PrevPassed++
			}
			if !r.Started.IsZero() && r.Updated.After(r.Started) {
				prevDurations = append(prevDurations, int(r.Updated.Sub(r.Started).Seconds()))
			}
		}
	}
	s.MedianSeconds = percentile(durations, 0.5)
	s.PrevMedianSeconds = percentile(prevDurations, 0.5)
	s.QueueSeconds = percentile(queues, 0.5)
	for name, a := range jobAccs {
		s.Jobs = append(s.Jobs, CIJob{Name: name, Runs: a.runs, Failed: a.failed, MedianSeconds: percentile(a.secs, 0.5), P90Seconds: percentile(a.secs, 0.9)})
	}
	sort.Slice(s.Jobs, func(i, j int) bool {
		if s.Jobs[i].MedianSeconds != s.Jobs[j].MedianSeconds {
			return s.Jobs[i].MedianSeconds > s.Jobs[j].MedianSeconds
		}
		return s.Jobs[i].Name < s.Jobs[j].Name
	})

	// Oldest first from here: red to green, and flakiness, follow time.
	sort.Slice(inPeriod, func(i, j int) bool {
		if !inPeriod[i].Created.Equal(inPeriod[j].Created) {
			return inPeriod[i].Created.Before(inPeriod[j].Created)
		}
		return inPeriod[i].ID < inPeriod[j].ID
	})
	redSince := map[string]time.Time{}
	var greens []int
	for _, r := range inPeriod {
		k := r.Workflow + "\x00" + r.Branch
		if failed(r.Conclusion) {
			if _, ok := redSince[k]; !ok {
				redSince[k] = r.Updated
			}
		} else if t, ok := redSince[k]; ok {
			greens = append(greens, int(r.Updated.Sub(t).Seconds()))
			delete(redSince, k)
		}
	}
	s.RedToGreenSeconds, s.RedToGreenCount = percentile(greens, 0.5), len(greens)

	// A job that failed and passed on one commit in one workflow.
	type seen struct{ failed, passed bool }
	bySha := map[string]map[string]*seen{}
	last := map[string]time.Time{}
	for _, r := range inPeriod {
		k := r.Workflow + "\x00" + r.Sha
		if bySha[k] == nil {
			bySha[k] = map[string]*seen{}
		}
		for _, j := range jobs[r.ID] {
			sn := bySha[k][j.Name]
			if sn == nil {
				sn = &seen{}
				bySha[k][j.Name] = sn
			}
			if failed(j.Conclusion) {
				sn.failed = true
			} else if j.Conclusion == "success" {
				sn.passed = true
			}
			if sn.failed && sn.passed && r.Updated.After(last[k+"\x00"+j.Name]) {
				last[k+"\x00"+j.Name] = r.Updated
			}
		}
	}
	flaky := map[string]*CIFlaky{}
	for k, names := range bySha {
		for name, sn := range names {
			if !sn.failed || !sn.passed {
				continue
			}
			f := flaky[name]
			if f == nil {
				f = &CIFlaky{Name: name, How: "failed and passed on the same commit"}
				flaky[name] = f
			}
			f.Times++
			if t := last[k+"\x00"+name]; t.After(f.Last) {
				f.Last = t
			}
		}
	}
	for _, f := range flaky {
		s.Flaky = append(s.Flaky, *f)
	}
	sort.Slice(s.Flaky, func(i, j int) bool {
		if s.Flaky[i].Times != s.Flaky[j].Times {
			return s.Flaky[i].Times > s.Flaky[j].Times
		}
		return s.Flaky[i].Name < s.Flaky[j].Name
	})
	// A run that passed only on a later attempt: which job failed first is
	// not in the run's latest attempt, so the run itself is named.
	retried := map[string]*CIFlaky{}
	var retriedNames []string
	for _, r := range inPeriod {
		if r.Attempt > 1 && r.Conclusion == "success" {
			f := retried[r.Workflow]
			if f == nil {
				f = &CIFlaky{Name: r.Workflow}
				retried[r.Workflow] = f
				retriedNames = append(retriedNames, r.Workflow)
			}
			f.Times++
			f.How = fmt.Sprintf("passed on attempt %d", r.Attempt)
			f.Last = r.Updated
		}
	}
	sort.Strings(retriedNames)
	for _, n := range retriedNames {
		s.Flaky = append(s.Flaky, *retried[n])
	}

	// What can be run again: each workflow's newest run on each branch,
	// where it failed. Newest first.
	newest := map[string]Run{}
	for _, r := range runs {
		if r.Status != "completed" {
			continue
		}
		k := r.Workflow + "\x00" + r.Branch
		if o, ok := newest[k]; !ok || r.Created.After(o.Created) || (r.Created.Equal(o.Created) && r.ID > o.ID) {
			newest[k] = r
		}
	}
	for _, r := range newest {
		if failed(r.Conclusion) {
			s.Failing = append(s.Failing, r)
		}
	}
	sort.Slice(s.Failing, func(i, j int) bool {
		if !s.Failing[i].Created.Equal(s.Failing[j].Created) {
			return s.Failing[i].Created.After(s.Failing[j].Created)
		}
		return s.Failing[i].ID > s.Failing[j].ID
	})
	return s
}

// percentile is the nearest-rank percentile of xs (p in 0..1), 0 for none.
func percentile(xs []int, p float64) int {
	if len(xs) == 0 {
		return 0
	}
	s := append([]int(nil), xs...)
	sort.Ints(s)
	i := int(math.Ceil(p*float64(len(s)))) - 1
	return s[max(0, min(i, len(s)-1))]
}

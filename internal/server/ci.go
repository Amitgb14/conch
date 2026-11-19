package server

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Amitgb14/conch/internal/ghx"
	"github.com/Amitgb14/conch/internal/proto"
)

// The CI panel's numbers: a project's GitHub Actions history read through
// gh and added up (ghx.ComputeStats), and a failed run's jobs run again.

const (
	ciDaysDefault = 14
	ciDaysMax     = 90
	// ciJobsPerCall bounds how many runs' jobs one ci.stats reads: each is
	// a gh call, and a first look at a busy repository could need hundreds.
	// The rest are said to be pending, and the next call reads more.
	ciJobsPerCall = 40
	ciJobsWorkers = 4
	// ciJobsKept bounds the jobs kept per project.
	ciJobsKept = 3000
)

type ciRunKey struct {
	id      int64
	attempt int
}

func ciNoGH(err error) *proto.Error {
	if errors.Is(err, ghx.ErrNoGH) {
		return proto.Errorf(proto.ErrBadRequest, "gh is not installed on this machine: install the GitHub CLI and run gh auth login to see CI")
	}
	return proto.Errorf(proto.ErrBadRequest, "%v", err)
}

func (pm *projectManager) ciStats(sp proto.CIStatsParams) (proto.CIStats, *proto.Error) {
	p, perr := pm.gitProject(sp.ProjectID)
	if perr != nil {
		return proto.CIStats{}, perr
	}
	days := sp.Days
	switch {
	case days <= 0:
		days = ciDaysDefault
	case days > ciDaysMax:
		days = ciDaysMax
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	// Two periods' worth: this one and the one it is compared with.
	runs, err := ghx.Runs(ctx, p.root, min(200+days*2*20, 1000))
	if err != nil {
		return proto.CIStats{}, ciNoGH(err)
	}
	now := time.Now()
	start := now.AddDate(0, 0, -days)

	p.mu.Lock()
	if p.ciJobs == nil {
		p.ciJobs = map[ciRunKey][]ghx.Job{}
	}
	jobs := map[int64][]ghx.Job{}
	var want []ghx.Run
	for _, r := range runs {
		if r.Status != "completed" || r.Created.Before(start) {
			continue
		}
		if js, ok := p.ciJobs[ciRunKey{r.ID, r.Attempt}]; ok {
			jobs[r.ID] = js
		} else {
			want = append(want, r)
		}
	}
	p.mu.Unlock()

	pending := max(len(want)-ciJobsPerCall, 0)
	want = want[:min(len(want), ciJobsPerCall)]
	var mu sync.Mutex
	var wg sync.WaitGroup
	next := make(chan ghx.Run)
	for range min(ciJobsWorkers, len(want)) {
		wg.Add(1)
		goSafe("ci jobs", func() {
			defer wg.Done()
			for r := range next {
				js, err := ghx.RunJobs(ctx, p.root, r.ID)
				mu.Lock()
				if err != nil {
					pending++
				} else {
					jobs[r.ID] = js
				}
				mu.Unlock()
				if err == nil {
					p.mu.Lock()
					if len(p.ciJobs) >= ciJobsKept {
						clear(p.ciJobs)
					}
					p.ciJobs[ciRunKey{r.ID, r.Attempt}] = js
					p.mu.Unlock()
				}
			}
		})
	}
	for _, r := range want {
		next <- r
	}
	close(next)
	wg.Wait()

	st := ghx.ComputeStats(runs, jobs, now, days, time.Local)
	out := proto.CIStats{
		Days: st.Days, Runs: st.Runs, Passed: st.Passed, Failed: st.Failed, PrevRuns: st.PrevRuns, PrevPassed: st.PrevPassed,
		MedianSeconds: st.MedianSeconds, PrevMedianSeconds: st.PrevMedianSeconds, QueueSeconds: st.QueueSeconds,
		RedToGreenSeconds: st.RedToGreenSeconds, RedToGreenCount: st.RedToGreenCount, JobsPending: pending,
	}
	for _, d := range st.Daily {
		out.Daily = append(out.Daily, proto.CIDay{Date: d.Date, Passed: d.Passed, Failed: d.Failed})
	}
	for _, j := range st.Jobs {
		out.Jobs = append(out.Jobs, proto.CIJob{Name: j.Name, Runs: j.Runs, Failed: j.Failed, MedianSeconds: j.MedianSeconds, P90Seconds: j.P90Seconds})
	}
	for _, f := range st.Flaky {
		out.Flaky = append(out.Flaky, proto.CIFlaky{Name: f.Name, Times: f.Times, How: f.How, Last: f.Last})
	}
	for _, r := range st.Failing {
		out.Failing = append(out.Failing, proto.CIRun{ID: r.ID, Workflow: r.Workflow, Branch: r.Branch, Sha: r.Sha,
			Conclusion: r.Conclusion, Attempt: r.Attempt, URL: r.URL, At: r.Updated})
	}
	return out, nil
}

func (pm *projectManager) ciRerun(rp proto.CIRerunParams) (proto.CIRerunResult, *proto.Error) {
	p, perr := pm.gitProject(rp.ProjectID)
	if perr != nil {
		return proto.CIRerunResult{}, perr
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	id := rp.RunID
	if id == 0 {
		if rp.Branch == "" {
			return proto.CIRerunResult{}, proto.Errorf(proto.ErrBadRequest, "no run or branch given")
		}
		p.mu.Lock()
		head := p.prs[rp.Branch].Head
		p.mu.Unlock()
		found, err := ghx.FailedRunID(ctx, p.root, rp.Branch, head)
		switch {
		case errors.Is(err, ghx.ErrNoFailedRun):
			return proto.CIRerunResult{}, proto.Errorf(proto.ErrNotFound,
				"no failed GitHub Actions run on %s to run again: the failing check is another service's, or it was already re-run", rp.Branch)
		case err != nil:
			return proto.CIRerunResult{}, ciNoGH(err)
		}
		id = found
	}
	if err := ghx.Rerun(ctx, p.root, id); err != nil {
		return proto.CIRerunResult{}, ciNoGH(err)
	}
	// Its checks are pending again: the pull request says so sooner.
	pm.requestPRs(p)
	return proto.CIRerunResult{RunID: id}, nil
}

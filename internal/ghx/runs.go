package ghx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// FailedRun is a GitHub Actions run that failed on a branch, with the log
// of its failed steps.
type FailedRun struct {
	ID       int64
	Workflow string
	URL      string
	Log      string // the failed steps' output, as gh prints it
}

// ErrNoFailedRun means nothing on the branch failed in GitHub Actions: the
// check that failed is someone else's (a status from another service), or
// the run has since been retried.
var ErrNoFailedRun = errors.New("no failed GitHub Actions run on this branch")

// FailedLog finds the newest failed Actions run on branch in the
// repository at dir — at commit head when given, so a run about older work
// is not mistaken for this one — and fetches its failed steps' log.
func FailedLog(ctx context.Context, dir, branch, head string) (FailedRun, error) {
	runs, err := failedRuns(ctx, dir, branch, head)
	if err != nil {
		return FailedRun{}, err
	}
	r := runs[0]
	log, err := run(ctx, dir, "run", "view", strconv.FormatInt(r.ID, 10), "--log-failed")
	if err != nil {
		return FailedRun{}, err
	}
	return FailedRun{ID: r.ID, Workflow: r.Workflow, URL: r.URL, Log: string(log)}, nil
}

// failedRuns are the failed Actions runs on branch, newest first — at
// commit head when given, so a run about older work is not taken for this
// one — or ErrNoFailedRun.
func failedRuns(ctx context.Context, dir, branch, head string) ([]Run, error) {
	out, err := run(ctx, dir, "run", "list", "--branch", branch, "--limit", "20",
		"--json", "databaseId,workflowName,url,headSha,conclusion")
	if err != nil {
		return nil, err
	}
	var runs []struct {
		ID         int64  `json:"databaseId"`
		Workflow   string `json:"workflowName"`
		URL        string `json:"url"`
		HeadSha    string `json:"headSha"`
		Conclusion string `json:"conclusion"`
	}
	if err := json.Unmarshal(out, &runs); err != nil {
		return nil, fmt.Errorf("gh run list: %w", err)
	}
	var found []Run
	for _, r := range runs {
		if !strings.EqualFold(r.Conclusion, "failure") && !strings.EqualFold(r.Conclusion, "timed_out") {
			continue
		}
		if head != "" && r.HeadSha != "" && r.HeadSha != head {
			continue
		}
		found = append(found, Run{ID: r.ID, Workflow: r.Workflow, URL: r.URL, Sha: r.HeadSha, Conclusion: r.Conclusion, Branch: branch})
	}
	if len(found) == 0 {
		return nil, ErrNoFailedRun
	}
	return found, nil
}

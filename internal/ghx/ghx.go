// Package ghx reads pull requests through the GitHub CLI, so conch uses the
// user's existing gh login and never handles tokens itself.
package ghx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Check summaries.
const (
	ChecksPass    = "pass"
	ChecksFail    = "fail"
	ChecksPending = "pending"
)

// PR is a pull request.
type PR struct {
	Number  int
	Title   string
	State   string // OPEN, MERGED, CLOSED
	Draft   bool
	URL     string
	Branch  string // head branch
	Review  string // APPROVED, CHANGES_REQUESTED, REVIEW_REQUIRED or ""
	Checks  string // ChecksPass, ChecksFail, ChecksPending or "" when none
	Passed  int    // checks that passed
	Total   int    // checks reported
	Updated time.Time
}

// ErrNoGH means the gh binary is not installed.
var ErrNoGH = errors.New("gh (GitHub CLI) is not installed")

const fields = "number,title,state,isDraft,url,headRefName,reviewDecision,statusCheckRollup,updatedAt"

// List returns recent pull requests of the repository in dir. The gh binary
// is $CONCH_GH when set.
func List(ctx context.Context, dir string) ([]PR, error) {
	bin := os.Getenv("CONCH_GH")
	if bin == "" {
		bin = "gh"
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		return nil, ErrNoGH
	}
	cmd := exec.CommandContext(ctx, path, "pr", "list", "--state", "all", "--limit", "100", "--json", fields)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1", "NO_COLOR=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if i := strings.IndexByte(msg, '\n'); i >= 0 {
			msg = msg[:i]
		}
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("gh pr list: %s", msg)
	}
	return Parse(stdout.Bytes())
}

type rawPR struct {
	Number         int       `json:"number"`
	Title          string    `json:"title"`
	State          string    `json:"state"`
	IsDraft        bool      `json:"isDraft"`
	URL            string    `json:"url"`
	HeadRefName    string    `json:"headRefName"`
	ReviewDecision string    `json:"reviewDecision"`
	UpdatedAt      time.Time `json:"updatedAt"`
	Rollup         []struct {
		Typename   string `json:"__typename"`
		Status     string `json:"status"`     // CheckRun
		Conclusion string `json:"conclusion"` // CheckRun
		State      string `json:"state"`      // StatusContext
	} `json:"statusCheckRollup"`
}

// Parse decodes `gh pr list --json` output.
func Parse(data []byte) ([]PR, error) {
	var raw []rawPR
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse gh output: %w", err)
	}
	prs := make([]PR, 0, len(raw))
	for _, r := range raw {
		pr := PR{
			Number: r.Number, Title: r.Title, State: r.State, Draft: r.IsDraft, URL: r.URL,
			Branch: r.HeadRefName, Review: r.ReviewDecision, Updated: r.UpdatedAt,
		}
		failed, pending := 0, 0
		for _, c := range r.Rollup {
			var outcome string
			if c.Typename == "StatusContext" {
				outcome = contextOutcome(c.State)
			} else {
				outcome = runOutcome(c.Status, c.Conclusion)
			}
			pr.Total++
			switch outcome {
			case ChecksPass:
				pr.Passed++
			case ChecksFail:
				failed++
			default:
				pending++
			}
		}
		switch {
		case failed > 0:
			pr.Checks = ChecksFail
		case pending > 0:
			pr.Checks = ChecksPending
		case pr.Total > 0:
			pr.Checks = ChecksPass
		}
		prs = append(prs, pr)
	}
	return prs, nil
}

func runOutcome(status, conclusion string) string {
	if status != "COMPLETED" {
		return ChecksPending
	}
	switch conclusion {
	case "SUCCESS", "NEUTRAL", "SKIPPED":
		return ChecksPass
	case "FAILURE", "TIMED_OUT", "CANCELLED", "ACTION_REQUIRED", "STARTUP_FAILURE", "STALE":
		return ChecksFail
	}
	return ChecksPending
}

func contextOutcome(state string) string {
	switch state {
	case "SUCCESS":
		return ChecksPass
	case "FAILURE", "ERROR":
		return ChecksFail
	}
	return ChecksPending
}

// ByBranch picks one pull request per head branch: an open one when there
// is one, else the most recently updated.
func ByBranch(prs []PR) map[string]PR {
	out := map[string]PR{}
	for _, pr := range prs {
		cur, ok := out[pr.Branch]
		switch {
		case !ok:
			out[pr.Branch] = pr
		case (pr.State == "OPEN") != (cur.State == "OPEN"):
			if pr.State == "OPEN" {
				out[pr.Branch] = pr
			}
		case pr.Updated.After(cur.Updated):
			out[pr.Branch] = pr
		}
	}
	return out
}

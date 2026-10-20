package proto

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// AutopilotPolicy is what a server does on its own (CapAutopilot): the
// [verify] settings of whoever's TUI last handed them over, kept by the
// server so they outlive that TUI.
type AutopilotPolicy struct {
	// AutoFix is how many failures in a row — CI or the project's own
	// check — go back to a branch's agent by themselves. 0 sends none.
	AutoFix int `json:"auto_fix,omitempty"`
	// AutoMerge merges a pull request on GitHub when its checks turn
	// green and nothing holds it back.
	AutoMerge bool `json:"auto_merge,omitempty"`
	// AutoPR pushes a branch whose agent has finished — once its check
	// passes, where the project has one — and opens a pull request for
	// it, or pushes the new commits to the one already open.
	AutoPR bool `json:"auto_pr,omitempty"`
	// AutoResolve is how many times in a row a pull request that
	// conflicts with its base goes back to the branch's agent to resolve,
	// once the server's own rebase onto the base has stopped on the
	// conflict. 0 leaves conflicts alone.
	AutoResolve int `json:"auto_resolve,omitempty"`
	// RequireTests opens a pull request whose branch changes code and no
	// test as a draft — which auto_merge does not merge — and asks the
	// agent to add one; MaxDiff does the same for one that changes more
	// lines than it says (0 is no limit). Once they are met, a later push
	// marks it ready.
	RequireTests bool `json:"require_tests,omitempty"`
	MaxDiff      int  `json:"max_diff,omitempty"`
	// Checks is each project's own check, by project ID: what the server
	// runs in a branch's worktree when its agent finishes.
	Checks map[string]string `json:"checks,omitempty"`
}

// AutopilotBranch is one branch the server is minding.
type AutopilotBranch struct {
	ProjectID string `json:"project_id"`
	Branch    string `json:"branch"`
	// Fixes is how many failures went back to its agent since it last
	// passed.
	Fixes int `json:"fixes,omitempty"`
	// Waiting is a green pull request waiting to merge, and Held what
	// last kept it back.
	Waiting bool   `json:"waiting,omitempty"`
	Held    string `json:"held,omitempty"`
	// CheckFailed is the project's own check failing at the branch's
	// current commit.
	CheckFailed bool `json:"check_failed,omitempty"`
	// Checking is the terminal the server is running the check in.
	Checking string `json:"checking,omitempty"`
	// Unshipped is what keeps a finished branch from being pushed or
	// given a pull request (auto_pr).
	Unshipped string `json:"unshipped,omitempty"`
	// Conflicting is a pull request that conflicts with its base, and
	// Resolves how many times its agent has been asked to resolve it.
	Conflicting bool `json:"conflicting,omitempty"`
	Resolves    int  `json:"resolves,omitempty"`
}

// AutopilotState answers autopilot.get and autopilot.set.
type AutopilotState struct {
	Policy   AutopilotPolicy   `json:"policy"`
	Branches []AutopilotBranch `json:"branches,omitempty"`
}

// AutopilotCheck is how a project's own check on a branch ended: Exit
// from the terminal it ran in, Pane, whose output the server reads to
// hand a failure to the agent, and Command, to name it there.
type AutopilotCheck struct {
	ProjectID string `json:"project_id"`
	Branch    string `json:"branch"`
	Pane      string `json:"pane,omitempty"`
	Command   string `json:"command,omitempty"`
	Exit      int    `json:"exit"`
}

// AutopilotNote is something the server did on its own, or held back
// from, said once. Merged marks a pull request it merged, so a client
// re-reads what it shows of that branch.
type AutopilotNote struct {
	ProjectID string `json:"project_id"`
	Branch    string `json:"branch"`
	Text      string `json:"text"`
	Error     bool   `json:"error,omitempty"`
	Merged    bool   `json:"merged,omitempty"`
}

// A failure sent to an agent is bounded: enough of the end of the output
// to show the error, never a megabyte of a log.
const (
	failureTailLines = 80
	failureTailBytes = 6000
)

// CheckPrefix names the terminal a project's check runs in, whether the
// server or a TUI started it: a TUI knows the server's by the name.
const CheckPrefix = "check · "

// PRChecks is a branch's CI as conch compares it, the server and the TUI
// alike: the checks of an open pull request, and "" for a branch with
// none. A closed or merged pull request is not somebody's move any more,
// so its checks are not either.
func PRChecks(b BranchInfo) string {
	if b.PR == nil || b.PR.State != "OPEN" {
		return ""
	}
	return b.PR.Checks
}

// FailureTail is the end of a check's output: blank lines at the bottom
// dropped, escape codes stripped, at most failureTailLines lines and
// failureTailBytes bytes, cut at a line.
func FailureTail(lines []string) string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > failureTailLines {
		lines = lines[len(lines)-failureTailLines:]
	}
	var kept []string
	size := 0
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimRight(ansi.Strip(lines[i]), " ")
		if size+len(l)+1 > failureTailBytes {
			if len(kept) == 0 { // one enormous line: keep its end
				l = strings.ToValidUTF8(l[len(l)-failureTailBytes+1:], "")
				kept = append(kept, l)
			}
			break
		}
		size += len(l) + 1
		kept = append(kept, l)
	}
	for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
		kept[i], kept[j] = kept[j], kept[i]
	}
	return strings.Join(kept, "\n")
}

// FailurePrompt is what the agent is told: which command, how it ended,
// its output, and what is wanted — a fix proved by running it again.
func FailurePrompt(what, command string, exit int, output string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s failed on this branch", what)
	if command != "" {
		fmt.Fprintf(&b, " (`%s`", command)
		if exit != 0 {
			fmt.Fprintf(&b, ", exit %d", exit)
		}
		b.WriteString(")")
	}
	b.WriteString(".")
	if output != "" {
		b.WriteString(" The end of its output:\n\n```\n" + output + "\n```\n\n")
	} else {
		b.WriteString(" It printed nothing.\n\n")
	}
	b.WriteString("Find the cause and fix it, then run it again to confirm it passes. If the failure is not in this branch's work, say so instead of changing unrelated code.")
	return b.String()
}

// CIFailurePrompt is FailurePrompt for a failed CI run, with a link to it.
func CIFailurePrompt(log BranchCILog) string {
	what := "CI"
	if log.Workflow != "" {
		what = "CI (" + log.Workflow + ")"
	}
	text := FailurePrompt(what, "", 0, FailureTail(strings.Split(log.Log, "\n")))
	if log.URL != "" {
		text += "\n\nThe run: " + log.URL
	}
	return text
}

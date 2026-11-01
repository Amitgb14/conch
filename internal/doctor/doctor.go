// Package doctor is what `conch doctor` finds and how it is said: a check
// of something conch leans on, its outcome, and the fix. The checks
// themselves run in cmd/conch, where the server and the tools are reached;
// the TUI is handed them and shows the same words.
package doctor

import (
	"fmt"
	"strings"
)

// Check outcomes.
const (
	OK   = "ok"
	Warn = "warn" // works, but something is missing or out of date
	Fail = "fail" // conch cannot do what it is for until this is fixed
)

// Check is one thing checked.
type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

// Mark is a check's outcome as one character.
func (c Check) Mark() string {
	switch c.Status {
	case Warn:
		return "!"
	case Fail:
		return "✗"
	}
	return "✓"
}

// Command is the fix as something to paste into a shell, or "" when the
// fix is advice rather than a command. A note after two spaces — "(or
// codex, gemini…)", "(panes and agents keep running)" — is for reading,
// and pasted it is a syntax error, so it is left off; so is a fix that
// is more than one step said in words ("conch server stop, then conch").
func (c Check) Command() string {
	cmd := c.Fix
	if i := strings.Index(cmd, "  ("); i >= 0 {
		cmd = cmd[:i]
	}
	cmd = strings.TrimSpace(cmd)
	first, _, _ := strings.Cut(cmd, " ")
	switch first {
	case "conch", "git", "gh", "xcode-select":
	default:
		return ""
	}
	if strings.Contains(cmd, ", ") || strings.Contains(cmd, "; ") {
		return ""
	}
	return cmd
}

// Count is how many checks warned and how many failed.
func Count(checks []Check) (warns, fails int) {
	for _, c := range checks {
		switch c.Status {
		case Warn:
			warns++
		case Fail:
			fails++
		}
	}
	return warns, fails
}

// Summary is the answer at the bottom of the list, in a line.
func Summary(checks []Check) string {
	warns, fails := Count(checks)
	switch {
	case fails == 0 && warns == 0:
		return "All good."
	case fails == 0:
		return fmt.Sprintf("conch works; %s worth a look.", counted(warns, "thing"))
	case warns == 0:
		return counted(fails, "thing") + " to fix."
	}
	return fmt.Sprintf("%s to fix, %d more worth a look.", counted(fails, "thing"), warns)
}

// Text lays the checks out one to a line, the fix indented beneath, and
// ends with the summary so a long list still has an answer at the bottom.
func Text(checks []Check) string {
	var b strings.Builder
	for _, c := range checks {
		fmt.Fprintf(&b, "%s %-9s %s\n", c.Mark(), c.Name, c.Detail)
		if c.Fix != "" {
			fmt.Fprintf(&b, "  %-9s → %s\n", "", c.Fix)
		}
	}
	return b.String() + "\n" + Summary(checks) + "\n"
}

func counted(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}

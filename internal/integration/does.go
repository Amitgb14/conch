package integration

// Feature is one thing an integration does in conch, as an integration's
// page says it: what, where in conch, and how fully — "yes", "partly",
// "no", or "built-in" for what conch already has without it.
type Feature struct {
	What  string `json:"what"`
	Where string `json:"where"`
	Level string `json:"level"`
}

// Does lists what a file of kind with these operations does in conch,
// from the kind's contract rather than written per integration, so every
// page says it the same way.
func Does(kind string, ops []string) []Feature {
	has := func(op string) bool { return slicesHas(ops, op) }
	level := func(ok bool) string {
		if ok {
			return "yes"
		}
		return "no"
	}
	switch kind {
	case KindTickets:
		mine := Feature{"Your own tickets", "Mine in the Tickets view, conch ticket ls -mine", level(has("mine"))}
		return []Feature{
			{"Open tickets beside GitHub's issues", "The Tickets view, conch ticket all, and each project's tickets", level(has("list"))},
			{"Start a task from a ticket", "New Task, conch task -ticket NAME:KEY (shortcut:123): its text and comments become the prompt", level(has("get"))},
			mine,
			{"Comment on a ticket", "conch ticket comment, and agents through it", level(has("comment"))},
			{"Link the branch and pull request", "conch ticket link", level(has("link"))},
			{"Move a ticket", "conch ticket status", level(has("move"))},
		}
	case KindNotify:
		return []Feature{
			{"Agent waiting, agent done, pull request merged", "Sent by conch's server, as the desktop notifications are, whether or not conch is open", level(has("send"))},
		}
	case KindCI:
	default:
		return nil
	}
	out := []Feature{
		{"A failed job's output goes to the branch's agent", "The autopilot's auto_fix, conch branch fix-ci, and the review queue's f", level(has("failed_log"))},
		{"Re-run what failed", "conch branch rerun-ci", level(has("rerun"))},
		{"Runs for a branch and commit", "Which run a red check came from, its link and duration", level(has("runs"))},
	}
	stats := Feature{"CI panel statistics", "Durations and pass rate", level(has("stats"))}
	if !has("stats") && has("runs") {
		stats.Level, stats.Where = "partly", "Durations and pass rate, from the recent runs only"
	}
	return append(out, stats,
		Feature{"Checks on the pull request", "Already there: the service reports to GitHub, and conch reads GitHub's checks", "built-in"})
}

// Package report builds what somebody hitting a problem can hand over in
// one keystroke: the facts about their conch, written out for a GitHub
// issue, and the link that opens one with it already filled in.
//
// The rule the whole package is built on is **facts, not contents**:
// versions, counts, sizes, capability names and the last error's words.
// Never a screen, a prompt, a path, a project, a branch or a pane's title.
// That is what makes it safe to put behind one key — nothing has to be read
// through before it is sent, and nobody has to trust a redactor.
//
// Nothing here sends anything. It renders text and a URL; the browser opens
// with that text visible, and the person presses submit.
package report

import (
	"fmt"
	"net/url"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Repo is where conch's issues live.
const Repo = "https://github.com/Amitgb14/conch"

// maxBody is how much of the body a GitHub issue link carries. Browsers and
// servers both give up on a long URL (8 KiB is the usual limit), and a
// truncated report is worse than a short one, so the block is kept well
// inside it and the overflow goes to the clipboard instead.
const maxBody = 4000

// Facts is one conch, as a report describes it.
type Facts struct {
	// Version and Build are this binary's; Platform is its GOOS/GOARCH.
	Version  string
	Build    string
	Platform string
	// Server is the server this conch is talking to, if any.
	Server Server
	// Terminal is what conch is drawing on, for a TUI report.
	Terminal Terminal
	// Agents counts the agents running, by name: claude×2, codex. The
	// names are conch's own words for them, not anything a person typed.
	Agents map[string]int
	// Machines is how many machines are in the catalog and how many of
	// them answered.
	Machines, MachinesOnline int
	// LastError is the last thing conch said went wrong — its own message,
	// which is written for people and names no file.
	LastError string
	// What the person was doing, when they have said.
	What string
}

// Server is the conch server a report is about.
type Server struct {
	Version  string
	Build    string
	Platform string
	PID      int
	Started  time.Time
	LoadedAt time.Time
	Panes    int
	// Missing are capabilities this build expects and the server lacks,
	// which is the first thing to look at in a report from a stale server.
	Missing []string
}

// Terminal is what the TUI is drawing on.
type Terminal struct {
	Program string // $TERM_PROGRAM, e.g. iTerm.app
	Term    string // $TERM
	Cols    int
	Rows    int
	Icons   string // the [ui] icons setting: text, nerd or off
	Tmux    bool
	SSH     bool
}

// Now is the clock, for tests.
var Now = time.Now

// Text is the block a report carries: one fact per line, nothing that could
// hold what somebody typed or what an agent said.
func (f Facts) Text() string {
	var b strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }

	line("conch %s", versionLine(f.Version, f.Build, f.Platform))
	switch {
	case f.Server.PID == 0:
		line("server: not running, or not reached")
	default:
		line("server: %s, pid %d%s", versionLine(f.Server.Version, f.Server.Build, f.Server.Platform),
			f.Server.PID, uptime(f.Server.Started))
		if !f.Server.LoadedAt.IsZero() && f.Server.LoadedAt.After(f.Server.Started) {
			line("  reloaded %s ago", since(f.Server.LoadedAt))
		}
		if len(f.Server.Missing) > 0 {
			line("  missing: %s", strings.Join(f.Server.Missing, " "))
		}
	}
	line("panes: %d%s", f.Server.Panes, agentsPart(f.Agents))
	if f.Machines > 0 {
		line("machines: %d, %d reachable", f.Machines, f.MachinesOnline)
	}
	if t := f.Terminal.line(); t != "" {
		line("terminal: %s", t)
	}
	if f.LastError != "" {
		line("last error: %s", oneLine(f.LastError))
	}
	return b.String()
}

// line is the terminal a TUI report was made on; "" when there was none
// (a report from a command).
func (t Terminal) line() string {
	var parts []string
	if t.Program != "" {
		parts = append(parts, t.Program)
	}
	if t.Term != "" {
		parts = append(parts, t.Term)
	}
	if t.Cols > 0 && t.Rows > 0 {
		parts = append(parts, fmt.Sprintf("%d×%d", t.Cols, t.Rows))
	}
	if t.Icons != "" {
		parts = append(parts, "icons="+t.Icons)
	}
	if t.Tmux {
		parts = append(parts, "tmux")
	}
	if t.SSH {
		parts = append(parts, "over ssh")
	}
	return strings.Join(parts, ", ")
}

func versionLine(version, build, platform string) string {
	s := version
	if s == "" {
		s = "unknown version"
	}
	var in []string
	if build != "" {
		in = append(in, "build "+build)
	}
	if platform == "" && version != "" {
		platform = runtime.GOOS + "/" + runtime.GOARCH
	}
	if platform != "" {
		in = append(in, platform)
	}
	if len(in) > 0 {
		s += " (" + strings.Join(in, ", ") + ")"
	}
	return s
}

// agentsPart lists the agents running, in a fixed order so two reports of
// the same thing read the same.
func agentsPart(agents map[string]int) string {
	if len(agents) == 0 {
		return ""
	}
	names := make([]string, 0, len(agents))
	for name := range agents {
		names = append(names, name)
	}
	sort.Strings(names)
	var parts []string
	for _, name := range names {
		if n := agents[name]; n > 1 {
			parts = append(parts, fmt.Sprintf("%s×%d", name, n))
		} else if n == 1 {
			parts = append(parts, name)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return " · " + strings.Join(parts, ", ")
}

func uptime(started time.Time) string {
	if started.IsZero() {
		return ""
	}
	return ", up " + since(started)
}

// since is a rough age: a report wants the order of magnitude, not seconds.
func since(t time.Time) string {
	d := Now().Sub(t)
	switch {
	case d < 0:
		return "0m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// oneLine keeps a message to a line and a sensible length: it goes in a URL,
// and a wrapped error reads as several facts.
func oneLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	const max = 300
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}

// Body is the issue's body: what the person says first, then the facts in a
// fenced block so GitHub leaves them alone.
func (f Facts) Body() string {
	var b strings.Builder
	if what := strings.TrimSpace(f.What); what != "" {
		b.WriteString(what + "\n\n")
	} else {
		b.WriteString("<!-- What happened, and what you expected. -->\n\n")
	}
	b.WriteString("```\n")
	b.WriteString(f.Text())
	b.WriteString("```\n")
	return b.String()
}

// IssueURL opens a new issue with the title and body already in it. The
// person sees every word before anything is sent — the browser is where
// this ends, not a request from conch.
func (f Facts) IssueURL(title string) string {
	body := f.Body()
	if len(body) > maxBody {
		// Too long for a link: the facts alone still fit, and what the
		// person wrote is theirs to paste back.
		body = "```\n" + f.Text() + "```\n"
	}
	if len(body) > maxBody {
		body = body[:maxBody] + "\n```\n"
	}
	q := url.Values{}
	if title = oneLine(title); title != "" {
		q.Set("title", title)
	}
	q.Set("body", body)
	return Repo + "/issues/new?" + q.Encode()
}

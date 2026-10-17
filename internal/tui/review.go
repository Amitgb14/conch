package tui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

// Reviewing an agent's work is reading the diff and saying what is wrong
// with it, and the saying used to happen somewhere else: copied into the
// agent's prompt by hand, a line reference at a time. In the changes view
// a comments the hunk under the cursor, and S sends every comment on the
// branch to its agent as one message, each with the file and the hunk it
// is about quoted — the hunk itself, so a comment still makes sense after
// the agent has rewritten the lines it was about.

// The comments are the server's (review.go there), so another TUI, the
// command line, the phone and a reviewing agent see the same ones, and they
// outlive this view and this TUI. The machine holds what its server last
// said; the view reads its branch's share of that whenever it draws.

type reviewsMsg struct {
	machine string
	gen     int
	list    proto.ReviewList
	flash   string
}

// loadReviews asks a machine for every branch's comments.
func (mach *machine) loadReviews() tea.Cmd {
	c, id, gen := mach.c, mach.id, mach.gen
	if c == nil || len(c.MissingCapabilities([]string{proto.CapReviewComments})) > 0 {
		return nil
	}
	return func() tea.Msg {
		var list proto.ReviewList
		if err := callCtx(c, proto.MethodReviewList, proto.ReviewRef{}, &list); err != nil {
			return nil
		}
		return reviewsMsg{machine: id, gen: gen, list: list}
	}
}

// setReviews takes in what the server says: every comment when the list
// names no branch, else that branch's.
func (mach *machine) setReviews(list proto.ReviewList) {
	if list.ProjectID == "" && list.Branch == "" {
		mach.reviews = list.Comments
		return
	}
	kept := slices.DeleteFunc(slices.Clone(mach.reviews), func(c proto.ReviewComment) bool {
		return c.ProjectID == list.ProjectID && c.Branch == list.Branch
	})
	mach.reviews = append(kept, list.Comments...)
}

// reviewsOn is a branch's comments, in the order they were written.
func (mach *machine) reviewsOn(projectID, branch string) []proto.ReviewComment {
	var out []proto.ReviewComment
	for _, c := range mach.reviews {
		if c.ProjectID == projectID && c.Branch == branch {
			out = append(out, c)
		}
	}
	return out
}

func (m *Model) receiveReviews(msg reviewsMsg) {
	mach := m.machine(msg.machine)
	if mach == nil || msg.gen != mach.gen {
		return
	}
	mach.setReviews(msg.list)
	if msg.flash != "" {
		m.setFlash(msg.flash, false)
	}
}

// syncComments brings the view's comments up to what its machine has.
func (cv *changesView) syncComments(m Model) {
	cv.comments = nil
	if mach := m.machine(cv.machine); mach != nil {
		cv.comments = mach.reviewsOn(cv.projectID, cv.branch)
	}
}

// commentAt is this conch's own comment on hunk i of the open diff, if
// any: the one a edits. A pull request's are GitHub's (hunkComments).
func (cv *changesView) commentAt(i int) (int, bool) {
	_, hunks, _ := cv.hunksOf()
	if i < 0 || i >= len(hunks) {
		return -1, false
	}
	quoted := proto.QuoteHunk(hunks[i])
	for n, c := range cv.comments {
		if c.Source == "" && c.File == cv.diffFile && c.Hunk == quoted {
			return n, true
		}
	}
	return -1, false
}

// hunkComments is every comment on hunk i of the open diff: this conch's,
// by the hunk it quotes, and a pull request's, by the line it is on now.
// An outdated one is about lines no longer there, so it is on no hunk.
func (cv *changesView) hunkComments(i int) []proto.ReviewComment {
	_, hunks, _ := cv.hunksOf()
	if i < 0 || i >= len(hunks) {
		return nil
	}
	quoted := proto.QuoteHunk(hunks[i])
	start, n := newRangeOf(strings.SplitN(hunks[i], "\n", 2)[0])
	var out []proto.ReviewComment
	for _, c := range cv.comments {
		switch {
		case c.File != cv.diffFile:
		case c.Source == "" && c.Hunk == quoted:
			out = append(out, c)
		case c.Source != "" && !c.Outdated && start > 0 && c.Line >= start && c.Line < start+max(n, 1):
			out = append(out, c)
		}
	}
	return out
}

// offHunks is the open file's comments that are on none of its hunks —
// outdated ones, ones about the whole file — and the pull request's own.
func (cv *changesView) offHunks() []proto.ReviewComment {
	_, hunks, _ := cv.hunksOf()
	on := map[string]bool{}
	for i := range hunks {
		for _, c := range cv.hunkComments(i) {
			on[c.ID] = true
		}
	}
	var out []proto.ReviewComment
	for _, c := range cv.comments {
		if !on[c.ID] && (c.File == cv.diffFile || c.File == "") {
			out = append(out, c)
		}
	}
	return out
}

// newRangeOf reads the new side of a hunk header: "@@ -3,4 +5,6 @@" gives
// 5 and 6, and "+5" alone one line. 0 when it can't be read.
func newRangeOf(header string) (start, n int) {
	start = newStartOf(header)
	if start == 0 {
		return 0, 0
	}
	field := header[strings.Index(header, "+")+1:]
	if end := strings.IndexAny(field, " @"); end >= 0 {
		field = field[:end]
	}
	_, count, ok := strings.Cut(field, ",")
	if !ok {
		return start, 1
	}
	n, err := strconv.Atoi(count)
	if err != nil || n < 0 {
		return start, 1
	}
	return start, n
}

// fileComments counts the comments on a file.
func (cv *changesView) fileComments(file string) int {
	n := 0
	for _, c := range cv.comments {
		if c.File == file {
			n++
		}
	}
	return n
}

// githubComments counts the comments from the pull request.
func (cv *changesView) githubComments() int {
	n := 0
	for _, c := range cv.comments {
		if c.Source != "" {
			n++
		}
	}
	return n
}

// hunkMark is what the gutter shows for hunk i beside the cursor: a comment
// first, since that is what will be sent, then a mark for the commit.
func (cv *changesView) hunkMark(i int) string {
	if len(cv.hunkComments(i)) > 0 {
		return styleWarn.Render("✎")
	}
	if cv.markedHunk(i) {
		return styleOK.Render("✓")
	}
	return " "
}

// commentLines say what was written about the hunk under the cursor, and
// about this file and the pull request elsewhere, at most max lines: who,
// where, and as much of it as fits.
func (cv *changesView) commentLines(w, most int) []string {
	_, hunks, _ := cv.hunksOf()
	if len(cv.comments) == 0 || len(hunks) == 0 && cv.diffFile == "" || most <= 0 {
		return nil
	}
	here := cv.hunkComments(clamp(cv.hunkSel, 0, max(len(hunks)-1, 0)))
	all := append(here, cv.offHunks()...)
	var out []string
	for n, c := range all {
		if len(out) == most-1 && len(all) > most {
			out = append(out, styleMuted.Render(fmt.Sprintf("  … %d more · conch review lists them all", len(all)-n)))
			break
		}
		who := proto.ReviewAuthor(c)
		if who == "" {
			who = "you"
		}
		where := ""
		if n >= len(here) {
			where = " · " + proto.ReviewWhere(c)
		} else if c.Line > 0 {
			where = fmt.Sprintf(" · line %d", c.Line)
		}
		if c.Outdated {
			where += " (outdated)"
		}
		switch r := len(c.Replies); {
		case r == 1:
			where += " · 1 reply"
		case r > 1:
			where += fmt.Sprintf(" · %d replies", r)
		}
		note := strings.Join(strings.Fields(c.Note), " ")
		out = append(out, ansi.Truncate(styleWarn.Render("✎ "+who)+styleMuted.Render(where+": ")+note, max(w, 1), "…"))
	}
	return out
}

const reviewsPredate = "the server there predates review comments; reload it (conch server reload)"

// commentHunk asks for a comment on the hunk under the cursor, or edits the
// one already there; an empty comment removes it.
func (cv *changesView) commentHunk(m *Model) tea.Cmd {
	cv.syncComments(*m)
	_, hunks, _ := cv.hunksOf()
	switch {
	case cv.data == nil || cv.data.Worktree == "":
		m.setFlash("comments go to the agent working on the branch: check it out first (c in the tree starts one)", true)
		return nil
	case len(hunks) == 0:
		m.setFlash("this diff has no hunks to comment on", true)
		return nil
	case !m.hasCapability(cv.machine, proto.CapReviewComments):
		m.setFlash(reviewsPredate, true)
		return nil
	}
	i := clamp(cv.hunkSel, 0, len(hunks)-1)
	existing, had := cv.commentAt(i)
	value := ""
	if had {
		value = cv.comments[existing].Note
	}
	header := strings.SplitN(hunks[i], "\n", 2)[0]
	d := newDialog(*m, " Comment · "+cv.diffFile+" ", []string{
		"On " + header,
		"Sent to the branch's agent with the hunk quoted, when you press S. Empty removes it.",
	}, []string{"Comment"}, []string{value})
	c := proto.ReviewComment{ProjectID: cv.projectID, Branch: cv.branch, File: cv.diffFile, Hunk: hunks[i]}
	machine, branch := cv.machine, cv.branch
	d.submit = func(m *Model, v []string) tea.Cmd {
		m.overlay = nil
		c.Note = strings.TrimSpace(v[0])
		mach := m.machine(machine)
		if mach == nil || mach.c == nil {
			m.setFlash(m.offlineText(machine), true)
			return nil
		}
		gen := mach.gen
		var list proto.ReviewList
		return m.callOn(machine, proto.MethodReviewSet, c, &list, func() tea.Msg {
			flash := "comment removed"
			if c.Note != "" {
				flash = fmt.Sprintf("%s on %s · S sends them to its agent", count(len(list.Comments), "comment"), branch)
			}
			return reviewsMsg{machine: machine, gen: gen, list: list, flash: flash}
		})
	}
	m.overlay = d
	return d.focusCmd()
}

// sendReview has the server hand the branch's comments to its agent, or
// start the default agent in its worktree with them; it takes them away
// once they are delivered.
func (cv *changesView) sendReview(m *Model) tea.Cmd {
	cv.syncComments(*m)
	switch {
	case !m.hasCapability(cv.machine, proto.CapReviewComments):
		m.setFlash(reviewsPredate, true)
		return nil
	case len(cv.comments) == 0:
		m.setFlash("no comments to send: a on a hunk of a diff writes one", true)
		return nil
	}
	n := len(cv.comments)
	machine, branch, agent := cv.machine, cv.branch, m.defaultAgent()
	cols, rows := m.paneArea()
	m.setFlash("sending "+count(n, "review comment")+"…", false)
	var res proto.ReviewSendResult
	params := proto.ReviewSendParams{ProjectID: cv.projectID, Branch: branch, Agent: agent, Cols: cols, Rows: rows}
	mach := m.machine(machine)
	if mach == nil || mach.c == nil {
		m.setFlash(m.offlineText(machine), true)
		return nil
	}
	c := mach.c
	return func() tea.Msg {
		err := callCtx(c, proto.MethodReviewSend, params, &res)
		switch {
		case err != nil:
			return promptSentMsg{machine: machine, err: err}
		case res.Started:
			return flashMsg("started " + agentLabel(agent) + " on " + branch + " with " + count(res.Sent, "review comment"))
		}
		name := ""
		if res.Pane.Agent != nil {
			name = res.Pane.Agent.Name
		}
		return promptSentMsg{machine: machine, pane: res.Pane.ID, res: proto.AgentPromptResult{ID: res.Pane.ID, Agent: name, Turn: res.Turn}}
	}
}

package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/client"
)

// Reviewing an agent's work is reading the diff and saying what is wrong
// with it, and the saying used to happen somewhere else: copied into the
// agent's prompt by hand, a line reference at a time. In the changes view
// a comments the hunk under the cursor, and S sends every comment on the
// branch to its agent as one message, each with the file and the hunk it
// is about quoted — the hunk itself, so a comment still makes sense after
// the agent has rewritten the lines it was about.

// reviewComment is one comment on a hunk of a branch's diff.
type reviewComment struct {
	file string
	hunk string // the hunk's text when the comment was made
	note string
}

// maxHunkQuote bounds how much of a hunk a comment quotes: enough to say
// where, not a whole file rewritten.
const maxHunkQuote = 40

// commentAt is the comment on hunk i of the open diff, if any.
func (cv *changesView) commentAt(i int) (int, bool) {
	_, hunks, _ := cv.hunksOf()
	if i < 0 || i >= len(hunks) {
		return -1, false
	}
	for n, c := range cv.comments {
		if c.file == cv.diffFile && c.hunk == hunks[i] {
			return n, true
		}
	}
	return -1, false
}

// fileComments counts the comments on a file.
func (cv *changesView) fileComments(file string) int {
	n := 0
	for _, c := range cv.comments {
		if c.file == file {
			n++
		}
	}
	return n
}

// hunkMark is what the gutter shows for hunk i beside the cursor: a comment
// first, since that is what will be sent, then a mark for the commit.
func (cv *changesView) hunkMark(i int) string {
	if _, ok := cv.commentAt(i); ok {
		return styleWarn.Render("✎")
	}
	if cv.markedHunk(i) {
		return styleOK.Render("✓")
	}
	return " "
}

// commentHunk asks for a comment on the hunk under the cursor, or edits the
// one already there; an empty comment removes it.
func (cv *changesView) commentHunk(m *Model) tea.Cmd {
	_, hunks, _ := cv.hunksOf()
	switch {
	case cv.data == nil || cv.data.Worktree == "":
		m.setFlash("comments go to the agent working on the branch: check it out first (c in the tree starts one)", true)
		return nil
	case len(hunks) == 0:
		m.setFlash("this diff has no hunks to comment on", true)
		return nil
	}
	i := clamp(cv.hunkSel, 0, len(hunks)-1)
	existing, had := cv.commentAt(i)
	value := ""
	if had {
		value = cv.comments[existing].note
	}
	header := strings.SplitN(hunks[i], "\n", 2)[0]
	d := newDialog(*m, " Comment · "+cv.diffFile+" ", []string{
		"On " + header,
		"Sent to the branch's agent with the hunk quoted, when you press S. Empty removes it.",
	}, []string{"Comment"}, []string{value})
	file, hunk := cv.diffFile, hunks[i]
	d.submit = func(m *Model, v []string) tea.Cmd {
		note := strings.TrimSpace(v[0])
		m.overlay = nil
		cv.setComment(file, hunk, note)
		if note == "" {
			m.setFlash("comment removed", false)
			return nil
		}
		m.setFlash(fmt.Sprintf("%s on %s · S sends them to its agent", count(len(cv.comments), "comment"), cv.branch), false)
		return nil
	}
	m.overlay = d
	return d.focusCmd()
}

// setComment puts, replaces or (with an empty note) removes the comment on
// a hunk.
func (cv *changesView) setComment(file, hunk, note string) {
	for n, c := range cv.comments {
		if c.file == file && c.hunk == hunk {
			if note == "" {
				cv.comments = append(cv.comments[:n], cv.comments[n+1:]...)
			} else {
				cv.comments[n].note = note
			}
			return
		}
	}
	if note != "" {
		cv.comments = append(cv.comments, reviewComment{file: file, hunk: hunk, note: note})
	}
}

// reviewPrompt is the message the agent gets: every comment, numbered,
// with where it is and what it is about.
func reviewPrompt(branch string, comments []reviewComment) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Review comments on your changes on %s:\n", branch)
	for n, c := range comments {
		lines := strings.Split(strings.TrimRight(c.hunk, "\n"), "\n")
		header := lines[0]
		body := lines[1:]
		cut := ""
		if len(body) > maxHunkQuote {
			cut = fmt.Sprintf("… %d more lines\n", len(body)-maxHunkQuote)
			body = body[:maxHunkQuote]
		}
		fmt.Fprintf(&b, "\n%d. %s, at %s\n```diff\n%s\n%s```\n%s\n", n+1, c.file, header,
			strings.Join(body, "\n"), cut, c.note)
	}
	b.WriteString("\nAddress each comment. If you disagree with one, say why rather than changing the code.")
	return b.String()
}

// sendReview hands the branch's comments to its agent, or starts the
// default agent in its worktree with them, and clears them once sent.
func (cv *changesView) sendReview(m *Model) tea.Cmd {
	if len(cv.comments) == 0 {
		m.setFlash("no comments to send: a on a hunk of a diff writes one", true)
		return nil
	}
	text := reviewPrompt(cv.branch, cv.comments)
	n := len(cv.comments)
	cmd := m.sendToBranchAgent(cv.machine, cv.projectID, cv.branch, count(n, "review comment"), func(*client.Client) (string, error) {
		return text, nil
	})
	if cmd != nil {
		cv.comments = nil
	}
	return cmd
}

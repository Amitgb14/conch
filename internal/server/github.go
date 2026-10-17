package server

import (
	"context"
	"fmt"
	"log"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Amitgb14/conch/internal/ghx"
	"github.com/Amitgb14/conch/internal/proto"
)

// A branch's pull request is reviewed on GitHub, and what the reviewers
// said is work for the branch's agent as much as a comment written in the
// changes view. So the unresolved threads and the reviews that asked for
// something are read through gh — the person's own login, as for the pull
// requests themselves — and listed with conch's own comments: the TUI
// marks them on the diff, conch review and the phone show them, and S
// hands them over with the rest. They are GitHub's, so they are never
// changed here, and sending one does not resolve it; it is remembered as
// handed over instead, and comes back when somebody adds to its thread.

const (
	// githubRefresh re-reads a pull request that hasn't moved: resolving a
	// thread does not always change the pull request's own time.
	githubRefresh = 10 * time.Minute
	// githubHunkLines is how much of GitHub's hunk a comment keeps: the
	// lines just above it, which is where GitHub's hunk ends.
	githubHunkLines = 12
)

// prReviews is what one branch's pull request says.
type prReviews struct {
	projectID, branch string
	number            int
	updated           time.Time // the pull request's, when this was read
	read              time.Time
	comments          []proto.ReviewComment
	last              map[string]string // comment ID → its thread's newest comment
}

// handedTo is a pull request's comment that went to the agent, as it
// stood: Last is the newest comment of its thread then.
type handedTo struct {
	ProjectID string `json:"project_id"`
	Branch    string `json:"branch"`
	Last      string `json:"last"`
}

// githubLocked adds what pull requests say to out, after conch's own,
// leaving out what was handed over already.
func (b *reviewBook) githubLocked(ref proto.ReviewRef, out proto.ReviewList) proto.ReviewList {
	// In order of branch, or map order would shuffle the list each call.
	for _, key := range slices.Sorted(maps.Keys(b.github)) {
		pr := b.github[key]
		if ref.ProjectID != "" && pr.projectID != ref.ProjectID || ref.Branch != "" && pr.branch != ref.Branch {
			continue
		}
		for _, c := range pr.comments {
			if h, ok := b.handed[c.ID]; ok && h.Last == pr.last[c.ID] {
				continue
			}
			out.Comments = append(out.Comments, c)
		}
	}
	return out
}

// githubOwnerLocked is the pull request a comment ID is from, if any.
func (b *reviewBook) githubOwnerLocked(id string) *prReviews {
	if id == "" {
		return nil
	}
	for _, pr := range b.github {
		if _, ok := pr.last[id]; ok {
			return pr
		}
	}
	return nil
}

// githubDue says whether a branch's pull request should be read again.
func (b *reviewBook) githubDue(projectID, branch string, updated, now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	pr := b.github[branchKey(projectID, branch)]
	return pr == nil || !pr.updated.Equal(updated) || now.Sub(pr.read) > githubRefresh
}

// setGitHub keeps what a pull request says, and says whether that changed
// what the branch's list shows. A failed read (rv nil) keeps what was
// there and waits for the pull request to move or githubRefresh.
func (b *reviewBook) setGitHub(projectID, branch string, number int, rv *ghx.Reviews, updated, now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	key := branchKey(projectID, branch)
	old := b.github[key]
	ref := proto.ReviewRef{ProjectID: projectID, Branch: branch}
	before := b.githubLocked(ref, proto.ReviewList{}).Comments
	pr := &prReviews{projectID: projectID, branch: branch, number: number, updated: updated, read: now, last: map[string]string{}}
	if rv == nil {
		if old != nil {
			pr.comments, pr.last = old.comments, old.last
		}
		b.github[key] = pr
		return false
	}
	pr.comments, pr.last = prComments(projectID, branch, *rv)
	b.github[key] = pr
	// A thread resolved since is gone for good: so is its handing over.
	pruned := false
	for id, h := range b.handed {
		if h.ProjectID == projectID && h.Branch == branch {
			if _, ok := pr.last[id]; !ok {
				delete(b.handed, id)
				pruned = true
			}
		}
	}
	if pruned {
		b.saveLocked()
	}
	return !reflect.DeepEqual(before, b.githubLocked(ref, proto.ReviewList{}).Comments)
}

// forgetGitHub drops the pull requests of a project's branches that have
// none open any more — merged, closed, or the branch gone — and says
// which branches showed comments from them.
func (b *reviewBook) forgetGitHub(projectID string, open map[string]bool) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var had []string
	for key, pr := range b.github {
		if pr.projectID != projectID || open[pr.branch] {
			continue
		}
		if len(b.githubLocked(proto.ReviewRef{ProjectID: projectID, Branch: pr.branch}, proto.ReviewList{}).Comments) > 0 {
			had = append(had, pr.branch)
		}
		delete(b.github, key)
	}
	return had
}

// prComments is a pull request's threads and reviews as review comments,
// and each one's newest comment, by which a thread added to is told from
// one handed over already.
func prComments(projectID, branch string, rv ghx.Reviews) ([]proto.ReviewComment, map[string]string) {
	var out []proto.ReviewComment
	last := map[string]string{}
	for _, th := range rv.Threads {
		first := th.Comments[0]
		c := proto.ReviewComment{
			ID: "gh" + strconv.FormatInt(first.ID, 10), ProjectID: projectID, Branch: branch,
			File: th.Path, Line: th.Line, Hunk: githubHunk(th.Hunk), Note: clipNote(first.Body),
			By: first.Author, At: first.At, Source: proto.ReviewGitHub, URL: first.URL, Outdated: th.Outdated,
		}
		for _, r := range th.Comments[1:] {
			c.Replies = append(c.Replies, proto.ReviewReply{By: r.Author, Note: clipNote(r.Body), At: r.At})
		}
		out = append(out, c)
		last[c.ID] = strconv.FormatInt(th.Comments[len(th.Comments)-1].ID, 10)
	}
	for _, r := range rv.Reviews {
		c := proto.ReviewComment{
			ID: "ghr" + strconv.FormatInt(r.ID, 10), ProjectID: projectID, Branch: branch,
			Note: clipNote(r.Body), By: r.Author, At: r.At, Source: proto.ReviewGitHub, URL: r.URL,
		}
		out = append(out, c)
		last[c.ID] = strconv.FormatInt(r.ID, 10)
	}
	return out, last
}

// githubHunk keeps the header of GitHub's hunk and the lines just above
// the comment, which is where GitHub ends it.
func githubHunk(h string) string {
	lines := strings.Split(strings.TrimRight(h, "\n"), "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "@@") {
		return ""
	}
	if more := len(lines) - 1 - githubHunkLines; more > 0 {
		unit := "lines"
		if more == 1 {
			unit = "line"
		}
		return lines[0] + fmt.Sprintf("\n… %d earlier %s\n", more, unit) + strings.Join(lines[1+more:], "\n")
	}
	return strings.Join(lines, "\n")
}

// clipNote keeps a long GitHub comment to what one here may be.
func clipNote(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
	if len(s) <= reviewNoteMax {
		return s
	}
	cut := reviewNoteMax
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "\n… (the rest is on GitHub)"
}

// fetchPRReviews reads what the open pull requests of a project's branches
// say, each when it has moved since it was last read, and tells the
// clients of what changed. prs are gh's, by head branch.
func (s *Server) fetchPRReviews(p *project, branches []string, prs map[string]ghx.PR) {
	open := map[string]bool{}
	for _, br := range branches {
		if pr, ok := prs[br]; ok && pr.State == "OPEN" {
			open[br] = true
		}
	}
	for _, br := range s.reviews.forgetGitHub(p.id, open) {
		s.broadcast(proto.EventReviewChanged, s.reviews.list(proto.ReviewRef{ProjectID: p.id, Branch: br}))
	}
	for _, br := range branches {
		pr := prs[br]
		if !open[br] || !s.reviews.githubDue(p.id, br, pr.Updated, time.Now()) {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
		rv, err := ghx.PRReviews(ctx, p.root, pr.Number)
		cancel()
		got := &rv
		if err != nil {
			log.Printf("project %s: review comments on pull request #%d: %v", p.id, pr.Number, err)
			got = nil
		}
		if s.reviews.setGitHub(p.id, br, pr.Number, got, pr.Updated, time.Now()) {
			s.broadcast(proto.EventReviewChanged, s.reviews.list(proto.ReviewRef{ProjectID: p.id, Branch: br}))
		}
	}
}

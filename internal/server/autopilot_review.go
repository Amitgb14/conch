package server

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/Amitgb14/conch/internal/ghx"
	"github.com/Amitgb14/conch/internal/gitx"
	"github.com/Amitgb14/conch/internal/proto"
)

// A review that asks for changes, or a thread nobody has resolved, held a
// green pull request back (mergeHold) and nothing more: the agent never
// heard of it, and the branch waited for somebody to pass the comments on.
// So with auto_review the autopilot passes them on itself, as S does —
// what the pull request's threads and reviews say, already read for the
// changes view (github.go) — once each, to a free agent only. Once the
// agent has pushed, it answers each thread on GitHub with what changed,
// and with resolve_threads resolves the ones whose file the push touched.
// A thread stays GitHub's: answering is said in the thread, and nothing
// here is marked handed over, so the merge is still held until a person
// resolves it.

// reviewSent is one comment as it went to the agent.
type reviewSent struct {
	Sum    string `json:"sum"`            // what its thread said then (sumOf)
	Number int    `json:"number"`         // the pull request
	Head   string `json:"head,omitempty"` // the pull request's head commit then
	Agent  string `json:"agent,omitempty"`
	// A thread's, to answer it: its node id, its first comment, its file.
	// A review's summary has none and is not answered.
	Thread  string `json:"thread,omitempty"`
	Comment int64  `json:"comment,omitempty"`
	Path    string `json:"path,omitempty"`
	Replied bool   `json:"replied,omitempty"`
}

// prSaid is what a branch's pull request says, as the autopilot reads it.
// The maps are the book's, never written once made, so it is read without
// its lock.
type prSaid struct {
	number          int
	comments        []proto.ReviewComment
	threads, sums   map[string]string
	requested, ours map[string]bool
}

// githubFor is what branch's open pull request says, if it has been read.
func (b *reviewBook) githubFor(projectID, branch string) (prSaid, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	pr := b.github[branchKey(projectID, branch)]
	if pr == nil {
		return prSaid{}, false
	}
	return prSaid{number: pr.number, comments: slices.Clone(pr.comments), threads: pr.threads, sums: pr.sums,
		requested: pr.requested, ours: pr.ours}, true
}

// githubKeys is the branches whose pull requests have been read.
func (b *reviewBook) githubKeys() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	keys := make([]string, 0, len(b.github))
	for key := range b.github {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// reviewsOn says whether the policy sends review comments: auto_review,
// while auto_fix has the autopilot sending work back at all.
func reviewsOn(pol proto.AutopilotPolicy) bool { return pol.AutoReview && pol.AutoFix > 0 }

// autopilotReviews sends each branch's new review comments to its agent,
// and answers the threads of those whose agent has pushed since.
func (s *Server) autopilotReviews() {
	if s.autopilot == nil || s.reviews == nil || !reviewsOn(s.autopilot.policy()) {
		return
	}
	keys := s.reviews.githubKeys()
	// A branch the book has forgotten — its pull request merged or
	// closed — has nothing left to answer either.
	s.autopilot.mu.Lock()
	for id := range s.autopilot.saved.ReviewSent {
		if !slices.Contains(keys, sentBranch(id)) {
			delete(s.autopilot.saved.ReviewSent, id)
		}
	}
	s.autopilot.mu.Unlock()
	for _, key := range keys {
		projectID, branch, _ := strings.Cut(key, "\x00")
		s.autopilotReview(projectID, branch)
	}
	s.autopilotReplies()
}

// sentBranch is the branch key a ReviewSent key is under.
func sentBranch(id string) string {
	if i := strings.LastIndexByte(id, 0); i >= 0 {
		return id[:i]
	}
	return id
}

// autopilotReview sends one branch's new review comments to its agent.
func (s *Server) autopilotReview(projectID, branch string) {
	key := branchKey(projectID, branch)
	a := s.autopilot
	p, perr := s.projects.get(projectID)
	if perr != nil {
		return
	}
	p.mu.Lock()
	pr, ok := p.prs[branch]
	p.mu.Unlock()
	said, read := s.reviews.githubFor(projectID, branch)
	if !ok || pr.State != "OPEN" || !read || said.number != pr.Number {
		return
	}
	a.mu.Lock()
	// A thread resolved since, or a review gone, is done with.
	present := map[string]bool{}
	for _, c := range said.comments {
		present[c.ID] = true
	}
	for id := range a.saved.ReviewSent {
		if sentBranch(id) == key && !present[strings.TrimPrefix(id, key+"\x00")] {
			delete(a.saved.ReviewSent, id)
		}
	}
	var fresh []proto.ReviewComment
	news := false
	for _, c := range said.comments {
		// What conch's own reviewer posted went to the agent already,
		// from its report.
		if said.ours[c.ID] {
			continue
		}
		if sent, ok := a.saved.ReviewSent[key+"\x00"+c.ID]; ok && sent.Sum == said.sums[c.ID] {
			continue
		}
		fresh = append(fresh, c)
		// A thread is work; a review's summary is, when it asked for
		// changes — one that only commented goes along with work, and
		// is not worth a turn of its own.
		if _, thread := said.threads[c.ID]; thread || said.requested[c.ID] {
			news = true
		}
	}
	busy := a.reviewing[key]
	if news && !busy {
		a.reviewing[key] = true
	}
	a.mu.Unlock()
	if !news {
		s.unsay(key, "review")
		return
	}
	if busy {
		return
	}
	done := func() {
		a.mu.Lock()
		delete(a.reviewing, key)
		a.mu.Unlock()
	}
	what := fmt.Sprintf("%s on #%d", plural(len(fresh), "review comment"), pr.Number)
	agent := s.freeAgent(projectID, branch)
	if agent == nil {
		done()
		if waiting := s.busyAgent(projectID, branch); waiting != nil {
			s.noteOnce(key, "review", fmt.Sprintf("%s wait for %s to finish its turn", what, waiting.DisplayName()), false)
		} else {
			s.noteOnce(key, "review", fmt.Sprintf("%s, and no agent is working on %s to send them to", what, branch), false)
		}
		return
	}
	text := proto.ReviewPrompt(branch, fresh) + reviewFollowUp(pr.Number, a.policy().AutoPR)
	goSafe("autopilot review", func() {
		defer done()
		if _, perr := s.autopilotPrompt(proto.AgentPromptParams{ID: agent.ID, Text: text}); perr != nil {
			s.noteOnce(key, "review", fmt.Sprintf("%s could not go to %s: %s", what, agent.DisplayName(), perr.Message), true)
			return
		}
		a.mu.Lock()
		for _, c := range fresh {
			sent := reviewSent{Sum: said.sums[c.ID], Number: pr.Number, Head: pr.Head, Agent: agent.DisplayName()}
			if thread, ok := said.threads[c.ID]; ok {
				sent.Thread, sent.Path = thread, c.File
				sent.Comment, _ = strconv.ParseInt(strings.TrimPrefix(c.ID, "gh"), 10, 64)
			} else {
				sent.Replied = true // a review's summary has no thread to answer in
			}
			a.saved.ReviewSent[key+"\x00"+c.ID] = sent
		}
		a.saveLocked()
		a.mu.Unlock()
		s.unsay(key, "review")
		s.autopilotNote(proto.AutopilotNote{ProjectID: projectID, Branch: branch, Kind: proto.ActivityReview,
			Text: fmt.Sprintf("sent %s to %s", what, agent.DisplayName())})
	})
}

// reviewFollowUp is what the agent is told after the comments: they are
// GitHub's, and answered there from what it pushes.
func reviewFollowUp(number int, autoPR bool) string {
	push := "Commit what you change and push it"
	if autoPR {
		push = "Commit what you change; conch pushes it when you finish"
	}
	return fmt.Sprintf("\n\nThese are from pull request #%d on GitHub. %s, and conch answers each thread there with what changed.", number, push)
}

// autopilotReplies answers, on GitHub, the threads sent to an agent that
// has pushed since — once it has finished its turn, so the answer is
// about all it did — and resolves them where the policy says to.
func (s *Server) autopilotReplies() {
	a := s.autopilot
	a.mu.Lock()
	if a.replying {
		a.mu.Unlock()
		return
	}
	byBranch := map[string][]string{}
	for id, sent := range a.saved.ReviewSent {
		if !sent.Replied && sent.Comment > 0 {
			byBranch[sentBranch(id)] = append(byBranch[sentBranch(id)], id)
		}
	}
	if len(byBranch) == 0 {
		a.mu.Unlock()
		return
	}
	a.replying = true
	a.mu.Unlock()
	goSafe("autopilot replies", func() {
		defer func() {
			a.mu.Lock()
			a.replying = false
			a.mu.Unlock()
		}()
		for _, key := range slices.Sorted(maps.Keys(byBranch)) {
			s.autopilotReply(key, byBranch[key])
		}
	})
}

// autopilotReply answers one branch's threads, ids being their ReviewSent
// keys, if its pull request has moved on from where they were sent.
func (s *Server) autopilotReply(key string, ids []string) {
	a := s.autopilot
	projectID, branch, _ := strings.Cut(key, "\x00")
	p, perr := s.projects.get(projectID)
	if perr != nil {
		return
	}
	p.mu.Lock()
	pr, ok := p.prs[branch]
	p.mu.Unlock()
	if !ok || pr.State != "OPEN" || pr.Head == "" || s.busyAgent(projectID, branch) != nil {
		return
	}
	a.mu.Lock()
	tried := a.replyTried[key] == pr.Head
	var due []string
	sents := map[string]reviewSent{}
	for _, id := range ids {
		sent, ok := a.saved.ReviewSent[id]
		if ok && !sent.Replied && sent.Number == pr.Number && sent.Head != pr.Head {
			due = append(due, id)
			sents[id] = sent
		}
	}
	a.mu.Unlock()
	if len(due) == 0 || tried {
		return
	}
	sort.Strings(due)
	resolve := a.policy().ResolveThreads
	answered, resolved := 0, 0
	var failed error
	for _, id := range due {
		sent := sents[id]
		body, touched := s.replyText(p, sent, pr.Head)
		ctx, cancel := context.WithTimeout(context.Background(), networkTimeout)
		_, err := ghx.ReplyToComment(ctx, p.root, pr.Number, sent.Comment, ghx.Marked(ghx.AutopilotMark, body))
		cancel()
		if err != nil {
			failed = err
			break
		}
		answered++
		a.mu.Lock()
		if cur, ok := a.saved.ReviewSent[id]; ok && cur.Sum == sent.Sum {
			cur.Replied = true
			a.saved.ReviewSent[id] = cur
			a.saveLocked()
		}
		a.mu.Unlock()
		// Resolved only where the push changed the file it is about: a
		// thread the agent answered by leaving the code alone is the
		// reviewer's to close.
		if resolve && touched && sent.Thread != "" {
			ctx, cancel := context.WithTimeout(context.Background(), networkTimeout)
			err := ghx.ResolveThread(ctx, p.root, sent.Thread)
			cancel()
			if err != nil {
				failed = err
				break
			}
			resolved++
		}
	}
	text := ""
	if answered > 0 {
		text = fmt.Sprintf("answered %s on #%d after %s", plural(answered, "review thread"), pr.Number, pushedBy(sents[due[0]].Agent, pr.Head))
		if resolved > 0 {
			text += fmt.Sprintf("; resolved %d", resolved)
		}
		s.autopilotNote(proto.AutopilotNote{ProjectID: projectID, Branch: branch, Kind: proto.ActivityReview, Text: text})
	}
	if failed != nil {
		a.mu.Lock()
		a.replyTried[key] = pr.Head
		a.mu.Unlock()
		s.autopilotNote(proto.AutopilotNote{ProjectID: projectID, Branch: branch, Kind: proto.ActivityReview, Error: true,
			Text: fmt.Sprintf("could not answer the review threads on #%d: %v", pr.Number, failed)})
	}
}

// pushedBy says who pushed what: "AGENT pushed abc1234".
func pushedBy(agent, head string) string {
	if agent == "" {
		agent = "the agent"
	}
	return agent + " pushed " + shortCommit(head)
}

// replyText is a thread's answer: the commits pushed since it went to the
// agent, and whether they changed its file — which is also whether it may
// be resolved.
func (s *Server) replyText(p *project, sent reviewSent, head string) (string, bool) {
	var b strings.Builder
	b.WriteString(pushedBy(sent.Agent, head) + " after this comment was sent to it")
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	if sent.Head == "" {
		b.WriteString(".")
		return b.String(), false
	}
	subjects, err := gitx.Subjects(ctx, p.root, sent.Head, head, 10)
	if err != nil {
		// The commits are not here (pushed from elsewhere): say only what
		// GitHub says.
		b.WriteString(".")
		return b.String(), false
	}
	b.WriteString(":\n")
	for _, sub := range subjects {
		b.WriteString("\n- " + sub)
	}
	touched := false
	if cmp, err := gitx.CompareCommits(ctx, p.root, sent.Head, head); err == nil {
		for _, f := range cmp.Files {
			if f.Path == sent.Path || f.OrigPath == sent.Path {
				touched = true
			}
		}
	}
	if sent.Path != "" {
		if touched {
			fmt.Fprintf(&b, "\n\n`%s` changed in it.", sent.Path)
		} else {
			fmt.Fprintf(&b, "\n\n`%s` did not change in it.", sent.Path)
		}
	}
	return b.String(), touched
}

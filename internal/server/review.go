package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// Review comments are what somebody reading a branch's diff said about it,
// waiting to be handed to the agent working there. The server keeps them,
// not the TUI that wrote them, so another TUI, the command line, the phone
// and an agent reviewing the work all see the same ones, and they outlive
// the changes view, the TUI and a reload (review.json).

const (
	// reviewMax keeps a runaway script from filling the file.
	reviewMax = 1000
	// reviewNoteMax is a long comment, not a pasted file.
	reviewNoteMax = 8000
	// reviewSentKeep is how long a sent note stays, with its replies, for
	// the person to read: past it nobody is coming back to the thread.
	reviewSentKeep = 30 * 24 * time.Hour
)

type reviewBook struct {
	file string

	mu       sync.Mutex
	comments []proto.ReviewComment
	sending  map[string]bool // branches whose comments are on their way
	// github is what each branch's pull request says, read through gh
	// (github.go); handed is which of it went to the agent already, so it
	// is not sent again until somebody adds to the thread.
	github map[string]*prReviews
	handed map[string]handedTo
}

// reviewFile is review.json. A file from before handed loads as it is.
type reviewFile struct {
	Comments []proto.ReviewComment `json:"comments"`
	Handed   map[string]handedTo   `json:"handed,omitempty"`
}

func branchKey(projectID, branch string) string { return projectID + "\x00" + branch }

func loadReviews(configDir string) *reviewBook {
	b := &reviewBook{file: filepath.Join(configDir, "review.json"), sending: map[string]bool{},
		github: map[string]*prReviews{}, handed: map[string]handedTo{}}
	raw, err := os.ReadFile(b.file)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("review: %v", err)
		}
		return b
	}
	var saved reviewFile
	if err := json.Unmarshal(raw, &saved); err != nil {
		log.Printf("review: %s: %v", b.file, err)
		return b
	}
	for _, c := range saved.Comments {
		if checkComment(&c) == nil && c.ID != "" && c.Source == "" && !staleSent(c, time.Now()) {
			b.comments = append(b.comments, c)
		}
	}
	for id, h := range saved.Handed {
		if id != "" && h.ProjectID != "" && h.Branch != "" {
			b.handed[id] = h
		}
	}
	return b
}

// checkComment says what is wrong with a comment, tidying what it can.
// An empty note is allowed: it removes the comment.
func checkComment(c *proto.ReviewComment) *proto.Error {
	c.Note = strings.TrimSpace(c.Note)
	c.File = strings.TrimSpace(c.File)
	if c.Hunk != "" {
		c.Hunk = proto.QuoteHunk(c.Hunk)
	}
	switch {
	case c.ProjectID == "" || c.Branch == "":
		return proto.Errorf(proto.ErrBadRequest, "a review comment is on a branch of a project")
	case c.File == "" && c.ID == "":
		return proto.Errorf(proto.ErrBadRequest, "a review comment is about a file")
	case c.Line < 0:
		return proto.Errorf(proto.ErrBadRequest, "line %d is not a line", c.Line)
	case len(c.Note) > reviewNoteMax:
		return proto.Errorf(proto.ErrBadRequest, "a comment of %d bytes is too long; keep it under %d", len(c.Note), reviewNoteMax)
	}
	return nil
}

// samePlace says whether two comments are about the same place, so writing
// one again changes it rather than adding a second.
func samePlace(a, b proto.ReviewComment) bool {
	return a.ProjectID == b.ProjectID && a.Branch == b.Branch && a.File == b.File && a.Hunk == b.Hunk && a.Line == b.Line
}

// staleSent is a note sent longer ago than anyone reads a thread.
func staleSent(c proto.ReviewComment, now time.Time) bool {
	return c.SentAt != nil && now.Sub(*c.SentAt) > reviewSentKeep
}

// listLocked is a branch's notes: those waiting to be sent, and with
// ref.Sent those sent already too.
func (b *reviewBook) listLocked(ref proto.ReviewRef) proto.ReviewList {
	out := proto.ReviewList{ProjectID: ref.ProjectID, Branch: ref.Branch, Comments: []proto.ReviewComment{}}
	for _, c := range b.comments {
		if c.SentAt != nil && !ref.Sent {
			continue
		}
		if (ref.ProjectID == "" || c.ProjectID == ref.ProjectID) && (ref.Branch == "" || c.Branch == ref.Branch) {
			out.Comments = append(out.Comments, c)
		}
	}
	return b.githubLocked(ref, out)
}

func (b *reviewBook) list(ref proto.ReviewRef) proto.ReviewList {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.listLocked(ref)
}

func (b *reviewBook) saveLocked() {
	raw, _ := json.MarshalIndent(reviewFile{Comments: b.comments, Handed: b.handed}, "", "  ")
	tmp := b.file + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		log.Printf("review: save: %v", err)
		return
	}
	if err := os.Rename(tmp, b.file); err != nil {
		log.Printf("review: save: %v", err)
	}
}

// set adds, changes or (with an empty note) removes a comment, and says
// what the branch has now.
func (b *reviewBook) set(c proto.ReviewComment, now time.Time) (proto.ReviewList, *proto.Error) {
	// What GitHub says is read from it, never written here; whom a note
	// went to is the send's to say, and replies review.reply's.
	c.Source, c.URL, c.Outdated, c.Replies = "", "", false, nil
	c.SentTo, c.SentPane, c.SentAt = "", "", nil
	if perr := checkComment(&c); perr != nil {
		return proto.ReviewList{}, perr
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if pr := b.githubOwnerLocked(c.ID); pr != nil {
		return proto.ReviewList{}, proto.Errorf(proto.ErrBadRequest,
			"review comment %s is on GitHub (pull request #%d); answer or resolve it there", c.ID, pr.number)
	}
	at := -1
	for i, o := range b.comments {
		// A note on the same place as one already sent is a new one: the
		// sent note is a thread of its own.
		if c.ID != "" && o.ID == c.ID || c.ID == "" && o.SentAt == nil && samePlace(o, c) {
			at = i
			break
		}
	}
	switch {
	case at < 0 && c.ID != "":
		return proto.ReviewList{}, proto.Errorf(proto.ErrNotFound, "no review comment %s; it may have been sent already", c.ID)
	case at < 0 && c.Note == "":
		return b.listLocked(proto.ReviewRef{ProjectID: c.ProjectID, Branch: c.Branch}), nil
	case at < 0 && len(b.comments) >= reviewMax:
		return proto.ReviewList{}, proto.Errorf(proto.ErrBadRequest, "%d review comments are waiting already; send or remove some first", reviewMax)
	case at >= 0 && (b.comments[at].ProjectID != c.ProjectID || b.comments[at].Branch != c.Branch):
		return proto.ReviewList{}, proto.Errorf(proto.ErrBadRequest, "review comment %s is on another branch", c.ID)
	case at < 0:
		c.ID, c.At = newReviewID(), now
		b.comments = append(b.comments, c)
	case c.Note == "":
		b.comments = append(b.comments[:at], b.comments[at+1:]...)
	default:
		b.comments[at].Note, b.comments[at].At = c.Note, now
		if c.By != "" {
			b.comments[at].By = c.By
		}
		// A sent note changed is a new thing to say: it waits to be sent
		// again, its replies with it.
		b.comments[at].SentTo, b.comments[at].SentPane, b.comments[at].SentAt = "", "", nil
	}
	b.saveLocked()
	return b.listLocked(proto.ReviewRef{ProjectID: c.ProjectID, Branch: c.Branch}), nil
}

// drop removes the comments on a branch, or with no branch a project's,
// and says which branches had some.
func (b *reviewBook) drop(projectID, branch string) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var kept []proto.ReviewComment
	var had []string
	seen := map[string]bool{}
	for _, c := range b.comments {
		if c.ProjectID == projectID && (branch == "" || c.Branch == branch) {
			if !seen[c.Branch] {
				seen[c.Branch] = true
				had = append(had, c.Branch)
			}
			continue
		}
		kept = append(kept, c)
	}
	for key, pr := range b.github {
		if pr.projectID == projectID && (branch == "" || pr.branch == branch) {
			delete(b.github, key)
			if len(pr.comments) > 0 && !seen[pr.branch] {
				seen[pr.branch] = true
				had = append(had, pr.branch)
			}
		}
	}
	dropped := false
	for id, h := range b.handed {
		if h.ProjectID == projectID && (branch == "" || h.Branch == branch) {
			delete(b.handed, id)
			dropped = true
		}
	}
	b.comments = kept
	if len(had) > 0 || dropped {
		b.saveLocked()
	}
	return had
}

// reply answers note id: by an agent (by its pane's label) or by the
// person (by ""), whose answer puts the note back among those waiting to
// be sent, the thread with it.
func (b *reviewBook) reply(rp proto.ReviewReplyParams, by string, now time.Time) (proto.ReviewComment, *proto.Error) {
	rp.Note = strings.TrimSpace(rp.Note)
	switch {
	case rp.ID == "":
		return proto.ReviewComment{}, proto.Errorf(proto.ErrBadRequest, "which review comment? give its id")
	case rp.Note == "":
		return proto.ReviewComment{}, proto.Errorf(proto.ErrBadRequest, "a reply says something")
	case len(rp.Note) > reviewNoteMax:
		return proto.ReviewComment{}, proto.Errorf(proto.ErrBadRequest, "a reply of %d bytes is too long; keep it under %d", len(rp.Note), reviewNoteMax)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if pr := b.githubOwnerLocked(rp.ID); pr != nil {
		return proto.ReviewComment{}, proto.Errorf(proto.ErrBadRequest,
			"review comment %s is on GitHub (pull request #%d); answer it there", rp.ID, pr.number)
	}
	for i := range b.comments {
		c := &b.comments[i]
		if c.ID != rp.ID {
			continue
		}
		if c.ProjectID != rp.ProjectID {
			return proto.ReviewComment{}, proto.Errorf(proto.ErrBadRequest, "review comment %s is in another project", rp.ID)
		}
		if len(c.Replies) >= reviewMax {
			return proto.ReviewComment{}, proto.Errorf(proto.ErrBadRequest, "review comment %s has %d replies already", rp.ID, reviewMax)
		}
		c.Replies = append(c.Replies, proto.ReviewReply{By: by, Note: rp.Note, At: now})
		if by == "" {
			c.SentTo, c.SentPane, c.SentAt = "", "", nil
		}
		b.saveLocked()
		return *c, nil
	}
	return proto.ReviewComment{}, proto.Errorf(proto.ErrNotFound, "no review comment %s; it may have been resolved", rp.ID)
}

func newReviewID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return "c" + hex.EncodeToString(b[:])
}

// reviewSet is review.set: the comment as the connection's agent, if any,
// wrote it.
func (s *Server) reviewSet(c *client, rc proto.ReviewComment) (proto.ReviewList, *proto.Error) {
	if _, perr := s.projects.get(rc.ProjectID); perr != nil {
		return proto.ReviewList{}, perr
	}
	rc.By = ""
	if caller, ok := s.scopedCaller(c); ok {
		rc.By = caller.label
	}
	list, perr := s.reviews.set(rc, time.Now())
	if perr == nil {
		s.broadcast(proto.EventReviewChanged, list)
	}
	return list, perr
}

// reviewReply is review.reply: the answer signed by the connection's
// agent, if it is one — an agent cannot answer as the person.
func (s *Server) reviewReply(c *client, rp proto.ReviewReplyParams) (proto.ReviewComment, *proto.Error) {
	if _, perr := s.projects.get(rp.ProjectID); perr != nil {
		return proto.ReviewComment{}, perr
	}
	by := ""
	if caller, ok := s.scopedCaller(c); ok {
		by = s.replyAuthor(caller)
	}
	got, perr := s.reviews.reply(rp, by, time.Now())
	if perr == nil {
		s.broadcast(proto.EventReviewChanged, s.reviews.list(proto.ReviewRef{ProjectID: got.ProjectID, Branch: got.Branch}))
	}
	return got, perr
}

// replyAuthor is how an agent's answer is signed: its pane's name, which
// the person knows it by, then its agent, then the pane's id — a bare
// "p12" under a line says nothing.
func (s *Server) replyAuthor(caller scopeCaller) string {
	if e, perr := s.get(caller.id); perr == nil {
		info := e.info()
		switch {
		case strings.TrimSpace(info.Name) != "":
			return strings.TrimSpace(info.Name)
		case info.Agent != nil && info.Agent.Name != "":
			return info.Agent.Name
		}
	}
	if caller.agent != "" {
		return caller.agent
	}
	return caller.label
}

// reviewDropped takes away the comments of a branch that is gone — merged
// or discarded — or of a project no longer here, and tells the clients.
func (s *Server) reviewDropped(projectID, branch string) {
	for _, br := range s.reviews.drop(projectID, branch) {
		s.broadcast(proto.EventReviewChanged, proto.ReviewList{ProjectID: projectID, Branch: br, Comments: []proto.ReviewComment{}})
	}
}

// reviewSend hands a branch's comments to the agent working on it — the
// one not waiting on you, if there are several — or starts sp.Agent in
// its worktree with them. Once they are delivered they are taken away;
// comments written meanwhile stay for the next send.
func (s *Server) reviewSend(c *client, sp proto.ReviewSendParams) (proto.ReviewSendResult, *proto.Error) {
	p, perr := s.projects.get(sp.ProjectID)
	if perr != nil {
		return proto.ReviewSendResult{}, perr
	}
	key := branchKey(sp.ProjectID, sp.Branch)
	b := s.reviews
	b.mu.Lock()
	comments := b.listLocked(proto.ReviewRef{ProjectID: sp.ProjectID, Branch: sp.Branch}).Comments
	busy := b.sending[key]
	if len(comments) > 0 && !busy {
		b.sending[key] = true
	}
	b.mu.Unlock()
	switch {
	case len(comments) == 0:
		return proto.ReviewSendResult{}, proto.Errorf(proto.ErrBadRequest, "no review comments on %s to send", sp.Branch)
	case busy:
		return proto.ReviewSendResult{}, proto.Errorf(proto.ErrBadRequest, "the comments on %s are being sent already", sp.Branch)
	}
	defer func() {
		b.mu.Lock()
		delete(b.sending, key)
		b.mu.Unlock()
	}()

	text := proto.ReviewPrompt(sp.Branch, comments)
	res := proto.ReviewSendResult{Sent: len(comments)}
	var agent *proto.PaneInfo
	switch {
	case sp.Pane != "":
		// The agent picked: running, an agent, and in this project — a
		// pane id is not a way past the project the call is scoped to.
		e, perr := s.get(sp.Pane)
		var info proto.PaneInfo
		if perr == nil {
			info = e.info()
		}
		if perr != nil || info.State != proto.PaneRunning || info.Agent == nil {
			return proto.ReviewSendResult{}, proto.Errorf(proto.ErrNotFound, "no agent is running in pane %s", sp.Pane)
		}
		if info.ProjectID != sp.ProjectID {
			return proto.ReviewSendResult{}, proto.Errorf(proto.ErrBadRequest, "pane %s is not an agent of this project", sp.Pane)
		}
		agent = &info
	case sp.New:
		if sp.Agent == "" {
			return proto.ReviewSendResult{}, proto.Errorf(proto.ErrBadRequest, "name the agent to start with the comments")
		}
	default:
		agent = s.branchAgent(sp.ProjectID, sp.Branch)
	}
	if agent != nil {
		pr, perr := s.promptAgent(proto.AgentPromptParams{ID: agent.ID, Text: text})
		if perr != nil {
			return proto.ReviewSendResult{}, perr
		}
		res.Pane, res.Turn = s.infoOf(*agent), pr.Turn
	} else {
		wt, ok := p.branchWorktree(sp.Branch)
		switch {
		case sp.Agent == "":
			return proto.ReviewSendResult{}, proto.Errorf(proto.ErrNotFound, "no agent is working on %s; name one to start with the comments", sp.Branch)
		case !ok || wt.Path == "":
			return proto.ReviewSendResult{}, proto.Errorf(proto.ErrNotFound, "no agent is working on %s, and it has no worktree to start one in", sp.Branch)
		}
		info, perr := s.made(c)(s.create(proto.PaneCreateParams{Agent: sp.Agent, Prompt: text, Cwd: wt.Path, Cols: sp.Cols, Rows: sp.Rows}))
		if perr != nil {
			return proto.ReviewSendResult{}, perr
		}
		res.Pane, res.Started = info, true
	}

	sent := map[string]bool{}
	for _, c := range comments {
		sent[c.ID] = true
	}
	b.mu.Lock()
	// A pull request's comments stay on GitHub; they are remembered as
	// handed over, and come back only when somebody adds to the thread.
	for _, c := range comments {
		if c.Source != proto.ReviewGitHub {
			continue
		}
		if pr := b.github[key]; pr != nil {
			b.handed[c.ID] = handedTo{ProjectID: sp.ProjectID, Branch: sp.Branch, Last: pr.last[c.ID]}
		}
	}
	// conch's own notes stay, said as sent and to whom, for the agent's
	// replies to land on (review.thread.v1); sent long ago, they go.
	to, at := res.Pane.Name, time.Now()
	if to == "" && res.Pane.Agent != nil {
		to = res.Pane.Agent.Name
	}
	kept := b.comments[:0]
	for _, c := range b.comments {
		if sent[c.ID] {
			c.SentTo, c.SentPane, c.SentAt = to, res.Pane.ID, &at
		}
		if !staleSent(c, at) {
			kept = append(kept, c)
		}
	}
	b.comments = kept
	b.saveLocked()
	list := b.listLocked(proto.ReviewRef{ProjectID: sp.ProjectID, Branch: sp.Branch})
	b.mu.Unlock()
	s.broadcast(proto.EventReviewChanged, list)
	return res, nil
}

// branchAgent is the agent working on a branch: the one not waiting on
// you if there are several, the first otherwise. Reviewers are left out:
// comments are for the agent that wrote the work.
func (s *Server) branchAgent(projectID, branch string) *proto.PaneInfo {
	s.mu.Lock()
	entries := make([]*entry, 0, len(s.panes))
	for _, e := range s.panes {
		entries = append(entries, e)
	}
	s.mu.Unlock()
	var found *proto.PaneInfo
	for _, e := range entries {
		info := e.info()
		if info.Agent == nil || info.State != proto.PaneRunning || info.ProjectID != projectID || info.Branch != branch || strings.HasPrefix(info.Name, "review · ") {
			continue
		}
		if found == nil || found.Agent.State == proto.AgentBlocked && info.Agent.State != proto.AgentBlocked ||
			(found.Agent.State == proto.AgentBlocked) == (info.Agent.State == proto.AgentBlocked) && info.Created.Before(found.Created) {
			i := info
			found = &i
		}
	}
	return found
}

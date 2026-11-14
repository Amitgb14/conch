package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Amitgb14/conch/internal/ghx"
	"github.com/Amitgb14/conch/internal/gitx"
	"github.com/Amitgb14/conch/internal/proto"
)

// Intake takes an issue once somebody has labelled it ready, so a person
// still read every issue first, to decide whether it was clear enough to
// build. Triage does that reading: an issue with a project's triage label
// is given to an agent, in a checkout of the base branch kept for it,
// which reads the issue and the code and writes a verdict — ready,
// needs-info or too-big — with a comment saying what done means, how big
// it is and which files it touches, or what has to be answered first. The
// server posts the comment, labels the issue (ready is intake's label, so
// intake takes it next), and closes the agent.
//
// One triage at a time per project: each is an agent run, and the person
// pays for it. What was triaged is kept in triage.json, so nothing is read
// twice; relabelling an issue (the needs-info comment says to) asks again.
// The agent is asked to change nothing, and its checkout is reset before
// every triage, so nothing it might have written outlives it.

// triageTimeout is how long an agent has to write its verdict.
var triageTimeout = 20 * time.Minute

// The labels a verdict gives, besides intake's ready.
const (
	labelNeedsInfo = "needs-info"
	labelTooBig    = "too-big"
	labelReady     = "ready" // when no intake label is named
)

type triageRecord struct {
	Project string    `json:"project"`
	Issue   int       `json:"issue"`
	Title   string    `json:"title,omitempty"`
	Started time.Time `json:"started"`
	Ended   time.Time `json:"ended,omitempty"`
	// Pane and Name are the agent reading it, while it does.
	Pane string `json:"pane,omitempty"`
	Name string `json:"name,omitempty"`
	// Verdict is what it said; Failed why there is none.
	Verdict string `json:"verdict,omitempty"`
	Failed  string `json:"failed,omitempty"`
	// Gone is set once the issue was seen without the triage label, so
	// labelling it again asks again.
	Gone bool `json:"gone,omitempty"`
}

func (r triageRecord) running() bool { return r.Pane != "" && r.Ended.IsZero() }

type triageFile struct {
	Issues map[string]triageRecord `json:"issues,omitempty"`
}

type triageBook struct {
	file  string
	mu    sync.Mutex
	saved triageFile
	busy  bool
	said  map[string]string
}

func loadTriage(configDir string) *triageBook {
	b := &triageBook{file: filepath.Join(configDir, "triage.json"), said: map[string]string{}}
	raw, err := os.ReadFile(b.file)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("triage: %v", err)
		}
	} else if err := json.Unmarshal(raw, &b.saved); err != nil {
		log.Printf("triage: %s: %v", b.file, err)
	}
	if b.saved.Issues == nil {
		b.saved.Issues = map[string]triageRecord{}
	}
	return b
}

func (b *triageBook) put(r triageRecord) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.saved.Issues[intakeKey(r.Project, r.Issue)] = r
	raw, _ := json.MarshalIndent(b.saved, "", "  ")
	tmp := b.file + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		log.Printf("triage: save: %v", err)
		return
	}
	if err := os.Rename(tmp, b.file); err != nil {
		log.Printf("triage: save: %v", err)
	}
}

func (b *triageBook) get(project string, issue int) (triageRecord, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	r, ok := b.saved.Issues[intakeKey(project, issue)]
	return r, ok
}

// all is the project's records.
func (b *triageBook) all(project string) []triageRecord {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []triageRecord
	for _, r := range b.saved.Issues {
		if r.Project == project {
			out = append(out, r)
		}
	}
	return out
}

// note logs a project's problem once.
func (b *triageBook) note(project, what string) {
	b.mu.Lock()
	last := b.said[project]
	b.said[project] = what
	b.mu.Unlock()
	if what != "" && what != last {
		log.Printf("triage: %s", what)
	}
}

// triageOnce looks at every project with a triage label: finishes the
// triage under way, or starts the next one.
func (s *Server) triageOnce() {
	if s.autopilot == nil || s.triage == nil {
		return
	}
	pol := s.autopilot.policy()
	if len(pol.Triage) == 0 {
		return
	}
	b := s.triage
	b.mu.Lock()
	if b.busy {
		b.mu.Unlock()
		return
	}
	b.busy = true
	b.mu.Unlock()
	defer func() { b.mu.Lock(); b.busy = false; b.mu.Unlock() }()
	ids := make([]string, 0, len(pol.Triage))
	for id := range pol.Triage {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		s.triageProject(id, pol.Triage[id], pol)
	}
}

// readyLabel is the label a ready verdict gives: intake's for the project,
// so intake takes it next.
func readyLabel(pol proto.AutopilotPolicy, id string) string {
	if l := strings.TrimSpace(pol.Intake[id]); l != "" {
		return l
	}
	return labelReady
}

func (s *Server) triageProject(id, label string, pol proto.AutopilotPolicy) {
	b := s.triage
	p, perr := s.projects.gitProject(id)
	if perr != nil {
		b.note(id, fmt.Sprintf("project %s: %s", id, perr.Message))
		return
	}
	for _, r := range b.all(id) {
		if r.running() {
			s.triageFinish(p, r, label, pol)
			return // one at a time
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	issues, err := ghx.IssuesLabelled(ctx, p.root, label, intakeLimit)
	if err != nil {
		b.note(id, fmt.Sprintf("project %s, label %q: %v", p.snapshot().Name, label, err))
		return
	}
	b.note(id, "")
	labelled := map[int]bool{}
	for _, is := range issues {
		labelled[is.Number] = true
	}
	if len(issues) < intakeLimit {
		for _, r := range b.all(id) {
			if !r.Gone && !r.running() && !labelled[r.Issue] {
				r.Gone = true
				b.put(r)
			}
		}
	}
	done := map[string]bool{readyLabel(pol, id): true, labelNeedsInfo: true, labelTooBig: true}
	for _, is := range issues {
		if r, ok := b.get(id, is.Number); ok && !r.Gone {
			continue
		}
		rec := triageRecord{Project: id, Issue: is.Number, Title: is.Title, Started: time.Now()}
		if had := firstLabel(is.Labels, done); had != "" {
			// Somebody has already said: not read again.
			rec.Ended, rec.Verdict = time.Now(), "already labelled "+had
			b.put(rec)
			continue
		}
		s.triageStart(ctx, p, rec, pol)
		return
	}
}

func firstLabel(have []string, want map[string]bool) string {
	for _, l := range have {
		if want[l] {
			return l
		}
	}
	return ""
}

// triageDir is the checkout of the base branch kept for triage, and the
// verdict file in it.
func (s *Server) triageDir(id string) string { return filepath.Join(s.configDir, "triage", id) }

const verdictFile = ".conch-triage.json"

func (s *Server) triageStart(ctx context.Context, p *project, rec triageRecord, pol proto.AutopilotPolicy) {
	b := s.triage
	info := p.snapshot()
	is, err := ghx.IssueByNumber(ctx, p.root, rec.Issue)
	if err != nil {
		b.note(p.id, fmt.Sprintf("#%d: %v", rec.Issue, err))
		return
	}
	dir := s.triageDir(p.id)
	base := info.Base
	if base == "" {
		base = "HEAD"
	}
	if err := gitx.CheckoutDetached(ctx, p.root, dir, base); err != nil {
		b.note(p.id, fmt.Sprintf("#%d: a checkout to read in: %v", rec.Issue, err))
		return
	}
	os.Remove(filepath.Join(dir, verdictFile))
	agent := pol.TriageAgent
	if agent == "" {
		agent = "claude"
	}
	rec.Name = fmt.Sprintf("%s#%d", proto.TriagePrefix, rec.Issue)
	pi, perr := s.create(proto.PaneCreateParams{Name: rec.Name, Agent: agent, Cwd: dir,
		Prompt: triagePrompt(is, filepath.Join(dir, verdictFile))})
	if perr != nil {
		rec.Ended, rec.Failed = time.Now(), "the agent did not start: "+perr.Message
		b.put(rec)
		log.Printf("triage: #%d: %s", rec.Issue, rec.Failed)
		return
	}
	rec.Pane = pi.ID
	b.put(rec)
	log.Printf("triage: %s reads #%d %q of %s", agent, rec.Issue, rec.Title, info.Name)
}

// triageVerdict is what the agent writes.
type triageVerdict struct {
	Verdict string `json:"verdict"`
	Size    string `json:"size,omitempty"`
	Comment string `json:"comment"`
}

// triageFinish posts a verdict that is in, or gives up on an agent that
// has gone or taken too long. Either way the agent is closed.
func (s *Server) triageFinish(p *project, rec triageRecord, label string, pol proto.AutopilotPolicy) {
	b := s.triage
	path := filepath.Join(s.triageDir(p.id), verdictFile)
	raw, rerr := os.ReadFile(path)
	alive := false
	if e, perr := s.get(rec.Pane); perr == nil {
		// A fresh server numbers its panes from p1 again: only the pane
		// still bearing its name is it.
		info := e.info()
		alive = info.State == proto.PaneRunning && info.Name == rec.Name
	}
	switch {
	case rerr == nil:
		var v triageVerdict
		if err := json.Unmarshal(raw, &v); err != nil || strings.TrimSpace(v.Comment) == "" || !validVerdict(v.Verdict) {
			rec.Failed = "its verdict did not read: want {verdict, size, comment}"
			break
		}
		if err := s.triagePost(p, rec, v, label, pol); err != nil {
			rec.Failed = err.Error()
			break
		}
		rec.Verdict = v.Verdict
	case !alive:
		rec.Failed = "the agent ended without a verdict"
	case time.Since(rec.Started) > triageTimeout:
		rec.Failed = fmt.Sprintf("no verdict after %v", triageTimeout)
	default:
		return // still reading
	}
	rec.Ended = time.Now()
	if alive {
		s.close(rec.Pane)
	}
	rec.Pane = ""
	os.Remove(path)
	b.put(rec)
	if rec.Failed != "" {
		log.Printf("triage: #%d: %s; the issue is left as it was", rec.Issue, rec.Failed)
	} else {
		log.Printf("triage: #%d is %s", rec.Issue, rec.Verdict)
	}
}

func validVerdict(v string) bool {
	return v == "ready" || v == labelNeedsInfo || v == labelTooBig
}

// triagePost comments on the issue and labels it.
func (s *Server) triagePost(p *project, rec triageRecord, v triageVerdict, label string, pol proto.AutopilotPolicy) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	agent := pol.TriageAgent
	if agent == "" {
		agent = "claude"
	}
	if err := ghx.CommentIssue(ctx, p.root, rec.Issue, triageComment(v, agent)); err != nil {
		return fmt.Errorf("the comment: %w", err)
	}
	if pol.TriageCommentOnly {
		return nil
	}
	add := readyLabel(pol, p.id)
	switch v.Verdict {
	case labelNeedsInfo:
		add = labelNeedsInfo
	case labelTooBig:
		add = labelTooBig
	}
	colors := map[string]string{labelNeedsInfo: "fbca04", labelTooBig: "d93f0b"}
	color := colors[add]
	if color == "" {
		color = "0e8a16"
	}
	if err := ghx.EnsureLabel(ctx, p.root, add, color, "set by conch triage"); err != nil {
		return fmt.Errorf("the label %s: %w", add, err)
	}
	if err := ghx.EditLabels(ctx, p.root, rec.Issue, []string{add}, []string{label}); err != nil {
		return fmt.Errorf("the labels: %w", err)
	}
	return nil
}

// triageComment is what is posted: the agent's comment, said to be an
// agent's, and the verdict.
func triageComment(v triageVerdict, agent string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "🤖 **conch triage** · read by %s, which changed nothing\n\n", agent)
	b.WriteString(strings.TrimSpace(v.Comment))
	fmt.Fprintf(&b, "\n\n---\nVerdict: **%s**", v.Verdict)
	if sz := strings.TrimSpace(v.Size); sz != "" {
		fmt.Fprintf(&b, " · size %s", sz)
	}
	if v.Verdict != "ready" {
		b.WriteString(" · answer here and label it again to have it read again")
	}
	return b.String()
}

// triagePrompt is what the agent is told.
func triagePrompt(is ghx.Issue, verdict string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Triage GitHub issue #%d of this repository. You only read: do not change, create or delete any file except the one below, and run nothing that changes the repository, the network or this machine.\n\n", is.Number)
	fmt.Fprintf(&b, "Issue #%d: %s\n", is.Number, strings.TrimSpace(is.Title))
	if is.URL != "" {
		fmt.Fprintf(&b, "%s\n", is.URL)
	}
	body := strings.TrimSpace(is.Body)
	if len(body) > maxIssueBody {
		body = strings.ToValidUTF8(body[:maxIssueBody], "") + "\n\n… (the rest of the issue is on GitHub)"
	}
	if body != "" {
		b.WriteString("\n" + body + "\n")
	}
	b.WriteString(`
Read the issue and the code it is about, then decide:
- ready: another agent could build it from this issue without asking anything;
- needs-info: something has to be answered first;
- too-big: it is more than one pull request's work.

Write your answer as one JSON object to ` + verdict + `, and nothing else there:
{"verdict": "ready" | "needs-info" | "too-big", "size": "S" | "M" | "L", "comment": "markdown for the issue"}

The comment, for ready: "Done means" (each point something a test can check), "Size" with the files it will touch, and "Out of scope". For needs-info: the questions, each one answerable, and what you found that raised them. For too-big: the parts it splits into, each one a ticket of its own. Keep it short. When the file is written, say "triage written" and stop.
`)
	return b.String()
}

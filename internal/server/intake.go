package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/Amitgb14/conch/internal/ghx"
	"github.com/Amitgb14/conch/internal/proto"
)

// The autopilot carries a finished branch to a merged pull request; intake
// is the other end, the work nobody handed in. A project with a label in
// [verify.intake] has its open issues with that label read every
// intakeEvery, and each one not yet taken becomes a queued task — conch
// task -queue -issue N — which the schedule starts once fewer than
// intake_wip agents are busy in the project, and the autopilot carries on
// from there. The server does it, not the TUI, for the same reason the
// autopilot is here: the TUI is often closed, and that is the point.
//
// What was taken is kept in intake.json, so a reload or a restart takes
// nothing twice. Seeing an issue still labelled is the same request
// again, not a new one; an issue seen without the label, or closed, and
// labelled again later is the person asking again, and is taken again.
// An issue somebody is already on — a branch named for it, a task for it
// on the schedule — is recorded as taken and left alone.

// intakeEvery is how often the labelled issues are read: a minute would
// spend the person's GitHub rate limit on a backlog that moves daily.
var intakeEvery = 5 * time.Minute

// intakeLimit is the issues read per project per look. Only a list shorter
// than this is known to be all of them, which is what marking an issue
// gone needs.
const intakeLimit = 100

// intakeRecord is one issue taken.
type intakeRecord struct {
	Project string    `json:"project"`
	Issue   int       `json:"issue"`
	Title   string    `json:"title,omitempty"`
	Taken   time.Time `json:"taken"`
	// Item is the schedule item it became; empty when somebody was
	// already working on it.
	Item string `json:"item,omitempty"`
	// Gone is set once it was seen without the label, or closed, so that
	// labelling it again takes it again.
	Gone bool `json:"gone,omitempty"`
}

type intakeFile struct {
	Taken map[string]intakeRecord `json:"taken,omitempty"`
}

type intakeBook struct {
	file string

	mu    sync.Mutex
	saved intakeFile
	busy  bool
	// said is the last problem logged for each project, so a missing gh
	// is said once rather than every five minutes.
	said map[string]string
}

func loadIntake(configDir string) *intakeBook {
	b := &intakeBook{file: filepath.Join(configDir, "intake.json"), said: map[string]string{}}
	raw, err := os.ReadFile(b.file)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("intake: %v", err)
		}
	} else if err := json.Unmarshal(raw, &b.saved); err != nil {
		log.Printf("intake: %s: %v", b.file, err)
	}
	if b.saved.Taken == nil {
		b.saved.Taken = map[string]intakeRecord{}
	}
	return b
}

func intakeKey(project string, issue int) string { return fmt.Sprintf("%s#%d", project, issue) }

func (b *intakeBook) saveLocked() {
	raw, _ := json.MarshalIndent(b.saved, "", "  ")
	tmp := b.file + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		log.Printf("intake: save: %v", err)
		return
	}
	if err := os.Rename(tmp, b.file); err != nil {
		log.Printf("intake: save: %v", err)
	}
}

// note logs a project's problem once, and its clearing.
func (b *intakeBook) note(project, what string) {
	b.mu.Lock()
	last := b.said[project]
	b.said[project] = what
	b.mu.Unlock()
	switch {
	case what != "" && what != last:
		log.Printf("intake: %s", what)
	case what == "" && last != "":
		log.Printf("intake: project %s reads its issues again", project)
	}
}

func (s *Server) runIntake(quit <-chan struct{}) {
	t := time.NewTicker(intakeEvery)
	defer t.Stop()
	for {
		select {
		case <-quit:
			return
		case <-t.C:
			s.backlogOnce()
		}
	}
}

// intakeOnce reads every project's labelled issues and queues the new
// ones. One look at a time: a slow gh must not have the next tick start
// a second look that takes the same issues.
func (s *Server) intakeOnce() {
	if s.autopilot == nil || s.intake == nil {
		return
	}
	pol := s.autopilot.policy()
	if len(pol.Intake) == 0 {
		return
	}
	b := s.intake
	b.mu.Lock()
	if b.busy {
		b.mu.Unlock()
		return
	}
	b.busy = true
	b.mu.Unlock()
	defer func() { b.mu.Lock(); b.busy = false; b.mu.Unlock() }()

	wip := pol.IntakeWIP
	if wip <= 0 {
		wip = proto.DefaultWIP
	}
	ids := make([]string, 0, len(pol.Intake))
	for id := range pol.Intake {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		s.intakeProject(id, pol.Intake[id], wip)
	}
}

func (s *Server) intakeProject(id, label string, wip int) {
	b := s.intake
	p, perr := s.projects.gitProject(id)
	if perr != nil {
		b.note(id, fmt.Sprintf("project %s: %s", id, perr.Message))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	issues, err := ghx.IssuesLabelled(ctx, p.root, label, intakeLimit)
	if err != nil {
		b.note(id, fmt.Sprintf("project %s, label %q: %v", p.snapshot().Name, label, err))
		return
	}
	b.note(id, "")
	info := p.snapshot()

	labelled := map[int]bool{}
	for _, is := range issues {
		labelled[is.Number] = true
	}
	b.mu.Lock()
	changed := false
	if len(issues) < intakeLimit {
		for key, r := range b.saved.Taken {
			if r.Project == id && !r.Gone && !labelled[r.Issue] {
				r.Gone = true
				b.saved.Taken[key] = r
				changed = true
			}
		}
	}
	b.mu.Unlock()

	// Only as many waiting as could start: a backlog of fifty is fifty
	// issues on GitHub, not fifty tasks on the schedule.
	room := wip - s.intakeWaiting(id)
	for _, is := range issues {
		key := intakeKey(id, is.Number)
		b.mu.Lock()
		r, ok := b.saved.Taken[key]
		b.mu.Unlock()
		if ok && !r.Gone {
			continue
		}
		rec := intakeRecord{Project: id, Issue: is.Number, Title: is.Title, Taken: time.Now()}
		if !s.issueInHand(id, info, is.Number) {
			if room <= 0 {
				break
			}
			it, perr := s.scheduleAdd(proto.ScheduledItem{At: time.Now(), Queue: true, WIP: wip,
				Task: &proto.TaskCreateParams{ProjectID: id, Issue: is.Number},
				Note: fmt.Sprintf("intake: #%d, labelled %s", is.Number, label)})
			if perr != nil {
				b.note(id, fmt.Sprintf("project %s: #%d not queued: %s", info.Name, is.Number, perr.Message))
				break
			}
			rec.Item = it.ID
			room--
			log.Printf("intake: took #%d %q of %s (label %s)", is.Number, is.Title, info.Name, label)
			msg := fmt.Sprintf("conch took this from the `%s` label: it is queued as a task, and starts once fewer than %d agents are busy in the project. Its pull request will say Fixes #%d.", label, wip, is.Number)
			if err := ghx.CommentIssue(ctx, p.root, is.Number, msg); err != nil {
				log.Printf("intake: comment on #%d: %v", is.Number, err)
			}
		}
		b.mu.Lock()
		b.saved.Taken[key] = rec
		b.mu.Unlock()
		changed = true
	}
	if changed {
		b.mu.Lock()
		b.saveLocked()
		b.mu.Unlock()
	}
}

// intakeWaiting counts the project's tasks intake queued that have not
// started yet.
func (s *Server) intakeWaiting(id string) int {
	n := 0
	for _, it := range s.schedules.list().Items {
		if it.Task != nil && it.Task.ProjectID == id && it.Queue && it.Task.Issue > 0 {
			n++
		}
	}
	return n
}

// issueInHand says whether somebody is already on the issue: a task for it
// on the schedule, or a branch named for it, as an issue task names one
// (conch/issue-12-…, and its attempts beneath it).
func (s *Server) issueInHand(id string, info proto.ProjectInfo, issue int) bool {
	for _, it := range s.schedules.list().Items {
		if it.Task != nil && it.Task.ProjectID == id && it.Task.Issue == issue {
			return true
		}
	}
	named := regexp.MustCompile(fmt.Sprintf(`(^|/)issue-%d(-|/|$)`, issue))
	for _, br := range info.Branches {
		if named.MatchString(br.Name) {
			return true
		}
	}
	return false
}

// backlogOnce is one look at the backlog: triage first, so an issue it
// labels ready is taken by intake in the same look.
func (s *Server) backlogOnce() {
	s.triageOnce()
	s.intakeOnce()
}

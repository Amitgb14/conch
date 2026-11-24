package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Amitgb14/conch/internal/gitx"
	"github.com/Amitgb14/conch/internal/proto"
)

// The task graph (run.v1): a job split into tasks that depend on one
// another, kept by the server so a coordinator agent that is restarted —
// or the person, from the TUI — finds it where it was. A task becomes
// ready when what it depends on is done, and the server starts it then,
// on a branch made from the first of those tasks' branches. Its worker
// says once that it is done or failed, with a line of result, which is
// more to go on than reading screens; it may ask its coordinator a
// question meanwhile. A task with a gate waits for a person's approval
// before it starts, whatever its dependencies say.
//
// Scoping follows the rest of the server (scope.go): an agent may change a
// run it coordinates — made, or started the agent that made it — and the
// workers the server starts for it count as started by that coordinator,
// so it reaches them and nothing else. A worker reports and asks for its
// own task only. Approving a gate is the person's alone.
//
// Kept in graph.json, read at start and so carried through a reload.

// graphEvery is how often runs are looked at without being kicked: a
// worker's pane ending sends nothing here, nor does a question's deadline.
var graphEvery = 3 * time.Second

type graphFile struct {
	Runs []proto.Run `json:"runs"`
}

type graphBook struct {
	file string
	kick chan struct{}

	mu   sync.Mutex
	runs []proto.Run
	// starting is the tasks being started now (run/task), so a tick that
	// comes along meanwhile does not start them twice.
	starting map[string]bool
}

func loadGraph(configDir string) *graphBook {
	b := &graphBook{file: filepath.Join(configDir, "graph.json"), kick: make(chan struct{}, 1), starting: map[string]bool{}}
	raw, err := os.ReadFile(b.file)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("runs: %v", err)
		}
		return b
	}
	var f graphFile
	if err := json.Unmarshal(raw, &f); err != nil {
		log.Printf("runs: %s: %v", b.file, err)
		return b
	}
	for _, r := range f.Runs {
		if r.ID == "" {
			continue
		}
		if r.Tasks == nil {
			r.Tasks = []proto.RunTask{}
		}
		b.runs = append(b.runs, r)
	}
	return b
}

func (b *graphBook) saveLocked() {
	raw, _ := json.MarshalIndent(graphFile{Runs: b.runs}, "", "  ")
	tmp := b.file + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		log.Printf("runs: save: %v", err)
		return
	}
	if err := os.Rename(tmp, b.file); err != nil {
		log.Printf("runs: save: %v", err)
	}
}

// poke asks the loop to look now rather than at its next tick.
func (b *graphBook) poke() {
	select {
	case b.kick <- struct{}{}:
	default:
	}
}

// findLocked is the run named by id or name.
func (b *graphBook) findLocked(ref string) (int, *proto.Error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return -1, proto.Errorf(proto.ErrBadRequest, "name the run")
	}
	for i := range b.runs {
		if b.runs[i].ID == ref {
			return i, nil
		}
	}
	for i := range b.runs {
		if b.runs[i].Name == ref {
			return i, nil
		}
	}
	return -1, proto.Errorf(proto.ErrNotFound, "no run %q (conch run ls)", ref)
}

func (b *graphBook) list() proto.RunList {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := proto.RunList{Runs: []proto.Run{}}
	for _, r := range b.runs {
		out.Runs = append(out.Runs, copyRun(r))
	}
	return out
}

func (b *graphBook) get(ref string) (proto.Run, *proto.Error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	i, perr := b.findLocked(ref)
	if perr != nil {
		return proto.Run{}, perr
	}
	return copyRun(b.runs[i]), nil
}

// copyRun is a run nobody else holds, to hand out of the lock.
func copyRun(r proto.Run) proto.Run {
	r.Tasks = slices.Clone(r.Tasks)
	for i := range r.Tasks {
		r.Tasks[i].After = slices.Clone(r.Tasks[i].After)
	}
	r.Questions = slices.Clone(r.Questions)
	if r.Tasks == nil {
		r.Tasks = []proto.RunTask{}
	}
	return r
}

func newID(prefix string) string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return prefix + hex.EncodeToString(b[:])
}

// addTasks adds specs to r: each given an id if it has none, its
// dependencies checked to exist, and the whole graph checked for a cycle.
// r is left as it was when it refuses.
func addTasks(r *proto.Run, specs []proto.RunTaskSpec) *proto.Error {
	tasks := slices.Clone(r.Tasks)
	have := map[string]bool{}
	for _, t := range tasks {
		have[t.ID] = true
	}
	next := len(tasks) + 1
	for _, sp := range specs {
		sp.ID = strings.TrimSpace(sp.ID)
		sp.Spec = strings.TrimSpace(sp.Spec)
		sp.Gate = strings.TrimSpace(sp.Gate)
		if sp.Spec == "" {
			return proto.Errorf(proto.ErrBadRequest, "task %s needs a spec: what the agent should do", newTaskName(sp.ID))
		}
		if sp.ID == "" {
			for have[fmt.Sprintf("t%d", next)] {
				next++
			}
			sp.ID = fmt.Sprintf("t%d", next)
		}
		if strings.ContainsAny(sp.ID, " \t\n,/") {
			return proto.Errorf(proto.ErrBadRequest, "task id %q: no spaces, commas or slashes", sp.ID)
		}
		if have[sp.ID] {
			return proto.Errorf(proto.ErrBadRequest, "run %s already has a task %s", r.Name, sp.ID)
		}
		have[sp.ID] = true
		var after []string
		for _, a := range sp.After {
			if a = strings.TrimSpace(a); a != "" && !slices.Contains(after, a) {
				after = append(after, a)
			}
		}
		sp.After = after
		tasks = append(tasks, proto.RunTask{RunTaskSpec: sp, Status: proto.RunPending})
	}
	for _, t := range tasks {
		for _, a := range t.After {
			if !have[a] {
				return proto.Errorf(proto.ErrBadRequest, "task %s waits for %s, which run %s has no task of", t.ID, a, r.Name)
			}
		}
	}
	if cycle := findCycle(tasks); cycle != nil {
		return proto.Errorf(proto.ErrBadRequest, "those tasks wait on each other, so none would start: %s", strings.Join(cycle, " → "))
	}
	r.Tasks = tasks
	return nil
}

func newTaskName(id string) string {
	if id == "" {
		return "(new)"
	}
	return id
}

// findCycle is a loop of dependencies among tasks, first task repeated at
// its end, or nil.
func findCycle(tasks []proto.RunTask) []string {
	deps := map[string][]string{}
	for _, t := range tasks {
		deps[t.ID] = t.After
	}
	const (
		unseen = iota
		onPath
		finished
	)
	state := map[string]int{}
	var path []string
	var visit func(id string) []string
	visit = func(id string) []string {
		switch state[id] {
		case onPath:
			i := slices.Index(path, id)
			return append(slices.Clone(path[i:]), id)
		case finished:
			return nil
		}
		state[id] = onPath
		path = append(path, id)
		for _, d := range deps[id] {
			if c := visit(d); c != nil {
				return c
			}
		}
		path = path[:len(path)-1]
		state[id] = finished
		return nil
	}
	for _, t := range tasks {
		if c := visit(t.ID); c != nil {
			return c
		}
	}
	return nil
}

// settle works out where every task not yet started stands, and times out
// the questions nobody answered. It reports whether anything changed.
func settle(r *proto.Run, now time.Time) bool {
	changed := false
	for i := range r.Tasks {
		t := &r.Tasks[i]
		switch t.Status {
		case proto.RunPending, proto.RunReady, proto.RunBlocked, "":
		default:
			continue
		}
		status, why := proto.RunReady, ""
		var failed, waiting []string
		for _, a := range t.After {
			if d := r.Task(a); d != nil {
				switch d.Status {
				case proto.RunDone:
				case proto.RunFailed:
					failed = append(failed, a)
				default:
					waiting = append(waiting, a)
				}
			}
		}
		switch {
		case len(failed) > 0:
			status, why = proto.RunBlocked, strings.Join(failed, ", ")+" failed"
		case len(waiting) > 0:
			status, why = proto.RunPending, "waiting for "+strings.Join(waiting, ", ")
		case t.Gate != "" && !t.Approved:
			status, why = proto.RunBlocked, proto.WhyGate+t.Gate
		}
		if t.Status != status || t.Why != why {
			t.Status, t.Why = status, why
			changed = true
		}
	}
	for i := range r.Questions {
		q := &r.Questions[i]
		if q.Open() && !q.Deadline.IsZero() && !now.Before(q.Deadline) {
			q.TimedOut, q.Answer = true, q.Default
			changed = true
		}
	}
	return changed
}

// runBranch is the branch a task's worker works on.
func runBranch(r proto.Run, t proto.RunTask) string {
	return gitx.Slug(r.Name) + "/" + gitx.Slug(t.ID)
}

// runPrompt is what a task's worker is told: its spec, where its branch
// came from and what the tasks before it reported, and how to report.
func runPrompt(r proto.Run, t proto.RunTask) string {
	var b strings.Builder
	b.WriteString(t.Spec)
	fmt.Fprintf(&b, "\n\n(This is task %s of the run %q, which conch keeps.", t.ID, r.Name)
	if len(t.After) > 0 {
		first := r.Task(t.After[0])
		if first != nil && first.Branch != "" {
			fmt.Fprintf(&b, " Your branch starts from %s, the branch of task %s.", first.Branch, first.ID)
		}
		var others []string
		for _, a := range t.After[1:] {
			if d := r.Task(a); d != nil && d.Branch != "" {
				others = append(others, d.Branch)
			}
		}
		if len(others) > 0 {
			fmt.Fprintf(&b, " Merge %s into it before you start: you depend on that work too.", strings.Join(others, " and "))
		}
		for _, a := range t.After {
			if d := r.Task(a); d != nil && d.Result != "" {
				fmt.Fprintf(&b, " Task %s reported: %s.", d.ID, strings.TrimRight(oneLine(d.Result), "."))
			}
		}
	}
	b.WriteString(" When you have finished, run `conch run done \"WHAT YOU DID, in a line\"`, or `conch run fail \"WHY\"` if you cannot; say it once, at the end. To ask the agent coordinating this run something, run `conch run ask -option A -option B \"QUESTION\"`, which waits for the answer.)")
	return b.String()
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// mayCoordinate refuses a scoped agent that does not coordinate r: one
// that neither made it nor started the agent that did.
func (s *Server) mayCoordinate(c *client, r proto.Run, what string) *proto.Error {
	caller, ok := s.scopedCaller(c)
	if !ok || (r.Coordinator != "" && r.Coordinator == caller.id) {
		return nil
	}
	if r.Coordinator != "" {
		if e, perr := s.get(r.Coordinator); perr == nil && startedBy(e, caller) {
			return nil
		}
	}
	return outOfScope(caller, "%s run %s: it does not coordinate that run", what, r.Name)
}

// runCreate makes a run. An agent makes one in its own project, and
// coordinates it.
func (s *Server) runCreate(c *client, p proto.RunCreateParams) (proto.Run, *proto.Error) {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		return proto.Run{}, proto.Errorf(proto.ErrBadRequest, "a run needs a name")
	}
	if strings.ContainsAny(p.Name, "\n\t") {
		return proto.Run{}, proto.Errorf(proto.ErrBadRequest, "a run's name is one line")
	}
	if _, perr := s.projects.get(p.ProjectID); perr != nil {
		return proto.Run{}, perr
	}
	r := proto.Run{ID: newID("r"), Name: p.Name, ProjectID: p.ProjectID, Manual: p.Manual, Created: time.Now(), Tasks: []proto.RunTask{}}
	if caller, ok := s.scopedCaller(c); ok {
		if perr := s.projectInScope(caller, proto.MethodRunCreate, p.ProjectID); perr != nil {
			return proto.Run{}, perr
		}
		r.Coordinator = caller.id
	}
	if perr := addTasks(&r, p.Tasks); perr != nil {
		return proto.Run{}, perr
	}
	settle(&r, time.Now())
	b := s.graph
	b.mu.Lock()
	for _, o := range b.runs {
		if o.Name == r.Name {
			b.mu.Unlock()
			return proto.Run{}, proto.Errorf(proto.ErrBadRequest, "there is a run named %q already (%s); choose another name, or conch run rm it", r.Name, o.ID)
		}
	}
	b.runs = append(b.runs, r)
	b.saveLocked()
	out := copyRun(r)
	b.mu.Unlock()
	s.broadcast(proto.EventRunChanged, out)
	b.poke()
	return out, nil
}

// runChange changes the run named by ref under the book's lock, saves it
// and says so. change works on a copy: nothing is kept if it refuses.
func (s *Server) runChange(ref string, change func(r *proto.Run) *proto.Error) (proto.Run, *proto.Error) {
	b := s.graph
	b.mu.Lock()
	i, perr := b.findLocked(ref)
	if perr != nil {
		b.mu.Unlock()
		return proto.Run{}, perr
	}
	r := copyRun(b.runs[i])
	if perr := change(&r); perr != nil {
		b.mu.Unlock()
		return proto.Run{}, perr
	}
	settle(&r, time.Now())
	b.runs[i] = r
	b.saveLocked()
	out := copyRun(r)
	b.mu.Unlock()
	s.broadcast(proto.EventRunChanged, out)
	b.poke()
	return out, nil
}

func (s *Server) runAdd(c *client, p proto.RunAddParams) (proto.Run, *proto.Error) {
	r, perr := s.graph.get(p.Run)
	if perr != nil {
		return r, perr
	}
	if perr := s.mayCoordinate(c, r, "add tasks to"); perr != nil {
		return proto.Run{}, perr
	}
	if len(p.Tasks) == 0 {
		return proto.Run{}, proto.Errorf(proto.ErrBadRequest, "no tasks to add")
	}
	return s.runChange(r.ID, func(r *proto.Run) *proto.Error { return addTasks(r, p.Tasks) })
}

func (s *Server) runRemove(c *client, p proto.RunRef) *proto.Error {
	r, perr := s.graph.get(p.Run)
	if perr != nil {
		return perr
	}
	if perr := s.mayCoordinate(c, r, "remove"); perr != nil {
		return perr
	}
	b := s.graph
	b.mu.Lock()
	if i, perr := b.findLocked(r.ID); perr == nil {
		b.runs = slices.Delete(b.runs, i, i+1)
		b.saveLocked()
	}
	b.mu.Unlock()
	s.broadcast(proto.EventRunRemoved, proto.RunRef{Run: r.ID})
	return nil
}

// workerTask finds the task a report or a question is about: the one
// named, or — named by nobody — the one the caller's pane works on. A
// scoped agent may speak only for its own task, or for any task of a run
// it coordinates.
func (s *Server) workerTask(c *client, run, task, what string) (proto.Run, proto.RunTask, *proto.Error) {
	run, task = strings.TrimSpace(run), strings.TrimSpace(task)
	if task == "" {
		pane := ""
		if c != nil {
			pane = c.pane
		}
		var found []struct{ run, task string }
		for _, r := range s.graph.list().Runs {
			if run != "" && r.ID != run && r.Name != run {
				continue
			}
			for _, t := range r.Tasks {
				if pane != "" && t.Pane == pane {
					found = append(found, struct{ run, task string }{r.ID, t.ID})
				}
			}
		}
		switch {
		case len(found) == 0 && pane == "":
			return proto.Run{}, proto.RunTask{}, proto.Errorf(proto.ErrBadRequest, "this is not a run's worker; name the run and the task (-run, -task)")
		case len(found) == 0:
			return proto.Run{}, proto.RunTask{}, proto.Errorf(proto.ErrBadRequest, "pane %s works on no run's task; name the run and the task (-run, -task)", pane)
		}
		run, task = found[len(found)-1].run, found[len(found)-1].task
	}
	r, perr := s.graph.get(run)
	if perr != nil {
		return r, proto.RunTask{}, perr
	}
	t := r.Task(task)
	if t == nil {
		return r, proto.RunTask{}, proto.Errorf(proto.ErrNotFound, "run %s has no task %s", r.Name, task)
	}
	if caller, ok := s.scopedCaller(c); ok && t.Pane != caller.id {
		if s.mayCoordinate(c, r, what) != nil {
			return r, *t, outOfScope(caller, "%s task %s of run %s: it is not that task's worker, nor the run's coordinator", what, t.ID, r.Name)
		}
	}
	return r, *t, nil
}

// runReport is a worker saying it is done or failed, once.
func (s *Server) runReport(c *client, p proto.RunReportParams) (proto.Run, *proto.Error) {
	if p.Status != proto.RunDone && p.Status != proto.RunFailed {
		return proto.Run{}, proto.Errorf(proto.ErrBadRequest, "a task reports %s or %s, not %q", proto.RunDone, proto.RunFailed, p.Status)
	}
	r, t, perr := s.workerTask(c, p.Run, p.Task, "report for")
	if perr != nil {
		return proto.Run{}, perr
	}
	return s.runChange(r.ID, func(r *proto.Run) *proto.Error {
		t := r.Task(t.ID)
		switch t.Status {
		case proto.RunDone, proto.RunFailed:
			return proto.Errorf(proto.ErrBadRequest, "task %s of run %s has already reported %s; a task reports once", t.ID, r.Name, t.Status)
		case proto.RunRunning:
		default:
			return proto.Errorf(proto.ErrBadRequest, "task %s of run %s has not started (%s)", t.ID, r.Name, t.Status)
		}
		t.Status, t.Result, t.Ended = p.Status, strings.TrimSpace(p.Result), time.Now()
		t.Why = ""
		return nil
	})
}

// runAsk records a worker's question for its coordinator.
func (s *Server) runAsk(c *client, p proto.RunAskParams) (proto.RunAskResult, *proto.Error) {
	p.Text = strings.TrimSpace(p.Text)
	if p.Text == "" {
		return proto.RunAskResult{}, proto.Errorf(proto.ErrBadRequest, "ask a question")
	}
	if p.TimeoutSecs < 0 {
		return proto.RunAskResult{}, proto.Errorf(proto.ErrBadRequest, "a timeout cannot be negative")
	}
	var opts []string
	for _, o := range p.Options {
		if o = strings.TrimSpace(o); o != "" && !slices.Contains(opts, o) {
			opts = append(opts, o)
		}
	}
	p.Default = strings.TrimSpace(p.Default)
	if p.Default != "" && len(opts) > 0 && !slices.Contains(opts, p.Default) {
		return proto.RunAskResult{}, proto.Errorf(proto.ErrBadRequest, "the default %q is not one of the options", p.Default)
	}
	r, t, perr := s.workerTask(c, p.Run, p.Task, "ask for")
	if perr != nil {
		return proto.RunAskResult{}, perr
	}
	var q proto.RunQuestion
	out, perr := s.runChange(r.ID, func(r *proto.Run) *proto.Error {
		now := time.Now()
		q = proto.RunQuestion{ID: fmt.Sprintf("q%d", len(r.Questions)+1), Task: t.ID, Text: p.Text, Options: opts, Default: p.Default, Asked: now}
		if p.TimeoutSecs > 0 {
			q.Deadline = now.Add(time.Duration(p.TimeoutSecs) * time.Second)
		}
		r.Questions = append(r.Questions, q)
		return nil
	})
	if perr != nil {
		return proto.RunAskResult{}, perr
	}
	return proto.RunAskResult{Run: out.ID, Question: q}, nil
}

// runAnswer is the coordinator answering a question.
func (s *Server) runAnswer(c *client, p proto.RunAnswerParams) (proto.Run, *proto.Error) {
	r, perr := s.graph.get(p.Run)
	if perr != nil {
		return r, perr
	}
	if perr := s.mayCoordinate(c, r, "answer a question in"); perr != nil {
		return proto.Run{}, perr
	}
	answer := strings.TrimSpace(p.Answer)
	if answer == "" {
		return proto.Run{}, proto.Errorf(proto.ErrBadRequest, "give an answer")
	}
	return s.runChange(r.ID, func(r *proto.Run) *proto.Error {
		for i := range r.Questions {
			q := &r.Questions[i]
			if q.ID != p.Question {
				continue
			}
			switch {
			case q.TimedOut:
				return proto.Errorf(proto.ErrBadRequest, "question %s timed out unanswered; the worker has gone on without it", q.ID)
			case q.Answered:
				return proto.Errorf(proto.ErrBadRequest, "question %s was answered already: %s", q.ID, q.Answer)
			}
			if len(q.Options) > 0 {
				i := slices.IndexFunc(q.Options, func(o string) bool { return strings.EqualFold(o, answer) })
				if i < 0 {
					return proto.Errorf(proto.ErrBadRequest, "answer one of: %s", strings.Join(q.Options, ", "))
				}
				answer = q.Options[i]
			}
			q.Answer, q.Answered = answer, true
			return nil
		}
		return proto.Errorf(proto.ErrNotFound, "run %s has no question %s", r.Name, p.Question)
	})
}

// runApprove is a person approving a gate, or turning it down. Never an
// agent's: a gate is where the person decides.
func (s *Server) runApprove(c *client, p proto.RunApproveParams) (proto.Run, *proto.Error) {
	if caller, ok := s.scopedCaller(c); ok {
		return proto.Run{}, outOfScope(caller, "approve a run's gate: that is for the person to decide")
	}
	return s.runChange(p.Run, func(r *proto.Run) *proto.Error {
		t := r.Task(strings.TrimSpace(p.Task))
		switch {
		case t == nil:
			return proto.Errorf(proto.ErrNotFound, "run %s has no task %s", r.Name, p.Task)
		case t.Gate == "":
			return proto.Errorf(proto.ErrBadRequest, "task %s of run %s has no gate to approve", t.ID, r.Name)
		case t.Approved:
			return proto.Errorf(proto.ErrBadRequest, "task %s of run %s is approved already", t.ID, r.Name)
		}
		switch t.Status {
		case proto.RunPending, proto.RunBlocked, proto.RunReady:
		default:
			return proto.Errorf(proto.ErrBadRequest, "task %s of run %s is %s already", t.ID, r.Name, t.Status)
		}
		if p.Deny {
			t.Status, t.Why, t.Ended = proto.RunFailed, "a person turned it down: "+t.Gate, time.Now()
			return nil
		}
		t.Approved = true
		return nil
	})
}

// runStart starts a run's ready tasks now — every one, or the one named —
// for a manual run, or one whose tick has not come yet.
func (s *Server) runStart(c *client, p proto.RunStartParams) (proto.Run, *proto.Error) {
	r, perr := s.graph.get(p.Run)
	if perr != nil {
		return r, perr
	}
	if perr := s.mayCoordinate(c, r, "start tasks of"); perr != nil {
		return proto.Run{}, perr
	}
	if p.Task != "" {
		t := r.Task(p.Task)
		switch {
		case t == nil:
			return proto.Run{}, proto.Errorf(proto.ErrNotFound, "run %s has no task %s", r.Name, p.Task)
		case t.Status != proto.RunReady:
			why := ""
			if t.Why != "" {
				why = ": " + t.Why
			}
			return proto.Run{}, proto.Errorf(proto.ErrBadRequest, "task %s of run %s is %s, not ready%s", t.ID, r.Name, t.Status, why)
		}
	}
	var firstErr *proto.Error
	for _, t := range r.Tasks {
		if t.Status == proto.RunReady && (p.Task == "" || t.ID == p.Task) {
			if perr := s.startRunTask(r.ID, t.ID); perr != nil && firstErr == nil {
				firstErr = perr
			}
		}
	}
	if firstErr != nil && p.Task != "" {
		return proto.Run{}, firstErr
	}
	return s.graph.get(r.ID)
}

// startRunTask starts the worker of a ready task, on a branch made from
// its first dependency's, and records its pane. A task that cannot start
// fails, saying why: retrying it every tick would only say so again.
func (s *Server) startRunTask(runID, taskID string) *proto.Error {
	b := s.graph
	key := runID + "/" + taskID
	b.mu.Lock()
	i, perr := b.findLocked(runID)
	if perr != nil || b.starting[key] {
		b.mu.Unlock()
		return perr
	}
	r := copyRun(b.runs[i])
	t := r.Task(taskID)
	if t == nil || t.Status != proto.RunReady {
		b.mu.Unlock()
		return nil
	}
	b.starting[key] = true
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.starting, key)
		b.mu.Unlock()
	}()

	tp := proto.TaskCreateParams{ProjectID: r.ProjectID, Prompt: runPrompt(r, *t), Branch: runBranch(r, *t), Agent: t.Agent, Cols: 120, Rows: 40}
	if len(t.After) > 0 {
		if first := r.Task(t.After[0]); first != nil {
			tp.Base = first.Branch
		}
	}
	info, branch, perr := s.startTask(tp)
	if perr == nil && r.Coordinator != "" {
		s.startedFor(r.Coordinator, info.ID)
	}
	_, cerr := s.runChange(runID, func(r *proto.Run) *proto.Error {
		t := r.Task(taskID)
		if t == nil {
			return proto.Errorf(proto.ErrNotFound, "gone")
		}
		if perr != nil {
			t.Status, t.Why, t.Ended = proto.RunFailed, "it could not start: "+perr.Message, time.Now()
			return nil
		}
		t.Status, t.Why, t.Pane, t.Branch, t.Started = proto.RunRunning, "", info.ID, branch, time.Now()
		return nil
	})
	if perr != nil {
		log.Printf("runs: task %s of run %s could not start: %s", taskID, r.Name, perr.Message)
		return perr
	}
	if cerr != nil { // the run was taken away while it started
		log.Printf("runs: run %s went while task %s started in %s", runID, taskID, info.ID)
	}
	return nil
}

// startedFor makes the coordinator the creator of a worker the server
// started for it, as if it had started it itself, so its scope reaches
// the worker (scope.go).
func (s *Server) startedFor(coordinator, id string) {
	lineage := []string{coordinator}
	if by, perr := s.get(coordinator); perr == nil {
		lineage = append(lineage, by.creators()...)
	}
	e, perr := s.get(id)
	if perr != nil {
		return
	}
	e.mu.Lock()
	if e.lineage == nil {
		e.lineage = lineage
	}
	e.mu.Unlock()
}

// advanceRuns is one look at every run: a worker whose pane ended without
// a report has failed, a question past its deadline is timed out, and a
// ready task of a run that is not manual is started.
func (s *Server) advanceRuns() {
	alive := map[string]bool{}
	for _, info := range s.list() {
		alive[info.ID] = info.State == proto.PaneRunning
	}
	b := s.graph
	now := time.Now()
	var changed []proto.Run
	type start struct{ run, task string }
	var starts []start
	b.mu.Lock()
	for i := range b.runs {
		r := &b.runs[i]
		moved := false
		for j := range r.Tasks {
			t := &r.Tasks[j]
			if t.Status == proto.RunRunning && t.Pane != "" && !alive[t.Pane] {
				t.Status, t.Why, t.Ended = proto.RunFailed, "its worker's pane ("+t.Pane+") ended before it reported", now
				moved = true
			}
		}
		if settle(r, now) || moved {
			changed = append(changed, copyRun(*r))
		}
		if r.Manual {
			continue
		}
		for _, t := range r.Tasks {
			if t.Status == proto.RunReady && !b.starting[r.ID+"/"+t.ID] {
				starts = append(starts, start{r.ID, t.ID})
			}
		}
	}
	if len(changed) > 0 {
		b.saveLocked()
	}
	b.mu.Unlock()
	for _, r := range changed {
		s.broadcast(proto.EventRunChanged, r)
	}
	for _, st := range starts {
		_ = s.startRunTask(st.run, st.task)
	}
}

func (s *Server) runGraph(quit <-chan struct{}) {
	t := time.NewTicker(graphEvery)
	defer t.Stop()
	for {
		select {
		case <-quit:
			return
		case <-t.C:
		case <-s.graph.kick:
		}
		s.advanceRuns()
	}
}

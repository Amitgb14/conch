package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// ---- the graph itself, without panes ----

func specs(pairs ...string) []proto.RunTaskSpec {
	var out []proto.RunTaskSpec
	for _, p := range pairs {
		id, after, _ := strings.Cut(p, "<")
		sp := proto.RunTaskSpec{ID: id, Spec: "do " + id}
		if after != "" {
			sp.After = strings.Split(after, ",")
		}
		out = append(out, sp)
	}
	return out
}

func statuses(r proto.Run) string {
	var out []string
	for _, t := range r.Tasks {
		out = append(out, t.ID+"="+t.Status)
	}
	return strings.Join(out, " ")
}

func finishTask(t *testing.T, r *proto.Run, id, status string) {
	t.Helper()
	task := r.Task(id)
	if task == nil {
		t.Fatalf("no task %s", id)
	}
	task.Status = status
	settle(r, time.Now())
}

func TestRunChain(t *testing.T) {
	r := proto.Run{Name: "job"}
	if perr := addTasks(&r, specs("a", "b<a", "c<b")); perr != nil {
		t.Fatal(perr)
	}
	settle(&r, time.Now())
	if got := statuses(r); got != "a=ready b=pending c=pending" {
		t.Fatal(got)
	}
	if why := r.Task("b").Why; why != "waiting for a" {
		t.Fatalf("why %q", why)
	}
	finishTask(t, &r, "a", proto.RunDone)
	if got := statuses(r); got != "a=done b=ready c=pending" {
		t.Fatal(got)
	}
	finishTask(t, &r, "b", proto.RunDone)
	finishTask(t, &r, "c", proto.RunDone)
	if !r.Over() {
		t.Fatal("a finished chain is not over")
	}
}

func TestRunDiamond(t *testing.T) {
	r := proto.Run{Name: "job"}
	if perr := addTasks(&r, specs("a", "b<a", "c<a", "d<b,c")); perr != nil {
		t.Fatal(perr)
	}
	finishTask(t, &r, "a", proto.RunDone)
	if got := statuses(r); got != "a=done b=ready c=ready d=pending" {
		t.Fatal(got)
	}
	finishTask(t, &r, "b", proto.RunDone)
	r.Task("c").Status = proto.RunRunning
	settle(&r, time.Now())
	if d := r.Task("d"); d.Status != proto.RunPending || d.Why != "waiting for c" {
		t.Fatalf("d with c still running: %+v", d)
	}
	finishTask(t, &r, "c", proto.RunDone)
	if d := r.Task("d"); d.Status != proto.RunReady {
		t.Fatalf("d with both done: %+v", d)
	}
	// Its worker is told where its branch came from, to merge the other
	// side, and what both reported.
	r.Task("b").Branch, r.Task("b").Result = "job/b", "added the model."
	r.Task("c").Branch, r.Task("c").Result = "job/c", "added the view"
	p := runPrompt(r, *r.Task("d"))
	for _, want := range []string{"do d", "starts from job/b", "Merge job/c", "Task b reported: added the model.", "Task c reported: added the view.", "conch run done"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %q:\n%s", want, p)
		}
	}
	if b := runBranch(r, *r.Task("d")); b != "job/d" {
		t.Fatalf("branch %q", b)
	}
}

func TestRunRefusesBadGraphs(t *testing.T) {
	for name, tc := range map[string]struct {
		specs []proto.RunTaskSpec
		want  string
	}{
		"cycle":        {specs("a<c", "b<a", "c<b"), "wait on each other"},
		"self":         {specs("a<a"), "a → a"},
		"unknown":      {specs("a<zz"), "no task of"},
		"duplicate":    {specs("a", "a"), "already has a task a"},
		"empty spec":   {[]proto.RunTaskSpec{{ID: "a", Spec: "  "}}, "needs a spec"},
		"bad id":       {[]proto.RunTaskSpec{{ID: "a b", Spec: "x"}}, "no spaces"},
		"cycle in sub": {specs("a", "b<a,d", "c<b", "d<c"), "b → d → c → b"},
	} {
		t.Run(name, func(t *testing.T) {
			r := proto.Run{Name: "job"}
			addTasks(&r, specs("x"))
			perr := addTasks(&r, tc.specs)
			if perr == nil || !strings.Contains(perr.Message, tc.want) {
				t.Fatalf("got %v, want %q", perr, tc.want)
			}
			if len(r.Tasks) != 1 {
				t.Fatalf("a refused add changed the run: %+v", r.Tasks)
			}
		})
	}
	// Ids are given to tasks without one, past those taken; repeated and
	// blank dependencies are dropped.
	r := proto.Run{Name: "job"}
	if perr := addTasks(&r, []proto.RunTaskSpec{{ID: "t2", Spec: "x"}, {Spec: "y"}, {Spec: "z", After: []string{"t2", " ", "t2"}}}); perr != nil {
		t.Fatal(perr)
	}
	if got := statuses(r); got != "t2=pending t1=pending t3=pending" {
		t.Fatal(got)
	}
	if after := r.Task("t3").After; !slices.Equal(after, []string{"t2"}) {
		t.Fatalf("after %v", after)
	}
}

func TestRunFailureBlocksAndGateWaits(t *testing.T) {
	r := proto.Run{Name: "job"}
	addTasks(&r, []proto.RunTaskSpec{{ID: "a", Spec: "x"}, {ID: "b", Spec: "y", After: []string{"a"}}, {ID: "ship", Spec: "z", Gate: "merge it?"}})
	settle(&r, time.Now())
	if g := r.Task("ship"); !g.GateWaits() || g.Why != "waiting for a person to approve: merge it?" {
		t.Fatalf("gate: %+v", g)
	}
	finishTask(t, &r, "a", proto.RunFailed)
	if b := r.Task("b"); b.Status != proto.RunBlocked || b.Why != "a failed" || b.GateWaits() {
		t.Fatalf("after a failure: %+v", b)
	}
	r.Task("ship").Approved = true
	settle(&r, time.Now())
	if g := r.Task("ship"); g.Status != proto.RunReady {
		t.Fatalf("approved: %+v", g)
	}
}

func TestRunQuestionTimesOut(t *testing.T) {
	now := time.Now()
	r := proto.Run{Name: "job", Questions: []proto.RunQuestion{
		{ID: "q1", Text: "which?", Default: "left", Deadline: now.Add(time.Minute)},
		{ID: "q2", Text: "ever?"}, // no deadline: waits for good
		{ID: "q3", Text: "done", Answer: "yes", Answered: true, Deadline: now.Add(-time.Hour)},
	}}
	if settle(&r, now) {
		t.Fatal("nothing was due")
	}
	if !settle(&r, now.Add(time.Minute)) {
		t.Fatal("the deadline passed unnoticed")
	}
	q := r.Questions
	if !q[0].TimedOut || q[0].Answer != "left" || q[0].Open() || !q[1].Open() || q[2].TimedOut {
		t.Fatalf("%+v", q)
	}
}

func TestRunGraphFile(t *testing.T) {
	dir := t.TempDir()
	if b := loadGraph(dir); len(b.list().Runs) != 0 {
		t.Fatal("runs from nowhere")
	}
	os.WriteFile(filepath.Join(dir, "graph.json"), []byte("{not json"), 0o600)
	if b := loadGraph(dir); len(b.list().Runs) != 0 {
		t.Fatal("runs from a broken file")
	}
	// A run without an id is dropped; one without tasks lists none, not null.
	os.WriteFile(filepath.Join(dir, "graph.json"), []byte(`{"runs":[{"name":"x"},{"id":"r1","name":"y"}]}`), 0o600)
	b := loadGraph(dir)
	list := b.list().Runs
	if len(list) != 1 || list[0].Tasks == nil {
		t.Fatalf("%+v", list)
	}
	raw, _ := json.Marshal(list[0])
	if !strings.Contains(string(raw), `"tasks":[]`) {
		t.Fatalf("%s", raw)
	}
	if _, perr := b.get("nope"); perr == nil || perr.Code != proto.ErrNotFound {
		t.Fatal(perr)
	}
	if _, perr := b.get(" "); perr == nil || perr.Code != proto.ErrBadRequest {
		t.Fatal(perr)
	}
}

// ---- runs on a server ----

func mustRun(t *testing.T) func(proto.Run, *proto.Error) proto.Run {
	return func(r proto.Run, perr *proto.Error) proto.Run {
		t.Helper()
		if perr != nil {
			t.Fatal(perr)
		}
		return r
	}
}

func runTask(t *testing.T, s *Server, run, task string) proto.RunTask {
	t.Helper()
	r, perr := s.graph.get(run)
	if perr != nil {
		t.Fatal(perr)
	}
	tk := r.Task(task)
	if tk == nil {
		t.Fatalf("no task %s in %s", task, statuses(r))
	}
	return *tk
}

// A chain starts its first task, and the second once the first reports
// done, on a branch made from the first's — whose commits it has.
func TestRunStartsTasksInOrder(t *testing.T) {
	s, p := afterFixture(t)
	r := mustRun(t)(s.runCreate(nil, proto.RunCreateParams{Name: "Job", ProjectID: p.id, Tasks: specs("model", "api<model")}))
	if r.Coordinator != "" || statuses(r) != "model=ready api=pending" {
		t.Fatalf("%+v", r)
	}
	s.advanceRuns()
	m := runTask(t, s, r.ID, "model")
	if m.Status != proto.RunRunning || m.Pane == "" || m.Branch != "job/model" {
		t.Fatalf("model: %+v", m)
	}
	branchPane(t, s, p, "job/model")
	head := commitOn(t, p, "job/model", "model.go")
	if a := runTask(t, s, r.ID, "api"); a.Status != proto.RunPending {
		t.Fatalf("api started early: %+v", a)
	}

	// Done, once: the second time is refused, and changes nothing.
	mustRun(t)(s.runReport(nil, proto.RunReportParams{Run: r.ID, Task: "model", Status: proto.RunDone, Result: "the model is in"}))
	_, perr := s.runReport(nil, proto.RunReportParams{Run: r.ID, Task: "model", Status: proto.RunFailed})
	if perr == nil || !strings.Contains(perr.Message, "already reported done") {
		t.Fatalf("second report: %v", perr)
	}
	if m := runTask(t, s, r.ID, "model"); m.Status != proto.RunDone || m.Result != "the model is in" || m.Ended.IsZero() {
		t.Fatalf("model after reports: %+v", m)
	}
	// A task that has not started has nothing to report.
	if _, perr := s.runReport(nil, proto.RunReportParams{Run: "Job", Task: "nope", Status: proto.RunDone}); perr == nil || perr.Code != proto.ErrNotFound {
		t.Fatalf("unknown task: %v", perr)
	}
	if _, perr := s.runReport(nil, proto.RunReportParams{Run: "Job", Task: "model", Status: "great"}); perr == nil {
		t.Fatal("a status that is neither done nor failed")
	}

	s.advanceRuns()
	a := runTask(t, s, r.ID, "api")
	if a.Status != proto.RunRunning || a.Branch != "job/api" {
		t.Fatalf("api: %+v", a)
	}
	apiHead := commitOn(t, p, "job/api", "api.go")
	if !isAncestor(t, p, head, apiHead) {
		t.Fatal("api's branch does not start from model's")
	}

	// Its worker reports for itself, by its pane alone.
	worker := &client{pane: a.Pane}
	mustRun(t)(s.runReport(worker, proto.RunReportParams{Status: proto.RunDone}))
	if r := mustRun(t)(s.graph.get("Job")); !r.Over() {
		t.Fatalf("not over: %s", statuses(r))
	}
	// A pane that works on nothing is told to name the task.
	if _, perr := s.runReport(&client{pane: "p999"}, proto.RunReportParams{Status: proto.RunDone}); perr == nil || !strings.Contains(perr.Message, "works on no run's task") {
		t.Fatal(perr)
	}
	if _, perr := s.runReport(nil, proto.RunReportParams{Status: proto.RunDone}); perr == nil || !strings.Contains(perr.Message, "name the run") {
		t.Fatal(perr)
	}
	// Two runs cannot share a name; a run needs a project and a name.
	if _, perr := s.runCreate(nil, proto.RunCreateParams{Name: "Job", ProjectID: p.id}); perr == nil {
		t.Fatal("a second run named Job")
	}
	if _, perr := s.runCreate(nil, proto.RunCreateParams{Name: "x", ProjectID: "nope"}); perr == nil {
		t.Fatal("a run in no project")
	}
	if _, perr := s.runCreate(nil, proto.RunCreateParams{Name: " ", ProjectID: p.id}); perr == nil {
		t.Fatal("a run with no name")
	}
}

// A worker whose pane ends without a report has failed, and what waits
// for it is blocked, saying so.
func TestRunWorkerDiesBeforeReporting(t *testing.T) {
	s, p := afterFixture(t)
	r := mustRun(t)(s.runCreate(nil, proto.RunCreateParams{Name: "job", ProjectID: p.id, Tasks: specs("a", "b<a")}))
	s.advanceRuns()
	a := runTask(t, s, r.ID, "a")
	if a.Status != proto.RunRunning {
		t.Fatalf("%+v", a)
	}
	s.close(a.Pane)
	s.advanceRuns()
	a = runTask(t, s, r.ID, "a")
	if a.Status != proto.RunFailed || !strings.Contains(a.Why, "ended before it reported") {
		t.Fatalf("a: %+v", a)
	}
	if b := runTask(t, s, r.ID, "b"); b.Status != proto.RunBlocked || b.Why != "a failed" {
		t.Fatalf("b: %+v", b)
	}
	// Reporting now is too late.
	if _, perr := s.runReport(nil, proto.RunReportParams{Run: r.ID, Task: "a", Status: proto.RunDone}); perr == nil {
		t.Fatal("a dead task reported done")
	}
}

// A manual run starts nothing by itself; run.start starts what is ready,
// and refuses what is not.
func TestRunManualStart(t *testing.T) {
	s, p := afterFixture(t)
	r := mustRun(t)(s.runCreate(nil, proto.RunCreateParams{Name: "job", ProjectID: p.id, Manual: true, Tasks: specs("a", "b<a")}))
	s.advanceRuns()
	if a := runTask(t, s, r.ID, "a"); a.Status != proto.RunReady {
		t.Fatalf("a manual run started %+v", a)
	}
	if _, perr := s.runStart(nil, proto.RunStartParams{Run: r.ID, Task: "b"}); perr == nil || !strings.Contains(perr.Message, "waiting for a") {
		t.Fatalf("started b: %v", perr)
	}
	r = mustRun(t)(s.runStart(nil, proto.RunStartParams{Run: "job"}))
	if a := r.Task("a"); a.Status != proto.RunRunning || a.Pane == "" {
		t.Fatalf("%+v", a)
	}
	// A task that cannot start fails, saying why, rather than trying
	// again every tick.
	mustRun(t)(s.runAdd(nil, proto.RunAddParams{Run: "job", Tasks: []proto.RunTaskSpec{{ID: "c", Spec: "x", Agent: "no-such-agent"}}}))
	if _, perr := s.runStart(nil, proto.RunStartParams{Run: "job", Task: "c"}); perr == nil {
		t.Fatal("an unknown agent started")
	}
	if c := runTask(t, s, r.ID, "c"); c.Status != proto.RunFailed || !strings.Contains(c.Why, "could not start") {
		t.Fatalf("c: %+v", c)
	}
	if _, perr := s.runAdd(nil, proto.RunAddParams{Run: "job"}); perr == nil {
		t.Fatal("added nothing")
	}
	if perr := s.runRemove(nil, proto.RunRef{Run: "job"}); perr != nil {
		t.Fatal(perr)
	}
	if len(s.graph.list().Runs) != 0 {
		t.Fatal("removed run still listed")
	}
}

// A gate waits for the person, and an approval given to the server after
// a reload — a new server reading the same graph.json — starts it.
func TestRunGateApprovedAfterReload(t *testing.T) {
	s, p := afterFixture(t)
	r := mustRun(t)(s.runCreate(nil, proto.RunCreateParams{Name: "job", ProjectID: p.id, Tasks: []proto.RunTaskSpec{
		{ID: "ship", Spec: "release it", Gate: "release 1.2?"},
		{ID: "no", Spec: "x", Gate: "this one?"},
	}}))
	s.advanceRuns()
	if g := runTask(t, s, r.ID, "ship"); !g.GateWaits() || g.Pane != "" {
		t.Fatalf("gate: %+v", g)
	}

	s2 := New(filepath.Join(s.configDir, "s2.sock"), s.configDir)
	s2.projects = s.projects // the projects are loaded, as Run would load them
	s2.adapters, s2.manifests = s.adapters, s.manifests
	s2.watcher.stop()
	s2.watcher = nil
	if g := runTask(t, s2, r.ID, "ship"); !g.GateWaits() {
		t.Fatalf("after reload: %+v", g)
	}
	r2 := mustRun(t)(s2.runApprove(nil, proto.RunApproveParams{Run: "job", Task: "ship"}))
	if g := r2.Task("ship"); !g.Approved || g.Status != proto.RunReady {
		t.Fatalf("approved: %+v", g)
	}
	if _, perr := s2.runApprove(nil, proto.RunApproveParams{Run: "job", Task: "ship"}); perr == nil {
		t.Fatal("approved twice")
	}
	mustRun(t)(s2.runApprove(nil, proto.RunApproveParams{Run: "job", Task: "no", Deny: true}))
	if g := runTask(t, s2, r.ID, "no"); g.Status != proto.RunFailed || !strings.Contains(g.Why, "turned it down") {
		t.Fatalf("denied: %+v", g)
	}
	if _, perr := s2.runApprove(nil, proto.RunApproveParams{Run: "job", Task: "nope"}); perr == nil {
		t.Fatal("approved a task that is not there")
	}
	// And the approval is kept for the next one in turn.
	s3 := New(filepath.Join(s.configDir, "s3.sock"), s.configDir)
	s3.watcher.stop()
	if g := runTask(t, s3, r.ID, "ship"); !g.Approved {
		t.Fatalf("approval not saved: %+v", g)
	}
	s2.advanceRuns()
	if g := runTask(t, s2, r.ID, "ship"); g.Status != proto.RunRunning {
		t.Fatalf("approved gate did not start: %+v", g)
	}
	for _, info := range s2.list() {
		s2.close(info.ID)
	}
	// A task with no gate has none to approve.
	mustRun(t)(s.runAdd(nil, proto.RunAddParams{Run: "job", Tasks: specs("plain")}))
	if _, perr := s.runApprove(nil, proto.RunApproveParams{Run: "job", Task: "plain"}); perr == nil || !strings.Contains(perr.Message, "no gate") {
		t.Fatal(perr)
	}
}

// A question is answered by its coordinator, or times out on its default;
// an answer outside the options, or too late, is refused.
func TestRunQuestions(t *testing.T) {
	s, p := afterFixture(t)
	r := mustRun(t)(s.runCreate(nil, proto.RunCreateParams{Name: "job", ProjectID: p.id, Manual: true, Tasks: specs("a")}))
	if _, perr := s.runAsk(nil, proto.RunAskParams{Run: "job", Task: "a", Text: "x", Options: []string{"l", "r"}, Default: "up"}); perr == nil {
		t.Fatal("a default outside the options")
	}
	if _, perr := s.runAsk(nil, proto.RunAskParams{Run: "job", Task: "a", Text: " "}); perr == nil {
		t.Fatal("an empty question")
	}
	if _, perr := s.runAsk(nil, proto.RunAskParams{Run: "job", Task: "a", Text: "x", TimeoutSecs: -1}); perr == nil {
		t.Fatal("a negative timeout")
	}
	q1 := mustAsk(t)(s.runAsk(nil, proto.RunAskParams{Run: "job", Task: "a", Text: "left or right?", Options: []string{"Left", "Right", "Left"}}))
	if q1.Question.ID != "q1" || !slices.Equal(q1.Question.Options, []string{"Left", "Right"}) {
		t.Fatalf("%+v", q1)
	}
	if _, perr := s.runAnswer(nil, proto.RunAnswerParams{Run: "job", Question: "q1", Answer: "up"}); perr == nil || !strings.Contains(perr.Message, "Left, Right") {
		t.Fatal(perr)
	}
	r = mustRun(t)(s.runAnswer(nil, proto.RunAnswerParams{Run: "job", Question: "q1", Answer: "right"}))
	if q := r.Questions[0]; q.Answer != "Right" || !q.Answered {
		t.Fatalf("%+v", q)
	}
	if _, perr := s.runAnswer(nil, proto.RunAnswerParams{Run: "job", Question: "q1", Answer: "Left"}); perr == nil {
		t.Fatal("answered twice")
	}

	q2 := mustAsk(t)(s.runAsk(nil, proto.RunAskParams{Run: "job", Task: "a", Text: "go on?", Default: "yes", TimeoutSecs: 60}))
	if q2.Question.Deadline.IsZero() {
		t.Fatal("no deadline")
	}
	s.graph.mu.Lock()
	s.graph.runs[0].Questions[1].Deadline = time.Now().Add(-time.Second)
	s.graph.mu.Unlock()
	s.advanceRuns()
	r = mustRun(t)(s.graph.get("job"))
	if q := r.Questions[1]; !q.TimedOut || q.Answer != "yes" {
		t.Fatalf("timed out: %+v", q)
	}
	if _, perr := s.runAnswer(nil, proto.RunAnswerParams{Run: "job", Question: "q2", Answer: "no"}); perr == nil || !strings.Contains(perr.Message, "timed out") {
		t.Fatal(perr)
	}
	if _, perr := s.runAnswer(nil, proto.RunAnswerParams{Run: "job", Question: "q9", Answer: "no"}); perr == nil || perr.Code != proto.ErrNotFound {
		t.Fatal(perr)
	}
	if _, perr := s.runAnswer(nil, proto.RunAnswerParams{Run: "job", Question: "q1", Answer: ""}); perr == nil {
		t.Fatal("an empty answer")
	}
}

func mustAsk(t *testing.T) func(proto.RunAskResult, *proto.Error) proto.RunAskResult {
	return func(r proto.RunAskResult, perr *proto.Error) proto.RunAskResult {
		t.Helper()
		if perr != nil {
			t.Fatal(perr)
		}
		return r
	}
}

// A worker reports and asks for its own task only; a coordinator reaches
// the run it made and the workers started for it, and no other; nobody but
// the person approves a gate.
func TestRunScoping(t *testing.T) {
	s, p := afterFixture(t)
	runA := mustRun(t)(s.runCreate(nil, proto.RunCreateParams{Name: "A", ProjectID: p.id, Tasks: specs("a")}))
	runB := mustRun(t)(s.runCreate(nil, proto.RunCreateParams{Name: "B", ProjectID: p.id, Tasks: []proto.RunTaskSpec{{ID: "b", Spec: "x"}, {ID: "g", Spec: "y", Gate: "ok?"}}}))
	s.advanceRuns()
	a, b := runTask(t, s, runA.ID, "a"), runTask(t, s, runB.ID, "b")
	branchPane(t, s, p, a.Branch)
	branchPane(t, s, p, b.Branch)
	workerA, workerB := &client{pane: a.Pane}, &client{pane: b.Pane}

	// A worker of A cannot report, ask or answer for B, nor change it.
	if _, perr := s.runReport(workerA, proto.RunReportParams{Run: "B", Task: "b", Status: proto.RunDone}); perr == nil || perr.Code != proto.ErrOutOfScope {
		t.Fatalf("A's worker reported for B: %v", perr)
	}
	if _, perr := s.runAsk(workerA, proto.RunAskParams{Run: "B", Task: "b", Text: "x"}); perr == nil || perr.Code != proto.ErrOutOfScope {
		t.Fatalf("A's worker asked for B: %v", perr)
	}
	if _, perr := s.runAdd(workerA, proto.RunAddParams{Run: "B", Tasks: specs("z")}); perr == nil || perr.Code != proto.ErrOutOfScope {
		t.Fatalf("A's worker added to B: %v", perr)
	}
	if perr := s.runRemove(workerA, proto.RunRef{Run: "B"}); perr == nil || perr.Code != proto.ErrOutOfScope {
		t.Fatalf("A's worker removed B: %v", perr)
	}
	if _, perr := s.runApprove(workerB, proto.RunApproveParams{Run: "B", Task: "g"}); perr == nil || perr.Code != proto.ErrOutOfScope {
		t.Fatalf("an agent approved a gate: %v", perr)
	}
	// Naming nothing, a worker reports for its own task; it has no other.
	mustAsk(t)(s.runAsk(workerB, proto.RunAskParams{Text: "which db?"}))
	mustRun(t)(s.runReport(workerA, proto.RunReportParams{Status: proto.RunDone, Result: "ok"}))
	if got := runTask(t, s, runB.ID, "b"); got.Status != proto.RunRunning {
		t.Fatalf("B's task moved: %+v", got)
	}

	// B's worker coordinates a run of its own: its workers are its, so it
	// reaches them, answers their questions and changes its run — and A's
	// worker reaches none of it.
	runC := mustRun(t)(s.runCreate(workerB, proto.RunCreateParams{Name: "C", ProjectID: p.id, Tasks: specs("c")}))
	if runC.Coordinator != b.Pane {
		t.Fatalf("coordinator %q, want %s", runC.Coordinator, b.Pane)
	}
	s.advanceRuns()
	c := runTask(t, s, runC.ID, "c")
	if c.Pane == "" {
		t.Fatalf("c: %+v", c)
	}
	// Started by the server, the worker counts as the coordinator's: the
	// same lineage a pane it started itself would have (scope.go).
	if ce := a5Entry(t, s, c.Pane); !startedBy(ce, mustCaller(t, s, workerB)) || startedBy(ce, mustCaller(t, s, workerA)) {
		t.Fatalf("worker C's lineage: %v", ce.creators())
	}
	branchPane(t, s, p, c.Branch)
	workerC := &client{pane: c.Pane}
	q := mustAsk(t)(s.runAsk(workerC, proto.RunAskParams{Text: "tabs or spaces?", Options: []string{"tabs", "spaces"}}))
	if _, perr := s.runAnswer(workerA, proto.RunAnswerParams{Run: "C", Question: q.Question.ID, Answer: "tabs"}); perr == nil || perr.Code != proto.ErrOutOfScope {
		t.Fatalf("another agent answered: %v", perr)
	}
	if _, perr := s.runAnswer(workerC, proto.RunAnswerParams{Run: "C", Question: q.Question.ID, Answer: "tabs"}); perr == nil || perr.Code != proto.ErrOutOfScope {
		t.Fatalf("the worker answered itself: %v", perr)
	}
	mustRun(t)(s.runAnswer(workerB, proto.RunAnswerParams{Run: "C", Question: q.Question.ID, Answer: "spaces"}))
	mustRun(t)(s.runAdd(workerB, proto.RunAddParams{Run: "C", Tasks: specs("d<c")}))
	// The coordinator may say for its worker that it failed.
	mustRun(t)(s.runReport(workerB, proto.RunReportParams{Run: "C", Task: "c", Status: proto.RunFailed, Result: "stuck"}))
	if _, perr := s.runReport(workerC, proto.RunReportParams{Status: proto.RunDone}); perr == nil {
		t.Fatal("reported after its coordinator did")
	}
	// An agent makes a run only in its own project.
	other := filepath.Join(filepath.Dir(p.root), "web")
	a5GitRepo(t, other)
	op, err := s.projects.add(other, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, perr := s.runCreate(workerB, proto.RunCreateParams{Name: "D", ProjectID: op.id}); perr == nil || perr.Code != proto.ErrOutOfScope {
		t.Fatalf("a run in another project: %v", perr)
	}
}

func mustCaller(t *testing.T, s *Server, c *client) scopeCaller {
	t.Helper()
	caller, ok := s.scopedCaller(c)
	if !ok {
		t.Fatalf("pane %s is not a scoped agent", c.pane)
	}
	return caller
}

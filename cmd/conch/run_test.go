package main

import (
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

// fakeRuns is a server that keeps one run, as the real one would answer.
type fakeRuns struct {
	mu  sync.Mutex
	run proto.Run
	// answerAfter answers a question asked this many gets later.
	gets int
}

func (f *fakeRuns) handle(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch msg.Method {
	case proto.MethodProjectAdd:
		return proto.ProjectInfo{ID: "proj1", Name: "api", Git: true}, nil
	case proto.MethodRunCreate:
		var p proto.RunCreateParams
		json.Unmarshal(msg.Params, &p)
		f.run = proto.Run{ID: "r1", Name: p.Name, ProjectID: p.ProjectID, Manual: p.Manual, Tasks: []proto.RunTask{}}
		for _, sp := range p.Tasks {
			f.run.Tasks = append(f.run.Tasks, proto.RunTask{RunTaskSpec: sp, Status: proto.RunReady})
		}
		return f.run, nil
	case proto.MethodRunAdd:
		var p proto.RunAddParams
		json.Unmarshal(msg.Params, &p)
		for _, sp := range p.Tasks {
			if slices.Contains(sp.After, "nope") {
				return nil, proto.Errorf(proto.ErrBadRequest, "task %s waits for nope, which run %s has no task of", sp.ID, f.run.Name)
			}
			st := proto.RunTask{RunTaskSpec: sp, Status: proto.RunReady}
			if len(sp.After) > 0 {
				st.Status, st.Why = proto.RunPending, "waiting for "+strings.Join(sp.After, ", ")
			}
			f.run.Tasks = append(f.run.Tasks, st)
		}
		return f.run, nil
	case proto.MethodRunGet, proto.MethodRunStart, proto.MethodRunApprove, proto.MethodRunAnswer:
		f.gets++
		if msg.Method == proto.MethodRunAnswer {
			f.run.Questions[0].Answer, f.run.Questions[0].Answered = "spaces", true
		}
		// The coordinator answers the second time the asker looks.
		if len(f.run.Questions) > 0 && f.run.Questions[0].Open() && f.gets >= 2 && f.run.Questions[0].Default == "" {
			f.run.Questions[0].Answer, f.run.Questions[0].Answered = "tabs", true
		}
		if len(f.run.Questions) > 0 && f.run.Questions[0].Open() && f.run.Questions[0].Default != "" {
			f.run.Questions[0].Answer, f.run.Questions[0].TimedOut = f.run.Questions[0].Default, true
		}
		return f.run, nil
	case proto.MethodRunList:
		return proto.RunList{Runs: []proto.Run{f.run}}, nil
	case proto.MethodRunReport:
		var p proto.RunReportParams
		json.Unmarshal(msg.Params, &p)
		if f.run.Tasks[0].Status == proto.RunDone {
			return nil, proto.Errorf(proto.ErrBadRequest, "task a of run %s has already reported done; a task reports once", f.run.Name)
		}
		f.run.Tasks[0].Status, f.run.Tasks[0].Result = p.Status, p.Result
		return f.run, nil
	case proto.MethodRunAsk:
		var p proto.RunAskParams
		json.Unmarshal(msg.Params, &p)
		q := proto.RunQuestion{ID: "q1", Task: "a", Text: p.Text, Options: p.Options, Default: p.Default}
		f.run.Questions = []proto.RunQuestion{q}
		f.gets = 0
		return proto.RunAskResult{Run: "r1", Question: q}, nil
	case proto.MethodRunRemove:
		return nil, nil
	}
	return nil, proto.Errorf(proto.ErrUnknown, "?")
}

func TestRunCommands(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	f := &fakeRuns{}
	srv.setHandle(f.handle)
	run := func(args ...string) (string, error) {
		var err error
		out, _ := a4Capture(t, "", func() { err = runRun(args) })
		return out, err
	}

	out, err := run("new", "-manual", "checkout")
	if err != nil || !strings.Contains(out, "r1: run checkout in api") {
		t.Fatalf("new: %q %v", out, err)
	}
	var cp proto.RunCreateParams
	srv.params(t, proto.MethodRunCreate, &cp)
	if cp.Name != "checkout" || cp.ProjectID != "proj1" || !cp.Manual {
		t.Fatalf("create %+v", cp)
	}
	if out, err = run("add", "-id", "a", "checkout", "add", "the", "model"); err != nil || !strings.Contains(out, "a: ready") {
		t.Fatalf("add: %q %v", out, err)
	}
	if out, err = run("add", "-id", "b", "-after", "a, ,a2", "-gate", "ship it?", "checkout", "the api"); err != nil || !strings.Contains(out, "b: pending (waiting for a, a2)") {
		t.Fatalf("add after: %q %v", out, err)
	}
	var ap proto.RunAddParams
	srv.params(t, proto.MethodRunAdd, &ap)
	if sp := ap.Tasks[0]; ap.Run != "checkout" || sp.Spec != "the api" || !slices.Equal(sp.After, []string{"a", "a2"}) || sp.Gate != "ship it?" {
		t.Fatalf("add params %+v", ap)
	}
	if _, err = run("add", "-after", "nope", "checkout", "x"); err == nil || !strings.Contains(err.Error(), "no task of") {
		t.Fatalf("add after nope: %v", err)
	}
	if out, err = run("show", "checkout"); err != nil || !strings.Contains(out, "run checkout (r1), tasks start when asked") || !strings.Contains(out, "waiting for a, a2") {
		t.Fatalf("show: %q %v", out, err)
	}
	if out, err = run("show", "-json", "checkout"); err != nil || !strings.Contains(out, `"name": "checkout"`) {
		t.Fatalf("show -json: %q %v", out, err)
	}
	if out, err = run("ls"); err != nil || !strings.Contains(out, "1 ready, 1 pending") {
		t.Fatalf("ls: %q %v", out, err)
	}

	// A worker reports once; the second is the server's refusal.
	if out, err = run("done", "the", "model", "is", "in"); err != nil || !strings.Contains(out, "reported done") {
		t.Fatalf("done: %q %v", out, err)
	}
	var rp proto.RunReportParams
	srv.params(t, proto.MethodRunReport, &rp)
	if rp.Status != proto.RunDone || rp.Result != "the model is in" || rp.Run != "" || rp.Task != "" {
		t.Fatalf("report %+v", rp)
	}
	if _, err = run("done", "again"); err == nil || !strings.Contains(err.Error(), "reports once") {
		t.Fatalf("done twice: %v", err)
	}
	if _, err = run("fail"); err == nil {
		t.Fatal("a report with nothing to say")
	}

	// ask waits for the answer and prints it.
	if out, err = run("ask", "-option", "tabs", "-option", "spaces", "tabs", "or", "spaces?"); err != nil || strings.TrimSpace(out) != "tabs" {
		t.Fatalf("ask: %q %v", out, err)
	}
	var qp proto.RunAskParams
	srv.params(t, proto.MethodRunAsk, &qp)
	if !slices.Equal(qp.Options, []string{"tabs", "spaces"}) || qp.Text != "tabs or spaces?" || qp.TimeoutSecs != 0 {
		t.Fatalf("ask params %+v", qp)
	}
	// One that times out goes on with its default.
	if out, err = run("ask", "-default", "yes", "-timeout", "1500ms", "go on?"); err != nil || strings.TrimSpace(out) != "yes" {
		t.Fatalf("ask with a default: %q %v", out, err)
	}
	srv.params(t, proto.MethodRunAsk, &qp)
	if qp.TimeoutSecs != 2 {
		t.Fatalf("timeout rounded to %d", qp.TimeoutSecs)
	}
	if _, err = run("ask", "-timeout", "-1s", "x"); err == nil {
		t.Fatal("a negative timeout")
	}

	if out, err = run("answer", "checkout", "q1", "spaces"); err != nil || !strings.Contains(out, "answered q1") {
		t.Fatalf("answer: %q %v", out, err)
	}
	if out, err = run("approve", "checkout", "b"); err != nil || !strings.Contains(out, "approved b of checkout") {
		t.Fatalf("approve: %q %v", out, err)
	}
	if out, err = run("deny", "checkout", "b"); err != nil || !strings.Contains(out, "turned down b") {
		t.Fatalf("deny: %q %v", out, err)
	}
	var appr proto.RunApproveParams
	srv.params(t, proto.MethodRunApprove, &appr)
	if !appr.Deny || appr.Task != "b" {
		t.Fatalf("deny params %+v", appr)
	}
	if _, err = run("start", "checkout", "b"); err != nil {
		t.Fatal(err)
	}
	if _, err = run("rm", "checkout"); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"bogus"}, {"answer", "checkout", "q1"}, {"approve", "checkout"}, {"start"}, {"show"}, {"new"}, {"add", "checkout"}, {"wait"}, {"rm"}, {"ls", "x"}} {
		if _, err := run(args...); err == nil {
			t.Errorf("run %v was taken", args)
		}
	}

	// A server from before runs is named, with what to do.
	srv.setHello(func(int) proto.HelloResult {
		h := currentHello(0)
		h.Capabilities = slices.DeleteFunc(slices.Clone(h.Capabilities), func(c string) bool { return c == proto.CapRuns })
		return h
	})
	if _, err := run("ls"); err == nil || !strings.Contains(err.Error(), "predates runs") {
		t.Fatalf("old server: %v", err)
	}
	if r := a4MCPOne(t, "run_status", nil); !r.Result.IsError {
		t.Fatal("run_status on an old server")
	}
}

func TestRunNeeds(t *testing.T) {
	task := func(id, status, why string) proto.RunTask {
		return proto.RunTask{RunTaskSpec: proto.RunTaskSpec{ID: id, Spec: "x"}, Status: status, Why: why}
	}
	gate := task("g", proto.RunBlocked, "waiting for a person to approve: ok?")
	gate.Gate = "ok?"
	for _, tc := range []struct {
		name string
		run  proto.Run
		want string // "" waits on
	}{
		{"empty", proto.Run{Name: "j"}, ""},
		{"running", proto.Run{Name: "j", Tasks: []proto.RunTask{task("a", proto.RunRunning, ""), task("b", proto.RunPending, "")}}, ""},
		{"over", proto.Run{Name: "j", Tasks: []proto.RunTask{task("a", proto.RunDone, "")}}, "over: every task is done"},
		{"over with failures", proto.Run{Name: "j", Tasks: []proto.RunTask{task("a", proto.RunDone, ""), task("b", proto.RunFailed, "")}}, "1 of 2 tasks failed"},
		{"question", proto.Run{Name: "j", Tasks: []proto.RunTask{task("a", proto.RunRunning, "")}, Questions: []proto.RunQuestion{{ID: "q1", Task: "a", Text: "which?"}}}, "question q1 from a: which?"},
		{"answered question", proto.Run{Name: "j", Tasks: []proto.RunTask{task("a", proto.RunRunning, "")}, Questions: []proto.RunQuestion{{ID: "q1", Answered: true}}}, ""},
		{"gate waits for the person", proto.Run{Name: "j", Tasks: []proto.RunTask{task("a", proto.RunDone, ""), gate}}, ""},
		{"stuck on a failure", proto.Run{Name: "j", Tasks: []proto.RunTask{task("a", proto.RunFailed, ""), task("b", proto.RunBlocked, "a failed")}}, "stuck: nothing left can go on by itself — b (a failed)"},
		{"manual and ready", proto.Run{Name: "j", Manual: true, Tasks: []proto.RunTask{task("a", proto.RunReady, "")}}, "conch run start j a"},
	} {
		why, ok := runNeeds(tc.run)
		if ok != (tc.want != "") || !strings.Contains(why, tc.want) {
			t.Errorf("%s: %q %v, want %q", tc.name, why, ok, tc.want)
		}
	}
}

// conch run wait returns once the server says the run is over, and gives
// up at its timeout otherwise.
func TestRunWait(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	var mu sync.Mutex
	gets := 0
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		mu.Lock()
		defer mu.Unlock()
		gets++
		st := proto.RunRunning
		if gets > 1 {
			st = proto.RunDone
		}
		return proto.Run{ID: "r1", Name: "j", Tasks: []proto.RunTask{{RunTaskSpec: proto.RunTaskSpec{ID: "a"}, Status: st}}}, nil
	})
	var err error
	out, _ := a4Capture(t, "", func() { err = runRun([]string{"wait", "j"}) })
	if err != nil || !strings.Contains(out, "over: every task is done") {
		t.Fatalf("wait: %q %v", out, err)
	}
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		return proto.Run{ID: "r1", Name: "j", Tasks: []proto.RunTask{{RunTaskSpec: proto.RunTaskSpec{ID: "a"}, Status: proto.RunRunning}}}, nil
	})
	start := time.Now()
	a4Capture(t, "", func() { err = runRun([]string{"wait", "-timeout", "300ms", "j"}) })
	if err == nil || !strings.Contains(err.Error(), "timed out") || time.Since(start) > 5*time.Second {
		t.Fatalf("wait with a timeout: %v after %s", err, time.Since(start))
	}
}

// The run tools call the methods the commands call, with the same payload.
func TestMCPRunTools(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	f := &fakeRuns{}
	srv.setHandle(f.handle)
	r := a4MCPOne(t, "run_create", map[string]any{"name": "checkout", "tasks": []map[string]any{
		{"id": "a", "spec": "the model"}, {"id": "b", "spec": "the api", "after": []string{"a"}, "gate": "ok?"},
	}})
	if r.Result.IsError {
		t.Fatalf("run_create: %+v", r)
	}
	var cp proto.RunCreateParams
	srv.params(t, proto.MethodRunCreate, &cp)
	if cp.Name != "checkout" || len(cp.Tasks) != 2 || cp.Tasks[1].Gate != "ok?" || !slices.Equal(cp.Tasks[1].After, []string{"a"}) {
		t.Fatalf("create %+v", cp)
	}
	if r := a4MCPOne(t, "run_create", map[string]any{"name": "x", "tasks": []map[string]any{{"spec": "y", "colour": "red"}}}); !r.Result.IsError {
		t.Fatal("a field nobody knows was ignored")
	}
	if r := a4MCPOne(t, "run_status", map[string]any{"run": "checkout"}); r.Result.IsError {
		t.Fatalf("run_status: %+v", r)
	}
	if r := a4MCPOne(t, "run_status", nil); r.Result.IsError {
		t.Fatalf("run_status for all: %+v", r)
	}
	if r := a4MCPOne(t, "run_report", map[string]any{"status": "done", "result": "in"}); r.Result.IsError {
		t.Fatalf("run_report: %+v", r)
	}
	var rp proto.RunReportParams
	srv.params(t, proto.MethodRunReport, &rp)
	if rp.Status != "done" || rp.Result != "in" {
		t.Fatalf("report %+v", rp)
	}
	if r := a4MCPOne(t, "run_report", map[string]any{"status": "done", "result": " "}); !r.Result.IsError {
		t.Fatal("a report with no result")
	}
	if r := a4MCPOne(t, "run_ask", map[string]any{"question": "tabs?", "options": []string{"tabs", "spaces"}}); r.Result.IsError {
		t.Fatalf("run_ask: %+v", r)
	}
	if r := a4MCPOne(t, "run_ask", map[string]any{"question": "x", "timeout_seconds": -1}); !r.Result.IsError {
		t.Fatal("a negative timeout")
	}
	if r := a4MCPOne(t, "run_answer", map[string]any{"run": "checkout", "question": "q1", "answer": "spaces"}); r.Result.IsError {
		t.Fatalf("run_answer: %+v", r)
	}
	if r := a4MCPOne(t, "run_add", map[string]any{"run": "checkout", "tasks": []map[string]any{{"id": "c", "spec": "z"}}}); r.Result.IsError {
		t.Fatalf("run_add: %+v", r)
	}
	if r := a4MCPOne(t, "run_start", map[string]any{"run": "checkout"}); r.Result.IsError {
		t.Fatalf("run_start: %+v", r)
	}
	for _, m := range []string{proto.MethodRunCreate, proto.MethodRunGet, proto.MethodRunList, proto.MethodRunReport, proto.MethodRunAsk, proto.MethodRunAnswer, proto.MethodRunAdd, proto.MethodRunStart} {
		if !slices.Contains(srv.methods(), m) {
			t.Errorf("no tool called %s", m)
		}
	}
}

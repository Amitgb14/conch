package pane

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// A report conch cannot trust is dropped whole rather than half-kept: a
// report with one bad field is a wrong report, not a partial one.
func TestParseStatus(t *testing.T) {
	for _, c := range []struct {
		name string
		body string
		want ProgramStatus
		ok   bool
	}{
		{"the simplest report", "state=idle", ProgramStatus{State: "idle", Progress: -1}, true},
		{"every key", "state=blocked:kind=permission:app=claude-code:progress=40:id=build/test:msg=" + b64("Allow npm install?"),
			ProgramStatus{State: "blocked", Kind: "permission", App: "claude-code", Progress: 40, ID: "build/test", Msg: "Allow npm install?"}, true},
		{"a title", "state=working:title=" + b64("go test"), ProgramStatus{State: "working", Title: "go test", Progress: -1}, true},
		{"base64 without padding", "state=done:msg=" + strings.TrimRight(b64("hi"), "="),
			ProgramStatus{State: "done", Msg: "hi", Progress: -1}, true},
		{"a malformed pair is skipped", "state=working:nonsense:app=x", ProgramStatus{State: "working", App: "x", Progress: -1}, true},
		{"an unknown key is ignored", "state=working:colour=red", ProgramStatus{State: "working", Progress: -1}, true},
		{"the last value of a repeated key wins", "state=idle:app=a:app=b", ProgramStatus{State: "idle", App: "b", Progress: -1}, true},
		{"clear is not a state but is taken", "state=clear:id=build", ProgramStatus{State: "clear", ID: "build", Progress: -1}, true},
		{"a kind conch does not know is dropped, the report is not", "state=blocked:kind=vibes",
			ProgramStatus{State: "blocked", Progress: -1}, true},

		{"no state at all", "app=x", ProgramStatus{}, false},
		{"a state conch does not know", "state=thinking", ProgramStatus{}, false},
		{"empty", "", ProgramStatus{}, false},
		{"progress past the end", "state=working:progress=101", ProgramStatus{}, false},
		{"progress that is not a number", "state=working:progress=lots", ProgramStatus{}, false},
		{"an id with a bad character", "state=idle:id=build/my test", ProgramStatus{}, false},
		{"dots are ordinary id characters, not a path", "state=idle:id=build/../etc",
			ProgramStatus{State: "idle", ID: "build/../etc", Progress: -1}, true},
		{"an id nine deep", "state=idle:id=a/b/c/d/e/f/g/h/i", ProgramStatus{}, false},
		{"an id with an empty segment", "state=idle:id=build//test", ProgramStatus{}, false},
		{"an id past the byte limit", "state=idle:id=" + strings.Repeat("ab/", 50), ProgramStatus{}, false},
		{"an app with a space", "state=idle:app=my agent", ProgramStatus{}, false},
		{"an app past the limit", "state=idle:app=" + strings.Repeat("a", 33), ProgramStatus{}, false},
		{"a msg that is not base64", "state=idle:msg=not base64!", ProgramStatus{}, false},
		{"a msg with a control character in it", "state=idle:msg=" + b64("one\x1b[31mred"), ProgramStatus{}, false},
		{"a msg with a newline in it", "state=idle:msg=" + b64("two\nlines"), ProgramStatus{}, false},
		{"a msg past the limit", "state=idle:msg=" + strings.Repeat("A", 1100), ProgramStatus{}, false},
	} {
		got, ok := parseStatus(c.body)
		if ok != c.ok {
			t.Errorf("%s: ok=%v, wanted %v (%+v)", c.name, ok, c.ok, got)
			continue
		}
		if ok && got != c.want {
			t.Errorf("%s:\n got %+v\nwant %+v", c.name, got, c.want)
		}
	}
	// An empty id is the root record, which is the usual case.
	if rec, ok := parseStatus("state=working:id="); !ok || rec.ID != "" {
		t.Errorf("an empty id: %+v %v", rec, ok)
	}
}

// The query a program opens with, and what conch answers.
func TestStatusQuery(t *testing.T) {
	if !statusQuery("?") {
		t.Error("the query was not recognised")
	}
	for _, body := range []string{"", "state=idle", "??", " ?"} {
		if statusQuery(body) {
			t.Errorf("%q was taken for the query", body)
		}
	}
	if statusReply != "\x1b]7501;?\x1b\\" {
		t.Errorf("the answer is %q, which is not the query's own body back", statusReply)
	}
}

func apply(t *testing.T, r *statusRecords, body string) {
	t.Helper()
	rec, ok := parseStatus(body)
	if !ok {
		t.Fatalf("parse %q", body)
	}
	r.apply(rec, time.Now())
}

// A report replaces its record whole, a clear takes the children with it,
// and a child without an app belongs to its parent's.
func TestStatusRecords(t *testing.T) {
	var r statusRecords
	if _, ok := r.current(); ok {
		t.Error("a program that said nothing has a state")
	}
	apply(t, &r, "state=working:app=make:msg="+b64("compiling"))
	// The root is an ancestor of everything, so a program names itself
	// once and its children are known by it.
	apply(t, &r, "state=working:id=build/test")
	if got := r.byID["build/test"].App; got != "make" {
		t.Errorf("a child did not take the root's app: %q", got)
	}
	apply(t, &r, "state=working:id=build:app=cargo")
	apply(t, &r, "state=working:id=build/docs")
	if got := r.byID["build/docs"].App; got != "cargo" {
		t.Errorf("a child did not take its parent's app: %q", got)
	}
	// Replacing a record drops what the new report left out.
	apply(t, &r, "state=idle")
	if rec := r.byID[""]; rec.Msg != "" || rec.App != "" {
		t.Errorf("a replaced record kept old keys: %+v", rec)
	}
	// Clearing a record takes its children.
	apply(t, &r, "state=clear:id=build")
	for _, id := range []string{"build", "build/test", "build/docs"} {
		if _, still := r.byID[id]; still {
			t.Errorf("%s survived the clear", id)
		}
	}
	if _, ok := r.byID[""]; !ok {
		t.Error("clearing a child removed the root")
	}
	// Clearing with no id removes everything.
	apply(t, &r, "state=clear")
	if len(r.byID) != 0 || len(r.order) != 0 {
		t.Errorf("a full clear left %v", r.byID)
	}
	if _, ok := r.current(); ok {
		t.Error("a cleared program still has a state")
	}
}

// What a program is doing, for a reader that wants one answer: anything
// blocked first, then the program itself, then the newest.
func TestStatusCurrent(t *testing.T) {
	var r statusRecords
	apply(t, &r, "state=working")
	apply(t, &r, "state=idle:id=lint")
	if got, _ := r.current(); got.ID != "" || got.State != "working" {
		t.Errorf("the root should speak for the program: %+v", got)
	}
	apply(t, &r, "state=blocked:id=lint:kind=question")
	if got, _ := r.current(); got.ID != "lint" || got.State != "blocked" {
		t.Errorf("a blocked child did not come first: %+v", got)
	}
	// With no root at all, the newest record speaks.
	var child statusRecords
	apply(t, &child, "state=idle:id=a")
	apply(t, &child, "state=working:id=b")
	if got, _ := child.current(); got.ID != "b" {
		t.Errorf("without a root, the newest should speak: %+v", got)
	}
}

// Work that is no longer going on, and a question nobody can answer, do
// not outlive the command. What it finished or failed at does.
func TestStatusSettle(t *testing.T) {
	var r statusRecords
	apply(t, &r, "state=working:id=build")
	apply(t, &r, "state=blocked:id=ask")
	apply(t, &r, "state=done:id=fmt")
	apply(t, &r, "state=error:id=vet")
	apply(t, &r, "state=idle")
	r.settle()
	for _, id := range []string{"build", "ask"} {
		if _, still := r.byID[id]; still {
			t.Errorf("%s outlived the command", id)
		}
	}
	for _, id := range []string{"fmt", "vet", ""} {
		if _, kept := r.byID[id]; !kept {
			t.Errorf("%s was thrown away, and nobody has seen it", id)
		}
	}
	if len(r.order) != len(r.byID) {
		t.Errorf("order and records disagree: %v vs %v", r.order, r.byID)
	}
}

// A program cannot grow conch's memory by reporting: the oldest record
// goes when the cap is reached.
func TestStatusRecordCap(t *testing.T) {
	var r statusRecords
	for i := range maxRecords + 20 {
		apply(t, &r, "state=working:id=r"+itoa(i))
	}
	if len(r.byID) != maxRecords || len(r.order) != maxRecords {
		t.Fatalf("kept %d records (order %d), cap is %d", len(r.byID), len(r.order), maxRecords)
	}
	if _, still := r.byID["r0"]; still {
		t.Error("the oldest record was kept")
	}
	if _, kept := r.byID["r"+itoa(maxRecords+19)]; !kept {
		t.Error("the newest record was dropped")
	}
	// Updating a record makes it the newest, so it is not the next to go.
	apply(t, &r, "state=idle:id=r"+itoa(maxRecords+5))
	for i := range 10 {
		apply(t, &r, "state=working:id=new"+itoa(i))
	}
	if _, kept := r.byID["r"+itoa(maxRecords+5)]; !kept {
		t.Error("a record updated just now was dropped as the oldest")
	}
}

// A reload carries the records: a program reports when its state changes,
// so one that is still blocked will not say so again for the new server.
func TestStatusRestore(t *testing.T) {
	var r statusRecords
	apply(t, &r, "state=blocked:kind=permission:app=claude-code")
	apply(t, &r, "state=working:id=sub")
	var back statusRecords
	back.restore(r.all())
	got, ok := back.current()
	if !ok || got.State != "blocked" || got.Kind != "permission" || got.App != "claude-code" {
		t.Errorf("after a reload: %+v %v", got, ok)
	}
	if len(back.all()) != 2 {
		t.Errorf("records lost in the reload: %+v", back.all())
	}
	// A snapshot from an older server carries none, and one with a record
	// that has no state is not kept as an empty one.
	var empty statusRecords
	empty.restore(nil)
	empty.restore([]ProgramStatus{{ID: "x"}})
	if _, ok := empty.current(); ok {
		t.Error("an empty snapshot gave a state")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// The whole loop against a real program on a real pty: it asks whether
// conch speaks the protocol, conch answers down the terminal, and the
// program — having been answered — reports. A program that is not
// answered says nothing, which is why the answer is the part that makes
// the rest of this work.
func TestProgramStatusThroughAPty(t *testing.T) {
	const script = `stty -icanon -echo min 0 time 30 2>/dev/null
printf '\033]7501;?\033\\'
reply=$(dd bs=1 count=10 2>/dev/null | od -An -tx1 | tr -d ' \n')
echo "REPLY:$reply"
printf '\033]7501;state=working:app=standin:msg=%s\033\\' "$(printf 'building the thing' | base64 | tr -d '\n')"
echo READY
sleep 30`
	p, err := Start(Options{ID: "t-status", Command: []string{"/bin/sh", "-c", script}, Cols: 80, Rows: 12})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)

	// ESC ] 7 5 0 1 ; ? ESC \ — the query's own body, back.
	waitScreen(t, p, "REPLY:1b5d373530313b3f1b5c")
	waitScreen(t, p, "READY")

	deadline := time.Now().Add(5 * time.Second)
	var rec ProgramStatus
	for time.Now().Before(deadline) {
		var ok bool
		if rec, ok = p.ProgramStatus(); ok {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if rec.State != "working" || rec.App != "standin" || rec.Msg != "building the thing" {
		t.Fatalf("what the program said never arrived: %+v", rec)
	}
	if rec.At.IsZero() {
		t.Error("the report has no time on it")
	}

	// Nothing of it reached the screen: a report is not output.
	if screen := strings.Join(p.PlainLines(), "\n"); strings.Contains(screen, "7501") ||
		strings.Contains(screen, "state=working") {
		t.Errorf("the report was drawn:\n%s", screen)
	}
}

// Work that was going on does not outlive the program that reported it,
// but what it finished does — that is the thing nobody has seen.
func TestProgramStatusAfterTheProgramExits(t *testing.T) {
	const script = `printf '\033]7501;state=working:app=standin\033\\'
printf '\033]7501;state=blocked:id=ask:kind=question\033\\'
printf '\033]7501;state=done:id=fmt\033\\'
echo REPORTED`
	p, err := Start(Options{ID: "t-exit", Command: []string{"/bin/sh", "-c", script}, Cols: 80, Rows: 12})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	waitScreen(t, p, "REPORTED")

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if p.Info().State == proto.PaneExited {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := p.Info().State; got != proto.PaneExited {
		t.Fatalf("the program is %q, not exited", got)
	}
	ids := map[string]string{}
	for _, rec := range p.ProgramStatusAll() {
		ids[rec.ID] = rec.State
	}
	if _, still := ids[""]; still {
		t.Errorf("work outlived the program: %v", ids)
	}
	if _, still := ids["ask"]; still {
		t.Errorf("a question nobody can answer outlived the program: %v", ids)
	}
	if ids["fmt"] != "done" {
		t.Errorf("what it finished was thrown away: %v", ids)
	}
}

// The other moment work stops being work: the shell draws its next
// prompt (OSC 133 A), so the command that was running has finished even
// though the pane's program — the shell — is still there.
func TestProgramStatusClearedAtTheNextPrompt(t *testing.T) {
	// It waits to be told before marking the prompt, so the window where
	// all three records exist is not a race with the test.
	const script = `printf '\033]7501;state=working:app=make\033\\'
printf '\033]7501;state=blocked:id=ask:kind=question\033\\'
printf '\033]7501;state=done:id=built\033\\'
echo REPORTED
read go
printf '\033]133;A\033\\'
echo PROMPTED
sleep 30`
	p, err := Start(Options{ID: "t-prompt", Command: []string{"/bin/sh", "-c", script}, Cols: 80, Rows: 12})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	waitScreen(t, p, "REPORTED")

	// Everything is there until the prompt marker arrives.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(p.ProgramStatusAll()) < 3 {
		time.Sleep(20 * time.Millisecond)
	}
	if got := len(p.ProgramStatusAll()); got != 3 {
		t.Fatalf("before the prompt: %d records, %+v", got, p.ProgramStatusAll())
	}
	if err := p.SendText("go\n", false); err != nil {
		t.Fatal(err)
	}
	waitScreen(t, p, "PROMPTED")

	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(p.ProgramStatusAll()) == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	left := map[string]string{}
	for _, rec := range p.ProgramStatusAll() {
		left[rec.ID] = rec.State
	}
	if len(left) != 1 || left["built"] != "done" {
		t.Fatalf("after the prompt: %v — working and blocked go, done stays", left)
	}
	// The program is still running: this is the prompt rule, not the exit one.
	if got := p.Info().State; got != proto.PaneRunning {
		t.Fatalf("the program is %q", got)
	}
}

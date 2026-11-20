package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func readProgress(t *testing.T, path string) []progressEvent {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []progressEvent
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e progressEvent
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("not a JSON line: %q (%v)", sc.Text(), err)
		}
		if e.At <= 0 {
			t.Fatalf("no time: %q", sc.Text())
		}
		out = append(out, e)
	}
	return out
}

// marks is the steps' starts, dones and fails in order, and the end.
func marks(es []progressEvent) string {
	var b []string
	for _, e := range es {
		switch {
		case e.End != "":
			b = append(b, "end:"+e.End)
		case e.State != "":
			b = append(b, e.Step+":"+e.State)
		}
	}
	return strings.Join(b, " ")
}

func TestA4SandboxCreateWritesItsSteps(t *testing.T) {
	a4Env(t)
	newA4Daytona(t)
	a4SandboxMachine(t)
	path := filepath.Join(t.TempDir(), "steps.jsonl")

	var err error
	_, errOut := a4Capture(t, "", func() {
		err = runSandbox([]string{"-provider", "daytona", "create", "-label", "fix-login", "-progress", path})
	})
	if err != nil {
		t.Fatalf("create: %v\n%s", err, errOut)
	}
	es := readProgress(t, path)
	want := "create:start create:done answer:start answer:done install:start install:done check:start check:done add:done end:ready"
	if got := marks(es); got != want {
		t.Fatalf("steps\n got %s\nwant %s", got, want)
	}
	var sandbox, machine string
	for _, e := range es {
		if e.Sandbox != "" {
			sandbox = e.Sandbox
		}
		if e.End == "ready" {
			machine = e.Machine + "/" + e.Label
		}
	}
	if sandbox != "sbx1-0123456789" || machine != "fix-login/fix-login" {
		t.Fatalf("sandbox %q machine %q", sandbox, machine)
	}
	// The lines are stderr's, under the step they belong to, and never the token.
	var copying bool
	for _, e := range es {
		if strings.HasPrefix(e.Line, "copying conch") {
			copying = e.Step == stepInstall
		}
	}
	b, _ := os.ReadFile(path)
	if !copying || strings.Contains(string(b), "tok-secret") {
		t.Fatalf("copying under install %v, or the token written:\n%s", copying, b)
	}
	// stderr is as it was.
	if !strings.Contains(errOut, "Creating a Daytona sandbox…") || !strings.Contains(errOut, "probing fix-login…") {
		t.Fatalf("stderr:\n%s", errOut)
	}
}

func TestA4SandboxCreateKeepsAFailureForTheApp(t *testing.T) {
	a4Env(t)
	d := newA4Daytona(t)
	a4SandboxMachine(t)
	t.Setenv("A4_PROBE_FAIL", "tok-secret@gw.test: Permission denied (publickey).")
	path := filepath.Join(t.TempDir(), "steps.jsonl")

	var err error
	// Nothing on stdin: -keep must not ask. ssh is refused while it waits
	// for the sandbox to answer, so that is the step that failed.
	_, errOut := a4Capture(t, "", func() {
		err = runSandbox([]string{"-provider", "daytona", "create", "-keep", "-progress", path})
	})
	if err == nil || strings.Contains(errOut, "[Y/n]") || d.state("sbx1-0123456789") != "started" {
		t.Fatalf("-keep: %v (%s)\n%s", err, d.state("sbx1-0123456789"), errOut)
	}
	es := readProgress(t, path)
	last := es[len(es)-1]
	if last.End != "failed" || last.Kept == nil || !*last.Kept || !strings.Contains(last.Error, "Permission denied") || strings.Contains(last.Error, "tok-secret") {
		t.Fatalf("end %+v", last)
	}
	if got := marks(es); !strings.HasSuffix(got, "answer:fail end:failed") {
		t.Fatalf("steps %s", got)
	}

	// -yes deletes, and says so.
	path2 := filepath.Join(t.TempDir(), "steps.jsonl")
	a4Capture(t, "", func() {
		err = runSandbox([]string{"-provider", "daytona", "create", "-yes", "-progress", path2})
	})
	es = readProgress(t, path2)
	if last := es[len(es)-1]; err == nil || last.Kept == nil || *last.Kept || d.state("sbx2-0123456789") != "" {
		t.Fatalf("-yes: %v %+v", err, last)
	}

	// Both is a mistake, refused before anything is made.
	n := len(d.created)
	a4Capture(t, "", func() { err = runSandbox([]string{"-provider", "daytona", "create", "-yes", "-keep"}) })
	if err == nil || !strings.Contains(err.Error(), "not both") || len(d.created) != n {
		t.Fatalf("-yes -keep: %v", err)
	}
}

func TestA4SandboxCreateProgressIntoNowhere(t *testing.T) {
	a4Env(t)
	d := newA4Daytona(t)
	var err error
	a4Capture(t, "", func() {
		err = runSandbox([]string{"-provider", "daytona", "create", "-progress", filepath.Join(t.TempDir(), "no", "such", "dir", "f")})
	})
	if err == nil || len(d.created) != 0 {
		t.Fatalf("a progress file that can't be made should stop it before spending: %v, %d made", err, len(d.created))
	}
}

func TestA4SandboxAddWritesItsSteps(t *testing.T) {
	a4Env(t)
	d := newA4Daytona(t)
	a4SandboxMachine(t)
	d.set("sb-mine", "stopped")
	path := filepath.Join(t.TempDir(), "steps.jsonl")
	var err error
	_, errOut := a4Capture(t, "", func() {
		err = runSandbox([]string{"-provider", "daytona", "add", "-label", "mine", "-progress", path, "sb-mine"})
	})
	if err != nil {
		t.Fatalf("add: %v\n%s", err, errOut)
	}
	if got := marks(readProgress(t, path)); !strings.HasPrefix(got, "create:start create:done answer:start") || !strings.HasSuffix(got, "add:done end:ready") {
		t.Fatalf("steps %s", got)
	}
	// One it can't add fails on its first step, the sandbox left as it was.
	path2 := filepath.Join(t.TempDir(), "steps.jsonl")
	a4Capture(t, "", func() {
		err = runSandbox([]string{"-provider", "daytona", "add", "-progress", path2, "sb-nowhere"})
	})
	es := readProgress(t, path2)
	if last := es[len(es)-1]; err == nil || last.End != "failed" || last.Kept == nil || !*last.Kept {
		t.Fatalf("add of an unknown one: %v %+v", err, es)
	}
}

func TestStepOf(t *testing.T) {
	for _, c := range []struct{ line, current, want string }{
		{"Creating a Daytona sandbox", "", stepCreate},
		{"waiting for sandbox-m_agq7ts to answer", stepCreate, stepAnswer},
		{"probing sandbox-m_agq7ts", stepAnswer, stepInstall},
		{"conch is not installed on sandbox-m_agq7ts (linux/amd64); installing", stepInstall, stepInstall},
		{"conch on x is from an older build (lacks a.v1); installing", stepAnswer, stepInstall},
		{"building conch for linux/amd64 from /src", stepInstall, stepInstall},
		{"copying conch to x (29 MB) · attempt 2", stepInstall, stepInstall},
		{"downloading conch 0.2.2 for linux/amd64", stepInstall, stepInstall},
		{"connecting", stepInstall, stepCheck},
		{"something new a later conch says", stepInstall, stepInstall},
		{"something before any step", "", stepCreate},
	} {
		if got := stepOf(c.line, c.current); got != c.want {
			t.Errorf("stepOf(%q, %q) = %q, want %q", c.line, c.current, got, c.want)
		}
	}
}

func TestProgressLogFailedOnTheCheck(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p")
	l, err := openProgress(path)
	if err != nil {
		t.Fatal(err)
	}
	l.now = func() time.Time { return time.Unix(1, 0) }
	l.begin(stepCreate, "Creating")
	l.note("probing x")
	l.failed(errors.New("conch on x is still from an older build after installing (lacks agent.move.v1): built from /old"), true)
	l.close()
	if got := marks(readProgress(t, path)); got != "create:start create:done install:start install:done check:fail end:failed" {
		t.Fatalf("steps %s", got)
	}
	// A nil log does nothing, and no file is no log.
	var none *progressLog
	none.begin(stepCreate, "x")
	none.note("x")
	none.sandbox("x")
	none.ready("m", "l")
	none.failed(errors.New("x"), false)
	none.close()
	if l, err := openProgress(""); l != nil || err != nil {
		t.Fatalf("openProgress(\"\") = %v, %v", l, err)
	}
}

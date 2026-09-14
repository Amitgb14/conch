package ghx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestA6ListUsesGHOnPath(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(t.TempDir(), "log")
	script := "#!/bin/sh\n{ /bin/pwd; for a in \"$@\"; do printf '%s\\n' \"$a\"; done; echo \"P=$GH_PROMPT_DISABLED U=$GH_NO_UPDATE_NOTIFIER C=$NO_COLOR\"; } > " + log + "\necho '[]'\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONCH_GH", "")
	t.Setenv("PATH", dir)
	work, _ := filepath.EvalSymlinks(t.TempDir())
	prs, err := List(context.Background(), work)
	if err != nil {
		t.Fatal(err)
	}
	if prs == nil || len(prs) != 0 {
		t.Fatalf("empty list: %#v", prs)
	}
	b, _ := os.ReadFile(log)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if got, _ := filepath.EvalSymlinks(lines[0]); got != work {
		t.Fatalf("dir: %q want %q", lines[0], work)
	}
	want := []string{"pr", "list", "--state", "all", "--limit", "100", "--json", fields}
	if strings.Join(lines[1:len(lines)-1], " ") != strings.Join(want, " ") {
		t.Fatalf("args: %q", lines[1:len(lines)-1])
	}
	if lines[len(lines)-1] != "P=1 U=1 C=1" {
		t.Fatalf("env: %q", lines[len(lines)-1])
	}

	// No gh anywhere on PATH.
	t.Setenv("PATH", t.TempDir())
	if _, err := List(context.Background(), work); !errors.Is(err, ErrNoGH) {
		t.Fatalf("no gh: %v", err)
	}
}

func TestA6ListFailureModes(t *testing.T) {
	cases := []struct {
		name, script, want string
	}{
		{"silent exit", "exit 4\n", "gh pr list: exit status 4"},
		{"malformed json", "echo '[{\"number\": \"three\"}]'\n", "parse gh output"},
		{"truncated json", "printf '[{\"number\":1'\n", "parse gh output"},
		{"prose", "echo 'To get started with GitHub CLI, please run: gh auth login'\n", "parse gh output"},
		{"stderr with leading blank", "printf '\\n  auth required  \\nmore\\n' >&2; exit 1\n", "gh pr list: auth required"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fakeGH(t, c.script)
			_, err := List(context.Background(), t.TempDir())
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want %q", err, c.want)
			}
			if strings.Contains(err.Error(), "more") {
				t.Fatalf("only the first line: %v", err)
			}
		})
	}
}

func TestA6ListCanceled(t *testing.T) {
	fakeGH(t, "exec /bin/sleep 30\n")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := List(ctx, t.TempDir()); err == nil {
		t.Fatal("canceled list succeeded")
	}
	if time.Since(start) > 10*time.Second {
		t.Fatal("cancel did not stop gh")
	}
}

func TestA6ParseEdges(t *testing.T) {
	prs, err := Parse([]byte("null"))
	if err != nil || prs == nil || len(prs) != 0 {
		t.Fatalf("null: %#v %v", prs, err)
	}
	if _, err := Parse(nil); err == nil {
		t.Fatal("empty input accepted")
	}
	if _, err := Parse([]byte(`{"number":1}`)); err == nil {
		t.Fatal("object accepted as list")
	}
	prs, err = Parse([]byte(`[{"number":9,"statusCheckRollup":[
		{"__typename":"CheckRun","status":"COMPLETED","conclusion":"NEUTRAL"},
		{"status":"COMPLETED","conclusion":"SUCCESS"},
		{"__typename":"StatusContext","state":"ERROR"},
		{"__typename":"StatusContext","state":"EXPECTED"}]},
		{"number":10,"statusCheckRollup":null,"updatedAt":"2026-01-02T03:04:05Z"}]`))
	if err != nil {
		t.Fatal(err)
	}
	if p := prs[0]; p.Total != 4 || p.Passed != 2 || p.Checks != ChecksFail {
		t.Fatalf("mixed: %+v", p)
	}
	if p := prs[1]; p.Total != 0 || p.Checks != "" || !p.Updated.Equal(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Fatalf("no checks: %+v", p)
	}
	if _, err := Parse([]byte(`[{"updatedAt":"yesterday"}]`)); err == nil {
		t.Fatal("bad time accepted")
	}
}

func TestA6Outcomes(t *testing.T) {
	for _, c := range []struct{ status, conclusion, want string }{
		{"QUEUED", "", ChecksPending},
		{"IN_PROGRESS", "SUCCESS", ChecksPending},
		{"COMPLETED", "SUCCESS", ChecksPass},
		{"COMPLETED", "NEUTRAL", ChecksPass},
		{"COMPLETED", "SKIPPED", ChecksPass},
		{"COMPLETED", "FAILURE", ChecksFail},
		{"COMPLETED", "TIMED_OUT", ChecksFail},
		{"COMPLETED", "CANCELLED", ChecksFail},
		{"COMPLETED", "ACTION_REQUIRED", ChecksFail},
		{"COMPLETED", "STARTUP_FAILURE", ChecksFail},
		{"COMPLETED", "STALE", ChecksFail},
		{"COMPLETED", "", ChecksPending},
		{"COMPLETED", "WEIRD", ChecksPending},
	} {
		if got := runOutcome(c.status, c.conclusion); got != c.want {
			t.Errorf("runOutcome(%s,%s) = %s, want %s", c.status, c.conclusion, got, c.want)
		}
	}
	for state, want := range map[string]string{"SUCCESS": ChecksPass, "FAILURE": ChecksFail, "ERROR": ChecksFail, "PENDING": ChecksPending, "EXPECTED": ChecksPending, "": ChecksPending} {
		if got := contextOutcome(state); got != want {
			t.Errorf("contextOutcome(%s) = %s, want %s", state, got, want)
		}
	}
}

func TestA6ByBranch(t *testing.T) {
	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Hour)
	prs := []PR{
		{Number: 1, Branch: "a", State: "MERGED", Updated: t2},
		{Number: 2, Branch: "a", State: "CLOSED", Updated: t1}, // older, same openness: ignored
		{Number: 3, Branch: "b", State: "OPEN", Updated: t1},
		{Number: 4, Branch: "b", State: "OPEN", Updated: t2}, // newer open wins
		{Number: 5, Branch: "b", State: "CLOSED", Updated: t2.Add(time.Hour)},
		{Number: 6, Branch: "c", State: "CLOSED", Updated: t1},
		{Number: 7, Branch: "c", State: "CLOSED", Updated: t1}, // equal time: first kept
	}
	by := ByBranch(prs)
	if len(by) != 3 || by["a"].Number != 1 || by["b"].Number != 4 || by["c"].Number != 6 {
		t.Fatalf("by branch: %+v", by)
	}
	if len(ByBranch(nil)) != 0 {
		t.Fatal("nil")
	}
}

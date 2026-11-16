package server_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// fakeCIGH puts a gh on CONCH_GH that lists runs, answers each run's jobs
// (writing down which it was asked for) and records re-runs, so a test can
// see what the server asked GitHub for.
func fakeCIGH(t *testing.T, runs string) (dir string) {
	t.Helper()
	dir = t.TempDir()
	os.WriteFile(filepath.Join(dir, "runs"), []byte(runs), 0o600)
	script := `#!/bin/sh
d="` + dir + `"
case "$1 $2" in
"pr list") echo '[]' ;;
"run list")
  if [ "$3" = --branch ]; then echo '[{"databaseId": 8, "workflowName": "ci", "url": "u8", "headSha": "h", "conclusion": "failure"}]'; else cat "$d/runs"; fi ;;
"run view") echo "$3" >> "$d/viewed"
  echo '{"jobs":[{"name":"test","conclusion":"success","startedAt":"2026-11-01T00:00:00Z","completedAt":"2026-11-01T00:05:00Z"}]}' ;;
"run rerun") echo "$3 $4" >> "$d/reruns"; [ "$3" = 404 ] && { echo "run 404 not found" >&2; exit 1; } ; true ;;
*) exit 7 ;;
esac
`
	gh := filepath.Join(dir, "gh")
	os.WriteFile(gh, []byte(script), 0o755)
	t.Setenv("CONCH_GH", gh)
	return dir
}

func ciRunJSON(id int, conclusion string, ago time.Duration) string {
	created := time.Now().Add(-ago).UTC().Format(time.RFC3339)
	done := time.Now().Add(-ago + 3*time.Minute).UTC().Format(time.RFC3339)
	return fmt.Sprintf(`{"databaseId":%d,"workflowName":"ci","headBranch":"main","headSha":"s%d","event":"push","status":"completed","conclusion":%q,"attempt":1,"url":"u%d","createdAt":%q,"startedAt":%q,"updatedAt":%q}`,
		id, id, conclusion, id, created, created, done)
}

func TestCIStats(t *testing.T) {
	runs := "[" + strings.Join([]string{
		ciRunJSON(3, "success", time.Hour),
		ciRunJSON(2, "failure", 2*time.Hour),
		ciRunJSON(1, "success", 3*time.Hour),
	}, ",") + "]"
	dir := fakeCIGH(t, runs)
	c, _, proj := setupRepo(t)
	if miss := c.MissingCapabilities([]string{proto.CapCIStats, proto.CapCIRerun}); len(miss) > 0 {
		t.Fatalf("capabilities missing: %v", miss)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var s proto.CIStats
	if err := c.Call(ctx, proto.MethodCIStats, proto.CIStatsParams{ProjectID: proj.ID, Days: 7}, &s); err != nil {
		t.Fatal(err)
	}
	if s.Days != 7 || s.Runs != 3 || s.Passed != 2 || s.Failed != 1 || len(s.Daily) != 7 || s.MedianSeconds != 180 {
		t.Fatalf("stats: %+v", s)
	}
	if len(s.Jobs) != 1 || s.Jobs[0].Name != "test" || s.Jobs[0].MedianSeconds != 300 || s.JobsPending != 0 {
		t.Errorf("jobs: %+v pending %d", s.Jobs, s.JobsPending)
	}
	// A finished run's jobs never change: asked for once, then kept.
	if err := c.Call(ctx, proto.MethodCIStats, proto.CIStatsParams{ProjectID: proj.ID}, &s); err != nil {
		t.Fatal(err)
	}
	if s.Days != 14 {
		t.Errorf("no days given is 14: %d", s.Days)
	}
	viewed, _ := os.ReadFile(filepath.Join(dir, "viewed"))
	if n := len(strings.Fields(string(viewed))); n != 3 {
		t.Errorf("jobs read %d times for 3 runs: %q", n, viewed)
	}
	// Too many days is held to 90.
	if err := c.Call(ctx, proto.MethodCIStats, proto.CIStatsParams{ProjectID: proj.ID, Days: 1000}, &s); err != nil || s.Days != 90 {
		t.Errorf("1000 days: %d %v", s.Days, err)
	}
}

func TestCIStatsRefusals(t *testing.T) {
	fakeCIGH(t, `oops`)
	c, _, proj := setupRepo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var s proto.CIStats
	if err := c.Call(ctx, proto.MethodCIStats, proto.CIStatsParams{ProjectID: proj.ID}, &s); err == nil || !strings.Contains(err.Error(), "gh run list") {
		t.Errorf("gh's bad answer: %v", err)
	}
	if err := c.Call(ctx, proto.MethodCIStats, proto.CIStatsParams{ProjectID: "nope"}, &s); err == nil {
		t.Error("an unknown project was answered")
	}
	t.Setenv("CONCH_GH", filepath.Join(t.TempDir(), "no-gh"))
	if err := c.Call(ctx, proto.MethodCIStats, proto.CIStatsParams{ProjectID: proj.ID}, &s); err == nil || !strings.Contains(err.Error(), "gh") {
		t.Errorf("no gh: %v", err)
	}
}

func TestCIRerun(t *testing.T) {
	dir := fakeCIGH(t, `[]`)
	c, _, proj := setupRepo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var r proto.CIRerunResult
	if err := c.Call(ctx, proto.MethodCIRerun, proto.CIRerunParams{ProjectID: proj.ID, RunID: 42}, &r); err != nil || r.RunID != 42 {
		t.Fatalf("by run: %+v %v", r, err)
	}
	// By branch: its newest failed run.
	if err := c.Call(ctx, proto.MethodCIRerun, proto.CIRerunParams{ProjectID: proj.ID, Branch: "main"}, &r); err != nil || r.RunID != 8 {
		t.Fatalf("by branch: %+v %v", r, err)
	}
	reruns, _ := os.ReadFile(filepath.Join(dir, "reruns"))
	if string(reruns) != "42 --failed\n8 --failed\n" {
		t.Errorf("gh was asked %q", reruns)
	}
	if err := c.Call(ctx, proto.MethodCIRerun, proto.CIRerunParams{ProjectID: proj.ID}, &r); err == nil {
		t.Error("neither a run nor a branch was taken")
	}
	if err := c.Call(ctx, proto.MethodCIRerun, proto.CIRerunParams{ProjectID: proj.ID, RunID: 404}, &r); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("gh's refusal: %v", err)
	}
}

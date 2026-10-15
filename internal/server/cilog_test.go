package server_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// fakeGHOn puts a gh on CONCH_GH that answers pull requests with none and
// runs with list, printing log for run view.
func fakeGHOn(t *testing.T, list, log string) {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "log"), []byte(log), 0o600)
	script := `#!/bin/sh
case "$1 $2" in
"pr list") echo '[]' ;;
"run list") printf '%s' '` + list + `' ;;
"run view") cat "` + filepath.Join(dir, "log") + `" ;;
*) exit 7 ;;
esac
`
	gh := filepath.Join(dir, "gh")
	os.WriteFile(gh, []byte(script), 0o755)
	t.Setenv("CONCH_GH", gh)
}

func TestBranchCILog(t *testing.T) {
	fakeGHOn(t, `[{"databaseId": 4, "workflowName": "ci", "url": "https://x/runs/4", "headSha": "abc", "conclusion": "failure"}]`,
		strings.Repeat("noise\n", 20000)+"test\tgo test\t--- FAIL: TestX\n")
	c, _, proj := setupRepo(t)
	if miss := c.MissingCapabilities([]string{proto.CapBranchCILog}); len(miss) > 0 {
		t.Fatalf("capability missing: %v", miss)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var got proto.BranchCILog
	if err := c.Call(ctx, proto.MethodBranchCILog, proto.BranchCILogParams{ProjectID: proj.ID, Branch: "main"}, &got); err != nil {
		t.Fatal(err)
	}
	if got.Workflow != "ci" || got.URL != "https://x/runs/4" || !strings.HasSuffix(got.Log, "--- FAIL: TestX\n") {
		t.Fatalf("got %s %s, log ends %q", got.Workflow, got.URL, got.Log[max(len(got.Log)-40, 0):])
	}
	if len(got.Log) > 64<<10 {
		t.Fatalf("log of %d bytes was not capped", len(got.Log))
	}
}

func TestBranchCILogRefusals(t *testing.T) {
	fakeGHOn(t, `[{"databaseId": 4, "conclusion": "success"}]`, "")
	c, _, proj := setupRepo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for name, tc := range map[string]struct {
		params proto.BranchCILogParams
		want   string
	}{
		"nothing failed": {proto.BranchCILogParams{ProjectID: proj.ID, Branch: "main"}, "no failed GitHub Actions run on main"},
		"no branch":      {proto.BranchCILogParams{ProjectID: proj.ID}, "no branch given"},
		"no project":     {proto.BranchCILogParams{ProjectID: "nope", Branch: "main"}, "nope"},
	} {
		err := c.Call(ctx, proto.MethodBranchCILog, tc.params, nil)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", name, err, tc.want)
		}
	}
}

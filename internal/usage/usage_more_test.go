package usage

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func a6Append(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(s)
	f.Close()
}

func a6Assistant(id, model string, in, cw, cr, out int) string {
	return `{"type":"assistant","message":{"id":"` + id + `","model":"` + model + `","usage":{"input_tokens":` + strconv.Itoa(in) +
		`,"cache_creation_input_tokens":` + strconv.Itoa(cw) + `,"cache_read_input_tokens":` + strconv.Itoa(cr) + `,"output_tokens":` + strconv.Itoa(out) + `}}}` + "\n"
}

func TestA6TranscriptMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "none.jsonl")
	tr := NewTranscript(path)
	if tr.Path() != path {
		t.Fatalf("Path: %s", tr.Path())
	}
	tok, err := tr.Update()
	if err == nil || tok != (Tokens{}) {
		t.Fatalf("missing: %+v %v", tok, err)
	}
	// Totals so far survive the file disappearing.
	a6Append(t, path, a6Assistant("m1", "opus", 1, 2, 3, 4))
	if tok, err := tr.Update(); err != nil || tok.Output != 4 {
		t.Fatalf("created: %+v %v", tok, err)
	}
	os.Remove(path)
	tok, err = tr.Update()
	if err == nil || tok.Output != 4 || tok.Context != 6 || tok.Model != "opus" {
		t.Fatalf("removed: %+v %v", tok, err)
	}
}

func TestA6TranscriptSkipsNoise(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	a6Append(t, path, strings.Join([]string{
		`not json`,
		`{"type":"assistant","message":{"id":"m0"}}`,                               // no usage
		`{"type":"assistant","message":{"usage":{"output_tokens":99}}}`,            // no id
		`{"type":"user","message":{"id":"u1","usage":{"output_tokens":99}}}`,       // not assistant
		`{"type":"cost-state","totalCostUSD":0}`,                                   // zero cost ignored
		`{"type":"cost-state","totalCostUSD":"cheap"}`,                             // malformed
		`{"type":"assistant","message":{"id":"m1","usage":{"output_tokens":"x"}}}`, // malformed usage
		``,
		strings.TrimSuffix(a6Assistant("m1", "sonnet", 5, 0, 0, 1), "\n"),
		strings.TrimSuffix(a6Assistant("m2", "", 7, 0, 100, 2), "\n"), // empty model keeps the last
		`{"type":"cost-state","totalCostUSD":1.5}`,
		`{"type":"cost-state","totalCostUSD":0}`,
	}, "\n")+"\n")
	tok, err := NewTranscript(path).Update()
	if err != nil {
		t.Fatal(err)
	}
	want := Tokens{Input: 12, CacheRead: 100, Output: 3, Context: 107, Model: "sonnet", CostUSD: 1.5}
	if tok != want {
		t.Fatalf("got %+v, want %+v", tok, want)
	}
}

func TestA6TranscriptOverlongLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	huge := `{"type":"user","message":{"content":"` + strings.Repeat("y", 3<<20) + `"}}` + "\n"
	a6Append(t, path, huge+a6Assistant("m1", "opus", 10, 0, 0, 20))
	tok, err := NewTranscript(path).Update()
	if err != nil || tok.Output != 20 || tok.Input != 10 {
		t.Fatalf("after a long line: %+v %v", tok, err)
	}
}

func TestA6TranscriptRewrittenStartsOver(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	a6Append(t, path, a6Assistant("m1", "opus", 100, 0, 0, 50)+a6Assistant("m2", "opus", 100, 0, 0, 50))
	tr := NewTranscript(path)
	if tok, _ := tr.Update(); tok.Output != 100 {
		t.Fatalf("first: %+v", tok)
	}
	// Compaction rewrites the file shorter.
	os.WriteFile(path, []byte(a6Assistant("m9", "opus", 1, 0, 0, 1)), 0o600)
	tok, err := tr.Update()
	if err != nil || tok.Output != 1 || tok.Input != 1 || tok.Context != 1 {
		t.Fatalf("rewritten: %+v %v", tok, err)
	}
	// Nothing new: same totals, nothing re-counted.
	if again, _ := tr.Update(); again != tok {
		t.Fatalf("idempotent: %+v vs %+v", again, tok)
	}
}

func TestA6TranscriptContextIsLatestNewID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	// A later update to an older id doesn't move the context to it.
	a6Append(t, path, a6Assistant("m1", "opus", 1, 0, 0, 1)+a6Assistant("m2", "opus", 2, 0, 500, 1)+a6Assistant("m1", "opus", 1, 0, 0, 9))
	tok, _ := NewTranscript(path).Update()
	if tok.Context != 502 || tok.Output != 10 {
		t.Fatalf("context: %+v", tok)
	}
}

func TestA6CodexRolloutMissingAndRewrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	r := NewCodexRollout(path)
	if r.Path() != path || r.Limits() != nil {
		t.Fatal("new rollout")
	}
	if _, err := r.Update(); err == nil {
		t.Fatal("missing file")
	}
	long := `{"type":"turn_context","payload":{"model":"gpt-a"}}` + "\n" +
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":900,"cached_input_tokens":100,"output_tokens":90},"last_token_usage":{"input_tokens":50}}}}` + "\n"
	a6Append(t, path, long)
	tok, err := r.Update()
	if err != nil || tok.Input != 800 || tok.CacheRead != 100 || tok.Model != "gpt-a" || tok.Context != 50 {
		t.Fatalf("first: %+v %v", tok, err)
	}
	os.WriteFile(path, []byte(`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":5,"output_tokens":1},"last_token_usage":{"input_tokens":5}}}}`+"\n"), 0o600)
	tok, _ = r.Update()
	if tok.Input != 5 || tok.Output != 1 || tok.Model != "" || tok.CacheRead != 0 {
		t.Fatalf("rewrite: %+v", tok)
	}
	os.Remove(path)
	if tok2, err := r.Update(); err == nil || tok2 != tok {
		t.Fatalf("removed: %+v %v", tok2, err)
	}
}

func TestA6CodexRolloutNoise(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	a6Append(t, path, strings.Join([]string{
		`{"type":"event_msg","payload":{"type":"agent_message","message":"mentions \"token_count\" in text"}}`,
		`{"type":"event_msg","payload":{"type":"token_count", broken`,
		`{"type":"turn_context","payload":{}}`,
		`{"type":"response_item","payload":{"type":"message","model":"not-a-turn-context","content":"turn_context"}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":10,"output_tokens":3},"last_token_usage":{"input_tokens":10}}}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":20,"output_tokens":6},"last_token_usage":{"input_tokens":10}}}`, // partial line at EOF
	}, "\n"))
	r := NewCodexRollout(path)
	tok, err := r.Update()
	if err != nil || tok.Input != 10 || tok.Output != 3 || tok.Model != "" {
		t.Fatalf("noise: %+v %v", tok, err)
	}
	a6Append(t, path, "}\n")
	if tok, _ = r.Update(); tok.Input != 20 || tok.Output != 6 {
		t.Fatalf("completed partial line: %+v", tok)
	}
	if r.Limits() != nil {
		t.Fatalf("no limits reported: %+v", r.Limits())
	}
}

func TestA6CodexLimitsVariants(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	r := NewCodexRollout(path)

	// Primary only, an hour-long window, no reset time; info null.
	a6Append(t, path, `{"type":"event_msg","payload":{"type":"token_count","info":null,"rate_limits":{"primary":{"used_percent":80,"window_minutes":60}}}}`+"\n")
	before := time.Now()
	tok, _ := r.Update()
	l := r.Limits()
	if tok != (Tokens{}) || l == nil || l.Agent != "codex" || l.FiveHour == nil || l.FiveHour.UsedPct != 80 || !l.FiveHour.ResetsAt.IsZero() || l.Week != nil || l.At.Before(before) {
		t.Fatalf("primary only: %+v %+v", tok, l)
	}

	// Exactly a day counts as the short window; unknown length as the week.
	a6Append(t, path, `{"type":"event_msg","payload":{"type":"token_count","rate_limits":{"primary":{"used_percent":1,"window_minutes":1440},"secondary":{"used_percent":2}}}}`+"\n")
	r.Update()
	l = r.Limits()
	if l.FiveHour == nil || l.FiveHour.UsedPct != 1 || l.Week == nil || l.Week.UsedPct != 2 {
		t.Fatalf("day and unknown: %+v %+v", l.FiveHour, l.Week)
	}

	// Empty rate limits keep the last report.
	a6Append(t, path, `{"type":"event_msg","payload":{"type":"token_count","rate_limits":{"primary":null,"secondary":null}}}`+"\n")
	a6Append(t, path, `{"type":"event_msg","payload":{"type":"token_count","rate_limits":null}}`+"\n")
	r.Update()
	if r.Limits() != l {
		t.Fatalf("empty limits replaced the last report: %+v", r.Limits())
	}

	// Over 100% is kept as reported.
	a6Append(t, path, `{"type":"event_msg","payload":{"type":"token_count","rate_limits":{"secondary":{"used_percent":104.5,"window_minutes":10080,"resets_at":1800000000}}}}`+"\n")
	r.Update()
	if l := r.Limits(); l.FiveHour != nil || l.Week.UsedPct != 104.5 || l.Week.ResetsAt.Unix() != 1800000000 {
		t.Fatalf("exceeded: %+v", l)
	}
}

func TestA6OpenCodeSessionFakeSQLite(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	if _, err := OpenCodeSession("/x.db", "ses"); err == nil {
		t.Fatal("no sqlite3 should fail")
	}

	fake := func(body string) {
		os.WriteFile(filepath.Join(dir, "sqlite3"), []byte("#!/bin/sh\n"+body), 0o755)
	}
	log := filepath.Join(t.TempDir(), "args")
	fake(`for a in "$@"; do printf '%s\n' "$a"; done > ` + log + `
echo '[{"tokens_input":1,"tokens_output":2,"tokens_cache_read":3,"tokens_cache_write":4,"cost":0.5,"model":"not json"}]'
`)
	tok, err := OpenCodeSession("/data/opencode.db", "it's")
	if err != nil || tok != (Tokens{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4, CostUSD: 0.5}) {
		t.Fatalf("row: %+v %v", tok, err)
	}
	b, _ := os.ReadFile(log)
	args := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	if len(args) != 4 || args[0] != "-readonly" || args[1] != "-json" || args[2] != "/data/opencode.db" || !strings.HasSuffix(args[3], "WHERE id = 'it''s'") {
		t.Fatalf("args: %q", args)
	}

	// Null model column: no model.
	fake(`echo '[{"tokens_input":9,"model":null}]'` + "\n")
	if tok, err := OpenCodeSession("/db", "s"); err != nil || tok.Input != 9 || tok.Model != "" {
		t.Fatalf("null model: %+v %v", tok, err)
	}

	for name, body := range map[string]string{
		"fails":    "echo 'Error: no such table: session' >&2; exit 1\n",
		"empty":    "exit 0\n",
		"blank":    "printf '\\n  \\n'\n",
		"no rows":  "echo '[]'\n",
		"garbage":  "echo 'Error'\n",
		"wrong ty": `echo '[{"tokens_input":"many"}]'` + "\n",
	} {
		fake(body)
		if _, err := OpenCodeSession("/db", "ses_404"); err == nil {
			t.Errorf("%s: no error", name)
		} else if name != "fails" && !strings.Contains(err.Error(), "no opencode session ses_404") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

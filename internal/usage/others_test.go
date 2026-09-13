package usage

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCodexRollout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	write := func(s string) {
		f, _ := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		f.WriteString(s)
		f.Close()
	}
	write(`{"type":"session_meta","payload":{"id":"c1"}}
{"type":"turn_context","payload":{"model":"gpt-5.6"}}
{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":12000,"cached_input_tokens":0,"output_tokens":100},"last_token_usage":{"input_tokens":12000,"output_tokens":100}}}}
`)
	r := NewCodexRollout(path)
	tok, err := r.Update()
	if err != nil || tok.Input != 12000 || tok.Output != 100 || tok.Model != "gpt-5.6" || tok.Context != 12000 {
		t.Fatalf("first: %+v %v", tok, err)
	}
	write(`{"type":"event_msg","payload":{"type":"token_count","info":null}}
{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":25000,"cached_input_tokens":12000,"output_tokens":250},"last_token_usage":{"input_tokens":13000,"output_tokens":150}}}}
`)
	if tok, _ = r.Update(); tok.Input != 13000 || tok.CacheRead != 12000 || tok.Output != 250 || tok.Context != 13000 {
		t.Fatalf("cumulative: %+v", tok)
	}
}

func TestOpenCodeSession(t *testing.T) {
	bin, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("no sqlite3")
	}
	db := filepath.Join(t.TempDir(), "opencode.db")
	sql := `CREATE TABLE session (id text, tokens_input integer, tokens_output integer, tokens_cache_read integer, tokens_cache_write integer, cost real, model text);
INSERT INTO session VALUES ('ses_1', 20438, 812, 1000, 0, 0.0259, '{"id":"grok-4.3","providerID":"xai"}');`
	if out, err := exec.Command(bin, db, sql).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	tok, err := OpenCodeSession(db, "ses_1")
	if err != nil || tok.Input != 20438 || tok.Output != 812 || tok.CacheRead != 1000 || tok.CostUSD != 0.0259 || tok.Model != "grok-4.3" {
		t.Fatalf("%+v %v", tok, err)
	}
	if _, err := OpenCodeSession(db, "missing'; DROP TABLE session; --"); err == nil {
		t.Fatal("unknown session should fail")
	}
	if _, err := OpenCodeSession(db, "ses_1"); err != nil {
		t.Fatal("table must survive the quoted id")
	}
}

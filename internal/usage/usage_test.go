package usage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTranscript(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	write := func(s string) {
		f, _ := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		f.WriteString(s)
		f.Close()
	}
	write(`{"type":"user","message":{"role":"user","content":"hi"}}
{"type":"assistant","message":{"id":"m1","model":"claude-opus-5","usage":{"input_tokens":10,"cache_creation_input_tokens":1000,"cache_read_input_tokens":0,"output_tokens":5}}}
{"type":"assistant","message":{"id":"m1","model":"claude-opus-5","usage":{"input_tokens":10,"cache_creation_input_tokens":1000,"cache_read_input_tokens":0,"output_tokens":40}}}
`)
	tr := NewTranscript(path)
	tok, err := tr.Update()
	if err != nil || tok.Output != 40 || tok.CacheWrite != 1000 || tok.Context != 1010 || tok.Model != "claude-opus-5" {
		t.Fatalf("first read: %+v %v", tok, err)
	}
	// A second response, with a partial line still being written.
	write(`{"type":"assistant","message":{"id":"m2","model":"claude-opus-5","usage":{"input_tokens":3,"cache_creation_input_tokens":0,"cache_read_input_tokens":1010,"output_tokens":7}}}
{"type":"assistant","message":{"id":"m3","usa`)
	tok, _ = tr.Update()
	if tok.Output != 47 || tok.CacheRead != 1010 || tok.Context != 1013 {
		t.Fatalf("incremental: %+v", tok)
	}
	write(`ge":{"input_tokens":1,"cache_creation_input_tokens":0,"cache_read_input_tokens":1020,"output_tokens":2}}}
`)
	if tok, _ = tr.Update(); tok.Output != 49 || tok.Context != 1021 {
		t.Fatalf("completed line: %+v", tok)
	}
}

func TestTranscriptCost(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	os.WriteFile(path, []byte(`{"type":"assistant","message":{"id":"m1","model":"claude-opus-5","usage":{"input_tokens":10,"output_tokens":5}}}
{"type":"cost-state","totalCostUSD":0.42}
`), 0o644)
	tok, err := NewTranscript(path).Update()
	if err != nil || tok.CostUSD != 0.42 || tok.Output != 5 {
		t.Fatalf("%+v %v", tok, err)
	}
}

// Package usage totals an agent session's token usage from its transcript.
package usage

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
)

// Tokens is a session's usage so far.
type Tokens struct {
	Input      int    `json:"input"`       // uncached input tokens
	CacheWrite int    `json:"cache_write"` // input tokens written to the prompt cache
	CacheRead  int    `json:"cache_read"`  // input tokens read from the cache
	Output     int    `json:"output"`
	Context    int    `json:"context"` // tokens in the latest request's context
	Model      string `json:"model,omitempty"`
	// CostUSD is the session's cost when the agent reports one; 0 otherwise
	// (conch doesn't estimate prices).
	CostUSD float64 `json:"cost_usd,omitempty"`
	// ContextSize is the model's context window, when known.
	ContextSize int `json:"context_size,omitempty"`
}

type counts struct {
	input, cacheWrite, cacheRead, output int
}

// Transcript follows a Claude Code transcript (JSON lines) as it grows.
// A response is written as several lines sharing one message id and
// carrying its usage so far, so usage is kept per id and the latest wins.
type Transcript struct {
	path   string
	offset int64
	byID   map[string]counts
	order  []string
	model  string
	cost   float64
}

// NewTranscript follows the transcript at path.
func NewTranscript(path string) *Transcript {
	return &Transcript{path: path, byID: map[string]counts{}}
}

// Path is the transcript file.
func (t *Transcript) Path() string { return t.path }

type line struct {
	Type         string  `json:"type"`
	TotalCostUSD float64 `json:"totalCostUSD"`
	Message struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage *struct {
			Input      int `json:"input_tokens"`
			CacheWrite int `json:"cache_creation_input_tokens"`
			CacheRead  int `json:"cache_read_input_tokens"`
			Output     int `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// Update reads what was appended since the last call and returns the
// totals. A partial last line is left for next time.
func (t *Transcript) Update() (Tokens, error) {
	f, err := os.Open(t.path)
	if err != nil {
		return t.totals(), err
	}
	defer f.Close()
	if st, err := f.Stat(); err == nil && st.Size() < t.offset {
		// Rewritten (e.g. compacted): start over.
		t.offset, t.byID, t.order = 0, map[string]counts{}, nil
	}
	if _, err := f.Seek(t.offset, io.SeekStart); err != nil {
		return t.totals(), err
	}
	r := bufio.NewReaderSize(f, 1<<16)
	for {
		b, err := r.ReadBytes('\n')
		if err != nil {
			break // EOF, or a line still being written
		}
		t.offset += int64(len(b))
		var l line
		if json.Unmarshal(b, &l) != nil {
			continue
		}
		if l.Type == "cost-state" && l.TotalCostUSD > 0 {
			t.cost = l.TotalCostUSD
			continue
		}
		if l.Type != "assistant" || l.Message.Usage == nil || l.Message.ID == "" {
			continue
		}
		u := l.Message.Usage
		if _, seen := t.byID[l.Message.ID]; !seen {
			t.order = append(t.order, l.Message.ID)
		}
		t.byID[l.Message.ID] = counts{u.Input, u.CacheWrite, u.CacheRead, u.Output}
		if l.Message.Model != "" {
			t.model = l.Message.Model
		}
	}
	return t.totals(), nil
}

func (t *Transcript) totals() Tokens {
	tok := Tokens{Model: t.model, CostUSD: t.cost}
	for _, id := range t.order {
		c := t.byID[id]
		tok.Input += c.input
		tok.CacheWrite += c.cacheWrite
		tok.CacheRead += c.cacheRead
		tok.Output += c.output
	}
	if n := len(t.order); n > 0 {
		c := t.byID[t.order[n-1]]
		tok.Context = c.input + c.cacheWrite + c.cacheRead
	}
	return tok
}

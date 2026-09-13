package usage

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// CodexRollout follows a Codex session file (JSON lines) as it grows. Codex
// writes cumulative token_count events, so the latest one holds the totals.
type CodexRollout struct {
	path   string
	offset int64
	tok    Tokens
}

// NewCodexRollout follows the rollout file at path.
func NewCodexRollout(path string) *CodexRollout { return &CodexRollout{path: path} }

// Path is the rollout file.
func (c *CodexRollout) Path() string { return c.path }

// Update reads what was appended since the last call.
func (c *CodexRollout) Update() (Tokens, error) {
	f, err := os.Open(c.path)
	if err != nil {
		return c.tok, err
	}
	defer f.Close()
	if st, err := f.Stat(); err == nil && st.Size() < c.offset {
		c.offset, c.tok = 0, Tokens{}
	}
	if _, err := f.Seek(c.offset, io.SeekStart); err != nil {
		return c.tok, err
	}
	r := bufio.NewReaderSize(f, 1<<16)
	type usage struct {
		Input  int `json:"input_tokens"`
		Cached int `json:"cached_input_tokens"`
		Output int `json:"output_tokens"`
	}
	for {
		b, err := r.ReadBytes('\n')
		if err != nil {
			break
		}
		c.offset += int64(len(b))
		if !strings.Contains(string(b), `"token_count"`) && !strings.Contains(string(b), `"turn_context"`) {
			continue
		}
		var l struct {
			Type    string `json:"type"`
			Payload struct {
				Type  string `json:"type"`
				Model string `json:"model"`
				Info  *struct {
					Total usage `json:"total_token_usage"`
					Last  usage `json:"last_token_usage"`
				} `json:"info"`
			} `json:"payload"`
		}
		if json.Unmarshal(b, &l) != nil {
			continue
		}
		switch {
		case l.Type == "turn_context" && l.Payload.Model != "":
			c.tok.Model = l.Payload.Model
		case l.Payload.Type == "token_count" && l.Payload.Info != nil:
			t := l.Payload.Info.Total
			c.tok.Input, c.tok.CacheRead, c.tok.Output = t.Input-t.Cached, t.Cached, t.Output
			c.tok.Context = l.Payload.Info.Last.Input
		}
	}
	return c.tok, nil
}

// OpenCodeSession reads a session's usage from OpenCode's sqlite store
// through the sqlite3 command.
func OpenCodeSession(db, id string) (Tokens, error) {
	bin, err := exec.LookPath("sqlite3")
	if err != nil {
		return Tokens{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	q := fmt.Sprintf("SELECT tokens_input, tokens_output, tokens_cache_read, tokens_cache_write, cost, model FROM session WHERE id = '%s'",
		strings.ReplaceAll(id, "'", "''"))
	out, err := exec.CommandContext(ctx, bin, "-readonly", "-json", db, q).Output()
	if err != nil {
		return Tokens{}, err
	}
	var rows []struct {
		Input      int     `json:"tokens_input"`
		Output     int     `json:"tokens_output"`
		CacheRead  int     `json:"tokens_cache_read"`
		CacheWrite int     `json:"tokens_cache_write"`
		Cost       float64 `json:"cost"`
		Model      string  `json:"model"`
	}
	if len(strings.TrimSpace(string(out))) == 0 {
		return Tokens{}, fmt.Errorf("no opencode session %s", id)
	}
	if err := json.Unmarshal(out, &rows); err != nil || len(rows) == 0 {
		return Tokens{}, fmt.Errorf("no opencode session %s", id)
	}
	r := rows[0]
	tok := Tokens{Input: r.Input, Output: r.Output, CacheRead: r.CacheRead, CacheWrite: r.CacheWrite, CostUSD: r.Cost}
	var model struct {
		ID string `json:"id"`
	}
	if json.Unmarshal([]byte(r.Model), &model) == nil {
		tok.Model = model.ID
	}
	return tok, nil
}

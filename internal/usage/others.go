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

	"github.com/Amitgb14/conch/internal/proto"
)

// CodexRollout follows a Codex session file (JSON lines) as it grows. Codex
// writes cumulative token_count events, so the latest one holds the totals.
type CodexRollout struct {
	path   string
	offset int64
	tok    Tokens
	limits *proto.PlanLimits
}

// Limits returns the plan limits the session last reported, if any.
func (c *CodexRollout) Limits() *proto.PlanLimits { return c.limits }

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
					Total  usage `json:"total_token_usage"`
					Last   usage `json:"last_token_usage"`
					Window int   `json:"model_context_window"`
				} `json:"info"`
				RateLimits *struct {
					Primary   *codexWindow `json:"primary"`
					Secondary *codexWindow `json:"secondary"`
				} `json:"rate_limits"`
			} `json:"payload"`
		}
		if json.Unmarshal(b, &l) != nil {
			continue
		}
		switch {
		case l.Type == "turn_context" && l.Payload.Model != "":
			c.tok.Model = l.Payload.Model
		case l.Payload.Type == "token_count":
			if info := l.Payload.Info; info != nil {
				t := info.Total
				c.tok.Input, c.tok.CacheRead, c.tok.Output = t.Input-t.Cached, t.Cached, t.Output
				c.tok.Context, c.tok.ContextSize = info.Last.Input, info.Window
			}
			if rl := l.Payload.RateLimits; rl != nil {
				lim := &proto.PlanLimits{Agent: "codex", At: time.Now()}
				for _, w := range []*codexWindow{rl.Primary, rl.Secondary} {
					if w == nil {
						continue
					}
					lw := &proto.LimitWindow{UsedPct: w.UsedPercent}
					if w.ResetsAt > 0 {
						lw.ResetsAt = time.Unix(w.ResetsAt, 0)
					}
					// Windows are told apart by length: hours or a week.
					if w.WindowMinutes > 0 && w.WindowMinutes <= 24*60 {
						lim.FiveHour = lw
					} else {
						lim.Week = lw
					}
				}
				if lim.FiveHour != nil || lim.Week != nil {
					c.limits = lim
				}
			}
		}
	}
	return c.tok, nil
}

type codexWindow struct {
	UsedPercent   float64 `json:"used_percent"`
	WindowMinutes int     `json:"window_minutes"`
	ResetsAt      int64   `json:"resets_at"`
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

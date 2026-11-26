package usage

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// What an agent spent over the last day, week and month, by model, read
// from the conversations it saved — not only the sessions open in conch,
// which is what the rest of this package follows. Claude Code writes each
// response's usage on its assistant lines, several lines to a response
// sharing one message id (the latest wins, as in Transcript); Codex
// writes running totals in token_count events, so what a turn used is
// the difference from the total before it, which also keeps a total
// repeated with nothing new (a rate-limit update) from counting twice.
//
// Antigravity CLI (agy) writes each model step's own usage — its model,
// input, cached and output tokens — on that step's line of the
// conversation's transcript.jsonl; a step written again is the same step,
// the latest winning.
//
// Files are read once and then from where they were left, so asking again
// costs only what was appended. Only files changed within the longest
// period are read at all.

// historyPeriods are the periods summarised, in hours.
var historyPeriods = []int{24, 7 * 24, 30 * 24}

// message is one response's usage.
type message struct {
	at    time.Time
	model string
	tok   proto.UsageTokens
}

// histFile is what has been read of one file.
type histFile struct {
	offset int64
	size   int64
	// Claude: by message id, the latest line winning.
	byID map[string]message
	// Codex: each turn, and the running total and model so far.
	turns []message
	total proto.UsageTokens
	model string
}

// History follows the saved conversations of every agent it is asked
// about. Safe for concurrent use.
type History struct {
	mu    sync.Mutex
	files map[string]*histFile
}

// NewHistory is an empty History.
func NewHistory() *History { return &History{files: map[string]*histFile{}} }

// Root is another of an agent's configuration folders: an account of its
// own (internal/accounts), whose conversations count as the agent's.
type Root struct {
	Agent, Dir string
	// Name is the account's, for ByAccount; "" is the agent's own.
	Name string
}

// rootsOf is own and every other folder of agent, each once.
func rootsOf(agent, own string, others []Root) []string {
	out := []string{own}
	seen := map[string]bool{filepath.Clean(own): true}
	for _, r := range others {
		if r.Agent == agent && r.Dir != "" && !seen[filepath.Clean(r.Dir)] {
			seen[filepath.Clean(r.Dir)] = true
			out = append(out, r.Dir)
		}
	}
	return out
}

// Summary is what each agent used in each period, by model, as of now.
// home is the home directory whose agents' folders are read; env says
// where an agent was told to keep them instead (CLAUDE_CONFIG_DIR,
// CODEX_HOME); others are the agents' other accounts.
func (h *History) Summary(home string, env func(string) string, now time.Time, others ...Root) proto.UsageHistory {
	longest := time.Duration(historyPeriods[len(historyPeriods)-1]) * time.Hour
	since := now.Add(-longest)
	out := proto.UsageHistory{At: now}

	claudeRoot := filepath.Join(home, ".claude")
	if d := env("CLAUDE_CONFIG_DIR"); d != "" {
		claudeRoot = d
	}
	codexRoot := filepath.Join(home, ".codex")
	if d := env("CODEX_HOME"); d != "" {
		codexRoot = d
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	var claude, codex []message
	seen := map[string]bool{}
	for _, root := range rootsOf("claude", claudeRoot, others) {
		for _, f := range recentFiles(filepath.Join(root, "projects"), since) {
			hf := h.read(f, readClaude)
			for id, m := range hf.byID {
				// A resumed conversation can carry earlier responses into
				// a new file: each response is counted once.
				if !seen[id] {
					seen[id] = true
					claude = append(claude, m)
				}
			}
		}
	}
	for _, root := range rootsOf("codex", codexRoot, others) {
		for _, f := range recentFiles(filepath.Join(root, "sessions"), since) {
			codex = append(codex, h.read(f, readCodex).turns...)
		}
	}
	if len(claude) > 0 {
		out.Agents = append(out.Agents, summarise("claude", claude, now))
	}
	if len(codex) > 0 {
		out.Agents = append(out.Agents, summarise("codex", codex, now))
	}
	// An account of agy's is a folder given as --gemini_dir (#367).
	var agy []message
	for _, root := range rootsOf("antigravity", filepath.Join(home, ".gemini"), others) {
		for _, f := range antigravityFiles(root, since) {
			for _, m := range h.read(f, readAntigravity).byID {
				agy = append(agy, m)
			}
		}
	}
	if len(agy) > 0 {
		out.Agents = append(out.Agents, summarise("antigravity", agy, now))
	}
	return out
}

// antigravityFiles is each of agy's conversations' transcript changed
// since then, under root (~/.gemini, or an account's --gemini_dir). Only
// transcript.jsonl: transcript_full.jsonl and the chunks beside it hold
// the same steps again.
func antigravityFiles(root string, since time.Time) []string {
	files, _ := filepath.Glob(filepath.Join(root, "antigravity-cli", "brain", "*", ".system_generated", "logs", "transcript.jsonl"))
	var out []string
	for _, f := range files {
		if info, err := os.Stat(f); err == nil && !info.ModTime().Before(since) {
			out = append(out, f)
		}
	}
	return out
}

// recentFiles is every .jsonl under dir changed since then.
func recentFiles(dir string, since time.Time) []string {
	var out []string
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		if info, err := d.Info(); err == nil && !info.ModTime().Before(since) {
			out = append(out, path)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// read brings a file's entry up to date with what was appended, starting
// over when the file is shorter than what was read (rewritten).
func (h *History) read(path string, parse func(*histFile, []byte)) *histFile {
	hf := h.files[path]
	if hf == nil {
		hf = &histFile{byID: map[string]message{}}
		h.files[path] = hf
	}
	f, err := os.Open(path)
	if err != nil {
		return hf
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return hf
	}
	if st.Size() < hf.offset {
		*hf = histFile{byID: map[string]message{}}
	}
	if st.Size() == hf.offset {
		return hf
	}
	if _, err := f.Seek(hf.offset, io.SeekStart); err != nil {
		return hf
	}
	r := bufio.NewReaderSize(f, 1<<16)
	for {
		b, err := r.ReadBytes('\n')
		if err != nil {
			break // EOF, or a line still being written: read next time
		}
		hf.offset += int64(len(b))
		parse(hf, b)
	}
	hf.size = st.Size()
	return hf
}

// readClaude takes one line of a Claude Code transcript.
func readClaude(hf *histFile, b []byte) {
	if !strings.Contains(string(b), `"assistant"`) {
		return
	}
	var l struct {
		Type      string    `json:"type"`
		Timestamp time.Time `json:"timestamp"`
		Message   struct {
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
	if json.Unmarshal(b, &l) != nil || l.Type != "assistant" || l.Message.ID == "" || l.Message.Usage == nil || l.Timestamp.IsZero() {
		return
	}
	// A model of "<synthetic>" is Claude Code's own placeholder, not a model.
	if l.Message.Model == "" || strings.HasPrefix(l.Message.Model, "<") {
		return
	}
	u := l.Message.Usage
	hf.byID[l.Message.ID] = message{at: l.Timestamp, model: l.Message.Model,
		tok: proto.UsageTokens{Input: u.Input, CacheWrite: u.CacheWrite, CacheRead: u.CacheRead, Output: u.Output, Messages: 1}}
}

// readCodex takes one line of a Codex rollout.
func readCodex(hf *histFile, b []byte) {
	s := string(b)
	if !strings.Contains(s, `"token_count"`) && !strings.Contains(s, `"turn_context"`) {
		return
	}
	type usage struct {
		Input  int `json:"input_tokens"`
		Cached int `json:"cached_input_tokens"`
		Output int `json:"output_tokens"`
	}
	var l struct {
		Timestamp time.Time `json:"timestamp"`
		Type      string    `json:"type"`
		Payload   struct {
			Type  string `json:"type"`
			Model string `json:"model"`
			Info  *struct {
				Total usage `json:"total_token_usage"`
			} `json:"info"`
		} `json:"payload"`
	}
	if json.Unmarshal(b, &l) != nil {
		return
	}
	if l.Type == "turn_context" {
		if l.Payload.Model != "" {
			hf.model = l.Payload.Model
		}
		return
	}
	if l.Payload.Type != "token_count" || l.Payload.Info == nil || l.Timestamp.IsZero() {
		return
	}
	t := l.Payload.Info.Total
	now := proto.UsageTokens{Input: t.Input - t.Cached, CacheRead: t.Cached, Output: t.Output}
	d := proto.UsageTokens{Input: now.Input - hf.total.Input, CacheRead: now.CacheRead - hf.total.CacheRead, Output: now.Output - hf.total.Output, Messages: 1}
	if d.Input < 0 || d.CacheRead < 0 || d.Output < 0 {
		// The total went down: a new count began; this one is all new.
		d = now
		d.Messages = 1
	}
	hf.total = now
	if d.Input == 0 && d.CacheRead == 0 && d.Output == 0 {
		return // the same total said again
	}
	model := hf.model
	if model == "" {
		model = "unknown"
	}
	hf.turns = append(hf.turns, message{at: l.Timestamp, model: model, tok: d})
}

// readAntigravity takes one line of an agy transcript: a model step's
// usage. Cached tokens are counted apart from input, as Claude's are: a
// real second turn read 2,309 in and 9,579 cached for a prompt the size of
// the first turn's 11,628 in.
func readAntigravity(hf *histFile, b []byte) {
	if !strings.Contains(string(b), `"output_tokens"`) {
		return
	}
	var l struct {
		Step    *int      `json:"step_index"`
		Type    string    `json:"type"`
		Created time.Time `json:"created_at"`
		Model   string    `json:"model"`
		Input   int       `json:"input_tokens"`
		Cached  int       `json:"cache_read_tokens"`
		Output  int       `json:"output_tokens"`
	}
	if json.Unmarshal(b, &l) != nil || l.Type != "PLANNER_RESPONSE" || l.Step == nil || l.Model == "" || l.Created.IsZero() {
		return
	}
	hf.byID[strconv.Itoa(*l.Step)] = message{at: l.Created, model: l.Model,
		tok: proto.UsageTokens{Input: l.Input, CacheRead: l.Cached, Output: l.Output, Messages: 1}}
}

// summarise is one agent's periods.
func summarise(agent string, msgs []message, now time.Time) proto.AgentUsage {
	a := proto.AgentUsage{Agent: agent}
	for _, hours := range historyPeriods {
		since := now.Add(-time.Duration(hours) * time.Hour)
		p := proto.UsagePeriod{Hours: hours}
		byModel := map[string]*proto.ModelUsage{}
		for _, m := range msgs {
			if m.at.Before(since) || m.at.After(now) {
				continue
			}
			p.UsageTokens.Add(m.tok)
			mu := byModel[m.model]
			if mu == nil {
				mu = &proto.ModelUsage{Model: m.model}
				byModel[m.model] = mu
			}
			mu.UsageTokens.Add(m.tok)
		}
		for _, mu := range byModel {
			p.Models = append(p.Models, *mu)
		}
		// The model that used most first; a tie by name, so the order holds.
		sort.Slice(p.Models, func(i, j int) bool {
			ti, tj := p.Models[i].Total(), p.Models[j].Total()
			if ti != tj {
				return ti > tj
			}
			return p.Models[i].Model < p.Models[j].Model
		})
		a.Periods = append(a.Periods, p)
	}
	return a
}

// ByAccount is Summary one entry per account rather than per agent, each
// with its last 24 hours and 30 days in buckets for charting, the hours and
// days starting where they do in loc. An account with nothing saved in the
// last 30 days is left out, as an agent is in Summary.
func (h *History) ByAccount(home string, env func(string) string, now time.Time, loc *time.Location, others ...Root) proto.UsageHistory {
	longest := time.Duration(historyPeriods[len(historyPeriods)-1]) * time.Hour
	since := now.Add(-longest)
	out := proto.UsageHistory{At: now}
	claudeRoot := filepath.Join(home, ".claude")
	if d := env("CLAUDE_CONFIG_DIR"); d != "" {
		claudeRoot = d
	}
	codexRoot := filepath.Join(home, ".codex")
	if d := env("CODEX_HOME"); d != "" {
		codexRoot = d
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	seen := map[string]bool{}
	for _, agent := range []string{"claude", "codex", "antigravity"} {
		own := claudeRoot
		switch agent {
		case "codex":
			own = codexRoot
		case "antigravity":
			own = filepath.Join(home, ".gemini")
		}
		done := map[string]bool{}
		roots := append([]Root{{Agent: agent, Dir: own}}, others...)
		for _, r := range roots {
			if r.Agent != agent || r.Dir == "" || done[filepath.Clean(r.Dir)] {
				continue
			}
			done[filepath.Clean(r.Dir)] = true
			var msgs []message
			switch agent {
			case "antigravity":
				for _, f := range antigravityFiles(r.Dir, since) {
					for _, m := range h.read(f, readAntigravity).byID {
						msgs = append(msgs, m)
					}
				}
			case "claude":
				for _, f := range recentFiles(filepath.Join(r.Dir, "projects"), since) {
					for id, m := range h.read(f, readClaude).byID {
						if !seen[id] {
							seen[id] = true
							msgs = append(msgs, m)
						}
					}
				}
			default:
				for _, f := range recentFiles(filepath.Join(r.Dir, "sessions"), since) {
					msgs = append(msgs, h.read(f, readCodex).turns...)
				}
			}
			if len(msgs) == 0 {
				continue
			}
			a := summarise(agent, msgs, now)
			a.Account = r.Name
			a.Hours = buckets(msgs, hourStarts(now, loc, 24), now)
			a.Days = buckets(msgs, dayStarts(now, loc, 30), now)
			out.Agents = append(out.Agents, a)
		}
	}
	return out
}

// hourStarts is the start of each of the last n hours, the current one
// last.
func hourStarts(now time.Time, loc *time.Location, n int) []time.Time {
	t := now.In(loc).Truncate(time.Hour)
	out := make([]time.Time, n)
	for i := range out {
		out[n-1-i] = t.Add(-time.Duration(i) * time.Hour)
	}
	return out
}

// dayStarts is the start of each of the last n days in loc, today last —
// by the calendar, so a day across a change of clocks is still one day.
func dayStarts(now time.Time, loc *time.Location, n int) []time.Time {
	y, m, d := now.In(loc).Date()
	out := make([]time.Time, n)
	for i := range out {
		out[n-1-i] = time.Date(y, m, d-i, 0, 0, 0, 0, loc)
	}
	return out
}

// buckets adds msgs into the spans starting at starts, each running to the
// next start (the last to now); what falls before the first or after now
// is left out. Each bucket's models are the one that used most first.
func buckets(msgs []message, starts []time.Time, now time.Time) []proto.UsageBucket {
	out := make([]proto.UsageBucket, len(starts))
	byModel := make([]map[string]*proto.ModelUsage, len(starts))
	for i, s := range starts {
		out[i].Start = s
		byModel[i] = map[string]*proto.ModelUsage{}
	}
	for _, m := range msgs {
		if m.at.Before(starts[0]) || m.at.After(now) {
			continue
		}
		i := sort.Search(len(starts), func(k int) bool { return starts[k].After(m.at) }) - 1
		if i < 0 {
			continue
		}
		out[i].UsageTokens.Add(m.tok)
		mu := byModel[i][m.model]
		if mu == nil {
			mu = &proto.ModelUsage{Model: m.model}
			byModel[i][m.model] = mu
		}
		mu.UsageTokens.Add(m.tok)
	}
	for i := range out {
		for _, mu := range byModel[i] {
			out[i].Models = append(out[i].Models, *mu)
		}
		sort.Slice(out[i].Models, func(a, b int) bool {
			ta, tb := out[i].Models[a].Total(), out[i].Models[b].Total()
			if ta != tb {
				return ta > tb
			}
			return out[i].Models[a].Model < out[i].Models[b].Model
		})
	}
	return out
}

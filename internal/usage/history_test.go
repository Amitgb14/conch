package usage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

var histNow = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

func claudeLine(id, model string, at time.Time, in, cw, cr, out int) string {
	return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"message":{"id":%q,"model":%q,"usage":{"input_tokens":%d,"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d,"output_tokens":%d}}}`,
		at.Format(time.RFC3339Nano), id, model, in, cw, cr, out) + "\n"
}

func codexTotal(at time.Time, in, cached, out int) string {
	return fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":%d,"cached_input_tokens":%d,"output_tokens":%d}}}}`,
		at.Format(time.RFC3339Nano), in, cached, out) + "\n"
}

func codexModel(model string) string {
	return fmt.Sprintf(`{"timestamp":"2026-11-04T00:00:00Z","type":"turn_context","payload":{"model":%q}}`, model) + "\n"
}

func writeAt(t *testing.T, path, body string, mod time.Time) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(path, mod, mod)
}

func period(t *testing.T, a proto.AgentUsage, hours int) proto.UsagePeriod {
	t.Helper()
	for _, p := range a.Periods {
		if p.Hours == hours {
			return p
		}
	}
	t.Fatalf("no %dh period in %+v", hours, a)
	return proto.UsagePeriod{}
}

func agentOf(t *testing.T, h proto.UsageHistory, name string) proto.AgentUsage {
	t.Helper()
	for _, a := range h.Agents {
		if a.Agent == name {
			return a
		}
	}
	t.Fatalf("no %s in %+v", name, h)
	return proto.AgentUsage{}
}

func noEnv(string) string { return "" }

func TestHistoryClaudeByPeriodAndModel(t *testing.T) {
	home := t.TempDir()
	proj := filepath.Join(home, ".claude", "projects", "-src-api")
	hour := time.Hour
	body := claudeLine("m1", "claude-opus-5-5", histNow.Add(-2*hour), 10, 100, 1000, 5) +
		// The same response again, further along: the latest wins.
		claudeLine("m1", "claude-opus-5-5", histNow.Add(-2*hour), 10, 100, 1000, 50) +
		claudeLine("m2", "claude-sonnet-5-5", histNow.Add(-30*hour), 1, 0, 0, 2) +
		claudeLine("m3", "claude-opus-5-5", histNow.Add(-10*24*hour), 3, 0, 0, 4) +
		claudeLine("m4", "claude-opus-5-5", histNow.Add(-40*24*hour), 999, 0, 0, 999) + // older than 30 days
		claudeLine("m5", "<synthetic>", histNow.Add(-hour), 7, 0, 0, 7) + // not a model
		`{"type":"user","timestamp":"2026-11-04T11:00:00Z"}` + "\n" +
		"not json at all\n" +
		claudeLine("", "claude-opus-5-5", histNow.Add(-hour), 7, 0, 0, 7) // no id
	writeAt(t, filepath.Join(proj, "a.jsonl"), body, histNow.Add(-hour))
	// The same response in another file — a resumed conversation — once.
	writeAt(t, filepath.Join(proj, "b.jsonl"), claudeLine("m1", "claude-opus-5-5", histNow.Add(-2*hour), 10, 100, 1000, 50), histNow.Add(-hour))

	a := agentOf(t, NewHistory().Summary(home, noEnv, histNow), "claude")
	day := period(t, a, 24)
	if day.Input != 10 || day.CacheWrite != 100 || day.CacheRead != 1000 || day.Output != 50 || day.Messages != 1 {
		t.Fatalf("24h %+v", day)
	}
	week := period(t, a, 7*24)
	if week.Input != 11 || week.Output != 52 || week.Messages != 2 || len(week.Models) != 2 {
		t.Fatalf("7d %+v", week)
	}
	if week.Models[0].Model != "claude-opus-5-5" || week.Models[1].Model != "claude-sonnet-5-5" || week.Models[1].Output != 2 {
		t.Fatalf("models, most first: %+v", week.Models)
	}
	month := period(t, a, 30*24)
	if month.Input != 14 || month.Output != 56 || month.Messages != 3 {
		t.Fatalf("30d %+v", month)
	}
}

func TestHistoryEdgesOfAPeriod(t *testing.T) {
	home := t.TempDir()
	proj := filepath.Join(home, ".claude", "projects", "p")
	body := claudeLine("in", "m", histNow.Add(-24*time.Hour), 1, 0, 0, 1) + // exactly 24h ago: in
		claudeLine("out", "m", histNow.Add(-24*time.Hour-time.Second), 2, 0, 0, 2) + // a second more: out
		claudeLine("future", "m", histNow.Add(time.Hour), 4, 0, 0, 4) // a clock ahead: in no period
	writeAt(t, filepath.Join(proj, "a.jsonl"), body, histNow)
	a := agentOf(t, NewHistory().Summary(home, noEnv, histNow), "claude")
	if day := period(t, a, 24); day.Input != 1 {
		t.Fatalf("24h %+v", day)
	}
	if week := period(t, a, 7*24); week.Input != 3 {
		t.Fatalf("7d %+v", week)
	}
}

func TestHistoryCodexCountsTheDifference(t *testing.T) {
	home := t.TempDir()
	h := time.Hour
	body := codexModel("gpt-5.4-codex") +
		codexTotal(histNow.Add(-3*h), 100, 40, 10) +
		codexTotal(histNow.Add(-3*h), 100, 40, 10) + // said again: nothing new
		codexTotal(histNow.Add(-2*h), 250, 90, 30) +
		codexModel("gpt-5.4-mini") +
		codexTotal(histNow.Add(-h), 260, 90, 35) +
		`{"timestamp":"2026-11-04T11:30:00Z","type":"event_msg","payload":{"type":"token_count","info":null}}` + "\n"
	writeAt(t, filepath.Join(home, ".codex", "sessions", "2026", "11", "04", "rollout-1.jsonl"), body, histNow)
	a := agentOf(t, NewHistory().Summary(home, noEnv, histNow), "codex")
	day := period(t, a, 24)
	// 260 input of which 90 cached: 170 fresh, 90 read, 35 out, three turns.
	if day.Input != 170 || day.CacheRead != 90 || day.Output != 35 || day.Messages != 3 {
		t.Fatalf("24h %+v", day)
	}
	if len(day.Models) != 2 || day.Models[0].Model != "gpt-5.4-codex" || day.Models[1].Model != "gpt-5.4-mini" || day.Models[1].Input != 10 {
		t.Fatalf("models %+v", day.Models)
	}
}

func TestHistoryReadsOnlyWhatWasAppended(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".claude", "projects", "p", "a.jsonl")
	writeAt(t, path, claudeLine("m1", "m", histNow.Add(-time.Hour), 1, 0, 0, 1), histNow)
	h := NewHistory()
	if day := period(t, agentOf(t, h.Summary(home, noEnv, histNow), "claude"), 24); day.Input != 1 {
		t.Fatalf("first %+v", day)
	}
	// Appended, and a line still being written.
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(claudeLine("m2", "m", histNow.Add(-time.Hour), 2, 0, 0, 2))
	f.WriteString(`{"type":"assistant","timest`)
	f.Close()
	if day := period(t, agentOf(t, h.Summary(home, noEnv, histNow), "claude"), 24); day.Input != 3 {
		t.Fatalf("after appending %+v", day)
	}
	// Rewritten shorter (compacted): read again from the start.
	writeAt(t, path, claudeLine("m9", "m", histNow.Add(-time.Hour), 5, 0, 0, 5), histNow)
	if day := period(t, agentOf(t, h.Summary(home, noEnv, histNow), "claude"), 24); day.Input != 5 {
		t.Fatalf("after a rewrite %+v", day)
	}
}

func TestHistoryLeavesOldFilesAndEmptyHomes(t *testing.T) {
	home := t.TempDir()
	if got := NewHistory().Summary(home, noEnv, histNow); len(got.Agents) != 0 || !got.At.Equal(histNow) {
		t.Fatalf("an empty home: %+v", got)
	}
	// A file not changed for 40 days is not read at all — even if its
	// lines say otherwise.
	writeAt(t, filepath.Join(home, ".claude", "projects", "p", "old.jsonl"),
		claudeLine("m1", "m", histNow.Add(-time.Hour), 1, 0, 0, 1), histNow.Add(-40*24*time.Hour))
	if got := NewHistory().Summary(home, noEnv, histNow); len(got.Agents) != 0 {
		t.Fatalf("an old file was read: %+v", got)
	}
}

func TestHistoryHonoursTheAgentsOwnFolders(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(t.TempDir(), "claude-cfg")
	cx := filepath.Join(t.TempDir(), "codex-home")
	writeAt(t, filepath.Join(cfg, "projects", "p", "a.jsonl"), claudeLine("m1", "m", histNow.Add(-time.Hour), 1, 0, 0, 1), histNow)
	writeAt(t, filepath.Join(cx, "sessions", "r.jsonl"), codexModel("x")+codexTotal(histNow.Add(-time.Hour), 4, 0, 1), histNow)
	env := func(k string) string {
		switch k {
		case "CLAUDE_CONFIG_DIR":
			return cfg
		case "CODEX_HOME":
			return cx
		}
		return ""
	}
	got := NewHistory().Summary(home, env, histNow)
	if len(got.Agents) != 2 || got.Agents[0].Agent != "claude" || got.Agents[1].Agent != "codex" {
		t.Fatalf("%+v", got)
	}
	if !strings.Contains(fmt.Sprint(got.Agents[1].Periods[0].Models), "x") {
		t.Fatalf("codex model %+v", got.Agents[1].Periods[0].Models)
	}
}

func TestUsageTokensAddAndTotal(t *testing.T) {
	var u proto.UsageTokens
	u.Add(proto.UsageTokens{Input: 1, CacheWrite: 2, CacheRead: 3, Output: 4, Messages: 1})
	u.Add(proto.UsageTokens{Input: 1, Messages: 1})
	if u.Total() != 11 || u.Messages != 2 {
		t.Fatalf("%+v total %d", u, u.Total())
	}
}

// An agent's other accounts keep their conversations in folders of their
// own, and what the agent used here counts them too.
func TestHistoryCountsEveryAccount(t *testing.T) {
	home := t.TempDir()
	work := t.TempDir()
	codexWork := t.TempDir()
	at := histNow.Add(-time.Hour)
	writeAt(t, filepath.Join(home, ".claude", "projects", "-src", "a.jsonl"), claudeLine("m1", "claude-opus-5-5", at, 1, 0, 0, 10), at)
	writeAt(t, filepath.Join(work, "projects", "-src", "b.jsonl"), claudeLine("m2", "claude-opus-5-5", at, 2, 0, 0, 20), at)
	writeAt(t, filepath.Join(codexWork, "sessions", "2026", "11", "04", "r.jsonl"), codexModel("gpt-5")+codexTotal(at, 100, 0, 7), at)
	h := NewHistory()
	got := h.Summary(home, func(string) string { return "" }, histNow,
		Root{Agent: "claude", Dir: work}, Root{Agent: "codex", Dir: codexWork},
		Root{Agent: "claude", Dir: filepath.Join(home, ".claude")}) // the agent's own again: not twice
	if day := period(t, agentOf(t, got, "claude"), 24); day.Output != 30 || day.Messages != 2 {
		t.Errorf("claude across accounts: %+v", day)
	}
	if day := period(t, agentOf(t, got, "codex"), 24); day.Output != 7 {
		t.Errorf("codex on an account: %+v", day)
	}
}

// TestHistoryByAccount: one entry per account with what it used, its last
// 24 hours and 30 days in buckets by model, and the agent's own named "".
func TestHistoryByAccount(t *testing.T) {
	home, work, review := t.TempDir(), t.TempDir(), t.TempDir()
	hourAgo, twoDays, old := histNow.Add(-time.Hour), histNow.Add(-49*time.Hour), histNow.Add(-40*24*time.Hour)
	writeAt(t, filepath.Join(home, ".claude", "projects", "-src", "a.jsonl"),
		claudeLine("m1", "claude-opus-5-5", hourAgo, 1, 0, 0, 10)+claudeLine("m2", "claude-sonnet-5-5", hourAgo.Add(5*time.Minute), 1, 0, 0, 30)+
			claudeLine("m3", "claude-opus-5-5", twoDays, 1, 0, 0, 5), hourAgo)
	writeAt(t, filepath.Join(work, "projects", "-src", "b.jsonl"), claudeLine("m4", "claude-opus-5-5", hourAgo, 2, 0, 0, 20), hourAgo)
	// The same response carried into a second account's file counts once.
	writeAt(t, filepath.Join(work, "projects", "-src", "c.jsonl"), claudeLine("m1", "claude-opus-5-5", hourAgo, 1, 0, 0, 10), hourAgo)
	writeAt(t, filepath.Join(review, "sessions", "2026", "11", "04", "r.jsonl"), codexModel("gpt-5")+codexTotal(hourAgo, 100, 40, 7), hourAgo)
	// An account whose last use is past 30 days is left out.
	idle := t.TempDir()
	writeAt(t, filepath.Join(idle, "projects", "-src", "d.jsonl"), claudeLine("m9", "claude-opus-5-5", old, 1, 0, 0, 99), old)

	got := NewHistory().ByAccount(home, noEnv, histNow, time.UTC,
		Root{Agent: "claude", Dir: work, Name: "work"}, Root{Agent: "codex", Dir: review, Name: "review"},
		Root{Agent: "claude", Dir: idle, Name: "idle"})
	byKey := map[string]proto.AgentUsage{}
	for _, a := range got.Agents {
		byKey[a.Agent+"|"+a.Account] = a
	}
	if len(got.Agents) != 3 || byKey["claude|idle"].Agent != "" {
		t.Fatalf("accounts: %v", func() []string {
			var k []string
			for key := range byKey {
				k = append(k, key)
			}
			return k
		}())
	}
	own, w, r := byKey["claude|"], byKey["claude|work"], byKey["codex|review"]
	if d := period(t, own, 24); d.Output != 40 || d.Messages != 2 {
		t.Errorf("own, a day: %+v", d)
	}
	if wk := period(t, own, 7*24); wk.Output != 45 {
		t.Errorf("own, a week: %+v", wk)
	}
	if d := period(t, w, 24); d.Output != 20 {
		t.Errorf("work counted the response it shares with own: %+v", d)
	}
	if d := period(t, r, 24); d.Output != 7 || d.CacheRead != 40 {
		t.Errorf("codex review: %+v", d)
	}
	// Buckets: 24 hours and 30 days, oldest first, the current one last.
	if len(own.Hours) != 24 || len(own.Days) != 30 {
		t.Fatalf("buckets: %d hours, %d days", len(own.Hours), len(own.Days))
	}
	if !own.Hours[23].Start.Equal(histNow.Truncate(time.Hour)) || !own.Days[29].Start.Equal(time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("last bucket starts %v, %v", own.Hours[23].Start, own.Days[29].Start)
	}
	h := own.Hours[22] // 11:00–12:00
	if h.Output != 40 || len(h.Models) != 2 || h.Models[0].Model != "claude-sonnet-5-5" || h.Models[0].Output != 30 {
		t.Errorf("the hour before now: %+v", h)
	}
	if own.Days[27].Output != 5 || own.Days[29].Output != 40 || own.Days[28].Output != 0 {
		t.Errorf("days: %d %d %d", own.Days[27].Output, own.Days[28].Output, own.Days[29].Output)
	}
	var total int
	for _, b := range own.Hours {
		total += b.Output
	}
	if total != 40 {
		t.Errorf("24 hours of buckets add up to %d, the day says 40", total)
	}
	// Summary, as older clients ask, is unchanged: one entry per agent.
	sum := NewHistory().Summary(home, noEnv, histNow, Root{Agent: "claude", Dir: work, Name: "work"})
	if len(sum.Agents) != 1 || sum.Agents[0].Account != "" || sum.Agents[0].Days != nil || period(t, sum.Agents[0], 24).Output != 60 {
		t.Errorf("summary: %+v", sum.Agents)
	}
	// Nobody has used anything: no entries.
	if e := NewHistory().ByAccount(t.TempDir(), noEnv, histNow, time.UTC); len(e.Agents) != 0 {
		t.Errorf("empty home: %+v", e.Agents)
	}
}

// TestHistoryDaysFollowTheCalendar: a day's bucket starts at local
// midnight, so a day across a change of clocks is still one day, and the
// day a response falls in is the local one.
func TestHistoryDaysFollowTheCalendar(t *testing.T) {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Skip("no tz database:", err)
	}
	// The clocks went back on 2026-11-01 in Los Angeles.
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, loc)
	starts := dayStarts(now, loc, 4)
	for i, want := range []int{31, 1, 2, 3} {
		if starts[i].Day() != want || starts[i].Hour() != 0 {
			t.Errorf("day %d starts %v", i, starts[i])
		}
	}
	if got := starts[1].Sub(starts[0]); got != 24*time.Hour {
		t.Errorf("an ordinary day (Oct 31) is %v", got)
	}
	if got := starts[2].Sub(starts[1]); got != 25*time.Hour {
		t.Errorf("the day the clocks went back (Nov 1) is %v", got)
	}
	// 11pm local on Nov 2 is Nov 3 in UTC, but lands on the 2nd here.
	msg := message{at: time.Date(2026, 11, 2, 23, 0, 0, 0, loc), model: "m", tok: proto.UsageTokens{Output: 1}}
	b := buckets([]message{msg}, starts, now)
	if b[2].Output != 1 || b[3].Output != 0 {
		t.Errorf("11pm on the 2nd: %d %d", b[2].Output, b[3].Output)
	}
	// Before the first bucket, or after now: left out.
	early := message{at: starts[0].Add(-time.Second), model: "m", tok: proto.UsageTokens{Output: 1}}
	late := message{at: now.Add(time.Minute), model: "m", tok: proto.UsageTokens{Output: 1}}
	for _, x := range buckets([]message{early, late}, starts, now) {
		if x.Output != 0 {
			t.Errorf("outside the buckets counted: %+v", x)
		}
	}
}

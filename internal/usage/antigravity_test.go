package usage

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// agyStep is a model step as agy 1.5.16 writes it in a conversation's
// transcript.jsonl.
func agyStep(i int, model string, at time.Time, in, cached, out int) string {
	return fmt.Sprintf(`{"step_index":%d,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":%q,"model":%q,"input_tokens":%d,"cache_read_tokens":%d,"output_tokens":%d,"content":"pong"}`,
		i, at.Format(time.RFC3339), model, in, cached, out) + "\n"
}

func agyUser(i int, at time.Time) string {
	return fmt.Sprintf(`{"step_index":%d,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":%q,"content":"<USER_REQUEST>\nhi\n</USER_REQUEST>"}`,
		i, at.Format(time.RFC3339)) + "\n"
}

func agyLog(home, id string) string {
	return filepath.Join(home, ".gemini", "antigravity-cli", "brain", id, ".system_generated", "logs")
}

func TestHistoryAntigravity(t *testing.T) {
	home := t.TempDir()
	hour := time.Hour
	body := agyUser(0, histNow.Add(-2*hour)) +
		agyStep(1, "gemini-4-hydrogen-high", histNow.Add(-2*hour), 1000, 400, 10) +
		// The same step written again, further along: the latest wins.
		agyStep(1, "gemini-4-hydrogen-high", histNow.Add(-2*hour), 1200, 400, 20) +
		agyStep(3, "gemini-4-flash", histNow.Add(-30*hour), 50, 0, 5) +
		agyStep(4, "gemini-4-flash", histNow.Add(-40*24*hour), 999, 0, 999) + // older than 30 days
		agyStep(5, "", histNow.Add(-hour), 7, 0, 7) + // no model
		// agy counts cached tokens apart from input, as a real second turn
		// did (2,309 in and 9,579 cached for a prompt of ~11.9k): more
		// cached than input is the usual case, not a fault.
		agyStep(6, "gemini-4-flash", histNow.Add(-hour), 10, 30, 1) +
		"not json\n" +
		`{"step_index":7,"type":"RUN_COMMAND","created_at":"2026-11-04T11:00:00Z","input_tokens":5}` + "\n"
	logs := agyLog(home, "c1")
	writeAt(t, filepath.Join(logs, "transcript.jsonl"), body, histNow.Add(-hour))
	// The same steps again beside it and in chunks: counted once, from
	// transcript.jsonl.
	writeAt(t, filepath.Join(logs, "transcript_full.jsonl"), body, histNow.Add(-hour))
	writeAt(t, filepath.Join(logs, "chunks", "transcript", "00000000.jsonl"), body, histNow.Add(-hour))
	// A second conversation, a subagent's, spends its own.
	writeAt(t, filepath.Join(agyLog(home, "c2"), "transcript.jsonl"), agyStep(1, "gemini-4-flash", histNow.Add(-3*hour), 100, 0, 3), histNow.Add(-hour))
	// A conversation untouched for longer than the longest period.
	writeAt(t, filepath.Join(agyLog(home, "old"), "transcript.jsonl"), agyStep(1, "gemini-4-flash", histNow.Add(-hour), 5, 0, 5), histNow.Add(-40*24*hour))

	got := NewHistory().Summary(home, noEnv, histNow)
	a := agentOf(t, got, "antigravity")
	day := period(t, a, 24)
	// 1200 + 10 + 100 in; 400 + 30 cached; 20 + 1 + 3 out.
	if day.Input != 1200+10+100 || day.CacheRead != 430 || day.Output != 24 || day.Messages != 3 {
		t.Errorf("24h: %+v", day.UsageTokens)
	}
	if len(day.Models) != 2 || day.Models[0].Model != "gemini-4-hydrogen-high" {
		t.Errorf("models: %+v", day.Models)
	}
	week := period(t, a, 7*24)
	if week.Output != 29 || week.Messages != 4 {
		t.Errorf("week: %+v", week.UsageTokens)
	}
	if month := period(t, a, 30*24); month.Output != 29 {
		t.Errorf("month: %+v", month.UsageTokens)
	}
}

func TestHistoryAntigravityReadsOnlyWhatWasAppended(t *testing.T) {
	home := t.TempDir()
	f := filepath.Join(agyLog(home, "c1"), "transcript.jsonl")
	writeAt(t, f, agyStep(1, "m", histNow.Add(-time.Hour), 10, 0, 1), histNow.Add(-time.Hour))
	h := NewHistory()
	if d := period(t, agentOf(t, h.Summary(home, noEnv, histNow), "antigravity"), 24); d.Output != 1 {
		t.Fatalf("%+v", d)
	}
	// A step being written: not counted until its line is whole.
	fh, err := os.OpenFile(f, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	line := agyStep(3, "m", histNow.Add(-time.Hour), 10, 0, 2)
	fh.WriteString(line[:20])
	if d := period(t, agentOf(t, h.Summary(home, noEnv, histNow), "antigravity"), 24); d.Output != 1 {
		t.Fatalf("half a line counted: %+v", d)
	}
	fh.WriteString(line[20:])
	fh.Close()
	if d := period(t, agentOf(t, h.Summary(home, noEnv, histNow), "antigravity"), 24); d.Output != 3 || d.Messages != 2 {
		t.Fatalf("appended: %+v", d)
	}
}

func TestHistoryAntigravityNothingSaved(t *testing.T) {
	home := t.TempDir()
	// No folder, then a conversation with no model step yet.
	for _, h := range []string{home, home} {
		for _, a := range NewHistory().Summary(h, noEnv, histNow).Agents {
			if a.Agent == "antigravity" {
				t.Fatalf("listed with nothing used: %+v", a)
			}
		}
		writeAt(t, filepath.Join(agyLog(home, "c1"), "transcript.jsonl"), agyUser(0, histNow.Add(-time.Hour)), histNow.Add(-time.Hour))
	}
}

// An account of agy's is a folder given as --gemini_dir: what it used
// counts as agy's, and on its own line by account.
func TestHistoryAntigravityAccounts(t *testing.T) {
	home := t.TempDir()
	work := filepath.Join(t.TempDir(), "work")
	writeAt(t, filepath.Join(agyLog(home, "own"), "transcript.jsonl"), agyStep(1, "m", histNow.Add(-time.Hour), 10, 0, 1), histNow.Add(-time.Hour))
	writeAt(t, filepath.Join(work, "antigravity-cli", "brain", "w1", ".system_generated", "logs", "transcript.jsonl"), agyStep(1, "m", histNow.Add(-time.Hour), 20, 0, 2), histNow.Add(-time.Hour))
	others := []Root{{Agent: "antigravity", Dir: work, Name: "work"}, {Agent: "antigravity", Dir: work, Name: "again"}}

	if d := period(t, agentOf(t, NewHistory().Summary(home, noEnv, histNow, others...), "antigravity"), 24); d.Output != 3 || d.Messages != 2 {
		t.Errorf("summary: %+v", d.UsageTokens)
	}
	by := map[string]int{}
	for _, a := range NewHistory().ByAccount(home, noEnv, histNow, time.UTC, others...).Agents {
		if a.Agent == "antigravity" {
			by[a.Account] += period(t, a, 24).Output
		}
	}
	if len(by) != 2 || by[""] != 1 || by["work"] != 2 {
		t.Errorf("by account: %v", by)
	}
}

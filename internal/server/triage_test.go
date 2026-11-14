package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/ghx"
	"github.com/Amitgb14/conch/internal/proto"
)

// triageGH is a gh whose labelled list is what setIssues last said (each
// issue with its own labels, newest first as gh lists them), whose issue
// view gives a body, and which logs every call.
func triageGH(t *testing.T) (setIssues func(map[int][]string, ...int), log func() string) {
	t.Helper()
	dir := t.TempDir()
	list, calls := filepath.Join(dir, "issues.json"), filepath.Join(dir, "calls")
	gh := filepath.Join(dir, "gh")
	os.WriteFile(gh, []byte(`#!/bin/sh
echo "$*" >> `+calls+`
case "$1 $2" in
"issue list") cat `+list+` ;;
"issue view") echo "{\"number\": $3, \"title\": \"thing $3\", \"body\": \"It breaks when $3.\", \"url\": \"https://x/$3\", \"labels\": []}" ;;
"issue comment"|"issue edit"|"label create") ;;
"pr list") echo '[]' ;;
"api graphql") echo '{}' ;;
*) exit 7 ;;
esac
`), 0o755)
	t.Setenv("CONCH_GH", gh)
	setIssues = func(labels map[int][]string, numbers ...int) {
		var out []map[string]any
		for _, n := range numbers {
			ls := []map[string]string{{"name": "new"}}
			for _, l := range labels[n] {
				ls = append(ls, map[string]string{"name": l})
			}
			out = append(out, map[string]any{"number": n, "title": "thing " + strconv.Itoa(n), "url": "https://x/" + strconv.Itoa(n), "labels": ls})
		}
		raw, _ := json.Marshal(out)
		if out == nil {
			raw = []byte("[]")
		}
		os.WriteFile(list, raw, 0o644)
	}
	setIssues(nil)
	log = func() string { b, _ := os.ReadFile(calls); return string(b) }
	return setIssues, log
}

func triageOn(s *Server, id string, commentOnly bool) {
	s.autopilot.set(proto.AutopilotPolicy{Triage: map[string]string{id: "new"}, Intake: map[string]string{id: "go"}, TriageCommentOnly: commentOnly})
}

// triaging is the triage agent's pane, if one is running.
func triaging(s *Server) (proto.PaneInfo, bool) {
	for _, info := range s.list() {
		if proto.IsTriager(info) && info.State == proto.PaneRunning {
			return info, true
		}
	}
	return proto.PaneInfo{}, false
}

func writeVerdict(t *testing.T, s *Server, id, raw string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(s.triageDir(id), verdictFile), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTriageReadsOneIssueAtATimeAndPostsTheVerdict(t *testing.T) {
	s, p := afterFixture(t)
	quietPRs(t, p)
	setIssues, ghLog := triageGH(t)
	setIssues(nil, 5, 4)
	triageOn(s, p.id, false)

	s.triageOnce()
	info, ok := triaging(s)
	if !ok || info.Name != proto.TriagePrefix+"#4" {
		t.Fatalf("not reading the oldest: %+v", s.list())
	}
	// It reads in a checkout of its own, at the base, on no branch.
	dir := s.triageDir(p.id)
	if !within(info.Cwd, dir) && info.Cwd != dir {
		t.Fatalf("reads in %s, want %s", info.Cwd, dir)
	}
	if head := apGit(t, dir, "rev-parse", "--abbrev-ref", "HEAD"); head != "HEAD" {
		t.Fatalf("the triage checkout is on branch %q", head)
	}
	// Still reading: nothing else starts.
	s.triageOnce()
	if n := countTriagers(s); n != 1 {
		t.Fatalf("%d triaging at once", n)
	}

	writeVerdict(t, s, p.id, `{"verdict":"ready","size":"S","comment":"**Done means**\n- a test that breaks when 4"}`)
	s.triageOnce()
	calls := ghLog()
	if !strings.Contains(calls, "issue comment 4 --body 🤖 **conch triage** · read by claude, which changed nothing") ||
		!strings.Contains(calls, "a test that breaks when 4") || !strings.Contains(calls, "Verdict: **ready** · size S") {
		t.Fatalf("comment:\n%s", calls)
	}
	// ready is intake's label, so intake takes it next; the triage label goes.
	if !strings.Contains(calls, "label create go") || !strings.Contains(calls, "issue edit 4 --add-label go --remove-label new") {
		t.Fatalf("labels:\n%s", calls)
	}
	if _, ok := triaging(s); ok {
		t.Fatal("the agent was left running")
	}
	if r, _ := s.triage.get(p.id, 4); r.Verdict != "ready" || r.Pane != "" || r.Ended.IsZero() {
		t.Fatalf("record %+v", r)
	}
	if _, err := os.Stat(filepath.Join(dir, verdictFile)); err == nil {
		t.Fatal("the verdict file was left")
	}

	// The next issue, and a verdict that does not read: nothing posted.
	s.triageOnce()
	if info, ok := triaging(s); !ok || info.Name != proto.TriagePrefix+"#5" {
		t.Fatal("did not go on to #5")
	}
	writeVerdict(t, s, p.id, `{"verdict":"maybe","comment":"x"}`)
	s.triageOnce()
	if r, _ := s.triage.get(p.id, 5); !strings.Contains(r.Failed, "did not read") || strings.Contains(ghLog(), "issue comment 5") {
		t.Fatalf("a bad verdict: %+v\n%s", r, ghLog())
	}
	// Neither is read again while still labelled.
	s.triageOnce()
	if _, ok := triaging(s); ok {
		t.Fatal("read an issue twice")
	}
	// A restart remembers.
	s.triage = loadTriage(s.configDir)
	s.triageOnce()
	if _, ok := triaging(s); ok {
		t.Fatal("a fresh server read an issue again")
	}
	// Relabelled — the label off, then on again — it is read again.
	setIssues(nil)
	s.triageOnce()
	setIssues(nil, 5)
	s.triageOnce()
	if info, ok := triaging(s); !ok || info.Name != proto.TriagePrefix+"#5" {
		t.Fatal("relabelled, not read again")
	}
}

func countTriagers(s *Server) int {
	n := 0
	for _, info := range s.list() {
		if proto.IsTriager(info) && info.State == proto.PaneRunning {
			n++
		}
	}
	return n
}

func TestTriageVerdictsAndGivingUp(t *testing.T) {
	s, p := afterFixture(t)
	quietPRs(t, p)
	setIssues, ghLog := triageGH(t)
	// #3 was labelled by a person already: left alone.
	setIssues(map[int][]string{3: {"needs-info"}}, 7, 3)
	triageOn(s, p.id, false)

	s.triageOnce()
	if r, _ := s.triage.get(p.id, 3); !strings.Contains(r.Verdict, "already labelled needs-info") {
		t.Fatalf("#3: %+v", r)
	}
	info, ok := triaging(s)
	if !ok || info.Name != proto.TriagePrefix+"#7" {
		t.Fatal("did not read #7")
	}
	writeVerdict(t, s, p.id, `{"verdict":"needs-info","comment":"Which part: listing or resuming?"}`)
	s.triageOnce()
	calls := ghLog()
	if !strings.Contains(calls, "issue edit 7 --add-label needs-info --remove-label new") || !strings.Contains(calls, "label it again to have it read again") {
		t.Fatalf("needs-info:\n%s", calls)
	}

	// An agent that ends without a verdict: given up, the issue untouched.
	setIssues(nil, 8)
	s.triageOnce()
	info, _ = triaging(s)
	s.close(info.ID)
	a5WaitFor(t, "the agent gone", func() bool { _, ok := triaging(s); return !ok })
	s.triageOnce()
	if r, _ := s.triage.get(p.id, 8); r.Failed != "the agent ended without a verdict" || strings.Contains(ghLog(), "issue comment 8") {
		t.Fatalf("#8: %+v", r)
	}

	// One that takes too long: closed.
	setIssues(nil, 9)
	s.triageOnce()
	was := triageTimeout
	triageTimeout = 0
	t.Cleanup(func() { triageTimeout = was })
	s.triageOnce()
	if r, _ := s.triage.get(p.id, 9); !strings.Contains(r.Failed, "no verdict after") {
		t.Fatalf("#9: %+v", r)
	}
	if _, ok := triaging(s); ok {
		t.Fatal("a timed-out agent was left running")
	}
}

func TestTriageCommentOnly(t *testing.T) {
	s, p := afterFixture(t)
	quietPRs(t, p)
	setIssues, ghLog := triageGH(t)
	setIssues(nil, 2)
	triageOn(s, p.id, true)
	s.triageOnce()
	writeVerdict(t, s, p.id, `{"verdict":"too-big","comment":"Split: the API, then the UI."}`)
	s.triageOnce()
	calls := ghLog()
	if !strings.Contains(calls, "issue comment 2") || strings.Contains(calls, "issue edit") || strings.Contains(calls, "label create") {
		t.Fatalf("comment only:\n%s", calls)
	}
	// Still not read again, though its label stays.
	s.triageOnce()
	if _, ok := triaging(s); ok {
		t.Fatal("read again while still labelled")
	}
}

func TestTriageIsOffWithoutALabel(t *testing.T) {
	s, p := afterFixture(t)
	quietPRs(t, p)
	setIssues, ghLog := triageGH(t)
	setIssues(nil, 1)
	s.autopilot.set(proto.AutopilotPolicy{Intake: map[string]string{p.id: "go"}})
	s.triageOnce()
	if strings.Contains(ghLog(), "issue") {
		t.Fatalf("read issues with no triage label:\n%s", ghLog())
	}
}

func TestTriageText(t *testing.T) {
	c := triageComment(triageVerdict{Verdict: "ready", Size: "M", Comment: "  **Done means**\n- x  "}, "codex")
	if !strings.HasPrefix(c, "🤖 **conch triage** · read by codex, which changed nothing\n\n**Done means**\n- x\n\n---\nVerdict: **ready** · size M") || strings.Contains(c, "label it again") {
		t.Fatalf("%q", c)
	}
	big := strings.Repeat("é", maxIssueBody)
	pr := triagePrompt(ghx.Issue{Number: 12, Title: "Make sessions faster", Body: big}, "/tmp/t/.conch-triage.json")
	for _, want := range []string{"Triage GitHub issue #12", "You only read", "Make sessions faster", "/tmp/t/.conch-triage.json", `"verdict": "ready" | "needs-info" | "too-big"`, "the rest of the issue is on GitHub"} {
		if !strings.Contains(pr, want) {
			t.Fatalf("prompt lacks %q", want)
		}
	}
	if !validVerdict("ready") || !validVerdict("needs-info") || !validVerdict("too-big") || validVerdict("") || validVerdict("Ready") {
		t.Fatal("verdicts")
	}
	// A triager holds no queue, as a reviewer does not.
	if holdsQueue(proto.PaneInfo{Name: proto.TriagePrefix + "#1", State: proto.PaneRunning, Created: time.Now(),
		Agent: &proto.AgentStatus{State: proto.AgentWorking}}, true, time.Now()) {
		t.Fatal("a triager holds the queue")
	}
	r := triageRecord{Pane: "p1"}
	if !r.running() {
		t.Fatal("running")
	}
	r.Ended = time.Now()
	if r.running() {
		t.Fatal("ended")
	}
}

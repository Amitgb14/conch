package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// What the autopilot did is kept, newest first, each with when it was and
// what kind of thing it was; asked for by project, a project gets its own.
func TestActivityKept(t *testing.T) {
	dir := t.TempDir()
	l := loadActivity(dir)
	before := time.Now()
	l.add(proto.AutopilotNote{ProjectID: "a", Branch: "x", Kind: proto.ActivityCI, Text: "one"})
	l.add(proto.AutopilotNote{ProjectID: "b", Branch: "y", Kind: proto.ActivityMerge, Text: "two", Merged: true})
	l.add(proto.AutopilotNote{ProjectID: "a", Branch: "z", Kind: proto.ActivityResolve, Text: "three", Error: true})
	got := l.list("a", 0)
	if len(got) != 2 || got[0].Text != "three" || got[1].Text != "one" {
		t.Fatalf("project a: %+v", got)
	}
	if got[0].At.Before(before) || got[0].Kind != proto.ActivityResolve || !got[0].Error {
		t.Errorf("stamped: %+v", got[0])
	}
	if all := l.list("", 0); len(all) != 3 || all[0].Text != "three" {
		t.Errorf("every project: %+v", all)
	}
	if one := l.list("", 1); len(one) != 1 || one[0].Text != "three" {
		t.Errorf("limit 1: %+v", one)
	}
	// A restart keeps it.
	again := loadActivity(dir).list("b", 0)
	if len(again) != 1 || !again[0].Merged || again[0].Kind != proto.ActivityMerge {
		t.Fatalf("after a restart: %+v", again)
	}
}

// The log is bounded in memory and on disk, and the newest stay.
func TestActivityCapped(t *testing.T) {
	dir := t.TempDir()
	l := loadActivity(dir)
	for i := range activityMax*2 + 10 {
		l.add(proto.AutopilotNote{ProjectID: "a", Text: fmt.Sprint(i)})
	}
	got := l.list("", activityMax+50)
	if len(got) != activityMax || got[0].Text != fmt.Sprint(activityMax*2+9) {
		t.Fatalf("kept %d, newest %q", len(got), got[0].Text)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, activityFile))
	if n := strings.Count(string(raw), "\n"); n > activityMax*2 {
		t.Errorf("%d lines on disk", n)
	}
	if again := loadActivity(dir).list("", activityMax); len(again) != activityMax || again[0].Text != got[0].Text {
		t.Errorf("reloaded %d, newest %q", len(again), again[0].Text)
	}
}

// A damaged line — a write cut short, a hand edit — loses that line and
// nothing else; a missing file is an empty log.
func TestActivityDamagedFile(t *testing.T) {
	dir := t.TempDir()
	if got := loadActivity(dir).list("", 0); len(got) != 0 {
		t.Fatalf("no file: %+v", got)
	}
	good := `{"project_id":"a","branch":"x","text":"kept","at":"2026-11-14T10:00:00Z"}`
	os.WriteFile(filepath.Join(dir, activityFile), []byte(good+"\n{\"project_id\":\"a\",\"tex\n"+good+"\n"), 0o600)
	l := loadActivity(dir)
	if got := l.list("a", 0); len(got) != 2 || got[0].Text != "kept" {
		t.Fatalf("damaged: %+v", got)
	}
	// Writing on after a damaged line starts a line of its own.
	l.add(proto.AutopilotNote{ProjectID: "a", Text: "new"})
	if got := loadActivity(dir).list("a", 0); len(got) != 3 || got[0].Text != "new" {
		t.Errorf("after writing on: %+v", got)
	}
}

// The autopilot's own notes land in the log with their kind: a pull
// request held, then merged.
func TestActivityFromTheAutopilot(t *testing.T) {
	s, p := afterFixture(t)
	quietPRs(t, p)
	fakeMergeGH(t)
	s.autopilot.set(proto.AutopilotPolicy{AutoMerge: true})
	head := apBranch(t, p, "feat")
	apPR(s, p, "feat", proto.PRInfo{Number: 5, Checks: "pending", Draft: true, Head: head})
	apPR(s, p, "feat", proto.PRInfo{Number: 5, Checks: "pass", Draft: true, Head: head})
	p.mu.Lock()
	pr := p.prs["feat"]
	pr.Draft = false
	p.prs["feat"] = pr
	p.mu.Unlock()
	s.autopilotMerges()
	a5WaitFor(t, "the merge in the log", func() bool {
		got := s.autopilotActivity(proto.AutopilotActivityParams{ProjectID: p.id}).Notes
		return len(got) >= 2 && got[0].Merged && got[0].Kind == proto.ActivityMerge && got[0].Branch == "feat"
	})
	got := s.autopilotActivity(proto.AutopilotActivityParams{ProjectID: p.id}).Notes
	if !strings.Contains(got[1].Text, "draft") || got[1].Kind != proto.ActivityMerge {
		t.Errorf("the hold: %+v", got[1])
	}
}

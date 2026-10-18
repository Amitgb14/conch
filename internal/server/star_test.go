package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// A star is conch's own mark on somebody else's file, so it is kept here
// and has to survive a restart: the point of starring a conversation is
// finding it next week.
func TestStarsAreKeptAndReload(t *testing.T) {
	dir := t.TempDir()
	b := loadStars(dir)
	if b.has("claude", "s1") {
		t.Fatal("a fresh book had a star in it")
	}

	changed, err := b.set("claude", "s1", true)
	if err != nil || !changed {
		t.Fatalf("starring: %v %v", changed, err)
	}
	// Starring the same one again changes nothing, so the file is not
	// rewritten for a keystroke that did not move anything.
	if changed, _ := b.set("claude", "s1", true); changed {
		t.Fatal("starring twice counted as a change")
	}
	// Two agents can hold one id, and they are different conversations.
	if _, err := b.set("codex", "s1", true); err != nil {
		t.Fatal(err)
	}
	if !b.has("claude", "s1") || !b.has("codex", "s1") || b.has("gemini", "s1") {
		t.Fatal("stars are per agent and id")
	}

	// It survives a restart, which is the whole point.
	again := loadStars(dir)
	if !again.has("claude", "s1") || !again.has("codex", "s1") {
		t.Fatal("stars did not come back")
	}
	if _, err := os.Stat(filepath.Join(dir, "stars.json")); err != nil {
		t.Fatalf("stars.json: %v", err)
	}

	// Unstarring takes it away, here and after a restart.
	if changed, _ := again.set("claude", "s1", false); !changed {
		t.Fatal("unstarring did nothing")
	}
	if loadStars(dir).has("claude", "s1") {
		t.Fatal("it came back")
	}

	// A session is an agent and an id; neither alone is one.
	for _, c := range [][2]string{{"", "s1"}, {"claude", ""}} {
		if _, err := b.set(c[0], c[1], true); err == nil {
			t.Fatalf("%q/%q was accepted", c[0], c[1])
		}
	}
	// A file somebody has mangled loads as no stars rather than failing
	// to start the server.
	os.WriteFile(filepath.Join(dir, "stars.json"), []byte("{not json"), 0o600)
	if n := len(loadStars(dir).on); n != 0 {
		t.Fatalf("a broken file gave %d stars", n)
	}
}

// mark says which of a list are starred, and the order puts them where
// they can be found: interrupted work first, then stars, then recency.
func TestStarsMarkAndSort(t *testing.T) {
	dir := t.TempDir()
	b := loadStars(dir)
	b.set("claude", "old", true)

	now := time.Now()
	list := []proto.SessionInfo{
		{Agent: "claude", ID: "new", Updated: now},
		{Agent: "claude", ID: "old", Updated: now.Add(-48 * time.Hour)},
		{Agent: "claude", ID: "stopped", Updated: now.Add(-72 * time.Hour), Interrupted: true},
		{Agent: "claude", Updated: now.Add(-time.Hour)}, // no id: nothing to star
	}
	b.mark(list)
	starred := map[string]bool{}
	for _, s := range list {
		starred[s.ID] = s.Starred
	}
	if !starred["old"] || starred["new"] || starred[""] {
		t.Fatalf("marked %v", starred)
	}
	sortSessions(list)
	var order []string
	for _, s := range list {
		order = append(order, s.ID)
	}
	// stopped (interrupted), old (starred), then the rest by recency.
	if got := order[0] + "," + order[1] + "," + order[2]; got != "stopped,old,new" {
		t.Fatalf("order %v", order)
	}
}

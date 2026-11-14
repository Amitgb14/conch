package server

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Saves made at once — a merge's tick, a client's edit — come out as the
// newest state, whole, with nothing left beside the file.
func TestGroupsSavedOneAtATime(t *testing.T) {
	a5IsolateEnv(t)
	s, _ := a5Server(t)
	g := s.groups
	for round := 0; round < 20; round++ {
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				g.mu.Lock()
				g.groups = append(g.groups, savedGroup{Name: fmt.Sprintf("g%d-%d", round, i), Projects: []string{"/x"}})
				g.mu.Unlock()
				g.changed()
			}()
		}
		wg.Wait()
		raw, err := os.ReadFile(g.file)
		if err != nil {
			t.Fatal(err)
		}
		var f groupsFile
		if err := json.Unmarshal(raw, &f); err != nil {
			t.Fatalf("round %d: %v\n%s", round, err, raw)
		}
		if want := (round + 1) * 8; len(f.Groups) != want {
			t.Fatalf("round %d: %d groups saved, want %d", round, len(f.Groups), want)
		}
	}
	ents, _ := os.ReadDir(filepath.Dir(g.file))
	for _, e := range ents {
		if strings.Contains(e.Name(), "groups") && e.Name() != "groups.json" {
			t.Fatalf("left behind: %s", e.Name())
		}
	}
}

// A groups.json that cannot be read is said, and kept aside rather than
// overwritten with no groups by the next save.
func TestGroupsBrokenFileKeptAside(t *testing.T) {
	a5IsolateEnv(t)
	s, dir := a5Server(t)
	file := filepath.Join(dir, "groups.json")
	if err := os.WriteFile(file, []byte(`{"groups":[{"name":"ab","projects":["/a"`), 0o600); err != nil {
		t.Fatal(err)
	}
	var logged strings.Builder
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	g := loadGroups(s, dir)
	log.SetOutput(os.Stderr)
	if len(g.groups) != 0 || !strings.Contains(logged.String(), "groups.json cannot be read") {
		t.Fatalf("groups %v, log %q", g.groups, logged.String())
	}
	g.changed()
	if b, err := os.ReadFile(file + ".broken"); err != nil || !strings.Contains(string(b), `"ab"`) {
		t.Fatalf("the broken file was not kept: %v %s", err, b)
	}
}

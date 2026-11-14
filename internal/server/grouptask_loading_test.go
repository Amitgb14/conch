package server

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// A group task asked for while the server is still reading its projects
// at start waits for them rather than saying the group has no repo
// (#199: the same command worked seconds later).
func TestGroupTaskWaitsForProjectsLoading(t *testing.T) {
	s, api := afterFixture(t)
	root := api.root
	// As at start: the catalog not read yet, the project not registered.
	s.projects.mu.Lock()
	delete(s.projects.projects, api.id)
	s.projects.order = nil
	s.projects.mu.Unlock()
	loading := s.projects.startLoading()
	s.groups.mu.Lock()
	s.groups.groups = append(s.groups.groups, savedGroup{Name: "clinic", Projects: []string{root}})
	s.groups.mu.Unlock()

	go func() {
		time.Sleep(200 * time.Millisecond)
		if _, err := s.projects.add(root, false); err != nil {
			t.Error(err)
		}
		loading()
	}()
	_, perr := s.groups.task(proto.GroupTaskParams{Group: "clinic", Prompt: "x", Branch: "feat", Agent: "no-such-agent"})
	if perr == nil || strings.Contains(perr.Message, "no repo") {
		t.Fatalf("asked before the projects were read: %v", perr)
	}
	if !strings.Contains(perr.Message, "no-such-agent") {
		t.Fatalf("want the task to get as far as its agent: %v", perr)
	}
}

// Loading that takes longer than a caller should wait is said as loading,
// not as a group without repos.
func TestGroupTaskSaysStillLoading(t *testing.T) {
	s, _ := afterFixture(t)
	old := projectsLoadWait
	projectsLoadWait = 50 * time.Millisecond
	t.Cleanup(func() { projectsLoadWait = old })
	loading := s.projects.startLoading()
	t.Cleanup(loading)
	s.groups.mu.Lock()
	s.groups.groups = append(s.groups.groups, savedGroup{Name: "clinic", Projects: []string{filepath.Join(s.configDir, "elsewhere")}})
	s.groups.mu.Unlock()
	_, perr := s.groups.task(proto.GroupTaskParams{Group: "clinic", Prompt: "x"})
	if perr == nil || !strings.Contains(perr.Message, "still loading") {
		t.Fatalf("want still loading: %v", perr)
	}
	// Once loaded, a group whose repos are gone has none.
	loading()
	_, perr = s.groups.task(proto.GroupTaskParams{Group: "clinic", Prompt: "x"})
	if perr == nil || !strings.Contains(perr.Message, "no repo") {
		t.Fatalf("want no repo once loaded: %v", perr)
	}
}

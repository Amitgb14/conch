package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

// A task's folder is in no project, so the project rule let every agent
// through: any could rewrite another group's CONCH-TASK.md or delete its
// links. It is now the task's agents' — the ones it started, what they
// start, whoever started them — and the agents of its repos, which may
// change those repos' worktrees on the task's branch already.
func TestTaskFolderScopedToItsTask(t *testing.T) {
	a5IsolateEnv(t)
	s, dir := a5Server(t)
	api := filesRepo(t, s, dir)
	other := filepath.Join(dir, "other")
	a5GitRepo(t, other)
	if _, err := s.projects.add(other, false); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(dir, "tasks", "clinic", "feat")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	brief := filepath.Join(folder, "CONCH-TASK.md")
	filesWrite(t, folder, "CONCH-TASK.md", "# the task\n")
	if err := os.Symlink(api, filepath.Join(folder, "repo")); err != nil {
		t.Fatal(err)
	}
	s.groups.mu.Lock()
	s.groups.groups = append(s.groups.groups, savedGroup{Name: "clinic", Projects: []string{api},
		Tasks: []savedTask{{Branch: "feat", Folder: folder, Projects: []string{api}, Panes: []string{"p1"}}}})
	s.groups.mu.Unlock()

	// p1 the task's agent, started by p5; p2 started by p1; p3 nobody's.
	for _, id := range []string{"p1", "p2", "p3", "p5"} {
		agentPane(t, s, id, "claude", dir, "exec sleep 30")
	}
	s.madeBy(&client{pane: "p5"}, "p1")
	s.madeBy(&client{pane: "p1"}, "p2")
	far := &client{}
	far.actFor = proto.ActForParams{ID: "laptop/p4@1", Label: "p4 on laptop", Agent: "codex"}

	apiID, otherID := projectID(api), projectID(other)
	for _, c := range []struct {
		name   string
		caller scopeCaller
		ok     bool
	}{
		{"the task's agent", scopeCaller{id: "p1"}, true},
		{"what it started", scopeCaller{id: "p2"}, true},
		{"what started it", scopeCaller{id: "p5"}, true},
		{"an agent of one of its repos", scopeCaller{id: "p3", project: apiID}, true},
		{"an agent of another repo", scopeCaller{id: "p3", project: otherID}, false},
		{"an agent in no project", scopeCaller{id: "p3"}, false},
		{"another machine's agent", scopeCaller{id: "laptop/p9@1", remote: true}, false},
	} {
		perr := s.checkoutInScope(c.caller, proto.MethodFSWrite, folder)
		if (perr == nil) != c.ok {
			t.Errorf("%s: %v, want allowed %v", c.name, perr, c.ok)
		}
		if perr != nil && (perr.Code != proto.ErrOutOfScope || !strings.Contains(perr.Message, "it is not working on that task")) {
			t.Errorf("%s: %v", c.name, perr)
		}
	}
	// One on another machine that started an agent of the task is its too.
	agentPane(t, s, "p6", "claude", dir, "exec sleep 30")
	s.madeBy(far, "p6")
	s.groups.mu.Lock()
	s.groups.groups[0].Tasks[0].Panes = append(s.groups.groups[0].Tasks[0].Panes, "p6")
	s.groups.mu.Unlock()
	if perr := s.checkoutInScope(scopeCaller{id: "laptop/p4@1", remote: true}, proto.MethodFSWrite, folder); perr != nil {
		t.Errorf("another machine's agent that started it: %v", perr)
	}

	// Through the methods: p3, an agent working on nothing of the task's,
	// rewrites nothing and deletes nothing; the task's own agent may.
	stranger, own := &client{pane: "p3"}, &client{pane: "p1"}
	if _, perr := s.writeFile(stranger, proto.FSWriteParams{Root: folder, Path: "CONCH-TASK.md", Data: "# mine now\n", Force: true}); perr == nil || perr.Code != proto.ErrOutOfScope {
		t.Fatalf("a stranger's write: %v", perr)
	}
	if _, perr := s.deletePath(stranger, proto.FSPathParams{Root: folder, Path: "repo"}); perr == nil || perr.Code != proto.ErrOutOfScope {
		t.Fatalf("a stranger's delete: %v", perr)
	}
	if b, _ := os.ReadFile(brief); string(b) != "# the task\n" {
		t.Fatalf("the brief was changed: %q", b)
	}
	if _, err := os.Lstat(filepath.Join(folder, "repo")); err != nil {
		t.Fatalf("the link was deleted: %v", err)
	}
	if _, perr := s.writeFile(own, proto.FSWriteParams{Root: folder, Path: "CONCH-TASK.md", Data: "# done\n", Force: true}); perr != nil {
		t.Fatalf("the task's agent: %v", perr)
	}
	// And the person, unscoped, as ever.
	if _, perr := s.writeFile(&client{}, proto.FSWriteParams{Root: folder, Path: "CONCH-TASK.md", Data: "# mine\n", Force: true}); perr != nil {
		t.Fatalf("the person: %v", perr)
	}
}

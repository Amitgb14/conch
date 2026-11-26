package server_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// removeRig is a server with a project, two panes working in it and one
// outside it.
func removeRig(t *testing.T) (c *client.Client, proj proto.ProjectInfo, in []string, out string) {
	t.Helper()
	a5Env(t)
	c, dir := startServer(t)
	ctx := a5Ctx(t)
	repo := filepath.Join(dir, "api")
	os.MkdirAll(repo, 0o755)
	git(t, repo, "init", "-q", "-b", "main")
	git(t, repo, "config", "user.name", "t")
	git(t, repo, "config", "user.email", "t@example.com")
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
	if err := c.Call(ctx, proto.MethodProjectAdd, proto.ProjectAddParams{Path: repo}, &proj); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		var p proto.PaneInfo
		if err := c.Call(ctx, proto.MethodPaneCreate, proto.PaneCreateParams{Command: []string{"/bin/sleep", "30"}, Cwd: repo}, &p); err != nil {
			t.Fatal(err)
		}
		if p.ProjectID != proj.ID {
			t.Fatalf("a pane in the project is in %q", p.ProjectID)
		}
		in = append(in, p.ID)
	}
	var loose proto.PaneInfo
	if err := c.Call(ctx, proto.MethodPaneCreate, proto.PaneCreateParams{Command: []string{"/bin/sleep", "30"}, Cwd: dir, NoProject: true}, &loose); err != nil {
		t.Fatal(err)
	}
	return c, proj, in, loose.ID
}

// Removing a project with close ends the panes working in it, and only
// those; the project goes as before.
func TestRemoveProjectClosesItsPanes(t *testing.T) {
	c, proj, in, out := removeRig(t)
	if !hasCap(c, proto.CapProjectRemoveClose) {
		t.Fatal("project.remove.close.v1 not announced")
	}
	var res proto.ProjectRemoveResult
	if err := c.Call(a5Ctx(t), proto.MethodProjectRemove, proto.ProjectRemoveParams{ID: proj.ID, Close: true}, &res); err != nil {
		t.Fatal(err)
	}
	slices.Sort(res.Closed)
	if !slices.Equal(res.Closed, in) {
		t.Fatalf("closed %v, want %v", res.Closed, in)
	}
	now := panesNow(t, c)
	if len(now) != 1 || now[out].ID != out {
		t.Fatalf("left running: %+v", now)
	}
	var list proto.ProjectList
	if err := c.Call(a5Ctx(t), proto.MethodProjectList, nil, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Projects) != 0 {
		t.Fatalf("the project stayed: %+v", list.Projects)
	}
}

// Without close, as an older client asks, the panes keep running.
func TestRemoveProjectLeavesPanesUnasked(t *testing.T) {
	c, proj, in, out := removeRig(t)
	var res proto.ProjectRemoveResult
	if err := c.Call(a5Ctx(t), proto.MethodProjectRemove, proto.ProjectRef{ID: proj.ID}, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Closed) != 0 {
		t.Fatalf("closed %v unasked", res.Closed)
	}
	now := panesNow(t, c)
	for _, id := range append(in, out) {
		if now[id].ID != id {
			t.Fatalf("%s went: %+v", id, now)
		}
	}
	// An unknown project closes nothing.
	err := c.Call(a5Ctx(t), proto.MethodProjectRemove, proto.ProjectRemoveParams{ID: "nope", Close: true}, nil)
	a5Code(t, err, proto.ErrNotFound)
	if len(panesNow(t, c)) != 3 {
		t.Fatal("a refused remove closed panes")
	}
}

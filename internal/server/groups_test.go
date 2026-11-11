package server_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/server"
)

// groupsServer is a server on dir that can be stopped and started again
// on the same dir, as a restart would.
func groupsServer(t *testing.T, dir string) (*client.Client, func()) {
	t.Helper()
	sock := filepath.Join(dir, "s.sock")
	srv := server.New(sock, dir)
	go srv.Run()
	var c *client.Client
	var err error
	for i := 0; i < 100; i++ {
		if c, err = client.Dial(sock, "test"); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	stop := func() { c.Close(); srv.StopAndWait() }
	return c, stop
}

func addFolder(t *testing.T, c *client.Client, dir, name string) proto.ProjectInfo {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var info proto.ProjectInfo
	if err := c.Call(ctx, proto.MethodProjectAdd, proto.ProjectAddParams{Path: p}, &info); err != nil {
		t.Fatal(err)
	}
	return info
}

func listGroups(t *testing.T, c *client.Client) []proto.GroupInfo {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var l proto.GroupList
	if err := c.Call(ctx, proto.MethodGroupList, nil, &l); err != nil {
		t.Fatal(err)
	}
	return l.Groups
}

func waitGroups(t *testing.T, c *client.Client, ok func([]proto.GroupInfo) bool) {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case m, open := <-c.Events:
			if !open {
				t.Fatalf("connection closed: %v", c.Err())
			}
			var l proto.GroupList
			if m.Event == proto.EventGroups && json.Unmarshal(m.Data, &l) == nil && ok(l.Groups) {
				return
			}
		case <-timeout:
			t.Fatal("timed out waiting for group.changed")
		}
	}
}

// A group is made from projects the server has, in the order given, each
// once; renamed, given notes, and removed — its projects staying — with
// every change broadcast. What it refuses says why.
func TestProjectGroups(t *testing.T) {
	c, dir := startServer(t)
	api := addFolder(t, c, dir, "spring")
	web := addFolder(t, c, dir, "angular-clinic")
	spec := addFolder(t, c, dir, "clinic-specs")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if got := listGroups(t, c); len(got) != 0 {
		t.Fatalf("groups before any: %+v", got)
	}
	var g proto.GroupInfo
	err := c.Call(ctx, proto.MethodGroupSave, proto.GroupSaveParams{Name: "clinic", Projects: []string{spec.ID, api.ID, web.ID, api.ID}, Notes: "  specs first  "}, &g)
	if err != nil {
		t.Fatal(err)
	}
	if g.Name != "clinic" || !slices.Equal(g.Projects, []string{spec.ID, api.ID, web.ID}) || g.Notes != "specs first" {
		t.Fatalf("saved %+v", g)
	}
	waitGroups(t, c, func(gs []proto.GroupInfo) bool { return len(gs) == 1 && gs[0].Name == "clinic" })
	if got := listGroups(t, c); len(got) != 1 || !slices.Equal(got[0].Projects, g.Projects) {
		t.Fatalf("listed %+v", got)
	}

	// Refused: a bad name, no projects, a project it has not got, a name
	// taken, renaming a group that is not there.
	for _, p := range []proto.GroupSaveParams{
		{Name: "", Projects: []string{api.ID}},
		{Name: " pad", Projects: []string{api.ID}},
		{Name: "bad\x07", Projects: []string{api.ID}},
		{Name: strings.Repeat("x", 65), Projects: []string{api.ID}},
		{Name: "empty"},
		{Name: "ghost", Projects: []string{"r0000000"}},
		{Name: "other", Old: "nope", Projects: []string{api.ID}},
	} {
		if err := c.Call(ctx, proto.MethodGroupSave, p, nil); err == nil {
			t.Errorf("%+v was taken", p)
		}
	}
	if err := c.Call(ctx, proto.MethodGroupSave, proto.GroupSaveParams{Name: "two", Projects: []string{api.ID}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(ctx, proto.MethodGroupSave, proto.GroupSaveParams{Name: "two", Old: "clinic", Projects: []string{api.ID}}, nil); err == nil {
		t.Error("renamed onto a name taken")
	}

	// Renamed, with a project fewer: in place, not moved to the end.
	if err := c.Call(ctx, proto.MethodGroupSave, proto.GroupSaveParams{Name: "clinic app", Old: "clinic", Projects: []string{api.ID, web.ID}}, nil); err != nil {
		t.Fatal(err)
	}
	got := listGroups(t, c)
	if len(got) != 2 || got[0].Name != "clinic app" || !slices.Equal(got[0].Projects, []string{api.ID, web.ID}) || got[0].Notes != "" || got[1].Name != "two" {
		t.Fatalf("after rename %+v", got)
	}

	// Removed: the group goes, its projects stay.
	if err := c.Call(ctx, proto.MethodGroupRemove, proto.GroupRef{Name: "two"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(ctx, proto.MethodGroupRemove, proto.GroupRef{Name: "two"}, nil); err == nil {
		t.Error("removed a group twice")
	}
	var pl proto.ProjectList
	if err := c.Call(ctx, proto.MethodProjectList, nil, &pl); err != nil || len(pl.Projects) != 3 {
		t.Fatalf("projects after removing a group: %v %d", err, len(pl.Projects))
	}
	if got := listGroups(t, c); len(got) != 1 || got[0].Name != "clinic app" {
		t.Fatalf("after remove %+v", got)
	}
	if slices.Contains(c.MissingCapabilities([]string{proto.CapProjectGroups}), proto.CapProjectGroups) {
		t.Error("no project.groups.v1")
	}
}

// A project removed from conch leaves every group it was in, and a group
// left with none goes too.
func TestProjectGroupsFollowRemovedProjects(t *testing.T) {
	c, dir := startServer(t)
	a := addFolder(t, c, dir, "a")
	b := addFolder(t, c, dir, "b")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for _, p := range []proto.GroupSaveParams{{Name: "both", Projects: []string{a.ID, b.ID}}, {Name: "just a", Projects: []string{a.ID}}} {
		if err := c.Call(ctx, proto.MethodGroupSave, p, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Call(ctx, proto.MethodProjectRemove, proto.ProjectRef{ID: a.ID}, nil); err != nil {
		t.Fatal(err)
	}
	waitGroups(t, c, func(gs []proto.GroupInfo) bool { return len(gs) == 1 })
	got := listGroups(t, c)
	if len(got) != 1 || got[0].Name != "both" || !slices.Equal(got[0].Projects, []string{b.ID}) {
		t.Fatalf("%+v", got)
	}
}

// Groups outlive the server: written to groups.json by folder, read back
// on the next start. A file that does not parse, or an entry without a
// name or projects, loads as nothing rather than stopping the server, and
// a project whose folder is gone is left out of what is listed.
func TestProjectGroupsAcrossRestarts(t *testing.T) {
	dir, err := os.MkdirTemp("", "conch")
	if err != nil {
		t.Fatal(err)
	}
	dir, _ = filepath.EvalSymlinks(dir)
	t.Cleanup(func() { os.RemoveAll(dir) })
	c, stop := groupsServer(t, dir)
	a := addFolder(t, c, dir, "a")
	b := addFolder(t, c, dir, "b")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := c.Call(ctx, proto.MethodGroupSave, proto.GroupSaveParams{Name: "ab", Projects: []string{b.ID, a.ID}, Notes: "b first"}, nil); err != nil {
		t.Fatal(err)
	}
	stop()
	raw, _ := os.ReadFile(filepath.Join(dir, "groups.json"))
	if !strings.Contains(string(raw), filepath.Join(dir, "b")) {
		t.Fatalf("not kept by folder: %s", raw)
	}

	c, stop = groupsServer(t, dir)
	waitFor := time.Now().Add(10 * time.Second)
	var got []proto.GroupInfo
	for time.Now().Before(waitFor) { // the projects load in the background
		if got = listGroups(t, c); len(got) == 1 && len(got[0].Projects) == 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(got) != 1 || !slices.Equal(got[0].Projects, []string{b.ID, a.ID}) || got[0].Notes != "b first" {
		t.Fatalf("after restart %+v", got)
	}
	stop()

	// b's folder gone: listed without it; then a broken file: nothing.
	os.RemoveAll(filepath.Join(dir, "b"))
	c, stop = groupsServer(t, dir)
	for time.Now().Before(time.Now().Add(5 * time.Second)) {
		if got = listGroups(t, c); len(got) == 1 && len(got[0].Projects) == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(got) != 1 || !slices.Equal(got[0].Projects, []string{a.ID}) {
		t.Fatalf("with b gone %+v", got)
	}
	stop()
	for _, bad := range []string{"{not json", `{"groups":[{"name":"","projects":["/x"]},{"name":"none"}]}`} {
		os.WriteFile(filepath.Join(dir, "groups.json"), []byte(bad), 0o600)
		c, stop = groupsServer(t, dir)
		if got := listGroups(t, c); len(got) != 0 {
			t.Errorf("%q loaded %+v", bad, got)
		}
		stop()
	}
}

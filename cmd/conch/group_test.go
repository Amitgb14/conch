package main

import (
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

// groupFake is a server holding projects and groups the way the real one
// answers for them, recording each group.save.
type groupFake struct {
	mu     sync.Mutex
	groups []proto.GroupInfo
	saves  []proto.GroupSaveParams
	gone   []string
}

func startGroupFake(t *testing.T, groups ...proto.GroupInfo) *groupFake {
	t.Helper()
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	f := &groupFake{groups: groups}
	projects := []proto.ProjectInfo{
		{ID: "r1", Name: "spring", Path: "/src/spring"},
		{ID: "r2", Name: "angular-clinic", Path: "/src/angular-clinic"},
		{ID: "r3", Name: "api", Path: "/src/a/api"},
		{ID: "r4", Name: "api", Path: "/src/b/api"},
	}
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch msg.Method {
		case proto.MethodProjectList:
			return proto.ProjectList{Projects: projects}, nil
		case proto.MethodGroupList:
			return proto.GroupList{Groups: slices.Clone(f.groups)}, nil
		case proto.MethodGroupSave:
			var p proto.GroupSaveParams
			json.Unmarshal(msg.Params, &p)
			f.saves = append(f.saves, p)
			return proto.GroupInfo{Name: p.Name, Projects: p.Projects, Notes: p.Notes}, nil
		case proto.MethodGroupRemove:
			var r proto.GroupRef
			json.Unmarshal(msg.Params, &r)
			f.gone = append(f.gone, r.Name)
		}
		return nil, nil
	})
	return f
}

func (f *groupFake) last(t *testing.T) proto.GroupSaveParams {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.saves) == 0 {
		t.Fatal("nothing saved")
	}
	return f.saves[len(f.saves)-1]
}

// add names projects by name, folder or ID, each once, and makes a group
// or adds to the one of that name, keeping what it had.
func TestGroupAdd(t *testing.T) {
	f := startGroupFake(t, proto.GroupInfo{Name: "clinic", Projects: []string{"r1"}, Notes: "specs first"})
	var err error
	out, _ := a4Capture(t, "", func() { err = runGroup([]string{"add", "new", "spring", "/src/angular-clinic", "r1"}) })
	if err != nil || !strings.Contains(out, "new: spring, angular-clinic") {
		t.Fatalf("%v %q", err, out)
	}
	if got := f.last(t); got.Name != "new" || got.Old != "" || !slices.Equal(got.Projects, []string{"r1", "r2"}) {
		t.Fatalf("%+v", got)
	}
	if err := runGroup([]string{"add", "clinic", "angular-clinic"}); err != nil {
		t.Fatal(err)
	}
	if got := f.last(t); got.Old != "clinic" || !slices.Equal(got.Projects, []string{"r1", "r2"}) || got.Notes != "specs first" {
		t.Fatalf("adding to a group: %+v", got)
	}
	// A name two projects share is a guess either way: refused, both said.
	err = runGroup([]string{"add", "x", "api"})
	if err == nil || !strings.Contains(err.Error(), "/src/a/api") || !strings.Contains(err.Error(), "/src/b/api") {
		t.Fatalf("shared name: %v", err)
	}
	if err := runGroup([]string{"add", "x", "/src/b/api"}); err != nil {
		t.Fatal(err)
	}
	if err := runGroup([]string{"add", "x", "nope"}); err == nil || !strings.Contains(err.Error(), "conch project ls") {
		t.Fatalf("unknown project: %v", err)
	}
	if err := runGroup([]string{"add", "x"}); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("no projects: %v", err)
	}
}

// rm takes projects out, or the whole group away, but never leaves one
// empty; rename and notes keep what else it has.
func TestGroupRmRenameNotes(t *testing.T) {
	f := startGroupFake(t, proto.GroupInfo{Name: "clinic", Projects: []string{"r1", "r2"}, Notes: "n"})
	if err := runGroup([]string{"rm", "clinic", "spring"}); err != nil {
		t.Fatal(err)
	}
	if got := f.last(t); !slices.Equal(got.Projects, []string{"r2"}) || got.Notes != "n" {
		t.Fatalf("%+v", got)
	}
	if err := runGroup([]string{"rm", "clinic", "spring", "angular-clinic"}); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("emptying: %v", err)
	}
	if err := runGroup([]string{"rm", "clinic", "/src/a/api"}); err == nil || !strings.Contains(err.Error(), "not in clinic") {
		t.Fatalf("not a member: %v", err)
	}
	var err error
	out, _ := a4Capture(t, "", func() { err = runGroup([]string{"rm", "clinic"}) })
	if err != nil || !strings.Contains(out, "its projects stay") || !slices.Equal(f.gone, []string{"clinic"}) {
		t.Fatalf("%v %q %v", err, out, f.gone)
	}
	if err := runGroup([]string{"rm", "nope"}); err == nil {
		t.Fatal("removed a group not there")
	}
	if err := runGroup([]string{"rename", "clinic", "clinic app"}); err != nil {
		t.Fatal(err)
	}
	if got := f.last(t); got.Name != "clinic app" || got.Old != "clinic" || !slices.Equal(got.Projects, []string{"r1", "r2"}) {
		t.Fatalf("%+v", got)
	}
	if err := runGroup([]string{"notes", "clinic", "API", "first"}); err != nil {
		t.Fatal(err)
	}
	if got := f.last(t); got.Notes != "API first" {
		t.Fatalf("%+v", got)
	}
	out, _ = a4Capture(t, "", func() { err = runGroup([]string{"notes", "clinic"}) })
	if err != nil || strings.TrimSpace(out) != "n" {
		t.Fatalf("%v %q", err, out)
	}
}

// ls lists them by name, and as JSON; an older server is told plainly; a
// word that is no subcommand is said before anything is reached.
func TestGroupLsAndOldServer(t *testing.T) {
	startGroupFake(t, proto.GroupInfo{Name: "clinic", Projects: []string{"r1", "r9"}, Notes: "n"})
	var err error
	out, _ := a4Capture(t, "", func() { err = runGroup(nil) })
	if err != nil || !strings.Contains(out, "clinic") || !strings.Contains(out, "spring, r9") {
		t.Fatalf("%v %q", err, out)
	}
	out, _ = a4Capture(t, "", func() { err = runGroup([]string{"ls", "-json"}) })
	var l proto.GroupList
	if err != nil || json.Unmarshal([]byte(out), &l) != nil || len(l.Groups) != 1 {
		t.Fatalf("%v %q", err, out)
	}
	if err := runGroup([]string{"bogus"}); err == nil {
		t.Fatal("a typo was taken")
	}

	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	srv.Hello = func(n int) proto.HelloResult {
		h := currentHello(n)
		h.Capabilities = slices.DeleteFunc(slices.Clone(h.Capabilities), func(c string) bool { return c == proto.CapProjectGroups })
		return h
	}
	if err := runGroup(nil); err == nil || !strings.Contains(err.Error(), "conch server reload") {
		t.Fatalf("old server: %v", err)
	}
}

func TestGroupNoGroups(t *testing.T) {
	startGroupFake(t)
	var err error
	out, _ := a4Capture(t, "", func() { err = runGroup([]string{"ls"}) })
	if err != nil || !strings.Contains(out, "no groups") {
		t.Fatalf("%v %q", err, out)
	}
}

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
	mu       sync.Mutex
	groups   []proto.GroupInfo
	saves    []proto.GroupSaveParams
	gone     []string
	tasks    []proto.GroupTaskParams
	taskGone []proto.GroupTaskRef
	merges   []proto.GroupTaskMergeParams
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
		case proto.MethodGroupTask:
			var p proto.GroupTaskParams
			json.Unmarshal(msg.Params, &p)
			f.tasks = append(f.tasks, p)
			ids := p.Projects
			if len(ids) == 0 {
				ids = []string{"r1", "r2"}
			}
			return proto.GroupTaskResult{Task: proto.GroupTask{Branch: "add-search", Folder: "/h/tasks/clinic/add-search", Projects: ids, Panes: []string{"p7"}},
				Panes: []proto.PaneInfo{{ID: "p7"}}}, nil
		case proto.MethodGroupTaskMerge:
			var p proto.GroupTaskMergeParams
			json.Unmarshal(msg.Params, &p)
			f.merges = append(f.merges, p)
			order := p.Order
			if len(order) == 0 {
				order = []string{"r1", "r2"}
			}
			return proto.GroupTask{Branch: p.Branch, Projects: []string{"r1", "r2"}, Merge: &proto.GroupTaskMerge{Order: order, State: proto.MergeWaiting}}, nil
		case proto.MethodGroupTaskRemove:
			var r proto.GroupTaskRef
			json.Unmarshal(msg.Params, &r)
			f.taskGone = append(f.taskGone, r)
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

// group task starts one across a group's repos — the prompt the words
// after the name, the repos given with -only by name — and says the
// folder and the agents; tasks lists them; task-rm forgets one.
func TestGroupTask(t *testing.T) {
	f := startGroupFake(t, proto.GroupInfo{Name: "clinic", Projects: []string{"r1", "r2"},
		Tasks: []proto.GroupTask{{Branch: "add-search", Folder: "/h/tasks/clinic/add-search", Projects: []string{"r1", "r2"}, Panes: []string{"p7"}}}})
	var err error
	out, _ := a4Capture(t, "", func() {
		err = runGroup([]string{"task", "-branch", "add-search", "-agent", "codex", "-account", "work", "-each", "-only", "spring,/src/angular-clinic", "clinic", "Add", "patient", "search"})
	})
	if err != nil || !strings.Contains(out, "add-search across spring, angular-clinic: /h/tasks/clinic/add-search") || !strings.Contains(out, "p7") {
		t.Fatalf("%v %q", err, out)
	}
	f.mu.Lock()
	got := f.tasks[len(f.tasks)-1]
	f.mu.Unlock()
	if got.Group != "clinic" || got.Prompt != "Add patient search" || got.Branch != "add-search" || got.Agent != "codex" || got.Account != "work" || !got.Each || !slices.Equal(got.Projects, []string{"r1", "r2"}) {
		t.Fatalf("%+v", got)
	}
	// Flags after the name are flags, not the prompt; after "--" the
	// prompt may start with a dash.
	a4Capture(t, "", func() {
		err = runGroup([]string{"task", "clinic", "-agent", "codex", "-each", "fix", "login", "-branch", "b2"})
	})
	f.mu.Lock()
	got = f.tasks[len(f.tasks)-1]
	f.mu.Unlock()
	if err != nil || got.Prompt != "fix login" || got.Agent != "codex" || !got.Each || got.Branch != "b2" {
		t.Fatalf("flags after the name: %v %+v", err, got)
	}
	a4Capture(t, "", func() { err = runGroup([]string{"task", "-agent", "codex", "clinic", "--", "-v", "is", "broken"}) })
	f.mu.Lock()
	got = f.tasks[len(f.tasks)-1]
	f.mu.Unlock()
	if err != nil || got.Prompt != "-v is broken" || got.Agent != "codex" {
		t.Fatalf("after --: %v %+v", err, got)
	}
	if err := runGroup([]string{"task", "clinic", "-nope", "x"}); err == nil || !strings.Contains(err.Error(), "-nope") {
		t.Fatalf("an unknown flag after the name: %v", err)
	}
	if err := runGroup([]string{"task", "clinic"}); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("no prompt: %v", err)
	}
	if err := runGroup([]string{"task", "-only", "nope", "clinic", "x"}); err == nil {
		t.Fatal("an unknown repo was taken")
	}
	out, _ = a4Capture(t, "", func() { err = runGroup([]string{"tasks", "clinic"}) })
	if err != nil || !strings.Contains(out, "add-search") || !strings.Contains(out, "spring, angular-clinic") || !strings.Contains(out, "/h/tasks/clinic/add-search") {
		t.Fatalf("%v %q", err, out)
	}
	out, _ = a4Capture(t, "", func() { err = runGroup([]string{"task-rm", "clinic", "add-search"}) })
	if err != nil || !strings.Contains(out, "keeps its worktree") || len(f.taskGone) != 1 || f.taskGone[0] != (proto.GroupTaskRef{Group: "clinic", Branch: "add-search"}) {
		t.Fatalf("%v %q %+v", err, out, f.taskGone)
	}
}

// group merge arms Merge All When Green with the order given by name, or
// the task's own, says the order, and -stop disarms it; tasks says where
// it stands.
func TestGroupMerge(t *testing.T) {
	f := startGroupFake(t, proto.GroupInfo{Name: "clinic", Projects: []string{"r1", "r2"},
		Tasks: []proto.GroupTask{{Branch: "add-search", Projects: []string{"r1", "r2"},
			Merge: &proto.GroupTaskMerge{Order: []string{"r2", "r1"}, Merged: []string{"r2"}, State: proto.MergeWaiting, Why: "spring #4 is waiting on its checks"}}}})
	var err error
	out, _ := a4Capture(t, "", func() {
		err = runGroup([]string{"merge", "-order", "angular-clinic,spring", "-remove", "clinic", "add-search"})
	})
	if err != nil || !strings.Contains(out, "add-search: merges angular-clinic, then spring once every pull request is green") {
		t.Fatalf("%v %q", err, out)
	}
	out, _ = a4Capture(t, "", func() { err = runGroup([]string{"merge", "clinic", "add-search"}) })
	if err != nil || !strings.Contains(out, "merges spring, then angular-clinic") {
		t.Fatalf("the task's own order: %v %q", err, out)
	}
	out, _ = a4Capture(t, "", func() { err = runGroup([]string{"merge", "-stop", "clinic", "add-search"}) })
	if err != nil || !strings.Contains(out, "no longer waiting") {
		t.Fatalf("%v %q", err, out)
	}
	f.mu.Lock()
	got := slices.Clone(f.merges)
	f.mu.Unlock()
	if len(got) != 3 || !slices.Equal(got[0].Order, []string{"r2", "r1"}) || !got[0].RemoveTask || got[0].Group != "clinic" || got[0].Branch != "add-search" ||
		len(got[1].Order) != 0 || got[1].RemoveTask || !got[2].Stop {
		t.Fatalf("%+v", got)
	}
	a4Capture(t, "", func() { err = runGroup([]string{"merge", "clinic", "add-search", "-stop"}) })
	f.mu.Lock()
	last := f.merges[len(f.merges)-1]
	f.mu.Unlock()
	if err != nil || !last.Stop || last.Branch != "add-search" {
		t.Fatalf("-stop after the branch: %v %+v", err, last)
	}
	if err := runGroup([]string{"merge", "clinic"}); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("no branch: %v", err)
	}
	if err := runGroup([]string{"merge", "-order", "nope", "clinic", "add-search"}); err == nil {
		t.Fatal("an unknown repo was taken")
	}
	out, _ = a4Capture(t, "", func() { err = runGroup([]string{"tasks", "clinic"}) })
	if err != nil || !strings.Contains(out, "waiting (1 of 2 merged): spring #4 is waiting on its checks") {
		t.Fatalf("%v %q", err, out)
	}
}

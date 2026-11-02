package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestMergeShared(t *testing.T) {
	v := func(s string) json.RawMessage { return json.RawMessage(`"` + s + `"`) }
	base := sharedUI{"a": v("1"), "b": v("1"), "c": v("1"), "d": v("1")}
	ours := sharedUI{"a": v("ours"), "b": v("1"), "d": v("ours"), "new-ours": v("x")}         // c removed here
	theirs := sharedUI{"a": v("theirs"), "b": v("theirs"), "c": v("1"), "new-theirs": v("y")} // d removed there
	got := mergeShared(base, ours, theirs)
	want := sharedUI{
		"a":          v("ours"),   // both changed it: ours, the newer
		"b":          v("theirs"), // only they did
		"d":          v("ours"),   // changed here, removed there: changed here wins
		"new-ours":   v("x"),
		"new-theirs": v("y"),
	} // c: removed here, untouched there
	if !got.equal(want) {
		t.Fatalf("got %s", got)
	}
	// Nothing to merge with gives ours; nothing changed here gives theirs.
	if !mergeShared(base, ours, base).equal(ours) || !mergeShared(base, base, theirs).equal(theirs) {
		t.Fatal("one side unchanged")
	}
	if len(mergeShared(nil, nil, nil)) != 0 {
		t.Fatal("empty")
	}
}

func TestSharedRoundTrip(t *testing.T) {
	st := uiState{
		Folders:        map[string][]savedFolder{"r1/4": {{Name: "eng", Members: []savedMember{{Name: "zsh", ID: "p1"}}}}, "r2/4": nil},
		SSHHosts:       map[string]sshHostInfo{"bb": {Name: "busybox"}},
		SavedSSH:       []string{"bb"},
		NoProjectOffer: []string{"/tmp/x"},
		SidebarWidth:   40, // not shared
	}
	s := sharedOf(st)
	if len(s) != 4 { // the empty section is nothing to share
		t.Fatalf("pieces %v", s)
	}
	var back uiState
	back.Folders = map[string][]savedFolder{"stale/4": {{Name: "x"}}}
	s.into(&back)
	if !sharedOf(back).equal(s) || back.SidebarWidth != 0 || back.Folders["stale/4"] != nil {
		t.Fatalf("back %+v", back)
	}
	// A piece that does not parse is left out, not fatal.
	s["folders/r3/4"] = json.RawMessage(`{bad`)
	s.into(&back)
	if len(back.Folders) != 1 {
		t.Fatalf("folders %+v", back.Folders)
	}
}

// twoTUIs is two models on one ui.json, as if both were started on it.
func twoTUIs(t *testing.T, st uiState) (a, b *Model, path string) {
	path = filepath.Join(t.TempDir(), "ui.json")
	if err := saveUIState(path, st); err != nil {
		t.Fatal(err)
	}
	open := func() *Model {
		st := loadUIState(path)
		m := &Model{statePath: path, expanded: st.Expanded, showAll: st.ShowAll, folders: st.Folders,
			savedSSH: st.SavedSSH, sshInfo: st.SSHHosts, noProjectOffer: st.NoProjectOffer}
		m.restoreSpaces(st.FirstSpace, st.Spaces, st.ActiveSpace)
		m.uiSync = newUISync(path, m.sharedNow())
		return m
	}
	return open(), open(), path
}

// save runs a model's save as the program would, handing back what it says.
func save(t *testing.T, m *Model) {
	t.Helper()
	if msg := m.saveState()(); msg != nil {
		updateModel(m, msg)
	}
}

func onDisk(t *testing.T, path string) uiState {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var st uiState
	if err := json.Unmarshal(b, &st); err != nil {
		t.Fatal(err)
	}
	return st
}

const engKey = "r1/4"

func members(m map[string][]savedFolder) []savedMember {
	if fs := m[engKey]; len(fs) > 0 {
		return fs[0].Members
	}
	return nil
}

// TestTwoTUIsKeepEachOthersFolders is what was seen: a terminal put in a
// folder in one TUI fell out of it when the other, open since before, folded
// a row — its save wrote its own old folders over the file.
func TestTwoTUIsKeepEachOthersFolders(t *testing.T) {
	a, b, path := twoTUIs(t, uiState{Folders: map[string][]savedFolder{engKey: {{Name: "eng"}}}})
	zsh := []savedMember{{Name: "zsh", ID: "p15"}}

	a.folders[engKey][0].Members = slices.Clone(zsh)
	save(t, a)
	b.expanded["something"] = true // nothing shared changed here
	save(t, b)
	if got := members(onDisk(t, path).Folders); !slices.Equal(got, zsh) {
		t.Fatalf("the other TUI's save undid it: %+v", got)
	}
	if !onDisk(t, path).Expanded["something"] {
		t.Fatal("its own part was not saved")
	}
	if got := members(b.folders); !slices.Equal(got, zsh) {
		t.Fatalf("the saving TUI did not take it in: %+v", got)
	}

	// Each changing a different section at once keeps both.
	a.folders["r1/11"] = []savedFolder{{Name: "hosts"}}
	b.noProjectOffer = []string{"/src/x"}
	save(t, a)
	save(t, b)
	got := onDisk(t, path)
	if len(got.Folders["r1/11"]) != 1 || !slices.Equal(got.NoProjectOffer, []string{"/src/x"}) || !slices.Equal(members(got.Folders), zsh) {
		t.Fatalf("on disk %+v", got)
	}
}

// TestTUISeesAnotherTUIsChange: the file is watched, so a change made in
// the other TUI shows without either saving again.
func TestTUISeesAnotherTUIsChange(t *testing.T) {
	a, b, path := twoTUIs(t, uiState{Folders: map[string][]savedFolder{engKey: {{Name: "eng"}}}})
	if msg := pollUIState(path, b.uiSync); msg.changed {
		t.Fatal("an unchanged file read as changed")
	}

	a.folders[engKey][0].Members = []savedMember{{Name: "zsh", ID: "p15"}}
	waitNewStamp(t, path)
	save(t, a)
	msg := pollUIState(path, b.uiSync)
	if !msg.changed {
		t.Fatal("the change was not seen")
	}
	updateModel(b, msg)
	if got := members(b.folders); len(got) != 1 || got[0].ID != "p15" {
		t.Fatalf("not taken in: %+v", got)
	}
	// Its own save is not news to it.
	if msg := pollUIState(path, a.uiSync); msg.changed {
		t.Fatal("a TUI read its own save as another's")
	}
	// And seen once.
	if msg := pollUIState(path, b.uiSync); msg.changed {
		t.Fatal("read twice")
	}
}

// TestPollLetsGoWhenASaveCameBetween: a save asked for after the file was
// read merges with the file itself; taking the reading in as well would put
// back what that save had just changed.
func TestPollLetsGoWhenASaveCameBetween(t *testing.T) {
	a, b, path := twoTUIs(t, uiState{Folders: map[string][]savedFolder{engKey: {{Name: "eng"}}}})
	a.folders[engKey][0].Members = []savedMember{{Name: "zsh", ID: "p15"}}
	waitNewStamp(t, path)
	save(t, a)
	msg := pollUIState(path, b.uiSync)

	b.folders["r1/11"] = []savedFolder{{Name: "ops"}}
	cmd := b.saveState()
	updateModel(b, msg)
	if len(b.folders["r1/11"]) != 1 {
		t.Fatalf("the reading put back an old state: %+v", b.folders)
	}
	if done := cmd(); done != nil {
		updateModel(b, done)
	}
	for name, fs := range map[string]map[string][]savedFolder{"on disk": onDisk(t, path).Folders, "here": b.folders} {
		if len(fs["r1/11"]) != 1 || len(members(fs)) != 1 {
			t.Fatalf("%s %+v", name, fs)
		}
	}
}

// TestOlderSaveNotWrittenOverANewer: saves run in the background, in no
// set order; one that finds a newer one on disk leaves it.
func TestOlderSaveNotWrittenOverANewer(t *testing.T) {
	a, _, path := twoTUIs(t, uiState{})
	a.sidebarW = 30
	first := a.saveState()
	a.sidebarW = 50
	second := a.saveState()
	second()
	first()
	if got := onDisk(t, path).SidebarWidth; got != 50 {
		t.Fatalf("width %d", got)
	}
}

// TestSaveOverABrokenFile: a file that does not parse is nothing to merge
// with, and the save writes what it has.
func TestSaveOverABrokenFile(t *testing.T) {
	a, _, path := twoTUIs(t, uiState{Folders: map[string][]savedFolder{engKey: {{Name: "eng"}}}})
	if err := os.WriteFile(path, []byte("{half"), 0o600); err != nil {
		t.Fatal(err)
	}
	if msg := pollUIState(path, a.uiSync); msg.changed {
		t.Fatal("a broken file read as a change")
	}
	save(t, a)
	if got := onDisk(t, path).Folders; len(got[engKey]) != 1 {
		t.Fatalf("folders %+v", got)
	}
	// Missing altogether: the same.
	os.Remove(path)
	save(t, a)
	if got := onDisk(t, path).Folders; len(got[engKey]) != 1 {
		t.Fatalf("folders %+v", got)
	}
}

// TestNoStatePathNoSync: tests and a TUI saving nowhere neither write nor
// watch.
func TestNoStatePathNoSync(t *testing.T) {
	m := Model{}
	if cmd := m.saveState(); cmd == nil || cmd() != nil {
		t.Fatal("saving nowhere")
	}
	if m.takeUIPoll(uiPollMsg{changed: true}) != nil || m.takeUIMerged(uiMergedMsg{}) != nil {
		t.Fatal("no sync to take into")
	}
}

// waitNewStamp waits until a write now would change the file's stamp, on a
// file system that keeps coarse times.
func waitNewStamp(t *testing.T, path string) {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	for time.Since(st.ModTime()) < 10*time.Millisecond {
		time.Sleep(time.Millisecond)
	}
}

// updateModel runs a message through Update and keeps the model it returns.
func updateModel(m *Model, msg tea.Msg) {
	next, _ := m.Update(msg)
	*m = next.(Model)
}

// spaceNames is a model's workspaces after the first, by name.
func spaceNames(m *Model) []string {
	var out []string
	for i, sp := range m.spaces {
		if i > 0 {
			out = append(out, sp.name)
		}
	}
	return out
}

// poll has b look at the file as its tick would, and take in what changed.
func poll(t *testing.T, m *Model, path string) {
	t.Helper()
	msg := pollUIState(path, m.uiSync)
	if !msg.changed {
		t.Fatal("the change was not seen")
	}
	updateModel(m, msg)
}

// Workspaces are shared by id: what each holds and the order, never the
// tabs, which are each TUI's own, nor which one is on screen.
func TestSharedSpacesRoundTrip(t *testing.T) {
	st := uiState{
		Spaces: []savedSpace{
			{ID: "sa", Name: "review", Projects: []string{"r1"}, Tabs: []savedTab{{}}, ActiveTab: 0},
			{Name: "old"}, // from before ids: named by its place
		},
		FirstSpace:  &savedSpace{Projects: []string{"r2"}},
		ActiveSpace: 2,
	}
	s := sharedOf(st)
	for _, k := range []string{"spaces/sa", "spaces/w2", "space_order", "first_space"} {
		if _, ok := s[k]; !ok {
			t.Fatalf("no %s in %v", k, s)
		}
	}
	if string(s["space_order"]) != `["sa","w2"]` {
		t.Fatalf("order %s", s["space_order"])
	}
	var shared savedSpace
	_ = json.Unmarshal(s["spaces/sa"], &shared)
	if shared.Tabs != nil || shared.Name != "review" || !slices.Equal(shared.Projects, []string{"r1"}) {
		t.Fatalf("shared %+v", shared)
	}

	// Taken back into a state that had them: tabs kept, on screen kept,
	// and the old one now carries its id.
	back := st
	back.Spaces = slices.Clone(st.Spaces)
	s.into(&back)
	if len(back.Spaces) != 2 || len(back.Spaces[0].Tabs) != 1 || back.Spaces[1].ID != "w2" || back.ActiveSpace != 2 {
		t.Fatalf("back %+v", back)
	}
	if back.FirstSpace == nil || !slices.Equal(back.FirstSpace.Projects, []string{"r2"}) {
		t.Fatalf("first %+v", back.FirstSpace)
	}
	// Into a state without them: no tabs, nothing on screen.
	var fresh uiState
	s.into(&fresh)
	if len(fresh.Spaces) != 2 || fresh.Spaces[0].Tabs != nil || fresh.ActiveSpace != 0 {
		t.Fatalf("fresh %+v", fresh)
	}
	// Into one that was on a workspace no longer there: the first.
	gone := uiState{Spaces: []savedSpace{{ID: "sx"}}, ActiveSpace: 1}
	s.into(&gone)
	if gone.ActiveSpace != 0 {
		t.Fatalf("active %d", gone.ActiveSpace)
	}
	// None at all: nothing shared, and nothing comes back.
	if s := sharedOf(uiState{}); len(s) != 0 {
		t.Fatalf("pieces %v", s)
	}
}

// The order is the list's, then any it does not name, by id; ids it names
// that have no workspace are dropped.
func TestSpaceOrder(t *testing.T) {
	sp := map[string]savedSpace{"a": {}, "b": {}, "c": {}, "d": {}}
	if got := spaceOrder([]string{"c", "gone", "a", "c"}, sp); !slices.Equal(got, []string{"c", "a", "b", "d"}) {
		t.Fatalf("order %v", got)
	}
	if got := spaceOrder(nil, nil); got != nil {
		t.Fatalf("empty %v", got)
	}
}

// A workspace made in one TUI shows in the other, renamed there it is
// renamed here, and closed there it goes here too.
func TestTwoTUIsShareWorkspaces(t *testing.T) {
	a, b, path := twoTUIs(t, uiState{})
	a.spaces = []*space{newSpace(), newSpace()}
	a.spaces[1].name = "review"
	a.spaces[1].projects["r1"] = true
	waitNewStamp(t, path)
	save(t, a)
	poll(t, b, path)
	if got := spaceNames(b); !slices.Equal(got, []string{"review"}) || !b.spaces[1].projects["r1"] || b.spaces[1].id != a.spaces[1].id {
		t.Fatalf("b's workspaces %v", got)
	}

	b.spaces[1].name = "reviews"
	waitNewStamp(t, path)
	save(t, b)
	poll(t, a, path)
	if got := spaceNames(a); !slices.Equal(got, []string{"reviews"}) {
		t.Fatalf("a's workspaces %v", got)
	}

	// Both make one at once: both are kept, in both.
	a.spaces = append(a.spaces, newSpace())
	a.spaces[2].name = "a-made"
	b.spaces = append(b.spaces, newSpace())
	b.spaces[2].name = "b-made"
	save(t, a)
	save(t, b)
	if got := spaceNames(b); len(got) != 3 || !slices.Contains(got, "a-made") || !slices.Contains(got, "b-made") {
		t.Fatalf("b's workspaces %v", got)
	}
	if got := onDisk(t, path).Spaces; len(got) != 3 {
		t.Fatalf("on disk %+v", got)
	}

	// Closed in a while b is on it: b goes to the first, and the closed
	// one's tabs come with it.
	// One a has not seen yet is not a's to close: it stays.
	a.spaces = a.spaces[:1]
	waitNewStamp(t, path)
	save(t, a)
	if got := spaceNames(a); !slices.Equal(got, []string{"b-made"}) {
		t.Fatalf("a closed one it had not seen: %v", got)
	}

	b.enterSpace(1)
	l := b.newLeaf(viewRef{})
	b.tabs = []*tab{{name: "kept", root: &layoutNode{leaf: l}, focus: l.id}}
	a.spaces = a.spaces[:1]
	waitNewStamp(t, path)
	save(t, a)
	poll(t, b, path)
	if len(b.spaces) != 0 || b.activeSpace != 0 {
		t.Fatalf("b still has %v on %d", spaceNames(b), b.activeSpace)
	}
	if len(b.tabs) != 1 || b.tabs[0].name != "kept" {
		t.Fatalf("tabs %+v", b.tabs)
	}
}

// Conch.app adds a workspace by appending it to the file, with nothing
// else: a TUI open on that file takes it in, keeping its own.
func TestWorkspaceWrittenByTheApp(t *testing.T) {
	a, _, path := twoTUIs(t, uiState{Spaces: []savedSpace{{Name: "old"}}})
	if got := spaceNames(a); !slices.Equal(got, []string{"old"}) || a.spaces[1].id != "w1" {
		t.Fatalf("restored %v", got)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	raw["spaces"] = append(raw["spaces"].([]any), map[string]any{"id": "sapp", "name": "from the app"})
	b, _ = json.Marshal(raw)
	waitNewStamp(t, path)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	poll(t, a, path)
	if got := spaceNames(a); !slices.Equal(got, []string{"old", "from the app"}) || a.spaces[2].id != "sapp" {
		t.Fatalf("workspaces %v", got)
	}
	// Its next save writes the ids down, the old one's included.
	save(t, a)
	got := onDisk(t, path).Spaces
	if len(got) != 2 || got[0].ID != "w1" || got[1].ID != "sapp" {
		t.Fatalf("on disk %+v", got)
	}
}

func TestNewSpaceIDs(t *testing.T) {
	a, b := newSpace().id, newSpace().id
	if a == b || len(a) != 13 || a[0] != 's' {
		t.Fatalf("ids %q %q", a, b)
	}
	if spaceID(savedSpace{ID: "sx"}, 4) != "sx" || spaceID(savedSpace{}, 0) != "w1" {
		t.Fatal("spaceID")
	}
}

// A ui.json an older conch left corrupt — a complete save with the tail of
// a longer one after it, as two saves at once through one temporary file
// made it — gives back its first, newest document rather than nothing:
// read as nothing, the next save wrote the loss over every workspace.
func TestUIStateWithATailStillLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ui.json")
	newer := uiState{Spaces: []savedSpace{{ID: "sa", Name: "review"}}, FirstSpace: &savedSpace{Name: "home"}, SidebarWidth: 40}
	b, _ := json.MarshalIndent(newer, "", "  ")
	hosts := make([]string, 50)
	for i := range hosts {
		hosts[i] = "host-" + itoa(i) + ".example.com"
	}
	older, _ := json.MarshalIndent(uiState{Spaces: []savedSpace{{ID: "sa"}, {ID: "sb"}}, SavedSSH: hosts}, "", "  ")
	corrupt := append(append([]byte{}, b...), '\n')
	if len(older) > len(corrupt) {
		corrupt = append(corrupt, older[len(corrupt):]...) // the longer one's tail
	}
	if err := os.WriteFile(path, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(corrupt, &uiState{}) == nil {
		t.Fatal("the fixture is not corrupt")
	}
	st := loadUIState(path)
	if len(st.Spaces) != 1 || st.Spaces[0].Name != "review" || st.FirstSpace == nil || st.FirstSpace.Name != "home" || st.SidebarWidth != 40 {
		t.Fatalf("loaded %+v", st)
	}
	if shared, ok := readShared(path); !ok || shared["spaces/sa"] == nil {
		t.Fatalf("shared %v %v", shared, ok)
	}
	// A file cut short is still nothing to go on.
	os.WriteFile(path, b[:len(b)/2], 0o600)
	if st := loadUIState(path); len(st.Spaces) != 0 {
		t.Fatalf("a half file loaded: %+v", st)
	}
}

// TUIs saving ui.json at once leave a file that parses, whichever wins.
func TestSaveUIStateRacing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ui.json")
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			st := uiState{SidebarWidth: i}
			for j := 0; j < i*40; j++ {
				st.SavedSSH = append(st.SavedSSH, "host-with-a-long-name.example.com")
			}
			if err := saveUIState(path, st); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var st uiState
	if err := json.Unmarshal(b, &st); err != nil {
		t.Fatalf("racing saves left a file that does not parse: %v", err)
	}
	if len(st.SavedSSH) != st.SidebarWidth*40 {
		t.Fatalf("a mixture of two saves: width %d, %d hosts", st.SidebarWidth, len(st.SavedSSH))
	}
}

package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
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

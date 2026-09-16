package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/buildinfo"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/update"
)

func a2Labels(items []updateItem) string {
	var out []string
	for _, it := range items {
		out = append(out, it.label+": "+it.detail)
	}
	return strings.Join(out, " | ")
}

func TestA2NewUpdateState(t *testing.T) {
	t.Setenv(updateRemotesEnv, "1")
	u := newUpdateState()
	if !u.remotes || os.Getenv(updateRemotesEnv) != "" {
		t.Fatal("a TUI restarted by an update updates remotes, once")
	}
	if u.exe == "" || u.exeMod.IsZero() {
		t.Fatalf("executable: %q %v", u.exe, u.exeMod)
	}
	if u.diskBuild != "" {
		t.Fatalf("the running test binary is not a newer build: %q", u.diskBuild)
	}
	if newUpdateState().remotes {
		t.Fatal("remotes without the environment variable")
	}
	if updateTick() == nil {
		t.Fatal("tick")
	}
}

func TestA2CheckUpdatesNoticesRebuild(t *testing.T) {
	m := a2Model()
	m.cfg.Update.CheckReleases = false // never reach the network
	exe := filepath.Join(t.TempDir(), "conch")
	if err := os.WriteFile(exe, []byte("a newer build"), 0o755); err != nil {
		t.Fatal(err)
	}
	m.upd = &updateState{exe: exe}
	if m.checkUpdates() == nil {
		t.Fatal("checks keep ticking")
	}
	want := buildinfo.HashFile(exe)
	if m.upd.diskBuild != want || want == "" {
		t.Fatalf("disk build %q, want %q", m.upd.diskBuild, want)
	}
	if m.targetBuild() != want {
		t.Fatal("the local server should move to the build on disk")
	}
	// Unchanged: no re-hash.
	m.upd.diskBuild = "kept"
	m.checkUpdates()
	if m.upd.diskBuild != "kept" {
		t.Fatal("an unchanged executable is not re-read")
	}
	// Rebuilt back to this build: nothing pending.
	m.upd.exe, _ = update.Executable()
	m.checkUpdates()
	if m.upd.diskBuild != "" {
		t.Fatalf("rebuilt to the running build: %q", m.upd.diskBuild)
	}
	// A missing executable leaves things as they are.
	m.upd.exe, m.upd.diskBuild = filepath.Join(t.TempDir(), "gone"), "x"
	m.checkUpdates()
	if m.upd.diskBuild != "x" {
		t.Fatal("a missing executable changed the state")
	}
	// Release checks happen at most once a day.
	m.cfg.Update.CheckReleases = true
	m.upd.releaseChecked = time.Now()
	m.checkUpdates()
	if time.Since(m.upd.releaseChecked) > time.Minute {
		t.Fatal("release check time moved")
	}

	// The tick message runs the check.
	m.cfg.Update.CheckReleases = false
	if cmd, handled := m.handleUpdate(updateTickMsg{}); !handled || cmd == nil {
		t.Fatal("tick handled")
	}
}

func TestA2PendingUpdates(t *testing.T) {
	m := a2Model()
	if m.pendingUpdates() != nil {
		t.Fatal("no update state, nothing pending")
	}
	m.upd = &updateState{}
	if got := m.pendingUpdates(); len(got) != 0 {
		t.Fatalf("up to date: %s", a2Labels(got))
	}
	if m.targetBuild() != buildinfo.Build() {
		t.Fatal("target is this build")
	}

	m.upd.release = &update.Release{Version: "9.9.9"}
	m.upd.diskBuild = "disk123"
	local := m.machines[0]
	local.c = a2Client()
	local.server.Build = "old456"
	behind := &machine{id: "box", label: "box", c: a2Client(), server: proto.HelloResult{BuildID: "remote-old"}}
	same := &machine{id: "same", label: "same", c: a2Client(), server: proto.HelloResult{BuildID: buildinfo.ID()}}
	unknown := &machine{id: "unk", label: "unk", c: a2Client()}
	offline := &machine{id: "off", label: "off", server: proto.HelloResult{BuildID: "remote-old"}}
	m.machines = append(m.machines, behind, same, unknown, offline)

	got := a2Labels(m.pendingUpdates())
	want := "Release: 9.9.9 available (running " + versionLabel() + ") | This TUI: new build on disk disk123 · restarts onto it | " +
		"Server: runs build old456 · reloads, panes keep running | box: runs build remote-old · installs and reloads | " +
		"same: runs build " + buildinfo.ID() + " · installs and reloads" // on this TUI's build, not the one on disk
	if got != want {
		t.Fatalf("pending:\n got %s\nwant %s", got, want)
	}
	// A server already on the target build is not behind.
	local.server.Build = "disk123"
	if m.serverBehindDisk() {
		t.Fatal("the server runs the build on disk")
	}
	local.server.Build = ""
	if m.serverBehindDisk() {
		t.Fatal("an unknown server build is not behind")
	}
	local.c = nil
	local.server.Build = "old"
	if m.serverBehindDisk() {
		t.Fatal("a disconnected server is not behind")
	}
}

func TestA2StartUpdateAndRemotes(t *testing.T) {
	m := a2Model()
	m.upd = &updateState{}
	if m.startUpdate(nil) != nil || m.flash != "conch is up to date" || m.upd.running {
		t.Fatalf("nothing to update: %q", m.flash)
	}
	m.upd.running = true
	m.flash = ""
	if m.startUpdate(nil) != nil || m.flash != "" {
		t.Fatal("an update in progress is not started twice")
	}
	m.upd.running = false
	m.upd.release = &update.Release{Version: "9.9.9"}
	// The command downloads, so it is only built.
	if m.startUpdate(nil) == nil || !m.upd.running {
		t.Fatal("pending updates start")
	}

	// No remote behind: done at once.
	m.upd.running = true
	if cmd, handled := m.handleUpdate(updateStepMsg{step: "remotes"}); !handled || cmd != nil || m.upd.running || m.flash != "conch is up to date · panes kept" {
		t.Fatalf("remotes with none behind: %q", m.flash)
	}
	behind := &machine{id: "box", label: "box", c: a2Client(), server: proto.HelloResult{BuildID: "old"}}
	m.machines = append(m.machines, behind, &machine{id: "off", label: "off"})
	m.upd.running = true
	if cmd := m.updateRemotes(); cmd == nil || !m.upd.running || m.flash != "updating box…" {
		t.Fatalf("remote behind: %q", m.flash)
	}
	if m.updateMachine("off") != nil || m.updateMachine("nope") != nil {
		t.Fatal("updating a disconnected machine")
	}

	// Machines that connect behind this build update only after an update restart.
	if m.autoUpdateMachine("box") != nil {
		t.Fatal("no auto update without an update restart")
	}
	m.upd.remotes = true
	if m.autoUpdateMachine(localMachine) != nil || m.autoUpdateMachine("off") != nil || m.autoUpdateMachine("nope") != nil {
		t.Fatal("auto update of local, offline or unknown machines")
	}
	if m.autoUpdateMachine("box") == nil {
		t.Fatal("a remote behind updates")
	}
	// Only on its first connection since the update: reconnecting with a
	// different build later (another client upgraded it) changes nothing.
	if m.autoUpdateMachine("box") != nil {
		t.Fatal("a second connection updated the machine again")
	}
	behind.server.BuildID = buildinfo.ID()
	m.upd.checked = nil
	if m.autoUpdateMachine("box") != nil {
		t.Fatal("a remote on this build")
	}
	behind.server.BuildID = "other"
	if m.autoUpdateMachine("box") != nil {
		t.Fatal("seen on this build first, then changed elsewhere: must not update")
	}
	m.upd = nil
	if m.autoUpdateMachine("box") != nil {
		t.Fatal("no update state")
	}
}

func TestA2HandleUpdateMessages(t *testing.T) {
	m := a2Model()
	m.upd = &updateState{running: true}
	if _, handled := m.handleUpdate(tickMsg{}); handled {
		t.Fatal("other messages are not update messages")
	}
	m.handleUpdate(releaseCheckMsg{rel: &update.Release{Version: "1.2.3"}})
	if m.upd.release == nil || m.upd.release.Version != "1.2.3" {
		t.Fatal("release check result")
	}
	m.handleUpdate(updateDoneMsg{err: errors.New("disk full")})
	if m.upd.running || m.flash != "update: disk full" || !m.flashIsErr || m.upd.release == nil {
		t.Fatalf("failed update: %q", m.flash)
	}
	m.upd.running = true
	m.handleUpdate(updateDoneMsg{})
	if m.upd.running || m.upd.release != nil || m.flash != "conch is up to date · panes kept" {
		t.Fatalf("finished update: %q", m.flash)
	}

	box := &machine{id: "box", label: "box", warning: "old", gen: 1}
	m.machines = append(m.machines, box)
	if cmd, handled := m.handleUpdate(machineUpdateMsg{machine: "nope"}); !handled || cmd != nil {
		t.Fatal("update of an unknown machine")
	}
	m.handleUpdate(machineUpdateMsg{machine: "box", err: errors.New("scp failed")})
	if m.flash != "update box: scp failed" || box.warning != "old" {
		t.Fatalf("failed machine update: %q", m.flash)
	}
	// Success reconnects (the command dials, so it is not run).
	if cmd, _ := m.handleUpdate(machineUpdateMsg{machine: "box"}); cmd == nil || box.warning != "" || box.state != stateConnecting || box.gen != 3 {
		t.Fatalf("machine updated: %+v", box)
	}

	cmd, handled := m.handleUpdate(restartTUIMsg{})
	if !handled || !m.restart || cmd == nil {
		t.Fatal("restart onto the new build")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("restart quits")
	}
	if !RestartRequested(*m) || RestartRequested(nil) || RestartRequested(Model{}) {
		t.Fatal("RestartRequested")
	}

	t.Setenv(updateRemotesEnv, "stale")
	env := RestartEnv(nil)
	n := 0
	for _, kv := range env {
		if strings.HasPrefix(kv, updateRemotesEnv+"=") {
			n++
			if kv != updateRemotesEnv+"=1" {
				t.Fatalf("restart env keeps %q", kv)
			}
		}
	}
	if n != 1 {
		t.Fatalf("%d update variables in the restart environment", n)
	}
}

// a2BehindModel has two machines behind this build (box1, box2), one on it
// (same) and one offline.
func a2BehindModel() (*Model, *machine, *machine) {
	m := a2Model()
	m.upd = &updateState{}
	box1 := &machine{id: "box1", label: "box1", c: a2Client(), server: proto.HelloResult{BuildID: "old1"}}
	box2 := &machine{id: "box2", label: "box2", c: a2Client(), server: proto.HelloResult{BuildID: "old2"}}
	same := &machine{id: "same", label: "same", c: a2Client(), server: proto.HelloResult{BuildID: buildinfo.ID()}}
	m.machines = append(m.machines, box1, box2, same, &machine{id: "off", label: "off", server: proto.HelloResult{BuildID: "old"}})
	return m, box1, box2
}

func TestVersionInfoTicksMachines(t *testing.T) {
	if proto.IsRelease() {
		t.Skip("release builds compare versions, not builds")
	}
	m, _, _ := a2BehindModel()
	v := newVersionInfo()
	m.overlay = v
	render := func() string {
		t.Helper()
		b := v.render(*m)
		for i, l := range b.lines {
			if w := ansi.StringWidth(l); w > m.width {
				t.Fatalf("line %d is %d wide on a %d screen", i, w, m.width)
			}
		}
		return ansi.Strip(strings.Join(b.lines, "\n"))
	}
	if got := strings.Join(v.machines(*m), ","); got != "box1,box2" {
		t.Fatalf("listed: %s", got) // nothing of this computer's is behind here
	}
	out := render()
	for _, want := range []string{"[x] box1", "[x] box2", "u update everything", "space untick one", "esc closes"} {
		if !strings.Contains(out, want) {
			t.Fatalf("all ticked lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "same") || strings.Contains(out, "off ") {
		t.Fatalf("lists a machine that isn't behind:\n%s", out)
	}

	key := func(k string) tea.Cmd {
		t.Helper()
		next, cmd := m.update(a2Key(k))
		*m = next.(Model)
		return cmd
	}
	// Moving and ticking keep the box open; the selection stops at the ends.
	key("up")
	key("down")
	key("down")
	key(" ")
	if m.overlay != v || v.sel != 1 || !v.skip["box2"] || v.skip["box1"] {
		t.Fatalf("untick box2: sel %d skip %v open %v", v.sel, v.skip, m.overlay == v)
	}
	if out = render(); !strings.Contains(out, "[ ] box2") || !strings.Contains(out, "u update 1 of 2 ") {
		t.Fatalf("after unticking:\n%s", out)
	}
	key("a") // not all ticked: ticks all
	if v.skip["box1"] || v.skip["box2"] {
		t.Fatalf("a ticks all: %v", v.skip)
	}
	key("a") // all ticked: unticks all
	if !v.skip["box1"] || !v.skip["box2"] {
		t.Fatalf("a again unticks all: %v", v.skip)
	}
	// Nothing ticked and nothing local: u says so and the box stays.
	if cmd := key("u"); cmd != nil || m.overlay != v || m.flash != "nothing ticked to update" || m.upd.running {
		t.Fatalf("u with nothing ticked: %q", m.flash)
	}

	// Click a row to tick it; the first content line is one below the border.
	b := v.render(*m)
	var y int
	for line, id := range v.rows {
		if id == "box1" {
			y = b.y + 1 + line
		}
	}
	v.mouse(m, tea.MouseMsg{X: b.x + 3, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}, b)
	if v.skip["box1"] || v.sel != 0 || m.overlay != v {
		t.Fatalf("click box1: %v sel %d", v.skip, v.sel)
	}
	v.mouse(m, tea.MouseMsg{X: b.x + 3, Y: y, Action: tea.MouseActionMotion}, b)
	if v.skip["box1"] {
		t.Fatal("motion toggled")
	}

	// u updates box1 only, then closes.
	cmd := key("u")
	if cmd == nil || m.overlay != nil || !m.upd.running || !m.upd.skip["box2"] || m.upd.skip["box1"] {
		t.Fatalf("u: open %v running %v skip %v", m.overlay != nil, m.upd.running, m.upd.skip)
	}
	if _, ok := cmd().(updateStepMsg); !ok { // nothing local to do: straight to remotes
		t.Fatal("expected the remotes step")
	}
	if c, _ := m.handleUpdate(updateStepMsg{step: "remotes"}); c == nil || m.flash != "updating box1…" {
		t.Fatalf("remotes: %q", m.flash)
	}
	m.handleUpdate(updateDoneMsg{})
	if m.flash != "conch updated · panes kept · not updated: box2" {
		t.Fatalf("done: %q", m.flash)
	}

	// A click outside closes; so does esc.
	m.overlay = v
	v.mouse(m, tea.MouseMsg{X: 0, Y: 0, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}, box{x: 50, y: 50, lines: []string{"x"}})
	if m.overlay != nil {
		t.Fatal("outside click kept the box")
	}
	m.overlay = v
	key("esc")
	if m.overlay != nil {
		t.Fatal("esc kept the box")
	}
}

func TestUpdateSkipsUntickedMachines(t *testing.T) {
	if proto.IsRelease() {
		t.Skip("release builds compare versions, not builds")
	}
	m, _, _ := a2BehindModel()
	// Both unticked, but the local server is behind: the update still runs
	// for this computer and leaves the machines alone.
	m.machines[0].c = a2Client()
	m.machines[0].server.Build = "oldlocal"
	m.upd.diskBuild = ""
	if !m.serverBehindDisk() {
		t.Skip("no build identity in this test binary")
	}
	skip := map[string]bool{"box1": true, "box2": true, "": true, "gone": false}
	if m.startUpdate(skip) == nil || !m.upd.running {
		t.Fatal("local server behind: runs")
	}
	if len(m.upd.skip) != 2 {
		t.Fatalf("skip keeps only unticked machines: %v", m.upd.skip)
	}
	skip["box1"] = false // the box's map changing later doesn't change the update
	if !m.upd.skip["box1"] {
		t.Fatal("update shares the box's map")
	}
	if cmd := m.updateRemotes(); cmd != nil || m.upd.running {
		t.Fatal("unticked machines updated")
	}
	if !strings.Contains(m.flash, "not updated: box1, box2") {
		t.Fatalf("flash: %q", m.flash)
	}

	// The TUI restarted by the update skips them too, even on first connect.
	m.restart = true
	env := RestartEnv(*m)
	var remotes, skipVar []string
	for _, kv := range env {
		if strings.HasPrefix(kv, updateRemotesEnv+"=") {
			remotes = append(remotes, kv)
		}
		if strings.HasPrefix(kv, updateSkipEnv+"=") {
			skipVar = append(skipVar, kv)
		}
	}
	if len(remotes) != 1 || strings.Join(skipVar, "|") != updateSkipEnv+"=box1,box2" {
		t.Fatalf("restart env: %v %v", remotes, skipVar)
	}
	t.Setenv(updateRemotesEnv, "1")
	t.Setenv(updateSkipEnv, "box1,,box2")
	u := newUpdateState()
	if !u.remotes || !u.skip["box1"] || !u.skip["box2"] || len(u.skip) != 2 || os.Getenv(updateSkipEnv) != "" {
		t.Fatalf("restarted state: %+v", u)
	}
	m.upd = u
	if m.autoUpdateMachine("box1") != nil || m.autoUpdateMachine("box2") != nil {
		t.Fatal("an unticked machine updated after the restart")
	}
	// A machine not listed at the time (offline then) still updates.
	m.machines[4].c = a2Client()
	if m.autoUpdateMachine("off") == nil {
		t.Fatal("a machine connecting later wasn't updated")
	}

	// Nothing skipped: no skip variable; a stray one isn't inherited, and
	// without the update variable it is ignored.
	m.upd = &updateState{}
	t.Setenv(updateSkipEnv, "stale")
	for _, kv := range RestartEnv(*m) {
		if strings.HasPrefix(kv, updateSkipEnv+"=") {
			t.Fatalf("stale skip kept: %s", kv)
		}
	}
	os.Unsetenv(updateRemotesEnv)
	t.Setenv(updateSkipEnv, "box1")
	if u := newUpdateState(); u.remotes || u.skip != nil || os.Getenv(updateSkipEnv) != "" {
		t.Fatalf("skip without an update: %+v", u)
	}
}

func TestVersionInfoTinyScreens(t *testing.T) {
	m, _, _ := a2BehindModel()
	v := newVersionInfo()
	for _, size := range [][2]int{{1, 1}, {20, 5}, {39, 12}, {300, 100}} {
		m.width, m.height = size[0], size[1]
		b := v.render(*m)
		if b.x < 0 || b.y < 0 {
			t.Fatalf("%v: box at %d,%d", size, b.x, b.y)
		}
		for _, l := range b.lines {
			if m.width > 2 && ansi.StringWidth(l) > m.width {
				t.Fatalf("%v: line %d wide", size, ansi.StringWidth(l))
			}
		}
	}
	// No machines behind: keys other than u and r close, as before.
	m2 := a2Model()
	m2.upd = &updateState{}
	v2 := newVersionInfo()
	m2.overlay = v2
	if handled, _ := v2.update(m2, a2Key("down")); !handled || m2.overlay != nil {
		t.Fatal("down without machines should close")
	}
}

// Found in use: after a rebuild, remotes on the running TUI's build weren't
// listed (they matched it), so they couldn't be unticked before the
// restarted TUI updated them all.
func TestPendingUpdatesComparesRemotesWithTheBuildOnDisk(t *testing.T) {
	if proto.IsRelease() {
		t.Skip("release builds compare versions, not builds")
	}
	m := a2Model()
	m.upd = &updateState{diskBuild: "newdisk12345"}
	onTUI := &machine{id: "old", label: "old", c: a2Client(), server: proto.HelloResult{BuildID: buildinfo.ID()}}
	onDisk := &machine{id: "new", label: "new", c: a2Client(), server: proto.HelloResult{BuildID: "newdisk12345"}}
	unknown := &machine{id: "unknown", label: "unknown", c: a2Client()} // a server too old to say
	offline := &machine{id: "off", label: "off", server: proto.HelloResult{BuildID: "x"}}
	m.machines = append(m.machines, onTUI, onDisk, unknown, offline)

	v := newVersionInfo()
	if got := strings.Join(v.machines(*m), ","); got != keyTUI+",old" {
		t.Fatalf("listed with a new build on disk: %q", got)
	}
	out := ansi.Strip(strings.Join(v.render(*m).lines, "\n"))
	if !strings.Contains(out, "This TUI") || !strings.Contains(out, "[x] old") {
		t.Fatalf("box:\n%s", out)
	}

	// Without a rebuild, remotes compare with this TUI, as before.
	m.upd.diskBuild = ""
	if got := strings.Join(v.machines(*m), ","); got != "new" {
		t.Fatalf("listed without a rebuild: %q", got)
	}
	m.upd = nil
	if m.remoteBehind(onTUI) || !m.remoteBehind(onDisk) {
		t.Fatal("no update state")
	}
}

// With one machine there was nothing to tick: only remote machines had
// checkboxes, so the box looked no different from before. Every pending
// item is tickable now, this computer's parts included.
func TestVersionInfoTicksThisComputer(t *testing.T) {
	m := a2Model()
	m.upd = &updateState{diskBuild: "newbuild1234", release: &update.Release{Version: "9.9.9"}}
	m.machines[0].c = a2Client()
	m.machines[0].server = proto.HelloResult{Build: "oldserver", PID: 7}
	v := newVersionInfo()
	m.overlay = v

	if got := strings.Join(v.machines(*m), ","); got != keyRelease+","+keyTUI+","+keyServer {
		t.Fatalf("a computer on its own lists %q", got)
	}
	out := ansi.Strip(strings.Join(v.render(*m).lines, "\n"))
	for _, want := range []string{"[x] Release", "[x] This TUI", "[x] Server", "space untick one"} {
		if !strings.Contains(out, want) {
			t.Fatalf("box lacks %q:\n%s", want, out)
		}
	}

	// Untick the TUI restart: the release still downloads and the server
	// still reloads, and this TUI keeps running the old build.
	key := func(k string) tea.Cmd {
		t.Helper()
		next, cmd := m.update(a2Key(k))
		*m = next.(Model)
		return cmd
	}
	key("down") // Release -> This TUI
	key(" ")
	if !v.skip[keyTUI] || v.skip[keyRelease] || v.skip[keyServer] {
		t.Fatalf("ticks: %v", v.skip)
	}
	if out = ansi.Strip(strings.Join(v.render(*m).lines, "\n")); !strings.Contains(out, "[ ] This TUI") || !strings.Contains(out, "u update 2 of 3") {
		t.Fatalf("after unticking the TUI:\n%s", out)
	}
	if cmd := key("u"); cmd == nil || !m.upd.running || !m.upd.skip[keyTUI] {
		t.Fatalf("u: running %v skip %v", m.upd.running, m.upd.skip)
	}
	m.handleUpdate(updateDoneMsg{})
	if m.flash != "conch updated · panes kept · not updated: this TUI" {
		t.Fatalf("status names what was left: %q", m.flash)
	}

	// Everything unticked: nothing runs.
	v2 := newVersionInfo()
	m.overlay, m.upd.running = v2, false
	for _, k := range v2.machines(*m) {
		v2.skip[k] = true
	}
	if cmd := m.startUpdate(v2.skip); cmd != nil || m.flash != "nothing ticked to update" || m.upd.running {
		t.Fatalf("all unticked: %q", m.flash)
	}
	// Unticking the release alone still reloads the server onto the build
	// on disk.
	only := map[string]bool{keyRelease: true}
	if m.startUpdate(only) == nil || !m.upd.skip[keyRelease] || m.upd.skip[keyServer] {
		t.Fatalf("release only: %v", m.upd.skip)
	}
}

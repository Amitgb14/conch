package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

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
		"Server: runs build old456 · reloads, panes keep running | box: runs build remote-old · installs and reloads"
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
	if m.startUpdate() != nil || m.flash != "conch is up to date" || m.upd.running {
		t.Fatalf("nothing to update: %q", m.flash)
	}
	m.upd.running = true
	m.flash = ""
	if m.startUpdate() != nil || m.flash != "" {
		t.Fatal("an update in progress is not started twice")
	}
	m.upd.running = false
	m.upd.release = &update.Release{Version: "9.9.9"}
	// The command downloads, so it is only built.
	if m.startUpdate() == nil || !m.upd.running {
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
	behind.server.BuildID = buildinfo.ID()
	if m.autoUpdateMachine("box") != nil {
		t.Fatal("a remote on this build")
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
	env := RestartEnv()
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

package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Two TUIs may run at once — one left open in another terminal, say — and
// each writes the whole of ui.json whenever anything changes. Reading the
// file only at start, a TUI that had been open a while wrote its own old
// folders back over a change made in the other one: a pane put in a folder
// fell out of it as soon as a row was folded in the other window.
//
// So what the TUIs share — folders, saved ssh hosts, the projects not to
// offer, the workspaces — is merged rather than overwritten. Each piece (a
// section's folders, one host, one workspace) goes the way of whoever
// changed it since this TUI last agreed with the file, and the file is
// watched so a change made elsewhere shows here. Conch.app adds a
// workspace by writing one into the file, so it is shared with the window
// as well. A workspace is shared as what it holds and where it is in the
// list; its tabs, which one is on screen, and what is folded stay each
// TUI's own.

const uiSyncEvery = time.Second

// uiSync is what a TUI and its ui.json last agreed on. It is shared by
// every copy of the Model and touched by background saves, hence the lock.
type uiSync struct {
	mu      sync.Mutex
	base    sharedUI // the shared parts as this TUI last left them in step
	seq     int      // saves asked for
	written int      // the newest save on disk; an older one is not written
	stamp   string   // the file as last read or written
}

// sharedUI is the shared parts of ui.json, a piece at a time, each as JSON
// so two can be compared.
type sharedUI map[string]json.RawMessage

func newUISync(path string, base sharedUI) *uiSync {
	return &uiSync{base: base, stamp: uiStamp(path)}
}

func uiStamp(path string) string {
	st, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d:%d", st.ModTime().UnixNano(), st.Size())
}

func sharedOf(st uiState) sharedUI {
	out := sharedUI{}
	put := func(k string, v any) {
		if b, err := json.Marshal(v); err == nil {
			out[k] = b
		}
	}
	for k, fs := range st.Folders {
		if len(fs) > 0 {
			put("folders/"+k, fs)
		}
	}
	for host, info := range st.SSHHosts {
		put("ssh_hosts/"+host, info)
	}
	if len(st.SavedSSH) > 0 {
		put("saved_ssh", st.SavedSSH)
	}
	if len(st.NoProjectOffer) > 0 {
		put("no_project_offer", st.NoProjectOffer)
	}
	for step, done := range st.Guide {
		if done {
			put("guide/"+step, true)
		}
	}
	// The workspaces: each by its id, without the tabs, which are this
	// TUI's; their order; and what the first holds.
	var order []string
	for i, ss := range st.Spaces {
		ss.ID, ss.Tabs, ss.ActiveTab = spaceID(ss, i), nil, 0
		put("spaces/"+ss.ID, ss)
		order = append(order, ss.ID)
	}
	if len(order) > 0 {
		put("space_order", order)
	}
	if st.FirstSpace != nil {
		fs := *st.FirstSpace
		fs.ID, fs.Tabs, fs.ActiveTab = "", nil, 0
		put("first_space", fs)
	}
	return out
}

// into puts the shared parts in st, replacing what was there.
func (s sharedUI) into(st *uiState) {
	st.Folders, st.SSHHosts, st.SavedSSH, st.NoProjectOffer, st.Guide = nil, nil, nil, nil, nil
	// The workspaces st had keep their tabs, and the one on screen stays
	// on screen if it is still there.
	own, activeID := map[string]savedSpace{}, ""
	for i, ss := range st.Spaces {
		id := spaceID(ss, i)
		own[id] = ss
		if st.ActiveSpace == i+1 {
			activeID = id
		}
	}
	st.Spaces, st.FirstSpace, st.ActiveSpace = nil, nil, 0
	spaces := map[string]savedSpace{}
	var order []string
	for k, v := range s {
		switch {
		case strings.HasPrefix(k, "folders/"):
			var fs []savedFolder
			if json.Unmarshal(v, &fs) == nil {
				if st.Folders == nil {
					st.Folders = map[string][]savedFolder{}
				}
				st.Folders[strings.TrimPrefix(k, "folders/")] = fs
			}
		case strings.HasPrefix(k, "ssh_hosts/"):
			var info sshHostInfo
			if json.Unmarshal(v, &info) == nil {
				if st.SSHHosts == nil {
					st.SSHHosts = map[string]sshHostInfo{}
				}
				st.SSHHosts[strings.TrimPrefix(k, "ssh_hosts/")] = info
			}
		case k == "saved_ssh":
			_ = json.Unmarshal(v, &st.SavedSSH)
		case k == "no_project_offer":
			_ = json.Unmarshal(v, &st.NoProjectOffer)
		case strings.HasPrefix(k, "guide/"):
			if st.Guide == nil {
				st.Guide = map[string]bool{}
			}
			st.Guide[strings.TrimPrefix(k, "guide/")] = true
		case strings.HasPrefix(k, "spaces/"):
			var ss savedSpace
			if json.Unmarshal(v, &ss) == nil {
				spaces[strings.TrimPrefix(k, "spaces/")] = ss
			}
		case k == "space_order":
			_ = json.Unmarshal(v, &order)
		case k == "first_space":
			var fs savedSpace
			if json.Unmarshal(v, &fs) == nil {
				st.FirstSpace = &fs
			}
		}
	}
	for _, id := range spaceOrder(order, spaces) {
		ss := spaces[id]
		ss.ID = id
		if o, ok := own[id]; ok {
			ss.Tabs, ss.ActiveTab = o.Tabs, o.ActiveTab
		}
		st.Spaces = append(st.Spaces, ss)
		if id == activeID {
			st.ActiveSpace = len(st.Spaces)
		}
	}
}

// spaceOrder is the workspaces' ids in the order the list was left in,
// then any the order does not name — one made in another TUI while this
// one reordered the list, say — by id, so every TUI puts them alike.
func spaceOrder(order []string, spaces map[string]savedSpace) []string {
	var out []string
	seen := map[string]bool{}
	for _, id := range order {
		if _, ok := spaces[id]; ok && !seen[id] {
			out = append(out, id)
			seen[id] = true
		}
	}
	var rest []string
	for id := range spaces {
		if !seen[id] {
			rest = append(rest, id)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

func (s sharedUI) equal(o sharedUI) bool {
	if len(s) != len(o) {
		return false
	}
	for k, v := range s {
		if w, ok := o[k]; !ok || !bytes.Equal(v, w) {
			return false
		}
	}
	return true
}

// mergeShared takes each piece from ours where this TUI changed it since
// base, and from theirs — the file — everywhere else. Both changing one
// piece leaves ours, the newer of the two.
func mergeShared(base, ours, theirs sharedUI) sharedUI {
	out := sharedUI{}
	keys := map[string]bool{}
	for _, s := range []sharedUI{base, ours, theirs} {
		for k := range s {
			keys[k] = true
		}
	}
	for k := range keys {
		v, ok := theirs[k]
		b, inBase := base[k]
		if o, inOurs := ours[k]; inOurs != inBase || !bytes.Equal(o, b) {
			v, ok = o, inOurs
		}
		if ok {
			out[k] = v
		}
	}
	return out
}

// readShared is the shared parts of the file; false for a file that is
// missing or does not parse, which is nothing to merge with.
func readShared(path string) (sharedUI, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var st uiState
	if json.Unmarshal(b, &st) != nil {
		return nil, false
	}
	return sharedOf(st), true
}

// sharedNow is the shared parts as this TUI has them.
func (m Model) sharedNow() sharedUI {
	return sharedOf(uiState{Folders: m.folders, SSHHosts: m.sshInfo, SavedSSH: m.savedSSH, NoProjectOffer: m.noProjectOffer, Guide: m.guide,
		Spaces: m.savedSpaces(), FirstSpace: m.savedFirstSpace()})
}

// takeShared puts shared parts in the model, cleaned as at start.
func (m *Model) takeShared(s sharedUI) {
	var st uiState
	s.into(&st)
	m.savedSSH = cleanSavedSSH(st.SavedSSH)
	m.sshInfo = cleanSSHInfo(m.savedSSH, st.SSHHosts)
	m.folders = st.Folders
	m.noProjectOffer = st.NoProjectOffer
	m.guide = st.Guide
	m.takeSpaces(st.FirstSpace, st.Spaces)
}

// writeUIState is a save in the background: the shared parts merged with
// what another TUI wrote since, the rest as this one has it. It says what
// the merge brought in, for the model to take too.
func writeUIState(path string, us *uiSync, seq int, base sharedUI, st uiState) tea.Msg {
	us.mu.Lock()
	defer us.mu.Unlock()
	if seq < us.written {
		return nil // a newer save is on disk already
	}
	ours := sharedOf(st)
	theirs, ok := readShared(path)
	if !ok {
		theirs = base
	}
	merged := mergeShared(base, ours, theirs)
	merged.into(&st)
	_ = saveUIState(path, st) // losing fold state is not worth interrupting the user
	us.written, us.stamp = seq, uiStamp(path)
	if merged.equal(ours) {
		return nil
	}
	return uiMergedMsg{was: ours, now: merged}
}

// uiMergedMsg is a save that brought in another TUI's changes: was is what
// this one had when it saved, now what went on disk.
type uiMergedMsg struct{ was, now sharedUI }

// uiPollMsg is ui.json after another process wrote it, or nothing new.
type uiPollMsg struct {
	theirs  sharedUI
	seq     int
	changed bool
}

// uiSyncTick looks at ui.json in a second, and reads it if it changed.
func uiSyncTick(path string, us *uiSync) tea.Cmd {
	return tea.Tick(uiSyncEvery, func(time.Time) tea.Msg { return pollUIState(path, us) })
}

func pollUIState(path string, us *uiSync) uiPollMsg {
	us.mu.Lock()
	defer us.mu.Unlock()
	stamp := uiStamp(path)
	if stamp == us.stamp {
		return uiPollMsg{}
	}
	theirs, ok := readShared(path)
	if !ok {
		return uiPollMsg{} // half written or broken: look again next time
	}
	us.stamp = stamp
	return uiPollMsg{theirs: theirs, seq: us.seq, changed: true}
}

// takeUIPoll brings another TUI's changes in. A save asked for since the
// file was read merges with it itself, so the reading is let go.
func (m *Model) takeUIPoll(msg uiPollMsg) tea.Cmd {
	us := m.uiSync
	if !msg.changed || us == nil {
		return nil
	}
	cur := m.sharedNow()
	us.mu.Lock()
	if msg.seq != us.seq {
		us.mu.Unlock()
		return nil
	}
	next := mergeShared(us.base, cur, msg.theirs)
	us.base = next
	us.mu.Unlock()
	if next.equal(cur) {
		return nil
	}
	m.takeShared(next)
	return m.rebuild()
}

// takeUIMerged brings in what a save merged from another TUI, keeping
// whatever changed here since that save was asked for.
func (m *Model) takeUIMerged(msg uiMergedMsg) tea.Cmd {
	us := m.uiSync
	if us == nil {
		return nil
	}
	cur := m.sharedNow()
	us.mu.Lock()
	us.base = mergeShared(msg.was, us.base, msg.now)
	us.mu.Unlock()
	next := mergeShared(msg.was, cur, msg.now)
	if next.equal(cur) {
		return nil
	}
	m.takeShared(next)
	return m.rebuild()
}

package tui

import (
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// ---- moving tabs: ctrl+b < > and ctrl+b . ----

// moveTab moves the active tab d places (±1) among the tabs in the bar.
func (m *Model) moveTab(d int) tea.Cmd {
	vis := m.visibleTabs()
	pos := slices.Index(vis, m.activeTab)
	if m.previewing || pos < 0 || len(vis) < 2 {
		return nil
	}
	to := pos + d
	if to < 0 || to >= len(vis) {
		return nil // already at that end, as tmux's swap-window stops
	}
	return m.moveTabTo(to)
}

// moveTabTo moves the active tab to position pos (0-based) of the bar.
func (m *Model) moveTabTo(pos int) tea.Cmd {
	vis := m.visibleTabs()
	from := slices.Index(vis, m.activeTab)
	if m.previewing || from < 0 || pos < 0 || pos >= len(vis) || pos == from {
		return nil
	}
	t := m.tabs[m.activeTab]
	target := m.tabs[vis[pos]]
	m.tabs = slices.Delete(m.tabs, m.activeTab, m.activeTab+1)
	at := slices.Index(m.tabs, target)
	if pos > from {
		at++ // after the tab it passes
	}
	m.tabs = slices.Insert(m.tabs, at, t)
	m.activeTab, m.keepTab = at, true
	return tea.Batch(m.syncView(), m.saveState())
}

// openMoveTab asks for the active tab's new position.
func (m *Model) openMoveTab() tea.Cmd {
	vis := m.visibleTabs()
	pos := slices.Index(vis, m.activeTab)
	if m.previewing || pos < 0 || len(vis) < 2 {
		m.setFlash("no other tab to move this one past", true)
		return nil
	}
	d := newDialog(*m, " Move tab ", []string{fmt.Sprintf("Position 1–%d in the tab bar; %d now.", len(vis), pos+1)}, []string{"Position"}, []string{""})
	d.submit = func(m *Model, v []string) tea.Cmd {
		n, err := strconv.Atoi(strings.TrimSpace(v[0]))
		if err != nil || n < 1 || n > len(m.visibleTabs()) {
			m.setFlash(fmt.Sprintf("a position from 1 to %d", len(m.visibleTabs())), true)
			return nil
		}
		return m.moveTabTo(n - 1)
	}
	m.overlay = d
	return d.focusCmd()
}

// ---- split numbers: ctrl+b q ----

// numbersFor is how long split numbers show, and how long a digit picks one.
const numbersFor = 2 * time.Second

type numbersDoneMsg struct{ gen int }

// showNumbers labels each split of the tab with its number.
func (m *Model) showNumbers() tea.Cmd {
	m.numbersUntil = time.Now().Add(numbersFor)
	m.numbersGen++
	gen := m.numbersGen
	return tea.Tick(numbersFor, func(time.Time) tea.Msg { return numbersDoneMsg{gen: gen} })
}

func (m Model) numbersShown() bool { return time.Now().Before(m.numbersUntil) }

// numberKey handles a key while split numbers show: a digit focuses that
// split; anything else hides the numbers and goes on as usual.
func (m *Model) numberKey(key string) (tea.Cmd, bool) {
	if !m.numbersShown() {
		return nil, false
	}
	m.numbersUntil = time.Time{}
	if len(key) != 1 || key[0] < '1' || key[0] > '9' {
		return nil, key == "esc"
	}
	ls := m.tab().root.leaves()
	if n := int(key[0] - '1'); n < len(ls) {
		return m.focusLeaf(ls[n].id), true
	}
	return nil, true
}

// numberBadge draws a split's number over the middle of its content.
func numberBadge(content []string, n, w int) []string {
	if len(content) == 0 || w < 3 {
		return content
	}
	badge := styleSel.Render(" " + strconv.Itoa(n) + " ")
	mid := len(content) / 2
	x := max((w-ansi.StringWidth(badge))/2, 0)
	out := slices.Clone(content)
	out[mid] = fit(splice(fit(out[mid], w), badge, x, w), w)
	return out
}

// ---- layouts: ctrl+b space and ctrl+b alt+1..5 ----

var layoutNames = []string{"even-horizontal", "even-vertical", "main-horizontal", "main-vertical", "tiled"}

// mainShare is the main split's share of the tab in the main layouts.
const mainShare = 0.6

// applyLayout arranges the tab's splits, keeping their order and focus.
func (m *Model) applyLayout(i int) tea.Cmd {
	t := m.tab()
	ls := t.root.leaves()
	if len(ls) < 2 {
		m.setFlash("one split: nothing to arrange", false)
		return nil
	}
	i = ((i % len(layoutNames)) + len(layoutNames)) % len(layoutNames)
	t.layout = i + 1
	t.root = buildLayout(layoutNames[i], ls)
	m.zoom = false
	m.setFlash("layout: "+layoutNames[i], false)
	return tea.Batch(m.syncView(), m.saveState())
}

func buildLayout(name string, ls []*leaf) *layoutNode {
	switch name {
	case "even-horizontal":
		return evenChain(ls, splitRight)
	case "even-vertical":
		return evenChain(ls, splitDown)
	case "main-horizontal":
		return &layoutNode{dir: splitDown, ratio: mainShare, a: &layoutNode{leaf: ls[0]}, b: evenChain(ls[1:], splitRight)}
	case "main-vertical":
		return &layoutNode{dir: splitRight, ratio: mainShare, a: &layoutNode{leaf: ls[0]}, b: evenChain(ls[1:], splitDown)}
	}
	// tiled: rows of up to cols splits.
	cols := int(math.Ceil(math.Sqrt(float64(len(ls)))))
	var rows []*layoutNode
	for start := 0; start < len(ls); start += cols {
		rows = append(rows, evenChain(ls[start:min(start+cols, len(ls))], splitRight))
	}
	return evenNodes(rows, splitDown)
}

// evenChain splits leaves in dir into equal parts.
func evenChain(ls []*leaf, dir splitDir) *layoutNode {
	nodes := make([]*layoutNode, len(ls))
	for i, l := range ls {
		nodes[i] = &layoutNode{leaf: l}
	}
	return evenNodes(nodes, dir)
}

func evenNodes(nodes []*layoutNode, dir splitDir) *layoutNode {
	if len(nodes) == 1 {
		return nodes[0]
	}
	return &layoutNode{dir: dir, ratio: 1 / float64(len(nodes)), a: nodes[0], b: evenNodes(nodes[1:], dir)}
}

// ---- clearer names in the tab picker ----

// pickerLabel names what a split shows with enough context to tell splits
// apart: the branch or folder of a pane and what its agent is doing.
func (m Model) pickerLabel(v viewRef) string {
	label := m.viewLabel(v)
	switch v.Kind {
	case kindPane:
		p := m.pane(v.Machine, v.PaneID)
		if p == nil {
			break
		}
		where := p.Branch
		if where == "" && p.Cwd != "" {
			where = filepath.Base(p.Cwd)
		}
		if where != "" {
			label += styleMuted.Render(" · " + where)
		}
		if p.Agent != nil {
			label += styleMuted.Render(" · " + p.Agent.State)
		}
	case kindBranch:
		label = "changes · " + v.Branch
	case kindSessions:
		label = "sessions · " + label
	}
	return label
}

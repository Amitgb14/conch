package tui

import (
	"fmt"
)

// A tab holds a layout: a binary tree whose leaves each show one view (a
// pane, a branch's changes, a project or a machine). The tree in the
// sidebar picks what the focused leaf shows.

type splitDir int

const (
	splitNone  splitDir = iota // a leaf
	splitRight                 // children side by side
	splitDown                  // children stacked
)

// viewRef is what a leaf shows, identified like the sidebar row it
// came from.
type viewRef struct {
	Row       string   `json:"row,omitempty"`
	Kind      nodeKind `json:"kind"`
	Machine   string   `json:"machine,omitempty"`
	PaneID    string   `json:"pane,omitempty"`
	ProjectID string   `json:"project,omitempty"`
	Branch    string   `json:"branch,omitempty"`
}

func (v viewRef) empty() bool { return v.Row == "" }

func viewOf(r row) viewRef {
	return viewRef{Row: r.id, Kind: r.kind, Machine: r.machine, PaneID: r.paneID, ProjectID: r.projectID, Branch: r.branch}
}

type leaf struct {
	id       int
	view     viewRef
	changes  *changesView  // when the view is a branch
	sessions *sessionsView // when the view lists sessions
	// pick marks a leaf made empty on purpose (a new tab or split) to be
	// filled by the next row the user opens, rather than following the
	// tree's cursor.
	pick bool
}

type layoutNode struct {
	dir   splitDir
	ratio float64 // share of the first child, 0..1
	a, b  *layoutNode
	leaf  *leaf
}

type tab struct {
	name  string
	root  *layoutNode
	focus int // leaf id
}

type rect struct{ x, y, w, h int }

func (r rect) contains(x, y int) bool { return x >= r.x && x < r.x+r.w && y >= r.y && y < r.y+r.h }

// minLeaf is the smallest a leaf may be squeezed to, border included.
const minLeaf = 6

func (n *layoutNode) leaves() []*leaf {
	if n == nil {
		return nil
	}
	if n.dir == splitNone {
		return []*leaf{n.leaf}
	}
	return append(n.a.leaves(), n.b.leaves()...)
}

func (t *tab) leaf(id int) *leaf {
	for _, l := range t.root.leaves() {
		if l.id == id {
			return l
		}
	}
	return nil
}

func (t *tab) focused() *leaf {
	if l := t.leaf(t.focus); l != nil {
		return l
	}
	l := t.root.leaves()[0]
	t.focus = l.id
	return l
}

// split divides leaf id, putting nl in the new half (right or below), and
// reports whether the leaf was found.
func (n *layoutNode) split(id int, dir splitDir, nl *leaf) bool {
	if n.dir == splitNone {
		if n.leaf.id != id {
			return false
		}
		old := *n
		*n = layoutNode{dir: dir, ratio: 0.5, a: &old, b: &layoutNode{leaf: nl}}
		return true
	}
	return n.a.split(id, dir, nl) || n.b.split(id, dir, nl)
}

// remove deletes leaf id; its sibling takes the parent's place. It returns
// the new root, which is nil when the last leaf was removed.
func (n *layoutNode) remove(id int) *layoutNode {
	if n.dir == splitNone {
		if n.leaf.id == id {
			return nil
		}
		return n
	}
	a, b := n.a.remove(id), n.b.remove(id)
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	}
	n.a, n.b = a, b
	return n
}

// splitBar is a draggable boundary between two children.
type splitBar struct {
	node *layoutNode
	area rect // the node's whole area
	pos  int  // column (splitRight) or row (splitDown) of the boundary
}

// layout places every leaf within r and collects the split boundaries.
func (n *layoutNode) layout(r rect, leaves map[int]rect, bars *[]splitBar) {
	if n.dir == splitNone {
		leaves[n.leaf.id] = r
		return
	}
	size := r.w
	if n.dir == splitDown {
		size = r.h
	}
	first := clamp(int(float64(size)*n.ratio+0.5), min(minLeaf, size/2), max(size-minLeaf, size/2))
	ra, rb := r, r
	if n.dir == splitRight {
		ra.w, rb.x, rb.w = first, r.x+first, r.w-first
		*bars = append(*bars, splitBar{node: n, area: r, pos: rb.x})
	} else {
		ra.h, rb.y, rb.h = first, r.y+first, r.h-first
		*bars = append(*bars, splitBar{node: n, area: r, pos: rb.y})
	}
	n.a.layout(ra, leaves, bars)
	n.b.layout(rb, leaves, bars)
}

// equalize resets every split to halves.
func (n *layoutNode) equalize() {
	if n.dir != splitNone {
		n.ratio = 0.5
		n.a.equalize()
		n.b.equalize()
	}
}

// neighbor finds the leaf next to from in direction dx, dy (one of them
// ±1): the closest leaf whose area lies that way and overlaps along the
// other axis.
func neighbor(rects map[int]rect, from, dx, dy int) (int, bool) {
	r, ok := rects[from]
	if !ok {
		return 0, false
	}
	best, bestDist := 0, -1
	for id, o := range rects {
		if id == from {
			continue
		}
		var dist int
		switch {
		case dx > 0 && o.x >= r.x+r.w && overlaps(o.y, o.h, r.y, r.h):
			dist = o.x - (r.x + r.w)
		case dx < 0 && o.x+o.w <= r.x && overlaps(o.y, o.h, r.y, r.h):
			dist = r.x - (o.x + o.w)
		case dy > 0 && o.y >= r.y+r.h && overlaps(o.x, o.w, r.x, r.w):
			dist = o.y - (r.y + r.h)
		case dy < 0 && o.y+o.h <= r.y && overlaps(o.x, o.w, r.x, r.w):
			dist = r.y - (o.y + o.h)
		default:
			continue
		}
		if bestDist < 0 || dist < bestDist || (dist == bestDist && id < best) {
			best, bestDist = id, dist
		}
	}
	return best, bestDist >= 0
}

func overlaps(a, al, b, bl int) bool { return a < b+bl && b < a+al }

// ---- persistence ----

type savedNode struct {
	Split string     `json:"split,omitempty"` // "", "right", "down"
	Ratio float64    `json:"ratio,omitempty"`
	A     *savedNode `json:"a,omitempty"`
	B     *savedNode `json:"b,omitempty"`
	View  *viewRef   `json:"view,omitempty"`
	Focus bool       `json:"focus,omitempty"`
}

type savedTab struct {
	Name string     `json:"name"`
	Root *savedNode `json:"root"`
}

func saveNode(n *layoutNode, focus int) *savedNode {
	if n.dir == splitNone {
		v := n.leaf.view
		return &savedNode{View: &v, Focus: n.leaf.id == focus}
	}
	s := &savedNode{Split: "right", Ratio: n.ratio, A: saveNode(n.a, focus), B: saveNode(n.b, focus)}
	if n.dir == splitDown {
		s.Split = "down"
	}
	return s
}

// loadNode rebuilds a layout, numbering leaves with next. It returns the
// focused leaf id (0 if none was marked).
func loadNode(s *savedNode, next func() int) (*layoutNode, int, error) {
	if s == nil {
		return nil, 0, fmt.Errorf("empty layout")
	}
	if s.Split == "" {
		l := &leaf{id: next()}
		if s.View != nil {
			l.view = *s.View
		}
		focus := 0
		if s.Focus {
			focus = l.id
		}
		return &layoutNode{leaf: l}, focus, nil
	}
	a, fa, err := loadNode(s.A, next)
	if err != nil {
		return nil, 0, err
	}
	b, fb, err := loadNode(s.B, next)
	if err != nil {
		return nil, 0, err
	}
	n := &layoutNode{dir: splitRight, ratio: s.Ratio, a: a, b: b}
	if s.Split == "down" {
		n.dir = splitDown
	}
	if n.ratio <= 0 || n.ratio >= 1 {
		n.ratio = 0.5
	}
	return n, max(fa, fb), nil
}

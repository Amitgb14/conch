package tui

import (
	"github.com/Amitgb14/conch/internal/proto"
)

// The scrollbar on a pane's right border: how far back through its history
// the screen is, and a thumb to drag it there directly. The wheel was the
// only way to move through a long conversation with the mouse, which says
// nothing about how much of it there is.
//
// The thumb is a space with a background, not a block glyph. █ measures one
// cell (its width is Ambiguous) and some fonts draw it two, which pushes
// every line with a thumb a column out and scatters the frame — the same
// trap the file icons keep to one cell by measuring. A coloured space is
// one cell in every font there is.

// scrollThumb is the rows of a track of h lines the thumb covers for a
// frame scrolled back by Offset of History lines: from, and how many.
// Nothing to scroll gives 0 lines.
func scrollThumb(f *proto.Frame, h int) (from, size int) {
	if f == nil || f.History <= 0 || h < 3 {
		return 0, 0
	}
	total := f.History + h
	size = max(h*h/total, 1)
	// Live (Offset 0) sits at the bottom; fully back sits at the top.
	from = (f.History - clamp(f.Offset, 0, f.History)) * (h - size) / f.History
	return clamp(from, 0, h-size), size
}

// scrollbarMark is what frameLinesBar draws on a leaf's right border.
func (m Model) scrollbarMark(v viewRef, h int) map[int]string {
	if v.Kind != kindPane {
		return nil
	}
	from, size := scrollThumb(m.frames[paneKey(v.Machine, v.PaneID)], h)
	if size == 0 {
		return nil
	}
	mark := map[int]string{}
	for i := from; i < from+size; i++ {
		mark[i] = styleThumb.Render(" ")
	}
	return mark
}

// offsetAt is the history offset a press at row i of a track of h lines
// asks for: the top is as far back as the pane goes, the bottom is live.
func offsetAt(f *proto.Frame, h, i int) int {
	if f == nil || f.History <= 0 || h < 2 {
		return 0
	}
	return clamp(f.History*(h-1-clamp(i, 0, h-1))/(h-1), 0, f.History)
}

// scrollTo moves the viewed pane to the history the scrollbar was grabbed
// at, in screen row y.
func (m *Model) scrollTo(y int) {
	if m.frame == nil || m.scrollH < 2 {
		return
	}
	want := offsetAt(m.frame, m.scrollH, y-m.scrollTop)
	m.scrollPane(want - m.offset)
}

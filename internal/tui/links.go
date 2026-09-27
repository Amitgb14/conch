package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// Links an agent printed. A login URL is the case that matters: an agent
// inside a sandbox asks you to open one, and the URL is longer than the
// pane — so it is wrapped, and drawn inside the agent's own box, which
// means selecting it with the mouse brings the border and the line break
// along with it:
//
//	│ …&scope=user%3Asessio │
//	│ ns%3Aclaude_code&code… │
//
//	Unknown scope: user:sessio│ns:claude_code
//
// conch has the screen, so it can put the URL back together: strip what
// the box drew, join the rows the text ran across, and hand over one
// link. ctrl+b u, or m on the pane.

// linksMax is how many links the menu offers: enough for a login flow,
// few enough to read.
const linksMax = 9

// linkStarts are the schemes worth offering.
var linkStarts = []string{"https://", "http://"}

// borderRunes are what a program's frame draws down the sides of a pane.
const borderRunes = "│┃|┆┇┊┋║▌▏▕"

// urlEnd is punctuation that ends a sentence rather than a URL. A closing
// bracket is left alone: URLs contain them, and a wrapped one may end on
// any character at all.
const urlEnd = ".,;:!?'\"”’"

// paneLinks are the links on a pane's screen, in the order they appear,
// each put back together across the rows it was wrapped over.
//
// The program did the wrapping, inside its own box, so there is no wrap
// flag to read: conch goes by shape. A row that fills the screen may have
// run on, and then the next row's first word is the rest of the link. That
// can be wrong — prose right under a full-width link would lend it a word
// — so what it found is shown before anything is opened, whole, rather
// than acted on quietly.
func paneLinks(lines []string) []string {
	rows := make([]row2, len(lines))
	width := 0
	for i, l := range lines {
		rows[i] = rowOf(ansi.Strip(l))
		width = max(width, rows[i].width)
	}
	var out []string
	seen := map[string]bool{}
	for y := range rows {
		text := rows[y].text
		for x := 0; x < len(text); {
			i, scheme := nextLink(text[x:])
			if i < 0 {
				break
			}
			i += x
			x = i + len(scheme)
			link := linkAt(rows, y, i, width)
			if link == "" || seen[link] {
				continue
			}
			seen[link] = true
			if out = append(out, link); len(out) == linksMax {
				return out
			}
		}
	}
	return out
}

// row2 is one row of the screen: what it says, where that starts, and how
// wide the row is. A wrapped block keeps its left edge, which is how a
// line that ran on is told from a new one.
type row2 struct {
	text   string // the content, with borders and padding off
	indent int    // the cell the content starts at
	width  int    // the whole row, trailing spaces off
}

func rowOf(line string) row2 {
	line = strings.TrimRight(line, " ")
	text := trimBorders(line)
	r := row2{text: text, width: len([]rune(line))}
	if text != "" {
		if i := strings.Index(line, text); i >= 0 {
			r.indent = len([]rune(line[:i]))
		}
	}
	return r
}

// nextLink is where the next link starts in s, and which scheme it is.
func nextLink(s string) (int, string) {
	at, found := -1, ""
	for _, scheme := range linkStarts {
		if i := strings.Index(s, scheme); i >= 0 && (at < 0 || i < at) {
			at, found = i, scheme
		}
	}
	return at, found
}

// linkWrapMax is how many rows a link is followed over, and linkMax how
// long it may come to. An OAuth URL is 400 characters and a narrow pane
// breaks it over a dozen rows, so the room has to be there; what keeps
// prose out is the shape — the row filled, the left edge kept — not a
// small number of rows.
const (
	linkWrapMax = 24
	linkMax     = 4096
)

// linkAt reads the link starting at column x of row y, following it onto
// the rows below while they look like the rest of it: the row it is on
// fills the screen, and the next keeps the same left edge. width is the
// widest row there is.
func linkAt(rows []row2, y, x, width int) string {
	var b strings.Builder
	rest := rows[y].text[x:]
	if cut := strings.IndexAny(rest, " \t"); cut >= 0 {
		return trimURLEnd(rest[:cut]) // it ended on this row
	}
	b.WriteString(rest)
	for n := y + 1; n < len(rows) && n-y <= linkWrapMax; n++ {
		prev, cur := rows[n-1], rows[n]
		switch {
		case prev.width < width:
			return trimURLEnd(b.String()) // the row had room to spare
		case cur.text == "" || cur.indent != prev.indent:
			return trimURLEnd(b.String()) // a line of its own, not the rest
		}
		part := cur.text
		if cut := strings.IndexAny(part, " \t"); cut >= 0 {
			part = part[:cut]
		}
		if i, _ := nextLink(part); part == "" || i == 0 {
			return trimURLEnd(b.String())
		}
		b.WriteString(part)
		if len(part) < len(cur.text) || b.Len() > linkMax {
			break // this row ended the line, or it has gone on long enough
		}
	}
	return trimURLEnd(b.String())
}

// trimBorders takes off what a program's frame drew at the sides, and the
// padding inside it, so a URL is not left with a border in the middle of
// it — and so a row that was wrapped starts with the text, not a space.
func trimBorders(s string) string {
	for {
		t := strings.TrimSpace(s)
		if t == "" {
			return ""
		}
		r := []rune(t)
		switch {
		case strings.ContainsRune(borderRunes, r[0]):
			s = string(r[1:])
		case strings.ContainsRune(borderRunes, r[len(r)-1]):
			s = string(r[:len(r)-1])
		default:
			return t
		}
	}
}

// trimURLEnd drops punctuation that ended the sentence rather than the
// link, and refuses anything with nothing after the scheme.
func trimURLEnd(s string) string {
	s = strings.TrimRight(s, urlEnd)
	for _, start := range linkStarts {
		if rest, ok := cutPrefix(s, start); ok {
			if rest == "" {
				return ""
			}
			return s
		}
	}
	return ""
}

func cutPrefix(s, prefix string) (string, bool) {
	if strings.HasPrefix(s, prefix) {
		return s[len(prefix):], true
	}
	return "", false
}

// openLinks offers the links on a pane's screen: opening one puts it on
// the clipboard as well, since a login URL often has to go somewhere else
// than this computer's browser.
func (m *Model) openLinks(r row) tea.Cmd {
	if r.kind != kindPane || m.frame == nil || m.viewing != r.paneID {
		m.setFlash("open the pane first (enter), then ctrl+b u for its links", true)
		return nil
	}
	links := paneLinks(m.frame.Lines)
	if len(links) == 0 {
		m.setFlash("no links on this pane's screen", true)
		return nil
	}
	mu := &menu{title: "Links on screen"}
	for i, link := range links {
		link := link
		key := ""
		if i < 9 {
			key = string(rune('1' + i))
		}
		mu.items = append(mu.items, menuItem{key, ansi.Truncate(link, 72, "…"), func(m *Model) tea.Cmd {
			return tea.Batch(copyText(link), openURL(link))
		}})
	}
	m.overlay = mu
	return nil
}

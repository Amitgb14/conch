package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

// A branch's deploy previews (Vercel, Netlify) in its view: a line per
// deploy integration with its newest deploy's state, the commit, and the
// preview's address — or, for a failed build, why — and v to open it.
// Sending a failed build to the agent needs nothing here: branch.ci_log
// reads it when no CI failed, so f in the review queue sends it. Releasing
// to production is the app's, behind a confirmation in the service's own
// words, and conch integration promote's.

// previewsEvery is how often a shown branch's previews are asked again;
// the server keeps them a moment itself.
const previewsEvery = 20 * time.Second

// previewsEntry is a branch's previews as last read.
type previewsEntry struct {
	at  time.Time
	out proto.BranchPreviews
}

type previewsMsg struct {
	key string
	out proto.BranchPreviews
	err error
}

func previewsKey(cv *changesView) string { return cv.machine + "|" + cv.projectID + "|" + cv.branch }

// askPreviews asks for the branch's previews, of a server that has them,
// at most every previewsEvery.
func (m *Model) askPreviews(cv *changesView) tea.Cmd {
	key := previewsKey(cv)
	if e, ok := m.previews[key]; ok && time.Since(e.at) < previewsEvery {
		return nil
	}
	c := m.clientOf(cv.machine)
	if c == nil || !m.hasCapability(cv.machine, proto.CapBranchPreviews) {
		return nil
	}
	if m.previews == nil {
		m.previews = map[string]previewsEntry{}
	}
	e := m.previews[key]
	e.at = time.Now() // asked; what was read stays shown meanwhile
	m.previews[key] = e
	params := proto.BranchCILogParams{ProjectID: cv.projectID, Branch: cv.branch}
	return func() tea.Msg {
		var out proto.BranchPreviews
		err := callCtx(c, proto.MethodBranchPreviews, params, &out)
		return previewsMsg{key: key, out: out, err: err}
	}
}

func (m *Model) receivePreviews(msg previewsMsg) {
	if msg.err != nil {
		return // the last answer stays; it is asked again later
	}
	if m.previews == nil {
		m.previews = map[string]previewsEntry{}
	}
	m.previews[msg.key] = previewsEntry{at: m.previews[msg.key].at, out: msg.out}
}

// deployLines is a line for each deploy integration the branch's project
// uses: its newest deploy, or why there is none to show.
func (m Model) deployLines(cv *changesView, w int) []string {
	e, ok := m.previews[previewsKey(cv)]
	if !ok {
		return nil
	}
	w = max(w, 1)
	var lines []string
	for _, src := range e.out.Integrations {
		switch {
		case src.Why != "":
			lines = append(lines, styleMuted.Render(ansi.Truncate(src.Service+": "+src.Why, w, "…")))
			continue
		case len(src.Deploys) == 0:
			lines = append(lines, styleMuted.Render(ansi.Truncate(src.Service+": no deploy of "+cv.branch+" yet", w, "…")))
			continue
		}
		d := src.Deploys[0]
		mark, word, style := "●", d.State, styleWarn
		right := ""
		switch d.State {
		case "ready":
			mark, style = "✓", styleOK
			right = hostOnly(d.URL) + " · v open"
		case "failed":
			mark, style = "✗", styleErr
			right = d.Error
			if d.Inspect != "" {
				right += " · v inspect"
			}
		case "canceled":
			mark, style = "–", styleMuted
		}
		left := src.Service + " " + style.Render(mark+" "+word)
		if d.Commit != "" {
			left += " " + styleMuted.Render(d.Commit[:min(len(d.Commit), 7)])
		}
		lines = append(lines, ansi.Truncate(spread(left, styleMuted.Render(right), w), w, "…"))
	}
	return lines
}

// openPreview opens the newest ready preview, else a failed or building
// one's page on its service.
func (cv *changesView) openPreview(m *Model) tea.Cmd {
	e := m.previews[previewsKey(cv)]
	for _, src := range e.out.Integrations {
		if len(src.Deploys) == 0 {
			continue
		}
		d := src.Deploys[0]
		if d.State == "ready" && d.URL != "" {
			return openURL(d.URL)
		}
		if d.Inspect != "" {
			return openURL(d.Inspect)
		}
	}
	m.setFlash("no deploy preview of "+cv.branch+" to open", true)
	return nil
}

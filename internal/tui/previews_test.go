package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

// previewsModel is a2Model with feat's previews read: Vercel's newest
// deploy as given, and Netlify's saying why it could not be asked.
func previewsModel(newest proto.DeployInfo) *Model {
	m := a2Model()
	m.previews = map[string]previewsEntry{localMachine + "|r1|feat": {at: time.Now(), out: proto.BranchPreviews{Integrations: []proto.DeploySource{
		{Source: "vercel", Service: "Vercel", Deploys: []proto.DeployInfo{newest, {ID: "dpl_0", State: "ready", URL: "https://old.vercel.app"}}},
		{Source: "netlify", Service: "Netlify", Why: "set $NETLIFY_AUTH_TOKEN where conch's server runs"},
	}}}}
	return m
}

var (
	readyDeploy  = proto.DeployInfo{ID: "dpl_1", State: "ready", URL: "https://web-app-3f2a91c-acme.vercel.app", Commit: "3f2a91c0ffee", Inspect: "https://vercel.com/acme/web-app/dpl_1"}
	failedDeploy = proto.DeployInfo{ID: "dpl_2", State: "failed", Error: "Build failed: type error in PlanCard.tsx", Commit: "9c41e0d", Inspect: "https://vercel.com/acme/web-app/dpl_2"}
)

func TestPreviewLines(t *testing.T) {
	cv := &changesView{machine: localMachine, projectID: "r1", branch: "feat"}
	out := a2Plain(cv.render(*previewsModel(readyDeploy), 140, 30))
	for _, want := range []string{"Vercel ✓ ready 3f2a91c", "web-app-3f2a91c-acme.vercel.app", "v open",
		"Netlify: set $NETLIFY_AUTH_TOKEN"} {
		if !strings.Contains(out, want) {
			t.Fatalf("render lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "old.vercel.app") {
		t.Fatalf("an older deploy shown:\n%s", out)
	}
	out = a2Plain(cv.render(*previewsModel(failedDeploy), 140, 30))
	if !strings.Contains(out, "Vercel ✗ failed 9c41e0d") || !strings.Contains(out, "Build failed: type error in PlanCard.tsx") || !strings.Contains(out, "v inspect") {
		t.Fatalf("failed:\n%s", out)
	}
	building := previewsModel(proto.DeployInfo{ID: "dpl_3", State: "building"})
	if out := a2Plain(cv.render(*building, 140, 30)); !strings.Contains(out, "Vercel ● building") {
		t.Fatalf("building:\n%s", out)
	}
	// None yet, and nothing read at all: a line, and none.
	none := a2Model()
	none.previews = map[string]previewsEntry{localMachine + "|r1|feat": {out: proto.BranchPreviews{Integrations: []proto.DeploySource{{Source: "vercel", Service: "Vercel"}}}}}
	if out := a2Plain(cv.render(*none, 140, 30)); !strings.Contains(out, "Vercel: no deploy of feat yet") {
		t.Fatalf("none yet:\n%s", out)
	}
	if lines := a2Model().deployLines(cv, 80); len(lines) != 0 {
		t.Fatalf("nothing read: %q", lines)
	}
}

// filesTop counts the preview lines, so a click lands on the file drawn.
func TestPreviewLinesFilesTop(t *testing.T) {
	m := previewsModel(failedDeploy)
	cv := &changesView{machine: localMachine, projectID: "r1", branch: "feat"}
	d := a2Changes()
	cv.data = &d
	lines := cv.render(*m, 120, 40)
	top := cv.filesTop(*m)
	if top < 1 || top >= len(lines) || !strings.Contains(ansi.Strip(lines[top-1]), "Uncommitted changes") {
		t.Fatalf("filesTop %d:\n%s", top, a2Plain(lines))
	}
}

func TestPreviewLinesWidths(t *testing.T) {
	for _, d := range []proto.DeployInfo{readyDeploy, failedDeploy} {
		m := previewsModel(d)
		cv := &changesView{machine: localMachine, projectID: "r1", branch: "feat"}
		for _, w := range []int{1, 20, 39, 80, 200} {
			lines := m.deployLines(cv, w)
			if len(lines) != 2 {
				t.Fatalf("width %d: %d lines", w, len(lines))
			}
			for i, l := range lines {
				if ansi.StringWidth(l) > w {
					t.Fatalf("width %d, line %d is %d wide: %q", w, i, ansi.StringWidth(l), ansi.Strip(l))
				}
			}
		}
	}
}

// v opens the newest ready preview, else a failed one's page on the
// service; with nothing to open it says so.
func TestPreviewOpenKey(t *testing.T) {
	var opened []string
	was := openInBrowser
	t.Cleanup(func() { openInBrowser = was })
	openInBrowser = func(u string) error { opened = append(opened, u); return nil }
	for _, tc := range []struct {
		d    proto.DeployInfo
		want string
	}{{readyDeploy, readyDeploy.URL}, {failedDeploy, failedDeploy.Inspect}} {
		m := previewsModel(tc.d)
		cv := &changesView{machine: localMachine, projectID: "r1", branch: "feat"}
		_, cmd := cv.key(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
		if cmd == nil {
			t.Fatalf("%s: nothing opened", tc.d.State)
		}
		a2Run(cmd)
		if len(opened) == 0 || opened[len(opened)-1] != tc.want {
			t.Fatalf("%s: opened %q", tc.d.State, opened)
		}
	}
	m := a2Model()
	cv := &changesView{machine: localMachine, projectID: "r1", branch: "feat"}
	if _, cmd := cv.key(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")}); cmd != nil || !strings.Contains(m.flash, "no deploy preview") {
		t.Fatalf("nothing to open: %q", m.flash)
	}
}

// Asked of a server that has previews, at most every so often; never of
// an older one.
func TestAskPreviews(t *testing.T) {
	m := a2Model()
	c, peer := a1FakeClient(t, proto.CapBranchPreviews)
	m.machines[0].c, m.machines[0].server = c, c.Server
	peer.setResult(proto.MethodBranchPreviews, proto.BranchPreviews{Integrations: []proto.DeploySource{
		{Source: "vercel", Service: "Vercel", Deploys: []proto.DeployInfo{readyDeploy}}}})
	cv := &changesView{machine: localMachine, projectID: "r1", branch: "feat"}
	cmd := m.askPreviews(cv)
	if cmd == nil {
		t.Fatal("not asked")
	}
	if m.askPreviews(cv) != nil {
		t.Fatal("asked again at once")
	}
	for _, msg := range a2Run(cmd) {
		if pm, ok := msg.(previewsMsg); ok {
			m.receivePreviews(pm)
		}
	}
	if lines := m.deployLines(cv, 120); len(lines) != 1 || !strings.Contains(ansi.Strip(lines[0]), "Vercel ✓ ready") {
		t.Fatalf("lines %q", lines)
	}
	old := a2Model()
	oc, _ := a1FakeClient(t)
	old.machines[0].c, old.machines[0].server = oc, oc.Server
	if old.askPreviews(cv) != nil {
		t.Fatal("asked an older server")
	}
}

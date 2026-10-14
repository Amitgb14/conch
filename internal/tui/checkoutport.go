package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/remote"
	"github.com/Amitgb14/conch/internal/sandbox"
)

// Every checkout is given ports of its own (CONCH_PORT, in each pane
// started there), so a dev server an agent starts in one worktree doesn't
// collide with another's. L opens the first of them in the browser: here
// directly, on an ssh machine through a port forward on conch's own
// connection, and in a sandbox through the provider's link — the same
// keystroke wherever the agent is working.

// forwardPort reaches a port on an ssh machine from this computer; tests
// replace it rather than run ssh.
var forwardPort = remote.ForwardPort

// checkoutOf is the checkout a row stands for: a worktree's branch, a pane
// in one, or the project itself (its main checkout).
func (m Model) checkoutOf(r row) (mid string, proj *proto.ProjectInfo, wt proto.WorktreeInfo, ok bool) {
	pl := m.placeOf(r)
	proj = m.project(pl.machine, pl.projectID)
	if proj == nil || !proj.Git || pl.dir == "" {
		return pl.machine, proj, wt, false
	}
	best := -1
	for i, w := range proj.Worktrees {
		if (pl.dir == w.Path || strings.HasPrefix(pl.dir, w.Path+"/")) && (best < 0 || len(w.Path) > len(proj.Worktrees[best].Path)) {
			best = i
		}
	}
	if best < 0 {
		return pl.machine, proj, wt, false
	}
	return pl.machine, proj, proj.Worktrees[best], true
}

// openCheckoutPort opens the first of a checkout's ports in the browser.
func (m *Model) openCheckoutPort(r row) tea.Cmd {
	mid, proj, wt, ok := m.checkoutOf(r)
	switch {
	case proj == nil || !proj.Git:
		m.setFlash("select a branch or a pane in a git project to open its preview", true)
		return nil
	case !ok:
		m.setFlash("this branch isn't checked out anywhere yet; open a terminal or an agent on it first", true)
		return nil
	case !m.hasCapability(mid, proto.CapWorktreeSetup):
		m.setFlash("the conch server on "+m.machineLabel(mid)+" predates per-checkout ports; reload it (conch server reload)", true)
		return nil
	case wt.Port == 0:
		m.setFlash("this checkout has no ports yet: open a terminal or an agent in it and conch gives it some", true)
		return nil
	}
	mach := m.machine(mid)
	if mach == nil {
		return nil
	}
	port, label := wt.Port, mach.label
	if mid == localMachine {
		return localPreview(mid, port, port)
	}
	if provider, id, isSandbox := mach.sandbox(); isSandbox {
		p, err := openSandboxProvider(provider)
		if err != nil {
			m.setFlash(err.Error(), true)
			return nil
		}
		pv, can := p.(sandbox.Previewer)
		if !can {
			m.setFlash(fmt.Sprintf("%s cannot give a link to a port", providerLabel(provider)), true)
			return nil
		}
		m.setFlash(fmt.Sprintf("asking %s for a link to port %d…", providerLabel(provider), port), false)
		return sandboxPreview(mid, label, pv, id, port)
	}
	target := mach.target
	m.setFlash(fmt.Sprintf("forwarding port %d from %s…", port, label), false)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		local, err := forwardPort(ctx, target, port)
		if err != nil {
			return previewDoneMsg{machine: mid, err: err}
		}
		return localPreview(mid, local, port)()
	}
}

// localPreview opens localhost:local, which is the checkout's port (on
// this computer, or forwarded from where it is); when nothing answers it
// says how to start a server there rather than open an error page.
func localPreview(mid string, local, port int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		link := fmt.Sprintf("http://localhost:%d", local)
		if code, _ := checkPreview(ctx, link); code == 0 {
			return previewDoneMsg{machine: mid, url: link, err: errString(fmt.Sprintf(
				"nothing answers on port %d yet. Start the dev server on it: it is $CONCH_PORT in every terminal of this checkout (npm run dev -- --port $CONCH_PORT, or PORT=$CONCH_PORT).", port))}
		}
		return previewDoneMsg{machine: mid, url: link}
	}
}

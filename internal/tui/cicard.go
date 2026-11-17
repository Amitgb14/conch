package tui

import (
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/integration"
	"github.com/Amitgb14/conch/internal/proto"
)

// A red check from a service conch cannot read is a dead end unless the
// branch says so: the agent was not sent the log, and nothing else
// explains why. So the branch's view puts a card above its files — the
// check, the service, the one action that fixes it (I installs its
// integration), the check's own page (O), and why conch thinks the
// project uses that service — and lists every check with where it ran.
// Once an integration reads it, the server marks the check readable and
// the card goes; the failure is then handled as an Actions one is.

// ciListMax is how many checks the branch's view lists before "… N more".
const ciListMax = 4

// unreadableFailure is the first failing check of a service conch knows
// and cannot read. An older server names no source, so it never has one.
func unreadableFailure(pr *proto.PRInfo) (proto.PRCheck, bool) {
	if pr == nil {
		return proto.PRCheck{}, false
	}
	for _, c := range pr.Runs {
		if c.State == "fail" && !c.Readable && c.Source != "" && c.Source != integration.SourceActions {
			return c, true
		}
	}
	return proto.PRCheck{}, false
}

// ciLines is the card, when there is one, and the checks, failures first.
func (m Model) ciLines(cv *changesView, w int) []string {
	pr := m.branchPR(cv.machine, cv.projectID, cv.branch)
	if pr == nil || len(pr.Runs) == 0 {
		return nil
	}
	w = max(w, 1)
	var lines []string
	if ck, ok := unreadableFailure(pr); ok {
		svc := integration.Label(ck.Source, nil)
		who := "the agent"
		if a := m.branchAgent(cv.machine, cv.projectID, cv.branch); a != nil {
			who = agentLabel(a.Agent.Name)
		}
		lines = append(lines, styleWarn.Render(ansi.Truncate(fmt.Sprintf("⚠ %s failed — conch can't read %s yet, so %s wasn't sent the log.", ck.Name, svc, who), w, "…")))
		keys := fmt.Sprintf("  I install %s · O open in %s · r re-run failed jobs", svc, svc)
		why := ""
		if found := m.ciWhy[cv.machine+"|"+cv.projectID][ck.Source]; found != "" {
			why = styleMuted.Render("found " + found)
		}
		lines = append(lines, ansi.Truncate(spread(styleWarn.Render(keys), why, w), w, "…"))
	}
	runs := slices.Clone(pr.Runs)
	order := map[string]int{"fail": 0, "pending": 1}
	slices.SortStableFunc(runs, func(a, b proto.PRCheck) int {
		ra, ok := order[a.State]
		if !ok {
			ra = 2
		}
		rb, ok := order[b.State]
		if !ok {
			rb = 2
		}
		return ra - rb
	})
	for i, c := range runs {
		if i == ciListMax {
			lines = append(lines, styleMuted.Render(ansi.Truncate(fmt.Sprintf("  … %d more checks", len(runs)-ciListMax), w, "…")))
			break
		}
		mark, word, style := "●", "running", styleWarn
		switch c.State {
		case "pass":
			mark, word, style = "✓", "passed", styleOK
		case "fail":
			mark, word, style = "✗", "failed", styleErr
		}
		where := hostOnly(c.URL)
		if c.State == "pass" && !c.Readable && c.Source != "" && c.Source != integration.SourceActions {
			where = "no integration needed to see it"
		}
		left := style.Render(mark) + " " + c.Name + styleMuted.Render(" — "+word)
		lines = append(lines, ansi.Truncate(spread(left, styleMuted.Render(where), w), w, "…"))
	}
	return lines
}

// installCI installs the integration that would read the card's check,
// in a terminal, checked against this branch's checkout.
func (cv *changesView) installCI(m *Model) tea.Cmd {
	ck, ok := unreadableFailure(m.branchPR(cv.machine, cv.projectID, cv.branch))
	if !ok {
		m.setFlash("no failing check here needs an integration", true)
		return nil
	}
	svc := integration.Label(ck.Source, nil)
	if cv.machine != localMachine {
		// The checks are read by that machine's server, from its own files.
		m.setFlash(fmt.Sprintf("%s reads its own checks: run conch integration install %s there", m.machineLabel(cv.machine), ck.Source), true)
		return nil
	}
	args := []string{"install"}
	if dir := m.branchDir(cv.machine, cv.projectID, cv.branch); dir != "" {
		args = append(args, "-cwd", dir)
	}
	args = append(args, ck.Source)
	return conchInPane(m, "install "+svc, "installing "+svc+" in this terminal", integrationScript(args...))
}

// openCICheck opens the card's check on its service's site.
func (cv *changesView) openCICheck(m *Model) tea.Cmd {
	ck, ok := unreadableFailure(m.branchPR(cv.machine, cv.projectID, cv.branch))
	if !ok || ck.URL == "" {
		m.setFlash("no failing check here to open; o opens the pull request", true)
		return nil
	}
	return openURL(ck.URL)
}

// rerunCI runs a red branch's failed jobs again: GitHub Actions through
// gh, or a CI integration that can (branch.ci_rerun).
func (cv *changesView) rerunCI(m *Model) tea.Cmd {
	pr := m.branchPR(cv.machine, cv.projectID, cv.branch)
	if pr == nil || pr.Checks != "fail" && !slices.ContainsFunc(pr.Runs, func(c proto.PRCheck) bool { return c.State == "fail" }) {
		m.setFlash("nothing failed on "+cv.branch+" to re-run", true)
		return nil
	}
	c := m.clientOf(cv.machine)
	switch {
	case c == nil:
		m.setFlash(m.offlineText(cv.machine), true)
		return nil
	case !m.hasCapability(cv.machine, proto.CapBranchCIRerun):
		m.setFlash("the server there predates re-running CI; reload it (conch server reload)", true)
		return nil
	case m.hasCapability(cv.machine, proto.CapPRCheckRerun) && !slices.ContainsFunc(pr.Runs, func(c proto.PRCheck) bool { return c.State == "fail" && c.Rerun }):
		// The server says which checks can be run again, and none that
		// failed can: say so rather than ask it to try.
		m.setFlash(noRerunWords(pr.Runs), true)
		return nil
	}
	m.setFlash("re-running the failed jobs on "+cv.branch+"…", false)
	params := proto.BranchCILogParams{ProjectID: cv.projectID, Branch: cv.branch}
	return func() tea.Msg {
		var out proto.BranchCIRerun
		if err := callCtx(c, proto.MethodBranchCIRerun, params, &out); err != nil {
			return errMsg{err}
		}
		return flashMsg(rerunWords(out, params.Branch))
	}
}

// rerunWords says what a re-run started: one run, or every run that
// failed at the head, and any that could not be started.
func rerunWords(out proto.BranchCIRerun, branch string) string {
	runs := "run " + out.Run
	if len(out.Runs) > 1 {
		runs = "runs " + strings.Join(out.Runs, ", ")
	}
	s := fmt.Sprintf("re-running %s's failed jobs on %s (%s)", firstNonEmpty(out.Service, out.Source), branch, runs)
	if out.Note != "" {
		s += "; not re-run: " + out.Note
	}
	return s
}

// noRerunWords says why nothing that failed can be run again.
func noRerunWords(runs []proto.PRCheck) string {
	for _, c := range runs {
		if c.State == "fail" && c.Source != "" {
			return integration.Label(c.Source, integration.Cached()) + " cannot re-run from conch: its integration has no re-run"
		}
	}
	return "nothing that failed can be re-run from conch"
}

// branchDir is where a branch is checked out, else its project's folder.
func (m Model) branchDir(machine, projectID, branch string) string {
	proj := m.project(machine, projectID)
	if proj == nil {
		return ""
	}
	for _, wt := range proj.Worktrees {
		if wt.Branch == branch {
			return wt.Path
		}
	}
	return proj.Path
}

// ciDetectMsg is why a project is thought to use each service, by name.
type ciDetectMsg struct {
	key string
	why map[string]string
}

// detectCI asks once per project why it is thought to use each service —
// only when a card would say it, and of a server that can tell.
func (m *Model) detectCI(cv *changesView) tea.Cmd {
	if _, ok := unreadableFailure(m.branchPR(cv.machine, cv.projectID, cv.branch)); !ok {
		return nil
	}
	key := cv.machine + "|" + cv.projectID
	if _, asked := m.ciWhy[key]; asked || !m.hasCapability(cv.machine, proto.CapIntegrationDetect) {
		return nil
	}
	if m.ciWhy == nil {
		m.ciWhy = map[string]map[string]string{}
	}
	m.ciWhy[key] = map[string]string{} // asked; a failure leaves it empty
	c, pid := m.clientOf(cv.machine), cv.projectID
	return func() tea.Msg {
		var out proto.IntegrationDetect
		why := map[string]string{}
		if err := callCtx(c, proto.MethodIntegrationDetect, proto.IntegrationDetectParams{ProjectID: pid}, &out); err == nil {
			for _, u := range out.Integrations {
				why[u.Name] = u.Why
			}
		}
		return ciDetectMsg{key: key, why: why}
	}
}

func (m *Model) receiveCIDetect(msg ciDetectMsg) {
	if m.ciWhy == nil {
		m.ciWhy = map[string]map[string]string{}
	}
	m.ciWhy[msg.key] = msg.why
}

package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

// Settings → Agents → Agent accounts: each machine's accounts as its
// server keeps them (internal/accounts), with what each has used of its
// plan. Adding one is `conch account add`, since signing in is done in
// the agent; what is here is what somebody looks at, and failover.

// accountsState is what a machine's server last said about its accounts.
type accountsState struct {
	list    proto.AccountList
	err     string
	loading bool
}

type accountsMsg struct {
	machine string
	list    proto.AccountList
	err     error
}

// loadAccounts asks a machine for its accounts.
func (m *Model) loadAccounts(mid string) tea.Cmd {
	return m.accountsCall(mid, "", nil)
}

// accountsCall makes a change on a machine (when method is set), then
// reads its accounts again.
func (m *Model) accountsCall(mid, method string, params any) tea.Cmd {
	if m.accounts == nil {
		m.accounts = map[string]*accountsState{}
	}
	c := m.clientOf(mid)
	switch {
	case m.machine(mid) == nil || c == nil:
		m.accounts[mid] = &accountsState{err: m.offlineText(mid)}
		return nil
	case len(c.MissingCapabilities([]string{proto.CapAccounts})) > 0:
		m.accounts[mid] = &accountsState{err: "its server predates agent accounts; reload or upgrade it"}
		return nil
	}
	old := m.accounts[mid]
	st := &accountsState{loading: true}
	if old != nil {
		st.list = old.list
	}
	m.accounts[mid] = st
	return func() tea.Msg {
		if method != "" {
			if err := callCtx(c, method, params, nil); err != nil {
				return accountsMsg{machine: mid, err: err}
			}
		}
		var l proto.AccountList
		err := callCtx(c, proto.MethodAccountList, nil, &l)
		return accountsMsg{machine: mid, list: l, err: err}
	}
}

func (m *Model) receiveAccounts(msg accountsMsg) tea.Cmd {
	if m.accounts == nil {
		m.accounts = map[string]*accountsState{}
	}
	if msg.err != nil {
		st := &accountsState{err: msg.err.Error()}
		if old := m.accounts[msg.machine]; old != nil {
			st.list = old.list
		}
		m.accounts[msg.machine] = st
		return nil
	}
	m.accounts[msg.machine] = &accountsState{list: msg.list}
	return nil
}

// nextFailover is the failover line a press moves to: off, 80, 90, 95.
func nextFailover(at int) int {
	switch {
	case at <= 0:
		return 80
	case at < 90 && at >= 80:
		return 90
	case at >= 90 && at < 95:
		return 95
	case at >= 95:
		return 0
	}
	return 80
}

// accountName is one row's name: the agent, and the account when it is
// not the agent's own.
func accountName(a proto.AccountInfo) string {
	if a.Default || a.Name == "" {
		return agentLabel(a.Agent)
	}
	return agentLabel(a.Agent) + " · " + a.Name
}

// accountDetail is what an account has used and how many agents it runs.
func accountDetail(a proto.AccountInfo, now time.Time) string {
	var parts []string
	if a.Limits != nil {
		if chip := limitsChip(*a.Limits, now); chip != "" {
			// The chip begins with the agent's name, which the row says.
			chip = strings.TrimPrefix(ansi.Strip(chip), accountAgent(a.Limits.Agent, a.Limits.Account)+" ")
			parts = append(parts, chip)
		}
	}
	switch {
	case a.Agents == 1:
		parts = append(parts, "1 agent")
	case a.Agents > 1:
		parts = append(parts, fmt.Sprintf("%d agents", a.Agents))
	}
	return strings.Join(parts, " · ")
}

func (s *settings) accountItems(m *Model) []settingItem {
	items := []settingItem{
		{header: true, label: "Agent accounts", detail: "several subscriptions of one agent, each signed in once"},
		{label: styleMuted.Render("‹ Agents"), detail: styleMuted.Render("esc"),
			run: func(m *Model) tea.Cmd { s.openPage(""); return nil }},
		{label: styleMuted.Render("  conch account add claude work, then conch new -agent claude -account work to sign in")},
	}
	now := time.Now()
	for _, mach := range m.machines {
		mid := mach.id
		st := m.accounts[mid]
		items = append(items, settingItem{})
		switch {
		case st == nil:
			items = append(items, settingItem{label: mach.label, detail: "enter reads it", run: func(m *Model) tea.Cmd { return m.loadAccounts(mid) }})
			continue
		case st.err != "" && len(st.list.Accounts) == 0:
			items = append(items, settingItem{label: mach.label, detail: styleErr.Render(st.err), run: func(m *Model) tea.Cmd { return m.loadAccounts(mid) }})
			continue
		}
		head := ""
		switch {
		case st.loading:
			head = "reading…"
		case st.err != "":
			head = st.err
		}
		items = append(items, settingItem{header: true, label: mach.label, detail: head})
		for _, a := range st.list.Accounts {
			items = append(items, settingItem{label: accountName(a), detail: accountDetail(a, now)})
		}
		for _, p := range st.list.Pins {
			items = append(items, settingItem{label: m.tildify(mid, p.Path), detail: "→ " + p.Account})
		}
		at := st.list.FailoverAt
		detail := "off"
		if at > 0 {
			detail = fmt.Sprintf("at %d%%: new agents move to the next account", at)
		}
		items = append(items, settingItem{label: "Failover", detail: detail, run: func(m *Model) tea.Cmd {
			return m.accountsCall(mid, proto.MethodAccountFailover, proto.AccountFailoverParams{At: nextFailover(at)})
		}})
	}
	return items
}

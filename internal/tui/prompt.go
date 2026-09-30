package tui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/proto"
)

// Prompting an agent from the tree: one message into a pane through
// agent.prompt, which refuses an agent that is waiting on a question of its
// own, and then saying when the work that answers it ends.
//
// The waiting follows the command line's rule (cmd/conch/prompt.go): the
// turn that answers is the Turn the result names, and it is over when the
// agent reaches a state worth reporting with that Turn behind it — or the
// moment it asks a question, since none was open when the message went in.

// promptWatch is a message sent and not yet answered.
type promptWatch struct {
	agent string // the agent it went to, so a pane taken over is noticed
	turn  int
	sent  time.Time
}

type promptSentMsg struct {
	machine, pane string
	res           proto.AgentPromptResult
	err           error
}

func promptKey(mid, pane string) string { return mid + "\x00" + pane }

// openPrompt asks for the message to send to a pane's agent.
func (m *Model) openPrompt(mid, pane string) tea.Cmd {
	p := m.pane(mid, pane)
	if p == nil || p.Agent == nil {
		m.setFlash("no agent in that pane", true)
		return nil
	}
	if !m.hasCapability(mid, proto.CapAgentPrompt) {
		m.setFlash("the server there predates prompting an agent; reload it", true)
		return nil
	}
	name := p.DisplayName()
	text := []string{"Sent to " + name + " as its next message, the way you would type it."}
	if p.Agent.State == proto.AgentBlocked {
		text = append(text, styleWarn.Render("It is waiting for an answer of its own — open it and answer that first."))
	}
	d := newDialog(*m, " Prompt "+p.Agent.Name+" ", text, []string{"Message"}, []string{""})
	d.submit = func(m *Model, v []string) tea.Cmd {
		msg := strings.TrimSpace(v[0])
		if msg == "" {
			return func() tea.Msg { return errMsg{errString("nothing to send")} }
		}
		return m.sendPrompt(mid, pane, msg)
	}
	m.overlay = d
	return d.focusCmd()
}

// sendPrompt hands the message over and remembers the turn that answers it.
func (m *Model) sendPrompt(mid, pane, text string) tea.Cmd {
	c := m.clientOf(mid)
	if c == nil {
		m.setFlash(m.offlineText(mid), true)
		return nil
	}
	return func() tea.Msg {
		var res proto.AgentPromptResult
		err := callCtx(c, proto.MethodAgentPrompt, proto.AgentPromptParams{ID: pane, Text: text}, &res)
		return promptSentMsg{machine: mid, pane: pane, res: res, err: err}
	}
}

func (m *Model) receivePromptSent(msg promptSentMsg) tea.Cmd {
	name := msg.pane
	if p := m.pane(msg.machine, msg.pane); p != nil {
		name = p.DisplayName()
	}
	if msg.err != nil {
		// An agent asking a question is refused by the server, and its own
		// words say so better than anything invented here.
		m.setFlash(msg.err.Error(), true)
		return nil
	}
	if m.prompts == nil {
		m.prompts = map[string]*promptWatch{}
	}
	m.prompts[promptKey(msg.machine, msg.pane)] = &promptWatch{agent: msg.res.Agent, turn: msg.res.Turn, sent: time.Now()}
	m.setFlash("sent to "+name+" · you'll be told when that work ends", false)
	return nil
}

// watchPrompt reports the end of work a message of yours started, once.
// Called for every pane update, beside the other watchers.
func (m *Model) watchPrompt(mach *machine, info proto.PaneInfo, gone bool) tea.Cmd {
	key := promptKey(mach.id, info.ID)
	w := m.prompts[key]
	if w == nil {
		return nil
	}
	name := info.DisplayName()
	var note string
	switch {
	case gone:
		note = name + " ended before it answered"
	case info.Agent == nil || info.Agent.Name != w.agent:
		note = name + " is not " + w.agent + " any more"
	case info.Agent.State == proto.AgentBlocked:
		note = name + " is waiting for you"
	case (info.Agent.State == proto.AgentDone || info.Agent.State == proto.AgentIdle) && info.Agent.Turn >= w.turn:
		note = name + " finished what you asked"
	default:
		return nil // still working, or a turn that started before the message
	}
	delete(m.prompts, key)
	if m.isViewing(mach.id, info.ID) || m.silenced(time.Now()) {
		m.setFlash(note, false)
		return nil
	}
	m.setFlash(note, false)
	title := "conch · " + w.agent
	if mach.id != localMachine {
		title += " on " + mach.label
	}
	return notify(m.cfg.Notify, title, note)
}

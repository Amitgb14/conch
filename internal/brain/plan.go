package brain

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// World is what the brain knows when planning: every connected machine,
// its projects and panes, and what the user has selected.
type World struct {
	Machines []Machine `json:"machines"`
	// Selected names what the user is looking at, e.g. "project api on
	// local" — the default target for vague requests.
	Selected     string `json:"selected,omitempty"`
	DefaultAgent string `json:"default_agent"`
}

// Machine is a computer running a conch server.
type Machine struct {
	ID       string    `json:"id"`
	Label    string    `json:"label"`
	Online   bool      `json:"online"`
	Agents   []string  `json:"installed_agents,omitempty"`
	Projects []Project `json:"projects,omitempty"`
	Panes    []Pane    `json:"panes,omitempty"`
}

// Project is a repository or folder on a machine.
type Project struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Path     string   `json:"path"`
	Git      bool     `json:"git"`
	Base     string   `json:"base,omitempty"`
	Branches []string `json:"branches,omitempty"` // "name" or "name (worktree)"
}

// Pane is a terminal, possibly running an agent.
type Pane struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Agent   string `json:"agent,omitempty"`
	State   string `json:"state,omitempty"` // idle, working, blocked, done
	Project string `json:"project,omitempty"`
	Branch  string `json:"branch,omitempty"`
	Cwd     string `json:"cwd"`
	Summary string `json:"summary,omitempty"`
}

// Action types.
const (
	ActStartTask  = "start_task"  // new branch + worktree + agent with a prompt
	ActStartAgent = "start_agent" // agent in an existing project/branch
	ActSend       = "send"        // type a message into a pane and press enter
	ActFocus      = "focus"       // show a pane
	ActClose      = "close"       // close a pane
	// ActShare hands one agent's conversation to another agent, new or
	// running; ActBroadcast sends one message to several agents at once.
	ActShare     = "share"
	ActBroadcast = "broadcast"
)

// Action is one step of a plan.
type Action struct {
	Type    string `json:"type"`
	Machine string `json:"machine,omitempty"`
	Project string `json:"project,omitempty"` // project id
	Branch  string `json:"branch,omitempty"`
	Base    string `json:"base,omitempty"`
	Agent   string `json:"agent,omitempty"`
	Prompt  string `json:"prompt,omitempty"`
	Pane    string `json:"pane,omitempty"`
	Text    string `json:"text,omitempty"`
	// ToPane is where a share goes when it continues in an agent that is
	// already running; empty starts a new one.
	ToPane string `json:"to_pane,omitempty"`
	// Panes are a broadcast's recipients, all on Machine.
	Panes []string `json:"panes,omitempty"`
}

// Plan is the brain's answer to a request: a reply for the user and the
// actions it proposes. Nothing runs until the user confirms.
type Plan struct {
	Reply   string   `json:"reply"`
	Actions []Action `json:"actions"`
}

var planSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"reply": map[string]any{"type": "string", "description": "Short answer or explanation for the user (plain text, at most 3 sentences)."},
		"actions": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"type":    map[string]any{"type": "string", "enum": []string{ActStartTask, ActStartAgent, ActSend, ActFocus, ActClose, ActShare, ActBroadcast}},
					"machine": map[string]any{"type": "string", "description": "machine id"},
					"project": map[string]any{"type": "string", "description": "project id"},
					"branch":  map[string]any{"type": "string"},
					"base":    map[string]any{"type": "string"},
					"agent":   map[string]any{"type": "string"},
					"prompt":  map[string]any{"type": "string"},
					"pane":    map[string]any{"type": "string", "description": "pane id"},
					"text":    map[string]any{"type": "string"},
					"to_pane": map[string]any{"type": "string", "description": "pane id a share continues in"},
					"panes": map[string]any{"type": "array", "description": "pane ids a broadcast goes to",
						"items": map[string]any{"type": "string"}},
				},
				"required": []string{"type", "machine"},
			},
		},
	},
	"required": []string{"reply", "actions"},
}

const planSystem = `You are the brain of conch, a terminal orchestrator that runs AI coding agents (Claude Code, Codex, Gemini CLI, OpenCode) in terminals on the user's machines. You turn the user's request into a plan of conch actions, or answer a question about their agents.

Actions:
- start_task: create a new branch and git worktree in a git project and start an agent there with a prompt. Fields: machine, project, prompt, agent (optional), branch (optional short kebab-case name; derived from the prompt when omitted), base (optional).
- start_agent: start an agent in an existing project checkout; with branch, in that branch's worktree (created if needed). Fields: machine, project, agent, branch (optional), prompt (optional first message).
- send: type a message to an agent already running in a pane and press enter. Fields: machine, pane, text.
- focus: show a pane. Fields: machine, pane.
- close: close a pane (stops its process). Fields: machine, pane. Only when the user clearly asks.
- share: hand the conversation of the agent in pane to another agent, so it continues the work with the context. Fields: machine, pane (the agent whose conversation is handed over), agent (which agent continues; defaults to the default agent), to_pane (optional: an agent already running that should continue it instead of starting a new one). Use for "hand this thread to Codex", "get a second opinion from Gemini on what Claude just did".
- broadcast: send one message to several agents at once and submit it. Fields: machine, panes (pane ids on that machine), text. Use for "tell everyone to run the tests"; for agents on several machines, emit one broadcast per machine.

Rules:
- Use only machine, project and pane ids from the state below. Use only installed agents; default to the default agent.
- To split a goal into parallel work, emit several start_task actions, each with a self-contained prompt that tells the agent exactly what to do and what not to touch, so agents don't collide.
- Prompts for agents should be specific and complete; they don't see this conversation.
- If the request is a question (what is waiting, what is agent X doing), answer in reply with no actions.
- If the request is ambiguous or refers to something that doesn't exist, explain in reply and emit no actions.
- Never send text that answers an agent's permission prompt unless the user explicitly says what to answer.`

// MakePlan asks the model for a plan.
func MakePlan(ctx context.Context, p Provider, w World, request string) (Plan, error) {
	state, _ := json.MarshalIndent(w, "", " ")
	prompt := fmt.Sprintf("Current state (%s):\n%s\n\nUser request: %s", time.Now().Format("2006-01-02 15:04"), state, request)
	res, err := p.Complete(ctx, Request{System: planSystem, Prompt: prompt, Schema: planSchema})
	if err != nil {
		return Plan{}, err
	}
	var plan Plan
	if err := json.Unmarshal(res.JSON, &plan); err != nil {
		return Plan{}, fmt.Errorf("unexpected plan from the model: %w", err)
	}
	return plan, nil
}

// Validate checks an action against the world and fills defaults. It
// returns why the action can't run.
func (w World) Validate(a *Action) error {
	m := w.machine(a.Machine)
	if m == nil {
		return fmt.Errorf("unknown machine %q", a.Machine)
	}
	if !m.Online {
		return fmt.Errorf("%s is offline", m.Label)
	}
	switch a.Type {
	case ActStartTask, ActStartAgent:
		if a.Agent == "" {
			a.Agent = w.DefaultAgent
		}
		proj := m.project(a.Project)
		if proj == nil {
			return fmt.Errorf("unknown project %q on %s", a.Project, m.Label)
		}
		if len(m.Agents) > 0 && !contains(m.Agents, a.Agent) {
			return fmt.Errorf("%s is not installed on %s", a.Agent, m.Label)
		}
		if a.Type == ActStartTask {
			if !proj.Git {
				return fmt.Errorf("%s is not a git repository", proj.Name)
			}
			if strings.TrimSpace(a.Prompt) == "" {
				return fmt.Errorf("a task needs a prompt")
			}
		}
	case ActSend, ActFocus, ActClose:
		pane := m.pane(a.Pane)
		if pane == nil {
			return fmt.Errorf("unknown pane %q on %s", a.Pane, m.Label)
		}
		if a.Type == ActSend && strings.TrimSpace(a.Text) == "" {
			return fmt.Errorf("nothing to send")
		}
	case ActShare:
		from := m.pane(a.Pane)
		switch {
		case from == nil:
			return fmt.Errorf("unknown pane %q on %s", a.Pane, m.Label)
		case from.Agent == "":
			return fmt.Errorf("%s is a terminal, not an agent", from.Name)
		}
		if a.Agent == "" {
			a.Agent = w.DefaultAgent
		}
		if a.ToPane != "" {
			to := m.pane(a.ToPane)
			switch {
			case to == nil:
				return fmt.Errorf("unknown pane %q on %s", a.ToPane, m.Label)
			case to.Agent == "":
				return fmt.Errorf("%s is a terminal, not an agent", to.Name)
			case to.ID == from.ID:
				return fmt.Errorf("%s can't hand its conversation to itself", from.Name)
			}
			a.Agent = to.Agent
		} else if len(m.Agents) > 0 && !contains(m.Agents, a.Agent) {
			return fmt.Errorf("%s is not installed on %s", a.Agent, m.Label)
		}
	case ActBroadcast:
		if strings.TrimSpace(a.Text) == "" {
			return fmt.Errorf("nothing to send")
		}
		if len(a.Panes) == 0 {
			return fmt.Errorf("no agents to send to")
		}
		seen := map[string]bool{}
		for _, id := range a.Panes {
			pane := m.pane(id)
			switch {
			case pane == nil:
				return fmt.Errorf("unknown pane %q on %s", id, m.Label)
			case pane.Agent == "":
				return fmt.Errorf("%s is a terminal, not an agent", pane.Name)
			case seen[id]:
				return fmt.Errorf("%s is listed twice", pane.Name)
			}
			seen[id] = true
		}
	default:
		return fmt.Errorf("unknown action %q", a.Type)
	}
	return nil
}

// Describe renders an action for confirmation.
func (w World) Describe(a Action) string {
	m := w.machine(a.Machine)
	where := a.Machine
	if m != nil {
		where = m.Label
		if p := m.project(a.Project); p != nil {
			where = p.Name + " on " + m.Label
		}
		if p := m.pane(a.Pane); p != nil {
			where = p.Name + " on " + m.Label
		}
	}
	switch a.Type {
	case ActStartTask:
		branch := a.Branch
		if branch == "" {
			branch = "new branch"
		}
		return fmt.Sprintf("Start %s in %s (%s): %s", a.Agent, where, branch, a.Prompt)
	case ActStartAgent:
		s := fmt.Sprintf("Start %s in %s", a.Agent, where)
		if a.Branch != "" {
			s += " on " + a.Branch
		}
		if a.Prompt != "" {
			s += ": " + a.Prompt
		}
		return s
	case ActSend:
		return fmt.Sprintf("Send to %s: %s", where, a.Text)
	case ActFocus:
		return "Show " + where
	case ActClose:
		return "Close " + where
	case ActShare:
		to := a.Agent + " (a new pane)"
		if m != nil && a.ToPane != "" {
			to = a.Agent
			if p := m.pane(a.ToPane); p != nil {
				to = p.Name
			}
		}
		return fmt.Sprintf("Hand the conversation of %s to %s", where, to)
	case ActBroadcast:
		var names []string
		for _, id := range a.Panes {
			name := id
			if m != nil {
				if p := m.pane(id); p != nil {
					name = p.Name
				}
			}
			names = append(names, name)
		}
		return fmt.Sprintf("Send to %d agents on %s (%s): %s", len(a.Panes), machineLabel(m, a.Machine), strings.Join(names, ", "), a.Text)
	}
	return a.Type
}

func machineLabel(m *Machine, id string) string {
	if m != nil {
		return m.Label
	}
	return id
}

func (w World) machine(id string) *Machine {
	for i := range w.Machines {
		if w.Machines[i].ID == id {
			return &w.Machines[i]
		}
	}
	return nil
}

func (m *Machine) project(id string) *Project {
	for i := range m.Projects {
		if m.Projects[i].ID == id {
			return &m.Projects[i]
		}
	}
	return nil
}

func (m *Machine) pane(id string) *Pane {
	for i := range m.Panes {
		if m.Panes[i].ID == id {
			return &m.Panes[i]
		}
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// MachineFrom describes a machine from its server's state. summaries maps
// pane ids to what the brain last said about them.
func MachineFrom(id, label string, online bool, agents []proto.AgentAvailability, projects []proto.ProjectInfo, panes []proto.PaneInfo, summaries map[string]string) Machine {
	m := Machine{ID: id, Label: label, Online: online}
	for _, a := range agents {
		if a.Installed {
			m.Agents = append(m.Agents, a.Name)
		}
	}
	for _, p := range projects {
		proj := Project{ID: p.ID, Name: p.Name, Path: p.Path, Git: p.Git, Base: p.Base}
		checkedOut := map[string]bool{}
		for _, wt := range p.Worktrees {
			checkedOut[wt.Branch] = true
		}
		for i, b := range p.Branches {
			if i == 30 {
				proj.Branches = append(proj.Branches, fmt.Sprintf("… %d more", len(p.Branches)-30))
				break
			}
			name := b.Name
			if checkedOut[b.Name] {
				name += " (worktree)"
			}
			proj.Branches = append(proj.Branches, name)
		}
		m.Projects = append(m.Projects, proj)
	}
	for _, p := range panes {
		if p.State != proto.PaneRunning {
			continue
		}
		pane := Pane{ID: p.ID, Name: p.DisplayName(), Project: p.ProjectID, Branch: p.Branch, Cwd: p.Cwd, Summary: summaries[p.ID]}
		if p.Agent != nil {
			pane.Agent, pane.State = p.Agent.Name, p.Agent.State
		}
		m.Panes = append(m.Panes, pane)
	}
	return m
}

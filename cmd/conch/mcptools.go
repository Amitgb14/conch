package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

// The tools `conch mcp` offers. They are the same work the CLI does —
// start, prompt, wait, read, task, list, rename — so the two doors cannot
// drift: each one calls the same server method with the same payload, and
// the wait is the same wait (await.go).
//
// What a tool returns is a sentence for the model and the same facts under
// structuredContent for a client that would rather have fields. What a
// tool refuses returns isError with a sentence saying what to do instead:
// the agent that called it is the one who can act on it.

type mcpTool struct {
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description"`
	Schema      any    `json:"inputSchema"`
	run         func(c *client.Client, args json.RawMessage) (any, error)
}

// schema builds an object schema. props is a name → description or a
// name → map for anything beyond a string.
func schema(required []string, props map[string]any) any {
	out := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}

func strProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}
func flagProp(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}
func numProp(desc string) map[string]any {
	return map[string]any{"type": "number", "description": desc}
}
func listProp(desc string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
}

// paneArg is the argument every tool that names a pane takes.
const paneArg = "the pane: its id (p3) or the name it was given (reviewer)"

// whenItIsReady ends the answer from start and task. An agent is not
// detected the instant its process does: conch reads the pane for it, so a
// prompt sent in the same breath is refused for a pane "not running an
// agent". Saying so is cheaper than a round trip on every prompt, and it is
// what the commands say too — `conch wait` prints the same thing.
const whenItIsReady = "Wait for it (the wait tool) before prompting it: conch has to see the agent start"

// agentArg says which agents there are without pinning them in an enum: a
// server that knows a new one should take it without a new conch here.
const agentArg = "which agent: claude, codex, gemini, opencode or devin; leave it out for the person's default"

func mcpTools() []mcpTool {
	return []mcpTool{
		{
			Name:        "list",
			Title:       "List panes",
			Description: "The panes conch is running that you may see: agents with what each is doing, and terminals. Call this first to find a helper's pane id or name.",
			Schema:      schema(nil, map[string]any{}),
			run:         mcpList,
		},
		{
			Name:        "read",
			Title:       "Read a pane's screen",
			Description: "What is on a pane's screen now — an agent's last answer, or the question it is waiting on. This is the screen, not a transcript: what scrolled away is not in it. Leave tail out for an agent: its interface keeps a prompt box and a status line at the bottom, so the last few lines are that furniture and not what it said. tail is for a program that prints and scrolls, like a build or a test run.",
			Schema: schema([]string{"pane"}, map[string]any{
				"pane": strProp(paneArg),
				"tail": numProp("only the last N lines, for a program that prints and scrolls; leave it out for an agent, whose last lines are its prompt box"),
			}),
			run: mcpRead,
		},
		{
			Name:        "start",
			Title:       "Start an agent",
			Description: "Start an agent in a pane of its own, in a folder that already exists. For work that wants its own branch and worktree, use task instead.",
			Schema: schema(nil, map[string]any{
				"agent":  strProp(agentArg),
				"cwd":    strProp("the folder it starts in; yours when left out"),
				"name":   strProp("a name to address it by, which no other running pane has"),
				"prompt": strProp("a first message, passed the way that agent expects"),
				"args":   strProp("extra arguments for the agent, as shell words (e.g. \"--model opus\")"),
			}),
			run: mcpStart,
		},
		{
			Name:        "prompt",
			Title:       "Prompt an agent",
			Description: "Send an agent its next message. Refused while it is waiting on a question of its own — read it first — and while conch has yet to see an agent start in the pane, so wait for one you have only just started. With wait, this does not return until the work the message starts has ended.",
			Schema: schema([]string{"pane", "text"}, map[string]any{
				"pane":            strProp(paneArg),
				"text":            strProp("the message"),
				"wait":            flagProp("wait until the work this starts has ended"),
				"until":           listProp("with wait, the states that end it: done, idle, waiting, working (default done, idle, waiting)"),
				"timeout_seconds": numProp("with wait, give up after this long (default 1800; 0 waits forever)"),
			}),
			run: mcpPrompt,
		},
		{
			Name:        "wait",
			Title:       "Wait for an agent",
			Description: "Block until the agent in a pane reaches one of the states asked for. Use it after starting a helper, rather than reading its screen in a loop.",
			Schema: schema([]string{"pane"}, map[string]any{
				"pane":            strProp(paneArg),
				"state":           listProp("the states that end it: done, idle, waiting, working (default done)"),
				"timeout_seconds": numProp("give up after this long (default 1800; 0 waits forever)"),
			}),
			run: mcpWait,
		},
		{
			Name:        "task",
			Title:       "Start a task on its own branch",
			Description: "Make a branch and a worktree of your project and start an agent there on a prompt, so its work is separate from yours. It sees only what is committed on the branch it starts from.",
			Schema: schema([]string{"prompt"}, map[string]any{
				"prompt": strProp("what the agent should do"),
				"cwd":    strProp("a folder of the project; yours when left out"),
				"branch": strProp("the branch to make; one is named after the prompt when left out"),
				"base":   strProp("the branch to start from; the project's base when left out"),
				"agent":  strProp(agentArg),
				"name":   strProp("a name to address the pane by"),
			}),
			run: mcpTask,
		},
		{
			Name:        "rename",
			Title:       "Name a pane",
			Description: "Give a pane a name you can address it by instead of its id. An empty name gives it its own back.",
			Schema: schema([]string{"pane"}, map[string]any{
				"pane": strProp(paneArg),
				"name": strProp("the new name; empty restores the pane's own"),
			}),
			run: mcpRename,
		},
	}
}

// mcpArgs decodes a tool's arguments, refusing a field it does not know:
// a model that invents one should be told, not have it ignored.
func mcpArgs(raw json.RawMessage, v any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("those arguments cannot be read: %v", err)
	}
	return nil
}

// mcpPane turns a pane id or name into an id.
func mcpPane(c *client.Client, ref string) (string, error) {
	if strings.TrimSpace(ref) == "" {
		return "", errors.New("say which pane: its id (p3) or its name. The list tool shows both")
	}
	id, err := resolvePane(c, ref)
	if err != nil {
		return "", err
	}
	return id, nil
}

// mcpWant turns the states a caller asked for into the set to stop on,
// with the words they were given back for messages.
func mcpWant(list []string, def string) (map[string]bool, string, error) {
	s := strings.Join(list, ",")
	if strings.TrimSpace(s) == "" {
		s = def
	}
	want, err := wantedStates(s)
	if err != nil {
		return nil, "", err
	}
	return want, s, nil
}

// mcpWait turns a timeout in seconds into a duration: absent is the
// default, 0 is no limit at all, and a negative one is a mistake worth
// saying rather than treating as none.
func mcpWaitFor(sec *float64) (time.Duration, error) {
	switch {
	case sec == nil:
		return mcpDefaultWait, nil
	case *sec < 0:
		return 0, fmt.Errorf("timeout_seconds cannot be negative (%v)", *sec)
	}
	return time.Duration(*sec * float64(time.Second)), nil
}

// paneFacts is what every tool says about a pane it acted on.
func paneFacts(p proto.PaneInfo) map[string]any {
	out := map[string]any{"pane": p.ID, "name": p.Name, "state": p.State, "cwd": p.Cwd}
	if p.Agent != nil {
		out["agent"] = p.Agent.Name
		out["agent_state"] = p.Agent.State
		// The agents it runs inside its own process (Claude's Agent tool),
		// which have no pane: the tree lists them under it, so a tool that
		// left them out would say less than the screen the person sees.
		var subs []map[string]any
		for _, a := range p.Agent.Subagents {
			sub := map[string]any{"id": a.ID}
			if a.Type != "" {
				sub["type"] = a.Type
			}
			if a.Description != "" {
				sub["task"] = a.Description
			}
			subs = append(subs, sub)
		}
		if len(subs) > 0 {
			out["subagents"] = subs
		}
	}
	if p.Branch != "" {
		out["branch"] = p.Branch
	}
	if p.ProjectID != "" {
		out["project"] = p.ProjectID
	}
	return out
}

// defaultAgent is the agent the person chose in config.toml, or "" for the
// server's own default — the same choice `conch task` makes with no -agent.
func defaultAgent() string {
	if cfg, err := config.Load(); err == nil {
		return cfg.Agents.Default
	}
	return ""
}

func mcpList(c *client.Client, raw json.RawMessage) (any, error) {
	var args struct{}
	if err := mcpArgs(raw, &args); err != nil {
		return nil, err
	}
	var list proto.PaneList
	if err := call(c, proto.MethodPaneList, nil, &list); err != nil {
		return nil, err
	}
	panes := make([]map[string]any, 0, len(list.Panes))
	var lines []string
	for _, p := range list.Panes {
		panes = append(panes, paneFacts(p))
		what := "terminal"
		if p.Agent != nil {
			what = p.Agent.Name + ":" + p.Agent.State
		}
		line := fmt.Sprintf("%s  %s  %s  %s", p.ID, nameOr(p.Name, "-"), what, p.Cwd)
		if n := subagentCount(p); n > 0 {
			line += fmt.Sprintf("  (+%d subagent%s inside it)", n, plural(n))
		}
		if p.State != proto.PaneRunning {
			line += "  (" + p.State + ")"
		}
		lines = append(lines, line)
	}
	text := "no panes"
	if len(lines) > 0 {
		text = "PANE  NAME  WHAT  FOLDER\n" + strings.Join(lines, "\n")
	}
	return toolResult(text, map[string]any{"panes": panes}), nil
}

// subagentCount is how many agents are running inside this pane's agent.
func subagentCount(p proto.PaneInfo) int {
	if p.Agent == nil {
		return 0
	}
	return len(p.Agent.Subagents)
}

func nameOr(s, alt string) string {
	if s == "" {
		return alt
	}
	return s
}

func mcpRead(c *client.Client, raw json.RawMessage) (any, error) {
	var args struct {
		Pane string   `json:"pane"`
		Tail *float64 `json:"tail"`
	}
	if err := mcpArgs(raw, &args); err != nil {
		return nil, err
	}
	id, err := mcpPane(c, args.Pane)
	if err != nil {
		return nil, err
	}
	var res proto.PaneReadResult
	if err := call(c, proto.MethodPaneRead, proto.PaneRef{ID: id}, &res); err != nil {
		return nil, err
	}
	// The blank rows under a program that does not fill the screen are
	// dropped: they are tokens a model pays for, and they would make tail
	// ask for the bottom of an empty screen rather than the last thing
	// said. Blank rows *between* lines stay — they are the layout.
	lines := res.Lines
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if args.Tail != nil {
		n := int(*args.Tail)
		if n < 0 {
			return nil, fmt.Errorf("tail cannot be negative (%d)", n)
		}
		if n < len(lines) {
			lines = lines[len(lines)-n:]
		}
	}
	text := strings.Join(lines, "\n")
	if text == "" {
		// Nothing on it at all: say so, rather than handing a model an
		// empty answer it has to guess at. A pane that has only just
		// started is the usual reason.
		text = "nothing on " + id + "'s screen yet"
	}
	return toolResult(text, map[string]any{"pane": id, "lines": lines}), nil
}

func mcpStart(c *client.Client, raw json.RawMessage) (any, error) {
	var args struct {
		Agent  string `json:"agent"`
		Cwd    string `json:"cwd"`
		Name   string `json:"name"`
		Prompt string `json:"prompt"`
		Args   string `json:"args"`
	}
	if err := mcpArgs(raw, &args); err != nil {
		return nil, err
	}
	agent := strings.TrimSpace(args.Agent)
	if agent == "" {
		agent = defaultAgent()
	}
	cwd := args.Cwd
	if strings.TrimSpace(cwd) == "" {
		cwd, _ = os.Getwd()
	}
	params := proto.PaneCreateParams{Agent: agent, AgentArgs: args.Args, Prompt: args.Prompt,
		Name: args.Name, Cwd: cwd, Cols: 120, Rows: 40}
	var info proto.PaneInfo
	if err := callFor(c, proto.MethodPaneCreate, params, &info, 30*time.Second); err != nil {
		return nil, err
	}
	text := fmt.Sprintf("%s: %s started in %s", info.ID, agent, info.Cwd)
	if info.Name != "" {
		text += fmt.Sprintf(", addressable as %q", info.Name)
	}
	return toolResult(text+". "+whenItIsReady, paneFacts(info)), nil
}

func mcpPrompt(c *client.Client, raw json.RawMessage) (any, error) {
	var args struct {
		Pane    string   `json:"pane"`
		Text    string   `json:"text"`
		Wait    bool     `json:"wait"`
		Until   []string `json:"until"`
		Timeout *float64 `json:"timeout_seconds"`
	}
	if err := mcpArgs(raw, &args); err != nil {
		return nil, err
	}
	if strings.TrimSpace(args.Text) == "" {
		return nil, errors.New("say what to send in text")
	}
	if miss := c.MissingCapabilities([]string{proto.CapAgentPrompt}); len(miss) > 0 {
		return nil, errors.New("the conch server there is from an older build without agent prompts; update it")
	}
	want, until, err := mcpWant(args.Until, "done,idle,waiting")
	if err != nil {
		return nil, err
	}
	timeout, err := mcpWaitFor(args.Timeout)
	if err != nil {
		return nil, err
	}
	id, err := mcpPane(c, args.Pane)
	if err != nil {
		return nil, err
	}
	// Sent on the connection whose events the wait below reads, so the
	// updates that answer it can only be queued, never missed.
	var res proto.AgentPromptResult
	if err := call(c, proto.MethodAgentPrompt, proto.AgentPromptParams{ID: id, Text: args.Text}, &res); err != nil {
		var perr *proto.Error
		if errors.As(err, &perr) && perr.Code == proto.ErrAgentBlocked {
			return nil, fmt.Errorf("%s; read its screen with the read tool and answer the question it is asking", perr.Message)
		}
		return nil, err
	}
	if !args.Wait {
		return toolResult(fmt.Sprintf("%s: sent to %s", res.ID, res.Agent),
			map[string]any{"pane": res.ID, "agent": res.Agent, "turn": res.Turn, "waited": false}), nil
	}
	info, state, err := awaitTurn(c, res, want, until, timeout)
	if err != nil {
		return nil, err
	}
	facts := paneFacts(info)
	facts["waited"] = true
	facts["turn"] = res.Turn
	return toolResult(fmt.Sprintf("%s: %s is %s. Read its screen for what it said", info.ID, res.Agent, state), facts), nil
}

func mcpWait(c *client.Client, raw json.RawMessage) (any, error) {
	var args struct {
		Pane    string   `json:"pane"`
		State   []string `json:"state"`
		Timeout *float64 `json:"timeout_seconds"`
	}
	if err := mcpArgs(raw, &args); err != nil {
		return nil, err
	}
	want, label, err := mcpWant(args.State, "done")
	if err != nil {
		return nil, err
	}
	timeout, err := mcpWaitFor(args.Timeout)
	if err != nil {
		return nil, err
	}
	id, err := mcpPane(c, args.Pane)
	if err != nil {
		return nil, err
	}
	info, state, err := awaitState(c, id, want, label, timeout, nil)
	if err != nil {
		return nil, err
	}
	return toolResult(fmt.Sprintf("%s: %s is %s", info.ID, nameOr(agentName(info), "the pane"), state), paneFacts(info)), nil
}

func agentName(p proto.PaneInfo) string {
	if p.Agent == nil {
		return ""
	}
	return p.Agent.Name
}

func mcpTask(c *client.Client, raw json.RawMessage) (any, error) {
	var args struct {
		Prompt string `json:"prompt"`
		Cwd    string `json:"cwd"`
		Branch string `json:"branch"`
		Base   string `json:"base"`
		Agent  string `json:"agent"`
		Name   string `json:"name"`
	}
	if err := mcpArgs(raw, &args); err != nil {
		return nil, err
	}
	if strings.TrimSpace(args.Prompt) == "" {
		return nil, errors.New("say what the agent should do in prompt")
	}
	dir := args.Cwd
	if strings.TrimSpace(dir) == "" {
		dir, _ = os.Getwd()
	}
	var proj proto.ProjectInfo
	if err := call(c, proto.MethodProjectAdd, proto.ProjectAddParams{Path: dir}, &proj); err != nil {
		return nil, err
	}
	agent := strings.TrimSpace(args.Agent)
	if agent == "" {
		agent = defaultAgent()
	}
	// No repository to branch: the agent works in the folder itself, which
	// is what `conch task` does there too.
	if !proj.Git {
		params := proto.PaneCreateParams{Agent: agent, Prompt: args.Prompt, Name: args.Name,
			Cwd: dir, Cols: 120, Rows: 40}
		var info proto.PaneInfo
		if err := callFor(c, proto.MethodPaneCreate, params, &info, 30*time.Second); err != nil {
			return nil, err
		}
		return toolResult(fmt.Sprintf("%s: %s started in %s — %s is not a git repository, so there is no branch. %s",
			info.ID, agent, info.Cwd, proj.Name, whenItIsReady), paneFacts(info)), nil
	}
	params := proto.TaskCreateParams{ProjectID: proj.ID, Prompt: args.Prompt, Branch: args.Branch,
		Base: args.Base, Agent: agent, Name: args.Name, Cols: 120, Rows: 40}
	var info proto.PaneInfo
	if err := callFor(c, proto.MethodTaskCreate, params, &info, harvestWait); err != nil {
		return nil, err
	}
	return toolResult(fmt.Sprintf("%s: %s started on branch %s in %s. %s", info.ID, agent, info.Branch, info.Cwd, whenItIsReady),
		paneFacts(info)), nil
}

func mcpRename(c *client.Client, raw json.RawMessage) (any, error) {
	var args struct {
		Pane string `json:"pane"`
		Name string `json:"name"`
	}
	if err := mcpArgs(raw, &args); err != nil {
		return nil, err
	}
	id, err := mcpPane(c, args.Pane)
	if err != nil {
		return nil, err
	}
	var info proto.PaneInfo
	if err := call(c, proto.MethodPaneRename, proto.PaneRenameParams{ID: id, Name: args.Name}, &info); err != nil {
		return nil, err
	}
	return toolResult(fmt.Sprintf("%s is now %q", info.ID, info.Name), paneFacts(info)), nil
}

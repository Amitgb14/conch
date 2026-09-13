package brain

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// AgentView is what the brain sees of one agent when summarising it.
type AgentView struct {
	Agent  string
	State  string
	Title  string
	Branch string
	Cwd    string
	Screen []string // visible terminal lines
}

// Summary says what an agent is doing and what, if anything, it needs.
type Summary struct {
	Doing string `json:"doing"` // at most ~70 characters
	Needs string `json:"needs"` // what the user must do; "" when nothing
}

var summarySchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"doing": map[string]any{"type": "string", "description": "What the agent is doing or has done, at most 70 characters, no trailing period."},
		"needs": map[string]any{"type": "string", "description": "What the user has to do next (answer a question, approve a command, review), at most 70 characters; empty when nothing."},
	},
	"required": []string{"doing", "needs"},
}

const summarySystem = `You summarise the terminal screen of an AI coding agent for a dashboard. Be concrete and terse: name files, commands and questions. Do not invent anything not visible on the screen. Ignore the agent's UI chrome (input box, footer hints, logos).`

// maxScreenLines bounds what is sent; the bottom of the screen matters most.
const maxScreenLines = 80

// Summarize asks the (small) model for a summary of an agent.
func Summarize(ctx context.Context, p Provider, v AgentView) (Summary, error) {
	lines := v.Screen
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > maxScreenLines {
		lines = lines[len(lines)-maxScreenLines:]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Agent: %s\nState: %s\n", v.Agent, v.State)
	if v.Title != "" {
		fmt.Fprintf(&b, "Terminal title: %s\n", v.Title)
	}
	if v.Branch != "" {
		fmt.Fprintf(&b, "Branch: %s\n", v.Branch)
	}
	fmt.Fprintf(&b, "Directory: %s\n\nScreen:\n%s\n", v.Cwd, strings.Join(lines, "\n"))
	res, err := p.Complete(ctx, Request{System: summarySystem, Prompt: b.String(), Schema: summarySchema, Small: true})
	if err != nil {
		return Summary{}, err
	}
	var s Summary
	if err := json.Unmarshal(res.JSON, &s); err != nil {
		return Summary{}, fmt.Errorf("unexpected summary from the model: %w", err)
	}
	s.Doing, s.Needs = clip(s.Doing, 90), clip(s.Needs, 90)
	return s, nil
}

func clip(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

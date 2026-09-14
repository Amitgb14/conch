package sessions

import (
	"fmt"
	"strings"
	"time"
)

// Agent labels for handoff documents.
var agentNames = map[string]string{
	"claude": "Claude Code", "codex": "Codex", "gemini": "Gemini CLI", "opencode": "OpenCode",
}

// AgentName is an agent's display name.
func AgentName(agent string) string {
	if n, ok := agentNames[agent]; ok {
		return n
	}
	return agent
}

const (
	maxHandoffTurn  = 8000      // characters kept from one message
	maxHandoffBytes = 400 << 10 // the whole document
)

// Handoff renders a conversation as a Markdown document another agent can
// read to continue the work. Very long messages are cut, and when the whole
// conversation is too long the oldest messages are left out (the first
// request is always kept).
func Handoff(s Session, turns []Turn) string {
	var head strings.Builder
	title := s.Title
	if title == "" {
		title = "untitled session"
	}
	fmt.Fprintf(&head, "# Handoff: %s\n\n", oneLine(title))
	fmt.Fprintf(&head, "Conversation from a %s session", AgentName(s.Agent))
	if s.ID != "" {
		fmt.Fprintf(&head, " (`%s`)", s.ID)
	}
	fmt.Fprintf(&head, " in `%s`", s.Dir)
	if s.Branch != "" {
		fmt.Fprintf(&head, " on branch `%s`", s.Branch)
	}
	if !s.Started.IsZero() {
		fmt.Fprintf(&head, ", %s", s.Started.Local().Format("2006-01-02 15:04"))
		if !s.Updated.IsZero() && s.Updated.After(s.Started) {
			fmt.Fprintf(&head, " – %s", s.Updated.Local().Format("2006-01-02 15:04"))
		}
	}
	head.WriteString(".\n\nOnly the messages are included: tool calls, command output and file edits are left out, so check the files themselves for the current state.\n")

	blocks := make([]string, len(turns))
	for i, t := range turns {
		blocks[i] = turnBlock(s.Agent, t)
	}
	if len(turns) == 0 {
		return head.String() + "\n_The session has no messages._\n"
	}
	// Keep the first request, then as many of the latest messages as fit.
	size := head.Len() + len(blocks[0])
	from := len(blocks)
	for from > 1 && size+len(blocks[from-1]) <= maxHandoffBytes {
		from--
		size += len(blocks[from])
	}
	var b strings.Builder
	b.WriteString(head.String())
	b.WriteString(blocks[0])
	if from > 1 {
		fmt.Fprintf(&b, "\n_… %d earlier messages left out …_\n", from-1)
	}
	for _, blk := range blocks[max(from, 1):] {
		b.WriteString(blk)
	}
	return b.String()
}

func turnBlock(agent string, t Turn) string {
	who := "User"
	if t.Role == "assistant" {
		who = AgentName(agent)
	}
	when := ""
	if !t.Time.IsZero() {
		when = " · " + t.Time.Local().Format(time.DateTime)
	}
	text := t.Text
	if r := []rune(text); len(r) > maxHandoffTurn {
		text = string(r[:maxHandoffTurn]) + "\n\n_… message cut …_"
	}
	return fmt.Sprintf("\n## %s%s\n\n%s\n", who, when, text)
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

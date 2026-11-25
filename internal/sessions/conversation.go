package sessions

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Conversation is a saved session as no agent in particular: what was
// said, the commands run with their output, the files read and edited,
// the other tools used, and the latest plan. Another agent can be handed
// it whatever wrote it. Thinking and reasoning are never kept: they are
// signed or encrypted, and nobody else could use them.
//
// It is built on demand from the agent's own files and kept nowhere.
type Conversation struct {
	Session Session
	Items   []Item // oldest first
	Plan    *Plan  // the latest plan or to-do list, if the agent kept one
	// MessagesOnly says the agent's tool calls could not be read: Items
	// holds only what was said.
	MessagesOnly bool
}

// Kind is what an Item is.
type Kind string

const (
	KindMessage Kind = "message" // something the user typed or the agent said
	KindCommand Kind = "command" // a shell command
	KindRead    Kind = "read"    // a file read
	KindEdit    Kind = "edit"    // a file edited, written or deleted
	KindTool    Kind = "tool"    // any other tool
	KindNote    Kind = "note"    // an image attached, an interruption, an error from the agent itself
)

// Item is one step of a Conversation.
type Item struct {
	Kind Kind
	// Role is who it came from: "user" or "assistant". Every tool call is
	// the assistant's; an attached image is the user's.
	Role string
	// Text is the message, the command line, the file's path, the tool's
	// name, or the note.
	Text string
	// Input is what an other tool was given, on one line and short.
	Input string
	// Output is what a command printed, its start and end kept.
	Output string
	// Size is how many bytes the call returned.
	Size int
	// Exit is a command's exit code, when Failed and it is known.
	Exit int
	// Failed says the call (or, for a note, the agent) reported an error.
	Failed bool
	// Done says the call's result has come.
	Done bool
	// Added and Removed count an edit's lines; Created and Deleted say it
	// made or removed the whole file.
	Added, Removed   int
	Created, Deleted bool
	Time             time.Time
}

// Plan is an agent's plan: steps with a status, or a plan as text.
type Plan struct {
	Text  string // an explanation, or the whole plan when it has no steps
	Steps []PlanStep
	Time  time.Time
}

// PlanStep is one step of a plan.
type PlanStep struct {
	Text   string
	Status string // "pending", "in_progress" or "completed"
}

// Open says some of the plan is still to do.
func (p *Plan) Open() bool {
	if p == nil {
		return false
	}
	if len(p.Steps) == 0 {
		return strings.TrimSpace(p.Text) != ""
	}
	for _, s := range p.Steps {
		if s.Status != "completed" {
			return true
		}
	}
	return false
}

// maxItemInput bounds an other tool's input as kept; maxItemOutput a
// command's output.
const (
	maxItemInput  = 200
	maxItemOutput = maxToolOutput
)

// ReadConversation reads a saved session as a Conversation. Claude Code's
// and Codex's are read whole; every other agent's are its messages alone,
// from Transcript, and say so in MessagesOnly.
func ReadConversation(ctx context.Context, e Env, s Session) (Conversation, error) {
	var c Conversation
	var err error
	switch s.Agent {
	case "claude":
		c, err = claudeConversation(s.Path)
	case "codex":
		c, err = codexConversation(s.Path)
	default:
		turns, terr := Transcript(ctx, e, s)
		if terr != nil {
			return Conversation{Session: s}, terr
		}
		return ConversationOf(s, turns), nil
	}
	if err != nil {
		return Conversation{Session: s}, err
	}
	c.Session = s
	c.Items = capItems(c.Items)
	return c, nil
}

// ConversationOf is a conversation of messages alone, for an agent whose
// tool calls conch does not read.
func ConversationOf(s Session, turns []Turn) Conversation {
	c := Conversation{Session: s, MessagesOnly: true}
	for _, t := range turns {
		c.Items = append(c.Items, Item{Kind: KindMessage, Role: t.Role, Text: t.Text, Time: t.Time})
	}
	return c
}

// Turns is what was said, as Transcript has it: an answer written in
// several blocks between tool calls is one turn.
func (c Conversation) Turns() []Turn {
	var turns []Turn
	for _, it := range c.Items {
		if it.Kind == KindMessage {
			turns = addTurn(turns, it.Role, it.Text, it.Time)
		}
	}
	return turns
}

// HasMessages says something was said, by either side.
func (c Conversation) HasMessages() bool {
	for _, it := range c.Items {
		if it.Kind == KindMessage {
			return true
		}
	}
	return false
}

// capItems keeps the most recent items within maxTranscriptBytes, as
// capTurns does for messages.
func capItems(items []Item) []Item {
	total := 0
	for i := len(items) - 1; i >= 0; i-- {
		total += len(items[i].Text) + len(items[i].Output) + len(items[i].Input)
		if total > maxTranscriptBytes {
			return items[i+1:]
		}
	}
	return items
}

// conv collects items as a reader meets them, matching each result to its
// call.
type conv struct {
	items []Item
	calls map[string][]int // a call's id → its items, until its result comes
	plan  *Plan
}

func newConv() *conv { return &conv{calls: map[string][]int{}} }

// message adds something said; injected context is left out, as Transcript
// leaves it.
func (c *conv) message(role, text string, t time.Time) {
	text = strings.TrimSpace(text)
	if text == "" || role == "user" && !realPrompt(text) {
		return
	}
	c.items = append(c.items, Item{Kind: KindMessage, Role: role, Text: text, Time: t})
}

func (c *conv) note(role, text string, failed bool, t time.Time) {
	c.items = append(c.items, Item{Kind: KindNote, Role: role, Text: text, Failed: failed, Time: t})
}

// call adds a tool call, remembered by id until its result comes.
func (c *conv) call(id string, it Item) {
	it.Role = "assistant"
	if it.Kind == KindTool {
		it.Input = clip(oneLine(it.Input), maxItemInput)
	}
	c.items = append(c.items, it)
	if id != "" {
		c.calls[id] = append(c.calls[id], len(c.items)-1)
	}
}

// result fills in a call's result. A result whose call was never seen —
// one read from the middle, a call conch skipped — is dropped.
func (c *conv) result(id, out string, failed bool, exit int) {
	at, ok := c.calls[id]
	if !ok {
		return
	}
	delete(c.calls, id)
	for _, i := range at {
		it := &c.items[i]
		it.Done, it.Failed, it.Size = true, failed, len(out)
		if it.Kind == KindCommand {
			it.Output = clipMiddle(strings.TrimRight(out, "\n "), maxItemOutput)
			if failed {
				it.Exit = exit
			}
		}
	}
}

// setPlan keeps the newest plan.
func (c *conv) setPlan(p Plan) {
	if p.Text == "" && len(p.Steps) == 0 {
		return
	}
	c.plan = &p
}

func (c *conv) done() Conversation { return Conversation{Items: c.items, Plan: c.plan} }

// lineStat counts what an edit of old into new takes out and puts in, by
// lines, leaving out the lines both start and end with.
func lineStat(old, new string) (added, removed int) {
	a, b := splitLines(old), splitLines(new)
	for len(a) > 0 && len(b) > 0 && a[0] == b[0] {
		a, b = a[1:], b[1:]
	}
	for len(a) > 0 && len(b) > 0 && a[len(a)-1] == b[len(b)-1] {
		a, b = a[:len(a)-1], b[:len(b)-1]
	}
	return len(b), len(a)
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// exitCode reads "Exit code N" at the start of a failed command's output,
// as Claude Code reports it.
func exitCode(out string) int {
	rest, ok := strings.CutPrefix(out, "Exit code ")
	if !ok {
		return 0
	}
	n, _ := strconv.Atoi(strings.Fields(rest + " x")[0])
	return n
}

// sizeText is a byte count as a person reads it.
func sizeText(n int) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%d B", n)
	case n < 1000*1000:
		return fmt.Sprintf("%.1f KB", float64(n)/1000)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/1000/1000)
	}
}

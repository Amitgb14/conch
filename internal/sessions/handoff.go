package sessions

import (
	"fmt"
	"strings"
	"time"
)

// Agent labels for handoff documents.
var agentNames = map[string]string{
	"claude": "Claude Code", "codex": "Codex", "gemini": "Gemini CLI", "opencode": "OpenCode", "devin": "Devin",
	"copilot": "Copilot CLI", "cline": "Cline", "qwen": "Qwen Code", "goose": "Goose",
}

// AgentName is an agent's display name.
func AgentName(agent string) string {
	if n, ok := agentNames[agent]; ok {
		return n
	}
	return agent
}

const (
	maxHandoffTurn  = 8000      // characters kept from one message, and from one turn's steps
	maxHandoffBytes = 400 << 10 // the whole document
	// maxHandoffOutput is how much of a command's output is shown: its
	// end, which is where a failure says what it was.
	maxHandoffOutput = 1200
	// maxShortTurn is what a turn in the middle of a conversation too long
	// for the document keeps of each message.
	maxShortTurn = 600
	// maxHandoffFiles bounds the list of files changed.
	maxHandoffFiles = 200
)

// Handoff renders a conversation as a Markdown document another agent can
// read to continue the work: where it stands (the open plan, the files
// changed, the last error), then every turn — the messages word for word,
// the commands run with their output cut, and the files read and edited
// and other tools as notes. A long message is cut; when the whole
// conversation is too long, the first request and the latest turns are
// kept whole and the turns between them shortened, the oldest of them
// left out if even that is too much.
func Handoff(c Conversation) string {
	s := c.Session
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
	if c.MessagesOnly {
		head.WriteString(".\n\nOnly the messages are included: tool calls, command output and file edits are left out, so check the files themselves for the current state.\n")
	} else {
		head.WriteString(".\n\nThe messages are word for word. Commands are shown with their output cut, files read and edited as notes, and the agent's own reasoning is left out, so check the files themselves for the current state.\n")
	}
	head.WriteString(standing(c))

	turns := groupTurns(c.Items)
	if len(turns) == 0 {
		return head.String() + "\n_The session has no messages._\n"
	}
	full := make([]string, len(turns))
	for i, t := range turns {
		full[i] = t.block(s.Agent, false)
	}
	// The first request and the latest turns whole, up to three quarters
	// of the room; then the turns before them shortened, newest first,
	// while they fit.
	const gapRoom = 100 // for the line saying turns were left out
	size := head.Len() + len(full[0]) + gapRoom
	from := len(turns)
	for from > 1 && size+len(full[from-1]) <= maxHandoffBytes*3/4 {
		from--
		size += len(full[from])
	}
	short := map[int]string{}
	kept := from
	for kept > 1 {
		b := turns[kept-1].block(s.Agent, true)
		if size+len(b) > maxHandoffBytes {
			break
		}
		kept--
		short[kept] = b
		size += len(b)
	}
	var b strings.Builder
	b.WriteString(head.String())
	b.WriteString(full[0])
	if kept > 1 {
		fmt.Fprintf(&b, "\n_… %d earlier turns left out …_\n", kept-1)
	}
	if len(short) > 0 {
		fmt.Fprintf(&b, "\n_The next %d turns are shortened: their messages cut and their steps counted._\n", len(short))
	}
	for i := max(kept, 1); i < len(turns); i++ {
		if blk, ok := short[i]; ok {
			b.WriteString(blk)
		} else {
			b.WriteString(full[i])
		}
	}
	return b.String()
}

// standing is the state of the work at the end: the plan still open, the
// files changed, and the last error. Empty when there is none of these.
func standing(c Conversation) string {
	var b strings.Builder
	if c.Plan.Open() {
		b.WriteString("\n### Open plan\n\n")
		if len(c.Plan.Steps) == 0 {
			b.WriteString(cutRunes(c.Plan.Text, maxHandoffTurn, "_… plan cut …_") + "\n")
		} else {
			if c.Plan.Text != "" {
				b.WriteString(cutRunes(oneLine(c.Plan.Text), maxShortTurn, "…") + "\n\n")
			}
			for _, st := range c.Plan.Steps {
				mark := "[ ]"
				switch st.Status {
				case "completed":
					mark = "[x]"
				case "in_progress":
					mark = "[ ] (in progress)"
				}
				fmt.Fprintf(&b, "- %s %s\n", mark, st.Text)
			}
		}
	}
	if files := changedFiles(c.Items); len(files) > 0 {
		b.WriteString("\n### Files changed\n\n")
		for i, f := range files {
			if i == maxHandoffFiles {
				fmt.Fprintf(&b, "- _… and %d more_\n", len(files)-i)
				break
			}
			fmt.Fprintf(&b, "- `%s` %s\n", f.Text, editStat(f))
		}
	}
	if last := lastError(c.Items); last != nil {
		b.WriteString("\n### Last error\n\n")
		b.WriteString(stepLine(*last, true))
	}
	if b.Len() == 0 {
		return ""
	}
	return "\n## Where it stands\n" + b.String()
}

// changedFiles is every file edited, in the order first edited, with what
// was done to it added up. A failed edit changed nothing.
func changedFiles(items []Item) []Item {
	var files []Item
	at := map[string]int{}
	for _, it := range items {
		if it.Kind != KindEdit || it.Failed || it.Text == "" {
			continue
		}
		i, ok := at[it.Text]
		if !ok {
			at[it.Text] = len(files)
			files = append(files, Item{Kind: KindEdit, Text: it.Text, Created: it.Created})
			i = len(files) - 1
		}
		f := &files[i]
		f.Added += it.Added
		f.Removed += it.Removed
		f.Deleted = it.Deleted
		if it.Created && f.Deleted {
			f.Deleted = false
		}
	}
	return files
}

// lastError is the newest step that failed and was not dealt with: a
// command is dealt with when the same command passed after it, and an
// error of the agent's own (a plan limit, a lost connection) when the
// agent answered after it.
func lastError(items []Item) *Item {
	passed := map[string]bool{}
	answered := false
	for i := len(items) - 1; i >= 0; i-- {
		it := items[i]
		switch {
		case it.Kind == KindMessage && it.Role == "assistant":
			answered = true
		case it.Kind == KindCommand && it.Done && !it.Failed:
			passed[it.Text] = true
		case it.Kind == KindCommand && it.Failed && !passed[it.Text]:
			return &items[i]
		case it.Kind == KindNote && it.Failed && !answered:
			return &items[i]
		}
	}
	return nil
}

func editStat(e Item) string {
	switch {
	case e.Deleted:
		return "(deleted)"
	case e.Created:
		return fmt.Sprintf("(new, +%d)", e.Added)
	}
	return fmt.Sprintf("(+%d −%d)", e.Added, e.Removed)
}

// turn is one side's part of the conversation: a user's message, or the
// agent's answer with the steps it took, in the order they came.
type turn struct {
	role  string
	time  time.Time
	items []Item
}

// groupTurns makes turns of a conversation's items: a user's message
// starts a turn, and whatever the agent says or does after it is the
// agent's turn.
func groupTurns(items []Item) []turn {
	var turns []turn
	for _, it := range items {
		role := it.Role
		if role != "user" {
			role = "assistant"
		}
		n := len(turns)
		if n == 0 || turns[n-1].role != role || role == "user" && it.Kind == KindMessage && turns[n-1].said() {
			turns = append(turns, turn{role: role, time: it.Time})
			n++
		}
		turns[n-1].items = append(turns[n-1].items, it)
	}
	return turns
}

// said says the turn has a message in it.
func (t turn) said() bool {
	for _, it := range t.items {
		if it.Kind == KindMessage {
			return true
		}
	}
	return false
}

// block is the turn as Markdown: its messages as paragraphs and its steps
// as lists between them. In full, the messages together keep
// maxHandoffTurn characters and the steps maxHandoffTurn bytes, the rest
// counted; shortened, the messages keep maxShortTurn and the steps are
// only counted.
func (t turn) block(agent string, short bool) string {
	who := "User"
	if t.role == "assistant" {
		who = AgentName(agent)
	}
	when := ""
	if !t.time.IsZero() {
		when = " · " + t.time.Local().Format(time.DateTime)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n## %s%s\n", who, when)
	textRoom, textNote := maxHandoffTurn, "_… message cut …_"
	if short {
		textRoom, textNote = maxShortTurn, "_… cut …_"
	}
	stepRoom := maxHandoffTurn
	inList := false
	for i := 0; i < len(t.items); i++ {
		it := t.items[i]
		if it.Kind == KindMessage {
			if textRoom <= 0 {
				continue
			}
			text := cutRunes(it.Text, textRoom, textNote)
			textRoom -= len([]rune(it.Text))
			b.WriteString("\n" + text + "\n")
			inList = false
			continue
		}
		// A run of steps.
		j := i
		for j < len(t.items) && t.items[j].Kind != KindMessage {
			j++
		}
		run := t.items[i:j]
		i = j - 1
		if !inList {
			b.WriteString("\n")
			inList = true
		}
		if short {
			fmt.Fprintf(&b, "- _%s_\n", countSteps(run))
			continue
		}
		for k, st := range run {
			line := stepLine(st, false)
			if len(line) > stepRoom {
				if stepRoom > 0 {
					rest := []Item{}
					for _, r := range t.items[i-len(run)+1+k:] {
						if r.Kind != KindMessage {
							rest = append(rest, r)
						}
					}
					fmt.Fprintf(&b, "- _… and %s_\n", countSteps(rest))
				}
				stepRoom = 0
				break
			}
			stepRoom -= len(line)
			b.WriteString(line)
		}
	}
	return b.String()
}

// stepLine is one step as a list item; a command's output follows it,
// cut to its end. With last set it is the Last error section's, which
// says more of the output.
func stepLine(it Item, last bool) string {
	var b strings.Builder
	switch it.Kind {
	case KindCommand:
		fmt.Fprintf(&b, "- Ran %s", inlineCode(it.Text))
		switch {
		case it.Failed && it.Exit != 0:
			fmt.Fprintf(&b, " — failed, exit %d", it.Exit)
		case it.Failed:
			b.WriteString(" — failed")
		case !it.Done:
			b.WriteString(" — no result")
		}
		b.WriteString("\n")
		if out := strings.TrimSpace(it.Output); out != "" {
			n := maxHandoffOutput
			if last {
				n *= 2
			}
			b.WriteString(fence(tail(out, n), "  "))
		}
		return b.String()
	case KindRead:
		fmt.Fprintf(&b, "- Read `%s`", it.Text)
	case KindEdit:
		verb := "Edited"
		switch {
		case it.Deleted:
			verb = "Deleted"
		case it.Created:
			verb = "Created"
		}
		fmt.Fprintf(&b, "- %s `%s`", verb, it.Text)
		if !it.Deleted {
			fmt.Fprintf(&b, " (+%d −%d)", it.Added, it.Removed)
		}
	case KindTool:
		fmt.Fprintf(&b, "- Used %s", inlineCode(it.Text))
		if it.Input != "" {
			b.WriteString(": " + inlineCode(it.Input))
		}
		if it.Done && it.Size > 0 {
			fmt.Fprintf(&b, " (%s back)", sizeText(it.Size))
		}
	case KindNote:
		b.WriteString("- _" + strings.ReplaceAll(it.Text, "_", `\_`) + "_")
	}
	switch {
	case it.Kind == KindNote:
	case it.Failed:
		b.WriteString(" — failed")
	case !it.Done:
		b.WriteString(" — no result")
	}
	b.WriteString("\n")
	return b.String()
}

// countSteps says what steps were taken, in counts.
func countSteps(steps []Item) string {
	var cmd, read, edit, tool, note int
	for _, s := range steps {
		switch s.Kind {
		case KindCommand:
			cmd++
		case KindRead:
			read++
		case KindEdit:
			edit++
		case KindTool:
			tool++
		default:
			note++
		}
	}
	var parts []string
	add := func(n int, one, many string) {
		switch {
		case n == 1:
			parts = append(parts, one)
		case n > 1:
			parts = append(parts, fmt.Sprintf(many, n))
		}
	}
	add(cmd, "1 command run", "%d commands run")
	add(read, "1 file read", "%d files read")
	add(edit, "1 edit", "%d edits")
	add(tool, "1 other tool used", "%d other tools used")
	add(note, "1 note", "%d notes")
	return strings.Join(parts, ", ")
}

// inlineCode is s as inline code on one line, with a fence long enough
// for any backticks in it.
func inlineCode(s string) string {
	s = oneLine(s)
	if r := []rune(s); len(r) > maxShortTurn {
		s = string(r[:maxShortTurn]) + "…"
	}
	ticks := "`"
	for strings.Contains(s, ticks) {
		ticks += "`"
	}
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") {
		s = " " + s + " "
	}
	return ticks + s + ticks
}

// fence is text as a code block, indented to sit under a list item, with a
// fence longer than any run of backticks inside.
func fence(text, indent string) string {
	ticks := "```"
	for strings.Contains(text, ticks) {
		ticks += "`"
	}
	var b strings.Builder
	b.WriteString(indent + ticks + "\n")
	for _, l := range strings.Split(text, "\n") {
		b.WriteString(indent + l + "\n")
	}
	b.WriteString(indent + ticks + "\n")
	return b.String()
}

// tail keeps the end of s within about n bytes, on a line boundary where
// one is near, saying how many lines went.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := len(s) - n
	if i := strings.IndexByte(s[cut:], '\n'); i >= 0 && i < n/4 {
		cut += i + 1
	}
	for cut < len(s) && !utf8Start(s[cut]) {
		cut++
	}
	return fmt.Sprintf("… %d lines cut …\n", strings.Count(s[:cut], "\n")+1) + s[cut:]
}

// cutRunes keeps n characters of s, saying so with note when it cut.
func cutRunes(s string, n int, note string) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "\n\n" + note
	}
	return s
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

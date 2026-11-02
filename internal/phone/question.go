package phone

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Amitgb14/conch/internal/proto"
)

// choiceLine is one row of a numbered menu: "❯ 1. Yes", "  2. No", with or
// without the box an agent draws round it. The first group is the cursor.
// Gemini CLI marks its cursor with ● where Claude Code has ❯ and Codex ›.
var choiceLine = regexp.MustCompile(`^[\s│┃|]*([❯›>●])?\s*(\d{1,2})\.\s+(\S.*?)[\s│┃|]*$`)

// cursorLine and plainLine are the rows of a menu without numbers, as
// Claude Code draws its folder-trust question: the cursor's row
// "❯ No, exit" and the others indented to where its label starts.
var (
	cursorLine = regexp.MustCompile(`^([\s│┃|]*[❯›●]\s+)(\S.*?)[\s│┃|]*$`)
	plainLine  = regexp.MustCompile(`^([\s│┃|]*\s)(\S.*?)[\s│┃|]*$`)
	// numberedLabel is a label that is a numbered menu's row.
	numberedLabel = regexp.MustCompile(`^\d{1,2}\.\s`)
)

// menu is the choices read off a screen: their labels in order (number 1
// first), the row of the first, and which one the cursor is on.
type menu struct {
	labels []string
	top    int
	cursor int // index into labels
}

// readMenu finds the numbered menu nearest the bottom of the screen: rows
// numbered from 1 without a gap, one of them under the cursor. Anything
// else — a numbered list in an agent's answer, a menu with no cursor to
// steer by — is not a menu the gateway will press keys into.
func readMenu(screen []string) (menu, bool) {
	var best menu
	found := false
	var cur menu
	cursors := 0
	flush := func() {
		if len(cur.labels) >= 2 && cursors == 1 {
			best, found = cur, true
		}
		cur, cursors = menu{}, 0
	}
	numberAt := -1 // the column the current menu's numbers start in
	for i, line := range screen {
		m := choiceLine.FindStringSubmatch(line)
		if m == nil {
			// A line indented past the numbers, under a choice, is that
			// choice's description (Claude Code's question tool writes
			// one under each): the menu carries on below it.
			if len(cur.labels) > 0 && numberAt >= 0 && descriptionLine(line, numberAt) {
				continue
			}
			flush()
			numberAt = -1
			continue
		}
		// In characters, not bytes: the cursor ❯ is one column and three bytes.
		numberAt = utf8.RuneCountInString(line[:strings.IndexFunc(line, func(r rune) bool { return r >= '0' && r <= '9' })])
		n, _ := strconv.Atoi(m[2])
		if n != len(cur.labels)+1 {
			flush()
			if n != 1 {
				continue
			}
		}
		if n == 1 {
			cur.top = i
		}
		if m[1] != "" {
			cur.cursor = n - 1
			cursors++
		}
		cur.labels = append(cur.labels, m[3])
	}
	flush()
	if found {
		return best, true
	}
	return readPlainMenu(screen)
}

// descriptionLine is whether line is written under a choice whose number
// starts in column numberAt: text, beginning further right than that.
func descriptionLine(line string, numberAt int) bool {
	text := strings.TrimLeft(line, " \t│┃|")
	if strings.TrimSpace(text) == "" {
		return false
	}
	return utf8.RuneCountInString(line)-utf8.RuneCountInString(text) > numberAt
}

// readPlainMenu finds a menu without numbers nearest the bottom of the
// screen: two or more rows together, exactly one under the cursor, every
// label starting in the same column — what Claude Code asks whether to
// trust a folder with. Being strict about the column is what keeps an
// agent's answer, or a shell prompt, from reading as a menu.
func readPlainMenu(screen []string) (menu, bool) {
	var best menu
	found := false
	for i := 0; i < len(screen); i++ {
		c := cursorLine.FindStringSubmatch(screen[i])
		if c == nil {
			continue
		}
		col := len([]rune(c[1]))
		label := func(line string) (string, bool) {
			m := plainLine.FindStringSubmatch(line)
			if m == nil || len([]rune(m[1])) != col || cursorLine.MatchString(line) {
				return "", false
			}
			return m[2], true
		}
		top := i
		for top > 0 {
			if _, ok := label(screen[top-1]); !ok {
				break
			}
			top--
		}
		var labels []string
		for j := top; j < i; j++ {
			l, _ := label(screen[j])
			labels = append(labels, l)
		}
		cursor := len(labels)
		labels = append(labels, c[2])
		for j := i + 1; j < len(screen); j++ {
			l, ok := label(screen[j])
			if !ok {
				break
			}
			labels = append(labels, l)
		}
		numbered := false
		for _, l := range labels {
			numbered = numbered || numberedLabel.MatchString(l)
		}
		// Rows with numbers are a numbered menu's, read above or refused
		// there (one not starting at 1, one with a gap): never this.
		if len(labels) >= 2 && !numbered {
			best, found = menu{labels: labels, top: top, cursor: cursor}, true
		}
	}
	return best, found
}

// questionTextMax bounds a question read off a screen.
const questionTextMax = 500

// boxChars are what agents draw a prompt's frame with.
const boxChars = "│┃|╭╮╰╯─━┌┐└┘"

// unbox is a screen row without its frame or the space round it.
func unbox(line string) string {
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(line), boxChars))
}

// screenText is the question as the screen shows it: the paragraph just
// above the menu, or with no menu the last one on the screen.
func screenText(screen []string, m menu, hasMenu bool) string {
	end := len(screen)
	if hasMenu {
		end = m.top
	}
	var rows []string
	for i := end - 1; i >= 0 && len(rows) < 8; i-- {
		raw := strings.TrimSpace(screen[i])
		text := unbox(raw)
		if text == "" {
			// An empty row, or the top of the prompt's box, ends the
			// question; an empty row inside the box does not.
			if len(rows) > 0 && (raw == "" || strings.ContainsAny(raw, "╭┌")) {
				break
			}
			continue
		}
		rows = append([]string{text}, rows...)
	}
	text := strings.Join(rows, " ")
	if r := []rune(text); len(r) > questionTextMax {
		text = string(r[:questionTextMax])
	}
	return text
}

// readQuestion is what a waiting agent asks, from its status and its
// screen (plain text, as pane.read gives it). Nil unless it is waiting.
func readQuestion(p proto.PaneInfo, screen []string) *Question {
	if p.Agent == nil || p.Agent.State != proto.AgentBlocked {
		return nil
	}
	m, ok := readMenu(screen)
	q := &Question{Text: strings.TrimSpace(p.Agent.Message), Choices: []Choice{}}
	if q.Text == "" {
		q.Text = screenText(screen, m, ok)
	}
	if ok {
		for i, label := range m.labels {
			q.Choices = append(q.Choices, Choice{Choice: strconv.Itoa(i + 1), Label: label, Default: i == m.cursor})
		}
	}
	q.ID = questionID(p.ID, p.Agent.Since, q)
	return q
}

// questionID changes whenever the question does: a hash of the pane, when
// the agent started waiting, the text and the choices. It is what makes an
// answer safe to send late — one for a question that has gone is refused.
func questionID(pane string, since time.Time, q *Question) string {
	h := sha256.New()
	h.Write([]byte(pane + "\x00" + since.UTC().Format(time.RFC3339Nano) + "\x00" + q.Text))
	for _, c := range q.Choices {
		h.Write([]byte("\x00" + c.Choice + "\x00" + c.Label))
		if c.Default {
			h.Write([]byte("\x00default"))
		}
	}
	return "q_" + hex.EncodeToString(h.Sum(nil))[:12]
}

// choiceKeys is what picks a choice on the screen it was read from: the
// cursor moved from where it stands to that row, then Enter. Moving the
// cursor, rather than typing the row's number, sends one confirming key and
// no more — an agent that takes a number as the answer would leave a
// following Enter for whatever it asks next.
func choiceKeys(screen []string, choice string) ([]string, bool) {
	m, ok := readMenu(screen)
	if !ok {
		return nil, false
	}
	n, err := strconv.Atoi(choice)
	if err != nil || n < 1 || n > len(m.labels) || strconv.Itoa(n) != choice {
		return nil, false
	}
	var keys []string
	for i := m.cursor; i < n-1; i++ {
		keys = append(keys, "down")
	}
	for i := m.cursor; i > n-1; i-- {
		keys = append(keys, "up")
	}
	return append(keys, "enter"), true
}

// ReadQuestion is readQuestion for conch's own commands: the Mac app
// answers a waiting agent from its card through `conch agent question`
// and `conch agent answer`, and reads a menu exactly as the phone does.
func ReadQuestion(p proto.PaneInfo, screen []string) *Question { return readQuestion(p, screen) }

// ChoiceKeys is choiceKeys for conch's own commands (see ReadQuestion).
func ChoiceKeys(screen []string, choice string) ([]string, bool) { return choiceKeys(screen, choice) }

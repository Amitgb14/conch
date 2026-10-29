package main

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Help for one command: `conch branch -h`, `conch task --help`,
// `conch help branch`. It is the first thing somebody types to find out
// how a command goes, and it did something different everywhere — a
// command's own flag list here, "unknown project subcommand -h" there, and
// `conch status -h` started a server. So -h is answered before any command
// sees it, from the lines about that command in the full usage: one text,
// which the completion test already holds to the commands that exist.
//
// Only as the first word after the command: further in, -h belongs to a
// subcommand's own flags (`conch branch pr -h`), and a pane named "help"
// is a pane.

// helpAliases are the other names a command answers to.
var helpAliases = map[string]string{
	"ls": "status", "worktrees": "worktree", "machines": "machine", "sandboxes": "sandbox", "--skill": "skill",
}

// helpDocs is where on the CLI page a command is written up, for the ones
// whose section is not named after them.
var helpDocs = map[string]string{
	"new": "panes", "status": "panes", "read": "panes", "send": "panes", "wait": "panes", "close": "panes",
	"rename": "panes", "redraw": "panes", "view": "panes", "show": "panes",
	"schedule": "task", "skill": "agent", "upload": "machine", "download": "machine",
}

// helpExamples are a few real uses of the commands people reach for
// first, for whoever learns faster from one than from a grammar.
var helpExamples = map[string][]string{
	"task": {
		`conch task "fix the flaky login test"`,
		`conch task -agent codex,claude "add a dark mode toggle"   # one try each, side by side`,
		`conch task -issue 42                                       # work on a GitHub issue`,
	},
	"new": {
		"conch new -agent claude                  # Claude Code in this folder",
		"conch new -name api -- npm run dev       # any program, in a pane named api",
	},
	"send":   {`conch send p3 "run the tests again"`, "conch send -keys p3 ctrl+c"},
	"read":   {"conch read p3"},
	"wait":   {"conch wait p3 && conch read p3", "conch wait -state waiting -timeout 10m p3"},
	"status": {"conch status", "conch -m busybox status"},
	"project": {
		"conch project add .",
		"conch project setup api npm install      # run in each new worktree",
	},
	"branch": {
		`conch branch commit -m "Fix the login race"`,
		"conch branch pr -draft",
		"conch branch merge",
	},
	"review":   {`conch review add src/auth.go:42 "this misses the expired case"`, "conch review send"},
	"worktree": {"conch worktree ls", "conch worktree clean      # says what it would remove; -y removes it"},
	"agent":    {"conch agent status", "conch agent install codex", `conch agent prompt -wait p3 "now add tests"`},
	"machine":  {"conch machine add me@devbox", "conch -m devbox task \"profile the importer\""},
	"web":      {"conch web", "conch web pair -permission reply"},
	"update":   {"conch update", "conch update rollback"},
}

// compWords are the words of a completion list.
func compWords(items []compItem) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.word)
	}
	return out
}

// helpCommand is the name a command is listed under, or "".
func helpCommand(word string) string {
	if a, ok := helpAliases[word]; ok {
		word = a
	}
	if slices.Contains(compWords(compCommands), word) {
		return word
	}
	return ""
}

// helpWords are the commands a usage line is about: conch CMD,
// conch -m MACHINE CMD, and conch CMD|CMD.
var helpWords = regexp.MustCompile(`^  conch (?:-m MACHINE )?([a-z][a-z|-]*)`)

// usageOf is what the full usage says about a command: each line naming
// it, with the lines indented under it.
func usageOf(cmd string) string {
	lines := strings.Split(usage, "\n")
	var b strings.Builder
	for i := 0; i < len(lines); i++ {
		m := helpWords.FindStringSubmatch(lines[i])
		if m == nil || !slices.Contains(strings.Split(m[1], "|"), cmd) {
			continue
		}
		b.WriteString(lines[i] + "\n")
		for i+1 < len(lines) && strings.HasPrefix(lines[i+1], "                ") {
			i++
			b.WriteString(lines[i] + "\n")
		}
	}
	return b.String()
}

// commandHelp is the help for one command.
func commandHelp(cmd string) string {
	if cmd == "help" {
		return shortUsage
	}
	var b strings.Builder
	for _, c := range compCommands {
		if c.word == cmd {
			fmt.Fprintf(&b, "conch %s — %s\n\n", cmd, c.desc)
		}
	}
	b.WriteString(usageOf(cmd))
	if ex := helpExamples[cmd]; len(ex) > 0 {
		b.WriteString("\nFor example:\n")
		for _, e := range ex {
			b.WriteString("  " + e + "\n")
		}
	}
	anchor := cmd
	if a, ok := helpDocs[cmd]; ok {
		anchor = a
	}
	fmt.Fprintf(&b, "\nconch help all lists every command · https://amitgb14.github.io/conch/docs/cli#%s\n", anchor)
	return b.String()
}

// isHelpFlag reports whether a word asks for help.
func isHelpFlag(w string) bool { return w == "-h" || w == "--help" || w == "-help" }

// unknownCommand is what is said about a word that is no command: the one
// it was probably meant to be, when one is close, rather than every
// command there is.
func unknownCommand(word string) string {
	if near := nearestCommand(word); near != "" {
		return fmt.Sprintf("conch: unknown command %q — did you mean conch %s?\n"+
			"conch help lists the commands to start with; conch help all, every one.\n", word, near)
	}
	return fmt.Sprintf("conch: unknown command %q\n\n%s", word, shortUsage)
}

// nearestCommand is the command a mistyped word most likely meant, or "".
func nearestCommand(word string) string {
	names := append(compWords(compCommands), "ls", "worktrees", "machines", "sandboxes")
	return helpCommand(nearestWord(word, names))
}

// nearestWord is the one of names a mistyped word most likely meant: one a
// couple of edits away, or one it is the start of. "" when none is close
// enough to be worth saying.
func nearestWord(word string, names []string) string {
	word = strings.ToLower(word)
	if len(word) < 2 {
		return ""
	}
	best, bestD := "", 3
	for _, name := range names {
		if strings.HasPrefix(name, word) && len(word) >= 3 {
			return name
		}
		// Short words are close to everything: "go" is two edits from
		// "bug", which is no suggestion at all.
		limit := 2
		if len(word) <= 4 {
			limit = 1
		}
		if d := editDistance(word, name); d <= limit && d < bestD {
			best, bestD = name, d
		}
	}
	return best
}

// editDistance counts the insertions, deletions, substitutions and swaps
// of neighbours that turn a into b — a swap being the commonest typo of all.
func editDistance(a, b string) int {
	x, y := []rune(a), []rune(b)
	d := make([][]int, len(x)+1)
	for i := range d {
		d[i] = make([]int, len(y)+1)
		d[i][0] = i
	}
	for j := range y {
		d[0][j+1] = j + 1
	}
	for i := 1; i <= len(x); i++ {
		for j := 1; j <= len(y); j++ {
			cost := 1
			if x[i-1] == y[j-1] {
				cost = 0
			}
			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && x[i-1] == y[j-2] && x[i-2] == y[j-1] {
				d[i][j] = min(d[i][j], d[i-2][j-2]+1)
			}
		}
	}
	return d[len(x)][len(y)]
}

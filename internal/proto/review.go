package proto

import (
	"fmt"
	"strings"
)

// ReviewQuoteLines bounds how much of a hunk a review comment keeps: enough
// to say where, not a whole file rewritten.
const ReviewQuoteLines = 40

// QuoteHunk is what a review comment keeps of a hunk: its header and first
// ReviewQuoteLines lines, and a line saying how many more there were. A
// client finds the comment on a hunk by quoting the hunk the same way.
//
// A hunk quoted already is left as it is, so the server and a client agree
// whichever of them quoted it.
func QuoteHunk(hunk string) string {
	lines := strings.Split(strings.TrimRight(hunk, "\n"), "\n")
	if len(lines)-1 <= ReviewQuoteLines || len(lines)-1 == ReviewQuoteLines+1 && quotedRest(lines[len(lines)-1]) {
		return strings.Join(lines, "\n")
	}
	more := len(lines) - 1 - ReviewQuoteLines
	unit := "lines"
	if more == 1 {
		unit = "line"
	}
	return strings.Join(lines[:1+ReviewQuoteLines], "\n") + fmt.Sprintf("\n… %d more %s", more, unit)
}

// quotedRest says whether a line is the one QuoteHunk ends a long hunk with.
// A diff line starts with a space, +, - or a backslash, never with "…".
func quotedRest(line string) bool {
	return strings.HasPrefix(line, "… ") && (strings.HasSuffix(line, " more lines") || strings.HasSuffix(line, " more line"))
}

// ReviewWhere is where a comment is, for people: the file and its line or
// hunk, or the pull request as a whole for a review's own words.
func ReviewWhere(c ReviewComment) string {
	switch {
	case c.File == "":
		return "the pull request"
	case c.Line > 0:
		return fmt.Sprintf("%s:%d", c.File, c.Line)
	case c.Hunk != "":
		return c.File + " " + strings.SplitN(c.Hunk, "\n", 2)[0]
	}
	return c.File
}

// ReviewAuthor is who wrote a comment, for people: "" for the person at
// the keyboard, an agent's pane, or a reviewer on GitHub.
func ReviewAuthor(c ReviewComment) string {
	switch {
	case c.Source == ReviewGitHub && c.By != "":
		return c.By + " on GitHub"
	case c.Source == ReviewGitHub:
		return "GitHub"
	}
	return c.By
}

// ReviewPrompt is the message a branch's agent gets for its comments:
// each numbered, with where it is, who said it and the hunk it is about,
// and a pull request's replies after it.
func ReviewPrompt(branch string, comments []ReviewComment) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Review comments on your changes on %s:\n", branch)
	for n, c := range comments {
		var about []string
		if a := ReviewAuthor(c); a != "" {
			about = append(about, "from "+a)
		}
		if c.Outdated {
			about = append(about, "outdated: those lines have changed since")
		}
		by := ""
		if len(about) > 0 {
			by = " (" + strings.Join(about, "; ") + ")"
		}
		header, body, _ := strings.Cut(c.Hunk, "\n")
		switch {
		case c.File == "":
			fmt.Fprintf(&b, "\n%d. On the pull request as a whole%s\n", n+1, by)
		case c.Line > 0:
			fmt.Fprintf(&b, "\n%d. %s, line %d%s\n", n+1, c.File, c.Line, by)
		case c.Hunk != "":
			fmt.Fprintf(&b, "\n%d. %s, at %s%s\n", n+1, c.File, header, by)
		default:
			fmt.Fprintf(&b, "\n%d. %s%s\n", n+1, c.File, by)
		}
		if c.Hunk != "" {
			fmt.Fprintf(&b, "```diff\n%s\n```\n", body)
		}
		b.WriteString(c.Note + "\n")
		for _, r := range c.Replies {
			who := r.By
			if who == "" {
				who = "someone"
			}
			fmt.Fprintf(&b, "  Reply from %s: %s\n", who, indentReply(r.Note))
		}
	}
	b.WriteString("\nAddress each comment. If you disagree with one, say why rather than changing the code.")
	return b.String()
}

// indentReply keeps a reply's later lines under its first.
func indentReply(note string) string {
	return strings.ReplaceAll(strings.TrimSpace(note), "\n", "\n  ")
}

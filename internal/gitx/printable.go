package gitx

import "strings"

// Printable makes text git keeps as it was written — a subject, an author,
// a branch name — safe to draw on a terminal. git keeps control
// characters, so a cloned repository's commit could carry an escape
// (OSC 52 writes the clipboard, OSC 2 retitles the window) that opening
// its history would obey. C0 controls, DEL and the C1 range (0x80–0x9f,
// which a terminal may take as the start of a sequence) become a dot, a
// tab a space, and bytes that are not UTF-8 (a raw 0x9b, say) U+FFFD.
func Printable(s string) string { return printable(s, false) }

// PrintableText is Printable for a commit's body: its line breaks stay.
func PrintableText(s string) string { return printable(s, true) }

func printable(s string, lines bool) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' && lines:
			return r
		case r == '\t':
			return ' '
		case unsafeRune(r):
			return '·'
		}
		return r
	}, s)
}

func unsafeRune(r rune) bool {
	return r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f)
}

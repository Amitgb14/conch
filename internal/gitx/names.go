package gitx

import (
	"path/filepath"
	"strings"
	"unicode"
)

const maxSlug = 60

// WorktreeDir is where a worktree for branch lives: a sibling directory of
// the repository named <repo>.worktrees.
func WorktreeDir(root, branch string) string {
	return filepath.Join(filepath.Dir(root), filepath.Base(root)+".worktrees", Slug(branch))
}

// Slug makes s safe for a directory or branch component: lowercase, with
// runs of other characters (including "/") collapsed to "-".
func Slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '_' {
			b.WriteRune(r)
			dash = false
		} else if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-.")
	if len(out) > maxSlug {
		out = strings.Trim(out[:maxSlug], "-.")
	}
	if out == "" {
		return "task"
	}
	return out
}

var stopwords = map[string]bool{
	"the": true, "a": true, "an": true, "to": true, "of": true, "and": true, "in": true,
	"on": true, "for": true, "with": true, "please": true, "could": true, "you": true,
}

// BranchFromPrompt derives a branch name from an agent prompt, e.g.
// "Fix the flaky login tests!" -> "conch/fix-flaky-login-tests".
func BranchFromPrompt(prompt string) string {
	prompt = strings.NewReplacer("'", "", "’", "").Replace(strings.ToLower(prompt))
	words := strings.FieldsFunc(prompt, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	var kept []string
	for _, w := range words {
		if !stopwords[w] {
			kept = append(kept, w)
		}
		if len(kept) == 5 {
			break
		}
	}
	return "conch/" + Slug(strings.Join(kept, "-"))
}

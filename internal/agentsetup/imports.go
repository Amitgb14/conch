package agentsetup

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Amitgb14/conch/internal/proto"
)

// Claude Code and Gemini CLI expand "@path" imports in their memory files
// (CLAUDE.md, GEMINI.md): paths relative to the importing file, "~/" paths
// or absolute ones, up to five levels deep, ignoring imports inside code.

const maxImportDepth = 5

// importRef matches "@path" at the start of a line or after white space,
// so e-mail addresses and "@mentions" inside words don't count.
var importRef = regexp.MustCompile(`(?:^|\s)@((?:[^\s\\]|\\ )+)`)

// memory adds a memory file and the files it imports.
func (r *report) memory(home, path, scope, detail string) bool {
	if !r.instruction(path, scope, detail) {
		return false
	}
	r.imports(home, path, scope, 1, map[string]bool{cleanPath(path): true})
	return true
}

func (r *report) imports(home, path, scope string, depth int, visiting map[string]bool) {
	if depth > maxImportDepth {
		return
	}
	for _, ref := range importsOf(path) {
		target := resolveImport(home, path, ref)
		key := cleanPath(target)
		if target == "" || visiting[key] || !isFile(target) {
			continue
		}
		r.add(GroupInstructions, proto.SetupItem{Name: filepath.Base(target), Scope: scope, Path: target,
			Detail: "imported by " + filepath.Base(path)})
		visiting[key] = true
		r.imports(home, target, scope, depth+1, visiting)
		delete(visiting, key)
	}
}

// importsOf lists the "@path" references of a memory file outside code
// blocks and code spans.
func importsOf(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil || len(b) > 1<<20 {
		return nil
	}
	var refs []string
	fence := ""
	for _, line := range strings.Split(string(b), "\n") {
		trimmed := strings.TrimSpace(line)
		if fence != "" {
			if strings.HasPrefix(trimmed, fence) {
				fence = ""
			}
			continue
		}
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fence = trimmed[:3]
			continue
		}
		for _, m := range importRef.FindAllStringSubmatch(stripCodeSpans(line), -1) {
			ref := strings.ReplaceAll(m[1], `\ `, " ")
			ref = strings.TrimRight(ref, ".,;:)!?") // punctuation ending a sentence
			if ref != "" {
				refs = append(refs, ref)
			}
		}
	}
	return refs
}

// stripCodeSpans blanks `inline code` so imports mentioned in it are skipped.
func stripCodeSpans(line string) string {
	var b strings.Builder
	in := false
	for _, c := range line {
		switch {
		case c == '`':
			in = !in
			b.WriteRune(' ')
		case in:
			b.WriteRune(' ')
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}

func resolveImport(home, from, ref string) string {
	switch {
	case ref == "~" || strings.HasPrefix(ref, "~/"):
		if home == "" {
			return ""
		}
		return filepath.Join(home, strings.TrimPrefix(ref, "~"))
	case filepath.IsAbs(ref):
		return ref
	}
	return filepath.Join(filepath.Dir(from), ref)
}

func cleanPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

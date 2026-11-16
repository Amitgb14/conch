package integration

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Pick reads a path out of a JSON value decoded with UseNumber.
//
// A selector's value matches without regard to case: a status somebody
// typed is matched to the one a tracker names.
//
// A path is dotted keys and list indexes, "items.0.id". A part may select
// from a list: "items[status=failed]" is its first item whose status reads
// failed; "items[*status=failed]" every such item; "items[*]" every item,
// and "[*]" alone every item of a reply that is a list. After a *, the
// rest is read in each item and the results put in one list; an item that
// lacks it adds nothing. A selector's key may reach inside an item with /
// (vcs/revision); its value is put through fill, so it may be a
// placeholder, and a value that is empty matches every item — a branch
// with no pull request has no head to look for.
//
// It returns nil when nothing is there, and for a path with a * a []any,
// or nil when that would be empty: nothing picked is nothing picked.
func Pick(v any, path string, fill func(string) string) any {
	if path == "" {
		return nil
	}
	cur, many := []any{v}, false
	for _, part := range splitPath(path) {
		field, sel, hasSel := part, "", false
		if i := strings.IndexByte(part, '['); i >= 0 && strings.HasSuffix(part, "]") {
			field, sel, hasSel = part[:i], part[i+1:len(part)-1], true
		}
		all := strings.HasPrefix(sel, "*")
		var key, want string
		if hasSel {
			if all {
				many = true
			}
			if rest := strings.TrimPrefix(sel, "*"); rest != "" {
				var eq bool
				key, want, eq = strings.Cut(rest, "=")
				if !eq || key == "" {
					return nil
				}
				if fill != nil && strings.Contains(want, "{") {
					want = fill(want)
				}
				key = strings.ReplaceAll(key, "/", ".")
			} else if !all {
				return nil // "[]"
			}
		}
		var next []any
		for _, c := range cur {
			if field != "" {
				if c = child(c, field); c == nil {
					continue
				}
			}
			if !hasSel {
				next = append(next, c)
				continue
			}
			list, ok := c.([]any)
			if !ok {
				continue
			}
			for _, it := range list {
				if key != "" && want != "" && !strings.EqualFold(Text(Pick(it, key, nil)), want) {
					continue
				}
				next = append(next, it)
				if !all {
					break
				}
			}
		}
		cur = next
		if len(cur) == 0 {
			return nil
		}
	}
	if many {
		return cur
	}
	return cur[0]
}

// child is one key of a map, or one index of a list.
func child(v any, key string) any {
	switch x := v.(type) {
	case map[string]any:
		return x[key]
	case []any:
		i, err := strconv.Atoi(key)
		if err != nil || i < 0 || i >= len(x) {
			return nil
		}
		return x[i]
	}
	return nil
}

// splitPath splits at the dots outside a selector, so a selector's value
// may hold one.
func splitPath(path string) []string {
	var parts []string
	depth, start := 0, 0
	for i, r := range path {
		switch r {
		case '[':
			depth++
		case ']':
			if depth > 0 {
				depth--
			}
		case '.':
			if depth == 0 {
				parts = append(parts, path[start:i])
				start = i + 1
			}
		}
	}
	return append(parts, path[start:])
}

// Text is a picked value as text: a string as it is, a number as written,
// nothing as "".
func Text(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case json.Number:
		return x.String()
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	default:
		return fmt.Sprint(x)
	}
}

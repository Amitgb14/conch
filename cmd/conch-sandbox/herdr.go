package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// herdr (the terminal, `herdr machine …`) and the herdr app (`hc machine
// …`) keep their own lists of machines, each an ssh target. Their JSON
// is read loosely — every string in an item, and its id and label — since
// conch-sandbox needs only to find its alias there, and a field renamed
// in a later release should not make every sandbox look forgotten.

// herdrBin is herdr: the plugin's own ($HERDR_BIN_PATH), else PATH's.
func herdrBin() string {
	if b := os.Getenv("HERDR_BIN_PATH"); b != "" {
		return b
	}
	return "herdr"
}

// hcBin is the herdr app's command line.
func hcBin() string { return "hc" }

// machine is one of a tool's saved machines.
type machine struct {
	ID, Label string
	strs      []string // every string in it, the target among them
}

// has says whether the machine is reached by alias.
func (m machine) has(alias string) bool {
	for _, s := range m.strs {
		if s == alias || strings.HasSuffix(s, "@"+alias) {
			return true
		}
	}
	return false
}

// toolMachines lists tool's machines.
func toolMachines(ctx context.Context, tool string) ([]machine, error) {
	var cmd *exec.Cmd
	switch tool {
	case "herdr":
		cmd = exec.CommandContext(ctx, herdrBin(), "machine", "list", "--json")
	case "herdr-app":
		cmd = exec.CommandContext(ctx, hcBin(), "machine", "ls", "--json")
	default:
		return nil, errUsage{fmt.Sprintf("--tool %q: herdr or herdr-app", tool)}
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("list %s's machines (%s): %s", tool, strings.Join(cmd.Args, " "), msg)
	}
	return parseMachines(out.Bytes())
}

// parseMachines reads a list of machines: an array, or an object holding
// one (machines, items, …).
func parseMachines(b []byte) ([]machine, error) {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, fmt.Errorf("its machine list is not JSON: %w", err)
	}
	items, ok := v.([]any)
	if !ok {
		obj, isObj := v.(map[string]any)
		if !isObj {
			return nil, errors.New("its machine list is not a list")
		}
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if l, isList := obj[k].([]any); isList {
				items = append(items, l...)
			}
		}
	}
	var out []machine
	for _, it := range items {
		obj, ok := it.(map[string]any)
		if !ok {
			continue
		}
		m := machine{strs: strings1(obj)}
		for _, k := range []string{"id", "profile_id", "profileId"} {
			if s, ok := obj[k].(string); ok && m.ID == "" {
				m.ID = s
			}
		}
		if s, ok := obj["label"].(string); ok {
			m.Label = s
		}
		out = append(out, m)
	}
	return out, nil
}

// strings1 is every string anywhere in v.
func strings1(v any) []string {
	var out []string
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			out = append(out, x)
		case map[string]any:
			for _, y := range x {
				walk(y)
			}
		case []any:
			for _, y := range x {
				walk(y)
			}
		}
	}
	walk(v)
	return out
}

// herdrAlias is the alias of the sandbox the herdr workspace a plugin
// action was run from is on. The context names the workspace's machine,
// by its target or by herdr's id or label for it; herdr's own list turns
// the second into the first.
func herdrAlias(ctx context.Context) (string, error) {
	raw := os.Getenv("HERDR_PLUGIN_CONTEXT_JSON")
	if raw == "" {
		return "", errors.New("--from-herdr: HERDR_PLUGIN_CONTEXT_JSON is not set: run it from a herdr plugin action")
	}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return "", fmt.Errorf("--from-herdr: HERDR_PLUGIN_CONTEXT_JSON is not JSON: %w", err)
	}
	said := map[string]bool{}
	for _, s := range strings1(v) {
		said[s] = true
	}
	found := map[string]bool{}
	for s := range said {
		if name := hostOf(s); aliasLike(name) {
			if _, err := aliasOwner(name); err == nil {
				found[name] = true
			}
		}
	}
	if len(found) == 0 {
		if ms, err := toolMachines(ctx, "herdr"); err == nil {
			for _, m := range ms {
				if (m.ID != "" && said[m.ID]) || (m.Label != "" && said[m.Label]) {
					for _, s := range m.strs {
						if name := hostOf(s); aliasLike(name) {
							if _, err := aliasOwner(name); err == nil {
								found[name] = true
							}
						}
					}
				}
			}
		}
	}
	switch len(found) {
	case 0:
		return "", errors.New("this herdr workspace is not on one of conch-sandbox's sandboxes")
	case 1:
		for a := range found {
			return a, nil
		}
	}
	names := make([]string, 0, len(found))
	for a := range found {
		names = append(names, a)
	}
	sort.Strings(names)
	return "", fmt.Errorf("the herdr context names more than one sandbox: %s", strings.Join(names, ", "))
}

// forgetInHerdr removes herdr's machine for alias, if it has one: herdr
// removes by its own profile id.
func (a *app) forgetInHerdr(ctx context.Context, alias string) {
	ms, err := toolMachines(ctx, "herdr")
	if err != nil {
		fmt.Fprintf(a.stderr, "  %v\n", err)
		return
	}
	for _, m := range ms {
		if !m.has(alias) || m.ID == "" {
			continue
		}
		cmd := exec.CommandContext(ctx, herdrBin(), "machine", "remove", m.ID)
		if out, err := cmd.CombinedOutput(); err != nil {
			fmt.Fprintf(a.stderr, "  couldn't remove herdr's machine %s: %v %s\n", m.ID, err, strings.TrimSpace(string(out)))
			continue
		}
		fmt.Fprintf(a.stderr, "  removed herdr's machine %s\n", firstOf(m.Label, m.ID))
	}
}

func firstOf(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// hostOf is the host of an ssh target, user@host.
func hostOf(target string) string { return target[strings.LastIndex(target, "@")+1:] }

// aliasLike says whether s could be one of conch-sandbox's aliases, before
// the file is read for it.
func aliasLike(s string) bool {
	return strings.HasPrefix(s, "conch-") && !strings.ContainsAny(s, " \t\n/")
}

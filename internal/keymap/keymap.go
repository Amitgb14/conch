// Package keymap is what a key pressed after conch's prefix does, and
// which key that is. The prefix table is tmux's, key for key, so somebody
// who has rebound tmux's wants to rebind conch's: `[keys.bind]` in
// config.toml names an action and the key that runs it, the way tmux's
// bind-key does.
//
// It is a package of its own, and not part of the TUI, because more than
// the TUI has to talk about these: `conch keys` lists them, `conch doctor`
// says when one is bound to nothing or twice, and the settings page offers
// the names. An action's name is a promise to somebody's config.toml —
// renaming one breaks their file silently, so a name here is kept.
package keymap

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
)

// Action is something a key after the prefix does. Its value is the name
// written in config.toml.
type Action string

// Every action a key can be bound to. The comment on each is what `conch
// keys` and the settings page say it does.
const (
	SplitRight  Action = "split_right"
	SplitDown   Action = "split_down"
	CloseSplit  Action = "close_split"
	FocusLeft   Action = "focus_left"
	FocusRight  Action = "focus_right"
	FocusUp     Action = "focus_up"
	FocusDown   Action = "focus_down"
	NextSplit   Action = "next_split"
	LastSplit   Action = "last_split"
	SwapPrev    Action = "swap_split_left"
	SwapNext    Action = "swap_split_right"
	Numbers     Action = "split_numbers"
	Equalize    Action = "equalize"
	NextLayout  Action = "next_layout"
	LayoutEvenH Action = "layout_even_h"
	LayoutEvenV Action = "layout_even_v"
	LayoutMainH Action = "layout_main_h"
	LayoutMainV Action = "layout_main_v"
	LayoutTiled Action = "layout_tiled"
	Zoom        Action = "zoom"
	Back        Action = "back"

	NewTab       Action = "new_tab"
	ChangesTab   Action = "changes_tab"
	NextTab      Action = "next_tab"
	PrevTab      Action = "prev_tab"
	LastTab      Action = "last_tab"
	MoveTab      Action = "move_tab"
	MoveTabLeft  Action = "move_tab_left"
	MoveTabRight Action = "move_tab_right"
	CloseTab     Action = "close_tab"
	RenameTab    Action = "rename_tab"
	TabPicker    Action = "tab_picker"

	NewWorkspace    Action = "new_workspace"
	PrevWorkspace   Action = "prev_workspace"
	NextWorkspace   Action = "next_workspace"
	RenameWorkspace Action = "rename_workspace"
	CloseWorkspace  Action = "close_workspace"

	ScrollMode    Action = "scroll_mode"
	Redraw        Action = "redraw"
	OpenLinks     Action = "open_links"
	MonitorQuiet  Action = "monitor_quiet"
	MonitorOutput Action = "monitor_output"
	SyncPanes     Action = "sync_panes"

	NextWaiting Action = "next_waiting"
	Palette     Action = "palette"
	Ask         Action = "ask"
	Help        Action = "help"
	Detach      Action = "detach"
)

// What each action does, in the words `conch keys`, the settings page and
// the docs use. Every action has one, and a test says so.
var what = map[Action]string{
	SplitRight:      "split right",
	SplitDown:       "split down",
	CloseSplit:      "close the split (ends its pane)",
	FocusLeft:       "focus the split to the left",
	FocusRight:      "focus the split to the right",
	FocusUp:         "focus the split above",
	FocusDown:       "focus the split below",
	NextSplit:       "next split",
	LastSplit:       "last split",
	SwapPrev:        "swap with the split before",
	SwapNext:        "swap with the split after",
	Numbers:         "show split numbers, then a digit",
	Equalize:        "equalize the splits",
	NextLayout:      "next layout",
	LayoutEvenH:     "layout: even horizontal",
	LayoutEvenV:     "layout: even vertical",
	LayoutMainH:     "layout: main horizontal",
	LayoutMainV:     "layout: main vertical",
	LayoutTiled:     "layout: tiled",
	Zoom:            "zoom the split",
	Back:            "back to the split before the last jump",
	NewTab:          "new tab (a terminal in it)",
	ChangesTab:      "this split's branch changes in a tab",
	NextTab:         "next tab",
	PrevTab:         "previous tab",
	LastTab:         "last tab",
	MoveTab:         "move the tab (asks where)",
	MoveTabLeft:     "move the tab left",
	MoveTabRight:    "move the tab right",
	CloseTab:        "close the tab",
	RenameTab:       "rename the tab",
	TabPicker:       "every tab, grouped",
	NewWorkspace:    "new workspace",
	PrevWorkspace:   "previous workspace",
	NextWorkspace:   "next workspace",
	RenameWorkspace: "rename the workspace",
	CloseWorkspace:  "close the workspace",
	ScrollMode:      "scroll the pane's history",
	Redraw:          "draw the pane again",
	OpenLinks:       "open a link the pane printed",
	MonitorQuiet:    "alert when the pane goes quiet",
	MonitorOutput:   "alert when the pane prints",
	SyncPanes:       "type into every split of the tab",
	NextWaiting:     "next agent waiting for you",
	Palette:         "the palette: go to anything, or run a command",
	Ask:             "ask conch in plain words",
	Help:            "the keys overlay",
	Detach:          "detach (agents keep running)",
}

// Defaults is the key, or keys, each action answers to when nothing is
// rebound — tmux's own wherever tmux binds that thing.
var Defaults = map[Action][]string{
	SplitRight:      {"v", "%", "|"},
	SplitDown:       {"-", `"`, "_"},
	CloseSplit:      {"x"},
	FocusLeft:       {"left", "h"},
	FocusRight:      {"right"},
	FocusUp:         {"up", "k"},
	FocusDown:       {"down", "j"},
	NextSplit:       {"o"},
	LastSplit:       {";"},
	SwapPrev:        {"{"},
	SwapNext:        {"}"},
	Numbers:         {"q"},
	Equalize:        {"="},
	NextLayout:      {" "},
	LayoutEvenH:     {"alt+1"},
	LayoutEvenV:     {"alt+2"},
	LayoutMainH:     {"alt+3"},
	LayoutMainV:     {"alt+4"},
	LayoutTiled:     {"alt+5"},
	Zoom:            {"z"},
	Back:            {"b"},
	NewTab:          {"c"},
	ChangesTab:      {"C"},
	NextTab:         {"n"},
	PrevTab:         {"p"},
	LastTab:         {"l"},
	MoveTab:         {"."},
	MoveTabLeft:     {"<"},
	MoveTabRight:    {">"},
	CloseTab:        {"&"},
	RenameTab:       {","},
	TabPicker:       {"w", "s"},
	NewWorkspace:    {"N"},
	PrevWorkspace:   {"("},
	NextWorkspace:   {")"},
	RenameWorkspace: {"$"},
	CloseWorkspace:  {"X"},
	ScrollMode:      {"[", "pgup"},
	Redraw:          {"r"},
	OpenLinks:       {"u"},
	MonitorQuiet:    {"M"},
	MonitorOutput:   {"A"},
	SyncPanes:       {"S"},
	NextWaiting:     {"!"},
	Palette:         {"/"},
	Ask:             {":"},
	Help:            {"?"},
	Detach:          {"d"},
}

// What is what an action does, for a list or a label.
func What(a Action) string { return what[a] }

// Actions is every action there is, in a settled order: the order they are
// listed in, which is the order `conch keys` prints and the docs follow.
func Actions() []Action {
	out := slices.Collect(maps.Keys(Defaults))
	slices.Sort(out)
	return out
}

// Map says what each key does after the prefix, both ways round: by key to
// route a press, and by action to say which key to press.
//
// Its zero value is the default keys, not no keys. Everything that holds
// one of these holds it by value — a Model copied for every message, a
// test that builds one field by field — and a zero Map that answered
// nothing would be a conch whose prefix table did nothing at all, which is
// a worse failure than any mistake in config.toml.
type Map struct {
	byKey map[string]Action
	byAct map[Action][]string
}

// std is the default table, made once: the zero Map stands for it.
var std = sync.OnceValue(func() Map {
	m, _ := Resolve(nil)
	return m
})

func (m Map) resolved() Map {
	if m.byKey == nil {
		return std()
	}
	return m
}

// Action is what key does, or "" if it does nothing.
func (m Map) Action(key string) Action { return m.resolved().byKey[key] }

// Keys is the keys an action answers to, in the order they are shown. It
// is empty for an action somebody has unbound.
func (m Map) Keys(a Action) []string { return m.resolved().byAct[a] }

// Key is the first key an action answers to, or "" when it is unbound.
func (m Map) Key(a Action) string {
	if ks := m.Keys(a); len(ks) > 0 {
		return ks[0]
	}
	return ""
}

// Bound reports whether a can be pressed at all.
func (m Map) Bound(a Action) bool { return len(m.Keys(a)) > 0 }

// Rebound reports whether anything in config.toml moved this action, which
// is what makes a hint worth spelling out rather than assuming.
func (m Map) Rebound(a Action) bool { return !slices.Equal(m.Keys(a), Defaults[a]) }

// Resolve reads a `[keys.bind]` table — an action's name to the key that
// runs it — into a Map, and says what was wrong with it rather than
// quietly dropping it. The rules, which are tmux's:
//
//   - what is not named keeps its default keys;
//   - a key named for one action is taken off whatever default held it, so
//     rebinding never needs the other action unbound first;
//   - an empty key (or "none") unbinds the action, as tmux's unbind-key;
//   - two actions on one key is a mistake, not a race: the first in
//     alphabetical order keeps it and the other is reported, so the same
//     file always gives the same keys.
//
// Nothing here can leave conch unreachable: the prefix is `[keys] prefix`,
// not an action, and a file that is all mistakes is a help overlay with
// nothing in its key column — never a TUI that cannot be typed at.
func Resolve(bind map[string]string) (Map, []string) {
	m := Map{byKey: map[string]Action{}, byAct: map[Action][]string{}}
	var problems []string
	named := map[Action]bool{}
	taken := map[string]Action{}
	for _, name := range slices.Sorted(maps.Keys(bind)) {
		a := Action(name)
		if _, ok := Defaults[a]; !ok {
			problems = append(problems, fmt.Sprintf("%s is not a key conch binds (conch keys lists them)", name))
			continue
		}
		named[a] = true
		key := Normalize(bind[name])
		if key == "" || key == "none" {
			continue // unbound on purpose
		}
		if other, dup := taken[key]; dup {
			problems = append(problems, fmt.Sprintf("%s and %s are both on %s, so %s keeps it", other, name, Show(key), other))
			continue
		}
		taken[key] = a
		m.byKey[key] = a
		m.byAct[a] = []string{key}
	}
	for _, a := range Actions() {
		if named[a] {
			continue
		}
		for _, k := range Defaults[a] {
			if _, used := taken[k]; used {
				continue // somebody wanted that key for something else
			}
			m.byKey[k] = a
			m.byAct[a] = append(m.byAct[a], k)
		}
	}
	return m, problems
}

// Default is the map with nothing rebound.
func Default() Map {
	m, _ := Resolve(nil)
	return m
}

// named is the spelling of the keys that have a name rather than a
// character, as a key is written in config.toml and shown back.
var named = map[string]string{
	" ": "space", "up": "↑", "down": "↓", "left": "←", "right": "→",
	"pgup": "pgup", "pgdown": "pgdown", "enter": "enter", "tab": "tab",
	"esc": "esc", "home": "home", "end": "end", "backspace": "backspace",
	"delete": "delete",
}

// Normalize is a key as written in config.toml in conch's own spelling.
// tmux's is taken too — `C-a`, `M-x`, `Space` — because somebody rebinding
// conch's prefix table has tmux's in front of them, and a key that silently
// never matches is the worst way to find that out.
func Normalize(key string) string {
	if key == " " {
		return key // the space key, which trimming would turn into nothing
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	for _, p := range []struct{ from, to string }{{"C-", "ctrl+"}, {"M-", "alt+"}, {"S-", "shift+"}} {
		if len(key) > len(p.from) && strings.HasPrefix(key, p.from) {
			key = p.to + key[len(p.from):]
		}
	}
	mod, rest, found := strings.Cut(key, "+")
	if !found {
		mod, rest = "", key
	}
	// A name is a name whatever case it is written in; a letter is not —
	// S and s are two keys, and lower-casing one would hand somebody the
	// wrong binding.
	if len(rest) > 1 {
		low := strings.ToLower(rest)
		if low == "space" {
			rest = " "
		} else if _, ok := named[low]; ok {
			rest = low
		}
	}
	if mod == "" {
		return rest
	}
	return strings.ToLower(mod) + "+" + rest
}

// Show is a key as it is shown to somebody — the arrows as arrows, space
// as a word rather than a blank in the middle of a list.
func Show(key string) string {
	if s, ok := named[key]; ok {
		return s
	}
	if mod, rest, found := strings.Cut(key, "+"); found {
		if s, ok := named[rest]; ok {
			return mod + "+" + s
		}
	}
	return key
}

// ShowAll is the keys of one action, as a key column shows them.
func ShowAll(keys []string) string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, Show(k))
	}
	return strings.Join(out, " ")
}

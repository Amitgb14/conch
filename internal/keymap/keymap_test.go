package keymap

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Every action has a default key and words for what it does: an action
// with neither is a name in somebody's config.toml that conch cannot
// explain or undo.
func TestEveryActionIsWholeAndUnique(t *testing.T) {
	if len(Actions()) < 40 {
		t.Fatalf("%d actions", len(Actions()))
	}
	who := map[string]Action{}
	for _, a := range Actions() {
		if What(a) == "" {
			t.Errorf("%s says nothing about itself", a)
		}
		keys := Defaults[a]
		if len(keys) == 0 {
			t.Errorf("%s has no default key", a)
		}
		if strings.ToLower(string(a)) != string(a) || strings.Contains(string(a), " ") {
			t.Errorf("%s is not a name for a config file", a)
		}
		for _, k := range keys {
			if other, dup := who[k]; dup {
				t.Errorf("%s and %s both default to %q", other, a, k)
			}
			who[k] = a
			if Normalize(k) != k {
				t.Errorf("%s: default key %q is not how conch spells it (%q)", a, k, Normalize(k))
			}
		}
	}
	// The digits and the resize keys are a family rather than an action,
	// so nothing may claim them: conch keys says as much.
	for _, k := range []string{"1", "5", "0", "ctrl+left", "alt+up"} {
		if a := Default().Action(k); a != "" {
			t.Errorf("%q is %s, but it is not a bindable key", k, a)
		}
	}
}

// The zero Map is the default keys. Everything holds one by value — a
// Model copied for every message, a test that fills in fields — and a
// zero Map that answered nothing would be a conch with no prefix table.
func TestZeroMapIsTheDefaults(t *testing.T) {
	var zero Map
	if got := zero.Action("x"); got != CloseSplit {
		t.Errorf("x is %q on the zero map", got)
	}
	if got := zero.Key(Zoom); got != "z" {
		t.Errorf("zoom is on %q", got)
	}
	if !zero.Bound(Detach) || zero.Rebound(Detach) {
		t.Error("the zero map calls detach rebound or unbound")
	}
}

func TestResolveRebinds(t *testing.T) {
	m, problems := Resolve(map[string]string{
		"split_right": `\`,
		"next_tab":    "C-n",   // tmux's spelling
		"zoom":        "Space", // and tmux's name for the key
		"close_split": "",      // unbound
	})
	if len(problems) > 0 {
		t.Fatalf("problems: %v", problems)
	}
	for _, tc := range []struct {
		key  string
		want Action
	}{
		{`\`, SplitRight},
		{"ctrl+n", NextTab},
		{" ", Zoom},
		{"x", ""},        // unbound
		{"v", ""},        // split_right moved, so its default is free
		{"z", ""},        // zoom moved
		{"n", ""},        // next_tab moved
		{"c", NewTab},    // untouched
		{`"`, SplitDown}, // and so are the other keys of an action
	} {
		if got := m.Action(tc.key); got != tc.want {
			t.Errorf("%q is %q, want %q", tc.key, got, tc.want)
		}
	}
	if m.Bound(CloseSplit) || m.Key(CloseSplit) != "" {
		t.Errorf("close_split is still on %q", m.Key(CloseSplit))
	}
	// next_layout's only key went to zoom, so it has none left — the
	// thing nothing else would tell you.
	if m.Bound(NextLayout) {
		t.Errorf("next_layout kept %q", m.Keys(NextLayout))
	}
	if !m.Rebound(SplitRight) || m.Rebound(NewTab) {
		t.Error("Rebound cannot tell a moved key from an untouched one")
	}
	if got := m.Keys(SplitRight); !slices.Equal(got, []string{`\`}) {
		t.Errorf("split_right is on %q", got)
	}
}

func TestResolveSaysWhatIsWrong(t *testing.T) {
	m, problems := Resolve(map[string]string{
		"nosuchthing": "k",
		"help":        "x",
		"detach":      "x", // two on one key
	})
	if len(problems) != 2 {
		t.Fatalf("problems: %v", problems)
	}
	if !strings.Contains(problems[0], "detach and help are both on x") || !strings.Contains(problems[0], "detach keeps it") {
		t.Errorf("the collision reads %q", problems[0])
	}
	if !strings.Contains(problems[1], "nosuchthing is not a key conch binds") {
		t.Errorf("the unknown action reads %q", problems[1])
	}
	// The first by name keeps it, so the same file always gives the same
	// keys; the loser is left with nothing rather than its default, which
	// somebody asked it to leave.
	if got := m.Action("x"); got != Detach {
		t.Errorf("x is %q", got)
	}
	if m.Bound(Help) {
		t.Errorf("help kept %q", m.Keys(Help))
	}
	// A table that is nothing but mistakes still leaves conch usable.
	m, problems = Resolve(map[string]string{"nope": "q", "also_nope": ""})
	if len(problems) != 2 || m.Action("x") != CloseSplit || m.Action("c") != NewTab {
		t.Fatalf("mistakes took the defaults away: %v", problems)
	}
}

func TestNormalizeAndShow(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"x", "x"},
		{" x ", "x"},
		{"C-n", "ctrl+n"},
		{"M-x", "alt+x"},
		{"S-tab", "shift+tab"},
		{"ctrl+n", "ctrl+n"},
		{"Space", " "},
		{"space", " "},
		{"SPACE", " "},
		{"Up", "up"},
		{"PgUp", "pgup"},
		{"M-Up", "alt+up"},
		{"C-Space", "ctrl+ "},
		{"S", "S"}, // a letter keeps its case: S and s are two keys
		{"s", "s"},
		{"C", "C"},
		{"M", "M"},
		{"", ""},
		{"  ", ""}, // nothing in particular: unbound
		{" ", " "}, // but one space is the space key, which tmux calls Space
		{"alt+1", "alt+1"},
		{"ALT+1", "alt+1"}, // the modifier's case does not matter
	} {
		if got := Normalize(tc.in); got != tc.want {
			t.Errorf("Normalize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	for _, tc := range []struct{ in, want string }{
		{" ", "space"}, {"up", "↑"}, {"ctrl+left", "ctrl+←"}, {"x", "x"}, {"alt+1", "alt+1"},
	} {
		if got := Show(tc.in); got != tc.want {
			t.Errorf("Show(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if got := ShowAll([]string{"left", "h"}); got != "← h" {
		t.Errorf("ShowAll: %q", got)
	}
	if got := ShowAll(nil); got != "" {
		t.Errorf("ShowAll(nil): %q", got)
	}
}

// A key named for an action is taken off every default that held it, not
// only the first — and an action keeps the rest of its own keys.
func TestResolveTakesTheKeyFromEveryDefault(t *testing.T) {
	m, problems := Resolve(map[string]string{"ask": "h"}) // h is focus_left's second key
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	if got := m.Action("h"); got != Ask {
		t.Errorf("h is %q", got)
	}
	if got := m.Keys(FocusLeft); !slices.Equal(got, []string{"left"}) {
		t.Errorf("focus_left is on %q, want only left", got)
	}
	if got := m.Keys(Ask); !slices.Equal(got, []string{"h"}) {
		t.Errorf("ask is on %q", got)
	}
}

// ResolveFor drops a binding on the prefix, says so, and keeps that
// action's default; the prefix is compared in conch's spelling, so tmux's
// C-a in either place is the same key.
func TestResolveForDropsThePrefix(t *testing.T) {
	m, problems := ResolveFor(map[string]string{"zoom": "C-a", "next_tab": "N"}, "ctrl+a")
	if got := m.Keys(Zoom); len(got) != 1 || got[0] != "z" {
		t.Errorf("zoom on %q, want its default z", got)
	}
	if m.Action("ctrl+a") != "" {
		t.Error("the prefix itself runs an action")
	}
	if m.Key(NextTab) != "N" {
		t.Errorf("next_tab on %q: the rest of the table must still apply", m.Key(NextTab))
	}
	if len(problems) != 1 || problems[0] != "zoom is on ctrl+a, which is the prefix itself" {
		t.Errorf("problems %q", problems)
	}
	// No bind, or no prefix: Resolve's table, nothing said.
	for _, c := range []struct {
		bind   map[string]string
		prefix string
	}{{nil, "ctrl+b"}, {map[string]string{"zoom": "Z"}, ""}} {
		if _, p := ResolveFor(c.bind, c.prefix); len(p) != 0 {
			t.Errorf("ResolveFor(%v, %q) says %q", c.bind, c.prefix, p)
		}
	}
}

// Conch.app does the actions it lists in prefixActionsInApp
// (macos/Sources/ConchKit/PrefixKeys.swift) by these names. CI does not
// build the app, so this is what notices an action renamed here and not
// there — which would turn that key into "the TUI's" in the app, unsaid.
func TestAppPrefixActionsExist(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "macos", "Sources", "ConchKit", "PrefixKeys.swift"))
	if err != nil {
		t.Fatal(err)
	}
	_, list, found := strings.Cut(string(src), "public let prefixActionsInApp: Set<String> = [")
	list, _, closed := strings.Cut(list, "]")
	if !found || !closed {
		t.Fatal("prefixActionsInApp not found in PrefixKeys.swift")
	}
	names := regexp.MustCompile(`"([a-z_]+)"`).FindAllStringSubmatch(list, -1)
	if len(names) < 10 {
		t.Fatalf("only %d actions read from the app's list", len(names))
	}
	for _, n := range names {
		if _, ok := Defaults[Action(n[1])]; !ok {
			t.Errorf("the app does %q, which is not an action", n[1])
		}
	}
}

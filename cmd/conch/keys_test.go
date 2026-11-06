package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/doctor"
	"github.com/Amitgb14/conch/internal/keymap"
)

// keysHome is a scratch CONCH_HOME with that config.toml in it.
func keysHome(t *testing.T, toml string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CONCH_HOME", dir)
	t.Setenv("CONCH_SOCKET", "")
	if toml != "" {
		if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(toml), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// keysSqueeze collapses the runs of spaces the list is laid out with, so a
// test says what a row *says* and not how wide tabwriter made the column —
// which follows the longest key in that run.
func keysSqueeze(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// `conch keys` with nothing rebound: every action, its key and what it
// does, and the two key families that are not in the table said plainly —
// somebody who cannot find `1` there should not be left guessing.
func TestKeysLists(t *testing.T) {
	keysHome(t, "")
	out, _ := a4Capture(t, "", func() {
		if err := cmdKeys(nil); err != nil {
			t.Fatalf("conch keys: %v", err)
		}
	})
	if !strings.HasPrefix(out, "ctrl+b, then:") {
		t.Errorf("starts %q", strings.SplitN(out, "\n", 2)[0])
	}
	for _, want := range []string{
		"x close_split", "z zoom", "space next_layout", "← h focus_left",
		"[keys.bind]", "A digit is the tab at that place",
	} {
		if !strings.Contains(keysSqueeze(out), want) {
			t.Errorf("conch keys does not say %q:\n%s", want, out)
		}
	}
	// Every action is listed, by name, with what it does.
	for _, a := range keymap.Actions() {
		if !strings.Contains(out, string(a)) || !strings.Contains(out, keymap.What(a)) {
			t.Errorf("%s is not listed", a)
		}
	}
	if strings.Contains(out, "(was ") || strings.Contains(out, "unbound") {
		t.Error("nothing is rebound, but it says something is")
	}
}

// Rebound, unbound, and a key taken by something else: each said in its
// own words, since "unbound" and "its key went elsewhere" are different
// mistakes.
func TestKeysSaysWhatMoved(t *testing.T) {
	keysHome(t, "[keys]\nprefix = \"ctrl+a\"\n\n[keys.bind]\nsplit_right = \"\\\\\"\nnext_tab = \"C-n\"\nzoom = \"Space\"\nclose_split = \"\"\n")
	out, _ := a4Capture(t, "", func() {
		if err := cmdKeys(nil); err != nil {
			t.Fatalf("conch keys: %v", err)
		}
	})
	for _, want := range []string{
		"ctrl+a, then:",
		`\ split_right split right (was v % |)`,
		"ctrl+n next_tab next tab (was n)",
		"space zoom zoom the split (was z)",
		"— close_split close the split (ends its pane) (unbound)",
		"— next_layout next layout (space is zoom now)",
	} {
		if !strings.Contains(keysSqueeze(out), want) {
			t.Errorf("conch keys does not say %q:\n%s", want, out)
		}
	}
	// -bound leaves out the ones that cannot be pressed.
	out, _ = a4Capture(t, "", func() {
		if err := cmdKeys([]string{"-bound"}); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(out, "close_split") || strings.Contains(out, "next_layout") {
		t.Errorf("-bound lists an unbound key:\n%s", out)
	}
	if !strings.Contains(out, "split_right") {
		t.Error("-bound lost a bound key")
	}
}

// -toml prints the table as config.toml takes it, so it can be pasted and
// edited from what is in force rather than from nothing.
func TestKeysTOML(t *testing.T) {
	keysHome(t, "[keys]\nprefix = \"ctrl+a\"\n\n[keys.bind]\nzoom = \"f\"\n")
	out, _ := a4Capture(t, "", func() {
		if err := cmdKeys([]string{"-toml"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "[keys.bind]") || !strings.Contains(out, `prefix = "ctrl+a"`) {
		t.Errorf("no table:\n%s", out)
	}
	for _, want := range []string{`zoom               = "f"`, `close_split        = "x"`, `# close the split`,
		`split_right        = "v"`} { // the first of v % |
		if !strings.Contains(out, want) {
			t.Errorf("-toml does not have %q:\n%s", want, out)
		}
	}
	// And what it prints is a table conch reads back as what it says: the
	// same file again, prefix and all, so it can be pasted whole.
	if err := os.WriteFile(filepath.Join(os.Getenv("CONCH_HOME"), "config.toml"), []byte(out), 0o600); err != nil {
		t.Fatal(err)
	}
	out2, _ := a4Capture(t, "", func() {
		if err := cmdKeys([]string{"-toml"}); err != nil {
			t.Fatal(err)
		}
	})
	if out2 != out {
		t.Errorf("printing its own table back changed it:\n%s\n---\n%s", out, out2)
	}
	// Pasting it settles an action that answered to several keys on the
	// first of them, which is said in the file and is why % no longer
	// splits.
	out3, _ := a4Capture(t, "", func() {
		if err := cmdKeys(nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(keysSqueeze(out3), `v split_right split right (was v % |)`) {
		t.Errorf("after pasting, split_right reads:\n%s", out3)
	}
}

// A mistake in the table is named, by both the command and the doctor, and
// the command exits non-zero so a script notices.
func TestKeysReportsMistakes(t *testing.T) {
	keysHome(t, "[keys.bind]\nnosuchthing = \"k\"\nhelp = \"x\"\ndetach = \"x\"\n")
	out, err := a4Capture(t, "", func() {
		if err := cmdKeys(nil); err == nil {
			t.Error("a bad table is not an error")
		} else if !strings.Contains(err.Error(), "nosuchthing is not a key conch binds") ||
			!strings.Contains(err.Error(), "detach and help are both on x") {
			t.Errorf("error: %v", err)
		}
	})
	_ = err
	// The list is still printed: the keys conch could make sense of are
	// the keys in force, and that is what somebody needs to see.
	if !strings.Contains(out, "split_right") {
		t.Errorf("no list:\n%s", out)
	}
	doctorEnv(t)
	keysHome(t, "[keys.bind]\nnosuchthing = \"k\"\n")
	if c := doctorFind(t, "keys"); c.Status != doctor.Warn ||
		!strings.Contains(c.Detail, "nosuchthing is not a key conch binds") || c.Fix == "" {
		t.Errorf("doctor: %+v", c)
	}
}

// The doctor says what the table came to when it is right, so somebody
// reading it can see their own keys are in force.
func TestDoctorKeysWhenRight(t *testing.T) {
	doctorEnv(t)
	keysHome(t, "[keys]\nprefix = \"ctrl+a\"\n\n[keys.bind]\nzoom = \"f\"\nclose_split = \"\"\n")
	c := doctorFind(t, "keys")
	if c.Status != doctor.OK {
		t.Fatalf("%+v", c)
	}
	for _, want := range []string{"ctrl+a, then", "1 rebound, 1 unbound"} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("detail %q, want %q in it", c.Detail, want)
		}
	}
	keysHome(t, "")
	if c := doctorFind(t, "keys"); c.Status != doctor.OK || strings.Contains(c.Detail, "rebound") {
		t.Errorf("defaults: %+v", c)
	}
}

// -json is what Conch.app reads its prefix keys from: the prefix in
// conch's spelling whatever config.toml wrote, every action with its keys
// (none for an unbound one, never a missing field), and a binding on the
// prefix taken out and said, as the TUI does — or the app and the TUI
// would give one file two different tables.
func TestKeysJSON(t *testing.T) {
	keysHome(t, "[keys]\nprefix = \"C-a\"\n\n[keys.bind]\nzoom = \"Space\"\nclose_split = \"\"\nnext_tab = \"ctrl+a\"\nnot_a_thing = \"y\"\n")
	out, _ := a4Capture(t, "", func() {
		if err := cmdKeys([]string{"-json"}); err != nil {
			t.Fatalf("conch keys -json: %v", err)
		}
	})
	var got keysTable
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if got.Prefix != "ctrl+a" {
		t.Errorf("prefix %q, want ctrl+a", got.Prefix)
	}
	if len(got.Keys) != len(keymap.Actions()) {
		t.Errorf("%d actions, want %d", len(got.Keys), len(keymap.Actions()))
	}
	by := map[string]keysBinding{}
	for _, b := range got.Keys {
		by[b.Action] = b
	}
	if k := by["zoom"].Keys; len(k) != 1 || k[0] != " " {
		t.Errorf("zoom on %q, want space", k)
	}
	if k := by["close_split"].Keys; k == nil || len(k) != 0 {
		t.Errorf("close_split on %#v, want an empty list", k)
	}
	if k := by["next_tab"].Keys; len(k) != 1 || k[0] != "n" {
		t.Errorf("next_tab on %q: a bind on the prefix must be dropped and the default kept", k)
	}
	if by["split_right"].What != keymap.What(keymap.SplitRight) {
		t.Errorf("split_right says %q", by["split_right"].What)
	}
	problems := strings.Join(got.Problems, "; ")
	for _, want := range []string{"next_tab is on ctrl+a, which is the prefix itself", "not_a_thing is not a key conch binds"} {
		if !strings.Contains(problems, want) {
			t.Errorf("problems %q do not say %q", problems, want)
		}
	}
	// An empty list rather than null: a decoder that wants an array has one.
	if strings.Contains(out, "null") {
		t.Errorf("-json has a null in it:\n%s", out)
	}
}

// With no config.toml at all, -json is the defaults, and no problems.
func TestKeysJSONDefaults(t *testing.T) {
	keysHome(t, "")
	out, _ := a4Capture(t, "", func() {
		if err := cmdKeys([]string{"-json"}); err != nil {
			t.Fatalf("conch keys -json: %v", err)
		}
	})
	var got keysTable
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.Prefix != "ctrl+b" || len(got.Problems) != 0 || strings.Contains(out, `"problems"`) {
		t.Errorf("defaults: prefix %q, problems %q", got.Prefix, got.Problems)
	}
	for _, b := range got.Keys {
		if want := keymap.Defaults[keymap.Action(b.Action)]; strings.Join(b.Keys, " ") != strings.Join(want, " ") {
			t.Errorf("%s on %q, want %q", b.Action, b.Keys, want)
		}
	}
}

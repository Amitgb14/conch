package tui

import (
	"io/fs"
	"slices"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/detect"
)

// shippedAgentNames is every agent conch ships a manifest for, read from
// the manifests themselves rather than written out here: a list in a test
// is one more place to forget an agent, and a forgotten agent is the one
// row in the tree with no mark.
func shippedAgentNames(t *testing.T) []string {
	t.Helper()
	entries, err := fs.ReadDir(detect.Builtin(), "manifests")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		b, err := fs.ReadFile(detect.Builtin(), "manifests/"+e.Name())
		if err != nil {
			t.Fatal(err)
		}
		var m struct {
			Agent string `toml:"agent"`
		}
		if _, err := toml.Decode(string(b), &m); err != nil || m.Agent == "" {
			t.Fatalf("%s: no agent name (%v)", e.Name(), err)
		}
		names = append(names, m.Agent)
	}
	slices.Sort(names)
	return names
}

// The list really is read, and holds the agents everybody knows are there.
func TestShippedAgentNamesAreRead(t *testing.T) {
	names := shippedAgentNames(t)
	for _, want := range []string{"claude", "codex", "gemini", "opencode", "devin", "pi"} {
		if !slices.Contains(names, want) {
			t.Errorf("%s is not among the shipped agents: %v", want, names)
		}
	}
}

// Every dot is two cells and every glyph one, or the names after them stop
// lining up (AGENTS.md, Adding an agent · Marks). The marks past the six
// squares are other emoji, which is where a one-cell character could slip
// in unnoticed.
func TestEveryMarkIsItsColumnsWide(t *testing.T) {
	for _, name := range shippedAgentNames(t) {
		if w := ansi.StringWidth(agentDots[name]); w != 2 {
			t.Errorf("%s's dot %q is %d cells, not 2", name, agentDots[name], w)
		}
		if w := ansi.StringWidth(agentGlyphs[name]); w != 1 {
			t.Errorf("%s's glyph is %d cells, not 1", name, w)
		}
	}
}

package tui

import (
	"path/filepath"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestSelectionText(t *testing.T) {
	lines := []string{
		"\x1b[31mfirst\x1b[0m line   ",
		"second line",
		"third",
	}
	for _, tc := range []struct {
		name string
		sel  selection
		want string
	}{
		{"within a line", selection{ax: 0, ay: 0, bx: 4, by: 0}, "first"},
		{"backwards drag", selection{ax: 3, ay: 1, bx: 6, by: 0}, "line\nseco"},
		{"across lines trims trailing space", selection{ax: 6, ay: 0, bx: 2, by: 2}, "line\nsecond line\nthi"},
	} {
		if got := tc.sel.text(lines, 20); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestSelectionHighlightKeepsWidth(t *testing.T) {
	lines := []string{"\x1b[32mhello\x1b[0m world", "next"}
	out := selection{ax: 2, ay: 0, bx: 7, by: 0}.highlight(lines, 20)
	if got := ansi.Strip(out[0]); got != "hello world" && got != "hello world         " {
		t.Fatalf("text changed: %q", got)
	}
	if out[1] != lines[1] {
		t.Fatal("unselected line changed")
	}
}

func TestWordAt(t *testing.T) {
	line := "  run go test ./internal/... now"
	from, to := wordAt(line, 16)
	if got := ansi.Cut(line, from, to+1); got != "./internal/..." {
		t.Fatalf("word %q", got)
	}
	if from, to := wordAt(line, 0); from != to {
		t.Fatal("space selected a word")
	}
}

func TestUIStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ui.json")
	if st := loadUIState(path); st.Expanded == nil || st.ShowAll == nil {
		t.Fatal("defaults must have maps")
	}
	want := uiState{Expanded: map[string]bool{"p:r1": false, "p:r1/branches": true}, ShowAll: map[string]bool{"r1": true}, SidebarWidth: 44}
	if err := saveUIState(path, want); err != nil {
		t.Fatal(err)
	}
	got := loadUIState(path)
	if got.SidebarWidth != 44 || got.Expanded["p:r1"] || !got.Expanded["p:r1/branches"] || !got.ShowAll["r1"] {
		t.Fatalf("round trip: %+v", got)
	}
}

func TestApplyTheme(t *testing.T) {
	defer applyTheme("conch", "")
	applyTheme("nord", "")
	if colorAccent != themeByName("nord").accent {
		t.Fatalf("nord accent: %v", colorAccent)
	}
	applyTheme("nord", "orange")
	if colorAccent != accentColors["orange"] {
		t.Fatalf("accent override: %v", colorAccent)
	}
	applyTheme("nonsense", "#123456")
	if colorAccent != "#123456" || colorBorder != themes[0].border {
		t.Fatalf("unknown theme with hex accent: %v %v", colorAccent, colorBorder)
	}
}

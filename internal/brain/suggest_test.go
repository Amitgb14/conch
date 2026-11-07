package brain

import (
	"strings"
	"testing"
)

func suggestWorld() World {
	return World{Machines: []Machine{
		{ID: "local", Label: "local", Online: true,
			Projects: []Project{{ID: "r1", Name: "api"}},
			Panes: []Pane{
				{ID: "p1", Name: "claude", Agent: "claude", State: "blocked"},
				{ID: "p2", Name: "codex", Agent: "codex", State: "working"},
				{ID: "p3", Name: "zsh"},
			}},
		{ID: "box", Label: "box", Online: false, Panes: []Pane{
			{ID: "p1", Name: "gemini", Agent: "gemini", State: "blocked"}, // offline: not counted
		}},
	}}
}

func optionTexts(opts []Suggestion) []string {
	var out []string
	for _, o := range opts {
		out = append(out, o.Text)
	}
	return out
}

func findOption(opts []Suggestion, text string) (Suggestion, bool) {
	for _, o := range opts {
		if o.Text == text {
			return o, true
		}
	}
	return Suggestion{}, false
}

func TestSuggestFromTheWorld(t *testing.T) {
	w := suggestWorld()
	if c := CountAgents(w); c != (Counts{Agents: 2, Waiting: 1, Working: 1}) || c.String() != "2 agents · 1 waiting · 1 working" {
		t.Fatalf("counts %+v %q", c, c.String())
	}
	opts := Suggest(w, "codex", false, true, nil)
	got := optionTexts(opts)
	want := []string{"what is waiting for me?", "show claude", "tell codex to …", "tell everyone to …", "what is working?", "start an agent on api to …"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("options\n got %q\nwant %q", got, want)
	}
	if o, _ := findOption(opts, "what is waiting for me?"); o.Note != "1 waiting · instant" || o.Fill() {
		t.Fatalf("waiting: %+v", o)
	}
	// What says instant is answered by the fast path.
	if o, _ := findOption(opts, "tell codex to …"); !o.Fill() || o.Stem() != "tell codex to " || !strings.HasSuffix(o.Note, "instant") {
		t.Fatalf("tell: %+v", o)
	}
	for _, o := range opts {
		if o.Recent {
			t.Fatalf("no history, no recent: %+v", o)
		}
	}

	// A selected project offers "here" instead of naming one; the agent
	// selected is not offered back to itself as waiting.
	opts = Suggest(w, "claude", true, true, nil)
	if _, ok := findOption(opts, "start an agent here to …"); !ok {
		t.Fatalf("place: %q", optionTexts(opts))
	}
	if _, ok := findOption(opts, "show claude"); ok {
		t.Fatalf("the selected agent is shown already: %q", optionTexts(opts))
	}
	if _, ok := findOption(opts, "start an agent on api to …"); ok {
		t.Fatal("with a place selected, here is enough")
	}
}

func TestSuggestEmptyWorld(t *testing.T) {
	var w World
	if c := CountAgents(w); c.String() != "no agents running" {
		t.Fatalf("%q", c.String())
	}
	opts := Suggest(w, "", false, true, nil)
	// Still something to try, and nothing that names what isn't there.
	if got := optionTexts(opts); len(got) != 1 || got[0] != "what is waiting for me?" {
		t.Fatalf("empty world: %q", got)
	}
}

func TestSuggestRecent(t *testing.T) {
	w := suggestWorld()
	hist := []string{"one", "two", "  ", "what is working?", "three", "four", "five", "show zsh"}
	opts := Suggest(w, "", false, true, hist)
	var recent []string
	for _, o := range opts {
		if o.Recent {
			recent = append(recent, o.Text)
		}
	}
	// Newest first, blanks and repeats of a suggestion skipped, four at most.
	if strings.Join(recent, "|") != "show zsh|five|four|three" {
		t.Fatalf("recent %q", recent)
	}
	if o, _ := findOption(opts, "show zsh"); o.Note != "instant" {
		t.Fatalf("show zsh: %+v", o)
	}
	if o, _ := findOption(opts, "five"); o.Note != "asks the model" {
		t.Fatalf("five: %+v", o)
	}
}

func TestSuggestMatching(t *testing.T) {
	opts := Suggest(suggestWorld(), "codex", false, true, []string{"show zsh"})
	if len(Matching(opts, "")) != len(opts) || len(Matching(opts, "   ")) != len(opts) {
		t.Fatal("nothing typed keeps every option")
	}
	if got := optionTexts(Matching(opts, "TELL")); strings.Join(got, "|") != "tell codex to …|tell everyone to …" {
		t.Fatalf("tell: %q", got)
	}
	// The option already typed is not offered again.
	if got := optionTexts(Matching(opts, "show zsh")); len(got) != 0 {
		t.Fatalf("exact: %q", got)
	}
	if got := Matching(opts, "nothing like this"); len(got) != 0 {
		t.Fatalf("no match: %q", optionTexts(got))
	}
}

// With the fast path off every request goes to the model, so nothing may
// say it is instant — but a waiting agent is still worth naming.
func TestSuggestWithoutTheFastPath(t *testing.T) {
	opts := Suggest(suggestWorld(), "codex", false, false, []string{"show zsh"})
	for _, o := range opts {
		if strings.Contains(o.Note, "instant") {
			t.Fatalf("%q says instant with the fast path off", o.Text)
		}
	}
	if o, ok := findOption(opts, "show claude"); !ok || o.Note != "waiting for you" {
		t.Fatalf("show claude: %+v %v", o, ok)
	}
	if o, _ := findOption(opts, "show zsh"); o.Note != "asks the model" {
		t.Fatalf("recent: %+v", o)
	}
	if o, _ := findOption(opts, "what is waiting for me?"); o.Note != "1 waiting" {
		t.Fatalf("waiting: %+v", o)
	}
}

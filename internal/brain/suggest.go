package brain

import (
	"fmt"
	"strings"
)

// Suggestion is one line of the list under an Ask request: something
// worth asking now, from what the agents are doing, or something asked
// before. A blank request box gave no hint of what conch can do. The TUI
// draws them, and `conch ask -suggest` hands them to the Mac app, so
// both offer the same things and say the same about what they cost.
type Suggestion struct {
	Text   string `json:"text"`           // the request, ending in "…" when it is to be finished
	Note   string `json:"note,omitempty"` // what it does or why it is offered
	Recent bool   `json:"recent,omitempty"`
}

// fill is an option that needs finishing — "tell codex to …" — and so is
// put in the box for the rest to be typed rather than asked as it is.
func (o Suggestion) Fill() bool { return strings.HasSuffix(o.Text, "…") }

// stem is what an option puts in the request box.
func (o Suggestion) Stem() string { return strings.TrimSuffix(o.Text, "…") }

// Counts are the agents on the world's online machines, by what they
// are doing.
type Counts struct {
	Agents  int `json:"agents"`
	Waiting int `json:"waiting"`
	Working int `json:"working"`
	Done    int `json:"done"`
}

func CountAgents(w World) Counts {
	var c Counts
	for _, m := range w.Machines {
		if !m.Online {
			continue
		}
		for _, p := range m.Panes {
			if p.Agent == "" {
				continue
			}
			c.Agents++
			switch p.State {
			case "blocked":
				c.Waiting++
			case "working":
				c.Working++
			case "done":
				c.Done++
			}
		}
	}
	return c
}

func (c Counts) String() string {
	if c.Agents == 0 {
		return "no agents running"
	}
	parts := []string{fmt.Sprintf("%d agent", c.Agents)}
	if c.Agents != 1 {
		parts[0] += "s"
	}
	for _, s := range []struct {
		n    int
		what string
	}{{c.Waiting, "waiting"}, {c.Working, "working"}, {c.Done, "done"}} {
		if s.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", s.n, s.what))
		}
	}
	return strings.Join(parts, " · ")
}

// maxRecent is how many past requests the list offers.
const MaxRecent = 4

// Suggest gives the suggestions for this moment, then the recent
// requests, newest first. here is the name of the agent selected, if
// one is; place says a project or branch is selected, which a vague
// "start an agent here" can rely on. fast is whether conch answers what
// it can without the model (no_fast_path turns it off), which decides
// what each one says it costs.
func Suggest(w World, here string, place, fast bool, history []string) []Suggestion {
	plain := func(req string) bool { _, ok := FastPlan(w, req); return ok }
	// cost says after what a request does whether the model is asked.
	cost := func(what, req string) string {
		switch {
		case fast && plain(req) && what == "":
			return "instant"
		case fast && plain(req):
			return what + " · instant"
		case what == "":
			return "asks the model"
		}
		return what
	}
	seen := map[string]bool{}
	var opts []Suggestion
	add := func(o Suggestion) {
		k := strings.ToLower(o.Stem())
		if seen[k] {
			return
		}
		seen[k] = true
		opts = append(opts, o)
	}
	c := CountAgents(w)
	const waiting = "what is waiting for me?"
	if c.Waiting > 0 {
		add(Suggestion{Text: waiting, Note: cost(fmt.Sprintf("%d waiting", c.Waiting), waiting)})
		// Up to three of them by name, each a click away — named only
		// when the name finds that one pane.
		n := 0
		for _, m := range w.Machines {
			for _, p := range m.Panes {
				if !m.Online || p.Agent == "" || p.State != "blocked" || p.Name == here || n == 3 {
					continue
				}
				if req := "show " + strings.ToLower(p.Name); plain(req) {
					add(Suggestion{Text: req, Note: cost("waiting for you", req)})
					n++
				}
			}
		}
	}
	if here != "" {
		name := strings.ToLower(here)
		add(Suggestion{Text: "tell " + name + " to …", Note: cost("type the message", "tell "+name+" to continue")})
	}
	if c.Agents > 1 {
		add(Suggestion{Text: "tell everyone to …", Note: cost(fmt.Sprintf("all %d agents", c.Agents), "tell everyone to continue")})
	}
	if place {
		add(Suggestion{Text: "start an agent here to …", Note: "a new branch · the model plans it"})
	}
	const working = "what is working?"
	if c.Working > 0 {
		add(Suggestion{Text: working, Note: cost(fmt.Sprintf("%d working", c.Working), working)})
	}
	if c.Waiting == 0 {
		add(Suggestion{Text: waiting, Note: cost("", waiting)})
	}
	if !place {
		if proj := firstProject(w); proj != "" {
			add(Suggestion{Text: "start an agent on " + proj + " to …", Note: "a new branch · the model plans it"})
		}
	}
	n := 0
	for i := len(history) - 1; i >= 0 && n < MaxRecent; i-- {
		h := strings.TrimSpace(history[i])
		if h == "" || seen[strings.ToLower(h)] {
			continue
		}
		add(Suggestion{Text: h, Note: cost("", h), Recent: true})
		n++
	}
	return opts
}

// firstProject names a project on an online machine, for an example that
// names something real.
func firstProject(w World) string {
	for _, m := range w.Machines {
		if m.Online && len(m.Projects) > 0 {
			return m.Projects[0].Name
		}
	}
	return ""
}

// Matching are the suggestions a typed request narrows the list to: those
// that contain it, other than the one it already is.
func Matching(opts []Suggestion, typed string) []Suggestion {
	t := strings.ToLower(strings.TrimSpace(typed))
	if t == "" {
		return opts
	}
	var out []Suggestion
	for _, o := range opts {
		s := strings.ToLower(strings.TrimSpace(o.Stem()))
		if s != t && strings.Contains(s, t) {
			out = append(out, o)
		}
	}
	return out
}

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
// requests, newest first. here is the name of the agent selected in the
// tree, if one is; place says a project or branch is selected, which a
// vague "start an agent here" can rely on.
func Suggest(w World, here string, place bool, history []string) []Suggestion {
	instant := func(req string) string {
		if _, ok := FastPlan(w, req); ok {
			return "instant"
		}
		return "asks the model"
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
	if c.Waiting > 0 {
		add(Suggestion{Text: "what is waiting for me?", Note: fmt.Sprintf("%d waiting · instant", c.Waiting)})
		// Up to three of them by name, each a key away.
		n := 0
		for _, m := range w.Machines {
			for _, p := range m.Panes {
				if !m.Online || p.Agent == "" || p.State != "blocked" || p.Name == here || n == 3 {
					continue
				}
				if req := "show " + strings.ToLower(p.Name); instant(req) == "instant" {
					add(Suggestion{Text: req, Note: "waiting for you"})
					n++
				}
			}
		}
	}
	if here != "" {
		name := strings.ToLower(here)
		note := "type the message"
		if instant("tell "+name+" to continue") == "instant" {
			note = "type the message · instant"
		}
		add(Suggestion{Text: "tell " + name + " to …", Note: note})
	}
	if c.Agents > 1 {
		add(Suggestion{Text: "tell everyone to …", Note: fmt.Sprintf("all %d agents · instant", c.Agents)})
	}
	if place {
		add(Suggestion{Text: "start an agent here to …", Note: "a new branch · the model plans it"})
	}
	if c.Working > 0 {
		add(Suggestion{Text: "what is working?", Note: fmt.Sprintf("%d working · instant", c.Working)})
	}
	if c.Waiting == 0 {
		add(Suggestion{Text: "what is waiting for me?", Note: "instant"})
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
		add(Suggestion{Text: h, Note: instant(h), Recent: true})
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

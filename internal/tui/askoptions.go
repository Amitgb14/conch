package tui

import (
	"fmt"
	"strings"

	"github.com/Amitgb14/conch/internal/brain"
)

// askOption is one line of the list under the Ask request: something
// worth asking now, from what the agents are doing, or something asked
// before. A blank request box gave no hint of what conch can do, and
// the history behind ↑ was invisible until pressed.
type askOption struct {
	text   string // the request, ending in "…" when it is to be finished
	note   string // what it does or why it is offered, said on the right
	recent bool
}

// fill is an option that needs finishing — "tell codex to …" — and so is
// put in the box for the rest to be typed rather than asked as it is.
func (o askOption) fill() bool { return strings.HasSuffix(o.text, "…") }

// stem is what an option puts in the request box.
func (o askOption) stem() string { return strings.TrimSuffix(o.text, "…") }

// askCounts are the agents the world holds, by what they are doing.
type askCounts struct{ agents, waiting, working, done int }

func countAgents(w brain.World) askCounts {
	var c askCounts
	for _, m := range w.Machines {
		if !m.Online {
			continue
		}
		for _, p := range m.Panes {
			if p.Agent == "" {
				continue
			}
			c.agents++
			switch p.State {
			case "blocked":
				c.waiting++
			case "working":
				c.working++
			case "done":
				c.done++
			}
		}
	}
	return c
}

func (c askCounts) String() string {
	if c.agents == 0 {
		return "no agents running"
	}
	parts := []string{count(c.agents, "agent")}
	for _, s := range []struct {
		n    int
		what string
	}{{c.waiting, "waiting"}, {c.working, "working"}, {c.done, "done"}} {
		if s.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", s.n, s.what))
		}
	}
	return strings.Join(parts, " · ")
}

// maxRecent is how many past requests the list offers.
const maxRecent = 4

// askOptions are the suggestions for this moment, then the recent
// requests, newest first. here is the name of the agent selected in the
// tree, if one is; place says a project or branch is selected, which a
// vague "start an agent here" can rely on.
func askOptions(w brain.World, here string, place bool, history []string) []askOption {
	instant := func(req string) string {
		if _, ok := brain.FastPlan(w, req); ok {
			return "instant"
		}
		return "asks the model"
	}
	seen := map[string]bool{}
	var opts []askOption
	add := func(o askOption) {
		k := strings.ToLower(o.stem())
		if seen[k] {
			return
		}
		seen[k] = true
		opts = append(opts, o)
	}
	c := countAgents(w)
	if c.waiting > 0 {
		add(askOption{text: "what is waiting for me?", note: fmt.Sprintf("%d waiting · instant", c.waiting)})
		// Up to three of them by name, each a key away.
		n := 0
		for _, m := range w.Machines {
			for _, p := range m.Panes {
				if !m.Online || p.Agent == "" || p.State != "blocked" || p.Name == here || n == 3 {
					continue
				}
				if req := "show " + strings.ToLower(p.Name); instant(req) == "instant" {
					add(askOption{text: req, note: "waiting for you"})
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
		add(askOption{text: "tell " + name + " to …", note: note})
	}
	if c.agents > 1 {
		add(askOption{text: "tell everyone to …", note: fmt.Sprintf("all %d agents · instant", c.agents)})
	}
	if place {
		add(askOption{text: "start an agent here to …", note: "a new branch · the model plans it"})
	}
	if c.working > 0 {
		add(askOption{text: "what is working?", note: fmt.Sprintf("%d working · instant", c.working)})
	}
	if c.waiting == 0 {
		add(askOption{text: "what is waiting for me?", note: "instant"})
	}
	if !place {
		if proj := firstProject(w); proj != "" {
			add(askOption{text: "start an agent on " + proj + " to …", note: "a new branch · the model plans it"})
		}
	}
	n := 0
	for i := len(history) - 1; i >= 0 && n < maxRecent; i-- {
		h := strings.TrimSpace(history[i])
		if h == "" || seen[strings.ToLower(h)] {
			continue
		}
		add(askOption{text: h, note: instant(h), recent: true})
		n++
	}
	return opts
}

// firstProject names a project on an online machine, for an example that
// names something real.
func firstProject(w brain.World) string {
	for _, m := range w.Machines {
		if m.Online && len(m.Projects) > 0 {
			return m.Projects[0].Name
		}
	}
	return ""
}

// matching are the options a typed request narrows the list to: those
// that contain it, other than the one it already is.
func matching(opts []askOption, typed string) []askOption {
	t := strings.ToLower(strings.TrimSpace(typed))
	if t == "" {
		return opts
	}
	var out []askOption
	for _, o := range opts {
		s := strings.ToLower(strings.TrimSpace(o.stem()))
		if s != t && strings.Contains(s, t) {
			out = append(out, o)
		}
	}
	return out
}

package brain

import (
	"fmt"
	"strings"
	"unicode"
)

// FastPlan answers the requests whose meaning is plain — show or close one
// pane, tell one agent or every agent something, what is waiting or
// working — from the world alone, without asking the model. A model call
// costs seconds (the Claude CLI starts a process for each), and these
// requests are most of what the bar is asked.
//
// It answers only when there is exactly one reading: a name that matches
// two panes, or a pane and a project, or a message that talks about the
// agent in the third person ("check its tests") is left to the model,
// which can ask or rewrite it. A wrong fast answer costs a rejection and
// a second request — more than the model call it saved — so ok false is
// the default and every match has to earn its way out of it.
func FastPlan(w World, request string) (Plan, bool) {
	req := normalize(request)
	if req == "" {
		return Plan{}, false
	}
	if p, ok := fastQuestion(w, req); ok {
		return p, true
	}
	for _, f := range []func(World, string) (Plan, bool){fastBroadcast, fastSend, fastFocusClose} {
		if p, ok := f(w, req); ok {
			p.Fast = true
			return p, true
		}
	}
	return Plan{}, false
}

// normalize lowercases a request, collapses its spaces and drops what
// politeness and punctuation add without changing what is asked.
func normalize(s string) string {
	s = strings.ToLower(strings.Join(strings.Fields(s), " "))
	s = strings.TrimRight(s, "?.! ")
	s = strings.TrimPrefix(s, "please ")
	s = strings.TrimSuffix(s, " please")
	s = strings.TrimSuffix(s, ",")
	return strings.TrimSpace(s)
}

var (
	waitingAsks = []string{"what is waiting", "what's waiting", "what is waiting for me", "what's waiting for me",
		"who is waiting", "who's waiting", "who is waiting for me", "which agents are waiting", "what needs me", "what needs my attention"}
	workingAsks = []string{"what is working", "what's working", "who is working", "which agents are working",
		"what is running", "what's running", "which agents are running"}
)

func fastQuestion(w World, req string) (Plan, bool) {
	state, verb := "", ""
	switch {
	case contains(waitingAsks, req):
		state, verb = "blocked", "waiting for you"
	case contains(workingAsks, req):
		state, verb = "working", "working"
	default:
		return Plan{}, false
	}
	var found []string
	for _, m := range w.Machines {
		if !m.Online {
			continue
		}
		for _, p := range m.Panes {
			if p.Agent == "" || p.State != state {
				continue
			}
			s := fmt.Sprintf("%s (%s on %s)", p.Name, p.Agent, m.Label)
			if p.Summary != "" {
				s += ": " + p.Summary
			}
			found = append(found, s)
		}
	}
	var reply string
	switch len(found) {
	case 0:
		reply = "No agent is " + verb + "."
	case 1:
		reply = "1 agent is " + verb + ": " + found[0] + "."
	default:
		reply = fmt.Sprintf("%d agents are %s: %s.", len(found), verb, strings.Join(found, "; "))
	}
	return Plan{Reply: reply, Actions: []Action{}, Fast: true}, true
}

// everyone is how a request names every agent.
var everyone = []string{"everyone", "everybody", "all agents", "all the agents", "every agent", "all"}

func fastBroadcast(w World, req string) (Plan, bool) {
	rest, ok := cutVerb(req, "tell ", "ask ")
	if !ok {
		return Plan{}, false
	}
	var text string
	for _, e := range everyone {
		if t, ok := strings.CutPrefix(rest, e+" to "); ok {
			text = t
			break
		}
	}
	text, ok = message(text)
	if !ok {
		return Plan{}, false
	}
	var plan Plan
	for _, m := range w.Machines {
		if !m.Online {
			continue
		}
		var ids []string
		for _, p := range m.Panes {
			if p.Agent != "" {
				ids = append(ids, p.ID)
			}
		}
		if len(ids) > 0 {
			plan.Actions = append(plan.Actions, Action{Type: ActBroadcast, Machine: m.ID, Panes: ids, Text: text})
		}
	}
	// No agent to tell is for the model to explain, with the world in view.
	return plan, len(plan.Actions) > 0
}

func fastSend(w World, req string) (Plan, bool) {
	rest, ok := cutVerb(req, "tell ", "ask ")
	if !ok {
		return Plan{}, false
	}
	// "tell claude to go to bed": the name ends at one of the " to "s, and
	// exactly one split may name exactly one agent.
	var hit *Action
	for i := 0; ; {
		j := strings.Index(rest[i:], " to ")
		if j < 0 {
			break
		}
		name, text := rest[:i+j], rest[i+j+len(" to "):]
		i += j + 1
		mid, pane, ok := w.only(name)
		if !ok {
			continue
		}
		if hit != nil {
			return Plan{}, false
		}
		hit = &Action{Type: ActSend, Machine: mid, Pane: pane.ID, Text: text}
		if pane.Agent == "" {
			return Plan{}, false // typed into a shell, a message runs as a command
		}
	}
	if hit == nil {
		return Plan{}, false
	}
	text, ok := message(hit.Text)
	if !ok {
		return Plan{}, false
	}
	hit.Text = text
	return Plan{Actions: []Action{*hit}}, true
}

func fastFocusClose(w World, req string) (Plan, bool) {
	typ := ActFocus
	name, ok := cutVerb(req, "show ", "focus ", "go to ", "switch to ", "open ")
	if !ok {
		if name, ok = cutVerb(req, "close ", "kill "); !ok {
			return Plan{}, false
		}
		typ = ActClose
	}
	mid, pane, ok := w.only(name)
	if !ok {
		return Plan{}, false
	}
	return Plan{Actions: []Action{{Type: typ, Machine: mid, Pane: pane.ID}}}, true
}

func cutVerb(s string, verbs ...string) (string, bool) {
	for _, v := range verbs {
		if rest, ok := strings.CutPrefix(s, v); ok && rest != "" {
			return rest, true
		}
	}
	return "", false
}

// thirdPerson are words that talk about the agent rather than to it
// ("tell codex to check its tests"). Sent as they are, the agent reads
// about somebody else; rewriting them is the model's job.
var thirdPerson = map[string]bool{"it": true, "its": true, "itself": true, "they": true, "them": true, "their": true,
	"themselves": true, "he": true, "she": true, "him": true, "her": true, "his": true, "hers": true}

// message turns what follows "to" into the message an agent is sent, or
// says it can't be sent as it stands.
func message(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", false
	}
	for _, word := range strings.FieldsFunc(text, func(r rune) bool { return !unicode.IsLetter(r) && r != '\'' }) {
		if thirdPerson[strings.TrimSuffix(word, "'s")] {
			return "", false
		}
	}
	r := []rune(text)
	r[0] = unicode.ToUpper(r[0])
	return string(r), true
}

// only finds the single pane a name stands for, on an online machine. A
// name that is also a project's or a machine's, or that two panes answer
// to, stands for nothing certain.
func (w World) only(name string) (string, Pane, bool) {
	name = strings.TrimPrefix(strings.TrimSpace(name), "the ")
	for _, suffix := range []string{" pane", " agent", " terminal", " tab"} {
		name = strings.TrimSuffix(name, suffix)
	}
	if name == "" {
		return "", Pane{}, false
	}
	var mid string
	var hit Pane
	n := 0
	for _, m := range w.Machines {
		if strings.EqualFold(m.ID, name) || strings.EqualFold(m.Label, name) {
			return "", Pane{}, false
		}
		for _, p := range m.Projects {
			if strings.EqualFold(p.Name, name) || strings.EqualFold(p.ID, name) {
				return "", Pane{}, false
			}
		}
		if !m.Online {
			continue
		}
		for _, p := range m.Panes {
			if p.answersTo(name) {
				mid, hit = m.ID, p
				n++
			}
		}
	}
	return mid, hit, n == 1
}

func (p Pane) answersTo(name string) bool {
	for _, alias := range []string{p.ID, p.Name, p.Branch, p.Agent} {
		// An agent's title may start with its own mark ("✳ Fix login").
		alias = strings.TrimSpace(strings.TrimLeftFunc(alias, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }))
		if alias != "" && strings.EqualFold(alias, name) {
			return true
		}
	}
	return false
}

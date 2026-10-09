package config

import (
	"strconv"
	"strings"
)

// Custom actions are the commands you put on conch's menus yourself. Most
// of what a plugin system is asked for is this: run the project's tests on
// this branch, open a pull request, tail a log, open the worktree in
// something else. Each one is a shell command somebody already runs by
// hand in that checkout, and conch's part is knowing *which* checkout —
// so it passes the machine, project, branch, worktree and pane in the
// environment and runs the command in a terminal of its own.
//
// Deliberately not a plugin API: no registry to host, nothing to version,
// no interface to keep stable for other people's code. An action is a line
// in your own config.toml and it only ever runs because you chose it from
// a menu.
//
//	[[actions]]
//	name = "Run tests"
//	run  = "go test ./..."
//	on   = ["branch", "project"]
type Action struct {
	// Name is the label on the menu, and how an action is named in the
	// terminal it runs in.
	Name string `toml:"name"`
	// Run is the command, as a shell reads it: a pipeline, several
	// commands, a script of your own — whatever `sh -lc` takes.
	Run string `toml:"run"`
	// On is the kinds of row the action is offered on: pane, branch,
	// project, machine. Empty is all four, since most actions make sense
	// wherever there is a directory to run them in.
	On []string `toml:"on,omitempty"`
	// Keep leaves the terminal open when the command exits, so what it
	// printed can still be read — which is what a command that takes a
	// second and then disappears would lose. Unset is true; `keep = false`
	// closes it like an ordinary terminal, for an action whose output is
	// not the point.
	Keep *bool `toml:"keep,omitempty"`
}

// Where an action can be offered. These are the words in `on`, and the
// kinds of row in the tree that have a place to run a command.
const (
	ActionOnPane    = "pane"
	ActionOnBranch  = "branch"
	ActionOnProject = "project"
	ActionOnMachine = "machine"
)

// ActionPlaces is every value `on` takes, in menu order.
var ActionPlaces = []string{ActionOnPane, ActionOnBranch, ActionOnProject, ActionOnMachine}

// Keeps reports whether the terminal stays open after the command exits.
func (a Action) Keeps() bool { return a.Keep == nil || *a.Keep }

// Named reports whether the action has both halves it needs: something to
// put on the menu and something to run.
func (a Action) Named() bool {
	return strings.TrimSpace(a.Name) != "" && strings.TrimSpace(a.Run) != ""
}

// Offers reports whether the action belongs on a row of this kind.
func (a Action) Offers(place string) bool {
	if len(a.On) == 0 {
		return true
	}
	for _, p := range a.On {
		if strings.EqualFold(strings.TrimSpace(p), place) {
			return true
		}
	}
	return false
}

// ActionsOn is the actions offered on a kind of row, in the order they
// were written: a menu that reorders itself between two conch runs is
// worse than one in an order somebody chose. Half-written ones are left
// out — ActionProblems says why.
func (c Config) ActionsOn(place string) []Action {
	var out []Action
	for _, a := range c.Actions {
		if a.Named() && a.Offers(place) {
			out = append(out, a)
		}
	}
	return out
}

// ActionProblems names what is wrong with the actions in config.toml, so
// an action that never appears on a menu can say why rather than being
// quietly dropped. The settings screen shows them.
func (c Config) ActionProblems() []string {
	var out []string
	seen := map[string]bool{}
	for i, a := range c.Actions {
		name, run := strings.TrimSpace(a.Name), strings.TrimSpace(a.Run)
		switch {
		case name == "" && run == "":
			out = append(out, ordinal(i)+" has neither a name nor a command")
			continue
		case name == "":
			out = append(out, ordinal(i)+" has no name: "+run)
			continue
		case run == "":
			out = append(out, name+" has no command to run")
			continue
		}
		if seen[strings.ToLower(name)] {
			out = append(out, "two actions are called "+name)
		}
		seen[strings.ToLower(name)] = true
		for _, p := range a.On {
			if !known(strings.TrimSpace(p)) {
				out = append(out, name+" is offered on "+strings.TrimSpace(p)+", which is not one of "+strings.Join(ActionPlaces, ", "))
			}
		}
	}
	return out
}

// known reports whether p is one of the places an action can be offered.
func known(p string) bool {
	for _, k := range ActionPlaces {
		if strings.EqualFold(p, k) {
			return true
		}
	}
	return false
}

// ordinal names an action by its place in the file, which is all there is
// to call one that has no name.
func ordinal(i int) string {
	switch i {
	case 0:
		return "the first action"
	case 1:
		return "the second action"
	case 2:
		return "the third action"
	}
	return "action " + strconv.Itoa(i+1)
}

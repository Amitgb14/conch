package config

import (
	"strings"
)

// Team is a group of agents: a name and a colour, and rules that put an
// agent in it when nobody has — a project it works in, or a branch
// pattern. An agent can also be put in a team by hand (pane.team), which
// wins over every rule. Conch.app reads the same rules
// (ConchKit/Teams.swift), so both say the same team for an agent.
type Team struct {
	Name string `toml:"name"`
	// Color is one of TeamColors, or "#rrggbb"; "" takes one by the
	// team's place in the list.
	Color string `toml:"color,omitempty"`
	// Projects are project names whose agents join the team.
	Projects []string `toml:"projects,omitempty"`
	// Branches are patterns, * matching anything, for the branch an agent
	// works on: "ui/*", "*-redesign".
	Branches []string `toml:"branches,omitempty"`
}

// TeamColors are the colour names a team may use.
var TeamColors = []string{"blue", "orange", "purple", "teal", "green", "red", "pink", "yellow", "gray"}

// NoTeam set on a pane by hand keeps it out of every team, whatever the
// rules say.
const NoTeam = "-"

// TeamFor is the team an agent is in: the one it was put in by hand
// (set), else the first team whose rule matches its project or branch.
// "" is none. A team set by hand that no longer exists is still its team:
// the name is what was chosen, and the tree shows it.
func TeamFor(teams []Team, set, project, branch string) string {
	switch set {
	case NoTeam:
		return ""
	case "":
	default:
		return set
	}
	for _, t := range teams {
		if t.Name == "" {
			continue
		}
		for _, p := range t.Projects {
			if project != "" && strings.EqualFold(p, project) {
				return t.Name
			}
		}
		for _, b := range t.Branches {
			if branch != "" && globMatch(b, branch) {
				return t.Name
			}
		}
	}
	return ""
}

// globMatch is a branch pattern: * matches any run of characters, slashes
// included (a branch's slashes are part of its name, not directories);
// anything else is itself.
func globMatch(pattern, s string) bool {
	if !strings.Contains(pattern, "*") {
		return pattern == s
	}
	parts := strings.Split(pattern, "*")
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	last := parts[len(parts)-1]
	for _, p := range parts[1 : len(parts)-1] {
		i := strings.Index(s, p)
		if i < 0 {
			return false
		}
		s = s[i+len(p):]
	}
	return strings.HasSuffix(s, last) && len(s) >= len(last)
}

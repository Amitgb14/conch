package accounts

import "sort"

// Cand is one of an agent's accounts as a move weighs it.
type Cand struct {
	// Name is "" for the agent's own.
	Name string
	// Room is what is left of its fullest live window, 0–100; Reported
	// false means it has said nothing yet.
	Room     float64
	Reported bool
	// Signed is whether it is signed in, nil when nobody can tell.
	Signed *bool
	// Agents is how many agents run on it now.
	Agents int
	// Order is its place in the order added, the agent's own first.
	Order int
}

// Usable is whether a move to it would go: not known to be signed out,
// and with room (an account that said nothing counts as room).
func (c Cand) Usable() bool {
	if c.Signed != nil && !*c.Signed {
		return false
	}
	return !c.Reported || c.Room > 0
}

// tier is how good a choice it is, lower first: room under the failover
// line, then nothing reported, then room past the line, then unusable.
func (c Cand) tier(line int) int {
	switch {
	case !c.Usable():
		return 3
	case !c.Reported:
		return 1
	case line > 0 && 100-c.Room >= float64(line):
		return 2
	default:
		return 0
	}
}

// Rank orders an agent's accounts for a move, the best first: by tier,
// then the most room, then the fewest agents (to spread them), then the
// order added. line is the agent's failover line, 0 for off.
func Rank(cs []Cand, line int) []Cand {
	out := append([]Cand(nil), cs...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if ta, tb := a.tier(line), b.tier(line); ta != tb {
			return ta < tb
		}
		if a.Reported && b.Reported && a.Room != b.Room {
			return a.Room > b.Room
		}
		if a.Agents != b.Agents {
			return a.Agents < b.Agents
		}
		return a.Order < b.Order
	})
	return out
}

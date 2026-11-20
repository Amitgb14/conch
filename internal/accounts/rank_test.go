package accounts

import "testing"

func names(cs []Cand) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Name
		if out[i] == "" {
			out[i] = "default"
		}
	}
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestRank: room under the line first, most room first; then an account
// that said nothing; then room past the line; never one signed out or full.
func TestRank(t *testing.T) {
	yes, no := true, false
	cs := []Cand{
		{Name: "", Room: 0, Reported: true, Agents: 17, Order: 0},        // full
		{Name: "backup", Room: 41, Reported: true, Agents: 1, Order: 1},  // 59% used: past a 50 line
		{Name: "backup2", Room: 96, Reported: true, Agents: 3, Order: 2}, // the most room
		{Name: "fresh", Signed: &yes, Order: 3},                          // nothing reported
		{Name: "work", Room: 90, Reported: true, Signed: &no, Order: 4},  // signed out
		{Name: "spare", Room: 96, Reported: true, Agents: 0, Order: 5},   // as much room, fewer agents
	}
	got := names(Rank(cs, 50))
	want := []string{"spare", "backup2", "fresh", "backup", "work", "default"}
	if !eq(got, want) {
		t.Errorf("rank at 50: %v, want %v", got, want)
	}
	// Failover off: past-the-line is no tier of its own.
	if got := names(Rank(cs, 0)); !eq(got, []string{"spare", "backup2", "backup", "fresh", "work", "default"}) {
		t.Errorf("rank with failover off: %v", got)
	}
	for _, c := range cs {
		if want := c.Name != "" && c.Name != "work"; c.Usable() != want {
			t.Errorf("%q usable %v", c.Name, c.Usable())
		}
	}
	// Ties all the way: the order added.
	tie := []Cand{{Name: "b", Room: 50, Reported: true, Order: 2}, {Name: "a", Room: 50, Reported: true, Order: 1}}
	if got := names(Rank(tie, 80)); !eq(got, []string{"a", "b"}) {
		t.Errorf("a tie: %v", got)
	}
	if len(Rank(nil, 80)) != 0 {
		t.Error("nothing to rank")
	}
	// The input is left as it was.
	if cs[0].Name != "" || cs[5].Name != "spare" {
		t.Error("Rank reordered its input")
	}
}

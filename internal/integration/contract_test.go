package integration

import (
	"strings"
	"testing"
)

func TestContract(t *testing.T) {
	c, ok := Contract(KindCI)
	if !ok || strings.Join(c.Required, ",") != "runs" || strings.Join(c.Optional, ",") != "failed_log,rerun,stats" ||
		strings.Join(c.States, ",") != "pass,fail,pending,canceled" {
		t.Fatalf("ci: %+v %v", c, ok)
	}
	// What the agent adapting a file is told: every operation, and what
	// each is for.
	for _, want := range []string{"[runs] (required)", "[failed_log]", "[rerun]", "[stats]", "pass, fail, pending, canceled"} {
		if !strings.Contains(c.Text, want) {
			t.Errorf("no %q in\n%s", want, c.Text)
		}
	}
	// A copy: changing it does not change the contract.
	c.Required[0] = "x"
	if again, _ := Contract(KindCI); again.Required[0] != "runs" {
		t.Fatal("the contract was changed through its copy")
	}
	for _, k := range []string{"", KindTickets, KindNotify, KindHost, KindSandbox, "nope"} {
		if _, ok := Contract(k); ok {
			t.Errorf("%q has a contract in this conch", k)
		}
	}
}

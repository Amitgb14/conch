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
	// Tickets and notifications have contracts of their own since T12, each
	// operation said in words for the agent adapting one.
	for kind, want := range map[string][]string{KindTickets: {"[list] (required)", "[get] (required)", "[mine]", "[comment]", "[link]", "[move]"}, KindNotify: {"[send] (required)"}} {
		c, ok := Contract(kind)
		if !ok {
			t.Fatalf("%s: no contract", kind)
		}
		for _, w := range want {
			if !strings.Contains(c.Text, w) {
				t.Errorf("%s: no %q in\n%s", kind, w, c.Text)
			}
		}
		if strings.Contains(c.Text, "]: \n") || strings.Contains(c.Text, "(required): \n") {
			t.Errorf("%s: an operation with no words:\n%s", kind, c.Text)
		}
	}
	for _, k := range []string{"", KindHost, KindSandbox, "nope"} {
		if _, ok := Contract(k); ok {
			t.Errorf("%q has a contract in this conch", k)
		}
	}
}

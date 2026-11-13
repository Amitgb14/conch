package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/tracker"
)

func TestTicketPrompt(t *testing.T) {
	tk := tracker.Ticket{Tracker: "linear", Key: "ENG-7", Title: " Login fails ", Body: "Steps:\n1. open",
		URL: "https://linear.app/a/issue/ENG-7", Comments: []tracker.Comment{{Author: "Ana", Body: "Seen on 17.2"}, {Body: " "}, {Body: "no author"}}}
	p := ticketPrompt(tk, " keep the old API ")
	for _, want := range []string{
		"Work on Linear ticket ENG-7: Login fails\nhttps://linear.app/a/issue/ENG-7\n",
		"Steps:\n1. open", "Comments:\n- Ana: Seen on 17.2\n- no author\n", "Also: keep the old API",
		"a pull request for it should name ENG-7",
	} {
		if !strings.Contains(p, want) {
			t.Fatalf("prompt lacks %q:\n%s", want, p)
		}
	}
	// Nothing but a title.
	p = ticketPrompt(tracker.Ticket{Tracker: "jira", Key: "P-1", Title: "Typo"}, "")
	if strings.Contains(p, "Also:") || strings.Contains(p, "Comments:") || !strings.HasPrefix(p, "Work on Jira ticket P-1: Typo\n") {
		t.Fatalf("bare:\n%s", p)
	}
	// A huge body and a flood of comments are cut, at valid text.
	huge := strings.Repeat("é", maxIssueBody)
	var many []tracker.Comment
	for range 200 {
		many = append(many, tracker.Comment{Body: strings.Repeat("ü", 500)})
	}
	p = ticketPrompt(tracker.Ticket{Tracker: "jira", Key: "P-2", Title: "Crash", Body: huge, Comments: many}, "")
	if len(p) > 2*maxIssueBody+600 || !strings.Contains(p, "the rest of the ticket is in Jira") || !strings.Contains(p, "P-2") {
		t.Fatalf("huge: %d bytes", len(p))
	}
	if !strings.Contains(p, "more comments are in Jira") {
		t.Fatalf("comments not cut:\n%s", p[len(p)-300:])
	}
	for _, r := range p {
		if r == '\uFFFD' {
			t.Fatal("cut mid-character")
		}
	}
}

// A tracker's key is read from the environment, then the Keychain — which
// is where Conch.app keeps one for a server it started — and not at all
// where there is no Keychain.
func TestTrackerSecret(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "security")
	os.WriteFile(bin, []byte(`#!/bin/sh
if [ "$1" = find-generic-password ] && [ "$3" = conch-mcp ] && [ "$5" = LINEAR_API_KEY ]; then echo 'kc-key'; exit 0; fi
exit 44
`), 0o755)
	t.Setenv("CONCH_SECURITY", bin)
	t.Setenv("LINEAR_API_KEY", "")
	s := &Server{}
	if got := s.trackerSecret("LINEAR_API_KEY"); got != "kc-key" {
		t.Errorf("from the Keychain: %q", got)
	}
	if got := s.trackerSecret("JIRA_API_TOKEN"); got != "" {
		t.Errorf("an item the Keychain lacks: %q", got)
	}
	t.Setenv("LINEAR_API_KEY", "env-key")
	if got := s.trackerSecret("LINEAR_API_KEY"); got != "env-key" {
		t.Errorf("the environment first: %q", got)
	}
	was := keychainHas
	keychainHas = func() bool { return false }
	t.Cleanup(func() { keychainHas = was })
	t.Setenv("LINEAR_API_KEY", "")
	s = &Server{}
	if got := s.trackerSecret("LINEAR_API_KEY"); got != "" {
		t.Errorf("no Keychain here: %q", got)
	}
}

package server

import (
	"fmt"
	"strings"
	"testing"
)

// A burst of dropped paths — a large node_modules rewritten — is one line
// in the log, not one per path (#199).
func TestDropLogOneLinePerBurst(t *testing.T) {
	var lines []string
	logf := func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }
	var d dropLog
	d.report(logf)
	if len(lines) != 0 {
		t.Fatalf("nothing dropped, yet %q", lines)
	}
	d.add("/w/node_modules/a")
	d.report(logf)
	if len(lines) != 1 || !strings.Contains(lines[0], "dropped an event for /w/node_modules/a") {
		t.Fatalf("one: %q", lines)
	}
	lines = nil
	for i := range 5000 {
		d.add(fmt.Sprintf("/w/node_modules/p%d", i))
	}
	d.report(logf)
	d.report(logf) // reported once
	if len(lines) != 1 || !strings.Contains(lines[0], "dropped 5000 events") || !strings.Contains(lines[0], "/w/node_modules/p0") {
		t.Fatalf("burst: %q", lines)
	}
}

// The backend in use reports its drops when the watcher asks what it
// missed, so the line comes once per tick.
func TestBackendReportsDropsWhenAsked(t *testing.T) {
	be, err := newBackend()
	if err != nil {
		t.Skip("no watch backend here:", err)
	}
	defer be.close()
	var lines []string
	old := watchLogf
	watchLogf = func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }
	t.Cleanup(func() { watchLogf = old })
	d := be.(interface{ noteDropped(string) })
	for i := range 300 {
		d.noteDropped(fmt.Sprintf("/w/f%d", i))
	}
	if len(lines) != 0 {
		t.Fatalf("logged as dropped: %d lines", len(lines))
	}
	if !be.missed() {
		t.Fatal("drops not owned up to")
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "dropped 300 events") {
		t.Fatalf("lines %q", lines)
	}
	if be.missed() || len(lines) != 1 {
		t.Fatalf("reported twice: %q", lines)
	}
}

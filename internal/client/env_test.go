package client

import (
	"slices"
	"testing"
)

// A server started from conch's own window must not hand CONCH_APP_WINDOW
// to its panes: a conch run in one would retitle that window.
func TestWithoutVar(t *testing.T) {
	got := withoutVar([]string{"CONCH_APP_WINDOW=1", "PATH=/bin", "CONCH_APPLE=x", "CONCH_APP_WINDOW="}, "CONCH_APP_WINDOW")
	if !slices.Equal(got, []string{"PATH=/bin", "CONCH_APPLE=x"}) {
		t.Fatalf("%q", got)
	}
	if got := withoutVar(nil, "CONCH_APP_WINDOW"); len(got) != 0 {
		t.Fatalf("%q", got)
	}
}

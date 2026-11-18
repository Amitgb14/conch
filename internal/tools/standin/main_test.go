package main

import "testing"

func TestBanner(t *testing.T) {
	if got, want := banner("claude", nil), "stand-in for claude · not an agent; nothing is sent to any model\n"; got != want {
		t.Errorf("no arguments: %q, want %q", got, want)
	}
	got := banner("claude", []string{"--resume", "f7a2"})
	if want := "stand-in for claude · not an agent; nothing is sent to any model\nstarted with: --resume f7a2\n"; got != want {
		t.Errorf("with arguments: %q, want %q", got, want)
	}
}

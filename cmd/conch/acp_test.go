package main

import (
	"strings"
	"testing"
)

func TestParseACP(t *testing.T) {
	f, err := parseACP([]string{"-agent", "speaker", "-label", "Speaker", "-prompt", "-starts with a dash", "-load", "s-1", "--", "LANG=C", "X_1=y", "speaker", "--acp", "--", "more"})
	if err != nil {
		t.Fatal(err)
	}
	if f.agent != "speaker" || f.label != "Speaker" || f.prompt != "-starts with a dash" || f.load != "s-1" {
		t.Errorf("flags = %+v", f)
	}
	if strings.Join(f.env, " ") != "LANG=C X_1=y" || strings.Join(f.command, " ") != "speaker --acp -- more" {
		t.Errorf("env %q command %q", f.env, f.command)
	}
	for _, bad := range [][]string{
		{},                                   // no --
		{"-agent", "x"},                      // no --
		{"--"},                               // nothing after it
		{"--", "A=b"},                        // only an assignment
		{"stray", "--", "speaker"},           // a word that is no flag
		{"-nope", "--", "speaker"},           // a flag that is not conch acp's
		{"-load", "--yolo", "--", "speaker"}, // a session id that is an option
	} {
		if _, err := parseACP(bad); err == nil {
			t.Errorf("parseACP(%q) took it", bad)
		}
	}
}

func TestAssignment(t *testing.T) {
	for w, want := range map[string]bool{"A=b": true, "_x1=": true, "1A=b": false, "=b": false, "a-b=c": false, "plain": false, "--flag=x": false} {
		if assignment(w) != want {
			t.Errorf("assignment(%q) = %v", w, !want)
		}
	}
}

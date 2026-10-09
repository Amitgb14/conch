package pane

import (
	"strings"
	"testing"
)

func TestTitleScanner(t *testing.T) {
	for _, tc := range []struct {
		chunks []string
		want   string
		ok     bool
	}{
		{[]string{"\x1b]0;✳ Claude Code\x07"}, "✳ Claude Code", true},
		{[]string{"\x1b]2;a;b\x1b\\"}, "a;b", true},
		{[]string{"text \x1b]0;sp", "lit ✳\xe2", "\x9c\xb3 x\x07 more"}, "split ✳✳ x", true},
		{[]string{"\x1b]7;file://host/dir\x07"}, "", false}, // cwd report, not a title
		{[]string{"\x1b]0;first\x07\x1b]0;second\x07"}, "second", true},
		{[]string{"\x1b[31mred\x1b[0m"}, "", false},
	} {
		var s titleScanner
		got, ok := "", false
		for _, c := range tc.chunks {
			if t, k := s.scan([]byte(c)); k {
				got, ok = t, true
			}
		}
		if got != tc.want || ok != tc.ok {
			t.Errorf("%q: got %q,%v want %q,%v", tc.chunks, got, ok, tc.want, tc.ok)
		}
	}
}

// The scanner reads what the emulator cannot be trusted with, and the
// reads it gets are whatever the kernel handed over: a sequence split
// across two of them is still one sequence.
func TestOSCScanner(t *testing.T) {
	for _, c := range []struct {
		name   string
		chunks []string
		want   []oscSeq
	}{
		{"BEL ends one", []string{"\x1b]0;title\x07"}, []oscSeq{{"0", "title"}}},
		{"ST ends one", []string{"\x1b]7501;state=idle\x1b\\"}, []oscSeq{{"7501", "state=idle"}}},
		{"several in one read", []string{"a\x1b]2;x\x07b\x1b]7501;state=working\x1b\\c"},
			[]oscSeq{{"2", "x"}, {"7501", "state=working"}}},
		{"split between reads", []string{"\x1b]75", "01;sta", "te=done\x1b", "\\"}, []oscSeq{{"7501", "state=done"}}},
		{"split inside the terminator", []string{"\x1b]7501;state=done\x1b", "\\"}, []oscSeq{{"7501", "state=done"}}},
		{"an aborted sequence", []string{"\x1b]7501;state=done\x1bX"}, nil},
		{"a five-digit number is still read", []string{"\x1b]99999;x\x07"}, []oscSeq{{"99999", "x"}}},
		{"a number too long is not", []string{"\x1b]123456;x\x07"}, nil},
		{"no payload separator", []string{"\x1b]7501\x07"}, nil},
		{"an empty payload", []string{"\x1b]7501;\x07"}, []oscSeq{{"7501", ""}}},
		{"ordinary text", []string{"hello \x1b[31mred\x1b[0m"}, nil},
	} {
		var s oscScanner
		var got []oscSeq
		for _, chunk := range c.chunks {
			got = append(got, s.scan([]byte(chunk))...)
		}
		if len(got) != len(c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: got %v, want %v", c.name, got, c.want)
				break
			}
		}
	}
	// A program that writes megabytes into one OSC must not grow the
	// buffer with it; what is kept is capped and the sequence still ends.
	var s oscScanner
	long := "\x1b]7501;" + strings.Repeat("x", maxOSC*3) + "\x07"
	got := s.scan([]byte(long))
	if len(got) != 1 || len(got[0].data) != maxOSC {
		t.Errorf("an overlong payload: %d sequences, %d bytes kept", len(got), len(got[0].data))
	}
	// And the scanner is ready for the next one.
	if next := s.scan([]byte("\x1b]0;after\x07")); len(next) != 1 || next[0].data != "after" {
		t.Errorf("after an overlong payload: %v", next)
	}
}

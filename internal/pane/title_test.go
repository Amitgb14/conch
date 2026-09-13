package pane

import "testing"

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

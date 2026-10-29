package doctor

import (
	"strings"
	"testing"
)

func TestText(t *testing.T) {
	ok := Check{Name: "a", Status: OK, Detail: "fine"}
	warn := Check{Name: "b", Status: Warn, Detail: "meh", Fix: "do b"}
	fail := Check{Name: "c", Status: Fail, Detail: "broken", Fix: "do c"}
	for _, tc := range []struct {
		checks []Check
		end    string
	}{
		{nil, "\nAll good.\n"},
		{[]Check{ok}, "✓ a         fine\n\nAll good.\n"},
		{[]Check{ok, warn}, "! b         meh\n            → do b\n\nconch works; 1 thing worth a look.\n"},
		{[]Check{fail}, "✗ c         broken\n            → do c\n\n1 thing to fix.\n"},
		{[]Check{fail, fail}, "\n2 things to fix.\n"},
		{[]Check{fail, warn, warn}, "\n1 thing to fix, 2 more worth a look.\n"},
	} {
		if got := Text(tc.checks); !strings.HasSuffix(got, tc.end) {
			t.Errorf("%v:\n%q\nwant it to end %q", tc.checks, got, tc.end)
		}
	}
}

func TestCount(t *testing.T) {
	checks := []Check{{Status: OK}, {Status: Warn}, {Status: Fail}, {Status: Warn}, {}}
	if w, f := Count(checks); w != 2 || f != 1 {
		t.Errorf("Count = %d warns, %d fails; want 2, 1", w, f)
	}
	if w, f := Count(nil); w != 0 || f != 0 {
		t.Errorf("Count(nil) = %d, %d", w, f)
	}
	// An outcome nobody knows (a newer conch's) is not counted as trouble.
	if got := (Check{Status: "later"}).Mark(); got != "✓" {
		t.Errorf("an unknown status is marked %q", got)
	}
}

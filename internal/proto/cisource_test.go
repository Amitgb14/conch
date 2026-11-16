package proto

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestCISourceCapabilities(t *testing.T) {
	for _, c := range []string{CapCISources, CapPRCheckSource, CapCIRerun} {
		if !slices.Contains(Capabilities, c) {
			t.Errorf("%s not announced", c)
		}
	}
}

// The new fields are left out when empty, so an older client sees exactly
// what it saw before.
func TestCISourceFieldsOmitted(t *testing.T) {
	b, _ := json.Marshal(BranchCILog{Log: "x"})
	if string(b) != `{"log":"x"}` {
		t.Fatalf("log: %s", b)
	}
	b, _ = json.Marshal(PRCheck{Name: "n", State: "fail"})
	if string(b) != `{"name":"n","state":"fail"}` {
		t.Fatalf("check: %s", b)
	}
	var old BranchCILog
	if err := json.Unmarshal([]byte(`{"workflow":"ci","url":"u","log":"l"}`), &old); err != nil || old.Source != "" {
		t.Fatalf("an older server's reply: %+v %v", old, err)
	}
}

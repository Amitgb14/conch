package proto

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestCISourceCapabilities(t *testing.T) {
	for _, c := range []string{CapCISources, CapPRCheckSource, CapCIRerun, CapIntegrationDetect} {
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

// A note about a failed run says whose it was, as the feed reads it:
// "CI failed on B — CircleCI · workflow W · job J", and how much of what
// was read, from where.
func TestNoteCIHeadline(t *testing.T) {
	full := &NoteCI{Source: "circleci", Service: "CircleCI", Workflow: "build-and-test", Job: "unittest-rgw", Lines: 212, Host: "circleci.com"}
	for _, tc := range []struct {
		ci         *NoteCI
		head, read string
	}{
		{full, "CI failed on rgw-lc-fix — CircleCI · workflow build-and-test · job unittest-rgw", "read 212 lines of the failed log from circleci.com"},
		{&NoteCI{Source: "github-actions", Service: "GitHub Actions", Workflow: "ci", Lines: 1}, "CI failed on rgw-lc-fix — GitHub Actions · workflow ci", "read 1 line of the failed log"},
		{&NoteCI{Source: "jenkins"}, "CI failed on rgw-lc-fix — jenkins", ""},
		{&NoteCI{Job: "test"}, "CI failed on rgw-lc-fix — job test", ""},
		{&NoteCI{}, "CI failed on rgw-lc-fix", ""},
		{nil, "CI failed on rgw-lc-fix", ""},
	} {
		if got := tc.ci.Headline("rgw-lc-fix"); got != tc.head {
			t.Errorf("headline %+v = %q, want %q", tc.ci, got, tc.head)
		}
		if got := tc.ci.Read(); got != tc.read {
			t.Errorf("read %+v = %q, want %q", tc.ci, got, tc.read)
		}
	}
	// FromLog: the source, the job and the size of what was read.
	ci := NoteFromLog(BranchCILog{Source: "circleci", Service: "CircleCI", Workflow: "w", Job: "j",
		URL: "https://app.circleci.com/pipelines/x", Log: "a\nb\nc\n"})
	if *ci != (NoteCI{Source: "circleci", Service: "CircleCI", Workflow: "w", Job: "j", URL: "https://app.circleci.com/pipelines/x", Lines: 3, Host: "app.circleci.com"}) {
		t.Fatalf("from a log: %+v", ci)
	}
	if ci := NoteFromLog(BranchCILog{Log: "one line, no newline"}); ci.Lines != 1 || ci.Host != "" {
		t.Fatalf("one line: %+v", ci)
	}
	if ci := NoteFromLog(BranchCILog{URL: "::bad"}); ci.Lines != 0 || ci.Host != "" {
		t.Fatalf("empty: %+v", ci)
	}
}

// The note's CI is left out when it is not about CI, and an older
// server's note, without it, still reads.
func TestNoteCIOmitted(t *testing.T) {
	if !slices.Contains(Capabilities, CapAutopilotNoteCI) {
		t.Fatal("autopilot note CI not announced")
	}
	b, _ := json.Marshal(AutopilotNote{ProjectID: "p", Branch: "b", Text: "t"})
	if strings.Contains(string(b), `"ci"`) {
		t.Fatalf("note: %s", b)
	}
	var n AutopilotNote
	if err := json.Unmarshal([]byte(`{"project_id":"p","branch":"b","text":"t","ci":{"source":"circleci","job":"j","lines":3}}`), &n); err != nil ||
		n.CI == nil || n.CI.Source != "circleci" || n.CI.Lines != 3 {
		t.Fatalf("round trip: %+v %v", n, err)
	}
}

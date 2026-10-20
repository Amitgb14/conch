package proto

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFailureTail(t *testing.T) {
	if got := FailureTail(nil); got != "" {
		t.Fatalf("nothing: %q", got)
	}
	if got := FailureTail([]string{"", "  ", ""}); got != "" {
		t.Fatalf("blank lines: %q", got)
	}
	// Escape codes go, trailing blanks go, the order stays.
	got := FailureTail([]string{"\x1b[31mFAIL\x1b[0m   ", "ok", "", ""})
	if got != "FAIL\nok" {
		t.Fatalf("plain: %q", got)
	}
	// Only the end of a long run.
	var many []string
	for i := range 500 {
		many = append(many, strings.Repeat("x", 10)+string(rune('a'+i%26)))
	}
	got = FailureTail(many)
	if lines := strings.Split(got, "\n"); len(lines) != failureTailLines || lines[len(lines)-1] != many[499] {
		t.Fatalf("%d lines, last %q", len(lines), lines[len(lines)-1])
	}
	// Wide lines stop at the byte cap, cut at a line.
	wide := []string{strings.Repeat("a", 4000), strings.Repeat("b", 4000), "end"}
	if got := FailureTail(wide); got != strings.Repeat("b", 4000)+"\nend" {
		t.Fatalf("byte cap kept %d bytes", len(got))
	}
	// One line bigger than the cap keeps its end, and stays valid text.
	huge := strings.Repeat("é", failureTailBytes) // two bytes each
	got = FailureTail([]string{huge})
	if len(got) > failureTailBytes || !strings.HasSuffix(huge, got) || !json.Valid([]byte(`"`+got+`"`)) {
		t.Fatalf("one huge line: %d bytes", len(got))
	}
}

func TestFailurePrompt(t *testing.T) {
	p := FailurePrompt("The project's check", "go test ./...", 2, "FAIL x")
	for _, want := range []string{"`go test ./...`, exit 2", "```\nFAIL x\n```", "run it again"} {
		if !strings.Contains(p, want) {
			t.Fatalf("prompt lacks %q:\n%s", want, p)
		}
	}
	if p := FailurePrompt("CI", "", 0, ""); !strings.HasPrefix(p, "CI failed on this branch. It printed nothing.") {
		t.Fatalf("bare: %s", p)
	}
}

func TestCIFailurePrompt(t *testing.T) {
	p := CIFailurePrompt(BranchCILog{Workflow: "ci", URL: "https://x/runs/1", Log: "step\nFAIL y\n\n"})
	for _, want := range []string{"CI (ci) failed", "```\nstep\nFAIL y\n```", "The run: https://x/runs/1"} {
		if !strings.Contains(p, want) {
			t.Fatalf("prompt lacks %q:\n%s", want, p)
		}
	}
	if p := CIFailurePrompt(BranchCILog{}); !strings.HasPrefix(p, "CI failed on this branch. It printed nothing.") || strings.Contains(p, "The run") {
		t.Fatalf("bare: %s", p)
	}
}

// The server and the TUI compare CI by this one function: an open pull
// request's checks, and nothing for any other.
func TestPRChecks(t *testing.T) {
	for _, tc := range []struct {
		pr   *PRInfo
		want string
	}{
		{nil, ""},
		{&PRInfo{State: "OPEN", Checks: "fail"}, "fail"},
		{&PRInfo{State: "OPEN"}, ""},
		{&PRInfo{State: "MERGED", Checks: "pass"}, ""},
		{&PRInfo{State: "CLOSED", Checks: "fail"}, ""},
	} {
		if got := PRChecks(BranchInfo{Name: "x", PR: tc.pr}); got != tc.want {
			t.Fatalf("%+v: %q, want %q", tc.pr, got, tc.want)
		}
	}
	if !strings.HasPrefix(CheckPrefix+"feat", "check · ") {
		t.Fatal("the check terminal's name changed: older TUIs adopt the server's checks by it")
	}
}

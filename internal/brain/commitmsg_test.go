package brain

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type fakeProvider struct {
	res Response
	err error
	got Request
}

func (f *fakeProvider) Name() string { return "fake" }
func (f *fakeProvider) Check() error { return nil }
func (f *fakeProvider) Complete(_ context.Context, r Request) (Response, error) {
	f.got = r
	return f.res, f.err
}

func TestWriteCommitMessage(t *testing.T) {
	p := &fakeProvider{res: Response{JSON: json.RawMessage(`{"subject":"Check the db and redis together.\nextra","body":"  They waited in turn.  "}`)}}
	m, err := WriteCommitMessage(context.Background(), p, "diff --git a/x b/x\n+1\n", "making healthz concurrent")
	if err != nil {
		t.Fatal(err)
	}
	if m.Subject != "Check the db and redis together" || m.Body != "They waited in turn." {
		t.Fatalf("%+v", m)
	}
	if m.String() != "Check the db and redis together\n\nThey waited in turn." {
		t.Fatalf("%q", m.String())
	}
	if !p.got.Small || p.got.Schema == nil || !strings.Contains(p.got.Prompt, "making healthz concurrent") || !strings.Contains(p.got.Prompt, "+1") {
		t.Fatalf("request %+v", p.got)
	}
}

func TestWriteCommitMessageEdges(t *testing.T) {
	if _, err := WriteCommitMessage(context.Background(), &fakeProvider{}, "  \n", ""); !errors.Is(err, ErrEmptyDiff) {
		t.Fatalf("empty diff: %v", err)
	}
	// Plain text: the first line is the subject.
	m, err := WriteCommitMessage(context.Background(), &fakeProvider{res: Response{Text: "Fix the flaky test\n\nIt raced the listener."}}, "d", "")
	if err != nil || m.Subject != "Fix the flaky test" || m.Body != "It raced the listener." {
		t.Fatalf("%+v %v", m, err)
	}
	if m := (CommitMessage{Subject: "Only a subject"}); m.String() != "Only a subject" {
		t.Fatalf("%q", m.String())
	}
	if _, err := WriteCommitMessage(context.Background(), &fakeProvider{}, "d", ""); err == nil {
		t.Fatal("no answer at all was taken")
	}
	if _, err := WriteCommitMessage(context.Background(), &fakeProvider{err: errors.New("down")}, "d", ""); err == nil || err.Error() != "down" {
		t.Fatalf("provider error: %v", err)
	}
	// An enormous diff is cut.
	p := &fakeProvider{res: Response{Text: "s"}}
	WriteCommitMessage(context.Background(), p, strings.Repeat("x", maxDiffBytes*2), "")
	if len(p.got.Prompt) > maxDiffBytes+200 || !strings.Contains(p.got.Prompt, "left out") {
		t.Fatalf("prompt of %d bytes", len(p.got.Prompt))
	}
}

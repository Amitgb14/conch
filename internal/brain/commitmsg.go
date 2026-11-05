package brain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// A commit message written from what is about to be committed: the staged
// diff, and optionally what the agent said it was doing. Conch.app's
// "Write from the changes" asks for one; nothing is committed by asking.

// CommitMessage is a subject and an optional body.
type CommitMessage struct {
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

// String is the message as git takes it: the subject, a blank line, the body.
func (m CommitMessage) String() string {
	if strings.TrimSpace(m.Body) == "" {
		return strings.TrimSpace(m.Subject)
	}
	return strings.TrimSpace(m.Subject) + "\n\n" + strings.TrimSpace(m.Body)
}

var commitSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"subject": map[string]any{"type": "string", "description": "One line, imperative, at most 72 characters, no trailing period."},
		"body":    map[string]any{"type": "string", "description": "Why the change was made, wrapped at 72 columns; empty when the subject says it all."},
	},
	"required": []string{"subject", "body"},
}

const commitSystem = `You write git commit messages. The subject is one imperative line of at most 72 characters saying what changed, with no trailing period. The body, wrapped at 72 columns, says why — the problem and the reason for this fix — not a list of the files touched. Never invent a change the diff does not show. Leave the body empty when the subject says it all.`

// maxDiffBytes bounds what is sent: a commit's diff can be enormous, and
// the start of each file says most of what it is.
const maxDiffBytes = 60_000

// ErrEmptyDiff is a request with nothing in it to describe.
var ErrEmptyDiff = errors.New("there is nothing staged to write a message for")

// WriteCommitMessage asks the model for a message for diff. context, when
// not empty, is what the agent that made the change said it was doing.
func WriteCommitMessage(ctx context.Context, p Provider, diff, context string) (CommitMessage, error) {
	if strings.TrimSpace(diff) == "" {
		return CommitMessage{}, ErrEmptyDiff
	}
	if len(diff) > maxDiffBytes {
		diff = diff[:maxDiffBytes] + "\n[… the rest of the diff is left out]\n"
	}
	prompt := "The staged diff:\n\n" + diff
	if c := strings.TrimSpace(context); c != "" {
		prompt = "What the agent that made this change said it was doing:\n" + c + "\n\n" + prompt
	}
	res, err := p.Complete(ctx, Request{System: commitSystem, Prompt: prompt, Schema: commitSchema, Small: true})
	if err != nil {
		return CommitMessage{}, err
	}
	var m CommitMessage
	if len(res.JSON) > 0 && json.Unmarshal(res.JSON, &m) == nil && strings.TrimSpace(m.Subject) != "" {
		return tidyCommit(m), nil
	}
	// A provider that answered in plain text: its first line is the subject.
	text := strings.TrimSpace(res.Text)
	if text == "" {
		return CommitMessage{}, fmt.Errorf("%s gave no message", p.Name())
	}
	subject, body, _ := strings.Cut(text, "\n")
	return tidyCommit(CommitMessage{Subject: subject, Body: body}), nil
}

// tidyCommit keeps the subject to one line without a trailing period.
func tidyCommit(m CommitMessage) CommitMessage {
	s := strings.TrimSpace(m.Subject)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	m.Subject = strings.TrimRight(s, ".")
	m.Body = strings.TrimSpace(m.Body)
	return m
}

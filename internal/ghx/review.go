package ghx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Submitting a review of a pull request: approve, request changes or
// comment, with comments on lines of its diff, in one review — as GitHub's
// own "Finish your review" does — through gh's API, since `gh pr review`
// takes no comments on lines.

// LineComment is a comment on one line of a pull request's diff, on its
// new side.
type LineComment struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Side string `json:"side"`
	Body string `json:"body"`
}

// Review events as GitHub names them.
var reviewEvents = map[string]string{"approve": "APPROVE", "request_changes": "REQUEST_CHANGES", "comment": "COMMENT"}

// Review submits a review of pull request number. event is approve,
// request_changes or comment; the last two need a body or a comment, as
// GitHub insists.
func Review(ctx context.Context, dir string, number int, event, body string, comments []LineComment) error {
	ev, ok := reviewEvents[event]
	if !ok {
		return fmt.Errorf("a review approves, requests changes or comments, not %q", event)
	}
	if ev != "APPROVE" && strings.TrimSpace(body) == "" && len(comments) == 0 {
		return errors.New("a review that is not an approval needs something said: a summary or a comment on a line")
	}
	for i := range comments {
		if comments[i].Side == "" {
			comments[i].Side = "RIGHT"
		}
		if comments[i].Path == "" || comments[i].Line <= 0 || strings.TrimSpace(comments[i].Body) == "" {
			return fmt.Errorf("comment %d is not on a line of a file, or says nothing", i+1)
		}
	}
	payload := map[string]any{"event": ev, "body": body}
	if len(comments) > 0 {
		payload["comments"] = comments
	}
	in, _ := json.Marshal(payload)
	_, err := runInput(ctx, dir, in, "api", "--method", "POST", "repos/{owner}/{repo}/pulls/"+strconv.Itoa(number)+"/reviews", "--input", "-")
	return err
}

// runInput is run with stdin given.
func runInput(ctx context.Context, dir string, input []byte, args ...string) ([]byte, error) {
	path, err := exec.LookPath(Binary())
	if err != nil {
		return nil, ErrNoGH
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1", "NO_COLOR=1")
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if i := strings.IndexByte(msg, '\n'); i >= 0 {
			msg = msg[:i]
		}
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("gh %s: %s", args[0], msg)
	}
	return stdout.Bytes(), nil
}

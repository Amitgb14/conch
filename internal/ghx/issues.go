package ghx

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Issue is a GitHub issue, as much of it as a task needs.
type Issue struct {
	Number int      `json:"number"`
	Title  string   `json:"title"`
	Body   string   `json:"body"`
	URL    string   `json:"url"`
	Labels []string `json:"labels,omitempty"`
}

type rawIssue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	URL    string `json:"url"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

func (r rawIssue) issue() Issue {
	is := Issue{Number: r.Number, Title: r.Title, Body: r.Body, URL: r.URL}
	for _, l := range r.Labels {
		is.Labels = append(is.Labels, l.Name)
	}
	return is
}

// Issues lists the open issues of the repository at dir, newest first, up
// to limit.
func Issues(ctx context.Context, dir string, limit int) ([]Issue, error) {
	if limit <= 0 {
		limit = 30
	}
	out, err := run(ctx, dir, "issue", "list", "--state", "open", "--limit", strconv.Itoa(limit),
		"--json", "number,title,body,url,labels")
	if err != nil {
		return nil, err
	}
	var raw []rawIssue
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("gh issue list: %w", err)
	}
	issues := make([]Issue, 0, len(raw))
	for _, r := range raw {
		issues = append(issues, r.issue())
	}
	return issues, nil
}

// IssueByNumber reads one issue of the repository at dir.
func IssueByNumber(ctx context.Context, dir string, number int) (Issue, error) {
	out, err := run(ctx, dir, "issue", "view", strconv.Itoa(number), "--json", "number,title,body,url,labels")
	if err != nil {
		return Issue{}, err
	}
	var r rawIssue
	if err := json.Unmarshal(out, &r); err != nil {
		return Issue{}, fmt.Errorf("gh issue view: %w", err)
	}
	return r.issue(), nil
}

// Whoami is the GitHub login gh is signed in as, asked in dir (any folder
// will do: the login is gh's, not the repository's).
func Whoami(ctx context.Context, dir string) (string, error) {
	out, err := run(ctx, dir, "api", "user", "--jq", ".login")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// IssueCount counts the open issues of the repository at dir, up to limit;
// mine counts those assigned to the person gh is signed in as.
func IssueCount(ctx context.Context, dir string, mine bool, limit int) (int, error) {
	args := []string{"issue", "list", "--state", "open", "--limit", strconv.Itoa(limit), "--json", "number"}
	if mine {
		args = append(args, "--assignee", "@me")
	}
	out, err := run(ctx, dir, args...)
	if err != nil {
		return 0, err
	}
	var raw []struct{ Number int }
	if err := json.Unmarshal(out, &raw); err != nil {
		return 0, fmt.Errorf("gh issue list: %w", err)
	}
	return len(raw), nil
}

// IssuesLabelled lists the open issues of the repository at dir that carry
// label, oldest first — the order a backlog is worked in — up to limit.
func IssuesLabelled(ctx context.Context, dir, label string, limit int) ([]Issue, error) {
	if limit <= 0 {
		limit = 30
	}
	out, err := run(ctx, dir, "issue", "list", "--state", "open", "--label", label, "--limit", strconv.Itoa(limit),
		"--json", "number,title,url,labels")
	if err != nil {
		return nil, err
	}
	var raw []rawIssue
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("gh issue list: %w", err)
	}
	issues := make([]Issue, 0, len(raw))
	for i := len(raw) - 1; i >= 0; i-- { // gh lists newest first
		issues = append(issues, raw[i].issue())
	}
	return issues, nil
}

// CommentIssue adds a comment to issue number of the repository at dir.
func CommentIssue(ctx context.Context, dir string, number int, body string) error {
	_, err := run(ctx, dir, "issue", "comment", strconv.Itoa(number), "--body", body)
	return err
}

package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/ghx"
	"github.com/Amitgb14/conch/internal/gitx"
	"github.com/Amitgb14/conch/internal/proto"
)

// Work often starts as an issue. A task can be started from one — the
// person picks it, conch reads it through gh on the machine with the
// checkout, and the agent is given its title and body — so nothing is
// copied out of a browser by hand.

// maxIssueBody keeps an issue with a pasted log in it from becoming the
// whole prompt.
const maxIssueBody = 12 << 10

func (pm *projectManager) issues(ip proto.IssuesParams) (proto.IssueList, *proto.Error) {
	p, perr := pm.gitProject(ip.ProjectID)
	if perr != nil {
		return proto.IssueList{}, perr
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	list, err := ghx.Issues(ctx, p.root, min(max(ip.Limit, 0), 100))
	if err != nil {
		return proto.IssueList{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	out := proto.IssueList{Issues: []proto.IssueInfo{}}
	for _, is := range list {
		out.Issues = append(out.Issues, proto.IssueInfo{Number: is.Number, Title: is.Title, URL: is.URL, Labels: is.Labels})
	}
	return out, nil
}

// issueTask fills a task's prompt and branch from the issue it names.
func (s *Server) issueTask(p *project, tp *proto.TaskCreateParams) *proto.Error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	is, err := ghx.IssueByNumber(ctx, p.root, tp.Issue)
	if err != nil {
		return proto.Errorf(proto.ErrBadRequest, "issue #%d: %v", tp.Issue, err)
	}
	tp.Prompt = issuePrompt(is, tp.Prompt)
	if tp.Branch == "" {
		tp.Branch = gitx.BranchFromPrompt(p.snapshot().Name, fmt.Sprintf("issue %d %s", is.Number, is.Title))
	}
	return nil
}

// issuePrompt is what the agent is told about an issue.
func issuePrompt(is ghx.Issue, extra string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Work on GitHub issue #%d: %s\n", is.Number, strings.TrimSpace(is.Title))
	if is.URL != "" {
		fmt.Fprintf(&b, "%s\n", is.URL)
	}
	body := strings.TrimSpace(is.Body)
	if len(body) > maxIssueBody {
		body = strings.ToValidUTF8(body[:maxIssueBody], "") + "\n\n… (the rest of the issue is on GitHub)"
	}
	if body != "" {
		b.WriteString("\n" + body + "\n")
	}
	if extra = strings.TrimSpace(extra); extra != "" {
		b.WriteString("\nAlso: " + extra + "\n")
	}
	fmt.Fprintf(&b, "\nWhen it is done, say so in a sentence; a pull request for it should say \"Fixes #%d\".", is.Number)
	return b.String()
}

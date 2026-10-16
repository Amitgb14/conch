package ghx

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Thread is an unresolved conversation on a line of a pull request's diff.
// Line is where it is now; an outdated thread — its lines changed since —
// has only the line it was written on.
type Thread struct {
	Path     string
	Line     int
	Outdated bool
	Hunk     string // the diff GitHub shows above the comment, ending at its line
	Comments []Comment
}

// Comment is one comment of a thread, or a review's own words.
type Comment struct {
	ID     int64
	Author string
	Body   string
	URL    string
	At     time.Time
	State  string // a review's: CHANGES_REQUESTED, COMMENTED
}

// Reviews is what reviewers said on a pull request that is still open to
// act on: the threads nobody resolved, and the reviews that said something
// besides approving.
type Reviews struct {
	Threads []Thread
	Reviews []Comment
}

// reviewsQuery asks for a pull request's threads and reviews. A hundred
// threads and fifty comments a thread is more than one review leaves.
const reviewsQuery = `query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      reviewThreads(first: 100) {
        nodes {
          isResolved isOutdated path line originalLine
          comments(first: 50) { nodes { databaseId author { login } body diffHunk url createdAt } }
        }
      }
      reviews(last: 50) { nodes { databaseId author { login } body url state submittedAt } }
    }
  }
}`

type rawAuthor struct {
	Login string `json:"login"`
}

type rawReviews struct {
	Data struct {
		Repository struct {
			PullRequest *struct {
				ReviewThreads struct {
					Nodes []struct {
						IsResolved   bool   `json:"isResolved"`
						IsOutdated   bool   `json:"isOutdated"`
						Path         string `json:"path"`
						Line         *int   `json:"line"`
						OriginalLine *int   `json:"originalLine"`
						Comments     struct {
							Nodes []struct {
								ID        int64      `json:"databaseId"`
								Author    *rawAuthor `json:"author"`
								Body      string     `json:"body"`
								DiffHunk  string     `json:"diffHunk"`
								URL       string     `json:"url"`
								CreatedAt time.Time  `json:"createdAt"`
							} `json:"nodes"`
						} `json:"comments"`
					} `json:"nodes"`
				} `json:"reviewThreads"`
				Reviews struct {
					Nodes []struct {
						ID          int64      `json:"databaseId"`
						Author      *rawAuthor `json:"author"`
						Body        string     `json:"body"`
						URL         string     `json:"url"`
						State       string     `json:"state"`
						SubmittedAt time.Time  `json:"submittedAt"`
					} `json:"nodes"`
				} `json:"reviews"`
			} `json:"pullRequest"`
		} `json:"repository"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// PRReviews reads what reviewers said on pull request number of the
// repository in dir. gh fills in the repository from the checkout.
func PRReviews(ctx context.Context, dir string, number int) (Reviews, error) {
	out, err := run(ctx, dir, "api", "graphql",
		"-F", "owner={owner}", "-F", "name={repo}", "-F", "number="+strconv.Itoa(number), "-f", "query="+reviewsQuery)
	if err != nil {
		return Reviews{}, err
	}
	return ParseReviews(out)
}

// ParseReviews decodes PRReviews' answer, keeping what is still to act on.
func ParseReviews(data []byte) (Reviews, error) {
	var raw rawReviews
	if err := json.Unmarshal(data, &raw); err != nil {
		return Reviews{}, fmt.Errorf("parse gh api graphql: %w", err)
	}
	if len(raw.Errors) > 0 {
		return Reviews{}, fmt.Errorf("gh api graphql: %s", raw.Errors[0].Message)
	}
	pr := raw.Data.Repository.PullRequest
	if pr == nil {
		return Reviews{}, fmt.Errorf("gh api graphql: no such pull request")
	}
	var out Reviews
	for _, t := range pr.ReviewThreads.Nodes {
		if t.IsResolved || len(t.Comments.Nodes) == 0 {
			continue
		}
		th := Thread{Path: t.Path, Outdated: t.IsOutdated || t.Line == nil, Hunk: t.Comments.Nodes[0].DiffHunk}
		switch {
		case t.Line != nil:
			th.Line = *t.Line
		case t.OriginalLine != nil:
			th.Line = *t.OriginalLine
		}
		for _, c := range t.Comments.Nodes {
			th.Comments = append(th.Comments, Comment{ID: c.ID, Author: login(c.Author), Body: c.Body, URL: c.URL, At: c.CreatedAt})
		}
		out.Threads = append(out.Threads, th)
	}
	for _, r := range pr.Reviews.Nodes {
		// An approval's words are thanks, not work; a pending review is
		// not sent yet, and a dismissed one was taken back.
		if strings.TrimSpace(r.Body) == "" || r.State != "CHANGES_REQUESTED" && r.State != "COMMENTED" {
			continue
		}
		out.Reviews = append(out.Reviews, Comment{ID: r.ID, Author: login(r.Author), Body: r.Body, URL: r.URL, At: r.SubmittedAt, State: r.State})
	}
	return out, nil
}

// login is an author's name; a deleted account has none.
func login(a *rawAuthor) string {
	if a == nil || a.Login == "" {
		return "ghost"
	}
	return a.Login
}

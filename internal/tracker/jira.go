package tracker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// jira is Jira Cloud, through its REST API with an Atlassian account's
// email and API token (basic authentication). A ticket is read through
// version 2, whose description and comments are plain text where
// version 3's are Atlassian's document format; a list through version 3's
// search/jql, which is what Atlassian keeps now that version 2's search
// is going.
type jira struct {
	site, email, token string
}

// NewJira is the Jira Cloud site (https://acme.atlassian.net), reached as
// email with the token.
func NewJira(site, email, token string) Tracker {
	return &jira{site: strings.TrimRight(site, "/"), email: email, token: token}
}

func (j *jira) Name() string { return Jira }

func (j *jira) call(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, j.site+path, body)
	if err != nil {
		return err
	}
	req.SetBasicAuth(j.email, j.token)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return do(req, "Jira", out)
}

type jiraFields struct {
	Summary     string `json:"summary"`
	Description string `json:"description"`
	Status      *struct {
		Name string `json:"name"`
	} `json:"status"`
	Comment *struct {
		Comments []struct {
			Body   string `json:"body"`
			Author *struct {
				DisplayName string `json:"displayName"`
			} `json:"author"`
		} `json:"comments"`
	} `json:"comment"`
}

type jiraIssue struct {
	Key    string     `json:"key"`
	Fields jiraFields `json:"fields"`
}

func (j *jira) ticket(i jiraIssue) Ticket {
	t := Ticket{Tracker: Jira, Key: i.Key, Title: i.Fields.Summary, Body: i.Fields.Description,
		URL: j.site + "/browse/" + i.Key}
	if i.Fields.Status != nil {
		t.Status = i.Fields.Status.Name
	}
	if i.Fields.Comment != nil {
		for _, c := range i.Fields.Comment.Comments {
			cm := Comment{Body: c.Body}
			if c.Author != nil {
				cm.Author = c.Author.DisplayName
			}
			t.Comments = append(t.Comments, cm)
		}
	}
	return t
}

// jqlKey is a project key as JQL may be given it: quoted, since a key is
// letters, digits and underscores but a reserved word would not be taken
// bare.
func jqlKey(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(strings.ToUpper(strings.TrimSpace(s))) + `"`
}

func (j *jira) List(ctx context.Context, opt ListOptions) ([]Ticket, error) {
	clauses := []string{"statusCategory != Done"}
	if s := strings.TrimSpace(opt.Scope); s != "" {
		clauses = append(clauses, "project = "+jqlKey(s))
	}
	if opt.Assigned {
		clauses = append(clauses, "assignee = currentUser()")
	}
	q := url.Values{}
	q.Set("jql", strings.Join(clauses, " AND ")+" ORDER BY updated DESC")
	q.Set("fields", "summary,status")
	q.Set("maxResults", strconv.Itoa(opt.limit()))
	var reply struct {
		Issues []jiraIssue `json:"issues"`
	}
	if err := j.call(ctx, http.MethodGet, "/rest/api/3/search/jql?"+q.Encode(), nil, &reply); err != nil {
		return nil, err
	}
	out := make([]Ticket, 0, len(reply.Issues))
	for _, i := range reply.Issues {
		out = append(out, j.ticket(i))
	}
	return out, nil
}

func issuePath(key string) string { return "/rest/api/2/issue/" + url.PathEscape(key) }

func (j *jira) Get(ctx context.Context, key string) (Ticket, error) {
	var i jiraIssue
	if err := j.call(ctx, http.MethodGet, issuePath(key)+"?fields=summary,description,status,comment", nil, &i); err != nil {
		return Ticket{}, err
	}
	t := j.ticket(i)
	var links []struct {
		Object struct {
			URL   string `json:"url"`
			Title string `json:"title"`
		} `json:"object"`
	}
	// Links are a nicety: a ticket read without them is still the ticket.
	if err := j.call(ctx, http.MethodGet, issuePath(key)+"/remotelink", nil, &links); err == nil {
		for _, l := range links {
			t.Links = append(t.Links, Link{Title: l.Object.Title, URL: l.Object.URL})
		}
	}
	return t, nil
}

func (j *jira) Comment(ctx context.Context, key, body string) error {
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("a comment needs something to say")
	}
	return j.call(ctx, http.MethodPost, issuePath(key)+"/comment", map[string]string{"body": body}, nil)
}

// SetStatus takes the transition whose name, or the status it leads to,
// is the one asked for: Jira moves a ticket by a workflow's transitions,
// not by naming a status.
func (j *jira) SetStatus(ctx context.Context, key, status string) (string, error) {
	var reply struct {
		Transitions []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			To   struct {
				Name string `json:"name"`
			} `json:"to"`
		} `json:"transitions"`
	}
	if err := j.call(ctx, http.MethodGet, issuePath(key)+"/transitions", nil, &reply); err != nil {
		return "", err
	}
	var to, names []string
	for _, t := range reply.Transitions {
		to = append(to, t.To.Name)
		names = append(names, t.Name)
	}
	n, ok := pick(status, to)
	if !ok {
		n, ok = pick(status, names)
	}
	if !ok {
		if len(to) == 0 {
			return "", fmt.Errorf("%s can't be moved from where it is", key)
		}
		return "", fmt.Errorf("%s can't move to %q from where it is; it can move to: %s", key, status, strings.Join(to, ", "))
	}
	tr := reply.Transitions[n]
	if err := j.call(ctx, http.MethodPost, issuePath(key)+"/transitions", map[string]any{"transition": map[string]string{"id": tr.ID}}, nil); err != nil {
		return "", err
	}
	return tr.To.Name, nil
}

func (j *jira) Link(ctx context.Context, key, link, title string) error {
	if strings.TrimSpace(link) == "" {
		return fmt.Errorf("a link needs an address")
	}
	if title == "" {
		title = link
	}
	// The address as the global ID: linking the same pull request again
	// updates its link rather than adding a second.
	obj := map[string]any{"globalId": link, "object": map[string]string{"url": link, "title": title}}
	return j.call(ctx, http.MethodPost, issuePath(key)+"/remotelink", obj, nil)
}

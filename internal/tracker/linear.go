package tracker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// LinearAPI is Linear's GraphQL endpoint.
const LinearAPI = "https://api.linear.app/graphql"

// linear is Linear, through its GraphQL API with a personal API key —
// which Linear takes as the Authorization header as it is, with no
// Bearer (that is for OAuth tokens).
type linear struct {
	url, key string
}

// NewLinear is Linear at url ("" is LinearAPI) with the key.
func NewLinear(url, key string) Tracker {
	if url == "" {
		url = LinearAPI
	}
	return &linear{url: url, key: key}
}

func (l *linear) Name() string { return Linear }

// query runs one GraphQL request and decodes its data into out.
func (l *linear) query(ctx context.Context, q string, vars map[string]any, out any) error {
	body, err := json.Marshal(map[string]any{"query": q, "variables": vars})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, l.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", l.key)
	var reply struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message    string `json:"message"`
			Extensions struct {
				Code string `json:"code"`
			} `json:"extensions"`
		} `json:"errors"`
	}
	if err := do(req, "Linear", &reply); err != nil {
		return err
	}
	if len(reply.Errors) > 0 {
		var msgs []string
		for _, e := range reply.Errors {
			// Linear says a missing issue as an error, not a null.
			if e.Extensions.Code == "ENTITY_NOT_FOUND" || strings.Contains(strings.ToLower(e.Message), "not found") {
				return fmt.Errorf("Linear: %w", ErrNotFound)
			}
			msgs = append(msgs, e.Message)
		}
		return fmt.Errorf("Linear: %s", strings.Join(msgs, "; "))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(reply.Data, out)
}

type linearIssue struct {
	ID          string `json:"id"`
	Identifier  string `json:"identifier"`
	Title       string `json:"title"`
	Description string `json:"description"`
	URL         string `json:"url"`
	State       *struct {
		Name string `json:"name"`
	} `json:"state"`
	Comments struct {
		Nodes []struct {
			Body string `json:"body"`
			User *struct {
				Name string `json:"name"`
			} `json:"user"`
		} `json:"nodes"`
	} `json:"comments"`
	Attachments struct {
		Nodes []Link `json:"nodes"`
	} `json:"attachments"`
}

func (i linearIssue) ticket() Ticket {
	t := Ticket{Tracker: Linear, Key: i.Identifier, Title: i.Title, Body: i.Description, URL: i.URL}
	if i.State != nil {
		t.Status = i.State.Name
	}
	for _, c := range i.Comments.Nodes {
		cm := Comment{Body: c.Body}
		if c.User != nil {
			cm.Author = c.User.Name
		}
		t.Comments = append(t.Comments, cm)
	}
	t.Links = append(t.Links, i.Attachments.Nodes...)
	return t
}

const linearList = `query Tickets($first: Int!, $filter: IssueFilter) {
  issues(first: $first, filter: $filter, orderBy: updatedAt) {
    nodes { identifier title url state { name } }
  }
}`

func (l *linear) List(ctx context.Context, opt ListOptions) ([]Ticket, error) {
	filter := map[string]any{"state": map[string]any{"type": map[string]any{"nin": []string{"completed", "canceled"}}}}
	if s := strings.TrimSpace(opt.Scope); s != "" {
		filter["team"] = map[string]any{"key": map[string]any{"eq": strings.ToUpper(s)}}
	}
	if opt.Assigned {
		filter["assignee"] = map[string]any{"isMe": map[string]any{"eq": true}}
	}
	var data struct {
		Issues struct {
			Nodes []linearIssue `json:"nodes"`
		} `json:"issues"`
	}
	if err := l.query(ctx, linearList, map[string]any{"first": opt.limit(), "filter": filter}, &data); err != nil {
		return nil, err
	}
	out := make([]Ticket, 0, len(data.Issues.Nodes))
	for _, i := range data.Issues.Nodes {
		out = append(out, i.ticket())
	}
	return out, nil
}

const linearGet = `query Ticket($id: String!) {
  issue(id: $id) {
    id identifier title description url state { name }
    comments(first: 50) { nodes { body user { name } } }
    attachments(first: 50) { nodes { title url } }
  }
}`

func (l *linear) issue(ctx context.Context, key string) (linearIssue, error) {
	var data struct {
		Issue *linearIssue `json:"issue"`
	}
	if err := l.query(ctx, linearGet, map[string]any{"id": key}, &data); err != nil {
		return linearIssue{}, err
	}
	if data.Issue == nil {
		return linearIssue{}, fmt.Errorf("Linear: %w", ErrNotFound)
	}
	return *data.Issue, nil
}

func (l *linear) Get(ctx context.Context, key string) (Ticket, error) {
	i, err := l.issue(ctx, key)
	if err != nil {
		return Ticket{}, err
	}
	return i.ticket(), nil
}

const linearComment = `mutation Comment($id: String!, $body: String!) {
  commentCreate(input: {issueId: $id, body: $body}) { success }
}`

func (l *linear) Comment(ctx context.Context, key, body string) error {
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("a comment needs something to say")
	}
	i, err := l.issue(ctx, key)
	if err != nil {
		return err
	}
	var data struct {
		CommentCreate struct {
			Success bool `json:"success"`
		} `json:"commentCreate"`
	}
	if err := l.query(ctx, linearComment, map[string]any{"id": i.ID, "body": body}, &data); err != nil {
		return err
	}
	if !data.CommentCreate.Success {
		return fmt.Errorf("Linear did not add the comment")
	}
	return nil
}

const linearStates = `query States($id: String!) {
  issue(id: $id) { id team { states { nodes { id name } } } }
}`

const linearSetState = `mutation SetState($id: String!, $state: String!) {
  issueUpdate(id: $id, input: {stateId: $state}) { success issue { state { name } } }
}`

func (l *linear) SetStatus(ctx context.Context, key, status string) (string, error) {
	var data struct {
		Issue *struct {
			ID   string `json:"id"`
			Team struct {
				States struct {
					Nodes []struct {
						ID   string `json:"id"`
						Name string `json:"name"`
					} `json:"nodes"`
				} `json:"states"`
			} `json:"team"`
		} `json:"issue"`
	}
	if err := l.query(ctx, linearStates, map[string]any{"id": key}, &data); err != nil {
		return "", err
	}
	if data.Issue == nil {
		return "", fmt.Errorf("Linear: %w", ErrNotFound)
	}
	var names []string
	for _, s := range data.Issue.Team.States.Nodes {
		names = append(names, s.Name)
	}
	n, ok := pick(status, names)
	if !ok {
		return "", fmt.Errorf("%s has no status %q; its team's are: %s", key, status, strings.Join(names, ", "))
	}
	var set struct {
		IssueUpdate struct {
			Success bool `json:"success"`
			Issue   struct {
				State struct {
					Name string `json:"name"`
				} `json:"state"`
			} `json:"issue"`
		} `json:"issueUpdate"`
	}
	state := data.Issue.Team.States.Nodes[n]
	if err := l.query(ctx, linearSetState, map[string]any{"id": data.Issue.ID, "state": state.ID}, &set); err != nil {
		return "", err
	}
	if !set.IssueUpdate.Success {
		return "", fmt.Errorf("Linear did not move %s", key)
	}
	if got := set.IssueUpdate.Issue.State.Name; got != "" {
		return got, nil
	}
	return state.Name, nil
}

const linearLink = `mutation Link($id: String!, $url: String!, $title: String) {
  attachmentLinkURL(issueId: $id, url: $url, title: $title) { success }
}`

func (l *linear) Link(ctx context.Context, key, url, title string) error {
	if strings.TrimSpace(url) == "" {
		return fmt.Errorf("a link needs an address")
	}
	i, err := l.issue(ctx, key)
	if err != nil {
		return err
	}
	vars := map[string]any{"id": i.ID, "url": url}
	if title != "" {
		vars["title"] = title
	}
	var data struct {
		AttachmentLinkURL struct {
			Success bool `json:"success"`
		} `json:"attachmentLinkURL"`
	}
	if err := l.query(ctx, linearLink, vars, &data); err != nil {
		return err
	}
	if !data.AttachmentLinkURL.Success {
		return fmt.Errorf("Linear did not link %s", url)
	}
	return nil
}

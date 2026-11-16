package integration

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Amitgb14/conch/internal/tracker"
)

// A tickets file is a tracker conch has no code for: Shortcut, GitLab's
// issues. Tracker makes one a tracker.Tracker, so everything that reads
// Linear and Jira — the Tickets view, a task started from a ticket,
// conch ticket — reads it the same way.

// Tracker is the file's tickets for the project at t (its remote fills
// {project}; the branch and head are not used).
func (c *Conn) Tracker(t Target) tracker.Tracker {
	return &fileTracker{c: c, t: t}
}

type fileTracker struct {
	c *Conn
	t Target
}

func (ft *fileTracker) Name() string { return ft.c.f.Name }

// List is the file's open tickets, or with Assigned its mine operation's.
func (ft *fileTracker) List(ctx context.Context, opt tracker.ListOptions) ([]tracker.Ticket, error) {
	op, what := ft.c.f.List, "list"
	if opt.Assigned {
		if !ft.c.f.Mine.Has() {
			return nil, fmt.Errorf("%s: its file cannot list the tickets assigned to you", ft.c.f.DisplayName())
		}
		op, what = ft.c.f.Mine, "mine"
	}
	limit := opt.Limit
	if limit <= 0 {
		limit = 30
	}
	limit = min(limit, 100)
	vals := ft.c.vars(ft.t, "")
	vals["scope"], vals["limit"] = opt.Scope, strconv.Itoa(limit)
	last, err := ft.c.steps(ctx, what, op.Operation, vals)
	if errors.Is(err, ErrNothing) {
		return []tracker.Ticket{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []tracker.Ticket{}
	for _, it := range listOf(Pick(last, op.Items, nil)) {
		t := ft.ticket(op, it, vals)
		if t.Key == "" {
			continue
		}
		out = append(out, t)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

// Get reads one ticket, with its comments when the file says where they
// are.
func (ft *fileTracker) Get(ctx context.Context, key string) (tracker.Ticket, error) {
	vals, err := ft.keyed(key)
	if err != nil {
		return tracker.Ticket{}, err
	}
	op := ft.c.f.Get
	last, err := ft.c.steps(ctx, "get", op.Operation, vals)
	if err != nil {
		return tracker.Ticket{}, notFound(key, err)
	}
	it := last
	if op.Items != "" {
		if it = Pick(last, op.Items, func(s string) string { return fillText(s, vals) }); it == nil {
			return tracker.Ticket{}, fmt.Errorf("%s: %w", key, tracker.ErrNotFound)
		}
	}
	t := ft.ticket(op, it, vals)
	if t.Key == "" {
		t.Key = key
	}
	if op.Comments != "" {
		var cs any
		if refOnly(op.Comments) {
			cs, _ = lookup(op.Comments[1:len(op.Comments)-1], vals)
		} else {
			cs = Pick(it, op.Comments, nil)
		}
		for _, c := range listOf(cs) {
			body := strings.TrimSpace(Text(Pick(c, op.CommentBody, nil)))
			if body == "" {
				continue
			}
			t.Comments = append(t.Comments, tracker.Comment{Author: Text(Pick(c, op.CommentAuthor, nil)), Body: body})
		}
	}
	return t, nil
}

// Comment adds a comment.
func (ft *fileTracker) Comment(ctx context.Context, key, body string) error {
	return ft.do(ctx, "comment", ft.c.f.Comment, key, map[string]any{"body": body})
}

// SetStatus moves the ticket and reads it back for the status it is in.
func (ft *fileTracker) SetStatus(ctx context.Context, key, status string) (string, error) {
	if err := ft.do(ctx, "move", ft.c.f.Move, key, map[string]any{"status": strings.TrimSpace(status)}); err != nil {
		return "", err
	}
	t, err := ft.Get(ctx, key)
	if err != nil {
		return status, nil // moved; reading it back is a nicety
	}
	return t.Status, nil
}

// Link attaches a link; a file whose service has no links of its own
// says it in a comment.
func (ft *fileTracker) Link(ctx context.Context, key, url, title string) error {
	if strings.TrimSpace(title) == "" {
		title = url
	}
	return ft.do(ctx, "link", ft.c.f.Link, key, map[string]any{"url": url, "title": title})
}

func (ft *fileTracker) do(ctx context.Context, what string, op Operation, key string, extra map[string]any) error {
	if !op.Has() {
		return fmt.Errorf("%s: its file cannot %s", ft.c.f.DisplayName(), what)
	}
	vals, err := ft.keyed(key)
	if err != nil {
		return err
	}
	for k, v := range extra {
		vals[k] = v
	}
	_, err = ft.c.steps(ctx, what, op, vals)
	if errors.Is(err, ErrNothing) {
		// A pick on the way found nothing: a status the service has not
		// got, a ticket it has not.
		return fmt.Errorf("%s: %w", key, err)
	}
	return notFound(key, err)
}

func (ft *fileTracker) keyed(key string) (map[string]any, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, errors.New("no ticket given")
	}
	vals := ft.c.vars(ft.t, "")
	vals["ticket"] = key
	return vals, nil
}

// ticket reads one ticket out of it.
func (ft *fileTracker) ticket(op TicketsOp, it any, vals map[string]any) tracker.Ticket {
	vals["item"] = it
	defer delete(vals, "item")
	field := func(spec string) string {
		if spec == "" {
			return ""
		}
		if strings.Contains(spec, "{") {
			return fillText(spec, vals)
		}
		return Text(Pick(it, spec, nil))
	}
	t := tracker.Ticket{Tracker: ft.c.f.Name, Key: field(op.Key), Title: field(op.Title), Body: field(op.Description),
		URL: field(op.URL), Status: field(op.Status)}
	if op.StatusNames != "" && t.Status != "" {
		names, _ := lookup(op.StatusNames[1:len(op.StatusNames)-1], vals)
		for _, n := range flat(names) {
			if Text(Pick(n, "id", nil)) == t.Status {
				t.Status = Text(Pick(n, "name", nil))
				break
			}
		}
	}
	return t
}

// flat is a list with the lists in it opened: [*].states is a list of
// every workflow's states.
func flat(v any) []any {
	var out []any
	for _, x := range listOf(v) {
		if l, ok := x.([]any); ok {
			out = append(out, flat(l)...)
		} else {
			out = append(out, x)
		}
	}
	return out
}

// notFound says a 404 as the trackers do.
func notFound(key string, err error) error {
	var se *StatusError
	if errors.As(err, &se) && se.Code == http.StatusNotFound || errors.Is(err, ErrNothing) {
		return fmt.Errorf("%s: %w", key, tracker.ErrNotFound)
	}
	return err
}

// Message is one notification: what happened, more about it, and where
// to look.
type Message struct {
	Title string
	Body  string
	URL   string
}

// The most of a message sent: a chat's webhook refuses a long one
// (Discord's content is 2000 characters), and a notification is read at a
// glance.
const (
	maxTitle = 200
	maxBody  = 1500
)

// Send sends a notification through a notify file.
func (c *Conn) Send(ctx context.Context, m Message) error {
	if !c.f.Send.Has() {
		return fmt.Errorf("%s: its file cannot send", c.f.DisplayName())
	}
	vals := map[string]any{"title": cut(strings.TrimSpace(m.Title), maxTitle),
		"body": cut(strings.TrimSpace(m.Body), maxBody), "url": strings.TrimSpace(m.URL)}
	_, err := c.steps(ctx, "send", c.f.Send, vals)
	if errors.Is(err, ErrNothing) {
		return nil // an empty answer is the usual one
	}
	return err
}

func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + "…"
}

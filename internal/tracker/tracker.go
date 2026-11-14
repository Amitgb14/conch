// Package tracker reads and updates tickets in the trackers teams use
// besides GitHub issues — Linear and Jira Cloud — so a task can start from
// one and its branch and pull request can be linked back to it. Each is
// reached over its own HTTP API with a personal key; nothing here runs a
// program or keeps state.
package tracker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Ticket is a ticket, as much of it as a task needs.
type Ticket struct {
	Tracker  string    `json:"tracker"`
	Key      string    `json:"key"`
	Title    string    `json:"title"`
	Body     string    `json:"body,omitempty"`
	URL      string    `json:"url,omitempty"`
	Status   string    `json:"status,omitempty"`
	Comments []Comment `json:"comments,omitempty"`
	Links    []Link    `json:"links,omitempty"`
}

// Comment is one comment on a ticket.
type Comment struct {
	Author string `json:"author,omitempty"`
	Body   string `json:"body"`
}

// Link is a link attached to a ticket: a branch, a pull request.
type Link struct {
	Title string `json:"title,omitempty"`
	URL   string `json:"url"`
}

// ListOptions narrow a list of tickets.
type ListOptions struct {
	// Scope is the Linear team's key or the Jira project's key; "" is
	// every one the key can see.
	Scope string
	// Assigned keeps only the tickets assigned to the key's own person.
	Assigned bool
	// Limit is how many, newest first; 0 is 30.
	Limit int
}

func (o ListOptions) limit() int {
	if o.Limit <= 0 {
		return 30
	}
	return min(o.Limit, 100)
}

// Tracker is one tracker conch has code for.
type Tracker interface {
	Name() string
	// List is the open tickets, most recently changed first.
	List(ctx context.Context, opt ListOptions) ([]Ticket, error)
	// Get reads one ticket, with its comments and links.
	Get(ctx context.Context, key string) (Ticket, error)
	// Comment adds a comment.
	Comment(ctx context.Context, key, body string) error
	// SetStatus moves the ticket to the status named (In Review, Done),
	// matched without regard to case, and says the status it is now in.
	SetStatus(ctx context.Context, key, status string) (string, error)
	// Link attaches a link — a branch, a pull request — to the ticket.
	Link(ctx context.Context, key, url, title string) error
}

// ErrNotFound is a ticket the tracker has not got, or will not show.
var ErrNotFound = errors.New("no such ticket")

// client is what both trackers share: one HTTP client with a timeout, and
// a body read with a cap so a misbehaving server can't fill memory.
var client = &http.Client{Timeout: 30 * time.Second}

const maxReply = 8 << 20

// do sends req and decodes a 2xx reply's JSON into out (when out is not
// nil); anything else is an error saying what the server said.
func do(req *http.Request, name string, out any) error {
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxReply))
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%s refused the key (%s): check it, and that it may see this", name, resp.Status)
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("%s: %w", name, ErrNotFound)
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return fmt.Errorf("%s: %s%s", name, resp.Status, said(body))
	}
	if out == nil || len(body) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%s: reading its reply: %w", name, err)
	}
	return nil
}

// said is the start of an error reply, for the message.
func said(body []byte) string {
	s := strings.TrimSpace(string(body))
	if s == "" {
		return ""
	}
	if len(s) > 300 {
		s = strings.ToValidUTF8(s[:300], "") + "…"
	}
	return ": " + s
}

// pick is the one of names that matches want without regard to case.
func pick(want string, names []string) (int, bool) {
	want = strings.TrimSpace(want)
	for i, n := range names {
		if strings.EqualFold(n, want) {
			return i, true
		}
	}
	return -1, false
}

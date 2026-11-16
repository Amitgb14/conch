package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ErrNothing means a step picked nothing: there is no such run, nothing
// failed, no log to read. It is an answer, not a failure.
var ErrNothing = errors.New("nothing there")

// ErrNoKey means the integration's key is not set.
var ErrNoKey = errors.New("no key")

// maxReply is the most of one reply read; a log of a big suite runs to
// megabytes, and the caller keeps only its end.
const maxReply = 16 << 20

// Transport carries every request; tests point it at their own servers.
var Transport http.RoundTripper = http.DefaultTransport

// Timeout bounds one request; the caller's context bounds the operation.
var Timeout = time.Minute

// Target is what an operation is asked about: a branch of the project
// whose remote is Remote, at Head (its pull request's head commit, "" for
// none).
type Target struct {
	Remote Remote
	Branch string
	Head   string
}

// Run is one CI run, in the ci contract's words. State is pass, fail,
// pending or canceled; Raw is the service's own word for it.
type Run struct {
	ID       string    `json:"id"`
	Name     string    `json:"name,omitempty"`
	State    string    `json:"state"`
	Raw      string    `json:"raw,omitempty"`
	Commit   string    `json:"commit,omitempty"`
	URL      string    `json:"url,omitempty"`
	Started  time.Time `json:"started,omitempty"`
	Finished time.Time `json:"finished,omitempty"`
}

// Log is what a failed run printed, and which job printed it.
type Log struct {
	Job  string
	Text string
}

// Conn is a file with what it needs to be used: the key, the user for
// basic auth, and the base URL the person gave for base_url = "ask".
type Conn struct {
	f    *File
	key  string
	user string
	base string
}

// Connect readies the file for calls. base replaces api ("" keeps it);
// it must be https, or the file's api when there is no base.
func (f *File) Connect(key, user, base string) (*Conn, error) {
	if f.Kind != KindCI {
		return nil, fmt.Errorf("%s: kind %q is not one this conch runs", f.Name, f.Kind)
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, fmt.Errorf("%w: set $%s to a %s key", ErrNoKey, f.KeyEnv, f.DisplayName())
	}
	if f.Auth.Type == "basic" && strings.TrimSpace(user) == "" {
		return nil, fmt.Errorf("%w: set $%s to your %s user name", ErrNoKey, f.Auth.UserEnv, f.DisplayName())
	}
	if base == "" {
		base = f.API
	} else if f.BaseURL != "ask" {
		return nil, fmt.Errorf("%s: its API is %s; it takes no other", f.Name, f.API)
	}
	if !webURL(base) {
		return nil, fmt.Errorf("%s: %q is not an https:// URL", f.Name, base)
	}
	return &Conn{f: f, key: key, user: strings.TrimSpace(user), base: strings.TrimRight(base, "/")}, nil
}

// Runs lists the runs of t's branch, newest first, as the file reads them.
func (c *Conn) Runs(ctx context.Context, t Target) ([]Run, error) {
	return c.runs(ctx, t, c.f.Runs)
}

// Stats lists runs over a longer time, for the CI panel; Runs when the
// file has no stats.
func (c *Conn) Stats(ctx context.Context, t Target) ([]Run, error) {
	if !c.f.Stats.Has() {
		return c.Runs(ctx, t)
	}
	return c.runs(ctx, t, c.f.Stats)
}

func (c *Conn) runs(ctx context.Context, t Target, op RunsOp) ([]Run, error) {
	vals := c.vars(t, "")
	last, err := c.steps(ctx, "runs", op.Operation, vals)
	if errors.Is(err, ErrNothing) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var items []any
	switch x := Pick(last, op.Items, nil).(type) {
	case nil:
	case []any:
		items = x
	default:
		items = []any{x}
	}
	runs := make([]Run, 0, len(items))
	for _, it := range items {
		vals["item"] = it
		field := func(spec string) string {
			if strings.Contains(spec, "{") {
				return fillText(spec, vals)
			}
			return Text(Pick(it, spec, nil))
		}
		r := Run{ID: field(op.ID), Name: field(op.Name), Raw: field(op.State), Commit: field(op.Commit),
			URL: field(op.URL), Started: parseTime(field(op.Started)), Finished: parseTime(field(op.Finished))}
		if r.ID == "" {
			continue
		}
		r.State = c.f.States[r.Raw]
		if r.State == "" {
			r.State = "pending" // a state the file does not know is not a verdict
		}
		runs = append(runs, r)
	}
	delete(vals, "item")
	return runs, nil
}

// FailedLog is what run printed where it failed.
func (c *Conn) FailedLog(ctx context.Context, t Target, run string) (Log, error) {
	if !c.f.FailedLog.Has() {
		return Log{}, fmt.Errorf("%s: its file cannot read a log", c.f.DisplayName())
	}
	vals := c.vars(t, run)
	last, err := c.steps(ctx, "failed_log", c.f.FailedLog.Operation, vals)
	if err != nil {
		return Log{}, err
	}
	return Log{Job: fillText(c.f.FailedLog.Job, vals), Text: Text(last)}, nil
}

// Rerun re-runs what failed in run.
func (c *Conn) Rerun(ctx context.Context, t Target, run string) error {
	if !c.f.Rerun.Has() {
		return fmt.Errorf("%s: its file cannot re-run", c.f.DisplayName())
	}
	_, err := c.steps(ctx, "rerun", c.f.Rerun, c.vars(t, run))
	if errors.Is(err, ErrNothing) {
		return nil
	}
	return err
}

// vars are the placeholders every call has.
func (c *Conn) vars(t Target, run string) map[string]any {
	path := t.Remote.Path
	owner, _, _ := strings.Cut(path, "/")
	repo := path[strings.LastIndex(path, "/")+1:]
	parts := map[string]any{"host": t.Remote.Host, "owner": owner, "repo": repo, "path": path,
		"path_escaped": strings.ReplaceAll(url.PathEscape(path), "/", "%2F")}
	v := map[string]any{"branch": t.Branch, "head": t.Head, "project": fillText(c.f.Project.ID, parts)}
	for k, x := range parts {
		v[k] = x
	}
	if run != "" {
		v["run"] = run
	}
	return v
}

// steps runs an operation and returns the last step's value.
func (c *Conn) steps(ctx context.Context, what string, op Operation, vals map[string]any) (any, error) {
	var last any
	for i, s := range op.steps() {
		items := []any{nil}
		if s.Each != "" {
			switch x := vals[s.Each].(type) {
			case []any:
				items = x
			case nil:
				return nil, fmt.Errorf("%w: [%s] step %d has no %s", ErrNothing, what, i+1, s.Each)
			default:
				items = []any{x}
			}
		}
		var texts []string
		var picked []any
		for _, it := range items {
			if s.Each != "" {
				vals["item"] = it
			}
			raw, err := c.call(ctx, s, vals)
			if err != nil {
				return nil, fmt.Errorf("[%s] step %d: %w", what, i+1, err)
			}
			switch {
			case s.Follow != "":
				urls, err := jsonPick(raw, s.Follow, vals)
				if err != nil {
					return nil, fmt.Errorf("[%s] step %d: %w", what, i+1, err)
				}
				for _, u := range listOf(urls) {
					body, err := c.fetch(ctx, Text(u))
					if err != nil {
						return nil, fmt.Errorf("[%s] step %d: %w", what, i+1, err)
					}
					t, err := textOf(body, s.Text)
					if err != nil {
						return nil, fmt.Errorf("[%s] step %d: %s: %w", what, i+1, Text(u), err)
					}
					texts = append(texts, t)
				}
			case s.Text != "":
				t, err := textOf(raw, s.Text)
				if err != nil {
					return nil, fmt.Errorf("[%s] step %d: %w", what, i+1, err)
				}
				texts = append(texts, t)
			default:
				v, err := jsonPick(raw, s.Pick, vals)
				if err != nil {
					return nil, fmt.Errorf("[%s] step %d: %w", what, i+1, err)
				}
				if v != nil {
					picked = append(picked, v)
				}
			}
		}
		delete(vals, "item")
		switch {
		case s.Follow != "" || s.Text != "":
			if len(texts) == 0 {
				return nil, fmt.Errorf("%w: [%s] step %d found nothing to read", ErrNothing, what, i+1)
			}
			last = joinLines(texts)
		case len(picked) == 0:
			return nil, fmt.Errorf("%w: [%s] step %d picked nothing", ErrNothing, what, i+1)
		case s.Each != "":
			last = picked
		default:
			last = picked[0]
		}
		vals["reply"] = last
		if s.As != "" {
			vals[s.As] = last
		}
	}
	return last, nil
}

// jsonPick reads a reply as JSON and picks path from it; an empty reply
// or path is the reply itself (nil for an empty one).
func jsonPick(raw []byte, path string, vals map[string]any) (any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		if path == "" {
			return map[string]any{}, nil // a call with an empty answer still answered
		}
		return nil, nil
	}
	var v any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&v); err != nil {
		return nil, errors.New("the reply is not JSON")
	}
	if path == "" {
		return v, nil
	}
	return Pick(v, path, func(s string) string { return fillText(s, vals) }), nil
}

func textOf(raw []byte, spec string) (string, error) {
	if spec == "." || spec == "" {
		return string(raw), nil
	}
	v, err := jsonPick(raw, spec, nil)
	if err != nil {
		return "", err
	}
	var parts []string
	for _, x := range listOf(v) {
		parts = append(parts, Text(x))
	}
	return joinLines(parts), nil
}

func listOf(v any) []any {
	switch x := v.(type) {
	case nil:
		return nil
	case []any:
		return x
	}
	return []any{v}
}

// joinLines puts texts together a line each, without doubling a newline
// one already ends with.
func joinLines(parts []string) string {
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(p)
		if p != "" && !strings.HasSuffix(p, "\n") {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// call makes a step's request to the API, with the key.
func (c *Conn) call(ctx context.Context, s Step, vals map[string]any) ([]byte, error) {
	method, target, _ := strings.Cut(strings.TrimSpace(s.Call), " ")
	path, query, _ := strings.Cut(target, "?")
	p, err := fillPath(path, vals)
	if err != nil {
		return nil, err
	}
	if q := fillQuery(query, vals); q != "" {
		p += "?" + q
	}
	var body io.Reader
	if s.Body != nil {
		filled, _ := fillBody(s.Body, vals)
		enc, err := json.Marshal(filled)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(enc)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+p, body)
	if err != nil {
		return nil, err
	}
	apiHost := req.URL.Host
	switch c.f.Auth.Type {
	case "bearer":
		req.Header.Set("Authorization", "Bearer "+c.key)
	case "basic":
		req.SetBasicAuth(c.user, c.key)
	default:
		header := firstSet(c.f.Auth.Header, c.f.AuthHeader, "Authorization")
		value := firstSet(c.f.Auth.Value, c.f.AuthValue, "Bearer {key}")
		req.Header.Set(header, strings.ReplaceAll(value, "{key}", c.key))
	}
	if s.Text == "." {
		req.Header.Set("Accept", "text/plain, */*")
	} else {
		req.Header.Set("Accept", "application/json")
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// The key may be in a header Go keeps across a redirect (anything but
	// Authorization), so a redirect may not leave the API's own host and
	// port.
	return c.send(req, method+" "+path, func(u *url.URL) bool { return u.Host == apiHost && u.Scheme == req.URL.Scheme })
}

// fetch reads a URL a reply handed back, from a follow host, without the
// key.
func (c *Conn) fetch(ctx context.Context, raw string) ([]byte, error) {
	u, err := url.Parse(raw)
	if err != nil || !c.followable(u) {
		return nil, fmt.Errorf("%s may not be fetched: not an https URL on a host in follow_hosts (%s)",
			redact(raw), strings.Join(c.f.FollowHosts, ", "))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	return c.send(req, "GET "+u.Host+u.Path, c.followable)
}

func (c *Conn) followable(u *url.URL) bool {
	host := strings.ToLower(u.Hostname())
	if !(u.Scheme == "https" || u.Scheme == "http" && loopback(host)) {
		return false
	}
	for _, h := range c.f.FollowHosts {
		h = strings.ToLower(h)
		if host == h || strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return false
}

func (c *Conn) send(req *http.Request, what string, allowed func(*url.URL) bool) ([]byte, error) {
	req.Header.Set("User-Agent", "conch")
	hc := &http.Client{Transport: Transport, Timeout: Timeout, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !allowed(r.URL) {
			return fmt.Errorf("redirected to %s, which the file does not name", r.URL.Host)
		}
		return nil
	}}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, scrub(err, c.key))
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxReply+1))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	if len(data) > maxReply {
		return nil, fmt.Errorf("%s: the reply is over %d MB", what, maxReply>>20)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		snippet := strings.TrimSpace(string(data))
		if len(snippet) > 200 {
			snippet = snippet[:200] + "…"
		}
		e := fmt.Sprintf("%s answered %d", what, resp.StatusCode)
		if snippet != "" {
			e += ": " + strings.ReplaceAll(snippet, c.key, "…")
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			e += fmt.Sprintf(" (check $%s)", c.f.KeyEnv)
		}
		return nil, errors.New(e)
	}
	return data, nil
}

// scrub keeps the key out of an error that quotes a request.
func scrub(err error, key string) error {
	if key == "" || !strings.Contains(err.Error(), key) {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), key, "…"))
}

// redact drops a URL's query, where a presigned URL keeps its signature.
func redact(raw string) string {
	s, _, _ := strings.Cut(raw, "?")
	return s
}

// lookup is a placeholder's value: name, or name.path into a step's.
func lookup(ref string, vals map[string]any) (any, bool) {
	root, path, dotted := strings.Cut(ref, ".")
	v, ok := vals[root]
	if !ok {
		return nil, false
	}
	if dotted {
		v = Pick(v, path, nil)
	}
	return v, v != nil
}

func fillWith(s string, vals map[string]any, esc func(name, v string) string) (string, []string) {
	var missing []string
	out := placeholderRE.ReplaceAllStringFunc(s, func(m string) string {
		ref := m[1 : len(m)-1]
		v, ok := lookup(ref, vals)
		t := Text(v)
		if !ok || t == "" {
			missing = append(missing, m)
			return ""
		}
		return esc(ref, t)
	})
	return out, missing
}

func fillText(s string, vals map[string]any) string {
	out, _ := fillWith(s, vals, func(_, v string) string { return v })
	return out
}

// fillPath fills a call's path: every value escaped but {project}, and an
// empty one an error, since a path with a hole in it asks for something
// else.
func fillPath(path string, vals map[string]any) (string, error) {
	out, missing := fillWith(path, vals, func(ref, v string) string {
		if ref == "project" {
			return v
		}
		return url.PathEscape(v)
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("%s is empty", strings.Join(missing, ", "))
	}
	return out, nil
}

// fillQuery fills a query, dropping a parameter that ends up empty.
func fillQuery(query string, vals map[string]any) string {
	if query == "" {
		return ""
	}
	var parts []string
	for _, kv := range strings.Split(query, "&") {
		k, v, _ := strings.Cut(kv, "=")
		filled := fillText(v, vals)
		if v != "" && filled == "" {
			continue
		}
		parts = append(parts, k+"="+url.QueryEscape(filled))
	}
	return strings.Join(parts, "&")
}

// fillBody fills a body's strings: one that is a single placeholder takes
// that value's own type, and is left out when it has none.
func fillBody(v any, vals map[string]any) (any, bool) {
	switch x := v.(type) {
	case string:
		if m := placeholderRE.FindString(x); m == x && m != "" {
			got, ok := lookup(m[1:len(m)-1], vals)
			return got, ok && Text(got) != ""
		}
		return fillText(x, vals), true
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			if f, ok := fillBody(e, vals); ok {
				out[k] = f
			}
		}
		return out, true
	case []any:
		var out []any
		for _, e := range x {
			if f, ok := fillBody(e, vals); ok {
				out = append(out, f)
			}
		}
		return out, true
	case []map[string]any:
		var out []any
		for _, e := range x {
			f, _ := fillBody(e, vals)
			out = append(out, f)
		}
		return out, true
	}
	return v, true
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return time.Time{}
	}
	if n > 1e12 {
		return time.UnixMilli(n)
	}
	return time.Unix(n, 0)
}

func firstSet(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

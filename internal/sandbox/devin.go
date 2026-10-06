package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Amitgb14/conch/internal/config"
)

// Devin Cloud runs each session on a VM of its own, with Devin working in
// it. conch makes a session through Devin's v3 API, tagged so it can find
// its own again, and reaches the VM with `devin ssh`, which wraps ssh to
// Devin's gateway and approves the connection with the CLI's own login —
// the API hands out no ssh credential of its own. So two things are
// needed: an API key (a service user's, cog_…) for the session, and a
// signed-in devin CLI for the way in.
//
// A session is never just a machine: it has to be given a task, and Devin
// works on it. conch gives it one that asks it to wait, so it spends as
// little as it can. Devin also puts a session to sleep when nothing has
// happened in it for a while, and a sleeping session's VM is not running;
// conch shows it stopped, and starting it wakes it.

// DefaultDevinURL is Devin's API.
const DefaultDevinURL = "https://api.devin.ai"

// devinPrompt is the task a session conch makes is given: Devin must be
// told something, and this asks it to leave the machine to the person.
const devinPrompt = "This session is a machine for conch, a terminal orchestrator: a person will connect over SSH and run their own tools in it. " +
	"Do not change anything in the machine or in any repository. Reply that the machine is ready, then wait."

// devinWake is the message that wakes a sleeping session: Devin resumes a
// session when it is sent a message, and has no other way to be asked.
const devinWake = "conch is reconnecting to this machine. There is nothing for you to do; reply that it is ready and wait."

// Devin is the provider.
type Devin struct {
	base   string
	keyEnv string
	key    string
	// platform is where a session runs: an OS the organization offers or
	// an outpost pool of its own. "" is the organization's default.
	platform string
	hc       *http.Client

	mu  sync.Mutex
	org string // from DEVIN_ORG_ID, or asked of the API once
}

// NewDevin returns a Devin Cloud provider for cfg.
func NewDevin(cfg config.ProviderCfg) *Devin {
	d := &Devin{keyEnv: cfg.APIKeyEnv, platform: strings.TrimSpace(cfg.Snapshot), hc: &http.Client{Timeout: time.Minute}}
	if d.keyEnv == "" {
		d.keyEnv = "DEVIN_API_KEY"
	}
	d.key = strings.TrimSpace(cfg.APIKey)
	if d.key == "" {
		d.key = strings.TrimSpace(os.Getenv(d.keyEnv))
	}
	d.org = strings.TrimSpace(os.Getenv("DEVIN_ORG_ID"))
	d.base = strings.TrimRight(firstSet(cfg.APIURL, os.Getenv("DEVIN_API_URL"), DefaultDevinURL), "/")
	return d
}

func (d *Devin) Name() string { return "devin" }

func (d *Devin) Check() error {
	if d.key == "" {
		return fmt.Errorf("%w: set $%s to a Devin API key (a service user's, cog_…), or put the key in Settings → Sandboxes", ErrNotConfigured, d.keyEnv)
	}
	return nil
}

// devinSession is a Devin session, as far as conch reads it.
type devinSession struct {
	ID           string   `json:"session_id"`
	Title        string   `json:"title"`
	Status       string   `json:"status"`
	StatusDetail string   `json:"status_detail"`
	Archived     bool     `json:"is_archived"`
	Tags         []string `json:"tags"`
	URL          string   `json:"url"`
	CreatedAt    int64    `json:"created_at"`
}

// sandbox maps Devin's statuses onto conch's. A session that has ended
// ("exit") is gone: nothing starts it again. A suspended one is asleep,
// with its VM kept, which is what conch calls stopped.
func (s devinSession) sandbox() Sandbox {
	state := State(s.Status)
	switch s.Status {
	case "new", "claimed", "resuming":
		state = StateStarting
	case "running":
		state = StateStarted
	case "suspended":
		state = StateStopped
	case "exit":
		state = StateDestroyed
	case "error":
		state = StateError
	}
	var created time.Time
	if s.CreatedAt > 0 {
		created = time.Unix(s.CreatedAt, 0)
	}
	sb := Sandbox{ID: s.ID, Name: firstSet(s.Title, s.ID), State: state, Created: created}
	if state == StateStopped || state == StateError {
		sb.Reason = s.StatusDetail // why it went to sleep: inactivity, out_of_credits…
	}
	if len(s.Tags) > 0 {
		sb.Labels = map[string]string{}
		for _, t := range s.Tags {
			sb.Labels[t] = ""
		}
	}
	return sb
}

func (d *Devin) Create(ctx context.Context, spec Spec) (Sandbox, error) {
	// Devin chooses the size of its VMs; a number would only be ignored.
	if spec.CPU > 0 || spec.Memory > 0 || spec.Disk > 0 {
		return Sandbox{}, errors.New("Devin chooses its machines' size: leave vCPUs, memory and disk empty, and put a platform or an outpost pool where the snapshot goes")
	}
	body := map[string]any{
		"prompt": devinPrompt,
		"title":  firstSet(spec.Name, "conch sandbox"),
		"tags":   devinTags(spec.Labels),
	}
	if p := firstSet(spec.Snapshot, d.platform); p != "" {
		body["platform"] = p
	}
	if len(spec.Env) > 0 {
		// Variables go in as the session's secrets, which Devin sets in
		// the VM's environment; sorted, so the request is the same each time.
		names := make([]string, 0, len(spec.Env))
		for k := range spec.Env {
			names = append(names, k)
		}
		sort.Strings(names)
		secrets := make([]map[string]any, 0, len(names))
		for _, k := range names {
			secrets = append(secrets, map[string]any{"key": k, "value": spec.Env[k], "sensitive": true})
		}
		body["session_secrets"] = secrets
	}
	var out devinSession
	if err := d.call(ctx, http.MethodPost, "/sessions", nil, body, &out); err != nil {
		return Sandbox{}, fmt.Errorf("create session: %w", err)
	}
	if out.ID == "" {
		return Sandbox{}, errors.New("create session: Devin named no session")
	}
	return d.wait(ctx, out.sandbox(), StateStarted)
}

// devinTags are the tags a new session carries: conch's own, so List can
// find it, and any labels asked for.
func devinTags(labels map[string]string) []string {
	tags := []string{Label}
	for k, v := range labels {
		if k == Label {
			continue
		}
		if v != "" {
			k += "=" + v
		}
		tags = append(tags, k)
	}
	sort.Strings(tags[1:])
	return tags
}

func (d *Devin) Get(ctx context.Context, id string) (Sandbox, error) {
	s, err := d.get(ctx, id)
	if err != nil {
		return Sandbox{}, err
	}
	return s.sandbox(), nil
}

func (d *Devin) get(ctx context.Context, id string) (devinSession, error) {
	if id == "" {
		return devinSession{}, ErrNotFound
	}
	var out devinSession
	if err := d.call(ctx, http.MethodGet, "/sessions/"+url.PathEscape(id), nil, nil, &out); err != nil {
		return devinSession{}, err
	}
	if out.ID == "" {
		return devinSession{}, ErrNotFound
	}
	return out, nil
}

// List returns the sessions conch made: the ones tagged with its label.
// Sessions that have ended are left out, as a deleted sandbox is.
func (d *Devin) List(ctx context.Context) ([]Sandbox, error) {
	var list []Sandbox
	cursor := ""
	for page := 0; ; page++ {
		if page == maxPages {
			return list, fmt.Errorf("list sessions: more than %d pages", maxPages)
		}
		q := url.Values{"tags": {Label}, "first": {"100"}}
		if cursor != "" {
			q.Set("after", cursor)
		}
		var out struct {
			Items     []devinSession `json:"items"`
			EndCursor string         `json:"end_cursor"`
			HasNext   bool           `json:"has_next_page"`
		}
		if err := d.call(ctx, http.MethodGet, "/sessions", q, nil, &out); err != nil {
			return list, fmt.Errorf("list sessions: %w", err)
		}
		for _, s := range out.Items {
			// The API filters by tag; this keeps to conch's own should a
			// server not, rather than listing every session in the org.
			if s.Status == "exit" || !hasTag(s.Tags, Label) {
				continue
			}
			list = append(list, s.sandbox())
		}
		if !out.HasNext || out.EndCursor == "" || out.EndCursor == cursor {
			return list, nil
		}
		cursor = out.EndCursor
	}
}

func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}

// Start wakes a sleeping session. Devin has no call for that: an archived
// session is unarchived, and a message wakes it.
func (d *Devin) Start(ctx context.Context, id string) (Sandbox, error) {
	s, err := d.get(ctx, id)
	if err != nil {
		return Sandbox{}, err
	}
	sb := s.sandbox()
	switch {
	case sb.State == StateStarted && !s.Archived:
		return sb, nil
	case sb.State.Going():
		return sb, fmt.Errorf("session %s has ended; Devin cannot start it again", id)
	}
	if s.Archived {
		if err := d.call(ctx, http.MethodPost, "/sessions/"+url.PathEscape(id)+"/unarchive", nil, map[string]any{}, nil); err != nil {
			return sb, fmt.Errorf("unarchive session: %w", err)
		}
	}
	if sb.State != StateStarted && !sb.State.comingUp() {
		if err := d.call(ctx, http.MethodPost, "/sessions/"+url.PathEscape(id)+"/messages", nil,
			map[string]any{"message": devinWake}, nil); err != nil {
			return sb, fmt.Errorf("wake session: %w", err)
		}
	}
	return d.wait(ctx, sb, StateStarted)
}

// Stop puts the session to sleep, keeping its VM. The API's way to do
// that is to archive it; Start unarchives it again.
func (d *Devin) Stop(ctx context.Context, id string) error {
	s, err := d.get(ctx, id)
	if err != nil {
		return err
	}
	sb := s.sandbox()
	switch {
	case sb.State.Going():
		return nil // ended: nothing is running to stop
	case sb.State == StateStopped && s.Archived:
		return nil
	}
	if !s.Archived {
		if err := d.call(ctx, http.MethodPost, "/sessions/"+url.PathEscape(id)+"/archive", nil, map[string]any{}, nil); err != nil {
			return fmt.Errorf("stop session: %w", err)
		}
	}
	_, err = d.wait(ctx, sb, StateStopped)
	return err
}

// Delete ends the session. Devin keeps an ended session's record, which
// conch no longer lists.
func (d *Devin) Delete(ctx context.Context, id string) error {
	if id == "" {
		return ErrNotFound
	}
	err := d.call(ctx, http.MethodDelete, "/sessions/"+url.PathEscape(id), nil, nil, nil)
	if err == nil || errors.Is(err, ErrNotFound) {
		return err
	}
	// Ending one that has already ended may be refused; it is gone either way.
	if s, gerr := d.get(ctx, id); errors.Is(gerr, ErrNotFound) {
		return ErrNotFound
	} else if gerr == nil && s.Status == "exit" {
		return nil
	}
	return fmt.Errorf("delete session: %w", err)
}

// devinGateway is Devin's ssh gateway, which a session's VM is reached
// through as devin-<id>.
const devinGateway = "ssh.devin.ai"

// SSHAccess is where the VM is, for completeness: plain ssh there asks
// for approval in a browser, so conch goes in with SSHCommand instead.
func (d *Devin) SSHAccess(ctx context.Context, id string) (Access, error) {
	if _, err := d.get(ctx, id); err != nil {
		return Access{}, err
	}
	user := id
	if !strings.HasPrefix(user, "devin-") {
		user = "devin-" + user
	}
	return Access{User: user, PlainUser: true, Host: devinGateway}, nil
}

// SSHCommand is `devin ssh ID`: the devin CLI approves the connection with
// its own login, which an API key cannot.
func (d *Devin) SSHCommand(id string) ([]string, error) {
	if id == "" {
		return nil, ErrNotFound
	}
	bin, err := devinBinary()
	if err != nil {
		return nil, err
	}
	return []string{bin, "ssh", id}, nil
}

// devinBinary finds the devin CLI: $CONCH_DEVIN, then PATH, then where its
// installer puts it — a server started from a desktop may have neither.
func devinBinary() (string, error) {
	if b := strings.TrimSpace(os.Getenv("CONCH_DEVIN")); b != "" {
		return b, nil
	}
	if b, err := exec.LookPath("devin"); err == nil {
		return b, nil
	}
	if home, err := os.UserHomeDir(); err == nil {
		b := filepath.Join(home, ".local", "bin", "devin")
		if fi, err := os.Stat(b); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return b, nil
		}
	}
	return "", errors.New("the devin CLI is needed to reach a Devin session: install it (conch agent install devin) and sign in with devin auth login")
}

// orgID is the organization sessions are made in: $DEVIN_ORG_ID, or the
// one Devin says the key's sessions run in.
func (d *Devin) orgID(ctx context.Context) (string, error) {
	d.mu.Lock()
	org := d.org
	d.mu.Unlock()
	if org != "" {
		return org, nil
	}
	var self struct {
		OrgID         string `json:"org_id"`
		SessionsOrgID string `json:"devin_sessions_org_id"`
	}
	if err := d.request(ctx, http.MethodGet, d.base+"/v3/self", nil, &self); err != nil {
		return "", fmt.Errorf("which organization: %w", err)
	}
	org = firstSet(self.SessionsOrgID, self.OrgID)
	if org == "" {
		return "", errors.New("Devin named no organization for this key to make sessions in; set $DEVIN_ORG_ID")
	}
	d.mu.Lock()
	d.org = org
	d.mu.Unlock()
	return org, nil
}

// wait polls until the session reaches want, or the context ends.
func (d *Devin) wait(ctx context.Context, s Sandbox, want State) (Sandbox, error) {
	for {
		switch {
		case s.State == want:
			return s, nil
		case s.State == StateError || (s.State.Going() && want != StateDestroyed):
			return s, &FailedError{ID: s.ID, State: s.State, Reason: s.Reason}
		case want == StateStarted && s.State == StateStopped && s.Reason != "" && s.Reason != "inactivity" && s.Reason != "user_request":
			// Asleep for a reason waiting won't mend: out of credits, a
			// usage limit, a declined payment.
			return s, &FailedError{ID: s.ID, State: s.State, Reason: s.Reason}
		}
		select {
		case <-ctx.Done():
			return s, fmt.Errorf("session %s is still %s: %w", s.ID, s.State, ctx.Err())
		case <-time.After(pollEvery):
		}
		got, err := d.Get(ctx, s.ID)
		if err != nil {
			return s, err
		}
		s = got
	}
}

// call talks to the organization's part of the API: path is under
// /v3/organizations/ORG.
func (d *Devin) call(ctx context.Context, method, path string, q url.Values, body, out any) error {
	if err := d.Check(); err != nil {
		return err
	}
	org, err := d.orgID(ctx)
	if err != nil {
		return err
	}
	u := d.base + "/v3/organizations/" + url.PathEscape(org) + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return d.request(ctx, method, u, body, out)
}

func (d *Devin) request(ctx context.Context, method, u string, body, out any) error {
	if err := d.Check(); err != nil {
		return err
	}
	var rd io.Reader
	if body != nil {
		enc, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(enc)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+d.key)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := d.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return err
	}
	if len(data) > maxBody {
		return fmt.Errorf("devin: reply to %s is over %d bytes", method, maxBody)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &DevinError{Status: resp.StatusCode, Message: devinMessage(data), keyEnv: d.keyEnv}
	}
	if out == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	return json.Unmarshal(data, out)
}

// devinMessage is the reason out of an error reply: a problem+json body,
// whose detail says what happened and whose title is the kind of thing.
func devinMessage(data []byte) string {
	var e struct {
		Detail json.RawMessage `json:"detail"`
		Title  string          `json:"title"`
	}
	if json.Unmarshal(data, &e) != nil {
		return strings.TrimSpace(string(data))
	}
	// detail is a string, or for a validation failure a list of them.
	var detail string
	if json.Unmarshal(e.Detail, &detail) != nil && len(e.Detail) > 0 {
		detail = string(e.Detail)
	}
	return firstSet(detail, e.Title)
}

// DevinError is what Devin's API answered with.
type DevinError struct {
	Status  int
	Message string
	keyEnv  string
}

func (e *DevinError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	msg = fmt.Sprintf("devin: %s (%d)", msg, e.Status)
	switch e.Status {
	case http.StatusUnauthorized:
		msg += "; check the key in $" + e.keyEnv
	case http.StatusForbidden:
		msg += "; the key's service user needs permission to manage sessions"
	case http.StatusTooManyRequests:
		msg += "; Devin is being asked too often, or the plan allows no more sessions at once"
	}
	return msg
}

// Is makes a 404 match ErrNotFound.
func (e *DevinError) Is(target error) bool {
	return target == ErrNotFound && e.Status == http.StatusNotFound
}

// PlaceHint says what goes where the snapshot does: Devin runs a session
// on a platform the organization offers, or on an outpost pool.
func (d *Devin) PlaceHint() string { return "a platform (e.g. windows) or an outpost pool" }

// Note says what is unlike other providers' machines: Devin works in the
// session and bills for it, the way in is the devin CLI, and an idle
// session is put to sleep.
func (d *Devin) Note() string {
	return "Devin is given a task asking it to wait, and bills ACUs while the session runs. conch goes in with the devin CLI, which must be signed in (devin auth login). Devin puts an idle session to sleep; start it again to wake it."
}

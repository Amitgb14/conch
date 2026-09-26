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
	"strconv"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/config"
)

// DefaultDaytonaURL is Daytona's API.
const DefaultDaytonaURL = "https://app.daytona.io/api"

// daytonaSSHHost is where Daytona's ssh gateway listens when an access
// reply doesn't say.
const daytonaSSHHost = "ssh.app.daytona.io"

// pollEvery is how often a wait asks for the sandbox's state; tests shorten it.
var pollEvery = 2 * time.Second

// maxBody bounds a reply read from the API.
const maxBody = 4 << 20

// maxPages bounds List, against a cursor that never ends.
const maxPages = 50

// Daytona manages sandboxes through Daytona's REST API.
type Daytona struct {
	base   string
	keyEnv string
	key    string
	target string
	snap   string
	hc     *http.Client
}

// NewDaytona returns a Daytona provider for cfg, reading the key and any
// unset endpoint or region from the environment.
func NewDaytona(cfg config.DaytonaCfg) *Daytona {
	d := &Daytona{keyEnv: cfg.APIKeyEnv, snap: cfg.Snapshot, hc: &http.Client{Timeout: time.Minute}}
	if d.keyEnv == "" {
		d.keyEnv = "DAYTONA_API_KEY"
	}
	d.key = strings.TrimSpace(os.Getenv(d.keyEnv))
	d.base = strings.TrimRight(firstSet(cfg.APIURL, os.Getenv("DAYTONA_API_URL"), DefaultDaytonaURL), "/")
	d.target = firstSet(cfg.Target, os.Getenv("DAYTONA_TARGET"))
	return d
}

func firstSet(vs ...string) string {
	for _, v := range vs {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

func (d *Daytona) Name() string { return "daytona" }

func (d *Daytona) Check() error {
	if d.key == "" {
		return fmt.Errorf("%w: set $%s to a Daytona API key", ErrNotConfigured, d.keyEnv)
	}
	return nil
}

// daytonaSandbox is the API's sandbox, as far as conch reads it.
type daytonaSandbox struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	State       State             `json:"state"`
	ErrorReason string            `json:"errorReason"`
	Target      string            `json:"target"`
	CPU         int               `json:"cpu"`
	Memory      int               `json:"memory"`
	Disk        int               `json:"disk"`
	Labels      map[string]string `json:"labels"`
	CreatedAt   string            `json:"createdAt"`
	// DesiredState is where Daytona is taking it: "destroyed" for a
	// sandbox deleted moments ago that still reports its old state.
	DesiredState State `json:"desiredState"`
}

func (s daytonaSandbox) sandbox() Sandbox {
	created, _ := time.Parse(time.RFC3339, s.CreatedAt)
	return Sandbox{ID: s.ID, Name: s.Name, State: s.State, Reason: s.ErrorReason, Target: s.Target,
		CPU: s.CPU, Memory: s.Memory, Disk: s.Disk, Labels: s.Labels, Created: created}
}

func (d *Daytona) Create(ctx context.Context, spec Spec) (Sandbox, error) {
	labels := map[string]string{}
	for k, v := range spec.Labels {
		labels[k] = v
	}
	labels[Label] = "1"
	body := map[string]any{
		"labels": labels,
		// Sent even when 0: Daytona's own default stops the sandbox, and
		// the agents in it, 15 minutes after the TUI lets go.
		"autoStopInterval": max(spec.AutoStop, 0),
	}
	set := func(k string, v any, ok bool) {
		if ok {
			body[k] = v
		}
	}
	snap := firstSet(spec.Snapshot, d.snap)
	set("name", spec.Name, spec.Name != "")
	set("snapshot", snap, snap != "")
	set("target", d.target, d.target != "")
	set("cpu", spec.CPU, spec.CPU > 0)
	set("memory", spec.Memory, spec.Memory > 0)
	set("disk", spec.Disk, spec.Disk > 0)
	set("env", spec.Env, len(spec.Env) > 0)
	var out daytonaSandbox
	if err := d.call(ctx, http.MethodPost, "/sandbox", nil, body, &out); err != nil {
		return Sandbox{}, fmt.Errorf("create sandbox: %w", err)
	}
	if out.ID == "" {
		return Sandbox{}, errors.New("create sandbox: Daytona returned no sandbox id")
	}
	return d.wait(ctx, out.sandbox(), StateStarted)
}

func (d *Daytona) Get(ctx context.Context, id string) (Sandbox, error) {
	if id == "" {
		return Sandbox{}, ErrNotFound
	}
	var out daytonaSandbox
	if err := d.call(ctx, http.MethodGet, "/sandbox/"+url.PathEscape(id), nil, nil, &out); err != nil {
		return Sandbox{}, err
	}
	return out.sandbox(), nil
}

func (d *Daytona) List(ctx context.Context) ([]Sandbox, error) {
	var all []Sandbox
	cursor := ""
	for page := 0; ; page++ {
		if page == maxPages {
			return all, fmt.Errorf("list sandboxes: more than %d pages", maxPages)
		}
		q := url.Values{"labels": {`{"` + Label + `":"1"}`}, "limit": {"200"}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		var out struct {
			Items      []daytonaSandbox `json:"items"`
			NextCursor *string          `json:"nextCursor"`
		}
		if err := d.call(ctx, http.MethodGet, "/sandbox", q, nil, &out); err != nil {
			return all, fmt.Errorf("list sandboxes: %w", err)
		}
		for _, s := range out.Items {
			if s.DesiredState == StateDestroyed {
				continue // deleted; Daytona just hasn't finished
			}
			all = append(all, s.sandbox())
		}
		if out.NextCursor == nil || *out.NextCursor == "" || *out.NextCursor == cursor {
			return all, nil
		}
		cursor = *out.NextCursor
	}
}

func (d *Daytona) Start(ctx context.Context, id string) (Sandbox, error) {
	s, err := d.Get(ctx, id)
	if err != nil {
		return Sandbox{}, err
	}
	switch s.State {
	case StateStarted:
		return s, nil
	case StateStopping:
		// Starting a sandbox on its way down is refused; let it get there.
		if s, err = d.wait(ctx, s, StateStopped); err != nil {
			return s, err
		}
	}
	if !s.State.comingUp() {
		if err := d.call(ctx, http.MethodPost, "/sandbox/"+url.PathEscape(id)+"/start", nil, nil, nil); err != nil {
			return s, fmt.Errorf("start sandbox: %w", err)
		}
	}
	return d.wait(ctx, s, StateStarted)
}

func (d *Daytona) Stop(ctx context.Context, id string) error {
	s, err := d.Get(ctx, id)
	if err != nil {
		return err
	}
	switch s.State {
	case StateStopped, StateArchived:
		return nil
	case StateStopping:
	default:
		if err := d.call(ctx, http.MethodPost, "/sandbox/"+url.PathEscape(id)+"/stop", nil, nil, nil); err != nil {
			return fmt.Errorf("stop sandbox: %w", err)
		}
	}
	_, err = d.wait(ctx, s, StateStopped)
	return err
}

func (d *Daytona) Delete(ctx context.Context, id string) error {
	if id == "" {
		return ErrNotFound
	}
	if err := d.call(ctx, http.MethodDelete, "/sandbox/"+url.PathEscape(id), nil, nil, nil); err != nil {
		return fmt.Errorf("delete sandbox: %w", err)
	}
	return nil
}

func (d *Daytona) SSHAccess(ctx context.Context, id string) (Access, error) {
	if id == "" {
		return Access{}, ErrNotFound
	}
	var out struct {
		Token      string `json:"token"`
		ExpiresAt  string `json:"expiresAt"`
		SSHCommand string `json:"sshCommand"`
	}
	if err := d.call(ctx, http.MethodPost, "/sandbox/"+url.PathEscape(id)+"/ssh-access", nil, nil, &out); err != nil {
		return Access{}, fmt.Errorf("ssh access: %w", err)
	}
	if out.Token == "" {
		return Access{}, errors.New("ssh access: Daytona returned no token")
	}
	a := Access{User: out.Token, Host: daytonaSSHHost}
	if host, port, ok := parseSSHCommand(out.SSHCommand, out.Token); ok {
		a.Host, a.Port = host, port
	}
	a.Expires, _ = time.Parse(time.RFC3339, out.ExpiresAt)
	return a, nil
}

// parseSSHCommand reads the host and port from a command like
// "ssh -p 2222 TOKEN@host", so a self-hosted Daytona's gateway is found.
func parseSSHCommand(cmd, token string) (host string, port int, ok bool) {
	f := strings.Fields(cmd)
	if len(f) < 2 || f[0] != "ssh" {
		return "", 0, false
	}
	for i := 1; i < len(f); i++ {
		switch {
		case f[i] == "-p" && i+1 < len(f):
			p, err := strconv.Atoi(f[i+1])
			if err != nil || p <= 0 || p > 65535 {
				return "", 0, false
			}
			port = p
			i++
		case strings.HasPrefix(f[i], token+"@"):
			host = strings.TrimPrefix(f[i], token+"@")
		}
	}
	if host == "" || strings.ContainsAny(host, "@/ ") {
		return "", 0, false
	}
	return host, port, true
}

// wait polls until the sandbox reaches want, fails, or ctx ends. Brief
// trouble reaching the API (a restart, rate limits) doesn't end it.
func (d *Daytona) wait(ctx context.Context, s Sandbox, want State) (Sandbox, error) {
	// The first look is at once: a start or stop often lands before it.
	transient, delay := 0, time.Duration(0)
	for {
		switch {
		case s.State == want:
			return s, nil
		case want == StateStopped && s.State == StateArchived:
			return s, nil
		case s.State.failed():
			return s, &FailedError{ID: s.ID, State: s.State, Reason: s.Reason}
		}
		select {
		case <-ctx.Done():
			return s, fmt.Errorf("waiting for sandbox %s to be %s (it is %s): %w", s.ID, want, s.State, ctx.Err())
		case <-time.After(delay):
		}
		delay = pollEvery
		next, err := d.Get(ctx, s.ID)
		var api *APIError
		switch {
		case err == nil:
			s, transient = next, 0
		case ctx.Err() != nil:
			return s, fmt.Errorf("waiting for sandbox %s to be %s (it is %s): %w", s.ID, want, s.State, ctx.Err())
		case errors.As(err, &api) && api.temporary() && transient < 5:
			transient++
			delay = max(delay, api.RetryAfter)
		default:
			return s, err
		}
	}
}

// APIError is a request Daytona refused.
type APIError struct {
	Status     int
	Message    string
	RetryAfter time.Duration
	keyEnv     string
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	msg = fmt.Sprintf("daytona: %s (%d)", msg, e.Status)
	if e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden {
		msg += "; check the key in $" + e.keyEnv
	}
	return msg
}

// Is makes a 404 match ErrNotFound.
func (e *APIError) Is(target error) bool {
	return target == ErrNotFound && e.Status == http.StatusNotFound
}

func (e *APIError) temporary() bool {
	return e.Status == http.StatusTooManyRequests || e.Status >= 500
}

// call sends one request. A nil body sends none; a nil out ignores the reply.
func (d *Daytona) call(ctx context.Context, method, path string, q url.Values, body, out any) error {
	if err := d.Check(); err != nil {
		return err
	}
	u := d.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
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
		return fmt.Errorf("daytona: reply to %s %s is over %d bytes", method, path, maxBody)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &APIError{Status: resp.StatusCode, Message: errorMessage(data), RetryAfter: retryAfter(resp.Header), keyEnv: d.keyEnv}
	}
	if out == nil || len(bytes.TrimSpace(data)) == 0 {
		if out != nil {
			return fmt.Errorf("daytona: empty reply to %s %s", method, path)
		}
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("daytona: reading reply to %s %s: %w", method, path, err)
	}
	return nil
}

// errorMessage reads the message from an error reply: {"message": "…"},
// or a list of messages for a rejected body.
func errorMessage(data []byte) string {
	var e struct {
		Message json.RawMessage `json:"message"`
		Error   string          `json:"error"`
	}
	if json.Unmarshal(data, &e) != nil {
		s := strings.TrimSpace(string(data))
		if len(s) > 200 || strings.HasPrefix(s, "<") {
			return "" // an HTML page from a proxy says nothing useful
		}
		return s
	}
	var one string
	if json.Unmarshal(e.Message, &one) == nil && one != "" {
		return one
	}
	var many []string
	if json.Unmarshal(e.Message, &many) == nil && len(many) > 0 {
		return strings.Join(many, "; ")
	}
	return e.Error
}

// retryAfter reads Retry-After, or the per-limit Retry-After-<name> headers
// Daytona sends, in seconds.
func retryAfter(h http.Header) time.Duration {
	var d time.Duration
	for k, vs := range h {
		if !strings.HasPrefix(strings.ToLower(k), "retry-after") || len(vs) == 0 {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimSpace(vs[0])); err == nil && n > 0 {
			d = max(d, min(time.Duration(n)*time.Second, time.Minute))
		}
	}
	return d
}

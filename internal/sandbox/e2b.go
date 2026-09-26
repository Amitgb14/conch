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
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/config"
)

// E2B runs sandboxes as Firecracker microVMs reached through its own API.
// Unlike Daytona it offers no ssh: its agent inside the sandbox (envd)
// runs processes and terminals over ConnectRPC instead, which is what
// conch bridges its protocol over. This file is the platform side —
// making, listing, pausing and ending sandboxes; the bridge lives with
// the transport.
//
// A sandbox has a time to live rather than an idle timeout: E2B ends it
// when the clock runs out, so conch pushes the clock back while it is
// connected (Refresh) and asks for a pause rather than a kill at the end
// of it, since a paused sandbox keeps its filesystem *and* its memory and
// costs nothing.

// DefaultE2BURL is E2B's platform API.
const DefaultE2BURL = "https://api.e2b.app"

// defaultE2BTemplate is what a sandbox is made from when nothing names a
// template: E2B's own image with Node, Python and the usual tools.
const defaultE2BTemplate = "base"

// e2bTTL is how long a new sandbox is given, and how far Refresh pushes
// the end back. E2B's own default is five minutes, which is too short for
// an agent left working; conch keeps it topped up instead.
const e2bTTL = time.Hour

// E2B is the provider.
type E2B struct {
	base   string
	keyEnv string
	key    string
	tmpl   string
	hc     *http.Client
	// fromSettings says the key came from config.toml rather than the
	// environment.
	fromSettings bool
}

// NewE2B returns an E2B provider for cfg, reading the key and any unset
// endpoint from the environment.
func NewE2B(cfg config.ProviderCfg) *E2B {
	e := &E2B{keyEnv: cfg.APIKeyEnv, tmpl: cfg.Snapshot, hc: &http.Client{Timeout: time.Minute}}
	if e.keyEnv == "" {
		e.keyEnv = "E2B_API_KEY"
	}
	e.key = strings.TrimSpace(cfg.APIKey)
	if e.key == "" {
		e.key = strings.TrimSpace(os.Getenv(e.keyEnv))
	} else {
		e.fromSettings = true
	}
	e.base = strings.TrimRight(firstSet(cfg.APIURL, os.Getenv("E2B_API_URL"), DefaultE2BURL), "/")
	return e
}

func (e *E2B) Name() string { return "e2b" }

func (e *E2B) Check() error {
	if e.key == "" {
		return fmt.Errorf("%w: set $%s to an E2B API key, or put the key in Settings → Sandboxes", ErrNotConfigured, e.keyEnv)
	}
	return nil
}

// e2bSandbox is E2B's sandbox, as far as conch reads it. Create and list
// answer with different shapes of the same thing, so both are read here.
type e2bSandbox struct {
	SandboxID  string            `json:"sandboxID"`
	TemplateID string            `json:"templateID"`
	Alias      string            `json:"alias"`
	State      string            `json:"state"` // "running" or "paused"
	StartedAt  string            `json:"startedAt"`
	EndAt      string            `json:"endAt"`
	CPUCount   int               `json:"cpuCount"`
	MemoryMB   int               `json:"memoryMB"`
	DiskSizeMB int               `json:"diskSizeMB"`
	Metadata   map[string]string `json:"metadata"`
	// EnvdAccessToken and Domain are how the sandbox itself is reached;
	// they come back from create and get, not from the list.
	EnvdAccessToken string `json:"envdAccessToken"`
	Domain          string `json:"domain"`
}

// sandbox maps E2B's shape onto conch's. E2B reports running or paused,
// where conch says started or stopped, and sizes in MB where conch says
// GiB.
func (s e2bSandbox) sandbox() Sandbox {
	started, _ := time.Parse(time.RFC3339, s.StartedAt)
	state := StateStopped
	switch s.State {
	case "running":
		state = StateStarted
	case "paused":
		state = StateStopped
	case "": // a create that hasn't said yet is on its way up
		state = StateStarting
	default:
		state = State(s.State)
	}
	return Sandbox{
		ID: s.SandboxID, Name: firstSet(s.Alias, s.SandboxID), State: state,
		CPU: s.CPUCount, Memory: s.MemoryMB / 1024, Disk: s.DiskSizeMB / 1024,
		Labels: s.Metadata, Created: started,
	}
}

// Access is how the sandbox itself is reached: E2B gives no ssh, so this
// carries envd's own address and token instead of an ssh destination.
// Host is the sandbox's domain and User its access token, keeping to the
// shape the rest of conch already passes around without showing.
func (s e2bSandbox) access() Access {
	end, _ := time.Parse(time.RFC3339, s.EndAt)
	return Access{User: s.EnvdAccessToken, Host: firstSet(s.Domain, "e2b.app"), Expires: end}
}

func (e *E2B) Create(ctx context.Context, spec Spec) (Sandbox, error) {
	labels := map[string]string{}
	for k, v := range spec.Labels {
		labels[k] = v
	}
	labels[Label] = "1" // conch's own, so List finds it again
	body := map[string]any{
		"templateID": firstSet(spec.Snapshot, e.tmpl, defaultE2BTemplate),
		"timeout":    int(ttlOf(spec).Seconds()),
		"metadata":   labels,
		// Ending is for conch to decide, so a sandbox whose clock runs out
		// is paused rather than killed: its files and memory are kept, and
		// a paused sandbox costs nothing.
		"autoPause":       true,
		"autoPauseMemory": true,
	}
	if len(spec.Env) > 0 {
		body["envVars"] = spec.Env
	}
	var out e2bSandbox
	if err := e.call(ctx, http.MethodPost, "/v2/sandboxes", nil, body, &out); err != nil {
		return Sandbox{}, fmt.Errorf("create sandbox: %w", err)
	}
	s := out.sandbox()
	if s.ID == "" {
		return s, errors.New("create sandbox: e2b named no sandbox")
	}
	if s.State == StateStarting {
		s.State = StateStarted // E2B answers once it is up
	}
	return s, nil
}

// ttlOf is how long a new sandbox is given. A spec that asks to be stopped
// after so many idle minutes is taken at its word, since E2B's clock is
// the only thing it has; otherwise conch's own hour, kept topped up while
// it is connected.
func ttlOf(spec Spec) time.Duration {
	if spec.AutoStop > 0 {
		return time.Duration(spec.AutoStop) * time.Minute
	}
	return e2bTTL
}

func (e *E2B) Get(ctx context.Context, id string) (Sandbox, error) {
	if id == "" {
		return Sandbox{}, ErrNotFound
	}
	var out e2bSandbox
	if err := e.call(ctx, http.MethodGet, "/sandboxes/"+url.PathEscape(id), nil, nil, &out); err != nil {
		return Sandbox{}, err
	}
	return out.sandbox(), nil
}

// List returns the sandboxes conch made, running or paused. E2B filters
// by the metadata conch sets, so somebody else's sandboxes in the same
// account are left alone.
func (e *E2B) List(ctx context.Context) ([]Sandbox, error) {
	q := url.Values{
		"metadata": {Label + "=1"},
		"state":    {"running", "paused"},
	}
	var out []e2bSandbox
	if err := e.call(ctx, http.MethodGet, "/v2/sandboxes", q, nil, &out); err != nil {
		return nil, fmt.Errorf("list sandboxes: %w", err)
	}
	list := make([]Sandbox, 0, len(out))
	for _, s := range out {
		list = append(list, s.sandbox())
	}
	return list, nil
}

// Start resumes a paused sandbox. One already running is left alone, so
// starting twice is not an error.
func (e *E2B) Start(ctx context.Context, id string) (Sandbox, error) {
	s, err := e.Get(ctx, id)
	if err != nil {
		return Sandbox{}, err
	}
	if s.State == StateStarted {
		return s, nil
	}
	var out e2bSandbox
	body := map[string]any{"timeout": int(e2bTTL.Seconds())}
	if err := e.call(ctx, http.MethodPost, "/v2/sandboxes/"+url.PathEscape(id)+"/connect", nil, body, &out); err != nil {
		return s, fmt.Errorf("start sandbox: %w", err)
	}
	got := out.sandbox()
	if got.State == StateStarting {
		got.State = StateStarted
	}
	return got, nil
}

// Stop pauses the sandbox: its filesystem and memory are kept, so what
// ran there is still running when it comes back.
func (e *E2B) Stop(ctx context.Context, id string) error {
	if err := e.call(ctx, http.MethodPost, "/sandboxes/"+url.PathEscape(id)+"/pause", nil, nil, nil); err != nil {
		if errors.Is(err, ErrNotFound) {
			return err
		}
		// Already paused, or still being paused: the state asked for.
		if s, gerr := e.Get(ctx, id); gerr == nil && s.State == StateStopped {
			return nil
		}
		return fmt.Errorf("pause sandbox: %w", err)
	}
	return nil
}

// Delete ends the sandbox for good. One E2B has already forgotten counts
// as deleted, so cleaning up after a delete made elsewhere works.
func (e *E2B) Delete(ctx context.Context, id string) error {
	if id == "" {
		return ErrNotFound
	}
	err := e.call(ctx, http.MethodDelete, "/sandboxes/"+url.PathEscape(id), nil, nil, nil)
	if err == nil || errors.Is(err, ErrNotFound) {
		return err
	}
	if _, gerr := e.Get(ctx, id); errors.Is(gerr, ErrNotFound) {
		return ErrNotFound
	}
	return fmt.Errorf("delete sandbox: %w", err)
}

// Refresh pushes the sandbox's end back, so one that is being worked in
// isn't ended under the agent. E2B's timeout is from now, not added to
// what is left, so this sets the whole of it each time.
func (e *E2B) Refresh(ctx context.Context, id string, d time.Duration) error {
	if d <= 0 {
		d = e2bTTL
	}
	body := map[string]any{"timeout": int(d.Seconds())}
	if err := e.call(ctx, http.MethodPost, "/sandboxes/"+url.PathEscape(id)+"/timeout", nil, body, nil); err != nil {
		return fmt.Errorf("keep sandbox alive: %w", err)
	}
	return nil
}

// SSHAccess says what E2B cannot do. conch reaches its sandboxes through
// envd instead; Reach says so, and the transport asks for envdAccess.
func (e *E2B) SSHAccess(ctx context.Context, id string) (Access, error) {
	return Access{}, fmt.Errorf("e2b sandboxes have no ssh: conch reaches them through their own agent")
}

// envdAccess is where the sandbox's own agent is and what it takes to
// talk to it: the token is fresh every time, as Daytona's ssh access is.
func (e *E2B) envdAccess(ctx context.Context, id string) (Access, error) {
	if id == "" {
		return Access{}, ErrNotFound
	}
	var out e2bSandbox
	if err := e.call(ctx, http.MethodGet, "/sandboxes/"+url.PathEscape(id), nil, nil, &out); err != nil {
		return Access{}, err
	}
	if out.EnvdAccessToken == "" {
		return Access{}, fmt.Errorf("e2b gave no access token for sandbox %s", id)
	}
	return out.access(), nil
}

// call talks to E2B's platform API. Errors come back as E2BError, which
// says what went wrong without ever quoting the key.
func (e *E2B) call(ctx context.Context, method, path string, q url.Values, body, out any) error {
	if err := e.Check(); err != nil {
		return err
	}
	u := e.base + path
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
	req.Header.Set("X-API-Key", e.key)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := e.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return err
	}
	if len(data) > maxBody {
		return fmt.Errorf("e2b: reply to %s %s is over %d bytes", method, path, maxBody)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &E2BError{Status: resp.StatusCode, Message: errorMessage(data), keyEnv: e.keyEnv}
	}
	if out == nil || len(bytes.TrimSpace(data)) == 0 {
		if out != nil {
			return fmt.Errorf("e2b: empty reply to %s %s", method, path)
		}
		return nil
	}
	return json.Unmarshal(data, out)
}

// E2BError is what E2B's API answered with.
type E2BError struct {
	Status  int
	Message string
	keyEnv  string
}

func (e *E2BError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	msg = fmt.Sprintf("e2b: %s (%d)", msg, e.Status)
	if e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden {
		msg += "; check the key in $" + e.keyEnv
	}
	return msg
}

// Is makes a 404 match ErrNotFound.
func (e *E2BError) Is(target error) bool {
	return target == ErrNotFound && e.Status == http.StatusNotFound
}

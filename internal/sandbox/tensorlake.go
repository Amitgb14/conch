package sandbox

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/config"
)

// Tensorlake runs sandboxes as microVMs behind an ssh gateway of its own:
// sandbox.tensorlake.ai, with the sandbox's id as the user name, which is
// what the gateway routes on. The gateway lets in the ssh keys registered
// with the person's Tensorlake account — `tl sbx ssh keys add` — and that
// takes a login of the person's own, not an API key, so unlike boat.dev
// conch cannot authorize a key itself: it offers the key it would have
// authorized, and Note says to register it once.
//
// Only a named sandbox can be suspended and resumed, so conch names every
// one it makes, "conch-…", which is also how List tells conch's own apart:
// Tensorlake keeps no labels.

// DefaultTensorlakeURL is Tensorlake's API.
const DefaultTensorlakeURL = "https://api.tensorlake.ai"

// DefaultTensorlakeSSH is the gateway every sandbox is reached through.
const DefaultTensorlakeSSH = "sandbox.tensorlake.ai"

// tensorlakePrefix starts the name of every sandbox conch makes.
const tensorlakePrefix = "conch-"

// Tensorlake's limits on a root disk, in GiB.
const (
	tensorlakeMinDisk = 10
	tensorlakeMaxDisk = 100
)

// Tensorlake is the provider.
type Tensorlake struct {
	base    string
	sshHost string
	keyEnv  string
	key     string
	image   string
	hc      *http.Client
}

// NewTensorlake returns a Tensorlake provider for cfg, reading the key and
// any unset endpoint from the environment.
func NewTensorlake(cfg config.ProviderCfg) *Tensorlake {
	t := &Tensorlake{keyEnv: cfg.APIKeyEnv, image: cfg.Snapshot, hc: &http.Client{Timeout: time.Minute}}
	if t.keyEnv == "" {
		t.keyEnv = "TENSORLAKE_API_KEY"
	}
	t.key = strings.TrimSpace(cfg.APIKey)
	if t.key == "" {
		t.key = strings.TrimSpace(os.Getenv(t.keyEnv))
	}
	// The API's base: an address given with /sandboxes on the end — the
	// one Tensorlake's docs show beside create — would have every call
	// sent to /sandboxes/sandboxes, which answers 405.
	t.base = strings.TrimRight(firstSet(cfg.APIURL, os.Getenv("TENSORLAKE_API_URL"), DefaultTensorlakeURL), "/")
	t.base = strings.TrimRight(strings.TrimSuffix(t.base, "/sandboxes"), "/")
	t.sshHost = firstSet(os.Getenv("TENSORLAKE_SSH_HOST"), DefaultTensorlakeSSH)
	return t
}

func (t *Tensorlake) Name() string { return "tensorlake" }

func (t *Tensorlake) Check() error {
	if t.key == "" {
		return fmt.Errorf("%w: set $%s to a Tensorlake API key, or put the key in Settings → Sandboxes", ErrNotConfigured, t.keyEnv)
	}
	return nil
}

// Note says what has to be done once before conch can get in.
func (t *Tensorlake) Note() string {
	return "Tensorlake lets in the ssh keys registered with your account, which conch cannot do with an API key: register yours once with Tensorlake's CLI — pip install tensorlake, tl login, then env -u TENSORLAKE_API_KEY tl sbx ssh keys add ~/.ssh/id_ed25519.pub (conch offers ~/.ssh/id_ed25519, id_ecdsa or id_rsa, the first it finds, else its own key)."
}

// AccountKey says the gateway takes a key registered with the account, so
// the transport offers the one it would otherwise have authorized.
func (t *Tensorlake) AccountKey() bool { return true }

// tensorlakeSandbox is Tensorlake's sandbox, as far as conch reads it.
type tensorlakeSandbox struct {
	ID                string  `json:"id"`
	SandboxID         string  `json:"sandbox_id"` // what create answers with
	Name              *string `json:"name"`
	Image             string  `json:"image"`
	Status            string  `json:"status"`
	State             string  `json:"state"` // create's 202 says state
	PendingReason     *string `json:"pending_reason"`
	TerminationReason *string `json:"termination_reason"`
	ErrorDetails      *string `json:"error_details"`
	CreatedAt         int64   `json:"created_at"` // milliseconds
	Resources         struct {
		CPUs     float64 `json:"cpus"`
		MemoryMB int     `json:"memory_mb"`
		DiskMB   int     `json:"disk_mb"`
	} `json:"resources"`
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// sandbox maps Tensorlake's states onto conch's: suspended is stopped,
// terminated is gone, and a resume that failed is an error, with
// Tensorlake's diagnosis as the reason.
func (s tensorlakeSandbox) sandbox() Sandbox {
	st := firstSet(s.Status, s.State)
	state := State(st)
	switch st {
	case "pending":
		state = StateStarting
	case "running":
		state = StateStarted
	case "suspending":
		state = StateStopping
	case "suspended":
		state = StateStopped
	case "terminated":
		state = StateDestroyed
	case "resume_failed":
		state = StateError
	}
	var created time.Time
	if s.CreatedAt > 0 {
		created = time.UnixMilli(s.CreatedAt)
	}
	id := firstSet(s.ID, s.SandboxID)
	return Sandbox{
		ID: id, Name: firstSet(deref(s.Name), id), State: state,
		Reason: firstSet(deref(s.ErrorDetails), deref(s.TerminationReason), deref(s.PendingReason)),
		CPU:    int(s.Resources.CPUs), Memory: s.Resources.MemoryMB / 1024, Disk: s.Resources.DiskMB / 1024,
		Created: created,
	}
}

// tensorlakeName is a sandbox name of conch's: its prefix, what the person
// called it as far as Tensorlake takes letters, and a few random ones,
// since a name is unique in a project and the same label can be asked for
// twice.
var tensorlakeNameChars = regexp.MustCompile(`[^a-z0-9-]+`)

func tensorlakeName(label string) string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	slug := strings.Trim(tensorlakeNameChars.ReplaceAllString(strings.ToLower(label), "-"), "-")
	if len(slug) > 32 {
		slug = strings.Trim(slug[:32], "-")
	}
	if slug == "" {
		return tensorlakePrefix + hex.EncodeToString(b)
	}
	return tensorlakePrefix + slug + "-" + hex.EncodeToString(b)
}

func (t *Tensorlake) Create(ctx context.Context, spec Spec) (Sandbox, error) {
	// Tensorlake passes variables in only as secrets it already holds, by
	// name; refusing says so, where dropping them would leave an agent
	// without the key it was promised.
	if len(spec.Env) > 0 {
		return Sandbox{}, errors.New("a Tensorlake sandbox takes no variables when a sandbox is made: keep them as Tensorlake secrets instead, and remove them from env in [sandbox.tensorlake]")
	}
	if spec.Disk > 0 && (spec.Disk < tensorlakeMinDisk || spec.Disk > tensorlakeMaxDisk) {
		return Sandbox{}, fmt.Errorf("a disk on Tensorlake is %d to %d GiB, not %d", tensorlakeMinDisk, tensorlakeMaxDisk, spec.Disk)
	}
	body := map[string]any{
		"name": tensorlakeName(spec.Name),
		// conch waits for it itself, as for every provider, so the wait
		// can end with the context rather than with Tensorlake's.
		"wait": false,
		// 0 is the longest the plan allows: conch's own idle watch stops
		// a sandbox nobody is using.
		"timeout_secs": 0,
	}
	if spec.AutoStop > 0 {
		body["timeout_secs"] = spec.AutoStop * 60
	}
	if img := firstSet(spec.Snapshot, t.image); img != "" {
		body["image"] = img
	}
	res := map[string]any{}
	if spec.CPU > 0 {
		res["cpus"] = spec.CPU
	}
	if spec.Memory > 0 {
		res["memory_mb"] = spec.Memory * 1024
	}
	if spec.Disk > 0 {
		res["disk_mb"] = spec.Disk * 1024
	}
	if len(res) > 0 {
		body["resources"] = res
	}
	var out tensorlakeSandbox
	if err := t.call(ctx, http.MethodPost, "/sandboxes", nil, body, &out); err != nil {
		return Sandbox{}, fmt.Errorf("create sandbox: %w", err)
	}
	s := out.sandbox()
	if s.ID == "" {
		return Sandbox{}, errors.New("create sandbox: Tensorlake named no sandbox")
	}
	if s.Name == s.ID {
		s.Name, _ = body["name"].(string)
	}
	return t.wait(ctx, s, StateStarted)
}

func (t *Tensorlake) Get(ctx context.Context, id string) (Sandbox, error) {
	if id == "" {
		return Sandbox{}, ErrNotFound
	}
	var out tensorlakeSandbox
	if err := t.call(ctx, http.MethodGet, "/sandboxes/"+url.PathEscape(id), nil, nil, &out); err != nil {
		return Sandbox{}, err
	}
	s := out.sandbox()
	if s.ID == "" {
		return Sandbox{}, ErrNotFound
	}
	return s, nil
}

// List returns the sandboxes conch made: the ones whose name it gave.
func (t *Tensorlake) List(ctx context.Context) ([]Sandbox, error) {
	var list []Sandbox
	cursor := ""
	for page := 0; ; page++ {
		if page == maxPages {
			return list, fmt.Errorf("list sandboxes: more than %d pages", maxPages)
		}
		q := url.Values{"limit": {"100"}}
		if cursor != "" {
			q.Set("cursor", cursor)
			q.Set("direction", "forward")
		}
		var out struct {
			Sandboxes  []tensorlakeSandbox `json:"sandboxes"`
			NextCursor *string             `json:"next_cursor"`
		}
		if err := t.call(ctx, http.MethodGet, "/sandboxes", q, nil, &out); err != nil {
			return list, fmt.Errorf("list sandboxes: %w", err)
		}
		for _, s := range out.Sandboxes {
			if strings.HasPrefix(deref(s.Name), tensorlakePrefix) {
				list = append(list, s.sandbox())
			}
		}
		next := deref(out.NextCursor)
		if next == "" || next == cursor || len(out.Sandboxes) == 0 {
			return list, nil
		}
		cursor = next
	}
}

// Start resumes a suspended sandbox, keeping its disk and its memory.
func (t *Tensorlake) Start(ctx context.Context, id string) (Sandbox, error) {
	s, err := t.Get(ctx, id)
	if err != nil {
		return Sandbox{}, err
	}
	if s.State == StateStarted {
		return s, nil
	}
	// A terminated sandbox cannot come back; say why it ended.
	if s.State.Going() {
		return s, &FailedError{ID: s.ID, State: s.State, Reason: s.Reason}
	}
	if !s.State.comingUp() {
		// A failed resume is retried by asking again, which is also what
		// clears Tensorlake's diagnosis of it.
		if err := t.call(ctx, http.MethodPost, "/sandboxes/"+url.PathEscape(id)+"/resume", nil, nil, nil); err != nil {
			return s, fmt.Errorf("resume sandbox: %w", err)
		}
		s.State = StateStarting
	}
	return t.wait(ctx, s, StateStarted)
}

// Stop suspends the sandbox: Tensorlake keeps it as it stands, to resume.
func (t *Tensorlake) Stop(ctx context.Context, id string) error {
	s, err := t.Get(ctx, id)
	if err != nil {
		return err
	}
	if s.State == StateStopped {
		return nil
	}
	if s.State != StateStopping {
		if err := t.call(ctx, http.MethodPost, "/sandboxes/"+url.PathEscape(id)+"/suspend", nil, nil, nil); err != nil {
			return fmt.Errorf("suspend sandbox: %w", err)
		}
		s.State = StateStopping
	}
	_, err = t.wait(ctx, s, StateStopped)
	return err
}

// Delete terminates the sandbox. Tensorlake answers success for one that
// had already ended, so only one it has never heard of is ErrNotFound.
func (t *Tensorlake) Delete(ctx context.Context, id string) error {
	if id == "" {
		return ErrNotFound
	}
	err := t.call(ctx, http.MethodDelete, "/sandboxes/"+url.PathEscape(id), nil, nil, nil)
	if err == nil || errors.Is(err, ErrNotFound) {
		return err
	}
	return fmt.Errorf("delete sandbox: %w", err)
}

// SSHAccess is the gateway, with the sandbox's id as the user: not a
// secret, since the key registered with the account is what lets one in.
func (t *Tensorlake) SSHAccess(ctx context.Context, id string) (Access, error) {
	if id == "" {
		return Access{}, ErrNotFound
	}
	if _, err := t.Get(ctx, id); err != nil {
		return Access{}, err
	}
	return Access{User: id, PlainUser: true, Host: t.sshHost}, nil
}

// wait polls until the sandbox reaches want, or the context ends.
func (t *Tensorlake) wait(ctx context.Context, s Sandbox, want State) (Sandbox, error) {
	for {
		switch {
		case s.State == want:
			return s, nil
		case s.State.failed():
			return s, &FailedError{ID: s.ID, State: s.State, Reason: s.Reason}
		}
		select {
		case <-ctx.Done():
			return s, fmt.Errorf("sandbox %s is still %s: %w", s.ID, s.State, ctx.Err())
		case <-time.After(pollEvery):
		}
		got, err := t.Get(ctx, s.ID)
		if err != nil {
			return s, err
		}
		s = got
	}
}

// call talks to Tensorlake's API. Its errors are plain text.
func (t *Tensorlake) call(ctx context.Context, method, path string, q url.Values, body, out any) error {
	if err := t.Check(); err != nil {
		return err
	}
	u := t.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
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
	req.Header.Set("Authorization", "Bearer "+t.key)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := t.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return err
	}
	if len(data) > maxBody {
		return fmt.Errorf("tensorlake: reply to %s %s is over %d bytes", method, path, maxBody)
	}
	if resp.StatusCode == http.StatusNotFound && method != http.MethodPost {
		return ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &TensorlakeError{Status: resp.StatusCode, Message: tensorlakeMessage(data), keyEnv: t.keyEnv}
	}
	if out == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	return json.Unmarshal(data, out)
}

// tensorlakeMessage is the reason out of an error reply: plain text for
// the sandbox calls, a JSON message from the proxy.
func tensorlakeMessage(data []byte) string {
	var e struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if json.Unmarshal(data, &e) == nil && firstSet(e.Message, e.Error) != "" {
		return firstSet(e.Message, e.Error)
	}
	msg := strings.TrimSpace(string(data))
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	return msg
}

// TensorlakeError is what Tensorlake's API answered with.
type TensorlakeError struct {
	Status  int
	Message string
	keyEnv  string
}

func (e *TensorlakeError) Error() string {
	msg := fmt.Sprintf("tensorlake: %d", e.Status)
	if e.Message != "" {
		msg += " " + e.Message
	}
	if e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden {
		msg += fmt.Sprintf(" (check $%s, or the key in Settings → Sandboxes)", e.keyEnv)
	}
	return msg
}

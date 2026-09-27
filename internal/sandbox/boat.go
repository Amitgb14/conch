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
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/config"
)

// Boat (boat.dev) runs sandboxes as whole Ubuntu machines with an sshd of
// their own, which is the shape conch already knows: a key is authorized
// through the API, and everything after that is ssh. Stopping one keeps
// its disk and costs nothing; starting it again is a resume onto a new
// machine, so its address changes and is read afresh every time.

// DefaultBoatURL is boat.dev's API.
const DefaultBoatURL = "https://boat.dev/api/v1"

// boatTTL is how long a new sandbox is given. Boat counts from when the
// sandbox was made rather than from the last thing that happened in it,
// and its free trial allows two hours, so that is what conch asks for:
// a longer life is a setting (auto_stop, in minutes) rather than a
// create that fails on the plan most people start on. conch's own idle
// watch stops a sandbox nobody is using well before this.
const boatTTL = 2 * time.Hour

// boatAddressWait is how long conch waits for a new sandbox to be given an
// address: boat answers ready a moment before the machine has one. Tests
// shorten it.
var boatAddressWait = 90 * time.Second

// boatUser is who you are when you ssh into a boat sandbox.
const boatUser = "user"

// Boat is the provider.
type Boat struct {
	base   string
	keyEnv string
	key    string
	size   string
	// autoStop is the life a new sandbox is given, in minutes; 0 is boatTTL.
	autoStop int
	hc       *http.Client
	// fromSettings says the key came from config.toml rather than the
	// environment.
	fromSettings bool
}

// NewBoat returns a boat.dev provider for cfg.
func NewBoat(cfg config.ProviderCfg) *Boat {
	b := &Boat{keyEnv: cfg.APIKeyEnv, size: cfg.Snapshot, autoStop: cfg.AutoStop, hc: &http.Client{Timeout: time.Minute}}
	if b.keyEnv == "" {
		b.keyEnv = "BOAT_API_KEY"
	}
	b.key = strings.TrimSpace(cfg.APIKey)
	if b.key == "" {
		b.key = strings.TrimSpace(os.Getenv(b.keyEnv))
	} else {
		b.fromSettings = true
	}
	b.base = strings.TrimRight(firstSet(cfg.APIURL, os.Getenv("BOAT_API_URL"), DefaultBoatURL), "/")
	return b
}

func (b *Boat) Name() string { return "boat" }

func (b *Boat) Check() error {
	if b.key == "" {
		return fmt.Errorf("%w: set $%s to a boat.dev API key, or put the key in Settings → Sandboxes", ErrNotConfigured, b.keyEnv)
	}
	return nil
}

// boatSandbox is boat.dev's sandbox, as far as conch reads it.
type boatSandbox struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"`
	Error string `json:"error"`
	Type  string `json:"type"`
	VCPU  int    `json:"vcpu"`
	MemGB int    `json:"memoryGB"`
	IP    string `json:"ip"`
	// SSHEndpoint is host:port, set only when the machine has no public
	// IPv4 of its own. When it is there it is what to connect to.
	SSHEndpoint string `json:"sshEndpoint"`
	Subdomain   string `json:"subdomain"`
	CreatedAt   string `json:"createdAt"`
	SetupStatus string `json:"setupStatus"`
	SetupError  string `json:"setupError"`
}

// sandbox maps boat's states onto conch's. Boat has more of them than
// conch needs: everything on the way up is starting, everything on the
// way down is stopping, and its own "running" means its own agent is
// busy — not that the machine is up — so it is not read as a state here.
func (s boatSandbox) sandbox() Sandbox {
	created, _ := time.Parse(time.RFC3339, s.CreatedAt)
	state := State(s.State)
	switch s.State {
	case "ready", "idle", "running":
		state = StateStarted
	case "init", "provisioning", "provisioned", "cloning":
		state = StateStarting
	case "archiving":
		state = StateStopping
	case "archived":
		state = StateStopped
	case "error", "cancelled":
		state = StateError
	}
	return Sandbox{
		ID: s.ID, Name: firstSet(s.Name, s.ID), State: state, Reason: firstSet(s.Error, s.SetupError),
		CPU: s.VCPU, Memory: s.MemGB, Created: created,
	}
}

// boatReply is the envelope every answer comes in.
type boatReply struct {
	OK      bool         `json:"ok"`
	Type    string       `json:"type"`
	Sandbox *boatSandbox `json:"sandbox"`
}

func (b *Boat) Create(ctx context.Context, spec Spec) (Sandbox, error) {
	// Boat's machines come in named sizes rather than a number of CPUs, so
	// numbers are refused instead of quietly ignored: the size goes where
	// the snapshot does.
	if spec.CPU > 0 || spec.Memory > 0 || spec.Disk > 0 {
		return Sandbox{}, fmt.Errorf("boat.dev machines come in sizes, not numbers: put %s where the snapshot goes, and leave vCPUs, memory and disk empty",
			strings.Join(boatSizeNames, ", "))
	}
	body := map[string]any{"ttlSeconds": int(ttlFor(spec).Seconds())}
	if size := firstSet(spec.Snapshot, b.size); size != "" {
		// A named snapshot is deployed with "from"; a size is a "type".
		if boatSizes[size] {
			body["type"] = size
		} else {
			body["from"] = size
		}
	}
	if len(spec.Env) > 0 {
		body["env"] = spec.Env
	}
	var out boatReply
	err := b.call(ctx, http.MethodPost, "/sandboxes", nil, body, &out)
	// A plan has a longest life it allows, and says so rather than
	// clamping: ask again for the longest it named instead of handing back
	// a refusal the person can do nothing about.
	if shorter, ok := shorterTTL(err, body["ttlSeconds"]); ok {
		body["ttlSeconds"] = shorter
		out = boatReply{}
		err = b.call(ctx, http.MethodPost, "/sandboxes", nil, body, &out)
	}
	if err != nil {
		return Sandbox{}, fmt.Errorf("create sandbox: %w", err)
	}
	if out.Sandbox == nil || out.Sandbox.ID == "" {
		return Sandbox{}, errors.New("create sandbox: boat named no sandbox")
	}
	// Boat answers before the machine is up; conch waits, as it does for
	// every provider, so what comes back can be reached.
	return b.wait(ctx, out.Sandbox.sandbox(), StateStarted)
}

// boatSizes are the machine sizes; anything else in the snapshot setting
// is taken for the name of a snapshot to deploy from.
// boatSizeNames are the sizes create takes. boat.dev sells an xlarge as
// well, but its create only accepts these three — one has to be allocated
// to the account first — so conch does not offer a size boat would refuse.
var boatSizeNames = []string{"small", "default", "large"}

// SizeNames says boat's machines are sized by name, so nothing offers a
// number of vCPUs for one.
func (b *Boat) SizeNames() []string { return append([]string(nil), boatSizeNames...) }

// Life is how long a new sandbox is given, so the dialog that makes one
// can say it: boat's clock, not conch's.
func (b *Boat) Life() time.Duration { return ttlFor(Spec{AutoStop: b.autoStop}) }

var boatSizes = func() map[string]bool {
	m := map[string]bool{}
	for _, s := range boatSizeNames {
		m[s] = true
	}
	return m
}()

// ttlSecondsRefused reads the longest life a refusal allows: boat says
// what the plan permits, in seconds or in hours.
var (
	ttlSecondsRefused = regexp.MustCompile(`(?i)(\d{2,8})\s*seconds`)
	ttlHoursRefused   = regexp.MustCompile(`(?i)(\d{1,4})\s*hours?`)
)

// boatMaxTTL is the longest life boat allows at all: 30 days.
const boatMaxTTL = 30 * 24 * time.Hour

// shorterTTL is the life to ask for again when a refusal was about the
// length of it, and ok is false for any other error — or for a limit that
// is not shorter than what was asked, which would only fail again.
func shorterTTL(err error, asked any) (int, bool) {
	var berr *BoatError
	if !errors.As(err, &berr) || berr.Status < 400 || berr.Status > 499 {
		return 0, false
	}
	msg := berr.Message
	// The free trial's refusal is trial_auto_stop_required, whose message
	// need not mention a ttl at all, so the code counts too.
	if !strings.Contains(strings.ToLower(msg), "ttl") && !strings.Contains(strings.ToLower(msg), "auto-stop") &&
		berr.Code != "trial_auto_stop_required" {
		return 0, false
	}
	allowed := int(boatFreeTTL.Seconds()) // a limit conch cannot read: the free trial's
	if m := ttlSecondsRefused.FindStringSubmatch(msg); m != nil {
		allowed, _ = strconv.Atoi(m[1])
	} else if m := ttlHoursRefused.FindStringSubmatch(msg); m != nil {
		hours, _ := strconv.Atoi(m[1])
		allowed = hours * 3600
	}
	was, _ := asked.(int)
	if allowed < 60 || was <= allowed {
		return 0, false
	}
	return allowed, true
}

// boatFreeTTL is what boat's free trial allows a sandbox, and what conch
// falls back to when a refusal about the length names no number.
const boatFreeTTL = 2 * time.Hour

// ttlFor is how long a new sandbox is given. Boat's clock runs from when
// it was made, not from the last thing that happened in it, so a spec
// that asks to be stopped when idle is not what this is; conch's own idle
// watch does that.
func ttlFor(spec Spec) time.Duration {
	if spec.AutoStop > 0 {
		return min(time.Duration(spec.AutoStop)*time.Minute, boatMaxTTL)
	}
	return boatTTL
}

func (b *Boat) Get(ctx context.Context, id string) (Sandbox, error) {
	if id == "" {
		return Sandbox{}, ErrNotFound
	}
	var out boatReply
	if err := b.call(ctx, http.MethodGet, "/sandboxes/"+url.PathEscape(id), nil, nil, &out); err != nil {
		return Sandbox{}, err
	}
	if out.Sandbox == nil {
		return Sandbox{}, ErrNotFound
	}
	return out.Sandbox.sandbox(), nil
}

// List returns the account's sandboxes. Boat has no labels, so conch
// cannot tell the ones it made from the ones you made yourself; the
// machines it knows about are the ones in its own catalog.
func (b *Boat) List(ctx context.Context) ([]Sandbox, error) {
	var list []Sandbox
	cursor := ""
	for page := 0; ; page++ {
		if page == maxPages {
			return list, fmt.Errorf("list sandboxes: more than %d pages", maxPages)
		}
		q := url.Values{"limit": {"100"}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		var out struct {
			Sandboxes []boatSandbox `json:"sandboxes"`
			PageInfo  struct {
				NextCursor string `json:"nextCursor"`
				HasMore    bool   `json:"hasMore"`
			} `json:"pageInfo"`
		}
		if err := b.call(ctx, http.MethodGet, "/sandboxes", q, nil, &out); err != nil {
			return list, fmt.Errorf("list sandboxes: %w", err)
		}
		for _, s := range out.Sandboxes {
			list = append(list, s.sandbox())
		}
		if !out.PageInfo.HasMore || out.PageInfo.NextCursor == "" || out.PageInfo.NextCursor == cursor {
			return list, nil
		}
		cursor = out.PageInfo.NextCursor
	}
}

// Start brings a stopped sandbox back. Boat has no start: a stopped one
// is resumed onto a new machine, keeping its id and its disk, which is
// why its address must be read again afterwards.
func (b *Boat) Start(ctx context.Context, id string) (Sandbox, error) {
	s, err := b.Get(ctx, id)
	if err != nil {
		return Sandbox{}, err
	}
	if s.State == StateStarted {
		return s, nil
	}
	if !s.State.comingUp() {
		var out boatReply
		body := map[string]any{"ttlSeconds": int(boatTTL.Seconds())}
		path := "/sandboxes/" + url.PathEscape(id) + "/resume"
		rerr := b.call(ctx, http.MethodPost, path, nil, body, &out)
		if shorter, ok := shorterTTL(rerr, body["ttlSeconds"]); ok {
			body["ttlSeconds"] = shorter
			rerr = b.call(ctx, http.MethodPost, path, nil, body, &out)
		}
		if rerr != nil {
			return s, fmt.Errorf("resume sandbox: %w", rerr)
		}
	}
	return b.wait(ctx, s, StateStarted)
}

// Stop archives the sandbox: boat snapshots it first and refuses to stop
// one whose snapshot is failing, so nothing is lost. That refusal is
// passed on rather than forced.
func (b *Boat) Stop(ctx context.Context, id string) error {
	s, err := b.Get(ctx, id)
	if err != nil {
		return err
	}
	if s.State == StateStopped {
		return nil
	}
	if s.State != StateStopping {
		if err := b.call(ctx, http.MethodPost, "/sandboxes/"+url.PathEscape(id)+"/stop", nil, map[string]any{}, nil); err != nil {
			return fmt.Errorf("stop sandbox: %w", err)
		}
	}
	_, err = b.wait(ctx, s, StateStopped)
	return err
}

// Delete throws the sandbox and its snapshots away. Boat wants the id
// repeated in a header before it will, which conch does rather than
// asking twice: it has already asked.
func (b *Boat) Delete(ctx context.Context, id string) error {
	if id == "" {
		return ErrNotFound
	}
	err := b.callWith(ctx, http.MethodDelete, "/sandboxes/"+url.PathEscape(id), nil, nil, nil,
		map[string]string{"X-Ascii-Confirm-Delete": id})
	if err == nil || errors.Is(err, ErrNotFound) {
		return err
	}
	if _, gerr := b.Get(ctx, id); errors.Is(gerr, ErrNotFound) {
		return ErrNotFound
	}
	return fmt.Errorf("delete sandbox: %w", err)
}

// SSHAccess is where to ssh, once a key has been authorized: boat's
// sandboxes have an sshd of their own, so there is no token in this —
// AuthorizeKey is what makes the way in.
//
// A sandbox that has just come up may have no address for a moment — boat
// reports it once the machine has one — so this waits for it rather than
// failing a create that was going to work.
func (b *Boat) SSHAccess(ctx context.Context, id string) (Access, error) {
	if id == "" {
		return Access{}, ErrNotFound
	}
	var out boatReply
	deadline := time.Now().Add(boatAddressWait)
	for {
		out = boatReply{}
		if err := b.call(ctx, http.MethodGet, "/sandboxes/"+url.PathEscape(id), nil, nil, &out); err != nil {
			return Access{}, err
		}
		if out.Sandbox == nil {
			return Access{}, ErrNotFound
		}
		if out.Sandbox.IP != "" || strings.TrimSpace(out.Sandbox.SSHEndpoint) != "" {
			break
		}
		if time.Now().After(deadline) {
			return Access{}, fmt.Errorf("sandbox %s has no address yet (it is %s)", id, out.Sandbox.sandbox().State)
		}
		select {
		case <-ctx.Done():
			return Access{}, fmt.Errorf("sandbox %s has no address yet: %w", id, ctx.Err())
		case <-time.After(pollEvery):
		}
	}
	a := Access{User: boatUser, PlainUser: true, Host: out.Sandbox.IP}
	// A machine with no public IPv4 of its own is reached through a
	// forwarder, which is host:port and not the address in ip.
	if ep := strings.TrimSpace(out.Sandbox.SSHEndpoint); ep != "" {
		host, port, ok := strings.Cut(ep, ":")
		if !ok {
			return Access{}, fmt.Errorf("boat gave an ssh endpoint conch cannot read: %q", ep)
		}
		n, err := strconv.Atoi(port)
		if err != nil {
			return Access{}, fmt.Errorf("boat gave an ssh endpoint conch cannot read: %q", ep)
		}
		a.Host, a.Port = host, n
	}
	return a, nil
}

// AuthorizeKey lets a public key in. Boat takes the key itself rather
// than handing out a secret, so conch's own key opens the sandbox and the
// private half never leaves this computer.
func (b *Boat) AuthorizeKey(ctx context.Context, id, publicKey string) error {
	if id == "" {
		return ErrNotFound
	}
	if strings.TrimSpace(publicKey) == "" {
		return errors.New("no public key to authorize")
	}
	body := map[string]any{"key": strings.TrimSpace(publicKey)}
	if err := b.call(ctx, http.MethodPost, "/sandboxes/"+url.PathEscape(id)+"/sshkey", nil, body, nil); err != nil {
		return fmt.Errorf("authorize the ssh key: %w", err)
	}
	return nil
}

// PreviewURL exposes a port and returns its link. Boat's link is sticky —
// the same port answers at the same address next time — and carries its
// own token unless it was made public, so expires is not something it
// takes.
func (b *Boat) PreviewURL(ctx context.Context, id string, port int, _ time.Duration) (string, error) {
	if id == "" {
		return "", ErrNotFound
	}
	if port < 1 || port > 65535 {
		return "", fmt.Errorf("port %d: a port is 1 to 65535", port)
	}
	var out struct {
		URL string `json:"url"`
	}
	body := map[string]any{"port": port, "title": "conch"}
	if err := b.call(ctx, http.MethodPost, "/sandboxes/"+url.PathEscape(id)+"/host", nil, body, &out); err != nil {
		return "", fmt.Errorf("preview link: %w", err)
	}
	if out.URL == "" {
		return "", fmt.Errorf("boat gave no link for port %d", port)
	}
	return out.URL, nil
}

// Usage is what boat says the sandbox has cost: machine time while it was
// running, which is all boat charges for — a stopped sandbox keeps its
// disk for nothing.
func (b *Boat) Usage(ctx context.Context, id string, from, to time.Time) (Usage, error) {
	if id == "" {
		return Usage{}, ErrNotFound
	}
	q := url.Values{
		"since": {from.UTC().Format(time.RFC3339)},
		"until": {to.UTC().Format(time.RFC3339)},
	}
	var out struct {
		Seconds int     `json:"seconds"`
		Dollars float64 `json:"dollars"`
		Running bool    `json:"running"`
	}
	if err := b.call(ctx, http.MethodGet, "/sandboxes/"+url.PathEscape(id)+"/usage", q, nil, &out); err != nil {
		return Usage{}, fmt.Errorf("usage: %w", err)
	}
	if out.Seconds == 0 && out.Dollars == 0 {
		return Usage{From: from, To: to}, nil // nothing to report
	}
	// Boat gives a total rather than the stretches it came from, so the
	// whole window is the one period there is to show. Its size is read
	// from the sandbox, so the period says what was running rather than
	// looking like a stopped one: boat charges for machine time only.
	u := Usage{Known: true, Cost: out.Dollars, From: from, To: to}
	period := UsagePeriod{From: to.Add(-time.Duration(out.Seconds) * time.Second), To: to, Cost: out.Dollars}
	if s, err := b.Get(ctx, id); err == nil {
		period.CPU, period.MemGiB = s.CPU, s.Memory
	}
	u.Periods = []UsagePeriod{period}
	return u, nil
}

// Snapshot keeps the sandbox under a name, to deploy others from. Boat
// takes a fresh capture of a running sandbox first, which takes minutes;
// the name is ready when it says it is.
func (b *Boat) Snapshot(ctx context.Context, id, name string) error {
	if id == "" {
		return ErrNotFound
	}
	if strings.TrimSpace(name) == "" {
		return errors.New("a snapshot needs a name")
	}
	body := map[string]any{"sandboxId": id, "name": strings.TrimSpace(name)}
	if err := b.call(ctx, http.MethodPost, "/named-snapshots", nil, body, nil); err != nil {
		return fmt.Errorf("snapshot %s: %w", name, err)
	}
	return nil
}

// Snapshots lists the named ones, which are the ones you can deploy from.
func (b *Boat) Snapshots(ctx context.Context) ([]Snap, error) {
	var out struct {
		Snapshots []struct {
			Name      string `json:"name"`
			State     string `json:"state"`
			Type      string `json:"type"`
			CreatedAt string `json:"createdAt"`
		} `json:"snapshots"`
	}
	if err := b.call(ctx, http.MethodGet, "/named-snapshots", nil, nil, &out); err != nil {
		return nil, fmt.Errorf("list snapshots: %w", err)
	}
	list := make([]Snap, 0, len(out.Snapshots))
	for _, s := range out.Snapshots {
		if s.Name == "" {
			continue
		}
		created, _ := time.Parse(time.RFC3339, s.CreatedAt)
		list = append(list, Snap{Name: s.Name, State: s.State, Size: s.Type, Created: created})
	}
	return list, nil
}

// ForgetSnapshot releases a named snapshot. Boat keeps ten of them, so
// this is how you make room for another.
func (b *Boat) ForgetSnapshot(ctx context.Context, name string) error {
	if strings.TrimSpace(name) == "" {
		return ErrNotFound
	}
	if err := b.call(ctx, http.MethodDelete, "/named-snapshots/"+url.PathEscape(name), nil, nil, nil); err != nil {
		if errors.Is(err, ErrNotFound) {
			return err
		}
		return fmt.Errorf("forget snapshot %s: %w", name, err)
	}
	return nil
}

// wait polls until the sandbox reaches want, or the context ends. Boat
// answers its lifecycle calls before the machine has moved, so every one
// of them waits here.
func (b *Boat) wait(ctx context.Context, s Sandbox, want State) (Sandbox, error) {
	for {
		switch {
		case s.State == want:
			return s, nil
		case s.State == StateError || s.State == "cancelled":
			return s, &FailedError{ID: s.ID, State: s.State, Reason: s.Reason}
		}
		select {
		case <-ctx.Done():
			return s, fmt.Errorf("sandbox %s is still %s: %w", s.ID, s.State, ctx.Err())
		case <-time.After(pollEvery):
		}
		got, err := b.Get(ctx, s.ID)
		if err != nil {
			return s, err
		}
		s = got
	}
}

func (b *Boat) call(ctx context.Context, method, path string, q url.Values, body, out any) error {
	return b.callWith(ctx, method, path, q, body, out, nil)
}

// callWith talks to boat.dev, with headers some calls need. Its answers
// carry an ok flag and an error object; the status is what conch acts on,
// and the message is what it shows.
func (b *Boat) callWith(ctx context.Context, method, path string, q url.Values, body, out any, headers map[string]string) error {
	if err := b.Check(); err != nil {
		return err
	}
	u := b.base + path
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
	req.Header.Set("Authorization", "Bearer "+b.key)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := b.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return err
	}
	if len(data) > maxBody {
		return fmt.Errorf("boat: reply to %s %s is over %d bytes", method, path, maxBody)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg, code := boatMessage(data)
		return &BoatError{Status: resp.StatusCode, Message: msg, Code: code, keyEnv: b.keyEnv}
	}
	if out == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	return json.Unmarshal(data, out)
}

// boatMessage is the reason out of an error reply, and the code boat put
// on it: the code is what conch can act on, the message what it shows.
func boatMessage(data []byte) (msg, code string) {
	var e struct {
		Message string `json:"message"`
		Error   struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
		Code string `json:"code"`
	}
	if json.Unmarshal(data, &e) != nil {
		return strings.TrimSpace(string(data)), ""
	}
	msg, code = firstSet(e.Error.Message, e.Message), firstSet(e.Error.Code, e.Code)
	if code != "" && !strings.Contains(msg, code) {
		msg = firstSet(msg, code)
	}
	return msg, code
}

// BoatError is what boat.dev's API answered with.
type BoatError struct {
	Status  int
	Message string
	// Code is boat's own name for the refusal, e.g. billing_required.
	Code   string
	keyEnv string
}

// boatAdvice is what to do about the refusals a person meets, by boat's
// own code: the message alone says what happened, not what would mend it.
var boatAdvice = map[string]string{
	"billing_required":                "boat.dev wants a plan or a payment method on the account before it will make sandboxes",
	"account_not_ready":               "the boat.dev account is not ready yet — finish setting it up in the dashboard",
	"ambiguous_org":                   "the key reaches more than one organisation; say which in boat.dev's dashboard",
	"trial_auto_stop_required":        "the free trial allows a sandbox two hours at most; conch asks for that",
	"trial_machine_class_not_allowed": "the free trial allows the small and default sizes only",
	"limit_reached":                   "as many sandboxes as the plan allows are running (two on the free trial): stop or delete one",
	"daily_limit_reached":             "as many sandboxes as the plan allows have been started today",
	"member_limit_reached":            "as many sandboxes as the plan allows are running for this member",
	"rate_limited":                    "boat.dev is being asked too often; try again in a moment",
	"invalid_env":                     "boat.dev keeps some variable names for itself; rename the one it refused",
	"unknown_environment":             "no boat.dev environment of that name",
	"api_key_sandbox_forbidden":       "that key is scoped to one sandbox and cannot reach this one",
}

func (e *BoatError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	msg = fmt.Sprintf("boat: %s (%d)", msg, e.Status)
	if advice := boatAdvice[e.Code]; advice != "" {
		return msg + "; " + advice
	}
	switch e.Status {
	case http.StatusUnauthorized, http.StatusForbidden:
		msg += "; check the key in $" + e.keyEnv
	case http.StatusPaymentRequired:
		msg += "; boat.dev needs a plan before it will make sandboxes"
	case http.StatusTooManyRequests:
		msg += "; the plan allows only so many sandboxes at once"
	}
	return msg
}

// Is makes a 404 match ErrNotFound.
func (e *BoatError) Is(target error) bool {
	return target == ErrNotFound && e.Status == http.StatusNotFound
}

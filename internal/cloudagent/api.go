package cloudagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Contract is the conch-cloud contract version this agent speaks: sent as
// Conch-Contract on every call, and as cloud.contract.<Contract> among the
// capabilities in hello (PROTOCOL §1).
const Contract = "1.3.0"

// ContractCapability is how hello says which contract the VM implements.
const ContractCapability = "cloud.contract." + Contract

// VMHello is POST /v1/vm/hello's body (openapi.yaml VMHello).
type VMHello struct {
	ConchVersion string   `json:"conch_version"`
	Capabilities []string `json:"capabilities"`
	ServerKey    string   `json:"server_key"`
	WokeFrom     string   `json:"woke_from,omitempty"`
}

// What woke_from may say.
const (
	WokeBoot   = "boot"
	WokeDisk   = "disk"
	WokeMemory = "memory"
)

// VMConfig is what hello answers (openapi.yaml VMConfig).
type VMConfig struct {
	MachineID   string      `json:"machine_id"`
	RelayURL    string      `json:"relay_url"`
	RoomSecret  string      `json:"room_secret"`
	Sleep       SleepPolicy `json:"sleep"`
	GitProxy    string      `json:"git_proxy,omitempty"`
	EgressProxy string      `json:"egress_proxy,omitempty"`
}

// SleepPolicy is VMConfig.sleep. The control plane decides sleep; the VM
// only keeps it to say so in its log.
type SleepPolicy struct {
	IdleSeconds          int `json:"idle_seconds,omitempty"`
	UnattendedMaxSeconds int `json:"unattended_max_seconds,omitempty"`
	ArchiveAfterSeconds  int `json:"archive_after_seconds,omitempty"`
}

// AgentActivity is one agent in an activity report.
type AgentActivity struct {
	Agent   string `json:"agent"`
	State   string `json:"state"`
	Project string `json:"project,omitempty"`
}

// VMActivity is POST /v1/vm/activity's body.
type VMActivity struct {
	Agents          []AgentActivity `json:"agents"`
	ClientsAttached int             `json:"clients_attached"`
	ProcessesBusy   bool            `json:"processes_busy"`
}

// VMCommand is something the control plane asks the VM to do (1.1.0).
type VMCommand struct {
	Type   string `json:"type"`
	Reason string `json:"reason,omitempty"`
}

// CommandPauseAgents is the unattended cap's pause (PROTOCOL §5).
const CommandPauseAgents = "pause_agents"

type vmActivityResult struct {
	Commands []VMCommand `json:"commands"`
}

// PushTarget is one device subscription to encrypt a push to (PROTOCOL §8).
type PushTarget struct {
	DeviceID           string `json:"device_id"`
	Kind               string `json:"kind"`
	P256dh             string `json:"p256dh"`
	Auth               string `json:"auth"`
	MaxCiphertextBytes int    `json:"max_ciphertext_bytes"`
}

// PushPayload is one encrypted notification for one device.
type PushPayload struct {
	DeviceID   string `json:"device_id"`
	Ciphertext string `json:"ciphertext"`
}

// VMPush is POST /v1/vm/push's body.
type VMPush struct {
	Urgency  string        `json:"urgency,omitempty"`
	Payloads []PushPayload `json:"payloads"`
}

// Problem is an error the control plane answered (RFC 9457, openapi.yaml
// Problem).
type Problem struct {
	Status            int    `json:"status"`
	Type              string `json:"type"`
	Title             string `json:"title"`
	Detail            string `json:"detail,omitempty"`
	Code              string `json:"code"`
	ContractSupported string `json:"contract_supported,omitempty"`
	RetryAfter        int    `json:"retry_after,omitempty"`
}

func (p *Problem) Error() string {
	msg := p.Detail
	if msg == "" {
		msg = p.Title
	}
	if msg == "" {
		msg = http.StatusText(p.Status)
	}
	if p.Code != "" {
		return fmt.Sprintf("%d %s: %s", p.Status, p.Code, msg)
	}
	return fmt.Sprintf("%d: %s", p.Status, msg)
}

// retryable is a problem worth asking again about: the control plane
// being down or busy, not the request being wrong.
func (p *Problem) retryable() bool {
	return p.Status >= 500 || p.Status == http.StatusTooManyRequests || p.Status == http.StatusRequestTimeout
}

// ErrUnauthorized is the control plane refusing the machine token. Each
// inject mints a new token and the old one stops working at once, so this
// is what an agent left over from an earlier inject sees.
var ErrUnauthorized = errors.New("the control plane refused this machine's token")

// API is the control plane as the VM calls it: the machineToken calls of
// openapi.yaml, each with Conch-Contract and the token.
type API struct {
	Base string // boot.json's api
	HTTP *http.Client

	mu    sync.Mutex
	token string
}

// NewAPI is the API at base, called with token.
func NewAPI(base, token string, hc *http.Client) *API {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &API{Base: strings.TrimRight(base, "/"), HTTP: hc, token: token}
}

// SetToken replaces the machine token, after a new inject.
func (a *API) SetToken(token string) {
	a.mu.Lock()
	a.token = token
	a.mu.Unlock()
}

// Token is the machine token calls are made with: the git credential
// helper hands it to git, from memory.
func (a *API) Token() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.token
}

// maxBody is the most of an answer read: everything the VM endpoints
// answer is small.
const maxBody = 1 << 20

// do makes one call. out, when not nil, takes a 200's JSON body. A
// problem answer is returned as *Problem (or ErrUnauthorized for a 401,
// wrapping the problem).
func (a *API) do(ctx context.Context, method, path string, in, out any) (int, error) {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.Base+path, body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Conch-Contract", Contract)
	req.Header.Set("Authorization", "Bearer "+a.Token())
	req.Header.Set("Accept", "application/json, application/problem+json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := a.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, maxBody+1))
	if err != nil {
		return res.StatusCode, err
	}
	if len(b) > maxBody {
		return res.StatusCode, fmt.Errorf("%s %s: answer over %d bytes", method, path, maxBody)
	}
	if res.StatusCode >= 400 {
		p := &Problem{Status: res.StatusCode}
		ct, _, _ := mime.ParseMediaType(res.Header.Get("Content-Type"))
		if ct == "application/problem+json" || ct == "application/json" {
			_ = json.Unmarshal(b, p)
			p.Status = res.StatusCode
		}
		if p.RetryAfter == 0 {
			if s := res.Header.Get("Retry-After"); s != "" {
				fmt.Sscanf(s, "%d", &p.RetryAfter)
			}
		}
		if res.StatusCode == http.StatusUnauthorized {
			return res.StatusCode, fmt.Errorf("%w (%v)", ErrUnauthorized, p)
		}
		return res.StatusCode, p
	}
	if out != nil && res.StatusCode == http.StatusOK {
		if err := json.Unmarshal(b, out); err != nil {
			return res.StatusCode, fmt.Errorf("%s %s: %w", method, path, err)
		}
	}
	return res.StatusCode, nil
}

// Hello is one POST /v1/vm/hello.
func (a *API) Hello(ctx context.Context, h VMHello) (VMConfig, error) {
	var cfg VMConfig
	if _, err := a.do(ctx, http.MethodPost, "/v1/vm/hello", h, &cfg); err != nil {
		return VMConfig{}, err
	}
	if cfg.RelayURL == "" || cfg.RoomSecret == "" {
		return cfg, errors.New("hello's answer has no relay_url or room_secret")
	}
	return cfg, nil
}

// Activity is one POST /v1/vm/activity. A server speaking 1.1.0 or later
// answers 200 with commands; 204 is a 1.0.0 server, with none.
func (a *API) Activity(ctx context.Context, act VMActivity) ([]VMCommand, error) {
	if act.Agents == nil {
		act.Agents = []AgentActivity{}
	}
	var res vmActivityResult
	status, err := a.do(ctx, http.MethodPost, "/v1/vm/activity", act, &res)
	if err != nil || status == http.StatusNoContent {
		return nil, err
	}
	return res.Commands, nil
}

// Env is GET /v1/vm/env: the env-mode secrets, for an agent being started.
func (a *API) Env(ctx context.Context) (map[string]string, error) {
	var res struct {
		Env map[string]string `json:"env"`
	}
	if _, err := a.do(ctx, http.MethodGet, "/v1/vm/env", nil, &res); err != nil {
		return nil, err
	}
	return res.Env, nil
}

// PushTargets is GET /v1/vm/push/targets.
func (a *API) PushTargets(ctx context.Context) ([]PushTarget, error) {
	var res struct {
		Targets []PushTarget `json:"targets"`
	}
	if _, err := a.do(ctx, http.MethodGet, "/v1/vm/push/targets", nil, &res); err != nil {
		return nil, err
	}
	return res.Targets, nil
}

// Push is POST /v1/vm/push.
func (a *API) Push(ctx context.Context, p VMPush) error {
	_, err := a.do(ctx, http.MethodPost, "/v1/vm/push", p, nil)
	return err
}

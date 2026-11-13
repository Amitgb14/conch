package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Client calls the control plane as a signed-in device.
type Client struct {
	API  string
	HTTP *http.Client

	mu      sync.Mutex
	account *Account
	tok     tokens
}

// httpClient is what every request goes through; tests may replace it.
var httpClient = &http.Client{Timeout: 30 * time.Second}

// Open is the signed-in account's client, or ErrNotSignedIn. It reads
// only what is on disk (and the Keychain): no request is made.
func Open() (*Client, error) {
	a, err := LoadAccount()
	if err != nil {
		return nil, err
	}
	t, err := a.tokens()
	if err != nil {
		return nil, err
	}
	return &Client{API: a.API, HTTP: httpClient, account: a, tok: t}, nil
}

// Account is what cloud.json says about this sign-in.
func (c *Client) Account() Account {
	c.mu.Lock()
	defer c.mu.Unlock()
	a := *c.account
	a.AccessToken, a.RefreshToken = "", ""
	return a
}

// refreshEarly is how long before it expires an access token is renewed.
const refreshEarly = time.Minute

// maxRetries bounds trying again after 429 or 503; maxRetryWait each wait.
var (
	maxRetries   = 2
	maxRetryWait = 10 * time.Second
)

// do makes one call as the device: the contract's header, the bearer
// token (renewed first when it is about to expire, and once more if the
// server says it is not valid), and the answer decoded into out, or a
// *Problem.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	return c.doHeader(ctx, method, path, nil, body, out)
}

// doHeader is do with headers of the call's own (an Idempotency-Key).
func (c *Client) doHeader(ctx context.Context, method, path string, h map[string]string, body, out any) error {
	refreshed := false
	for attempt := 0; ; attempt++ {
		access, err := c.access(ctx)
		if err != nil {
			return err
		}
		status, err := call(ctx, c.HTTP, c.API, method, path, access, h, body, out)
		var p *Problem
		switch {
		case status == http.StatusUnauthorized && !refreshed:
			refreshed = true
			if err := c.refresh(ctx, access); err != nil {
				return err
			}
			continue
		case errors.As(err, &p) && (status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable) && attempt < maxRetries:
			wait := min(time.Duration(max(p.RetryAfter, 1))*time.Second, maxRetryWait)
			select {
			case <-ctx.Done():
				return err
			case <-time.After(wait):
			}
			continue
		}
		return err
	}
}

// access is a current access token, renewed if it is about to expire.
func (c *Client) access(ctx context.Context) (string, error) {
	c.mu.Lock()
	t := c.tok
	c.mu.Unlock()
	if t.Access != "" && (t.Expires.IsZero() || time.Until(t.Expires) > refreshEarly) {
		return t.Access, nil
	}
	if err := c.refresh(ctx, t.Access); err != nil {
		return "", err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tok.Access, nil
}

// refresh trades the refresh token for new tokens, unless another
// process already has since stale was the access token: then its tokens
// are taken from disk. A refresh token the server refuses signs this
// device out.
func (c *Client) refresh(ctx context.Context, stale string) error {
	unlock, err := lock()
	if err != nil {
		return err
	}
	defer unlock()
	a, err := LoadAccount()
	if err != nil {
		if errors.Is(err, ErrNotSignedIn) {
			return ErrSignedOut // logged out by another conch meanwhile
		}
		return err
	}
	t, err := a.tokens()
	if err != nil {
		return err
	}
	if t.Access != "" && t.Access != stale && time.Until(t.Expires) > refreshEarly {
		c.mu.Lock()
		c.account, c.tok = a, t
		c.mu.Unlock()
		return nil
	}
	var res tokenResponse
	_, err = call(ctx, c.HTTP, a.API, http.MethodPost, "/v1/auth/token", "", nil,
		map[string]string{"grant_type": "refresh_token", "refresh_token": t.Refresh}, &res)
	var p *Problem
	if errors.As(err, &p) && p.Status >= 400 && p.Status < 500 && p.Code != "rate_limited" && p.Code != "contract_unsupported" {
		_ = forget()
		if errors.Is(err, ErrSuspended) {
			return fmt.Errorf("%w: %v", ErrSignedOut, err)
		}
		return ErrSignedOut
	}
	if err != nil {
		return err
	}
	next := tokens{Access: res.AccessToken, Refresh: res.RefreshToken, Expires: time.Now().Add(time.Duration(res.ExpiresIn) * time.Second)}
	if res.DeviceID != "" {
		a.DeviceID = res.DeviceID
	}
	if err := a.save(next); err != nil {
		return err
	}
	c.mu.Lock()
	c.account, c.tok = a, next
	c.mu.Unlock()
	return nil
}

// call is one HTTP request to the control plane: the status, and out
// filled from a 2xx answer or a *Problem for any other.
func call(ctx context.Context, hc *http.Client, api, method, path, bearer string, h map[string]string, body, out any) (int, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, api+path, rd)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Conch-Contract", Contract)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	for k, v := range h {
		if v != "" {
			req.Header.Set(k, v)
		}
	}
	res, err := hc.Do(req)
	if err != nil {
		return 0, fmt.Errorf("Conch Cloud (%s): %w", hostOf(api), err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return res.StatusCode, err
	}
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		if out != nil && len(bytes.TrimSpace(b)) > 0 {
			if err := json.Unmarshal(b, out); err != nil {
				return res.StatusCode, fmt.Errorf("Conch Cloud answered %s %s with something unexpected: %w", method, path, err)
			}
		}
		return res.StatusCode, nil
	}
	return res.StatusCode, problemFrom(res, b)
}

// problemFrom is a non-2xx answer as a *Problem, made up from the status
// when the body is not one.
func problemFrom(res *http.Response, b []byte) *Problem {
	p := &Problem{}
	ct, _, _ := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if ct == "application/problem+json" || ct == "application/json" {
		_ = json.Unmarshal(b, p)
	}
	if p.Status == 0 {
		p.Status = res.StatusCode
	}
	if p.Title == "" {
		p.Title = http.StatusText(res.StatusCode)
	}
	if p.Code == "" {
		switch res.StatusCode {
		case http.StatusUnauthorized:
			p.Code = "unauthorized"
		case http.StatusNotFound:
			p.Code = "not_found"
		case http.StatusTooManyRequests:
			p.Code = "rate_limited"
		case http.StatusServiceUnavailable:
			p.Code = "unavailable"
		}
	}
	if p.RetryAfter == 0 {
		if s, err := strconv.Atoi(res.Header.Get("Retry-After")); err == nil {
			p.RetryAfter = s
		}
	}
	return p
}

func hostOf(api string) string {
	if u, err := url.Parse(api); err == nil && u.Host != "" {
		return u.Host
	}
	return api
}

// ---- the account ----

// Me is the signed-in account. Its handle is kept in cloud.json, for
// saying who is signed in without asking.
func (c *Client) Me(ctx context.Context) (Me, error) {
	var m Me
	if err := c.do(ctx, http.MethodGet, "/v1/me", nil, &m); err != nil {
		return m, err
	}
	c.rememberHandle(m.Handle)
	return m, nil
}

func (c *Client) rememberHandle(handle string) {
	if handle == "" {
		return
	}
	unlock, err := lock()
	if err != nil {
		return
	}
	defer unlock()
	a, err := LoadAccount()
	if err != nil || a.Handle == handle {
		return
	}
	t, err := a.tokens()
	if err != nil {
		return
	}
	a.Handle = handle
	if a.save(t) == nil {
		c.mu.Lock()
		c.account.Handle = handle
		c.mu.Unlock()
	}
}

// Devices lists the devices signed in to the account.
func (c *Client) Devices(ctx context.Context) ([]Device, error) {
	var out struct {
		Devices []Device `json:"devices"`
	}
	err := c.do(ctx, http.MethodGet, "/v1/devices", nil, &out)
	return out.Devices, err
}

// Providers lists where the account can run machines.
func (c *Client) Providers(ctx context.Context) ([]Provider, error) {
	var out struct {
		Providers []Provider `json:"providers"`
	}
	err := c.do(ctx, http.MethodGet, "/v1/providers", nil, &out)
	return out.Providers, err
}

// Sizes lists the sizes and regions the account may create.
func (c *Client) Sizes(ctx context.Context) (Sizes, error) {
	var out Sizes
	err := c.do(ctx, http.MethodGet, "/v1/sizes", nil, &out)
	return out, err
}

// ---- machines ----

// Machines lists the account's machines.
func (c *Client) Machines(ctx context.Context) ([]Machine, error) {
	var out struct {
		Machines []Machine `json:"machines"`
	}
	err := c.do(ctx, http.MethodGet, "/v1/machines", nil, &out)
	return out.Machines, err
}

// Machine is one machine.
func (c *Client) Machine(ctx context.Context, id string) (Machine, error) {
	var m Machine
	err := c.do(ctx, http.MethodGet, "/v1/machines/"+url.PathEscape(id), nil, &m)
	return m, err
}

// CreateMachine makes a machine, which answers at once in `creating`.
// The idempotency key makes asking twice (a retry) make one machine.
func (c *Client) CreateMachine(ctx context.Context, spec MachineCreate, idempotencyKey string) (Machine, error) {
	var m Machine
	err := c.doHeader(ctx, http.MethodPost, "/v1/machines", map[string]string{"Idempotency-Key": idempotencyKey}, spec, &m)
	return m, err
}

// UpdateMachine renames or resizes a machine, or changes its sleep.
func (c *Client) UpdateMachine(ctx context.Context, id string, u MachineUpdate) (Machine, error) {
	var m Machine
	err := c.do(ctx, http.MethodPatch, "/v1/machines/"+url.PathEscape(id), u, &m)
	return m, err
}

// DeleteMachine destroys a machine and its disk.
func (c *Client) DeleteMachine(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/v1/machines/"+url.PathEscape(id), nil, nil)
}

// Wake starts or wakes a machine.
func (c *Client) Wake(ctx context.Context, id string) (Machine, error) {
	var m Machine
	err := c.do(ctx, http.MethodPost, "/v1/machines/"+url.PathEscape(id)+"/wake", nil, &m)
	return m, err
}

// Sleep puts a machine to sleep; ErrAgentsWorking unless force.
func (c *Client) Sleep(ctx context.Context, id string, force bool) (Machine, error) {
	var m Machine
	var body any
	if force {
		body = map[string]bool{"force": true}
	}
	err := c.do(ctx, http.MethodPost, "/v1/machines/"+url.PathEscape(id)+"/sleep", body, &m)
	return m, err
}

// Connect is what is needed to open a tunnel to a machine; it wakes an
// asleep one.
func (c *Client) Connect(ctx context.Context, id string) (ConnectInfo, error) {
	var ci ConnectInfo
	err := c.do(ctx, http.MethodPost, "/v1/machines/"+url.PathEscape(id)+"/connect", nil, &ci)
	return ci, err
}

// IsMachineID says whether id has the shape of a machine id.
func IsMachineID(id string) bool {
	rest, ok := strings.CutPrefix(id, "m_")
	if !ok || len(rest) != 16 {
		return false
	}
	for _, r := range rest {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

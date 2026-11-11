package cloud

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// The device flow (RFC 8628's shape, openapi.yaml /v1/auth/device and
// /v1/auth/token): conch asks for a code, the person approves it in a
// browser where they are signed in, and conch, polling, is given its
// tokens.

// Clients a device signs in as (DeviceAuthRequest.client).
const (
	ClientCLI    = "cli"
	ClientTUI    = "tui"
	ClientMacApp = "mac_app"
)

// pollUnit is a second of the poll's interval; tests shorten it.
var pollUnit = time.Second

// StartLogin asks api for a device code to show the person.
func StartLogin(ctx context.Context, api, client, deviceName, conchVersion string) (DeviceAuth, error) {
	if len(deviceName) > 80 {
		deviceName = deviceName[:80]
	}
	body := map[string]string{"client": client, "device_name": deviceName}
	if conchVersion != "" {
		body["conch_version"] = conchVersion
	}
	var da DeviceAuth
	_, err := call(ctx, httpClient, api, http.MethodPost, "/v1/auth/device", "", nil, body, &da)
	if err == nil && (da.DeviceCode == "" || da.UserCode == "") {
		err = errors.New("Conch Cloud gave no device code")
	}
	return da, err
}

// ErrLoginDenied and ErrLoginExpired end a sign-in that will not succeed.
var (
	ErrLoginDenied  = errors.New("the sign-in was denied")
	ErrLoginExpired = errors.New("the code expired before it was approved; run conch login again")
)

// FinishLogin polls until the person approves the code (every interval
// seconds, five more after each slow_down), then keeps the tokens and
// returns the signed-in client.
func FinishLogin(ctx context.Context, api string, da DeviceAuth) (*Client, error) {
	interval := max(da.Interval, 1)
	var deadline <-chan time.Time
	if da.ExpiresIn > 0 {
		t := time.NewTimer(time.Duration(da.ExpiresIn) * pollUnit)
		defer t.Stop()
		deadline = t.C
	}
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline:
			return nil, ErrLoginExpired
		case <-time.After(time.Duration(interval) * pollUnit):
		}
		var res tokenResponse
		_, err := call(ctx, httpClient, api, http.MethodPost, "/v1/auth/token", "", nil,
			map[string]string{"grant_type": "device_code", "device_code": da.DeviceCode}, &res)
		switch {
		case errors.Is(err, ErrAuthorizationPending):
			continue
		case errors.Is(err, ErrSlowDown):
			interval += 5
			continue
		case errors.Is(err, ErrAccessDenied):
			return nil, ErrLoginDenied
		case errors.Is(err, ErrExpiredToken):
			return nil, ErrLoginExpired
		case errors.Is(err, ErrSuspended):
			return nil, err
		case err != nil:
			var p *Problem
			if errors.As(err, &p) && (p.Status == http.StatusTooManyRequests || p.Status >= 500) {
				continue // the server's trouble, not the sign-in's
			}
			return nil, err
		}
		if res.AccessToken == "" || res.RefreshToken == "" {
			return nil, errors.New("Conch Cloud answered the sign-in without tokens")
		}
		return keep(api, res)
	}
}

// keep saves a new sign-in, replacing any other on this CONCH_HOME.
func keep(api string, res tokenResponse) (*Client, error) {
	unlock, err := lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	_ = forget()
	a := &Account{API: api, DeviceID: res.DeviceID}
	t := tokens{Access: res.AccessToken, Refresh: res.RefreshToken, Expires: time.Now().Add(time.Duration(res.ExpiresIn) * time.Second)}
	if err := a.save(t); err != nil {
		return nil, fmt.Errorf("keep the sign-in: %w", err)
	}
	return &Client{API: api, HTTP: httpClient, account: a, tok: t}, nil
}

// Logout revokes this device's sign-in and forgets it. The tokens are
// forgotten even when the server cannot be told — signing out of this
// conch must always work — and the error then says the device is still
// listed on the account.
func (c *Client) Logout(ctx context.Context) error {
	err := c.do(ctx, http.MethodPost, "/v1/auth/revoke", nil, nil)
	if errors.Is(err, ErrSignedOut) {
		err = nil // the server had already let it go
	}
	unlock, lerr := lock()
	if lerr == nil {
		defer unlock()
	}
	if ferr := forget(); ferr != nil {
		return ferr
	}
	if err != nil {
		return fmt.Errorf("signed out here, but Conch Cloud could not be told (%w); remove this device on the account's Devices page", err)
	}
	return nil
}

// Forget drops this conch's sign-in without telling the server.
func Forget() error {
	unlock, err := lock()
	if err == nil {
		defer unlock()
	}
	return forget()
}

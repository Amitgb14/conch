package cloudagent

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"time"
)

// helloBackoff is the first and the longest wait between hellos the
// control plane did not answer. Variables so a test waits less.
var (
	helloFirstWait = time.Second
	helloMaxWait   = 30 * time.Second
)

// ErrFatal wraps an answer to hello that asking again cannot change: the
// request is wrong, the machine's key is not the one clients pinned, or
// the control plane does not speak this contract.
var ErrFatal = errors.New("the control plane refused this machine")

// sayHello calls hello until the control plane answers it, waiting longer
// each time (jittered, up to helloMaxWait) while it is unreachable or
// failing on its side. A refused token is returned as ErrUnauthorized and
// anything else asking again would not change as ErrFatal.
func sayHello(ctx context.Context, api *API, h VMHello, logf func(string, ...any)) (VMConfig, error) {
	wait := helloFirstWait
	for attempt := 1; ; attempt++ {
		cfg, err := api.Hello(ctx, h)
		if err == nil {
			return cfg, nil
		}
		if ctx.Err() != nil {
			return VMConfig{}, ctx.Err()
		}
		if errors.Is(err, ErrUnauthorized) {
			return VMConfig{}, err
		}
		next := jitter(wait)
		var p *Problem
		if errors.As(err, &p) {
			if !p.retryable() {
				switch {
				case p.Status == http.StatusUpgradeRequired:
					return VMConfig{}, fmt.Errorf("%w: it does not speak contract %s (it supports %q): %v", ErrFatal, Contract, p.ContractSupported, p)
				case p.Status == http.StatusConflict:
					return VMConfig{}, fmt.Errorf("%w: it has a different server key for this machine (%v)", ErrFatal, p)
				}
				return VMConfig{}, fmt.Errorf("%w: %v", ErrFatal, p)
			}
			if p.RetryAfter > 0 {
				next = time.Duration(p.RetryAfter) * time.Second
			}
		}
		logf("cloud: hello failed (attempt %d): %v; trying again in %s", attempt, err, next.Round(time.Millisecond))
		select {
		case <-ctx.Done():
			return VMConfig{}, ctx.Err()
		case <-time.After(next):
		}
		wait = min(wait*2, helloMaxWait)
	}
}

// jitter is d less up to a half of it, so machines a control plane outage
// stopped together do not all come back in the same second.
func jitter(d time.Duration) time.Duration {
	if d <= 1 {
		return d
	}
	return d - time.Duration(rand.Int64N(int64(d/2)+1))
}

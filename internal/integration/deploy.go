package integration

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// A deploy file is where a branch is built and shown (Vercel, Netlify):
// its previews, what a failed build printed, building one again and
// releasing one to production.

// Deploy is one deploy, in the deploy contract's words: a run's fields —
// State is ready, building, failed or canceled, URL the preview's
// address — and where it went, why it failed, and the service's own page
// for it.
type Deploy struct {
	Run
	Environment string `json:"environment,omitempty"`
	Error       string `json:"error,omitempty"`
	Inspect     string `json:"inspect,omitempty"`
}

// Previews lists the deploys of t's branch, newest first, at t's head
// when it has one. A project the service has nothing of is none.
func (c *Conn) Previews(ctx context.Context, t Target) ([]Deploy, error) {
	if c.f.Kind != KindDeploy {
		return nil, fmt.Errorf("%s: not a deploy integration", c.f.DisplayName())
	}
	op := c.f.Previews
	var out []Deploy
	err := c.items(ctx, t, "previews", op.RunsOp, func(r Run, field func(string) string) {
		out = append(out, Deploy{Run: r, Environment: field(op.Environment), Error: field(op.Error), Inspect: field(op.Inspect)})
	})
	return out, err
}

// Redeploy builds deploy again.
func (c *Conn) Redeploy(ctx context.Context, t Target, deploy string) error {
	return c.act(ctx, t, "redeploy", c.f.Redeploy, deploy)
}

// Promote releases deploy to production. It changes what everybody is
// shown: whoever calls it has asked the person first.
func (c *Conn) Promote(ctx context.Context, t Target, deploy string) error {
	return c.act(ctx, t, "promote", c.f.Promote, deploy)
}

func (c *Conn) act(ctx context.Context, t Target, what string, op Operation, deploy string) error {
	if c.f.Kind != KindDeploy {
		return fmt.Errorf("%s: not a deploy integration", c.f.DisplayName())
	}
	if !op.Has() {
		return fmt.Errorf("%s: its file cannot %s", c.f.DisplayName(), what)
	}
	if strings.TrimSpace(deploy) == "" {
		return errors.New("no deploy given")
	}
	// A pick on the way that finds nothing is no such deploy, said as an
	// error: nothing was released, and nobody should think it was. (An
	// empty answer to the last call still answered.)
	_, err := c.steps(ctx, what, op, c.vars(t, deploy))
	return err
}

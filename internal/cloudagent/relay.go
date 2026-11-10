package cloudagent

import "context"

// startLink joins the relay as the room's gateway (PROTOCOL §3.1): not yet.
func (a *Agent) startLink(ctx context.Context, cfg VMConfig) {}

func (a *Agent) stopLink() {}

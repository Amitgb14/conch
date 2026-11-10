package cloudagent

import (
	"context"
	"sync"

	"github.com/Amitgb14/conch/internal/proto"
)

type pusher struct{ mu sync.Mutex }

// pushWaiting tells the account's devices an agent waits (PROTOCOL §8): not yet.
func (a *Agent) pushWaiting(ctx context.Context, p proto.PaneInfo) {}

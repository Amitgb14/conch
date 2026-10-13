package client

import (
	"context"
	"time"
)

// chunksInFlight is how many chunks a copy keeps on the way at once. One
// at a time left the link idle while each chunk was encoded, crossed the
// bridge and was answered: about half what scp gets over the same ssh.
const chunksInFlight = 8

// chunkTimeout bounds one chunk's call, wherever the step that waits for
// it is.
var chunkTimeout = time.Minute

// pipe keeps chunk calls in flight and hands their answers back in the
// order they were asked for, so progress counts only what has arrived
// whole from the start.
type pipe[T any] struct {
	ctx    context.Context
	cancel context.CancelFunc
	queue  []chan piped[T]
}

type piped[T any] struct {
	v   T
	err error
}

func newPipe[T any]() *pipe[T] {
	ctx, cancel := context.WithCancel(context.Background())
	return &pipe[T]{ctx: ctx, cancel: cancel}
}

// busy is how many calls are in flight.
func (p *pipe[T]) busy() int { return len(p.queue) }

// send starts a call. It runs on the pipe's own context, not a step's: a
// step ends while the calls it started are still on their way.
func (p *pipe[T]) send(call func(context.Context) (T, error)) {
	ch := make(chan piped[T], 1)
	p.queue = append(p.queue, ch)
	go func() {
		ctx, cancel := context.WithTimeout(p.ctx, chunkTimeout)
		defer cancel()
		v, err := call(ctx)
		ch <- piped[T]{v, err}
	}()
}

// next waits for the oldest call's answer.
func (p *pipe[T]) next(ctx context.Context) (T, error) {
	select {
	case r := <-p.queue[0]:
		p.queue = p.queue[1:]
		return r.v, r.err
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}

// stop gives up on every call still in flight.
func (p *pipe[T]) stop() {
	p.cancel()
	p.queue = nil
}

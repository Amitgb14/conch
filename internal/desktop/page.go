package desktop

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/Amitgb14/conch/internal/fleet"
)

// maxMessage bounds what a page may send in one message: a call's params,
// which for an upload is a file in base64.
const maxMessage = 32 << 20

// queueMax is how many messages other than frames may wait for a page.
// A page that falls that far behind is not reading, and is closed rather
// than left to grow the host without end.
const queueMax = 1024

// in is a message from a page.
//
//	{"t":"call","id":"1","machine":"local","method":"pane.list","params":{}}
//	{"t":"subscribe","pane":"local:p3"}   {"t":"unsubscribe","pane":"local:p3"}
//	{"t":"state","state":{…}}             saves desktop.json
type in struct {
	T       string          `json:"t"`
	ID      string          `json:"id,omitempty"`
	Machine string          `json:"machine,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Pane    string          `json:"pane,omitempty"`
	State   json.RawMessage `json:"state,omitempty"`
}

// out is a message to a page.
//
//	{"t":"hello","machines":[…],"state":{…}}
//	{"t":"machines","machines":[…]}
//	{"t":"result","id":"1","result":…}  or  {"t":"result","id":"1","error":{…}}
//	{"t":"event","machine":"local","event":"pane.created","data":…}
//	{"t":"frame","pane":"local:p3","frame":…}
type out struct {
	T        string          `json:"t"`
	ID       string          `json:"id,omitempty"`
	Machines []Machine       `json:"machines,omitempty"`
	State    json.RawMessage `json:"state,omitempty"`
	Result   json.RawMessage `json:"result,omitempty"`
	Error    *Error          `json:"error,omitempty"`
	Machine  string          `json:"machine,omitempty"`
	Event    string          `json:"event,omitempty"`
	Data     json.RawMessage `json:"data,omitempty"`
	Pane     string          `json:"pane,omitempty"`
	Frame    json.RawMessage `json:"frame,omitempty"`
	Conch    string          `json:"conch,omitempty"` // hello: the conch to run in a pane
}

// Error is a call that failed: the server's own code and words where it
// answered, else the host's.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// page is one open page.
type page struct {
	h  *Host
	ws *websocket.Conn

	mu     sync.Mutex
	queue  []out
	frames map[string]json.RawMessage // newest unsent frame per pane
	order  []string                   // panes with a frame waiting, oldest first
	subs   map[string]int             // panes this page subscribed, and how often
	wake   chan struct{}
	done   chan struct{}
	ended  bool
}

func newPage(h *Host, ws *websocket.Conn) *page {
	return &page{h: h, ws: ws, frames: map[string]json.RawMessage{}, subs: map[string]int{},
		wake: make(chan struct{}, 1), done: make(chan struct{})}
}

// send queues a message; one that would overflow the queue ends the page.
func (p *page) send(m out) {
	p.mu.Lock()
	if p.ended {
		p.mu.Unlock()
		return
	}
	if len(p.queue) >= queueMax {
		p.mu.Unlock()
		p.close("the page is not reading")
		return
	}
	p.queue = append(p.queue, m)
	p.mu.Unlock()
	p.poke()
}

// frame queues a pane's frame for a page that subscribed to it, replacing
// one still waiting: a screen superseded before it was sent is not worth
// sending, and a slow page then costs one frame per pane, not a backlog.
func (p *page) frame(ref string, data json.RawMessage) {
	p.mu.Lock()
	if p.ended || p.subs[ref] == 0 {
		p.mu.Unlock()
		return
	}
	if _, waiting := p.frames[ref]; !waiting {
		p.order = append(p.order, ref)
	}
	p.frames[ref] = data
	p.mu.Unlock()
	p.poke()
}

func (p *page) poke() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// next is what to write: queued messages first, since a result or an
// event may say a pane closed before its last frame matters.
func (p *page) next() (out, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.queue) > 0 {
		m := p.queue[0]
		p.queue[0] = out{}
		p.queue = p.queue[1:]
		return m, true
	}
	for len(p.order) > 0 {
		ref := p.order[0]
		p.order = p.order[1:]
		if f, ok := p.frames[ref]; ok {
			delete(p.frames, ref)
			return out{T: "frame", Pane: ref, Frame: f}, true
		}
	}
	return out{}, false
}

func (p *page) subscribed() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var refs []string
	for ref, n := range p.subs {
		for range n {
			refs = append(refs, ref)
		}
	}
	return refs
}

// close ends the page with a reason it is shown.
func (p *page) close(reason string) {
	p.mu.Lock()
	if p.ended {
		p.mu.Unlock()
		return
	}
	p.ended = true
	p.mu.Unlock()
	close(p.done)
	// Closing waits for the page's answer; whoever ended it — the event
	// follower, for a page that stopped reading — must not.
	go p.ws.Close(websocket.StatusPolicyViolation, reason)
}

// run serves the page until it goes.
func (p *page) run(ctx context.Context) {
	// Deferred last first: cancel the calls in flight, end the page, then
	// wait for the calls to see it.
	ctx, cancel := context.WithCancel(ctx)
	var calls sync.WaitGroup
	defer calls.Wait()
	defer p.close("")
	defer cancel()
	go p.write(ctx)

	p.send(out{T: "hello", Machines: p.h.machineList(), State: p.h.state.load(p.h.o.Logf), Conch: p.h.o.Conch})
	for {
		var m in
		if err := wsjson.Read(ctx, p.ws, &m); err != nil {
			return
		}
		switch m.T {
		case "call":
			// Calls run side by side: one waiting on a slow machine must
			// not hold up the rest.
			calls.Add(1)
			go func() {
				defer calls.Done()
				res, e := p.h.call(ctx, m.Machine, m.Method, m.Params)
				p.send(out{T: "result", ID: m.ID, Result: res, Error: e})
			}()
		case "subscribe":
			ref := canonical(m.Pane)
			last, e := p.h.subscribe(ref)
			if e != nil {
				p.send(out{T: "result", ID: m.ID, Error: e})
				continue
			}
			p.mu.Lock()
			p.subs[ref]++
			p.mu.Unlock()
			if last != nil {
				p.frame(ref, last)
			}
		case "unsubscribe":
			ref := canonical(m.Pane)
			p.mu.Lock()
			had := p.subs[ref] > 0
			if had {
				if p.subs[ref]--; p.subs[ref] == 0 {
					delete(p.subs, ref)
					delete(p.frames, ref)
				}
			}
			p.mu.Unlock()
			if had {
				p.h.unsubscribe(ref)
			}
		case "state":
			e := p.h.state.save(m.State)
			if m.ID != "" || e != nil {
				p.send(out{T: "result", ID: m.ID, Error: e})
			}
		default:
			p.send(out{T: "result", ID: m.ID, Error: &Error{Code: "bad_request", Message: "unknown message " + m.T}})
		}
	}
}

// canonical is a pane reference keyed one way: a bare `p3` is this
// computer's, so it is `local:p3` everywhere here.
func canonical(ref string) string {
	if machine, pane, err := fleet.SplitPaneID(ref); err == nil {
		return fleet.ComposePaneID(machine, pane)
	}
	return ref
}

// write sends what is queued until the page ends.
func (p *page) write(ctx context.Context) {
	for {
		for {
			m, ok := p.next()
			if !ok {
				break
			}
			if err := wsjson.Write(ctx, p.ws, m); err != nil {
				p.close("")
				return
			}
		}
		select {
		case <-p.wake:
		case <-p.done:
			return
		case <-ctx.Done():
			return
		}
	}
}

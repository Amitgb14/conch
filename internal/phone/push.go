package phone

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"sync"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// pushJob is one message for the phones that asked for its kind.
type pushJob struct {
	msg PushMessage
}

// pushRetry is how long the push watcher waits before connecting to the
// server again after losing it.
var pushRetry = time.Second

// pushes watches the agents and queues a notification when one goes to
// waiting, or to done, for as long as the gateway runs. It has its own
// connection to the server. What each agent was doing is learnt from the
// list when it connects, so an agent already waiting then is not news —
// after a server reload as much as at start.
func (g *Gateway) pushes() {
	if g.dial == nil {
		return // no server to watch
	}
	// One watcher per machine: a waiting agent on another machine is the
	// reason a phone wants a notification at all. Local is watched here;
	// the others are picked up as they come up and watched beside it.
	go g.pushesElsewhere()
	for {
		c, err := g.dial()
		if err == nil {
			g.watchForPushes(LocalMachine, c)
			c.Close()
		}
		select {
		case <-g.quit:
			return
		case <-time.After(pushRetry):
		}
	}
}

// pushesElsewhere watches every other machine for agents that start
// waiting, one goroutine per machine, started when it first answers and
// ended when it goes.
func (g *Gateway) pushesElsewhere() {
	watching := map[string]bool{}
	var mu sync.Mutex
	for {
		for _, mc := range g.machines() {
			if mc.id == LocalMachine || mc.client() == nil {
				continue
			}
			mu.Lock()
			already := watching[mc.id]
			if !already {
				watching[mc.id] = true
			}
			mu.Unlock()
			if already {
				continue
			}
			id := mc.id
			go func() {
				defer func() {
					mu.Lock()
					delete(watching, id)
					mu.Unlock()
				}()
				c, err := g.dialMachine(id)
				if err != nil {
					return
				}
				defer c.Close()
				g.watchForPushes(id, c)
			}()
		}
		select {
		case <-g.quit:
			return
		case <-time.After(pushRetry):
		}
	}
}

// watchForPushes follows one connection until it ends or the gateway
// closes.
func (g *Gateway) watchForPushes(machine string, c *client.Client) {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	var list proto.PaneList
	err := c.Call(ctx, proto.MethodPaneList, nil, &list)
	cancel()
	if err != nil {
		return
	}
	last := map[string]string{}
	for _, p := range list.Panes {
		if isAgent(p) {
			last[p.ID] = phoneState(p.Agent.State)
		}
	}
	// What the phone is told, and what a tap on the notification opens.
	paneRef := func(id string) string { return composePaneID(machine, id) }
	if g.pushWatching != nil {
		g.pushWatching()
	}
	projects := map[string]string{}
	for {
		select {
		case <-g.quit:
			return
		case msg, ok := <-c.Events:
			if !ok {
				return
			}
			switch msg.Event {
			case proto.EventPaneUpdated, proto.EventPaneCreated:
				var p proto.PaneInfo
				if json.Unmarshal(msg.Data, &p) != nil {
					continue
				}
				if !isAgent(p) {
					delete(last, p.ID)
					continue
				}
				now, was := phoneState(p.Agent.State), last[p.ID]
				last[p.ID] = now
				if now == was || now != StateWaiting && now != StateDone {
					continue
				}
				if _, known := projects[p.ProjectID]; p.ProjectID != "" && !known {
					ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
					if names, err := projectNames(ctx, c); err == nil {
						projects = names
					}
					cancel()
				}
				g.queuePush(PushMessage{Type: now, Pane: paneRef(p.ID), Name: p.DisplayName(),
					Project: projects[p.ProjectID], URL: "/agent/" + paneRef(p.ID)})
			case proto.EventPaneExited, proto.EventPaneClosed:
				var ref proto.PaneRef
				if json.Unmarshal(msg.Data, &ref) == nil {
					delete(last, ref.ID)
				}
			}
		}
	}
}

// queuePush hands a message to the sender. A sender far behind — push
// services not answering — loses the message rather than holding up the
// watcher.
func (g *Gateway) queuePush(msg PushMessage) {
	select {
	case g.pushQueue <- pushJob{msg: msg}:
	default:
		g.logf("push %s %s dropped: too many waiting to be sent", msg.Type, msg.Pane)
	}
}

// sendPushes sends each queued message to every subscription that asked
// for its kind. A subscription the push service says is gone is dropped.
// Endpoints are not logged: each is an address anyone could push to.
func (g *Gateway) sendPushes() {
	for {
		var job pushJob
		select {
		case <-g.quit:
			return
		case job = <-g.pushQueue:
		}
		var subs []struct {
			device string
			sub    PushSubscription
		}
		for _, d := range g.currentDevices() {
			// A notification names a pane on a machine, and taps through
			// to it: a device that may not see that machine is not told
			// its agents are waiting either.
			if !d.Reaches(machineOf(job.msg.Pane)) {
				continue
			}
			for _, s := range d.Push {
				if slices.Contains(s.On, job.msg.Type) {
					subs = append(subs, struct {
						device string
						sub    PushSubscription
					}{d.ID, s})
				}
			}
		}
		if len(subs) == 0 {
			continue
		}
		key, err := g.store.VAPIDKey()
		if err != nil {
			g.logf("push: %v", err)
			continue
		}
		sent := 0
		for _, s := range subs {
			// Checked again at sending: the file may have been edited by
			// hand, and a device can't be allowed to aim the laptop anywhere.
			if !g.pushAllowed(s.sub.Endpoint) {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			err := sendPush(ctx, g.pushClient, key, s.sub, job.msg, g.now())
			cancel()
			switch {
			case errors.Is(err, errGone):
				g.logf("push %s %s: a subscription of %s is gone; dropped", job.msg.Type, job.msg.Pane, s.device)
				if err := g.store.Unsubscribe(s.device, s.sub.Endpoint); err != nil {
					g.logf("push: %v", err)
				}
			case err != nil:
				var uerr *url.Error
				if errors.As(err, &uerr) {
					err = uerr.Err // without the endpoint it names
				}
				g.logf("push %s %s to %s: %v", job.msg.Type, job.msg.Pane, s.device, err)
			default:
				sent++
			}
		}
		g.logf("push %s %s: sent to %d of %d", job.msg.Type, job.msg.Pane, sent, len(subs))
	}
}

package cloud

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// eventsRetry is the first pause before a dropped stream is opened again.
var eventsRetry = time.Second

// Events follows GET /v1/events, calling on for each event until ctx
// ends. A dropped stream is opened again after a pause, resuming from the
// last event's id (Last-Event-ID), so a machine's state is not missed
// across a reconnect. It returns only for ctx, or for an error trying
// again cannot mend: the device signed out, or the contract refused.
func (c *Client) Events(ctx context.Context, on func(Event)) error {
	last := ""
	backoff := eventsRetry
	for {
		_, err := c.events(ctx, last, func(e Event) {
			last = e.ID
			backoff = eventsRetry
			on(e)
		})
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case errors.Is(err, ErrSignedOut), errors.Is(err, ErrContractUnsupported), errors.Is(err, ErrSuspended):
			return err
		}
		if err := sleepCtx(ctx, backoff); err != nil {
			return err
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

// events reads one stream until it ends.
func (c *Client) events(ctx context.Context, last string, on func(Event)) (int, error) {
	for refreshed := false; ; refreshed = true {
		access, err := c.access(ctx)
		if err != nil {
			return 0, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.API+"/v1/events", nil)
		if err != nil {
			return 0, err
		}
		req.Header.Set("Conch-Contract", Contract)
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("Authorization", "Bearer "+access)
		if last != "" {
			req.Header.Set("Last-Event-ID", last)
		}
		hc := *c.HTTP
		hc.Timeout = 0 // a stream lasts
		res, err := hc.Do(req)
		if err != nil {
			return 0, err
		}
		if res.StatusCode == http.StatusUnauthorized && !refreshed {
			res.Body.Close()
			if err := c.refresh(ctx, access); err != nil {
				return 0, err
			}
			continue
		}
		if res.StatusCode != http.StatusOK {
			b := make([]byte, 64<<10)
			n, _ := res.Body.Read(b)
			res.Body.Close()
			return 0, problemFrom(res, b[:n])
		}
		n, err := readEvents(res, on)
		res.Body.Close()
		return n, err
	}
}

// readEvents parses a text/event-stream: an event is its lines up to a
// blank one; "id:" and "data:" are what conch reads, ":" lines are the
// heartbeat.
func readEvents(res *http.Response, on func(Event)) (int, error) {
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	n := 0
	var id string
	var data []string
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if len(data) > 0 {
				var e Event
				if err := json.Unmarshal([]byte(strings.Join(data, "\n")), &e); err == nil && e.Type != "" {
					e.ID = id
					n++
					on(e)
				}
			}
			id, data = "", nil
		case strings.HasPrefix(line, ":"):
		case strings.HasPrefix(line, "id:"):
			id = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := sc.Err(); err != nil {
		return n, err
	}
	return n, fmt.Errorf("the event stream ended")
}

package server_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// subscribedPane starts command in a pane, subscribes to it and waits for
// its first frame.
func subscribedPane(t *testing.T, c *client.Client, command ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var info proto.PaneInfo
	if err := c.Call(ctx, proto.MethodPaneCreate, proto.PaneCreateParams{Command: command, Cols: 40, Rows: 5}, &info); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(ctx, proto.MethodPaneSubscribe, proto.PaneRef{ID: info.ID}, nil); err != nil {
		t.Fatal(err)
	}
	nextFrame(t, c, info.ID, 5*time.Second)
	return info.ID
}

// nextFrame is the next frame of pane id, or fails after wait.
func nextFrame(t *testing.T, c *client.Client, id string, wait time.Duration) proto.Frame {
	t.Helper()
	timeout := time.After(wait)
	for {
		select {
		case m, ok := <-c.Events:
			if !ok {
				t.Fatalf("connection closed: %v", c.Err())
			}
			var f proto.Frame
			if m.Event == proto.EventPaneFrame && json.Unmarshal(m.Data, &f) == nil && f.ID == id {
				return f
			}
		case <-timeout:
			t.Fatal("timed out waiting for a frame")
		}
	}
}

// A change after a quiet spell is sent at once. The server used to wait a
// whole frame interval (33 ms) after every change before sending it, so no
// keystroke could echo sooner than that, on any client. The fastest of
// several tries has to beat it by a margin; the old loop could never get
// under 33 ms however idle the machine.
func TestFrameAfterQuietGoesOutAtOnce(t *testing.T) {
	t.Setenv("CONCH_SOCKET", "")
	t.Setenv("CONCH_PANE_ID", "")
	c, _ := startServer(t)
	id := subscribedPane(t, c, "/bin/cat")

	best := time.Hour
	typed := ""
	for i := 0; i < 8; i++ {
		time.Sleep(100 * time.Millisecond) // quiet: well past the interval
		typed += string(rune('a' + i))
		start := time.Now()
		c.Notify(proto.MethodPaneSendText, proto.PaneSendTextParams{ID: id, Text: typed[len(typed)-1:]})
		for {
			f := nextFrame(t, c, id, 5*time.Second)
			if strings.Contains(strings.Join(f.Lines, "\n"), typed) {
				break
			}
		}
		best = min(best, time.Since(start))
	}
	if best >= 25*time.Millisecond {
		t.Fatalf("fastest echo took %v; a frame after a quiet spell should not wait for the interval", best)
	}
}

// A screen that never stops changing is still held to one frame per
// interval: about 30 a second, never a frame per write.
func TestFramesStillThrottledWhileBusy(t *testing.T) {
	t.Setenv("CONCH_SOCKET", "")
	t.Setenv("CONCH_PANE_ID", "")
	c, _ := startServer(t)
	id := subscribedPane(t, c, "/bin/sh", "-c", "i=0; while :; do i=$((i+1)); echo busy-$i; done")

	frames := 0
	end := time.After(time.Second)
	for counting := true; counting; {
		select {
		case m, ok := <-c.Events:
			if !ok {
				t.Fatalf("connection closed: %v", c.Err())
			}
			var f proto.Frame
			if m.Event == proto.EventPaneFrame && json.Unmarshal(m.Data, &f) == nil && f.ID == id {
				frames++
			}
		case <-end:
			counting = false
		}
	}
	// 1 s / 33 ms is 30; one more for a frame in flight at each end.
	if frames > 32 {
		t.Fatalf("%d frames in a second: the interval is not holding", frames)
	}
	if frames < 5 {
		t.Fatalf("only %d frames in a second of constant output", frames)
	}
}

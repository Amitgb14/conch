package cloudagent

import (
	"context"
	"errors"
	"syscall"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// A server the agent started ends with the test that started it — and so
// does one it started again: the agent starts its server whenever its own
// goes, so stopping the server while the agent still ran left a second
// one, detached, listening in a folder the test then removed. Every run
// of the suite could leave one, and they lasted for days.
func TestNoServerOutlivesItsTest(t *testing.T) {
	var pids []int
	t.Run("rig", func(t *testing.T) {
		r := newRig(t)
		r.run()
		r.cloud.waitFor("POST /v1/vm/hello", 1, 20*time.Second)
		r.connect()
		pids = append(pids, r.c.Server.PID)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		r.c.Call(ctx, proto.MethodServerStop, nil, nil)
		for range 500 {
			if c, err := client.Dial(r.opts.ServerSocket, "test"); err == nil {
				pid := c.Server.PID
				c.Close()
				if pid != pids[0] {
					pids = append(pids, pid)
					return
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("the agent never started its server again")
	})
	for _, pid := range pids {
		if pid <= 0 || pid == syscall.Getpid() {
			t.Fatalf("server pid %d", pid)
		}
		if !gone(pid, 5*time.Second) {
			syscall.Kill(pid, syscall.SIGKILL)
			t.Errorf("server %d outlived its test", pid)
		}
	}
}

// gone waits for pid to have exited.
func gone(pid int, within time.Duration) bool {
	for end := time.Now().Add(within); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			return true
		}
	}
	return false
}

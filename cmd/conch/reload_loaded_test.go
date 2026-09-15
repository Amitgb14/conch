package main

import (
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

// A server that keeps its start time across a reload (for uptime) reports
// the reload through loaded_at; the CLI waits for that.
func TestServerReloadKeepsStartTime(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	started := time.Unix(1700000000, 0)
	srv.setHello(func(n int) proto.HelloResult {
		h := currentHello(n)
		h.Started = started // unchanged by the reload
		h.LoadedAt = started
		if n > 1 {
			h.LoadedAt = started.Add(time.Hour)
			h.Build = "newbuild"
		}
		return h
	})
	var err error
	out, _ := a4Capture(t, "", func() { err = runServer([]string{"reload"}) })
	if err != nil || out != "server reloaded: build newbuild, pid 1000 (panes kept)\n" {
		t.Fatalf("reload: %q %v", out, err)
	}
}

func TestLoadedAt(t *testing.T) {
	s, l := time.Unix(100, 0), time.Unix(200, 0)
	if !loadedAt(proto.HelloResult{Started: s, LoadedAt: l}).Equal(l) {
		t.Error("loaded_at preferred")
	}
	if !loadedAt(proto.HelloResult{Started: s}).Equal(s) {
		t.Error("older servers: started")
	}
}

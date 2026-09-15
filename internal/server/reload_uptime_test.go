package server

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// A reloaded server reports when the first server of the chain started, so
// uptime doesn't reset at every reload; state from an older build keeps
// the new process's own start.
func TestReloadKeepsStartTime(t *testing.T) {
	a5IsolateEnv(t)
	hello := func(s *Server) proto.HelloResult {
		t.Helper()
		b, _ := json.Marshal(proto.HelloParams{Protocol: proto.ProtocolVersion})
		res, perr := s.dispatch(nil, proto.Message{ID: "1", Method: proto.MethodHello, Params: b})
		if perr != nil {
			t.Fatal(perr)
		}
		return res.(proto.HelloResult)
	}

	s, _ := a5Server(t)
	own := hello(s).Started
	if time.Since(own) > time.Minute {
		t.Fatalf("a new server's start: %v", own)
	}
	s.adopt(&reloadState{NextID: 3}) // an older build's state: no start time
	if got := hello(s).Started; !got.Equal(own) {
		t.Fatalf("zero start replaced %v with %v", own, got)
	}

	first := time.Now().Add(-26 * time.Hour).Truncate(time.Second)
	s2, _ := a5Server(t)
	s2.adopt(&reloadState{NextID: 3, Started: first})
	if got := hello(s2).Started; !got.Equal(first) {
		t.Fatalf("started %v, want %v", got, first)
	}
	if s2.nextID != 3 {
		t.Fatalf("next id %d", s2.nextID)
	}
}

func TestReloadedServerReportsLoadedAt(t *testing.T) {
	a5IsolateEnv(t)
	s, _ := a5Server(t)
	b, _ := json.Marshal(proto.HelloParams{Protocol: proto.ProtocolVersion})
	res, _ := s.dispatch(nil, proto.Message{ID: "1", Method: proto.MethodHello, Params: b})
	h := res.(proto.HelloResult)
	if h.LoadedAt.IsZero() || h.LoadedAt.Before(h.Started) {
		t.Fatalf("loaded_at %v started %v", h.LoadedAt, h.Started)
	}
	first := time.Now().Add(-time.Hour)
	s.adopt(&reloadState{Started: first})
	res, _ = s.dispatch(nil, proto.Message{ID: "2", Method: proto.MethodHello, Params: b})
	if h2 := res.(proto.HelloResult); !h2.Started.Equal(first) || !h2.LoadedAt.Equal(h.LoadedAt) {
		t.Fatalf("after adopt: started %v loaded %v", h2.Started, h2.LoadedAt)
	}
}

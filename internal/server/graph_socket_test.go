package server_test

import (
	"slices"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

// The run methods are reached through the socket, and the server says it
// has them, so a client can tell an older one.
func TestRunMethodsOverTheSocket(t *testing.T) {
	c, _ := startServer(t)
	if !slices.Contains(c.Server.Capabilities, proto.CapRuns) {
		t.Fatalf("hello lacks %s", proto.CapRuns)
	}
	var list proto.RunList
	if err := call(t, c, proto.MethodRunList, nil, &list); err != nil || list.Runs == nil || len(list.Runs) != 0 {
		t.Fatalf("list %+v %v", list, err)
	}
	wantErr(t, call(t, c, proto.MethodRunCreate, proto.RunCreateParams{Name: "job", ProjectID: "nope"}, nil), "nope")
	wantErr(t, call(t, c, proto.MethodRunGet, proto.RunRef{Run: "job"}, nil), "no run")
	wantErr(t, call(t, c, proto.MethodRunReport, proto.RunReportParams{Status: proto.RunDone}, nil), "name the run")
	wantErr(t, call(t, c, proto.MethodRunApprove, proto.RunApproveParams{Run: "job", Task: "a"}, nil), "no run")
	wantErr(t, call(t, c, proto.MethodRunCreate, map[string]any{"name": 5}, nil), "invalid params")
}

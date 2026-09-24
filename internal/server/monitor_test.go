package server_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// createCat starts a pane that prints only what it is sent, so the test
// decides when there is output.
func createCat(t *testing.T, c *client.Client, dir string) proto.PaneInfo {
	t.Helper()
	var info proto.PaneInfo
	if err := call(t, c, proto.MethodPaneCreate, proto.PaneCreateParams{Command: []string{"/bin/cat"}, Cwd: dir, NoProject: true}, &info); err != nil {
		t.Fatal(err)
	}
	return info
}

func waitAlert(t *testing.T, c *client.Client, id, want string) {
	t.Helper()
	waitEvent(t, c, func(m proto.Message) bool {
		var p proto.PaneInfo
		return m.Event == proto.EventPaneUpdated && json.Unmarshal(m.Data, &p) == nil && p.ID == id && p.Alert == want
	})
}

func TestPaneMonitorActivity(t *testing.T) {
	t.Setenv("CONCH_SOCKET", "")
	t.Setenv("CONCH_PANE_ID", "")
	c, dir := startServer(t)
	info := createCat(t, c, dir)

	var got proto.PaneInfo
	if err := call(t, c, proto.MethodPaneMonitor, proto.PaneMonitorParams{ID: info.ID, PaneMonitor: proto.PaneMonitor{Activity: true}}, &got); err != nil {
		t.Fatal(err)
	}
	if got.Monitor == nil || !got.Monitor.Activity || got.Alert != "" {
		t.Fatalf("after pane.monitor: %+v %q", got.Monitor, got.Alert)
	}
	if err := call(t, c, proto.MethodPaneSendText, proto.PaneSendTextParams{ID: info.ID, Text: "printed\n"}, nil); err != nil {
		t.Fatal(err)
	}
	waitAlert(t, c, info.ID, proto.AlertActivity)

	// Looking at the pane clears it.
	c.Notify(proto.MethodPaneSubscribe, proto.PaneRef{ID: info.ID})
	waitAlert(t, c, info.ID, "")
}

func TestPaneMonitorSilence(t *testing.T) {
	t.Setenv("CONCH_SOCKET", "")
	t.Setenv("CONCH_PANE_ID", "")
	c, dir := startServer(t)
	info := createCat(t, c, dir)
	if err := call(t, c, proto.MethodPaneMonitor, proto.PaneMonitorParams{ID: info.ID, PaneMonitor: proto.PaneMonitor{Silence: 1}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := call(t, c, proto.MethodPaneSendText, proto.PaneSendTextParams{ID: info.ID, Text: "working\n"}, nil); err != nil {
		t.Fatal(err)
	}
	waitAlert(t, c, info.ID, proto.AlertSilence)

	// Marking it seen clears it too.
	if err := call(t, c, proto.MethodPaneMarkSeen, proto.PaneRef{ID: info.ID}, nil); err != nil {
		t.Fatal(err)
	}
	waitAlert(t, c, info.ID, "")

	// Off again: the pane shows no monitoring.
	var got proto.PaneInfo
	if err := call(t, c, proto.MethodPaneMonitor, proto.PaneMonitorParams{ID: info.ID}, &got); err != nil {
		t.Fatal(err)
	}
	if got.Monitor != nil {
		t.Fatalf("monitor after turning it off: %+v", got.Monitor)
	}
}

func TestPaneMonitorBadRequests(t *testing.T) {
	t.Setenv("CONCH_SOCKET", "")
	t.Setenv("CONCH_PANE_ID", "")
	c, dir := startServer(t)
	info := createCat(t, c, dir)
	wantErr(t, call(t, c, proto.MethodPaneMonitor, proto.PaneMonitorParams{ID: info.ID, PaneMonitor: proto.PaneMonitor{Silence: -1}}, nil), "silence")
	if err := call(t, c, proto.MethodPaneMonitor, proto.PaneMonitorParams{ID: "p404", PaneMonitor: proto.PaneMonitor{Activity: true}}, nil); err == nil {
		t.Fatal("unknown pane accepted")
	}
	if err := call(t, c, proto.MethodPaneSearch, proto.PaneSearchParams{ID: "p404", Query: "x"}, nil); err == nil {
		t.Fatal("search of an unknown pane accepted")
	}
}

func TestPaneSearchOverTheProtocol(t *testing.T) {
	t.Setenv("CONCH_SOCKET", "")
	t.Setenv("CONCH_PANE_ID", "")
	c, dir := startServer(t)
	var info proto.PaneInfo
	script := `i=1; while [ $i -le 60 ]; do echo "row-$i"; i=$((i+1)); done; echo Done-Mark; sleep 30`
	if err := call(t, c, proto.MethodPaneCreate, proto.PaneCreateParams{Command: []string{"/bin/sh", "-c", script}, Cwd: dir, NoProject: true, Rows: 10, Cols: 40}, &info); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		var r proto.PaneReadResult
		if call(t, c, proto.MethodPaneRead, proto.PaneRef{ID: info.ID}, &r) == nil && strings.Contains(strings.Join(r.Lines, "\n"), "Done-Mark") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("never printed its marker: %q", r.Lines)
		}
	}
	var res proto.PaneSearchResult
	if err := call(t, c, proto.MethodPaneSearch, proto.PaneSearchParams{ID: info.ID, Query: "row-5", Line: -1}, &res); err != nil {
		t.Fatal(err)
	}
	if !res.Found || res.History == 0 || res.Line >= res.History || res.Width != 5 {
		t.Fatalf("row-5 should be found in history: %+v", res) // the first of row-5, row-50…
	}
	var none proto.PaneSearchResult
	if err := call(t, c, proto.MethodPaneSearch, proto.PaneSearchParams{ID: info.ID, Query: "nowhere"}, &none); err != nil || none.Found {
		t.Fatalf("absent text: %+v %v", none, err)
	}
}

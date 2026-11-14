package server_test

import (
	"os"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// TestSystemStatsNamesConchProcesses reads this machine for real and
// asks whether a pane's program, and what it started, come back named by
// the pane — and the server by itself — however small they are.
func TestSystemStatsNamesConchProcesses(t *testing.T) {
	t.Setenv("CONCH_SOCKET", "")
	t.Setenv("CONCH_PANE_ID", "")
	c, _ := startServer(t)
	var pane proto.PaneInfo
	if err := call(t, c, proto.MethodPaneCreate, proto.PaneCreateParams{Name: "builder",
		Command: []string{"/bin/sh", "-c", "sleep 300 & wait"}}, &pane); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		var st proto.SystemStats
		if err := call(t, c, proto.MethodSystemStats, proto.SystemStatsParams{}, &st); err != nil {
			t.Fatal(err)
		}
		var shell, child, server bool
		for _, p := range st.Processes {
			switch {
			case p.PID == pane.PID:
				shell = p.Pane == pane.ID && p.PaneName == "builder"
			case p.PPID == pane.PID && p.Name == "sleep":
				child = p.Pane == pane.ID
			case p.PID == os.Getpid(): // the server runs in this test's process
				server = p.Conch == "server" && p.Pane == ""
			}
		}
		if shell && child && server {
			if st.Host.Cores < 1 || st.Memory == nil || st.Procs < 3 {
				t.Errorf("stats = host %+v memory %+v procs %d", st.Host, st.Memory, st.Procs)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane %s (pid %d): shell %v child %v server %v among %d processes", pane.ID, pane.PID, shell, child, server, len(st.Processes))
		}
		time.Sleep(200 * time.Millisecond)
	}

	var sum proto.SystemStats
	if err := call(t, c, proto.MethodSystemStats, proto.SystemStatsParams{Summary: true}, &sum); err != nil {
		t.Fatal(err)
	}
	if len(sum.Processes) != 0 || len(sum.History) != 0 {
		t.Errorf("summary listed %d processes, %d points", len(sum.Processes), len(sum.History))
	}
	if err := call(t, c, proto.MethodSystemStats, "not an object", nil); err == nil {
		t.Error("bad params accepted")
	}
}

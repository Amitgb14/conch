package server

import (
	"time"

	"github.com/Amitgb14/conch/internal/detect"
	"github.com/Amitgb14/conch/internal/proto"
)

// Monitoring, as tmux's monitor-activity and monitor-silence: a pane the
// user asked about raises an alert when it prints while nobody is looking
// at it, or when it has gone quiet after printing. Agents say for
// themselves when they need the user; this is for everything else — a
// build, a test run, a server's log.

// paneMonitor is an entry's monitoring. Guarded by entry.mu.
type paneMonitor struct {
	proto.PaneMonitor
	alert   string    // proto.AlertActivity or AlertSilence, until looked at
	lastOut time.Time // when the pane last printed
	armed   bool      // it printed since it last went quiet, so silence counts
}

// step moves the monitoring on after a look at the pane: output says the
// screen changed since the last look, watched that a client shows the pane.
func (m *paneMonitor) step(now time.Time, output, watched bool) {
	if output {
		m.lastOut, m.armed = now, true
		if m.Activity && !watched && m.alert == "" {
			m.alert = proto.AlertActivity
		}
	}
	if m.Silence > 0 && m.armed && now.Sub(m.lastOut) >= time.Duration(m.Silence)*time.Second {
		m.armed = false
		if !watched {
			// Quiet after output usually means something finished, which
			// says more than that it printed.
			m.alert = proto.AlertSilence
		}
	}
}

// set replaces what is monitored. What was raised is dropped, and silence
// counts only after the next output: a shell already sitting quiet at its
// prompt has nothing to report.
func (m *paneMonitor) set(pm proto.PaneMonitor) {
	m.PaneMonitor, m.alert, m.armed = pm, "", false
}

// info is the monitoring as clients see it.
func (m *paneMonitor) info() (*proto.PaneMonitor, string) {
	if m.PaneMonitor == (proto.PaneMonitor{}) {
		return nil, m.alert
	}
	pm := m.PaneMonitor
	return &pm, m.alert
}

func (s *Server) setMonitor(e *entry, pm proto.PaneMonitor) {
	e.evalMu.Lock()
	defer e.evalMu.Unlock()
	s.evaluate(e, func(*detect.Tracker) { e.monitor.set(pm) })
}

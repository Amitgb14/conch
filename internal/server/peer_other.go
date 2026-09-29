//go:build !darwin && !linux

package server

import "net"

// peerPID is 0 where conch doesn't know how to ask: every caller is then
// taken to be outside the panes, as before scoping.
func peerPID(net.Conn) int { return 0 }

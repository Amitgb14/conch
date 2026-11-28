//go:build darwin && cgo

package sysstat

import (
	"os"
	"testing"
)

// TestDarwinFootprints reads this test's own footprint for real — some
// memory — and leaves out a pid that is not there and the nonsense ones.
func TestDarwinFootprints(t *testing.T) {
	me := os.Getpid()
	got := darwinFootprints([]int{me, 0, -1, 1 << 30})
	if got[me] < 1<<20 {
		t.Errorf("own footprint = %d bytes, want at least a megabyte", got[me])
	}
	if len(got) != 1 {
		t.Errorf("footprints = %v, want only this process's", got)
	}
	if Local().Footprints == nil {
		t.Error("a cgo build on macOS reads no footprints")
	}
	if len(darwinFootprints(nil)) != 0 {
		t.Error("no pids, yet footprints")
	}
}

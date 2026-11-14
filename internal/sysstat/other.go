//go:build !linux && !darwin

package sysstat

import (
	"os"
	"runtime"

	"github.com/Amitgb14/conch/internal/proto"
)

// Local is what ps alone can say, on a system conch has no reader for.
func Local() Source {
	return Source{
		Host: func() proto.SystemHost {
			h := proto.SystemHost{OS: runtime.GOOS, Arch: runtime.GOARCH, Cores: runtime.NumCPU()}
			h.Name, _ = os.Hostname()
			return h
		},
		Procs:    psProcs,
		Commands: psCommands,
	}
}

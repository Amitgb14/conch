//go:build darwin && !cgo

package sysstat

// darwinTicks has no way in without cgo: the machine's CPU is then
// reckoned from its processes (Sampler.read).
func darwinTicks() (CPUTicks, bool) { return CPUTicks{}, false }

// darwinFootprints needs proc_pid_rusage, which is cgo's too: without it
// RSS is the figure.
var darwinFootprints func(pids []int) map[int]uint64

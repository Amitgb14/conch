//go:build darwin && !cgo

package sysstat

// darwinTicks has no way in without cgo: the machine's CPU is then
// reckoned from its processes (Sampler.read).
func darwinTicks() (CPUTicks, bool) { return CPUTicks{}, false }

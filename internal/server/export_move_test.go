package server

import "time"

// SetMoveTiming makes servers started after it look for and wait on moves
// for the given times, for the tests outside the package; it gives back a
// restore to run once those servers have stopped (t.Cleanup before
// startServer).
func SetMoveTiming(tick, delay time.Duration) (restore func()) {
	oldTick, oldDelay := moveTick, moveDelay
	moveTick, moveDelay = tick, delay
	return func() { moveTick, moveDelay = oldTick, oldDelay }
}

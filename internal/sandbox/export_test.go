package sandbox

import (
	"testing"
	"time"
)

// What the external tests (sandboxd_test.go) shorten or replace.

func SandboxdPollForTest(t *testing.T, d time.Duration) {
	old := sandboxdPoll
	sandboxdPoll = d
	t.Cleanup(func() { sandboxdPoll = old })
}

func SandboxdListenWaitForTest(t *testing.T, d time.Duration) {
	old := sandboxdListenWait
	sandboxdListenWait = d
	t.Cleanup(func() { sandboxdListenWait = old })
}

func SandboxdSelfForTest(t *testing.T, s *Sandboxd, self string) {
	s.self = func() (string, error) { return self, nil }
}

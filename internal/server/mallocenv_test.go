package server_test

import (
	"os"
	"strings"
	"testing"
)

// A MallocStackLogging variable the server was started with is not passed
// on: every conch it ran said "can't turn off malloc stack logging" into
// server.log, thousands of lines (#199). The server runs in this process,
// so what it leaves in the environment is what its children get.
func TestServerDropsMallocStackLogging(t *testing.T) {
	t.Setenv("MallocStackLogging", "0")
	t.Setenv("MallocStackLoggingNoCompact", "1")
	t.Setenv("MallocScribble", "1") // not stack logging: kept
	startServer(t)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "MallocStackLogging") {
			t.Errorf("still in the environment: %s", kv)
		}
	}
	if os.Getenv("MallocScribble") != "1" {
		t.Error("MallocScribble was dropped too")
	}
}

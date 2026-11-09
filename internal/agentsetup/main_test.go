package agentsetup

import (
	"os"
	"testing"
)

// TestMain keeps every test away from the developer's own conch folder:
// a sync's record goes there, so a test that forgot to set CONCH_HOME
// would write into it.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "conch-agentsetup")
	if err != nil {
		panic(err)
	}
	os.Setenv("CONCH_HOME", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

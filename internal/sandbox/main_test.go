package sandbox

import (
	"os"
	"testing"
)

// Every test starts with a CONCH_HOME of its own making: conch has no
// provider of its own, so one that read the developer's providers folder
// passed on a Mac with daytona installed and failed on CI without it.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "conch-sandbox-home")
	if err != nil {
		panic(err)
	}
	os.Setenv("CONCH_HOME", home)
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}

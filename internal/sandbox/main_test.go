package sandbox

import (
	"os"
	"testing"
)

// testUnset are the variables a provider reads its key, its address or the
// registry's from. A developer who uses Daytona has DAYTONA_API_KEY set,
// and a test that read it would send it to a fake — or, with an address
// of their own set, send a fake's calls to their real account.
var testUnset = []string{
	"CONCH_PROVIDERS_URL",
	"DAYTONA_API_KEY", "DAYTONA_API_URL", "DAYTONA_TARGET", "DAYTONA_ANALYTICS_URL",
	"BOAT_API_KEY", "BOAT_API_URL",
	"TENSORLAKE_API_KEY", "TENSORLAKE_API_URL", "TENSORLAKE_SSH_HOST",
	"E2B_API_KEY", "E2B_API_URL",
	"INSTABOX_API_KEY", "ACME_API_KEY",
	"DEVIN_API_KEY", "DEVIN_API_URL", "DEVIN_ORG_ID",
}

// Every test starts with a CONCH_HOME of its own making: conch has no
// provider of its own, so one that read the developer's providers folder
// passed on a Mac with daytona installed and failed on CI without it.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "conch-sandbox-home")
	if err != nil {
		panic(err)
	}
	os.Setenv("CONCH_HOME", home)
	for _, k := range testUnset {
		os.Unsetenv(k)
	}
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}

package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/integration"
)

// installAcme installs testIntegration at version 1 through the command,
// as somebody would.
func installAcme(t *testing.T, reg *cliRegistry) string {
	t.Helper()
	reg.publish(t, testIntegration, 1, acme)
	if _, _, err := a4Out(t, func() error { return runIntegration([]string{"install", "-y", "acme-ci"}) }); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(os.Getenv("CONCH_HOME"), "integrations", "acme-ci.toml")
}

func acmeVersion(t *testing.T) int {
	t.Helper()
	recs, err := integration.Records()
	if err != nil {
		t.Fatal(err)
	}
	return recs["acme-ci"].Version
}

// A version that keeps its hosts but reads the key from another variable
// sends that variable's value to them: asked about even with -y, and not
// taken with nobody to answer.
func TestIntegrationUpdateAsksAboutAnotherKey(t *testing.T) {
	cases := []struct {
		name, src string
		want      []string
	}{
		{"key_env", strings.Replace(testIntegration, `key_env = "ACME_TOKEN"`, `key_env = "GITHUB_TOKEN"`, 1),
			[]string{"sends $GITHUB_TOKEN to api.acme.dev as your key", "1 sent $ACME_TOKEN", "acme-ci: not updated"}},
		{"user_env", strings.Replace(testIntegration, `key_help = "Acme → Tokens."`, "key_help = \"Acme → Tokens.\"\n[auth]\ntype = \"basic\"\nuser_env = \"ACME_USER\"", 1),
			[]string{"$ACME_TOKEN (and $ACME_USER as the user name) to api.acme.dev", "acme-ci: not updated"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			integrationEnv(t)
			reg := serveRegistry(t)
			path := installAcme(t, reg)
			if recs, _ := integration.Records(); recs["acme-ci"].Key == nil || recs["acme-ci"].Key.KeyEnv != "ACME_TOKEN" {
				t.Fatalf("the install kept no key variable: %+v", recs["acme-ci"])
			}
			reg.publish(t, tc.src, 2, acme)
			out, _, err := a4Out(t, func() error { return runIntegration([]string{"update", "-y"}) })
			if err != nil {
				t.Fatal(err)
			}
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Errorf("update said %q, lacking %q", out, w)
				}
			}
			if acmeVersion(t) != 1 {
				t.Fatal("updated unasked")
			}
			if data, _ := os.ReadFile(path); string(data) != testIntegration {
				t.Fatal("the file was replaced")
			}
		})
	}
}

// A record from before the key's variable was kept is compared with the
// installed file: the same variable updates with -y, another is asked.
func TestIntegrationUpdateOlderRecord(t *testing.T) {
	integrationEnv(t)
	reg := serveRegistry(t)
	installAcme(t, reg)
	recPath := filepath.Join(os.Getenv("CONCH_HOME"), "integrations", "registry.json")
	dropKey := func() {
		t.Helper()
		var m map[string]map[string]any
		data, _ := os.ReadFile(recPath)
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatal(err)
		}
		delete(m["acme-ci"], "key")
		data, _ = json.Marshal(m)
		os.WriteFile(recPath, data, 0o600)
		integration.Forget()
		if recs, err := integration.Records(); err != nil || recs["acme-ci"].Key != nil {
			t.Fatalf("an older record: %+v %v", recs, err)
		}
	}
	dropKey()
	reg.publish(t, strings.Replace(testIntegration, `key_env = "ACME_TOKEN"`, `key_env = "OTHER_TOKEN"`, 1), 2, acme)
	out, _, err := a4Out(t, func() error { return runIntegration([]string{"update", "-y"}) })
	if err != nil || !strings.Contains(out, "sends $OTHER_TOKEN") || !strings.Contains(out, "1 sent $ACME_TOKEN") || acmeVersion(t) != 1 {
		t.Fatalf("another key, older record: %q %v", out, err)
	}
	reg.publish(t, testIntegration+"# v3\n", 3, acme)
	out, _, err = a4Out(t, func() error { return runIntegration([]string{"update", "-y"}) })
	if err != nil || !strings.Contains(out, "updated to 3") {
		t.Fatalf("the same key, older record: %q %v", out, err)
	}
	if recs, _ := integration.Records(); recs["acme-ci"].Key == nil {
		t.Fatal("the update kept no key variable")
	}
}

// An update whose install fails leaves the installed version as it was,
// rather than removed.
func TestIntegrationUpdateFailingInstallKeepsTheOld(t *testing.T) {
	integrationEnv(t)
	reg := serveRegistry(t)
	path := installAcme(t, reg)
	reg.publish(t, testIntegration+"# v2\n", 2, acme)
	old := installIntegration
	installIntegration = func(integration.Entry, []byte) error { return errors.New("disk full") }
	t.Cleanup(func() { installIntegration = old })

	out, _, err := a4Out(t, func() error { return runIntegration([]string{"update", "-y"}) })
	if err == nil || !strings.Contains(err.Error(), "disk full") || !strings.Contains(err.Error(), "version 1 is still installed") {
		t.Fatalf("a failed install: %q %v", out, err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != testIntegration {
		t.Fatalf("the installed file: %v", err)
	}
	if acmeVersion(t) != 1 {
		t.Fatal("the record moved")
	}
	// And with the install working again it updates over the old one.
	installIntegration = old
	out, _, err = a4Out(t, func() error { return runIntegration([]string{"update", "-y"}) })
	if err != nil || !strings.Contains(out, "updated to 2") || acmeVersion(t) != 2 {
		t.Fatalf("update over: %q %v", out, err)
	}
}

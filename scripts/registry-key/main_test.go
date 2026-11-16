package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/integration"
)

func TestSignAndVerify(t *testing.T) {
	priv, pub, err := integration.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	index := filepath.Join(dir, "index.json")
	os.WriteFile(index, []byte(`{"format": 2}`), 0o600)
	t.Setenv("CONCH_REGISTRY_SIGNING_KEY", priv)
	sig, _ := integration.Sign(priv, []byte(`{"format": 2}`))
	sigPath := filepath.Join(dir, "index.json.sig")
	os.WriteFile(sigPath, []byte(sig+"\n"), 0o600)
	if err := run([]string{"verify", pub, index, sigPath}); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(index, []byte(`{"format": 2, "x": 1}`), 0o600)
	if err := run([]string{"verify", pub, index, sigPath}); err == nil {
		t.Fatal("a changed index verified")
	}
	t.Setenv("CONCH_REGISTRY_SIGNING_KEY", "")
	if err := run([]string{"sign", index}); err == nil || !strings.Contains(err.Error(), "CONCH_REGISTRY_SIGNING_KEY") {
		t.Fatalf("no key: %v", err)
	}
	for _, bad := range [][]string{nil, {"what"}, {"sign"}, {"verify", "a"}} {
		if err := run(bad); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}

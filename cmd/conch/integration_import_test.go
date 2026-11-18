package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/integration"
)

// conductorIntegration is the registry's import/conductor file, made to
// run on this platform so a test on Linux finds it too.
func conductorIntegration() string {
	return `format = 3
kind = "import"
name = "conductor"
label = "Conductor"
note = "Conductor's conversations and workspaces, from its own database on this Mac."

[source]
platforms = ["` + runtime.GOOS + `"]
store = "sqlite"
path = "~/Library/Application Support/com.conductor.app/conductor.db"
reader = "conductor"
min_reader = 1

[detect]
files = ["~/Library/Application Support/com.conductor.app/conductor.db"]
`
}

// publishImport lists the Conductor import, its index entry saying
// minReader.
func (r *cliRegistry) publishImport(t *testing.T, src string, minReader int) {
	t.Helper()
	sum := sha256.Sum256([]byte(src))
	e := integration.Entry{Kind: integration.KindImport, Name: "conductor", Label: "Conductor", Version: 1, Format: 3,
		File: "integrations/conductor.toml", SHA256: hex.EncodeToString(sum[:]), Platforms: []string{runtime.GOOS},
		Reader: "conductor", MinReader: minReader}
	ix, _ := json.Marshal(integration.Index{Format: 2, Entries: []integration.Entry{e}})
	r.mu.Lock()
	r.files["index.json"] = ix
	r.files[e.File] = []byte(src)
	r.mu.Unlock()
}

// The issue (#271): "conductor is kind "import", which this conch does
// not read: update conch".
func TestIntegrationImportInstall(t *testing.T) {
	home, stored := integrationEnv(t)
	userHome := t.TempDir()
	t.Setenv("HOME", userHome)
	reg := serveRegistry(t)
	reg.publishImport(t, conductorIntegration(), 1)

	out, _, err := a4Out(t, func() error { return runIntegration([]string{"install", "-y", "conductor"}) })
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	for _, want := range []string{"Conductor reads ~/Library/Application Support/com.conductor.app/conductor.db on this machine; nothing is sent anywhere",
		"installed conductor 1 (import)", "not found on this machine"} {
		if !strings.Contains(out, want) {
			t.Errorf("install said %q, lacking %q", out, want)
		}
	}
	if strings.Contains(out, "key") {
		t.Errorf("an import has no key to ask for: %q", out)
	}
	if b, _ := os.ReadFile(stored); len(b) > 0 {
		t.Fatalf("the Keychain was given %q", b)
	}
	if _, err := os.Stat(filepath.Join(home, "integrations", "conductor.toml")); err != nil {
		t.Fatal(err)
	}

	// Found once Conductor's database is under the home.
	db := filepath.Join(userHome, "Library", "Application Support", "com.conductor.app", "conductor.db")
	os.MkdirAll(filepath.Dir(db), 0o700)
	os.WriteFile(db, nil, 0o600)
	out, _, err = a4Out(t, func() error { return runIntegration([]string{"ls", "-json"}) })
	if err != nil {
		t.Fatal(err)
	}
	var ls struct {
		Integrations []lsRow `json:"integrations"`
		Problems     []string
	}
	json.Unmarshal([]byte(out), &ls)
	var row *lsRow
	for i := range ls.Integrations {
		if ls.Integrations[i].Name == "conductor" {
			row = &ls.Integrations[i]
		}
	}
	if row == nil || row.State != "found on this machine" || len(ls.Problems) > 0 {
		t.Fatalf("ls: %s", out)
	}

	out, _, err = a4Out(t, func() error { return runIntegration([]string{"info", "-json", "conductor"}) })
	if err != nil {
		t.Fatal(err)
	}
	var info infoJSON
	if err := json.Unmarshal([]byte(out), &info); err != nil {
		t.Fatal(err)
	}
	if info.Kind != "import" || !info.Installed || info.Reader != "conductor" || info.MinReader != 1 ||
		len(info.Found) != 1 || info.Found[0] != db || info.NeedsNewer != "" || info.KeyEnv != "" || len(info.Does) == 0 {
		t.Fatalf("info: %s", out)
	}
	out, _, err = a4Out(t, func() error { return runIntegration([]string{"info", "conductor"}) })
	if err != nil || strings.Contains(out, "Your key") || !strings.Contains(out, "nothing is sent anywhere") || !strings.Contains(out, "conch import conductor") {
		t.Fatalf("info: %q %v", out, err)
	}
	// An import has no operations, so it has no line listing none.
	if strings.Contains(out, "What it can do") {
		t.Errorf("info lists what an import can do, which is nothing:\n%s", out)
	}

	// conch integration check reads an import file (the registry's CI
	// checks import/*/integration.toml with it).
	file := filepath.Join(t.TempDir(), "integration.toml")
	os.WriteFile(file, []byte(conductorIntegration()), 0o600)
	out, _, err = a4Out(t, func() error { return runIntegration([]string{"check", file}) })
	if err != nil || out != "conductor is fine\n" {
		t.Fatalf("check: %q %v", out, err)
	}
	os.WriteFile(file, []byte(strings.Replace(conductorIntegration(), "min_reader = 1", "min_reader = 7", 1)), 0o600)
	out, _, err = a4Out(t, func() error { return runIntegration([]string{"check", file}) })
	if err == nil || !strings.Contains(out, "needs a newer conch") {
		t.Fatalf("check of a newer reader: %q %v", out, err)
	}
}

func TestIntegrationImportNeedsNewerReader(t *testing.T) {
	home, _ := integrationEnv(t)
	t.Setenv("HOME", t.TempDir())
	reg := serveRegistry(t)
	src := strings.Replace(conductorIntegration(), "min_reader = 1", "min_reader = 2", 1)
	reg.publishImport(t, src, 2)

	out, _, err := a4Out(t, func() error { return runIntegration([]string{"search", "-json"}) })
	if err != nil || !strings.Contains(out, `"needs_newer":"reader \"conductor\" version 2`) {
		t.Fatalf("search: %q %v", out, err)
	}
	out, _, err = a4Out(t, func() error { return runIntegration([]string{"search"}) })
	if err != nil || !strings.Contains(out, "needs a newer conch") || !strings.Contains(out, "this machine only") || strings.Contains(out, "your own server") {
		t.Fatalf("search: %q %v", out, err)
	}
	out, _, err = a4Out(t, func() error { return runIntegration([]string{"info", "-json", "conductor"}) })
	if err != nil || !strings.Contains(out, `"needs_newer":"reader \"conductor\" version 2`) {
		t.Fatalf("info: %q %v", out, err)
	}
	_, _, err = a4Out(t, func() error { return runIntegration([]string{"install", "-y", "conductor"}) })
	if err == nil || !strings.Contains(err.Error(), "Conductor needs a newer conch") {
		t.Fatalf("install: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "integrations", "conductor.toml")); err == nil {
		t.Fatal("installed a file this conch cannot read")
	}
}

func TestIntegrationImportFoundBeforeInstall(t *testing.T) {
	integrationEnv(t)
	userHome := t.TempDir()
	t.Setenv("HOME", userHome)
	reg := serveRegistry(t)
	src := conductorIntegration()
	sum := sha256.Sum256([]byte(src))
	const path = "~/Library/Application Support/com.conductor.app/conductor.db"
	e := integration.Entry{Kind: integration.KindImport, Name: "conductor", Label: "Conductor", Version: 1, Format: 3,
		File: "integrations/conductor.toml", SHA256: hex.EncodeToString(sum[:]), Platforms: []string{runtime.GOOS},
		Reader: "conductor", MinReader: 1, Capabilities: &integration.Capabilities{Detect: []string{"files"}, DetectOn: map[string][]string{"files": {path}}}}
	ix, _ := json.Marshal(integration.Index{Format: 2, Entries: []integration.Entry{e}})
	reg.files["index.json"], reg.files[e.File] = ix, []byte(src)
	db := integration.HomePath(path, userHome)
	os.MkdirAll(filepath.Dir(db), 0o700)
	os.WriteFile(db, nil, 0o600)
	out, _, err := a4Out(t, func() error { return runIntegration([]string{"info", "-json", "conductor"}) })
	var info infoJSON
	if err != nil || json.Unmarshal([]byte(out), &info) != nil || info.Installed || len(info.Found) != 1 || info.Found[0] != db {
		t.Fatalf("info: %q %v", out, err)
	}
	out, _, err = a4Out(t, func() error { return runIntegration([]string{"search", "-json"}) })
	if err != nil || !strings.Contains(out, `"found":[`+mustJSON(db)+`]`) {
		t.Fatalf("search: %q %v", out, err)
	}
}

func mustJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

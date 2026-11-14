package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/update"
)

// latestServer says the latest release is latest, and counts the asking.
func latestServer(t *testing.T, latest string) *int {
	t.Helper()
	asked := new(int)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/latest" {
			*asked++
			w.Header().Set("Location", "https://github.com/Amitgb14/conch/releases/tag/v"+latest)
			w.WriteHeader(http.StatusFound)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("CONCH_RELEASE_URL", srv.URL)
	return asked
}

func TestAppUpdateCheck(t *testing.T) {
	a4Env(t)
	latestServer(t, "0.2.1")
	var err error
	out, _ := a4Capture(t, "", func() { err = runApp([]string{"update", "check", "-json", "-version", "0.2.0"}) })
	if err != nil {
		t.Fatal(err)
	}
	var c update.AppCheck
	if err := json.Unmarshal([]byte(out), &c); err != nil {
		t.Fatalf("%v: %q", err, out)
	}
	if !c.Newer || c.Latest != "0.2.1" || c.Current != "0.2.0" || !strings.Contains(c.Notes, "v0.2.1") {
		t.Fatalf("%+v", c)
	}

	out, _ = a4Capture(t, "", func() { err = runApp([]string{"update", "check", "-version", "0.2.0"}) })
	if err != nil || !strings.Contains(out, "Conch 0.2.1 is available") || !strings.Contains(out, "0.2.0") {
		t.Fatalf("text: %q %v", out, err)
	}
	out, _ = a4Capture(t, "", func() { err = runApp([]string{"update", "check", "-version", "0.2.1"}) })
	if err != nil || !strings.Contains(out, "up to date") {
		t.Fatalf("current: %q %v", out, err)
	}
}

func TestAppUpdateCheckDevBuild(t *testing.T) {
	a4Env(t)
	asked := latestServer(t, "9.9.9")
	var err error
	out, _ := a4Capture(t, "", func() { err = runApp([]string{"update", "check", "-version", "0.2.1-dev"}) })
	if err != nil || !strings.Contains(out, "built from source") {
		t.Fatalf("%q %v", out, err)
	}
	out, _ = a4Capture(t, "", func() { err = runApp([]string{"update", "check", "-json", "-version", "0.2.1-dev"}) })
	if err != nil || !strings.Contains(out, `"dev":true`) {
		t.Fatalf("json: %q %v", out, err)
	}
	if *asked != 0 {
		t.Fatalf("a development build asked %d times", *asked)
	}
}

func TestAppUpdateCheckFails(t *testing.T) {
	a4Env(t) // CONCH_RELEASE_URL refuses
	var err error
	a4Capture(t, "", func() { err = runApp([]string{"update", "check", "-version", "0.2.0"}) })
	if err == nil {
		t.Fatal("no error")
	}
}

func TestAppUpdateInstallRefuses(t *testing.T) {
	a4Env(t)
	var err error
	// Not run from inside a bundle, and none named.
	a4Capture(t, "", func() { err = runApp([]string{"update", "install", "0.2.1"}) })
	if err == nil || !strings.Contains(err.Error(), "-app") {
		t.Fatalf("no bundle: %v", err)
	}
	a4Capture(t, "", func() {
		err = runApp([]string{"update", "install", "-app", "/private/var/folders/x/AppTranslocation/1/d/Conch.app", "0.2.1"})
	})
	if err == nil || !strings.Contains(err.Error(), "Translocation") {
		t.Fatalf("translocated: %v", err)
	}
	a4Capture(t, "", func() { err = runApp([]string{"update", "install", "-app", "/Applications/Conch.app"}) })
	if err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("no version: %v", err)
	}
	a4Capture(t, "", func() { err = runApp([]string{"update", "frob"}) })
	if err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("unknown verb: %v", err)
	}
}

func TestAppUpdateRollback(t *testing.T) {
	a4Env(t)
	app := filepath.Join(t.TempDir(), "Conch.app")
	os.MkdirAll(filepath.Join(app, "Contents"), 0o755)
	os.WriteFile(filepath.Join(app, "Contents", "Info.plist"),
		[]byte("<key>CFBundleShortVersionString</key><string>0.2.1</string>"), 0o644)
	var err error
	a4Capture(t, "", func() { err = runApp([]string{"update", "rollback", "-app", app}) })
	if err == nil || !strings.Contains(err.Error(), "no previous") {
		t.Fatalf("nothing kept: %v", err)
	}
	// Kept from an update: swapped back, and said.
	prev := filepath.Join(filepath.Dir(app), ".Conch.app.previous")
	os.MkdirAll(filepath.Join(prev, "Contents"), 0o755)
	os.WriteFile(filepath.Join(prev, "Contents", "Info.plist"),
		[]byte("<key>CFBundleShortVersionString</key><string>0.2.0</string>"), 0o644)
	out, _ := a4Capture(t, "", func() { err = runApp([]string{"update", "rollback", "-app", app}) })
	if err != nil || !strings.Contains(out, "0.2.0") {
		t.Fatalf("rollback: %q %v", out, err)
	}
	if v, ok := update.PreviousApp(app); !ok || v != "0.2.1" {
		t.Fatalf("previous after rollback: %q %v", v, ok)
	}
}

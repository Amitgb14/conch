package integration

import (
	"os"
	"path/filepath"
	"testing"
)

// Which variables a version reads is kept at install, and a version that
// reads another is a change; an older record without it is compared with
// the installed file.
func TestKeyChange(t *testing.T) {
	isolate(t)
	e := Entry{Name: "acme-ci", Registry: "test", Version: 1, KeyEnv: "ACME_TOKEN", Hosts: acmeHosts}
	if err := Install(e, []byte(good)); err != nil {
		t.Fatal(err)
	}
	recs, _ := Records()
	rec := recs["acme-ci"]
	if rec.Key == nil || *rec.Key != (KeyVars{KeyEnv: "ACME_TOKEN"}) {
		t.Fatalf("record %+v", rec)
	}
	if _, _, changed := KeyChange("acme-ci", rec, e); changed {
		t.Fatal("the same key read as changed")
	}
	other := e
	other.KeyEnv = "GITHUB_TOKEN"
	if was, known, changed := KeyChange("acme-ci", rec, other); !changed || !known || was.KeyEnv != "ACME_TOKEN" {
		t.Fatalf("another key: %+v %v %v", was, known, changed)
	}
	user := e
	user.UserEnv = "ACME_USER"
	if _, _, changed := KeyChange("acme-ci", rec, user); !changed {
		t.Fatal("a user name now sent was not noticed")
	}

	// A record from before: the "key" member is absent and it still loads.
	old := []byte(`{"acme-ci":{"registry":"test","version":1,"sha256":"x","hosts":{"api":"api.acme.dev"},"installed":"2026-11-01"}}`)
	if err := os.WriteFile(recordsPath(), old, 0o600); err != nil {
		t.Fatal(err)
	}
	recs, err := Records()
	if err != nil || recs["acme-ci"].Key != nil || recs["acme-ci"].Version != 1 {
		t.Fatalf("an older record: %+v %v", recs, err)
	}
	rec = recs["acme-ci"]
	if was, known, changed := KeyChange("acme-ci", rec, e); changed || !known || was.KeyEnv != "ACME_TOKEN" {
		t.Fatalf("older record, the file's own key: %+v %v %v", was, known, changed)
	}
	if was, known, changed := KeyChange("acme-ci", rec, other); !changed || !known || was.KeyEnv != "ACME_TOKEN" {
		t.Fatalf("older record, another key: %+v %v %v", was, known, changed)
	}
	// Nothing to read it from: a change, said as not known.
	path := filepath.Join(Dir(), "acme-ci.toml")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, known, changed := KeyChange("acme-ci", rec, e); !changed || known {
		t.Fatalf("no file: known %v changed %v", known, changed)
	}
	if err := os.WriteFile(path, []byte("not = [toml"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, known, changed := KeyChange("acme-ci", rec, e); !changed || known {
		t.Fatalf("a broken file: known %v changed %v", known, changed)
	}
	if _, known, changed := KeyChange("../x", rec, e); !changed || known {
		t.Fatal("a path for a name")
	}
}

package cloud

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/relay"
)

// These test what is not exported; the rest of the package's tests are
// cloud_test, which can reach the fake control plane (cloudtest).

func isolate(t *testing.T) {
	t.Helper()
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("CONCH_HOME", filepath.Join(dir, "conch"))
	t.Setenv("HOME", dir)
}

func TestKeyPinning(t *testing.T) {
	isolate(t)
	created := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	m := Machine{ID: "m_aaaaaaaaaaaaaaaa", Name: "box", CreatedAt: created}
	if err := checkPin(m, "key-one"); err != nil {
		t.Fatalf("first use: %v", err)
	}
	if err := checkPin(m, "key-one"); err != nil {
		t.Fatalf("same key: %v", err)
	}
	err := checkPin(m, "key-two")
	var kc *KeyChangedError
	if !errors.As(err, &kc) || !strings.Contains(err.Error(), "forget-key m_aaaaaaaaaaaaaaaa") {
		t.Fatalf("changed key: %v", err)
	}
	// Recreated: a new created_at, a new key, pinned again.
	m.CreatedAt = created.Add(time.Hour)
	if err := checkPin(m, "key-two"); err != nil {
		t.Fatalf("recreated: %v", err)
	}
	if err := checkPin(m, "key-one"); err == nil {
		t.Fatal("the old key passed after re-pinning")
	}
	// Forgotten: whatever comes next is pinned.
	if err := ForgetKey(m.ID); err != nil {
		t.Fatal(err)
	}
	if err := checkPin(m, "key-three"); err != nil {
		t.Fatal(err)
	}
	if err := ForgetKey("m_bbbbbbbbbbbbbbbb"); err == nil {
		t.Fatal("forgot a key never pinned")
	}
	st, _ := os.Stat(KeysPath())
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("cloud-keys.json is %04o", st.Mode().Perm())
	}
	if err := checkPin(m, ""); err == nil {
		t.Fatal("an empty key passed")
	}
}

func TestDecodeKey(t *testing.T) {
	k, _ := relay.NewKey()
	for _, s := range []string{relay.EncodeKey(k.Public), relay.EncodeKey(k.Public) + "="} {
		if b, err := decodeKey(s); err != nil || !bytes.Equal(b, k.Public) {
			t.Fatalf("%q: %v", s, err)
		}
	}
	if _, err := decodeKey("c2hvcnQ"); err == nil {
		t.Fatal("a short key decoded")
	}
	if _, err := decodeKey("!!"); err == nil {
		t.Fatal("garbage decoded")
	}
}

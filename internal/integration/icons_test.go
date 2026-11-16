package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64"><rect width="64" height="64" fill="#343434"/></svg>`

func sum(b string) string {
	s := sha256.Sum256([]byte(b))
	return hex.EncodeToString(s[:])
}

// An integration's icon is downloaded once, checked against the index's
// hash, and kept under a name that changes with it; anything that is not
// a small SVG or PNG at an icons/ path is refused, and nothing is kept.
func TestFetchIcon(t *testing.T) {
	t.Setenv("CONCH_HOME", t.TempDir())
	fr := newFakeRegistry(t, "")
	fr.files["icons/circleci.svg"] = []byte(testSVG)
	r := Registry{Name: "test", URL: fr.srv.URL}
	e := Entry{Name: "circleci", Icon: "icons/circleci.svg", IconSHA256: sum(testSVG)}
	ctx := context.Background()

	got, err := FetchIcon(ctx, r, e)
	if err != nil || filepath.Dir(got) != IconsDir() || !strings.HasPrefix(filepath.Base(got), "circleci-") || filepath.Ext(got) != ".svg" {
		t.Fatalf("fetched %q %v", got, err)
	}
	if b, _ := os.ReadFile(got); string(b) != testSVG {
		t.Fatalf("kept %q", b)
	}
	// Kept: asked again, the registry is not.
	delete(fr.files, "icons/circleci.svg")
	if again, err := FetchIcon(ctx, r, e); err != nil || again != got {
		t.Fatalf("again %q %v", again, err)
	}
	// A new icon replaces the old one.
	newer := strings.Replace(testSVG, "343434", "000000", 1)
	fr.files["icons/circleci.svg"] = []byte(newer)
	e2 := e
	e2.IconSHA256 = sum(newer)
	if p, err := FetchIcon(ctx, r, e2); err != nil || p == got {
		t.Fatalf("a new icon: %q %v", p, err)
	}
	if _, err := os.Stat(got); !os.IsNotExist(err) {
		t.Fatal("the old icon was left")
	}

	// No icon is no path, and no error.
	if p, err := FetchIcon(ctx, r, Entry{Name: "x"}); p != "" || err != nil {
		t.Fatalf("none: %q %v", p, err)
	}
	for _, c := range []struct {
		e    Entry
		body string
		want string
	}{
		{Entry{Name: "ab", Icon: "icons/ab.svg", IconSHA256: sum("x")}, testSVG, "hash"},
		{Entry{Name: "ab", Icon: "../ab.svg", IconSHA256: sum(testSVG)}, testSVG, "path"},
		{Entry{Name: "ab", Icon: "icons/ab.gif", IconSHA256: sum(testSVG)}, testSVG, "path"},
		{Entry{Name: "ab", Icon: "icons/ab.svg", IconSHA256: "short"}, testSVG, "hash"},
		{Entry{Name: "ab", Icon: "icons/ab.svg", IconSHA256: sum("not a picture")}, "not a picture", "not an SVG"},
		{Entry{Name: "ab", Icon: "icons/ab.png", IconSHA256: sum("not a picture")}, "not a picture", "not a PNG"},
		{Entry{Name: "ab", Icon: "icons/ab.svg", IconSHA256: sum(strings.Repeat("<svg>", 8000))}, strings.Repeat("<svg>", 8000), "bytes"},
	} {
		fr.files[strings.TrimPrefix(c.e.Icon, "../")] = []byte(c.body)
		if _, err := FetchIcon(ctx, r, c.e); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.e.Icon, err, c.want)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(IconsDir(), "ab-*")); len(left) != 0 {
		t.Fatalf("a refused icon was kept: %v", left)
	}
}

// The index's kinds are read, and an index from before them has none; the
// last index read is kept for when the registry cannot be reached.
func TestIndexKindsAndCache(t *testing.T) {
	t.Setenv("CONCH_HOME", t.TempDir())
	fr := newFakeRegistry(t, "")
	fr.files["index.json"] = []byte(`{"format":2,"kinds":[{"kind":"ci","label":"CI","about":"checks"},{"kind":"deploy","label":"Deploys"}],"entries":[{"kind":"ci","name":"circleci","label":"CircleCI","version":1,"icon":"icons/circleci.svg","icon_sha256":"` + sum(testSVG) + `","hosts":{"api":"circleci.com"}}]}`)
	r := Registry{Name: "test", URL: fr.srv.URL}
	ctx := context.Background()
	ix, err := FetchIndex(ctx, r)
	if err != nil || len(ix.Kinds) != 2 || ix.Kinds[1].Label != "Deploys" || ix.Entries[0].Icon != "icons/circleci.svg" {
		t.Fatalf("index %+v %v", ix, err)
	}
	b, _ := json.Marshal(ix.Kinds[0])
	if string(b) != `{"kind":"ci","label":"CI","about":"checks"}` {
		t.Fatalf("kind %s", b)
	}
	// The registry gone: the last one read, said to be that.
	fr.srv.Close()
	ix, err = IndexOrCached(ctx, r)
	if err != nil || !ix.Cached || len(ix.Entries) != 1 {
		t.Fatalf("cached %+v %v", ix, err)
	}
	// Never read and gone: the error.
	if _, err := IndexOrCached(ctx, Registry{Name: "other", URL: fr.srv.URL}); err == nil {
		t.Fatal("no cache, no registry")
	}
	if ix, _ := ParseIndex([]byte(`{"format":2,"entries":[]}`)); len(ix.Kinds) != 0 {
		t.Fatal("an older index has kinds")
	}
}

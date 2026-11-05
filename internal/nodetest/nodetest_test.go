package nodetest

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// outcome stands in for a test, to see whether Path skipped or failed it
// without doing either to this one. Path calls only these three; the
// embedded nil TB would panic on anything else, which would say so.
type outcome struct {
	testing.TB
	fatal, skip string
}

func (o *outcome) Helper()                   {}
func (o *outcome) Fatalf(f string, a ...any) { o.fatal = fmt.Sprintf(f, a...) }
func (o *outcome) Skip(a ...any)             { o.skip = fmt.Sprint(a...) }

func TestRequired(t *testing.T) {
	for _, c := range []struct {
		ci   string
		want bool
	}{
		{"", false}, {"0", false}, {"false", false},
		{"true", true}, {"1", true}, {"yes", true},
	} {
		t.Setenv("CI", c.ci)
		if got := Required(); got != c.want {
			t.Errorf("CI=%q: Required() = %v, want %v", c.ci, got, c.want)
		}
	}
}

// A scratch PATH with a node on it, or with nothing.
func withPath(t *testing.T, node bool) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	if !node {
		return ""
	}
	p := filepath.Join(dir, "node")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPathFindsNode(t *testing.T) {
	want := withPath(t, true)
	for _, ci := range []string{"", "true"} {
		t.Setenv("CI", ci)
		o := &outcome{}
		if got := Path(o); got != want || o.fatal != "" || o.skip != "" {
			t.Fatalf("CI=%q: %q fatal=%q skip=%q, want %q", ci, got, o.fatal, o.skip, want)
		}
	}
}

func TestPathWithoutNodeSkipsOutsideCI(t *testing.T) {
	withPath(t, false)
	t.Setenv("CI", "")
	o := &outcome{}
	if got := Path(o); got != "" || o.skip == "" || o.fatal != "" {
		t.Fatalf("%q fatal=%q skip=%q: want a skip", got, o.fatal, o.skip)
	}
}

func TestPathWithoutNodeFailsUnderCI(t *testing.T) {
	withPath(t, false)
	t.Setenv("CI", "true")
	o := &outcome{}
	if got := Path(o); got != "" || o.fatal == "" || o.skip != "" {
		t.Fatalf("%q fatal=%q skip=%q: want a failure", got, o.fatal, o.skip)
	}
}

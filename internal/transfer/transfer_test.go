package transfer

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, data string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), mode); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPackUnpackRoundTrip(t *testing.T) {
	src := t.TempDir()
	write(t, filepath.Join(src, "a.txt"), "alpha", 0o644)
	write(t, filepath.Join(src, "bin", "run.sh"), "#!/bin/sh\n", 0o755)
	write(t, filepath.Join(src, ".hidden"), "dot", 0o600)
	write(t, filepath.Join(src, "deep", "er", "x"), "", 0o644) // empty file
	os.Mkdir(filepath.Join(src, "empty"), 0o755)
	os.Symlink("a.txt", filepath.Join(src, "link"))
	os.Symlink("/nowhere/at/all", filepath.Join(src, "broken"))

	var buf bytes.Buffer
	if err := Pack(&buf, src); err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	if err := Unpack(&buf, dst, false); err != nil {
		t.Fatal(err)
	}
	for rel, want := range map[string]string{"a.txt": "alpha", "bin/run.sh": "#!/bin/sh\n", ".hidden": "dot", "deep/er/x": ""} {
		if got := read(t, filepath.Join(dst, rel)); got != want {
			t.Errorf("%s = %q, want %q", rel, got, want)
		}
	}
	if st, err := os.Stat(filepath.Join(dst, "bin", "run.sh")); err != nil || st.Mode().Perm() != 0o755 {
		t.Errorf("mode not kept: %v %v", st, err)
	}
	if st, err := os.Stat(filepath.Join(dst, ".hidden")); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("private mode not kept: %v %v", st, err)
	}
	if st, err := os.Stat(filepath.Join(dst, "empty")); err != nil || !st.IsDir() {
		t.Errorf("empty folder lost: %v", err)
	}
	for name, want := range map[string]string{"link": "a.txt", "broken": "/nowhere/at/all"} {
		if got, err := os.Readlink(filepath.Join(dst, name)); err != nil || got != want {
			t.Errorf("%s -> %q %v, want %q", name, got, err, want)
		}
	}
}

func TestPackRefusesFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "f")
	write(t, f, "x", 0o644)
	if err := Pack(&bytes.Buffer{}, f); err == nil {
		t.Fatal("packed a file as a folder")
	}
	if err := Pack(&bytes.Buffer{}, filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("packed a missing folder")
	}
}

func TestPackEmptyFolder(t *testing.T) {
	var buf bytes.Buffer
	if err := Pack(&buf, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	if err := Unpack(&buf, dst, false); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dst); len(entries) != 0 {
		t.Fatalf("unpacked %v from an empty folder", entries)
	}
}

func TestUnpackExisting(t *testing.T) {
	src := t.TempDir()
	write(t, filepath.Join(src, "a.txt"), "new", 0o644)
	write(t, filepath.Join(src, "sub", "b.txt"), "b", 0o644)
	var buf bytes.Buffer
	if err := Pack(&buf, src); err != nil {
		t.Fatal(err)
	}
	archive := buf.Bytes()

	dst := t.TempDir()
	write(t, filepath.Join(dst, "a.txt"), "old", 0o644)
	write(t, filepath.Join(dst, "keep.txt"), "mine", 0o644)
	if err := Unpack(bytes.NewReader(archive), dst, false); !errors.Is(err, os.ErrExist) {
		t.Fatalf("overwrote without replace: %v", err)
	}
	if got := read(t, filepath.Join(dst, "a.txt")); got != "old" {
		t.Fatalf("a.txt = %q", got)
	}
	// With replace the folder is merged: written over, and nothing else lost.
	if err := Unpack(bytes.NewReader(archive), dst, true); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(dst, "a.txt")) != "new" || read(t, filepath.Join(dst, "keep.txt")) != "mine" || read(t, filepath.Join(dst, "sub", "b.txt")) != "b" {
		t.Fatal("replace did not merge")
	}
	// A folder where a file goes is not removed, even with replace.
	dst2 := t.TempDir()
	os.Mkdir(filepath.Join(dst2, "a.txt"), 0o755)
	if err := Unpack(bytes.NewReader(archive), dst2, true); err == nil || !strings.Contains(err.Error(), "folder is in the way") {
		t.Fatalf("folder in the way: %v", err)
	}
}

// evil builds an archive by hand, as a hostile machine would send.
func evil(t *testing.T, entries ...tar.Header) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, h := range entries {
		h := h
		body := ""
		if h.Typeflag == tar.TypeReg {
			body = "pwned"
			h.Size = int64(len(body))
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(body))
	}
	tw.Close()
	zw.Close()
	return buf.Bytes()
}

func TestUnpackStaysInside(t *testing.T) {
	outside := t.TempDir()
	for name, archive := range map[string][]byte{
		"dotdot":    evil(t, tar.Header{Name: "../escape", Typeflag: tar.TypeReg, Mode: 0o644}),
		"deep":      evil(t, tar.Header{Name: "a/../../escape", Typeflag: tar.TypeReg, Mode: 0o644}),
		"absolute":  evil(t, tar.Header{Name: filepath.Join(outside, "escape"), Typeflag: tar.TypeReg, Mode: 0o644}),
		"empty":     evil(t, tar.Header{Name: "", Typeflag: tar.TypeReg, Mode: 0o644}),
		"backslash": evil(t, tar.Header{Name: `..\escape`, Typeflag: tar.TypeReg, Mode: 0o644}),
		// A link to outside, then a file written through it.
		"through link": evil(t,
			tar.Header{Name: "out", Typeflag: tar.TypeSymlink, Linkname: outside},
			tar.Header{Name: "out/escape", Typeflag: tar.TypeReg, Mode: 0o644}),
		// The same, with the file named as the link itself.
		"onto link": evil(t,
			tar.Header{Name: "x", Typeflag: tar.TypeSymlink, Linkname: filepath.Join(outside, "escape")},
			tar.Header{Name: "x", Typeflag: tar.TypeReg, Mode: 0o644}),
	} {
		t.Run(name, func(t *testing.T) {
			dst := t.TempDir()
			Unpack(bytes.NewReader(archive), dst, false)
			if _, err := os.Lstat(filepath.Join(outside, "escape")); err == nil {
				t.Fatal("wrote outside the folder")
			}
			if _, err := os.Lstat(filepath.Join(filepath.Dir(dst), "escape")); err == nil {
				t.Fatal("wrote beside the folder")
			}
		})
	}
	// With replace, a file over a link replaces the link and leaves its
	// target alone.
	dst := t.TempDir()
	target := filepath.Join(outside, "victim")
	write(t, target, "safe", 0o644)
	archive := evil(t,
		tar.Header{Name: "x", Typeflag: tar.TypeSymlink, Linkname: target},
		tar.Header{Name: "x", Typeflag: tar.TypeReg, Mode: 0o644})
	if err := Unpack(bytes.NewReader(archive), dst, true); err != nil {
		t.Fatal(err)
	}
	if read(t, target) != "safe" {
		t.Fatal("wrote through a symlink")
	}
	if st, _ := os.Lstat(filepath.Join(dst, "x")); st == nil || !st.Mode().IsRegular() {
		t.Fatal("the link was not replaced")
	}
}

func TestUnpackNotAnArchive(t *testing.T) {
	dst := t.TempDir()
	if err := Unpack(strings.NewReader("hello"), dst, false); err == nil {
		t.Fatal("unpacked plain text")
	}
	// Cut short: what was there is an error, not a silent half.
	var buf bytes.Buffer
	src := t.TempDir()
	write(t, filepath.Join(src, "big"), strings.Repeat("x", 100000), 0o644)
	Pack(&buf, src)
	if err := Unpack(bytes.NewReader(buf.Bytes()[:buf.Len()/2]), t.TempDir(), false); err == nil {
		t.Fatal("unpacked a truncated archive")
	}
	if err := Unpack(&buf, filepath.Join(dst, "missing"), false); err == nil {
		t.Fatal("unpacked into a missing folder")
	}
	f := filepath.Join(dst, "file")
	write(t, f, "", 0o644)
	if err := Unpack(&buf, f, false); err == nil {
		t.Fatal("unpacked into a file")
	}
}

func TestFreeName(t *testing.T) {
	dir := t.TempDir()
	if got := FreeName(dir, "ca.pem"); got != "ca.pem" {
		t.Fatalf("free: %q", got)
	}
	write(t, filepath.Join(dir, "ca.pem"), "", 0o644)
	if got := FreeName(dir, "ca.pem"); got != "ca 2.pem" {
		t.Fatalf("taken: %q", got)
	}
	write(t, filepath.Join(dir, "ca 2.pem"), "", 0o644)
	if got := FreeName(dir, "ca.pem"); got != "ca 3.pem" {
		t.Fatalf("twice taken: %q", got)
	}
	write(t, filepath.Join(dir, ".bashrc"), "", 0o644)
	if got := FreeName(dir, ".bashrc"); got != ".bashrc 2" {
		t.Fatalf("dotfile: %q", got)
	}
	os.Mkdir(filepath.Join(dir, "logs"), 0o755)
	if got := FreeName(dir, "logs"); got != "logs 2" {
		t.Fatalf("folder: %q", got)
	}
	os.Symlink("/nowhere", filepath.Join(dir, "dangling"))
	if got := FreeName(dir, "dangling"); got != "dangling 2" {
		t.Fatalf("a broken link is still taken: %q", got)
	}
	if got := FreeName(filepath.Join(dir, "missing"), "x"); got != "x" {
		t.Fatalf("missing folder: %q", got)
	}
}

func TestSafeName(t *testing.T) {
	for in, want := range map[string]string{
		"ca.pem": "ca.pem", "../../etc/passwd": "passwd", `..\x`: "x", "": "file", ".": "file", "..": "file",
		"a/": "file", "bad\x1b]0;title\x07.txt": "bad]0;title.txt", "  spaced  ": "spaced", "my file.txt": "my file.txt",
	} {
		if got := SafeName(in); got != want {
			t.Errorf("SafeName(%q) = %q, want %q", in, got, want)
		}
	}
}

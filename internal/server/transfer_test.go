package server_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// Copying files and folders to and from a machine (fs.transfer.v1), the
// way `conch upload -to`, `conch download` and the Files view do. Here
// both ends are on one machine; the server cannot tell.

func transferEnv(t *testing.T) (c *client.Client, home string) {
	t.Helper()
	home = t.TempDir()
	home, _ = filepath.EvalSymlinks(home)
	t.Setenv("HOME", home)
	c, _ = startServer(t)
	if missing := c.MissingCapabilities([]string{proto.CapFSTransfer}); len(missing) > 0 {
		t.Fatalf("not advertised: %v", missing)
	}
	return c, home
}

func transferCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func put(t *testing.T, path, data string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func got(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func upload(t *testing.T, c *client.Client, local, dir string, replace bool) (string, error) {
	t.Helper()
	up, err := client.OpenUpload(c, local)
	if err != nil {
		t.Fatal(err)
	}
	defer up.Close()
	return up.Into(dir, replace).Run(transferCtx(t))
}

func TestUploadFileIntoFolder(t *testing.T) {
	c, home := transferEnv(t)
	src := t.TempDir()
	put(t, filepath.Join(src, "ca.pem"), "cert")
	os.Mkdir(filepath.Join(home, "certs"), 0o755)

	// From ~, as somebody types it.
	path, err := upload(t, c, filepath.Join(src, "ca.pem"), "~/certs", false)
	if err != nil || path != filepath.Join(home, "certs", "ca.pem") || got(t, path) != "cert" {
		t.Fatalf("upload: %q %v", path, err)
	}
	// Again: numbered rather than written over.
	put(t, filepath.Join(src, "ca.pem"), "cert 2")
	path, err = upload(t, c, filepath.Join(src, "ca.pem"), filepath.Join(home, "certs"), false)
	if err != nil || filepath.Base(path) != "ca 2.pem" || got(t, path) != "cert 2" || got(t, filepath.Join(home, "certs", "ca.pem")) != "cert" {
		t.Fatalf("second upload: %q %v", path, err)
	}
	// With replace, written over in place.
	path, err = upload(t, c, filepath.Join(src, "ca.pem"), "~/certs", true)
	if err != nil || filepath.Base(path) != "ca.pem" || got(t, path) != "cert 2" {
		t.Fatalf("replace: %q %v", path, err)
	}
	// Nothing half-sent is left behind in the folder.
	entries, _ := os.ReadDir(filepath.Join(home, "certs"))
	for _, e := range entries {
		if strings.Contains(e.Name(), ".part") {
			t.Fatalf("left %s", e.Name())
		}
	}
	// Into the home folder itself.
	if path, err = upload(t, c, filepath.Join(src, "ca.pem"), "~", false); err != nil || path != filepath.Join(home, "ca.pem") {
		t.Fatalf("home: %q %v", path, err)
	}
}

func TestUploadKeepsMode(t *testing.T) {
	c, home := transferEnv(t)
	src := t.TempDir()
	script := filepath.Join(src, "run.sh")
	put(t, script, "#!/bin/sh\n")
	os.Chmod(script, 0o755)
	path, err := upload(t, c, script, home, false)
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(path); st == nil || st.Mode().Perm() != 0o755 {
		t.Fatalf("mode: %v", st)
	}
	// A file sent from bytes has no mode to keep: an ordinary one.
	path, err = client.NewUpload(c, "notes.txt", []byte("hi")).Into(home, false).Run(transferCtx(t))
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(path); st == nil || st.Mode().Perm() != 0o644 {
		t.Fatalf("default mode: %v", st)
	}
}

func TestUploadEmptyAndChunkedFiles(t *testing.T) {
	c, home := transferEnv(t)
	src := t.TempDir()
	put(t, filepath.Join(src, "empty"), "")
	big := strings.Repeat("0123456789abcdef", proto.UploadChunkSize/16*2+7) // three chunks
	put(t, filepath.Join(src, "big"), big)
	for name, want := range map[string]string{"empty": "", "big": big} {
		path, err := upload(t, c, filepath.Join(src, name), home, false)
		if err != nil || got(t, path) != want {
			t.Fatalf("%s: %q %v", name, path, err)
		}
	}
}

func TestUploadFolder(t *testing.T) {
	c, home := transferEnv(t)
	src := filepath.Join(t.TempDir(), "site")
	put(t, filepath.Join(src, "index.html"), "<h1>")
	put(t, filepath.Join(src, "css", "a.css"), "body{}")
	os.Mkdir(filepath.Join(src, "empty"), 0o755)

	path, err := upload(t, c, src, "~", false)
	if err != nil || path != filepath.Join(home, "site") {
		t.Fatalf("upload: %q %v", path, err)
	}
	if got(t, filepath.Join(path, "index.html")) != "<h1>" || got(t, filepath.Join(path, "css", "a.css")) != "body{}" {
		t.Fatal("contents")
	}
	if st, err := os.Stat(filepath.Join(path, "empty")); err != nil || !st.IsDir() {
		t.Fatal("empty folder lost")
	}
	// Again: a second folder beside it, the first untouched.
	put(t, filepath.Join(src, "index.html"), "<h2>")
	path, err = upload(t, c, src, home, false)
	if err != nil || filepath.Base(path) != "site 2" || got(t, filepath.Join(home, "site", "index.html")) != "<h1>" {
		t.Fatalf("second: %q %v", path, err)
	}
	// With replace, merged into the first: changed files written over,
	// files only there kept.
	put(t, filepath.Join(home, "site", "mine.txt"), "keep")
	path, err = upload(t, c, src, home, true)
	if err != nil || path != filepath.Join(home, "site") || got(t, filepath.Join(path, "index.html")) != "<h2>" || got(t, filepath.Join(path, "mine.txt")) != "keep" {
		t.Fatalf("replace: %q %v", path, err)
	}
	// Without a folder, into the uploads folder like a dropped file.
	up, err := client.OpenUpload(c, src)
	if err != nil {
		t.Fatal(err)
	}
	defer up.Close()
	path, err = up.Run(transferCtx(t))
	if err != nil || filepath.Base(path) != "site" || !strings.Contains(path, string(filepath.Separator)+"uploads"+string(filepath.Separator)) {
		t.Fatalf("to uploads: %q %v", path, err)
	}
	if got(t, filepath.Join(path, "css", "a.css")) != "body{}" {
		t.Fatal("uploads contents")
	}
}

func TestUploadRefused(t *testing.T) {
	c, home := transferEnv(t)
	src := t.TempDir()
	put(t, filepath.Join(src, "f"), "x")
	put(t, filepath.Join(home, "file"), "")
	for name, tc := range map[string]struct{ dir, says string }{
		"missing folder": {"~/nope", filepath.Join(home, "nope") + " doesn't exist"},
		"not a folder":   {"~/file", "is not a folder"},
	} {
		if _, err := upload(t, c, filepath.Join(src, "f"), tc.dir, false); err == nil || !strings.Contains(err.Error(), tc.says) {
			t.Errorf("%s: %v, want %q", name, err, tc.says)
		}
	}
	// A folder where a file would be replaced is not removed.
	os.Mkdir(filepath.Join(home, "f"), 0o755)
	if _, err := upload(t, c, filepath.Join(src, "f"), home, true); err == nil {
		t.Fatal("replaced a folder with a file")
	}
	if st, err := os.Stat(filepath.Join(home, "f")); err != nil || !st.IsDir() {
		t.Fatal("the folder is gone")
	}
	// The other way: a file where a folder would be merged.
	folder := filepath.Join(src, "file")
	put(t, filepath.Join(folder, "x"), "x")
	if _, err := upload(t, c, folder, home, true); err == nil {
		t.Fatal("merged a folder into a file")
	}
	// An archive that is not one leaves nothing behind.
	var res proto.FSUploadResult
	err := c.Call(transferCtx(t), proto.MethodFSUpload, proto.FSUploadParams{Name: "junk", Size: 5, Data: []byte("hello"), Final: true, Dir: home, Archive: true}, &res)
	if err == nil {
		t.Fatal("unpacked junk")
	}
	if _, err := os.Stat(filepath.Join(home, "junk")); err == nil {
		t.Fatal("left a half folder")
	}
}

func download(t *testing.T, c *client.Client, remote, dest string, replace bool) (string, error) {
	t.Helper()
	d := client.NewDownload(c, remote, dest, replace)
	defer d.Close()
	return d.Run(transferCtx(t))
}

func TestDownloadFile(t *testing.T) {
	c, home := transferEnv(t)
	big := strings.Repeat("x", proto.UploadChunkSize*2+3)
	put(t, filepath.Join(home, "logs", "big.log"), big)
	put(t, filepath.Join(home, "empty"), "")
	dest := t.TempDir()

	path, err := download(t, c, "~/logs/big.log", dest, false)
	if err != nil || path != filepath.Join(dest, "big.log") || got(t, path) != big {
		t.Fatalf("download: %q %v", path, err)
	}
	if path, err = download(t, c, filepath.Join(home, "empty"), dest, false); err != nil || got(t, path) != "" {
		t.Fatalf("empty: %q %v", path, err)
	}
	// Again: numbered, the first kept.
	put(t, filepath.Join(home, "logs", "big.log"), "short")
	if path, err = download(t, c, "~/logs/big.log", dest, false); err != nil || filepath.Base(path) != "big 2.log" || got(t, path) != "short" {
		t.Fatalf("again: %q %v", path, err)
	}
	if path, err = download(t, c, "~/logs/big.log", dest, true); err != nil || filepath.Base(path) != "big.log" || got(t, path) != "short" {
		t.Fatalf("replace: %q %v", path, err)
	}
	// The original stays where it was: a download is read in place, not moved.
	if got(t, filepath.Join(home, "logs", "big.log")) != "short" {
		t.Fatal("the original is gone")
	}
	entries, _ := os.ReadDir(dest)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".part") {
			t.Fatalf("left %s", e.Name())
		}
	}
}

func TestDownloadFolder(t *testing.T) {
	c, home := transferEnv(t)
	put(t, filepath.Join(home, "proj", "main.go"), "package main")
	put(t, filepath.Join(home, "proj", "internal", "x.go"), "package x")
	os.Symlink("main.go", filepath.Join(home, "proj", "link"))
	dest := t.TempDir()

	path, err := download(t, c, "~/proj", dest, false)
	if err != nil || path != filepath.Join(dest, "proj") {
		t.Fatalf("download: %q %v", path, err)
	}
	if got(t, filepath.Join(path, "main.go")) != "package main" || got(t, filepath.Join(path, "internal", "x.go")) != "package x" {
		t.Fatal("contents")
	}
	if l, _ := os.Readlink(filepath.Join(path, "link")); l != "main.go" {
		t.Fatalf("link: %q", l)
	}
	if path, err = download(t, c, "~/proj", dest, false); err != nil || filepath.Base(path) != "proj 2" {
		t.Fatalf("again: %q %v", path, err)
	}
	// The home folder itself, by ~.
	if path, err = download(t, c, "~", dest, false); err != nil || got(t, filepath.Join(path, "proj", "main.go")) != "package main" {
		t.Fatalf("home: %q %v", path, err)
	}
}

func TestDownloadRefused(t *testing.T) {
	c, home := transferEnv(t)
	dest := t.TempDir()
	put(t, filepath.Join(home, "f"), "x")
	if _, err := download(t, c, "~/missing", dest, false); err == nil || !strings.Contains(err.Error(), filepath.Join(home, "missing")+" doesn't exist") {
		t.Fatalf("downloaded nothing: %v", err)
	}
	if _, err := download(t, c, "", dest, false); err == nil {
		t.Fatal("downloaded an empty path")
	}
	if _, err := download(t, c, "~/f", filepath.Join(dest, "missing"), false); err == nil {
		t.Fatal("into a missing folder")
	}
	if _, err := download(t, c, "~/f", filepath.Join(home, "f"), false); err == nil {
		t.Fatal("into a file")
	}
	// A fifo would hang the read: refused before.
	fifo := filepath.Join(home, "pipe")
	if err := mkfifo(fifo); err == nil {
		if _, err := download(t, c, fifo, dest, false); err == nil {
			t.Fatal("downloaded a fifo")
		}
	}
	// An ID nobody gave out.
	var chunk proto.FSDownloadChunk
	if err := c.Call(transferCtx(t), proto.MethodFSDownloadRead, proto.FSDownloadReadParams{ID: "nope"}, &chunk); err == nil {
		t.Fatal("read an unknown download")
	}
	// A download that is read to the end is forgotten.
	var info proto.FSDownload
	if err := c.Call(transferCtx(t), proto.MethodFSDownload, proto.FSDownloadParams{Path: "~/f"}, &info); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(transferCtx(t), proto.MethodFSDownloadRead, proto.FSDownloadReadParams{ID: info.ID}, &chunk); err != nil || !chunk.EOF || string(chunk.Data) != "x" {
		t.Fatalf("read: %+v %v", chunk, err)
	}
	if err := c.Call(transferCtx(t), proto.MethodFSDownloadRead, proto.FSDownloadReadParams{ID: info.ID}, &chunk); err == nil {
		t.Fatal("read twice")
	}
	if got(t, filepath.Join(home, "f")) != "x" {
		t.Fatal("the downloaded file was deleted with the download")
	}
}

func TestDownloadFileShrinks(t *testing.T) {
	c, home := transferEnv(t)
	f := filepath.Join(home, "growing.log")
	put(t, f, strings.Repeat("x", proto.UploadChunkSize+10))
	var info proto.FSDownload
	if err := c.Call(transferCtx(t), proto.MethodFSDownload, proto.FSDownloadParams{Path: f}, &info); err != nil {
		t.Fatal(err)
	}
	put(t, f, "cut") // rotated meanwhile
	var chunk proto.FSDownloadChunk
	err := c.Call(transferCtx(t), proto.MethodFSDownloadRead, proto.FSDownloadReadParams{ID: info.ID}, &chunk)
	if err == nil || !strings.Contains(err.Error(), "shorter") {
		t.Fatalf("read a shrunk file: %d bytes, %v", len(chunk.Data), err)
	}
	if bytes.Contains(chunk.Data, []byte{0}) {
		t.Fatal("zeros passed off as the file")
	}
}

func TestBrowseMachineFiles(t *testing.T) {
	c, home := transferEnv(t)
	put(t, filepath.Join(home, "notes.txt"), "hello")
	put(t, filepath.Join(home, ".secret"), "s")
	os.Mkdir(filepath.Join(home, "code"), 0o755)

	var list proto.FSList
	if err := c.Call(transferCtx(t), proto.MethodFSList, proto.FSListParams{Path: "~", Files: true}, &list); err != nil {
		t.Fatal(err)
	}
	if list.Path != home || len(list.Entries) != 2 || list.Entries[0].Name != "code" || !list.Entries[0].Dir || list.Entries[1].Name != "notes.txt" || list.Entries[1].Size != 5 {
		t.Fatalf("list: %+v", list)
	}
	if err := c.Call(transferCtx(t), proto.MethodFSList, proto.FSListParams{Path: "~", Files: true, Hidden: true}, &list); err != nil || len(list.Entries) != 3 {
		t.Fatalf("hidden: %+v %v", list, err)
	}
	var res proto.FSReadResult
	if err := c.Call(transferCtx(t), proto.MethodFSRead, proto.FSReadParams{Path: "~/notes.txt"}, &res); err != nil || res.Data != "hello" || res.Path != filepath.Join(home, "notes.txt") {
		t.Fatalf("read: %+v %v", res, err)
	}
	if err := c.Call(transferCtx(t), proto.MethodFSRead, proto.FSReadParams{Path: "~/code"}, &res); err == nil {
		t.Fatal("read a folder")
	}
	if err := c.Call(transferCtx(t), proto.MethodFSList, proto.FSListParams{Path: "~/missing", Files: true}, &list); err == nil {
		t.Fatal("listed a missing folder")
	}
}

func mkfifo(path string) error { return syscall.Mkfifo(path, 0o600) }

// manyChunks is n whole chunks and a few bytes more, each chunk different,
// so one written in the wrong place shows.
func manyChunks(n int) string {
	var b strings.Builder
	for i := range n {
		b.WriteString(strings.Repeat(string(rune('a'+i%26)), proto.UploadChunkSize))
	}
	return b.String() + "tail!"
}

func TestTransferManyChunksBothWays(t *testing.T) {
	c, home := transferEnv(t)
	src, dest := t.TempDir(), t.TempDir()
	big := manyChunks(21)
	put(t, filepath.Join(src, "big.bin"), big)
	put(t, filepath.Join(src, "tree", "a", "big.bin"), big) // a folder that packs to many chunks
	put(t, filepath.Join(src, "tree", "b.txt"), "b")

	for _, name := range []string{"big.bin", "tree"} {
		there, err := upload(t, c, filepath.Join(src, name), "~", false)
		if err != nil || there != filepath.Join(home, name) {
			t.Fatalf("upload %s: %q %v", name, there, err)
		}
		back, err := download(t, c, there, dest, false)
		if err != nil || back != filepath.Join(dest, name) {
			t.Fatalf("download %s: %q %v", name, back, err)
		}
	}
	if got(t, filepath.Join(home, "big.bin")) != big || got(t, filepath.Join(dest, "big.bin")) != big ||
		got(t, filepath.Join(dest, "tree", "a", "big.bin")) != big || got(t, filepath.Join(dest, "tree", "b.txt")) != "b" {
		t.Fatal("not the same bytes")
	}
	for _, dir := range []string{home, dest} {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if strings.Contains(e.Name(), ".part") {
				t.Fatalf("left behind in %s: %s", dir, e.Name())
			}
		}
	}
}

// sendChunk sends one chunk of a parallel upload by hand.
func sendChunk(t *testing.T, c *client.Client, p proto.FSUploadParams) (proto.FSUploadResult, error) {
	t.Helper()
	var res proto.FSUploadResult
	err := c.Call(transferCtx(t), proto.MethodFSUpload, p, &res)
	return res, err
}

func TestUploadChunksOutOfOrder(t *testing.T) {
	c, home := transferEnv(t)
	data := []byte(manyChunks(3))
	at := func(i int) []byte { return data[i*proto.UploadChunkSize : min((i+1)*proto.UploadChunkSize, len(data))] }
	res, err := sendChunk(t, c, proto.FSUploadParams{Name: "f", Size: int64(len(data)), Dir: "~", Parallel: true, Data: at(0)})
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{2, 1} {
		if _, err := sendChunk(t, c, proto.FSUploadParams{Upload: res.Upload, Offset: int64(i * proto.UploadChunkSize), Data: at(i)}); err != nil {
			t.Fatalf("chunk %d: %v", i, err)
		}
	}
	res, err = sendChunk(t, c, proto.FSUploadParams{Upload: res.Upload, Offset: 3 * proto.UploadChunkSize, Data: at(3), Final: true})
	if err != nil || res.Path != filepath.Join(home, "f") || got(t, res.Path) != string(data) {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestUploadParallelChunkRefused(t *testing.T) {
	const chunk = proto.UploadChunkSize
	data := []byte(manyChunks(3)) // three whole chunks and five bytes
	for name, tc := range map[string]struct {
		p    proto.FSUploadParams
		says string
	}{
		"between chunks":   {proto.FSUploadParams{Offset: chunk + 1, Data: data[:chunk]}, "no chunk"},
		"before the start": {proto.FSUploadParams{Offset: -chunk, Data: data[:chunk]}, "no chunk"},
		"past the end":     {proto.FSUploadParams{Offset: 4 * chunk, Data: data[:5], Final: true}, "no chunk"},
		"short":            {proto.FSUploadParams{Offset: chunk, Data: data[:chunk-1]}, "not"},
		"again":            {proto.FSUploadParams{Offset: 0, Data: data[:chunk]}, "twice"},
		"last not final":   {proto.FSUploadParams{Offset: 3 * chunk, Data: data[3*chunk:]}, "last chunk"},
		"final too soon":   {proto.FSUploadParams{Offset: chunk, Data: data[chunk : 2*chunk], Final: true}, "last chunk"},
		"final, gaps":      {proto.FSUploadParams{Offset: 3 * chunk, Data: data[3*chunk:], Final: true}, "ended after"},
	} {
		t.Run(name, func(t *testing.T) {
			c, home := transferEnv(t)
			res, err := sendChunk(t, c, proto.FSUploadParams{Name: "f", Size: int64(len(data)), Dir: "~", Parallel: true, Data: data[:chunk]})
			if err != nil {
				t.Fatal(err)
			}
			tc.p.Upload = res.Upload
			if _, err := sendChunk(t, c, tc.p); err == nil || !strings.Contains(err.Error(), tc.says) {
				t.Fatalf("got %v, want %q", err, tc.says)
			}
			// The upload is abandoned: nothing half-written is left, and
			// it takes no more chunks.
			if entries, _ := os.ReadDir(home); len(entries) != 0 {
				t.Fatalf("left behind: %v", entries)
			}
			if _, err := sendChunk(t, c, proto.FSUploadParams{Upload: res.Upload, Offset: chunk, Data: data[chunk : 2*chunk]}); err == nil {
				t.Fatal("took a chunk after refusing one")
			}
		})
	}
}

func TestDownloadShrinksWithReadsInFlight(t *testing.T) {
	c, home := transferEnv(t)
	f := filepath.Join(home, "app.log")
	put(t, f, manyChunks(20))
	dest := t.TempDir()
	d := client.NewDownload(c, f, dest, false)
	defer d.Close()
	if done, err := d.Step(transferCtx(t)); err != nil || done {
		t.Fatalf("start: %v %v", done, err)
	}
	put(t, f, manyChunks(2)) // rotated while it was copied
	if _, err := d.Run(transferCtx(t)); err == nil || !strings.Contains(err.Error(), "shorter") {
		t.Fatalf("got %v", err)
	}
	if entries, _ := os.ReadDir(dest); len(entries) != 0 {
		t.Fatalf("left behind: %v", entries)
	}
}

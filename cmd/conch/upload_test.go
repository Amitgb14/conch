package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/server"
)

func TestUploadCommand(t *testing.T) {
	dir := a4Env(t)
	srv := server.New(config.SocketPath(), dir)
	go srv.Run()
	t.Cleanup(func() {
		srv.Stop()
		for i := 0; i < 100; i++ {
			if _, err := os.Stat(config.SocketPath()); err != nil {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
	})
	for i := 0; ; i++ {
		if c, err := client.Dial(config.SocketPath(), "test"); err == nil {
			c.Close()
			break
		}
		if i == 200 {
			t.Fatal("server did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}

	files := t.TempDir()
	shot := filepath.Join(files, "Shot 1.png")
	big := filepath.Join(files, "big.bin")
	bigData := bytes.Repeat([]byte{7}, proto.UploadChunkSize+1)
	os.WriteFile(shot, []byte("png"), 0o600)
	os.WriteFile(big, bigData, 0o600)

	var err error
	out, _ := a4Capture(t, "", func() { err = runUpload([]string{shot, big}) })
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if err != nil || len(lines) != 2 || filepath.Base(lines[0]) != "Shot 1.png" || filepath.Base(lines[1]) != "big.bin" {
		t.Fatalf("upload: %q %v", out, err)
	}
	for i, want := range [][]byte{[]byte("png"), bigData} {
		if !strings.HasPrefix(lines[i], filepath.Join(dir, "uploads")+"/") {
			t.Fatalf("stored outside uploads: %s", lines[i])
		}
		if got, _ := os.ReadFile(lines[i]); !bytes.Equal(got, want) {
			t.Fatalf("%s: wrong bytes", lines[i])
		}
	}

	// Nothing is uploaded when one of the paths can't be.
	for name, args := range map[string][]string{
		"missing": {shot, filepath.Join(files, "gone.png")},
		"device":  {shot, "/dev/null"},
	} {
		out, _ := a4Capture(t, "", func() { err = runUpload(args) })
		if err == nil || out != "" {
			t.Errorf("%s: %q %v", name, out, err)
		}
	}
	if err := runUpload(nil); err == nil || !strings.Contains(err.Error(), "usage: conch [-m MACHINE] upload [-to DIR] [-f] PATH...") {
		t.Fatalf("usage: %v", err)
	}
}

func TestUploadCommandOldServer(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	srv.setHello(func(n int) proto.HelloResult {
		h := currentHello(n)
		h.Capabilities = []string{"pane.v1"}
		return h
	})
	file := filepath.Join(t.TempDir(), "a.png")
	os.WriteFile(file, []byte("x"), 0o600)
	var err error
	a4Capture(t, "", func() { err = runUpload([]string{file}) })
	if err == nil || !strings.Contains(err.Error(), "older and can't take uploads") {
		t.Fatalf("old server: %v", err)
	}

	srv.setHello(currentHello)
	srv.setHandle(func(proto.Message, *proto.Conn) (any, *proto.Error) {
		return nil, proto.Errorf(proto.ErrBadRequest, "disk full")
	})
	a4Capture(t, "", func() { err = runUpload([]string{file}) })
	if err == nil || !strings.Contains(err.Error(), "a.png: bad_request: disk full") {
		t.Fatalf("server error: %v", err)
	}
}

// realServer runs a conch server on the test's socket, for commands that
// copy files through it.
func realServer(t *testing.T, dir string) {
	t.Helper()
	srv := server.New(config.SocketPath(), dir)
	go srv.Run()
	t.Cleanup(func() {
		srv.Stop()
		for i := 0; i < 100; i++ {
			if _, err := os.Stat(config.SocketPath()); err != nil {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
	})
	for i := 0; ; i++ {
		if c, err := client.Dial(config.SocketPath(), "test"); err == nil {
			c.Close()
			return
		}
		if i == 200 {
			t.Fatal("server did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestUploadToFolderCommand(t *testing.T) {
	realServer(t, a4Env(t))
	home := os.Getenv("HOME")
	os.Mkdir(filepath.Join(home, "certs"), 0o755)
	src := t.TempDir()
	pem := filepath.Join(src, "ca.pem")
	os.WriteFile(pem, []byte("cert"), 0o600)
	site := filepath.Join(src, "site")
	os.MkdirAll(filepath.Join(site, "css"), 0o755)
	os.WriteFile(filepath.Join(site, "css", "a.css"), []byte("body{}"), 0o644)

	var err error
	out, _ := a4Capture(t, "", func() { err = runUpload([]string{"-to", "~/certs", pem, site}) })
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if err != nil || len(lines) != 2 || lines[0] != filepath.Join(home, "certs", "ca.pem") || lines[1] != filepath.Join(home, "certs", "site") {
		t.Fatalf("upload: %q %v", out, err)
	}
	if b, _ := os.ReadFile(filepath.Join(home, "certs", "site", "css", "a.css")); string(b) != "body{}" {
		t.Fatal("folder contents")
	}
	// Again: numbered; with -f, written over.
	out, _ = a4Capture(t, "", func() { err = runUpload([]string{"-to", "~/certs", pem}) })
	if err != nil || filepath.Base(strings.TrimSpace(out)) != "ca 2.pem" {
		t.Fatalf("again: %q %v", out, err)
	}
	os.WriteFile(pem, []byte("new"), 0o600)
	out, _ = a4Capture(t, "", func() { err = runUpload([]string{"-f", "-to", "~/certs", pem}) })
	if b, _ := os.ReadFile(filepath.Join(home, "certs", "ca.pem")); err != nil || string(b) != "new" {
		t.Fatalf("-f: %q %v", out, err)
	}
	// A folder there that isn't: nothing copied, the error says why.
	out, _ = a4Capture(t, "", func() { err = runUpload([]string{"-to", "~/nope", pem}) })
	if err == nil || out != "" || !strings.Contains(err.Error(), "ca.pem") {
		t.Fatalf("missing folder: %q %v", out, err)
	}
	if err := runUpload([]string{"-bogus", pem}); err == nil {
		t.Fatal("unknown flag")
	}
}

func TestDownloadCommand(t *testing.T) {
	realServer(t, a4Env(t))
	home := os.Getenv("HOME")
	os.MkdirAll(filepath.Join(home, "proj", "sub"), 0o755)
	os.WriteFile(filepath.Join(home, "proj", "sub", "x.go"), []byte("package x"), 0o644)
	os.WriteFile(filepath.Join(home, "ca.pem"), []byte("cert"), 0o600)
	dest := t.TempDir()

	var err error
	out, _ := a4Capture(t, "", func() { err = runDownload([]string{"-to", dest, "~/ca.pem", "~/proj"}) })
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if err != nil || len(lines) != 2 || lines[0] != filepath.Join(dest, "ca.pem") || lines[1] != filepath.Join(dest, "proj") {
		t.Fatalf("download: %q %v", out, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "proj", "sub", "x.go")); string(b) != "package x" {
		t.Fatal("folder contents")
	}
	out, _ = a4Capture(t, "", func() { err = runDownload([]string{"-to", dest, "~/ca.pem"}) })
	if err != nil || filepath.Base(strings.TrimSpace(out)) != "ca 2.pem" {
		t.Fatalf("again: %q %v", out, err)
	}
	// Into the current folder by default.
	t.Chdir(dest)
	os.WriteFile(filepath.Join(home, "ca.pem"), []byte("new"), 0o600)
	out, _ = a4Capture(t, "", func() { err = runDownload([]string{"-f", "~/ca.pem"}) })
	if b, _ := os.ReadFile(filepath.Join(dest, "ca.pem")); err != nil || string(b) != "new" {
		t.Fatalf("-f into .: %q %v", out, err)
	}

	for name, args := range map[string][]string{
		"missing there": {"-to", dest, "~/gone"},
		"missing here":  {"-to", filepath.Join(dest, "gone"), "~/ca.pem"},
		"file here":     {"-to", filepath.Join(dest, "ca.pem"), "~/ca.pem"},
	} {
		if _, err := a4CaptureErr(t, func() error { return runDownload(args) }); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if err := runDownload(nil); err == nil || !strings.Contains(err.Error(), downloadUsage) {
		t.Fatalf("usage: %v", err)
	}
}

func TestCopyCommandsOldServer(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	srv.setHello(func(n int) proto.HelloResult {
		h := currentHello(n)
		h.Capabilities = []string{"pane.v1", "fs.upload.v1"} // drops, but nothing more
		return h
	})
	src := t.TempDir()
	file := filepath.Join(src, "a.png")
	os.WriteFile(file, []byte("x"), 0o600)
	for name, run := range map[string]func() error{
		"upload -to":    func() error { return runUpload([]string{"-to", "~", file}) },
		"upload folder": func() error { return runUpload([]string{src}) },
		"download":      func() error { return runDownload([]string{"-to", src, "~/a"}) },
	} {
		if _, err := a4CaptureErr(t, run); err == nil || !strings.Contains(err.Error(), "conch machine upgrade") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

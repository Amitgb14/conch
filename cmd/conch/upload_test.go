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

	// Nothing is uploaded when one of the files can't be read.
	for name, args := range map[string][]string{
		"missing": {shot, filepath.Join(files, "gone.png")},
		"folder":  {shot, files},
	} {
		out, _ := a4Capture(t, "", func() { err = runUpload(args) })
		if err == nil || out != "" {
			t.Errorf("%s: %q %v", name, out, err)
		}
	}
	if err := runUpload(nil); err == nil || !strings.Contains(err.Error(), "usage: conch [-m MACHINE] upload FILE...") {
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

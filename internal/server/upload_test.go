package server_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

func uploadCall(t *testing.T, c *client.Client, p proto.FSUploadParams) (proto.FSUploadResult, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var res proto.FSUploadResult
	err := c.Call(ctx, proto.MethodFSUpload, p, &res)
	return res, err
}

func runUpload(t *testing.T, c *client.Client, name string, data []byte) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	path, err := client.NewUpload(c, name, data).Run(ctx)
	if err != nil {
		t.Fatalf("upload %s: %v", name, err)
	}
	return path
}

// uploadFiles lists the files under dir/uploads, relative to it.
func uploadFiles(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	root := filepath.Join(dir, "uploads")
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			rel, _ := filepath.Rel(root, p)
			out = append(out, rel)
		}
		return nil
	})
	return out
}

func TestUploadStoresFile(t *testing.T) {
	c, dir := startServer(t)
	if len(c.MissingCapabilities([]string{"fs.upload.v1"})) > 0 {
		t.Fatal("fs.upload.v1 not advertised")
	}
	small := []byte("\x89PNG small")
	big := bytes.Repeat([]byte("0123456789abcdef"), proto.UploadChunkSize/16*2+100) // three chunks
	for _, tc := range []struct {
		name string
		data []byte
	}{{"Shot 1.png", small}, {"big.bin", big}, {"empty.txt", nil}} {
		path := runUpload(t, c, tc.name, tc.data)
		if filepath.Base(path) != tc.name || !strings.HasPrefix(path, filepath.Join(dir, "uploads", time.Now().Format("2006-01-02"))+"/") {
			t.Fatalf("%s stored at %s", tc.name, path)
		}
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, tc.data) {
			t.Fatalf("%s: %d bytes, want %d (%v)", tc.name, len(got), len(tc.data), err)
		}
		st, _ := os.Stat(path)
		folder, _ := os.Stat(filepath.Dir(path))
		day, _ := os.Stat(filepath.Dir(filepath.Dir(path)))
		if st.Mode().Perm() != 0o600 || folder.Mode().Perm() != 0o700 || day.Mode().Perm() != 0o700 {
			t.Fatalf("%s modes: file %v folder %v day %v", tc.name, st.Mode(), folder.Mode(), day.Mode())
		}
	}
	if files := uploadFiles(t, dir); len(files) != 3 || slices.ContainsFunc(files, func(f string) bool { return strings.HasSuffix(f, ".part") }) {
		t.Fatalf("left behind: %v", files)
	}
}

func TestUploadSameNameAtOnce(t *testing.T) {
	c, _ := startServer(t)
	var wg sync.WaitGroup
	paths := make([]string, 4)
	for i := range paths {
		wg.Add(1)
		go func() {
			defer wg.Done()
			data := bytes.Repeat([]byte{byte('a' + i)}, proto.UploadChunkSize+i)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			path, err := client.NewUpload(c, "same.png", data).Run(ctx)
			if err != nil {
				t.Errorf("upload %d: %v", i, err)
				return
			}
			if got, _ := os.ReadFile(path); !bytes.Equal(got, data) {
				t.Errorf("upload %d: wrong bytes", i)
			}
			paths[i] = path
		}()
	}
	wg.Wait()
	slices.Sort(paths)
	if len(slices.Compact(paths)) != 4 {
		t.Fatalf("uploads collided: %v", paths)
	}
}

func TestUploadNames(t *testing.T) {
	c, dir := startServer(t)
	long := strings.Repeat("é", 150) + ".png" // 300 bytes of name
	for _, tc := range []struct{ name, want string }{
		{"../../etc/passwd", "passwd"},
		{"a/b.png", "b.png"},
		{"..", "file"},
		{"", "file"},
		{"/", "file"},
		{"bad\x00na\x1bme\n.png", "badname.png"},
		{"Screenshot 2026-09-16 at 10.02.11 AM.png", "Screenshot 2026-09-16 at 10.02.11 AM.png"},
		{long, strings.Repeat("é", 98) + ".png"},
	} {
		path := runUpload(t, c, tc.name, []byte("x"))
		if filepath.Base(path) != tc.want {
			t.Errorf("name %q stored as %q, want %q", tc.name, filepath.Base(path), tc.want)
		}
		if rel, err := filepath.Rel(filepath.Join(dir, "uploads"), path); err != nil || strings.HasPrefix(rel, "..") || strings.Count(rel, "/") != 2 {
			t.Errorf("name %q escaped the uploads folder: %s", tc.name, path)
		}
	}
}

func TestUploadLimitsAndErrors(t *testing.T) {
	c, dir := startServer(t)
	var perr *proto.Error
	isErr := func(err error, code, text string) bool {
		return errors.As(err, &perr) && perr.Code == code && strings.Contains(perr.Message, text)
	}

	// The exact size announced is accepted, one byte over is refused and cleaned up.
	res, err := uploadCall(t, c, proto.FSUploadParams{Name: "a.png", Size: 4, Data: []byte("ab")})
	if err != nil || res.Upload == "" || res.Path != "" {
		t.Fatalf("first chunk: %+v %v", res, err)
	}
	if _, err := uploadCall(t, c, proto.FSUploadParams{Upload: res.Upload, Data: []byte("cde"), Final: true}); !isErr(err, proto.ErrBadRequest, "larger than the 4 bytes") {
		t.Fatalf("over size: %v", err)
	}
	if files := uploadFiles(t, dir); len(files) != 0 {
		t.Fatalf("over size left %v", files)
	}
	// Ended short.
	res, _ = uploadCall(t, c, proto.FSUploadParams{Name: "b.png", Size: 4, Data: []byte("ab")})
	if _, err := uploadCall(t, c, proto.FSUploadParams{Upload: res.Upload, Data: []byte("c"), Final: true}); !isErr(err, proto.ErrBadRequest, "after 3 of 4 bytes") {
		t.Fatalf("short: %v", err)
	}
	// Too big for the server whatever the client allows.
	if _, err := uploadCall(t, c, proto.FSUploadParams{Name: "huge.mov", Size: 1 << 40}); !isErr(err, proto.ErrBadRequest, "huge.mov is 1048576 MB; the limit is 256 MB") {
		t.Fatalf("huge: %v", err)
	}
	if _, err := uploadCall(t, c, proto.FSUploadParams{Name: "neg", Size: -1}); !isErr(err, proto.ErrBadRequest, "limit") {
		t.Fatalf("negative: %v", err)
	}
	// Unknown IDs, and chunks after the final one.
	if _, err := uploadCall(t, c, proto.FSUploadParams{Upload: "nope", Data: []byte("x")}); !isErr(err, proto.ErrNotFound, `no upload "nope"`) {
		t.Fatalf("unknown: %v", err)
	}
	res, _ = uploadCall(t, c, proto.FSUploadParams{Name: "c.png", Size: 1, Data: []byte("x"), Final: true})
	if res.Path == "" {
		t.Fatal("one-chunk upload has no path")
	}
	if _, err := uploadCall(t, c, proto.FSUploadParams{Upload: res.Upload, Data: []byte("y"), Final: true}); !isErr(err, proto.ErrNotFound, "no upload") {
		t.Fatalf("after final: %v", err)
	}
	if got, _ := os.ReadFile(res.Path); string(got) != "x" {
		t.Fatalf("a chunk after final changed the file: %q", got)
	}
	// Bad params.
	if err := c.Call(context.Background(), proto.MethodFSUpload, "not an object", nil); !isErr(err, proto.ErrBadRequest, "invalid params") {
		t.Fatalf("bad params: %v", err)
	}
}

// Another connection can't continue an upload, and closing the one that
// started it deletes the partial file.
func TestUploadBelongsToConnection(t *testing.T) {
	c, dir := startServer(t)
	res, err := uploadCall(t, c, proto.FSUploadParams{Name: "half.png", Size: 10, Data: []byte("12345")})
	if err != nil {
		t.Fatal(err)
	}
	other, err := client.Dial(filepath.Join(dir, "s.sock"), "other")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err := uploadCall(t, other, proto.FSUploadParams{Upload: res.Upload, Data: []byte("67890"), Final: true}); err == nil {
		t.Fatal("another connection continued the upload")
	}
	if files := uploadFiles(t, dir); len(files) != 1 || !strings.HasSuffix(files[0], "half.png.part") {
		t.Fatalf("mid-upload: %v", files)
	}
	c.Close()
	deadline := time.Now().Add(5 * time.Second)
	for len(uploadFiles(t, dir)) > 0 {
		if time.Now().After(deadline) {
			t.Fatalf("closed connection left %v", uploadFiles(t, dir))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Uploads run on the connection that streams pane output.
func TestUploadWhileFramesStream(t *testing.T) {
	c, _ := startServer(t)
	var info proto.PaneInfo
	// The loop gives up on its own: a test binary that is killed — by a
	// timeout, or a run that hangs — takes its cleanup with it, and a
	// hundred wake-ups a second left behind for good is a heavy thing to
	// leave on somebody's machine.
	tick := "n=0; while [ $n -lt 6000 ]; do echo tick; sleep 0.01; n=$((n+1)); done"
	if err := c.Call(context.Background(), proto.MethodPaneCreate, proto.PaneCreateParams{
		Command: []string{"/bin/sh", "-c", tick}, Cwd: os.TempDir(), NoProject: true, Cols: 40, Rows: 10,
	}, &info); err != nil {
		t.Fatal(err)
	}
	// And it is closed here rather than left to the server's shutdown.
	t.Cleanup(func() {
		_ = c.Call(context.Background(), proto.MethodPaneClose, proto.PaneRef{ID: info.ID}, nil)
	})
	if err := c.Call(context.Background(), proto.MethodPaneSubscribe, proto.PaneRef{ID: info.ID}, nil); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var frames int
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case msg, ok := <-c.Events:
				if !ok {
					return
				}
				if msg.Event == proto.EventPaneFrame {
					frames++
				}
			case <-stop:
				return
			}
		}
	}()
	data := bytes.Repeat([]byte("z"), 3*proto.UploadChunkSize)
	path := runUpload(t, c, "stream.bin", data)
	close(stop)
	wg.Wait()
	if got, _ := os.ReadFile(path); !bytes.Equal(got, data) {
		t.Fatal("wrong bytes")
	}
	if frames == 0 {
		t.Fatal("no frames arrived during the upload")
	}
}

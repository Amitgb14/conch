package client

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/transfer"
)

// Upload sends a file to the server's uploads folder one chunk per Step,
// so a caller can show progress between chunks.
type Upload struct {
	c    *Client
	name string
	src  io.ReaderAt
	size int64
	id   string
	sent int64
	path string

	// Where it goes (fs.transfer.v1); see proto.FSUploadParams.
	dir     string
	archive bool
	replace bool
	mode    uint32
	// done releases what OpenUpload opened.
	done func()

	// A server that takes chunks in any order (fs.transfer.v1) gets
	// several at once: next is where the next one to send starts, and
	// sent counts only those answered, from the start.
	parallel bool
	next     int64
	pipe     *pipe[int64]
}

// NewUpload prepares an upload of data under name.
func NewUpload(c *Client, name string, data []byte) *Upload {
	return &Upload{c: c, name: name, src: bytes.NewReader(data), size: int64(len(data))}
}

// OpenUpload prepares an upload of the file or folder at path, read as it
// is sent rather than all at once. A folder is packed into a temporary
// file first. Close releases both.
func OpenUpload(c *Client, path string) (*Upload, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	name := filepath.Base(filepath.Clean(path))
	switch {
	case st.IsDir():
		tmp, err := os.CreateTemp("", "conch-upload-*.tar.gz")
		if err != nil {
			return nil, err
		}
		release := func() { tmp.Close(); os.Remove(tmp.Name()) }
		if err := transfer.Pack(tmp, path); err != nil {
			release()
			return nil, fmt.Errorf("pack %s: %w", path, err)
		}
		packed, err := tmp.Stat()
		if err != nil {
			release()
			return nil, err
		}
		return &Upload{c: c, name: name, src: tmp, size: packed.Size(), archive: true, done: release}, nil
	case !st.Mode().IsRegular():
		return nil, fmt.Errorf("%s is not a file or folder", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return &Upload{c: c, name: name, src: f, size: st.Size(), mode: uint32(st.Mode().Perm()), done: func() { f.Close() }}, nil
}

// Into sends the upload to the folder dir there (absolute, or from ~)
// rather than the uploads folder; with replace, a file or folder already
// there by that name is written over. It needs fs.transfer.v1.
func (u *Upload) Into(dir string, replace bool) *Upload {
	u.dir, u.replace = dir, replace
	return u
}

// Close releases the file OpenUpload opened, and a folder's pack, and
// gives up on chunks still on their way.
func (u *Upload) Close() {
	if u.pipe != nil {
		u.pipe.stop()
	}
	if u.done != nil {
		u.done()
		u.done = nil
	}
}

// Step moves the upload on — the first chunk, the next one answered, or
// the last — and reports whether it is complete.
func (u *Upload) Step(ctx context.Context) (bool, error) {
	if u.path != "" {
		return true, nil
	}
	if u.id == "" {
		// The first chunk names the upload, so it goes alone.
		u.parallel = u.size > proto.UploadChunkSize && len(u.c.MissingCapabilities([]string{proto.CapFSTransfer})) == 0
		return u.send(ctx, 0)
	}
	if !u.parallel {
		return u.send(ctx, u.sent)
	}
	if u.pipe == nil {
		u.pipe = newPipe[int64]()
	}
	// The last chunk ends the upload, so every other one must be in
	// before it is sent.
	last := (u.size - 1) / proto.UploadChunkSize * proto.UploadChunkSize
	for u.pipe.busy() < chunksInFlight && u.next < last {
		off, id := u.next, u.id
		data, err := u.read(off)
		if err != nil {
			u.pipe.stop()
			return false, err
		}
		u.next += int64(len(data))
		u.pipe.send(func(ctx context.Context) (int64, error) {
			p := proto.FSUploadParams{Upload: id, Data: data, Offset: off}
			return int64(len(data)), u.c.Call(ctx, proto.MethodFSUpload, p, nil)
		})
	}
	if u.pipe.busy() > 0 {
		n, err := u.pipe.next(ctx)
		if err != nil {
			u.pipe.stop()
			return false, err
		}
		u.sent += n
		return false, nil
	}
	return u.send(ctx, last)
}

// read reads the chunk that starts at off.
func (u *Upload) read(off int64) ([]byte, error) {
	data := make([]byte, min(proto.UploadChunkSize, u.size-off))
	if n, err := u.src.ReadAt(data, off); n < len(data) {
		if err == nil || err == io.EOF {
			err = fmt.Errorf("%s got shorter while it was sent", u.name)
		}
		return nil, err
	}
	return data, nil
}

// send sends the chunk at off and waits for its answer: the first, the
// last, or each in turn for a server that wants them in order.
func (u *Upload) send(ctx context.Context, off int64) (bool, error) {
	data, err := u.read(off)
	if err != nil {
		return false, err
	}
	end := off + int64(len(data))
	p := proto.FSUploadParams{Upload: u.id, Data: data, Final: end == u.size}
	if u.parallel {
		p.Offset = off
	}
	if u.id == "" {
		p.Name, p.Size = u.name, u.size
		p.Dir, p.Archive, p.Replace, p.Mode = u.dir, u.archive, u.replace, u.mode
		p.Parallel = u.parallel
	}
	var res proto.FSUploadResult
	if err := u.c.Call(ctx, proto.MethodFSUpload, p, &res); err != nil {
		return false, err
	}
	u.id, u.sent, u.next = res.Upload, end, end
	if p.Final {
		u.path = res.Path
	}
	return p.Final, nil
}

// Timeout is how long the next step may take: the last chunk of a folder
// has the server unpack it.
func (u *Upload) Timeout() time.Duration {
	if u.archive && u.size-u.sent <= proto.UploadChunkSize {
		return 10 * time.Minute
	}
	return time.Minute
}

// Run sends every remaining chunk and returns the stored file's path.
func (u *Upload) Run(ctx context.Context) (string, error) {
	for {
		done, err := u.Step(ctx)
		if err != nil || done {
			return u.path, err
		}
	}
}

// Name is the file's name; Sent and Size are bytes.
func (u *Upload) Name() string { return u.name }
func (u *Upload) Sent() int    { return int(u.sent) }
func (u *Upload) Size() int    { return int(u.size) }

// Dir reports whether this is a folder.
func (u *Upload) Dir() bool { return u.archive }

// Path is the stored file's path on the server, once the upload is complete.
func (u *Upload) Path() string { return u.path }

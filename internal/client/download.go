package client

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/transfer"
)

// Download copies a file or folder from the server's machine into a folder
// here, one chunk per Step so a caller can show progress. It arrives under
// a temporary name and takes its own only once whole; a folder is unpacked
// then. Without replace a name already taken here is kept and the copy is
// numbered, as a browser does.
type Download struct {
	c       *Client
	remote  string
	dest    string
	replace bool
	ssh     string // a pane whose ssh host it comes from (fs.ssh.v1)

	started bool
	info    proto.FSDownload
	tmp     *os.File
	got     int64
	path    string

	// Chunks are read several at once and written here in order: next is
	// where the next one to ask for starts.
	next int64
	pipe *pipe[proto.FSDownloadChunk]
}

// NewDownload prepares a download of remote (absolute, or from ~ there)
// into the folder dest here. It needs fs.transfer.v1.
func NewDownload(c *Client, remote, dest string, replace bool) *Download {
	return &Download{c: c, remote: remote, dest: dest, replace: replace}
}

// On brings it from the host the pane ssh is logged in to rather than the
// machine itself; remote is then a path on that host. It needs fs.ssh.v1.
func (d *Download) On(ssh string) *Download {
	d.ssh = ssh
	return d
}

// Timeout is how long the next step may take: a folder is packed before
// the first chunk can be read.
func (d *Download) Timeout() time.Duration {
	if !d.started {
		return 10 * time.Minute
	}
	return time.Minute
}

// Step fetches the next chunk and reports whether the download is whole
// and in place.
func (d *Download) Step(ctx context.Context) (bool, error) {
	if d.path != "" {
		return true, nil
	}
	if !d.started {
		if st, err := os.Stat(d.dest); err != nil {
			return false, err
		} else if !st.IsDir() {
			return false, fmt.Errorf("%s is not a folder", d.dest)
		}
		if d.ssh != "" && len(d.c.MissingCapabilities([]string{proto.CapFSSSH})) > 0 {
			return false, errNoSSHFiles
		}
		if err := d.c.Call(ctx, proto.MethodFSDownload, proto.FSDownloadParams{Path: d.remote, SSH: d.ssh}, &d.info); err != nil {
			return false, err
		}
		d.started = true
		d.info.Name = transfer.SafeName(d.info.Name) // the other machine's word for it
		tmp, err := os.CreateTemp(d.dest, "."+d.info.Name+".part-*")
		if err != nil {
			return false, err
		}
		d.tmp = tmp
		return false, nil
	}
	// Reading the last chunk lets the server forget the download, so it
	// is asked for alone, once every other one is in.
	last := max(d.info.Size-1, 0) / proto.UploadChunkSize * proto.UploadChunkSize
	if d.pipe == nil {
		d.pipe = newPipe[proto.FSDownloadChunk]()
	}
	for d.pipe.busy() < chunksInFlight && d.next < last {
		p := proto.FSDownloadReadParams{ID: d.info.ID, Offset: d.next}
		d.next += proto.UploadChunkSize
		d.pipe.send(func(ctx context.Context) (proto.FSDownloadChunk, error) {
			var chunk proto.FSDownloadChunk
			return chunk, d.c.Call(ctx, proto.MethodFSDownloadRead, p, &chunk)
		})
	}
	var (
		chunk proto.FSDownloadChunk
		err   error
	)
	if d.pipe.busy() > 0 {
		chunk, err = d.pipe.next(ctx)
	} else {
		err = d.c.Call(ctx, proto.MethodFSDownloadRead, proto.FSDownloadReadParams{ID: d.info.ID, Offset: last}, &chunk)
	}
	if err != nil {
		d.Close()
		return false, err
	}
	// Each chunk is whole, and only the last says it is the end.
	want := min(proto.UploadChunkSize, d.info.Size-d.got)
	if int64(len(chunk.Data)) != want || chunk.EOF != (d.got+want == d.info.Size) {
		d.Close()
		return false, errors.New("the other machine sent more or less than it said it would")
	}
	if _, err := d.tmp.Write(chunk.Data); err != nil {
		d.Close()
		return false, err
	}
	d.got += want
	if !chunk.EOF {
		return false, nil
	}
	path, err := d.finish()
	d.Close()
	if err != nil {
		return false, err
	}
	d.path = path
	return true, nil
}

// finish puts the whole download where it belongs.
func (d *Download) finish() (string, error) {
	if err := d.tmp.Close(); err != nil {
		return "", err
	}
	name := d.info.Name
	if !d.replace {
		name = transfer.FreeName(d.dest, name)
	}
	target := filepath.Join(d.dest, name)
	st, err := os.Lstat(target)
	exists := err == nil
	if !d.info.Dir {
		if exists && st.IsDir() {
			return "", fmt.Errorf("%s is a folder", target)
		}
		// CreateTemp made it private; a copied file is an ordinary one.
		if err := os.Chmod(d.tmp.Name(), 0o644); err != nil {
			return "", err
		}
		return target, os.Rename(d.tmp.Name(), target)
	}
	switch {
	case exists && !st.IsDir():
		return "", fmt.Errorf("%s is in the way and is not a folder", target)
	case !exists:
		if err := os.Mkdir(target, 0o755); err != nil {
			return "", err
		}
	}
	f, err := os.Open(d.tmp.Name())
	if err != nil {
		return "", err
	}
	defer f.Close()
	if err := transfer.Unpack(f, target, d.replace); err != nil {
		if !exists {
			os.RemoveAll(target) // half a folder is worse than none
		}
		return "", err
	}
	return target, nil
}

// Close drops what an unfinished download wrote and gives up on chunks
// still on their way. The server forgets its side once nobody reads it.
func (d *Download) Close() {
	if d.pipe != nil {
		d.pipe.stop()
	}
	if d.tmp != nil {
		d.tmp.Close()
		os.Remove(d.tmp.Name())
		d.tmp = nil
	}
}

// Run fetches every remaining chunk and returns where the copy is here.
func (d *Download) Run(ctx context.Context) (string, error) {
	for {
		done, err := d.Step(ctx)
		if err != nil || done {
			return d.path, err
		}
	}
}

// Name is what it is called (known once started); Got and Size are bytes
// (a folder's are its pack's).
func (d *Download) Name() string { return d.info.Name }
func (d *Download) Got() int64   { return d.got }
func (d *Download) Size() int64  { return d.info.Size }

// Dir reports whether a folder is coming, once started.
func (d *Download) Dir() bool { return d.info.Dir }

// Path is where the copy is here, once whole.
func (d *Download) Path() string { return d.path }

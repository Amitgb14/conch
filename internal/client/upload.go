package client

import (
	"context"

	"github.com/Amitgb14/conch/internal/proto"
)

// Upload sends a file to the server's uploads folder one chunk per Step,
// so a caller can show progress between chunks.
type Upload struct {
	c    *Client
	name string
	data []byte
	id   string
	sent int
	path string
}

// NewUpload prepares an upload of data under name.
func NewUpload(c *Client, name string, data []byte) *Upload {
	return &Upload{c: c, name: name, data: data}
}

// Step sends the next chunk and reports whether the upload is complete.
func (u *Upload) Step(ctx context.Context) (bool, error) {
	if u.path != "" {
		return true, nil
	}
	end := min(u.sent+proto.UploadChunkSize, len(u.data))
	p := proto.FSUploadParams{Upload: u.id, Data: u.data[u.sent:end], Final: end == len(u.data)}
	if u.id == "" {
		p.Name, p.Size = u.name, int64(len(u.data))
	}
	var res proto.FSUploadResult
	if err := u.c.Call(ctx, proto.MethodFSUpload, p, &res); err != nil {
		return false, err
	}
	u.id, u.sent = res.Upload, end
	if p.Final {
		u.path = res.Path
	}
	return p.Final, nil
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
func (u *Upload) Sent() int    { return u.sent }
func (u *Upload) Size() int    { return len(u.data) }

// Path is the stored file's path on the server, once the upload is complete.
func (u *Upload) Path() string { return u.path }

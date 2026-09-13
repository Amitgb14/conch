package gitx

import (
	"bytes"
	"context"
)

// maxDiff caps the size of a diff returned by Diff.
const maxDiff = 1 << 20

const truncatedNote = "\n… diff truncated\n"

// Diff returns a unified diff of file in dir. With rev empty it shows the
// working tree and index against HEAD (untracked files diff against
// /dev/null); otherwise it is `git diff <rev> -- file`, e.g. "main...feat".
func Diff(ctx context.Context, dir, file, rev string) (string, error) {
	opts := []string{"diff", "--no-color", "--no-ext-diff"}
	var args []string
	ok := []int(nil)
	switch {
	case rev != "":
		args = append(opts, rev, "--", file)
	case isUntracked(ctx, dir, file):
		// --no-index exits 1 when the files differ.
		args = append(opts, "--no-index", "--", "/dev/null", file)
		ok = []int{1}
	default:
		args = append(opts, headOrEmptyTree(ctx, dir), "--", file)
	}
	var buf capBuffer
	if _, _, err := runTo(ctx, dir, &buf, ok, args...); err != nil {
		return "", err
	}
	if buf.truncated {
		return buf.String() + truncatedNote, nil
	}
	return buf.String(), nil
}

func isUntracked(ctx context.Context, dir, file string) bool {
	files, err := statusFiles(ctx, dir, file)
	return err == nil && len(files) == 1 && files[0].Code == "?"
}

// capBuffer keeps the first maxDiff bytes written and discards the rest,
// still reporting success so git is not killed by a broken pipe. It wraps
// rather than embeds bytes.Buffer so io.Copy cannot bypass Write via ReadFrom.
type capBuffer struct {
	buf       bytes.Buffer
	truncated bool
}

func (b *capBuffer) Bytes() []byte  { return b.buf.Bytes() }
func (b *capBuffer) String() string { return b.buf.String() }

func (b *capBuffer) Write(p []byte) (int, error) {
	if room := maxDiff - b.buf.Len(); len(p) > room {
		b.buf.Write(p[:max(room, 0)])
		b.truncated = true
		return len(p), nil
	}
	return b.buf.Write(p)
}

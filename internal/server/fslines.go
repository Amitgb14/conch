package server

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Amitgb14/conch/internal/proto"
)

// fs.lines and fs.write_range: a file too big to open whole — a log of a
// few gigabytes — read by its lines and edited a few at a time. The client
// holds the lines on screen and no more; the server keeps an index of
// where every lineMark'th line starts, so going to line ten million reads
// at most lineMark lines to get there. A log grows while it is looked at,
// so an index is extended rather than built again when the file has only
// been added to.

// lineMark is how many lines apart the index marks the file.
const lineMark = 1024

// lineIndexMax is how many files' indexes are kept.
const lineIndexMax = 8

// lineSigLen is how much of the end of an indexed file is kept to tell a
// file that was added to from one that was rewritten.
const lineSigLen = 64

type lineIndex struct {
	size     int64
	mod      time.Time
	sig      []byte  // the last lineSigLen bytes indexed
	marks    []int64 // marks[k] is where line k*lineMark+1 starts
	newlines int     // how many newlines there are
	last     byte    // the last byte of the file
	used     time.Time
}

// lines is how many lines the file has: a last line without a newline
// counts.
func (ix *lineIndex) lines() int {
	if ix.size > 0 && ix.last != '\n' {
		return ix.newlines + 1
	}
	return ix.newlines
}

type lineIndexes struct {
	mu sync.Mutex
	m  map[string]*lineIndex
}

func (li *lineIndexes) drop(path string) {
	li.mu.Lock()
	for p := range li.m {
		if p == path || within(p, path) {
			delete(li.m, p)
		}
	}
	li.mu.Unlock()
}

// get is f's index: kept, extended, or read afresh.
func (li *lineIndexes) get(path string, f *os.File, info os.FileInfo) (*lineIndex, error) {
	li.mu.Lock()
	old := li.m[path]
	li.mu.Unlock()
	if old != nil && old.size == info.Size() && old.mod.Equal(info.ModTime()) {
		li.mu.Lock()
		old.used = time.Now()
		li.mu.Unlock()
		return old, nil
	}
	ix := &lineIndex{marks: []int64{0}}
	if old != nil && info.Size() > old.size && sameTail(f, old) {
		ix.marks = append([]int64(nil), old.marks...)
		ix.newlines, ix.size, ix.last, ix.sig = old.newlines, old.size, old.last, old.sig
	}
	if err := ix.scan(f, info.Size()); err != nil {
		return nil, err
	}
	ix.mod, ix.used = info.ModTime(), time.Now()
	li.mu.Lock()
	if li.m == nil {
		li.m = map[string]*lineIndex{}
	}
	li.m[path] = ix
	for len(li.m) > lineIndexMax {
		var oldest string
		for p, x := range li.m {
			if oldest == "" || x.used.Before(li.m[oldest].used) {
				oldest = p
			}
		}
		delete(li.m, oldest)
	}
	li.mu.Unlock()
	return ix, nil
}

// sameTail says whether the bytes an index ended on are still where they
// were: the file was added to, not rewritten.
func sameTail(f *os.File, old *lineIndex) bool {
	if len(old.sig) == 0 {
		return old.size == 0
	}
	buf := make([]byte, len(old.sig))
	if _, err := f.ReadAt(buf, old.size-int64(len(buf))); err != nil {
		return false
	}
	return bytes.Equal(buf, old.sig)
}

// scan indexes the file from where the index ends to size.
func (ix *lineIndex) scan(f *os.File, size int64) error {
	buf := make([]byte, 1<<20)
	off := ix.size
	for off < size {
		n, err := f.ReadAt(buf[:min(int64(len(buf)), size-off)], off)
		chunk := buf[:n]
		for i := 0; ; {
			j := bytes.IndexByte(chunk[i:], '\n')
			if j < 0 {
				break
			}
			i += j + 1
			ix.newlines++
			if ix.newlines%lineMark == 0 {
				ix.marks = append(ix.marks, off+int64(i))
			}
		}
		if n > 0 {
			ix.last = chunk[n-1]
			off += int64(n)
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		if n == 0 {
			break // shorter than it said: indexed as far as it goes
		}
	}
	ix.size = off
	sig := make([]byte, min(int64(lineSigLen), off))
	if _, err := f.ReadAt(sig, off-int64(len(sig))); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	ix.sig = sig
	return nil
}

// start is where line n (from 1) begins.
func (ix *lineIndex) start(f *os.File, n int) (int64, error) {
	k := (n - 1) / lineMark
	off := ix.marks[min(k, len(ix.marks)-1)]
	skip := n - 1 - k*lineMark
	if skip == 0 {
		return off, nil
	}
	r := bufio.NewReaderSize(io.NewSectionReader(f, off, ix.size-off), 64<<10)
	for skip > 0 {
		chunk, err := r.ReadSlice('\n')
		off += int64(len(chunk))
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil {
			return off, nil
		}
		skip--
	}
	return off, nil
}

func (s *Server) readLines(lp proto.FSLinesParams) (proto.FSLinesResult, *proto.Error) {
	root, perr := s.checkoutRoot(lp.Root)
	if perr != nil {
		return proto.FSLinesResult{}, perr
	}
	if lp.Path == "" || lp.Path == "." {
		return proto.FSLinesResult{}, proto.Errorf(proto.ErrBadRequest, "fs.lines needs a file")
	}
	path, perr := confine(root, lp.Path)
	if perr != nil {
		return proto.FSLinesResult{}, perr
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	f, info, perr := openRegular(path, lp.Path)
	if perr != nil {
		return proto.FSLinesResult{}, perr
	}
	defer f.Close()
	ix, err := s.lineIdx.get(path, f, info)
	if err != nil {
		return proto.FSLinesResult{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	count := lp.Count
	if count <= 0 {
		count = proto.FSLinesDefault
	}
	count = min(count, proto.FSLinesMax)
	total := ix.lines()
	first := max(lp.Line, 1)
	if lp.Tail {
		first = max(total-count+1, 1)
	}
	res := proto.FSLinesResult{Path: path, Size: ix.size, ModTime: info.ModTime(), Line: first, Lines: total}
	if first > total {
		res.Offset = ix.size
		res.Hash = contentHash(nil)
		return res, nil
	}
	off, err := ix.start(f, first)
	if err != nil {
		return proto.FSLinesResult{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	r := bufio.NewReaderSize(io.NewSectionReader(f, off, ix.size-off), 64<<10)
	var data []byte
	for got := 0; got < count && len(data) < proto.FSLinesBytesMax; {
		chunk, err := r.ReadSlice('\n')
		room := proto.FSLinesBytesMax - len(data)
		if len(chunk) > room {
			// One line longer than what one answer holds: cut on a
			// whole character, the rest for the next.
			cut := room
			for cut > 0 && !utf8.RuneStart(chunk[cut]) {
				cut--
			}
			data = append(data, chunk[:cut]...)
			break
		}
		data = append(data, chunk...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		got++
		if err != nil {
			break
		}
	}
	res.Offset, res.Length = off, int64(len(data))
	switch {
	case bytes.IndexByte(data, 0) >= 0:
		res.Binary = true
	case !utf8.Valid(data):
		res.Data, res.Lossy = strings.ToValidUTF8(string(data), "�"), true
	default:
		res.Data, res.Hash = string(data), contentHash(data)
	}
	return res, nil
}

func (s *Server) writeRange(c *client, wp proto.FSWriteRangeParams) (proto.FSWriteResult, *proto.Error) {
	root, perr := s.writableCheckout(c, wp.Root, proto.MethodFSWriteRange)
	if perr != nil {
		return proto.FSWriteResult{}, perr
	}
	switch {
	case wp.Path == "" || wp.Path == ".":
		return proto.FSWriteResult{}, proto.Errorf(proto.ErrBadRequest, "fs.write_range needs a file")
	case len(wp.Data) > proto.FSWriteMax:
		return proto.FSWriteResult{}, proto.Errorf(proto.ErrBadRequest, "%d bytes is more than fs.write_range takes (%d)", len(wp.Data), proto.FSWriteMax)
	case wp.Offset < 0 || wp.Length < 0:
		return proto.FSWriteResult{}, proto.Errorf(proto.ErrBadRequest, "a range can't be negative")
	case wp.Base == "":
		return proto.FSWriteResult{}, proto.Errorf(proto.ErrBadRequest, "fs.write_range needs the fingerprint of what it replaces")
	}
	path, perr := confine(root, wp.Path)
	if perr != nil {
		return proto.FSWriteResult{}, perr
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	f, info, perr := openRegular(path, wp.Path)
	if perr != nil {
		return proto.FSWriteResult{}, perr
	}
	defer f.Close()
	if wp.Offset+wp.Length > info.Size() {
		return proto.FSWriteResult{}, proto.Errorf(proto.ErrFileChanged, "%s is shorter than it was when read", wp.Path)
	}
	old := make([]byte, wp.Length)
	if _, err := f.ReadAt(old, wp.Offset); err != nil && !errors.Is(err, io.EOF) {
		return proto.FSWriteResult{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	if contentHash(old) != wp.Base {
		return proto.FSWriteResult{}, proto.Errorf(proto.ErrFileChanged, "those lines of %s changed on disk since they were read", wp.Path)
	}
	size := info.Size()
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".conch-*")
	if err != nil {
		return proto.FSWriteResult{}, proto.Errorf(proto.ErrInternal, "%v", err)
	}
	name := tmp.Name()
	defer os.Remove(name)
	err = func() error {
		defer tmp.Close()
		if _, err := io.Copy(tmp, io.NewSectionReader(f, 0, wp.Offset)); err != nil {
			return err
		}
		if _, err := tmp.WriteString(wp.Data); err != nil {
			return err
		}
		end := wp.Offset + wp.Length
		if _, err := io.Copy(tmp, io.NewSectionReader(f, end, size-end)); err != nil {
			return err
		}
		if err := tmp.Chmod(info.Mode().Perm()); err != nil {
			return err
		}
		return tmp.Sync()
	}()
	if err != nil {
		return proto.FSWriteResult{}, proto.Errorf(proto.ErrInternal, "save %s: %v", wp.Path, err)
	}
	// Written to while it was copied — a log going on — and what was added
	// would be lost: refused rather than dropped.
	if now, err := os.Stat(path); err != nil || now.Size() != size || !now.ModTime().Equal(info.ModTime()) {
		return proto.FSWriteResult{}, proto.Errorf(proto.ErrFileChanged, "%s changed while it was being saved", wp.Path)
	}
	if err := os.Rename(name, path); err != nil {
		return proto.FSWriteResult{}, proto.Errorf(proto.ErrInternal, "save %s: %v", wp.Path, err)
	}
	s.lineIdx.drop(path)
	res := proto.FSWriteResult{Path: path, Size: size - wp.Length + int64(len(wp.Data)), Hash: contentHash([]byte(wp.Data))}
	if info, err := os.Stat(path); err == nil {
		res.ModTime, res.Size = info.ModTime(), info.Size()
	}
	log.Printf("fs.write_range: %s (%d bytes at %d)", path, len(wp.Data), wp.Offset)
	return res, nil
}

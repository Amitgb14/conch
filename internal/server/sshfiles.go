package server

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pkg/sftp"

	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/remote"
	"github.com/Amitgb14/conch/internal/transfer"
)

// The files of a host a terminal is logged in to with ssh (fs.ssh.v1). The
// server sees the terminal's foreground program; when that is ssh it reads
// back the host and options, and the Files view reaches the host through
// an sftp session of the server's own made with them. Every fs method then
// works on that host as it does here — the same listing, preview, packing
// and copying, on a transfer.FS that is sftp rather than the disk.

const sshFilesIdle = 5 * time.Minute // a session nobody used for this long is closed

// sshFilesConnect bounds logging in, a jump host included; tests shorten it.
var sshFilesConnect = 30 * time.Second

// sshFiles keeps one sftp session per pane, while it is used.
type sshFiles struct {
	mu   sync.Mutex
	open map[string]*sshSession // by pane
}

type sshSession struct {
	key   string        // the login it was made for
	ready chan struct{} // closed once connected, or failed
	err   *proto.Error  // why it failed, once ready
	fs    *sftpFS
	cmd   *exec.Cmd
	dead  atomic.Bool // ssh exited
}

func newSSHFiles() *sshFiles { return &sshFiles{open: map[string]*sshSession{}} }

// filesOn is the files a request reaches: this machine's, or with ssh set
// the host that pane is logged in to.
func (s *Server) filesOn(ssh string) (transfer.FS, *proto.Error) {
	if ssh == "" {
		return transfer.Local, nil
	}
	e, perr := s.get(ssh)
	if perr != nil {
		return nil, perr
	}
	e.mu.Lock()
	login, ok := e.login, e.loginOK
	e.mu.Unlock()
	if !ok {
		return nil, proto.Errorf(proto.ErrBadRequest, "pane %s is not logged in anywhere with ssh now", ssh)
	}
	return s.sshFiles.session(ssh, login)
}

func loginKey(l remote.Login) string {
	return l.Host + "\x00" + strings.Join(l.Opts, "\x00")
}

// session is the pane's sftp session to the host, made the first time and
// again whenever the login changed or ssh went away.
func (sf *sshFiles) session(pane string, login remote.Login) (transfer.FS, *proto.Error) {
	key := loginKey(login)
	sf.mu.Lock()
	ss := sf.open[pane]
	if ss != nil {
		select {
		case <-ss.ready:
			if ss.key != key || ss.err != nil || ss.dead.Load() {
				ss.close()
				ss = nil
			}
		default:
			if ss.key != key {
				ss = nil // still connecting, to a host this pane has left
			}
		}
	}
	if ss == nil {
		ss = &sshSession{key: key, ready: make(chan struct{})}
		sf.open[pane] = ss
		go ss.connect(login)
	}
	sf.mu.Unlock()
	<-ss.ready
	if ss.err != nil {
		return nil, ss.err
	}
	ss.fs.touch()
	return ss.fs, nil
}

// connect logs in and starts sftp. It never asks for anything: a host that
// wants a password fails with ssh's own reason, said plainly.
func (ss *sshSession) connect(login remote.Login) {
	defer close(ss.ready)
	argv := remote.FilesCommand(login)
	cmd := exec.Command(argv[0], argv[1:]...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		ss.err = proto.Errorf(proto.ErrInternal, "%v", err)
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		ss.err = proto.Errorf(proto.ErrInternal, "%v", err)
		return
	}
	var stderr tailBuffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		ss.err = proto.Errorf(proto.ErrBadRequest, "ssh: %v", err)
		return
	}
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		ss.dead.Store(true)
		close(exited)
	}()
	type made struct {
		c   *sftp.Client
		err error
	}
	done := make(chan made, 1)
	go func() {
		// Each chunk of a copy is one write of half a megabyte, which sftp
		// sends 32 KB at a time; one at a time, waiting for each answer,
		// was a tenth of scp's speed over the same link. A write that fails
		// part way may leave a hole, but a failed copy's part file is
		// removed whole.
		c, err := sftp.NewClientPipe(stdout, stdin, sftp.UseConcurrentWrites(true), sftp.UseConcurrentReads(true))
		done <- made{c, err}
	}()
	var m made
	select {
	case m = <-done:
	case <-time.After(sshFilesConnect):
		m.err = errors.New("timed out")
	}
	if m.err != nil {
		_ = cmd.Process.Kill()
		select {
		case <-exited: // so stderr is all there
		case <-time.After(2 * time.Second):
		}
		ss.err = proto.Errorf(proto.ErrBadRequest, "%s", sshFilesError(login.Host, stderr.String(), m.err))
		return
	}
	home, err := m.c.Getwd()
	if err != nil {
		m.c.Close()
		_ = cmd.Process.Kill()
		ss.err = proto.Errorf(proto.ErrBadRequest, "%s: %v", login.Host, err)
		return
	}
	ss.cmd, ss.fs = cmd, &sftpFS{c: m.c, home: home}
	ss.fs.touch()
	log.Printf("ssh files: %s (home %s)", login.Host, home)
}

// sshFilesError says why a session could not be made, in the words that
// help: what ssh said last, and what to do about a password.
func sshFilesError(host, stderr string, err error) string {
	last := ""
	for _, l := range strings.Split(strings.TrimSpace(stderr), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			last = l
		}
	}
	switch {
	case strings.Contains(last, "Permission denied"):
		return fmt.Sprintf("%s wants a password or a key conch can't use (%s). Files over ssh logs in on its own, without asking: an ssh key or agent, or ControlMaster in ~/.ssh/config to share the terminal's connection, makes it work", host, last)
	case strings.Contains(last, "subsystem request failed"):
		return host + " has no sftp server, which Files over ssh needs"
	case last != "":
		return host + ": " + last
	}
	return fmt.Sprintf("%s: %v", host, err)
}

func (ss *sshSession) close() {
	if ss.fs != nil {
		ss.fs.c.Close()
	}
	if ss.cmd != nil && ss.cmd.Process != nil {
		_ = ss.cmd.Process.Kill()
	}
}

// drop closes a pane's session: the pane has gone.
func (sf *sshFiles) drop(pane string) {
	sf.mu.Lock()
	ss := sf.open[pane]
	delete(sf.open, pane)
	sf.mu.Unlock()
	if ss != nil {
		go func() { <-ss.ready; ss.close() }()
	}
}

// sweep closes sessions nobody has used for a while.
func (sf *sshFiles) sweep(now time.Time) {
	sf.mu.Lock()
	defer sf.mu.Unlock()
	for pane, ss := range sf.open {
		select {
		case <-ss.ready:
		default:
			continue
		}
		if ss.err != nil || ss.dead.Load() || now.Sub(ss.fs.used()) >= sshFilesIdle {
			ss.close()
			delete(sf.open, pane)
		}
	}
}

func (sf *sshFiles) run(quit <-chan struct{}) {
	t := time.NewTicker(sshFilesIdle / 5)
	defer t.Stop()
	for {
		select {
		case now := <-t.C:
			sf.sweep(now)
		case <-quit:
			sf.mu.Lock()
			for _, ss := range sf.open {
				select {
				case <-ss.ready:
					ss.close()
				default:
				}
			}
			sf.mu.Unlock()
			return
		}
	}
}

// tailBuffer keeps the end of what ssh says on stderr.
type tailBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b.Write(p)
	if t.b.Len() > 8<<10 {
		t.b.Next(t.b.Len() - 4<<10)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.b.String()
}

// sftpFS is a host's files through sftp.
type sftpFS struct {
	c    *sftp.Client
	home string
	last atomic.Int64 // when it was last used, for sweep
}

func (f *sftpFS) touch()          { f.last.Store(time.Now().UnixNano()) }
func (f *sftpFS) used() time.Time { return time.Unix(0, f.last.Load()) }

func (f *sftpFS) Stat(name string) (fs.FileInfo, error)  { f.touch(); return f.c.Stat(name) }
func (f *sftpFS) Lstat(name string) (fs.FileInfo, error) { f.touch(); return f.c.Lstat(name) }

func (f *sftpFS) ReadDir(name string) ([]fs.FileInfo, error) {
	f.touch()
	return f.c.ReadDir(name)
}

func (f *sftpFS) Open(name string) (transfer.File, error) {
	f.touch()
	file, err := f.c.Open(name)
	if err != nil {
		return nil, err
	}
	return &sftpFile{File: file, fs: f}, nil
}

func (f *sftpFS) Create(name string, perm fs.FileMode) (transfer.File, error) {
	f.touch()
	file, err := f.c.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return nil, err
	}
	if err := file.Chmod(perm); err != nil {
		file.Close()
		return nil, err
	}
	return &sftpFile{File: file, fs: f}, nil
}

func (f *sftpFS) Mkdir(name string, perm fs.FileMode) error {
	f.touch()
	if err := f.c.Mkdir(name); err != nil {
		return err
	}
	return f.c.Chmod(name, perm)
}

func (f *sftpFS) Chmod(name string, mode fs.FileMode) error { f.touch(); return f.c.Chmod(name, mode) }

// Rename replaces what is at newname, as rename(2) does. Plain sftp
// refuses to; OpenSSH's posix-rename does it in one step, and a server
// without it gets the old file removed first.
func (f *sftpFS) Rename(oldname, newname string) error {
	f.touch()
	if _, ok := f.c.HasExtension("posix-rename@openssh.com"); ok {
		return f.c.PosixRename(oldname, newname)
	}
	if st, err := f.c.Lstat(newname); err == nil && !st.IsDir() {
		if err := f.c.Remove(newname); err != nil {
			return err
		}
	}
	return f.c.Rename(oldname, newname)
}

func (f *sftpFS) Remove(name string) error             { f.touch(); return f.c.Remove(name) }
func (f *sftpFS) RemoveAll(name string) error          { f.touch(); return f.c.RemoveAll(name) }
func (f *sftpFS) Symlink(target, name string) error    { f.touch(); return f.c.Symlink(target, name) }
func (f *sftpFS) Readlink(name string) (string, error) { f.touch(); return f.c.ReadLink(name) }
func (f *sftpFS) Home() (string, error)                { return f.home, nil }

// sftpFile keeps its session from being swept while it is read or written.
type sftpFile struct {
	*sftp.File
	fs *sftpFS
}

func (s *sftpFile) Read(p []byte) (int, error) { s.fs.touch(); return s.File.Read(p) }
func (s *sftpFile) ReadAt(p []byte, off int64) (int, error) {
	s.fs.touch()
	return s.File.ReadAt(p, off)
}
func (s *sftpFile) Write(p []byte) (int, error) { s.fs.touch(); return s.File.Write(p) }
func (s *sftpFile) WriteAt(p []byte, off int64) (int, error) {
	s.fs.touch()
	return s.File.WriteAt(p, off)
}

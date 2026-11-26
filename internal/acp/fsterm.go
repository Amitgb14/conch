package acp

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unicode/utf8"
)

// Files and terminals are the client's half of the protocol: the agent
// asks conch to read a file, write one or run a command. conch serves
// them inside the pane's folder only — the folder the person started the
// agent in — so an agent cannot reach past it through conch, whatever it
// can do through its own process.

// workspace is the folder an agent's file and terminal requests are kept to.
type workspace struct {
	root string // resolved, absolute

	mu    sync.Mutex
	terms map[string]*terminal
	next  int
}

func newWorkspace(dir string) (*workspace, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		abs = r
	}
	return &workspace{root: abs, terms: map[string]*terminal{}}, nil
}

// resolve is path inside the workspace, links followed, or an error
// saying it is not. A file that does not exist yet is judged by the
// nearest folder above it that does, so a link there cannot lead out.
func (w *workspace) resolve(path string) (string, error) {
	if path == "" || !filepath.IsAbs(path) {
		return "", fmt.Errorf("%q is not an absolute path", path)
	}
	p := filepath.Clean(path)
	rest := ""
	cur := p
	for {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			cur = r
			break
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
	full := filepath.Join(cur, rest)
	if full != w.root && !strings.HasPrefix(full, w.root+string(filepath.Separator)) {
		return "", fmt.Errorf("%s is outside %s, the folder this agent was started in", path, w.root)
	}
	return full, nil
}

func (w *workspace) readFile(p ReadFileParams) (ReadFileResult, *Error) {
	full, err := w.resolve(p.Path)
	if err != nil {
		return ReadFileResult{}, &Error{Code: codeInvalidParams, Message: err.Error()}
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return ReadFileResult{}, &Error{Code: codeInternal, Message: err.Error()}
	}
	text := string(b)
	if p.Line > 0 || p.Limit > 0 {
		lines := strings.SplitAfter(text, "\n")
		start := 0
		if p.Line > 0 {
			start = min(p.Line-1, len(lines))
		}
		end := len(lines)
		if p.Limit > 0 {
			end = min(start+p.Limit, end)
		}
		text = strings.Join(lines[start:end], "")
	}
	return ReadFileResult{Content: text}, nil
}

func (w *workspace) writeFile(p WriteFileParams) *Error {
	full, err := w.resolve(p.Path)
	if err != nil {
		return &Error{Code: codeInvalidParams, Message: err.Error()}
	}
	mode := os.FileMode(0o644)
	if st, err := os.Stat(full); err == nil {
		mode = st.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return &Error{Code: codeInternal, Message: err.Error()}
	}
	if err := os.WriteFile(full, []byte(p.Content), mode); err != nil {
		return &Error{Code: codeInternal, Message: err.Error()}
	}
	return nil
}

// defaultOutputLimit bounds a terminal's kept output when the agent sets
// no limit of its own.
const defaultOutputLimit = 1 << 20

type terminal struct {
	cmd   *exec.Cmd
	limit int

	mu        sync.Mutex
	out       []byte
	truncated bool
	exited    bool
	status    ExitStatus
	done      chan struct{}
}

func (t *terminal) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.out = append(t.out, b...)
	if len(t.out) > t.limit {
		// The protocol keeps the end: drop from the start, at a character.
		cut := len(t.out) - t.limit
		for cut < len(t.out) && !utf8.RuneStart(t.out[cut]) {
			cut++
		}
		t.out = append([]byte(nil), t.out[cut:]...)
		t.truncated = true
	}
	return len(b), nil
}

func (w *workspace) createTerminal(p TermCreateParams) (TermCreateResult, *Error) {
	if strings.TrimSpace(p.Command) == "" {
		return TermCreateResult{}, &Error{Code: codeInvalidParams, Message: "no command"}
	}
	dir := w.root
	if p.Cwd != "" {
		d, err := w.resolve(p.Cwd)
		if err != nil {
			return TermCreateResult{}, &Error{Code: codeInvalidParams, Message: err.Error()}
		}
		dir = d
	}
	limit := p.OutputByteLimit
	if limit <= 0 {
		limit = defaultOutputLimit
	}
	t := &terminal{limit: limit, done: make(chan struct{})}
	var cmd *exec.Cmd
	if len(p.Args) == 0 && strings.ContainsAny(p.Command, " \t|&;<>$`'\"") {
		// A whole command line, as agents often send one: the shell's.
		cmd = exec.Command("/bin/sh", "-c", p.Command)
	} else {
		cmd = exec.Command(p.Command, p.Args...)
	}
	cmd.Dir = dir
	cmd.Env = os.Environ()
	for _, e := range p.Env {
		cmd.Env = append(cmd.Env, e.Name+"="+e.Value)
	}
	cmd.Stdout, cmd.Stderr = t, t
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	t.cmd = cmd
	if err := cmd.Start(); err != nil {
		return TermCreateResult{}, &Error{Code: codeInternal, Message: err.Error()}
	}
	go func() {
		err := cmd.Wait()
		t.mu.Lock()
		t.exited = true
		t.status = exitStatusOf(cmd, err)
		t.mu.Unlock()
		close(t.done)
	}()
	w.mu.Lock()
	w.next++
	id := fmt.Sprintf("term-%d", w.next)
	w.terms[id] = t
	w.mu.Unlock()
	return TermCreateResult{TerminalID: id}, nil
}

func exitStatusOf(cmd *exec.Cmd, err error) ExitStatus {
	var st ExitStatus
	var ee *exec.ExitError
	if err != nil && !errors.As(err, &ee) {
		code := -1
		st.ExitCode = &code
		return st
	}
	ps := cmd.ProcessState
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		sig := ws.Signal().String()
		st.Signal = &sig
		return st
	}
	code := ps.ExitCode()
	st.ExitCode = &code
	return st
}

func (w *workspace) terminal(id string) (*terminal, *Error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	t := w.terms[id]
	if t == nil {
		return nil, &Error{Code: codeInvalidParams, Message: fmt.Sprintf("no terminal %q", id)}
	}
	return t, nil
}

func (w *workspace) output(id string) (TermOutputResult, *Error) {
	t, rerr := w.terminal(id)
	if rerr != nil {
		return TermOutputResult{}, rerr
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	res := TermOutputResult{Output: string(bytes.ToValidUTF8(t.out, nil)), Truncated: t.truncated}
	if t.exited {
		st := t.status
		res.ExitStatus = &st
	}
	return res, nil
}

func (w *workspace) wait(id string, stop <-chan struct{}) (ExitStatus, *Error) {
	t, rerr := w.terminal(id)
	if rerr != nil {
		return ExitStatus{}, rerr
	}
	select {
	case <-t.done:
	case <-stop:
		return ExitStatus{}, &Error{Code: codeInternal, Message: "conch is closing"}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.status, nil
}

func (t *terminal) kill() {
	select {
	case <-t.done:
		return
	default:
	}
	if t.cmd.Process != nil {
		_ = syscall.Kill(-t.cmd.Process.Pid, syscall.SIGKILL)
	}
}

func (w *workspace) killTerminal(id string) *Error {
	t, rerr := w.terminal(id)
	if rerr != nil {
		return rerr
	}
	t.kill()
	return nil
}

func (w *workspace) release(id string) *Error {
	t, rerr := w.terminal(id)
	if rerr != nil {
		return rerr
	}
	t.kill()
	w.mu.Lock()
	delete(w.terms, id)
	w.mu.Unlock()
	return nil
}

// closeAll ends every terminal the agent left running.
func (w *workspace) closeAll() {
	w.mu.Lock()
	terms := w.terms
	w.terms = map[string]*terminal{}
	w.mu.Unlock()
	for _, t := range terms {
		t.kill()
	}
}

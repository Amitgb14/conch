// Package sandboxdtest is a fake sandboxd, sandbox-cli's API, for tests:
// sandboxes are records, and a process is whatever the test's Run does
// with its argv, wired to the attach stream as sandboxd wires a guest's.
package sandboxdtest

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Proc is a process a test runs for the fake: its argv, its stdin, where
// its output goes. It returns the exit status.
type Proc struct {
	Argv   []string
	Tty    bool
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// Resized gets the terminal sizes attach sends.
	Resized chan [2]uint16
}

// Box is a sandbox the fake holds.
type Box struct {
	ID, Name, State string
	Labels          map[string]string
	CPUs            float64
	MemoryMB        int
}

// Server is the fake.
type Server struct {
	t   *testing.T
	srv *httptest.Server
	URL string

	mu      sync.Mutex
	boxes   map[string]*Box
	order   []string
	procs   map[int][]string
	ttys    map[int]bool
	nextPID int
	nextID  int
	calls   []string
	creates []map[string]any
	// Caps is what /v1/capabilities answers.
	Caps map[string]bool
	// Token, when set, is required as a bearer token.
	Token string
	// run runs a process (SetRun); nil echoes stdin to stdout and exits 0.
	run func(p Proc) int
	// Pending is how many looks a new sandbox stays pending for.
	Pending int
	// Fail answers "METHOD path-suffix" with this status and code.
	Fail map[string][2]string
	// Tunnel dials what a tunnel to port reaches; nil is 127.0.0.1:port.
	Tunnel func(port int) (net.Conn, error)
}

// New starts a fake on a TCP port.
func New(t *testing.T) *Server {
	t.Helper()
	s := newServer(t)
	s.srv = httptest.NewServer(s)
	s.URL = s.srv.URL
	t.Cleanup(s.srv.Close)
	return s
}

// NewUnix starts a fake on a unix socket at path.
func NewUnix(t *testing.T, path string) *Server {
	t.Helper()
	s := newServer(t)
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	s.srv = &httptest.Server{Listener: ln, Config: &http.Server{Handler: s}}
	s.srv.Start()
	s.URL = "unix://" + path
	t.Cleanup(func() { s.srv.Close(); os.Remove(path) })
	return s
}

func newServer(t *testing.T) *Server {
	return &Server{t: t, boxes: map[string]*Box{}, procs: map[int][]string{}, ttys: map[int]bool{},
		Caps: map[string]bool{"tunnel": true}, Fail: map[string][2]string{}}
}

// SetRun sets what a process does: given its argv and stdio, it returns
// the exit status. A process already running keeps the one it started with.
func (s *Server) SetRun(run func(p Proc) int) {
	s.mu.Lock()
	s.run = run
	s.mu.Unlock()
}

// Add puts a sandbox in the fake.
func (s *Server) Add(b Box) *Box {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b.State == "" {
		b.State = "running"
	}
	s.boxes[b.ID] = &b
	s.order = append(s.order, b.ID)
	return &b
}

// State is a sandbox's state, or "gone".
func (s *Server) State(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b := s.boxes[id]; b != nil {
		return b.State
	}
	return "gone"
}

// Called is every request, "METHOD path?query", one a line.
func (s *Server) Called() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.calls, "\n")
}

// Creates are the bodies of the creates asked for.
func (s *Server) Creates() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any{}, s.creates...)
}

// Argvs are the processes started, in order.
func (s *Server) Argvs() [][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out [][]string
	for pid := 1; pid <= s.nextPID; pid++ {
		out = append(out, s.procs[pid])
	}
	return out
}

func (s *Server) fail(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": msg}})
}

func (s *Server) box(b *Box) map[string]any {
	return map[string]any{"id": b.ID, "name": b.Name, "state": b.State, "image": "sandbox-base", "cpus": b.CPUs,
		"memory_mb": b.MemoryMB, "disk_mb": 4096, "labels": b.Labels, "created_at": "2026-10-03T10:00:00Z",
		"network": map[string]any{"mode": "none", "allow": nil}}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.calls = append(s.calls, r.Method+" "+r.URL.RequestURI())
	s.mu.Unlock()
	if s.Token != "" && r.Header.Get("Authorization") != "Bearer "+s.Token {
		s.fail(w, http.StatusUnauthorized, "unauthorized", "a token is needed")
		return
	}
	if r.Host != "localhost" && strings.HasPrefix(s.URL, "unix://") {
		s.fail(w, http.StatusForbidden, "forbidden_origin", "host "+r.Host)
		return
	}
	for key, f := range s.Fail {
		method, suffix, _ := strings.Cut(key, " ")
		if r.Method == method && strings.HasSuffix(r.URL.Path, suffix) {
			status, _ := strconv.Atoi(f[0])
			s.fail(w, status, f[1], "told to fail")
			return
		}
	}
	path := strings.TrimPrefix(r.URL.Path, "/v1/")
	parts := strings.Split(path, "/")
	switch {
	case path == "capabilities":
		json.NewEncoder(w).Encode(map[string]any{"api_version": "v1", "backend": "fake", "capabilities": s.Caps})
	case path == "sandboxes" && r.Method == http.MethodPost:
		var in map[string]any
		json.NewDecoder(r.Body).Decode(&in)
		s.mu.Lock()
		s.creates = append(s.creates, in)
		s.nextID++
		id := fmt.Sprintf("sbx_%08d0123", s.nextID)
		labels := map[string]string{}
		if l, ok := in["labels"].(map[string]any); ok {
			for k, v := range l {
				labels[k], _ = v.(string)
			}
		}
		state := "running"
		if s.Pending > 0 {
			state = "pending"
		}
		b := &Box{ID: id, State: state, Labels: labels}
		if c, ok := in["cpus"].(float64); ok {
			b.CPUs = c
		}
		s.boxes[id] = b
		s.order = append(s.order, id)
		body := s.box(b)
		s.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(body)
	case path == "sandboxes" && r.Method == http.MethodGet:
		s.mu.Lock()
		var list []map[string]any
		for i := len(s.order) - 1; i >= 0; i-- {
			b := s.boxes[s.order[i]]
			if b == nil {
				continue
			}
			ok := true
			for _, sel := range r.URL.Query()["label"] {
				k, v, _ := strings.Cut(sel, "=")
				ok = ok && b.Labels[k] == v
			}
			if ok {
				list = append(list, s.box(b))
			}
		}
		s.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"sandboxes": list})
	case len(parts) >= 2 && parts[0] == "sandboxes":
		s.sandbox(w, r, parts[1], parts[2:])
	default:
		s.fail(w, http.StatusNotFound, "not_found", path)
	}
}

func (s *Server) sandbox(w http.ResponseWriter, r *http.Request, id string, rest []string) {
	s.mu.Lock()
	b := s.boxes[id]
	s.mu.Unlock()
	if b == nil {
		s.fail(w, http.StatusNotFound, "not_found", "no sandbox "+id)
		return
	}
	switch {
	case len(rest) == 0 && r.Method == http.MethodGet:
		s.mu.Lock()
		if b.State == "pending" {
			if s.Pending--; s.Pending <= 0 {
				b.State = "running"
			}
		}
		body := s.box(b)
		s.mu.Unlock()
		json.NewEncoder(w).Encode(body)
	case len(rest) == 0 && r.Method == http.MethodDelete:
		s.mu.Lock()
		b.State = "terminated"
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case len(rest) == 1 && rest[0] == "suspend":
		s.mu.Lock()
		b.State = "suspended"
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case len(rest) == 1 && rest[0] == "resume":
		s.mu.Lock()
		b.State = "running"
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case len(rest) == 1 && rest[0] == "processes" && r.Method == http.MethodPost:
		var in struct {
			Argv []string `json:"argv"`
			Tty  bool     `json:"tty"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		s.mu.Lock()
		s.nextPID++
		pid := s.nextPID
		s.procs[pid], s.ttys[pid] = in.Argv, in.Tty
		s.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"pid": pid, "argv": in.Argv, "state": "running"})
	case len(rest) == 3 && rest[0] == "processes" && rest[2] == "attach":
		pid, _ := strconv.Atoi(rest[1])
		s.mu.Lock()
		argv, tty := s.procs[pid], s.ttys[pid]
		s.mu.Unlock()
		if argv == nil || r.Header.Get("Upgrade") != "sbx-stream/1" {
			s.fail(w, http.StatusBadRequest, "invalid_request", "no such process or upgrade")
			return
		}
		conn := s.hijack(w, "sbx-stream/1")
		if conn != nil {
			go s.attach(conn, argv, tty)
		}
	case len(rest) == 1 && rest[0] == "tunnel":
		port, _ := strconv.Atoi(r.URL.Query().Get("port"))
		dial := s.Tunnel
		if dial == nil {
			dial = func(port int) (net.Conn, error) { return net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port)) }
		}
		inside, err := dial(port)
		if err != nil {
			s.fail(w, http.StatusBadGateway, "internal", err.Error())
			return
		}
		conn := s.hijack(w, "sbx-tunnel/1")
		if conn == nil {
			inside.Close()
			return
		}
		// Half-close aware, as sandboxd's is: the client finishing what it
		// sends ends that direction only.
		go func() {
			defer conn.Close()
			defer inside.Close()
			go func() {
				io.Copy(inside, conn)
				if cw, ok := inside.(interface{ CloseWrite() error }); ok {
					cw.CloseWrite()
				}
			}()
			io.Copy(conn, inside)
		}()
	default:
		s.fail(w, http.StatusNotFound, "not_found", strings.Join(rest, "/"))
	}
}

func (s *Server) hijack(w http.ResponseWriter, protocol string) net.Conn {
	hj, ok := w.(http.Hijacker)
	if !ok {
		s.t.Error("sandboxdtest: no hijacker")
		return nil
	}
	conn, buf, err := hj.Hijack()
	if err != nil {
		s.t.Error(err)
		return nil
	}
	fmt.Fprintf(buf, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: %s\r\n\r\n", protocol)
	buf.Flush()
	return conn
}

// frameWriter sends what a process writes as frames of one type.
type frameWriter struct {
	mu   *sync.Mutex
	w    io.Writer
	kind byte
}

func (f frameWriter) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := writeFrame(f.w, f.kind, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func writeFrame(w io.Writer, typ byte, payload []byte) error {
	var hdr [5]byte
	hdr[0] = typ
	binary.BigEndian.PutUint32(hdr[1:], uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func (s *Server) attach(conn net.Conn, argv []string, tty bool) {
	defer conn.Close()
	stdinR, stdinW := io.Pipe()
	resized := make(chan [2]uint16, 16)
	go func() {
		br := bufio.NewReader(conn)
		defer stdinW.Close()
		for {
			var hdr [5]byte
			if _, err := io.ReadFull(br, hdr[:]); err != nil {
				return
			}
			payload := make([]byte, binary.BigEndian.Uint32(hdr[1:]))
			if _, err := io.ReadFull(br, payload); err != nil {
				return
			}
			switch hdr[0] {
			case 'i':
				stdinW.Write(payload)
			case 'e':
				stdinW.Close()
			case 'r':
				if len(payload) == 4 {
					select {
					case resized <- [2]uint16{binary.BigEndian.Uint16(payload[0:2]), binary.BigEndian.Uint16(payload[2:4])}:
					default:
					}
				}
			}
		}
	}()
	var mu sync.Mutex
	p := Proc{Argv: argv, Tty: tty, Stdin: stdinR, Stdout: frameWriter{&mu, conn, 'o'}, Stderr: frameWriter{&mu, conn, 'E'}, Resized: resized}
	if tty {
		p.Stderr = p.Stdout // one stream on a terminal
	}
	s.mu.Lock()
	run := s.run
	s.mu.Unlock()
	if run == nil {
		run = func(p Proc) int { io.Copy(p.Stdout, p.Stdin); return 0 }
	}
	code := run(p)
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], uint32(int32(code)))
	mu.Lock()
	writeFrame(conn, 'x', b[:])
	mu.Unlock()
	time.Sleep(10 * time.Millisecond) // let the exit frame go before closing
}

// Listen is a Run helper for conch's bridge: it listens on a loopback
// port, says so as conch bridge -listen does, and hands the one connection
// it takes to serve.
func Listen(p Proc, serve func(net.Conn)) int {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(p.Stderr, err)
		return 1
	}
	fmt.Fprintf(p.Stdout, "listening %d\n", ln.Addr().(*net.TCPAddr).Port)
	go func() {
		defer ln.Close()
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		serve(conn)
	}()
	return 0
}

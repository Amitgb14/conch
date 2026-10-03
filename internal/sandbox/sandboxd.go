package sandbox

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Amitgb14/conch/internal/config"
)

// Sandboxd is sandbox-cli's sandboxes, through sandboxd's API (v1): a
// microVM each — Apple's container runtime on a Mac, Firecracker on a Linux
// machine of your own — behind one API, so conch neither knows nor cares
// which. sandboxd has no ssh: conch reaches a sandbox by starting a process
// in it and attaching to its stdin and stdout, and carries its own
// connection through a tunnel to a port inside, since an attached process's
// output is kept, and capped, for replay.

// DefaultSandboxdIdle is how long a sandbox conch makes may sit with no
// request and no process before sandboxd deletes it: the longest sandboxd
// allows, since its idling ends a sandbox rather than pausing it. conch's
// own idle watch is what stops one nobody is using.
const DefaultSandboxdIdle = 7 * 24 * time.Hour

// sandboxdHome is the guest's home, where scripts run.
const sandboxdHome = "/sandbox/home"

// sandboxdPoll is how often a pending sandbox is looked at; tests shorten
// it.
var sandboxdPoll = 500 * time.Millisecond

// sandboxdListenWait bounds waiting for conch inside to say which port it
// listens on.
var sandboxdListenWait = 30 * time.Second

// Sandboxd is the provider.
type Sandboxd struct {
	endpoint string // unix:///path or http(s)://host:port
	token    string
	ca       []byte
	setupErr error // what is wrong with the settings, for Check

	image    string
	network  string
	allow    []string
	autoStop int // minutes; 0 is DefaultSandboxdIdle

	base string // what requests are made to
	hc   *http.Client

	capsMu sync.Mutex
	caps   map[string]bool

	// self is this conch, which the exec commands run; tests replace it.
	self func() (string, error)
}

// NewSandboxd returns the provider for cfg: the endpoint it names, or the
// context sandbox-cli is using — its local socket unless told otherwise.
func NewSandboxd(cfg config.ProviderCfg) *Sandboxd {
	s := &Sandboxd{image: strings.TrimSpace(cfg.Snapshot), network: strings.TrimSpace(cfg.Network),
		allow: cfg.Allow, autoStop: cfg.AutoStop, self: os.Executable}
	s.setupErr = s.resolve(cfg)
	if s.setupErr == nil {
		s.setupErr = s.dialer()
	}
	return s
}

// sandboxConfigDir is sandbox-cli's own configuration folder.
func sandboxConfigDir() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "sandbox")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "sandbox")
}

// sandboxdSocket is where a local sandboxd listens, as sandbox-cli has it.
func sandboxdSocket() string {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return filepath.Join(d, "sandboxd.sock")
	}
	return filepath.Join(sandboxConfigDir(), "sandboxd.sock")
}

// sandboxContexts is sandbox-cli's contexts.json.
type sandboxContexts struct {
	Current  string `json:"current"`
	Contexts map[string]struct {
		Endpoint  string `json:"endpoint"`
		TokenFile string `json:"token_file"`
		CAFile    string `json:"ca_file"`
	} `json:"contexts"`
}

// resolve works out the endpoint, token and CA: the settings' endpoint
// when there is one, otherwise a sandbox-cli context — the one named,
// $SANDBOX_CONTEXT, or sandbox-cli's current one, which is "local" when
// nothing says otherwise.
func (s *Sandboxd) resolve(cfg config.ProviderCfg) error {
	if url := strings.TrimSpace(cfg.APIURL); url != "" {
		s.endpoint = url
		s.token = strings.TrimSpace(cfg.APIKey)
		if s.token == "" {
			s.token = strings.TrimSpace(os.Getenv(firstSet(cfg.APIKeyEnv, "SANDBOXD_TOKEN")))
		}
		if cfg.CAFile != "" {
			ca, err := os.ReadFile(expandHome(cfg.CAFile))
			if err != nil {
				return fmt.Errorf("ca_file: %w", err)
			}
			s.ca = ca
		}
		return nil
	}
	var file sandboxContexts
	path := filepath.Join(sandboxConfigDir(), "contexts.json")
	switch data, err := os.ReadFile(path); {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return err
	default:
		if err := json.Unmarshal(data, &file); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	name := firstSet(cfg.Target, os.Getenv("SANDBOX_CONTEXT"), file.Current, "local")
	if name == "local" {
		s.endpoint = "unix://" + sandboxdSocket()
		return nil
	}
	ctx, ok := file.Contexts[name]
	if !ok {
		return fmt.Errorf("%w: sandbox-cli has no context %q (sandbox-cli context ls)", ErrNotConfigured, name)
	}
	s.endpoint = ctx.Endpoint
	if ctx.TokenFile != "" {
		tok, err := os.ReadFile(expandHome(ctx.TokenFile))
		if err != nil {
			return fmt.Errorf("context %s: %w", name, err)
		}
		s.token = strings.TrimSpace(string(tok))
	}
	if ctx.CAFile != "" {
		ca, err := os.ReadFile(expandHome(ctx.CAFile))
		if err != nil {
			return fmt.Errorf("context %s: %w", name, err)
		}
		s.ca = ca
	}
	return nil
}

// dialer makes the HTTP client for the endpoint.
func (s *Sandboxd) dialer() error {
	if path, ok := strings.CutPrefix(s.endpoint, "unix://"); ok {
		if path == "" {
			return fmt.Errorf("endpoint %q names no socket", s.endpoint)
		}
		// The host is never dialled; it only has to be a loopback name,
		// which sandboxd's guard against DNS rebinding wants.
		s.base = "http://localhost"
		s.hc = &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", path)
		}}}
		return nil
	}
	u, err := url.Parse(s.endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("endpoint %q: give http(s)://host[:port] or unix:///path", s.endpoint)
	}
	s.base = strings.TrimSuffix(u.String(), "/")
	tr := &http.Transport{}
	if len(s.ca) > 0 {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(s.ca) {
			return errors.New("no certificates in the CA file")
		}
		tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	s.hc = &http.Client{Transport: tr}
	return nil
}

func expandHome(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, rest)
		}
	}
	return p
}

func (s *Sandboxd) Name() string { return "sandbox-cli" }

// Endpoint is where sandboxd is reached.
func (s *Sandboxd) Endpoint() string { return s.endpoint }

// local reports whether sandboxd runs on this computer.
func (s *Sandboxd) local() bool { return strings.HasPrefix(s.endpoint, "unix://") }

// CanBind: only a sandboxd on this computer can mount a folder of it.
func (s *Sandboxd) CanBind() bool { return s.local() }

func (s *Sandboxd) Check() error {
	if s.setupErr != nil {
		if errors.Is(s.setupErr, ErrNotConfigured) {
			return s.setupErr
		}
		return fmt.Errorf("%w: %v", ErrNotConfigured, s.setupErr)
	}
	if path, ok := strings.CutPrefix(s.endpoint, "unix://"); ok {
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("%w: sandboxd isn't running here (nothing at %s); start it as sandbox-cli's docs say", ErrNotConfigured, path)
		}
		return nil
	}
	if s.token == "" {
		return fmt.Errorf("%w: %s needs a token: set $SANDBOXD_TOKEN, or use a sandbox-cli context that has one", ErrNotConfigured, s.endpoint)
	}
	return nil
}

// apiError is sandboxd's error body.
type apiError struct {
	Status  int
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *apiError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("sandboxd: %s (%d)", e.Code, e.Status)
	}
	return fmt.Sprintf("sandboxd: %s (%d)", e.Message, e.Status)
}

func (s *Sandboxd) request(ctx context.Context, method, path string, in any) (*http.Request, error) {
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.base+path, body)
	if err != nil {
		return nil, err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	return req, nil
}

// failure reads an error response.
func failure(resp *http.Response) error {
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var body struct {
		Error apiError `json:"error"`
	}
	e := &apiError{Status: resp.StatusCode}
	if json.Unmarshal(data, &body) == nil && body.Error.Code != "" {
		e.Code, e.Message = body.Error.Code, body.Error.Message
	} else {
		e.Code, e.Message = "internal", strings.TrimSpace(string(data))
	}
	if e.Code == "not_found" || resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%w: %s", ErrNotFound, e.Message)
	}
	return e
}

// call makes a JSON request.
func (s *Sandboxd) call(ctx context.Context, method, path string, in, out any) error {
	if s.setupErr != nil {
		return s.Check()
	}
	req, err := s.request(ctx, method, path, in)
	if err != nil {
		return err
	}
	resp, err := s.hc.Do(req)
	if err != nil {
		return fmt.Errorf("sandboxd at %s: %w", s.endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return failure(resp)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// capable reports whether the endpoint has a capability, asking once.
func (s *Sandboxd) capable(ctx context.Context, name string) (bool, error) {
	s.capsMu.Lock()
	defer s.capsMu.Unlock()
	if s.caps == nil {
		var out struct {
			Capabilities map[string]bool `json:"capabilities"`
		}
		if err := s.call(ctx, http.MethodGet, "/v1/capabilities", nil, &out); err != nil {
			return false, err
		}
		s.caps = out.Capabilities
		if s.caps == nil {
			s.caps = map[string]bool{}
		}
	}
	return s.caps[name], nil
}

// sbx is sandboxd's sandbox, as far as conch reads it.
type sbx struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	State     string            `json:"state"`
	Image     string            `json:"image"`
	CPUs      float64           `json:"cpus"`
	MemoryMB  int               `json:"memory_mb"`
	DiskMB    int               `json:"disk_mb"`
	CreatedAt time.Time         `json:"created_at"`
	Labels    map[string]string `json:"labels"`
}

func (b sbx) sandbox() Sandbox {
	state := State(b.State)
	switch b.State {
	case "pending":
		state = StateStarting
	case "running":
		state = StateStarted
	case "suspended":
		state = StateStopped
	case "terminated":
		state = StateDestroyed
	}
	return Sandbox{ID: b.ID, Name: b.Name, State: state, CPU: int(b.CPUs + 0.5), Memory: b.MemoryMB / 1024,
		Disk: b.DiskMB / 1024, Labels: b.Labels, Created: b.CreatedAt}
}

// sandboxPath is a sandbox's place in the API. The ID goes in a path, so
// one that would change the path is refused before it is sent.
func sandboxPath(id string) (string, error) {
	if id == "" || strings.ContainsAny(id, "/?#%") || id == "." || id == ".." {
		return "", ErrNotFound
	}
	return "/v1/sandboxes/" + id, nil
}

func (s *Sandboxd) Create(ctx context.Context, spec Spec) (Sandbox, error) {
	idle := int(DefaultSandboxdIdle / time.Second)
	if spec.AutoStop > 0 {
		idle = spec.AutoStop * 60
	} else if s.autoStop > 0 {
		idle = s.autoStop * 60
	}
	labels := map[string]string{Label: "1"}
	for k, v := range spec.Labels {
		labels[k] = v
	}
	in := map[string]any{
		"image":             firstSet(spec.Snapshot, s.image),
		"labels":            labels,
		"idle_timeout_secs": idle,
	}
	if spec.Name != "" {
		in["name"] = spec.Name
	}
	if spec.CPU > 0 {
		in["cpus"] = spec.CPU
	}
	if spec.Memory > 0 {
		in["memory_mb"] = spec.Memory * 1024
	}
	if spec.Disk > 0 {
		in["disk_mb"] = spec.Disk * 1024
	}
	if len(spec.Env) > 0 {
		in["env"] = spec.Env
	}
	if in["image"] == "" {
		delete(in, "image")
	}
	if s.network != "" {
		// No allow list is sandboxd's own default for the mode; an empty
		// one would mean nothing at all, which it refuses.
		policy := map[string]any{"mode": s.network}
		if len(s.allow) > 0 {
			policy["allow"] = s.allow
		}
		in["network"] = policy
	}
	if dir := strings.TrimSpace(spec.Dir); dir != "" {
		if !s.CanBind() {
			return Sandbox{}, fmt.Errorf("a folder can only be mounted when sandboxd runs on this computer, not at %s", s.endpoint)
		}
		abs, err := filepath.Abs(expandHome(dir))
		if err != nil {
			return Sandbox{}, err
		}
		if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
			return Sandbox{}, fmt.Errorf("%s is not a folder", dir)
		}
		in["bind"] = map[string]any{"host_path": abs}
	}
	var out sbx
	if err := s.call(ctx, http.MethodPost, "/v1/sandboxes", in, &out); err != nil {
		return Sandbox{}, fmt.Errorf("create sandbox: %w", err)
	}
	return s.wait(ctx, out.sandbox())
}

// wait looks at a pending sandbox until it runs.
func (s *Sandboxd) wait(ctx context.Context, got Sandbox) (Sandbox, error) {
	for {
		switch {
		case got.State == StateStarted:
			return got, nil
		case got.State.failed():
			return got, &FailedError{ID: got.ID, State: got.State}
		}
		select {
		case <-ctx.Done():
			return got, fmt.Errorf("sandbox %s is still %s: %w", got.ID, got.State, ctx.Err())
		case <-time.After(sandboxdPoll):
		}
		next, err := s.Get(ctx, got.ID)
		if err != nil {
			if ctx.Err() != nil { // the time ran out mid-question
				return got, fmt.Errorf("sandbox %s is still %s: %w", got.ID, got.State, ctx.Err())
			}
			return got, err
		}
		got = next
	}
}

// Get finds one of conch's sandboxes: one it didn't make is not found.
func (s *Sandboxd) Get(ctx context.Context, id string) (Sandbox, error) {
	path, err := sandboxPath(id)
	if err != nil {
		return Sandbox{}, err
	}
	var out sbx
	if err := s.call(ctx, http.MethodGet, path, nil, &out); err != nil {
		return Sandbox{}, err
	}
	if out.Labels[Label] != "1" {
		return Sandbox{}, ErrNotFound
	}
	return out.sandbox(), nil
}

// List returns conch's sandboxes that are still there: sandboxd keeps a
// terminated one listed for a while, which is no sandbox to show.
func (s *Sandboxd) List(ctx context.Context) ([]Sandbox, error) {
	var out struct {
		Sandboxes []sbx `json:"sandboxes"`
	}
	if err := s.call(ctx, http.MethodGet, "/v1/sandboxes?label="+url.QueryEscape(Label+"=1"), nil, &out); err != nil {
		return nil, err
	}
	var list []Sandbox
	for _, b := range out.Sandboxes {
		if b.State != "terminated" && b.Labels[Label] == "1" {
			list = append(list, b.sandbox())
		}
	}
	return list, nil
}

// Start resumes a suspended sandbox.
func (s *Sandboxd) Start(ctx context.Context, id string) (Sandbox, error) {
	got, err := s.Get(ctx, id)
	if err != nil || got.State == StateStarted {
		return got, err
	}
	if got.State.Going() {
		return got, &FailedError{ID: id, State: got.State}
	}
	path, _ := sandboxPath(id)
	if got.State == StateStopped {
		if err := s.call(ctx, http.MethodPost, path+"/resume", nil, nil); err != nil {
			return got, fmt.Errorf("resume: %w", err)
		}
	}
	got, err = s.Get(ctx, id)
	if err != nil {
		return got, err
	}
	return s.wait(ctx, got)
}

// Stop suspends the sandbox, keeping its memory and disk. Not every
// backend can (a Mac's can't), and then there is no stopping it — only
// deleting.
func (s *Sandboxd) Stop(ctx context.Context, id string) error {
	got, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if got.State == StateStopped {
		return nil
	}
	can, err := s.capable(ctx, "suspend")
	if err != nil {
		return err
	}
	if !can {
		return fmt.Errorf("%w: sandboxd at %s can't suspend one", ErrCannotStop, s.endpoint)
	}
	path, _ := sandboxPath(id)
	if err := s.call(ctx, http.MethodPost, path+"/suspend", nil, nil); err != nil {
		var ae *apiError
		if errors.As(err, &ae) && ae.Status == http.StatusConflict {
			return fmt.Errorf("suspend: something is still running there that sandboxd won't cut off: %w", err)
		}
		return fmt.Errorf("suspend: %w", err)
	}
	return nil
}

func (s *Sandboxd) Delete(ctx context.Context, id string) error {
	got, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if got.State == StateDestroyed {
		return ErrNotFound
	}
	path, _ := sandboxPath(id)
	return s.call(ctx, http.MethodDelete, path, nil, nil)
}

func (s *Sandboxd) SSHAccess(context.Context, string) (Access, error) {
	return Access{}, errors.New("a sandbox-cli sandbox has no ssh; conch reaches it through sandboxd")
}

// ExecArgv is conch's own sandbox-io, which runs the script through the
// API.
func (s *Sandboxd) ExecArgv(id string, tty bool) []string {
	argv := s.ioArgv()
	if tty {
		argv = append(argv, "-t")
	}
	return append(argv, id)
}

// BridgeArgv is sandbox-io carrying conch's connection through a tunnel.
func (s *Sandboxd) BridgeArgv(id string) []string {
	return append(s.ioArgv(), "-bridge", id)
}

func (s *Sandboxd) ioArgv() []string {
	self, err := s.self()
	if err != nil {
		self = "conch"
	}
	return []string{self, "sandbox-io", "-provider", s.Name()}
}

// Frames of an attached process's stream: a type byte, a big-endian
// length, the payload.
const (
	frameStdin  byte = 'i'
	frameEOF    byte = 'e'
	frameResize byte = 'r'
	frameStdout byte = 'o'
	frameStderr byte = 'E'
	frameExit   byte = 'x'
	maxFrame         = 1 << 20
)

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

func readFrame(r io.Reader) (byte, []byte, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(hdr[1:])
	if n > maxFrame {
		return 0, nil, fmt.Errorf("a frame of %d bytes", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, nil, err
	}
	return hdr[0], buf, nil
}

// upgrade turns a GET into a stream of protocol.
func (s *Sandboxd) upgrade(ctx context.Context, path, protocol string) (io.ReadWriteCloser, error) {
	if s.setupErr != nil {
		return nil, s.Check()
	}
	req, err := s.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", protocol)
	resp, err := s.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sandboxd at %s: %w", s.endpoint, err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			return nil, failure(resp)
		}
		return nil, fmt.Errorf("%s: %s", protocol, resp.Status)
	}
	rwc, ok := resp.Body.(io.ReadWriteCloser)
	if !ok {
		resp.Body.Close()
		return nil, fmt.Errorf("%s: the connection was not upgraded", protocol)
	}
	return rwc, nil
}

// Term is the terminal an exec runs on, for a person at one.
type Term struct {
	Rows, Cols uint16
	// Resize brings new sizes until it is closed.
	Resize <-chan [2]uint16
}

// start starts argv in the sandbox in the background and attaches to it.
func (s *Sandboxd) start(ctx context.Context, id string, argv []string, term *Term) (io.ReadWriteCloser, error) {
	path, err := sandboxPath(id)
	if err != nil {
		return nil, err
	}
	in := map[string]any{"argv": argv, "cwd": sandboxdHome}
	if term != nil {
		in["tty"], in["rows"], in["cols"] = true, term.Rows, term.Cols
	}
	var proc struct {
		PID int `json:"pid"`
	}
	if err := s.call(ctx, http.MethodPost, path+"/processes", in, &proc); err != nil {
		return nil, err
	}
	// Attaching replays the output from the start, so nothing the process
	// said before this is lost.
	return s.upgrade(ctx, path+"/processes/"+strconv.Itoa(proc.PID)+"/attach", "sbx-stream/1")
}

// Exec runs script in the sandbox with stdin, stdout and stderr passed
// through, and returns its exit status. With a terminal, its output is one
// stream.
func (s *Sandboxd) Exec(ctx context.Context, id, script string, term *Term, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	stream, err := s.start(ctx, id, []string{"sh", "-c", script}, term)
	if err != nil {
		return -1, err
	}
	defer stream.Close()
	var mu sync.Mutex
	send := func(typ byte, b []byte) error {
		mu.Lock()
		defer mu.Unlock()
		return writeFrame(stream, typ, b)
	}
	if stdin != nil {
		go func() {
			buf := make([]byte, 64<<10)
			for {
				n, err := stdin.Read(buf)
				if n > 0 && send(frameStdin, buf[:n]) != nil {
					return
				}
				if err != nil {
					_ = send(frameEOF, nil)
					return
				}
			}
		}()
	} else {
		_ = send(frameEOF, nil)
	}
	if term != nil && term.Resize != nil {
		go func() {
			for size := range term.Resize {
				var b [4]byte
				binary.BigEndian.PutUint16(b[0:2], size[0])
				binary.BigEndian.PutUint16(b[2:4], size[1])
				if send(frameResize, b[:]) != nil {
					return
				}
			}
		}()
	}
	for {
		typ, payload, err := readFrame(stream)
		if err != nil {
			return -1, fmt.Errorf("the connection to %s ended: %w", id, err)
		}
		switch typ {
		case frameStdout:
			_, _ = stdout.Write(payload)
		case frameStderr:
			_, _ = stderr.Write(payload)
		case frameExit:
			if len(payload) != 4 {
				return -1, errors.New("a malformed exit frame")
			}
			return int(int32(binary.BigEndian.Uint32(payload))), nil
		}
	}
}

// Bridge carries conch's connection: script (conch there, with "bridge")
// is started listening on a port of the sandbox's own loopback, and the
// connection goes through a tunnel to it, raw bytes both ways, for as long
// as either side keeps it. Not through the attached process itself, whose
// output sandboxd keeps for replay and stops passing on past a limit.
func (s *Sandboxd) Bridge(ctx context.Context, id, script string, stdin io.Reader, stdout io.Writer) error {
	stream, err := s.start(ctx, id, []string{"sh", "-c", script + " -listen 127.0.0.1:0"}, nil)
	if err != nil {
		return err
	}
	port, err := listenPort(stream)
	stream.Close() // detaches; it keeps listening
	if err != nil {
		return err
	}
	path, _ := sandboxPath(id)
	tunnel, err := s.upgrade(ctx, path+"/tunnel?port="+strconv.Itoa(port), "sbx-tunnel/1")
	if err != nil {
		return fmt.Errorf("tunnel to conch in %s: %w", id, err)
	}
	defer tunnel.Close()
	// What conch sends goes in until it stops; the bridge lasts until conch
	// there stops answering. conch ending its side kills this process,
	// which is how a connection that is done gets closed.
	go func() {
		_, _ = io.Copy(tunnel, stdin)
		if cw, ok := tunnel.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
	}()
	_, _ = io.Copy(stdout, tunnel)
	return nil
}

// listenPort reads "listening PORT" from conch's bridge, or why it ended
// without saying it.
func listenPort(stream io.Reader) (int, error) {
	type result struct {
		port int
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		var out, errOut bytes.Buffer
		for {
			typ, payload, err := readFrame(stream)
			if err != nil {
				ch <- result{err: fmt.Errorf("conch there ended before it listened: %w", err)}
				return
			}
			switch typ {
			case frameStdout:
				out.Write(payload)
				sc := bufio.NewScanner(bytes.NewReader(out.Bytes()))
				for sc.Scan() {
					if rest, ok := strings.CutPrefix(sc.Text(), "listening "); ok {
						if port, err := strconv.Atoi(strings.TrimSpace(rest)); err == nil && port > 0 && port < 65536 {
							ch <- result{port: port}
							return
						}
					}
				}
			case frameStderr:
				errOut.Write(payload)
			case frameExit:
				msg := strings.TrimSpace(errOut.String())
				if strings.Contains(msg, "-listen") || strings.Contains(msg, "flag provided") {
					msg = "the conch there is too old to be reached this way: " + msg
				}
				if msg == "" {
					msg = "it exited without saying why"
				}
				ch <- result{err: errors.New("conch there didn't listen: " + msg)}
				return
			}
		}
	}()
	select {
	case r := <-ch:
		return r.port, r.err
	case <-time.After(sandboxdListenWait):
		return 0, errors.New("conch there never said where it listens")
	}
}

// Streamer is a provider that carries a script, or conch's connection,
// itself rather than through ssh: what conch sandbox-io runs.
type Streamer interface {
	Exec(ctx context.Context, id, script string, term *Term, stdin io.Reader, stdout, stderr io.Writer) (int, error)
	Bridge(ctx context.Context, id, script string, stdin io.Reader, stdout io.Writer) error
}

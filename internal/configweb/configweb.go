// Package configweb is a page in the browser for editing config.toml:
// every setting the TUI's Settings screen has, and the ones it only names
// (actions, permission rules, check commands), as a form.
//
// It listens on 127.0.0.1 only, and the API answers only a request that
// carries the token handed out with the address — in the URL's fragment,
// which a browser never sends anywhere — in a header of its own. A page
// on another site can reach 127.0.0.1 but cannot read the fragment, and
// cannot set the header without a preflight this server never grants; the
// Host check stops a name that resolves to 127.0.0.1 being used to the
// same end. Anything else running as you could edit config.toml anyway.
package configweb

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/Amitgb14/conch/internal/config"
)

// The page, built by `make settings-ui` from app/ and committed, so that
// conch builds with go alone.
//
//go:embed all:dist
var dist embed.FS

// TokenHeader carries the token on every API request.
const TokenHeader = "X-Conch-Token"

// maxBody is the most a save may send: config.toml is a few kilobytes.
const maxBody = 1 << 20

// Server is the editor. OnSave, when set, is told about every saved
// configuration — the TUI takes it at once rather than writing its own
// older copy over it the next time it saves.
type Server struct {
	Choices Choices
	OnSave  func(config.Config)

	token string
	mu    sync.Mutex // one save at a time: the version check and the write are one step
}

// New makes a server with a fresh token.
func New(c Choices) (*Server, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return &Server{Choices: c, token: hex.EncodeToString(b)}, nil
}

// Token is what the page has to send back.
func (s *Server) Token() string { return s.token }

// URL is the address to open for a server listening on addr.
func (s *Server) URL(addr string) string {
	return "http://" + addr + "/#t=" + s.token
}

// Payload is what the page is given, and what a save answers with.
type Payload struct {
	// Config is config.toml as TOML reads it, defaults filled in: tables
	// by their keys, so the page and the file use the same names.
	Config   map[string]any `json:"config"`
	Raw      string         `json:"raw"`     // the file as it is
	Version  string         `json:"version"` // what the file was when read; a save names it
	Path     string         `json:"path"`
	Exists   bool           `json:"exists"`
	Schema   []Section      `json:"schema"`
	Problems []string       `json:"problems,omitempty"` // what conch will not use as written
}

// saveRequest is a save: either the form's Config or the Raw text.
type saveRequest struct {
	Version string          `json:"version"`
	Config  json.RawMessage `json:"config,omitempty"`
	Raw     *string         `json:"raw,omitempty"`
}

// apiError is how a refusal is said, with the current state when the
// refusal is that the file moved underneath.
type apiError struct {
	Error   string   `json:"error"`
	Current *Payload `json:"current,omitempty"`
}

// Handler serves the page and the API.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/config", s.guard(s.config))
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // the embed always has dist, or this does not build
	}
	files := http.FileServerFS(sub)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if !localHost(r.Host) {
			http.Error(w, "not this host", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; frame-ancestors 'none'")
		files.ServeHTTP(w, r)
	})
	return mux
}

// localHost reports whether a request's Host names this computer by
// address, not by some name that a site can point here.
func localHost(host string) bool {
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		h = host
	}
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(strings.Trim(h, "[]"))
	return ip != nil && ip.IsLoopback()
}

func (s *Server) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get(TokenHeader)
		if !localHost(r.Host) || subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
			writeJSON(w, http.StatusUnauthorized, apiError{Error: "this page's key is out of date: open Edit in browser again from conch"})
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		next(w, r)
	}
}

func (s *Server) config(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		p, err := s.read()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, apiError{Error: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, p)
	case http.MethodPut:
		s.save(w, r)
	default:
		w.Header().Set("Allow", "GET, PUT")
		writeJSON(w, http.StatusMethodNotAllowed, apiError{Error: "GET or PUT"})
	}
}

// version names the file's contents, so a save can tell that somebody —
// the TUI, an editor, another tab — wrote it since the page read it.
func version(raw []byte, exists bool) string {
	if !exists {
		return "none"
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:8])
}

func readFile() ([]byte, bool, error) {
	raw, err := os.ReadFile(config.File())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	return raw, err == nil, err
}

func (s *Server) read() (*Payload, error) {
	raw, exists, err := readFile()
	if err != nil {
		return nil, err
	}
	p := &Payload{Raw: string(raw), Version: version(raw, exists), Path: config.File(), Exists: exists, Schema: Schema(s.Choices)}
	cfg := config.Default()
	if exists {
		if _, err := toml.Decode(string(raw), &cfg); err != nil {
			// The form cannot show a file it cannot read; the raw text
			// can, which is where the mistake gets put right.
			p.Problems = append(p.Problems, "config.toml can't be read, so conch is using its defaults: "+err.Error())
			cfg = config.Default()
		}
	}
	if cfg.Keys.Prefix == "" {
		cfg.Keys.Prefix = config.Default().Keys.Prefix
	}
	if p.Config, err = toMap(cfg); err != nil {
		return nil, err
	}
	p.Problems = append(p.Problems, problems(cfg)...)
	return p, nil
}

func (s *Server) save(w http.ResponseWriter, r *http.Request) {
	var req saveRequest
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBody))
	if err := dec.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "that isn't a save: " + err.Error()})
		return
	}
	var cfg config.Config
	var err error
	switch {
	case req.Raw != nil:
		cfg, err = fromRaw(*req.Raw)
	case len(req.Config) > 0:
		cfg, err = fromJSON(req.Config)
	default:
		err = errors.New("nothing to save")
	}
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: err.Error()})
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	raw, exists, err := readFile()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiError{Error: err.Error()})
		return
	}
	if req.Version != version(raw, exists) {
		cur, _ := s.read()
		writeJSON(w, http.StatusConflict, apiError{Error: "config.toml changed since this page read it", Current: cur})
		return
	}
	if req.Raw != nil {
		// Written as it was typed, comments and all: that is what a raw
		// edit is for. An empty one is the defaults.
		err = writeRaw(*req.Raw)
	} else {
		err = config.Save(cfg)
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiError{Error: "saving: " + err.Error()})
		return
	}
	if s.OnSave != nil {
		s.OnSave(cfg)
	}
	p, err := s.read()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func writeRaw(text string) error {
	if err := os.MkdirAll(config.Dir(), 0o700); err != nil {
		return err
	}
	tmp := config.File() + ".tmp"
	if err := os.WriteFile(tmp, []byte(text), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, config.File())
}

// fromRaw reads config.toml's text as Load would.
func fromRaw(text string) (config.Config, error) {
	cfg := config.Default()
	if _, err := toml.Decode(text, &cfg); err != nil {
		return config.Config{}, fmt.Errorf("that isn't TOML conch can read: %w", err)
	}
	if cfg.Keys.Prefix == "" {
		cfg.Keys.Prefix = config.Default().Keys.Prefix
	}
	return cfg, nil
}

// fromJSON reads the form: the same tables as the file, as JSON. It goes
// through TOML so that a value of the wrong type is refused by the same
// decoder that would refuse it in the file, naming the key.
func fromJSON(data json.RawMessage) (config.Config, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return config.Config{}, fmt.Errorf("that isn't a configuration: %w", err)
	}
	if m == nil {
		return config.Config{}, errors.New("that isn't a configuration: no tables")
	}
	clean, err := tomlValues(m)
	if err != nil {
		return config.Config{}, err
	}
	var b bytes.Buffer
	if err := toml.NewEncoder(&b).Encode(clean); err != nil {
		return config.Config{}, fmt.Errorf("that isn't a configuration: %w", err)
	}
	return fromRaw(b.String())
}

// tomlValues turns JSON's values into TOML's: a number is an integer when
// it is a whole one, and a null is a key left out — which is how a field
// that may be absent (idle_stop, keep) goes back to its default.
func tomlValues(v any) (any, error) {
	switch v := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, x := range v {
			if x == nil {
				continue
			}
			c, err := tomlValues(x)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", k, err)
			}
			out[k] = c
		}
		return out, nil
	case []any:
		out := make([]any, 0, len(v))
		for i, x := range v {
			if x == nil {
				continue
			}
			c, err := tomlValues(x)
			if err != nil {
				return nil, fmt.Errorf("item %d: %w", i+1, err)
			}
			out = append(out, c)
		}
		return out, nil
	case json.Number:
		if n, err := strconv.ParseInt(string(v), 10, 64); err == nil {
			return n, nil
		}
		f, err := v.Float64()
		if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
			return nil, fmt.Errorf("%s isn't a number", v)
		}
		return f, nil
	}
	return v, nil
}

// toMap is cfg as TOML would write it, read back as plain values.
func toMap(cfg config.Config) (map[string]any, error) {
	var b bytes.Buffer
	if err := toml.NewEncoder(&b).Encode(cfg); err != nil {
		return nil, err
	}
	m := map[string]any{}
	if _, err := toml.Decode(b.String(), &m); err != nil {
		return nil, err
	}
	dropZeros(m, reflect.TypeOf(cfg))
	return m, nil
}

// dropZeros takes out the numbers the encoder wrote as 0 for a key marked
// omitempty — it leaves out only empty strings, lists and tables — since
// for those 0 is "not set" (web.port, a provider's prices), and the page
// should show the field empty with its default rather than a 0 nobody
// typed. A pointer's 0 (idle_stop) is somebody's choice and stays.
func dropZeros(m map[string]any, t reflect.Type) {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Map {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name, opts, _ := strings.Cut(f.Tag.Get("toml"), ",")
		v, ok := m[name]
		if !ok {
			continue
		}
		switch x := v.(type) {
		case map[string]any:
			if f.Type.Kind() == reflect.Map { // keyed tables: [sandbox.NAME]
				for _, sub := range x {
					if sm, ok := sub.(map[string]any); ok {
						dropZeros(sm, f.Type)
					}
				}
			} else {
				dropZeros(x, f.Type)
			}
		case []map[string]any:
			for _, sm := range x {
				dropZeros(sm, f.Type)
			}
		case int64, float64:
			if strings.Contains(opts, "omitempty") && f.Type.Kind() != reflect.Pointer && (x == int64(0) || x == float64(0)) {
				delete(m, name)
			}
		}
	}
}

// problems is what conch will pass over in cfg, as the TUI would say it.
func problems(cfg config.Config) []string {
	out := cfg.ActionProblems()
	for i, r := range cfg.Permissions {
		if err := r.Check(); err != nil {
			out = append(out, fmt.Sprintf("permission rule %d: %v", i+1, err))
		}
	}
	return out
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// Running is a server listening.
type Running struct {
	URL string
	srv *http.Server
	ln  net.Listener
}

// Listen starts s on 127.0.0.1:port (0 picks one).
func (s *Server) Listen(port int) (*Running, error) {
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: readHeaderTimeout}
	go func() { _ = srv.Serve(ln) }()
	return &Running{URL: s.URL(ln.Addr().String()), srv: srv, ln: ln}, nil
}

// Close stops it.
func (r *Running) Close() error { return r.srv.Close() }

// readHeaderTimeout keeps a connection that never finishes its request
// from being held open.
const readHeaderTimeout = 10 * time.Second

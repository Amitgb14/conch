package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// host is the Go half of the window: what the page reaches over a loopback
// WebSocket. For the spike it does two things — replays recorded frames for
// the renderer benchmark, or shows one pane of a conch server for the
// input-to-echo measurement.
type host struct {
	token    string
	frames   []json.RawMessage // bench: recorded frames
	sock     string            // live: a conch server's socket
	autotest bool
	reports  chan json.RawMessage
	addr     string // where the socket listens, once it does
}

// message is one WebSocket message either way.
type message struct {
	T     string          `json:"t"`
	Frame json.RawMessage `json:"frame,omitempty"`
	Count int             `json:"count,omitempty"`
	Pace  int             `json:"pace,omitempty"` // ms between frames; 0 floods
	Text  string          `json:"text,omitempty"`
	Cols  int             `json:"cols,omitempty"`
	Rows  int             `json:"rows,omitempty"`
	Error string          `json:"error,omitempty"`
}

func newToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // the system has no randomness; nothing is safe to serve
	}
	return hex.EncodeToString(b)
}

// loadFrames reads a recorder's output, one frame a line.
func loadFrames(path string) ([]json.RawMessage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []json.RawMessage
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		line := append([]byte(nil), sc.Bytes()...)
		if !json.Valid(line) {
			return nil, fmt.Errorf("%s:%d: not JSON", path, len(out)+1)
		}
		out = append(out, line)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: no frames", path)
	}
	return out, nil
}

// listen starts the socket on a random loopback port.
func (h *host) listen() (net.Listener, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	h.addr = ln.Addr().String()
	return ln, nil
}

// config is what the page asks for first: where the socket is, and what to
// do. The token goes with it only where config itself cannot be reached by
// another program (the webview's own scheme); a browser gets it in the
// URL's fragment instead.
func (h *host) config(withToken bool) map[string]any {
	c := map[string]any{"socket": "ws://" + h.addr + "/socket", "report": "http://" + h.addr + "/report", "autotest": h.autotest}
	if h.frames != nil {
		c["mode"] = "bench"
	} else {
		c["mode"] = "live"
	}
	if withToken {
		c["token"] = h.token
	}
	return c
}

func (h *host) authorized(r *http.Request) bool {
	got := r.URL.Query().Get("token")
	if got == "" {
		got = r.Header.Get("X-Conch-Token")
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(h.token)) == 1
}

// handler serves the socket, reports, and — given assets — the page, for a
// browser. The webview loads the page from its own scheme instead.
func (h *host) handler(assets fs.FS) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/config.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(h.config(false))
	})
	mux.HandleFunc("/socket", h.serveSocket)
	mux.HandleFunc("/report", h.serveReport)
	if assets != nil {
		mux.Handle("/", http.FileServerFS(assets))
	}
	return mux
}

// webviewHandler answers what the webview asks its own scheme for that is
// not an asset: config, with the token.
func (h *host) webviewHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/config.json" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(h.config(true))
	})
}

func (h *host) serveReport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "X-Conch-Token, Content-Type")
	if r.Method == http.MethodOptions {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if !h.authorized(r) {
		http.Error(w, "no", http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil || !json.Valid(body) {
		http.Error(w, "bad report", http.StatusBadRequest)
		return
	}
	select {
	case h.reports <- body:
	default: // nobody is waiting; the report is printed anyway
		fmt.Println(string(body))
	}
}

func (h *host) serveSocket(w http.ResponseWriter, r *http.Request) {
	if !h.authorized(r) {
		http.Error(w, "no", http.StatusUnauthorized)
		return
	}
	// The page comes from the webview's own scheme, or from this listener
	// for a browser (same origin, always allowed).
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"wails", "wails.localhost"}})
	if err != nil {
		return
	}
	defer c.CloseNow()
	c.SetReadLimit(1 << 20)
	ctx := r.Context()
	if h.frames != nil {
		h.bench(ctx, c)
	} else {
		h.live(ctx, c)
	}
}

// bench replays the frames twice: paced as the server sends them, then as
// fast as the socket takes them.
func (h *host) bench(ctx context.Context, c *websocket.Conn) {
	var start message
	if err := wsjson.Read(ctx, c, &start); err != nil || start.T != "start" {
		return
	}
	for _, pace := range []int{33, 0} {
		if wsjson.Write(ctx, c, message{T: "run", Count: len(h.frames), Pace: pace}) != nil {
			return
		}
		for _, f := range h.frames {
			if c.Write(ctx, websocket.MessageText, append(append([]byte(`{"t":"frame","frame":`), f...), '}')) != nil {
				return
			}
			if pace > 0 {
				time.Sleep(time.Duration(pace) * time.Millisecond)
			}
		}
		if wsjson.Write(ctx, c, message{T: "end"}) != nil {
			return
		}
		var ack message
		if wsjson.Read(ctx, c, &ack) != nil {
			return
		}
	}
}

// live shows one pane running cat on the conch server: the page sizes it,
// types into it, and is sent its frames.
func (h *host) live(ctx context.Context, c *websocket.Conn) {
	cl, err := client.Dial(h.sock, "conch-desktop-spike")
	if err != nil {
		_ = wsjson.Write(ctx, c, message{T: "error", Error: err.Error()})
		return
	}
	defer cl.Close()
	var (
		mu sync.Mutex
		id string
	)
	go func() {
		for msg := range cl.Events {
			if msg.Event != proto.EventPaneFrame {
				continue
			}
			mu.Lock()
			mine := id
			mu.Unlock()
			var f struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(msg.Data, &f) != nil || f.ID != mine {
				continue
			}
			if c.Write(ctx, websocket.MessageText, append(append([]byte(`{"t":"frame","frame":`), msg.Data...), '}')) != nil {
				return
			}
		}
	}()
	call := func(method string, params, out any) error {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return cl.Call(cctx, method, params, out)
	}
	for {
		var m message
		if err := wsjson.Read(ctx, c, &m); err != nil {
			mu.Lock()
			if id != "" {
				_ = call(proto.MethodPaneClose, proto.PaneRef{ID: id}, nil)
			}
			mu.Unlock()
			return
		}
		mu.Lock()
		cur := id
		mu.Unlock()
		switch m.T {
		case "resize":
			if cur == "" {
				var info proto.PaneInfo
				err := call(proto.MethodPaneCreate, proto.PaneCreateParams{Name: "spike", Command: []string{"/bin/cat"}, Cols: m.Cols, Rows: m.Rows, NoProject: true}, &info)
				if err == nil {
					mu.Lock()
					id = info.ID
					mu.Unlock()
					err = call(proto.MethodPaneSubscribe, proto.PaneRef{ID: info.ID}, nil)
				}
				if err != nil {
					_ = wsjson.Write(ctx, c, message{T: "error", Error: err.Error()})
					return
				}
				continue
			}
			_ = call(proto.MethodPaneResize, proto.PaneResizeParams{ID: cur, Cols: m.Cols, Rows: m.Rows}, nil)
		case "text":
			if cur != "" {
				cl.Notify(proto.MethodPaneSendText, proto.PaneSendTextParams{ID: cur, Text: m.Text})
			}
		}
	}
}

// serve runs the handler until the listener closes.
func serve(ln net.Listener, h http.Handler) {
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
		fmt.Fprintln(os.Stderr, "serve:", err)
	}
}

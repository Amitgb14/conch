package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// `conch mcp -http ADDRESS` is the same tools over MCP's Streamable HTTP,
// for an agent that is not a process conch started: one on another
// computer, in a container, in a tool that only speaks HTTP. Nothing gets
// in without an access token (conch token create), sent as
// `Authorization: Bearer …`: this door never acts as the person, which is
// what a process on this computer's own socket does. Each token has a
// connection to conch of its own, holding that token, so the server — not
// this process — decides what each may do (#350).
//
// It answers each POST with one JSON reply; there is no stream to open,
// as no tool sends anything before its answer. It is plain HTTP: put it on
// 127.0.0.1, or behind something that adds TLS, such as tailscale serve.

// mcpHTTPMaxTokens bounds the connections kept, one per token in use.
const mcpHTTPMaxTokens = 64

type mcpHTTP struct {
	// dial makes a connection to conch holding token; tests replace it.
	dial func(token string) (*client.Client, error)

	mu       sync.Mutex
	sessions map[[32]byte]*mcpSession
}

func newMCPHTTP(dial func(token string) (*client.Client, error)) *mcpHTTP {
	return &mcpHTTP{dial: dial, sessions: map[[32]byte]*mcpSession{}}
}

func runMCPHTTP(addr string) error {
	// Only the tokens sent count: one this process inherited (it was
	// started in a pane) would be presented first on every connection.
	_ = os.Unsetenv(proto.TokenEnv)
	h := newMCPHTTP(func(token string) (*client.Client, error) {
		c, err := connect(true)
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := c.Present(ctx, token); err != nil {
			c.Close()
			return nil, err
		}
		return c, nil
	})
	defer h.close()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "conch mcp: MCP over HTTP at http://%s/mcp; each request needs Authorization: Bearer TOKEN (conch token create)\n", ln.Addr())
	if host, _, _ := net.SplitHostPort(ln.Addr().String()); !isLoopback(host) {
		fmt.Fprintln(os.Stderr, "conch mcp: this is plain HTTP on more than this computer; tokens cross the network as they are — put TLS in front of it")
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	return srv.Serve(ln)
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (h *mcpHTTP) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for k, s := range h.sessions {
		s.close()
		delete(h.sessions, k)
	}
}

// session is the token's own session, made and checked the first time it
// is used. A token conch refuses is never kept.
func (h *mcpHTTP) session(token string) (*mcpSession, error) {
	key := sha256.Sum256([]byte(token))
	h.mu.Lock()
	s, ok := h.sessions[key]
	h.mu.Unlock()
	if ok {
		return s, nil
	}
	c, err := h.dial(token)
	if err != nil {
		return nil, err
	}
	s = &mcpSession{err: os.Stderr, c: c, dial: func() (*client.Client, error) { return h.dial(token) }}
	h.mu.Lock()
	defer h.mu.Unlock()
	if old, ok := h.sessions[key]; ok { // another request got there first
		s.close()
		return old, nil
	}
	if len(h.sessions) >= mcpHTTPMaxTokens {
		for k, old := range h.sessions { // any one: they are made again when used
			old.close()
			delete(h.sessions, k)
			break
		}
	}
	h.sessions[key] = s
	return s, nil
}

func (h *mcpHTTP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/mcp" && r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	// A page in a browser can send here too; the spec has servers refuse
	// another site's. The token would stop it anyway, but a page must
	// not even learn whether one is right.
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		if err != nil || !isLoopback(u.Hostname()) {
			http.Error(w, "conch mcp answers no web page's requests", http.StatusForbidden)
			return
		}
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "conch mcp takes POST; it opens no stream", http.StatusMethodNotAllowed)
		return
	}
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	token = strings.TrimSpace(token)
	if !ok || token == "" {
		unauthorized(w, "an access token is needed: Authorization: Bearer TOKEN (conch token create makes one)")
		return
	}
	s, err := h.session(token)
	if err != nil {
		var perr *proto.Error
		if errors.As(err, &perr) && perr.Code == proto.ErrUnauthorized {
			unauthorized(w, perr.Message)
			return
		}
		http.Error(w, "conch is not answering: "+err.Error(), http.StatusBadGateway)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, mcpMaxLine+1))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if len(body) > mcpMaxLine {
		http.Error(w, fmt.Sprintf("a message over %d bytes", mcpMaxLine), http.StatusRequestEntityTooLarge)
		return
	}
	var res mcpResponse
	var req mcpRequest
	trimmed := strings.TrimSpace(string(body))
	switch {
	case strings.HasPrefix(trimmed, "["):
		res = mcpResponse{Error: &mcpError{mcpInvalidRequest, "one message a request; this revision of MCP has no batches"}}
	case json.Unmarshal(body, &req) != nil:
		res = mcpResponse{Error: &mcpError{mcpParseError, "that is not JSON"}}
	default:
		var answered bool
		if res, answered = s.answer(req); !answered {
			w.WriteHeader(http.StatusAccepted) // a notification: nothing to say
			return
		}
	}
	res.JSONRPC = "2.0"
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}

func unauthorized(w http.ResponseWriter, why string) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="conch"`)
	http.Error(w, why, http.StatusUnauthorized)
}

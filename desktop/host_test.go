package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func TestLoadFrames(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	frames, err := loadFrames(write("ok.jsonl", `{"id":"p1","cols":2,"rows":1,"lines":["ab"]}`+"\n"+`{"id":"p1","cols":2,"rows":1,"lines":["cd"]}`+"\n"))
	if err != nil || len(frames) != 2 {
		t.Fatalf("got %d frames, %v", len(frames), err)
	}
	// The last line need not end in a newline.
	if frames, err := loadFrames(write("nonl.jsonl", `{"id":"p1"}`)); err != nil || len(frames) != 1 {
		t.Fatalf("no trailing newline: %d, %v", len(frames), err)
	}
	for name, body := range map[string]string{
		"empty.jsonl":     "",
		"truncated.jsonl": `{"id":"p1"}` + "\n" + `{"id":"p`,
		"garbage.jsonl":   "not json\n",
	} {
		if _, err := loadFrames(write(name, body)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if _, err := loadFrames(filepath.Join(dir, "missing.jsonl")); err == nil {
		t.Error("missing file: no error")
	}
}

func TestTokensDiffer(t *testing.T) {
	a, b := newToken(), newToken()
	if a == b || len(a) != 64 {
		t.Fatalf("tokens %q %q", a, b)
	}
}

// benchHost is a host replaying two tiny frames, served by httptest.
func benchHost(t *testing.T) (*host, *httptest.Server) {
	t.Helper()
	h := &host{token: newToken(), reports: make(chan json.RawMessage, 1), frames: []json.RawMessage{
		json.RawMessage(`{"id":"p1","cols":2,"rows":1,"lines":["ab"]}`),
		json.RawMessage(`{"id":"p1","cols":2,"rows":1,"lines":["cd"]}`),
	}}
	srv := httptest.NewServer(h.handler(nil))
	t.Cleanup(srv.Close)
	h.addr = strings.TrimPrefix(srv.URL, "http://")
	return h, srv
}

func TestSocketRefusesWithoutToken(t *testing.T) {
	h, srv := benchHost(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws := "ws" + strings.TrimPrefix(srv.URL, "http") + "/socket"
	for _, tok := range []string{"", "wrong", h.token[:63]} {
		_, resp, err := websocket.Dial(ctx, ws+"?token="+tok, nil)
		if err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("token %q: err %v, resp %v", tok, err, resp)
		}
	}
}

func TestSocketRefusesForeignOrigin(t *testing.T) {
	h, srv := benchHost(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws := "ws" + strings.TrimPrefix(srv.URL, "http") + "/socket?token=" + h.token
	_, _, err := websocket.Dial(ctx, ws, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {"https://evil.example"}}})
	if err == nil {
		t.Fatal("a page from another origin was let in")
	}
	c, _, err := websocket.Dial(ctx, ws, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {"wails://wails"}}})
	if err != nil {
		t.Fatalf("the webview's origin: %v", err)
	}
	c.CloseNow()
}

func TestBenchReplaysPacedThenFlooded(t *testing.T) {
	h, srv := benchHost(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/socket?token="+h.token, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	if err := wsjson.Write(ctx, c, message{T: "start"}); err != nil {
		t.Fatal(err)
	}
	var paces []int
	for len(paces) < 2 {
		var m message
		if err := wsjson.Read(ctx, c, &m); err != nil {
			t.Fatal(err)
		}
		if m.T != "run" || m.Count != 2 {
			t.Fatalf("want run of 2, got %+v", m)
		}
		paces = append(paces, m.Pace)
		for i := 0; i < 2; i++ {
			if err := wsjson.Read(ctx, c, &m); err != nil || m.T != "frame" || !json.Valid(m.Frame) {
				t.Fatalf("frame %d: %+v %v", i, m, err)
			}
		}
		if err := wsjson.Read(ctx, c, &m); err != nil || m.T != "end" {
			t.Fatalf("want end, got %+v %v", m, err)
		}
		if err := wsjson.Write(ctx, c, message{T: "ack"}); err != nil {
			t.Fatal(err)
		}
	}
	if paces[0] == 0 || paces[1] != 0 {
		t.Fatalf("paces %v: want paced, then flood", paces)
	}
}

func TestReport(t *testing.T) {
	h, srv := benchHost(t)
	post := func(tok, body string) int {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/report", strings.NewReader(body))
		req.Header.Set("X-Conch-Token", tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if got := post("wrong", `{}`); got != http.StatusUnauthorized {
		t.Errorf("wrong token: %d", got)
	}
	if got := post(h.token, `{"mode":`); got != http.StatusBadRequest {
		t.Errorf("truncated report: %d", got)
	}
	if got := post(h.token, `{"mode":"bench"}`); got != http.StatusOK {
		t.Fatalf("report: %d", got)
	}
	if got := string(<-h.reports); got != `{"mode":"bench"}` {
		t.Errorf("report %q", got)
	}
	resp, err := http.Get(srv.URL + "/report")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d", resp.StatusCode)
	}
}

func TestConfigKeepsTheTokenOffTheListener(t *testing.T) {
	h, srv := benchHost(t)
	resp, err := http.Get(srv.URL + "/config.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var c map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&c); err != nil {
		t.Fatal(err)
	}
	if _, ok := c["token"]; ok {
		t.Fatal("the loopback listener handed out the token")
	}
	if c["mode"] != "bench" || c["socket"] != "ws://"+h.addr+"/socket" {
		t.Fatalf("config %v", c)
	}
	rec := httptest.NewRecorder()
	h.webviewHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/config.json", nil))
	if !strings.Contains(rec.Body.String(), h.token) {
		t.Fatal("the webview was not given the token")
	}
	rec = httptest.NewRecorder()
	h.webviewHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/other", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("other path: %d", rec.Code)
	}
}

// TestUILogic runs the page's plain functions with node, as the phone's
// TestUILogic does. Skipped without node.
func TestUILogic(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node")
	}
	out, err := exec.Command(node, "--test", "ui/term.test.mjs").CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

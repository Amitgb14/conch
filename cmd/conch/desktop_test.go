package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/Amitgb14/conch/internal/config"
)

// conch desktop -browser serves the page and its socket on loopback, and
// prints the address with the token; the page reaches this computer's
// server through it.
func TestDesktopBrowser(t *testing.T) {
	a4Env(t)
	startA4Server(t, config.SocketPath())
	exe, _ := os.Executable()
	cmd := a4Command(exe, "desktop", "-browser", "-no-open")
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Signal(syscall.SIGTERM); cmd.Wait() }()
	lines := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			if after, ok := strings.CutPrefix(sc.Text(), "conch desktop: "); ok {
				lines <- after
				return
			}
		}
	}()
	var url string
	select {
	case url = <-lines:
	case <-time.After(15 * time.Second):
		t.Fatal("conch desktop printed no address")
	}
	base, token, ok := strings.Cut(url, "/#token=")
	if !ok || !strings.HasPrefix(base, "http://127.0.0.1:") || len(token) != 64 {
		t.Fatalf("address %q", url)
	}

	resp, err := http.Get(base + "/")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(page), "app.mjs") {
		t.Fatalf("page %.100s", page)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ws := "ws" + strings.TrimPrefix(base, "http") + "/socket?token="
	if _, _, err := websocket.Dial(ctx, ws+"wrong", nil); err == nil {
		t.Fatal("a wrong token was let in")
	}
	c, _, err := websocket.Dial(ctx, ws+token, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	var hello struct {
		T        string `json:"t"`
		Machines []struct{ ID, State string }
	}
	if err := wsjson.Read(ctx, c, &hello); err != nil || hello.T != "hello" || len(hello.Machines) == 0 ||
		hello.Machines[0].ID != "local" || hello.Machines[0].State != "online" {
		t.Fatalf("hello %+v %v", hello, err)
	}
	if err := wsjson.Write(ctx, c, map[string]any{"t": "call", "id": "1", "method": "pane.list"}); err != nil {
		t.Fatal(err)
	}
	var res struct {
		T      string          `json:"t"`
		ID     string          `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  any             `json:"error"`
	}
	for res.ID != "1" {
		if err := wsjson.Read(ctx, c, &res); err != nil {
			t.Fatal(err)
		}
	}
	// The test server answers every call with {}: what matters here is
	// that the call went through to it and its answer came back.
	if res.Error != nil || string(res.Result) != "{}" {
		t.Fatalf("pane.list %+v", res)
	}

	// It stops on a signal, cleanly.
	cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("exit: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("conch desktop did not stop")
	}
}

func TestDesktopRefuses(t *testing.T) {
	a4Env(t)
	exe, _ := os.Executable()
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, "desktop app"},
		{[]string{"-browser", "-listen", "0.0.0.0:0"}, "only a loopback address"},
		{[]string{"-browser", "-listen", "192.168.1.5:8000"}, "only a loopback address"},
		{[]string{"-browser", "-listen", "nonsense"}, "-listen"},
		{[]string{"-browser", "extra"}, "unexpected"},
	} {
		code, _, stderr := a4RunBinary(t, exe, "", append([]string{"desktop"}, tc.args...)...)
		if code == 0 || !strings.Contains(stderr, tc.want) {
			t.Errorf("%v: exit %d, %q", tc.args, code, stderr)
		}
	}
}

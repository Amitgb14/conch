package main

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/desktop"
	"github.com/Amitgb14/conch/internal/server"
)

func TestDesktopArgs(t *testing.T) {
	a4Env(t)
	for _, args := range [][]string{{"extra"}, {"-every", "0s"}, {"-every", "-1s"}, {"-nope"}} {
		if err := runDesktopOn(args, strings.NewReader(""), io.Discard); err == nil {
			t.Errorf("%q was taken", args)
		}
	}
}

// TestDesktopOverARealServer runs the command as an app would: hello in,
// hello and the snapshot of this computer out, and the bridge ending when
// the app closes its end.
func TestDesktopOverARealServer(t *testing.T) {
	dir := a4Env(t)
	srv := server.New(config.SocketPath(), dir)
	go srv.Run()
	t.Cleanup(srv.StopAndWait)
	for i := 0; ; i++ {
		if c, err := client.Dial(config.SocketPath(), "test"); err == nil {
			c.Close()
			break
		}
		if i == 200 {
			t.Fatal("server did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- runDesktopOn([]string{"-every", "50ms"}, inR, outW)
		outW.Close()
	}()
	if _, err := inW.Write([]byte(`{"type":"hello","data":{"app":"test"}}` + "\n")); err != nil {
		t.Fatal(err)
	}
	lines := bufio.NewReader(outR)
	read := func() desktop.Message {
		t.Helper()
		got := make(chan desktop.Message, 1)
		go func() {
			line, err := lines.ReadBytes('\n')
			var m desktop.Message
			if err == nil {
				err = json.Unmarshal(line, &m)
			}
			if err != nil {
				m.Error = "read: " + err.Error()
			}
			got <- m
		}()
		select {
		case m := <-got:
			return m
		case <-time.After(15 * time.Second):
			t.Fatal("timed out reading conch desktop")
		}
		return desktop.Message{}
	}
	if m := read(); m.Type != desktop.TypeHello {
		t.Fatalf("first: %+v", m)
	}
	m := read()
	var st desktop.State
	if err := json.Unmarshal(m.Data, &st); err != nil || m.Type != desktop.TypeSnapshot {
		t.Fatalf("second: %+v %v", m, err)
	}
	if len(st.Machines) == 0 || st.Machines[0].ID != "local" || st.Machines[0].State != "online" {
		t.Errorf("snapshot: %+v", st.Machines)
	}

	inW.Close()
	go io.Copy(io.Discard, outR)
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("an app closing its end is not an error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("conch desktop did not end with its input")
	}
	if _, err := os.Stat(config.SocketPath()); err != nil {
		t.Errorf("the server went with the bridge: %v", err)
	}
}

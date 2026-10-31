package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/configweb"
)

func TestConfigPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CONCH_HOME", dir)
	code, out, _ := a4RunMain(t, "", "config", "path")
	if code != 0 || strings.TrimSpace(out) != dir+"/config.toml" {
		t.Fatalf("code %d out %q", code, out)
	}
}

func TestConfigUsage(t *testing.T) {
	t.Setenv("CONCH_HOME", t.TempDir())
	for _, args := range [][]string{{}, {"nope"}, {"path", "x"}, {"web", "extra"}, {"web", "-port", "70000"}, {"web", "-port", "-1"}} {
		if err := runConfig(args); err == nil || !strings.Contains(err.Error(), "conch config") && !strings.Contains(err.Error(), "unknown") {
			t.Errorf("%v: %v", args, err)
		}
	}
}

// conch config web serves the page with its key, opens it, and stops when
// interrupted.
func TestConfigWeb(t *testing.T) {
	t.Setenv("CONCH_HOME", t.TempDir())
	var opened string
	oldOpen, oldWait := openInBrowser, configWait
	openInBrowser = func(url string) error { opened = url; return nil }
	var served string
	configWait = func(_ context.Context, url string) {
		base, token, _ := strings.Cut(url, "#t=")
		req, _ := http.NewRequest(http.MethodGet, strings.TrimSuffix(base, "/")+"/api/config", nil)
		req.Header.Set(configweb.TokenHeader, token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Error(err)
			return
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		served = string(body)
	}
	t.Cleanup(func() { openInBrowser, configWait = oldOpen, oldWait })

	if err := runConfig([]string{"web"}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(opened, "http://127.0.0.1:") || !strings.Contains(opened, "#t=") {
		t.Fatalf("opened %q", opened)
	}
	if !strings.Contains(served, config.File()) || !strings.Contains(served, `"schema"`) {
		t.Fatalf("served %.300s", served)
	}
	base, _, _ := strings.Cut(opened, "#")
	if res, err := http.Get(base); err == nil {
		res.Body.Close()
		t.Fatal("still serving after it returned")
	}

	// -no-open opens nothing; a browser that can't be opened is said, not fatal.
	opened = ""
	if err := runConfig([]string{"web", "-no-open"}); err != nil || opened != "" {
		t.Fatalf("err %v opened %q", err, opened)
	}
	openInBrowser = func(string) error { return errors.New("no display") }
	if err := runConfig([]string{"web"}); err != nil {
		t.Fatal(err)
	}
}

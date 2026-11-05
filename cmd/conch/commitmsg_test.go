package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommitMessage(t *testing.T) {
	dir := a4Env(t)
	var got string
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 1<<16)
		n, _ := r.Body.Read(b)
		got = string(b[:n])
		w.Write([]byte(`{"choices":[{"message":{"content":"{\"subject\":\"Check db and redis together\",\"body\":\"They waited in turn.\"}"}}]}`))
	}))
	defer llm.Close()
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte(fmt.Sprintf("[brain]\nprovider = \"openai\"\nmodel = \"test\"\nbase_url = %q\n", llm.URL+"/v1")), 0o600)
	t.Setenv("OPENAI_API_KEY", "")
	old := stdinIn
	defer func() { stdinIn = old }()
	stdinIn = strings.NewReader("diff --git a/h.go b/h.go\n+\tg.Wait()\n")
	out, _ := a4Capture(t, "", func() {
		if err := runCommitMessage([]string{"-context", "make healthz concurrent"}); err != nil {
			t.Fatal(err)
		}
	})
	if strings.TrimSpace(out) != "Check db and redis together\n\nThey waited in turn." {
		t.Fatalf("printed %q", out)
	}
	if !strings.Contains(got, "g.Wait()") || !strings.Contains(got, "make healthz concurrent") {
		t.Fatalf("the model was not given the diff and context: %s", got)
	}

	stdinIn = strings.NewReader("")
	if err := runCommitMessage(nil); err == nil || !strings.Contains(err.Error(), "nothing staged") {
		t.Fatalf("empty diff: %v", err)
	}
	if err := runCommitMessage([]string{"extra"}); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("stray argument: %v", err)
	}
}

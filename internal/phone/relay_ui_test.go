package phone

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/nodetest"
	"github.com/Amitgb14/conch/internal/relay"
	"github.com/coder/websocket"
)

// The phone's app speaks the relay's tunnel in JavaScript (ui/tunnel.mjs)
// and the gateway in Go (internal/relay): two implementations of one
// protocol, so they are run against each other, through a real relay.
func TestTunnelJSAgainstGo(t *testing.T) {
	node := nodetest.Path(t)
	const big = 300000
	mux := http.NewServeMux()
	mux.HandleFunc("/api/echo", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "tok"})
		_ = json.NewEncoder(w).Encode(map[string]string{"body": string(b), "cookie": r.Header.Get("Cookie")})
	})
	mux.HandleFunc("/api/big", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("y", big))
	})
	mux.HandleFunc("/api/socket", func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		c.SetReadLimit(1 << 20)
		for {
			_, m, err := c.Read(r.Context())
			if err != nil {
				return
			}
			if string(m) == "bye" {
				c.Close(websocket.StatusNormalClosure, "")
				return
			}
			_ = c.Write(r.Context(), websocket.MessageText, append([]byte("echo:"), m...))
		}
	})
	ts := httptest.NewServer(&relay.Server{})
	defer ts.Close()
	secret, _ := relay.NewSecret()
	key, _ := relay.NewKey()
	l := &relay.Link{Relay: ts.URL, Secret: secret, Key: key, Handler: mux, Logf: t.Logf}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = l.Run(ctx) }()
	for deadline := time.Now().Add(10 * time.Second); !l.Status().Connected; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the gateway never connected")
		}
	}
	other, _ := relay.NewKey()
	for _, extra := range [][]string{nil, {"WRONG=" + relay.EncodeKey(other.Public)}} {
		cmd := exec.Command(node, "uitest/tunnel.e2e.mjs")
		cmd.Env = append(os.Environ(), "NODE_OPTIONS=", "NO_COLOR=1", "RELAY="+ts.URL, "ROOM="+l.Room(),
			"KEY="+relay.EncodeKey(key.Public), "FP="+relay.Fingerprint(key.Public), "BIG="+strconv.Itoa(big))
		cmd.Env = append(cmd.Env, extra...)
		out, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(out), "ok") {
			t.Fatalf("node %v: %v\n%s", extra, err, out)
		}
	}
}

// The phone says a key's fingerprint in the same words as the computer.
func TestFingerprintWordsAgree(t *testing.T) {
	js, _ := uiFiles.ReadFile("ui/tunnel.mjs")
	m := regexp.MustCompile(`(?s)export const WORDS = \[(.*?)\n\]`).FindSubmatch(js)
	if m == nil {
		t.Fatal("no WORDS in tunnel.mjs")
	}
	var words []string
	for _, w := range regexp.MustCompile(`"([a-z]+)"`).FindAllSubmatch(m[1], -1) {
		words = append(words, string(w[1]))
	}
	if strings.Join(words, " ") != strings.Join(relay.Words(), " ") {
		t.Fatal("ui/tunnel.mjs and internal/relay/words.go have different word lists")
	}
}

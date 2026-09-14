package proto

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestSessionSearchAndShareWire(t *testing.T) {
	for _, v := range []any{
		SessionSearchParams{ProjectID: "r1", Query: "race", Limit: 5},
		SessionShareParams{Agent: "claude", ID: "c1", Dir: "/src", To: "codex", Cols: 80, Rows: 24},
		SessionShareParams{Agent: "codex", ID: "x1", Dir: "/src", PaneID: "p3"},
		SessionShareResult{Pane: PaneInfo{ID: "p3", State: PaneRunning}, Path: "/src/.conch/handoff/codex-x1.md"},
		SessionInfo{Agent: "claude", ID: "c1", Dir: "/src", Title: "t", Snippet: "…race…"},
	} {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		back := reflect.New(reflect.TypeOf(v))
		if err := json.Unmarshal(b, back.Interface()); err != nil || !reflect.DeepEqual(back.Elem().Interface(), v) {
			t.Fatalf("%T round trip: %s %v", v, b, err)
		}
	}
	b, _ := json.Marshal(SessionInfo{Agent: "claude"})
	if strings.Contains(string(b), "snippet") {
		t.Fatalf("empty snippet is sent: %s", b)
	}
	b, _ = json.Marshal(SessionShareParams{Agent: "a", ID: "i", Dir: "d", To: "codex"})
	if strings.Contains(string(b), "pane_id") || !strings.Contains(string(b), `"to":"codex"`) {
		t.Fatalf("share params: %s", b)
	}
	for _, c := range []string{"session.search.v1", "session.share.v1"} {
		if !slices.Contains(Capabilities, c) {
			t.Errorf("capability %s not advertised", c)
		}
	}
}

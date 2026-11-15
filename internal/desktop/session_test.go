package desktop

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// testdata/session.jsonl is a recorded session between an app and the
// bridge, and testdata/final.json the state an app holds at its end. The
// Linux and Windows apps replay the same two files as their fake bridge,
// in Rust and C#, so this test is what keeps them telling the truth: every
// line must be exactly what these types write, and applying the session
// must give exactly the final state.

// line is one line of the recording.
type line struct {
	From    string  `json:"from"` // app or bridge
	Message Message `json:"message"`
}

// apply is how an app keeps its state: a snapshot replaces it; a patch
// replaces each item it sets where it already is, adds a new one at the
// end, and drops the keys it removes.
func apply(s State, p Patch) State {
	if p.Machines != nil {
		s.Machines = applyList(s.Machines, p.Machines, func(m Machine) string { return m.ID })
	}
	if p.Projects != nil {
		s.Projects = applyList(s.Projects, p.Projects, func(x Project) string { return x.Key })
	}
	if p.Panes != nil {
		s.Panes = applyList(s.Panes, p.Panes, func(x Pane) string { return x.Key })
	}
	return s
}

func applyList[T any](list []T, c *Changes[T], key func(T) string) []T {
	out := append([]T{}, list...)
	for _, x := range c.Set {
		i := 0
		for i < len(out) && key(out[i]) != key(x) {
			i++
		}
		if i < len(out) {
			out[i] = x
		} else {
			out = append(out, x)
		}
	}
	gone := map[string]bool{}
	for _, k := range c.Remove {
		gone[k] = true
	}
	kept := out[:0]
	for _, x := range out {
		if !gone[key(x)] {
			kept = append(kept, x)
		}
	}
	return kept
}

// strict decodes data into v refusing unknown fields, and says whether
// writing v back gives the same bytes: a field the recording has that the
// types do not, or spells differently, fails here.
func strict(t *testing.T, n int, data json.RawMessage, v any) {
	t.Helper()
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		t.Fatalf("line %d: %v in %s", n, err, data)
	}
	again, _ := json.Marshal(reflect.ValueOf(v).Elem().Interface())
	var want bytes.Buffer
	_ = json.Compact(&want, data)
	if !bytes.Equal(again, want.Bytes()) {
		t.Errorf("line %d is not what the bridge writes:\n got  %s\n want %s", n, want.Bytes(), again)
	}
}

func TestRecordedSession(t *testing.T) {
	f, err := os.Open("testdata/session.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var state State
	seq := 0
	intents := map[string]string{} // id → intent
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		var l line
		d := json.NewDecoder(bytes.NewReader(sc.Bytes()))
		d.DisallowUnknownFields()
		if err := d.Decode(&l); err != nil {
			t.Fatalf("line %d: %v", n, err)
		}
		m := l.Message
		if again, _ := json.Marshal(m); !bytes.Contains(sc.Bytes(), again) {
			t.Errorf("line %d's envelope is not what the bridge writes: %s", n, again)
		}
		switch {
		case l.From == "app" && m.Type == TypeHello:
			strict(t, n, m.Data, &AppHello{})
		case l.From == "app" && m.Type == TypeIntent:
			intents[m.ID] = m.Intent
			switch m.Intent {
			case IntentPaneNew:
				strict(t, n, m.Data, &PaneNew{})
			case IntentPaneClose, IntentPaneAttach:
				strict(t, n, m.Data, &PaneRef{})
			default:
				t.Errorf("line %d: intent %q is not one the bridge takes", n, m.Intent)
			}
		case l.From == "bridge" && m.Type == TypeHello:
			strict(t, n, m.Data, &BridgeHello{})
		case l.From == "bridge" && (m.Type == TypeSnapshot || m.Type == TypePatch):
			if m.Seq != seq+1 {
				t.Errorf("line %d: seq %d after %d", n, m.Seq, seq)
			}
			seq = m.Seq
			if m.Type == TypeSnapshot {
				state = State{}
				strict(t, n, m.Data, &state)
				continue
			}
			var p Patch
			strict(t, n, m.Data, &p)
			state = apply(state, p)
		case l.From == "bridge" && m.Type == TypeNotify:
			strict(t, n, m.Data, &Notify{})
		case l.From == "bridge" && m.Type == TypeResult:
			intent, ok := intents[m.ID]
			if !ok {
				t.Errorf("line %d: a result for %q, which no intent had", n, m.ID)
			}
			if m.OK == (m.Error != "") {
				t.Errorf("line %d: a result is ok or has an error, not both or neither", n)
			}
			switch {
			case m.Data == nil:
			case intent == IntentPaneNew:
				strict(t, n, m.Data, &PaneRef{})
			case intent == IntentPaneAttach:
				strict(t, n, m.Data, &Attach{})
			default:
				t.Errorf("line %d: %s answers with no data", n, intent)
			}
		default:
			t.Errorf("line %d: %s does not send %q", n, l.From, m.Type)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile("testdata/final.json")
	if err != nil {
		t.Fatal(err)
	}
	var want State
	strict(t, 0, raw, &want)
	if !reflect.DeepEqual(state, want) {
		got, _ := json.MarshalIndent(state, "", "  ")
		t.Errorf("the session ends in\n%s\nnot testdata/final.json", got)
	}
}

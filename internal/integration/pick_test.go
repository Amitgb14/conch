package integration

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

func decode(t *testing.T, s string) any {
	t.Helper()
	var v any
	d := json.NewDecoder(bytes.NewReader([]byte(s)))
	d.UseNumber()
	if err := d.Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

const pickDoc = `{
  "items": [
    {"id": "p1", "number": 12, "status": "running", "vcs": {"revision": "aaa"}},
    {"id": "p2", "number": 11, "status": "failed", "vcs": {"revision": "bbb"}, "ok": false},
    {"id": "p3", "number": 10, "status": "failed", "vcs": {"revision": "ccc"}}
  ],
  "steps": [
    {"name": "checkout", "actions": [{"status": "success", "output_url": "u0"}]},
    {"name": "test", "actions": [{"status": "failed", "output_url": "u1"}, {"status": "failed", "output_url": "u2"}]},
    {"name": "lint", "actions": [{"status": "failed"}, {"status": "failed", "output_url": "u3"}]}
  ],
  "empty": [],
  "one": [{"id": "only"}],
  "v.2": {"x": "dotted"}
}`

func TestPick(t *testing.T) {
	doc := decode(t, pickDoc)
	fill := func(s string) string {
		return map[string]string{"{head}": "bbb", "{none}": "", "{dotted}": "a.b"}[s]
	}
	for path, want := range map[string]any{
		"items.0.id":                      "p1",
		"items.2.vcs.revision":            "ccc",
		"items.9.id":                      nil,
		"items.-1.id":                     nil,
		"items.x":                         nil,
		"nope":                            nil,
		"":                                nil,
		"items[status=failed].id":         "p2",
		"items[status=passed].id":         nil,
		"items[number=11].id":             "p2", // a number read as text
		"items[ok=false].id":              "p2", // a bool, too
		"items[vcs/revision={head}].id":   "p2", // a key reaching in, a value filled
		"items[vcs/revision={none}].id":   "p1", // an empty value matches all
		"items[vcs/revision={dotted}].id": nil,  // a dot in a filled value is still a value
		"items[*status=failed].id":        []any{"p2", "p3"},
		"items[*].number":                 []any{json.Number("12"), json.Number("11"), json.Number("10")},
		"items[*status=gone].id":          nil,
		"steps[*].actions[*status=failed].output_url": []any{"u1", "u2", "u3"},
		"steps[name=test].actions.1.output_url":       "u2",
		"empty[*]":                                    nil,
		"empty[status=failed]":                        nil,
		"one[*].id":                                   []any{"only"},
		"one.0.id":                                    "only",
		"items[status]":                               nil, // no =
		"items[=x]":                                   nil, // no key
		"id[*]":                                       nil, // not a list
	} {
		got := Pick(doc, path, fill)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%q: %#v, want %#v", path, got, want)
		}
	}
	list := decode(t, `[{"id": 1, "status": "failed"}, {"id": 2, "status": "success"}]`)
	if got := Pick(list, "[*]", nil); len(got.([]any)) != 2 {
		t.Errorf("[*] of a list: %#v", got)
	}
	if got := Pick(list, "[status=failed].id", nil); got != json.Number("1") {
		t.Errorf("[k=v] of a list: %#v", got)
	}
	if got := Pick(list, "0", nil); got == nil {
		t.Error("an index of a list")
	}
	if got := Pick(nil, "a", nil); got != nil {
		t.Errorf("nil: %#v", got)
	}
}

func TestText(t *testing.T) {
	for v, want := range map[any]string{
		"x": "x", json.Number("12"): "12", true: "true", nil: "", 1.5: "1.5",
	} {
		if got := Text(v); got != want {
			t.Errorf("%#v: %q, want %q", v, got, want)
		}
	}
}

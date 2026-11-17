package integration

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

// An index as the registry builds it since its PR #10: capabilities on
// every entry but the built-ins, and a provider with no [ssh] in disabled.
const capsIndex = `{"format": 2,
 "entries": [
  {"kind": "sandbox", "name": "instabox", "label": "InstaBox", "version": 1, "format": 3, "file": "sandbox/instabox/provider.toml", "sha256": "x",
   "capabilities": {"operations": ["create","get","list","start","stop","delete"],
     "ssh": {"key":"none","access":true,"port":"reply"},
     "create": ["auto_stop_secs","cpu","env","memory_mb","name","snapshot"],
     "settings": [], "size": null, "lifetime": false,
     "states": ["error","gone","started","starting","stopped","stopping"],
     "list": {"paged":true,"all_mine":false}, "auth": {"type":"header","header":"X-API-Key"}, "needs": ["key"]}},
  {"kind": "ci", "name": "circleci", "label": "CircleCI", "version": 1, "format": 3, "file": "ci/circleci/integration.toml", "sha256": "y",
   "operations": ["runs","failed_log","rerun"],
   "capabilities": {"operations":["runs","failed_log","rerun"], "auth":{"type":"header","header":"Circle-Token"}, "needs":["key"],
     "detect":["checks","files"], "states":["canceled","fail","pass","pending"], "follow":true}},
  {"kind": "ci", "name": "jenkins", "label": "Jenkins", "version": 1, "format": 3, "file": "ci/jenkins/integration.toml", "sha256": "z",
   "capabilities": {"operations":["runs","failed_log"], "auth":{"type":"basic"}, "needs":["base_url","key","user"]}},
  {"kind": "tickets", "name": "linear", "label": "Linear", "builtin": true}
 ],
 "disabled": [
  {"kind": "sandbox", "name": "archil", "label": "Archil", "version": 1, "format": 3, "file": "sandbox/archil/provider.toml", "sha256": "w",
   "capabilities": {"operations": ["create","get","list","start","stop","delete"], "ssh": null, "create": ["name"], "needs": ["key"]}},
  {"kind": "sandbox", "name": "Bad Name", "label": "x"}
 ]}`

func TestIndexCapabilities(t *testing.T) {
	ix, err := ParseIndex([]byte(capsIndex))
	if err != nil {
		t.Fatal(err)
	}
	box, _ := ix.Find("instabox")
	c := box.Capabilities
	if c == nil || c.SSH == nil || c.SSH.Port != "reply" || c.SSH.Key != "none" || !c.SSH.Access {
		t.Fatalf("instabox's capabilities: %+v", c)
	}
	if c.Size != "" || c.Lifetime || c.List == nil || !c.List.Paged || c.Auth.Header != "X-API-Key" {
		t.Errorf("instabox's capabilities: %+v", c)
	}
	if !c.Can("start") || c.Can("snapshot_save") || c.Can("preview") {
		t.Errorf("Can: start %v snapshot %v preview %v", c.Can("start"), c.Can("snapshot_save"), c.Can("preview"))
	}
	if !c.TakesPart("cpu") || !c.TakesPart("memory") || c.TakesPart("disk") || !c.Takes("snapshot") || c.Takes("image") {
		t.Errorf("Takes: %v", c.Create)
	}
	if c.Off(box.Kind) {
		t.Error("instabox, with ssh, is off")
	}

	// A built-in has none, and so goes by conch's own code: it can
	// everything, and takes everything, as before capabilities.
	lin, _ := ix.Find("linear")
	if lin.Capabilities != nil || !lin.Capabilities.Can("comment") || !lin.Capabilities.TakesPart("disk") || lin.Capabilities.Off(lin.Kind) {
		t.Errorf("a built-in's capabilities: %+v", lin.Capabilities)
	}

	ci, _ := ix.Find("circleci")
	if !ci.Capabilities.Can("rerun") || !ci.Capabilities.Follow || len(ci.Capabilities.Detect) != 2 || ci.Capabilities.Off(ci.Kind) {
		t.Errorf("circleci's capabilities: %+v", ci.Capabilities)
	}
	jk, _ := ix.Find("jenkins")
	if jk.Capabilities.Can("rerun") {
		t.Error("jenkins can re-run, with no rerun")
	}
	if got := jk.Capabilities.NeedsWords(); got != "the address of your own server, an API key, a user name" {
		t.Errorf("jenkins needs %q", got)
	}

	// disabled is read, its bad names left out like the entries'.
	if len(ix.Disabled) != 1 || ix.Disabled[0].Name != "archil" {
		t.Fatalf("disabled: %+v", ix.Disabled)
	}
	off := ix.Disabled[0]
	if !off.Capabilities.Off(off.Kind) || off.Capabilities.SSHWords(off.Kind) != "no ssh yet" {
		t.Errorf("archil is not off: %+v", off.Capabilities)
	}
	if _, found := ix.Find("archil"); found {
		t.Error("a disabled entry is found to install")
	}
	if got := ix.SearchDisabled("arch"); len(got) != 1 {
		t.Errorf("SearchDisabled: %+v", got)
	}
	if got := ix.Search("arch"); len(got) != 0 {
		t.Errorf("Search finds a disabled entry: %+v", got)
	}
}

// An index from before capabilities, and the old mirror's format 1, read
// as they did: no capabilities, nothing disabled.
func TestIndexWithoutCapabilities(t *testing.T) {
	for _, data := range []string{
		`{"format": 2, "entries": [{"kind": "sandbox", "name": "boat", "label": "boat.dev", "format": 3}]}`,
		`{"format": 1, "providers": [{"name": "boat", "label": "boat.dev", "format": 3}]}`,
	} {
		ix, err := ParseIndex([]byte(data))
		if err != nil {
			t.Fatal(err)
		}
		e, _ := ix.Find("boat")
		if e.Capabilities != nil || len(ix.Disabled) != 0 || e.Capabilities.Off(e.Kind) {
			t.Errorf("%s: capabilities %+v, disabled %+v", data, e.Capabilities, ix.Disabled)
		}
	}
}

func TestCapabilityWords(t *testing.T) {
	var boat Capabilities
	if err := json.Unmarshal([]byte(`{"operations": ["create","get","list","start","stop","delete","snapshot_save","snapshot_list","snapshot_forget","preview","usage"],
		"ssh": {"key":"authorize","access":true,"port":"endpoint"}}`), &boat); err != nil {
		t.Fatal(err)
	}
	if got := boat.OperationWords(KindSandbox); got != "create, start, stop, delete, snapshots, preview links, usage" {
		t.Errorf("boat's operations: %q", got)
	}
	if got := boat.SSHWords(KindSandbox); got != "a key conch hands it, through the provider's forwarder" {
		t.Errorf("boat's ssh: %q", got)
	}
	tk := Capabilities{Operations: []string{"list", "get", "comment", "future_op"}}
	if got := tk.OperationWords(KindTickets); got != "list, get, comment, future_op" {
		t.Errorf("a tracker's operations: %q", got)
	}
	if got := (&Capabilities{SSH: &SSHCaps{Key: "account", Port: "fixed"}}).SSHWords(KindCI); got != "" {
		t.Errorf("an integration's ssh words: %q", got)
	}
	var none *Capabilities
	if none.OperationWords(KindSandbox) != "" || none.SSHWords(KindSandbox) != "" || none.NeedsWords() != "" {
		t.Error("nil capabilities say something")
	}
}

// What the registry's index said for these files on 2026-11-16 (the files
// in testdata/registry are the registry's): conch's working out agrees.
var registrySaidIntegrations = map[string]string{
	"circleci":            `{"operations": ["runs", "failed_log", "rerun"], "auth": {"type": "header", "header": "Circle-Token"}, "needs": ["key"], "detect": ["checks", "files"], "states": ["canceled", "fail", "pass", "pending"], "follow": true}`,
	"jenkins":             `{"operations": ["runs", "failed_log"], "auth": {"type": "basic"}, "needs": ["base_url", "key", "user"], "detect": ["checks", "files"], "states": ["canceled", "fail", "pass"], "follow": false}`,
	"bitbucket-pipelines": `{"operations": ["runs", "failed_log"], "auth": {"type": "basic"}, "needs": ["key", "user"], "detect": ["files", "remotes"], "states": ["canceled", "fail", "pass"], "follow": false}`,
	"ntfy":                `{"operations": ["send"], "auth": {"type": "url"}, "needs": ["base_url", "key"], "detect": [], "states": [], "follow": false}`,
	// The registry's list of a tracker's operations has no mine (what is
	// assigned to you), which conch's does: told to the registry.
	"gitlab-issues": `{"operations": ["list", "mine", "get", "comment", "link", "move"], "auth": {"type": "header", "header": "PRIVATE-TOKEN"}, "needs": ["base_url", "key"], "detect": ["remotes"], "states": [], "follow": false}`,
}

func TestIntegrationFileCapabilitiesAgreeWithTheRegistry(t *testing.T) {
	for name, said := range registrySaidIntegrations {
		data, err := os.ReadFile("testdata/registry/" + name + ".toml")
		if err != nil {
			t.Fatal(err)
		}
		f, problems, err := Parse(data)
		if err != nil || len(problems) > 0 {
			t.Fatalf("%s: %v %v", name, err, problems)
		}
		var want Capabilities
		if err := json.Unmarshal([]byte(said), &want); err != nil {
			t.Fatal(err)
		}
		if got := FileCapabilities(f); !reflect.DeepEqual(*got, want) {
			g, _ := json.Marshal(got)
			t.Errorf("%s:\n got %s\nwant %s", name, g, said)
		}
	}
}

// A check can be run again through Actions, or an integration whose file
// has [rerun]; one without, or a service with no file here, cannot.
func TestCanRerun(t *testing.T) {
	read := func(name string) *File {
		data, _ := os.ReadFile("testdata/registry/" + name + ".toml")
		f, _, err := Parse(data)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	files := []*File{read("circleci"), read("jenkins"), read("gitlab-issues")}
	for src, want := range map[string]bool{SourceActions: true, "circleci": true, "jenkins": false, "gitlab-issues": false, "azure-pipelines": false, "": false} {
		if got := CanRerun(src, files); got != want {
			t.Errorf("CanRerun(%q) = %v", src, got)
		}
	}
}

// The registry's capabilities_version 2 gives a lifetime as its numbers.
// This conch read only a bool there, and one entry it could not read lost
// it the whole index: the Integrations page went without the registry's
// sandboxes and every icon (seen 2026-11-17, boat's entry). boat's and
// daytona's are the live index's, as it was published then.
func TestIndexCapabilitiesVersion2(t *testing.T) {
	const ix2 = `{"format": 2, "capabilities_version": 2, "entries": [
  {"kind": "sandbox", "name": "boat", "label": "boat.dev", "format": 4, "file": "sandbox/boat/provider.toml", "sha256": "a",
   "capabilities": {"operations": ["create", "get", "list", "start", "stop", "delete", "snapshot_save", "snapshot_list", "snapshot_forget", "preview", "usage"], "ssh": {"key": "authorize", "access": true, "port": "endpoint"}, "create": ["env", "image", "name", "size", "ttl_secs"], "settings": ["image"], "size": "names", "lifetime": {"default_mins": 120, "max_mins": 43200}, "states": ["error", "started", "starting", "stopped", "stopping"], "list": {"paged": true, "all_mine": true}, "auth": {"type": "header", "header": "Authorization"}, "needs": ["key"]}},
  {"kind": "sandbox", "name": "daytona", "label": "Daytona", "format": 4, "file": "sandbox/daytona/provider.toml", "sha256": "b",
   "capabilities": {"operations": ["create", "get", "list", "start", "stop", "delete", "snapshot_save", "snapshot_list", "snapshot_forget", "preview"], "ssh": {"key": "none", "access": true, "port": "fixed"}, "create": ["cpu", "disk_gib", "env", "image", "label", "memory_gib", "region"], "settings": ["region", "image"], "size": "limits", "lifetime": null, "states": ["error", "gone", "started", "starting", "stopped", "stopping"], "list": {"paged": true, "all_mine": true}, "auth": {"type": "header", "header": "Authorization"}, "needs": ["key"]}},
  {"kind": "sandbox", "name": "maxonly", "label": "x", "capabilities": {"operations": ["create"], "lifetime": {"max_mins": 60}}},
  {"kind": "sandbox", "name": "zeroes", "label": "x", "capabilities": {"operations": ["create"], "lifetime": {"default_mins": 0, "max_mins": 0}}},
  {"kind": "sandbox", "name": "empty", "label": "x", "capabilities": {"operations": ["create"], "lifetime": {}}},
  {"kind": "sandbox", "name": "v1yes", "label": "x", "capabilities": {"operations": ["create"], "lifetime": true}},
  {"kind": "sandbox", "name": "v1no", "label": "x", "capabilities": {"operations": ["create"], "lifetime": false}},
  {"kind": "sandbox", "name": "unknown", "label": "x", "capabilities": {"operations": ["create"], "lifetime": "2h"}},
  {"kind": "sandbox", "name": "absent", "label": "x", "capabilities": {"operations": ["create"]}},
  {"kind": "ci", "name": "circleci", "label": "CircleCI", "capabilities": {"operations": ["runs"], "follow": true}}
 ]}`
	ix, err := ParseIndex([]byte(ix2))
	if err != nil {
		t.Fatal(err)
	}
	if len(ix.Entries) != 10 {
		t.Fatalf("%d entries, want all 10", len(ix.Entries))
	}
	want := map[string]bool{"boat": true, "daytona": false, "maxonly": true, "zeroes": false, "empty": false,
		"v1yes": true, "v1no": false, "unknown": false, "absent": false, "circleci": false}
	for name, life := range want {
		e, ok := ix.Find(name)
		if !ok || e.Capabilities == nil {
			t.Fatalf("%s: missing or no capabilities", name)
		}
		if e.Capabilities.Lifetime != life {
			t.Errorf("%s: lifetime %v, want %v", name, e.Capabilities.Lifetime, life)
		}
	}
	// The rest of the capabilities are read around it.
	boat, _ := ix.Find("boat")
	if c := boat.Capabilities; c.Size != "names" || !c.Can("usage") || c.SSH == nil || c.SSH.Port != "endpoint" || !c.List.AllMine || !c.Takes("ttl_secs") {
		t.Errorf("boat's capabilities: %+v", c)
	}
	day, _ := ix.Find("daytona")
	if c := day.Capabilities; len(c.Settings) != 2 || c.Size != "limits" || c.Can("usage") {
		t.Errorf("daytona's capabilities: %+v", c)
	}
	if ci, _ := ix.Find("circleci"); !ci.Capabilities.Follow {
		t.Error("circleci's follow lost")
	}
	// conch still writes the bool it always has (registry.json, and the
	// app's -json, which decode lifetime as a Bool).
	out, err := json.Marshal(boat.Capabilities)
	if err != nil || !strings.Contains(string(out), `"lifetime":true`) {
		t.Errorf("written as %s (%v)", out, err)
	}
	var back Capabilities
	if err := json.Unmarshal(out, &back); err != nil || !reflect.DeepEqual(back, *boat.Capabilities) {
		t.Errorf("round trip: %+v (%v)", back, err)
	}
}

// Capabilities that are not an object still fail, as before: only a
// lifetime of a shape conch does not know is forgiven.
func TestCapabilitiesStillRefuseNonsense(t *testing.T) {
	for _, bad := range []string{`[]`, `"x"`, `{"operations": "create"}`, `{"follow": "yes"}`} {
		var c Capabilities
		if err := json.Unmarshal([]byte(bad), &c); err == nil {
			t.Errorf("%s read without an error: %+v", bad, c)
		}
	}
	var c Capabilities
	if err := json.Unmarshal([]byte(`null`), &c); err != nil {
		t.Errorf("null: %v", err)
	}
}

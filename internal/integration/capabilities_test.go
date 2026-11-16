package integration

import (
	"encoding/json"
	"os"
	"reflect"
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

package sandbox

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/integration"
)

// What the registry's index said for these files on 2026-11-16, word for
// word: the files in testdata/registry are the registry's, so conch's
// working out must agree with its own.
var registrySaid = map[string]string{
	"boat":       `{"operations": ["create", "get", "list", "start", "stop", "delete", "snapshot_save", "snapshot_list", "snapshot_forget", "preview", "usage"], "ssh": {"key": "authorize", "access": true, "port": "endpoint"}, "create": ["env", "image", "name", "size", "ttl_secs"], "settings": ["image"], "size": "names", "lifetime": true, "states": ["error", "started", "starting", "stopped", "stopping"], "list": {"paged": true, "all_mine": true}, "auth": {"type": "header", "header": "Authorization"}, "needs": ["key"]}`,
	"daytona":    `{"operations": ["create", "get", "list", "start", "stop", "delete", "snapshot_save", "snapshot_list", "snapshot_forget", "preview"], "ssh": {"key": "none", "access": true, "port": "fixed"}, "create": ["cpu", "disk_gib", "env", "image", "label", "memory_gib", "region"], "settings": ["region", "image"], "size": "limits", "lifetime": false, "states": ["error", "gone", "started", "starting", "stopped", "stopping"], "list": {"paged": true, "all_mine": true}, "auth": {"type": "header", "header": "Authorization"}, "needs": ["key"]}`,
	"tensorlake": `{"operations": ["create", "get", "list", "start", "stop", "delete"], "ssh": {"key": "account", "access": false, "port": "fixed"}, "create": ["cpu", "disk_mb", "image", "memory_mb", "name"], "settings": ["image"], "size": "limits", "lifetime": false, "states": ["error", "gone", "started", "starting", "stopped", "stopping"], "list": {"paged": true, "all_mine": false}, "auth": {"type": "header", "header": "Authorization"}, "needs": ["key"]}`,
}

func TestFileCapabilitiesAgreeWithTheRegistry(t *testing.T) {
	for name, said := range registrySaid {
		f, problems, err := ReadProviderFile("testdata/registry/" + name + ".toml")
		if err != nil || len(problems) > 0 {
			t.Fatalf("%s: %v %v", name, err, problems)
		}
		var want integration.Capabilities
		if err := json.Unmarshal([]byte(said), &want); err != nil {
			t.Fatal(err)
		}
		if got := FileCapabilities(f); !reflect.DeepEqual(*got, want) {
			g, _ := json.Marshal(got)
			t.Errorf("%s:\n got %s\nwant %s", name, g, said)
		}
	}
}

func TestFileCapabilitiesEdges(t *testing.T) {
	// InstaBox's sizes come from its plan, and it takes a template.
	f, _, err := ReadProviderFile("testdata/registry/instabox.toml")
	if err != nil {
		t.Fatal(err)
	}
	c := FileCapabilities(f)
	if c.Size != "plan" || !c.Takes("template") || c.TakesPart("disk") || !c.TakesPart("memory") || c.Can("preview") || !c.Can("snapshot_save") {
		t.Errorf("instabox: %+v", c)
	}
	// A file with no [ssh] is off, as the registry's disabled ones are;
	// a lone [lifetime] max counts.
	f.SSH = SSHWay{}
	f.Lifetime = LifeSpec{MaxMins: 60}
	if c := FileCapabilities(f); !c.Off(integration.KindSandbox) || !c.Lifetime {
		t.Errorf("no ssh: %+v", c)
	}
	// {id} and {reply.x} are not what create asks for; a query's are.
	f.Create.Call.Call = "POST /v1/boxes?zone={region}"
	f.Create.Body = map[string]any{"a": "{id}", "b": []any{"{reply.x}", map[string]any{"c": "{cpu}"}}}
	if c := FileCapabilities(f); !reflect.DeepEqual(c.Create, []string{"cpu", "region"}) {
		t.Errorf("create: %v", c.Create)
	}
	// An empty file can do nothing, said as an empty list, never null.
	b, _ := json.Marshal(FileCapabilities(&ProviderFile{}))
	if !strings.Contains(string(b), `"operations":[]`) {
		t.Errorf("empty: %s", b)
	}
}

// CapabilitiesOf takes the registry's while the file is the one they were
// worked out from, and the file's own otherwise.
func TestCapabilitiesOf(t *testing.T) {
	t.Setenv("CONCH_HOME", t.TempDir())
	if CapabilitiesOf("nobody") != nil {
		t.Error("a provider with no file has capabilities")
	}
	data, _ := os.ReadFile("testdata/registry/tensorlake.toml")
	// The registry's say something the file does not, so it can be told
	// which was used.
	theirs := &integration.Capabilities{Operations: []string{"create", "get", "list", "start", "stop", "delete", "future_op"}}
	if _, err := InstallRegistryFile("tensorlake", data, theirs); err != nil {
		t.Fatal(err)
	}
	if got := CapabilitiesOf("tensorlake"); !got.Can("future_op") {
		t.Errorf("the registry's were not used: %+v", got)
	}
	// Edited here: the file's own.
	os.WriteFile(ProviderFilePath("tensorlake"), append(data, '\n'), 0o600)
	if got := CapabilitiesOf("tensorlake"); got.Can("future_op") || !got.Can("start") {
		t.Errorf("an edited file's: %+v", got)
	}
	// Installed before the registry said: the file's own, until update
	// keeps them.
	if _, err := InstallProviderFile("tensorlake", data); err != nil {
		t.Fatal(err)
	}
	if got := CapabilitiesOf("tensorlake"); got.Can("future_op") || got.SSH == nil {
		t.Errorf("no record's: %+v", got)
	}
	if err := KeepCapabilities("tensorlake", theirs); err != nil {
		t.Fatal(err)
	}
	if got := CapabilitiesOf("tensorlake"); !got.Can("future_op") {
		t.Errorf("kept ones were not used: %+v", got)
	}
	if err := KeepCapabilities("nobody", theirs); err != nil {
		t.Errorf("keeping for one not installed: %v", err)
	}
	// A file of the person's own has no record at all.
	os.WriteFile(ProviderFilePath("mine"), []byte(strings.Replace(string(data), `name = "tensorlake"`, `name = "mine"`, 1)), 0o600)
	if got := CapabilitiesOf("mine"); got == nil || !got.Can("delete") {
		t.Errorf("a file of your own: %+v", got)
	}
}

// The index's disabled providers are read, and kept apart from the ones
// to install.
func TestRegistryDisabled(t *testing.T) {
	t.Setenv("CONCH_HOME", t.TempDir())
	r := newFakeRegistry(t)
	r.raw = []byte(`{"format": 2,
	 "entries": [{"kind": "sandbox", "name": "zeta", "label": "Zeta", "format": 3,
	   "capabilities": {"operations": ["create"], "ssh": {"key": "account", "access": false, "port": "fixed"}}},
	   {"kind": "ci", "name": "circleci", "label": "CircleCI"}],
	 "disabled": [{"kind": "sandbox", "name": "archil", "label": "Archil", "format": 3,
	   "capabilities": {"operations": ["create"], "ssh": null}},
	   {"kind": "sandbox", "name": "Bad Name"}, {"kind": "ci", "name": "offci"}]}`)
	ix, err := FetchIndex(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ix.Providers) != 1 || ix.Providers[0].Capabilities == nil || ix.Providers[0].Capabilities.SSH == nil {
		t.Fatalf("providers: %+v", ix.Providers)
	}
	if len(ix.Disabled) != 1 || ix.Disabled[0].Name != "archil" || !ix.Disabled[0].Capabilities.Off(integration.KindSandbox) {
		t.Fatalf("disabled: %+v", ix.Disabled)
	}
	if _, ok := ix.Find("archil"); ok {
		t.Error("a disabled provider is found to install")
	}
	if _, ok := ix.FindDisabled("archil"); !ok {
		t.Error("FindDisabled")
	}
	if len(ix.SearchDisabled("arc")) != 1 || len(ix.Search("arc")) != 0 {
		t.Error("search")
	}
}

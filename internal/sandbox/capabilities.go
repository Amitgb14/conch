package sandbox

import (
	"slices"
	"sort"
	"strings"

	"github.com/Amitgb14/conch/internal/integration"
)

// What a provider can do. The registry puts it on every entry of its
// index (integration.Capabilities), worked out from the file the way
// FileCapabilities does here; install keeps it with the file's record.
// The file is still what runs, so the record's are used only while the
// file is the one they were worked out from, and FileCapabilities answers
// for a file of the person's own, one edited since, or one installed
// before the registry said.

// FileCapabilities is what f can do, as the registry's index would say
// it (conch-integrations' scripts/registry.py sandbox_capabilities).
func FileCapabilities(f *ProviderFile) *integration.Capabilities {
	c := &integration.Capabilities{Operations: []string{}, Create: []string{}, Settings: []string{}, States: []string{}}
	for _, op := range []struct {
		name string
		call string
	}{
		{"create", f.Create.Call.Call}, {"get", f.Get.Call.Call}, {"list", f.List.Call.Call},
		{"start", f.Start.Call}, {"stop", f.Stop.Call}, {"delete", f.Delete.Call},
		{"snapshot_save", f.Snapshot.Save.Call}, {"snapshot_list", f.Snapshot.List.Call.Call}, {"snapshot_forget", f.Snapshot.Forget.Call},
		{"preview", f.Preview.Call.Call}, {"usage", f.Usage.Call.Call},
	} {
		if op.call != "" {
			c.Operations = append(c.Operations, op.name)
		}
	}
	if f.SSH.Host != "" || f.SSH.Endpoint != "" || f.SSH.Command != "" {
		s := &integration.SSHCaps{Key: firstSet(f.SSH.Key, "account"), Access: f.SSH.Access.Call != "", Port: "fixed"}
		switch {
		case f.SSH.Endpoint != "":
			s.Port = "endpoint"
		case f.SSH.Port.Reply != "":
			s.Port = "reply"
		}
		c.SSH = s
	}
	// The call's own query counts: {region} there is a setting it sends.
	seen := map[string]bool{}
	note := func(s string) {
		for _, p := range placeholder.FindAllString(s, -1) {
			p = strings.Trim(p, "{}")
			if p != "id" && !strings.Contains(p, ".") {
				seen[p] = true
			}
		}
	}
	walk(f.Create.Body, note)
	note(f.Create.Call.Call)
	for p := range seen {
		c.Create = append(c.Create, p)
	}
	sort.Strings(c.Create)
	for _, s := range f.Settings {
		c.Settings = append(c.Settings, s.Key)
	}
	switch z := f.Size; {
	case len(z.Names) > 0:
		c.Size = "names"
	case z.Choices.Call != "":
		c.Size = "plan"
	case len(z.CPU)+len(z.MemoryGiB)+len(z.DiskGiB) > 0:
		c.Size = "limits"
	}
	c.Lifetime = f.Lifetime.DefaultMins > 0 || f.Lifetime.MaxMins > 0
	for _, st := range f.States {
		if !slices.Contains(c.States, st) {
			c.States = append(c.States, st)
		}
	}
	sort.Strings(c.States)
	c.List = &integration.ListCaps{Paged: f.List.Next != "", AllMine: f.List.AllMine}
	c.Auth = &integration.AuthCaps{Type: "header", Header: firstSet(f.AuthHeader, "Authorization")}
	if f.KeyEnv != "" {
		c.Needs = []string{"key"}
	}
	return c
}

// CapabilitiesOf is what the installed provider name can do: the
// registry's, kept when it was installed, while its file is still that
// one; else worked out from the file. nil when there is no file to read.
func CapabilitiesOf(name string) *integration.Capabilities {
	f, _, err := ReadProviderFile(ProviderFilePath(name))
	if err != nil {
		return nil
	}
	if m, err := ReadInstalled(); err == nil {
		if in, ok := m[name]; ok && in.Capabilities != nil {
			if edited, err := EditedSinceInstall(name, in); err == nil && !edited {
				return in.Capabilities
			}
		}
	}
	return FileCapabilities(f)
}

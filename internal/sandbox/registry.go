package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/integration"
)

// The registry is a GitHub repository of provider files that people have
// adapted and checked (github.com/Amitgb14/conch-integrations, which was
// conch-sandbox-providers and holds every kind of integration now; the
// old address is a mirror of its sandbox providers for older conches). Its
// CI publishes an index of them, and the files themselves, on GitHub
// Pages; conch installs from there. A file installed from it is recorded
// in providers/registry.json with its hash, so an update can tell a file
// somebody edited here from one that is as it came.

// RegistryRepo is where provider files are published to.
const RegistryRepo = "Amitgb14/conch-integrations"

// registryURL is where the index is read from; $CONCH_PROVIDERS_URL
// overrides it, for a registry of one's own and for tests.
const registryURL = "https://amitgb14.github.io/conch-integrations"

// RegistryURL is the registry conch reads.
func RegistryURL() string {
	if u := os.Getenv("CONCH_PROVIDERS_URL"); u != "" {
		return strings.TrimRight(u, "/")
	}
	return integration.OwnURL()
}

// RegistryEntry is one provider in the index.
type RegistryEntry struct {
	Name      string         `json:"name"`
	Label     string         `json:"label"`
	API       string         `json:"api"`
	APIHost   string         `json:"api_host"`
	SSHHost   string         `json:"ssh_host"`
	KeyEnv    string         `json:"key_env"`
	Format    int            `json:"format"`
	Note      string         `json:"note,omitempty"`
	File      string         `json:"file"`
	SHA256    string         `json:"sha256"`
	AdaptedBy string         `json:"adapted_by"`
	Adapted   string         `json:"adapted"`
	Docs      string         `json:"docs"`
	Conch     string         `json:"conch"`
	Checked   *RegistryCheck `json:"checked"`
	// Capabilities are what the registry worked out the file can do; nil
	// in an index from before them.
	Capabilities *integration.Capabilities `json:"capabilities,omitempty"`
}

// RegistryCheck is the last time somebody's key listed sandboxes with a
// provider's file.
type RegistryCheck struct {
	Date      string `json:"date"`
	Conch     string `json:"conch"`
	Sandboxes int    `json:"sandboxes"`
}

// Index is the registry's list.
type Index struct {
	Format    int             `json:"format"`
	Providers []RegistryEntry `json:"providers"`
	// Disabled are listed and cannot be installed yet: no [ssh], so conch
	// could make their sandboxes and never reach them.
	Disabled []RegistryEntry `json:"-"`
}

// Find is the entry for name.
func (ix *Index) Find(name string) (RegistryEntry, bool) {
	for _, e := range ix.Providers {
		if e.Name == name {
			return e, true
		}
	}
	return RegistryEntry{}, false
}

// Search is the entries whose name, label or API host holds term, every
// one for an empty term.
func (ix *Index) Search(term string) []RegistryEntry { return searchEntries(ix.Providers, term) }

// SearchDisabled is Search over the providers that cannot be installed yet.
func (ix *Index) SearchDisabled(term string) []RegistryEntry { return searchEntries(ix.Disabled, term) }

// FindDisabled is the disabled entry for name.
func (ix *Index) FindDisabled(name string) (RegistryEntry, bool) {
	i := slices.IndexFunc(ix.Disabled, func(e RegistryEntry) bool { return e.Name == name })
	if i < 0 {
		return RegistryEntry{}, false
	}
	return ix.Disabled[i], true
}

func searchEntries(entries []RegistryEntry, term string) []RegistryEntry {
	term = strings.ToLower(strings.TrimSpace(term))
	var out []RegistryEntry
	for _, e := range entries {
		if term == "" || strings.Contains(e.Name, term) || strings.Contains(strings.ToLower(e.Label), term) || strings.Contains(e.APIHost, term) {
			out = append(out, e)
		}
	}
	return out
}

// ForHost is the entry whose API is at host, or whose docs are, for a URL
// somebody is about to adapt: no need to adapt what is already here.
func (ix *Index) ForHost(host string) (RegistryEntry, bool) {
	host = strings.ToLower(host)
	for _, e := range ix.Providers {
		docs, _ := url.Parse(e.Docs)
		if host != "" && (host == e.APIHost || (docs != nil && host == docs.Hostname())) {
			return e, true
		}
	}
	return RegistryEntry{}, false
}

const (
	maxIndex    = 1 << 20
	indexFormat = 2
)

var registryClient = &http.Client{Timeout: 20 * time.Second}

func registryGet(ctx context.Context, path string, limit int64) ([]byte, error) {
	base := RegistryURL()
	u, err := url.Parse(base)
	if err != nil || !(u.Scheme == "https" || (u.Scheme == "http" && isLoopback(u.Hostname()))) {
		return nil, fmt.Errorf("the provider registry %q is not an https:// URL", base)
	}
	req, err := http.NewRequestWithContext(ctx, "GET", base+"/"+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := registryClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("the provider registry: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the provider registry: %s answered %s", req.URL, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("the provider registry: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("the provider registry: %s is over %d bytes", path, limit)
	}
	return data, nil
}

// FetchIndex reads the registry's list: the same index every kind of
// integration is in, checked against its signature as theirs is
// (integration.FetchIndexData), keeping the sandbox providers. Format 1 is
// the index from before the move; format 2's sandbox entries keep its
// fields.
func FetchIndex(ctx context.Context) (*Index, error) {
	reg := integration.Registries(config.IntegrationsCfg{})[0]
	reg.URL = RegistryURL()
	data, _, err := integration.FetchIndexData(ctx, reg)
	if err != nil {
		return nil, fmt.Errorf("the provider registry: %w", err)
	}
	var ix Index
	if err := json.Unmarshal(data, &ix); err != nil {
		return nil, fmt.Errorf("the provider registry's index: %w", err)
	}
	switch ix.Format {
	case 1:
	case 2:
		type entry struct {
			Kind string `json:"kind"`
			RegistryEntry
		}
		var v2 struct {
			Entries  []entry `json:"entries"`
			Disabled []entry `json:"disabled"`
		}
		if err := json.Unmarshal(data, &v2); err != nil {
			return nil, fmt.Errorf("the provider registry's index: %w", err)
		}
		ix.Providers = nil
		for _, e := range v2.Entries {
			if e.Kind == "sandbox" {
				ix.Providers = append(ix.Providers, e.RegistryEntry)
			}
		}
		for _, e := range v2.Disabled {
			if e.Kind == "sandbox" {
				ix.Disabled = append(ix.Disabled, e.RegistryEntry)
			}
		}
	default:
		return nil, fmt.Errorf("the provider registry's index is format %d; this conch reads up to %d: update conch", ix.Format, indexFormat)
	}
	// An entry whose name could not be a provider's is left out: its name
	// would be made a path here, and no conch could install it anyway.
	bad := func(e RegistryEntry) bool { return CheckName(e.Name) != nil }
	ix.Providers = slices.DeleteFunc(ix.Providers, bad)
	ix.Disabled = slices.DeleteFunc(ix.Disabled, bad)
	sort.Slice(ix.Providers, func(i, j int) bool { return ix.Providers[i].Name < ix.Providers[j].Name })
	sort.Slice(ix.Disabled, func(i, j int) bool { return ix.Disabled[i].Name < ix.Disabled[j].Name })
	return &ix, nil
}

// FetchProviderFile downloads e's file and checks it is the one the index
// lists, byte for byte.
func FetchProviderFile(ctx context.Context, e RegistryEntry) ([]byte, error) {
	if e.Format > ProviderFormat {
		return nil, fmt.Errorf("%s's file is format %d; this conch reads up to %d: update conch", e.Name, e.Format, ProviderFormat)
	}
	if !strings.HasPrefix(e.File, "providers/") || strings.Contains(e.File, "..") {
		return nil, fmt.Errorf("the index lists %s's file at %q", e.Name, e.File)
	}
	data, err := registryGet(ctx, e.File, 256<<10)
	if err != nil {
		return nil, err
	}
	if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != e.SHA256 {
		return nil, fmt.Errorf("%s's file is not the one the index lists (its hash differs): try again in a minute, as the registry may be publishing", e.Name)
	}
	return data, nil
}

// Installed is what was installed from the registry: the file's hash as
// it came, the hosts it named then, and which of the person's variables
// it read the key from and how it sent it. A record from before KeyEnv
// was kept has it empty, which says nothing — not that the file named
// none (a file must) — so an update asks about it once.
type Installed struct {
	SHA256     string `json:"sha256"`
	APIHost    string `json:"api_host"`
	SSHHost    string `json:"ssh_host"`
	KeyEnv     string `json:"key_env,omitempty"`
	AuthHeader string `json:"auth_header,omitempty"`
	AuthValue  string `json:"auth_value,omitempty"`
	Installed  string `json:"installed"`
	// Capabilities are the registry's for the file as it came
	// (CapabilitiesOf); nil for one installed before it said.
	Capabilities *integration.Capabilities `json:"capabilities,omitempty"`
}

// FileKey is how a provider file sends a key: the variable it is read
// from, and the header and value it goes in (empty for the defaults).
func FileKey(f *ProviderFile) (keyEnv, authHeader, authValue string) {
	return f.KeyEnv, f.AuthHeader, f.AuthValue
}

// SameKey says whether f sends the key as in was recorded sending it;
// false for a record that never said.
func (in Installed) SameKey(f *ProviderFile) bool {
	env, h, v := FileKey(f)
	return in.KeyEnv != "" && in.KeyEnv == env && in.AuthHeader == h && in.AuthValue == v
}

func installedPath() string { return filepath.Join(ProvidersDir(), "registry.json") }

// ReadInstalled is every provider installed from the registry. A missing
// file is none.
func ReadInstalled() (map[string]Installed, error) {
	m := map[string]Installed{}
	data, err := os.ReadFile(installedPath())
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", installedPath(), err)
	}
	return m, nil
}

func writeInstalled(m map[string]Installed) error {
	data, _ := json.MarshalIndent(m, "", "  ")
	return writeAtomic(installedPath(), append(data, '\n'))
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// FileHosts are the hosts a provider file sends the key and ssh to.
func FileHosts(f *ProviderFile) (api, ssh string) {
	if u, err := url.Parse(f.API); err == nil {
		api = u.Hostname()
	}
	return api, f.SSH.Host
}

// ParseProviderFile checks data as a provider file named name, without
// keeping it anywhere.
func ParseProviderFile(name string, data []byte) (*ProviderFile, []string, error) {
	if err := CheckName(name); err != nil {
		return nil, nil, err
	}
	dir, err := os.MkdirTemp("", "conch-provider")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, name+".toml")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return nil, nil, err
	}
	f, problems, err := ReadProviderFile(path)
	if err == nil && f.Name != name {
		problems = append(problems, fmt.Sprintf("the file names %q, not %q", f.Name, name))
	}
	return f, problems, err
}

// InstallProviderFile keeps data as provider name, installed from the
// registry. It is checked first: a file this conch cannot run is refused
// rather than saved and skipped.
func InstallProviderFile(name string, data []byte) (*ProviderFile, error) {
	return InstallRegistryFile(name, data, nil)
}

// InstallRegistryFile is InstallProviderFile keeping what the registry's
// index said the file can do with its record.
func InstallRegistryFile(name string, data []byte, caps *integration.Capabilities) (*ProviderFile, error) {
	f, problems, err := ParseProviderFile(name, data)
	if err != nil {
		return nil, err
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("%s's file cannot be used here: %s", name, strings.Join(problems, "; "))
	}
	m, err := ReadInstalled()
	if err != nil {
		return nil, err
	}
	if err := writeAtomic(ProviderFilePath(name), data); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	api, ssh := FileHosts(f)
	env, h, v := FileKey(f)
	m[name] = Installed{SHA256: hex.EncodeToString(sum[:]), APIHost: api, SSHHost: ssh, KeyEnv: env, AuthHeader: h, AuthValue: v,
		Installed: time.Now().UTC().Format("2006-01-02"), Capabilities: caps}
	return f, writeInstalled(m)
}

// KeepCapabilities records what the registry says name's file can do, for
// one installed before it said.
func KeepCapabilities(name string, caps *integration.Capabilities) error {
	m, err := ReadInstalled()
	if err != nil {
		return err
	}
	in, ok := m[name]
	if !ok {
		return nil
	}
	in.Capabilities = caps
	m[name] = in
	return writeInstalled(m)
}

// ForgetInstalled drops name from the record, when its file is removed.
func ForgetInstalled(name string) error {
	m, err := ReadInstalled()
	if err != nil {
		return err
	}
	if _, ok := m[name]; !ok {
		return nil
	}
	delete(m, name)
	return writeInstalled(m)
}

// EditedSinceInstall says whether name's file is no longer as it came from
// the registry. A file that is gone is not edited: there is nothing to keep.
func EditedSinceInstall(name string, in Installed) (bool, error) {
	data, err := os.ReadFile(ProviderFilePath(name))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]) != in.SHA256, nil
}

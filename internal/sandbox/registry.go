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
	"sort"
	"strings"
	"time"
)

// The registry is a GitHub repository of provider files that people have
// adapted and checked (github.com/Amitgb14/conch-sandbox-providers). Its
// CI publishes an index of them, and the files themselves, on GitHub
// Pages; conch installs from there. A file installed from it is recorded
// in providers/registry.json with its hash, so an update can tell a file
// somebody edited here from one that is as it came.

// RegistryRepo is where provider files are published to.
const RegistryRepo = "Amitgb14/conch-sandbox-providers"

// registryURL is where the index is read from; $CONCH_PROVIDERS_URL
// overrides it, for a registry of one's own and for tests.
const registryURL = "https://amitgb14.github.io/conch-sandbox-providers"

// RegistryURL is the registry conch reads.
func RegistryURL() string {
	if u := os.Getenv("CONCH_PROVIDERS_URL"); u != "" {
		return strings.TrimRight(u, "/")
	}
	return registryURL
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
func (ix *Index) Search(term string) []RegistryEntry {
	term = strings.ToLower(strings.TrimSpace(term))
	var out []RegistryEntry
	for _, e := range ix.Providers {
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
	indexFormat = 1
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

// FetchIndex reads the registry's list.
func FetchIndex(ctx context.Context) (*Index, error) {
	data, err := registryGet(ctx, "index.json", maxIndex)
	if err != nil {
		return nil, err
	}
	var ix Index
	if err := json.Unmarshal(data, &ix); err != nil {
		return nil, fmt.Errorf("the provider registry's index: %w", err)
	}
	if ix.Format != indexFormat {
		return nil, fmt.Errorf("the provider registry's index is format %d; this conch reads %d: update conch", ix.Format, indexFormat)
	}
	sort.Slice(ix.Providers, func(i, j int) bool { return ix.Providers[i].Name < ix.Providers[j].Name })
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
// it came, and the hosts it named then.
type Installed struct {
	SHA256    string `json:"sha256"`
	APIHost   string `json:"api_host"`
	SSHHost   string `json:"ssh_host"`
	Installed string `json:"installed"`
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
	if Builtin(name) {
		return nil, fmt.Errorf("%s is built into conch", name)
	}
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
	m[name] = Installed{SHA256: hex.EncodeToString(sum[:]), APIHost: api, SSHHost: ssh, Installed: time.Now().UTC().Format("2006-01-02")}
	return f, writeInstalled(m)
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

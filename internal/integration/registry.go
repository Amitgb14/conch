package integration

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
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
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/config"
)

// The registry is a GitHub repository of files people have adapted and
// checked; its CI publishes an index of them and the files themselves on
// Pages, and conch installs from there. Each version of a file is pinned
// by its hash in the index, and the index itself is signed (ed25519, over
// its exact bytes, in index.json.sig) by a key the registry's CI holds, so
// a repository or a Pages site somebody else got into cannot swap a file.
//
// Until conch's own registry signs, RegistryKey is empty: its index is
// read and said to be unsigned. A registry conch knows a key for must
// sign, or nothing is read from it.

// RegistryKey is the public key conch's own registry signs its index
// with, base64; empty until its CI signs.
var RegistryKey = ""

// registryURL is conch's own registry; $CONCH_PROVIDERS_URL replaces it
// (with $CONCH_PROVIDERS_KEY its key), for a registry of one's own and for
// tests.
const registryURL = "https://amitgb14.github.io/conch-integrations"

// Registry is where an index is read from, and the key it must be signed
// with ("" for none known).
type Registry struct {
	Name string
	URL  string
	Key  string
}

// Registries are conch's own, then those config.toml adds.
func Registries(cfg config.IntegrationsCfg) []Registry {
	own := Registry{Name: "conch-integrations", URL: OwnURL(), Key: RegistryKey}
	if u := os.Getenv("CONCH_PROVIDERS_URL"); u != "" {
		own.URL, own.Key = strings.TrimRight(u, "/"), os.Getenv("CONCH_PROVIDERS_KEY")
	}
	out := []Registry{own}
	for _, r := range cfg.Registries {
		if r.URL != "" {
			out = append(out, Registry{Name: firstSet(r.Name, r.URL), URL: strings.TrimRight(r.URL, "/"), Key: r.Key})
		}
	}
	return out
}

// OwnURL is conch-integrations' address — in a test binary, an address
// nothing answers, unless the test gave one ($CONCH_PROVIDERS_URL): opening
// a sandbox provider conch once had installs its file from the registry,
// so a test in any package that opens one would otherwise reach the real
// registry over the network.
func OwnURL() string {
	if testing.Testing() {
		return "http://127.0.0.1:1/no-registry-in-tests"
	}
	return registryURL
}

// Entry is one file in an index.
type Entry struct {
	Kind  string `json:"kind"`
	Name  string `json:"name"`
	Label string `json:"label"`
	// BuiltIn is something conch's own code reads — GitHub, Linear, Jira —
	// listed so the catalog is one place; it has no file to install.
	BuiltIn bool   `json:"builtin,omitempty"`
	Version int    `json:"version"`
	Format  int    `json:"format"`
	File    string `json:"file"`
	SHA256  string `json:"sha256"`
	// Hosts are where the file sends the key (API) and what it fetches
	// without it (Follow); the person agrees to them before installing.
	Hosts Hosts `json:"hosts"`
	// KeyEnv is the variable the file reads the key from: shown with the
	// hosts, since a file reading another service's variable would send
	// that service's key to its own.
	KeyEnv     string   `json:"key_env"`
	UserEnv    string   `json:"user_env,omitempty"`
	Operations []string `json:"operations,omitempty"`
	// Tier is verified (a maintainer's key ran it, Checked says when) or
	// community.
	Tier      string       `json:"tier,omitempty"`
	Checked   *Checked     `json:"checked,omitempty"`
	Revoked   []Revocation `json:"revoked,omitempty"`
	Installs  int          `json:"installs,omitempty"`
	Note      string       `json:"note,omitempty"`
	Docs      string       `json:"docs,omitempty"`
	AdaptedBy string       `json:"adapted_by,omitempty"`
	Adapted   string       `json:"adapted,omitempty"`
	// Icon is the service's own mark, beside the index (icons/NAME.svg or
	// .png), and IconSHA256 its hash: FetchIcon downloads and checks it.
	Icon       string `json:"icon,omitempty"`
	IconSHA256 string `json:"icon_sha256,omitempty"`
	// Capabilities are what it can do, worked out from its file by the
	// registry; nil for a built-in or an index from before them.
	Capabilities *Capabilities `json:"capabilities,omitempty"`

	// Registry is which index it was read from.
	Registry string `json:"-"`
}

// Hosts are a file's hosts as an index lists them.
type Hosts struct {
	API    string   `json:"api"`
	Follow []string `json:"follow,omitempty"`
	SSH    string   `json:"ssh,omitempty"`
}

// Checked is when a maintainer last ran the file against the service.
type Checked struct {
	Date   string `json:"date"`
	Conch  string `json:"conch"`
	Detail string `json:"detail,omitempty"`
}

// Revocation takes a version out of use, saying why.
type Revocation struct {
	Version int    `json:"version"`
	Reason  string `json:"reason"`
}

// DisplayName is how people write it.
func (e Entry) DisplayName() string {
	if e.Label != "" {
		return e.Label
	}
	return e.Name
}

// RevokedReason is why version is revoked, "" when it is not.
func (e Entry) RevokedReason(version int) string {
	for _, r := range e.Revoked {
		if r.Version == version {
			return r.Reason
		}
	}
	return ""
}

// Index is a registry's list, and whether it was signed.
type Index struct {
	Format int `json:"format"`
	// Kinds are the registry's folders, in the order it lists them, with
	// what people call each: an index from before them has none.
	Kinds   []KindInfo `json:"kinds,omitempty"`
	Entries []Entry    `json:"entries"`
	// Disabled are listed but cannot be installed yet: a sandbox provider
	// with no [ssh], whose machines conch could make and not reach.
	Disabled []Entry `json:"disabled,omitempty"`
	Registry string  `json:"-"`
	Signed   bool    `json:"-"`
	// Cached says this is the last index read, the registry being out of
	// reach (IndexOrCached).
	Cached bool `json:"-"`
}

// Find is the entry for name.
func (ix *Index) Find(name string) (Entry, bool) {
	for _, e := range ix.Entries {
		if e.Name == name {
			return e, true
		}
	}
	return Entry{}, false
}

// Search is the entries whose kind, name or label holds term; all of
// them for "".
func (ix *Index) Search(term string) []Entry { return search(ix.Entries, term) }

// SearchDisabled is Search over the entries that cannot be installed yet.
func (ix *Index) SearchDisabled(term string) []Entry { return search(ix.Disabled, term) }

func search(entries []Entry, term string) []Entry {
	term = strings.ToLower(strings.TrimSpace(term))
	var out []Entry
	for _, e := range entries {
		if term == "" || e.Kind == term || strings.Contains(e.Name, term) || strings.Contains(strings.ToLower(e.Label), term) {
			out = append(out, e)
		}
	}
	return out
}

const (
	maxIndex = 1 << 20
	maxSig   = 1 << 10
	maxFile  = 256 << 10
)

var registryClient = &http.Client{Timeout: 20 * time.Second}

func registryGet(ctx context.Context, r Registry, path string, limit int64) ([]byte, int, error) {
	u, err := url.Parse(r.URL)
	if err != nil || !(u.Scheme == "https" || u.Scheme == "http" && loopback(u.Hostname())) {
		return nil, 0, fmt.Errorf("the registry %s (%q) is not an https:// URL", r.Name, r.URL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.URL+"/"+path, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", "conch")
	resp, err := registryClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("the registry %s: %w", r.Name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, fmt.Errorf("the registry %s: %s answered %s", r.Name, path, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, 0, fmt.Errorf("the registry %s: %w", r.Name, err)
	}
	if int64(len(data)) > limit {
		return nil, 0, fmt.Errorf("the registry %s: %s is over %d bytes", r.Name, path, limit)
	}
	return data, resp.StatusCode, nil
}

// IndexFormat is the newest index this conch reads. Format 1 is the
// sandbox providers' index, read as entries of kind sandbox.
const IndexFormat = 2

// FetchIndex reads a registry's index, checking its signature when a key
// is known for it.
func FetchIndex(ctx context.Context, r Registry) (*Index, error) {
	data, signed, err := FetchIndexData(ctx, r)
	if err != nil {
		return nil, err
	}
	ix, err := ParseIndex(data)
	if err != nil {
		return nil, fmt.Errorf("the registry %s's index: %w", r.Name, err)
	}
	keepIndex(r, data, signed)
	ix.Registry, ix.Signed = r.Name, signed
	for i := range ix.Entries {
		ix.Entries[i].Registry = r.Name
	}
	for i := range ix.Disabled {
		ix.Disabled[i].Registry = r.Name
	}
	return ix, nil
}

// FetchIndexData is a registry's index as it came, checked against its
// signature when a key is known for it — for the sandbox providers'
// installer too, which reads the same index.
func FetchIndexData(ctx context.Context, r Registry) ([]byte, bool, error) {
	data, _, err := registryGet(ctx, r, "index.json", maxIndex)
	if err != nil {
		return nil, false, err
	}
	if r.Key == "" {
		return data, false, nil
	}
	sig, status, err := registryGet(ctx, r, "index.json.sig", maxSig)
	switch {
	case status == http.StatusNotFound:
		return nil, false, fmt.Errorf("the registry %s's index is not signed, and conch knows its key: nothing is read from it", r.Name)
	case err != nil:
		return nil, false, err
	}
	if err := Verify(r.Key, data, sig); err != nil {
		return nil, false, fmt.Errorf("the registry %s's index: %w: nothing is read from it", r.Name, err)
	}
	return data, true, nil
}

// ParseIndex reads an index of either format.
func ParseIndex(data []byte) (*Index, error) {
	var head struct {
		Format    int `json:"format"`
		Providers []struct {
			Name      string   `json:"name"`
			Label     string   `json:"label"`
			KeyEnv    string   `json:"key_env"`
			APIHost   string   `json:"api_host"`
			SSHHost   string   `json:"ssh_host"`
			Format    int      `json:"format"`
			Note      string   `json:"note"`
			File      string   `json:"file"`
			SHA256    string   `json:"sha256"`
			AdaptedBy string   `json:"adapted_by"`
			Adapted   string   `json:"adapted"`
			Docs      string   `json:"docs"`
			Checked   *Checked `json:"checked"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, err
	}
	var ix Index
	switch head.Format {
	case 1:
		ix.Format = 1
		for _, p := range head.Providers {
			e := Entry{Kind: KindSandbox, Name: p.Name, Label: p.Label, KeyEnv: p.KeyEnv, Format: p.Format, File: p.File, SHA256: p.SHA256,
				Hosts: Hosts{API: p.APIHost, SSH: p.SSHHost}, Note: p.Note, Docs: p.Docs, AdaptedBy: p.AdaptedBy, Adapted: p.Adapted,
				Checked: p.Checked, Tier: "community"}
			if p.Checked != nil {
				e.Tier = "verified"
			}
			ix.Entries = append(ix.Entries, e)
		}
	case 2:
		if err := json.Unmarshal(data, &ix); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("format %d; this conch reads up to %d: update conch", head.Format, IndexFormat)
	}
	// A name that could not be a file's is left out: it would be made a
	// path here, and no conch could install it.
	bad := func(e Entry) bool { return CheckName(e.Name) != nil }
	ix.Entries = slices.DeleteFunc(ix.Entries, bad)
	ix.Disabled = slices.DeleteFunc(ix.Disabled, bad)
	sort.Slice(ix.Entries, func(i, j int) bool { return ix.Entries[i].Name < ix.Entries[j].Name })
	sort.Slice(ix.Disabled, func(i, j int) bool { return ix.Disabled[i].Name < ix.Disabled[j].Name })
	return &ix, nil
}

// Sign signs data with a private key (base64 of its 32-byte seed),
// returning the signature as base64: what the registry's CI writes to
// index.json.sig.
func Sign(private string, data []byte) (string, error) {
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(private))
	if err != nil || len(seed) != ed25519.SeedSize {
		return "", errors.New("the private key is not a base64 ed25519 seed")
	}
	return base64.StdEncoding.EncodeToString(ed25519.Sign(ed25519.NewKeyFromSeed(seed), data)), nil
}

// Verify checks a base64 signature of data against a base64 public key.
func Verify(public string, data, sig []byte) error {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(public))
	if err != nil || len(key) != ed25519.PublicKeySize {
		return errors.New("its key is not a base64 ed25519 public key")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || !ed25519.Verify(key, data, raw) {
		return errors.New("its signature does not match")
	}
	return nil
}

// NewKey makes a signing key pair, base64: the private seed for the
// registry's CI, and the public key for conch.
func NewKey() (private, public string, err error) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(priv.Seed()), base64.StdEncoding.EncodeToString(pub), nil
}

// Record is what was installed from a registry: which version, its hash
// as it came, the hosts the person agreed to, and — after a refresh of
// the index — why that version was revoked.
type Record struct {
	Registry  string `json:"registry"`
	Version   int    `json:"version"`
	SHA256    string `json:"sha256"`
	Hosts     Hosts  `json:"hosts"`
	Installed string `json:"installed"`
	Revoked   string `json:"revoked,omitempty"`
	// Capabilities are the registry's for the file as it came
	// (CapabilitiesOf); nil for one installed before it said.
	Capabilities *Capabilities `json:"capabilities,omitempty"`
}

func recordsPath() string { return filepath.Join(Dir(), "registry.json") }

// Records is every integration installed from a registry; none for a
// missing file.
func Records() (map[string]Record, error) {
	m := map[string]Record{}
	data, err := os.ReadFile(recordsPath())
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", recordsPath(), err)
	}
	return m, nil
}

func writeRecords(m map[string]Record) error {
	data, _ := json.MarshalIndent(m, "", "  ")
	return writeAtomic(recordsPath(), append(data, '\n'))
}

// FetchFile downloads e's file and checks it is the one the index lists,
// byte for byte, and that it is one this conch can use as listed: its
// kind, its name, no host the index did not show.
func FetchFile(ctx context.Context, r Registry, e Entry) ([]byte, *File, error) {
	switch {
	case e.BuiltIn:
		return nil, nil, fmt.Errorf("%s is built into conch: there is nothing to install", e.DisplayName())
	case e.Kind == KindSandbox:
		return nil, nil, fmt.Errorf("%s is a sandbox provider: conch sandbox provider install %s", e.Name, e.Name)
	case contracts[e.Kind].required == nil:
		return nil, nil, fmt.Errorf("%s is kind %q, which this conch does not read: update conch", e.Name, e.Kind)
	case e.Format > Format:
		return nil, nil, fmt.Errorf("%s's file is format %d; this conch reads up to %d: update conch", e.Name, e.Format, Format)
	case !strings.HasPrefix(e.File, "integrations/") || strings.Contains(e.File, ".."):
		return nil, nil, fmt.Errorf("the index lists %s's file at %q", e.Name, e.File)
	}
	data, _, err := registryGet(ctx, r, e.File, maxFile)
	if err != nil {
		return nil, nil, err
	}
	if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != e.SHA256 {
		return nil, nil, fmt.Errorf("%s's file is not the one the index lists (its hash differs): try again in a minute, as the registry may be publishing", e.Name)
	}
	f, problems, err := Parse(data)
	switch {
	case err != nil:
		return nil, nil, fmt.Errorf("%s's file: %w", e.Name, err)
	case len(problems) > 0:
		return nil, nil, fmt.Errorf("%s's file cannot be used here: %s", e.Name, strings.Join(problems, "; "))
	case f.Name != e.Name || f.Kind != e.Kind:
		return nil, nil, fmt.Errorf("%s's file is %s %q, not what the index lists", e.Name, f.Kind, f.Name)
	}
	if f.KeyEnv != e.KeyEnv {
		return nil, nil, fmt.Errorf("%s's file reads your key from $%s, not the $%s the index lists: not installed", e.Name, f.KeyEnv, e.KeyEnv)
	}
	if f.Auth.UserEnv != e.UserEnv {
		return nil, nil, fmt.Errorf("%s's file reads a user name from $%s, not the $%s the index lists: not installed", e.Name, f.Auth.UserEnv, e.UserEnv)
	}
	if extra := beyond(f.Hosts(), e.Hosts); len(extra) > 0 {
		return nil, nil, fmt.Errorf("%s's file reaches %s, which the index does not show", e.Name, strings.Join(extra, ", "))
	}
	return data, f, nil
}

// beyond is the hosts a file reaches that the listed ones do not cover.
func beyond(hosts []string, listed Hosts) []string {
	ok := map[string]bool{strings.ToLower(listed.API): true}
	for _, h := range listed.Follow {
		ok[strings.ToLower(h)] = true
	}
	var out []string
	for _, h := range hosts {
		if !ok[strings.ToLower(h)] {
			out = append(out, h)
		}
	}
	return out
}

// HostsChanged says whether e reaches hosts the installed version did
// not, which the person must agree to again before an update.
func HostsChanged(rec Record, e Entry) bool {
	was := append([]string{rec.Hosts.API}, rec.Hosts.Follow...)
	return len(beyond(append([]string{e.Hosts.API}, e.Hosts.Follow...), Hosts{API: was[0], Follow: was[1:]})) > 0
}

// Install keeps a file fetched for e, recording where it came from. A
// file somebody edited since it was installed is not replaced: their
// change is theirs.
func Install(e Entry, data []byte) error {
	recs, err := Records()
	if err != nil {
		return err
	}
	path := filepath.Join(Dir(), e.Name+".toml")
	if rec, ok := recs[e.Name]; ok {
		if edited, err := editedSince(path, rec); err != nil {
			return err
		} else if edited {
			return fmt.Errorf("%s was edited here since it was installed; remove it first to take the registry's (conch integration remove %s)", path, e.Name)
		}
	} else if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s is a file of your own, not one from a registry; remove it first to install over it", path)
	}
	if err := writeAtomic(path, data); err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	recs[e.Name] = Record{Registry: e.Registry, Version: e.Version, SHA256: hex.EncodeToString(sum[:]), Hosts: e.Hosts,
		Installed: time.Now().UTC().Format("2006-01-02"), Capabilities: e.Capabilities}
	Forget()
	return writeRecords(recs)
}

// Remove deletes an installed file and its record.
func Remove(name string) error {
	if err := CheckName(name); err != nil {
		return err
	}
	path := filepath.Join(Dir(), name+".toml")
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	} else if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s is not installed", name)
	}
	recs, err := Records()
	if err != nil {
		return err
	}
	delete(recs, name)
	Forget()
	return writeRecords(recs)
}

// Edited says whether name's file is no longer as it came.
func Edited(name string) (bool, error) {
	recs, err := Records()
	if err != nil {
		return false, err
	}
	rec, ok := recs[name]
	if !ok {
		return false, nil
	}
	return editedSince(filepath.Join(Dir(), name+".toml"), rec)
}

func editedSince(path string, rec Record) (bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]) != rec.SHA256, nil
}

// MarkRevoked writes down, for every installed file from ix's registry,
// whether its version is revoked; All then leaves a revoked one out and
// says why. It returns the names newly revoked.
func MarkRevoked(ix *Index) ([]string, error) {
	recs, err := Records()
	if err != nil {
		return nil, err
	}
	var newly []string
	changed := false
	for name, rec := range recs {
		if rec.Registry != ix.Registry {
			continue
		}
		e, ok := ix.Find(name)
		reason := ""
		if ok {
			reason = e.RevokedReason(rec.Version)
		}
		if reason != rec.Revoked {
			if reason != "" {
				newly = append(newly, name)
			}
			rec.Revoked = reason
			recs[name] = rec
			changed = true
		}
	}
	sort.Strings(newly)
	if !changed {
		return nil, nil
	}
	Forget()
	return newly, writeRecords(recs)
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

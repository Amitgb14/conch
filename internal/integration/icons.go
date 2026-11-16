package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// KindInfo is one of the registry's kinds: a top-level folder of it, and
// what people call it.
type KindInfo struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
	About string `json:"about,omitempty"`
}

// maxIcon is the largest icon the registry takes.
const maxIcon = 32 << 10

// iconPath is where an index may say an icon is: beside it, by name.
var iconPath = regexp.MustCompile(`^icons/[a-z][a-z0-9-]{1,30}\.(svg|png)$`)

var oldIcon = regexp.MustCompile(`^[0-9a-f]{12}\.(svg|png)$`)

// IconsDir is where downloaded icons are kept.
func IconsDir() string { return filepath.Join(Dir(), "icons") }

// FetchIcon is the local copy of e's icon, downloaded from r the first
// time and checked against the hash the index gives — so a registry's
// signature covers its icons too. "" is an entry with no icon. The name
// carries the hash, so a new icon is a new file and the old one goes.
func FetchIcon(ctx context.Context, r Registry, e Entry) (string, error) {
	if e.Icon == "" {
		return "", nil
	}
	if !iconPath.MatchString(e.Icon) || CheckName(e.Name) != nil {
		return "", fmt.Errorf("%s's icon: the index names the path %q, not icons/NAME.svg or .png", e.Name, e.Icon)
	}
	if len(e.IconSHA256) != 64 {
		return "", fmt.Errorf("%s's icon: the index gives no hash for it", e.Name)
	}
	ext := path.Ext(e.Icon)
	local := filepath.Join(IconsDir(), e.Name+"-"+e.IconSHA256[:12]+ext)
	if _, err := os.Stat(local); err == nil {
		return local, nil
	}
	data, _, err := registryGet(ctx, r, e.Icon, maxIcon)
	if err != nil {
		return "", fmt.Errorf("%s's icon: %w", e.Name, err)
	}
	got := sha256.Sum256(data)
	if hex.EncodeToString(got[:]) != e.IconSHA256 {
		return "", fmt.Errorf("%s's icon is not the one the index's hash names: not kept", e.Name)
	}
	switch ext {
	case ".png":
		if !bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) {
			return "", fmt.Errorf("%s's icon is not a PNG", e.Name)
		}
	case ".svg":
		if !bytes.Contains(bytes.ToLower(data[:min(len(data), 512)]), []byte("<svg")) {
			return "", fmt.Errorf("%s's icon is not an SVG", e.Name)
		}
	}
	if err := os.MkdirAll(IconsDir(), 0o700); err != nil {
		return "", err
	}
	if err := writeFileAtomic(local, data); err != nil {
		return "", err
	}
	// The one it replaces, if any.
	// The one it replaces: NAME-HASH12.ext, which another name with this
	// one as its start (circleci-x) cannot be mistaken for.
	old, _ := filepath.Glob(filepath.Join(IconsDir(), e.Name+"-*"))
	for _, o := range old {
		if o != local && oldIcon.MatchString(strings.TrimPrefix(filepath.Base(o), e.Name+"-")) {
			_ = os.Remove(o)
		}
	}
	return local, nil
}

func writeFileAtomic(p string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(p), ".icon-*")
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
	return os.Rename(tmp.Name(), p)
}

// indexCache is where a registry's last index is kept.
func indexCache(r Registry) string {
	name := regexp.MustCompile(`[^a-zA-Z0-9-]+`).ReplaceAllString(r.Name, "_")
	return filepath.Join(Dir(), "index-cache", name+".json")
}

type cachedIndex struct {
	Signed bool            `json:"signed"`
	Data   json.RawMessage `json:"data"`
}

// keepIndex keeps an index just read, for when the registry is out of
// reach. Failing to is no failure of the read.
func keepIndex(r Registry, data []byte, signed bool) {
	b, err := json.Marshal(cachedIndex{Signed: signed, Data: data})
	if err != nil || os.MkdirAll(filepath.Dir(indexCache(r)), 0o700) != nil {
		return
	}
	_ = writeFileAtomic(indexCache(r), b)
}

// IndexOrCached is r's index, or the last one read when r cannot be
// reached — for drawing the catalog's icons and kinds offline. Installing
// reads the registry itself (FetchIndex), never this.
func IndexOrCached(ctx context.Context, r Registry) (*Index, error) {
	ix, err := FetchIndex(ctx, r)
	if err == nil {
		return ix, nil
	}
	b, rerr := os.ReadFile(indexCache(r))
	if rerr != nil {
		return nil, err
	}
	var c cachedIndex
	if json.Unmarshal(b, &c) != nil {
		return nil, err
	}
	cached, perr := ParseIndex(c.Data)
	if perr != nil {
		return nil, errors.Join(err, perr)
	}
	cached.Registry, cached.Signed, cached.Cached = r.Name, c.Signed, true
	for i := range cached.Entries {
		cached.Entries[i].Registry = r.Name
	}
	return cached, nil
}

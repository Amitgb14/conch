package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Amitgb14/conch/internal/config"
)

// conch had providers of its own once — Daytona, boat.dev, Tensorlake —
// and now has none: they are files in conch-integrations like any other.
// Machines made by those built-ins still name them, and config.toml still
// has their keys, so the first time one is needed and no file is
// installed, its file is installed from the registry without asking.
//
// Without asking is only right because nothing new is sent anywhere: the
// file must send the key from the same variable to the same host the
// built-in did. A file that names another is refused here and left for
// conch sandbox provider install, which shows its hosts and asks.
//
// The label is for a machine that names one before its file is here. The
// variables after it are what the built-in read when config.toml did not
// say (formerEnv): a self-hosted Daytona was reached by DAYTONA_API_URL,
// and without it the key went to the cloud.
var former = map[string]formerProvider{
	"daytona":    {api: "app.daytona.io", keyEnv: "DAYTONA_API_KEY", label: "Daytona", urlEnv: "DAYTONA_API_URL", regionEnv: "DAYTONA_TARGET"},
	"boat":       {api: "boat.dev", keyEnv: "BOAT_API_KEY", label: "boat.dev", urlEnv: "BOAT_API_URL"},
	"tensorlake": {api: "api.tensorlake.ai", keyEnv: "TENSORLAKE_API_KEY", label: "Tensorlake", urlEnv: "TENSORLAKE_API_URL", sshEnv: "TENSORLAKE_SSH_HOST"},
}

type formerProvider struct {
	api, keyEnv, label        string
	urlEnv, regionEnv, sshEnv string
}

// formerEnv lays the variables a former built-in read under cfg, as it
// did: config.toml wins, the variable fills what it leaves empty. The ssh
// host had no key in config.toml, so its variable is the only say.
func formerEnv(f *ProviderFile, cfg config.ProviderCfg) config.ProviderCfg {
	was, ok := former[f.Name]
	if !ok {
		return cfg
	}
	if cfg.APIURL == "" && was.urlEnv != "" {
		cfg.APIURL = strings.TrimSpace(os.Getenv(was.urlEnv))
	}
	if cfg.RegionOr() == "" && was.regionEnv != "" {
		cfg.Region = strings.TrimSpace(os.Getenv(was.regionEnv))
	}
	if was.sshEnv != "" {
		if h := strings.TrimSpace(os.Getenv(was.sshEnv)); h != "" {
			f.SSH.Host = h
		}
	}
	return cfg
}

// ErrDropped is a provider conch no longer supports at all.
var ErrDropped = errors.New("no longer supported")

// dropped are providers conch had and dropped, with what to do about a
// sandbox that is still there.
var dropped = map[string]string{
	"devin": "Devin Cloud is no longer a sandbox provider in conch: remove the machine here, and end its sandbox in Devin's own console (app.devin.ai) so it stops costing",
}

// Dropped says why a provider conch had is gone, and what to do about a
// sandbox of it, or false for any other name.
func Dropped(name string) (string, bool) {
	why, ok := dropped[name]
	return why, ok
}

// adoptTimeout bounds asking the registry for a former built-in's file.
const adoptTimeout = 30 * time.Second

// adopting holds a lock per former built-in, so two Opens at once fetch
// and install its file once: the second finds it there.
var adopting struct {
	sync.Mutex
	names map[string]*sync.Mutex
}

// adopt installs a former built-in's file from the registry, unless
// another Open just did.
func adopt(name string) error {
	want, ok := former[name]
	if !ok {
		return fmt.Errorf("unknown sandbox provider %q", name)
	}
	adopting.Lock()
	if adopting.names == nil {
		adopting.names = map[string]*sync.Mutex{}
	}
	mu := adopting.names[name]
	if mu == nil {
		mu = &sync.Mutex{}
		adopting.names[name] = mu
	}
	adopting.Unlock()
	mu.Lock()
	defer mu.Unlock()
	if !fileMissing(name) {
		return nil
	}
	how := fmt.Sprintf("conch sandbox provider install %s installs it", name)
	ctx, cancel := context.WithTimeout(context.Background(), adoptTimeout)
	defer cancel()
	ix, err := FetchIndex(ctx)
	if err != nil {
		return fmt.Errorf("%s is a provider file now, and the registry could not be read for it (%v): %s", name, err, how)
	}
	e, ok := ix.Find(name)
	if !ok {
		return fmt.Errorf("%s is a provider file now, and the registry has none: %s once it does, or conch sandbox adapt makes one", name, how)
	}
	if e.Format > ProviderFormat {
		return fmt.Errorf("%s's file needs a newer conch (format %d; this one reads up to %d): conch update", name, e.Format, ProviderFormat)
	}
	data, err := FetchProviderFile(ctx, e)
	if err != nil {
		return fmt.Errorf("%s's file: %w; %s", name, err, how)
	}
	f, problems, err := ParseProviderFile(name, data)
	switch {
	case err != nil:
		return fmt.Errorf("%s's file: %w", name, err)
	case len(problems) > 0:
		return fmt.Errorf("%s's file cannot be used here: %v", name, problems)
	}
	if api, _ := FileHosts(f); api != want.api || f.KeyEnv != want.keyEnv {
		return fmt.Errorf("%s's file in the registry sends $%s to %s, not $%s to %s as conch's own did, so it is not installed unasked: %s, after reading what it says",
			name, f.KeyEnv, api, want.keyEnv, want.api, how)
	}
	_, err = InstallRegistryFile(name, data, e.Capabilities)
	return err
}

// fileMissing says whether the provider has no file here.
func fileMissing(name string) bool {
	_, err := os.Stat(ProviderFilePath(name))
	return errors.Is(err, os.ErrNotExist)
}

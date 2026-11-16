package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"
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
// The label is for a machine that names one before its file is here.
var former = map[string]struct{ api, keyEnv, label string }{
	"daytona":    {"app.daytona.io", "DAYTONA_API_KEY", "Daytona"},
	"boat":       {"boat.dev", "BOAT_API_KEY", "boat.dev"},
	"tensorlake": {"api.tensorlake.ai", "TENSORLAKE_API_KEY", "Tensorlake"},
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

// adopt installs a former built-in's file from the registry.
func adopt(name string) error {
	want, ok := former[name]
	if !ok {
		return fmt.Errorf("unknown sandbox provider %q", name)
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

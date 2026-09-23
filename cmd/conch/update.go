package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/buildinfo"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/remote"
	"github.com/Amitgb14/conch/internal/update"
)

const updateUsage = `usage: conch update [VERSION | latest | list | rollback]`

// runUpdate replaces the running executable with another release: the
// latest upstream by default, a named one — newer or older — or the one
// conch ran before the last update. A running server keeps its old binary
// until it is reloaded, which this does when one is listening.
func runUpdate(args []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	if len(args) > 1 {
		return fmt.Errorf("%s", updateUsage)
	}
	arg := ""
	if len(args) == 1 {
		arg = strings.TrimSpace(args[0])
	}
	switch arg {
	case "list", "ls", "--list", "releases":
		return listReleases(ctx)
	case "rollback", "back", "--rollback", "downgrade":
		return rollback(ctx)
	case "-h", "--help", "help":
		fmt.Println(updateUsage)
		return nil
	}
	if strings.HasPrefix(arg, "-") {
		return fmt.Errorf("%s", updateUsage)
	}

	version := strings.TrimPrefix(arg, "v")
	if version == "" || version == "latest" {
		latest, err := remote.LatestRelease(ctx)
		if err != nil {
			return err
		}
		version = latest
		if version == proto.Version {
			fmt.Printf("conch %s is the latest release\n", version)
			return nil
		}
	} else if version == proto.Version {
		fmt.Printf("conch %s is already installed\n", version)
		return nil
	}
	return install(ctx, version)
}

// install puts version over this executable and reloads the local server
// onto it. Going back to an older version is the same swap, said
// differently.
func install(ctx context.Context, version string) error {
	exe, err := update.Executable()
	if err != nil {
		return err
	}
	back := update.Compare(version, proto.Version) < 0
	if kept, ok := update.Kept(version); ok {
		fmt.Fprintf(os.Stderr, "installing the conch %s kept at %s\n", version, kept)
	} else {
		fmt.Fprintf(os.Stderr, "downloading conch %s for %s\n", version, buildinfo.Platform())
	}
	if err := update.InstallRelease(ctx, version, exe); err != nil {
		return err
	}
	what := "updated"
	if back {
		what = "moved back"
	}
	fmt.Printf("%s %s: %s → %s\n", what, exe, proto.Version, version)
	c, err := connect(false)
	if err != nil {
		return nil // no server running: the next one starts on the new build
	}
	nc, err := reloadServer(c, exe)
	if err != nil {
		fmt.Printf("the server keeps the old build: %v\n", err)
		return nil
	}
	nc.Close()
	fmt.Println("server reloaded onto it; panes keep running")
	if back {
		fmt.Printf("back on %s: conch update returns to the latest release\n", version)
	} else {
		fmt.Println("remote machines: open conch and press u in the version box, or conch machine upgrade ID")
	}
	return nil
}

// rollback returns to the version conch ran before the last update. That
// step notes the version it leaves in turn, so a second rollback comes
// forward again.
func rollback(ctx context.Context) error {
	prev := update.Previous()
	switch {
	case prev == "":
		return fmt.Errorf("conch has no earlier version to go back to; name one: conch update VERSION")
	case prev == proto.Version:
		fmt.Printf("conch %s is already the version to go back to\n", prev)
		return nil
	}
	return install(ctx, prev)
}

// listReleases prints what is published upstream — the releases the install
// script and conch update choose from — newest first.
func listReleases(ctx context.Context) error {
	versions, err := update.Releases(ctx)
	if err != nil {
		return err
	}
	latest, _ := remote.LatestRelease(ctx) // marked when it can be found
	running := false
	for _, v := range versions {
		var marks []string
		if v == latest {
			marks = append(marks, "latest, what conch update installs")
		}
		mark := "  "
		if v == proto.Version {
			mark, running = "* ", true
			marks = append(marks, "running")
		}
		if _, ok := update.Kept(v); ok {
			marks = append(marks, "kept, no download needed")
		}
		line := mark + v
		if len(marks) > 0 {
			line += "  (" + strings.Join(marks, " · ") + ")"
		}
		fmt.Println(line)
	}
	fmt.Println()
	if !running {
		fmt.Printf("running %s\n", proto.Version)
	}
	if prev := update.Previous(); prev != "" {
		fmt.Printf("conch update rollback goes back to %s\n", prev)
	}
	fmt.Println("conch update VERSION installs any of these; an older one moves conch back")
	return nil
}

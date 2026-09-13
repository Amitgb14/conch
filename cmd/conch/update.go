package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/buildinfo"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/remote"
)

// runUpdate replaces the running executable with a release binary. A running
// server keeps its old binary until `conch server stop`.
func runUpdate(args []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	version := ""
	if len(args) > 0 {
		version = strings.TrimPrefix(args[0], "v")
	} else {
		latest, err := remote.LatestRelease(ctx)
		if err != nil {
			return err
		}
		version = latest
		if version == proto.Version {
			fmt.Printf("conch %s is the latest release\n", version)
			return nil
		}
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	fmt.Fprintf(os.Stderr, "downloading conch %s for %s\n", version, buildinfo.Platform())
	bin, err := remote.FetchRelease(ctx, version, buildinfo.Platform())
	if err != nil {
		return err
	}
	if err := remote.ReplaceExecutable(exe, bin); err != nil {
		return fmt.Errorf("replace %s: %w", exe, err)
	}
	fmt.Printf("updated %s: %s → %s\n", exe, proto.Version, version)
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
	fmt.Println("remote machines: open conch and press u in the version box, or conch machine upgrade ID")
	return nil
}

package gitx

import (
	"context"
	"strings"
)

// IgnoredDirs lists the directories in the checkout at dir that git ignores
// whole, relative to dir and slash-separated with no trailing slash. It is
// what a file watcher must not descend into: one entry stands for the whole
// of node_modules or target, so the answer stays small however large they are.
//
// Ignored files are left out — only directories, which are the ones that
// cost a watch each.
func IgnoredDirs(ctx context.Context, dir string) ([]string, error) {
	// --directory collapses a wholly ignored folder into one entry, so this
	// never walks into it; --no-empty-directory leaves out ones with nothing
	// in them, which cannot hold a change worth hearing about.
	out, err := run(ctx, dir, "ls-files", "--others", "--ignored", "--exclude-standard",
		"--directory", "--no-empty-directory", "-z")
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, p := range strings.Split(string(out), "\x00") {
		if strings.HasSuffix(p, "/") { // git marks directories, not files
			if p = strings.TrimSuffix(p, "/"); p != "" {
				dirs = append(dirs, p)
			}
		}
	}
	return dirs, nil
}

// IgnoredPaths lists what git ignores in the checkout at dir, relative to
// dir and slash-separated: files as they are, and a wholly ignored directory
// once, with a trailing slash, rather than everything in it.
func IgnoredPaths(ctx context.Context, dir string) ([]string, error) {
	out, err := run(ctx, dir, "ls-files", "--others", "--ignored", "--exclude-standard", "--directory", "-z")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths, nil
}

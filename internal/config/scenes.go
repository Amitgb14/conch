package config

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// BuiltInScenes are the scenes Conch.app draws itself, the names [app]
// background takes besides a file of your own.
var BuiltInScenes = []string{"reef", "pines", "lagoon", "stars", "board"}

// sceneName is what a scene file may be called: the same rule the app
// reads them by (ConchKit/UserScenes.swift).
var sceneName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// ScenesDir is where scenes of your own are kept: one file each, NAME.toml.
func ScenesDir() string { return filepath.Join(Dir(), "scenes") }

// UserScenes names the scene files in dir that the app would offer:
// sorted, named as the app allows, and none taking a built-in scene's
// name. Whether a file's contents are right is the app's to say.
func UserScenes(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".toml")
		if !ok || e.IsDir() || !sceneName.MatchString(name) || slices.Contains(BuiltInScenes, name) {
			continue
		}
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

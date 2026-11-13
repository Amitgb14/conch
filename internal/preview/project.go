package preview

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// detach starts an emulator in a session of its own, so it outlives the
// conch that started it and a ctrl+c in that terminal does not reach it.
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }

// Project is what kind of mobile app a folder holds, and how its dev
// server is started — what a preview needs running before there is
// anything of the app's to show.
type Project struct {
	Kind  string `json:"kind"`  // expo, react-native, flutter, xcode, android, "" for none found
	Label string `json:"label"` // as a person names it
	// Dev starts what reloads the app as files change; empty where the
	// platform's own IDE does that (Xcode).
	Dev string `json:"dev,omitempty"`
	// Platforms the app builds for, as Devices names them.
	Platforms []string `json:"platforms,omitempty"`
}

// Detect looks at a folder's own files, not its subfolders': a project
// is opened at its root, and walking node_modules would find every kind.
func Detect(dir string) Project {
	has := func(name string) bool {
		_, err := os.Stat(filepath.Join(dir, name))
		return err == nil
	}
	if b, err := os.ReadFile(filepath.Join(dir, "package.json")); err == nil {
		var pkg struct {
			Dependencies    map[string]string `json:"dependencies"`
			DevDependencies map[string]string `json:"devDependencies"`
		}
		if json.Unmarshal(b, &pkg) == nil {
			dep := func(n string) bool {
				_, a := pkg.Dependencies[n]
				_, b := pkg.DevDependencies[n]
				return a || b
			}
			both := []string{"ios", "android"}
			switch {
			case dep("expo"):
				return Project{Kind: "expo", Label: "Expo · React Native", Dev: "npx expo start", Platforms: both}
			case dep("react-native"):
				return Project{Kind: "react-native", Label: "React Native", Dev: "npx react-native start", Platforms: both}
			}
		}
	}
	if has("pubspec.yaml") {
		return Project{Kind: "flutter", Label: "Flutter", Dev: "flutter run", Platforms: []string{"ios", "android"}}
	}
	if globAny(dir, "*.xcworkspace", "*.xcodeproj") {
		return Project{Kind: "xcode", Label: "Xcode", Platforms: []string{"ios"}}
	}
	if has("settings.gradle") || has("settings.gradle.kts") || has("build.gradle") || has("build.gradle.kts") {
		if has("app") || mentionsAndroid(dir) {
			return Project{Kind: "android", Label: "Android · Gradle", Dev: "./gradlew installDebug", Platforms: []string{"android"}}
		}
	}
	return Project{}
}

func globAny(dir string, patterns ...string) bool {
	for _, p := range patterns {
		if m, _ := filepath.Glob(filepath.Join(dir, p)); len(m) > 0 {
			return true
		}
	}
	return false
}

// mentionsAndroid: a Gradle build with no app folder is still Android's
// when its build file applies the Android plugin; plain JVM builds are not.
func mentionsAndroid(dir string) bool {
	for _, n := range []string{"build.gradle", "build.gradle.kts"} {
		if b, err := os.ReadFile(filepath.Join(dir, n)); err == nil && strings.Contains(string(b), "com.android") {
			return true
		}
	}
	return false
}

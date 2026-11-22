package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// SetSandboxKey keeps a provider's API key in config.toml, under
// [sandbox.NAME] api_key, changing that one line and nothing else: Save
// rewrites the whole file, which is the settings screen's way but loses a
// person's comments, and a command that sets one key is not expected to.
// The edit is read back, and a file whose shape a line edit cannot be sure
// of (an inline table, dotted keys) is written by Save instead.
func SetSandboxKey(name, key string) error {
	old, err := os.ReadFile(File())
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	want := Default()
	if _, err := toml.Decode(string(old), &want); err != nil {
		// Saving over a file conch cannot read would lose it.
		return fmt.Errorf("read %s: %w; put it right first", File(), err)
	}
	if want.Keys.Prefix == "" {
		want.Keys.Prefix = Default().Keys.Prefix
	}
	if want.Sandbox == nil {
		want.Sandbox = SandboxCfg{}
	}
	pc := want.Sandbox[name]
	pc.APIKey = key
	want.Sandbox[name] = pc

	text := keyLine(string(old), name, key)
	got := Default()
	if _, err := toml.Decode(text, &got); err == nil {
		if got.Keys.Prefix == "" {
			got.Keys.Prefix = Default().Keys.Prefix
		}
		if reflect.DeepEqual(got, want) {
			if err := os.MkdirAll(Dir(), 0o700); err != nil {
				return err
			}
			return WriteFileAtomic(File(), []byte(text), 0o600)
		}
	}
	return Save(want)
}

var tableLine = regexp.MustCompile(`^\s*\[`)

// keyLine is text with [sandbox.NAME]'s api_key set: the line replaced
// where there is one, added under the table's header where there is not,
// and the table added at the end where there is none.
func keyLine(text, name, key string) string {
	header := regexp.MustCompile(`^\s*\[\s*sandbox\.` + regexp.QuoteMeta(name) + `\s*\]\s*(#.*)?$`)
	keyAt := regexp.MustCompile(`^(\s*)api_key\s*=`)
	set := "api_key = " + strconv.Quote(key)
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if !header.MatchString(l) {
			continue
		}
		for j := i + 1; j < len(lines) && !tableLine.MatchString(lines[j]); j++ {
			if m := keyAt.FindStringSubmatch(lines[j]); m != nil {
				lines[j] = m[1] + set
				return strings.Join(lines, "\n")
			}
		}
		lines = append(lines[:i+1], append([]string{set}, lines[i+1:]...)...)
		return strings.Join(lines, "\n")
	}
	var b bytes.Buffer
	b.WriteString(text)
	if text != "" {
		if !strings.HasSuffix(text, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "[sandbox.%s]\n%s\n", name, set)
	return b.String()
}

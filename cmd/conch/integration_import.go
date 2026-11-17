package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"slices"
	"strings"

	"github.com/Amitgb14/conch/internal/integration"
)

// importFacts are an import's, from its file when it is installed, else
// from the registry's entry: which reader, where it reads, and whether
// it is here.
type importFacts struct {
	reader     string
	min        int
	platforms  []string
	path       string
	found      []string
	needsNewer string
}

func importOf(f *integration.File, e integration.Entry, listed bool) importFacts {
	var x importFacts
	home, _ := os.UserHomeDir()
	switch {
	case f != nil && f.Kind == integration.KindImport:
		x = importFacts{reader: f.Source.Reader, min: f.Source.MinReader, platforms: f.Source.Platforms, path: f.Source.Path, found: f.Found(home)}
		x.needsNewer = integration.ReaderProblem(x.reader, x.min)
	case listed && e.Kind == integration.KindImport:
		x = importFacts{reader: e.Reader, min: e.MinReader, platforms: e.Platforms, needsNewer: e.NeedsNewer(), found: e.FoundEntry(home)}
	}
	return x
}

// words says what the import reads and that nothing leaves the machine.
func (x importFacts) words(label string) string {
	var b strings.Builder
	where := firstSetStr(x.path, "its own files")
	fmt.Fprintf(&b, "%s reads %s on this machine; nothing is sent anywhere.", label, where)
	switch {
	case x.needsNewer != "":
		fmt.Fprintf(&b, "\nThis conch cannot read it: %s.", x.needsNewer)
	case len(x.platforms) > 0 && !slices.Contains(x.platforms, runtime.GOOS):
		fmt.Fprintf(&b, "\nIt ran on %s only, not here.", strings.Join(x.platforms, ", "))
	case len(x.found) > 0:
		fmt.Fprintf(&b, "\nFound on this machine: conch import %s brings its conversations in.", x.reader)
	default:
		fmt.Fprintf(&b, "\nNot found on this machine. conch import %s reads it once it is.", x.reader)
	}
	return b.String()
}

// importState is an installed import's line in conch integration ls.
func importState(f *integration.File) string {
	home, _ := os.UserHomeDir()
	switch {
	case integration.ReaderProblem(f.Source.Reader, f.Source.MinReader) != "":
		return "needs a newer conch"
	case !f.RunsHere():
		return "not on " + runtime.GOOS
	case len(f.Found(home)) > 0:
		return "found on this machine"
	}
	return "not found on this machine"
}

// installImport installs an import: a file that says where another app
// keeps its conversations and which of conch's readers reads them. It
// has no key and sends nothing, so it is said rather than asked about
// hosts.
func installImport(ctx context.Context, reg integration.Registry, e integration.Entry, signed, yes bool) error {
	f := importFacts{reader: e.Reader, min: e.MinReader, platforms: e.Platforms}
	data, file, err := integration.FetchFile(ctx, reg, e)
	if err != nil {
		return err
	}
	f.path = file.Source.Path
	fmt.Printf("%s reads %s on this machine; nothing is sent anywhere.", e.DisplayName(), f.path)
	if !signed {
		fmt.Printf(" (The registry's index is unsigned.)")
	}
	if !yes {
		if !askYes(" Go on?") {
			return errors.New("nothing installed (-y installs without asking)")
		}
	} else {
		fmt.Println()
	}
	if err := integration.Install(e, data); err != nil {
		return err
	}
	fmt.Printf("installed %s %d (%s) · %s\n", e.Name, e.Version, e.Kind, importState(file))
	return nil
}

package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The commands README.md and AGENTS.md tell somebody to run are the ones
// the Makefile has. Written down because a recipe moves and its copy in
// the prose does not: `make test` gained `-timeout 20m` — the default 10m
// is one slow runner short for internal/server — while the README went on
// saying `go test -race ./...`, which is a different command that fails
// where the real one passes.

// docMake is a line of a code block that runs a make target, with what the
// prose says it does after the #.
var docMake = regexp.MustCompile(`(?m)^make ([a-z][a-z-]*) *(?:# *(.*))?$`)

// docMakeInline is a target named in a sentence, in backticks. Only in
// backticks: "make socket dirs with os.MkdirTemp" is not a target.
var docMakeInline = regexp.MustCompile("`make ([a-z][a-z-]*)`")

// docCommand is a whole command inside a comment — `go vet ./...` — told
// apart from a list of words by the package pattern it ends with. A comma
// or a dash ends it, so "gofmt, go vet, staticcheck" is prose, not one
// command being quoted.
var docCommand = regexp.MustCompile(`go [a-z]+[^,;—]*\./\.\.\.`)

func TestDocsRunRealMakeTargets(t *testing.T) {
	recipes := makeRecipes(t)
	for _, doc := range []string{"../../README.md", "../../AGENTS.md"} {
		body, err := os.ReadFile(doc)
		if err != nil {
			t.Fatal(err)
		}
		found := 0
		for _, m := range docMake.FindAllStringSubmatch(string(body), -1) {
			found++
			target, comment := m[1], m[2]
			recipe, ok := recipes[target]
			if !ok {
				t.Errorf("%s: make %s, which the Makefile has no target for", doc, target)
				continue
			}
			// What the comment quotes has to be the command that runs.
			for _, cmd := range docCommand.FindAllString(comment, -1) {
				if !strings.Contains(recipe, cmd) {
					t.Errorf("%s: make %s is said to run %q; the Makefile runs:\n%s", doc, target, cmd, recipe)
				}
			}
		}
		for _, m := range docMakeInline.FindAllStringSubmatch(string(body), -1) {
			found++
			if _, ok := recipes[m[1]]; !ok {
				t.Errorf("%s: `make %s`, which the Makefile has no target for", doc, m[1])
			}
		}
		if found == 0 {
			t.Errorf("%s: no make command found — the regexp or the document moved", doc)
		}
	}
}

// TestLintIsWhatCIChecksFirst: the README says `make lint` is what CI
// checks first, which is a claim about another file.
func TestLintIsWhatCIChecksFirst(t *testing.T) {
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(readme), "what CI checks first") {
		t.Skip("the README no longer claims it")
	}
	ci, err := os.ReadFile("../../.github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	lint := strings.Index(string(ci), "run: make lint")
	test := strings.Index(string(ci), "run: go test")
	switch {
	case lint < 0:
		t.Error("ci.yml does not run make lint")
	case test < 0:
		t.Error("ci.yml does not run the tests")
	case lint > test:
		t.Error("ci.yml runs the tests before make lint, so the README's \"checks first\" is wrong")
	}
}

// makeRecipes reads the Makefile as the lines of each target, which is all
// that is needed here: a recipe line begins with a tab.
func makeRecipes(t *testing.T) map[string]string {
	t.Helper()
	body, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	recipes := map[string]string{}
	target := ""
	for _, line := range strings.Split(string(body), "\n") {
		switch {
		case strings.HasPrefix(line, "\t"):
			if target != "" {
				recipes[target] += strings.TrimPrefix(line, "\t") + "\n"
			}
		case strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "":
			// A comment or a blank line leaves the target it is under,
			// since a recipe's own lines all start with a tab.
		default:
			target = ""
			if name, _, ok := strings.Cut(line, ":"); ok && !strings.ContainsAny(name, " \t=") && name != ".PHONY" {
				target = name
				if _, seen := recipes[name]; !seen {
					recipes[name] = ""
				}
			}
		}
	}
	if len(recipes) < 5 {
		t.Fatalf("read %d targets from the Makefile: %v", len(recipes), recipes)
	}
	return recipes
}

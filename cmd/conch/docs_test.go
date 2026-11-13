package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestDocsSidebarPages: every page the docs sidebar lists has a page.mdx,
// so a link added to the sidebar cannot lead to a 404 on the website.
func TestDocsSidebarPages(t *testing.T) {
	nav, err := os.ReadFile("../../web/src/lib/docs.ts")
	if err != nil {
		t.Fatal(err)
	}
	hrefs := regexp.MustCompile(`href: "(/docs[^"]*)"`).FindAllStringSubmatch(string(nav), -1)
	if len(hrefs) == 0 {
		t.Fatal("no pages found in docs.ts")
	}
	for _, m := range hrefs {
		dir := filepath.Join("../../web/src/app", strings.TrimPrefix(m[1], "/"))
		if _, err := os.Stat(filepath.Join(dir, "page.mdx")); err != nil {
			t.Errorf("sidebar lists %s, which has no page: %v", m[1], err)
		}
	}
}

// TestDocsComparePage: the comparison with other agent orchestrators is in
// the sidebar, has its table with a column for each tool, and says when it
// was written — what the others do changes weekly, so an undated table
// would read as current long after it is not.
func TestDocsComparePage(t *testing.T) {
	nav, err := os.ReadFile("../../web/src/lib/docs.ts")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(nav), `href: "/docs/compare"`) {
		t.Fatal("the docs sidebar does not list /docs/compare")
	}
	page, err := os.ReadFile("../../web/src/app/docs/compare/page.mdx")
	if err != nil {
		t.Fatal(err)
	}
	header := regexp.MustCompile(`(?m)^\|[^\n]*conch[^\n]*\|\n\| ?---`).Find(page)
	if header == nil {
		t.Fatal("no comparison table with a conch column")
	}
	for _, tool := range []string{"herdr", "T3 Code", "Orca"} {
		if !strings.Contains(string(header), tool) {
			t.Errorf("comparison table has no %s column: %s", tool, header)
		}
	}
	if !regexp.MustCompile(`As of \w+ \d{4}`).Match(page) {
		t.Error("comparison page does not say when it was written")
	}
	// Every row has as many cells as the header, or a cell renders under
	// the wrong tool.
	start := strings.Index(string(page), string(header))
	table, _, _ := strings.Cut(string(page)[start:], "\n\n")
	cells := func(l string) int { return strings.Count(strings.ReplaceAll(l, `\|`, ""), "|") }
	lines := strings.Split(strings.TrimSpace(table), "\n")
	for _, l := range lines[1:] {
		if cells(l) != cells(lines[0]) {
			t.Errorf("row has %d cells, header %d: %s", cells(l)-1, cells(lines[0])-1, l)
		}
	}
}

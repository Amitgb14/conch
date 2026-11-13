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

// TestDocsWhyPage: the page on what conch does that other agent
// orchestrators do not is in the sidebar, in place of the comparison table
// it replaced, and every point on it links to the page that explains it.
func TestDocsWhyPage(t *testing.T) {
	nav, err := os.ReadFile("../../web/src/lib/docs.ts")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(nav), `href: "/docs/why"`) {
		t.Fatal("the docs sidebar does not list /docs/why")
	}
	if strings.Contains(string(nav), "/docs/compare") {
		t.Error("the docs sidebar still lists /docs/compare")
	}
	if _, err := os.Stat("../../web/src/app/docs/compare"); err == nil {
		t.Error("the compare page is still there")
	}
	page, err := os.ReadFile("../../web/src/app/docs/why/page.mdx")
	if err != nil {
		t.Fatal(err)
	}
	points := regexp.MustCompile(`(?m)^## (.+)\n\n((?:.+\n?)+)`).FindAllStringSubmatch(string(page), -1)
	if len(points) < 5 {
		t.Fatalf("expected at least 5 points, found %d", len(points))
	}
	for _, p := range points {
		if !strings.Contains(p[2], "](/docs/") {
			t.Errorf("point %q links to no page explaining it", p[1])
		}
	}
	intro, err := os.ReadFile("../../web/src/app/docs/page.mdx")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(intro), "/docs/compare") || !strings.Contains(string(intro), "](/docs/why)") {
		t.Error("the introduction should link to /docs/why, not /docs/compare")
	}
}

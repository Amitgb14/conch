package main

import (
	"encoding/json"
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

// TestDocsComponents: every component a docs page uses is one the MDX map
// gives it, every screenshot it names exists, and every TUI screen it draws
// was captured — otherwise the page fails the website's build, which no Go
// test or CI job here runs.
func TestDocsComponents(t *testing.T) {
	mdx, err := os.ReadFile("../../web/src/mdx-components.tsx")
	if err != nil {
		t.Fatal(err)
	}
	shots, err := os.ReadFile("../../web/src/components/doc-shot.tsx")
	if err != nil {
		t.Fatal(err)
	}
	frames, err := os.ReadFile("../../web/src/components/landing/tui-frames.json")
	if err != nil {
		t.Fatal(err)
	}
	var tui struct {
		Frames map[string]json.RawMessage `json:"frames"`
	}
	if err := json.Unmarshal(frames, &tui); err != nil {
		t.Fatal(err)
	}
	registered := map[string]bool{}
	body := string(mdx)[strings.Index(string(mdx), "const components"):]
	for _, m := range regexp.MustCompile(`(?m)^  ([A-Z]\w*),$`).FindAllStringSubmatch(body, -1) {
		registered[m[1]] = true
	}
	pages, err := filepath.Glob("../../web/src/app/docs/*/page.mdx")
	if err != nil {
		t.Fatal(err)
	}
	pages = append(pages, "../../web/src/app/docs/page.mdx")
	tag := regexp.MustCompile(`<([A-Z]\w*)[\s/>]`)
	shot := regexp.MustCompile(`<Shot\s[^>]*?name="([^"]+)"`)
	screen := regexp.MustCompile(`<TuiShot\s[^>]*?id="([^"]+)"`)
	for _, p := range pages {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		// Code, fenced or inline, may show a <NAME> that is no component.
		src := regexp.MustCompile("(?s)```.*?```|`[^`\n]*`").ReplaceAllString(string(b), "")
		for _, m := range tag.FindAllStringSubmatch(src, -1) {
			if !registered[m[1]] {
				t.Errorf("%s uses <%s>, which mdx-components.tsx does not give it", p, m[1])
			}
		}
		for _, m := range shot.FindAllStringSubmatch(src, -1) {
			if !strings.Contains(string(shots), `"`+m[1]+`":`) {
				t.Errorf("%s shows screenshot %q, which doc-shot.tsx does not have", p, m[1])
			}
		}
		for _, m := range screen.FindAllStringSubmatch(src, -1) {
			if _, ok := tui.Frames[m[1]]; !ok {
				t.Errorf("%s draws TUI screen %q, which tui-frames.json does not have", p, m[1])
			}
		}
	}
}

// TestDocsPictures: the pages people read first carry pictures, not only
// prose — the introduction shows how conch fits together and what is new,
// and Why conch draws the autopilot's loop and shows the desktop app.
func TestDocsPictures(t *testing.T) {
	for page, want := range map[string][]string{
		"../../web/src/app/docs/page.mdx":        {"<Architecture", "<TuiShot", "<FeatureGrid"},
		"../../web/src/app/docs/why/page.mdx":    {"<FeatureGrid", "<AutopilotLoop", "<Shot"},
		"../../web/src/app/docs/tasks/page.mdx":  {"<TaskFlow"},
		"../../web/src/app/docs/agents/page.mdx": {"<AgentStates"},
	} {
		b, err := os.ReadFile(page)
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range want {
			if !strings.Contains(string(b), w) {
				t.Errorf("%s has no %s>", page, w)
			}
		}
	}
}

package main

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode"
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

// guides are the docs pages people read through rather than look things
// up in. They are kept short — what a feature is and how to use it, in a
// few lines beside a picture — because the long versions went unread. The
// reference pages (keys, command line, configuration, protocol) are tables
// to look things up in and are left out.
var guides = []string{
	"page.mdx", "installation/page.mdx", "quick-start/page.mdx", "why/page.mdx",
	"interface/page.mdx", "tasks/page.mdx", "agents/page.mdx", "agents-together/page.mdx",
	"sessions/page.mdx", "remote-machines/page.mdx", "sandboxes/page.mdx",
	"mac-app/page.mdx", "mobile-preview/page.mdx", "phone/page.mdx", "brain/page.mdx",
}

const (
	guideMaxWords     = 700 // a page's prose, outside code and components
	guideMaxParagraph = 60  // one paragraph, or one list item
)

// prose is what a reader reads on a page: no code blocks, no components,
// no metadata, no link targets.
func prose(page string) string {
	page = regexp.MustCompile("(?s)```.*?```").ReplaceAllString(page, "")
	page = regexp.MustCompile(`(?s)</?[A-Z]\w*[^>]*>`).ReplaceAllString(page, "")
	page = regexp.MustCompile(`(?m)^export const metadata.*$`).ReplaceAllString(page, "")
	page = regexp.MustCompile(`\]\([^)]*\)`).ReplaceAllString(page, "]")
	return page
}

// TestDocsGuidesAreShort: every guide page stays within its budget, so a
// page is a feature, a line on how to use it, and a picture.
func TestDocsGuidesAreShort(t *testing.T) {
	for _, g := range guides {
		b, err := os.ReadFile("../../web/src/app/docs/" + g)
		if err != nil {
			t.Fatal(err)
		}
		p := prose(string(b))
		if n := len(strings.Fields(p)); n > guideMaxWords {
			t.Errorf("%s: %d words of prose, keep it under %d", g, n, guideMaxWords)
		}
		for _, para := range regexp.MustCompile(`\n\s*\n|\n- |\n\d+\. `).Split(p, -1) {
			if strings.HasPrefix(strings.TrimSpace(para), "|") {
				continue // a table's rows are counted in the page's total
			}
			if n := len(strings.Fields(para)); n > guideMaxParagraph {
				t.Errorf("%s: a paragraph of %d words, keep each under %d: %.80s…", g, n, guideMaxParagraph, strings.TrimSpace(para))
			}
		}
	}
}

// slug is the id rehype-slug gives a heading: lower case, punctuation
// dropped, spaces as hyphens.
func slug(h string) string {
	h = strings.ToLower(strings.NewReplacer("`", "", "*", "").Replace(h))
	var b strings.Builder
	for _, r := range h {
		switch {
		case r == ' ':
			b.WriteRune('-')
		case r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		}
	}
	return b.String()
}

// TestDocsLinksResolve: every link to a docs page, from a page or from the
// website's components, reaches a page that is there and, with #, a
// heading on it — shortening a page must not leave links into what it cut.
func TestDocsLinksResolve(t *testing.T) {
	anchors := map[string]map[string]bool{}
	pages, _ := filepath.Glob("../../web/src/app/docs/*/page.mdx")
	pages = append(pages, "../../web/src/app/docs/page.mdx")
	heading := regexp.MustCompile(`(?m)^#{1,4} (.+)$`)
	for _, p := range pages {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		route := "/docs/" + strings.TrimSuffix(strings.TrimPrefix(filepath.ToSlash(p), "../../web/src/app/docs/"), "page.mdx")
		route = strings.TrimSuffix(route, "/")
		set := map[string]bool{}
		src := regexp.MustCompile("(?s)```.*?```").ReplaceAllString(string(b), "")
		for _, m := range heading.FindAllStringSubmatch(src, -1) {
			set[slug(strings.TrimSpace(m[1]))] = true
		}
		anchors[route] = set
	}
	link := regexp.MustCompile(`(?:\]\(|href[=:] ?")(/docs[^)"#\s]*)(?:#([^)"\s]+))?`)
	var sources []string
	sources = append(sources, pages...)
	_ = filepath.WalkDir("../../web/src/components", func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".tsx") {
			sources = append(sources, path)
		}
		return nil
	})
	sources = append(sources, "../../web/src/lib/docs.ts")
	checked := 0
	for _, s := range sources {
		b, err := os.ReadFile(s)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range link.FindAllStringSubmatch(string(b), -1) {
			checked++
			route := strings.TrimSuffix(m[1], "/")
			set, ok := anchors[route]
			if !ok {
				t.Errorf("%s links to %s, which is no docs page", s, m[1])
				continue
			}
			if m[2] != "" && !set[m[2]] {
				t.Errorf("%s links to %s#%s, which has no such heading", s, route, m[2])
			}
		}
	}
	if checked == 0 {
		t.Fatal("no docs links found — the regexp or the pages moved")
	}
}

// TestDocsHideCloud: Conch Cloud is not enabled in this release, so the
// website does not offer it. Its pages stay, for whoever turns it on, but
// the sidebar leaves them out, each says it is not enabled, and no other
// page — nor the site's components — names it, except the Command line
// page's own section about its commands.
func TestDocsHideCloud(t *testing.T) {
	nav, err := os.ReadFile("../../web/src/lib/docs.ts")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/docs/cloud", "/docs/cloud-machine"} {
		if strings.Contains(string(nav), `href: "`+p+`"`) {
			t.Errorf("the docs sidebar lists %s", p)
		}
		b, err := os.ReadFile("../../web/src/app" + p + "/page.mdx")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), "Conch Cloud is not enabled in this release") {
			t.Errorf("%s does not say Conch Cloud is not enabled", p)
		}
	}
	cloud := regexp.MustCompile(`(?i)conch cloud|/docs/cloud\b`)
	var files []string
	_ = filepath.WalkDir("../../web/src", func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && (strings.HasSuffix(path, ".mdx") || strings.HasSuffix(path, ".tsx") || strings.HasSuffix(path, ".ts")) {
			files = append(files, path)
		}
		return nil
	})
	for _, f := range files {
		if strings.Contains(f, "/docs/cloud/") || strings.Contains(f, "/docs/cloud-machine/") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		if strings.HasSuffix(f, "/docs/cli/page.mdx") {
			if i := strings.Index(src, "## Conch Cloud commands"); i >= 0 {
				src = src[:i] // the one place the commands are described
			}
		}
		for _, m := range cloud.FindAllStringIndex(src, -1) {
			line := strings.Count(src[:m[0]], "\n") + 1
			t.Errorf("%s:%d names Conch Cloud", f, line)
		}
	}
}

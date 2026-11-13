package main

import (
	"encoding/json"
	"image"
	_ "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Amitgb14/conch/internal/nodetest"
)

// The demo videos (web/scripts/demo-video) are drawn from a storyboard by
// hand on a Mac, never in CI, so a scene that names a picture somebody
// renamed, or zooms on a rectangle outside it, would only be found when
// somebody next needed the videos. These are the storyboard's rules.
type storyboard struct {
	Width, Height, FPS int
	Videos             []struct {
		Name, Title string
		Scenes      []struct {
			Kind    string
			Image   string
			Images  []string
			Focus   []float64
			Click   []float64
			Ring    bool
			Caption string
			Sub     string
			Title   string
			URL     string `json:"url"`
			Hold    float64
		}
	}
}

func loadStoryboard(t *testing.T) storyboard {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "web/scripts/demo-video/videos.json"))
	if err != nil {
		t.Fatal(err)
	}
	var sb storyboard
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields() // a misspelt key would be a scene silently missing its zoom
	if err := dec.Decode(&sb); err != nil {
		t.Fatalf("videos.json: %v", err)
	}
	return sb
}

func pngSize(t *testing.T, rel string) (int, int, bool) {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", rel))
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()
	c, format, err := image.DecodeConfig(f)
	if err != nil || format != "png" {
		t.Errorf("%s is not a PNG: %v", rel, err)
		return 0, 0, false
	}
	return c.Width, c.Height, true
}

func TestDemoVideoStoryboard(t *testing.T) {
	sb := loadStoryboard(t)
	if sb.Width != 1920 || sb.Height != 1080 || sb.FPS < 24 || sb.FPS > 60 {
		t.Errorf("frame is %dx%d at %d fps, want 1920x1080 at 24–60", sb.Width, sb.Height, sb.FPS)
	}
	if len(sb.Videos) < 3 {
		t.Fatalf("%d videos; the point is several", len(sb.Videos))
	}
	slug := regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	names := map[string]bool{}
	for _, v := range sb.Videos {
		if !slug.MatchString(v.Name) || names[v.Name] {
			t.Errorf("video name %q is not a unique file-safe slug", v.Name)
		}
		names[v.Name] = true
		if v.Title == "" {
			t.Errorf("%s: no title", v.Name)
		}
		n := len(v.Scenes)
		if n < 3 {
			t.Errorf("%s: %d scenes", v.Name, n)
			continue
		}
		// Opens on a title and ends on one, so a clip posted on its own says
		// what it is and where conch is.
		if v.Scenes[0].Kind != "title" || v.Scenes[n-1].Kind != "title" {
			t.Errorf("%s: must start and end with a title scene", v.Name)
		}
		total := 0.0
		for i, s := range v.Scenes {
			at := func(f string, a ...any) { t.Errorf("%s scene %d: "+f, append([]any{v.Name, i}, a...)...) }
			total += s.Hold
			if s.Hold < 1.5 || s.Hold > 8 {
				at("hold %.1fs: under 1.5s nobody reads it, over 8s it drags", s.Hold)
			}
			// A caption is read at a glance, on a phone, over a moving picture.
			if utf8.RuneCountInString(s.Caption) > 56 || utf8.RuneCountInString(s.Sub) > 90 || utf8.RuneCountInString(s.Title) > 48 {
				at("text too long to read in one look")
			}
			switch s.Kind {
			case "title":
				if s.Title == "" || s.Image != "" || len(s.Focus) > 0 || len(s.Click) > 0 {
					at("a title scene has a title and no picture")
				}
			case "phone":
				if len(s.Images) < 1 || len(s.Images) > 2 || s.Caption == "" {
					at("a phone scene shows one or two screens, with a caption")
				}
				for _, img := range s.Images {
					if _, _, ok := pngSize(t, img); !ok {
						at("%s is missing", img)
					}
				}
			case "window":
				if s.Caption == "" {
					at("no caption")
				}
				w, h, ok := pngSize(t, s.Image)
				if !ok {
					at("%s is missing", s.Image)
					continue
				}
				if len(s.Focus) != 0 && len(s.Focus) != 4 {
					at("focus is [x, y, w, h], got %v", s.Focus)
					continue
				}
				if s.Ring && len(s.Focus) == 0 {
					at("a ring needs a focus to go around")
				}
				if len(s.Focus) == 4 {
					x, y, fw, fh := s.Focus[0], s.Focus[1], s.Focus[2], s.Focus[3]
					if fw < 40 || fh < 20 || x < 0 || y < 0 || x+fw > float64(w) || y+fh > float64(h) {
						at("focus %v is outside %s (%dx%d)", s.Focus, s.Image, w, h)
					}
				}
				if len(s.Click) != 0 {
					if len(s.Click) != 2 {
						at("click is [x, y], got %v", s.Click)
						continue
					}
					cx, cy := s.Click[0], s.Click[1]
					inside := cx >= 0 && cy >= 0 && cx <= float64(w) && cy <= float64(h)
					if len(s.Focus) == 4 {
						f := s.Focus
						inside = cx >= f[0] && cy >= f[1] && cx <= f[0]+f[2] && cy <= f[1]+f[3]
					}
					if !inside {
						at("click %v lands outside what the camera shows", s.Click)
					}
				}
			default:
				at("unknown kind %q", s.Kind)
			}
		}
		if total < 10 || total > 60 {
			t.Errorf("%s runs %.0fs; a demo clip is 10–60s", v.Name, total)
		}
	}
}

// The camera, captions, ring and cursor are worked out by timeline.mjs, a
// pure function of the time, so a frame can be drawn in any order and a
// video drawn twice is the same video. Its own tests run under node.
func TestDemoVideoTimeline(t *testing.T) {
	node := nodetest.Path(t)
	out, err := exec.Command(node, filepath.Join("..", "..", "web/scripts/demo-video/timeline.test.mjs")).CombinedOutput()
	if err != nil {
		t.Fatalf("timeline.test.mjs: %v\n%s", err, out)
	}
}

func TestDemoVideoScripts(t *testing.T) {
	root := filepath.Join("..", "..")
	if out, err := exec.Command("bash", "-n", filepath.Join(root, "web/scripts/demo-video/capture.sh")).CombinedOutput(); err != nil {
		t.Errorf("capture.sh does not parse: %v\n%s", err, out)
	}
	rec, err := os.ReadFile(filepath.Join(root, "web/scripts/demo-video/record.py"))
	if err != nil {
		t.Fatal(err)
	}
	// The page is served to headless Chrome on loopback only, and Chrome is
	// given a profile of its own rather than the developer's.
	for _, want := range []string{`"127.0.0.1"`, "--user-data-dir=", "--headless"} {
		if !strings.Contains(string(rec), want) {
			t.Errorf("record.py lost %q", want)
		}
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3: record.py is not compiled here")
	}
	cmd := exec.Command(python, "-m", "py_compile", filepath.Join(root, "web/scripts/demo-video/record.py"))
	cmd.Env = append(os.Environ(), "PYTHONPYCACHEPREFIX="+t.TempDir())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("record.py does not compile: %v\n%s", err, out)
	}
}

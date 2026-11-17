package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A deploy service reports to GitHub as a check (Vercel, netlify/…): a
// red one is its file's to read, and with nothing installed it is still
// named, so the branch can say what would read it.
func TestCheckSourceDeploy(t *testing.T) {
	isolate(t)
	shipped, _ := All()
	vercel, netlify := fixture(t, "vercel"), fixture(t, "netlify")
	with := append(append([]*File{}, shipped...), vercel, netlify)
	for _, tc := range []struct {
		name, link string
		files      []*File
		source     string
		readable   bool
	}{
		{"Vercel", "https://vercel.com/acme/web-app/9mX", with, "vercel", true},
		{"Vercel – web-app", "", with, "vercel", true},
		{"netlify/web-app/deploy-preview", "https://app.netlify.com/sites/web-app/deploys/1", with, "netlify", true},
		{"Vercel", "https://vercel.com/acme/web-app/9mX", nil, "vercel", false},
		{"netlify/web-app/deploy-preview", "", nil, "netlify", false},
		{"Header rules - web-app", "https://app.netlify.com/sites/web-app/deploys/1", nil, "netlify", false},
		// A CI check is still its CI's, deploy files or not.
		{"ci/circleci: build", "", with, "circleci", true},
		{"test (ubuntu)", "https://github.com/acme/w/actions/runs/1/job/2", with, SourceActions, true},
	} {
		source, readable := CheckSource(tc.name, tc.link, tc.files)
		if source != tc.source || readable != tc.readable {
			t.Errorf("%q %q: %q %v, want %q %v", tc.name, tc.link, source, readable, tc.source, tc.readable)
		}
	}
	// A CI file matching the same check wins over a deploy file.
	ci, problems, _ := Parse([]byte(strings.Replace(good, `checks = ["acme/*"]`, `checks = ["Vercel*"]`, 1)))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	if s, _ := CheckSource("Vercel", "", []*File{vercel, ci}); s != "acme-ci" {
		t.Errorf("ci and deploy both match: %q, want the ci file", s)
	}
	if Label("vercel", nil) != "Vercel" || Label("netlify", nil) != "Netlify" {
		t.Error("known deploy services' labels")
	}
}

// A project with vercel.json or netlify.toml uses that service, said with
// the deploy kind — as is a check one reported, rather than calling it CI.
func TestDetectedDeploy(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "vercel.json"), []byte("{}"), 0o644)
	got := Detected(root, Remote{}, []CheckRef{{Name: "netlify/web-app/deploy-preview"}}, nil)
	kinds := map[string]string{}
	for _, d := range got {
		kinds[d.Name] = d.Kind + " " + d.Why
		if d.Installed {
			t.Errorf("%s installed with no file", d.Name)
		}
	}
	if kinds["vercel"] != "deploy vercel.json" || kinds["netlify"] != "deploy check netlify/web-app/deploy-preview" {
		t.Errorf("detected %v", kinds)
	}
	// Installed, the file's own [detect] names it, with the same kind.
	got = Detected(root, Remote{}, nil, []*File{fixture(t, "vercel")})
	if len(got) != 1 || got[0].Kind != KindDeploy || !got[0].Installed {
		t.Errorf("with the file: %+v", got)
	}
}

func TestRelease(t *testing.T) {
	vercel, netlify := fixture(t, "vercel"), fixture(t, "netlify")
	d := func(state, env string) Deploy { return Deploy{Run: Run{ID: "d1", State: state}, Environment: env} }

	r := ReleaseOf(vercel)
	if r.Verb != "Promote" || !strings.Contains(r.What, "built again for production") || r.Note == "" {
		t.Errorf("vercel's words: %+v", r)
	}
	if ok, why := CanRelease(vercel, d("ready", "")); !ok || why != "" {
		t.Errorf("vercel ready preview: %v %q", ok, why)
	}
	if ok, _ := CanRelease(vercel, d("ready", "production")); !ok {
		t.Error("vercel production deploy: released again is a roll back")
	}

	r = ReleaseOf(netlify)
	if r.Verb != "Publish" || !strings.Contains(r.What, "published deploy") || !strings.Contains(r.Note, "Nothing is built again") {
		t.Errorf("netlify's words: %+v", r)
	}
	if ok, why := CanRelease(netlify, d("ready", "deploy-preview")); ok || why != "A deploy preview can't be published: merge, then publish the production branch's deploy" {
		t.Errorf("netlify deploy preview: %v %q", ok, why)
	}
	if ok, why := CanRelease(netlify, d("ready", "branch-deploy")); ok || !strings.HasPrefix(why, "A branch deploy can't be published") {
		t.Errorf("netlify branch deploy: %v %q", ok, why)
	}
	if ok, _ := CanRelease(netlify, d("ready", "production")); !ok {
		t.Error("netlify production deploy")
	}

	// Only a ready deploy, and only from a file that can.
	for _, st := range []string{"building", "failed", "canceled", ""} {
		if ok, why := CanRelease(vercel, d(st, "")); ok || why != "" {
			t.Errorf("%s: %v %q", st, ok, why)
		}
	}
	none := *vercel
	none.Promote = Operation{}
	if ok, _ := CanRelease(&none, d("ready", "")); ok {
		t.Error("a file without promote")
	}
	if ok, _ := CanRelease(nil, d("ready", "")); ok {
		t.Error("no file")
	}

	// Any other deploy file: plain words, its label in them.
	other := *vercel
	other.Name, other.Label = "acme-deploy", "Acme"
	r = ReleaseOf(&other)
	if r.Verb != "Promote" || !strings.Contains(r.What, "Acme") {
		t.Errorf("another file's words: %+v", r)
	}
}

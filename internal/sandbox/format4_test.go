package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/config"
)

// publishedFile installs testdata/registry/NAME.toml — the file published
// in conch-integrations — as a provider file pointed at api, and opens it
// with cfg. Its name is the file's own.
func publishedFile(t *testing.T, name, api string, cfg config.ProviderCfg) *Custom {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "registry", name+".toml"))
	if err != nil {
		t.Fatal(err)
	}
	body := regexp.MustCompile(`(?m)^api = ".*"$`).ReplaceAllString(string(data), fmt.Sprintf("api = %q", api))
	customHome(t, name, body)
	p, err := openCustom(name, config.SandboxCfg{name: cfg})
	if err != nil {
		t.Fatalf("%s's file: %v", name, err)
	}
	return p.(*Custom)
}

// The three files that replace conch's own providers read with no problems.
func TestRegistryProviderFilesAreGood(t *testing.T) {
	for _, name := range []string{"boat", "daytona", "tensorlake"} {
		f, problems, err := ReadProviderFile(filepath.Join("testdata", "registry", name+".toml"))
		if err != nil || len(problems) > 0 {
			t.Errorf("%s: %v %v", name, err, problems)
			continue
		}
		if f.Format != 4 || f.Name != name {
			t.Errorf("%s: format %d name %q", name, f.Format, f.Name)
		}
	}
}

// boat.dev as a file does what conch's own boat provider did, against the
// same fake: a size where the image goes, the life asked for, an address
// that arrives late or through a forwarder, resume for start, delete's
// confirmation, the key authorized, preview, usage and snapshots.
func TestBoatFile(t *testing.T) {
	f, _ := newFakeBoat(t)
	old := waitUnit
	waitUnit = time.Millisecond
	t.Cleanup(func() { waitUnit = old })
	c := publishedFile(t, "boat", os.Getenv("BOAT_API_URL"), config.ProviderCfg{Snapshot: "large"})
	ctx := context.Background()

	s, err := c.Create(ctx, Spec{Name: "web"})
	if err != nil || s.State != StateStarted || s.CPU != 4 || s.Memory != 8 {
		t.Fatalf("create: %+v %v", s, err)
	}
	made := f.creations()[0]
	if made["type"] != "large" || made["from"] != nil || made["ttlSeconds"] != float64(7200) ||
		!strings.HasPrefix(fmt.Sprint(made["name"]), "conch-web-") || fmt.Sprint(made["labels"]) != "map[conch:1]" {
		t.Fatalf("create body: %v", made)
	}
	// An image of one's own goes as from; a size asked for wins over the
	// configured one; numbers are refused.
	if _, err := c.Create(ctx, Spec{Name: "b", Snapshot: "web-stack", Size: "small"}); err != nil {
		t.Fatal(err)
	}
	if m := f.creations()[1]; m["type"] != "small" || m["from"] != "web-stack" {
		t.Fatalf("second body: %v", m)
	}
	if _, err := c.Create(ctx, Spec{CPU: 2}); err == nil || !strings.Contains(err.Error(), "small, default, large") {
		t.Fatalf("numbers: %v", err)
	}
	if _, err := c.Create(ctx, Spec{Size: "xlarge"}); err == nil || !strings.Contains(err.Error(), "xlarge") {
		t.Fatalf("an unknown size: %v", err)
	}
	if got := c.Life(); got != 2*time.Hour {
		t.Fatalf("life %v", got)
	}

	list, err := c.List(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("list: %v %v", list, err)
	}

	a, err := c.SSHAccess(ctx, s.ID)
	if err != nil || a.User != "user" || !a.PlainUser || a.Host != "203.0.113.5" || a.Port != 0 {
		t.Fatalf("access: %+v %v", a, err)
	}
	f.edit(s.ID, func(b *boatSandbox) { b.SSHEndpoint = "fwd.boat.dev:2201" })
	if a, err = c.SSHAccess(ctx, s.ID); err != nil || a.Host != "fwd.boat.dev" || a.Port != 2201 {
		t.Fatalf("through the forwarder: %+v %v", a, err)
	}
	f.edit(s.ID, func(b *boatSandbox) { b.SSHEndpoint = "fwd.boat.dev" })
	if _, err = c.SSHAccess(ctx, s.ID); err == nil || !strings.Contains(err.Error(), "cannot read") {
		t.Fatalf("a broken endpoint: %v", err)
	}
	f.edit(s.ID, func(b *boatSandbox) { b.SSHEndpoint, b.IP = "", "" })
	if _, err = c.SSHAccess(ctx, s.ID); err == nil || !strings.Contains(err.Error(), "no address yet") {
		t.Fatalf("no address: %v", err)
	}

	ka, ok := As[KeyAuthorizer](Provider(c))
	if !ok || ka.AuthorizeKey(ctx, s.ID, " ssh-ed25519 AAAA conch\n") != nil {
		t.Fatal("authorize")
	}
	if got := string(f.body("POST /sandboxes/" + s.ID + "/sshkey")); got != `{"key":"ssh-ed25519 AAAA conch"}` {
		t.Fatalf("authorize body %s", got)
	}

	if err := c.Stop(ctx, s.ID); err != nil || f.state(s.ID) != "archived" {
		t.Fatalf("stop: %v %s", err, f.state(s.ID))
	}
	if s, err = c.Start(ctx, s.ID); err != nil || s.State != StateStarted {
		t.Fatalf("start: %+v %v", s, err)
	}
	if got := string(f.body("POST /sandboxes/" + s.ID + "/resume")); got != `{"ttlSeconds":7200}` {
		t.Fatalf("resume body %s", got)
	}

	pv, ok := As[Previewer](Provider(c))
	if !ok {
		t.Fatal("no preview")
	}
	if link, err := pv.PreviewURL(ctx, s.ID, 3000, time.Hour); err != nil || link != "https://frazil-pneuma-rallye-3000.on.boat.dev" && !strings.HasSuffix(link, "-3000.on.boat.dev") {
		t.Fatalf("preview: %q %v", link, err)
	}
	if _, err := pv.PreviewURL(ctx, s.ID, 70000, time.Hour); err == nil {
		t.Fatal("a port past 65535")
	}

	m, ok := As[Metered](Provider(c))
	if !ok {
		t.Fatal("no usage")
	}
	to := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	u, err := m.Usage(ctx, s.ID, to.Add(-24*time.Hour), to)
	if err != nil || !u.Known || u.Cost != 0.0498 || len(u.Periods) != 1 || u.Periods[0].To != to ||
		u.Periods[0].From != to.Add(-4980*time.Second) || !u.Periods[0].Running() {
		t.Fatalf("usage: %+v %v", u, err)
	}

	sn, ok := As[Snapshotter](Provider(c))
	if !ok {
		t.Fatal("no snapshots")
	}
	snaps, err := sn.Snapshots(ctx)
	if err != nil || len(snaps) != 1 || snaps[0].Name != "web-stack" || snaps[0].Size != "default" || snaps[0].Created.IsZero() {
		t.Fatalf("snapshots: %+v %v", snaps, err)
	}
	if err := sn.Snapshot(ctx, s.ID, " kept "); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	_ = json.Unmarshal(f.body("POST /named-snapshots"), &body)
	if body["sandboxId"] != s.ID || body["name"] != "kept" {
		t.Fatalf("snapshot body %v", body)
	}
	if err := sn.ForgetSnapshot(ctx, "kept"); err != nil {
		t.Fatal(err)
	}
	ch, err := c.Choices(ctx, "image")
	if err != nil || len(ch) != 1 || ch[0].Value != "web-stack" {
		t.Fatalf("image choices: %v %v", ch, err)
	}

	if err := c.Delete(ctx, s.ID); err != nil || f.header("DELETE /sandboxes/"+s.ID, "X-Ascii-Confirm-Delete") != s.ID {
		t.Fatalf("delete: %v", err)
	}
}

// Daytona as a file: the region where the built-in sent its target, the
// sandbox named for what it is, a list that keeps every sandbox the label
// says is conch's (the built-in's, named without conch-), sizes within
// limits, the signed preview link, and snapshots.
func TestDaytonaFile(t *testing.T) {
	f, _ := newFakeDaytona(t)
	c := publishedFile(t, "daytona", os.Getenv("DAYTONA_API_URL"), config.ProviderCfg{Target: "eu", Snapshot: "conch-dev"})
	ctx := context.Background()

	s, err := c.Create(ctx, Spec{Name: "api", CPU: 2, Memory: 4})
	if err != nil || s.State != StateStarted {
		t.Fatalf("create: %+v %v", s, err)
	}
	made := f.created[0]
	if made["target"] != "eu" || made["snapshot"] != "conch-dev" || made["name"] != "api" || made["cpu"] != float64(2) ||
		made["memory"] != float64(4) || made["disk"] != nil || made["autoStopInterval"] != float64(0) {
		t.Fatalf("create body: %v", made)
	}
	if _, err := c.Create(ctx, Spec{CPU: 16}); err == nil || !strings.Contains(err.Error(), "1 to 4") {
		t.Fatalf("too many vCPUs: %v", err)
	}
	if _, err := c.Create(ctx, Spec{Region: "mars"}); err == nil || !strings.Contains(err.Error(), "us, eu") {
		t.Fatalf("a region it has not: %v", err)
	}
	f.mu.Lock()
	f.pages = []string{`{"items":[{"id":"old-1","name":"old-1","state":"started"},{"id":"x","name":"conch-api-1a2b3c","state":"stopped"}],"nextCursor":null}`}
	f.mu.Unlock()
	list, err := c.List(ctx)
	if err != nil || len(list) != 2 || list[0].Name != "old-1" {
		t.Fatalf("list keeps the built-in's sandbox: %+v %v", list, err)
	}
	if q := f.queries["GET /sandbox"]; !strings.Contains(q, "labels=") {
		t.Fatalf("list asks for conch's label: %q", q)
	}

	pv, _ := As[Previewer](Provider(c))
	if _, err := pv.PreviewURL(ctx, s.ID, 22222, time.Hour); err == nil || !strings.Contains(err.Error(), "web terminal") {
		t.Fatalf("a reserved port: %v", err)
	}
	if _, err := pv.PreviewURL(ctx, s.ID, 3000, 48*time.Hour); err != nil {
		t.Fatal(err)
	}
	if q := f.queries["GET /sandbox/"+s.ID+"/ports/3000/signed-preview-url"]; q != "expiresInSeconds=86400" {
		t.Fatalf("preview query %q", q)
	}
	if _, ok := As[Metered](Provider(c)); ok {
		t.Fatal("usage: Daytona's is on another host, which a file cannot reach")
	}
	if _, ok := As[NamedSizes](Provider(c)); ok {
		t.Fatal("Daytona has no named sizes")
	}
	region, err := c.Choices(ctx, "region")
	if err != nil || len(region) != 2 || region[1].Value != "eu" {
		t.Fatalf("regions: %v %v", region, err)
	}
}

// What a format-4 file can get wrong, each said.
func TestProviderFileFormat4Problems(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "registry", "daytona.toml"))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ change, want string }{
		{"format = 4\n=>format = 3\n", "[[setting]] needs format = 4"},
		{"key = \"region\"=>key = \"zone\"", `key "zone": one of region, image`},
		{"choices = [\"us\", \"eu\"]=>choices = [\"us\", \"\"]", "an empty one"},
		{"choices = [\"us\", \"eu\"]=>choices = []", "choices, choices_call, or free"},
		{"items = \"items\"\nvalue = \"name\"\nfree = true=>value = \"name\"\nfree = true", "choices_call needs items and value"},
		{"choices_call = \"GET /snapshots?limit=100\"=>choices_call = \"POST /snapshots\"", "a GET"},
		{"cpu = [1, 4]=>cpu = [4, 1]", "[size] cpu: [min, max]"},
		{"cpu = [1, 4]=>names = [\"s\"]\ncpu = [1, 4]", "names, or cpu"},
		{"url = \"url\"\nmax_secs=>max_secs", "[preview] url"},
		{"name = \"{snapshot_name}\", includeMemory=>includeMemory", "never sends {snapshot_name}"},
	}
	for _, tc := range cases {
		from, to, _ := strings.Cut(tc.change, "=>")
		if !strings.Contains(string(data), from) {
			t.Fatalf("the file has no %q", from)
		}
		dir := t.TempDir()
		p := filepath.Join(dir, "daytona.toml")
		_ = os.WriteFile(p, []byte(strings.Replace(string(data), from, to, 1)), 0o600)
		_, problems, err := ReadProviderFile(p)
		if err != nil || !strings.Contains(strings.Join(problems, "\n"), tc.want) {
			t.Errorf("%s: want %q, got %v %v", tc.change, tc.want, problems, err)
		}
	}
	// A format-3 conch reads [size] as a key it does not know, and refuses
	// the file: which is the point of the format number.
	if ProviderFormat != 4 {
		t.Fatalf("format %d", ProviderFormat)
	}
}

// A setting's choices read from the provider: a failure and a timeout are
// said, not an empty list; a setting the file lacks has none.
func TestSettingChoicesFromTheAPI(t *testing.T) {
	var status = 200
	var slow time.Duration
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(slow)
		w.WriteHeader(status)
		fmt.Fprint(w, `{"items":[{"name":"a"},{"name":"a"},{"name":""},{"name":"b"}]}`)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("DAYTONA_API_KEY", "k")
	c := publishedFile(t, "daytona", srv.URL, config.ProviderCfg{})
	ctx := context.Background()
	if ch, err := c.Choices(ctx, "image"); err != nil || len(ch) != 2 {
		t.Fatalf("choices: %v %v", ch, err)
	}
	status = 500
	if _, err := c.Choices(ctx, "image"); err == nil || !strings.Contains(err.Error(), "Default image") {
		t.Fatalf("a failing call: %v", err)
	}
	status, slow = 200, 200*time.Millisecond
	short, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if _, err := c.Choices(short, "image"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a slow call: %v", err)
	}
	if _, err := c.Choices(ctx, "size"); err == nil {
		t.Fatal("a setting the file lacks")
	}
	if err := c.CheckSetting("image", "anything"); err != nil {
		t.Fatalf("a free setting: %v", err)
	}
	if err := c.CheckSetting("region", "eu"); err != nil {
		t.Fatal(err)
	}
}

// A placeholder in a call's path is escaped, and a query parameter left
// empty is dropped rather than sent as nothing.
func TestFillPath(t *testing.T) {
	v := vars{"{id}": "a/b", "{port}": 3000, "{region}": ""}
	for in, want := range map[string]string{
		"/x/{id}/ports/{port}":     "/x/a%2Fb/ports/3000",
		"/x?region={region}&n=1":   "/x?n=1",
		"/x?region={region}":       "/x",
		"/x?keep={other}":          "/x?keep={other}",
		"/x?labels=%7B%7D&limit=2": "/x?labels=%7B%7D&limit=2",
	} {
		if got := fillPath(in, v); got != want {
			t.Errorf("%s: %s, want %s", in, got, want)
		}
	}
}

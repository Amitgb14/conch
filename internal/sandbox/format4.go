package sandbox

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Format 4 is everything conch's own providers did that a file could not
// say, so that they could become files: what a person may choose for a
// provider ([[setting]] region and image, [size]), how long a sandbox is
// given ([lifetime]), snapshots, preview links, what a sandbox cost, an
// ssh address that arrives a moment after the sandbox does, and a list
// call that asks only for conch's own.

// Setting is one thing the provider lets a person choose, by its key:
// region or image. Its choices are a fixed list, or read from the API.
type Setting struct {
	Key   string `toml:"key"`
	Label string `toml:"label"`
	Help  string `toml:"help"`
	// Choices is a fixed list; ChoicesCall reads one: Items is where the
	// list is, Value each one's value and Name (optional) how it is shown.
	Choices     []string `toml:"choices"`
	ChoicesCall string   `toml:"choices_call"`
	Items       string   `toml:"items"`
	Value       string   `toml:"value"`
	Name        string   `toml:"name"`
	// Free says a value outside the choices is fine: an image of one's
	// own. Otherwise a value must be one of them.
	Free bool `toml:"free"`
	// Default is what the provider does when nothing is chosen, shown as
	// such; "" is the account's default.
	Default string `toml:"default"`
}

// settingKeys are the settings a file may declare, each a placeholder in
// its calls.
var settingKeys = []string{"region", "image", "template"}

// SizeSpec is how big a sandbox can be: named sizes, or limits on vCPUs,
// memory and disk (each [min, max]).
type SizeSpec struct {
	Names     []string `toml:"names" json:"names,omitempty"`
	Default   string   `toml:"default" json:"default,omitempty"`
	CPU       []int    `toml:"cpu" json:"cpu,omitempty"`
	MemoryGiB []int    `toml:"memory_gib" json:"memory_gib,omitempty"`
	DiskGiB   []int    `toml:"disk_gib" json:"disk_gib,omitempty"`
	// Choices reads the sizes the account may make from the API — a plan
	// that allows 4 vCPU / 8 GiB and nothing else, say — each item one
	// size, as {cpu} {memory_*} {disk_*}.
	Choices SizeChoices `toml:"choices" json:"-"`
}

// SizeChoices is where the sizes are: Call's reply, the list at Items,
// and in each item its vCPUs, memory (in MiB or GiB) and disk (GiB).
type SizeChoices struct {
	Call      string `toml:"call"`
	Items     string `toml:"items"`
	CPU       string `toml:"cpu"`
	MemoryMB  string `toml:"memory_mb"`
	MemoryGiB string `toml:"memory_gib"`
	DiskGiB   string `toml:"disk_gib"`
}

// SizeOption is one size the provider offers, in vCPUs and GiB; 0 is a
// part it does not say.
type SizeOption struct {
	CPU    int `json:"cpu"`
	Memory int `json:"memory,omitempty"`
	Disk   int `json:"disk,omitempty"`
}

func (s SizeSpec) limits() bool { return len(s.CPU)+len(s.MemoryGiB)+len(s.DiskGiB) > 0 }

// LifeSpec is a provider that gives a sandbox a length of life when it is
// made, {ttl_secs}: DefaultMins unless the person asks for longer, never
// past MaxMins.
type LifeSpec struct {
	DefaultMins int `toml:"default_mins"`
	MaxMins     int `toml:"max_mins"`
}

// SnapshotSpec keeps a sandbox to make others from: save {id} as
// {snapshot_name}, list them, forget one.
type SnapshotSpec struct {
	Save   Call         `toml:"save"`
	List   SnapListCall `toml:"list"`
	Forget Call         `toml:"forget"`
}

// SnapListCall reads the kept snapshots. Size may be a template over
// {item.PATH}, as "{item.cpu} vCPU, {item.memory} GiB".
type SnapListCall struct {
	Call
	Items   string `toml:"items"`
	Name    string `toml:"name"`
	State   string `toml:"state"`
	Size    string `toml:"size"`
	Created string `toml:"created"`
}

// PreviewSpec is a link to {port} inside sandbox {id}, good for
// {expires_secs}, at URL in the reply.
type PreviewSpec struct {
	Call
	URL     string `toml:"url"`
	MaxSecs int    `toml:"max_secs"`
	// Reserved are ports that are the provider's own, refused with why.
	Reserved map[string]string `toml:"reserved"`
}

// UsageSpec is what sandbox {id} cost between {from} and {to}: periods
// (Items, each with From/To/CPU/MemoryGiB/DiskGiB/Cost), or one total
// (Cost, and Seconds it ran).
type UsageSpec struct {
	Call
	Items     string `toml:"items"`
	From      string `toml:"from"`
	To        string `toml:"to"`
	CPU       string `toml:"cpu"`
	MemoryGiB string `toml:"memory_gib"`
	DiskGiB   string `toml:"disk_gib"`
	Cost      string `toml:"cost"`
	Seconds   string `toml:"seconds"`
}

// needs4 names the first thing in the file that format 3 did not have.
func (f *ProviderFile) needs4() string {
	switch {
	case len(f.Settings) > 0:
		return "[[setting]]"
	case len(f.Size.Names) > 0 || f.Size.limits() || f.Size.Choices.Call != "":
		return "[size]"
	case f.Lifetime.DefaultMins != 0 || f.Lifetime.MaxMins != 0:
		return "[lifetime]"
	case f.Snapshot.Save.Call != "" || f.Snapshot.List.Call.Call != "" || f.Snapshot.Forget.Call != "":
		return "[snapshot]"
	case f.Preview.Call.Call != "":
		return "[preview]"
	case f.Usage.Call.Call != "":
		return "[usage]"
	case f.SSH.Endpoint != "":
		return "[ssh] endpoint"
	case f.SSH.WaitSecs != 0:
		return "[ssh] wait_secs"
	case f.SSH.Command != "" || f.SSH.Expires != "":
		return "[ssh] command or expires"
	case f.List.AllMine:
		return "[list] all_mine"
	case f.Get.MemoryGiB != "" || f.Get.DiskGiB != "" || f.Get.Desired != "":
		return "[get] memory_gib, disk_gib or desired"
	}
	return ""
}

// problems4 is what is wrong with the format-4 parts of a file.
func (f *ProviderFile) problems4() []string {
	var p []string
	add := func(format string, a ...any) { p = append(p, fmt.Sprintf(format, a...)) }
	seen := map[string]bool{}
	for i, s := range f.Settings {
		where := fmt.Sprintf("[[setting]] %d", i+1)
		switch {
		case !slices.Contains(settingKeys, s.Key):
			add("%s key %q: one of %s", where, s.Key, strings.Join(settingKeys, ", "))
			continue
		case seen[s.Key]:
			add("%s key %q: said twice", where, s.Key)
		}
		seen[s.Key] = true
		where = "[[setting]] " + s.Key
		if s.ChoicesCall != "" {
			if err := checkCall(s.ChoicesCall); err != nil {
				add("%s choices_call: %v", where, err)
			} else if !strings.HasPrefix(s.ChoicesCall, "GET ") {
				add("%s choices_call: a GET, since choices are only read", where)
			}
			if s.Items == "" || s.Value == "" {
				add("%s: choices_call needs items and value: where the list is, and each one's value", where)
			}
			if len(s.Choices) > 0 {
				add("%s: choices or choices_call, not both", where)
			}
		} else if len(s.Choices) == 0 && !s.Free {
			add("%s: choices, choices_call, or free = true for a value of the person's own", where)
		}
		if slices.Contains(s.Choices, "") {
			add("%s choices: an empty one (leave the setting empty for the default instead)", where)
		}
		if s.Default != "" && len(s.Choices) > 0 && !s.Free && !slices.Contains(s.Choices, s.Default) {
			add("%s default %q is not one of its choices", where, s.Default)
		}
	}
	if len(f.Size.Names) > 0 && f.Size.limits() {
		add("[size]: names, or cpu, memory_gib and disk_gib limits, not both")
	}
	if z := f.Size.Choices; z.Call != "" {
		if err := checkCall(z.Call); err != nil {
			add("[size.choices] call: %v", err)
		} else if !strings.HasPrefix(z.Call, "GET ") {
			add("[size.choices] call: a GET, since sizes are only read")
		}
		if z.Items == "" || z.CPU == "" {
			add("[size.choices] items and cpu: where the list is, and each size's vCPUs")
		}
		if z.MemoryMB != "" && z.MemoryGiB != "" {
			add("[size.choices]: memory_mb or memory_gib, not both")
		}
		if len(f.Size.Names) > 0 {
			add("[size]: names, or choices read from the API, not both")
		}
	}
	if slices.Contains(f.Size.Names, "") {
		add("[size] names: an empty one")
	}
	if f.Size.Default != "" && !slices.Contains(f.Size.Names, f.Size.Default) {
		add("[size] default %q is not one of names", f.Size.Default)
	}
	for _, l := range []struct {
		what string
		v    []int
	}{{"cpu", f.Size.CPU}, {"memory_gib", f.Size.MemoryGiB}, {"disk_gib", f.Size.DiskGiB}} {
		if len(l.v) > 0 && (len(l.v) != 2 || l.v[0] < 1 || l.v[1] < l.v[0]) {
			add("[size] %s: [min, max], each at least 1", l.what)
		}
	}
	if len(f.Size.Names) > 0 && !bodyUses(f.Create.Body, "{size}") {
		add("[size] names: [create] body never sends {size}")
	}
	if f.Lifetime.DefaultMins < 0 || f.Lifetime.MaxMins < 0 || (f.Lifetime.MaxMins > 0 && f.Lifetime.DefaultMins > f.Lifetime.MaxMins) {
		add("[lifetime]: default_mins and max_mins in minutes, the default no more than the max")
	}
	if (f.Lifetime.DefaultMins > 0) != bodyUses(f.Create.Body, "{ttl_secs}") {
		add("[lifetime] and {ttl_secs} in [create] body go together")
	}
	sn := f.Snapshot
	if sn.Save.Call != "" || sn.List.Call.Call != "" || sn.Forget.Call != "" {
		for _, c := range []struct{ what, call string }{{"save", sn.Save.Call}, {"list", sn.List.Call.Call}, {"forget", sn.Forget.Call}} {
			if err := checkCall(c.call); err != nil {
				add("[snapshot.%s] call: %v", c.what, err)
			}
		}
		if !strings.Contains(sn.Save.Call, "{id}") && !bodyUses(sn.Save.Body, "{id}") {
			add("[snapshot.save] never names the sandbox, {id}")
		}
		if !strings.Contains(sn.Save.Call, "{snapshot_name}") && !bodyUses(sn.Save.Body, "{snapshot_name}") {
			add("[snapshot.save] never sends {snapshot_name}")
		}
		if !strings.Contains(sn.Forget.Call, "{snapshot_name}") && !bodyUses(sn.Forget.Body, "{snapshot_name}") {
			add("[snapshot.forget] never names {snapshot_name}")
		}
		if sn.List.Items == "" || sn.List.Name == "" {
			add("[snapshot.list] items and name: where the list is, and each one's name")
		}
	}
	if f.Preview.Call.Call != "" {
		if err := checkCall(f.Preview.Call.Call); err != nil {
			add("[preview] call: %v", err)
		}
		if !strings.Contains(f.Preview.Call.Call, "{port}") && !bodyUses(f.Preview.Body, "{port}") {
			add("[preview] never sends {port}")
		}
		if f.Preview.URL == "" {
			add("[preview] url: where the link is in the reply")
		}
	}
	if f.Usage.Call.Call != "" {
		if err := checkCall(f.Usage.Call.Call); err != nil {
			add("[usage] call: %v", err)
		}
		if f.Usage.Cost == "" {
			add("[usage] cost: where the cost is, in each item or the reply")
		}
	}
	if f.SSH.WaitSecs < 0 || f.SSH.WaitSecs > 600 {
		add("[ssh] wait_secs %d: 0 to 600", f.SSH.WaitSecs)
	}
	if (f.SSH.Endpoint != "" || f.SSH.WaitSecs > 0 || f.SSH.Command != "" || f.SSH.Expires != "") && f.SSH.Access.Call == "" {
		add("[ssh] endpoint, wait_secs, command and expires read [ssh.access]'s reply, and there is none")
	}
	return p
}

// placeholders4 are what format 4's calls may say beyond format 3's.
var placeholders4 = []string{"{region}", "{image}", "{template}", "{size}", "{ttl_secs}", "{port}", "{expires_secs}",
	"{snapshot_name}", "{from}", "{to}"}

// As is p as capability T — Previewer, Snapshotter, Metered, NamedSizes,
// Lifetime, KeyAuthorizer — when it has it. A provider file has the
// methods of all of them, and only what its file describes works, so a
// type assertion alone would offer a preview link no file makes.
func As[T any](p any) (T, bool) {
	t, ok := p.(T)
	if !ok {
		return t, false
	}
	if g, gated := p.(interface{ offers(any) bool }); gated && !g.offers((*T)(nil)) {
		var zero T
		return zero, false
	}
	return t, true
}

func (c *Custom) offers(what any) bool {
	f := c.f
	switch what.(type) {
	case *Previewer:
		return f.Preview.Call.Call != ""
	case *Snapshotter:
		return f.Snapshot.Save.Call != ""
	case *Metered:
		return f.Usage.Call.Call != ""
	case *NamedSizes:
		return len(f.Size.Names) > 0
	case *Lifetime:
		return f.Lifetime.DefaultMins > 0
	case *KeyAuthorizer:
		return f.SSH.Key == "authorize"
	}
	return true
}

// Settings are what the file lets a person choose, in its order.
func (c *Custom) Settings() []Setting { return append([]Setting(nil), c.f.Settings...) }

// Sizes is how big a sandbox can be, as the file says.
func (c *Custom) Sizes() SizeSpec { return c.f.Size }

// Choice is one value a setting may take.
type Choice struct {
	Value string `json:"value"`
	Name  string `json:"name,omitempty"`
}

// Choices are what setting key may be: the file's list, or read from the
// provider. A setting the file does not declare has none.
func (c *Custom) Choices(ctx context.Context, key string) ([]Choice, error) {
	i := slices.IndexFunc(c.f.Settings, func(s Setting) bool { return s.Key == key })
	if i < 0 {
		return nil, fmt.Errorf("%s has no setting %q", c.label(), key)
	}
	s := c.f.Settings[i]
	if s.ChoicesCall == "" {
		out := make([]Choice, 0, len(s.Choices))
		for _, v := range s.Choices {
			out = append(out, Choice{Value: v})
		}
		return out, nil
	}
	reply, err := c.do(ctx, Call{Call: s.ChoicesCall}, vars{})
	if err != nil {
		return nil, fmt.Errorf("%s's %s: %w", c.label(), firstSet(s.Label, s.Key), err)
	}
	items, ok := Pick(reply, s.Items).([]any)
	if !ok {
		return nil, fmt.Errorf("%s's %s: %q is not a list in the reply", c.label(), firstSet(s.Label, s.Key), s.Items)
	}
	var out []Choice
	for _, it := range items {
		v := pickStr(it, s.Value)
		if v == "" || slices.ContainsFunc(out, func(c Choice) bool { return c.Value == v }) {
			continue
		}
		ch := Choice{Value: v}
		if s.Name != "" {
			if n := pickStr(it, s.Name); n != v {
				ch.Name = n
			}
		}
		out = append(out, ch)
	}
	return out, nil
}

// CheckSetting says whether value can be setting key's: one of a fixed
// list, unless the setting takes values of the person's own. A list read
// from the provider is not checked here; the provider says.
func (c *Custom) CheckSetting(key, value string) error {
	i := slices.IndexFunc(c.f.Settings, func(s Setting) bool { return s.Key == key })
	if i < 0 {
		if value == "" {
			return nil
		}
		return fmt.Errorf("%s has no %s to choose", c.label(), key)
	}
	s := c.f.Settings[i]
	if value == "" || s.Free || s.ChoicesCall != "" || slices.Contains(s.Choices, value) {
		return nil
	}
	return fmt.Errorf("%s %q: %s takes %s", firstSet(s.Label, s.Key), value, c.label(), strings.Join(s.Choices, ", "))
}

// CheckSize says whether a size suits the file: a named one of its names,
// numbers within its limits, and never numbers for a provider of names.
func (c *Custom) CheckSize(name string, cpu, mem, disk int) error {
	z := c.f.Size
	if len(z.Names) > 0 {
		if cpu > 0 || mem > 0 || disk > 0 {
			return fmt.Errorf("%s machines come in sizes, not numbers: choose %s, and leave vCPUs, memory and disk empty", c.label(), strings.Join(z.Names, ", "))
		}
		if name != "" && !slices.Contains(z.Names, name) {
			return fmt.Errorf("size %q: %s has %s", name, c.label(), strings.Join(z.Names, ", "))
		}
		return nil
	}
	if name != "" {
		return fmt.Errorf("%s has no named sizes: give vCPUs, memory and disk instead of %q", c.label(), name)
	}
	for _, l := range []struct {
		what string
		n    int
		lim  []int
	}{{"vCPUs", cpu, z.CPU}, {"GiB of memory", mem, z.MemoryGiB}, {"GiB of disk", disk, z.DiskGiB}} {
		if l.n > 0 && len(l.lim) == 2 && (l.n < l.lim[0] || l.n > l.lim[1]) {
			return fmt.Errorf("%d %s: %s takes %d to %d", l.n, l.what, c.label(), l.lim[0], l.lim[1])
		}
	}
	return nil
}

// SizeOptions are the sizes the account may make, read from the provider
// as [size.choices] says; none for a file that reads none.
func (c *Custom) SizeOptions(ctx context.Context) ([]SizeOption, error) {
	z := c.f.Size.Choices
	if z.Call == "" {
		return nil, nil
	}
	reply, err := c.do(ctx, Call{Call: z.Call}, vars{})
	if err != nil {
		return nil, fmt.Errorf("%s's sizes: %w", c.label(), err)
	}
	items, ok := Pick(reply, z.Items).([]any)
	if !ok {
		return nil, fmt.Errorf("%s's sizes: %q is not a list in the reply", c.label(), z.Items)
	}
	var out []SizeOption
	for _, it := range items {
		o := SizeOption{CPU: int(pickNum(it, z.CPU)), Disk: int(pickNum(it, z.DiskGiB))}
		if z.MemoryMB != "" {
			o.Memory = int(pickNum(it, z.MemoryMB) / 1024)
		} else {
			o.Memory = int(pickNum(it, z.MemoryGiB))
		}
		if o.CPU > 0 && !slices.Contains(out, o) {
			out = append(out, o)
		}
	}
	return out, nil
}

// Takes are the sizes' parts the create call sends — cpu, memory, disk —
// so a form asks for no number the provider would never see.
func (c *Custom) Takes() []string {
	var out []string
	for _, t := range []struct {
		part string
		ph   []string
	}{{"cpu", []string{"{cpu}"}}, {"memory", []string{"{memory_gib}", "{memory_mb}"}}, {"disk", []string{"{disk_gib}", "{disk_mb}"}}} {
		for _, p := range t.ph {
			if bodyUses(c.f.Create.Body, p) {
				out = append(out, t.part)
				break
			}
		}
	}
	return out
}

// SizeNames are the file's named sizes, smallest first.
func (c *Custom) SizeNames() []string { return append([]string(nil), c.f.Size.Names...) }

// Life is what a new sandbox is given.
func (c *Custom) Life() time.Duration { return c.ttl(c.autoStop) }

// ttl is the life asked for: minutes asked, else the file's default, never
// past its max.
func (c *Custom) ttl(mins int) time.Duration {
	if mins <= 0 {
		mins = c.f.Lifetime.DefaultMins
	}
	if m := c.f.Lifetime.MaxMins; m > 0 && mins > m {
		mins = m
	}
	return time.Duration(mins) * time.Minute
}

// PreviewURL is the file's link to port inside sandbox id.
func (c *Custom) PreviewURL(ctx context.Context, id string, port int, expires time.Duration) (string, error) {
	if id == "" {
		return "", ErrNotFound
	}
	if port < 1 || port > 65535 {
		return "", fmt.Errorf("port %d: a port is 1 to 65535", port)
	}
	if why := c.f.Preview.Reserved[strconv.Itoa(port)]; why != "" {
		return "", fmt.Errorf("port %d is %s's own (%s)", port, c.f.Name, why)
	}
	secs := max(int(expires.Round(time.Second).Seconds()), 1)
	if m := c.f.Preview.MaxSecs; m > 0 && secs > m {
		secs = m
	}
	reply, err := c.do(ctx, c.f.Preview.Call, vars{"{id}": id, "{port}": port, "{expires_secs}": secs})
	if err != nil {
		return "", fmt.Errorf("preview link: %w", err)
	}
	link := pickStr(reply, c.f.Preview.URL)
	if link == "" {
		return "", fmt.Errorf("%s gave no link for port %d", c.label(), port)
	}
	if u, err := url.Parse(link); err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return "", fmt.Errorf("%s gave a link conch will not open: %q", c.label(), link)
	}
	return link, nil
}

// Snapshot keeps sandbox id as name.
func (c *Custom) Snapshot(ctx context.Context, id, name string) error {
	if id == "" {
		return ErrNotFound
	}
	if name = strings.TrimSpace(name); name == "" {
		return errors.New("a snapshot needs a name")
	}
	if _, err := c.do(ctx, c.f.Snapshot.Save, vars{"{id}": id, "{snapshot_name}": name}); err != nil {
		return fmt.Errorf("snapshot %s: %w", name, err)
	}
	return nil
}

// Snapshots lists what has been kept, newest first.
func (c *Custom) Snapshots(ctx context.Context) ([]Snap, error) {
	l := c.f.Snapshot.List
	reply, err := c.do(ctx, l.Call, vars{})
	if err != nil {
		return nil, fmt.Errorf("list snapshots: %w", err)
	}
	items, _ := Pick(reply, l.Items).([]any)
	out := make([]Snap, 0, len(items))
	for _, it := range items {
		name := pickStr(it, l.Name)
		if name == "" {
			continue
		}
		out = append(out, Snap{Name: name, State: pickStr(it, l.State), Size: itemTemplate(it, l.Size), Created: pickTime(it, l.Created)})
	}
	slices.SortStableFunc(out, func(a, b Snap) int { return b.Created.Compare(a.Created) })
	return out, nil
}

// ForgetSnapshot removes a kept snapshot.
func (c *Custom) ForgetSnapshot(ctx context.Context, name string) error {
	if strings.TrimSpace(name) == "" {
		return ErrNotFound
	}
	if _, err := c.do(ctx, c.f.Snapshot.Forget, vars{"{snapshot_name}": strings.TrimSpace(name)}); err != nil {
		if errors.Is(err, ErrNotFound) {
			return err
		}
		return fmt.Errorf("forget snapshot %s: %w", name, err)
	}
	return nil
}

// Usage is what the provider says sandbox id cost between from and to.
func (c *Custom) Usage(ctx context.Context, id string, from, to time.Time) (Usage, error) {
	if id == "" {
		return Usage{}, ErrNotFound
	}
	u := c.f.Usage
	reply, err := c.do(ctx, u.Call, vars{"{id}": id, "{from}": from.UTC().Format(time.RFC3339), "{to}": to.UTC().Format(time.RFC3339)})
	if err != nil {
		return Usage{}, fmt.Errorf("usage: %w", err)
	}
	out := Usage{From: from, To: to}
	if u.Items != "" {
		items, _ := Pick(reply, u.Items).([]any)
		for _, it := range items {
			p := UsagePeriod{From: pickTime(it, u.From), To: pickTime(it, u.To), CPU: int(pickNum(it, u.CPU)),
				MemGiB: int(pickNum(it, u.MemoryGiB)), DiskGiB: int(pickNum(it, u.DiskGiB)), Cost: pickNum(it, u.Cost)}
			out.Periods = append(out.Periods, p)
			out.Cost += p.Cost
		}
		out.Known = len(out.Periods) > 0
		return out, nil
	}
	cost, secs := pickNum(reply, u.Cost), pickNum(reply, u.Seconds)
	if cost == 0 && secs == 0 {
		return out, nil // nothing said yet
	}
	// A total with no stretches: the window is the one period there is,
	// sized as the sandbox is so it reads as running time.
	p := UsagePeriod{From: to.Add(-time.Duration(secs) * time.Second), To: to, Cost: cost}
	if s, err := c.Get(ctx, id); err == nil {
		p.CPU, p.MemGiB = s.CPU, s.Memory
	}
	out.Known, out.Cost, out.Periods = true, cost, []UsagePeriod{p}
	return out, nil
}

var itemRef = regexp.MustCompile(`\{item\.([^{}]+)\}`)

// itemTemplate reads path out of item, or fills a template of {item.PATH}.
// A template with nothing in any of its fields is empty.
func itemTemplate(item any, path string) string {
	if path == "" {
		return ""
	}
	if !strings.Contains(path, "{item.") {
		return pickStr(item, path)
	}
	found := false
	s := itemRef.ReplaceAllStringFunc(path, func(m string) string {
		v := pickStr(item, itemRef.FindStringSubmatch(m)[1])
		found = found || (v != "" && v != "0")
		return v
	})
	if !found {
		return ""
	}
	return s
}

// waitUnit is what [ssh] wait_secs counts in; tests shorten it.
var waitUnit = time.Second

// parseSSHCommand reads the host and port from a command like
// "ssh -p 2222 USER@host", for user.
func parseSSHCommand(cmd, user string) (host string, port int, ok bool) {
	f := strings.Fields(cmd)
	if len(f) < 2 || f[0] != "ssh" || user == "" {
		return "", 0, false
	}
	for i := 1; i < len(f); i++ {
		switch {
		case f[i] == "-p" && i+1 < len(f):
			p, err := strconv.Atoi(f[i+1])
			if err != nil || p <= 0 || p > 65535 {
				return "", 0, false
			}
			port = p
			i++
		case strings.HasPrefix(f[i], user+"@"):
			host = strings.TrimPrefix(f[i], user+"@")
		}
	}
	if host == "" || strings.ContainsAny(host, "@/ ") {
		return "", 0, false
	}
	return host, port, true
}

// endpoint splits an ssh endpoint, host:port.
func endpoint(s string) (host string, port int, err error) {
	h, p, ok := strings.Cut(strings.TrimSpace(s), ":")
	n, perr := strconv.Atoi(p)
	if !ok || h == "" || perr != nil || n < 1 || n > 65535 {
		return "", 0, fmt.Errorf("an ssh endpoint conch cannot read: %q", s)
	}
	return h, n, nil
}

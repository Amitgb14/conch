package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/charmbracelet/x/term"

	"github.com/Amitgb14/conch/internal/ghx"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/sandbox"
)

// The registry's side of `conch sandbox provider`: search, install,
// update and publish. A file from the registry sends the person's API key
// to the host it names, so install says the hosts before it saves
// anything, and update asks again when they change rather than taking a
// new destination for the key silently.

func registryCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), time.Minute)
}

// here says what this computer has of a registry provider.
func here(name string, inst map[string]sandbox.Installed) string {
	if _, err := os.Stat(sandbox.ProviderFilePath(name)); err != nil {
		return ""
	}
	in, ok := inst[name]
	if !ok {
		return "a file of your own"
	}
	if edited, _ := sandbox.EditedSinceInstall(name, in); edited {
		return "installed, edited here"
	}
	return "installed"
}

func liveCheck(e sandbox.RegistryEntry) string {
	if e.Checked == nil {
		return "not checked live yet"
	}
	return "checked " + e.Checked.Date
}

type searchJSON struct {
	sandbox.RegistryEntry
	Here string `json:"here,omitempty"`
	// Usable is false for a file newer than this conch reads.
	Usable bool `json:"usable"`
}

func providerSearch(args []string) error {
	fs := flag.NewFlagSet("sandbox provider search", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, cancel := registryCtx()
	defer cancel()
	ix, err := sandbox.FetchIndex(ctx)
	if err != nil {
		return err
	}
	inst, err := sandbox.ReadInstalled()
	if err != nil {
		return err
	}
	found := ix.Search(strings.Join(fs.Args(), " "))
	if *asJSON {
		out := []searchJSON{}
		for _, e := range found {
			out = append(out, searchJSON{e, here(e.Name, inst), e.Format <= sandbox.ProviderFormat})
		}
		b, _ := json.Marshal(out)
		fmt.Println(string(b))
		return nil
	}
	if len(found) == 0 {
		fmt.Println("no provider in the registry matches; adapt one with conch sandbox adapt URL")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tPROVIDER\tAPI\tLIVE CHECK\tHERE")
	for _, e := range found {
		h := here(e.Name, inst)
		if e.Format > sandbox.ProviderFormat {
			h = strings.TrimPrefix(h+"; ", "; ") + "needs a newer conch"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", e.Name, e.Label, e.APIHost, liveCheck(e), h)
	}
	tw.Flush()
	fmt.Println("\ninstall one with: conch sandbox provider install NAME")
	return nil
}

// describeEntry says what installing e means, before anything is saved.
func describeEntry(w io.Writer, e sandbox.RegistryEntry) {
	fmt.Fprintf(w, "%s (%s), adapted by %s on %s from %s\n", e.Label, e.Name, e.AdaptedBy, e.Adapted, e.Docs)
	fmt.Fprintf(w, "  your API key ($%s) goes to   %s\n", e.KeyEnv, e.APIHost)
	fmt.Fprintf(w, "  ssh goes to                  %s\n", sshHostWords(e.SSHHost))
	fmt.Fprintf(w, "  live check                   %s\n", liveCheck(e))
	if e.Note != "" {
		fmt.Fprintf(w, "\n%s\n", e.Note)
	}
}

// sshHostWords says a host read from the API in words.
func sshHostWords(h string) string {
	if strings.Contains(h, "{reply.") {
		return "the sandbox's own address, as the provider's API gives it"
	}
	return h
}

// pickEntry asks which provider to install, on a terminal.
func pickEntry(ix *sandbox.Index) (string, error) {
	if !term.IsTerminal(os.Stdin.Fd()) || !term.IsTerminal(os.Stdout.Fd()) {
		return "", errors.New("usage: conch sandbox provider install [-y] NAME")
	}
	inst, _ := sandbox.ReadInstalled()
	if len(ix.Providers) == 0 {
		return "", errors.New("the registry has no providers yet")
	}
	for i, e := range ix.Providers {
		h := here(e.Name, inst)
		if h != "" {
			h = "  (" + h + ")"
		}
		fmt.Printf("%2d  %-16s %s, %s%s\n", i+1, e.Name, e.Label, liveCheck(e), h)
	}
	fmt.Print("\nWhich one? ")
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	line = strings.TrimSpace(line)
	if n, err := strconv.Atoi(line); err == nil && n >= 1 && n <= len(ix.Providers) {
		return ix.Providers[n-1].Name, nil
	}
	if _, ok := ix.Find(line); ok {
		return line, nil
	}
	return "", errors.New("nothing installed")
}

func providerInstall(args []string) error {
	fs := flag.NewFlagSet("sandbox provider install", flag.ContinueOnError)
	yes := fs.Bool("y", false, "install without asking")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return errors.New("usage: conch sandbox provider install [-y] NAME")
	}
	ctx, cancel := registryCtx()
	defer cancel()
	ix, err := sandbox.FetchIndex(ctx)
	if err != nil {
		return err
	}
	name := fs.Arg(0)
	if name == "" {
		if name, err = pickEntry(ix); err != nil {
			return err
		}
	}
	e, ok := ix.Find(name)
	if !ok {
		return fmt.Errorf("the registry has no provider %s: conch sandbox provider search lists them, and conch sandbox adapt URL makes one", name)
	}
	return installEntry(ctx, e, *yes)
}

// installEntry installs e, asking first unless yes.
func installEntry(ctx context.Context, e sandbox.RegistryEntry, yes bool) error {
	if err := sandbox.CheckName(e.Name); err != nil {
		return err
	}
	if sandbox.Builtin(e.Name) {
		return fmt.Errorf("%s is built into conch", e.Name)
	}
	if e.Format > sandbox.ProviderFormat {
		return fmt.Errorf("%s's file needs a newer conch (format %d; this one reads up to %d): conch update", e.Name, e.Format, sandbox.ProviderFormat)
	}
	inst, err := sandbox.ReadInstalled()
	if err != nil {
		return err
	}
	if _, err := os.Stat(sandbox.ProviderFilePath(e.Name)); err == nil {
		if _, ok := inst[e.Name]; ok {
			return fmt.Errorf("%s is installed already: conch sandbox provider update %s takes the registry's newest", e.Name, e.Name)
		}
		return fmt.Errorf("%s is already a provider here, a file of your own: remove it first with conch sandbox provider rm %s", e.Name, e.Name)
	}
	describeEntry(os.Stdout, e)
	if !yes {
		fmt.Println()
		if !askYes("Install " + e.Name + "?") {
			return errors.New("nothing installed (-y installs without asking)")
		}
	}
	data, err := sandbox.FetchProviderFile(ctx, e)
	if err != nil {
		return err
	}
	// What the index said and what the file says must agree, or the
	// person was shown the wrong hosts, or the wrong variable: the file's
	// key_env is what is read and sent, so an index saying $ACME_API_KEY
	// over a file reading $GITHUB_TOKEN would send that token unasked.
	// Checked before the file is kept, so it is never there to be used.
	if f, _, err := sandbox.ParseProviderFile(e.Name, data); err == nil {
		if api, ssh := sandbox.FileHosts(f); api != e.APIHost || ssh != e.SSHHost {
			return fmt.Errorf("%s's file names %s and %s, not the hosts the index lists: not installed", e.Name, api, ssh)
		}
		if f.KeyEnv != e.KeyEnv {
			return fmt.Errorf("%s's file reads your key from $%s, not the $%s the index lists: not installed", e.Name, f.KeyEnv, e.KeyEnv)
		}
	}
	f, err := sandbox.InstallProviderFile(e.Name, data)
	if err != nil {
		return err
	}
	fmt.Printf("\ninstalled %s\n", sandbox.ProviderFilePath(e.Name))
	ck := checkProvider(f)
	if ck.Signed {
		printCheck(os.Stdout, &ck)
		fmt.Printf("conch sandbox -provider %s create makes one\n", e.Name)
		return nil
	}
	fmt.Printf("put your key in $%s (or Settings → Sandboxes), then conch sandbox -provider %s create makes one\n", e.KeyEnv, e.Name)
	return nil
}

func providerUpdate(args []string) error {
	fs := flag.NewFlagSet("sandbox provider update", flag.ContinueOnError)
	yes := fs.Bool("y", false, "do not ask; a provider whose hosts or key changed is then left as it is")
	if err := fs.Parse(args); err != nil {
		return err
	}
	inst, err := sandbox.ReadInstalled()
	if err != nil {
		return err
	}
	var names []string
	switch fs.NArg() {
	case 0:
		for n := range inst {
			names = append(names, n)
		}
		if len(names) == 0 {
			fmt.Println("nothing here was installed from the registry")
			return nil
		}
	case 1:
		if _, ok := inst[fs.Arg(0)]; !ok {
			return fmt.Errorf("%s was not installed from the registry", fs.Arg(0))
		}
		names = []string{fs.Arg(0)}
	default:
		return errors.New("usage: conch sandbox provider update [-y] [NAME]")
	}
	slices.Sort(names)
	ctx, cancel := registryCtx()
	defer cancel()
	ix, err := sandbox.FetchIndex(ctx)
	if err != nil {
		return err
	}
	failed := false
	for _, n := range names {
		msg, err := updateOne(ctx, ix, n, inst[n], *yes)
		if err != nil {
			failed = true
			msg = err.Error()
		}
		fmt.Printf("%s: %s\n", n, msg)
	}
	if failed {
		return exitStatus(1)
	}
	return nil
}

func updateOne(ctx context.Context, ix *sandbox.Index, n string, in sandbox.Installed, yes bool) (string, error) {
	if err := sandbox.CheckName(n); err != nil {
		return "", err
	}
	e, ok := ix.Find(n)
	if !ok {
		return "no longer in the registry; kept as it is", nil
	}
	if e.SHA256 == in.SHA256 {
		return "up to date", nil
	}
	edited, err := sandbox.EditedSinceInstall(n, in)
	if err != nil {
		return "", err
	}
	if edited {
		return fmt.Sprintf("edited here since it was installed, so left alone (conch sandbox provider rm %s, then install, takes the registry's)", n), nil
	}
	data, err := sandbox.FetchProviderFile(ctx, e)
	if err != nil {
		return "", err
	}
	f, problems, err := sandbox.ParseProviderFile(n, data)
	if err != nil {
		return "", fmt.Errorf("the registry's new file cannot be used here: %w", err)
	}
	if len(problems) > 0 {
		return "", fmt.Errorf("the registry's new file cannot be used here: %s", strings.Join(problems, "; "))
	}
	// Where the key goes and which key it is are asked about alike: a
	// file that kept its hosts but now reads $GITHUB_TOKEN sends that
	// token to them as surely as new hosts would send the key elsewhere.
	hosts := false
	if api, ssh := sandbox.FileHosts(f); api != in.APIHost || ssh != in.SSHHost {
		fmt.Printf("%s now sends your API key to %s (was %s), and ssh to %s (was %s)\n", n, api, in.APIHost, sshHostWords(ssh), sshHostWords(in.SSHHost))
		hosts = true
	}
	key := false
	if !in.SameKey(f) {
		env, h, v := sandbox.FileKey(f)
		sends := fmt.Sprintf("%s: %s", firstNonEmptyStr(h, "Authorization"), firstNonEmptyStr(v, "Bearer {key}"))
		if in.KeyEnv == "" {
			// Installed by a conch that kept no record of it.
			fmt.Printf("%s now sends $%s as your API key, as %s (what it sent before was not recorded)\n", n, env, sends)
		} else {
			was := fmt.Sprintf("%s: %s", firstNonEmptyStr(in.AuthHeader, "Authorization"), firstNonEmptyStr(in.AuthValue, "Bearer {key}"))
			fmt.Printf("%s now sends $%s as your API key, as %s (was $%s, as %s)\n", n, env, sends, in.KeyEnv, was)
		}
		key = true
	}
	if hosts || key {
		what, q := "its hosts changed", "Take the new hosts?"
		switch {
		case hosts && key:
			what, q = "its hosts and its key changed", "Take the new hosts and key?"
		case key:
			what, q = "the key it sends changed", "Take the new key?"
		}
		if yes || !askYes(q) {
			return what + ": left as it is (run conch sandbox provider update " + n + " to see them and say yes)", nil
		}
	}
	if _, err := sandbox.InstallProviderFile(n, data); err != nil {
		return "", err
	}
	return "updated", nil
}

// releaseVersion is this conch's version without a -dev.
func releaseVersion() string {
	v, _, _ := strings.Cut(strings.TrimPrefix(proto.Version, "v"), "-")
	return v
}

type publishMeta struct {
	AdaptedBy string                 `json:"adapted_by"`
	Adapted   string                 `json:"adapted"`
	Docs      string                 `json:"docs"`
	Conch     string                 `json:"conch"`
	Checked   *sandbox.RegistryCheck `json:"checked"`
}

// publishNow is the clock publishing reads, for tests.
var publishNow = time.Now

func providerPublish(args []string) error {
	fs := flag.NewFlagSet("sandbox provider publish", flag.ContinueOnError)
	dry := fs.Bool("dry-run", false, "show what would be proposed, and propose nothing")
	asJSON := fs.Bool("json", false, "the pull request as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: conch sandbox provider publish [-dry-run] [-json] NAME")
	}
	name := fs.Arg(0)
	if err := sandbox.CheckName(name); err != nil {
		return err
	}
	if sandbox.Builtin(name) {
		return fmt.Errorf("%s is built into conch", name)
	}
	path := sandbox.ProviderFilePath(name)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("no provider %s here: save it first (conch sandbox provider save %s)", name, name)
	}
	if err != nil {
		return err
	}
	f, problems, err := sandbox.ReadProviderFile(path)
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s has problems to fix first: conch sandbox provider check %s", name, name)
	}
	if inst, _ := sandbox.ReadInstalled(); inst != nil {
		if in, ok := inst[name]; ok {
			if edited, _ := sandbox.EditedSinceInstall(name, in); !edited {
				return fmt.Errorf("%s is the registry's own, unchanged: nothing to publish", name)
			}
		}
	}
	if u, err := url.Parse(f.Docs); err != nil || u.Scheme != "https" {
		return fmt.Errorf("%s's file says no docs = \"https://…\": say where it was adapted from, so a reviewer can check it", name)
	}
	// Published files are ones that have worked: the key lists sandboxes
	// with it now, or it is not proposed.
	ck := checkProvider(f)
	if !ck.Signed {
		return fmt.Errorf("publish checks %s with your key first, and could not: %s", name, ck.Why)
	}
	today := publishNow().UTC().Format("2006-01-02")
	meta := publishMeta{AdaptedBy: firstNonEmptyStr(f.AdaptedBy, "a person"), Adapted: firstNonEmptyStr(f.Adapted, today), Docs: f.Docs,
		Conch: releaseVersion(), Checked: &sandbox.RegistryCheck{Date: today, Conch: releaseVersion(), Sandboxes: ck.All}}
	metaJSON, _ := json.MarshalIndent(meta, "", "  ")
	metaJSON = append(metaJSON, '\n')
	api, ssh := sandbox.FileHosts(f)
	title := "Add " + firstNonEmptyStr(f.Label, name)
	body := fmt.Sprintf("Adapted by %s from %s and checked with conch %s: the key listed %d sandboxes.\n\n- API key goes to `%s`\n- ssh goes to `%s`\n\nProposed with `conch sandbox provider publish`.\n",
		meta.AdaptedBy, f.Docs, meta.Conch, ck.All, api, ssh)
	files := []struct {
		path string
		data []byte
	}{{"providers/" + name + "/provider.toml", data}, {"providers/" + name + "/meta.json", metaJSON}}
	if *dry {
		fmt.Printf("would propose to %s: %s\n\n%s", sandbox.RegistryRepo, title, body)
		for _, fl := range files {
			fmt.Printf("\n--- %s\n%s", fl.path, fl.data)
		}
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var user struct {
		Login string `json:"login"`
	}
	if err := ghJSON(ctx, "GET", "user", nil, &user); err != nil {
		return fmt.Errorf("publish opens a pull request with gh, signed in to GitHub (gh auth login): %w", err)
	}
	var repo struct {
		Permissions struct {
			Push bool `json:"push"`
		} `json:"permissions"`
	}
	if err := ghJSON(ctx, "GET", "repos/"+sandbox.RegistryRepo, nil, &repo); err != nil {
		return err
	}
	target, head := sandbox.RegistryRepo, ""
	branch := fmt.Sprintf("provider-%s-%s", name, publishNow().UTC().Format("20060102-150405"))
	if repo.Permissions.Push {
		head = branch
	} else {
		var fork struct {
			FullName string `json:"full_name"`
		}
		if err := ghJSON(ctx, "POST", "repos/"+sandbox.RegistryRepo+"/forks", map[string]any{"default_branch_only": true}, &fork); err != nil {
			return fmt.Errorf("fork %s: %w", sandbox.RegistryRepo, err)
		}
		target, head = fork.FullName, user.Login+":"+branch
		// A fork made before stays where it was; bring it up to date, or
		// the pull request would undo what was merged since.
		_, _ = ghx.API(ctx, "POST", "repos/"+target+"/merge-upstream", map[string]any{"branch": "main"})
	}
	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if err := ghJSON(ctx, "GET", "repos/"+target+"/git/ref/heads/main", nil, &ref); err != nil {
		return err
	}
	if _, err := ghx.API(ctx, "POST", "repos/"+target+"/git/refs", map[string]any{"ref": "refs/heads/" + branch, "sha": ref.Object.SHA}); err != nil {
		return fmt.Errorf("make a branch in %s: %w", target, err)
	}
	for _, fl := range files {
		put := map[string]any{"message": title + ": " + fl.path, "branch": branch, "content": base64.StdEncoding.EncodeToString(fl.data)}
		var existing struct {
			SHA string `json:"sha"`
		}
		// Replacing a file needs the one it replaces; a new one has none.
		if err := ghJSON(ctx, "GET", "repos/"+target+"/contents/"+fl.path+"?ref="+branch, nil, &existing); err == nil && existing.SHA != "" {
			put["sha"] = existing.SHA
		}
		if _, err := ghx.API(ctx, "PUT", "repos/"+target+"/contents/"+fl.path, put); err != nil {
			return fmt.Errorf("write %s: %w", fl.path, err)
		}
	}
	var pr struct {
		URL string `json:"html_url"`
	}
	if err := ghJSON(ctx, "POST", "repos/"+sandbox.RegistryRepo+"/pulls", map[string]any{"title": title, "head": head, "base": "main", "body": body}, &pr); err != nil {
		return fmt.Errorf("open the pull request: %w", err)
	}
	if *asJSON {
		b, _ := json.Marshal(map[string]string{"url": pr.URL})
		fmt.Println(string(b))
		return nil
	}
	fmt.Printf("proposed %s to the registry: %s\nit is listed once a maintainer has reviewed and merged it\n", name, pr.URL)
	return nil
}

func ghJSON(ctx context.Context, method, path string, body, out any) error {
	b, err := ghx.API(ctx, method, path, body)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("gh api %s: %w", path, err)
	}
	return nil
}

// registryHas is a provider in the registry that adapting urls as name
// would make again. Any failure to reach the registry is no: adapting
// still works without it.
func registryHas(urls []string, name string) (sandbox.RegistryEntry, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ix, err := sandbox.FetchIndex(ctx)
	if err != nil {
		return sandbox.RegistryEntry{}, false
	}
	if e, ok := ix.Find(name); ok {
		return e, true
	}
	for _, raw := range urls {
		if u, err := url.Parse(raw); err == nil {
			if e, ok := ix.ForHost(u.Hostname()); ok {
				return e, true
			}
		}
	}
	return sandbox.RegistryEntry{}, false
}

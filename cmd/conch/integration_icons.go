package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/Amitgb14/conch/internal/integration"
)

// iconsJSON is what Conch.app draws the catalog with: the registry's kinds
// in its order, and each integration's icon as a file here.
type iconsJSON struct {
	Kinds    []integration.KindInfo `json:"kinds"`
	Icons    map[string]string      `json:"icons"`
	Cached   bool                   `json:"cached,omitempty"`
	Problems []string               `json:"problems,omitempty"`
}

// defaultKinds are the kinds when no registry says them: the seven this
// conch has code for.
var defaultKinds = []integration.KindInfo{
	{Kind: integration.KindHost, Label: "Code hosts", About: "where the code and its pull requests live"},
	{Kind: integration.KindCI, Label: "CI", About: "what runs a branch's checks"},
	{Kind: integration.KindTickets, Label: "Ticket trackers", About: "where the work is written down"},
	{Kind: integration.KindSandbox, Label: "Sandboxes", About: "machines in the cloud for agents"},
	{Kind: integration.KindDeploy, Label: "Deploy previews", About: "where a branch is built and shown, and released to production"},
	{Kind: integration.KindNotify, Label: "Notifications", About: "where conch says an agent is waiting"},
	{Kind: integration.KindImport, Label: "Import", About: "other apps whose conversations conch can bring in"},
}

// iconFetchers bounds the icons downloaded at once.
const iconFetchers = 6

// integrationIcons is conch integration icons [-json]: every registry's
// icons, downloaded once and checked against its index, and its kinds. A
// registry out of reach gives what was last read of it.
func integrationIcons(args []string) error {
	fs := flag.NewFlagSet("integration icons", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return errors.New("usage: conch integration icons [-json]")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out := iconsJSON{Icons: map[string]string{}}
	type job struct {
		r integration.Registry
		e integration.Entry
	}
	var jobs []job
	for _, r := range integrationRegistries() {
		ix, err := integration.IndexOrCached(ctx, r)
		if err != nil {
			out.Problems = append(out.Problems, err.Error())
			continue
		}
		out.Cached = out.Cached || ix.Cached
		if out.Kinds == nil && len(ix.Kinds) > 0 {
			out.Kinds = ix.Kinds // the first registry's, conch's own
		}
		for _, e := range ix.Entries {
			if _, taken := out.Icons[e.Name]; e.Icon != "" && !taken {
				out.Icons[e.Name] = ""
				jobs = append(jobs, job{r, e})
			}
		}
	}
	if out.Kinds == nil {
		out.Kinds = defaultKinds
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, iconFetchers)
	for _, j := range jobs {
		wg.Add(1)
		go func(j job) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			p, err := integration.FetchIcon(ctx, j.r, j.e)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				out.Problems = append(out.Problems, err.Error())
				delete(out.Icons, j.e.Name)
				return
			}
			out.Icons[j.e.Name] = p
		}(j)
	}
	wg.Wait()
	sort.Strings(out.Problems)
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(out)
	}
	for _, k := range out.Kinds {
		fmt.Printf("%s\t%s\n", k.Kind, k.Label)
	}
	names := make([]string, 0, len(out.Icons))
	for n := range out.Icons {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Printf("%s\t%s\n", n, out.Icons[n])
	}
	for _, p := range out.Problems {
		fmt.Fprintln(os.Stderr, p)
	}
	return nil
}

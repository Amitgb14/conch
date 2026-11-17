package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// runServerHeap is `conch server heap`: the running server writes its heap
// profile and its goroutines to files and says how much memory it holds,
// without stopping or reloading — how a server that keeps growing is
// looked into. Two snapshots compared with `go tool pprof -base OLD NEW`
// name the code holding what was added between them.
func runServerHeap(args []string) error {
	fs := flag.NewFlagSet("server heap", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print the snapshot as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("conch server heap takes no arguments, not %q", fs.Arg(0))
	}
	c, err := connect(false)
	if err != nil {
		return downTo("look at", err)
	}
	defer c.Close()
	if len(c.MissingCapabilities([]string{proto.CapServerHeap})) > 0 {
		return fmt.Errorf("the conch server (pid %d) predates heap snapshots; reload it with `conch server reload`", c.Server.PID)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var h proto.ServerHeap
	if err := c.Call(ctx, proto.MethodServerHeap, nil, &h); err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(h)
	}
	fmt.Print(heapWords(h, c.Server.PID, machineFlag))
	return nil
}

// heapWords is a snapshot as a person reads it: the memory first, then
// where the files are and what to do with them. machine is the -m
// flag's: the files are on that machine, not this one.
func heapWords(h proto.ServerHeap, pid int, machine string) string {
	where := ""
	if machine != "" && machine != "local" {
		where = " (on " + machine + ")"
	}
	return fmt.Sprintf("server pid %d: %s live in %s objects · heap %s · %s from the system · %d goroutines · %d panes\n"+
		"heap profile%s:  %s\n"+
		"goroutines%s:    %s\n"+
		"what holds it:   go tool pprof -top -sample_index=inuse_space %s\n"+
		"what grew since: go tool pprof -top -sample_index=inuse_space -base OLDER.pb.gz %s\n",
		pid, bytesWords(h.HeapAlloc), countWords(h.HeapObjects), bytesWords(h.HeapInuse), bytesWords(h.Sys), h.Goroutine, h.Panes,
		where, h.Heap, where, h.Goroutines, h.Heap, h.Heap)
}

// bytesWords is n bytes in the unit a person reads it in.
func bytesWords(n uint64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.0f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// countWords is a count of objects, rounded where its last digits say
// nothing.
func countWords(n uint64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 10_000:
		return fmt.Sprintf("%.0fK", float64(n)/1e3)
	}
	return fmt.Sprintf("%d", n)
}

package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/Amitgb14/conch/internal/proto"
)

// conch view and conch show are the screen's half of the CLI: what the
// person is looking at, and a request to put panes in front of them. The
// server owns panes and the terminal owns the arrangement of them, so the
// server carries a TUI's answer here and the request back (internal/server/view.go).

func runView(args []string) error {
	fs := flag.NewFlagSet("view", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return errors.New("usage: conch view")
	}
	c, err := connect(false)
	if err != nil {
		return err
	}
	defer c.Close()
	if len(c.MissingCapabilities([]string{proto.CapView})) > 0 {
		return errors.New("the server here predates `conch view`; reload it with `conch server reload`")
	}
	var v proto.ViewState
	if err := call(c, proto.MethodViewGet, nil, &v); err != nil {
		return err
	}
	if len(v.Tabs) == 0 {
		fmt.Println("no terminal is showing this machine just now")
		return nil
	}
	for _, t := range v.Tabs {
		mark := " "
		if t.Active {
			mark = "*"
		}
		fmt.Printf("%s %s  %s\n", mark, t.Name, strings.Join(t.Panes, " "))
	}
	if v.Focus != "" {
		fmt.Printf("keyboard: %s\n", v.Focus)
	} else {
		fmt.Println("keyboard: the tree")
	}
	return nil
}

func runShow(args []string) error {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	name := fs.String("name", "", "what to call the tab, when one is opened")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("usage: conch show [-name NAME] ID [ID...]")
	}
	c, err := connect(false)
	if err != nil {
		return err
	}
	defer c.Close()
	if len(c.MissingCapabilities([]string{proto.CapView})) > 0 {
		return errors.New("the server here predates `conch show`; reload it with `conch server reload`")
	}
	ids := make([]string, 0, fs.NArg())
	for _, ref := range fs.Args() {
		id, err := resolvePane(c, ref)
		if err != nil {
			return err
		}
		ids = append(ids, id)
	}
	var res proto.ViewShowResult
	if err := call(c, proto.MethodViewShow, proto.ViewShowParams{Panes: ids, Name: *name, Why: mcpWho()}, &res); err != nil {
		return err
	}
	// A request, not a fact: nothing here knows whether a terminal acted.
	what := "asked for " + strings.Join(res.Panes, " ")
	if len(res.Panes) > 1 {
		what += " side by side"
	}
	fmt.Fprintln(os.Stderr, what)
	return nil
}

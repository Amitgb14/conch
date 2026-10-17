package main

import (
	"cmp"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// conch review is the changes view's comments from the command line: the
// same ones, kept by the server, so a reviewing agent can leave them for
// the person to read and send, or a person can send them from a shell.

const reviewUsage = `Usage:
  conch review      [-cwd DIR] [-branch B]                 list the comments waiting for the branch's agent
  conch review add  [-cwd DIR] [-branch B] FILE[:LINE] NOTE
                                                           comment on a file, or a line of it
  conch review rm   [-cwd DIR] [-branch B] ID              take a comment away (not one from GitHub)
  conch review send [-cwd DIR] [-branch B] [-agent A]      hand them to the branch's agent, or start A with them

Without -branch, the branch checked out in DIR (the current directory) is used.
The list includes the unresolved comments on the branch's pull request, read
through gh; sending hands them over too, and they stay on GitHub.
In the TUI's changes view, a comments a hunk and S sends.`

// reviewWait covers a send, which types into an agent and may start one.
const reviewWait = time.Minute

func runReview(args []string) error {
	sub := "ls"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "ls", "list":
		return reviewList(args)
	case "add":
		return reviewAdd(args)
	case "rm", "remove":
		return reviewRemove(args)
	case "send":
		return reviewSendCmd(args)
	case "help", "-h", "--help":
		fmt.Print(reviewUsage + "\n")
		return nil
	}
	return fmt.Errorf("unknown review command %q\n\n%s", sub, reviewUsage)
}

// openReview is branchFlags.open for a server that keeps review comments.
func openReview(bf *branchFlags, args []string) (*client.Client, string, string, error) {
	c, id, branch, err := bf.open(args)
	if err != nil {
		return nil, "", "", err
	}
	if miss := c.MissingCapabilities([]string{proto.CapReviewComments}); len(miss) > 0 {
		c.Close()
		return nil, "", "", fmt.Errorf("the conch server (pid %d) predates review comments; reload it with `conch server reload`", c.Server.PID)
	}
	return c, id, branch, nil
}

func reviewList(args []string) error {
	bf := newBranchFlags("review")
	c, id, branch, err := openReview(bf, args)
	if err != nil {
		return err
	}
	defer c.Close()
	switch sub := bf.fs.Arg(0); sub {
	case "":
	case "add", "rm", "remove", "send", "ls", "list":
		// The flags were put first: they belong after the command.
		return fmt.Errorf("the command goes before the flags: conch review %s -branch B …", sub)
	default:
		return fmt.Errorf("unknown review command %q\n\n%s", sub, reviewUsage)
	}
	var list proto.ReviewList
	if err := call(c, proto.MethodReviewList, proto.ReviewRef{ProjectID: id, Branch: branch}, &list); err != nil {
		return err
	}
	if len(list.Comments) == 0 {
		fmt.Printf("no review comments on %s\n", branch)
		return nil
	}
	for _, rc := range list.Comments {
		fmt.Printf("%s  %s%s\n", rc.ID, proto.ReviewWhere(rc), reviewBy(rc))
		for _, line := range strings.Split(rc.Note, "\n") {
			fmt.Printf("    %s\n", line)
		}
		for _, r := range rc.Replies {
			fmt.Printf("    ↳ %s: %s\n", cmp.Or(r.By, "someone"), strings.ReplaceAll(r.Note, "\n", "\n      "))
		}
		if rc.URL != "" {
			fmt.Printf("    %s\n", rc.URL)
		}
	}
	fmt.Printf("conch review send hands %s to the agent on %s\n", counted(len(list.Comments), "comment"), branch)
	return nil
}

// reviewBy says who wrote a comment and whether it is out of date.
func reviewBy(rc proto.ReviewComment) string {
	var about []string
	if a := proto.ReviewAuthor(rc); a != "" {
		about = append(about, a)
	}
	if rc.Outdated {
		about = append(about, "outdated")
	}
	if len(about) == 0 {
		return ""
	}
	return "  (" + strings.Join(about, ", ") + ")"
}

// splitFileLine reads FILE or FILE:LINE. A name with a colon of its own
// keeps it, unless what follows the last one is a line number.
func splitFileLine(arg string) (string, int, error) {
	if i := strings.LastIndex(arg, ":"); i > 0 {
		if n, err := strconv.Atoi(arg[i+1:]); err == nil {
			if n <= 0 {
				return "", 0, fmt.Errorf("%s: lines count from 1", arg)
			}
			return arg[:i], n, nil
		}
	}
	if arg == "" {
		return "", 0, errors.New("comment on which file?")
	}
	return arg, 0, nil
}

func reviewAdd(args []string) error {
	bf := newBranchFlags("review add")
	c, id, branch, err := openReview(bf, args)
	if err != nil {
		return err
	}
	defer c.Close()
	if bf.fs.NArg() < 2 {
		return fmt.Errorf("conch review add FILE[:LINE] NOTE\n\n%s", reviewUsage)
	}
	file, line, err := splitFileLine(bf.fs.Arg(0))
	if err != nil {
		return err
	}
	note := strings.TrimSpace(strings.Join(bf.fs.Args()[1:], " "))
	if note == "" {
		return errors.New("an empty comment says nothing; conch review rm ID takes one away")
	}
	var list proto.ReviewList
	rc := proto.ReviewComment{ProjectID: id, Branch: branch, File: file, Line: line, Note: note}
	if err := call(c, proto.MethodReviewSet, rc, &list); err != nil {
		return err
	}
	fmt.Printf("%s on %s · conch review send hands them to its agent\n", counted(len(list.Comments), "comment"), branch)
	return nil
}

func reviewRemove(args []string) error {
	bf := newBranchFlags("review rm")
	c, id, branch, err := openReview(bf, args)
	if err != nil {
		return err
	}
	defer c.Close()
	if bf.fs.NArg() != 1 {
		return fmt.Errorf("conch review rm ID (conch review lists them)")
	}
	var list proto.ReviewList
	if err := call(c, proto.MethodReviewSet, proto.ReviewComment{ID: bf.fs.Arg(0), ProjectID: id, Branch: branch}, &list); err != nil {
		return err
	}
	fmt.Printf("removed; %s left on %s\n", counted(len(list.Comments), "comment"), branch)
	return nil
}

func reviewSendCmd(args []string) error {
	bf := newBranchFlags("review send")
	agent := bf.fs.String("agent", "", "with no agent on the branch, start this one in its worktree with the comments")
	c, id, branch, err := openReview(bf, args)
	if err != nil {
		return err
	}
	defer c.Close()
	if bf.fs.NArg() > 0 {
		return fmt.Errorf("conch review send takes no arguments; -agent A starts one when none is there")
	}
	var res proto.ReviewSendResult
	params := proto.ReviewSendParams{ProjectID: id, Branch: branch, Agent: *agent, Cols: 120, Rows: 40}
	if err := callFor(c, proto.MethodReviewSend, params, &res, reviewWait); err != nil {
		var perr *proto.Error
		if errors.As(err, &perr) && perr.Code == proto.ErrNotFound && *agent == "" && strings.Contains(perr.Message, "name one") {
			return fmt.Errorf("%v: conch review send -agent claude", err)
		}
		return err
	}
	if res.Started {
		fmt.Printf("started %s in %s on %s with %s\n", *agent, res.Pane.ID, branch, counted(res.Sent, "review comment"))
		return nil
	}
	fmt.Printf("sent %s to %s on %s\n", counted(res.Sent, "review comment"), res.Pane.ID, branch)
	return nil
}

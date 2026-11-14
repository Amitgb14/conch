package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/tracker"
)

// conch ticket reads and changes tickets in Linear and Jira, through the
// server on the machine the project is on — which holds the key, as it
// holds gh's sign-in for issues. It changes a ticket only when told to:
// nothing here moves a ticket on its own.

const ticketUsage = `Usage:
  conch ticket ls      [-cwd DIR] [-tracker linear|jira] [-mine] [-n N] [-json]
                                         the project's open tickets, most recently changed first
  conch ticket show    [-cwd DIR] [-json] TICKET
                                         a ticket: its status, body, comments and links
  conch ticket comment [-cwd DIR] TICKET TEXT...
                                         add a comment
  conch ticket status  [-cwd DIR] TICKET STATUS...
                                         move it to a status, as the tracker names it (In Review)
  conch ticket link    [-cwd DIR] [-title T] TICKET URL
                                         attach a link: a branch, a pull request
  conch ticket check   [-cwd DIR] [-json]
                                         whether GitHub, Linear and Jira can be reached with
                                         what the server has: whose key, open tickets, what is wrong

TICKET is its key (ENG-123), its key after the tracker (linear:ENG-123,
jira:PROJ-42) or its address. Which tracker, and the key it is reached
with, are set up in [trackers] in config.toml (conch config web).`

func runTicket(args []string) error {
	if len(args) == 0 {
		return errors.New(ticketUsage)
	}
	sub, args := args[0], args[1:]
	switch sub {
	case "ls", "list":
		return ticketList(args)
	case "show", "view":
		return ticketShow(args)
	case "comment":
		return ticketChange("comment", args)
	case "status", "move":
		return ticketChange("status", args)
	case "link":
		return ticketChange("link", args)
	case "check":
		return ticketCheck(args)
	case "help", "-h", "--help":
		fmt.Println(ticketUsage)
		return nil
	}
	return unknownSubcommand("ticket", sub)
}

// openTickets connects, checks the server can reach trackers, and finds
// the project dir is in — none when it is in no project, since a ticket
// named with its tracker needs none.
func openTickets(dir string) (*client.Client, string, error) {
	if err := checkDir(dir); err != nil {
		return nil, "", err
	}
	c, err := connect(true)
	if err != nil {
		return nil, "", err
	}
	if len(c.MissingCapabilities([]string{proto.CapTickets})) > 0 {
		c.Close()
		return nil, "", errors.New("the conch server there predates tickets from Linear and Jira; reload it with `conch server reload`")
	}
	at, err := place(c, dir)
	if err != nil && dir != "" {
		c.Close()
		return nil, "", err
	}
	return c, at.ProjectID, nil
}

func ticketList(args []string) error {
	fs := flag.NewFlagSet("ticket ls", flag.ContinueOnError)
	cwd := fs.String("cwd", "", "a directory in the project (default: current)")
	which := fs.String("tracker", "", "linear or jira, when both are set up")
	mine := fs.Bool("mine", false, "only the tickets assigned to you")
	n := fs.Int("n", 30, "how many")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if w := strings.ToLower(*which); w != "" && w != tracker.Linear && w != tracker.Jira {
		return fmt.Errorf("-tracker is linear or jira, not %q", *which)
	}
	c, project, err := openTickets(*cwd)
	if err != nil {
		return err
	}
	defer c.Close()
	var list proto.TicketList
	if err := callFor(c, proto.MethodProjectTickets, proto.TicketsParams{ProjectID: project, Tracker: strings.ToLower(*which), Assigned: *mine, Limit: *n}, &list, harvestWait); err != nil {
		return err
	}
	if *asJSON {
		return printJSON(list)
	}
	if len(list.Tickets) == 0 {
		fmt.Fprintf(os.Stderr, "no open tickets in %s\n", tracker.Title(list.Tracker))
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, t := range list.Tickets {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", t.Key, t.Status, t.Title)
	}
	return tw.Flush()
}

func ticketShow(args []string) error {
	fs := flag.NewFlagSet("ticket show", flag.ContinueOnError)
	cwd := fs.String("cwd", "", "a directory in the project (default: current)")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: conch ticket show [-cwd DIR] [-json] TICKET")
	}
	ref := fs.Arg(0)
	if _, _, err := tracker.ParseRef(ref); err != nil {
		return err
	}
	c, project, err := openTickets(*cwd)
	if err != nil {
		return err
	}
	defer c.Close()
	var t proto.TicketInfo
	if err := callFor(c, proto.MethodTicketGet, proto.TicketGetParams{ProjectID: project, Ticket: ref}, &t, harvestWait); err != nil {
		return err
	}
	if *asJSON {
		return printJSON(t)
	}
	fmt.Printf("%s  %s\n", t.Key, t.Title)
	if line := strings.Join(nonEmpty(t.Status, t.URL), " · "); line != "" {
		fmt.Println(line)
	}
	if b := strings.TrimSpace(t.Body); b != "" {
		fmt.Printf("\n%s\n", b)
	}
	if len(t.Comments) > 0 {
		fmt.Println("\nComments:")
		for _, cm := range t.Comments {
			who := cm.Author
			if who == "" {
				who = "?"
			}
			fmt.Printf("  %s: %s\n", who, strings.ReplaceAll(strings.TrimSpace(cm.Body), "\n", "\n    "))
		}
	}
	if len(t.Links) > 0 {
		fmt.Println("\nLinks:")
		for _, l := range t.Links {
			fmt.Printf("  %s\n", strings.Join(nonEmpty(l.Title, l.URL), "  "))
		}
	}
	return nil
}

// ticketChange comments on a ticket, moves it or links something to it.
func ticketChange(what string, args []string) error {
	fs := flag.NewFlagSet("ticket "+what, flag.ContinueOnError)
	cwd := fs.String("cwd", "", "a directory in the project (default: current)")
	title := ""
	if what == "link" {
		fs.StringVar(&title, "title", "", "what the link is called in the ticket")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	usage := map[string]string{
		"comment": "usage: conch ticket comment [-cwd DIR] TICKET TEXT...",
		"status":  "usage: conch ticket status [-cwd DIR] TICKET STATUS...",
		"link":    "usage: conch ticket link [-cwd DIR] [-title T] TICKET URL",
	}[what]
	if fs.NArg() < 2 || (what == "link" && fs.NArg() != 2) {
		return errors.New(usage)
	}
	ref, rest := fs.Arg(0), strings.TrimSpace(strings.Join(fs.Args()[1:], " "))
	if _, _, err := tracker.ParseRef(ref); err != nil {
		return err
	}
	up := proto.TicketUpdateParams{Ticket: ref}
	switch what {
	case "comment":
		up.Comment = rest
	case "status":
		up.Status = rest
	case "link":
		if u, err := url.Parse(rest); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return fmt.Errorf("%q is not an address to link; give its https:// URL", rest)
		}
		up.LinkURL, up.LinkTitle = rest, strings.TrimSpace(title)
	}
	c, project, err := openTickets(*cwd)
	if err != nil {
		return err
	}
	defer c.Close()
	up.ProjectID = project
	var t proto.TicketInfo
	if err := callFor(c, proto.MethodTicketUpdate, up, &t, harvestWait); err != nil {
		return err
	}
	key := firstNonEmptyStr(t.Key, ref)
	switch what {
	case "comment":
		fmt.Printf("commented on %s\n", key)
	case "status":
		fmt.Printf("%s is %s\n", key, firstNonEmptyStr(t.Status, up.Status))
	case "link":
		fmt.Printf("linked %s to %s\n", up.LinkURL, key)
	}
	return nil
}

func printJSON(v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(b))
	return nil
}

func nonEmpty(s ...string) []string {
	var out []string
	for _, v := range s {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// ticketCheck says how each tracker stands, as Settings → Trackers does:
// connected and whose, off, or what went wrong — never the key.
func ticketCheck(args []string) error {
	fs := flag.NewFlagSet("ticket check", flag.ContinueOnError)
	cwd := fs.String("cwd", "", "a directory in the project, for its counts (default: current)")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	c, project, err := openTickets(*cwd)
	if err != nil {
		return err
	}
	defer c.Close()
	if len(c.MissingCapabilities([]string{proto.CapTicketCheck})) > 0 {
		return errors.New("the conch server there predates checking trackers; reload it with `conch server reload`")
	}
	var res proto.TicketCheckResult
	if err := callFor(c, proto.MethodTicketCheck, proto.TicketCheckParams{ProjectID: project}, &res, harvestWait); err != nil {
		return err
	}
	if *asJSON {
		return printJSON(res)
	}
	fmt.Print(trackerLines(res))
	return nil
}

// trackerLines is a check as text: a line for each tracker, then each
// project's entry.
func trackerLines(res proto.TicketCheckResult) string {
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	for _, t := range res.Trackers {
		name := "GitHub"
		if t.Tracker != "github" {
			name = tracker.Title(t.Tracker)
		}
		var what []string
		switch t.State {
		case proto.TrackerConnected:
			who := strings.Join(nonEmpty(t.Who, t.Workspace), " · ")
			if t.KeyFrom != "" {
				who += " (" + t.KeyFrom + ")"
			}
			what = append(what, who)
			if t.Open > 0 || t.Area != "" {
				n := fmt.Sprint(t.Open)
				if t.More {
					n += "+"
				}
				in := ""
				if t.Area != "" {
					in = " in " + t.Area
				}
				what = append(what, fmt.Sprintf("%s open, %d yours%s", n, t.Mine, in))
			}
			if t.Error != "" {
				what = append(what, t.Error)
			}
		case proto.TrackerOff:
			what = append(what, nonEmpty(t.Error, "not set up ([trackers] in config.toml)")[0])
		default:
			what = append(what, t.Error)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", name, t.State, strings.Join(what, " · "))
	}
	tw.Flush()
	if len(res.Projects) > 0 {
		b.WriteString("\nprojects:\n")
		tw = tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
		for _, p := range res.Projects {
			state := fmt.Sprintf("%d open", p.Open)
			if p.More {
				state = fmt.Sprintf("%d+ open", p.Open)
			}
			if p.Error != "" {
				state = p.Error
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", p.Project, p.Tracker, p.Area, state)
		}
		tw.Flush()
	}
	return b.String()
}

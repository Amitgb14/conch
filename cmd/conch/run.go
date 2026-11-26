package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// conch run: a job split into tasks that depend on one another, kept by
// the server (internal/server/graph.go). A coordinator makes the run and
// its tasks and waits; the server starts each task as what it waits for is
// done; each worker says once that it is done or failed, and may ask the
// coordinator a question; a gate waits for the person.

const runUsage = `usage: conch run ls | new [-cwd DIR] [-manual] NAME | add [-id ID] [-after A,B] [-agent A] [-gate TEXT] RUN SPEC... | show [-json] RUN | start RUN [TASK] | wait [-timeout D] RUN | done [-run R -task T] RESULT... | fail [-run R -task T] WHY... | ask [-run R -task T] [-option O]... [-default O] [-timeout D] QUESTION... | answer RUN QUESTION ANSWER... | approve RUN TASK | deny RUN TASK | rm RUN`

func runRun(args []string) error {
	if len(args) == 0 {
		args = []string{"ls"}
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "ls", "list", "new", "add", "show", "start", "wait", "done", "fail", "ask", "answer", "approve", "deny", "rm", "remove":
	default:
		return errors.New(runUsage)
	}
	c, err := connect(sub == "new")
	if err != nil {
		return err
	}
	defer c.Close()
	if err := needRuns(c); err != nil {
		return err
	}
	switch sub {
	case "ls", "list":
		if len(rest) != 0 {
			return errors.New(runUsage)
		}
		return runList(c, os.Stdout)
	case "new":
		return runNewCmd(c, rest)
	case "add":
		return runAddCmd(c, rest)
	case "show":
		return runShowCmd(c, rest)
	case "start":
		if len(rest) < 1 || len(rest) > 2 {
			return errors.New(runUsage)
		}
		p := proto.RunStartParams{Run: rest[0]}
		if len(rest) == 2 {
			p.Task = rest[1]
		}
		var r proto.Run
		if err := callFor(c, proto.MethodRunStart, p, &r, harvestWait); err != nil {
			return err
		}
		printRun(os.Stdout, r)
		return nil
	case "wait":
		return runWaitCmd(c, rest)
	case "done", "fail":
		return runReportCmd(c, sub, rest)
	case "ask":
		return runAskCmd(c, rest)
	case "answer":
		if len(rest) < 3 {
			return errors.New("usage: conch run answer RUN QUESTION ANSWER...")
		}
		var r proto.Run
		p := proto.RunAnswerParams{Run: rest[0], Question: rest[1], Answer: strings.Join(rest[2:], " ")}
		if err := call(c, proto.MethodRunAnswer, p, &r); err != nil {
			return err
		}
		fmt.Printf("answered %s\n", p.Question)
		return nil
	case "approve", "deny":
		if len(rest) != 2 {
			return fmt.Errorf("usage: conch run %s RUN TASK", sub)
		}
		var r proto.Run
		if err := call(c, proto.MethodRunApprove, proto.RunApproveParams{Run: rest[0], Task: rest[1], Deny: sub == "deny"}, &r); err != nil {
			return err
		}
		if sub == "deny" {
			fmt.Printf("turned down %s of %s: it will not run\n", rest[1], r.Name)
		} else {
			fmt.Printf("approved %s of %s\n", rest[1], r.Name)
		}
		return nil
	case "rm", "remove":
		if len(rest) != 1 {
			return errors.New("usage: conch run rm RUN")
		}
		return call(c, proto.MethodRunRemove, proto.RunRef{Run: rest[0]}, nil)
	}
	return errors.New(runUsage)
}

func needRuns(c *client.Client) error {
	if len(c.MissingCapabilities([]string{proto.CapRuns})) > 0 {
		return errors.New("the conch server there predates runs; reload it with `conch server reload`")
	}
	return nil
}

func runList(c *client.Client, w io.Writer) error {
	var list proto.RunList
	if err := call(c, proto.MethodRunList, nil, &list); err != nil {
		return err
	}
	if len(list.Runs) == 0 {
		fmt.Fprintln(w, "no runs")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tTASKS\tWAITING ON")
	for _, r := range list.Runs {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.ID, r.Name, runCounts(r), orDash(runAttention(r)))
	}
	return tw.Flush()
}

// runCounts is how many tasks are where: "2 done, 1 running".
func runCounts(r proto.Run) string {
	if len(r.Tasks) == 0 {
		return "none"
	}
	order := []string{proto.RunDone, proto.RunRunning, proto.RunReady, proto.RunPending, proto.RunBlocked, proto.RunFailed}
	n := map[string]int{}
	for _, t := range r.Tasks {
		n[t.Status]++
	}
	var out []string
	for _, s := range order {
		if n[s] > 0 {
			out = append(out, fmt.Sprintf("%d %s", n[s], s))
		}
	}
	return strings.Join(out, ", ")
}

// runAttention is what a run waits on somebody for: open questions and
// gates.
func runAttention(r proto.Run) string {
	var out []string
	for _, q := range r.Questions {
		if q.Open() {
			out = append(out, "question "+q.ID)
		}
	}
	for _, t := range r.Tasks {
		if t.GateWaits() {
			out = append(out, "approval of "+t.ID)
		}
	}
	return strings.Join(out, ", ")
}

func printRun(w io.Writer, r proto.Run) {
	fmt.Fprintf(w, "run %s (%s)", r.Name, r.ID)
	if r.Coordinator != "" {
		fmt.Fprintf(w, ", coordinated by %s", r.Coordinator)
	}
	if r.Manual {
		fmt.Fprint(w, ", tasks start when asked (conch run start)")
	}
	fmt.Fprintln(w)
	if len(r.Tasks) == 0 {
		fmt.Fprintf(w, "no tasks yet: conch run add %s SPEC\n", r.Name)
	} else {
		tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "TASK\tSTATUS\tAFTER\tPANE\tBRANCH\tNOTE")
		for _, t := range r.Tasks {
			note := t.Result
			if note == "" {
				note = t.Why
			}
			if note == "" {
				note = oneLineText(t.Spec, 50)
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", t.ID, t.Status, orDash(strings.Join(t.After, ",")), orDash(t.Pane), orDash(t.Branch), oneLineText(note, 70))
		}
		tw.Flush()
	}
	for _, q := range r.Questions {
		state := "open"
		switch {
		case q.Answered:
			state = "answered " + q.Answer
		case q.TimedOut && q.Answer != "":
			state = "timed out, took " + q.Answer
		case q.TimedOut:
			state = "timed out"
		}
		opts := ""
		if len(q.Options) > 0 {
			opts = " [" + strings.Join(q.Options, "/") + "]"
		}
		fmt.Fprintf(w, "%s from %s (%s): %s%s\n", q.ID, orDash(q.Task), state, oneLineText(q.Text, 80), opts)
	}
	for _, t := range r.Tasks {
		if t.GateWaits() {
			fmt.Fprintf(w, "%s waits for your approval: %s — conch run approve %s %s\n", t.ID, t.Gate, r.Name, t.ID)
		}
	}
}

func runNewCmd(c *client.Client, args []string) error {
	fs := flag.NewFlagSet("run new", flag.ContinueOnError)
	cwd := fs.String("cwd", "", "a directory in the project (default: current)")
	manual := fs.Bool("manual", false, "start tasks only when asked (conch run start), not as soon as they are ready")
	if err := fs.Parse(args); err != nil {
		return err
	}
	name := strings.Join(fs.Args(), " ")
	if strings.TrimSpace(name) == "" {
		return errors.New("usage: conch run new [-cwd DIR] [-manual] NAME")
	}
	dir := *cwd
	switch {
	case onRemoteMachine() && dir == "":
		return errors.New("a run works in a project: give -cwd, a folder of the project on that machine")
	case onRemoteMachine():
		if err := remoteDir(dir); err != nil {
			return err
		}
	case dir == "":
		dir, _ = os.Getwd()
	}
	var proj proto.ProjectInfo
	if err := callFor(c, proto.MethodProjectAdd, proto.ProjectAddParams{Path: dir}, &proj, harvestWait); err != nil {
		return err
	}
	if !proj.Git {
		return fmt.Errorf("%s is not a git repository: a run's tasks each work on a branch of their own", proj.Name)
	}
	var r proto.Run
	if err := call(c, proto.MethodRunCreate, proto.RunCreateParams{Name: name, ProjectID: proj.ID, Manual: *manual}, &r); err != nil {
		return err
	}
	fmt.Printf("%s: run %s in %s — add tasks with conch run add %s SPEC\n", r.ID, r.Name, proj.Name, r.ID)
	return nil
}

func runAddCmd(c *client.Client, args []string) error {
	fs := flag.NewFlagSet("run add", flag.ContinueOnError)
	id := fs.String("id", "", "the task's id within the run (default: t1, t2…)")
	after := fs.String("after", "", "the tasks it waits for, by id, separated by commas")
	agent := fs.String("agent", "", "the agent to run it (default: [agents] default)")
	gate := fs.String("gate", "", "wait for a person's approval before it starts, asking this")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 2 {
		return errors.New("usage: conch run add [-id ID] [-after A,B] [-agent A] [-gate TEXT] RUN SPEC...")
	}
	sp := proto.RunTaskSpec{ID: *id, Spec: strings.Join(fs.Args()[1:], " "), Agent: *agent, Gate: *gate}
	for _, a := range strings.Split(*after, ",") {
		if a = strings.TrimSpace(a); a != "" {
			sp.After = append(sp.After, a)
		}
	}
	var r proto.Run
	if err := call(c, proto.MethodRunAdd, proto.RunAddParams{Run: fs.Arg(0), Tasks: []proto.RunTaskSpec{sp}}, &r); err != nil {
		return err
	}
	t := r.Tasks[len(r.Tasks)-1]
	fmt.Printf("%s: %s", t.ID, t.Status)
	if t.Why != "" {
		fmt.Printf(" (%s)", t.Why)
	}
	fmt.Println()
	return nil
}

func runShowCmd(c *client.Client, args []string) error {
	fs := flag.NewFlagSet("run show", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print the run as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: conch run show [-json] RUN")
	}
	var r proto.Run
	if err := call(c, proto.MethodRunGet, proto.RunRef{Run: fs.Arg(0)}, &r); err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	}
	printRun(os.Stdout, r)
	return nil
}

// runNeeds says whether a coordinator waiting on r has something to do:
// a question to answer, the run over, or nothing left that can move by
// itself. A gate waiting for the person is none of those — the person
// approves it, and the wait goes on.
func runNeeds(r proto.Run) (string, bool) {
	for _, q := range r.Questions {
		if q.Open() {
			return fmt.Sprintf("question %s from %s: %s (conch run answer %s %s ANSWER)", q.ID, orDash(q.Task), q.Text, r.Name, q.ID), true
		}
	}
	if len(r.Tasks) == 0 {
		return "", false
	}
	if r.Over() {
		failed := 0
		for _, t := range r.Tasks {
			if t.Status == proto.RunFailed {
				failed++
			}
		}
		if failed > 0 {
			return fmt.Sprintf("over: %d of %d tasks failed", failed, len(r.Tasks)), true
		}
		return "over: every task is done", true
	}
	var stuck []string
	for _, t := range r.Tasks {
		switch {
		case t.Status == proto.RunBlocked && !t.GateWaits():
			stuck = append(stuck, t.ID+" ("+t.Why+")")
		case t.Status == proto.RunDone, t.Status == proto.RunFailed:
		case t.Status == proto.RunReady && r.Manual:
			stuck = append(stuck, t.ID+" (ready: conch run start "+r.Name+" "+t.ID+")")
		default:
			return "", false
		}
	}
	return "stuck: nothing left can go on by itself — " + strings.Join(stuck, ", "), true
}

// awaitRun waits until cond says the run is where it is wanted, the run is
// taken away, or the time runs out (0: no limit). It looks at every
// run.changed event and, as a server's clock moves questions past their
// deadline without one, every few seconds too.
func awaitRun(c *client.Client, ref string, timeout time.Duration, cond func(proto.Run) (string, bool)) (proto.Run, string, error) {
	var late <-chan time.Time
	if timeout > 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		late = t.C
	}
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		var r proto.Run
		if err := call(c, proto.MethodRunGet, proto.RunRef{Run: ref}, &r); err != nil {
			return r, "", err
		}
		if why, ok := cond(r); ok {
			return r, why, nil
		}
		ref = r.ID // a name could be taken by a new run meanwhile
	wait:
		for {
			select {
			case msg, ok := <-c.Events:
				if !ok {
					if err := c.Err(); err != nil {
						return r, "", err
					}
					return r, "", errors.New("the server closed the connection")
				}
				if msg.Event == proto.EventRunChanged || msg.Event == proto.EventRunRemoved {
					break wait
				}
			case <-tick.C:
				break wait
			case <-late:
				return r, "", waitTimeout{after: timeout}
			}
		}
	}
}

func runWaitCmd(c *client.Client, args []string) error {
	fs := flag.NewFlagSet("run wait", flag.ContinueOnError)
	timeout := fs.Duration("timeout", 0, "give up after this long (default: no limit)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: conch run wait [-timeout D] RUN")
	}
	r, why, err := awaitRun(c, fs.Arg(0), *timeout, runNeeds)
	if err != nil {
		return err
	}
	fmt.Println(why)
	printRun(os.Stdout, r)
	return nil
}

// workerFlags are -run and -task, which a worker leaves out: its pane
// says which task it is.
func workerFlags(name string) (*flag.FlagSet, *string, *string) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	run := fs.String("run", "", "the run (default: the one this pane works for)")
	task := fs.String("task", "", "the task (default: the one this pane works on)")
	return fs, run, task
}

func runReportCmd(c *client.Client, sub string, args []string) error {
	fs, run, task := workerFlags("run " + sub)
	if err := fs.Parse(args); err != nil {
		return err
	}
	result := strings.Join(fs.Args(), " ")
	if strings.TrimSpace(result) == "" {
		return fmt.Errorf("usage: conch run %s [-run R -task T] RESULT... — say in a line what you did, or why not", sub)
	}
	status := proto.RunDone
	if sub == "fail" {
		status = proto.RunFailed
	}
	var r proto.Run
	if err := call(c, proto.MethodRunReport, proto.RunReportParams{Run: *run, Task: *task, Status: status, Result: result}, &r); err != nil {
		return err
	}
	fmt.Printf("reported %s to run %s\n", status, r.Name)
	return nil
}

// optionList is a flag given once per option.
type optionList []string

func (o *optionList) String() string     { return strings.Join(*o, ",") }
func (o *optionList) Set(v string) error { *o = append(*o, v); return nil }

func runAskCmd(c *client.Client, args []string) error {
	fs, run, task := workerFlags("run ask")
	var opts optionList
	fs.Var(&opts, "option", "an answer to offer; give it once for each")
	def := fs.String("default", "", "the answer taken if nobody answers in time")
	timeout := fs.Duration("timeout", 0, "how long to wait for the answer (default: no limit)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	text := strings.Join(fs.Args(), " ")
	if strings.TrimSpace(text) == "" {
		return errors.New("usage: conch run ask [-run R -task T] [-option O]... [-default O] [-timeout D] QUESTION...")
	}
	if *timeout < 0 {
		return errors.New("-timeout cannot be negative")
	}
	secs := int((*timeout + time.Second - 1) / time.Second)
	q, err := askRun(c, proto.RunAskParams{Run: *run, Task: *task, Text: text, Options: opts, Default: *def, TimeoutSecs: secs})
	if err != nil {
		return err
	}
	if q.TimedOut {
		fmt.Fprintf(os.Stderr, "nobody answered in %s; going on with the default\n", *timeout)
	}
	fmt.Println(q.Answer)
	return nil
}

// askRun asks and waits for the answer: the coordinator's, or the default
// once the question times out. A question that times out with no default
// is an error, so a script stops rather than goes on with nothing.
func askRun(c *client.Client, p proto.RunAskParams) (proto.RunQuestion, error) {
	var res proto.RunAskResult
	if err := call(c, proto.MethodRunAsk, p, &res); err != nil {
		return proto.RunQuestion{}, err
	}
	var q proto.RunQuestion
	_, _, err := awaitRun(c, res.Run, 0, func(r proto.Run) (string, bool) {
		for _, x := range r.Questions {
			if x.ID == res.Question.ID {
				q = x
				return "", !x.Open()
			}
		}
		return "", false
	})
	if err != nil {
		return q, err
	}
	if q.TimedOut && q.Answer == "" {
		return q, fmt.Errorf("nobody answered %s in %ds, and it has no default", q.ID, p.TimeoutSecs)
	}
	return q, nil
}

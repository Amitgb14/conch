package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// The run tools: `conch run` for an agent that would rather call a tool.
// A coordinator makes a run, waits on it and answers its workers; a worker
// reports and asks. Same methods, same payloads, as the commands (run.go).

const runArg = "the run: its id (r1a2b3c4d) or its name"

func mcpRunTools() []mcpTool {
	taskItem := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":    strProp("its id within the run, for other tasks to wait on (default t1, t2…)"),
			"spec":  strProp("what its agent should do"),
			"after": listProp("the ids of the tasks it waits for; its branch starts from the first one's"),
			"agent": strProp(agentArg),
			"gate":  strProp("if set, a person is asked this and must approve before it starts"),
		},
		"required": []string{"spec"},
	}
	tasks := map[string]any{"type": "array", "items": taskItem, "description": "the tasks, each with what it waits for"}
	return []mcpTool{
		{
			Name:        "run_create",
			Title:       "Make a run of tasks",
			Description: "Split a job into tasks that depend on one another, kept by conch's server so neither you nor a restart loses it. Each task starts as soon as the tasks it waits for are done (unless manual), with an agent on a branch of its own made from the first one's. Its worker reports done or failed once; you wait with run_wait and answer its questions with run_answer. A task with a gate waits for the person.",
			Schema: schema([]string{"name"}, map[string]any{
				"name":   strProp("the run's name, unique among runs"),
				"cwd":    strProp("a folder of the project; yours when left out"),
				"manual": flagProp("start ready tasks only when run_start asks"),
				"tasks":  tasks,
			}),
			run: mcpRunCreate,
		},
		{
			Name:        "run_add",
			Title:       "Add tasks to a run",
			Description: "Add tasks to a run you coordinate; they may wait on tasks already in it.",
			Schema:      schema([]string{"run", "tasks"}, map[string]any{"run": strProp(runArg), "tasks": tasks}),
			run:         mcpRunAdd,
		},
		{
			Name:        "run_status",
			Title:       "Where a run stands",
			Description: "A run's tasks — pending, ready, running, done, failed or blocked, with each worker's pane, branch and result — and its questions. Without run, every run.",
			Schema:      schema(nil, map[string]any{"run": strProp(runArg)}),
			run:         mcpRunStatus,
		},
		{
			Name:        "run_start",
			Title:       "Start a run's ready tasks",
			Description: "Start a run's ready tasks now — every one, or the task named. For a manual run; others start their own.",
			Schema:      schema([]string{"run"}, map[string]any{"run": strProp(runArg), "task": strProp("only this task")}),
			run:         mcpRunStart,
		},
		{
			Name:        "run_wait",
			Title:       "Wait on a run",
			Description: "Block until a run needs you: a worker's question to answer, the run over, or nothing left that can go on by itself. A gate waiting for the person does not end it.",
			Schema: schema([]string{"run"}, map[string]any{
				"run":             strProp(runArg),
				"timeout_seconds": numProp("give up after this long (default 1800; 0 waits forever)"),
			}),
			run: mcpRunWait,
		},
		{
			Name:        "run_answer",
			Title:       "Answer a worker's question",
			Description: "Answer a question a worker of your run asked; with options, one of them.",
			Schema: schema([]string{"run", "question", "answer"}, map[string]any{
				"run": strProp(runArg), "question": strProp("its id (q1)"), "answer": strProp("the answer"),
			}),
			run: mcpRunAnswer,
		},
		{
			Name:        "run_report",
			Title:       "Report your task done or failed",
			Description: "For a run's worker: say once, at the end, that your task is done or failed, with a line of result. Leave run and task out: your pane says which task you are.",
			Schema: schema([]string{"status", "result"}, map[string]any{
				"status": map[string]any{"type": "string", "enum": []string{proto.RunDone, proto.RunFailed}, "description": "done or failed"},
				"result": strProp("what you did, or why you could not, in a line"),
				"run":    strProp(runArg + "; leave it out in a worker's pane"),
				"task":   strProp("the task; leave it out in a worker's pane"),
			}),
			run: mcpRunReport,
		},
		{
			Name:        "run_ask",
			Title:       "Ask your coordinator",
			Description: "For a run's worker: ask the agent coordinating the run a question and wait for its answer. With a timeout, the default is taken when nobody answers in time.",
			Schema: schema([]string{"question"}, map[string]any{
				"question":        strProp("the question"),
				"options":         listProp("the answers to choose from"),
				"default":         strProp("the answer taken if nobody answers in time"),
				"timeout_seconds": numProp("how long to wait for an answer (default: no limit)"),
				"run":             strProp(runArg + "; leave it out in a worker's pane"),
				"task":            strProp("the task; leave it out in a worker's pane"),
			}),
			run: mcpRunAsk,
		},
	}
}

func runText(r proto.Run) string {
	var b strings.Builder
	printRun(&b, r)
	return strings.TrimRight(b.String(), "\n")
}

func mcpRunCreate(c *client.Client, raw json.RawMessage) (any, error) {
	var args struct {
		Name   string              `json:"name"`
		Cwd    string              `json:"cwd"`
		Manual bool                `json:"manual"`
		Tasks  []proto.RunTaskSpec `json:"tasks"`
	}
	if err := mcpArgs(raw, &args); err != nil {
		return nil, err
	}
	if err := needRuns(c); err != nil {
		return nil, err
	}
	if strings.TrimSpace(args.Name) == "" {
		return nil, errors.New("give the run a name")
	}
	dir := args.Cwd
	if strings.TrimSpace(dir) == "" {
		dir, _ = os.Getwd()
	}
	var proj proto.ProjectInfo
	if err := callFor(c, proto.MethodProjectAdd, proto.ProjectAddParams{Path: dir}, &proj, harvestWait); err != nil {
		return nil, err
	}
	if !proj.Git {
		return nil, fmt.Errorf("%s is not a git repository: a run's tasks each work on a branch of their own", proj.Name)
	}
	var r proto.Run
	if err := call(c, proto.MethodRunCreate, proto.RunCreateParams{Name: args.Name, ProjectID: proj.ID, Manual: args.Manual, Tasks: args.Tasks}, &r); err != nil {
		return nil, err
	}
	return toolResult(runText(r)+"\nWait on it with run_wait.", r), nil
}

func mcpRunAdd(c *client.Client, raw json.RawMessage) (any, error) {
	var args proto.RunAddParams
	if err := mcpArgs(raw, &args); err != nil {
		return nil, err
	}
	if err := needRuns(c); err != nil {
		return nil, err
	}
	var r proto.Run
	if err := call(c, proto.MethodRunAdd, args, &r); err != nil {
		return nil, err
	}
	return toolResult(runText(r), r), nil
}

func mcpRunStatus(c *client.Client, raw json.RawMessage) (any, error) {
	var args proto.RunRef
	if err := mcpArgs(raw, &args); err != nil {
		return nil, err
	}
	if err := needRuns(c); err != nil {
		return nil, err
	}
	if strings.TrimSpace(args.Run) == "" {
		var b strings.Builder
		if err := runList(c, &b); err != nil {
			return nil, err
		}
		var list proto.RunList
		if err := call(c, proto.MethodRunList, nil, &list); err != nil {
			return nil, err
		}
		return toolResult(strings.TrimRight(b.String(), "\n"), list), nil
	}
	var r proto.Run
	if err := call(c, proto.MethodRunGet, args, &r); err != nil {
		return nil, err
	}
	return toolResult(runText(r), r), nil
}

func mcpRunStart(c *client.Client, raw json.RawMessage) (any, error) {
	var args proto.RunStartParams
	if err := mcpArgs(raw, &args); err != nil {
		return nil, err
	}
	if err := needRuns(c); err != nil {
		return nil, err
	}
	var r proto.Run
	if err := callFor(c, proto.MethodRunStart, args, &r, harvestWait); err != nil {
		return nil, err
	}
	return toolResult(runText(r), r), nil
}

func mcpRunWait(c *client.Client, raw json.RawMessage) (any, error) {
	var args struct {
		Run     string   `json:"run"`
		Timeout *float64 `json:"timeout_seconds"`
	}
	if err := mcpArgs(raw, &args); err != nil {
		return nil, err
	}
	if err := needRuns(c); err != nil {
		return nil, err
	}
	timeout, err := mcpWaitFor(args.Timeout)
	if err != nil {
		return nil, err
	}
	r, why, err := awaitRun(c, args.Run, timeout, runNeeds)
	if err != nil {
		return nil, err
	}
	return toolResult(why+"\n"+runText(r), r), nil
}

func mcpRunAnswer(c *client.Client, raw json.RawMessage) (any, error) {
	var args proto.RunAnswerParams
	if err := mcpArgs(raw, &args); err != nil {
		return nil, err
	}
	if err := needRuns(c); err != nil {
		return nil, err
	}
	var r proto.Run
	if err := call(c, proto.MethodRunAnswer, args, &r); err != nil {
		return nil, err
	}
	return toolResult(fmt.Sprintf("answered %s", args.Question), r), nil
}

func mcpRunReport(c *client.Client, raw json.RawMessage) (any, error) {
	var args proto.RunReportParams
	if err := mcpArgs(raw, &args); err != nil {
		return nil, err
	}
	if err := needRuns(c); err != nil {
		return nil, err
	}
	if strings.TrimSpace(args.Result) == "" {
		return nil, errors.New("say in result what you did, or why you could not")
	}
	var r proto.Run
	if err := call(c, proto.MethodRunReport, args, &r); err != nil {
		return nil, err
	}
	return toolResult(fmt.Sprintf("reported %s to run %s", args.Status, r.Name), r), nil
}

func mcpRunAsk(c *client.Client, raw json.RawMessage) (any, error) {
	var args struct {
		Question string   `json:"question"`
		Options  []string `json:"options"`
		Default  string   `json:"default"`
		Timeout  *float64 `json:"timeout_seconds"`
		Run      string   `json:"run"`
		Task     string   `json:"task"`
	}
	if err := mcpArgs(raw, &args); err != nil {
		return nil, err
	}
	if err := needRuns(c); err != nil {
		return nil, err
	}
	secs := 0
	if args.Timeout != nil {
		if *args.Timeout < 0 {
			return nil, fmt.Errorf("timeout_seconds cannot be negative (%v)", *args.Timeout)
		}
		secs = int(*args.Timeout + 0.999)
	}
	q, err := askRun(c, proto.RunAskParams{Run: args.Run, Task: args.Task, Text: args.Question, Options: args.Options, Default: args.Default, TimeoutSecs: secs})
	if err != nil {
		return nil, err
	}
	text := "the answer: " + q.Answer
	if q.TimedOut {
		text = "nobody answered in time; the default: " + q.Answer
	}
	return toolResult(text, q), nil
}

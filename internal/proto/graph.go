package proto

import "time"

// A task graph for agents driving agents (run.v1): a run is a named job
// split into tasks, each with what it depends on, and the server keeps
// which are done, which failed and where a person has to decide — so a
// coordinator agent that is restarted finds the job where it left it,
// rather than in a context it no longer has.

// CapRuns is the run.* methods, their run.changed event, and `conch run`.
const CapRuns = "run.v1"

const (
	MethodRunCreate  = "run.create"
	MethodRunAdd     = "run.add"
	MethodRunList    = "run.list"
	MethodRunGet     = "run.get"
	MethodRunStart   = "run.start"
	MethodRunReport  = "run.report"
	MethodRunAsk     = "run.ask"
	MethodRunAnswer  = "run.answer"
	MethodRunApprove = "run.approve"
	MethodRunRemove  = "run.remove"

	// EventRunChanged carries a Run each time anything in it changes.
	EventRunChanged = "run.changed"
	// EventRunRemoved carries a RunRef for a run taken away.
	EventRunRemoved = "run.removed"
)

// The states a task is in.
const (
	// RunPending: what it depends on has not finished yet.
	RunPending = "pending"
	// RunReady: free to start, and not started yet.
	RunReady = "ready"
	// RunRunning: an agent was started on it and has not reported.
	RunRunning = "running"
	// RunDone and RunFailed are what the worker reported, once.
	RunDone   = "done"
	RunFailed = "failed"
	// RunBlocked: it cannot go on by itself — a person's approval is
	// waited for (Gate), or something it depends on failed. Why says
	// which.
	RunBlocked = "blocked"
)

// Run is one job.
type Run struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ProjectID string `json:"project_id"`
	// Coordinator is the agent that made the run — its pane here, or its
	// id on another machine — and the only agent that may change it.
	// Empty for a run the person made.
	Coordinator string    `json:"coordinator,omitempty"`
	Created     time.Time `json:"created"`
	// Manual runs start a ready task only when run.start asks; otherwise
	// the server starts each as soon as it is ready.
	Manual    bool          `json:"manual,omitempty"`
	Tasks     []RunTask     `json:"tasks"`
	Questions []RunQuestion `json:"questions,omitempty"`
}

// Over says every task has finished, one way or the other.
func (r Run) Over() bool {
	for _, t := range r.Tasks {
		if t.Status != RunDone && t.Status != RunFailed {
			return false
		}
	}
	return true
}

// Task is the run's task with that id, if there is one.
func (r *Run) Task(id string) *RunTask {
	for i := range r.Tasks {
		if r.Tasks[i].ID == id {
			return &r.Tasks[i]
		}
	}
	return nil
}

// RunTaskSpec is a task as it is asked for.
type RunTaskSpec struct {
	// ID names it within the run (api, tests); t1, t2… when left out.
	ID string `json:"id,omitempty"`
	// Spec is what the agent is asked to do.
	Spec string `json:"spec"`
	// After is the ids of the tasks it waits for. Its branch starts from
	// the first one's.
	After []string `json:"after,omitempty"`
	Agent string   `json:"agent,omitempty"`
	// Gate, when set, is what a person is asked before it starts: it
	// waits for their approval once what it depends on is done.
	Gate string `json:"gate,omitempty"`
}

// RunTask is a task and where it is.
type RunTask struct {
	RunTaskSpec
	Status string `json:"status"`
	// Why says what a pending, blocked or failed task is waiting on or
	// what went wrong, in words.
	Why string `json:"why,omitempty"`
	// Approved is set once a person approved its gate.
	Approved bool   `json:"approved,omitempty"`
	Pane     string `json:"pane,omitempty"`
	Branch   string `json:"branch,omitempty"`
	// Result is the worker's short report.
	Result  string    `json:"result,omitempty"`
	Started time.Time `json:"started,omitzero"`
	Ended   time.Time `json:"ended,omitzero"`
}

// RunQuestion is a worker asking its coordinator something.
type RunQuestion struct {
	ID      string   `json:"id"`
	Task    string   `json:"task,omitempty"`
	Text    string   `json:"text"`
	Options []string `json:"options,omitempty"`
	// Default is the answer taken when nobody answers by Deadline.
	Default  string    `json:"default,omitempty"`
	Asked    time.Time `json:"asked"`
	Deadline time.Time `json:"deadline,omitzero"`
	Answer   string    `json:"answer,omitempty"`
	Answered bool      `json:"answered,omitempty"`
	// TimedOut says nobody answered in time; Answer is then the Default.
	TimedOut bool `json:"timed_out,omitempty"`
}

// Open says it waits for an answer still.
func (q RunQuestion) Open() bool { return !q.Answered && !q.TimedOut }

// RunCreateParams makes a run.
type RunCreateParams struct {
	Name      string        `json:"name"`
	ProjectID string        `json:"project_id"`
	Manual    bool          `json:"manual,omitempty"`
	Tasks     []RunTaskSpec `json:"tasks,omitempty"`
}

// RunAddParams adds tasks to a run.
type RunAddParams struct {
	Run   string        `json:"run"`
	Tasks []RunTaskSpec `json:"tasks"`
}

// RunRef names a run, by its id or its name.
type RunRef struct {
	Run string `json:"run"`
}

// RunList is every run.
type RunList struct {
	Runs []Run `json:"runs"`
}

// RunStartParams starts a run's ready tasks now, or the one named.
type RunStartParams struct {
	Run  string `json:"run"`
	Task string `json:"task,omitempty"`
}

// RunReportParams is a worker saying it is done or failed. Run and Task
// may be left out by a worker: its pane says which task it is.
type RunReportParams struct {
	Run    string `json:"run,omitempty"`
	Task   string `json:"task,omitempty"`
	Status string `json:"status"` // RunDone or RunFailed
	Result string `json:"result,omitempty"`
}

// RunAskParams is a worker asking its coordinator. Run and Task as for a
// report.
type RunAskParams struct {
	Run         string   `json:"run,omitempty"`
	Task        string   `json:"task,omitempty"`
	Text        string   `json:"text"`
	Options     []string `json:"options,omitempty"`
	Default     string   `json:"default,omitempty"`
	TimeoutSecs int      `json:"timeout_secs,omitempty"`
}

// RunAskResult names the question, to wait for its answer by.
type RunAskResult struct {
	Run      string      `json:"run"`
	Question RunQuestion `json:"question"`
}

// RunAnswerParams answers a question.
type RunAnswerParams struct {
	Run      string `json:"run"`
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

// RunApproveParams is a person approving a task's gate, or refusing it.
type RunApproveParams struct {
	Run  string `json:"run"`
	Task string `json:"task"`
	Deny bool   `json:"deny,omitempty"`
}

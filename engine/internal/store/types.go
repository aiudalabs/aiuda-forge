package store

// Status is a task/run lifecycle state. The kernel state machine is defined
// entirely by these states and the legalTransitions table below — there is no
// per-flow special casing.
type Status string

const (
	StatusQueued    Status = "QUEUED"
	StatusRunning   Status = "RUNNING"
	StatusAwaiting  Status = "AWAITING" // human_gate parked, waiting for approval
	StatusDone      Status = "DONE"
	StatusFailed    Status = "FAILED"
	StatusCancelled Status = "CANCELLED"
)

// legalTransitions is the ONLY source of truth for what state changes are
// allowed. Any transition not present here is rejected by transitionTx.
var legalTransitions = map[Status]map[Status]bool{
	StatusQueued:    {StatusRunning: true, StatusCancelled: true},
	StatusRunning:   {StatusDone: true, StatusFailed: true, StatusQueued: true, StatusCancelled: true, StatusAwaiting: true},
	StatusAwaiting:  {StatusDone: true, StatusFailed: true, StatusCancelled: true}, // approve / reject / cancel
	StatusFailed:    {StatusQueued: true},                                          // retry
	StatusDone:      {},                                                            // terminal
	StatusCancelled: {},                                                            // terminal
}

func transitionAllowed(from, to Status) bool {
	outs, ok := legalTransitions[from]
	if !ok {
		return false
	}
	return outs[to]
}

// IsTerminal reports whether a status is an end state.
func IsTerminal(s Status) bool {
	return s == StatusDone || s == StatusFailed || s == StatusCancelled
}

// Run is one execution of a workflow.
type Run struct {
	ID         string `json:"id"`
	WorkflowID string `json:"workflow_id"`
	Status     Status `json:"status"`
	Payload    string `json:"payload"`    // trigger payload, JSON
	ProjectID  string `json:"project_id"`           // the project this run belongs to (audit A1)
	DeletedAt  int64  `json:"deleted_at,omitempty"` // 0 = live; >0 = soft-deleted (D4)
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
}

// Task is one step-instance: the unit a worker claims and executes. The queue
// is over tasks; a run owns many tasks (one per executed step).
type Task struct {
	ID          string   `json:"id"`
	RunID       string   `json:"run_id"`
	WorkflowID  string   `json:"workflow_id"`
	StepID      string   `json:"step_id"`
	Type        string   `json:"type"` // step type: echo|agent|gate|pr|agentic_verify|human_gate
	Status      Status   `json:"status"`
	Payload     string   `json:"payload"` // resolved step inputs, JSON
	Result      string   `json:"result"`  // step output, JSON
	Error       string   `json:"error"`
	Attempts    int      `json:"attempts"`
	Fence       int64    `json:"fence"` // rotated on every claim; stale fences are rejected
	DependsOn   []string `json:"depends_on"`
	Wave        int      `json:"wave"`
	ClaimedBy   string   `json:"claimed_by"`
	HeartbeatAt int64    `json:"heartbeat_at"`
	ProjectID   string   `json:"project_id"` // the project this task belongs to (audit A1)
	CreatedAt   int64    `json:"created_at"`
	UpdatedAt   int64    `json:"updated_at"`
}

// Event is an append-only record on the bus. seq is a monotonic cursor used for
// replay (GET /runs/{id}/events?after=).
type Event struct {
	Seq       int64  `json:"seq"`
	RunID     string `json:"run_id"`
	TaskID    string `json:"task_id"`
	Type      string `json:"type"`
	Data      string `json:"data"`       // JSON
	ProjectID string `json:"project_id"` // the project this event belongs to (audit A1)
	CreatedAt int64  `json:"created_at"`
}

// Event type constants (the §B contract from docs/14_API_CONTRACT_v2.md).
const (
	EventRunCreated       = "run.created"
	EventRunStatusChanged = "run.status_changed"
	EventStepStatusChange = "step.status_changed"
	EventStepEvent        = "step.event"
	EventStepGate         = "step.gate"
	EventStepVerify       = "step.verify"
	EventRunAwaitingApprv = "run.awaiting_approval"
	EventRunDone          = "run.done"
	EventRunFailed        = "run.failed"
	EventRunCancelled     = "run.cancelled"
)

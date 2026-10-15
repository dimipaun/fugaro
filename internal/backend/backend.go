// Package backend is the CLI's only seam to the compute platform (design
// §3.4). internal/backend/gcp implements it on Cloud Run; a later Batch
// backend is a sibling package. Storage is not part of the seam: every
// backend uses gocloud.dev/blob.
package backend

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"
)

// CloudRun is the Cloud Run backend's name: launch.json's backend field
// and the job's FUGARO_BACKEND.
const CloudRun = "cloud-run"

// TaskTimeoutSlack is what the job's task timeout adds to the workflow's
// timeouts.total (design §4.5): internal/infra hands it to Terraform, and
// the runner's lock expiry and writeback window rely on it. It is the single source for both.
const TaskTimeoutSlack = 2 * time.Minute

// State is an execution's lifecycle state as the platform reports it.
type State string

const (
	StatePending   State = "pending"
	StateRunning   State = "running"
	StateSucceeded State = "succeeded" // the container exited 0; says nothing about the PR
	StateFailed    State = "failed"
	StateCancelled State = "cancelled"
)

// Terminal reports whether the execution has finished.
func (s State) Terminal() bool {
	return s == StateSucceeded || s == StateFailed || s == StateCancelled
}

// RepoRef names a repository: "owner/name" and its storage slug.
type RepoRef struct{ Repo, Slug string }

// LaunchSpec is one execution to start.
type LaunchSpec struct {
	Repo     RepoRef
	Workflow string
	RunID    string
	// Timeout is the workflow's total time for this run; zero means the job's own.
	// The backend adds TaskTimeoutSlack to it.
	Timeout time.Duration
}

// ExecID is a run's identity, parsed from its resource name by the
// backend's codec (codec.go): Cloud Run's fields today, and the same four
// slots for a backend whose name has fewer.
type ExecID struct{ GCPProject, Region, Job, Name string }

// Key identifies the execution regardless of how the project is written.
// Compare executions by Key, never by raw name.
func (id ExecID) Key() string { return id.Region + "/" + id.Job + "/" + id.Name }

// String is the canonical full resource name.
func (id ExecID) String() string {
	return "projects/" + id.GCPProject + "/locations/" + id.Region + "/jobs/" + id.Job + "/executions/" + id.Name
}

// ExecutionRef identifies a started execution.
type ExecutionRef struct {
	Name   string // the platform's full resource name
	Job    string
	LogURL string
}

// Execution is one execution as the platform reports it.
type Execution struct {
	Name, Job                   string
	State                       State
	Created, Started, Completed time.Time
	CPU, MemoryGiB              float64 // the task's limits, for compute cost
	LogURL                      string
	// Run is "<slug>/<run id>", the FUGARO_RUN the execution was launched
	// with, when the platform reports the execution's environment; empty
	// when it does not (the sweeper then treats the execution as possibly
	// anyone's of its job). Anyone who can start the job can set it, so
	// it is a hint to keep a registry entry, never evidence to act on.
	Run string
}

// Billed is how long the execution has run: start to completion, or to now
// while it runs; zero before it starts.
func (e Execution) Billed(now time.Time) time.Duration {
	if e.Started.IsZero() {
		return 0
	}
	end := e.Completed
	if end.IsZero() {
		end = now
	}
	return max(0, end.Sub(e.Started))
}

// ListFilter narrows List.
type ListFilter struct {
	Jobs       []string  // job names; empty means every Fugaro job
	Since      time.Time // executions created at or after this
	ActiveOnly bool      // only pending and running executions
}

// LogQuery selects one execution's log entries.
type LogQuery struct {
	Execution string
	Since     time.Time
	Follow    bool          // keep polling until the execution ends and its logs settle
	Poll      time.Duration // follow's poll interval; zero means the backend's default
}

// LogEntry is one structured log line.
type LogEntry struct {
	Time     time.Time
	Severity string
	InsertID string
	Message  string
	Fields   map[string]any // the JSON payload, including run_id, stage and stream
}

// Backend launches and inspects executions.
type Backend interface {
	// Codec is this backend's name codec: how it spells a run's identity,
	// and the platform signal that says a process is one of its runs
	// (design compute-backend.md §2.5).
	Codec() Codec
	Launch(ctx context.Context, spec LaunchSpec) (ExecutionRef, error)
	Execution(ctx context.Context, name string) (Execution, error)
	List(ctx context.Context, f ListFilter) ([]Execution, error)
	Logs(ctx context.Context, q LogQuery, fn func(LogEntry) error) error
	Cancel(ctx context.Context, name string) error
	// LongestTaskTimeout is the longest task timeout among the Fugaro
	// workflow jobs, zero when there are none. No run of them can be active
	// for longer, so it bounds how far back an active-only List must look.
	LongestTaskTimeout(ctx context.Context) (time.Duration, error)
	// TaskTimeout is the task timeout of the repository's workflow job
	// (DefaultTaskTimeout when the job sets none). ErrNotFound when the job
	// does not exist.
	TaskTimeout(ctx context.Context, slug, workflow string) (time.Duration, error)
}

// DefaultTaskTimeout is Cloud Run's task timeout for a job that sets none.
const DefaultTaskTimeout = 10 * time.Minute

// ErrNotFound means the execution (or job) does not exist.
var ErrNotFound = errors.New("not found")

// ErrRejected means the platform definitively refused a request (a 4xx
// other than 408, 429 and 499): nothing was created. Callers may undo their own
// bookkeeping only for this error; any other error is ambiguous.
var ErrRejected = errors.New("rejected")

// Prices are compute list prices per second (design §10.1).
type Prices struct {
	VCPUSecondUSD float64
	GiBSecondUSD  float64
	Source        string // where the figures come from, for reports
}

// ComputeUSD is the cost of cpu vCPUs and memGiB GiB for d.
func (p Prices) ComputeUSD(cpu, memGiB float64, d time.Duration) float64 {
	if d <= 0 {
		return 0
	}
	return d.Seconds() * (cpu*p.VCPUSecondUSD + memGiB*p.GiBSecondUSD)
}

var memoryRE = regexp.MustCompile(`^([1-9][0-9]*)(Mi|Gi)$`)

// MemoryGiB parses a Kubernetes-style quantity ("512Mi", "8Gi") into GiB.
func MemoryGiB(s string) (float64, error) {
	m := memoryRE.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("memory %q must look like 512Mi or 8Gi", s)
	}
	n, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, fmt.Errorf("memory %q: %w", s, err)
	}
	if m[2] == "Mi" {
		n /= 1024
	}
	return n, nil
}

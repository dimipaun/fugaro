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

// CloudBatch is Cloud Batch's backend name. internal/backend/batch speaks
// it, but nothing selects it yet: config validation refuses it everywhere
// until the owner's Check 57a measurement says the platform behaves as
// docs/design/compute-backend.md describes (design §9).
const CloudBatch = "cloud-batch"

// TaskTimeoutSlack is what the job's task timeout adds to the workflow's
// timeouts.total (design §4.5): internal/infra hands it to Terraform, and
// the runner's lock expiry and writeback window rely on it. It is the single source for both.
const TaskTimeoutSlack = 2 * time.Minute

// MaxRunTime is the longest total time a run may ask for with a per-run
// override (--total-timeout), and therefore the longest any run can
// legitimately be active whatever the workflows are configured with: the CLI
// refuses an override above it (OverrideCap, internal/cli/run.go) and the
// platform caps what a task may run for besides. A runaway value is more
// likely a typo than a plan, so it is a ceiling rather than a plan value.
//
// It lives here rather than in internal/cli because it is not a CLI policy
// but a fact about LaunchSpec.Timeout: anything that measures how long a run
// may legitimately be active has to account for it -- the CLI's max_parallel
// horizon (max(longest, OverrideCap), internal/cli/run.go) and the reaper's
// deadlines (internal/backend/batch/reap.go) both do -- and internal/backend
// cannot import internal/cli.
const MaxRunTime = 24 * time.Hour

// cloudRunMintSkew is how long before a run starts the run's ID may have been
// minted on Cloud Run: the platform starts the container as soon as the
// execution is created, so the two stamps are apart by that launch plus the
// skew between the launcher's clock and the container's. Half an hour is that
// with room to spare, and every minute of it a reader lists and then drops is a
// round trip paid for nothing.
const cloudRunMintSkew = 30 * time.Minute

// batchMintSkew is the same bound on Cloud Batch, and there it is a queue wait
// rather than a clock skew: Launch returns as soon as Batch accepts the job and
// the job sits QUEUED until the region has capacity (internal/backend/batch),
// which is what the reaper's queue deadline bounds -- max(the longest workflow
// timeout, MaxRunTime) plus TaskTimeoutSlack (internal/backend/batch/reap.go).
// The name of the backend is all a caller here has, and the installation's own
// longest workflow timeout is a platform read, so this is that deadline's floor:
// the shortest a Batch installation's queue deadline can be, and the look-back
// the pass had before it followed the backend at all.
const batchMintSkew = MaxRunTime + TaskTimeoutSlack

// MintSkewMargin is how far before the first day of a facts window a pass has
// to list runs to see every run that started inside that window. Runs are
// listed by the mint time their IDs carry and counted on the day the
// started_at of their record names, and the two stamps are apart by as much as
// the backend takes to start the run -- the launch, or the queue behind it -- so
// the margin is the backend's own bound and not the longest of them all: a run
// listed and then dropped by the day filter is a round trip that bought
// nothing, which is what a report is made of.
//
// A name that is not Cloud Run takes the Batch bound, and so does a caller that
// cannot name the backend: the history job runs in Terraform's environment,
// with no config file to read the name from, and it is the pass that WRITES the
// budget. There a run that is not listed is a day whose compute and hours are
// missing, so reading longer than it needs to is the only direction that is
// safe.
func MintSkewMargin(name string) time.Duration {
	if name == CloudRun {
		return cloudRunMintSkew
	}
	return batchMintSkew
}

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
	// Job is the job to run, by its own name (not a path): the shared project
	// job of the profile the workflow selects, under the project-centric model
	// (docs/design/project-centric.md §4.1, plan task 3.4 step 1). Empty means
	// the per-repository job of (Repo.Slug, Workflow), which is what every
	// project that has published no profiles gets, byte for byte, and what
	// every workflow that is not a `checkout: clone` one keeps until phase 4
	// ends (§4.4). The launcher computes it (gcp.ProjectJobName, from the
	// published policy); the backend only runs what it is given, because the
	// name is a fact about the project's profiles and not about the backend.
	Job string
	// Timeout is the workflow's total time for this run; zero means the job's own.
	// The backend adds TaskTimeoutSlack to it.
	Timeout time.Duration
	// Env are extra "NAME=value" variables for this execution only, merged
	// into the job's own environment (the Cloud Run v2 API merges override env
	// into the job's; docs/design/project-centric.md §2.2). They are plain: a
	// value here is readable in the job's definition and in every listing of it,
	// so a value that is a secret goes in SecretEnv instead — the budget marker
	// of a launch that minted is one (a fact about this launch, not about the
	// job's deploy-time environment, so it cannot come from fugaro update either
	// way; internal/runner's BudgetTokenEnv, plan R2-8(a)). No launcher sets an
	// entry here today.
	Env []string
	// SecretEnv are "NAME=value" variables like Env, for this execution only,
	// whose value is a secret: today the one entry is the budget marker of a
	// launch that sealed its token, whose value is the launch's seal key
	// (docs/design/budget-token-channel.md §7.1, D15). A backend carries a
	// secret entry only where the design accepts its audience — Cloud Run's
	// execution override, priced in §7.1 — and refuses the launch where it
	// cannot (Batch, whose only carrier is the job definition's plain
	// variables, readable through the Batch API by every launcher (roles/batch.jobsEditor, conditioned on the Fugaro job prefix) and by the reaper (batch.jobs.get), and possibly in the CreateJob audit record — not by a peer run, whose account holds only agentReporter and logWriter, and kept for the job's whole retention). No backend
	// quotes a secret entry's value in an error. A name may not be in both
	// Env and SecretEnv.
	SecretEnv []string
	// Docker is what the workflow asked for: `docker: true` (design
	// compute-backend.md §4.2, C16), a real Docker daemon for the run's own
	// containers. It is the workflow's ask and not a capability, so a backend
	// that cannot give a run a daemon must refuse the launch rather than run
	// the job without one: the agent's `docker` commands would fail one by
	// one, in the middle of a stage, with no daemon to talk to. On Batch it
	// mounts the VM's socket and puts DOCKER_HOST in the job env (§2.2,
	// §4.3); Cloud Run has no daemon at all and refuses it.
	Docker bool
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

// JobOf is the name of the job an execution was launched from: the field the
// platform's own report of an execution carries (Cloud Run's CLOUD_RUN_JOB,
// codec.go), read out of the execution name a run already has
// (Platform.ExecutionFromEnv, wired at internal/cli/exec.go).
//
// It is how a run learns the job it is in without reading its environment
// again. The job's name is what a project job's identity rests on (plan task
// 3.3 step 1, design §4.1): the provider is part of it, Cloud Run sets it, and
// a launcher cannot write it — unlike $FUGARO_GIT_PROVIDER, which any launcher
// can override (design §12a item 2, finding 4). The name is therefore read
// from the platform's own answer and never from a variable, and the runner
// computes the names a policy defines and matches this one against them
// (R2-5: the name is written, never parsed).
//
// An error means the name is not one any backend of this build parses, so the
// job cannot be named from it: the run halts rather than guessing an identity
// (fail closed). A local invocation has no execution name at all and never
// reaches this (its ExecutionFromEnv is "").
func JobOf(execution string) (string, error) {
	id, ok := ParseExecution(execution)
	if !ok {
		return "", fmt.Errorf("%s is not an execution name of any backend this build knows, so the job this run is in cannot be named", execution)
	}
	return id.Job, nil
}

// Execution is one execution as the platform reports it.
type Execution struct {
	Name, Job                   string
	State                       State
	Created, Started, Completed time.Time
	CPU, MemoryGiB              float64 // the task's limits, for compute cost
	// Preempted reports that the platform reclaimed this execution's machine
	// before the run could finish: the workload was killed mid-flight by
	// something outside it, not by a fault of its own. It is a fact the
	// platform's own report of the execution carries, read the same way State
	// is and from the same read -- never a second call, and never inferred
	// from a state alone: a terminal failure that is NOT a preemption (a
	// runnable that exited non-zero, an image that would not start) leaves it
	// false, or every dead run of a spot-using installation would read as
	// reclaimed.
	//
	// Only the Batch backend sets it, from Cloud Batch's own report of a job
	// whose VM was reclaimed (internal/backend/batch; design compute-backend.md
	// §1.3). Cloud Run has no such report and leaves it false everywhere, so
	// the field's presence is not what says which backend an execution came
	// from -- it is what says the platform named a preemption, and a caller
	// that derives anything from it must have a terminal failure to derive
	// from as well.
	//
	// It is evidence about a *dead* execution and nothing else: nothing
	// retries, finalizes or charges differently because of it. The read side
	// uses it to say what happened to a run whose own record never did
	// (runview.Row.Derived), and the reaper decides on state and never on this
	// field.
	Preempted bool
	// Region is the region the backend resolved this execution to: the
	// backend's own answer, from its configuration or from its codec's
	// parse of the name it returned. Callers that price an execution use
	// it instead of parsing the name themselves, because a name's shape is
	// the backend's business and a second platform's name need not have
	// Cloud Run's fields (design compute-backend.md §5.2). "" when the
	// backend does not report one; a caller that needs a region anyway
	// falls back to its own default, which is a fact about the caller and
	// not a second reading of the name.
	Region string
	// MachineType is the Compute Engine machine type backing this
	// execution, "" when the backend does not run on VMs (Cloud Run's
	// CPU/MemoryGiB are then the job's own limits, not a machine type's).
	// Its presence is what makes a run a VM-backed one: a VM is billed for
	// its machine type from its boot window, with a one-minute minimum
	// (BilledWindow, design compute-backend.md §5.2).
	MachineType string
	// Scheduled is when the platform scheduled this execution for: the point
	// at which Compute Engine starts provisioning the VM. On Batch that is
	// the job's SCHEDULED transition, and it is what a VM is billed from --
	// earlier than Started, because the VM boots, Batch sets the task up and
	// the image may be streamed or pulled all before the runner's first line
	// (design compute-backend.md §5.2). Zero when the backend does not report
	// one; BilledWindow then falls back to Created and says so, because that
	// window includes Batch's queue time -- the stretch before any VM exists
	// -- and so errs high.
	//
	// The Batch backend leaves it zero today: the job's status events are
	// where it would come from, and their shape is UNVERIFIED (Check 57a
	// steps 7 and 8, design §9). Reading a guess into it would be a wrong
	// cost, so nothing is inferred.
	Scheduled time.Time
	LogURL    string
	// Run is "<slug>/<run id>", the FUGARO_RUN the execution was launched
	// with, as the platform reports the execution's own environment (Cloud
	// Run's execution template, internal/backend/gcp); empty when it reports
	// no such variable (the sweeper then treats the execution as possibly
	// anyone's of its job, internal/budget).
	//
	// It is the binding the CLI's checkExecution applies, and the only one:
	// gcp.CheckRunExecution refuses an execution whose FUGARO_RUN is not the
	// run's own <slug>/<run id> before `ls`, `logs`, `diagnose`, `cancel` and
	// the retry paths act on a name the run's bucket objects — which the run's
	// own service account can write — named. The job used to be that binding
	// (JobName(slug, variant)); one project job serves every repository
	// admitted to a profile, so an execution's job no longer says which run it
	// is (design §4.1, plan task 3.4 step 2).
	//
	// Its limit is stated with it: whoever may start an execution of the job
	// may set the variable, so it says which launch an execution is, never who
	// asked for it — while the run's own service account, the writer of the
	// objects this binds, cannot set another execution's environment. That is
	// the whole of what it is evidence of, and why it is enough here.
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

// VMMinimumBilled is the shortest window Compute Engine bills: "All vCPUs,
// GPUs, and GB of memory are charged a minimum of 1 minute", then per
// second (cloud.google.com/compute/vm-instance-pricing, checked 2026-10-15,
// quoted in docs/design/compute-backend.md §5.2). Cloud Run jobs have no
// such floor, which is why the floor is applied here rather than in
// Prices.ComputeUSD: it is a property of the platform, not of the prices.
const VMMinimumBilled = time.Minute

// BilledWindow is how long this execution has been billed, and whether that
// is known at all (design compute-backend.md §5.2).
//
// For a VM-backed execution -- one whose MachineType is set -- it is the
// boot window: from Scheduled, the time the platform began provisioning the
// VM (design §5.2), to its completion or to now while it runs, floored at
// VMMinimumBilled. The VM boots, Batch sets the task up and the image may be
// streamed or pulled all before the runner's first line, and Compute Engine
// bills every second of it, so a window that started at Started would
// under-report exactly the runs whose cold start is most of their cost.
//
// When the backend reports no Scheduled time, the window starts at Created
// and WindowNote says so: that window includes Batch's queue time, the
// stretch before any VM exists, so it errs high -- which is the direction an
// estimate should err, but a reader has to be able to tell the two apart.
// The note travels to the estimate's source line with the figure.
//
// For every other execution it is Billed, unchanged: Cloud Run's own
// billing has no floor and starts when the container does, and a caller
// that priced it differently would change every Cloud Run estimate already
// recorded.
//
// ok is false for a VM-backed execution whose window cannot be bounded at
// all: neither Scheduled nor Created is set (nothing can be priced), or the
// execution has ended and no completion time was reported, so the window has
// no end either. The estimate must then say "unknown", never "free" -- and
// never a figure that grows: a caller re-reading a finished run must not
// re-price it higher every time it looks.
func (e Execution) BilledWindow(now time.Time) (time.Duration, bool) {
	if e.MachineType == "" {
		return e.Billed(now), true
	}
	start := e.Scheduled
	if start.IsZero() {
		start = e.Created
	}
	if start.IsZero() {
		return 0, false
	}
	end := e.Completed
	if end.IsZero() {
		if e.State.Terminal() {
			// It has ended and the backend reported no completion time
			// (the Batch backend leaves Completed zero: the job's status
			// events are where it comes from and their shape is UNVERIFIED,
			// design §2.3, §9). now is not the end of a window that is
			// already over, and a job's record outlives it by 60 days
			// (§2.4), so treating now as the end would read the same
			// finished run as costing more and more the longer it is
			// looked at.
			return 0, false
		}
		end = now
	}
	return max(VMMinimumBilled, end.Sub(start)), true
}

// WindowNote names the time a VM-backed execution's billed window started
// from, for the estimate's source line (design §5.2): the scheduled time, or
// the job's creation when the backend reported none, which includes Batch's
// queue time and errs high. "" for an execution that is not VM-backed, whose
// window is Cloud Run's own and whose rows carry no such line.
func (e Execution) WindowNote() string {
	if e.MachineType == "" {
		return ""
	}
	if e.Scheduled.IsZero() {
		return "window starts at job creation: includes queue time, errs high"
	}
	return "window starts at the job's scheduled time"
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
	// workflow jobs, zero when there are none. A run of one of them may
	// override its own total time past it, though (LaunchSpec.Timeout, up to
	// MaxRunTime), so a caller bounding how long a run may legitimately be
	// active must use max(LongestTaskTimeout, MaxRunTime).
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
	// VCPU and MemoryGiB are the quantities these prices bill, for a
	// platform whose unit is a machine type rather than the job's own
	// limits: Compute Engine bills an e2-standard-4 as 4 vCPU / 16 GiB
	// whatever the workflow asked for, so the machine type decides the
	// figure and not the workflow (design compute-backend.md §5.2). Both
	// zero means the platform bills whatever the execution's own
	// CPU/MemoryGiB are -- Cloud Run's jobs, and every price table that is
	// not a machine type's -- which is what Quantities then answers.
	VCPU      float64
	MemoryGiB float64
	Source    string // where the figures come from, for reports
}

// Quantities are the vCPU and GiB these prices bill: the machine type's when
// the table carries them (a VM backend), else the caller's own cpu and
// memGiB. It is what keeps "the quantities are the machine type's, not the
// workflow's" true by construction rather than by a backend remembering to
// copy them onto the execution.
func (p Prices) Quantities(cpu, memGiB float64) (float64, float64) {
	if p.VCPU > 0 && p.MemoryGiB > 0 {
		return p.VCPU, p.MemoryGiB
	}
	return cpu, memGiB
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

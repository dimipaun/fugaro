// Package runview joins what the runs bucket and the backend know about a
// run into one row (design §4.7, §9.1 ls): status, stage, cost and links.
// It never reports a run as more finished or more successful than
// result.json says: an execution that ended without a final record is an
// infra_error, never a success.
package runview

import (
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// Statuses beyond runstore's.
const (
	StatusUnlaunched = "unlaunched"
	StatusLaunching  = "launching" // a fresh launch claim, no launch.json yet
	StatusPending    = "pending"
	// StatusError is a run whose objects can't be trusted or read (a
	// corrupt launch.json or result.json, an execution that isn't the
	// run's): Reason says what; nothing about its progress is guessed.
	StatusError = "error"
)

// ReasonLost explains a launched run that the backend doesn't know and that
// never wrote a record, past runstore.ClaimTTL after its launch: it failed
// before the runner started, and its execution is gone.
const ReasonLost = "no execution found and the runner never recorded the run"

// ReasonNoFinalRecord explains a run whose execution is gone but whose
// record never reached a final status (OOM kill, task timeout, node loss).
const ReasonNoFinalRecord = "execution ended without finalizing"

// Input is everything known about one run.
type Input struct {
	Slug, RunID  string
	Task         *task.Spec
	Launch       *runstore.Launch
	Record       *runstore.Record
	Exec         *backend.Execution
	CancelMarker bool
	Claim        *runstore.Claim // the "launching" marker, if any
	// Problem, when set, is why the run's objects can't be followed; the
	// row is then StatusError with Problem as its reason.
	Problem string
}

// Row is one run as ls and diagnose show it.
type Row struct {
	Run         string        `json:"run"`
	Repo        string        `json:"repo"`
	Workflow    string        `json:"workflow,omitempty"`
	RunID       string        `json:"run_id"`
	Status      string        `json:"status"`
	Stage       string        `json:"stage,omitempty"`
	Reason      string        `json:"reason,omitempty"`
	Batch       string        `json:"batch,omitempty"`
	RequestedBy string        `json:"requested_by,omitempty"`
	PRURL       string        `json:"pr_url,omitempty"`
	Execution   string        `json:"execution,omitempty"`
	LogURL      string        `json:"log_url,omitempty"`
	Created     time.Time     `json:"created"`
	Cost        runstore.Cost `json:"cost"`
	Terminal    bool          `json:"terminal"`
	// Settled means the row won't change on its own: terminal, or
	// unlaunched (nobody is launching it). ls --watch stops when every row
	// is settled; a launching row is not, since its launch.json is coming.
	Settled bool `json:"settled"`
}

// PriceBook is the compute prices of a region (a local override, else the
// list price); "" means the caller's default region.
type PriceBook func(region string) backend.Prices

// Join builds the row for in, pricing compute with prices.
func Join(in Input, prices PriceBook, now time.Time) Row {
	row := Row{Run: in.Slug + "/" + in.RunID, RunID: in.RunID}
	row.Created, _ = runstore.RunTime(in.RunID)
	if t := in.Task; t != nil {
		row.Repo, row.Workflow, row.Batch, row.RequestedBy = t.Repo, t.Workflow, t.Batch, t.RequestedBy
	} else {
		row.Reason = "task.json unreadable"
	}
	if in.Launch != nil {
		row.Execution, row.LogURL = in.Launch.Execution, in.Launch.LogURL
	}
	r, e := in.Record, in.Exec
	if r != nil {
		row.Stage = r.Stage
		if r.Reason != "" {
			row.Reason = r.Reason
		}
		if r.PR != nil {
			row.PRURL = r.PR.URL
		}
		if r.Execution != "" {
			// The runner's record names the execution that owns the run; after
			// a double launch, launch.json may name the duplicate.
			row.Execution = r.Execution
		}
	}
	if e != nil && row.LogURL == "" {
		row.LogURL = e.LogURL
	}
	unstarted := in.Launch == nil && r == nil
	if in.Problem != "" {
		// Settled: re-reading won't make an untrusted object trustworthy.
		row.Status, row.Reason, row.Execution, row.Settled = StatusError, in.Problem, "", true
		row.Cost = runstore.ModelOnlyCost(0, runstore.BasisAPIList)
		return row
	}
	switch {
	case r != nil && r.Status != runstore.StatusRunning:
		row.Status = string(r.Status)
	case unstarted && in.CancelMarker:
		row.Status, row.Reason = string(runstore.StatusCancelled), "cancelled before launch"
	case unstarted && in.Claim != nil && now.Sub(in.Claim.At) < runstore.ClaimTTL:
		row.Status = StatusLaunching
	case unstarted:
		row.Status = StatusUnlaunched // no claim, or a stale one: its launcher is gone
	case e != nil && e.State == backend.StateCancelled:
		row.Status, row.Reason = string(runstore.StatusCancelled), "execution cancelled before the run finalized"
	case e != nil && e.State.Terminal():
		row.Status, row.Reason = string(runstore.StatusInfraError), ReasonNoFinalRecord+" ("+string(e.State)+")"
	case e != nil && e.State == backend.StatePending:
		row.Status = StatusPending
	case e == nil && r == nil && Lost(in.Launch, row.Created, now):
		// A live execution is always found by the backend; past the claim
		// TTL, lag can't explain its absence.
		row.Status, row.Reason = string(runstore.StatusInfraError), ReasonLost
	case e == nil && r != nil && r.Deadline != nil && now.After(*r.Deadline):
		row.Status, row.Reason = string(runstore.StatusInfraError), "no execution found and past the run's deadline"
	case e == nil && r != nil && r.Deadline == nil:
		// Nothing bounds it and nothing runs it: it will never finish on
		// its own.
		row.Status, row.Reason = string(runstore.StatusInfraError), "no execution found and the run has no deadline"
	case e != nil || r != nil:
		row.Status = string(runstore.StatusRunning)
	default:
		row.Status = StatusPending // launch.json only; the backend doesn't know the execution yet
	}
	switch row.Status {
	case StatusPending, StatusLaunching, StatusUnlaunched, string(runstore.StatusRunning):
	default:
		row.Terminal = true
	}
	row.Settled = row.Terminal || row.Status == StatusUnlaunched
	row.Cost = cost(r, e, prices, now)
	if unstarted {
		// Never launched: no compute at all, which is known, not unestimated.
		row.Cost = runstore.NewCost(row.Cost.ModelUSD, 0, row.Cost.ModelBasis)
	}
	return row
}

// Lost reports whether launch l, of a run with no record and no execution
// the backend knows, is older than runstore.ClaimTTL, by launched_at, else
// by created (the run ID's time): the run is then lost, not pending.
func Lost(l *runstore.Launch, created, now time.Time) bool {
	at := created
	if l != nil && !l.LaunchedAt.IsZero() {
		at = l.LaunchedAt
	}
	return l != nil && !at.IsZero() && now.Sub(at) > runstore.ClaimTTL
}

// cost is the row's cost: the model spend from the record, and compute
// from the execution when its resources are known, priced in the
// execution's own region, else the record's.
func cost(r *runstore.Record, e *backend.Execution, prices PriceBook, now time.Time) runstore.Cost {
	basis, model := runstore.BasisAPIList, 0.0
	var stored *runstore.Cost
	if r != nil {
		model, stored = r.CostUSD, r.Cost
		if stored != nil && stored.ModelBasis != "" {
			basis = stored.ModelBasis
		}
	}
	switch {
	case e != nil && (e.CPU > 0 || e.MemoryGiB > 0):
		region := ""
		if id, ok := backend.ParseExecution(e.Name); ok {
			region = id.Region
		}
		return runstore.NewCost(model, prices(region).ComputeUSD(e.CPU, e.MemoryGiB, e.Billed(now)), basis)
	case stored != nil && stored.ComputeEstimated:
		return runstore.NewCost(model, stored.ComputeUSD, basis)
	default:
		return runstore.ModelOnlyCost(model, basis)
	}
}

// Totals sums rows' costs (design §10.1).
type Totals struct {
	Runs             int     `json:"runs"`
	ModelUSD         float64 `json:"model_usd"`          // billed model spend (api-list)
	ModelNotionalUSD float64 `json:"model_notional_usd"` // subscription model spend, not billed
	ComputeUSD       float64 `json:"compute_usd"`
	TotalUSD         float64 `json:"total_usd"`
	// ComputeNotEstimated counts the rows whose compute is unknown: their
	// compute_usd 0 means "not estimated", so the sums are a lower bound.
	ComputeNotEstimated int `json:"compute_not_estimated"`
}

// Sum totals rows.
func Sum(rows []Row) Totals {
	t := Totals{Runs: len(rows)}
	for _, r := range rows {
		if r.Cost.ModelBasis == runstore.BasisSubscription {
			t.ModelNotionalUSD += r.Cost.ModelUSD
		} else {
			t.ModelUSD += r.Cost.ModelUSD
		}
		t.ComputeUSD += r.Cost.ComputeUSD
		t.TotalUSD += r.Cost.TotalUSD
		if !r.Cost.ComputeEstimated {
			t.ComputeNotEstimated++
		}
	}
	return t
}

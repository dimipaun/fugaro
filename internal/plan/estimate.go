package plan

import (
	"math"
	"time"

	"github.com/dimipaun/fugaro/internal/estimate"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/tier"
)

// This file is the plan's estimate (docs/orchestrator-graph-estimates
// reconciliation D2722, D2723): what a graph has left to take and what it costs
// in all, from the run history internal/estimate holds and what a caller knows
// about the tasks that are already done or going.
//
// The two answers are the spec's own (spec.md §1) and they are informational:
// nothing here reserves budget, and the deployed per-run cap stays the ceiling
// whatever a tier is estimated at.
//
// Five readings decide the arithmetic, and each refuses a tempting shortcut:
//
//   - **The time is a path and a division, and the longer of the two wins.** A
//     plan cannot finish before its longest chain of work has run, and it cannot
//     finish faster than its remaining work divided by the workers it has. Both
//     are computed and the larger is the answer (D2722): a plan whose tasks all
//     wait on each other is bounded by its chain, and a plan of a hundred
//     independent tasks is bounded by its parallelism.
//   - **The critical path is weighted, not counted.** A plan's own read counts
//     its critical path in tasks (`Analyze`, D26), which is the right answer to
//     "how long is this plan" and the wrong one to "when is it ready": three
//     dependents that are each an afternoon are not the same plan as three that
//     are each an hour. This file is the weight that read was missing (F25).
//   - **A task's own figure is what it took, where there is one.** A running
//     task has spent part of its estimate, so it counts what is left of it —
//     max(0, estimate − elapsed), never a negative (D2722) — and a done task has
//     no minutes left at all, so it weighs nothing and the path it is on is the
//     path that is left. Its money is a different question: a done or running
//     task's cost is what it actually cost (D2723), which is why this read takes
//     the actual cost of both.
//   - **Nothing is invented where history is missing.** A task whose estimated
//     cost is not known makes the plan's total *unknown* rather than partly
//     counted, and the answer says how many tasks that was (D2723): a figure
//     that added up the tasks it knew about would read as the plan's cost and be
//     an undercount every time. The time has no such gap, because a tier always
//     has seed minutes to be estimated at (D2712).
//   - **A task the plan did not size is assumed medium.** `tier` is optional on
//     a node (D2711) and a plan that left it out has still asked for an
//     estimate, so the task is estimated at the middle of the four and its own
//     entry says `assumed` rather than naming a sample it never had (D2722).
//
// The history itself is not read here. `internal/estimate` is the table of what
// runs have taken and cost, and a caller that may reach the network builds it;
// a caller that may not — `fugaro plan graph`, the session-start brief — hands
// this read the cached one, so the same estimate is available offline and the
// two callers cannot disagree about which figures a history carries (D2714 as
// amended).

// BasisAssumed is the basis of a task the plan gave no tier: it is estimated at
// AssumedTier, which is this read's own choice rather than a sample the history
// or a seed answered. It completes internal/estimate's vocabulary, whose own
// Basis names the three samples a tier can be answered from and deliberately
// stops before this one — a task that names no tier is never that package's
// answer. Every node's basis is therefore one of four words (D2722).
const BasisAssumed estimate.Basis = "assumed"

// AssumedTier is the tier a task the plan gave no tier is estimated at (D2722).
// Medium is the middle of the four, which is what a plan that said nothing has
// said: assuming trivial would understate what a graph costs, and assuming large
// would put a person off starting work nothing has measured.
const AssumedTier = tier.Medium

// Estimate is what a plan has left to take and what it costs in all: the two
// ranges a reader is shown, the words behind them, and the instant they were
// computed.
//
// The time is what is *left* and the cost is what the plan *costs* — the plan's
// own formulas state them that way (D2722, D2723), and the difference is
// deliberate: a person asks when the work will be ready and what it will have
// cost them, not what the second of those is once it is done.
type Estimate struct {
	// ReadyInMinutes is how long the plan has left: D2722's formula, the
	// weighted critical path and the remaining work over the running cap, at the
	// p25 and the p75 of the history behind each task.
	ReadyInMinutes estimate.Range `json:"ready_in_minutes"`
	// CostUSD is what the plan costs: what its done and running tasks have
	// actually cost, plus what its remaining tasks are estimated at, at the same
	// two percentiles (D2723).
	//
	// It is absent when some task's cost could not be known at all, and
	// CostUnknownTasks then says how many — the two fields are one answer, never
	// both and never neither.
	CostUSD *estimate.Range `json:"cost_usd,omitempty"`
	// CostUnknownTasks is how many tasks the plan's cost could not be computed
	// for: a task not started whose tier no run has paid for, or a done or
	// running one the caller gave no actual cost. It is zero exactly when
	// CostUSD is present.
	CostUnknownTasks int `json:"cost_unknown_tasks,omitempty"`
	// Nodes is one entry per task of the plan, in the plan's own order.
	Nodes []EstimateNode `json:"nodes"`
	// MaxRunning is how many tasks the estimate assumed run at once: the cap the
	// caller passed, or one where the caller passed none.
	MaxRunning int `json:"max_running"`
	// ComputedAt is when the estimate was computed, in UTC. It is what says how
	// stale a figure a screen is showing is — the history behind it carries its
	// own stamp, and nothing here refreshes anything.
	ComputedAt time.Time `json:"computed_at"`
}

// EstimateNode is one task's own part of the estimate.
//
// Basis is the whole of it because the figures are the estimate's: a reader that
// says "history for 9, seeds for 3" is asking how well the plan's tasks are
// known, and it asks it per task so a plan of mixed tiers is counted honestly
// rather than averaged into one word.
type EstimateNode struct {
	// ID is the task's id, as the graph spells it.
	ID string `json:"id"`
	// Basis is the sample this task's tier was answered from — `history`, `tier`
	// or `seed` — and `assumed` for a task the plan gave no tier (D2722). What
	// the task's own figures are is another matter: a done task's basis still
	// names the sample its tier would have been estimated from, while the money
	// it contributes is what it actually cost.
	Basis estimate.Basis `json:"basis"`
}

// NodeProgress is what a caller knows about one task of the plan beyond the
// graph: whether its work is done or going, how long a run has been going, and
// what it has cost. It is an input and not an inference — this read opens no
// bucket, joins no run and asks no cloud service, so the caller that has the
// runs is the caller that says.
//
// Done is read before Running, so a caller that knew of a run finishing between
// two reads leaves the task done rather than running; a task in neither state is
// one whose work has not started.
type NodeProgress struct {
	// Done is true for a task whose work is finished. It weighs nothing on the
	// path (D2722) and contributes the actual cost below (D2723).
	Done bool
	// Running is true for a task a run is working on now. It weighs what is left
	// of its estimate after ElapsedMinutes.
	Running bool
	// ElapsedMinutes is how long the task's run has been going, read only for a
	// running task. A run already past its estimate leaves zero minutes, never
	// negative ones (D2722).
	ElapsedMinutes float64
	// CostUSD is what the task has actually cost — the whole of a done run's
	// cost, the part of a running one's paid so far — and it is read for a done
	// or running task only. nil is "the caller has no figure", which is what a
	// record from before the cost breakdown existed gives, and the plan's total
	// then reads unknown rather than counting the task as free (D2723).
	CostUSD *float64
}

// EstimatePlan computes a plan's remaining time and cost (D2722, D2723).
//
// doc is the graph artifact; it is validated here, so a caller that built one by
// hand gets the read's own refusals rather than a walk that cannot finish. table
// is the run history this estimate is drawn from, and it may be nil: an empty
// history is the state every repository starts in, and it answers every tier its
// seed minutes and no cost at all (D2712).
//
// state is what the caller knows about the tasks, by node id, and a task it does
// not name is one whose work has not started. An id that is not a task of this
// graph is ignored rather than refused: a caller reading a plan folder and an
// orchestrator node at once may hold entries for tasks this graph does not
// carry, and the graph decides which tasks exist.
//
// maxRunning is how many tasks run at once — the daemon's `orchestrator
// .max_running`, which defaults to the person's `max_parallel` (A6). A value
// below one reads as one: a plan that runs nothing in parallel is the honest
// reading of "no cap was named", and it is the only figure that keeps the
// division finite, which is also what a document has to be to carry a number at
// all.
//
// mode is the rigor mode the tasks are estimated at, and it is the caller's
// because a rigor can be set per area and per run: a mode the history has no
// group for falls back to the tier's own sample, which the basis says.
//
// now is the caller's clock, stamped on the answer.
func EstimatePlan(doc Document, table *estimate.Table, state map[string]NodeProgress, maxRunning int, mode rigor.Mode, now time.Time) (Estimate, error) {
	normalized, err := Normalize("the graph", doc)
	if err != nil {
		return Estimate{}, err
	}
	doc = normalized
	if maxRunning < 1 {
		maxRunning = 1
	}

	minutesLow := make(map[string]float64, len(doc.Nodes))
	minutesHigh := make(map[string]float64, len(doc.Nodes))
	costLow := make(map[string]float64, len(doc.Nodes))
	costHigh := make(map[string]float64, len(doc.Nodes))

	nodes := make([]EstimateNode, 0, len(doc.Nodes))
	unknown := 0
	for _, n := range doc.Nodes {
		// What the history says about this task's tier. A task the plan gave no
		// tier is asked about medium all the same — the estimate has to put a
		// figure somewhere — and its basis says that is what happened.
		tr, basis := AssumedTier, BasisAssumed
		if n.Tier != nil {
			tr = *n.Tier
		}
		answer := table.Answer(tr, mode)
		if n.Tier != nil {
			basis = answer.Basis
		}
		nodes = append(nodes, EstimateNode{ID: n.ID, Basis: basis})

		progress := state[n.ID]
		switch {
		case progress.Done:
			// A finished task has no minutes left: D2722 gives it nothing, so the
			// chain it sits on is the work that is still in front of it and not
			// the time it already took.
			minutesLow[n.ID], minutesHigh[n.ID] = 0, 0
		case progress.Running:
			minutesLow[n.ID] = leftToRun(answer.Minutes.Low, progress.ElapsedMinutes)
			minutesHigh[n.ID] = leftToRun(answer.Minutes.High, progress.ElapsedMinutes)
		default:
			minutesLow[n.ID], minutesHigh[n.ID] = answer.Minutes.Low, answer.Minutes.High
		}

		// The money of a task that is done or going is what it cost; the money of
		// one not started is its tier's estimate. A task with neither figure —
		// a record carrying no cost breakdown, a tier no run has paid for — is
		// counted as unknown rather than as nothing, because a task counted as
		// nothing is a total that reads low with no sign on it (D2723).
		switch {
		case (progress.Done || progress.Running) && progress.CostUSD != nil:
			costLow[n.ID], costHigh[n.ID] = *progress.CostUSD, *progress.CostUSD
		case !progress.Done && !progress.Running && answer.HasCost:
			costLow[n.ID], costHigh[n.ID] = answer.Cost.Low, answer.Cost.High
		default:
			unknown++
		}
	}

	sum := func(weight map[string]float64) float64 {
		total := 0.0
		for _, n := range doc.Nodes {
			total += weight[n.ID]
		}
		return total
	}

	out := Estimate{
		ReadyInMinutes: estimate.Range{
			Low:  math.Max(weightedPath(doc, minutesLow), sum(minutesLow)/float64(maxRunning)),
			High: math.Max(weightedPath(doc, minutesHigh), sum(minutesHigh)/float64(maxRunning)),
		},
		Nodes:      nodes,
		MaxRunning: maxRunning,
		ComputedAt: now.UTC(),
	}
	if unknown > 0 {
		out.CostUnknownTasks = unknown
		return out, nil
	}
	total := estimate.Range{Low: sum(costLow), High: sum(costHigh)}
	out.CostUSD = &total
	return out, nil
}

// leftToRun is what a running task has left of an estimate: D2722's
// max(0, estimate − elapsed). A run already past its estimate is over it rather
// than owed minutes, and a negative weight would take time off every chain the
// task is on — an estimate that shrank as work got slower, which is the one
// thing this figure must never do.
func leftToRun(want, elapsed float64) float64 { return math.Max(0, want-elapsed) }

// weightedPath is the longest chain of dependencies in the graph, each task
// weighed by what the caller put in weight: D2722's "critical path weighted by
// each open task's duration estimate", which is the read's own critical path
// (Analyze, D26) with minutes on it instead of a count of tasks.
//
// The walk is memoized over the dependencies, so a diamond is measured once per
// task rather than once per path, and it terminates because the graph is
// acyclic: Normalize refused it otherwise before this walk exists.
func weightedPath(doc Document, weight map[string]float64) float64 {
	needs := make(map[string][]string, len(doc.Nodes))
	for _, e := range doc.Edges {
		needs[e.Task] = append(needs[e.Task], e.Needs)
	}
	behind := make(map[string]float64, len(doc.Nodes))
	var longest func(string) float64
	longest = func(id string) float64 {
		if got, ok := behind[id]; ok {
			return got
		}
		deepest := 0.0
		for _, dep := range needs[id] {
			if l := longest(dep); l > deepest {
				deepest = l
			}
		}
		behind[id] = weight[id] + deepest
		return behind[id]
	}
	out := 0.0
	for _, n := range doc.Nodes {
		if l := longest(n.ID); l > out {
			out = l
		}
	}
	return out
}

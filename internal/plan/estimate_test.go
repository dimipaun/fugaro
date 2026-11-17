package plan

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/estimate"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/tier"
)

// The tests below are the plan's estimate (D2722, D2723): the weighted critical
// path, the capped sum, what a running and a done task count, what a task
// without cost history does to the total, and the shape the answer is written
// in. Every table here is built by hand rather than from run records, because
// the arithmetic a test asserts is the estimate's and not internal/estimate's —
// that package's own tests pin how a table is read out of history.

// estimateAt is the instant every estimate here is computed at, so the
// document's own stamp is a figure a test can spell.
var estimateAt = time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)

// estTable is a history of the groups a test names, stamped at estimateAt. It
// carries no version of its own: the version is the cache file's, and an
// estimate computed over a table in memory has none to check.
func estTable(groups ...estimate.Group) *estimate.Table {
	return &estimate.Table{WrittenAt: estimateAt, Groups: groups}
}

// estGroup is one tier's sample at the plan's own rigor mode: WideRuns runs, so
// the answer is the p25-p75 as written — no widening, and no fallback to the
// tier — and the arithmetic a test asserts is the figures it wrote down. The
// durations are in minutes and the costs in dollars, in p25/p50/p75 order.
func estGroup(tr tier.Tier, minutes, cost [3]float64) estimate.Group {
	c := estimate.Quartiles{P25: cost[0], P50: cost[1], P75: cost[2]}
	return estimate.Group{
		Tier:            tr,
		Rigor:           rigor.Pro,
		Runs:            estimate.WideRuns,
		DurationMinutes: estimate.Quartiles{P25: minutes[0], P50: minutes[1], P75: minutes[2]},
		CostUSD:         &c,
		CostRuns:        estimate.WideRuns,
	}
}

// estDoc is a plan of the tasks given, in the order given, with no edges: a
// test that needs any adds them itself.
func estDoc(nodes ...Node) Document {
	return Document{
		SchemaVersion: SchemaVersion,
		Plan:          "docs/plans/estimate.md",
		Nodes:         nodes,
		Edges:         []Edge{},
	}
}

// estNode is one task of such a plan: an id, a title and the tier it is
// expected to be run at.
func estNode(id string, tr tier.Tier) Node {
	return Node{ID: id, Title: id, Footprint: NewFootprint(nil), Tier: &tr}
}

// estUntiered is a task the planner gave no tier: the shape D2711 leaves
// optional and D2722 answers at medium.
func estUntiered(id string) Node {
	return Node{ID: id, Title: id, Footprint: NewFootprint(nil)}
}

// estNeeds is one dependency of a test's graph: task waits for needs.
func estNeeds(task, needs string) Edge {
	return Edge{Task: task, Needs: needs, Reason: task + " waits for " + needs}
}

// estRange is a range a test spells, so a comparison names the two figures
// rather than the type's own field order.
func estRange(low, high float64) estimate.Range { return estimate.Range{Low: low, High: high} }

// TestTimeIsMaxOfPathAndCappedSum: D2722's formula is the larger of two
// answers, and which one wins depends on the plan's shape rather than on its
// size. A chain is bounded by its longest path however many workers it has, and
// a fan of independent tasks is bounded by the workers — and both figures are
// the weighted path and the capped sum, in minutes, rather than a count of
// tasks.
func TestTimeIsMaxOfPathAndCappedSum(t *testing.T) {
	// Medium is twenty runs of 10..30 minutes: p25 10, p50 20, p75 30, and the
	// range as p25-p75 because the sample is not under WideRuns.
	table := estTable(estGroup(tier.Medium, [3]float64{10, 20, 30}, [3]float64{1, 2, 3}))

	// A chain of three: the path is all three tasks, 30 minutes at the p25 and
	// 90 at the p75. Running them four at a time cannot shorten a chain, so the
	// path wins.
	chain := estDoc(estNode("a", tier.Medium), estNode("b", tier.Medium), estNode("c", tier.Medium))
	chain.Edges = []Edge{estNeeds("b", "a"), estNeeds("c", "b")}
	got, err := EstimatePlan(chain, table, nil, 4, rigor.Pro, estimateAt)
	if err != nil {
		t.Fatalf("a chain of three was refused: %v", err)
	}
	if want := estRange(30, 90); got.ReadyInMinutes != want {
		t.Errorf("a chain of three at four workers: ready_in_minutes = %+v, want %+v (the weighted path, not the sum over the cap)", got.ReadyInMinutes, want)
	}
	// The cost is every task's, whether or not the path runs through it: three
	// medium tasks are $1 to $3 each.
	if want := estRange(3, 9); got.CostUSD == nil || *got.CostUSD != want {
		t.Errorf("a chain of three: cost_usd = %v, want %+v", got.CostUSD, want)
	}

	// Six tasks and nothing between them: the path is one task, 10 to 30
	// minutes, and one worker at a time has to do all six, so the sum decides.
	fan := estDoc(
		estNode("a", tier.Medium), estNode("b", tier.Medium), estNode("c", tier.Medium),
		estNode("d", tier.Medium), estNode("e", tier.Medium), estNode("f", tier.Medium),
	)
	got, err = EstimatePlan(fan, table, nil, 1, rigor.Pro, estimateAt)
	if err != nil {
		t.Fatalf("a fan of six was refused: %v", err)
	}
	if want := estRange(60, 180); got.ReadyInMinutes != want {
		t.Errorf("six independent tasks at one worker: ready_in_minutes = %+v, want %+v (the remaining work over the cap, not one task's own minutes)", got.ReadyInMinutes, want)
	}

	// The same six with three workers: the cap is a divisor and not a floor, so
	// the sum is a third of what one worker would take — and still more than one
	// task's own range.
	got, err = EstimatePlan(fan, table, nil, 3, rigor.Pro, estimateAt)
	if err != nil {
		t.Fatalf("a fan of six at three workers was refused: %v", err)
	}
	if want := estRange(20, 60); got.ReadyInMinutes != want {
		t.Errorf("six independent tasks at three workers: ready_in_minutes = %+v, want %+v", got.ReadyInMinutes, want)
	}
}

// TestRunningNodeCountsRemaining: a task a run is working on has spent part of
// its estimate, so it counts what is left of it — max(0, estimate − elapsed) —
// and a run already past the low end of its range owes the plan no minutes at
// all rather than negative ones (D2722).
func TestRunningNodeCountsRemaining(t *testing.T) {
	table := estTable(estGroup(tier.Medium, [3]float64{10, 20, 30}, [3]float64{1, 2, 3}))
	doc := estDoc(estNode("a", tier.Medium))

	cases := []struct {
		elapsed float64
		want    estimate.Range
	}{
		// Six minutes into a 10..30 task: 4 to 24 minutes are left.
		{elapsed: 6, want: estRange(4, 24)},
		// Fifteen minutes in: the low end is past, so the range starts at zero
		// and not at -5.
		{elapsed: 15, want: estRange(0, 15)},
		// Past the whole range: nothing is left, and the plan is not owed time.
		{elapsed: 40, want: estRange(0, 0)},
	}
	for _, c := range cases {
		state := map[string]NodeProgress{"a": {Running: true, ElapsedMinutes: c.elapsed}}
		got, err := EstimatePlan(doc, table, state, 1, rigor.Pro, estimateAt)
		if err != nil {
			t.Fatalf("a running task at %g minutes was refused: %v", c.elapsed, err)
		}
		if got.ReadyInMinutes != c.want {
			t.Errorf("a task running for %g minutes: ready_in_minutes = %+v, want %+v", c.elapsed, got.ReadyInMinutes, c.want)
		}
	}
}

// TestDoneNodeCountsActual: a finished task has no minutes left, so the chain
// it sits on is the work still in front of it (D2722) — and its money is what
// it actually cost rather than what its tier was estimated at (D2723).
func TestDoneNodeCountsActual(t *testing.T) {
	table := estTable(estGroup(tier.Medium, [3]float64{10, 20, 30}, [3]float64{1, 2, 3}))
	doc := estDoc(estNode("a", tier.Medium), estNode("b", tier.Medium))
	doc.Edges = []Edge{estNeeds("b", "a")}

	actual := 4.0
	state := map[string]NodeProgress{"a": {Done: true, CostUSD: &actual}}
	got, err := EstimatePlan(doc, table, state, 1, rigor.Pro, estimateAt)
	if err != nil {
		t.Fatalf("a plan with a done task was refused: %v", err)
	}
	// One task's minutes are left: b's own range, not the chain's.
	if want := estRange(10, 30); got.ReadyInMinutes != want {
		t.Errorf("a done task in front of a queued one: ready_in_minutes = %+v, want %+v (a finished task weighs nothing)", got.ReadyInMinutes, want)
	}
	// But its money counts, and on top of the estimate for the task that is left.
	if want := estRange(5, 7); got.CostUSD == nil || *got.CostUSD != want {
		t.Errorf("a done task costing $4 in front of a queued one: cost_usd = %v, want %+v (the actual $4 plus $1..$3)", got.CostUSD, want)
	}
}

// TestMissingCostHistoryMakesUnknown: a plan's cost is withheld rather than
// partly counted when some task's cost is not known at all, and the answer says
// how many tasks that was (D2723). A tier with no run history is the case every
// repository starts in: it has seed minutes and no cost figure.
func TestMissingCostHistoryMakesUnknown(t *testing.T) {
	table := estTable(estGroup(tier.Medium, [3]float64{10, 20, 30}, [3]float64{1, 2, 3}))
	// Large is not in the table at all, so it answers its seed: 240 minutes
	// widened to half of them and twice them, and no cost.
	doc := estDoc(estNode("a", tier.Medium), estNode("b", tier.Large))
	got, err := EstimatePlan(doc, table, nil, 1, rigor.Pro, estimateAt)
	if err != nil {
		t.Fatalf("a plan of two known tiers was refused: %v", err)
	}
	if got.CostUSD != nil {
		t.Errorf("cost_usd = %+v, want no figure: one task's cost is not known, and a total that left it out would read as the plan's cost (D2723)", *got.CostUSD)
	}
	if got.CostUnknownTasks != 1 {
		t.Errorf("cost_unknown_tasks = %d, want 1 (the large task, whose tier no run has paid for)", got.CostUnknownTasks)
	}
	// The time is answered all the same: a tier always has seed minutes.
	if want := estRange(130, 510); got.ReadyInMinutes != want {
		t.Errorf("ready_in_minutes = %+v, want %+v (medium's 10..30 and large's seeded 120..480, one worker)", got.ReadyInMinutes, want)
	}
	if got := estimateBasis(t, got, "a"); got != estimate.BasisHistory {
		t.Errorf("a's basis = %q, want %q: medium's twenty runs answered it", got, estimate.BasisHistory)
	}
	if got := estimateBasis(t, got, "b"); got != estimate.BasisSeed {
		t.Errorf("b's basis = %q, want %q: no run of large has been paid for", got, estimate.BasisSeed)
	}

	// The side of the same rule a plan's own mode is on: a history with no
	// group for that mode answers from the tier alone, so the figures and the
	// cost step are the tier's own — a thin (tier, mode) sample makes a plan's
	// cost narrower rather than unknown — and the node's basis says which of the
	// two answered (D2713).
	byTier := estGroup(tier.Medium, [3]float64{10, 20, 30}, [3]float64{1, 2, 3})
	byTier.Rigor = ""
	got, err = EstimatePlan(estDoc(estNode("a", tier.Medium)), estTable(byTier), nil, 1, rigor.Pro, estimateAt)
	if err != nil {
		t.Fatalf("a plan whose mode has no group was refused: %v", err)
	}
	if got := estimateBasis(t, got, "a"); got != estimate.BasisTier {
		t.Errorf("a's basis = %q, want %q: no run of medium resolved the plan's mode", got, estimate.BasisTier)
	}
	if want := estRange(1, 3); got.CostUSD == nil || *got.CostUSD != want {
		t.Errorf("a plan answered by its tier alone: cost_usd = %v, want %+v (the tier's own median costs)", got.CostUSD, want)
	}
}

// TestUntieredNodeAssumedMedium: `tier` is optional on a node (D2711) and a
// plan that left it out has still asked for an estimate, so the task is
// estimated at the middle of the four and its own entry says `assumed` rather
// than naming a sample it never had (D2722). The figures are medium's — the
// assumption is a tier and not a gap — while the word a reader sees says which
// of the two it is looking at.
func TestUntieredNodeAssumedMedium(t *testing.T) {
	table := estTable(
		estGroup(tier.Small, [3]float64{2, 4, 6}, [3]float64{0.5, 1, 1.5}),
		estGroup(tier.Medium, [3]float64{10, 20, 30}, [3]float64{1, 2, 3}),
	)
	doc := estDoc(estUntiered("a"))
	got, err := EstimatePlan(doc, table, nil, 1, rigor.Pro, estimateAt)
	if err != nil {
		t.Fatalf("a plan with an untiered task was refused: %v", err)
	}
	if want := estRange(10, 30); got.ReadyInMinutes != want {
		t.Errorf("an untiered task: ready_in_minutes = %+v, want %+v (medium's own range)", got.ReadyInMinutes, want)
	}
	if want := estRange(1, 3); got.CostUSD == nil || *got.CostUSD != want {
		t.Errorf("an untiered task: cost_usd = %v, want %+v", got.CostUSD, want)
	}
	if got := estimateBasis(t, got, "a"); got != BasisAssumed {
		t.Errorf("an untiered task's basis = %q, want %q: nothing in the history answered it", got, BasisAssumed)
	}
}

// TestEstimateJSONNames: the shape a console and a skill read. The block is
// additive to what `plan status --json` prints, so its names are a contract
// (CLAUDE.md, a field never changes meaning) — and the cost is either a range
// or a count of the tasks it could not be computed for, never both and never
// neither (D2723).
func TestEstimateJSONNames(t *testing.T) {
	table := estTable(estGroup(tier.Medium, [3]float64{10, 20, 30}, [3]float64{1, 2, 3}))
	doc := estDoc(estNode("a", tier.Medium))
	got, err := EstimatePlan(doc, table, nil, 2, rigor.Pro, estimateAt)
	if err != nil {
		t.Fatalf("a plan of one task was refused: %v", err)
	}

	want := []string{"computed_at", "cost_usd", "max_running", "nodes", "ready_in_minutes"}
	if keys := estimateKeys(t, got); !reflect.DeepEqual(keys, want) {
		t.Errorf("the estimate's keys = %v, want %v", keys, want)
	}
	if keys, want := estimateKeys(t, got.ReadyInMinutes), []string{"high", "low"}; !reflect.DeepEqual(keys, want) {
		t.Errorf("ready_in_minutes' keys = %v, want %v", keys, want)
	}
	if keys, want := estimateKeys(t, got.Nodes[0]), []string{"basis", "id"}; !reflect.DeepEqual(keys, want) {
		t.Errorf("a node's keys = %v, want %v", keys, want)
	}
	if got.MaxRunning != 2 {
		t.Errorf("max_running = %d, want the 2 the caller passed", got.MaxRunning)
	}
	if !got.ComputedAt.Equal(estimateAt) || got.ComputedAt.Location() != time.UTC {
		t.Errorf("computed_at = %v, want the instant the estimate was computed at, in UTC", got.ComputedAt)
	}

	// A task whose cost is not known swaps the range for the count, and
	// cost_usd is gone rather than zero.
	unknown, err := EstimatePlan(estDoc(estUntiered("a")), estTable(), nil, 2, rigor.Pro, estimateAt)
	if err != nil {
		t.Fatalf("a plan with no history was refused: %v", err)
	}
	want = []string{"computed_at", "cost_unknown_tasks", "max_running", "nodes", "ready_in_minutes"}
	if keys := estimateKeys(t, unknown); !reflect.DeepEqual(keys, want) {
		t.Errorf("the estimate's keys with no cost history = %v, want %v", keys, want)
	}
	// And the range is written the same way whether or not a sample answered it:
	// an empty history is the seeds, not a refusal.
	if want := estRange(45, 180); unknown.ReadyInMinutes != want {
		t.Errorf("an empty history: ready_in_minutes = %+v, want %+v (medium's 90 seed minutes at half and twice them)", unknown.ReadyInMinutes, want)
	}
}

// estimateBasis is one task's basis in an estimate, and it fails the test when
// the estimate does not carry that task at all: a node missing from the answer
// is a node no reader can count, which is a different failure from a wrong
// word.
func estimateBasis(t *testing.T, est Estimate, id string) estimate.Basis {
	t.Helper()
	for _, n := range est.Nodes {
		if n.ID == id {
			return n.Basis
		}
	}
	t.Fatalf("the estimate carries no node %q; it carries %d of them", id, len(est.Nodes))
	return ""
}

// estimateKeys is the top-level keys of a value's JSON, sorted, so a test pins
// a document's shape rather than one spelling of it.
func estimateKeys(t *testing.T, v any) []string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("the marshalled value is not an object: %v", err)
	}
	keys := make([]string, 0, len(doc))
	for k := range doc {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

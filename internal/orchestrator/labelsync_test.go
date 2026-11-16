package orchestrator

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
)

// The label syncer's tests (T5).
//
// A whole tick is driven here — the seams are the Tick's own, the host behind
// them is internal/gitprov/fake, and the node is the state a test seeds — so
// what is pinned is what a daemon does to a repository's labels across ticks:
// the difference and nothing else written, a person's labels left where they
// are, a pull request that closed cleared once, a retry's first pull request
// left failed, and a failure that costs a line and never the cycle.

// labelNow is the clock a label test's tick stamps with. Nothing the syncer
// writes is stamped, so its value only has to be a reading.
var labelNow = time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)

// The two runs a label test works with: the ids a run really carries, because
// the state validator refuses anything else and a test that seeded one would
// pin the syncer against a node no daemon could hold.
const (
	runA = "20261116-120000-a1b2"
	runB = "20261116-120001-c3d4"
)

// labelStore is the node a tick reads and writes: the state a test seeded and
// the ETag it was read at. The label syncer writes no state, so Save is only
// the store's contract.
type labelStore struct {
	state State
	etag  ETag
}

func (s *labelStore) Load(context.Context) (State, ETag, error) { return s.state, s.etag, nil }

func (s *labelStore) Save(_ context.Context, next State, _ ETag) error {
	s.state, s.etag = next, "2"
	return nil
}

// labelRuns is the run view a label test's tick reads: no run ever reports, so
// the cycle folds nothing and the items are the test's own.
type labelRuns struct{}

func (labelRuns) Runs(context.Context, []string) ([]RunReport, error) { return nil, nil }

// labelPRs is the reader of the pull request behind a run: the numbers a test
// sets, the run ids it was asked for, and the one failure it answers with.
type labelPRs struct {
	numbers map[string]int
	asked   []string
	err     error
}

func (r *labelPRs) PRNumber(_ context.Context, runID string) (int, bool, error) {
	r.asked = append(r.asked, runID)
	if r.err != nil {
		return 0, false, r.err
	}
	number, ok := r.numbers[runID]
	return number, ok, nil
}

// labelHosts is the host of each repository, resolved as a daemon resolves one,
// and the repositories the tick asked for.
type labelHosts struct {
	hosts map[string]*fake.Provider
	asked []string
}

func (h *labelHosts) LabelerFor(_ context.Context, repo string) (gitprov.Labeler, error) {
	h.asked = append(h.asked, repo)
	p, ok := h.hosts[repo]
	if !ok {
		return nil, fmt.Errorf("no host for %s", repo)
	}
	return p, nil
}

// labelLines is the log a label test reads.
type labelLines struct{ lines []string }

func (l *labelLines) Warn(_ context.Context, line string) { l.lines = append(l.lines, line) }

// labelHarness is one daemon's tick and what its labels are kept through.
type labelHarness struct {
	t     *testing.T
	store *labelStore
	hosts *labelHosts
	prs   *labelPRs
	log   *labelLines
	tick  *Tick
}

// newLabelHarness is a daemon's first tick over one state. A second Step is the
// same daemon's next tick, because the seams and their memory are the same;
// another harness is the daemon after a restart, over the same hosts.
func newLabelHarness(t *testing.T, s State, hosts map[string]*fake.Provider, prs *labelPRs) *labelHarness {
	t.Helper()
	h := &labelHarness{
		t:     t,
		store: &labelStore{state: mustLabelState(t, s), etag: "1"},
		hosts: &labelHosts{hosts: hosts},
		prs:   prs,
		log:   &labelLines{},
	}
	h.tick = h.nextTick()
	return h
}

// nextTick is this daemon's next tick: seams a fresh daemon has — a memory with
// nothing in it — over the same hosts, which is the restart a test asks about.
func (h *labelHarness) nextTick() *Tick {
	return &Tick{
		Store:  h.store,
		Runs:   labelRuns{},
		Mint:   func(time.Time) (string, error) { return "run-unused", nil },
		Now:    func() (time.Time, bool) { return labelNow, true },
		Labels: LabelSeams{Labelers: h.hosts, PRs: h.prs, Log: h.log},
	}
}

// step runs one tick and fails the test if the cycle itself did not finish: a
// label failure is one line in the log and never a failed tick.
func (h *labelHarness) step() Report {
	h.t.Helper()
	rep, err := h.tick.Step(context.Background())
	if err != nil {
		h.t.Fatalf("the tick failed: %v", err)
	}
	return rep
}

// seed is the node the next tick reads: what a person or a session, or the
// fold of another cycle, moved the item to.
func (h *labelHarness) seed(t *testing.T, s State) {
	t.Helper()
	h.store.state = mustLabelState(t, s)
}

// mustLabelState is the state a test seeds, written and read back through the
// document's own encoding: a state this release would not accept would pin
// behaviour on a node no daemon could hold.
func mustLabelState(t *testing.T, s State) State {
	t.Helper()
	raw, err := EncodeState(s)
	if err != nil {
		t.Fatalf("the test's state is not one this release writes: %v", err)
	}
	back, err := DecodeState(raw)
	if err != nil {
		t.Fatalf("the test's state does not read back: %v", err)
	}
	return back
}

// workItem is one item of a test: the phase and the spec it is about, the run
// it recorded, and the repository it is on.
func workItem(id, repo string, phase Phase, runID, spec string) WorkItem {
	return WorkItem{
		ID:        id,
		Title:     "the widget",
		Repo:      repo,
		Recipe:    "default",
		RigorTier: "small",
		Brief:     "do the thing",
		Phase:     phase,
		RunID:     runID,
		Spec:      spec,
	}
}

// labelState is a node carrying the test's items and nothing else.
func labelState(items ...WorkItem) State {
	return State{
		SchemaVersion: SchemaVersion,
		Rev:           1,
		AgentID:       "daemon",
		Role:          RoleOrchestrator,
		WorkItems:     items,
	}
}

// labelHost is a fake host of one repository carrying the pull requests a test
// seeds.
func labelHost(repo string, prs ...fake.PRState) *fake.Provider {
	return &fake.Provider{Repo: repo, State: fake.State{PRs: prs}}
}

// labelPR is one pull request of a test's host: its number, its state (empty is
// open) and the labels it carries.
func labelPR(number int, state gitprov.PRState, labels ...string) fake.PRState {
	return fake.PRState{PR: gitprov.PR{Number: number}, State: state, Labels: labels}
}

// labelCalls is one host's label calls as lines a test reads: the op, the pull
// request and the run id, so a test asserts on what was asked rather than on
// the struct it was asked with.
func labelCalls(p *fake.Provider) []string {
	calls := p.Snapshot().LabelCalls
	out := make([]string, 0, len(calls))
	for _, c := range calls {
		switch c.Op {
		case "ListOpenPRLabels":
			out = append(out, "list")
		case "PRLabels":
			out = append(out, fmt.Sprintf("read #%d", c.PR))
		case "EnsureLabel":
			out = append(out, "ensure "+c.Name)
		case "AddLabels":
			out = append(out, fmt.Sprintf("add #%d run=%s %s", c.PR, c.RunID, strings.Join(c.Names, "+")))
		case "RemoveLabels":
			out = append(out, fmt.Sprintf("remove #%d run=%s %s", c.PR, c.RunID, strings.Join(c.Names, "+")))
		default:
			out = append(out, c.Op)
		}
	}
	return out
}

// wantCalls asserts one host's whole label call log.
func wantCalls(t *testing.T, p *fake.Provider, want ...string) {
	t.Helper()
	if got := labelCalls(p); !slices.Equal(got, want) {
		t.Errorf("the label calls are\n got %q\nwant %q", got, want)
	}
}

// prLabels is the labels a pull request of a host carries.
func prLabels(t *testing.T, p *fake.Provider, number int) []string {
	t.Helper()
	for _, pr := range p.Snapshot().PRs {
		if pr.Number == number {
			return pr.Labels
		}
	}
	t.Fatalf("the host carries no pull request #%d", number)
	return nil
}

// closePR closes one pull request of a test's host, as a person merging or
// closing it does. The fake takes one goroutine, which is the tick's.
func closePR(t *testing.T, p *fake.Provider, number int, state gitprov.PRState) {
	t.Helper()
	for i := range p.State.PRs {
		if p.State.PRs[i].Number == number {
			p.State.PRs[i].State = state
			return
		}
	}
	t.Fatalf("the host carries no pull request #%d", number)
}

// TestPhaseToLabels is R4: the status label each phase wants, and neither for
// every other phase.
func TestPhaseToLabels(t *testing.T) {
	for _, tc := range []struct {
		phase Phase
		want  []string
	}{
		{PhaseRunning, []string{labelRunning}},
		{PhaseFailed, []string{labelFailed}},
		{PhaseQueued, nil},
		{PhaseReview, nil},
		{PhaseBlocked, nil},
		{PhaseDone, nil},
		{PhaseCancelled, nil},
	} {
		t.Run(string(tc.phase), func(t *testing.T) {
			got := phaseToLabels(tc.phase)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("the labels of a %s item are %q, want %q", tc.phase, got, tc.want)
			}
			for _, name := range got {
				if colour, description := labelText(name); colour == "" || description == "" {
					t.Errorf("the label %s has no colour and description from the table", name)
				}
			}
		})
	}
}

// TestSpecLabelShape is R3: the only names a spec label is built from, and the
// names a hostile spec cannot become one of.
func TestSpecLabelShape(t *testing.T) {
	long := strings.Repeat("a", 44)
	for i, tc := range []struct {
		name string
		want string
	}{
		{"forge-labels", "spec:forge-labels"},
		{"FORGE-Labels", "spec:forge-labels"},
		{"a", "spec:a"},
		{"0-9", "spec:0-9"},
		{long, "spec:" + long},
		{"", ""},
		{strings.Repeat("a", 45), ""},
		{"Forge Labels", ""},
		{"forge_labels", ""},
		{"../../etc/passwd", ""},
		{"spec:forge-labels", ""},
		{"fugaro:running", ""},
		{"-leading", ""},
		{"forgé-labels", ""},
		{"Kelvin", ""},
		{"ſpec", ""},
	} {
		t.Run(fmt.Sprintf("%d-%q", i, tc.name), func(t *testing.T) {
			got, ok := specLabel(tc.name)
			switch {
			case tc.want == "" && ok:
				t.Fatalf("the name %q made the label %q", tc.name, got)
			case tc.want != "" && !ok:
				t.Fatalf("the name %q made no label, want %q", tc.name, tc.want)
			case got != tc.want:
				t.Fatalf("the name %q made the label %q, want %q", tc.name, got, tc.want)
			}
		})
	}

	t.Run("a name no label can be built from writes nothing and logs once", func(t *testing.T) {
		host := labelHost("o/a", labelPR(5, ""))
		lines := &labelLines{}
		seams := LabelSeams{
			Labelers: &labelHosts{hosts: map[string]*fake.Provider{"o/a": host}},
			PRs:      &labelPRs{numbers: map[string]int{runA: 5}},
			Log:      lines,
		}
		// The node is built here rather than through the document's own
		// encoding, and deliberately: a spec name of this shape is one the
		// node's validator refuses, so a state carrying one is exactly what
		// the syncer's own check is the last line against.
		s := State{
			SchemaVersion: SchemaVersion,
			AgentID:       "daemon",
			Role:          RoleOrchestrator,
			WorkItems:     []WorkItem{workItem("item-a", "o/a", PhaseRunning, runA, "../../etc/passwd")},
		}
		syncLabels(context.Background(), &seams, s)
		syncLabels(context.Background(), &seams, s)

		// The hostile name is no label of anybody's: the pull request gains its
		// status label, no spec label is written, and nothing asks the host to
		// create one.
		wantCalls(t, host, "list", "ensure fugaro:running", "add #5 run="+runA+" fugaro:running", "list")
		if got := prLabels(t, host, 5); !slices.Equal(got, []string{labelRunning}) {
			t.Errorf("the pull request carries %q, want the status label alone", got)
		}
		if len(lines.lines) != 1 {
			t.Fatalf("the log carries %d lines, want one: %q", len(lines.lines), lines.lines)
		}
	})
}

// TestSyncChangeOnly is F15 and D2435: one read per repository per tick, the
// difference written and nothing else, and a second tick with nothing changed
// writing nothing at all.
func TestSyncChangeOnly(t *testing.T) {
	hosts := map[string]*fake.Provider{
		"o/a": labelHost("o/a", labelPR(5, "", "spec:forge-labels")),
		"o/b": labelHost("o/b", labelPR(7, "", "spec:other", labelRunning)),
		"o/c": labelHost("o/c", labelPR(9, "")),
	}
	h := newLabelHarness(t, labelState(
		workItem("item-a", "o/a", PhaseRunning, runA, "forge-labels"),
		workItem("item-b", "o/b", PhaseDone, runB, "other"),
	), hosts, &labelPRs{numbers: map[string]int{runA: 5, runB: 7}})

	h.step()
	wantCalls(t, hosts["o/a"], "list", "ensure fugaro:running", "add #5 run="+runA+" fugaro:running")
	wantCalls(t, hosts["o/b"], "list", "remove #7 run="+runB+" fugaro:running")
	// o/c carries no item, so the tick neither read it nor resolved its host.
	wantCalls(t, hosts["o/c"])
	if got := h.hosts.asked; !slices.Equal(got, []string{"o/a", "o/b"}) {
		t.Errorf("the tick asked for the hosts %q, want the two repositories with an item", got)
	}

	h.step()
	wantCalls(t, hosts["o/a"], "list", "ensure fugaro:running", "add #5 run="+runA+" fugaro:running", "list")
	wantCalls(t, hosts["o/b"], "list", "remove #7 run="+runB+" fugaro:running", "list")
	if got := prLabels(t, hosts["o/a"], 5); !slices.Equal(got, []string{"spec:forge-labels", labelRunning}) {
		t.Errorf("pull request #5 carries %q after the second tick", got)
	}
	if got := prLabels(t, hosts["o/b"], 7); !slices.Equal(got, []string{"spec:other"}) {
		t.Errorf("pull request #7 carries %q after the second tick", got)
	}
	if len(h.log.lines) != 0 {
		t.Errorf("the ticks logged %q, want nothing", h.log.lines)
	}

	// A tick with no seams at all labels nothing: no read, no write, no line.
	bare := &Tick{
		Store: h.store,
		Runs:  labelRuns{},
		Mint:  func(time.Time) (string, error) { return "run-unused", nil },
		Now:   func() (time.Time, bool) { return labelNow, true },
	}
	if _, err := bare.Step(context.Background()); err != nil {
		t.Fatalf("the tick without seams failed: %v", err)
	}
	wantCalls(t, hosts["o/a"], "list", "ensure fugaro:running", "add #5 run="+runA+" fugaro:running", "list")
}

// TestSyncNeverTouchesForeignLabels is F23: a removal names the two status
// labels and never another, and a label a person added is left where it is.
func TestSyncNeverTouchesForeignLabels(t *testing.T) {
	host := labelHost("o/a",
		labelPR(5, "", "bug", "spec:someone-else", "FUGARO:FAILED"),
		labelPR(6, "", "spec:someone-else", labelRunning),
	)
	h := newLabelHarness(t, labelState(
		workItem("item-a", "o/a", PhaseRunning, runA, "forge-labels"),
		workItem("item-b", "o/a", PhaseDone, runB, ""),
	), map[string]*fake.Provider{"o/a": host}, &labelPRs{numbers: map[string]int{runA: 5, runB: 6}})

	h.step()
	// #5 wants its status label and its own spec label — the name the host
	// carries is somebody else's — and loses the failed label it holds under a
	// name spelled in capitals; #6 is done with no spec at all, so its own
	// status label goes from it and nothing else does.
	wantCalls(t, host,
		"list",
		"ensure fugaro:running",
		"ensure spec:forge-labels",
		"add #5 run="+runA+" fugaro:running+spec:forge-labels",
		"remove #5 run="+runA+" fugaro:failed",
		"remove #6 run="+runB+" fugaro:running",
	)
	// The status label matched case-insensitively, and the person's labels
	// survived both writes.
	if got := prLabels(t, host, 5); !slices.Equal(got, []string{"bug", "spec:someone-else", labelRunning, "spec:forge-labels"}) {
		t.Errorf("pull request #5 carries %q", got)
	}
	if got := prLabels(t, host, 6); !slices.Equal(got, []string{"spec:someone-else"}) {
		t.Errorf("pull request #6 carries %q, want the person's spec label", got)
	}
}

// TestSyncClearsOnMergedPR is R5: a pull request the open listing does not
// carry is read by number, a closed one loses both status labels, and it is not
// read again once it is known closed and clean.
func TestSyncClearsOnMergedPR(t *testing.T) {
	host := labelHost("o/a",
		labelPR(5, gitprov.PRMerged, "spec:forge-labels", labelRunning, labelFailed),
		labelPR(7, "", "spec:other"),
	)
	h := newLabelHarness(t, labelState(
		workItem("item-a", "o/a", PhaseFailed, runA, "forge-labels"),
		workItem("item-b", "o/a", PhaseRunning, runB, "other"),
	), map[string]*fake.Provider{"o/a": host}, &labelPRs{numbers: map[string]int{runA: 5, runB: 7}})

	h.step()
	wantCalls(t, host,
		"list",
		"read #5",
		"remove #5 run="+runA+" fugaro:running+fugaro:failed",
		"ensure fugaro:running",
		"add #7 run="+runB+" fugaro:running",
	)
	// The merged pull request keeps its spec label and loses both statuses.
	if got := prLabels(t, host, 5); !slices.Equal(got, []string{"spec:forge-labels"}) {
		t.Errorf("the merged pull request carries %q", got)
	}

	// The next tick reads the listing alone: a pull request known closed and
	// clean is not read by number again.
	h.step()
	wantCalls(t, host,
		"list",
		"read #5",
		"remove #5 run="+runA+" fugaro:running+fugaro:failed",
		"ensure fugaro:running",
		"add #7 run="+runB+" fugaro:running",
		"list",
	)
}

// TestSyncFailedStaysUntilClose is D2433 and R8: a failed run's pull request
// keeps fugaro:failed across ticks, and loses it when it closes.
func TestSyncFailedStaysUntilClose(t *testing.T) {
	host := labelHost("o/a", labelPR(5, "", "spec:forge-labels"))
	h := newLabelHarness(t, labelState(
		workItem("item-a", "o/a", PhaseFailed, runA, "forge-labels"),
	), map[string]*fake.Provider{"o/a": host}, &labelPRs{numbers: map[string]int{runA: 5}})

	h.step()
	wantCalls(t, host, "list", "ensure fugaro:failed", "add #5 run="+runA+" fugaro:failed")
	h.step()
	wantCalls(t, host, "list", "ensure fugaro:failed", "add #5 run="+runA+" fugaro:failed", "list")
	if got := prLabels(t, host, 5); !slices.Equal(got, []string{"spec:forge-labels", labelFailed}) {
		t.Fatalf("the failed pull request carries %q, want fugaro:failed still", got)
	}

	// It closes: the status label goes, and the spec label stays.
	closePR(t, host, 5, gitprov.PRClosed)
	h.step()
	wantCalls(t, host,
		"list",
		"ensure fugaro:failed",
		"add #5 run="+runA+" fugaro:failed",
		"list",
		"list",
		"read #5",
		"remove #5 run="+runA+" fugaro:failed",
	)
	if got := prLabels(t, host, 5); !slices.Equal(got, []string{"spec:forge-labels"}) {
		t.Errorf("the closed pull request carries %q", got)
	}
}

// TestSyncPerRunPullRequest is F21 and R8: the retry opens its own pull
// request, and the first one is left failed until it closes.
func TestSyncPerRunPullRequest(t *testing.T) {
	host := labelHost("o/a", labelPR(5, "", "spec:forge-labels"), labelPR(7, ""))
	h := newLabelHarness(t, labelState(
		workItem("item-a", "o/a", PhaseFailed, runA, "forge-labels"),
	), map[string]*fake.Provider{"o/a": host}, &labelPRs{numbers: map[string]int{runA: 5}})

	h.step()
	wantCalls(t, host, "list", "ensure fugaro:failed", "add #5 run="+runA+" fugaro:failed")

	// The retry: the item now names its own run, whose pull request is #7.
	h.seed(t, labelState(workItem("item-a", "o/a", PhaseRunning, runB, "forge-labels")))
	h.prs.numbers[runB] = 7
	h.step()
	wantCalls(t, host,
		"list",
		"ensure fugaro:failed",
		"add #5 run="+runA+" fugaro:failed",
		"list",
		"ensure fugaro:running",
		"ensure spec:forge-labels",
		"add #7 run="+runB+" fugaro:running+spec:forge-labels",
	)
	if got := prLabels(t, host, 5); !slices.Equal(got, []string{"spec:forge-labels", labelFailed}) {
		t.Errorf("the superseded pull request carries %q, want fugaro:failed still", got)
	}
	if got := prLabels(t, host, 7); !slices.Equal(got, []string{labelRunning, "spec:forge-labels"}) {
		t.Errorf("the retry's pull request carries %q", got)
	}
}

// TestSyncFailureLogsOnce is the spec's best-effort rule: a failed label call
// never fails the tick, and a spec label the host refused costs one line.
func TestSyncFailureLogsOnce(t *testing.T) {
	t.Run("a spec label the host refused is logged once and never asked for again", func(t *testing.T) {
		host := labelHost("o/a", labelPR(5, ""))
		host.FailEnsureLabel = 1
		h := newLabelHarness(t, labelState(
			workItem("item-a", "o/a", PhaseDone, runA, "forge-labels"),
		), map[string]*fake.Provider{"o/a": host}, &labelPRs{numbers: map[string]int{runA: 5}})

		h.step()
		// The ensure was the only call that could have written, and its failure
		// left the label out of the add rather than writing it uncoloured.
		wantCalls(t, host, "list", "ensure spec:forge-labels")
		if len(h.log.lines) != 1 {
			t.Fatalf("the log carries %d lines, want one: %q", len(h.log.lines), h.log.lines)
		}
		if !strings.Contains(h.log.lines[0], "spec:forge-labels") {
			t.Errorf("the line %q names no label", h.log.lines[0])
		}

		h.step()
		wantCalls(t, host, "list", "ensure spec:forge-labels", "list")
		if len(h.log.lines) != 1 {
			t.Errorf("the second tick logged %q, want the refusal remembered", h.log.lines)
		}
		if got := prLabels(t, host, 5); len(got) != 0 {
			t.Errorf("the pull request carries %q, want nothing", got)
		}
	})

	t.Run("an add that failed is retried by the next tick", func(t *testing.T) {
		host := labelHost("o/a", labelPR(5, ""))
		host.FailAddLabels = 1
		h := newLabelHarness(t, labelState(
			workItem("item-a", "o/a", PhaseRunning, runA, "forge-labels"),
		), map[string]*fake.Provider{"o/a": host}, &labelPRs{numbers: map[string]int{runA: 5}})

		h.step()
		wantCalls(t, host, "list", "ensure fugaro:running", "ensure spec:forge-labels",
			"add #5 run="+runA+" fugaro:running+spec:forge-labels")
		if len(h.log.lines) != 1 {
			t.Fatalf("the log carries %d lines, want one: %q", len(h.log.lines), h.log.lines)
		}

		// The second tick asks for nothing new to be created and writes the
		// add the host refused, which is how a host that comes back heals.
		h.step()
		wantCalls(t, host, "list", "ensure fugaro:running", "ensure spec:forge-labels",
			"add #5 run="+runA+" fugaro:running+spec:forge-labels",
			"list", "add #5 run="+runA+" fugaro:running+spec:forge-labels")
		if len(h.log.lines) != 1 {
			t.Errorf("the second tick logged %q, want the one line of the first", h.log.lines)
		}
		if got := prLabels(t, host, 5); !slices.Equal(got, []string{labelRunning, "spec:forge-labels"}) {
			t.Errorf("the pull request carries %q", got)
		}
	})
}

// TestSyncAfterRestartReadsFirst is F10: a daemon that restarted has an empty
// memory, reads what the host holds, and therefore writes nothing it already
// did — the whole of its loss is the read.
func TestSyncAfterRestartReadsFirst(t *testing.T) {
	// The pull request carries its status label and not yet its spec label, so
	// the daemon before the restart has a write to make and a memory worth
	// losing.
	host := labelHost("o/a", labelPR(5, "", labelRunning))
	hosts := map[string]*fake.Provider{"o/a": host}
	prs := &labelPRs{numbers: map[string]int{runA: 5}}
	s := labelState(workItem("item-a", "o/a", PhaseRunning, runA, "forge-labels"))

	before := newLabelHarness(t, s, hosts, prs)
	before.step()
	wantCalls(t, host, "list", "ensure spec:forge-labels", "add #5 run="+runA+" spec:forge-labels")

	// The restart: an empty memory over the same pull request. It reads the
	// listing, finds the label it created is there, and writes nothing — the
	// ensure it no longer remembers is not asked for again either, because the
	// name is not in the difference.
	after := newLabelHarness(t, s, hosts, prs)
	after.step()
	wantCalls(t, host,
		"list", "ensure spec:forge-labels", "add #5 run="+runA+" spec:forge-labels",
		"list",
	)
	if got := prLabels(t, host, 5); !slices.Equal(got, []string{labelRunning, "spec:forge-labels"}) {
		t.Errorf("the pull request carries %q after the restart", got)
	}
	if len(after.log.lines) != 0 {
		t.Errorf("the restarted daemon logged %q, want nothing", after.log.lines)
	}
}

package plan

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The tests below are CD3 of docs/contract-first-decoupling: the gain the read
// computes from a graph's recorded breaks, and the stubs that are still in the
// tree. Both are the read's own answers — the artifact carries neither — so
// what they are checked against is the graph and a checkout this test builds
// itself.
//
// They are written against CD2's v3Graph where it will serve: a task broken
// through a contract that depends on the same task, one integration node over
// both endpoints, a stub and a change request. The two shapes it does not have
// — a break that pays, and an edge onto the integration node — get a fixture of
// their own.

// paysGraph is a break that pays: two large tasks that no longer wait for each
// other, against a small contract and a small integration task. The contract
// depends on nothing, so the two sides run side by side; the integration task
// re-joins them (D1817).
const paysGraph = `{
  "schema_version": 3,
  "plan": "docs/plans/decoupled.md",
  "nodes": [
    {"id": "A", "title": "one side", "size": 5, "footprint": {"prediction": true, "paths": ["internal/a/**"]}},
    {"id": "B", "title": "the other side", "size": 5, "footprint": {"prediction": true, "paths": ["internal/b/**"]}},
    {"id": "C1", "title": "the contract", "kind": "contract", "size": 0.5, "footprint": {"prediction": true, "paths": ["internal/contract/types.go"]}},
    {"id": "I1", "title": "the integration", "kind": "integration", "footprint": {"prediction": true, "paths": ["internal/i/**"]}}
  ],
  "edges": [
    {"task": "B", "needs": "C1", "reason": "B is built against the contract"},
    {"task": "I1", "needs": "A", "reason": "the integration runs both sides"},
    {"task": "I1", "needs": "B", "reason": "the integration runs both sides"}
  ],
  "decoupling": [
    {"contract": "C1", "was": {"task": "B", "needs": "A"}, "integration": "I1", "reason": "B needed A's interface only"}
  ]
}`

// waitsGraph is a break with a task that waited on the integration node: X
// depends on I1, so before the pass X waited on both sides of the edge the
// contract broke.
const waitsGraph = `{
  "schema_version": 3,
  "plan": "docs/plans/decoupled.md",
  "nodes": [
    {"id": "A", "title": "the base", "size": 2, "footprint": {"prediction": true, "paths": ["internal/a/**"]}},
    {"id": "B", "title": "the dependent", "footprint": {"prediction": true, "paths": ["internal/b/**"]}},
    {"id": "X", "title": "what waits on the integration", "footprint": {"prediction": true, "paths": ["internal/x/**"]}},
    {"id": "C1", "title": "the contract", "kind": "contract", "footprint": {"prediction": true, "paths": ["internal/contract/types.go"]}},
    {"id": "I1", "title": "the integration", "kind": "integration", "footprint": {"prediction": true, "paths": ["internal/i/**"]}}
  ],
  "edges": [
    {"task": "B", "needs": "C1", "reason": "B is built against the contract"},
    {"task": "I1", "needs": "A", "reason": "the integration runs both sides"},
    {"task": "I1", "needs": "B", "reason": "the integration runs both sides"},
    {"task": "X", "needs": "I1", "reason": "X waits for both sides to be re-joined"}
  ],
  "decoupling": [
    {"contract": "C1", "was": {"task": "B", "needs": "A"}, "integration": "I1", "reason": "B needed A's interface only"}
  ]
}`

// gainOf reads a document the way the command does and returns its gain.
func gainOf(t *testing.T, body string) Gain {
	t.Helper()
	g := analyze(t, body)
	if g.Gain == nil {
		t.Fatal("the read reports no gain for a graph that recorded a break")
	}
	return *g.Gain
}

// TestGainReportsALossWhenTheBreakLengthensThePath: F5's case, and the reason
// the gain is weighed rather than counted. v3Graph's contract waits for the
// task it was broken against, so after the pass the work is a task longer: the
// break is a loss, and the read says so rather than reporting the two tasks it
// added as a gain.
func TestGainReportsALossWhenTheBreakLengthensThePath(t *testing.T) {
	g := analyze(t, v3Graph)
	if g.Gain == nil {
		t.Fatal("the read reports no gain for a graph that recorded a break")
	}
	gain := *g.Gain
	if want := (PathMeasure{Tasks: 2, Weight: 4}); gain.Before != want {
		t.Errorf("before = %+v, want %+v (B waits for A, weighed 3 + 1)", gain.Before, want)
	}
	if want := (PathMeasure{Tasks: 4, Weight: 5.5}); gain.After != want {
		t.Errorf("after = %+v, want %+v (I1 waits for B waits for C1 waits for A)", gain.After, want)
	}
	if gain.Improves {
		t.Error("the read reports a gain for a break that lengthens the weighted path")
	}
	// The before measure is the plan as it stood, so the task-count path it
	// reports is the one analyze.go answers for that plan -- and the after one
	// is the critical path of the document the read was handed.
	if got := g.CriticalPath.Length; gain.After.Tasks != got {
		t.Errorf("after.tasks = %d but the read's critical path is %d tasks; the two are one measure", gain.After.Tasks, got)
	}
}

// TestGainWeightedShrinksWhenSidesAreLarge: the same break, on a plan whose two
// sides are large against the contract and the integration task. The weighted
// path shrinks, which is the one question D1816 breaks an edge on, while the
// task-count path grows: the two measures are reported side by side because
// they answer differently.
func TestGainWeightedShrinksWhenSidesAreLarge(t *testing.T) {
	gain := gainOf(t, paysGraph)
	if want := (PathMeasure{Tasks: 2, Weight: 10}); gain.Before != want {
		t.Errorf("before = %+v, want %+v (B waits for A, 5 + 5)", gain.Before, want)
	}
	if want := (PathMeasure{Tasks: 3, Weight: 6.5}); gain.After != want {
		t.Errorf("after = %+v, want %+v (I1 waits for B waits for C1, 1 + 5 + 0.5)", gain.After, want)
	}
	if !gain.Improves {
		t.Error("the read reports no gain for a break that shortens the weighted path")
	}
	if gain.After.Tasks <= gain.Before.Tasks {
		t.Errorf("after.tasks = %d, before.tasks = %d; the fixture is here to show the count growing while the weight falls",
			gain.After.Tasks, gain.Before.Tasks)
	}
}

// TestGainIsComputedNotRead: the figures are a function of the graph and of
// nothing else. There is no key a planner could write them in -- CD2's
// TestGraphV3RefusesWhatItDoesNotKnow pins that `gain` is refused by name as an
// unknown field -- so what is checked here is the other half: the same document
// reads the same twice, and a size the planner wrote moves the weights.
func TestGainIsComputedNotRead(t *testing.T) {
	first := gainOf(t, v3Graph)
	again := gainOf(t, v3Graph)
	if first != again {
		t.Errorf("two reads of one document disagree: %+v and %+v", first, again)
	}

	// A is on the critical path of both plans, so the six it grows by is the
	// six both weights grow by -- and the harness itself, not a figure anyone
	// recorded beside the edge.
	bigger := gainOf(t, v3Doc(t, `{"id": "A", "title": "the base", "size": 3,`, `{"id": "A", "title": "the base", "size": 9,`))
	if got, want := bigger.Before.Weight, first.Before.Weight+6; got != want {
		t.Errorf("before.weight = %v after A's size grew by 6, want %v", got, want)
	}
	if got, want := bigger.After.Weight, first.After.Weight+6; got != want {
		t.Errorf("after.weight = %v after A's size grew by 6, want %v", got, want)
	}
	if bigger.Before.Tasks != first.Before.Tasks || bigger.After.Tasks != first.After.Tasks {
		t.Errorf("a size changed a task count: %+v and %+v", bigger, first)
	}
}

// TestAPathMeasureCanDescribeTwoChains: the two figures are measured
// independently — the longest chain in tasks, the heaviest one by size — so one
// `before` or `after` object can name two chains at once. Here the long chain is
// three tasks of weight 3 and the heavy one is two tasks of weight 10, and the
// measure reports both: a reader who took `weight` for the chain under `tasks`
// would be reading a chain nobody has.
func TestAPathMeasureCanDescribeTwoChains(t *testing.T) {
	weighed := func(n float64) *float64 { return &n }
	nodes := []Node{
		{ID: "A", Size: weighed(5)},
		{ID: "B", Size: weighed(5)},
		{ID: "C", Size: weighed(1)},
		{ID: "D", Size: weighed(1)},
		{ID: "E", Size: weighed(1)},
	}
	edges := []Edge{
		{Task: "B", Needs: "A"}, // the heavy chain: two tasks, weight 10
		{Task: "C", Needs: "D"}, // the long chain: three tasks, weight 3
		{Task: "D", Needs: "E"},
	}
	got, err := measurePath("graph.json", nodes, edges)
	if err != nil {
		t.Fatalf("measurePath: %v", err)
	}
	if want := (PathMeasure{Tasks: 3, Weight: 10}); got != want {
		t.Errorf("measurePath = %+v, want %+v: the longest chain and the heaviest one are not the same chain here", got, want)
	}
}

// TestGainCountsAddedTasks: the figure F11's cost is stated in. Every contract,
// integration and change-request node is a task the plan did not have before
// the pass, and the before graph is those nodes removed, so the count is also
// the difference between the two plans.
func TestGainCountsAddedTasks(t *testing.T) {
	gain := gainOf(t, v3Graph)
	if gain.AddedTasks != 3 {
		t.Errorf("added_tasks = %d, want 3 (C1, I1 and R1)", gain.AddedTasks)
	}
	if gain.Broken != 1 {
		t.Errorf("broken = %d, want the one recorded break", gain.Broken)
	}
	if gain.Kept != 0 {
		t.Errorf("kept = %d, want 0: v3Graph's only edges join a task to a node of the pass", gain.Kept)
	}

	// An edge the pass left alone is kept, and it is not one of the added
	// tasks' edges: E depends on D, both ordinary tasks.
	const kept = `{
	  "schema_version": 3,
	  "plan": "docs/plans/decoupled.md",
	  "nodes": [
	    {"id": "D", "title": "kept", "footprint": {"prediction": true, "paths": ["internal/d/**"]}},
	    {"id": "E", "title": "also kept", "footprint": {"prediction": true, "paths": ["internal/e/**"]}},
	    {"id": "B", "title": "the dependent", "footprint": {"prediction": true, "paths": ["internal/b/**"]}},
	    {"id": "C1", "title": "the contract", "kind": "contract", "footprint": {"prediction": true, "paths": ["internal/contract/types.go"]}},
	    {"id": "I1", "title": "the integration", "kind": "integration", "footprint": {"prediction": true, "paths": ["internal/i/**"]}}
	  ],
	  "edges": [
	    {"task": "E", "needs": "D", "reason": "ordinary work"},
	    {"task": "B", "needs": "C1", "reason": "B is built against the contract"},
	    {"task": "I1", "needs": "D", "reason": "the integration runs both sides"},
	    {"task": "I1", "needs": "B", "reason": "the integration runs both sides"}
	  ],
	  "decoupling": [
	    {"contract": "C1", "was": {"task": "B", "needs": "D"}, "integration": "I1", "reason": "B needed D's interface only"}
	  ]
	}`
	withKept := gainOf(t, kept)
	if withKept.Kept != 1 {
		t.Errorf("kept = %d, want 1 (E waits for D)", withKept.Kept)
	}
	if withKept.Broken != 1 || withKept.AddedTasks != 2 {
		t.Errorf("broken = %d and added_tasks = %d, want 1 and 2", withKept.Broken, withKept.AddedTasks)
	}
}

// TestGainCountsADependencyWrittenTwiceOnce: a dependency written twice is one
// dependency, and both figures are counted that way — the edges of the graph
// that were kept as much as the `was` edges the record names. Normalize does not
// de-duplicate either list and the third schema admits a repeated `was` entry,
// so the two are the document's own text and a count that took it at face value
// would state the pass's work in terms of how often a planner repeated itself.
func TestGainCountsADependencyWrittenTwiceOnce(t *testing.T) {
	const twice = `{
	  "schema_version": 3,
	  "plan": "docs/plans/decoupled.md",
	  "nodes": [
	    {"id": "A", "title": "the base", "footprint": {"prediction": true, "paths": ["internal/a/**"]}},
	    {"id": "B", "title": "the dependent", "footprint": {"prediction": true, "paths": ["internal/b/**"]}},
	    {"id": "D", "title": "kept", "footprint": {"prediction": true, "paths": ["internal/d/**"]}},
	    {"id": "E", "title": "also kept", "footprint": {"prediction": true, "paths": ["internal/e/**"]}},
	    {"id": "C1", "title": "the contract", "kind": "contract", "footprint": {"prediction": true, "paths": ["internal/contract/types.go"]}},
	    {"id": "I1", "title": "the integration", "kind": "integration", "footprint": {"prediction": true, "paths": ["internal/i/**"]}}
	  ],
	  "edges": [
	    {"task": "E", "needs": "D", "reason": "ordinary work"},
	    {"task": "E", "needs": "D", "reason": "ordinary work"},
	    {"task": "B", "needs": "C1", "reason": "B is built against the contract"},
	    {"task": "I1", "needs": "A", "reason": "the integration runs both sides"},
	    {"task": "I1", "needs": "B", "reason": "the integration runs both sides"}
	  ],
	  "decoupling": [
	    {"contract": "C1", "was": {"task": "B", "needs": "A"}, "integration": "I1", "reason": "B needed A's interface only"},
	    {"contract": "C1", "was": {"task": "B", "needs": "A"}, "integration": "I1", "reason": "B needed A's interface only"}
	  ]
	}`
	gain := gainOf(t, twice)
	if gain.Broken != 1 {
		t.Errorf("broken = %d, want 1: the record names the one edge twice", gain.Broken)
	}
	if gain.Kept != 1 {
		t.Errorf("kept = %d, want 1: E waits for D once, however many times the edge is written", gain.Kept)
	}
	// The measure is a property of the graph rather than of its text, so the
	// repeated entries do not lengthen the path either.
	if want := (PathMeasure{Tasks: 2, Weight: 2}); gain.Before != want {
		t.Errorf("before = %+v, want %+v: B waits for A", gain.Before, want)
	}
}

// TestGainRewritesEdgesOnTheIntegrationNode: a task that waited on the
// integration node waited on both sides of the break, so the before graph
// carries an edge onto each endpoint rather than onto the node the pass added.
// waitsGraph's X waits on I1, and without the rewrite the before plan would be
// the two tasks of B -> A and nothing else.
func TestGainRewritesEdgesOnTheIntegrationNode(t *testing.T) {
	gain := gainOf(t, waitsGraph)
	// X -> B -> A is three tasks and 1 + 1 + 2 of weight; X -> A on its own
	// would be two and 3.
	if want := (PathMeasure{Tasks: 3, Weight: 4}); gain.Before != want {
		t.Errorf("before = %+v, want %+v: X waited on both sides of the break", gain.Before, want)
	}
	if gain.Before.Tasks == 2 {
		t.Error("before.tasks = 2: the edge X -> I1 was dropped rather than rewritten onto the integration node's endpoints")
	}
}

// TestGainRefusesABeforeGraphWithACycle: the before graph is a reconstruction,
// and a document whose recorded breaks restore an edge in both directions
// closes a cycle in it. The two records are individually valid -- neither edge
// is still in the graph, and the one integration node covers both -- so the
// refusal is the read's own, and a walk that followed the cycle would hang
// rather than fail.
func TestGainRefusesABeforeGraphWithACycle(t *testing.T) {
	const bothWays = `{
	  "schema_version": 3,
	  "plan": "docs/plans/decoupled.md",
	  "nodes": [
	    {"id": "A", "title": "one side", "footprint": {"prediction": true, "paths": ["internal/a/**"]}},
	    {"id": "B", "title": "the other side", "footprint": {"prediction": true, "paths": ["internal/b/**"]}},
	    {"id": "C1", "title": "the contract", "kind": "contract", "footprint": {"prediction": true, "paths": ["internal/contract/types.go"]}},
	    {"id": "I1", "title": "the integration", "kind": "integration", "footprint": {"prediction": true, "paths": ["internal/i/**"]}}
	  ],
	  "edges": [
	    {"task": "I1", "needs": "A", "reason": "the integration runs both sides"},
	    {"task": "I1", "needs": "B", "reason": "the integration runs both sides"}
	  ],
	  "decoupling": [
	    {"contract": "C1", "was": {"task": "B", "needs": "A"}, "integration": "I1", "reason": "B needed A's interface only"},
	    {"contract": "C1", "was": {"task": "A", "needs": "B"}, "integration": "I1", "reason": "A needed B's interface only"}
	  ]
	}`
	doc := parseDoc(t, bothWays)
	if _, err := Analyze("graph.json", doc, nil); err == nil {
		t.Fatal("the read measured a before graph with a cycle in it")
	} else if !strings.Contains(err.Error(), "restore a cycle") {
		t.Errorf("Analyze's error = %v, want the before graph's cycle named", err)
	}
}

// TestGainRefusesABreakRecordedOntoANodeOfThePass: the third schema checks that
// a recorded `was` edge names two nodes of the graph and does not carry it in
// `edges` -- it does not check that they are *tasks*, because the pass's own
// nodes are ordinary nodes of the document. So a break recorded onto a contract
// reaches the read, and the before graph has nowhere to restore it: the pass's
// nodes are exactly what comes out of the reconstruction. Dropping the edge
// quietly would measure a plan nobody wrote.
func TestGainRefusesABreakRecordedOntoANodeOfThePass(t *testing.T) {
	const recordedOntoTheContract = `{
  "schema_version": 3,
  "plan": "docs/plans/decoupled.md",
  "nodes": [
    {"id": "B", "title": "the dependent", "footprint": {"prediction": true, "paths": ["internal/b/**"]}},
    {"id": "C1", "title": "the contract", "kind": "contract", "footprint": {"prediction": true, "paths": ["internal/contract/types.go"]}},
    {"id": "I1", "title": "the integration", "kind": "integration", "footprint": {"prediction": true, "paths": ["internal/i/**"]}}
  ],
  "edges": [
    {"task": "I1", "needs": "B", "reason": "the integration runs both sides"},
    {"task": "I1", "needs": "C1", "reason": "the integration runs both sides"}
  ],
  "decoupling": [
    {"contract": "C1", "was": {"task": "B", "needs": "C1"}, "integration": "I1", "reason": "B was built against the file C1 now names"}
  ]
}`
	// Parse accepts it: both endpoints are nodes, and the edge is not carried.
	doc := parseDoc(t, recordedOntoTheContract)
	if _, err := Analyze("graph.json", doc, nil); err == nil {
		t.Fatal("the read measured a before graph with a break recorded onto a node of the pass")
	} else if !strings.Contains(err.Error(), "one of them is a node of the pass") {
		t.Errorf("Analyze's error = %v, want the recorded edge named as untaskable", err)
	}
}

// TestGraphWithoutDecouplingHasNoGainNoise: the read's new fields are additive,
// so a graph that ran no pass reads exactly as it did. There is no gain to
// report -- a zero would be noise where nothing was broken -- and no contract
// node to hang a stub list on.
func TestGraphWithoutDecouplingHasNoGainNoise(t *testing.T) {
	for _, name := range []string{"graph.json", "graph-v2.json"} {
		g := analyzeFixture(t, name)
		if g.Gain != nil {
			t.Errorf("%s: the read reports a gain %+v for a plan that broke no edge", name, *g.Gain)
		}
		body, err := json.Marshal(g)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		for _, key := range []string{"gain", "stubs_present"} {
			if strings.Contains(string(body), `"`+key+`"`) {
				t.Errorf("%s: the read of a graph with no pass in it carries %s: %s", name, key, body)
			}
		}
	}
	// And a v3 graph that kept every edge is the same absence: the schema was
	// bumped, the plan ran no pass.
	const keptEveryEdge = `{"schema_version": 3, "plan": "p.md", "nodes": [
		{"id": "A", "title": "the base", "footprint": {"prediction": true, "paths": ["internal/a/**"]}},
		{"id": "B", "title": "the dependent", "footprint": {"prediction": true, "paths": ["internal/b/**"]}}],
		"edges": [{"task": "B", "needs": "A", "reason": "ordinary work"}]}`
	if g := analyze(t, keptEveryEdge); g.Gain != nil {
		t.Errorf("a v3 graph that broke no edge reports the gain %+v", *g.Gain)
	}
}

// stubGraph is v3Graph with a second stub, one that is not in the tree: the two
// are what "which stubs remain" is a question about.
func stubGraph(t *testing.T) string {
	t.Helper()
	return v3Doc(t,
		`"stubs": ["internal/contract/stub.go"]`,
		`"stubs": ["internal/contract/stub.go", "internal/contract/gone.go"]`,
		`"paths": ["internal/contract/types.go", "internal/contract/stub.go"]`,
		`"paths": ["internal/contract/types.go", "internal/contract/stub.go", "internal/contract/gone.go"]`)
}

// checkout is a working directory this test owns: a tree with a `.git` entry,
// which is what makes it the checkout a stub is resolved against.
//
// It answers with the same tree however many times one test asks — the marker
// is what says the checkout is one a test made, so a test that writes a stub
// into it and then reads keeps reading its own tree — and a test that has none
// yet gets one. That second half is what keeps the package's tests off the
// repository they happen to be run in: the read resolves a graph's stubs
// against the checkout it stands in, so a test that reads a graph without
// saying which tree it is read in would pass here and fail wherever there is no
// `.git` above the test's directory (CLAUDE.md, "hermetic tests").
const checkoutMarker = "fugaro-test-checkout"

func checkout(t *testing.T) string {
	t.Helper()
	if root := checkoutRoot("."); root != "" {
		if _, err := os.Lstat(filepath.Join(root, ".git", checkoutMarker)); err == nil {
			return root
		}
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", checkoutMarker), []byte("a checkout the plan tests own\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	return dir
}

// written is a file of the checkout at a repository-relative path.
func written(t *testing.T, root, repoPath string) string {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(repoPath))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("package contract\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return full
}

// stubsOf reads the document the way the command does and answers what the
// contract node in it still has in the tree.
func stubsOf(t *testing.T, body string) []string {
	t.Helper()
	g := analyze(t, body)
	for _, n := range g.Nodes {
		if n.ID == "C1" {
			return n.StubsPresent
		}
	}
	t.Fatal("the read carries no C1 node")
	return nil
}

// TestStubsPresentListsOnlyExistingFiles: the contract names two stubs and the
// tree has one of them, so one is reported. A stub that is not there is the
// answer the integration task exists for, and it is an absence rather than a
// path with a mark beside it.
func TestStubsPresentListsOnlyExistingFiles(t *testing.T) {
	root := checkout(t)
	written(t, root, "internal/contract/stub.go")
	if got, want := stubsOf(t, stubGraph(t)), []string{"internal/contract/stub.go"}; !reflect.DeepEqual(got, want) {
		t.Errorf("stubs_present = %v, want %v", got, want)
	}

	// And with both of them there, both are listed, in the contract's own order.
	written(t, root, "internal/contract/gone.go")
	if got, want := stubsOf(t, stubGraph(t)), []string{"internal/contract/stub.go", "internal/contract/gone.go"}; !reflect.DeepEqual(got, want) {
		t.Errorf("stubs_present = %v, want %v", got, want)
	}

	// A contract whose stubs are all gone carries no key at all: `stubs` beside
	// it says it had some, so the absence reads as "none remain".
	if err := os.Remove(filepath.Join(root, "internal/contract/stub.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "internal/contract/gone.go")); err != nil {
		t.Fatal(err)
	}
	if got := stubsOf(t, stubGraph(t)); len(got) != 0 {
		t.Errorf("stubs_present = %v with neither stub in the tree, want none", got)
	}
}

// TestStubsPresentDoesNotFollowSymlinksOut: a stub whose file is a symlink out
// of the checkout is not a file of this checkout, however exactly the path
// names one. The root refuses to follow it, so nothing is reported -- and the
// test would pass just as well if the read followed the link and found a file,
// which is the mistake this pins against.
func TestStubsPresentDoesNotFollowSymlinksOut(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "elsewhere.go")
	if err := os.WriteFile(outside, []byte("package elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := checkout(t)
	written(t, root, "internal/contract/stub.go")
	if err := os.Symlink(outside, filepath.Join(root, "internal/contract/gone.go")); err != nil {
		t.Fatal(err)
	}
	if got, want := stubsOf(t, stubGraph(t)), []string{"internal/contract/stub.go"}; !reflect.DeepEqual(got, want) {
		t.Errorf("stubs_present = %v, want %v: a symlink out of the checkout is not a stub of it", got, want)
	}
}

// TestStubsPresentDoesNotFollowASymlinkedDirectory: the same rule one element
// up. A directory the path has to pass through is a symlink to a tree outside
// the checkout, so the file it leads to is not one of the checkout's — the
// whole path is unreachable without leaving the root.
func TestStubsPresentDoesNotFollowASymlinkedDirectory(t *testing.T) {
	away := t.TempDir()
	written(t, away, "stub.go")
	written(t, away, "gone.go")
	root := checkout(t)
	if err := os.MkdirAll(filepath.Join(root, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(away, filepath.Join(root, "internal/contract")); err != nil {
		t.Fatal(err)
	}
	if got := stubsOf(t, stubGraph(t)); len(got) != 0 {
		t.Errorf("stubs_present = %v, want none: internal/contract is a symlink out of the checkout", got)
	}
}

// TestStubsPresentIsRefusedOutsideTheCheckout: a graph that names stubs and was
// read from another tree is refused. Every checkout of a repository has the
// same stub paths, so a read that resolved them against whatever tree it stood
// in would answer a question about a plan it was not given.
func TestStubsPresentIsRefusedOutsideTheCheckout(t *testing.T) {
	checkout(t)
	elsewhere := t.TempDir()
	artifact := filepath.Join(elsewhere, "decoupled.graph.json")
	doc := parseDoc(t, stubGraph(t))
	if _, err := Analyze(artifact, doc, nil); err == nil {
		t.Fatal("a graph naming stubs was read from outside the working directory's checkout")
	} else if !strings.Contains(err.Error(), "outside the working directory's checkout") {
		t.Errorf("Analyze's error = %v, want the checkout it was read in named", err)
	}
	// And the same document read where it belongs is read.
	if _, err := Analyze("docs/plans/decoupled.graph.json", doc, nil); err != nil {
		t.Errorf("a graph naming stubs was refused inside the checkout it is read in: %v", err)
	}
}

// TestStubsPresentDoesNotReadANestedCheckoutsParent: the tree the stubs are
// resolved in is the artifact's own git top level, not the one the working
// directory happens to sit in. A plan under a nested checkout is a plan of that
// tree, and a stub of the same path in the checkout above it is a different
// file: the parent's answer would be "this stub is still there" about a
// repository the plan does not describe.
func TestStubsPresentDoesNotReadANestedCheckoutsParent(t *testing.T) {
	outer := checkout(t)
	written(t, outer, "internal/contract/stub.go")
	nested := filepath.Join(outer, "nested")
	if err := os.MkdirAll(filepath.Join(nested, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(nested, "decoupled.graph.json")
	doc := parseDoc(t, stubGraph(t))
	if _, err := Analyze(artifact, doc, nil); err == nil {
		t.Fatal("a graph read from a nested checkout answered about the checkout above it")
	} else if !strings.Contains(err.Error(), "outside the working directory's checkout") {
		t.Errorf("Analyze's error = %v, want the artifact's own checkout named", err)
	}
}

// TestBeforeGraphEdgesAreBounded: the rewrite is a product of two sides that
// both come from the file — every edge onto an integration node against every
// endpoint that node re-joins — so a graph well inside the artifact's own cap
// can ask for gigabytes of them. A graph past the bound is refused, with the
// figure named, rather than read slowly; one under it is measured.
func TestBeforeGraphEdgesAreBounded(t *testing.T) {
	// builder builds a graph of `breaks` broken edges over one integration node
	// and `waiters` tasks that wait on it, so the before graph asks for
	// 2*breaks*waiters edges plus the restores.
	build := func(breaks, waiters int) string {
		var b strings.Builder
		b.WriteString(`{"schema_version":3,"plan":"p.md","nodes":[` +
			`{"id":"C1","title":"the contract","kind":"contract","footprint":{"prediction":true,"paths":["internal/contract/types.go"]}},` +
			`{"id":"I1","title":"the integration","kind":"integration","footprint":{"prediction":true,"paths":["internal/i/**"]}}`)
		for i := 0; i < breaks; i++ {
			for _, side := range []string{"P", "Q"} {
				fmt.Fprintf(&b, `,{"id":"%s%04d","title":"a side of a break","footprint":{"prediction":true,"paths":["internal/s/%s%04d/**"]}}`, side, i, side, i)
			}
		}
		for i := 0; i < waiters; i++ {
			fmt.Fprintf(&b, `,{"id":"W%04d","title":"a task that waits on the integration","footprint":{"prediction":true,"paths":["internal/w%04d/**"]}}`, i, i)
		}
		b.WriteString(`],"edges":[`)
		first := true
		edge := func(task, needs string) {
			if !first {
				b.WriteString(",")
			}
			first = false
			fmt.Fprintf(&b, `{"task":%q,"needs":%q,"reason":"the integration covers what it re-joins"}`, task, needs)
		}
		for i := 0; i < breaks; i++ {
			edge("I1", fmt.Sprintf("P%04d", i))
			edge("I1", fmt.Sprintf("Q%04d", i))
		}
		for i := 0; i < waiters; i++ {
			edge(fmt.Sprintf("W%04d", i), "I1")
		}
		b.WriteString(`],"decoupling":[`)
		for i := 0; i < breaks; i++ {
			if i > 0 {
				b.WriteString(",")
			}
			fmt.Fprintf(&b, `{"contract":"C1","was":{"task":"P%04d","needs":"Q%04d"},"integration":"I1","reason":"each side needed the other's interface only"}`, i, i)
		}
		b.WriteString(`]}`)
		return b.String()
	}
	// Under the bound: 20,000 rewritten edges. The graph is measured, which is
	// what the pass is read for.
	small := parseDoc(t, build(100, 100))
	if _, err := Analyze("graph.json", small, nil); err != nil {
		t.Fatalf("a before graph under the bound was refused: %v", err)
	}
	// Past it: the same shape at 320,000 rewritten edges, from a file of a few
	// hundred kilobytes. The refusal names the figure rather than the memory.
	if _, err := Analyze("graph.json", parseDoc(t, build(400, 400)), nil); err == nil {
		t.Fatal("the read built a before graph past the bound")
	} else if !strings.Contains(err.Error(), "more than 262144 edges in the before graph") {
		t.Errorf("Analyze's error = %v, want the bound named", err)
	}
}

// TestStubsPresentIsRefusedWithNoCheckout: the other half of "the tree is the
// checkout the plan is read in" — a working directory with no checkout above it
// has no tree to resolve a stub against, and a read that fell back on the
// directory it happened to stand in would answer about a repository the plan
// does not describe. The walk looks for a `.git` entry up to the filesystem
// root, so the only place the absence is a fact rather than an accident is the
// root itself; anywhere else the answer is whatever the machine has above it,
// and the test says so instead of asserting something it did not set up.
func TestStubsPresentIsRefusedWithNoCheckout(t *testing.T) {
	t.Chdir("/")
	if root := checkoutRoot("."); root != "" {
		t.Skipf("this filesystem has a checkout at its root (%s), so there is no directory without one above it to read from", root)
	}
	doc := parseDoc(t, stubGraph(t))
	if _, err := Analyze("docs/plans/decoupled.graph.json", doc, nil); err == nil {
		t.Fatal("a graph naming stubs was read with the working directory in no checkout")
	} else if !strings.Contains(err.Error(), "in no git checkout") {
		t.Errorf("Analyze's error = %v, want the missing checkout named", err)
	}
}

// TestPlanStatusReadsAV3Graph: the status read joins the graph through the same
// Parse and Analyze the graph read uses, so a v3 artifact is one it reads
// unchanged -- the pass's nodes are tasks like any other and its gathered
// fields are not its own. Nothing here is about the gain: a screen that draws
// states draws them over a decoupled plan as readily as over one that was never
// broken up.
func TestPlanStatusReadsAV3Graph(t *testing.T) {
	checkout(t)
	doc := parseDoc(t, stubGraph(t))
	status, err := State("decoupled", doc, nil, StateOptions{Artifact: "docs/plans/decoupled.graph.json", Repo: "acme/app"})
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if status.Tasks != 5 {
		t.Errorf("tasks = %d, want the five nodes of the document", status.Tasks)
	}
	states := map[string]StateNode{}
	for _, n := range status.Nodes {
		states[n.ID] = n
	}
	if got := states["B"].State; got != StateBlocked {
		t.Errorf("B is %q, want blocked: the contract it waits for is not done", got)
	}
	if got := states["A"].State; got != StateReady {
		t.Errorf("A is %q, want ready: nothing it needs is outstanding", got)
	}
}

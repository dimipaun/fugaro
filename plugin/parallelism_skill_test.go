package plugin_test

// The parallelism skill's graph example, held to the CLI's own schema (console
// v2 plan V2.8.1, design D25/D28 and the graph section of
// docs/design/desktop-console-v2.md).
//
// The skill tells a planner what to write into `<plan>.graph.json`, and
// `fugaro plan graph` refuses a document it does not understand rather than
// rendering part of it. That makes a renamed json tag in
// internal/plan/document.go a way to leave the skill instructing planners to
// write a document this build refuses, with every other test still green — the
// drift D36 names as the risk. Parsing the skill's own example with the real
// parser is the tie: `plan.Parse` runs with DisallowUnknownFields and refuses a
// document that is missing what it requires, so every key the skill spells is
// either the tag the document type carries or a refusal.
//
// The example is read out of SKILL.md rather than restated here on purpose: a
// copy in Go would be a second spelling of the schema, which is the thing being
// guarded against.

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/plan"
)

const (
	parallelismSkillPath = "skills/parallelism/SKILL.md"
	workingSkillPath     = "skills/working/SKILL.md"
)

// TestWorkingSkillPointsAtJobMax: the working skill's decide-first list is
// where a planner looks before launching anything, so the job max pointer there
// is the only thing that carries the definition to an agent that never opens
// the parallelism skill. routing's own pointer is pinned the same way
// (TestLaunchPointsAtRoutingSkill); without this one an edit could drop the
// pointer and leave every other test green.
func TestWorkingSkillPointsAtJobMax(t *testing.T) {
	skill, err := os.ReadFile(workingSkillPath)
	if err != nil {
		t.Fatal(err)
	}
	if want := "the `parallelism` skill, which defines job max"; !strings.Contains(string(skill), want) {
		t.Errorf("%s never says %q", workingSkillPath, want)
	}
}

// TestParallelismSkillJobMaxExceptionNeedsTheOptIn: the one-yes-per-batch rule
// is what a launch spends money under, so the single exception the skill
// carries must name both its conditions — the owner's opt-in, and dependencies
// merged rather than merely finished. "Ready" is not that: the plan's own
// `ready` state counts an unknown merge state as done, which a launch on the
// strength of it would read as merged code that is not there (D730).
func TestParallelismSkillJobMaxExceptionNeedsTheOptIn(t *testing.T) {
	skill, err := os.ReadFile(parallelismSkillPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(skill)
	for _, want := range []string{
		"The only exception is `/jobmax`",
		"the tasks whose dependencies are all merged",
		"turned on the project's opt-in",
		"with the opt-in off, this rule stands",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("%s does not say %q, so the exception reads as an unconditional one", parallelismSkillPath, want)
		}
	}
}

// TestParallelismSkillGraphExampleIsReadable: the document the skill shows a
// planner is a document this build reads, and it exercises the fields the
// skill's prose is about — a footprint that predicts paths, and an edge that
// says why. The schema version the example is written in is the other test's
// (TestParallelismSkillGraphExampleIsReadableAtV3), because a document is
// readable at any version this build accepts and this one is about the shape
// every version shares.
func TestParallelismSkillGraphExampleIsReadable(t *testing.T) {
	skill, err := os.ReadFile(parallelismSkillPath)
	if err != nil {
		t.Fatal(err)
	}
	example := firstJSONBlock(t, string(skill))
	doc, err := plan.Parse(parallelismSkillPath, []byte(example))
	if err != nil {
		t.Fatalf("the graph example in %s is not a document this build reads: %v", parallelismSkillPath, err)
	}
	if doc.Plan == "" {
		t.Error("the example names no plan")
	}
	if len(doc.Nodes) != 5 || len(doc.Edges) != 3 {
		t.Fatalf("the example carries %d nodes and %d edges, want the five and the three it shows: the two tasks, the contract, the integration task and the change request, with the edges that join them — the broken edge is gone from `edges`, and the contract waits for nothing, which is what the break turns on", len(doc.Nodes), len(doc.Edges))
	}
	for _, n := range doc.Nodes {
		if n.Title == "" {
			t.Errorf("node %q has no title", n.ID)
		}
		if !n.Footprint.Prediction {
			t.Errorf("node %q's footprint is not labelled a prediction", n.ID)
		}
		if len(n.Footprint.Paths) == 0 {
			t.Errorf("node %q predicts no path, so the example shows nothing of the footprint", n.ID)
		}
	}
	if reason := doc.Edges[0].Reason; reason == "" {
		t.Error("the example's edge carries no reason")
	}
}

// TestParallelismSkillGraphExampleIsReadableAtV3: the example is a schema-3
// document — the version this build writes — and it uses the fields the third
// schema adds, so a planner copying it writes a graph whose decoupling pass the
// read can compute. The readable test above cannot say either: Parse reads every
// version from 1 to 3, so the example it reads could be a v2 one carrying no
// `kind`, no `stubs` and no record of the break, and that is exactly the state
// this test fails in while the skill's text still writes schema 2.
//
// The fields are read off the parsed document rather than matched as text: the
// parser is the thing that says which key is which, and a skill whose example
// spells `stups` is one the read refuses rather than one a string search finds.
func TestParallelismSkillGraphExampleIsReadableAtV3(t *testing.T) {
	skill, err := os.ReadFile(parallelismSkillPath)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := plan.Parse(parallelismSkillPath, []byte(firstJSONBlock(t, string(skill))))
	if err != nil {
		t.Fatalf("the graph example in %s is not a document this build reads: %v", parallelismSkillPath, err)
	}
	if doc.SchemaVersion != plan.SchemaVersion {
		t.Fatalf("the example is schema version %d and this build writes %d; the schema move updates the skill's example and this line in one change", doc.SchemaVersion, plan.SchemaVersion)
	}

	kinds := make(map[string]string, len(doc.Nodes))
	for _, n := range doc.Nodes {
		kinds[n.ID] = n.Kind
	}
	contract := graphNodeOfKind(t, doc, plan.KindContract)
	if contract.Size == nil {
		t.Errorf("the example's contract %q carries no size: a contract's weight against the pair it separates is one of the two numbers the gain is computed from", contract.ID)
	}
	if len(contract.Stubs) == 0 {
		t.Errorf("the example's contract %q names no stub: the integration task deletes the files `stubs` lists, and an example that lists none shows neither half", contract.ID)
	}
	for _, stub := range contract.Stubs {
		if !slices.Contains(contract.Footprint.Paths, stub) {
			t.Errorf("the example's contract %q names the stub %q and its footprint does not name that file, which the read refuses: the example has to show the stub named in both places", contract.ID, stub)
		}
	}

	if len(doc.Decoupling) == 0 {
		t.Fatal("the example records no decoupling entry: `decoupling` is what says which edge the contract broke, and every other part of the pass is read from it")
	}
	for _, d := range doc.Decoupling {
		if kinds[d.Contract] != plan.KindContract {
			t.Errorf("the example records the contract %q, whose kind is %q", d.Contract, kinds[d.Contract])
		}
		if kinds[d.Integration] != plan.KindIntegration {
			t.Errorf("the example records the integration node %q, whose kind is %q", d.Integration, kinds[d.Integration])
		}
		if graphCarriesEdge(doc, d.Was.Task, d.Was.Needs) {
			t.Errorf("the example records the edge %s -> %s as broken and still carries it: an edge is broken or it is there, never both, and the read refuses a document that says both", d.Was.Task, d.Was.Needs)
		}
		if !graphCarriesEdge(doc, d.Integration, d.Was.Task) || !graphCarriesEdge(doc, d.Integration, d.Was.Needs) {
			t.Errorf("the example's integration node %q does not depend on both sides of the edge %s -> %s it covers, which the read refuses", d.Integration, d.Was.Task, d.Was.Needs)
		}
	}

	if len(doc.ContractChanges) == 0 {
		t.Fatal("the example records no change request: `contract_changes` is where a change against a merged contract is recorded, and the skill teaches it")
	}
	for _, c := range doc.ContractChanges {
		if kinds[c.Change] != plan.KindContractChange {
			t.Errorf("the example records the change %q, whose kind is %q", c.Change, kinds[c.Change])
		}
		if kinds[c.Contract] != plan.KindContract {
			t.Errorf("the example's change %q is against %q, whose kind is %q", c.Change, c.Contract, kinds[c.Contract])
		}
		if _, ok := kinds[c.RaisedBy]; !ok {
			t.Errorf("the example's change %q is raised by %q, which is not one of its nodes", c.Change, c.RaisedBy)
		}
		if len(c.Affects) == 0 {
			t.Errorf("the example's change %q affects no task: the tasks a change reaches are what an unlaunched dependent's edge and a launched one's follow-up are read from", c.Change)
		}
	}

	// The read's verdict on the example, which is what the example has to
	// satisfy: the section this one sits in says a break the read reports as no
	// gain is one to undo, so an example the read weighs as no gain teaches the
	// pass by a plan the skill itself tells a planner to throw away — and the
	// planner following it writes the contract as a task waiting on one side,
	// which leaves the dependent waiting on that side through the contract.
	//
	// The figures are read off the read rather than pinned here (D1816 — the
	// arithmetic is the CLI's): the verdict is the property, and a size or a
	// footprint that drifts until the weight no longer shrinks is exactly what
	// this catches.
	graph, err := plan.Analyze(parallelismSkillPath, doc, nil)
	if err != nil {
		t.Fatalf("the read refused the example in %s: %v", parallelismSkillPath, err)
	}
	if graph.Gain == nil {
		t.Fatal("the read reports no gain for the example, so it weighed no break: `gain` is what says whether the pass paid, and it is absent for a graph that broke no edge")
	}
	added := 0
	for _, n := range doc.Nodes {
		if n.Kind != "" {
			added++
		}
	}
	if graph.Gain.Broken != len(doc.Decoupling) || graph.Gain.AddedTasks != added {
		t.Errorf("the read reports the example as %d broken edge(s) and %d added task(s), want the %d its `decoupling` record names and the %d nodes the pass added: the verdict below is read off the same figures", graph.Gain.Broken, graph.Gain.AddedTasks, len(doc.Decoupling), added)
	}
	if !graph.Gain.Improves {
		t.Fatalf("`fugaro plan graph` reports the example's break as no gain: the weighted path is %v after against %v before, and the section's rule is that such a break is one to undo. The example has to be a break that pays — the contract waiting for neither side, and the pair it separates weighing more than the contract and its integration task", graph.Gain.After.Weight, graph.Gain.Before.Weight)
	}
}

// graphNodeOfKind returns the example's first node of kind, failing when it
// carries none.
func graphNodeOfKind(t *testing.T, doc plan.Document, kind string) plan.Node {
	t.Helper()
	for _, n := range doc.Nodes {
		if n.Kind == kind {
			return n
		}
	}
	t.Fatalf("the example carries no node of kind %q: the skill's example is what a planner copies, so every kind its text teaches has to be in it", kind)
	return plan.Node{}
}

// graphCarriesEdge reports whether the document carries task -> needs.
func graphCarriesEdge(doc plan.Document, task, needs string) bool {
	for _, e := range doc.Edges {
		if e.Task == task && e.Needs == needs {
			return true
		}
	}
	return false
}

// TestParallelismSkillDefinesJobMax: job max is defined here, and the three
// techniques that keep a graph launchable are named beside it (D729). The
// definition is a phrase because it is the thing a planner has to carry away:
// the measure is finished, reviewed runs, and the only true order between them
// is a real dependency. A skill that drifts to tokens spent, or that drops one
// of the techniques, would leave `/jobmax` launching a graph nobody kept wide.
func TestParallelismSkillDefinesJobMax(t *testing.T) {
	skill, err := os.ReadFile(parallelismSkillPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(skill)
	const definition = "the most completed, reviewed runs in parallel, not tokens spent; only real dependencies order runs"
	if !strings.Contains(text, definition) {
		t.Errorf("%s does not define job max as %q", parallelismSkillPath, definition)
	}
	for _, technique := range []string{
		"contracts first",
		"stubs and fakes",
		"wide and shallow",
	} {
		if !strings.Contains(text, technique) {
			t.Errorf("%s does not name the planning technique %q", parallelismSkillPath, technique)
		}
	}
}

// firstJSONBlock returns the body of the first ```json fence in text.
func firstJSONBlock(t *testing.T, text string) string {
	t.Helper()
	lines := strings.Split(text, "\n")
	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "```json" {
			start = i + 1
			break
		}
	}
	if start < 0 {
		t.Fatalf("%s carries no ```json block", parallelismSkillPath)
	}
	for i := start; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "```" {
			return strings.Join(lines[start:i], "\n")
		}
	}
	t.Fatalf("%s's ```json block is never closed", parallelismSkillPath)
	return ""
}

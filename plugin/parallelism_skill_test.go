package plugin_test

// The parallelism skill's graph example, held to the CLI's own schema (console
// v2 plan V2.8.1, design D25/D28 and the graph section of
// docs/desktop-console-v2/spec.md).
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
	"github.com/dimipaun/fugaro/internal/tier"
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
//
// The second pointer is the daemon's (R6, D2703): the Monitor section is where
// a session goes to launch, watch and cancel, and with the project's daemon
// live none of that is its to do — the work is queued and the `orchestrator`
// skill governs it. A section that lists the commands without saying so is the
// one an agent reads as a licence to keep launching while two orchestrators
// work the same graph.
func TestWorkingSkillPointsAtJobMax(t *testing.T) {
	skill, err := os.ReadFile(workingSkillPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(skill)
	if want := "the `parallelism` skill, which defines job max"; !strings.Contains(text, want) {
		t.Errorf("%s never says %q", workingSkillPath, want)
	}
	monitor := skillSection(t, text, "Monitor")
	for _, want := range []string{
		"the `orchestrator` skill governs launching",
		"never launch, sweep or watch runs yourself",
	} {
		if !strings.Contains(monitor, want) {
			t.Errorf("%s's Monitor section never says %q, so a session is left driving runs the daemon is already driving:\n%s", workingSkillPath, want, monitor)
		}
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
//
// The stub technique is pinned by its clause as well as its name: the skill
// bolds the name, so the two are one phrase to a reader and two strings here,
// and a skill that keeps "stubs and fakes" while dropping "so neither side
// waits" has kept a word for stubbing and lost the reason the edge goes away —
// a stub written to be waited on leaves the dependent waiting on the side the
// contract was meant to free.
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
		"so neither side waits",
		"wide and shallow",
	} {
		if !strings.Contains(text, technique) {
			t.Errorf("%s does not name the planning technique %q", parallelismSkillPath, technique)
		}
	}
}

// TestParallelismExampleGraphReads (R6): the example the skill shows carries
// `tier` beside `size` and is still a document this build reads, which is what
// S0 added the field for (D2711). The two keys are read off the parsed document
// rather than matched as text — the parser is what says which key is which, and
// a tier outside the vocabulary is a refusal rather than something a string
// search finds — and the four names are read off internal/tier rather than
// typed twice, so a vocabulary that grows or renames fails here as well as in
// the read.
//
// The example shows both, which is the point of the line beside it: a node with
// no `tier` is a task the planner did not size, so an example in which every
// node carries one would teach the field as compulsory, and a plan written from
// it would invent a tier for every task rather than leave the ones it cannot
// size alone.
func TestParallelismExampleGraphReads(t *testing.T) {
	skill, err := os.ReadFile(parallelismSkillPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(skill)
	doc, err := plan.Parse(parallelismSkillPath, []byte(firstJSONBlock(t, text)))
	if err != nil {
		t.Fatalf("the graph example in %s is not a document this build reads: %v", parallelismSkillPath, err)
	}
	tiered, untiered := 0, 0
	for _, n := range doc.Nodes {
		switch {
		case n.Tier == nil:
			untiered++
		case !tier.Known(*n.Tier):
			t.Errorf("the example's node %q carries the tier %q, which is not one of %s", n.ID, *n.Tier, tier.List())
		default:
			tiered++
		}
	}
	if tiered == 0 {
		t.Errorf("no node of the example in %s carries a tier, so the example shows nothing of the field the section beside it teaches", parallelismSkillPath)
	}
	if untiered == 0 {
		t.Errorf("every node of the example in %s carries a tier, and a node with none is a task the planner did not size: an example that sizes them all teaches the field as compulsory", parallelismSkillPath)
	}

	// The line about the field: the four names, and what makes the tier a
	// different statement from the size beside it — the one thing a planner
	// copying the example has to know not to conflate.
	var bullet string
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "- **`tier`**") {
			bullet = line
			break
		}
	}
	if bullet == "" {
		t.Fatalf("%s has no bullet about the tier field", parallelismSkillPath)
	}
	for _, v := range tier.Tiers {
		if !strings.Contains(bullet, "`"+string(v)+"`") {
			t.Errorf("the tier bullet does not name the tier %q, and it has to name every one the read accepts:\n%s", v, bullet)
		}
	}
	if !strings.Contains(bullet, "not `size`") {
		t.Errorf("the tier bullet never says how the tier differs from `size`, and the two are the pair a planner is likeliest to read as one:\n%s", bullet)
	}
}

// TestParallelismNamesOnlyRealFlags (R6): the two commands this section sends a
// planner to — the read that checks a graph, and the import that queues one —
// are commands the released CLI answers to, resolved against the real tree with
// their flags. The plugin's own lint checks every skill's commands and flags
// (TestPluginSkillsLintClean); what this adds is that these two are still found
// in this file's text, because a parser that stopped finding commands would
// make that lint pass by checking nothing, and a planner left with no command
// to check or queue a graph with is the failure the section exists to prevent.
func TestParallelismNamesOnlyRealFlags(t *testing.T) {
	files, err := loadSkillFiles(".")
	if err != nil {
		t.Fatal(err)
	}
	ci := newCommandIndex()
	var (
		named []string
		found *skillFile
	)
	for _, f := range files {
		if f.skill != "parallelism" {
			continue
		}
		found = f
		for _, u := range f.units {
			if u.userRuns || strings.Contains(u.info, "fugaro.yaml") {
				continue
			}
			for _, c := range ci.commands(u.text) {
				if len(c.args) == 0 || strings.ContainsAny(c.args[0], "<[…") {
					continue // a mention of the program, or a placeholder
				}
				for _, v := range ci.checkCommand(f, u.line, u.text, c) {
					t.Error(v)
				}
				named = append(named, c.path)
			}
		}
	}
	if found == nil {
		t.Fatalf("no file of the parallelism skill was loaded")
	}
	for _, want := range []string{"fugaro plan graph", "fugaro orchestrator import"} {
		if !slices.Contains(named, want) {
			t.Errorf("the parallelism skill never names %q, which is one of the commands its graph section sends a planner to (named: %v)", want, named)
		}
	}
	// And the check bites: a flag the command does not carry is refused, so the
	// pass above is the tree's answer rather than an empty loop.
	bad := ci.commands("fugaro orchestrator import --plan-not")
	if len(bad) != 1 || len(ci.checkCommand(found, 1, "fugaro orchestrator import --plan-not", bad[0])) == 0 {
		t.Errorf("`fugaro orchestrator import --plan-not` was accepted against the real tree, and `import` carries no such flag")
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

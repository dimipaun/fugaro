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
// says why.
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
	if doc.SchemaVersion != plan.SchemaVersion {
		t.Errorf("the example is schema version %d and this build reads %d", doc.SchemaVersion, plan.SchemaVersion)
	}
	if doc.Plan == "" {
		t.Error("the example names no plan")
	}
	if len(doc.Nodes) != 2 || len(doc.Edges) != 1 {
		t.Fatalf("the example carries %d nodes and %d edges, want the two and the one it shows", len(doc.Nodes), len(doc.Edges))
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

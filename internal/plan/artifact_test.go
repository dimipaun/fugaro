package plan

import (
	"path/filepath"
	"testing"
)

// shippedArtifact is the graph artifact that ships in this tree: the
// console dependency graph plan's own graph, written beside it by the
// parallelism skill (GV1.7). It is the tree's first real `<plan>.graph.json`,
// which also makes it the first artifact no fixture protects — whoever can push
// edits it, and nothing else in the tree parses it: a renamed json tag, a
// version written the old way, a repeated id, an edge that lost its reason or a
// cycle would leave `fugaro plan graph` refusing a document with every test
// still green. Reading it here, through the call the command itself makes, is
// the tie. internal/streams pins the shipped docs/release-streams.yaml the same
// way (TestRealFileParses), before its readers existed.
const shippedArtifact = "../../docs/console-dependency-graph/implementation-plan.graph.json"

// TestShippedPlanGraphReads: the artifact this tree carries is a document this
// build renders, it names the plan it sits beside, and it carries the stream
// the owner ruled for this plan (console, 2026-10-25) and the tasks the plan
// states.
//
// The counts are pinned rather than counted off the plan, because there is no
// Markdown plan scraper by design (finding F3): the plan and its artifact are
// one edit, and a task added to one without the other is exactly the drift this
// test is here to catch.
func TestShippedPlanGraphReads(t *testing.T) {
	doc, err := ReadFile(shippedArtifact)
	if err != nil {
		t.Fatalf("%s is not a graph this build reads: %v", shippedArtifact, err)
	}
	if doc.SchemaVersion != SchemaVersion {
		t.Errorf("the shipped artifact is schema version %d, and this build writes %d", doc.SchemaVersion, SchemaVersion)
	}
	if doc.Plan != "docs/console-dependency-graph/implementation-plan.md" {
		t.Errorf("the shipped artifact names the plan %q", doc.Plan)
	}
	// The artifact is found because it sits beside the plan it names: a move of
	// one without the other leaves the command looking for a file that is not
	// there, which is a refusal no reader would connect to the move.
	if beside := filepath.Join("..", "..", ArtifactPath(doc.Plan)); beside != shippedArtifact {
		t.Errorf("the artifact of the plan this file names is %s, and the one read here is %s", beside, shippedArtifact)
	}
	switch {
	case doc.Stream == nil:
		t.Error("the shipped artifact names no stream: this plan's screen is the console's own release (console 0.112.0 \"Melon\"), so the document says console rather than leaving the column to be guessed")
	case *doc.Stream != "console":
		t.Errorf("the shipped artifact names the stream %q, want console: the read-only screen this plan builds ships in the console's own stream", *doc.Stream)
	}
	if len(doc.Nodes) != 17 {
		t.Errorf("the shipped artifact carries %d tasks, want the plan's 17 (GV0.1 to GV4.1)", len(doc.Nodes))
	}
	if len(doc.Edges) != 18 {
		t.Errorf("the shipped artifact carries %d edges, want the plan's 18 (its own \"Edges (from Depends on)\" line)", len(doc.Edges))
	}
}

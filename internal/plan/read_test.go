package plan

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPlanReadsTheFixtureGraph: the artifact parses, and what it carries is
// what the plan wrote -- the nodes, the one edge with its reason, the plan it
// came from and the schema version.
func TestPlanReadsTheFixtureGraph(t *testing.T) {
	doc, err := ReadFile(filepath.Join("testdata", "graph.json"))
	if err != nil {
		t.Fatal(err)
	}
	if doc.SchemaVersion != SchemaVersion {
		t.Errorf("schema_version = %d, want %d", doc.SchemaVersion, SchemaVersion)
	}
	if doc.Plan != "docs/plans/2026-10-15-desktop-console-v2.md" {
		t.Errorf("plan = %q, want the plan the artifact names", doc.Plan)
	}
	if len(doc.Nodes) != 3 || len(doc.Edges) != 1 {
		t.Fatalf("read %d nodes and %d edges, want the fixture's 3 and 1", len(doc.Nodes), len(doc.Edges))
	}
	byID := map[string]Node{}
	for _, n := range doc.Nodes {
		byID[n.ID] = n
	}
	// A run, and the absence of one, are two different facts: null stays null.
	if got := byID["V2.2.1"]; got.Run == nil || *got.Run != "console-graph-1" {
		t.Errorf("V2.2.1's run = %v, want the recorded run", got.Run)
	}
	if got := byID["V2.1.1"]; got.Run != nil {
		t.Errorf("V2.1.1's run = %v, want null: a task with no run says so with null, not with an empty string", *got.Run)
	}
	if got := byID["V2.2.2"]; got.Version != nil {
		t.Errorf("V2.2.2's version = %v, want null", *got.Version)
	}
	if e := doc.Edges[0]; e.Task != "V2.2.2" || e.Needs != "V2.2.1" || e.Reason == "" {
		t.Errorf("edge = %+v, want the fixture's task/needs and its reason", e)
	}
}

// TestPlanRefusesWhatItCannotRead: every one of these is a document this build
// will not render, and each refusal names what is wrong rather than failing
// somewhere later. The refusals are typed, so a caller can tell one from a
// surprise.
func TestPlanRefusesWhatItCannotRead(t *testing.T) {
	const valid = `{"schema_version": 1, "plan": "p.md", "nodes": [
		{"id": "a", "title": "first", "footprint": {"prediction": true, "paths": ["internal/**"]}},
		{"id": "b", "title": "second", "footprint": {"prediction": true, "paths": []}}], "edges": []}`
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{"not JSON at all", `{"schema_version": 1,`, "not JSON"},
		{"no schema version", `{"plan": "p.md", "nodes": [], "edges": []}`, "carries no schema_version"},
		{"a future schema version", `{"schema_version": 2, "plan": "p.md", "nodes": [], "edges": []}`,
			"schema version 2 and this build reads version 1"},
		{"an unknown field at this version", `{"schema_version": 1, "plan": "p.md", "nodes": [], "edges": [], "critical_path": 3}`,
			"not a schema version 1 graph"},
		{"no plan", `{"schema_version": 1, "nodes": [], "edges": []}`, "does not name the plan it came from"},
		{"a node with no id", `{"schema_version": 1, "plan": "p.md", "nodes": [{"id": "", "title": "x", "footprint": {"prediction": true, "paths": []}}], "edges": []}`,
			"node 0 has no id"},
		{"two nodes with one id", `{"schema_version": 1, "plan": "p.md", "nodes": [
			{"id": "a", "title": "one", "footprint": {"prediction": true, "paths": []}},
			{"id": "a", "title": "two", "footprint": {"prediction": true, "paths": []}}], "edges": []}`,
			"two nodes carry the id \"a\""},
		{"a node with no title", `{"schema_version": 1, "plan": "p.md", "nodes": [{"id": "a", "title": "", "footprint": {"prediction": true, "paths": []}}], "edges": []}`,
			"node \"a\" has no title"},
		{"an empty run rather than null", `{"schema_version": 1, "plan": "p.md", "nodes": [{"id": "a", "title": "x", "run": "", "footprint": {"prediction": true, "paths": []}}], "edges": []}`,
			"carries an empty run"},
		{"an empty version rather than null", `{"schema_version": 1, "plan": "p.md", "nodes": [{"id": "a", "title": "x", "version": " ", "footprint": {"prediction": true, "paths": []}}], "edges": []}`,
			"carries an empty version"},
		{"an edge to a node that is not there", `{"schema_version": 1, "plan": "p.md", "nodes": [{"id": "a", "title": "x", "footprint": {"prediction": true, "paths": []}}], "edges": [{"task": "a", "needs": "ghost", "reason": "why"}]}`,
			"needs \"ghost\", which is not a node of this graph"},
		{"an edge from a node that is not there", `{"schema_version": 1, "plan": "p.md", "nodes": [{"id": "a", "title": "x", "footprint": {"prediction": true, "paths": []}}], "edges": [{"task": "ghost", "needs": "a", "reason": "why"}]}`,
			"starts at \"ghost\""},
		{"an edge with no reason", `{"schema_version": 1, "plan": "p.md", "nodes": [
			{"id": "a", "title": "x", "footprint": {"prediction": true, "paths": []}},
			{"id": "b", "title": "y", "footprint": {"prediction": true, "paths": []}}], "edges": [{"task": "a", "needs": "b", "reason": ""}]}`,
			"carries no reason"},
		{"a footprint that is not labelled a prediction", `{"schema_version": 1, "plan": "p.md", "nodes": [{"id": "a", "title": "x", "footprint": {"prediction": false, "paths": ["internal/**"]}}], "edges": []}`,
			"not labelled a prediction"},
		{"a footprint path that is not a glob", `{"schema_version": 1, "plan": "p.md", "nodes": [{"id": "a", "title": "x", "footprint": {"prediction": true, "paths": ["internal/[unclosed"]}}], "edges": []}`,
			"not a glob this repository's matcher understands"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse("graph.json", []byte(tc.body))
			if err == nil {
				t.Fatalf("Parse accepted %s", tc.name)
			}
			var refusal *Refusal
			if !errors.As(err, &refusal) {
				t.Fatalf("Parse's error for %s is %T, want a *Refusal", tc.name, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("refusal for %s = %q, want it to say %q", tc.name, err, tc.want)
			}
			if !strings.HasPrefix(err.Error(), "graph.json: ") {
				t.Errorf("refusal for %s does not name the artifact: %q", tc.name, err)
			}
		})
	}
	// And the same document without the fault reads, so the table above is
	// refusing the fault and not the shape.
	if _, err := Parse("graph.json", []byte(valid)); err != nil {
		t.Fatalf("the valid document was refused: %v", err)
	}
}

// TestPlanRefusesAFutureSchemaFromAFile: the same refusal, read off disk, and
// with the fixture a newer CLI would leave behind -- it carries a field this
// build has never heard of, which is exactly what the version check has to
// answer before the field check can.
func TestPlanRefusesAFutureSchemaFromAFile(t *testing.T) {
	_, err := ReadFile(filepath.Join("testdata", "future.json"))
	if err == nil {
		t.Fatal("a schema version 2 artifact was read as a version 1 graph")
	}
	if !strings.Contains(err.Error(), "schema version 2 and this build reads version 1") {
		t.Errorf("refusal = %q, want it to name both versions", err)
	}
	if !strings.Contains(err.Error(), "upgrade fugaro") {
		t.Errorf("refusal = %q, want it to say what to do about it", err)
	}
}

// TestPlanRefusesMalformedBytes: a truncated artifact is refused as not-JSON
// rather than read as a graph with no nodes.
func TestPlanRefusesMalformedBytes(t *testing.T) {
	_, err := ReadFile(filepath.Join("testdata", "malformed.json"))
	if err == nil || !strings.Contains(err.Error(), "not JSON") {
		t.Fatalf("a truncated artifact gave %v, want a refusal that says it is not JSON", err)
	}
}

// TestPlanRefusesACycle is the one refusal the design calls out by name: a
// cyclic graph is a refusal, not a render. Every "ready" and "blocked" answer
// is a function of the graph, so a cyclic one has no answers to give, and the
// refusal names the cycle rather than saying only that one exists.
func TestPlanRefusesACycle(t *testing.T) {
	_, err := ReadFile(filepath.Join("testdata", "cycle.json"))
	if err == nil {
		t.Fatal("a cyclic graph was rendered")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("refusal = %q, want it to say the graph is cyclic", err)
	}
	// The path, in the direction the edges point, closed at both ends.
	if !strings.Contains(err.Error(), "a -> b -> c -> a") {
		t.Errorf("refusal = %q, want it to name the cycle a -> b -> c -> a", err)
	}
	// A self-edge is the shortest cycle there is.
	self := `{"schema_version": 1, "plan": "p.md", "nodes": [{"id": "a", "title": "x", "footprint": {"prediction": true, "paths": []}}],
		"edges": [{"task": "a", "needs": "a", "reason": "itself"}]}`
	if _, err := Parse("graph.json", []byte(self)); err == nil || !strings.Contains(err.Error(), "a -> a") {
		t.Errorf("a self-edge gave %v, want the cycle a -> a", err)
	}
}

// TestPlanNormalizesWhatItEmits: the emitted shape is the contract. Arrays are
// arrays even when empty (a null is a different statement), a footprint is
// always labelled a prediction, and the label is set by this package rather
// than read off the document.
func TestPlanNormalizesWhatItEmits(t *testing.T) {
	doc, err := Parse("graph.json", []byte(`{"schema_version": 1, "plan": "p.md",
		"nodes": [{"id": "a", "title": "x", "footprint": {"prediction": true, "paths": null}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Edges == nil || len(doc.Edges) != 0 {
		t.Errorf("edges = %v, want an empty array rather than a null", doc.Edges)
	}
	if doc.Nodes[0].Footprint.Paths == nil || !doc.Nodes[0].Footprint.Prediction {
		t.Errorf("footprint = %+v, want an empty array and the prediction label", doc.Nodes[0].Footprint)
	}
	// The label is not a value the document gets to set to true and mean
	// something else: it is what a footprint is.
	if doc.Nodes[0].Footprint.Prediction != true {
		t.Errorf("a footprint came out unlabelled: %+v", doc.Nodes[0].Footprint)
	}
}

// TestPlanArtifactPath: the artifact sits beside its plan and takes the plan's
// name, so moving a plan moves its graph and nothing has to be told twice.
func TestPlanArtifactPath(t *testing.T) {
	for _, tc := range []struct{ plan, want string }{
		{"docs/plans/2026-10-15-desktop-console-v2.md", "docs/plans/2026-10-15-desktop-console-v2.graph.json"},
		{"plans/foo.yaml", "plans/foo.graph.json"},
		{"plan", "plan.graph.json"},
		{"docs/plans/a.b.md", "docs/plans/a.b.graph.json"},
		{"./docs/plans/x.md", "./docs/plans/x.graph.json"},
	} {
		if got := ArtifactPath(tc.plan); got != tc.want {
			t.Errorf("ArtifactPath(%q) = %q, want %q", tc.plan, got, tc.want)
		}
	}
}

// TestPlanFootprintIsAPrediction: D28's answer is that a footprint is advisory
// and labelled as a prediction *in the document*, so the label travels with the
// data. The mutation this guards is the one that matters: a reader that stopped
// setting it would leave every renderer to remember.
func TestPlanFootprintIsAPrediction(t *testing.T) {
	fp := NewFootprint([]string{"internal/**"})
	if !fp.Prediction {
		t.Error("NewFootprint built a footprint that is not labelled a prediction")
	}
	if fp.Paths == nil {
		t.Error("NewFootprint built a footprint with a null paths list")
	}
	// A node's footprint in a read document is labelled, whichever way the
	// document spelled its paths.
	doc, err := Parse("graph.json", []byte(`{"schema_version": 1, "plan": "p.md",
		"nodes": [{"id": "a", "title": "x", "footprint": {"prediction": true, "paths": ["a.go"]}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !doc.Nodes[0].Footprint.Prediction {
		t.Errorf("the read document's footprint is unlabelled: %+v", doc.Nodes[0].Footprint)
	}
}

// TestPlanFootprintMatchesTheRepositorysGlobSemantics: the matching is this
// repository's own -- doublestar, the library internal/imagecheck,
// internal/cache/key.go, internal/config/validate.go and
// internal/verify/reports.go already use -- so a `**` crosses separators and a
// pattern that names a directory's contents matches what it says. The
// mutations this fails on: swapping doublestar for path.Match (which has no
// `**`) and matching against an absolute or filesystem path instead of the
// repository's own.
func TestPlanFootprintMatchesTheRepositorysGlobSemantics(t *testing.T) {
	fp := NewFootprint([]string{"internal/plan/**", "docs/plans/*.md", "fugaro.yaml"})
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"internal/plan/read.go", true},
		{"internal/plan/testdata/graph.json", true},
		{"internal/cli/plan.go", false},
		{"docs/plans/2026-10-15-desktop-console-v2.md", true},
		{"docs/plans/sub/x.md", false},
		{"fugaro.yaml", true},
		{"/work/repo/internal/plan/read.go", false}, // a repository path, not a filesystem one
	} {
		if got := fp.Matches(tc.path); got != tc.want {
			t.Errorf("Matches(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
	// A task that predicts nothing matches nothing, and says so with an empty
	// list rather than with a pattern that would match everything.
	empty := NewFootprint(nil)
	if empty.Matches("internal/plan/read.go") {
		t.Error("an empty footprint matched a path")
	}
}

// TestPlanRefusesAnOversizedArtifact: the artifact is a file in a checkout,
// which anyone with push access can write, and what it carries goes to the
// console's renderer — so the read is bounded. A file past MaxArtifactBytes is
// refused with a sentence that says so, and the read stops at the bound
// rather than taking the whole file into memory first.
func TestPlanRefusesAnOversizedArtifact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.graph.json")
	pad := strings.Repeat(" ", MaxArtifactBytes)
	if err := os.WriteFile(path, []byte(`{"schema_version": 1, "plan": "p.md", "nodes": [], "edges": []}`+pad), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ReadFile(path)
	var refusal *Refusal
	if !errors.As(err, &refusal) || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("an oversized artifact gave %v, want a refusal naming the bound", err)
	}
}

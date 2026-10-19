package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/plan"
)

// planGraphFixture writes a plan and its graph artifact into a directory of the
// test's own and returns the plan's path. The artifact is a real one, in the
// shape the planner writes: the path is derived from the plan, so the test
// exercises the naming rule rather than passing a path the command could not
// have found for itself.
func planGraphFixture(t *testing.T, graph string) (dir, planPath string) {
	t.Helper()
	dir = t.TempDir()
	planPath = filepath.Join(dir, "plan.md")
	if err := os.WriteFile(planPath, []byte("# a plan\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plan.ArtifactPath(planPath), []byte(graph), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, planPath
}

const planGraphDoc = `{
  "schema_version": 1,
  "plan": "docs/plans/2026-10-15-desktop-console-v2.md",
  "nodes": [
    {"id": "V2.2.1", "title": "the graph document and the read that prints it",
     "run": "console-graph-1", "version": "0.9.0",
     "footprint": {"prediction": true, "paths": ["internal/plan/**", "internal/cli/plan.go"]}},
    {"id": "V2.2.2", "title": "the footprint", "run": null, "version": "0.9.0",
     "footprint": {"prediction": true, "paths": []}}
  ],
  "edges": [
    {"task": "V2.2.2", "needs": "V2.2.1", "reason": "the footprint is a field of the document V2.2.1 defines"}
  ]
}`

// TestPlanGraphPrintsTheArtifact: `fugaro plan graph --json` is the read the
// console spawns, and its schema is pinned key by key -- every key of the
// document, of a node, of an edge and of a footprint, so a field added to any
// of them fails a named test rather than reaching a screen.
func TestPlanGraphPrintsTheArtifact(t *testing.T) {
	_, planPath := planGraphFixture(t, planGraphDoc)
	out, _, err := execute(t, "plan", "graph", "--plan", planPath, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc plan.Document
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if doc.SchemaVersion != plan.SchemaVersion || len(doc.Nodes) != 2 || len(doc.Edges) != 1 {
		t.Fatalf("read back %+v, want the fixture's schema version, two nodes and one edge", doc)
	}

	var top map[string]any
	if err := json.Unmarshal([]byte(out), &top); err != nil {
		t.Fatal(err)
	}
	if k := keysOf(t, top); !reflect.DeepEqual(k, goldenKeys(t, "plan_graph_keys.golden")) {
		t.Errorf("plan graph --json top-level keys = %v", k)
	}
	nodes, _ := top["nodes"].([]any)
	if len(nodes) != 2 {
		t.Fatalf("nodes = %v, want two", nodes)
	}
	node, _ := nodes[0].(map[string]any)
	if k := keysOf(t, node); !reflect.DeepEqual(k, goldenKeys(t, "plan_graph_node_keys.golden")) {
		t.Errorf("plan graph --json nodes[] keys = %v", k)
	}
	footprint, _ := node["footprint"].(map[string]any)
	if k := keysOf(t, footprint); !reflect.DeepEqual(k, goldenKeys(t, "plan_graph_footprint_keys.golden")) {
		t.Errorf("plan graph --json nodes[].footprint keys = %v", k)
	}
	if footprint["prediction"] != true {
		t.Errorf("footprint = %v, want the prediction label (D28)", footprint)
	}
	edges, _ := top["edges"].([]any)
	edge, _ := edges[0].(map[string]any)
	if k := keysOf(t, edge); !reflect.DeepEqual(k, goldenKeys(t, "plan_graph_edge_keys.golden")) {
		t.Errorf("plan graph --json edges[] keys = %v", k)
	}

	// The three statements a renderer has to keep apart, spelled out in the
	// document: a node with no run (null), a node with a run, and a footprint
	// that predicts nothing (an empty array, never a null).
	second, _ := nodes[1].(map[string]any)
	if second["run"] != nil {
		t.Errorf("V2.2.2's run = %v, want null", second["run"])
	}
	if p, _ := second["footprint"].(map[string]any); p["paths"] == nil {
		t.Errorf("an empty footprint came out as a null: %v", second["footprint"])
	}
	if !strings.Contains(out, `"paths": []`) {
		t.Errorf("the document does not carry an empty paths array:\n%s", out)
	}

	// The human form names the plan, the tasks and the dependencies, and it
	// labels the footprint a prediction rather than printing bare globs.
	text, _, err := execute(t, "plan", "graph", "--plan", planPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"V2.2.1", "V2.2.2", "the footprint", "predicted:", "console-graph-1"} {
		if !strings.Contains(text, want) {
			t.Errorf("text graph does not carry %q:\n%s", want, text)
		}
	}
}

// TestPlanGraphRefusesWhatItCannotRender: the three refusals the design names,
// through the command rather than through the package -- a malformed artifact,
// one from a future schema version, and a cycle, which is a refusal and not a
// render.
func TestPlanGraphRefusesWhatItCannotRender(t *testing.T) {
	for _, tc := range []struct {
		name  string
		graph string
		want  string
	}{
		{"malformed", `{"schema_version": 1, "plan": "p.md", "nodes": [`, "not JSON"},
		{"a future schema version", `{"schema_version": 9, "plan": "p.md", "nodes": [], "edges": []}`,
			"schema version 9 and this build reads version 1"},
		{"a cycle", `{"schema_version": 1, "plan": "p.md", "nodes": [
			{"id": "a", "title": "one", "footprint": {"prediction": true, "paths": []}},
			{"id": "b", "title": "two", "footprint": {"prediction": true, "paths": []}}],
			"edges": [
				{"task": "a", "needs": "b", "reason": "a waits for b"},
				{"task": "b", "needs": "a", "reason": "and b waits for a"}]}`,
			"a -> b -> a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, planPath := planGraphFixture(t, tc.graph)
			out, _, err := execute(t, "plan", "graph", "--plan", planPath, "--json")
			if err == nil {
				t.Fatalf("plan graph rendered %s:\n%s", tc.name, out)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("refusal = %q, want it to say %q", err, tc.want)
			}
			if strings.Contains(out, `"nodes"`) {
				t.Errorf("a refused graph still printed a document:\n%s", out)
			}
		})
	}
}

// TestPlanGraphFindsTheArtifactBesideThePlan: the artifact's name is derived
// from the plan's, and with no --plan the checkout's docs/plans is searched --
// one artifact is read, none is a refusal that says what to pass, and more
// than one is a refusal that lists them rather than picking.
func TestPlanGraphFindsTheArtifactBesideThePlan(t *testing.T) {
	dir, _ := planGraphFixture(t, planGraphDoc)
	if got := plan.ArtifactPath(filepath.Join(dir, "plan.md")); got != filepath.Join(dir, "plan.graph.json") {
		t.Fatalf("the fixture's artifact is at %q", got)
	}

	// A checkout-shaped directory with one plan and its graph.
	root := t.TempDir()
	plans := filepath.Join(root, "docs", "plans")
	if err := os.MkdirAll(plans, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plans, "the-plan.graph.json"), []byte(planGraphDoc), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	out, _, err := execute(t, "plan", "graph", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "V2.2.1") {
		t.Errorf("the discovered artifact did not read:\n%s", out)
	}

	// Two artifacts: a refusal naming both, never a pick.
	if err := os.WriteFile(filepath.Join(plans, "another.graph.json"), []byte(planGraphDoc), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err = execute(t, "plan", "graph", "--json")
	if err == nil || !strings.Contains(err.Error(), "another.graph.json") {
		t.Fatalf("two artifacts gave %v, want a refusal listing them", err)
	}

	// None: a refusal that says what to pass.
	if err := os.RemoveAll(plans); err != nil {
		t.Fatal(err)
	}
	_, _, err = execute(t, "plan", "graph", "--json")
	if err == nil || !strings.Contains(err.Error(), "--plan") {
		t.Fatalf("no artifact gave %v, want a refusal naming --plan", err)
	}
}

// TestPlanGraphIsALocalRead: the command takes no cloud flags, which is what
// makes it safe to spawn without a project selected -- and it is the fact
// the console's JSON contract states about it in its flag inventory.
func TestPlanGraphIsALocalRead(t *testing.T) {
	cmd := newPlanGraphCmd()
	for _, flag := range []string{"project", "config", "gcp-project", "region"} {
		if cmd.Flags().Lookup(flag) != nil {
			t.Errorf("plan graph takes --%s; it is a local read and needs no installation", flag)
		}
	}
	if cmd.Flags().Lookup("json") == nil || cmd.Flags().Lookup("plan") == nil {
		t.Errorf("plan graph's flags are %s, want --plan and --json", cmd.Flags().FlagUsages())
	}
}

// TestPlanGraphTextIsTerminalSafe: the artifact is checkout text anyone with
// push access writes, so the human form escapes what would drive a terminal
// and keeps a tab from opening a column of its own in the table.
func TestPlanGraphTextIsTerminalSafe(t *testing.T) {
	graph := `{"schema_version": 1, "plan": "p.md", "nodes": [
		{"id": "a", "title": "evil\u001b]52;c;aGk=\u0007\ttitle", "footprint": {"prediction": true, "paths": []}}],
		"edges": []}`
	_, planPath := planGraphFixture(t, graph)
	text, _, err := execute(t, "plan", "graph", "--plan", planPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(text, "\x1b\x07") {
		t.Errorf("text graph carries a raw control character:\n%q", text)
	}
	for _, l := range strings.Split(text, "\n") {
		if strings.Contains(l, "evil") && strings.Contains(l, "\t") {
			t.Errorf("a title's tab reached the table: %q", l)
		}
	}
}

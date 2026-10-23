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
	criticalPath, _ := top["critical_path"].(map[string]any)
	if k := keysOf(t, criticalPath); !reflect.DeepEqual(k, goldenKeys(t, "plan_graph_critical_path_keys.golden")) {
		t.Errorf("plan graph --json critical_path keys = %v", k)
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

// planDiamondDoc is the fixture the readiness tests read: two arms off one base,
// joined at the end. It is the same shape as internal/plan's testdata fixture,
// written here as the command's own document so the CLI tests exercise the read
// rather than the package.
const planDiamondDoc = `{
  "schema_version": 1,
  "plan": "docs/plans/diamond.md",
  "nodes": [
    {"id": "a", "title": "the base", "run": null, "version": "0.9.0",
     "footprint": {"prediction": true, "paths": ["internal/plan/**"]}},
    {"id": "b", "title": "the left arm", "run": null, "version": "0.9.0",
     "footprint": {"prediction": true, "paths": []}},
    {"id": "c", "title": "the right arm", "run": null, "version": null,
     "footprint": {"prediction": true, "paths": []}},
    {"id": "d", "title": "the join", "run": null, "version": null,
     "footprint": {"prediction": true, "paths": []}}
  ],
  "edges": [
    {"task": "b", "needs": "a", "reason": "b builds on a"},
    {"task": "c", "needs": "a", "reason": "c builds on a"},
    {"task": "d", "needs": "b", "reason": "d needs the left arm"},
    {"task": "d", "needs": "c", "reason": "d needs the right arm"}
  ]
}`

// TestPlanGraphPrintsReadiness: the read computes readiness where the graph is
// read (design F7) and prints it in the same document -- a task is ready when
// every task it needs is done, and the done set is what the caller says it is
// (--done), because "this run finished" is not the same claim as "this task is
// done". Levels and the critical path are the same read's answers (D26).
func TestPlanGraphPrintsReadiness(t *testing.T) {
	_, planPath := planGraphFixture(t, planDiamondDoc)
	out, _, err := execute(t, "plan", "graph", "--plan", planPath, "--json", "--done", "a")
	if err != nil {
		t.Fatal(err)
	}
	var g plan.Graph
	if err := json.Unmarshal([]byte(out), &g); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	states := map[string]string{}
	for _, n := range g.Nodes {
		switch {
		case n.Done:
			states[n.ID] = "done"
		case n.Ready:
			states[n.ID] = "ready"
		case n.Blocked:
			states[n.ID] = "blocked"
		default:
			states[n.ID] = "none"
		}
	}
	want := map[string]string{"a": "done", "b": "ready", "c": "ready", "d": "blocked"}
	if !reflect.DeepEqual(states, want) {
		t.Errorf("with --done a the states are %v, want %v", states, want)
	}
	if g.Levels != 3 || g.CriticalPath.Length != 3 || strings.Join(g.CriticalPath.Tasks, " ") != "a b d" {
		t.Errorf("levels = %d, critical path = %+v, want 3 levels and the path a b d", g.Levels, g.CriticalPath)
	}
	if g.Bottleneck == nil || *g.Bottleneck != "a" {
		t.Errorf("bottleneck = %v, want a", g.Bottleneck)
	}

	// With no --done, nothing is done: the base is ready and the rest blocked.
	out, _, err = execute(t, "plan", "graph", "--plan", planPath, "--json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &g); err != nil {
		t.Fatal(err)
	}
	states = map[string]string{}
	for _, n := range g.Nodes {
		switch {
		case n.Done:
			states[n.ID] = "done"
		case n.Ready:
			states[n.ID] = "ready"
		case n.Blocked:
			states[n.ID] = "blocked"
		default:
			states[n.ID] = "none"
		}
	}
	if !reflect.DeepEqual(states, map[string]string{"a": "ready", "b": "blocked", "c": "blocked", "d": "blocked"}) {
		t.Errorf("with no --done the states are %v, want only a ready", states)
	}

	// The human form names the same answers, so the terminal shows what the
	// document carries.
	text, _, err := execute(t, "plan", "graph", "--plan", planPath, "--done", "a,b")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"LEVEL", "STATE", "ready", "blocked", "done", "critical path", "a -> b -> d", "bottleneck: a"} {
		if !strings.Contains(text, want) {
			t.Errorf("the text graph does not carry %q:\n%s", want, text)
		}
	}
}

// TestPlanGraphRefusesADoneSetItCannotPlace: a --done id that is not a task of
// the graph is refused rather than ignored -- an ignored one reads exactly like
// a task nobody has done, which is a wrong answer to the question the flag is
// asked.
func TestPlanGraphRefusesADoneSetItCannotPlace(t *testing.T) {
	_, planPath := planGraphFixture(t, planDiamondDoc)
	for _, tc := range []struct {
		name string
		done []string
		want string
	}{
		{"a task that is not there", []string{"ghost"}, `"ghost"`},
		{"an empty id", []string{"a,"}, "empty task id"},
		// `--done ""` is pflag's empty list rather than a done set, and the
		// contract (§6.9) promises an empty id is refused: it must not be read
		// as "no tasks are done".
		{"an empty flag", []string{""}, "names no task"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"plan", "graph", "--plan", planPath, "--json"}, "--done", strings.Join(tc.done, ","))
			out, _, err := execute(t, args...)
			if err == nil {
				t.Fatalf("plan graph accepted %s:\n%s", tc.name, out)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("refusal = %q, want it to say %q", err, tc.want)
			}
		})
	}
}

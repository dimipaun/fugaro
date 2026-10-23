package plan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The tests below are V2.2.3: readiness, the levels and the critical path. The
// three are one feature -- "ready" and "blocked" are functions of the graph and
// of the done set, and the critical path is the ranking over the same edges --
// so the tables here carry one graph each and assert all of what the read says
// about it.
//
// The diamond fixture (testdata/diamond.json) is the shape the feature exists
// for: two arms off one base, joined at the end. The cycle fixture
// (testdata/cycle.json) is the one document this read must refuse rather than
// answer, and the test that says so also says the refusal *returns*: a walk
// that followed a cycle would hang, and a hung test is not a failure anyone
// reads.

// analyze reads a document the way the CLI does -- Parse, then Analyze -- and
// fails the test if either refuses. A test that wants the refusal calls Analyze
// itself.
func analyze(t *testing.T, body string, done ...string) Graph {
	t.Helper()
	doc, err := Parse("graph.json", []byte(body))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	g, err := Analyze(doc, done)
	if err != nil {
		t.Fatalf("Analyze(done=%v): %v", done, err)
	}
	return g
}

// analyzeFixture is analyze over one of testdata's artifacts, read the way the
// command reads it.
func analyzeFixture(t *testing.T, name string, done ...string) Graph {
	t.Helper()
	doc, err := ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", name, err)
	}
	g, err := Analyze(doc, done)
	if err != nil {
		t.Fatalf("Analyze(%s, done=%v): %v", name, done, err)
	}
	return g
}

// stateOf is the one word a renderer colours a node by, read off the three
// fields the document carries rather than derived by a screen.
func stateOf(n NodeState) string {
	switch {
	case n.Done:
		return "done"
	case n.Ready:
		return "ready"
	case n.Blocked:
		return "blocked"
	}
	return "none"
}

// checkOneState asserts the invariant the three fields have: a task is done,
// ready or blocked, exactly one of the three, always. A node that is none of
// them is a node no renderer can colour, and a node that is two is a node that
// two screens would colour differently.
func checkOneState(t *testing.T, n NodeState) {
	t.Helper()
	n1 := 0
	for _, on := range []bool{n.Done, n.Ready, n.Blocked} {
		if on {
			n1++
		}
	}
	if n1 != 1 {
		t.Errorf("task %q is %s: done=%v ready=%v blocked=%v, want exactly one of the three", n.ID, stateOf(n), n.Done, n.Ready, n.Blocked)
	}
}

// byID indexes a read graph's nodes.
func byID(g Graph) map[string]NodeState {
	out := make(map[string]NodeState, len(g.Nodes))
	for _, n := range g.Nodes {
		out[n.ID] = n
	}
	return out
}

// TestPlanReadsTheDiamondFixture: the fixture parses and the read answers over
// it. Nothing is done, so the base is ready and everything that waits on it --
// directly or through an arm -- is blocked; the critical path runs the length of
// the diamond, and the base is its bottleneck.
func TestPlanReadsTheDiamondFixture(t *testing.T) {
	g := analyzeFixture(t, "diamond.json")
	if g.SchemaVersion != SchemaVersion || len(g.Nodes) != 4 || len(g.Edges) != 4 {
		t.Fatalf("read %+v, want the fixture's schema version, four nodes and four edges", g)
	}
	nodes := byID(g)
	for _, id := range []string{"a", "b", "c", "d"} {
		checkOneState(t, nodes[id])
	}
	if got := stateOf(nodes["a"]); got != "ready" {
		t.Errorf("a is %s with nothing done, want ready: nothing waits in front of it", got)
	}
	for _, id := range []string{"b", "c", "d"} {
		if got := stateOf(nodes[id]); got != "blocked" {
			t.Errorf("%s is %s with nothing done, want blocked", id, got)
		}
	}
	if g.Levels != 3 {
		t.Errorf("levels = %d, want 3 (a, then the two arms, then d)", g.Levels)
	}
	if g.CriticalPath.Length != 3 || strings.Join(g.CriticalPath.Tasks, " ") != "a b d" {
		t.Errorf("critical path = %+v, want 3 tasks, a b d", g.CriticalPath)
	}
	if g.Bottleneck == nil || *g.Bottleneck != "a" {
		t.Errorf("bottleneck = %v, want a: every other task waits on it", g.Bottleneck)
	}
}

// TestPlanReadyAndBlocked: a task is ready when every task it needs is done
// (design F7), and the done set is a fact the read is given rather than one it
// infers -- so a task marked done whose own dependency is not done stays done,
// and its dependency stays whatever the graph says it is. Table-driven, over
// the diamond, a chain, a graph with no edges at all, and the empty graph.
func TestPlanReadyAndBlocked(t *testing.T) {
	const chain = `{"schema_version": 1, "plan": "p.md", "nodes": [
		{"id": "a", "title": "first", "footprint": {"prediction": true, "paths": []}},
		{"id": "b", "title": "second", "footprint": {"prediction": true, "paths": []}},
		{"id": "c", "title": "third", "footprint": {"prediction": true, "paths": []}}],
		"edges": [
			{"task": "b", "needs": "a", "reason": "b waits for a"},
			{"task": "c", "needs": "b", "reason": "c waits for b"}]}`
	const independent = `{"schema_version": 1, "plan": "p.md", "nodes": [
		{"id": "a", "title": "first", "footprint": {"prediction": true, "paths": []}},
		{"id": "b", "title": "second", "footprint": {"prediction": true, "paths": []}},
		{"id": "c", "title": "third", "footprint": {"prediction": true, "paths": []}}],
		"edges": []}`
	const empty = `{"schema_version": 1, "plan": "p.md", "nodes": [], "edges": []}`

	diamond := func(t *testing.T) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join("testdata", "diamond.json"))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	for _, tc := range []struct {
		name  string
		graph func(*testing.T) string
		done  []string
		want  map[string]string
	}{
		{
			name:  "the diamond with nothing done",
			graph: diamond,
			want:  map[string]string{"a": "ready", "b": "blocked", "c": "blocked", "d": "blocked"},
		},
		{
			name:  "the diamond with its base done",
			graph: diamond,
			done:  []string{"a"},
			want:  map[string]string{"a": "done", "b": "ready", "c": "ready", "d": "blocked"},
		},
		{
			name:  "the diamond with one arm done",
			graph: diamond,
			done:  []string{"a", "b"},
			want:  map[string]string{"a": "done", "b": "done", "c": "ready", "d": "blocked"},
		},
		{
			name:  "the diamond with both arms done: the join is ready",
			graph: diamond,
			done:  []string{"a", "b", "c"},
			want:  map[string]string{"a": "done", "b": "done", "c": "done", "d": "ready"},
		},
		{
			name:  "the diamond finished",
			graph: diamond,
			done:  []string{"a", "b", "c", "d"},
			want:  map[string]string{"a": "done", "b": "done", "c": "done", "d": "done"},
		},
		{
			name:  "the done set is a fact, not an inference: d is done while a is not",
			graph: diamond,
			done:  []string{"d"},
			want:  map[string]string{"a": "ready", "b": "blocked", "c": "blocked", "d": "done"},
		},
		{
			name:  "a chain, one link done",
			graph: func(*testing.T) string { return chain },
			done:  []string{"a"},
			want:  map[string]string{"a": "done", "b": "ready", "c": "blocked"},
		},
		{
			name:  "a chain, nothing done",
			graph: func(*testing.T) string { return chain },
			want:  map[string]string{"a": "ready", "b": "blocked", "c": "blocked"},
		},
		{
			name:  "no edges at all: everything is ready at once",
			graph: func(*testing.T) string { return independent },
			want:  map[string]string{"a": "ready", "b": "ready", "c": "ready"},
		},
		{
			name:  "no tasks at all",
			graph: func(*testing.T) string { return empty },
			want:  map[string]string{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := analyze(t, tc.graph(t), tc.done...)
			nodes := byID(g)
			if len(nodes) != len(tc.want) {
				t.Fatalf("read %d tasks, want %d", len(nodes), len(tc.want))
			}
			for id, want := range tc.want {
				n, ok := nodes[id]
				if !ok {
					t.Errorf("the read carries no task %q", id)
					continue
				}
				checkOneState(t, n)
				if got := stateOf(n); got != want {
					t.Errorf("task %s is %s, want %s (done set %v)", id, got, want, tc.done)
				}
			}
			// The done set is echoed per task, so a renderer reads it off the
			// document rather than remembering what it passed.
			for _, id := range tc.done {
				if !nodes[id].Done {
					t.Errorf("task %s is in the done set and the document does not say so", id)
				}
			}
			for id, n := range nodes {
				if n.Done == contains(tc.done, id) {
					continue
				}
				t.Errorf("task %s: done = %v, and the done set %v says otherwise", id, n.Done, tc.done)
			}
		})
	}
}

// contains reports whether ids carries id.
func contains(ids []string, id string) bool {
	for _, got := range ids {
		if got == id {
			return true
		}
	}
	return false
}

// TestPlanLevelsAndCriticalPath: the levels, the critical path and the
// bottleneck ranking (design D26). The case the feature exists for is
// "the raw-count hub": a task six one-line dependents hang off is not the
// bottleneck of a plan whose longest chain is four tasks long, and a read that
// ranked by the raw count would name the hub.
func TestPlanLevelsAndCriticalPath(t *testing.T) {
	node := func(id string) string {
		return `{"id": "` + id + `", "title": "` + id + `", "footprint": {"prediction": true, "paths": []}}`
	}
	edge := func(task, needs string) string {
		return `{"task": "` + task + `", "needs": "` + needs + `", "reason": "` + task + ` waits for ` + needs + `"}`
	}
	graph := func(nodes, edges []string) string {
		return `{"schema_version": 1, "plan": "p.md", "nodes": [` + strings.Join(nodes, ",") + `],
			"edges": [` + strings.Join(edges, ",") + `]}`
	}

	// The hub: one task with six dependents, each a leaf of its own, and
	// beside it a chain of four. The hub has the most downstream tasks; the
	// chain is the longest work in the plan.
	hubNodes := []string{node("hub"), node("c1"), node("c2"), node("c3"), node("c4")}
	hubEdges := []string{edge("c2", "c1"), edge("c3", "c2"), edge("c4", "c3")}
	for _, leaf := range []string{"l1", "l2", "l3", "l4", "l5", "l6"} {
		hubNodes = append(hubNodes, node(leaf))
		hubEdges = append(hubEdges, edge(leaf, "hub"))
	}

	// Two chains of the same length, so the tie-break is the assertion.
	tieNodes := []string{node("aa1"), node("aa2"), node("bb1"), node("bb2")}
	tieEdges := []string{edge("aa2", "aa1"), edge("bb2", "bb1")}

	for _, tc := range []struct {
		name       string
		graph      string
		levels     int
		level      map[string]int
		critical   map[string]int // critical_path_length
		downstream map[string]int
		pathLength int
		pathTasks  string
		bottleneck string // "" means null
	}{
		{
			name:       "the empty graph answers with nothing rather than refusing",
			graph:      graph(nil, nil),
			levels:     0,
			level:      map[string]int{},
			critical:   map[string]int{},
			downstream: map[string]int{},
			pathLength: 0,
			pathTasks:  "",
			bottleneck: "",
		},
		{
			name:       "one task is a critical path of one",
			graph:      graph([]string{node("a")}, nil),
			levels:     1,
			level:      map[string]int{"a": 0},
			critical:   map[string]int{"a": 1},
			downstream: map[string]int{"a": 0},
			pathLength: 1,
			pathTasks:  "a",
			bottleneck: "a",
		},
		{
			name:       "the diamond: three levels, two arms of one length",
			graph:      graph([]string{node("a"), node("b"), node("c"), node("d")}, []string{edge("b", "a"), edge("c", "a"), edge("d", "b"), edge("d", "c")}),
			levels:     3,
			level:      map[string]int{"a": 0, "b": 1, "c": 1, "d": 2},
			critical:   map[string]int{"a": 3, "b": 2, "c": 2, "d": 1},
			downstream: map[string]int{"a": 3, "b": 1, "c": 1, "d": 0},
			pathLength: 3,
			// The two arms tie; the tie is broken by id, so the answer is the
			// same one every run.
			pathTasks:  "a b d",
			bottleneck: "a",
		},
		{
			name:       "the raw-count hub is not the bottleneck",
			graph:      graph(hubNodes, hubEdges),
			levels:     4,
			level:      map[string]int{"hub": 0, "l1": 1, "l2": 1, "l3": 1, "l4": 1, "l5": 1, "l6": 1, "c1": 0, "c2": 1, "c3": 2, "c4": 3},
			critical:   map[string]int{"hub": 2, "l1": 1, "l2": 1, "l3": 1, "l4": 1, "l5": 1, "l6": 1, "c1": 4, "c2": 3, "c3": 2, "c4": 1},
			downstream: map[string]int{"hub": 6, "l1": 0, "l2": 0, "l3": 0, "l4": 0, "l5": 0, "l6": 0, "c1": 3, "c2": 2, "c3": 1, "c4": 0},
			pathLength: 4,
			pathTasks:  "c1 c2 c3 c4",
			// hub has six tasks waiting on it and c1 has three: the raw count
			// names hub, the critical path names c1, and the read names c1.
			bottleneck: "c1",
		},
		{
			name:       "two chains of one length tie, and the tie is stable",
			graph:      graph(tieNodes, tieEdges),
			levels:     2,
			level:      map[string]int{"aa1": 0, "aa2": 1, "bb1": 0, "bb2": 1},
			critical:   map[string]int{"aa1": 2, "aa2": 1, "bb1": 2, "bb2": 1},
			downstream: map[string]int{"aa1": 1, "aa2": 0, "bb1": 1, "bb2": 0},
			pathLength: 2,
			pathTasks:  "aa1 aa2",
			bottleneck: "aa1",
		},
		{
			name:       "a duplicated edge is one dependency, not two",
			graph:      graph([]string{node("a"), node("b")}, []string{edge("b", "a"), edge("b", "a")}),
			levels:     2,
			level:      map[string]int{"a": 0, "b": 1},
			critical:   map[string]int{"a": 2, "b": 1},
			downstream: map[string]int{"a": 1, "b": 0},
			pathLength: 2,
			pathTasks:  "a b",
			bottleneck: "a",
		},
		{
			name:       "a task that waits on nothing in the middle of a chain is level zero",
			graph:      graph([]string{node("a"), node("b"), node("x")}, []string{edge("b", "a"), edge("x", "b")}),
			levels:     3,
			level:      map[string]int{"a": 0, "b": 1, "x": 2},
			critical:   map[string]int{"a": 3, "b": 2, "x": 1},
			downstream: map[string]int{"a": 2, "b": 1, "x": 0},
			pathLength: 3,
			pathTasks:  "a b x",
			bottleneck: "a",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := analyze(t, tc.graph)
			if g.Levels != tc.levels {
				t.Errorf("levels = %d, want %d", g.Levels, tc.levels)
			}
			nodes := byID(g)
			if len(nodes) != len(tc.level) {
				t.Fatalf("read %d tasks, want %d", len(nodes), len(tc.level))
			}
			for id, want := range tc.level {
				n, ok := nodes[id]
				if !ok {
					t.Errorf("the read carries no task %q", id)
					continue
				}
				if n.Level != want {
					t.Errorf("task %s is at level %d, want %d", id, n.Level, want)
				}
				if n.CriticalPathLength != tc.critical[id] {
					t.Errorf("task %s's critical path length = %d, want %d", id, n.CriticalPathLength, tc.critical[id])
				}
				if n.Downstream != tc.downstream[id] {
					t.Errorf("task %s's downstream count = %d, want %d", id, n.Downstream, tc.downstream[id])
				}
			}
			if g.CriticalPath.Length != tc.pathLength {
				t.Errorf("critical path length = %d, want %d", g.CriticalPath.Length, tc.pathLength)
			}
			if got := strings.Join(g.CriticalPath.Tasks, " "); got != tc.pathTasks {
				t.Errorf("critical path = %q, want %q", got, tc.pathTasks)
			}
			if tc.pathLength != len(g.CriticalPath.Tasks) {
				t.Errorf("the critical path says it is %d tasks long and names %d", g.CriticalPath.Length, len(g.CriticalPath.Tasks))
			}
			switch {
			case tc.bottleneck == "" && g.Bottleneck != nil:
				t.Errorf("bottleneck = %q, want null: this graph has no tasks", *g.Bottleneck)
			case tc.bottleneck != "" && g.Bottleneck == nil:
				t.Errorf("bottleneck = null, want %q", tc.bottleneck)
			case tc.bottleneck != "" && *g.Bottleneck != tc.bottleneck:
				t.Errorf("bottleneck = %q, want %q", *g.Bottleneck, tc.bottleneck)
			}
			// The path named is a path: each task in it waits on the one
			// before, and it is as long as the length says.
			if got := pathIsReal(t, g); got != "" {
				t.Error(got)
			}
			// And the ranking is a function of the graph, not of the order the
			// document happens to list its nodes in.
			again := analyze(t, tc.graph)
			if !sameAnalysis(g, again) {
				t.Errorf("two reads of one graph disagree:\n%+v\n%+v", g, again)
			}
		})
	}
}

// pathIsReal checks that the critical path the read named is one: consecutive
// tasks, each waiting on the one before it. It returns a sentence naming what
// is wrong, or "".
func pathIsReal(t *testing.T, g Graph) string {
	t.Helper()
	tasks := g.CriticalPath.Tasks
	if len(tasks) != g.CriticalPath.Length {
		return "the critical path's length and its task list disagree"
	}
	needs := map[string]bool{}
	for _, e := range g.Edges {
		needs[e.Task+"\x00"+e.Needs] = true
	}
	for i := 1; i < len(tasks); i++ {
		if !needs[tasks[i]+"\x00"+tasks[i-1]] {
			return "the critical path names " + tasks[i-1] + " then " + tasks[i] + ", and the second does not wait on the first"
		}
	}
	return ""
}

// sameAnalysis reports whether two reads of one graph say the same thing.
func sameAnalysis(a, b Graph) bool {
	if a.Levels != b.Levels || a.CriticalPath.Length != b.CriticalPath.Length ||
		strings.Join(a.CriticalPath.Tasks, " ") != strings.Join(b.CriticalPath.Tasks, " ") {
		return false
	}
	if (a.Bottleneck == nil) != (b.Bottleneck == nil) {
		return false
	}
	if a.Bottleneck != nil && *a.Bottleneck != *b.Bottleneck {
		return false
	}
	an, bn := byID(a), byID(b)
	for id, n := range an {
		m, ok := bn[id]
		if !ok || n.Level != m.Level || n.Done != m.Done || n.Ready != m.Ready ||
			n.Blocked != m.Blocked || n.CriticalPathLength != m.CriticalPathLength || n.Downstream != m.Downstream {
			return false
		}
	}
	return len(an) == len(bn)
}

// TestPlanAnalysisRefusesWhatItCannotAnswer: a graph the read cannot answer
// over is refused rather than guessed at -- a cycle, which is no answer at all
// rather than a slow one, and a done set naming something that is not a task of
// the graph.
//
// The cycle half is also the "does not hang" test: the fixture is read the way
// the command reads it (a refusal), and then handed to Analyze directly, which
// is the path a caller that skipped Parse would take. A walk that followed the
// cycle would not return, and the test would die of its timeout rather than
// reporting anything.
func TestPlanAnalysisRefusesWhatItCannotAnswer(t *testing.T) {
	// The command's own read refuses the fixture, naming the cycle.
	if _, err := ReadFile(filepath.Join("testdata", "cycle.json")); err == nil {
		t.Error("the cyclic fixture was read as a graph")
	} else if !strings.Contains(err.Error(), "a -> b -> c -> a") {
		t.Errorf("the read refused the cycle as %q, want it named", err)
	}

	// The same document, decoded without Parse: Analyze refuses it too.
	raw, err := os.ReadFile(filepath.Join("testdata", "cycle.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cyclic Document
	if err := json.Unmarshal(raw, &cyclic); err != nil {
		t.Fatal(err)
	}
	if g, err := Analyze(cyclic, nil); err == nil {
		t.Errorf("Analyze answered a cyclic graph: %+v", g)
	} else if !strings.Contains(err.Error(), "a -> b -> c -> a") {
		t.Errorf("Analyze refused the cycle as %q, want it named", err)
	}

	const valid = `{"schema_version": 1, "plan": "p.md", "nodes": [
		{"id": "a", "title": "first", "footprint": {"prediction": true, "paths": []}},
		{"id": "b", "title": "second", "footprint": {"prediction": true, "paths": []}}],
		"edges": [{"task": "b", "needs": "a", "reason": "b waits for a"}]}`
	for _, tc := range []struct {
		name string
		done []string
		want string
	}{
		{"a done task that is not a node", []string{"ghost"}, `the done set names "ghost", which is not a task of this graph`},
		{"a done set with an empty id", []string{"a", ""}, "the done set names an empty task id"},
		{"a done set with a blank id", []string{"  "}, "the done set names an empty task id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := Parse("graph.json", []byte(valid))
			if err != nil {
				t.Fatal(err)
			}
			if g, err := Analyze(doc, tc.done); err == nil {
				t.Fatalf("Analyze accepted %s: %+v", tc.name, g)
			} else if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("refusal = %q, want it to say %q", err, tc.want)
			}
		})
	}

	// The shortest cycle there is, through Analyze's own guard.
	self := Document{
		Plan:  "p.md",
		Nodes: []Node{{ID: "a", Title: "itself", Footprint: NewFootprint(nil)}},
		Edges: []Edge{{Task: "a", Needs: "a", Reason: "itself"}},
	}
	if _, err := Analyze(self, nil); err == nil || !strings.Contains(err.Error(), "a -> a") {
		t.Errorf("Analyze on a self-edge gave %v, want the cycle a -> a", err)
	}

	// And a document that never went through the read at all: Analyze is
	// exported, so these are the shapes a caller can hand it, and every one of
	// them is a refusal rather than a panic or a wrong answer. The edge cases
	// are the ones that would otherwise be a write to a map that is not there.
	node := Node{ID: "a", Title: "first", Footprint: NewFootprint(nil)}
	for _, tc := range []struct {
		name string
		doc  Document
		want string
	}{
		{"an edge from a node that is not there", Document{Plan: "p.md", Nodes: []Node{node},
			Edges: []Edge{{Task: "ghost", Needs: "a", Reason: "why"}}}, `starts at "ghost"`},
		{"an edge to a node that is not there", Document{Plan: "p.md", Nodes: []Node{node},
			Edges: []Edge{{Task: "a", Needs: "ghost", Reason: "why"}}}, `needs "ghost", which is not a node of this graph`},
		{"two nodes with one id", Document{Plan: "p.md", Nodes: []Node{node, node}, Edges: []Edge{}}, `two nodes carry the id "a"`},
		{"a node with no title", Document{Plan: "p.md",
			Nodes: []Node{{ID: "a", Footprint: NewFootprint(nil)}}, Edges: []Edge{}}, `node "a" has no title`},
		{"a footprint that is not labelled a prediction", Document{Plan: "p.md",
			Nodes: []Node{{ID: "a", Title: "x", Footprint: Footprint{Paths: []string{}}}}, Edges: []Edge{}},
			"not labelled a prediction"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, err := Analyze(tc.doc, nil)
			if err == nil {
				t.Fatalf("Analyze answered a document with %s: %+v", tc.name, g)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("refusal = %q, want it to say %q", err, tc.want)
			}
		})
	}
}

// TestPlanAnalysisEchoesTheArtifactsOwnFields: the read prints the artifact's
// document with its answers added to it, so a renderer needs one document and
// not two -- and the planner's fields arrive unchanged, nulls included.
func TestPlanAnalysisEchoesTheArtifactsOwnFields(t *testing.T) {
	g := analyzeFixture(t, "diamond.json", "a")
	if g.Plan != "docs/plans/diamond.md" {
		t.Errorf("plan = %q, want the plan the artifact names", g.Plan)
	}
	if len(g.Edges) != 4 || g.Edges[0].Reason == "" {
		t.Errorf("edges = %+v, want the artifact's own four edges with their reasons", g.Edges)
	}
	nodes := byID(g)
	if n := nodes["b"]; n.Version == nil || *n.Version != "0.9.0" {
		t.Errorf("b's version = %v, want the artifact's own", n.Version)
	}
	if n := nodes["b"]; n.Run != nil {
		t.Errorf("b's run = %v, want null: a task with no run says so with null", *n.Run)
	}
	if n := nodes["b"]; !n.Footprint.Prediction || n.Footprint.Paths == nil {
		t.Errorf("b's footprint = %+v, want the prediction label and an array", n.Footprint)
	}
}

package plan

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The tests below are GV1.1: the graph's answers in the shape the plan document
// is written in -- the tasks as waves, one downstream count per task, and the
// one critical path -- as functions over a Document rather than as a screen's
// own arithmetic. They sit beside V2.2.3's (analyze_test.go), which asserts the
// same figures through the read's own document, and the two are one
// computation rather than two: the functions here are the read, so a console
// that printed the document and one that called them cannot disagree.
//
// The hub fixture (testdata/hub.json) is the case the whole feature exists for:
// six tasks wait on one hub and five tasks form a chain beside it. The hub has
// the largest count of waiting tasks and the chain is the longest work in the
// plan, so a ranking by the raw count would name the wrong task -- which is
// exactly what D26 keeps the count beside the critical path to avoid.

// parseDoc parses an inline artifact the way the command reads it, and fails
// the test if it is refused.
func parseDoc(t *testing.T, body string) Document {
	t.Helper()
	doc, err := Parse("graph.json", []byte(body))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return doc
}

// readFixture is one of testdata's artifacts, read the way the command reads
// it.
func readFixture(t *testing.T, name string) Document {
	t.Helper()
	doc, err := ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", name, err)
	}
	return doc
}

// cycleDoc is a document with a cycle in it, built by hand: the read refuses
// the fixture before these functions see it, so a caller that skipped the read
// is the only way one of them can be handed a cycle at all.
func cycleDoc() Document {
	return Document{
		SchemaVersion: SchemaVersion,
		Plan:          "docs/plans/cyclic.md",
		Nodes: []Node{
			{ID: "a", Title: "first", Footprint: NewFootprint(nil)},
			{ID: "b", Title: "second", Footprint: NewFootprint(nil)},
			{ID: "c", Title: "third", Footprint: NewFootprint(nil)},
		},
		Edges: []Edge{
			{Task: "a", Needs: "b", Reason: "a waits for b"},
			{Task: "b", Needs: "c", Reason: "b waits for c"},
			{Task: "c", Needs: "a", Reason: "and c waits for a, which is the cycle"},
		},
	}
}

// TestLevels: the waves. A wave is every task whose deepest dependency is one
// wave shorter, each wave in id order, so two readers of one graph draw the
// same plan and a renderer never sorts a wave itself.
//
// The last two cases are the shape's own two edges: a graph with no tasks has
// no waves rather than an empty one it can be asked about, and a cycle has no
// waves at all -- a wave is a function of the graph, so a cyclic graph has none
// to give, and these functions answer with nothing rather than with a drawing
// (C7).
func TestLevels(t *testing.T) {
	for _, tc := range []struct {
		name string
		doc  Document
		want [][]string
	}{
		{
			name: "the diamond's two arms are one wave",
			doc:  readFixture(t, "diamond.json"),
			want: [][]string{{"a"}, {"b", "c"}, {"d"}},
		},
		{
			name: "the hub's leaves are one wave, beside the chain's second link",
			doc:  readFixture(t, "hub.json"),
			want: [][]string{{"a1", "h"}, {"a2", "l1", "l2", "l3", "l4", "l5", "l6"}, {"a3"}, {"a4"}, {"a5"}},
		},
		{
			name: "a graph with no tasks has no waves",
			doc:  parseDoc(t, `{"schema_version": 1, "plan": "p.md", "nodes": [], "edges": []}`),
			want: [][]string{},
		},
		{
			name: "a cycle is refused rather than drawn",
			doc:  cycleDoc(),
			want: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Levels(tc.doc)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Levels = %v, want %v", got, tc.want)
			}
		})
	}

	// The other two answers refuse the same document the same way: a cycle has
	// no count of waiting tasks and no longest chain either, and nil is what
	// "no answer" looks like in each shape.
	cyclic := cycleDoc()
	if got := Downstream(cyclic); got != nil {
		t.Errorf("Downstream(cycle) = %v, want nil: a cycle has no answers to give", got)
	}
	if got := CriticalPathTasks(cyclic); got != nil {
		t.Errorf("CriticalPathTasks(cycle) = %v, want nil: a cycle has no answers to give", got)
	}
}

// TestCriticalPathByCount: the critical path is the longest chain by count --
// counted in tasks, so a diamond's two ways through one task are one task, and
// a task many others wait on is not a long chain. The plan beside the chain has
// a hub with more tasks waiting on it than the chain is long, and the chain is
// the answer.
func TestCriticalPathByCount(t *testing.T) {
	doc := parseDoc(t, `{"schema_version": 1, "plan": "p.md", "nodes": [
		{"id": "a", "title": "a", "footprint": {"prediction": true, "paths": []}},
		{"id": "b", "title": "b", "footprint": {"prediction": true, "paths": []}},
		{"id": "c", "title": "c", "footprint": {"prediction": true, "paths": []}},
		{"id": "d", "title": "d", "footprint": {"prediction": true, "paths": []}},
		{"id": "e", "title": "e", "footprint": {"prediction": true, "paths": []}},
		{"id": "h", "title": "h", "footprint": {"prediction": true, "paths": []}},
		{"id": "q1", "title": "q1", "footprint": {"prediction": true, "paths": []}},
		{"id": "q2", "title": "q2", "footprint": {"prediction": true, "paths": []}},
		{"id": "q3", "title": "q3", "footprint": {"prediction": true, "paths": []}},
		{"id": "q4", "title": "q4", "footprint": {"prediction": true, "paths": []}},
		{"id": "q5", "title": "q5", "footprint": {"prediction": true, "paths": []}}], "edges": [
		{"task": "b", "needs": "a", "reason": "b waits for a"},
		{"task": "c", "needs": "a", "reason": "c waits for a"},
		{"task": "d", "needs": "b", "reason": "d waits for b"},
		{"task": "d", "needs": "c", "reason": "d waits for c"},
		{"task": "e", "needs": "d", "reason": "e waits for d"},
		{"task": "q1", "needs": "h", "reason": "q1 waits for h"},
		{"task": "q2", "needs": "h", "reason": "q2 waits for h"},
		{"task": "q3", "needs": "h", "reason": "q3 waits for h"},
		{"task": "q4", "needs": "h", "reason": "q4 waits for h"},
		{"task": "q5", "needs": "h", "reason": "q5 waits for h"}]}`)

	got := CriticalPathTasks(doc)
	want := []string{"a", "b", "d", "e"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("CriticalPathTasks = %v, want %v: the longest chain by count, counted in tasks", got, want)
	}
	// The chain is real: each task after the first waits on the one before it.
	needs := map[string]bool{}
	for _, e := range doc.Edges {
		needs[e.Task+"\x00"+e.Needs] = true
	}
	for i := 1; i < len(got); i++ {
		if !needs[got[i]+"\x00"+got[i-1]] {
			t.Errorf("the chain names %s then %s, and the second does not wait on the first", got[i-1], got[i])
		}
	}
	// The hub: five tasks wait on it and its chain is two tasks long, so a
	// ranking by how many tasks wait would name it and a count of tasks does
	// not.
	if down := Downstream(doc); down["h"] <= len(got) {
		t.Errorf("the fixture no longer makes its point: the hub's %d waiting tasks is not more than the chain's %d", down["h"], len(got))
	}
}

// TestCriticalPathTieBreak: two chains of one length, so the tie is the whole
// assertion. The smallest id at each step decides -- not the order the document
// lists the tasks in, and not how many tasks wait on either head, which is a
// different question (D26). Two reads of one graph therefore name one chain.
func TestCriticalPathTieBreak(t *testing.T) {
	doc := parseDoc(t, `{"schema_version": 1, "plan": "p.md", "nodes": [
		{"id": "bb1", "title": "bb1", "footprint": {"prediction": true, "paths": []}},
		{"id": "bb2", "title": "bb2", "footprint": {"prediction": true, "paths": []}},
		{"id": "q", "title": "q", "footprint": {"prediction": true, "paths": []}},
		{"id": "aa1", "title": "aa1", "footprint": {"prediction": true, "paths": []}},
		{"id": "aa2", "title": "aa2", "footprint": {"prediction": true, "paths": []}}], "edges": [
		{"task": "bb2", "needs": "bb1", "reason": "bb2 waits for bb1"},
		{"task": "aa2", "needs": "aa1", "reason": "aa2 waits for aa1"},
		{"task": "q", "needs": "bb1", "reason": "q waits for bb1 too"}]}`)

	got := CriticalPathTasks(doc)
	want := []string{"aa1", "aa2"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("CriticalPathTasks = %v, want %v: the ids decide the tie, not the document's order or the count of waiting tasks", got, want)
	}
	// bb1 is the head the count would name: two tasks wait on it and one on aa1.
	if down := Downstream(doc); down["bb1"] <= down["aa1"] {
		t.Errorf("the fixture no longer makes its point: bb1 has %d waiting tasks and aa1 has %d", down["bb1"], down["aa1"])
	}
}

// TestHubIsNotTheBottleneck: the hub fixture, asserted on the fixture rather
// than on an inline graph. A task six others wait on has the largest downstream
// count in the plan and is not on the critical path: the count answers how many
// tasks wait, and the path answers how long the work in front of a task is. A
// ranking that let the count decide would name a task that is not on the chain
// at all.
func TestHubIsNotTheBottleneck(t *testing.T) {
	doc := readFixture(t, "hub.json")

	down := Downstream(doc)
	hub := down["h"]
	for id, n := range down {
		if n > hub {
			t.Errorf("%s has %d tasks waiting on it and the hub has %d; the fixture's hub is no longer the biggest count", id, n, hub)
		}
	}

	path := CriticalPathTasks(doc)
	want := []string{"a1", "a2", "a3", "a4", "a5"}
	if strings.Join(path, " ") != strings.Join(want, " ") {
		t.Errorf("CriticalPathTasks = %v, want %v: the longest chain by count", path, want)
	}
	if contains(path, "h") {
		t.Errorf("the critical path is %v, and it runs through the hub: %d tasks wait on the hub and %d tasks are the longest work",
			path, hub, len(path))
	}
	if hub <= len(path) {
		t.Errorf("the fixture no longer makes its point: the hub's %d waiting tasks is not more than the chain's %d", hub, len(path))
	}
}

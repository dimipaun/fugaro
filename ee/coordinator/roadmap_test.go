// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package coordinator

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/coordpath"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// The roadmap record, as P9a builds it: four kinds of document under
// coordinator/project/roadmap, each keyed by coordpath.DocumentID over what it
// describes, and the read that answers them. Nothing here contacts Firebase —
// the database is internal/gcpfake's — and nothing here writes a document: the
// writers are P9b.

// roadmapFixture is the record testdata/roadmap.json holds: one project's
// plans, releases and cross-plan edges.
//
// The assignments are not in the fixture, and that is not an omission: an
// assignment's `release` is a release's key — a hash — and the repository's
// Convention 2 keeps a secret-shaped literal out of a fixture. They are built
// from the fixture's own releases instead (roadmapFixture.assignments), which
// is the same reference computed rather than written down.
type roadmapFixture struct {
	Plans    []Plan    `json:"plans"`
	Releases []Release `json:"releases"`
	Edges    []Edge    `json:"edges"`
}

// roadmapFixtureAt is the instant every document the fixture's helpers build
// is stamped with, so a test compares two of them without a clock.
var roadmapFixtureAt = time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)

// readRoadmapFixture reads the fixture strictly, so a field it carries that
// this build does not know fails the test rather than being dropped on the way
// in.
func readRoadmapFixture(t *testing.T) roadmapFixture {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "roadmap.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fx roadmapFixture
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&fx); err != nil {
		t.Fatalf("testdata/roadmap.json does not decode into the record's documents: %v", err)
	}
	return fx
}

// assignments is where the fixture's tasks ship: each plan's first task in the
// release its stream has open. A plan whose stream has no release — the cli
// plan of the fixture — has no assignment at all, which is what unassigned
// means (D2114).
func (fx roadmapFixture) assignments() []Assignment {
	var out []Assignment
	for _, p := range fx.Plans {
		if len(p.Tasks) == 0 {
			continue
		}
		for _, rel := range fx.Releases {
			if rel.Stream != p.Stream || rel.State != ReleaseOpen {
				continue
			}
			out = append(out, Assignment{
				Schema:  RoadmapSchema,
				Task:    p.Plan + "/" + p.Tasks[0].ID,
				Release: ReleaseID(rel.Stream, rel.ID),
				By:      "person-1@example.com",
				At:      roadmapFixtureAt,
			})
		}
	}
	return out
}

// seedRoadmapDocuments puts the fixture's plans, releases and edges under the
// paths this file's own helpers derive, so the read meets the record's node
// layout rather than one the test invented.
func seedRoadmapDocuments(t *testing.T, f *gcpfake.RTDB) roadmapFixture {
	t.Helper()
	fx := readRoadmapFixture(t)
	for _, p := range fx.Plans {
		f.Set(PathPlan(p.Plan), p)
	}
	for _, rel := range fx.Releases {
		f.Set(PathRelease(rel.Stream, rel.ID), rel)
	}
	for _, e := range fx.Edges {
		f.Set(PathEdge(e.Task, e.Needs), e)
	}
	return fx
}

// seedRoadmap is the fixture whole: its documents and the assignments its
// releases carry.
func seedRoadmap(t *testing.T, f *gcpfake.RTDB) roadmapFixture {
	t.Helper()
	fx := seedRoadmapDocuments(t, f)
	for _, a := range fx.assignments() {
		f.Set(PathAssign(a.Task), a)
	}
	return fx
}

// roadmapPtr is a pointer to v, for the record's optional fields.
func roadmapPtr[T any](v T) *T { return &v }

// requireNoNullLists walks v through its own JSON and fails for every list it
// finds marshalled as null, naming where it found it. It reads the wire shape
// rather than the Go value on purpose: a nil slice is exactly what a consumer
// of the record's document sees, and a reader that iterates one throws where a
// Go caller merely ranges over nothing.
//
// It walks the struct's fields rather than naming the lists, because a rule
// that named the fields it knows about would pass the day a later shape adds
// one, which is the day it would matter.
func requireNoNullLists(t *testing.T, v any, where string) {
	t.Helper()
	blob, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("%s does not marshal: %v", where, err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(blob, &fields); err != nil {
		t.Fatalf("%s does not marshal to an object: %v", where, err)
	}
	rv, rt := reflect.ValueOf(v), reflect.TypeOf(v)
	for i := range rt.NumField() {
		f := rt.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		raw, ok := fields[name]
		if name == "" || name == "-" || !ok {
			continue
		}
		switch f.Type.Kind() {
		case reflect.Slice:
			if string(raw) == "null" {
				t.Errorf("%s marshals %s as null, want the empty list it means: the record declares a list and never a null a reader has to interpret (nonNil)", where, name)
			}
			if f.Type.Elem().Kind() == reflect.Struct {
				for j := range rv.Field(i).Len() {
					requireNoNullLists(t, rv.Field(i).Index(j).Interface(), fmt.Sprintf("%s.%s[%d]", where, name, j))
				}
			}
		case reflect.Struct:
			// Only an object is walked: a struct that marshals to a scalar --
			// a time.Time, which is a string on the wire -- has no fields to
			// ask about, and decoding its JSON as a map would fail.
			if len(raw) > 0 && raw[0] == '{' {
				requireNoNullLists(t, rv.Field(i).Interface(), where+"."+name)
			}
		}
	}
}

// TestRoadmapDocumentsRoundTripStrictly: the record's four kinds of document
// survive a write and a read unchanged, and the read is strict — a document
// carrying a field this build does not know is refused rather than read with
// the field dropped.
//
// Strictness is the half that matters. A dropped field is a figure the record
// holds and the screen does not draw, with no error anywhere, and the record's
// documents are written by whoever holds the write scope: a reader that
// guessed at an unknown field would be guessing at another writer's meaning.
//
// Hand mutation this fails on: dropping DisallowUnknownFields from the
// decoder, which is the strict-decode half of P9a's red step.
func TestRoadmapDocumentsRoundTripStrictly(t *testing.T) {
	s, f := testStoreAndFake(t)
	fx := seedRoadmap(t, f)
	got, err := s.ReadRoadmap(testCtx(t))
	if err != nil {
		t.Fatal(err)
	}
	if got.Unreadable != 0 {
		t.Errorf("the fixture read as %d unreadable document(s), want none: %+v", got.Unreadable, got)
	}
	if len(got.Plans) != len(fx.Plans) || len(got.Releases) != len(fx.Releases) || len(got.Edges) != len(fx.Edges) {
		t.Fatalf("the record read as %d plans, %d releases and %d edges, want the fixture's %d, %d and %d",
			len(got.Plans), len(got.Releases), len(got.Edges), len(fx.Plans), len(fx.Releases), len(fx.Edges))
	}
	if len(got.Assignments) != len(fx.assignments()) {
		t.Errorf("the record read as %d assignments, want the fixture's %d", len(got.Assignments), len(fx.assignments()))
	}
	if len(got.Dangling) != 0 {
		t.Errorf("the fixture's own record read as dangling %q, want none: every endpoint is a plan document's", got.Dangling)
	}
	for _, doc := range []any{got.Plans[0], got.Releases[0], got.Assignments[0], got.Edges[0]} {
		raw, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		out := reflect.New(reflect.TypeOf(doc))
		if err := strictDecode(raw, out.Interface()); err != nil {
			t.Errorf("%T does not survive a strict round trip: %v", doc, err)
			continue
		}
		if back := out.Elem().Interface(); !reflect.DeepEqual(doc, back) {
			t.Errorf("%T round-tripped as %+v, want %+v", doc, back, doc)
		}
	}
	var plan map[string]any
	raw, err := json.Marshal(got.Plans[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	plan["footprint"] = map[string]any{"paths": []string{"internal/cli"}}
	extra, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	var back Plan
	if err := strictDecode(extra, &back); err == nil {
		t.Error("a plan document carrying a field this build does not know decoded: the record's documents are read strictly, and an unknown field is a refusal rather than a field dropped")
	}
}

// TestRoadmapKeysAreDeterministic: a document's key is a function of what it
// describes and of nothing else — no clock, no counter, no random source — so
// the same thing described twice is one node (Q6 A's idempotency, finding
// #17's house idiom). Another plan, task, stream or direction is another key,
// and a plan's provenance is in no key at all: it is provenance, and a plan
// republished from another checkout would otherwise be a second document.
//
// Hand mutation this fails on: mixing a timestamp or a counter into a key,
// which is the mutation that makes a second write of one plan a second node.
func TestRoadmapKeysAreDeterministic(t *testing.T) {
	if PlanID("plans/x") != PlanID("plans/x") {
		t.Error("PlanID is not a function of the plan id: two calls answered two keys")
	}
	for _, c := range []struct {
		name string
		a, b string
	}{
		{"PlanID", PlanID("plans/x"), PlanID("plans/y")},
		{"ReleaseID by stream", ReleaseID("console", "0.110"), ReleaseID("mobile", "0.110")},
		{"ReleaseID by id", ReleaseID("console", "0.110"), ReleaseID("console", "0.111")},
		{"AssignID", AssignID("plans/x/T1"), AssignID("plans/x/T2")},
		{"EdgeID by endpoint", EdgeID("plans/x/T1", "plans/y/T2"), EdgeID("plans/x/T1", "plans/y/T3")},
		{"EdgeID by direction", EdgeID("plans/x/T1", "plans/y/T2"), EdgeID("plans/y/T2", "plans/x/T1")},
	} {
		if c.a == c.b {
			t.Errorf("%s answered one key for two different things: %q", c.name, c.a)
		}
	}
	// The provenance is in no key, and the record holds one node for two plans
	// that differ only in where they were published from.
	s, f := testStoreAndFake(t)
	here := Plan{Schema: RoadmapSchema, Plan: "plans/x", Stream: "cli",
		Source: PlanSource{Repo: "acme/one", Path: "docs/x.graph.json", Commit: "1111"},
		Tasks:  []Task{}, Edges: []PlanEdge{}}
	there := here
	there.Source = PlanSource{Repo: "acme/two", Path: "docs/x.graph.json", Commit: "2222"}
	if PathPlan(here.Plan) != PathPlan(there.Plan) {
		t.Fatalf("two plans of one id are two nodes: %q and %q", PathPlan(here.Plan), PathPlan(there.Plan))
	}
	f.Set(PathPlan(here.Plan), here)
	f.Set(PathPlan(there.Plan), there)
	got, err := s.ReadRoadmap(testCtx(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Plans) != 1 {
		t.Errorf("the record holds %d plan nodes for one plan id, want one: the provenance is in no key", len(got.Plans))
	}
}

// TestRoadmapStreamsAreReadFromTheReleases: the record's streams are the
// `stream` values its releases carry and nothing else (D2113), so a project
// whose repositories carry no release catalogue works exactly the same way and
// no catalogue file is read anywhere in this package.
//
// The fixture's cli plan is what makes the first half bite: a plan names its
// own stream, and that stream is in the record only because a release carries
// it.
//
// Hand mutation this fails on: reading the streams from a catalogue file —
// docs/release-streams.yaml, which exists in the Fugaro repository alone —
// which the walk over this package's files refuses.
func TestRoadmapStreamsAreReadFromTheReleases(t *testing.T) {
	s, f := testStoreAndFake(t)
	fx := seedRoadmap(t, f)
	got, err := s.ReadRoadmap(testCtx(t))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"console", "mobile"}; !slices.Equal(got.Streams(), want) {
		t.Errorf("the record's streams read as %q, want %q: they are its releases' `stream` values", got.Streams(), want)
	}
	for _, p := range fx.Plans {
		if p.Stream == "cli" && slices.Contains(got.Streams(), "cli") {
			t.Errorf("the plan %s names the stream %q and the record holds no release in it: a plan's own stream is a display input and is never a stream of the record", p.Plan, p.Stream)
		}
	}
	// The scan is over the import path in quotes and over the catalogue's file
	// name, not over the words: this package's own comments name
	// internal/streams to say what a plan artifact's stream is, and a scan that
	// refused its own prose would be a scan nobody could keep true.
	streamsImport := `"github.com/dimipaun/fugaro/internal/` + "streams" + `"`
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no Go file of this package was walked, so the scan below proves nothing")
	}
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		body := string(data)
		if strings.Contains(body, streamsImport) {
			t.Errorf("%s imports the release-streams catalogue: the record's streams are the `stream` values its releases carry (D2113), so a repository without such a file loses nothing", path)
		}
		if !strings.HasSuffix(path, "_test.go") && strings.Contains(body, "release-streams") {
			t.Errorf("%s names a release-streams catalogue, which is a fact of one repository and not of the record", path)
		}
	}
}

// TestRoadmapSkipsANewerSchemaAndCountsIt: a document of a shape this build
// does not read is skipped and counted, never read as if it were this build's
// shape and never fatal to the read. A field never changes meaning under a
// version, so a document that says it is a later schema is not read whatever
// its fields happen to look like.
//
// Hand mutation this fails on: decoding leniently — dropping the schema check
// — which is P9a's own red step. The later document here decodes into this
// build's Plan shape on purpose, so nothing but the version stands between it
// and a screen drawing it.
func TestRoadmapSkipsANewerSchemaAndCountsIt(t *testing.T) {
	s, f := testStoreAndFake(t)
	f.Set(PathPlan("plans/now"), Plan{Schema: RoadmapSchema, Plan: "plans/now", Stream: "cli",
		Tasks: []Task{{ID: "N1", Title: "the shape this build writes", Version: roadmapPtr("0.10")}}, Edges: []PlanEdge{}})
	f.Set(PathPlan("plans/later"), map[string]any{
		"schema": RoadmapSchema + 1,
		"plan":   "plans/later",
		"stream": "cli",
		"source": map[string]any{"repo": "acme/app", "path": "docs/later.graph.json", "commit": "1111", "graph_schema": 4},
		"tasks":  []any{map[string]any{"id": "L1", "title": "the shape a later release writes", "version": "0.11"}},
		"edges":  []any{},
	})
	got, err := s.ReadRoadmap(testCtx(t))
	if err != nil {
		t.Fatal(err)
	}
	if got.Unreadable != 1 {
		t.Errorf("the record read as %d unreadable document(s), want the one of a later schema: %+v", got.Unreadable, got)
	}
	if len(got.Plans) != 1 || got.Plans[0].Plan != "plans/now" {
		t.Errorf("the record read its plans as %+v, want the one document of this schema", got.Plans)
	}
}

// TestRoadmapUndecodableDocumentIsCountedNotFatal: one document this build
// cannot read is one node skipped, not a read that fails and not a document
// read as empty. A hand-written document, an older build's or one whose field
// is of the wrong type must not blank a screen that could draw everything
// else — and must not be reported as a plan with nothing in it either, which
// would claim the record holds nothing where it holds something this build
// does not understand.
//
// Hand mutation this fails on: returning the read's own error for one bad
// document, and reading it as an empty one instead of counting it.
func TestRoadmapUndecodableDocumentIsCountedNotFatal(t *testing.T) {
	s, f := testStoreAndFake(t)
	f.Set(PathPlan("plans/good"), Plan{Schema: RoadmapSchema, Plan: "plans/good", Stream: "cli",
		Tasks: []Task{{ID: "G1", Title: "a plan this build wrote", Version: roadmapPtr("0.10")}}, Edges: []PlanEdge{}})
	f.Set(PathPlan("plans/hand-written"), map[string]any{
		"schema":    RoadmapSchema,
		"plan":      "plans/hand-written",
		"stream":    "cli",
		"footprint": map[string]any{"paths": []string{"internal/cli"}},
		"tasks":     []any{},
		"edges":     []any{},
	})
	f.Set(PathPlan("plans/wrong-type"), map[string]any{
		"schema": RoadmapSchema,
		"plan":   "plans/wrong-type",
		"stream": "cli",
		"tasks":  "none",
		"edges":  []any{},
	})
	got, err := s.ReadRoadmap(testCtx(t))
	if err != nil {
		t.Fatalf("one undecodable document failed the whole read: %v", err)
	}
	if got.Unreadable != 2 {
		t.Errorf("the record read as %d unreadable document(s), want the two it cannot read: %+v", got.Unreadable, got)
	}
	if len(got.Plans) != 1 || got.Plans[0].Plan != "plans/good" {
		t.Errorf("the record read its plans as %+v, want the one document this build can read", got.Plans)
	}
	// A node that is not the record at all is the read's own failure and not a
	// document's: the four collection names are this build's, and there is no
	// plan, release, assignment or edge in a node that is not an object. The
	// refusal still carries ErrUnreadable, so a caller can tell data it cannot
	// read from a database it could not reach.
	f.Set(PathRoadmap(), "not a record")
	if _, err := s.ReadRoadmap(testCtx(t)); !errors.Is(err, ErrUnreadable) {
		t.Errorf("reading a node that is not the record answered %v, want a refusal carrying ErrUnreadable", err)
	}
}

// TestRoadmapDanglingEndpointIsNamed: an assignment or an edge whose endpoint
// no plan document carries is dangling, and so is an assignment naming a
// release the record does not hold (D2115). They are kept and named rather
// than dropped — a task leaves every plan when a publish drops it or an
// unpublish removes its plan, and the prune that deletes the leftovers is a
// writer's (D2125) — and the name is the qualified task id, which is what a
// screen marks as dangling.
//
// Hand mutation this fails on: dropping the assignments and edges whose
// endpoints are gone, or reporting them without naming what dangles.
func TestRoadmapDanglingEndpointIsNamed(t *testing.T) {
	s, f := testStoreAndFake(t)
	f.Set(PathPlan("plans/p1"), Plan{Schema: RoadmapSchema, Plan: "plans/p1", Stream: "cli",
		Tasks: []Task{{ID: "T1", Title: "one", Version: roadmapPtr("0.10")}}, Edges: []PlanEdge{}})
	f.Set(PathRelease("cli", "0.10"), Release{Schema: RoadmapSchema, ID: "0.10", Stream: "cli", Name: "Badger",
		State: ReleaseOpen, Order: 1, By: "person-1@example.com", At: roadmapFixtureAt})
	f.Set(PathEdge("plans/p1/T1", "plans/gone/T9"), Edge{Schema: RoadmapSchema, Task: "plans/p1/T1", Needs: "plans/gone/T9",
		Reason: "the plan that carried it was unpublished", By: "person-1@example.com", At: roadmapFixtureAt})
	f.Set(PathAssign("plans/gone/T9"), Assignment{Schema: RoadmapSchema, Task: "plans/gone/T9",
		Release: ReleaseID("cli", "0.10"), By: "person-1@example.com", At: roadmapFixtureAt})
	f.Set(PathAssign("plans/p1/T1"), Assignment{Schema: RoadmapSchema, Task: "plans/p1/T1",
		Release: "a release the record does not hold", By: "person-1@example.com", At: roadmapFixtureAt})

	got, err := s.ReadRoadmap(testCtx(t))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"plans/gone/T9", "plans/p1/T1"}; !slices.Equal(got.Dangling, want) {
		t.Errorf("the record read as dangling %q, want %q", got.Dangling, want)
	}
	if len(got.Assignments) != 2 || len(got.Edges) != 1 {
		t.Errorf("the record kept %d assignment(s) and %d edge(s), want the two and the one it holds: a dangling endpoint is reported, not dropped",
			len(got.Assignments), len(got.Edges))
	}
}

// TestRoadmapUnassignedTaskHasNoDocument: a task nobody has moved is
// unassigned (D2114) — nothing is written into the record for it, and the
// read reports no assignment rather than inventing one from the plan's own
// version. That version is a display input beside the task, marked "from the
// plan", and an unassigned task is not dangling either: it is a task the
// record has nothing to say about, not a task the record cannot place.
//
// Hand mutation this fails on: seeding an assignment from a plan's `version`
// on read, which is the rule D2114 refuses.
func TestRoadmapUnassignedTaskHasNoDocument(t *testing.T) {
	s, f := testStoreAndFake(t)
	fx := seedRoadmapDocuments(t, f)
	first := fx.assignments()[0]
	f.Set(PathAssign(first.Task), first)

	const unassigned = "plans/2026-11-02-mobile-auth/M1"
	if v := f.Value(PathAssign(unassigned)); v != nil {
		t.Errorf("the fixture wrote a document for the unassigned task %s: %v", unassigned, v)
	}
	got, err := s.ReadRoadmap(testCtx(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Assignments) != 1 || got.Assignments[0].Task != first.Task {
		t.Fatalf("the record read its assignments as %+v, want the one it holds", got.Assignments)
	}
	for _, plan := range got.Plans {
		for _, task := range plan.Tasks {
			if plan.Plan+"/"+task.ID != unassigned {
				continue
			}
			if task.Version == nil || *task.Version != "0.200" {
				t.Errorf("the unassigned task's own version read as %v, want the plan's 0.200 beside it", task.Version)
			}
		}
	}
	if len(got.Dangling) != 0 {
		t.Errorf("an unassigned task read as dangling %q: a task with no assignment is not one the record cannot place", got.Dangling)
	}
}

// TestRoadmapTextIsSanitisedOnRead: every text a person reads — a task's
// title, a release's name, an edge's reason, a `by` — is a field on the way
// out exactly as it is on the way in. The document is written by whoever holds
// the write scope and drawn by a console, a CLI or a log, so the read is where
// a screen that never sanitised anything is still safe.
//
// Hand mutation this fails on: returning the stored text as it is, which puts
// an escape sequence on a terminal from a document nobody in this process
// wrote.
func TestRoadmapTextIsSanitisedOnRead(t *testing.T) {
	s, f := testStoreAndFake(t)
	const (
		hostileTitle = "the graph\x1b[31m's\x1b[0m document and its key"
		hostileName  = "Cherry\x07​"
		hostileBy    = "person-1​@example.com"
		hostileWhy   = "reads\x1b[2Jthe document shape the console plan wrote"
	)
	f.Set(PathPlan("plans/p1"), Plan{Schema: RoadmapSchema, Plan: "plans/p1", Stream: "cli",
		Tasks: []Task{{ID: "T1", Title: hostileTitle, Version: roadmapPtr("0.10")}}, Edges: []PlanEdge{}})
	f.Set(PathRelease("cli", "0.10"), Release{Schema: RoadmapSchema, ID: "0.10", Stream: "cli", Name: hostileName,
		State: ReleaseOpen, Order: 1, By: hostileBy, At: roadmapFixtureAt})
	f.Set(PathEdge("plans/p1/T1", "plans/p2/T2"), Edge{Schema: RoadmapSchema, Task: "plans/p1/T1", Needs: "plans/p2/T2",
		Reason: hostileWhy, By: hostileBy, At: roadmapFixtureAt})
	f.Set(PathAssign("plans/p1/T1"), Assignment{Schema: RoadmapSchema, Task: "plans/p1/T1",
		Release: ReleaseID("cli", "0.10"), By: hostileBy, At: roadmapFixtureAt})

	got, err := s.ReadRoadmap(testCtx(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Plans) != 1 || len(got.Releases) != 1 || len(got.Edges) != 1 || len(got.Assignments) != 1 {
		t.Fatalf("the record read as %+v, want one document of each kind", got)
	}
	for _, c := range []struct {
		what     string
		stored   string
		readBack string
	}{
		{"a task's title", hostileTitle, got.Plans[0].Tasks[0].Title},
		{"a release's name", hostileName, got.Releases[0].Name},
		{"a release's by", hostileBy, got.Releases[0].By},
		{"an edge's reason", hostileWhy, got.Edges[0].Reason},
		{"an edge's by", hostileBy, got.Edges[0].By},
		{"an assignment's by", hostileBy, got.Assignments[0].By},
	} {
		if want := safetext.Field(c.stored); c.readBack != want {
			t.Errorf("%s read back as %q, want safetext.Field of the stored %q, which is %q", c.what, c.readBack, c.stored, want)
		}
		if c.readBack == c.stored {
			t.Errorf("%s read back as it was stored (%q): the text of a document this process did not write is a field", c.what, c.readBack)
		}
	}
}

// TestRoadmapListsAreNeverNull: every list the record declares is a list on
// the way out, whether or not the record holds anything in it. An empty list
// and a null are two spellings of one fact to a Go caller and two different
// documents to a consumer: a screen that iterates the record's `dangling`
// throws on a null, and the console accepts a newer CLI on this contract, so a
// read that answered null for the ordinary case — a record holding plans with
// nothing dangling — would be a defect no Go reader sees.
//
// The three reads that answer a list with nothing in it are the three a test
// that seeds a document never reaches: a populated record with nothing
// dangling, a project whose record was never written, and a hand-written plan
// document that omits its own lists. A node is not only what this build's
// writer puts in it, and the sibling reader keeps the same promise through
// cleanList (TestReadRepoIntentsCleansTheListsItReads).
//
// Hand mutation this fails on: returning danglingIDs' slice as it is built,
// which is nil whenever nothing dangles, and dropping the nonNil pair in
// Plan.sanitised.
func TestRoadmapListsAreNeverNull(t *testing.T) {
	t.Run("a populated record with nothing dangling", func(t *testing.T) {
		s, f := testStoreAndFake(t)
		seedRoadmap(t, f)
		got, err := s.ReadRoadmap(testCtx(t))
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Dangling) != 0 {
			t.Fatalf("the fixture's record read as dangling %q, so this is not the ordinary case it means to pin", got.Dangling)
		}
		requireNoNullLists(t, got, "the record")
	})

	t.Run("an absent record", func(t *testing.T) {
		s, _ := testStoreAndFake(t)
		got, err := s.ReadRoadmap(testCtx(t))
		if err != nil {
			t.Fatal(err)
		}
		requireNoNullLists(t, got, "the absent record")
		blob, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		// The one spelling, said out loud: the two branches of the read -- the
		// record found and the record absent -- answer the same document.
		if !strings.Contains(string(blob), `"dangling":[]`) {
			t.Errorf("an absent record marshals as %s, want \"dangling\":[]", blob)
		}
	})

	t.Run("a plan document that omits its own lists", func(t *testing.T) {
		s, f := testStoreAndFake(t)
		f.Set(PathPlan("plans/hand-written"), map[string]any{
			"schema": RoadmapSchema,
			"plan":   "plans/hand-written",
			"stream": "cli",
			"source": map[string]any{
				"repo": "acme/app", "path": "docs/hand-written.graph.json",
				"commit": "1111", "graph_schema": 3,
			},
		})
		got, err := s.ReadRoadmap(testCtx(t))
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Plans) != 1 {
			t.Fatalf("the record read its plans as %+v, want the one hand-written document", got.Plans)
		}
		requireNoNullLists(t, got.Plans[0], "the hand-written plan")
	})
}

// TestRoadmapPathsStayInTheSubtree: every path helper answers a path under the
// coordinator's subtree, under the record's own node, with no empty segment —
// and the ids are hashes, so a plan id's own slashes are not in the path. The
// writer that holds a database-wide role has no IAM fence around it (D2118),
// so a path built out of a caller's text is the one thing that must not be
// able to leave the record.
//
// Hand mutation this fails on: building a path out of an id rather than out of
// its hash, which lets a plan id's `../` climb out of the subtree.
func TestRoadmapPathsStayInTheSubtree(t *testing.T) {
	paths := []struct {
		name string
		path string
	}{
		{"PathRoadmap", PathRoadmap()},
		{"PathPlans", PathPlans()},
		{"PathPlan", PathPlan("plans/2026-10-15-desktop-console-v2")},
		{"PathReleases", PathReleases()},
		{"PathRelease", PathRelease("console", "0.110")},
		{"PathAssigns", PathAssigns()},
		{"PathAssign", PathAssign("plans/2026-10-15-desktop-console-v2/V2.5.3")},
		{"PathEdges", PathEdges()},
		{"PathEdge", PathEdge("plans/a/T1", "plans/b/T2")},
	}
	for _, c := range paths {
		if !strings.HasPrefix(c.path, coordpath.PathRoot+"/") {
			t.Errorf("%s = %q, which is outside the coordinator's subtree %q", c.name, c.path, coordpath.PathRoot)
		}
		if !strings.HasPrefix(c.path, coordpath.PathProject+"/") {
			t.Errorf("%s = %q, which is not a project-wide document: the record is project-level (D2111)", c.name, c.path)
		}
		if !strings.HasPrefix(c.path, PathRoadmap()) {
			t.Errorf("%s = %q, which is beside the record rather than in it", c.name, c.path)
		}
		if strings.Contains(c.path, "//") || strings.HasSuffix(c.path, "/") {
			t.Errorf("%s = %q, which holds an empty segment", c.name, c.path)
		}
	}
	// The ids are hashes: a plan id is a path under docs/ and its slashes must
	// not reach the database, where a slash is a segment and `..` climbs.
	for _, c := range []struct {
		name string
		path string
	}{{"PathPlan", PathPlan("plans/../secret")}, {"PathAssign", PathAssign("../../config/kill")}} {
		parent, key := filepath.Split(c.path)
		parent = strings.TrimSuffix(parent, "/")
		if strings.ContainsAny(key, "./$#[]") {
			t.Errorf("%s = %q, whose key %q holds a character the database refuses in a node name", c.name, c.path, key)
		}
		if parent != PathPlans() && parent != PathAssigns() {
			t.Errorf("%s = %q, whose parent is %q rather than the collection it belongs to", c.name, c.path, parent)
		}
	}
	collections := []string{PathPlans(), PathReleases(), PathAssigns(), PathEdges()}
	for i := range collections {
		for j := range collections {
			if i != j && collections[i] == collections[j] {
				t.Errorf("two collections share the node %q: the four kinds of document are four collections", collections[i])
			}
		}
	}
}

// TestQualifiedIDSplitsAtTheLastSlash: a qualified task id is a plan id and a
// task id, and the split is at the LAST slash — a plan id is a path under
// docs/, so plans/2026-10-15-desktop-console-v2 is one plan and not two. A
// value that does not split into two non-empty halves names no task, and a
// caller that joined one anyway would be pointing at a node nothing wrote.
func TestQualifiedIDSplitsAtTheLastSlash(t *testing.T) {
	plan, task, ok := SplitQualifiedID("plans/2026-10-15-desktop-console-v2/V2.5.3")
	if !ok || plan != "plans/2026-10-15-desktop-console-v2" || task != "V2.5.3" {
		t.Errorf("SplitQualifiedID = (%q, %q, %v), want (plans/2026-10-15-desktop-console-v2, V2.5.3, true)", plan, task, ok)
	}
	if plan, task, ok := SplitQualifiedID("plans/p/T/1"); !ok || plan != "plans/p/T" || task != "1" {
		t.Errorf("SplitQualifiedID(plans/p/T/1) = (%q, %q, %v), want the split at the last slash", plan, task, ok)
	}
	for _, bad := range []string{"", "V2.5.3", "/V2.5.3", "plans/p/", "/"} {
		if plan, task, ok := SplitQualifiedID(bad); ok {
			t.Errorf("SplitQualifiedID(%q) = (%q, %q, true), want no qualified id", bad, plan, task)
		}
	}
}

// TestReleaseIDStreamAndNameShapes: D2120's bounds, which P9d validates a
// tool's arguments with before P9b writes. Each is anchored, so nothing before
// or after the pattern gets in — a leading hyphen (which would reach argv as a
// flag), a path separator, an upper-case letter, whitespace — and a name is
// bounded in characters rather than in bytes, because it is a label a person
// reads and not a key.
func TestReleaseIDStreamAndNameShapes(t *testing.T) {
	for _, c := range []struct {
		id string
		ok bool
	}{
		{"0.110", true}, {"v1", true}, {"a", true}, {"a.b-c", true},
		{"1.0.0-rc.1", true}, {strings.Repeat("a", 40), true},
		{"", false}, {"-a", false}, {".a", false}, {"A", false},
		{"a b", false}, {"a/b", false}, {"a_b", false}, {strings.Repeat("a", 41), false},
	} {
		if got := ValidReleaseID(c.id); got != c.ok {
			t.Errorf("ValidReleaseID(%q) = %v, want %v", c.id, got, c.ok)
		}
	}
	for _, c := range []struct {
		name string
		ok   bool
	}{
		{"cli", true}, {"console", true}, {"mobile", true}, {"a-b", true},
		{strings.Repeat("a", 32), true},
		{"", false}, {"-a", false}, {"A", false}, {"a_b", false},
		{"a.b", false}, {"a b", false}, {strings.Repeat("a", 33), false},
	} {
		if got := ValidStreamName(c.name); got != c.ok {
			t.Errorf("ValidStreamName(%q) = %v, want %v", c.name, got, c.ok)
		}
	}
	for _, c := range []struct {
		name string
		ok   bool
	}{
		{"Cherry", true}, {"Falcon", true}, {strings.Repeat("a", 64), true},
		{strings.Repeat("é", 64), true},
		{"", false}, {strings.Repeat("a", 65), false}, {strings.Repeat("é", 65), false},
	} {
		if got := ValidReleaseName(c.name); got != c.ok {
			t.Errorf("ValidReleaseName(%q) = %v, want %v", c.name, got, c.ok)
		}
	}
}

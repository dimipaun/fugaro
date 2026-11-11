// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package coordinator

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gcpfake"
)

// The roadmap writers, as P9b builds them: compare-and-set over P9a's shape,
// the refusals a write owes, and the prune the two writes that can orphan a
// document carry. Nothing here contacts Firebase — the database is
// internal/gcpfake's — and nothing here writes outside the coordinator's
// subtree, which is what the rules half of this part (the generated rules and
// the emulator) and internal/infra's IAM half hold.

// writeStore is a store, the fake database behind it and the record the
// fixture describes, which is where every test in this file starts.
func writeStore(t *testing.T) (*Store, *gcpfake.RTDB, roadmapFixture) {
	t.Helper()
	s, f := testStoreAndFake(t)
	return s, f, seedRoadmap(t, f)
}

// readDoc decodes the document the record holds at path, strictly, and reports
// whether there is one. It is the writer's own read, so a test sees exactly
// what a reader would.
func readDoc[T any](t *testing.T, s *Store, path string) (T, bool) {
	t.Helper()
	var out T
	var raw json.RawMessage
	if _, found, err := s.Read(testCtx(t), path, &raw); err != nil {
		t.Fatal(err)
	} else if !found {
		return out, false
	}
	if err := strictDecode(raw, &out); err != nil {
		t.Fatalf("reading /%s: %v", path, err)
	}
	return out, true
}

// planWithTasks builds the smallest plan document carrying n tasks of the
// given title, which is what the bound tests grow.
func planWithTasks(plan string, n int, title string) Plan {
	doc := Plan{
		Schema: RoadmapSchema, Plan: plan, Stream: "console",
		Source: PlanSource{Repo: "acme/app", Path: "docs/plans/x.graph.json", Commit: "0", GraphSchema: 3},
	}
	for i := 0; i < n; i++ {
		doc.Tasks = append(doc.Tasks, Task{ID: fmt.Sprintf("T%04d", i), Title: title})
	}
	return doc
}

// TestMoveRefusesAStaleExpectAndMovesNothing: the request carries the record as
// its builder saw it (D2114's `none` for a task with no assignment), and a
// record that has moved since is refused before anything is written.
func TestMoveRefusesAStaleExpectAndMovesNothing(t *testing.T) {
	s, _, fx := writeStore(t)
	task := fx.Plans[0].Plan + "/" + fx.Plans[0].Tasks[0].ID
	from, to := ReleaseID("console", "0.110"), ReleaseID("mobile", "0.200")
	before, found := readDoc[Assignment](t, s, PathAssign(task))
	if !found || before.Release != from {
		t.Fatalf("the fixture's assignment = %+v (found %v), want it in %s", before, found, from)
	}

	for _, tc := range []struct {
		name, expect string
	}{{"a task the record says is in another release", ReleaseID("mobile", "0.200")}, {"a task the record has no assignment for", NoneRelease}} {
		err := s.Move(testCtx(t), "person-2@example.com", task, to, tc.expect)
		if !errors.Is(err, ErrMoveStale) {
			t.Errorf("moving from %s: err = %v, want ErrMoveStale", tc.name, err)
		}
	}
	after, _ := readDoc[Assignment](t, s, PathAssign(task))
	if after != before {
		t.Errorf("a refused move wrote the document: %+v, want %+v", after, before)
	}

	// The expect the record actually holds is applied, and `prev` names the
	// release the move displaced.
	if err := s.Move(testCtx(t), "person-2@example.com", task, to, from); err != nil {
		t.Fatalf("moving with the expect the record holds: %v", err)
	}
	moved, _ := readDoc[Assignment](t, s, PathAssign(task))
	if moved.Release != to || moved.Prev == nil || *moved.Prev != from {
		t.Errorf("after the move the assignment = %+v, want it in %s with prev %s", moved, to, from)
	}
	// A task no plan document carries is not moved onto anything, stale
	// expect or not.
	if err := s.Move(testCtx(t), "person-2@example.com", "plans/gone/T1", NoneRelease, NoneRelease); !errors.Is(err, ErrUnknownTask) {
		t.Errorf("moving a task no plan carries: err = %v, want ErrUnknownTask", err)
	}
}

// TestMoveLostRaceIsReComparedNotRetriedBlind is the red step's subject: the
// write a move makes is held in flight while another person moves the same
// task, and what the move does with the lost ETag race is re-read the record
// and re-compare `expect` — refuse — rather than write its own value again,
// which is what Store.Write's blind retry would do and what would put a task
// back in a release somebody else has taken it out of.
func TestMoveLostRaceIsReComparedNotRetriedBlind(t *testing.T) {
	s, f, fx := writeStore(t)
	task := fx.Plans[0].Plan + "/" + fx.Plans[0].Tasks[0].ID
	from, to := ReleaseID("console", "0.110"), ReleaseID("mobile", "0.200")
	path := PathAssign(task)
	ctx := testCtx(t)

	// Two holds on the one path: the move's read consumes the first and
	// answers at once, the move's write consumes the second and waits.
	_, openRead := f.HoldNext(path)
	stamped, openWrite := f.HoldNext(path)
	defer openWrite()
	openRead()

	done := make(chan error, 1)
	go func() { done <- s.Move(ctx, "person-1@example.com", task, to, from) }()
	<-stamped // the write is in flight, unanswered

	// The other writer: somebody takes the task out of the release this
	// move's request was built on, into the finished one.
	other := Assignment{
		Schema: RoadmapSchema, Task: task, Release: ReleaseID("console", "0.109"),
		By: "person-2@example.com", At: roadmapFixtureAt,
	}
	f.Set(path, other)
	openWrite()

	if err := <-done; !errors.Is(err, ErrMoveStale) {
		t.Fatalf("a move whose write lost the race: err = %v, want ErrMoveStale", err)
	}
	if got, _ := readDoc[Assignment](t, s, path); got != other {
		t.Errorf("the lost race left %+v, want the other writer's %+v: the move wrote its own value again", got, other)
	}
}

// TestMoveRecordsPrevSoTheInverseRestores: a move records where it came from,
// so the inverse is one more move rather than a reconstruction — and a move
// and its inverse leave the document as it was but for `by` and `at`.
func TestMoveRecordsPrevSoTheInverseRestores(t *testing.T) {
	s, _, fx := writeStore(t)
	task := fx.Plans[0].Plan + "/" + fx.Plans[0].Tasks[0].ID
	from, to := ReleaseID("console", "0.110"), ReleaseID("mobile", "0.200")
	start, _ := readDoc[Assignment](t, s, PathAssign(task))

	if err := s.Move(testCtx(t), "person-2@example.com", task, to, from); err != nil {
		t.Fatal(err)
	}
	if err := s.Move(testCtx(t), "person-3@example.com", task, from, to); err != nil {
		t.Fatal(err)
	}
	end, _ := readDoc[Assignment](t, s, PathAssign(task))
	if end.Prev == nil || *end.Prev != to {
		t.Errorf("the inverse recorded prev = %v, want %s: the move it undoes is the one it displaced", end.Prev, to)
	}
	// The two documents differ in the write's own facts — who and when — and
	// in nothing else: an undo that left the release anywhere else would not
	// be an undo.
	start.By, end.By, start.At, end.At, start.Prev, end.Prev = "", "", time.Time{}, time.Time{}, nil, nil
	if start != end {
		t.Errorf("a move and its inverse left %+v, want the record it started from %+v", end, start)
	}
}

// TestMoveFromUnassignedExpectsNone pins D2114's word: a task no assignment
// document describes is `none`, and the move that gives it one writes the
// document it had none of.
func TestMoveFromUnassignedExpectsNone(t *testing.T) {
	s, _, fx := writeStore(t)
	var unassigned string
	for _, p := range fx.Plans {
		if p.Stream == "cli" {
			unassigned = p.Plan + "/" + p.Tasks[0].ID
		}
	}
	if unassigned == "" {
		t.Fatal("the fixture has no plan whose stream carries no release")
	}
	if _, found := readDoc[Assignment](t, s, PathAssign(unassigned)); found {
		t.Fatal("the fixture assigned the unassigned task")
	}
	if err := s.Move(testCtx(t), "person-1@example.com", unassigned, ReleaseID("console", "0.110"), "none"); err != nil {
		t.Fatalf("moving an unassigned task: %v", err)
	}
	got, found := readDoc[Assignment](t, s, PathAssign(unassigned))
	if !found || got.Release != ReleaseID("console", "0.110") || got.Prev != nil {
		t.Errorf("the unassigned task's assignment = %+v (found %v), want it in the console stream's open release with no prev", got, found)
	}
}

// TestMoveRefusesAnUnknownTaskOrRelease: the record's own two facts about what
// can be moved — a task a plan document carries, and a release it holds.
func TestMoveRefusesAnUnknownTaskOrRelease(t *testing.T) {
	s, _, fx := writeStore(t)
	task := fx.Plans[0].Plan + "/" + fx.Plans[0].Tasks[0].ID
	from := ReleaseID("console", "0.110")
	if err := s.Move(testCtx(t), "person-1@example.com", task, ReleaseID("console", "9.999"), from); !errors.Is(err, ErrUnknownRelease) {
		t.Errorf("moving into a release the record does not hold: err = %v, want ErrUnknownRelease", err)
	}
}

// TestMoveIntoAFinishedReleaseIsRefusedByTheRecord: D2113's state, and the
// record's own rather than a rule about versions — a release that shipped is
// not a box a task is moved into afterwards.
func TestMoveIntoAFinishedReleaseIsRefusedByTheRecord(t *testing.T) {
	s, f, fx := writeStore(t)
	task := fx.Plans[0].Plan + "/" + fx.Plans[0].Tasks[0].ID
	from := ReleaseID("console", "0.110")
	if err := s.Move(testCtx(t), "person-1@example.com", task, ReleaseID("console", "0.109"), from); !errors.Is(err, ErrReleaseFinished) {
		t.Errorf("moving into a finished release: err = %v, want ErrReleaseFinished", err)
	}
	if got := f.Value(PathAssign(task)); got == nil {
		t.Fatal("a refused move deleted the assignment")
	}
}

// TestReleaseIDAndStreamNeverChange: a release's identity is its key, so a
// document stored under a key its own fields disagree with is refused rather
// than written over — a rename is a new release, a new document.
func TestReleaseIDAndStreamNeverChange(t *testing.T) {
	s, f, _ := writeStore(t)
	drifted := Release{Schema: RoadmapSchema, ID: "0.112", Stream: "console", Name: "Drifted", State: ReleaseOpen}
	f.Set(PathRelease("console", "0.111"), drifted)

	err := s.SetRelease(testCtx(t), "person-1@example.com", Release{
		Stream: "console", ID: "0.111", Name: "Renamed", State: ReleaseOpen, Order: 2,
	})
	if !errors.Is(err, ErrReleaseImmutable) {
		t.Errorf("writing over a release whose own id disagrees with its key: err = %v, want ErrReleaseImmutable", err)
	}
	if err := s.DeleteRelease(testCtx(t), "console", "0.111"); !errors.Is(err, ErrReleaseImmutable) {
		t.Errorf("deleting a release whose own id disagrees with its key: err = %v, want ErrReleaseImmutable", err)
	}
	if got, _ := readDoc[Release](t, s, PathRelease("console", "0.111")); got != drifted {
		t.Errorf("a refused release write left %+v, want the document as it was %+v", got, drifted)
	}

	// A release whose fields agree with its key is created and changed on the
	// ETag the writer read, and an unchanged write writes nothing.
	rel := Release{Stream: "console", ID: "0.108", Name: "Acorn", State: ReleaseOpen, Order: 0}
	if err := s.SetRelease(testCtx(t), "person-1@example.com", rel); err != nil {
		t.Fatalf("creating a release: %v", err)
	}
	got, found := readDoc[Release](t, s, PathRelease("console", "0.108"))
	if !found || got.Name != "Acorn" || got.By != "person-1@example.com" || got.At.IsZero() {
		t.Fatalf("the created release = %+v (found %v), want the name, the actor and the instant", got, found)
	}
	rel.State = ReleaseFinished
	if err := s.SetRelease(testCtx(t), "person-2@example.com", rel); err != nil {
		t.Fatalf("changing a release's state: %v", err)
	}
	if got, _ := readDoc[Release](t, s, PathRelease("console", "0.108")); got.State != ReleaseFinished {
		t.Errorf("the changed release = %+v, want it finished", got)
	}
}

// TestReleaseInUseIsNotDeleted: deleting a release a task's assignment names
// would orphan that assignment (D2115), so the operator is told to move the
// task out instead.
func TestReleaseInUseIsNotDeleted(t *testing.T) {
	s, f, fx := writeStore(t)
	task := fx.Plans[0].Plan + "/" + fx.Plans[0].Tasks[0].ID
	if err := s.DeleteRelease(testCtx(t), "console", "0.110"); !errors.Is(err, ErrReleaseInUse) {
		t.Errorf("deleting a release an assignment names: err = %v, want ErrReleaseInUse", err)
	}
	if f.Value(PathRelease("console", "0.110")) == nil {
		t.Fatal("a refused release delete removed the release")
	}
	if got := f.Value(PathAssign(task)); got == nil {
		t.Fatal("a refused release delete removed the assignment")
	}
	unused := Release{Schema: RoadmapSchema, ID: "0.107", Stream: "console", Name: "Windfall", State: ReleaseOpen}
	f.Set(PathRelease("console", "0.107"), unused)
	if err := s.DeleteRelease(testCtx(t), "console", "0.107"); err != nil {
		t.Fatalf("deleting an unused release: %v", err)
	}
	if f.Value(PathRelease("console", "0.107")) != nil {
		t.Error("the deleted release is still in the record")
	}
}

// TestEdgeRefusesSelfInPlanAndCycle: D2112's three refusals, each with the
// reason it carries — a self-edge, an edge inside one plan (the artifact's),
// and an edge that closes a cycle across the record and the plan documents.
func TestEdgeRefusesSelfInPlanAndCycle(t *testing.T) {
	s, f, fx := writeStore(t)
	console := fx.Plans[0].Plan
	mobile := fx.Plans[1].Plan
	v21 := console + "/" + fx.Plans[0].Tasks[0].ID
	m1 := mobile + "/" + fx.Plans[1].Tasks[0].ID
	edge := func(task, needs, reason string) Edge {
		return Edge{Task: task, Needs: needs, Reason: reason}
	}
	for _, tc := range []struct {
		name  string
		edge  Edge
		want  error
		wants string
	}{
		{"a task that depends on itself", edge(m1, m1, "because"), ErrEdgeRefused, "itself"},
		{"both endpoints in one plan", edge(v21, console+"/"+fx.Plans[0].Tasks[1].ID, "because"), ErrEdgeRefused, "artifact"},
		{"an endpoint no plan carries", edge(m1, "plans/gone/T1", "because"), ErrUnknownTask, ""},
		{"an edge that closes a cycle", edge(v21, m1, "because"), ErrEdgeRefused, "cycle"},
		{"an edge with no reason", edge(console+"/"+fx.Plans[0].Tasks[2].ID, m1, ""), ErrRoadmapInvalid, ""},
	} {
		err := s.AddEdge(testCtx(t), "person-1@example.com", tc.edge)
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
			continue
		}
		if tc.wants != "" && !strings.Contains(err.Error(), tc.wants) {
			t.Errorf("%s: the refusal says %q, want it to name %q", tc.name, err, tc.wants)
		}
	}
	if got := len(f.Value(PathEdges()).(map[string]any)); got != 1 {
		t.Errorf("the record holds %d edge documents, want the fixture's one: a refused edge was written", got)
	}

	// The edge the fixture already holds is not added twice, and a new one is
	// written and removed.
	existing, _ := readDoc[Edge](t, s, PathEdge(m1, v21))
	same := edge(m1, v21, existing.Reason)
	if err := s.AddEdge(testCtx(t), "person-1@example.com", same); err != nil {
		t.Fatalf("re-adding an edge the record holds: %v", err)
	}
	if got, _ := readDoc[Edge](t, s, PathEdge(m1, v21)); got != existing {
		t.Errorf("re-adding an unchanged edge wrote the document: %+v, want %+v", got, existing)
	}
	added := edge(console+"/"+fx.Plans[0].Tasks[2].ID, m1, "the release note is written after the phone ships")
	if err := s.AddEdge(testCtx(t), "person-1@example.com", added); err != nil {
		t.Fatalf("adding a cross-plan edge: %v", err)
	}
	if got, found := readDoc[Edge](t, s, PathEdge(added.Task, added.Needs)); !found || got.Reason != added.Reason || got.By != "person-1@example.com" {
		t.Errorf("the added edge = %+v (found %v), want it with its reason and the actor", got, found)
	}
	if err := s.RemoveEdge(testCtx(t), added.Task, added.Needs); err != nil {
		t.Fatalf("removing an edge: %v", err)
	}
	if _, found := readDoc[Edge](t, s, PathEdge(added.Task, added.Needs)); found {
		t.Error("the removed edge is still in the record")
	}
}

// TestRoadmapBoundsAreEnforced: D2116's and D2120's bounds, each refused with
// the sentence that names it rather than truncated or written anyway.
func TestRoadmapBoundsAreEnforced(t *testing.T) {
	t.Run("a plan of more than a thousand tasks", func(t *testing.T) {
		s, _, _ := writeStore(t)
		err := s.PublishPlan(testCtx(t), planWithTasks("plans/big", MaxPlanTasks+1, "a task"), "", false)
		if !errors.Is(err, ErrRoadmapTooLarge) {
			t.Errorf("publishing %d tasks: err = %v, want ErrRoadmapTooLarge", MaxPlanTasks+1, err)
		}
	})
	t.Run("a plan document of more than 256 KiB", func(t *testing.T) {
		s, _, _ := writeStore(t)
		doc := planWithTasks("plans/wide", MaxPlanTasks, strings.Repeat("t", MaxTaskTitle))
		for i := 0; i < MaxPlanTasks; i++ {
			doc.Edges = append(doc.Edges, PlanEdge{Task: doc.Tasks[i].ID, Needs: doc.Tasks[(i+1)%MaxPlanTasks].ID})
		}
		if b, err := json.Marshal(doc); err != nil || len(b) <= MaxPlanBytes {
			t.Fatalf("the fixture plan is %d bytes (err %v): it is meant to pass the bound", len(b), err)
		}
		if err := s.PublishPlan(testCtx(t), doc, "", false); !errors.Is(err, ErrRoadmapTooLarge) {
			t.Errorf("publishing a plan past the size bound: err = %v, want ErrRoadmapTooLarge", err)
		}
	})
	t.Run("a title past its bound", func(t *testing.T) {
		s, _, _ := writeStore(t)
		doc := planWithTasks("plans/long", 1, strings.Repeat("t", MaxTaskTitle+1))
		if err := s.PublishPlan(testCtx(t), doc, "", false); !errors.Is(err, ErrRoadmapTooLarge) {
			t.Errorf("publishing a title of %d characters: err = %v, want ErrRoadmapTooLarge", MaxTaskTitle+1, err)
		}
	})
	t.Run("a reason past its bound", func(t *testing.T) {
		s, _, fx := writeStore(t)
		err := s.AddEdge(testCtx(t), "person-1@example.com", Edge{
			Task:   fx.Plans[0].Plan + "/" + fx.Plans[0].Tasks[0].ID,
			Needs:  fx.Plans[1].Plan + "/" + fx.Plans[1].Tasks[0].ID,
			Reason: strings.Repeat("r", MaxEdgeReason+1),
		})
		if !errors.Is(err, ErrRoadmapTooLarge) {
			t.Errorf("an edge reason of %d characters: err = %v, want ErrRoadmapTooLarge", MaxEdgeReason+1, err)
		}
	})
	t.Run("more plan documents than the record holds", func(t *testing.T) {
		s, f, _ := writeStore(t)
		for i := 0; i < MaxPlanDocuments; i++ {
			doc := planWithTasks(fmt.Sprintf("plans/p%03d", i), 1, "a task")
			f.Set(PathPlan(doc.Plan), doc)
		}
		if err := s.PublishPlan(testCtx(t), planWithTasks("plans/one-more", 1, "a task"), "", false); !errors.Is(err, ErrRoadmapTooLarge) {
			t.Errorf("publishing the %dth plan document: err = %v, want ErrRoadmapTooLarge", MaxPlanDocuments+1, err)
		}
	})
	t.Run("more cross-plan edges than the record holds", func(t *testing.T) {
		s, f, _ := writeStore(t)
		a, b := planWithTasks("plans/a", 45, "a"), planWithTasks("plans/b", 45, "b")
		f.Set(PathPlan(a.Plan), a)
		f.Set(PathPlan(b.Plan), b)
		seeded := 0
		for i := 0; i < 45 && seeded < MaxRoadmapEdges; i++ {
			for j := 0; j < 45 && seeded < MaxRoadmapEdges; j++ {
				e := Edge{
					Schema: RoadmapSchema, By: "person-1@example.com", At: roadmapFixtureAt, Reason: "because",
					Task:  fmt.Sprintf("%s/T%04d", a.Plan, i),
					Needs: fmt.Sprintf("%s/T%04d", b.Plan, j),
				}
				f.Set(PathEdge(e.Task, e.Needs), e)
				seeded++
			}
		}
		err := s.AddEdge(testCtx(t), "person-1@example.com", Edge{
			Task:   fmt.Sprintf("%s/T%04d", a.Plan, 44),
			Needs:  fmt.Sprintf("%s/T%04d", b.Plan, 44),
			Reason: "one too many",
		})
		if !errors.Is(err, ErrRoadmapTooLarge) {
			t.Errorf("adding the %dth cross-plan edge: err = %v, want ErrRoadmapTooLarge", MaxRoadmapEdges+1, err)
		}
	})
	t.Run("more releases than the record holds", func(t *testing.T) {
		s, f, _ := writeStore(t)
		for i := 0; i < MaxReleases; i++ {
			rel := Release{
				Schema: RoadmapSchema, Stream: fmt.Sprintf("s%02d", i%MaxStreams), ID: fmt.Sprintf("r%03d", i),
				Name: "a release", State: ReleaseOpen, By: "person-1@example.com", At: roadmapFixtureAt,
			}
			f.Set(PathRelease(rel.Stream, rel.ID), rel)
		}
		err := s.SetRelease(testCtx(t), "person-1@example.com", Release{Stream: "s00", ID: "r999", Name: "one too many", State: ReleaseOpen})
		if !errors.Is(err, ErrRoadmapTooLarge) {
			t.Errorf("creating the %dth release: err = %v, want ErrRoadmapTooLarge", MaxReleases+1, err)
		}
	})
	t.Run("more streams than the record holds", func(t *testing.T) {
		s, f, _ := writeStore(t)
		for i := 0; i < MaxStreams; i++ {
			rel := Release{
				Schema: RoadmapSchema, Stream: fmt.Sprintf("s%02d", i), ID: "r001",
				Name: "a release", State: ReleaseOpen, By: "person-1@example.com", At: roadmapFixtureAt,
			}
			f.Set(PathRelease(rel.Stream, rel.ID), rel)
		}
		err := s.SetRelease(testCtx(t), "person-1@example.com", Release{Stream: "s99", ID: "r001", Name: "one too many", State: ReleaseOpen})
		if !errors.Is(err, ErrRoadmapTooLarge) {
			t.Errorf("opening the %dth stream: err = %v, want ErrRoadmapTooLarge", MaxStreams+1, err)
		}
	})
}

// TestPublishUnchangedWritesNothing is P5's idempotency at the publish: an
// artifact the record already holds writes nothing and moves no ETag, which is
// what every other writer's `expect` compares against.
func TestPublishUnchangedWritesNothing(t *testing.T) {
	s, f, fx := writeStore(t)
	doc := fx.Plans[0]
	room := planWithTasks("plans/other", 1, "a task")
	if err := s.PublishPlan(testCtx(t), room, "", false); err != nil {
		t.Fatalf("publishing a plan the record does not hold: %v", err)
	}
	// Every write to the record is denied from here: a publish that wrote
	// anything would fail rather than answer nil.
	f.Deny(PathRoadmap())
	if err := s.PublishPlan(testCtx(t), doc, "", false); err != nil {
		t.Fatalf("publishing the document the record already holds: %v", err)
	}
	if got, _ := readDoc[Plan](t, s, PathPlan(doc.Plan)); got.Plan != doc.Plan {
		t.Errorf("the unchanged plan reads back as %+v", got)
	}
	// The control: a changed document does write, and the denial is what
	// fails it.
	changed := doc
	changed.Tasks = append([]Task{}, doc.Tasks...)
	changed.Tasks[0].Title = "a different title"
	if err := s.PublishPlan(testCtx(t), changed, "", false); err == nil {
		t.Error("a changed document was published while every write was denied: the unchanged publish proved nothing")
	}
}

// TestPublishWithAStaleExpectIsRefused: two people publishing one plan from
// two checkouts cannot silently overwrite each other, and a plan stored from
// another repository is refused unless the publisher means it (D2123).
func TestPublishWithAStaleExpectIsRefused(t *testing.T) {
	s, _, fx := writeStore(t)
	doc := fx.Plans[0]
	path := PathPlan(doc.Plan)
	etag, found, err := s.Read(testCtx(t), path, nil)
	if err != nil || !found {
		t.Fatalf("reading the stored plan: found %v, err %v", found, err)
	}
	changed := doc
	changed.Tasks = append([]Task{}, doc.Tasks...)
	changed.Tasks = changed.Tasks[:len(changed.Tasks)-1]

	if err := s.PublishPlan(testCtx(t), changed, etag+"-moved", false); !errors.Is(err, ErrPublishStale) {
		t.Errorf("publishing on a stale expect: err = %v, want ErrPublishStale", err)
	}
	if got, _ := readDoc[Plan](t, s, path); len(got.Tasks) != len(doc.Tasks) {
		t.Errorf("a refused publish wrote the document: %+v", got)
	}
	if err := s.PublishPlan(testCtx(t), changed, etag, false); err != nil {
		t.Fatalf("publishing on the expect the record holds: %v", err)
	}
	if got, _ := readDoc[Plan](t, s, path); len(got.Tasks) != len(changed.Tasks) {
		t.Errorf("the published plan holds %d tasks, want %d", len(got.Tasks), len(changed.Tasks))
	}

	// D2123: another repository's plan of the same id is not replaced
	// silently.
	foreign := changed
	foreign.Source = PlanSource{Repo: "other/repo", Path: "docs/plans/x.graph.json", Commit: "1", GraphSchema: 3}
	if err := s.PublishPlan(testCtx(t), foreign, "", false); !errors.Is(err, ErrPlanSource) {
		t.Errorf("publishing over another repository's plan: err = %v, want ErrPlanSource", err)
	}
	if err := s.PublishPlan(testCtx(t), foreign, "", true); err != nil {
		t.Fatalf("publishing over another repository's plan with --replace-source: %v", err)
	}
	if got, _ := readDoc[Plan](t, s, path); got.Source.Repo != "other/repo" {
		t.Errorf("the replaced plan's provenance = %+v, want it the publisher's", got.Source)
	}
}

// TestUnpublishMakesItsTasksDangling: removing a plan leaves the assignments
// and edges that named its tasks dangling rather than deleting them (D2115),
// which is what the reader reports and the prune later clears.
func TestUnpublishMakesItsTasksDangling(t *testing.T) {
	s, _, fx := writeStore(t)
	plan := fx.Plans[1].Plan
	task := plan + "/" + fx.Plans[1].Tasks[0].ID
	if err := s.UnpublishPlan(testCtx(t), plan); err != nil {
		t.Fatalf("unpublishing a plan: %v", err)
	}
	if _, found := readDoc[Plan](t, s, PathPlan(plan)); found {
		t.Error("the unpublished plan is still in the record")
	}
	rm, err := s.ReadRoadmap(testCtx(t))
	if err != nil {
		t.Fatal(err)
	}
	if !slicesContains(rm.Dangling, task) {
		t.Errorf("the dangling ids = %v, want the unpublished plan's task %s", rm.Dangling, task)
	}
	if _, found := readDoc[Assignment](t, s, PathAssign(task)); !found {
		t.Error("unpublishing deleted the assignment: it is dangling and kept, not dropped")
	}
}

// TestPruneDeletesAtMostOneHundredDanglingItems is D2125's bound: the prune
// runs inside a publish and an unpublish, deletes only what has been dangling
// past D2115's 30 days, and takes at most PruneBatch of them per call, so one
// person's request never carries a record's whole clean-up.
func TestPruneDeletesAtMostOneHundredDanglingItems(t *testing.T) {
	s, f := testStoreAndFake(t)
	plan := planWithTasks("plans/prune", 1, "the plan the prune hangs off")
	f.Set(PathPlan(plan.Plan), plan)
	old := time.Now().Add(-PruneAge - time.Hour)
	gone := func(i int) string { return fmt.Sprintf("plans/gone/T%03d", i) }
	for i := 0; i < 150; i++ {
		f.Set(PathAssign(gone(i)), Assignment{
			Schema: RoadmapSchema, Task: gone(i), Release: ReleaseID("console", "0.110"),
			By: "person-2@example.com", At: old,
		})
	}
	young := "plans/gone/Tyoung"
	f.Set(PathAssign(young), Assignment{
		Schema: RoadmapSchema, Task: young, Release: ReleaseID("console", "0.110"),
		By: "person-2@example.com", At: time.Now(),
	})

	if err := s.UnpublishPlan(testCtx(t), plan.Plan); err != nil {
		t.Fatalf("unpublishing the plan the prune hangs off: %v", err)
	}
	if left := countAssignments(t, f); left != 51 {
		t.Errorf("after one call the record holds %d assignments, want 51 (100 pruned, 50 old and the young one left)", left)
	}
	if f.Value(PathAssign(young)) == nil {
		t.Error("the prune deleted an assignment dangling for an hour: the age is D2115's 30 days")
	}
	// The write that follows carries the rest, and a document that is not
	// dangling is never touched.
	if err := s.PublishPlan(testCtx(t), plan, "", false); err != nil {
		t.Fatalf("publishing the plan back: %v", err)
	}
	if left := countAssignments(t, f); left != 1 {
		t.Errorf("after the second call the record holds %d assignments, want the young one alone", left)
	}
	if f.Value(PathAssign(young)) == nil {
		t.Error("the second prune deleted the young assignment")
	}
}

// TestThePruneLeavesADocumentThatMovedUnderIt: the prune's decision is one read
// of the record and its deletion is another, and a document that changed in
// between belongs to a decision that never saw it. Here a task is moved into a
// live release while the prune's own read of its assignment is in flight, and
// the move must survive: the deletion is put on the ETag that read returned —
// the moved document's own — and a prune that deleted on it would take a move
// made beside it.
func TestThePruneLeavesADocumentThatMovedUnderIt(t *testing.T) {
	s, f := testStoreAndFake(t)
	ctx := testCtx(t)
	live := ReleaseID("console", "0.110")
	f.Set(PathRelease("console", "0.110"), Release{
		Schema: RoadmapSchema, ID: "0.110", Stream: "console", Name: "console 0.110",
		State: ReleaseOpen, Order: 1, By: "person-2@example.com", At: time.Now().UTC(),
	})
	// The task is one a plan document carries, and its assignment names a
	// release the record does not hold: that, and nothing else, is what makes it
	// dangling.
	source := planWithTasks("plans/gone", 1, "the plan the task is in")
	f.Set(PathPlan(source.Plan), source)
	task := source.Plan + "/" + source.Tasks[0].ID
	f.Set(PathAssign(task), Assignment{
		Schema: RoadmapSchema, Task: task, Release: ReleaseID("console", "0.999"),
		By: "person-2@example.com", At: time.Now().Add(-PruneAge - time.Hour),
	})

	// The prune's own read of the dangling assignment is the one held, so the
	// other writer's move lands while the decision to delete it is in flight.
	stamped, openRead := f.HoldNext(PathAssign(task))
	defer openRead()
	done := make(chan error, 1)
	go func() { done <- s.PublishPlan(ctx, planWithTasks("plans/other", 1, "a task"), "", false) }()
	<-stamped
	moved := Assignment{
		Schema: RoadmapSchema, Task: task, Release: live,
		By: "person-3@example.com", At: time.Now().UTC(),
	}
	f.Set(PathAssign(task), moved)
	openRead()

	if err := <-done; err != nil {
		t.Fatalf("publishing beside a move: %v", err)
	}
	got, found := readDoc[Assignment](t, s, PathAssign(task))
	if !found {
		t.Fatal("the prune deleted an assignment that was moved while it was deciding")
	}
	if got.Release != moved.Release {
		t.Errorf("the assignment's release = %s, want %s: the move that landed beside the prune", got.Release, moved.Release)
	}
}

// TestAPublishKeepsTheMoveItOrphansAndARepublishRestoresIt: a publish that
// drops a task orphans its assignment, and D2115 keeps it — dangling and shown
// — for the 30 days, so a republish inside them puts the task back into its
// release with the move still on it. The prune runs inside that same publish
// and must not delete what the publish itself has just orphaned: the
// assignment's `at` is when it was last moved, which is long before the task
// left the plan, and a prune that counted the 30 days from it would take the
// move with the write that orphaned it, with no republish able to put it back.
func TestAPublishKeepsTheMoveItOrphansAndARepublishRestoresIt(t *testing.T) {
	s, f := testStoreAndFake(t)
	ctx := testCtx(t)
	f.Set(PathRelease("console", "0.110"), Release{
		Schema: RoadmapSchema, ID: "0.110", Stream: "console", Name: "console 0.110",
		State: ReleaseOpen, Order: 1, By: "person-2@example.com", At: time.Now().UTC(),
	})
	full := planWithTasks("plans/p", 2, "a task")
	if err := s.PublishPlan(ctx, full, "", false); err != nil {
		t.Fatalf("publishing the plan: %v", err)
	}
	task := full.Plan + "/" + full.Tasks[0].ID
	// The move is forty days old: made long before the publish that orphans it,
	// which is the whole point of the assertion below.
	f.Set(PathAssign(task), Assignment{
		Schema: RoadmapSchema, Task: task, Release: ReleaseID("console", "0.110"),
		By: "person-2@example.com", At: time.Now().Add(-PruneAge - 10*24*time.Hour),
	})

	short := full
	short.Tasks = full.Tasks[1:]
	if err := s.PublishPlan(ctx, short, "", false); err != nil {
		t.Fatalf("publishing the plan without the task: %v", err)
	}
	if _, found := readDoc[Assignment](t, s, PathAssign(task)); !found {
		t.Fatal("the publish deleted the assignment it had just orphaned: a dangling document is kept, not pruned by the write that orphaned it")
	}
	rm, err := s.ReadRoadmap(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slicesContains(rm.Dangling, task) {
		t.Errorf("the dangling ids = %v, want the orphaned %s", rm.Dangling, task)
	}

	if err := s.PublishPlan(ctx, full, "", false); err != nil {
		t.Fatalf("publishing the plan back: %v", err)
	}
	if rm, err = s.ReadRoadmap(ctx); err != nil {
		t.Fatal(err)
	}
	if slicesContains(rm.Dangling, task) {
		t.Errorf("the republished plan left %s dangling", task)
	}
	back, found := readDoc[Assignment](t, s, PathAssign(task))
	if !found || back.Release != ReleaseID("console", "0.110") {
		t.Errorf("after the republish the assignment = %+v (found %v), want the move that was kept", back, found)
	}
}

// countAssignments is how many assignment documents the fake record holds.
func countAssignments(t *testing.T, f *gcpfake.RTDB) int {
	t.Helper()
	m, _ := f.Value(PathAssigns()).(map[string]any)
	return len(m)
}

// slicesContains is slices.Contains spelled out for the test's one use, so the
// assertion reads as the sentence it makes.
func slicesContains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}

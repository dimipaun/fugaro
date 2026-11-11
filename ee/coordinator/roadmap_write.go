// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package coordinator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// This file is P9b of docs/coordinator/implementation-plan.md: the writers over
// P9a's shape, and the database half of the fence around it. It is called by
// one thing — the apply job (P9e), a non-public job running as the
// coordinator's account, applying the requests the front door's
// `roadmap:write` tools queue (D2118 as finally answered). No CLI command, no
// person's credential and not the front door calls it: every change to the
// record reaches it through that one writer.
//
// Three properties are the part rather than niceties around it:
//
//   - Every write is a compare-and-set on the ETag the writer read, and where
//     the value written depends on what was read the comparison is against
//     `expect`, the record as the request's builder saw it. The move is the
//     case that matters: its `prev` and the release it replaces are read from
//     the document, so a put that merely retried would move a task the record
//     no longer says is there (Store.Write's own loop, and the reason every
//     writer here does not use it).
//   - An unchanged document writes nothing and moves no ETag (P5's
//     idempotency): a publish of the artifact the record already holds, a
//     release set to the name and state it already carries. A caller that
//     cannot tell "written" from "already so" would otherwise move the version
//     every other writer compares against.
//   - The record's bounds are refused, never grown: D2116's and D2120's
//     counts and lengths, so a runaway caller is told what to change rather
//     than allowed to make the record unreadable for everybody.
//
// The writers key their documents with P9a's helpers (PlanID, ReleaseID,
// AssignID, EdgeID) and never invent a key, so what a write puts is what the
// reader already looks for. Nothing here verifies a document against another
// beyond the refusals stated: a reader reports what the record holds, and this
// file is the one place a question about the record's consistency is answered.

// Actor is the verified principal the front door recorded on a queued request
// (D2110, D2118). It becomes a document's `by`, so a person reading the record
// back can see who changed it, and it is a person's name rather than this
// build's string: it is sanitised like every other text the record holds. It
// is never a database identity — the apply job writes as the coordinator's
// account whatever the actor says, and the actor cannot widen that.
type Actor string

func (a Actor) by() string { return safetext.Field(string(a)) }

// NoneRelease is the word a task with no assignment is named by, in a move's
// `release` (unassign) and in its `expect` (D2114): a task with no assignment
// has no document at all, so there is nothing to read its release from.
const NoneRelease = "none"

// The refusals a writer owes, each a sentence naming what to change rather
// than a state the caller has to infer. They are sentinels because the apply
// job (P9e) has to tell one from another: a stale request is one to re-read
// and offer again, an unknown task is one to refuse for good until a plan
// carries it, and a record past its bound is one an operator has to be told
// about.
var (
	// ErrPublishStale is a publish whose `expect` is not the version the
	// record now holds: somebody else published the plan since the request
	// was built, and applying it would overwrite their version silently.
	ErrPublishStale = errors.New("the stored plan has moved since the request was built")
	// ErrPlanSource is D2123's refusal: the stored plan document was
	// published from another repository, and a plan id is a project-wide name
	// (D2111), so publishing over it would replace another repository's plan
	// without a word.
	ErrPlanSource = errors.New("the stored plan was published from another repository")
	// ErrMoveStale is a move whose `expect` is not the release the record now
	// holds: the task has been moved since the request was built, or the
	// request was built on a task that had no assignment and one has since
	// appeared.
	ErrMoveStale = errors.New("the task's stored release is not the one the request expected")
	// ErrUnknownTask is a task no plan document carries: an assignment or an
	// edge for it would be dangling the moment it was written (D2115), so the
	// plan is published first and the move or the edge after it.
	ErrUnknownTask = errors.New("no plan document carries that task")
	// ErrUnknownRelease is a release the record does not hold.
	ErrUnknownRelease = errors.New("the record holds no such release")
	// ErrReleaseFinished is a move into a release the record calls finished
	// (D2113): the record's own state, not a rule about versions.
	ErrReleaseFinished = errors.New("the release is finished")
	// ErrReleaseImmutable is a release document whose own `id` or `stream`
	// disagrees with the node it is stored under. A release's identity is its
	// key (ReleaseID), so the writer refuses to touch a document that names
	// another one rather than silently repairing the drift.
	ErrReleaseImmutable = errors.New("a release's id and stream never change")
	// ErrReleaseInUse is a release an assignment names: deleting it would
	// leave that assignment dangling (D2115), so the tasks are moved out
	// first.
	ErrReleaseInUse = errors.New("an assignment names that release")
	// ErrEdgeRefused is a dependency the record refuses on its own terms: a
	// self-edge, an edge inside one plan (that one is the artifact's, D2112),
	// or an edge that closes a cycle.
	ErrEdgeRefused = errors.New("the edge is refused")
	// ErrRoadmapTooLarge is a write past D2116's or D2120's bounds.
	ErrRoadmapTooLarge = errors.New("the write would pass the record's bound")
	// ErrRoadmapInvalid is a document that is not the shape the record holds:
	// an empty id, an unknown release state, a negative order.
	ErrRoadmapInvalid = errors.New("the document is not the shape the record holds")
)

// The record's bounds, all of them refusals and never truncations. D2116's
// counts are written per repository in the question the owner answered; under
// D2111's push model the record is project-wide and holds the plans of every
// repository, so a count here is the record's, which is the only set a writer
// can see whole.
const (
	// MaxPlanDocuments is how many plan documents the record holds.
	MaxPlanDocuments = 200
	// MaxPlanTasks is how many tasks one plan document may carry.
	MaxPlanTasks = 1000
	// MaxPlanBytes is how large one plan document may be, measured as the
	// JSON the writer puts.
	MaxPlanBytes = 256 * 1024
	// MaxRoadmapEdges is how many cross-plan edges the record holds.
	MaxRoadmapEdges = 2000
	// MaxTaskTitle is how many characters a task's title may be (D2116).
	MaxTaskTitle = 200
	// MaxEdgeReason is how many characters an edge's reason may be (D2116).
	MaxEdgeReason = 500
	// MaxReleases and MaxStreams are D2120's release bounds: how many release
	// documents the record holds, and how many streams they may name.
	MaxReleases = 200
	MaxStreams  = 32
)

// The prune's own two numbers (D2115, D2125). PruneAge is how long a dangling
// assignment or edge is kept before the prune may delete it, and PruneBatch is
// how many deletions one publish or unpublish call may make: the prune runs
// lazily inside those two writes and nowhere else, so an unbounded batch would
// put a record's whole clean-up inside one person's request.
const (
	PruneAge   = 30 * 24 * time.Hour
	PruneBatch = 100
)

// PublishPlan puts one plan artifact into the record: the document P9a's shape
// holds, keyed by PlanID over its plan id.
//
// An unchanged document writes nothing and moves no ETag, and a document whose
// `expect` is not the version the record now holds is refused before anything
// is written (ErrPublishStale). A publish whose provenance names another
// repository than the stored one is refused unless replaceSource says the
// publisher means it (D2123).
//
// It asks for no actor: the plan document carries no `by` (P9a's shape has no
// such field — the artifact's provenance is where it came from, and the
// publisher is not one of the record's facts).
func (s *Store) PublishPlan(ctx context.Context, doc Plan, expect string, replaceSource bool) error {
	doc.Schema = RoadmapSchema
	doc = doc.sanitised()
	if err := checkPlan(doc); err != nil {
		return err
	}
	path := PathPlan(doc.Plan)
	var raw json.RawMessage
	etag, found, err := s.Read(ctx, path, &raw)
	if err != nil {
		return err
	}
	if found {
		var stored Plan
		if err := strictDecode(raw, &stored); err != nil {
			return fmt.Errorf("coordinator: reading /%s: %w: %w", path, ErrUnreadable, err)
		}
		if expect != "" && expect != etag {
			return fmt.Errorf("%w: /%s has been published since it was read; read the record again and publish what it holds", ErrPublishStale, path)
		}
		if stored.Source.Repo != "" && doc.Source.Repo != "" && stored.Source.Repo != doc.Source.Repo && !replaceSource {
			return fmt.Errorf("%w: /%s came from %s; publish with --replace-source to replace another repository's plan", ErrPlanSource, path, safetext.Field(stored.Source.Repo))
		}
		if reflect.DeepEqual(stored.sanitised(), doc) {
			return nil
		}
	}
	// The record as this write found it, read before the put and never on the
	// path that writes nothing: it is the room the plan needs, and the basis the
	// prune below rests on — a document this publish is about to orphan is not
	// dangling in it, which is what keeps "kept and shown dangling" (D2115) true
	// of the very write that orphaned it.
	before, err := s.ReadRoadmap(ctx)
	if err != nil {
		return err
	}
	if !found {
		if err := checkPlanRoom(before); err != nil {
			return err
		}
	}
	if err := s.Put(ctx, path, etag, doc); err != nil {
		return fmt.Errorf("coordinator: publishing /%s: %w", path, err)
	}
	return s.pruneDangling(ctx, before)
}

// UnpublishPlan removes a plan document, and the tasks it carried become
// dangling in the assignments and edges that still name them (D2115) — which
// is what the prune beside it is for. It is idempotent: a plan the record does
// not hold is one an unpublish has already removed, and nothing is written.
func (s *Store) UnpublishPlan(ctx context.Context, plan string) error {
	path := PathPlan(plan)
	etag, found, err := s.Read(ctx, path, nil)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	before, err := s.ReadRoadmap(ctx)
	if err != nil {
		return err
	}
	if err := s.Put(ctx, path, etag, nil); err != nil {
		return fmt.Errorf("coordinator: unpublishing /%s: %w", path, err)
	}
	return s.pruneDangling(ctx, before)
}

// Move puts one task's assignment in the release a request names, or removes
// it when release is NoneRelease. Both release and expect are a release
// document's key (ReleaseID) or NoneRelease: the assignment stores the key
// (P9a), so comparing keys is comparing what the record holds rather than a
// name this writer would have to resolve through a second read.
//
// expect is the record as the request's builder saw it — the stored release,
// or none for a task with no assignment (D2114) — and it is checked before
// anything is written and again after every lost ETag race. That is the whole
// reason this does not call Store.Write: Write's loop re-reads the ETag and
// puts the same document again, and a move re-applied that way would set a
// task's release (and its `prev`) from a record it never compared against.
//
// The refusals, in the order they are decided: a stale expect, a task no plan
// document carries, a release the record does not hold, and a release whose
// own state is finished (D2113).
func (s *Store) Move(ctx context.Context, actor Actor, task, release, expect string) error {
	if _, _, ok := SplitQualifiedID(task); !ok {
		return fmt.Errorf("%w: %q is not a qualified task id (<plan>/<task>)", ErrRoadmapInvalid, task)
	}
	path := PathAssign(task)
	for attempt := 0; attempt < s.attempts; attempt++ {
		var raw json.RawMessage
		etag, found, err := s.Read(ctx, path, &raw)
		if err != nil {
			return err
		}
		stored := NoneRelease
		if found {
			var cur Assignment
			if err := strictDecode(raw, &cur); err != nil {
				return fmt.Errorf("coordinator: reading /%s: %w: %w", path, ErrUnreadable, err)
			}
			stored = cur.Release
		}
		if stored != expect {
			return fmt.Errorf("%w: /%s holds %s and the request expected %s; read the record again and move from what it holds", ErrMoveStale, path, stored, expect)
		}
		if err := s.checkMove(ctx, task, release); err != nil {
			return err
		}
		var doc any
		if release != NoneRelease {
			var prev *string
			if stored != NoneRelease {
				prev = &stored
			}
			doc = Assignment{Schema: RoadmapSchema, Task: task, Release: release, Prev: prev, By: actor.by(), At: time.Now().UTC()}
		}
		err = s.Put(ctx, path, etag, doc)
		if err == nil {
			return nil
		}
		if !errors.Is(err, rtdb.ErrPrecondition) {
			return fmt.Errorf("coordinator: moving %s: %w", task, err)
		}
		// The put lost the ETag race. The next turn re-reads the document and
		// re-compares `expect` against what it now holds, so a request built
		// on a record somebody else has moved is refused rather than applied
		// over theirs.
	}
	return fmt.Errorf("coordinator: moving %s: %d attempts lost the ETag race", task, s.attempts)
}

// checkMove is the move's two refusals about the record's own contents: the
// task must be one a plan document carries, and the release must be one the
// record holds and not one it calls finished.
func (s *Store) checkMove(ctx context.Context, task, release string) error {
	rm, err := s.ReadRoadmap(ctx)
	if err != nil {
		return err
	}
	if !roadmapHasTask(rm, task) {
		return fmt.Errorf("%w: %s; publish the plan that carries it first", ErrUnknownTask, task)
	}
	if release == NoneRelease {
		return nil
	}
	for _, rel := range rm.Releases {
		if ReleaseID(rel.Stream, rel.ID) != release {
			continue
		}
		if rel.State == ReleaseFinished {
			return fmt.Errorf("%w: %s is finished; move the task into an open release", ErrReleaseFinished, rel.Name)
		}
		return nil
	}
	return fmt.Errorf("%w: %s", ErrUnknownRelease, release)
}

// SetRelease creates a release or changes its name, state or order, on the
// ETag the writer read. The release's identity is its key (ReleaseID over the
// stream and the id), so a document stored under a key its own fields disagree
// with is refused rather than written over (ErrReleaseImmutable): a rename is
// a new release, a new document.
//
// The caller supplies the identity, the name, the state and the order; `by`
// and `at` are the writer's, because they are facts about this write.
func (s *Store) SetRelease(ctx context.Context, actor Actor, rel Release) error {
	if err := checkRelease(rel); err != nil {
		return err
	}
	rel.Schema = RoadmapSchema
	rel.Name, rel.By, rel.At = safetext.Field(rel.Name), actor.by(), time.Now().UTC()
	path := PathRelease(rel.Stream, rel.ID)
	var raw json.RawMessage
	etag, found, err := s.Read(ctx, path, &raw)
	if err != nil {
		return err
	}
	if found {
		var stored Release
		if err := strictDecode(raw, &stored); err != nil {
			return fmt.Errorf("coordinator: reading /%s: %w: %w", path, ErrUnreadable, err)
		}
		if stored.ID != rel.ID || stored.Stream != rel.Stream {
			return fmt.Errorf("%w: /%s holds the release %s of %s", ErrReleaseImmutable, path, stored.ID, stored.Stream)
		}
		if stored.Name == rel.Name && stored.State == rel.State && stored.Order == rel.Order {
			return nil
		}
	} else if err := s.checkReleaseRoom(ctx, rel.Stream); err != nil {
		return err
	}
	if err := s.Put(ctx, path, etag, rel); err != nil {
		return fmt.Errorf("coordinator: writing /%s: %w", path, err)
	}
	return nil
}

// DeleteRelease removes a release the record holds, refusing while an
// assignment still names it (ErrReleaseInUse): a release deleted under a
// task's assignment is what makes that assignment dangling (D2115), and an
// operator asking to delete a release is not asking to orphan the work in it.
func (s *Store) DeleteRelease(ctx context.Context, stream, id string) error {
	path := PathRelease(stream, id)
	var raw json.RawMessage
	etag, found, err := s.Read(ctx, path, &raw)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	var stored Release
	if err := strictDecode(raw, &stored); err != nil {
		return fmt.Errorf("coordinator: reading /%s: %w: %w", path, ErrUnreadable, err)
	}
	if stored.ID != id || stored.Stream != stream {
		return fmt.Errorf("%w: /%s holds the release %s of %s", ErrReleaseImmutable, path, stored.ID, stored.Stream)
	}
	rm, err := s.ReadRoadmap(ctx)
	if err != nil {
		return err
	}
	key := ReleaseID(stored.Stream, stored.ID)
	for _, a := range rm.Assignments {
		if a.Release == key {
			return fmt.Errorf("%w: %s is in %s; move it out first", ErrReleaseInUse, a.Task, stored.Name)
		}
	}
	if err := s.Put(ctx, path, etag, nil); err != nil {
		return fmt.Errorf("coordinator: deleting /%s: %w", path, err)
	}
	return nil
}

// AddEdge writes one cross-plan or cross-stream dependency, or changes the
// reason of one the record already holds, on the ETag the writer read.
//
// The refusals are D2112's and D2116's: a self-edge and an edge inside one
// plan (that one belongs in the artifact, where the planner reviews it), an
// endpoint no plan document carries, an edge that closes a cycle across the
// record and the plan documents — a cycle is refused rather than drawn — and
// a write past the record's edge bound. The reason is required: an edge nobody
// can check is not a dependency.
func (s *Store) AddEdge(ctx context.Context, actor Actor, edge Edge) error {
	edge.Schema = RoadmapSchema
	edge.Reason, edge.By, edge.At = safetext.Field(edge.Reason), actor.by(), time.Now().UTC()
	taskPlan, _, ok := SplitQualifiedID(edge.Task)
	if !ok {
		return fmt.Errorf("%w: %q is not a qualified task id (<plan>/<task>)", ErrRoadmapInvalid, edge.Task)
	}
	needPlan, _, ok := SplitQualifiedID(edge.Needs)
	if !ok {
		return fmt.Errorf("%w: %q is not a qualified task id (<plan>/<task>)", ErrRoadmapInvalid, edge.Needs)
	}
	if edge.Reason == "" {
		return fmt.Errorf("%w: an edge needs a reason", ErrRoadmapInvalid)
	}
	if utf8.RuneCountInString(edge.Reason) > MaxEdgeReason {
		return fmt.Errorf("%w: a reason is at most %d characters", ErrRoadmapTooLarge, MaxEdgeReason)
	}
	if edge.Task == edge.Needs {
		return fmt.Errorf("%w: a task does not depend on itself", ErrEdgeRefused)
	}
	if taskPlan == needPlan {
		return fmt.Errorf("%w: both endpoints are in %s, so the edge belongs in the plan's own artifact", ErrEdgeRefused, taskPlan)
	}
	rm, err := s.ReadRoadmap(ctx)
	if err != nil {
		return err
	}
	for _, id := range []string{edge.Task, edge.Needs} {
		if !roadmapHasTask(rm, id) {
			return fmt.Errorf("%w: %s; publish the plan that carries it first", ErrUnknownTask, id)
		}
	}
	if closesCycle(rm, edge.Task, edge.Needs) {
		return fmt.Errorf("%w: %s already depends on %s, so this edge would close a cycle", ErrEdgeRefused, edge.Needs, edge.Task)
	}
	path := PathEdge(edge.Task, edge.Needs)
	var raw json.RawMessage
	etag, found, err := s.Read(ctx, path, &raw)
	if err != nil {
		return err
	}
	if found {
		var stored Edge
		if err := strictDecode(raw, &stored); err != nil {
			return fmt.Errorf("coordinator: reading /%s: %w: %w", path, ErrUnreadable, err)
		}
		if stored.Reason == edge.Reason {
			return nil
		}
	} else if len(rm.Edges) >= MaxRoadmapEdges {
		return fmt.Errorf("%w: the record holds %d cross-plan edges; remove one before adding another", ErrRoadmapTooLarge, len(rm.Edges))
	}
	if err := s.Put(ctx, path, etag, edge); err != nil {
		return fmt.Errorf("coordinator: writing /%s: %w", path, err)
	}
	return nil
}

// RemoveEdge removes a cross-plan edge. It is idempotent: an edge the record
// does not hold is one a remove has already taken.
func (s *Store) RemoveEdge(ctx context.Context, task, needs string) error {
	path := PathEdge(task, needs)
	etag, found, err := s.Read(ctx, path, nil)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	if err := s.Put(ctx, path, etag, nil); err != nil {
		return fmt.Errorf("coordinator: deleting /%s: %w", path, err)
	}
	return nil
}

// pruneDangling deletes the assignment and edge documents that have been
// dangling for PruneAge, at most PruneBatch of them per call (D2115, D2125).
//
// It runs inside the two writes that can make a document dangling — a publish
// that drops a task and an unpublish that removes a plan — and nowhere else:
// there is no sweep in the roadmap's path, so a lazy prune on those two calls
// is where the 30 days are counted, and the batch bound is what keeps one
// person's request from carrying the whole of a record's clean-up.
//
// before is the record as the write that called this found it, and it is what
// makes the prune safe to run beside a record other writers are changing. No
// deletion rests on a single read: a document goes only when three observations
// agree, and the put that takes it is made on the ETag its own document was
// read on, so anything written to that document in between fails the put.
//
//   - The candidates come from before, so this write never deletes what it has
//     itself just orphaned: a document that was not dangling before the call is
//     not on the list whatever its own `at` says. That is what keeps D2115's
//     "kept and shown dangling" true of the publish or the unpublish that
//     orphaned it, and what lets a republish inside the 30 days put the task
//     back with its moves still on it.
//   - The candidates' own documents are read before the deletion is decided, so
//     a task moved out of a dangling assignment is seen for what it is: its
//     document no longer matches the one the record read holds, or it is no
//     longer dangling, and either way it stays.
//   - The decision comes from a second read of the record, taken after those
//     documents were read — never from before — so a plan published back
//     between the write and the prune leaves the assignments it restores
//     assigned: before is stale in exactly that direction, and a prune that
//     decided from it would delete the moves a republish has just put back.
//
// What this cannot do, named rather than papered over: the 30 days are counted
// from the document's own last write (`at`), which is a lower bound on how long
// it has been dangling and not the day it went dangling. A document whose task
// left the plans long after its last move can therefore still be deleted inside
// D2115's 30 days — by a later publish, never by the one that orphaned it. The
// record holds no fact that would date the orphaning: P9a's shape (schema 1)
// carries no "dangling since" field and no marker node, and a shape that did
// would be a later schema's.
func (s *Store) pruneDangling(ctx context.Context, before Roadmap) error {
	idx := indexRoadmap(before)
	cutoff := time.Now().Add(-PruneAge)
	var assigns, edges []pruneCandidate
	for _, a := range before.Assignments {
		if len(assigns)+len(edges) == PruneBatch {
			break
		}
		if !idx.assignment(a) || a.At.After(cutoff) {
			continue
		}
		c, ok, err := s.readPruneCandidate(ctx, PathAssign(a.Task))
		if err != nil {
			return err
		}
		if ok {
			assigns = append(assigns, c)
		}
	}
	for _, e := range before.Edges {
		if len(assigns)+len(edges) == PruneBatch {
			break
		}
		if !idx.edge(e) || e.At.After(cutoff) {
			continue
		}
		c, ok, err := s.readPruneCandidate(ctx, PathEdge(e.Task, e.Needs))
		if err != nil {
			return err
		}
		if ok {
			edges = append(edges, c)
		}
	}
	if len(assigns)+len(edges) == 0 {
		return nil
	}
	after, err := s.ReadRoadmap(ctx)
	if err != nil {
		return err
	}
	now := indexRoadmap(after)
	for _, c := range assigns {
		var doc Assignment
		if !roadmapDocument(c.raw, &doc) {
			continue
		}
		doc = doc.sanitised()
		stored, held := heldAssignment(after, doc.Task)
		if !held || !reflect.DeepEqual(stored, doc) || !now.assignment(stored) {
			continue
		}
		if err := s.deletePruned(ctx, c); err != nil {
			return err
		}
	}
	for _, c := range edges {
		var doc Edge
		if !roadmapDocument(c.raw, &doc) {
			continue
		}
		doc = doc.sanitised()
		stored, held := heldEdge(after, doc.Task, doc.Needs)
		if !held || !reflect.DeepEqual(stored, doc) || !now.edge(stored) {
			continue
		}
		if err := s.deletePruned(ctx, c); err != nil {
			return err
		}
	}
	return nil
}

// pruneCandidate is one document the prune may delete: where it lives, the ETag
// it was read on, and the document as it was read — the three facts the
// deletion is put on and checked against.
type pruneCandidate struct {
	path string
	etag string
	raw  json.RawMessage
}

// readPruneCandidate reads one candidate's own document. A document that is
// gone is one an earlier prune or a move has already taken, and one this build
// cannot read is not this writer's to delete: both are left alone, and neither
// is a candidate.
func (s *Store) readPruneCandidate(ctx context.Context, path string) (pruneCandidate, bool, error) {
	var c pruneCandidate
	c.path = path
	etag, found, err := s.Read(ctx, path, &c.raw)
	if err != nil || !found {
		return pruneCandidate{}, false, err
	}
	c.etag = etag
	return c, true, nil
}

// deletePruned deletes the document a candidate was read on. A put that loses
// its ETag race is a document somebody else changed between the read and here,
// and it is not this writer's to remove — the next publish or unpublish decides
// again on the record as it then stands.
func (s *Store) deletePruned(ctx context.Context, c pruneCandidate) error {
	if err := s.Put(ctx, c.path, c.etag, nil); err != nil {
		if errors.Is(err, rtdb.ErrPrecondition) {
			return nil
		}
		return fmt.Errorf("coordinator: pruning /%s: %w", c.path, err)
	}
	return nil
}

// heldAssignment is the assignment one read of the record holds for a task.
func heldAssignment(r Roadmap, task string) (Assignment, bool) {
	for _, a := range r.Assignments {
		if a.Task == task {
			return a, true
		}
	}
	return Assignment{}, false
}

// heldEdge is the edge one read of the record holds between two tasks.
func heldEdge(r Roadmap, task, needs string) (Edge, bool) {
	for _, e := range r.Edges {
		if e.Task == task && e.Needs == needs {
			return e, true
		}
	}
	return Edge{}, false
}

// checkPlan refuses a plan document that is not the shape the record holds, or
// that is past D2116's bounds.
func checkPlan(doc Plan) error {
	if doc.Plan == "" {
		return fmt.Errorf("%w: a plan document names its plan id", ErrRoadmapInvalid)
	}
	if len(doc.Tasks) > MaxPlanTasks {
		return fmt.Errorf("%w: a plan carries at most %d tasks, and this one carries %d", ErrRoadmapTooLarge, MaxPlanTasks, len(doc.Tasks))
	}
	for _, task := range doc.Tasks {
		if task.ID == "" {
			return fmt.Errorf("%w: a task of %s has no id", ErrRoadmapInvalid, doc.Plan)
		}
		if utf8.RuneCountInString(task.Title) > MaxTaskTitle {
			return fmt.Errorf("%w: a title is at most %d characters, and %s's is %d", ErrRoadmapTooLarge, MaxTaskTitle, task.ID, utf8.RuneCountInString(task.Title))
		}
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrRoadmapInvalid, err)
	}
	if len(b) > MaxPlanBytes {
		return fmt.Errorf("%w: a plan document is at most %d bytes, and this one is %d", ErrRoadmapTooLarge, MaxPlanBytes, len(b))
	}
	return nil
}

// checkPlanRoom refuses a plan document the record has no room for (D2116). It
// is asked of the record the caller already read, so the room a publish needs
// and the prune beside it are decided against one state of the record.
func checkPlanRoom(rm Roadmap) error {
	if len(rm.Plans) >= MaxPlanDocuments {
		return fmt.Errorf("%w: the record holds %d plan documents; unpublish one before publishing another", ErrRoadmapTooLarge, len(rm.Plans))
	}
	return nil
}

// checkRelease refuses a release document that is not the shape D2113 and
// D2120 give one: a stream name, a release id and a name of the shapes they
// name, one of the two states, and an order that is a position rather than a
// negative.
func checkRelease(rel Release) error {
	if !ValidStreamName(rel.Stream) {
		return fmt.Errorf("%w: %q is not a stream name", ErrRoadmapInvalid, rel.Stream)
	}
	if !ValidReleaseID(rel.ID) {
		return fmt.Errorf("%w: %q is not a release id", ErrRoadmapInvalid, rel.ID)
	}
	if !ValidReleaseName(rel.Name) {
		return fmt.Errorf("%w: a release's name is 1 to %d characters", ErrRoadmapTooLarge, ReleaseNameMax)
	}
	if rel.State != ReleaseOpen && rel.State != ReleaseFinished {
		return fmt.Errorf("%w: %q is not a release state (%s or %s)", ErrRoadmapInvalid, rel.State, ReleaseOpen, ReleaseFinished)
	}
	if rel.Order < 0 {
		return fmt.Errorf("%w: a release's order is a position in its stream, and %d is not one", ErrRoadmapInvalid, rel.Order)
	}
	return nil
}

// checkReleaseRoom refuses a release the record has no room for (D2120): its
// release count, and its stream count when the stream would be a new one.
func (s *Store) checkReleaseRoom(ctx context.Context, stream string) error {
	rm, err := s.ReadRoadmap(ctx)
	if err != nil {
		return err
	}
	if len(rm.Releases) >= MaxReleases {
		return fmt.Errorf("%w: the record holds %d releases; delete one before adding another", ErrRoadmapTooLarge, len(rm.Releases))
	}
	streams := rm.Streams()
	if !slices.Contains(streams, stream) {
		streams = append(streams, stream)
	}
	if len(streams) > MaxStreams {
		return fmt.Errorf("%w: %s would be the record's %dth stream, and a record holds at most %d", ErrRoadmapTooLarge, stream, len(streams), MaxStreams)
	}
	return nil
}

// roadmapHasTask reports whether any plan document of one read carries the
// qualified task id. It is the record's own answer to "is this task a task":
// nothing else knows, and an assignment or an edge for a task no plan carries
// is dangling the moment it is written (D2115).
func roadmapHasTask(rm Roadmap, task string) bool {
	for _, p := range rm.Plans {
		for _, t := range p.Tasks {
			if p.Plan+"/"+t.ID == task {
				return true
			}
		}
	}
	return false
}

// closesCycle reports whether an edge from task to needs would close a cycle
// across the record and the plan documents (D2112): both carry dependencies,
// a plan's own in its artifact and a cross-plan one as a document beside it,
// and a cycle is a refusal rather than a figure the graph would have to draw.
// The walk starts at needs and follows what each task depends on, so reaching
// task again is exactly what the new edge would close.
func closesCycle(rm Roadmap, task, needs string) bool {
	adj := map[string][]string{}
	for _, p := range rm.Plans {
		for _, e := range p.Edges {
			adj[p.Plan+"/"+e.Task] = append(adj[p.Plan+"/"+e.Task], p.Plan+"/"+e.Needs)
		}
	}
	for _, e := range rm.Edges {
		adj[e.Task] = append(adj[e.Task], e.Needs)
	}
	seen := map[string]bool{}
	var walk func(string) bool
	walk = func(n string) bool {
		if n == task {
			return true
		}
		if seen[n] {
			return false
		}
		seen[n] = true
		for _, next := range adj[n] {
			if walk(next) {
				return true
			}
		}
		return false
	}
	return walk(needs)
}

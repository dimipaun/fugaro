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
	"strings"
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
// is written (ErrPublishStale) — including one the record does not hold at all,
// which is what a plan unpublished since the publisher read it looks like: a
// publish that went ahead would put the plan back over that unpublish. A
// publish whose provenance names another repository than the stored one is
// refused unless replaceSource says the publisher means it (D2123).
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
	if !found {
		// A publish carrying a version the record no longer holds is refused
		// whether the plan is there under another version or not there at all:
		// an unpublish since the publisher read is a move of the document as
		// much as a second publish is, and a publish that went ahead would put
		// the plan back over it — the one write a person unpublishing a plan
		// means to have removed.
		if expect != "" {
			return fmt.Errorf("%w: /%s is not in the record; read the record again and publish what it holds", ErrPublishStale, path)
		}
	} else {
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
// own state is finished (D2113). A move to the release the record already
// holds is none of them: it writes nothing, because the document it would
// write is the one that is there, but for a `prev` and an `at` that would say
// a change happened where none did.
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
		// A move to the release the record already holds is no move: writing it
		// would set `prev` to that same release — the undo target a second move
		// needs, destroyed by the one request that changed nothing — and would
		// move the ETag every other writer's `expect` compares against. It
		// covers the unassign of a task that has no assignment (D2114), which
		// is a document that is not there being asked to not be there. The
		// record's own refusals are asked first: a task no plan carries and a
		// release the record does not hold are refused whether or not the move
		// would have written anything.
		if err := s.checkMove(ctx, task, release); err != nil {
			return err
		}
		if release == stored {
			return nil
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
	rel.Schema = RoadmapSchema
	// The name is sanitised before it is checked, as the plan's title and the
	// edge's reason are: safetext.Field drops what a screen must not draw, so a
	// name made of nothing but those characters is the empty name checkRelease
	// refuses rather than a stored label with nothing in it (D2113, GV4.1).
	rel.Name = safetext.Field(rel.Name)
	if err := checkRelease(rel); err != nil {
		return err
	}
	rel.By, rel.At = actor.by(), time.Now().UTC()
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

// PathDanglingMarks is the ledger the prune keeps: one mark per document the
// record holds dangling, saying when a prune first saw it so. It is the
// writer's own node and no reader's — it is not a fact about the roadmap but
// the bookkeeping that makes D2115's 30 days countable from the day a document
// went dangling, which is a day the document itself does not carry: its `at`
// is when it was last moved, and a task that leaves the plans years after its
// last move would otherwise be deleted by the next publish, minutes later.
func PathDanglingMarks() string { return PathRoadmap() + "/dangling" }

// PathDanglingMark is the mark for the document stored under key: an
// assignment's or an edge's own key, so a mark and the document it is about
// are one lookup apart.
func PathDanglingMark(key string) string { return PathDanglingMarks() + "/" + key }

// danglingMark is what the ledger holds for one document. It is a document of
// the record's own shape — a schema and a time — so it decodes the way every
// other one does.
type danglingMark struct {
	Schema int       `json:"schema"`
	At     time.Time `json:"at"`
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
// The 30 days are counted from PathDanglingMarks, and not from the document's
// own `at`: `at` is when the document was last written, which for an
// assignment moved long ago is years before the task left the plans, and a
// prune counting from it would delete a document inside its keep — the very
// write that orphaned it, for the assignment whose last move is old enough.
// The ledger is stamped on the first sighting and read on every one after, so
// the delete happens 30 days after a prune first saw the document dangling:
// never earlier than D2115's keep, and later only by how long the record went
// without a publish or an unpublish, which is the laziness D2125 chose.
//
// A mark whose document comes back — a republish puts its task in a plan again
// — is dropped by the publish that put it back, and a mark is dropped before
// the document it marks, so a mark never outlives its document and a key a
// later move reuses cannot inherit an old date. What the write cannot date is
// a document dangling because its release is gone: no mark is written for one
// and none is ever deleted, so it stays with the record (no writer here can
// make one — the move refuses a release the record does not hold and the
// delete refuses one an assignment names).
//
// before is the record as the write that called this found it, and it is what
// makes the prune safe to run beside a record other writers are changing. No
// deletion rests on a single read: a document goes only when four observations
// agree, and the put that takes it is made on the ETag its own document was
// read on, so anything written to that document in between fails the put.
//
//   - The candidates come from before, so this write never stamps or deletes
//     what it has itself just orphaned: a document that was not dangling before
//     the call is not on the list. That is what keeps D2115's "kept and shown
//     dangling" true of the publish or the unpublish that orphaned it, and what
//     lets a republish inside the 30 days put the task back with its moves
//     still on it.
//   - The candidates' own documents are read before anything is decided, and a
//     document that is no longer the one that read holds — another writer
//     changed it, or removed it — is left alone: it belongs to a decision that
//     never saw it, and the deletion below is put on the ETag of the document
//     as it was read, so the same is true of a write that lands later.
//   - The decision comes from a second read of the record, taken after those
//     documents were read — never from before — so a plan published back
//     between the write and the prune leaves the assignments it restores
//     assigned and their marks dropped: before is stale in exactly that
//     direction, and a prune that decided from it would delete the moves a
//     republish has just put back.
//   - The mark itself is a fourth: a document goes only when a mark for it is
//     older than PruneAge, and a mark is written the first time a prune sees
//     the document dangling.
func (s *Store) pruneDangling(ctx context.Context, before Roadmap) error {
	if len(before.Dangling) == 0 {
		return nil
	}
	marks, err := s.danglingMarks(ctx)
	if err != nil {
		return err
	}
	assigns, edges, err := s.danglingCandidates(ctx, before, marks)
	if err != nil {
		return err
	}
	if len(assigns)+len(edges) == 0 {
		return nil
	}
	after, err := s.ReadRoadmap(ctx)
	if err != nil {
		return err
	}
	for _, v := range danglingVerdicts(after, append(assigns, edges...)) {
		if err := s.applyDangling(ctx, v); err != nil {
			return err
		}
	}
	return nil
}

// pruneCandidate is one document the prune may act on: where it lives, the
// ETag it was read on, the document as it was read, the key its mark lives
// under, the mark the ledger held for it when this call began, and which of
// the record's two dangling families it belongs to.
type pruneCandidate struct {
	path string
	etag string
	raw  json.RawMessage
	key  string
	mark danglingMark
	edge bool
}

// danglingCandidates is the documents this call may act on, in the two sets
// the bound is spent on: those the ledger already marks, which are the ones a
// deletion or a republish's clearing can be about, and those it does not,
// which are stamped so that a later call can delete them.
//
// The two sets are gathered and capped apart because a mark is only a promise
// about the future: a document stamped today is dangling for the next 30 days,
// and one set in record order would spend every call's batch on those same
// documents and never reach the ones behind them. Each set is capped at
// PruneBatch, so a call reads at most twice the batch and deletes, at most,
// the batch (D2125).
//
// A document dangling because its release is gone is in neither set: no
// writer here can make one, and the record cannot date it.
func (s *Store) danglingCandidates(ctx context.Context, before Roadmap, marks map[string]danglingMark) (decide, stamp []pruneCandidate, err error) {
	idx := indexRoadmap(before)
	add := func(edge bool, path, key string) error {
		mark, marked := marks[key]
		if marked && len(decide) == PruneBatch {
			return nil
		}
		if !marked && len(stamp) == PruneBatch {
			return nil
		}
		c, ok, err := s.readPruneCandidate(ctx, path)
		if err != nil || !ok {
			return err
		}
		c.key, c.mark, c.edge = key, mark, edge
		if marked {
			decide = append(decide, c)
		} else {
			stamp = append(stamp, c)
		}
		return nil
	}
	for _, a := range before.Assignments {
		if !idx.assignment(a) || idx.carriesTask(a.Task) {
			continue
		}
		if err := add(false, PathAssign(a.Task), AssignID(a.Task)); err != nil {
			return nil, nil, err
		}
	}
	for _, e := range before.Edges {
		if !idx.edge(e) {
			continue
		}
		if err := add(true, PathEdge(e.Task, e.Needs), EdgeID(e.Task, e.Needs)); err != nil {
			return nil, nil, err
		}
	}
	return decide, stamp, nil
}

// danglingVerdict is one candidate as the record's second read answers for it:
// the record still holds the document as it was read, still holds it dangling,
// and it dangles because its task is what is missing — the only dangling this
// writer can date.
type danglingVerdict struct {
	candidate pruneCandidate
	dangling  bool
	datable   bool
}

// danglingVerdicts asks the record's second read about each candidate. A
// document that has changed since it was read, or that the read no longer
// holds at all, is not a verdict: it belongs to a decision that never saw it.
func danglingVerdicts(after Roadmap, cs []pruneCandidate) []danglingVerdict {
	idx := indexRoadmap(after)
	out := make([]danglingVerdict, 0, len(cs))
	for _, c := range cs {
		if c.edge {
			var doc Edge
			if !roadmapDocument(c.raw, &doc) {
				continue
			}
			doc = doc.sanitised()
			stored, held := heldEdge(after, doc.Task, doc.Needs)
			if !held || !reflect.DeepEqual(stored, doc) {
				continue
			}
			// An edge dangles only when a plan no longer carries one of its
			// endpoints, so every edge this writer can see dangling is datable.
			out = append(out, danglingVerdict{candidate: c, dangling: idx.edge(stored), datable: true})
			continue
		}
		var doc Assignment
		if !roadmapDocument(c.raw, &doc) {
			continue
		}
		doc = doc.sanitised()
		stored, held := heldAssignment(after, doc.Task)
		if !held || !reflect.DeepEqual(stored, doc) {
			continue
		}
		out = append(out, danglingVerdict{candidate: c, dangling: idx.assignment(stored), datable: !idx.carriesTask(stored.Task)})
	}
	return out
}

// applyDangling is the ledger's one decision, taken for one candidate that the
// record's second read still holds as it was read.
func (s *Store) applyDangling(ctx context.Context, v danglingVerdict) error {
	switch {
	case !v.dangling && v.candidate.mark.At.IsZero():
		return nil
	case !v.dangling:
		// A republish put the task back while this prune was deciding: the mark
		// goes with the dangling it was about, so a later orphaning of the same
		// document starts its own 30 days rather than inheriting these.
		return s.clearDanglingMark(ctx, v.candidate.key)
	case !v.datable:
		return nil
	case v.candidate.mark.At.IsZero():
		return s.putDanglingMark(ctx, v.candidate.key, time.Now().UTC())
	case time.Since(v.candidate.mark.At) < PruneAge:
		return nil
	}
	// The mark goes before the document it marks: a deletion that loses its
	// ETag race below leaves a document with no mark, which the next call
	// stamps again and keeps longer — the direction that loses nothing. The
	// other order would leave a mark for a document that is gone, and the key a
	// later move wrote the same task's assignment under would inherit it.
	if err := s.clearDanglingMark(ctx, v.candidate.key); err != nil {
		return err
	}
	return s.deletePruned(ctx, v.candidate)
}

// danglingMarks reads the ledger. A mark this build cannot read is one it does
// not trust: an unreadable mark and a missing one are the same answer — this
// document has not been seen dangling before — and the caller stamps a fresh
// one, which keeps the document longer and never shorter.
func (s *Store) danglingMarks(ctx context.Context) (map[string]danglingMark, error) {
	var raw map[string]json.RawMessage
	_, found, err := s.Read(ctx, PathDanglingMarks(), &raw)
	if err != nil || !found {
		return nil, err
	}
	marks := make(map[string]danglingMark, len(raw))
	for key, one := range raw {
		var m danglingMark
		if err := strictDecode(one, &m); err != nil || m.Schema != RoadmapSchema || m.At.IsZero() {
			continue
		}
		marks[key] = m
	}
	return marks, nil
}

// putDanglingMark stamps a document the ledger has not seen dangling before.
// It is a plain write and not a compare-and-set: a mark may only move forward,
// and two prunes racing to stamp one document write the later date, which is
// the one that keeps the document longer.
func (s *Store) putDanglingMark(ctx context.Context, key string, at time.Time) error {
	path := PathDanglingMark(key)
	if err := s.Write(ctx, path, danglingMark{Schema: RoadmapSchema, At: at}); err != nil {
		return fmt.Errorf("coordinator: marking /%s dangling: %w", path, err)
	}
	return nil
}

// clearDanglingMark drops a document's mark: its task is back in a plan, or
// the document is about to go. A mark that is not there is one an earlier
// prune dropped, and a delete of a node that does not exist is no write at all.
func (s *Store) clearDanglingMark(ctx context.Context, key string) error {
	path := PathDanglingMark(key)
	if err := s.Write(ctx, path, nil); err != nil {
		return fmt.Errorf("coordinator: clearing /%s: %w", path, err)
	}
	return nil
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
//
// A task id is a name inside its plan and carries no slash: the record's own
// name for a task is the qualified `<plan>/<task>`, which SplitQualifiedID
// splits at the last slash because a plan id is a path under docs/ and may
// hold slashes itself. A task id with one in it would make that split land
// inside the task, so `plans/x` with a task `y/T1` and `plans/x/y` with a task
// `T1` would be one qualified id for two different tasks: an edge between them
// would be read as an edge inside one plan — D2112's refusal, bypassed — and a
// name that masks another plan's task is a name no reader can tell from it.
// Two tasks of one plan sharing an id are refused with it, for the same
// reason: one name, two tasks, and the record holds one of them.
func checkPlan(doc Plan) error {
	if doc.Plan == "" {
		return fmt.Errorf("%w: a plan document names its plan id", ErrRoadmapInvalid)
	}
	if len(doc.Tasks) > MaxPlanTasks {
		return fmt.Errorf("%w: a plan carries at most %d tasks, and this one carries %d", ErrRoadmapTooLarge, MaxPlanTasks, len(doc.Tasks))
	}
	seen := make(map[string]bool, len(doc.Tasks))
	for _, task := range doc.Tasks {
		if task.ID == "" {
			return fmt.Errorf("%w: a task of %s has no id", ErrRoadmapInvalid, doc.Plan)
		}
		if strings.ContainsRune(task.ID, '/') {
			return fmt.Errorf("%w: the task id %q of %s holds a slash; a task id is a name inside its plan, and the record's qualified id is <plan>/<task>", ErrRoadmapInvalid, task.ID, doc.Plan)
		}
		if seen[task.ID] {
			return fmt.Errorf("%w: %s names the task %q twice; one name is one task", ErrRoadmapInvalid, doc.Plan, task.ID)
		}
		seen[task.ID] = true
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

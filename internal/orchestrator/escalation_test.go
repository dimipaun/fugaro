package orchestrator_test

import (
	"bytes"
	"errors"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/dimipaun/fugaro/internal/escalation"
	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// The two heads these tests write into a ledger, as a provider reports them:
// forty lower-hex characters, which is what git writes and what the ledger
// accepts.
const (
	headA = "9f2c1ab4d5e60718293a4b5c6d7e8f9012345678"
	headB = "0a1b2c3d4e5f60718293a4b5c6d7e8f901234567"
)

// hexHead is the shape a head in the ledger has: lower hex, possibly
// abbreviated, as the validator and the schema both state it. Bitbucket
// reports twelve characters, which is why the floor is not forty.
var hexHead = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

// step is one move on the ladder. The rung it leaves is the zero Rung on the
// first move — an item entering the ladder — as the document spells "none".
func step(from, to escalation.Rung, reason escalation.Reason, at time.Time, head string) orchestrator.Transition {
	return orchestrator.Transition{From: from, To: to, Reason: reason, At: at.UTC(), HeadSHA: head}
}

// ledger is a ledger whose EnteredAt is the last transition's own time, which
// is the one invariant the validator states about the two halves of the block.
func ledger(rung escalation.Rung, outer int, transitions ...orchestrator.Transition) orchestrator.Escalation {
	e := orchestrator.Escalation{Rung: rung, OuterAttempts: outer, Transitions: transitions}
	if len(transitions) > 0 {
		e.EnteredAt = transitions[len(transitions)-1].At
	}
	return e
}

// escalationOp is the daemon's write of a ledger: a work item upsert carrying
// the block and nothing else.
func escalationOp(id string, e orchestrator.Escalation) orchestrator.Op {
	return upsertOp(id, func(op *orchestrator.WorkItemOp) { op.Escalation = &e })
}

// escalationAt is the ledger a document carries on one of its work items.
func escalationAt(t *testing.T, doc map[string]any, i int) map[string]any {
	t.Helper()
	e, ok := workItemAt(t, doc, i)["escalation"].(map[string]any)
	if !ok {
		t.Fatalf("the work item %d carries no escalation", i)
	}
	return e
}

// lastTransition is the transition the ledger ends on: the one whose time the
// block's entered-at is.
func lastTransition(t *testing.T, e map[string]any) map[string]any {
	t.Helper()
	list, ok := e["transitions"].([]any)
	if !ok || len(list) == 0 {
		t.Fatal("the ledger carries no transition")
	}
	return list[len(list)-1].(map[string]any)
}

// chainOf is n transitions one after another on one rung: the shape the bound
// tests need, where what matters is the count and not the moves.
func chainOf(n int) []any {
	transitions := make([]any, 0, n)
	from := ""
	for range n {
		tr := map[string]any{"to": "R0", "reason": "rebase", "at": "2026-11-07T07:00:00Z"}
		if from != "" {
			tr["from"] = from
		}
		transitions = append(transitions, tr)
		from = "R0"
	}
	return transitions
}

// TestStateEscalationRoundTrip is the ledger as a document: what the daemon
// writes is what this release reads back, field for field, and the schema
// describes the block the encoder writes rather than a shape near it.
//
// Mutation: drop the Escalation field from WorkItem (or its json tag) and the
// block comes back empty; widen the schema's escalation object and the fixture
// stops being the document it describes.
func TestStateEscalationRoundTrip(t *testing.T) {
	now := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	s := stateWithAQueuedItem(t, now)
	s = mustApply(t, s, orchestrator.Daemon, "daemon", now, escalationOp("wi-1", ledger(
		escalation.R2, 2,
		step("", escalation.R0, escalation.ReasonRebase, now, headA),
		step(escalation.R0, escalation.R2, escalation.ReasonAttempts, now.Add(time.Hour), ""),
	)))

	raw, err := orchestrator.EncodeState(s)
	if err != nil {
		t.Fatalf("a state carrying a ledger does not encode: %v", err)
	}
	back, err := orchestrator.DecodeState(raw)
	if err != nil {
		t.Fatalf("a state carrying a ledger does not decode: %v", err)
	}
	got := back.WorkItems[0].Escalation
	if got == nil {
		t.Fatal("the ledger the daemon wrote came back as nothing at all")
	}
	if got.Rung != escalation.R2 || got.OuterAttempts != 2 {
		t.Fatalf("the ledger came back at rung %s with %d ladder runs, want R2 and 2", got.Rung, got.OuterAttempts)
	}
	if !got.EnteredAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("the ledger entered its rung at %s, want the last transition's own time %s", got.EnteredAt, now.Add(time.Hour))
	}
	if len(got.Transitions) != 2 {
		t.Fatalf("the ledger came back with %d transitions, want the two that were written", len(got.Transitions))
	}
	if got.Transitions[0].HeadSHA != headA || got.Transitions[0].Reason != escalation.ReasonRebase {
		t.Fatalf("the first transition came back as %+v", got.Transitions[0])
	}
	if got.Transitions[1].From != escalation.R0 || got.Transitions[1].To != escalation.R2 || got.Transitions[1].Reason != escalation.ReasonAttempts {
		t.Fatalf("the second transition came back as %+v", got.Transitions[1])
	}

	// Both documents the schema is about: the fixture, which carries a ledger,
	// and the one just written by this release.
	sch := compileSchema(t, "../../schemas/orchestrator-state.schema.json")
	for _, tc := range []struct {
		name string
		raw  []byte
	}{{"the fixture", readFixture(t)}, {"a state this release wrote", raw}} {
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(tc.raw))
		if err != nil {
			t.Fatal(err)
		}
		if err := sch.Validate(doc); err != nil {
			t.Errorf("%s is not a document the schema accepts: %v", tc.name, err)
		}
	}
}

// TestPatchEscalationAppendOnly is the ledger's own rule: a transition that
// happened is not rewritten, not reordered and not dropped, and the rung and
// the run count only move forward. It is the red step of E7 — while a patch
// can rewrite a past transition, every case below lands and the test fails.
//
// Mutation: replace appendEscalation's prefix check with an assignment and the
// rewrite and the drop both land; drop the rung and the count checks and the
// ladder walks back down.
func TestPatchEscalationAppendOnly(t *testing.T) {
	now := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	first := ledger(escalation.R0, 1, step("", escalation.R0, escalation.ReasonRebase, now, headA))
	second := ledger(escalation.R2, 2,
		step("", escalation.R0, escalation.ReasonRebase, now, headA),
		step(escalation.R0, escalation.R2, escalation.ReasonAttempts, now.Add(time.Hour), ""),
	)

	s := stateWithAQueuedItem(t, now)
	s = mustApply(t, s, orchestrator.Daemon, "daemon", now, escalationOp("wi-1", first))
	s = mustApply(t, s, orchestrator.Daemon, "daemon", now, escalationOp("wi-1", second))
	if got := s.WorkItems[0].Escalation; got == nil || len(got.Transitions) != 2 || got.Rung != escalation.R2 {
		t.Fatalf("the two appends left the ledger as %+v", got)
	}

	for _, tc := range []struct {
		name string
		next orchestrator.Escalation
	}{
		{"a past transition with another reason", ledger(escalation.R2, 2,
			step("", escalation.R0, escalation.ReasonAge, now, headA),
			step(escalation.R0, escalation.R2, escalation.ReasonAttempts, now.Add(time.Hour), ""),
		)},
		{"a past transition dropped", ledger(escalation.R2, 2,
			step(escalation.R0, escalation.R2, escalation.ReasonAttempts, now.Add(time.Hour), ""),
		)},
		// A transition appended that walks back down: the chain holds, the last
		// transition is the rung the block is at, and the ledger still went
		// backwards — which is the one shape the chain alone cannot refuse.
		{"a rung walked back down", ledger(escalation.R0, 3,
			step("", escalation.R0, escalation.ReasonRebase, now, headA),
			step(escalation.R0, escalation.R2, escalation.ReasonAttempts, now.Add(time.Hour), ""),
			step(escalation.R2, escalation.R0, escalation.ReasonRebase, now.Add(2*time.Hour), headB),
		)},
		{"a count of ladder runs walked back", ledger(escalation.R2, 1,
			step("", escalation.R0, escalation.ReasonRebase, now, headA),
			step(escalation.R0, escalation.R2, escalation.ReasonAttempts, now.Add(time.Hour), ""),
		)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next, err := applyAs(t, s, orchestrator.Daemon, "daemon", now, escalationOp("wi-1", tc.next))
			if !errors.Is(err, orchestrator.ErrInvalid) {
				t.Fatalf("a patch carrying %s landed: err = %v, want %v", tc.name, err, orchestrator.ErrInvalid)
			}
			if next.WorkItems != nil {
				t.Fatal("a refused patch handed back a state")
			}
			if got := s.WorkItems[0].Escalation; got == nil || len(got.Transitions) != 2 || got.Transitions[0].Reason != escalation.ReasonRebase {
				t.Fatalf("a refused patch changed the ledger it was refused for: %+v", got)
			}
		})
	}

	// The state never shares a slice with the patch a caller still holds: a
	// caller that reuses its own block after the write cannot reach the ledger
	// through it.
	block := ledger(escalation.R0, 1, step("", escalation.R0, escalation.ReasonRebase, now, headA))
	next := mustApply(t, stateWithAQueuedItem(t, now), orchestrator.Daemon, "daemon", now, escalationOp("wi-1", block))
	block.Transitions[0].Reason = escalation.ReasonCost
	block.Transitions[0].HeadSHA = headB
	if got := next.WorkItems[0].Escalation.Transitions[0]; got.Reason != escalation.ReasonRebase || got.HeadSHA != headA {
		t.Fatalf("the ledger followed the caller's own slice after the write: %+v", got)
	}
}

// TestValidateEscalationBounds is the ledger as a document this release reads
// back: a rung or a reason it has never heard of, a chain with a gap in it, a
// block whose halves disagree, and a ledger past its own bounds are all
// refused where they are read rather than carried into a daemon that would act
// on them.
//
// Mutation: drop any check in validateEscalation and the case that names it
// stops failing; move MaxEscalationTransitions or MaxOuterAttempts and the
// boundary case after the table does.
func TestValidateEscalationBounds(t *testing.T) {
	raw := readFixture(t)
	for _, tc := range []struct {
		name   string
		mutate func(*testing.T, map[string]any)
		want   error
	}{
		{"more transitions than the ledger may hold", func(_ *testing.T, e map[string]any) {
			e["transitions"] = chainOf(orchestrator.MaxEscalationTransitions + 1)
		}, orchestrator.ErrTooLarge},
		{"more ladder runs than an item may spend", func(_ *testing.T, e map[string]any) {
			e["outer_attempts"] = orchestrator.MaxOuterAttempts + 1
		}, orchestrator.ErrTooLarge},
		{"a negative count of ladder runs", func(_ *testing.T, e map[string]any) {
			e["outer_attempts"] = -1
		}, orchestrator.ErrTooLarge},
		{"a ledger with no transition at all", func(_ *testing.T, e map[string]any) {
			delete(e, "transitions")
		}, orchestrator.ErrInvalid},
		{"a chain with a gap in it", func(t *testing.T, e map[string]any) {
			lastTransition(t, e)["from"] = "R1"
		}, orchestrator.ErrInvalid},
		{"a rung that is not one", func(_ *testing.T, e map[string]any) {
			e["rung"] = "R9"
		}, orchestrator.ErrInvalid},
		{"a reason that is not one", func(t *testing.T, e map[string]any) {
			lastTransition(t, e)["reason"] = "the reviewer asked for it"
		}, orchestrator.ErrInvalid},
		{"a head that is not a git object name", func(t *testing.T, e map[string]any) {
			lastTransition(t, e)["head_sha"] = "refs/heads/retry-loop"
		}, orchestrator.ErrInvalid},
		{"a rung the last transition never entered", func(_ *testing.T, e map[string]any) {
			e["rung"] = "R4"
		}, orchestrator.ErrInvalid},
		{"an entered-at that is not the last transition's time", func(_ *testing.T, e map[string]any) {
			e["entered_at"] = "2026-11-07T08:00:00Z"
		}, orchestrator.ErrInvalid},
		{"no time it entered its rung", func(_ *testing.T, e map[string]any) {
			delete(e, "entered_at")
		}, orchestrator.ErrInvalid},
		{"a transition with no time", func(t *testing.T, e map[string]any) {
			delete(lastTransition(t, e), "at")
		}, orchestrator.ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := document(t, raw)
			tc.mutate(t, escalationAt(t, doc, 0))
			if _, err := orchestrator.DecodeState(remarshal(t, doc)); !errors.Is(err, tc.want) {
				t.Fatalf("a document with %s decoded: err = %v, want %v", tc.name, err, tc.want)
			}
		})
	}

	// A bound is a bound and not a wall one short of it: a ledger holding
	// exactly MaxEscalationTransitions, at exactly MaxOuterAttempts, is one
	// this release reads.
	doc := document(t, raw)
	e := escalationAt(t, doc, 0)
	e["transitions"] = chainOf(orchestrator.MaxEscalationTransitions)
	e["rung"] = "R0"
	e["outer_attempts"] = orchestrator.MaxOuterAttempts
	e["entered_at"] = "2026-11-07T07:00:00Z"
	if _, err := orchestrator.DecodeState(remarshal(t, doc)); err != nil {
		t.Fatalf("a ledger at exactly its bounds did not decode: %v", err)
	}
}

// TestStateV1MigratesToV2 is the raise the ladder's ledger needed: this
// release writes schema version 2, a node written by the release before it —
// version 1, and the version 0 node before the schema carried a version at
// all — is raised by Migrate, and the raise is the whole of it. It is what
// makes `fugaro update` the whole upgrade: the daemon runs Migrate on its own
// state at start-up (internal/cli), so a person runs one command and nothing
// else.
//
// Mutation: leave Migrate at 0 -> 1 and a version 1 node is refused as a
// version this release does not read.
func TestStateV1MigratesToV2(t *testing.T) {
	if orchestrator.SchemaVersion != 2 {
		t.Fatalf("this release writes schema version %d, want the 2 the ladder's ledger was added in", orchestrator.SchemaVersion)
	}
	current, err := orchestrator.DecodeState(readFixture(t))
	if err != nil {
		t.Fatalf("the fixture is not a state this release reads: %v", err)
	}

	doc := document(t, readFixture(t))
	doc["schema_version"] = 1
	previous := remarshal(t, doc)

	raised, err := orchestrator.DecodeState(previous)
	if err != nil {
		t.Fatalf("a node from the release before this one did not decode: %v", err)
	}
	if raised.SchemaVersion != 2 {
		t.Fatalf("the node came back at schema version %d, want 2", raised.SchemaVersion)
	}

	// Nothing but the version moved: the ledger on the item is the one that was
	// there, the approval stands, and the digest it binds is the same — the
	// ledger is bookkeeping, so an update does not revoke a person's go.
	if raised.Rev != current.Rev || len(raised.WorkItems) != len(current.WorkItems) || len(raised.Intents) != len(current.Intents) {
		t.Fatalf("the raise changed more than the version: rev %d -> %d, %d items -> %d, %d intents -> %d",
			current.Rev, raised.Rev, len(current.WorkItems), len(raised.WorkItems), len(current.Intents), len(raised.Intents))
	}
	was, is := current.WorkItems[0].Escalation, raised.WorkItems[0].Escalation
	if is == nil || !reflect.DeepEqual(*was, *is) {
		t.Fatalf("the ledger did not survive the raise: %+v, want %+v", is, was)
	}
	if got, want := orchestrator.SpecDigest(raised.WorkItems[0]), orchestrator.SpecDigest(current.WorkItems[0]); got != want {
		t.Fatalf("the migrated item digests to %s and the one it came from to %s", got, want)
	}
	if raised.WorkItems[0].Approved == nil {
		t.Fatal("the raise dropped the approval the item carried")
	}

	// One call from the version before the ladder kept its ledger, and one call
	// from the version before the schema carried a version: Migrate steps, and
	// the daemon's start-up does not have to know which node it found.
	direct, changed := orchestrator.Migrate(mustDecode(t, previous, false))
	if !changed || direct.SchemaVersion != 2 {
		t.Fatalf("Migrate over a version 1 node: changed = %v, version = %d", changed, direct.SchemaVersion)
	}
	doc["schema_version"] = 0
	oldest := remarshal(t, doc)
	both, changed := orchestrator.Migrate(mustDecode(t, oldest, false))
	if !changed || both.SchemaVersion != 2 {
		t.Fatalf("Migrate over a version 0 node: changed = %v, version = %d", changed, both.SchemaVersion)
	}
	if _, err := orchestrator.DecodeState(oldest); err != nil {
		t.Fatalf("a node from before the schema carried a version did not decode: %v", err)
	}
}

// TestOldDaemonStopsOnNewerVersion is the direction that must stop rather than
// guess. A node written by a release after this one is refused — the daemon
// reports an update being due and writes nothing — and the same is true of a
// ledger this release's vocabulary cannot place: a rung its ladder has never
// heard of, or a field a later release added to the block, is refused rather
// than read as the nearest thing to it.
//
// Mutation: make the version check "different" rather than "newer" and the
// raise of TestStateV1MigratesToV2 fails; drop the closed vocabularies and a
// rung from a later release is read as the zero rung.
func TestOldDaemonStopsOnNewerVersion(t *testing.T) {
	doc := document(t, readFixture(t))
	doc["schema_version"] = orchestrator.SchemaVersion + 1
	future := remarshal(t, doc)

	if _, err := orchestrator.DecodeState(future); !errors.Is(err, orchestrator.ErrSchemaVersion) {
		t.Fatalf("a node from a later release decoded: err = %v, want %v", err, orchestrator.ErrSchemaVersion)
	}
	if _, err := orchestrator.DecodeState(future); err == nil || !strings.Contains(err.Error(), "this release writes") {
		t.Fatalf("the refusal does not name the version this release writes: %v", err)
	}
	if again, changed := orchestrator.Migrate(mustDecode(t, future, false)); changed || again.SchemaVersion != orchestrator.SchemaVersion+1 {
		t.Fatalf("Migrate touched a node from a later release: changed = %v, version = %d", changed, again.SchemaVersion)
	}

	for _, tc := range []struct {
		name   string
		mutate func(*testing.T, map[string]any)
	}{
		{"a rung this release's ladder has never heard of", func(t *testing.T, e map[string]any) {
			e["rung"] = "R9"
		}},
		{"a field a later release added to the block", func(t *testing.T, e map[string]any) {
			e["judge"] = "blocked on the flaky test"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := document(t, readFixture(t))
			tc.mutate(t, escalationAt(t, doc, 0))
			_, err := orchestrator.DecodeState(remarshal(t, doc))
			if !errors.Is(err, orchestrator.ErrInvalid) {
				t.Fatalf("a document with %s decoded: err = %v, want %v", tc.name, err, orchestrator.ErrInvalid)
			}
		})
	}
}

// TestEscalationNotInSpecDigest is what makes the ladder usable at all: the
// ledger is bookkeeping, so the ladder acting on an item does not move the
// spec digest and does not take a standing approval away. The alternative is a
// daemon that asks a person for a go on work they already approved, every time
// the ladder moves (D2042).
//
// Mutation: drop escalation from bookkeepingFields and the first half fails;
// clear Approved when a ledger lands and the second half does.
func TestEscalationNotInSpecDigest(t *testing.T) {
	now := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	base := workItemOf("wi-1")
	digest := orchestrator.SpecDigest(base)

	for _, tc := range []struct {
		name string
		e    orchestrator.Escalation
	}{
		{"the first move on the ladder", ledger(escalation.R0, 1,
			step("", escalation.R0, escalation.ReasonRebase, now, headA))},
		{"a stronger pass", ledger(escalation.R2, 2,
			step("", escalation.R0, escalation.ReasonRebase, now, headA),
			step(escalation.R0, escalation.R2, escalation.ReasonAttempts, now.Add(time.Hour), ""))},
		{"a merge with follow-ups", ledger(escalation.R3, 3,
			step("", escalation.R0, escalation.ReasonRebase, now, headA),
			step(escalation.R0, escalation.R2, escalation.ReasonAttempts, now.Add(time.Hour), ""),
			step(escalation.R2, escalation.R3, escalation.ReasonR2Findings, now.Add(2*time.Hour), headB))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := base
			e := tc.e
			w.Escalation = &e
			if got := orchestrator.SpecDigest(w); got != digest {
				t.Fatalf("the ledger moved the spec digest: %s, want %s", got, digest)
			}
		})
	}

	// The fixture's own approval is the digest of its item, ledger and all: the
	// ledger it carries is not part of the spec the person read.
	fixture, err := orchestrator.DecodeState(readFixture(t))
	if err != nil {
		t.Fatalf("the fixture is not a state this release reads: %v", err)
	}
	if fixture.WorkItems[0].Escalation == nil {
		t.Fatal("the fixture carries no ledger, so it does not prove the digest leaves one out")
	}
	if got := orchestrator.SpecDigest(fixture.WorkItems[0]); got != fixture.WorkItems[0].Approved.SpecDigest {
		t.Fatalf("the fixture's item digests to %s and the approval it carries binds %s", got, fixture.WorkItems[0].Approved.SpecDigest)
	}

	// And the approval itself survives the daemon writing the ledger, which is
	// the thing the digest rule is for.
	approved, digest := approvedState(t, now)
	next := mustApply(t, approved, orchestrator.Daemon, "daemon", now, escalationOp("wi-1", ledger(escalation.R0, 1,
		step("", escalation.R0, escalation.ReasonRebase, now, headA))))
	if next.WorkItems[0].Approved == nil {
		t.Fatal("the ladder moving took a standing approval away")
	}
	if got := next.WorkItems[0].Approved.SpecDigest; got != digest {
		t.Fatalf("the standing approval binds %s after the ladder moved and %s before", got, digest)
	}
}

// TestAgentActorCannotWriteEscalation is G12: the ladder's ledger is the
// daemon's alone. A session, a person or the decision agent that could write
// it could claim a rung nobody climbed — and rung 3 is where a merge happens —
// so the upsert refuses the block from every class but the daemon, while the
// declarative fields those classes do write are untouched by the refusal.
//
// Mutation: drop op.Escalation from applyUpsert's daemon-only set and the
// three refusals below land.
func TestAgentActorCannotWriteEscalation(t *testing.T) {
	now := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	block := ledger(escalation.R3, 1, step("", escalation.R3, escalation.ReasonR2Clean, now, headA))

	for _, class := range []orchestrator.Class{orchestrator.Session, orchestrator.Person, orchestrator.DecisionAgent} {
		t.Run(string(class), func(t *testing.T) {
			s := stateWithAQueuedItem(t, now)
			next, err := applyAs(t, s, class, "whoever", now, escalationOp("wi-1", block))
			if !errors.Is(err, orchestrator.ErrActorNotAllowed) {
				t.Fatalf("a %s wrote the ladder's ledger: err = %v, want %v", class, err, orchestrator.ErrActorNotAllowed)
			}
			if !strings.Contains(err.Error(), "escalation") {
				t.Fatalf("the refusal does not say which field it is about: %v", err)
			}
			if next.WorkItems != nil {
				t.Fatal("a refused patch handed back a state")
			}
			if s.WorkItems[0].Escalation != nil {
				t.Fatal("a refused patch left a ledger on the item")
			}
		})
	}

	// The refusal is the ledger's and not the upsert's: what a session is for
	// still lands.
	s := stateWithAQueuedItem(t, now)
	s = mustApply(t, s, orchestrator.Session, "whoever", now, upsertOp("wi-1", func(op *orchestrator.WorkItemOp) {
		op.Title = ptr("land the state migration, again")
		op.Note = ptr("waiting on the flaky test")
	}))
	if s.WorkItems[0].Title != "land the state migration, again" || s.WorkItems[0].Escalation != nil {
		t.Fatalf("a session's own upsert landed as %+v", s.WorkItems[0])
	}

	// And the daemon may, which is the whole of who writes it.
	byDaemon := mustApply(t, stateWithAQueuedItem(t, now), orchestrator.Daemon, "daemon", now, escalationOp("wi-1", block))
	if got := byDaemon.WorkItems[0].Escalation; got == nil || got.Rung != escalation.R3 {
		t.Fatalf("the daemon's own write of the ledger landed as %+v", got)
	}
}

// TestLedgerHoldsNoPromptOrDiff is G8: the ledger is a record of moves, not a
// place text sits. Its shape is the first half — there is no field a prompt, a
// finding or a diff could be written into — and its values are the second:
// every string is a value of a closed vocabulary, a server time or a git
// object name, and anything else is refused rather than stored.
//
// Mutation: add a free-text field to Transition (say a summary) and the shape
// check fails; drop KnownReason from the validator and a finding is stored in
// the reason field.
func TestLedgerHoldsNoPromptOrDiff(t *testing.T) {
	if got, want := jsonNames(reflect.TypeOf(orchestrator.Transition{})), []string{"at", "from", "head_sha", "reason", "to"}; !slices.Equal(got, want) {
		t.Fatalf("a transition writes %v, and the ledger's fields are %v: nothing else may sit in one", got, want)
	}
	if got, want := jsonNames(reflect.TypeOf(orchestrator.Escalation{})), []string{"entered_at", "outer_attempts", "rung", "transitions"}; !slices.Equal(got, want) {
		t.Fatalf("a ledger writes %v, and its fields are %v", got, want)
	}

	// The fixture's ledger is walked value by value: a string that is none of
	// the four kinds would be text this document has nowhere to put.
	e := escalationAt(t, document(t, readFixture(t)), 0)
	if rung, _ := e["rung"].(string); !escalation.KnownRung(escalation.Rung(rung)) {
		t.Errorf("the ledger is at %q, which is not a rung", rung)
	}
	if at, _ := e["entered_at"].(string); !isServerTime(at) {
		t.Errorf("the ledger entered its rung at %q, which is not a server time", at)
	}
	for i, raw := range e["transitions"].([]any) {
		tr := raw.(map[string]any)
		for _, name := range []string{"from", "to"} {
			if v, ok := tr[name].(string); ok && !escalation.KnownRung(escalation.Rung(v)) {
				t.Errorf("the transition %d has %s %q, which is not a rung", i+1, name, v)
			}
		}
		if v, _ := tr["reason"].(string); !escalation.KnownReason(escalation.Reason(v)) {
			t.Errorf("the transition %d is %q, which is not a reason code", i+1, v)
		}
		if v, _ := tr["at"].(string); !isServerTime(v) {
			t.Errorf("the transition %d is stamped %q, which is not a server time", i+1, v)
		}
		if v, ok := tr["head_sha"].(string); ok && !hexHead.MatchString(v) {
			t.Errorf("the transition %d names the head %q, which is not a git object name", i+1, v)
		}
	}

	for _, tc := range []struct {
		name   string
		mutate func(*testing.T, map[string]any)
	}{
		{"a reason that is the finding itself", func(t *testing.T, e map[string]any) {
			lastTransition(t, e)["reason"] = "the reviewer said the retry loop leaks a file descriptor"
		}},
		{"a rung that is a sentence", func(_ *testing.T, e map[string]any) {
			e["rung"] = "escalate to the strong model"
		}},
		{"a head that is a branch name", func(t *testing.T, e map[string]any) {
			lastTransition(t, e)["head_sha"] = "refs/heads/retry-loop"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := document(t, readFixture(t))
			tc.mutate(t, escalationAt(t, doc, 0))
			if _, err := orchestrator.DecodeState(remarshal(t, doc)); !errors.Is(err, orchestrator.ErrInvalid) {
				t.Fatalf("a document with %s decoded: err = %v, want %v", tc.name, err, orchestrator.ErrInvalid)
			}
		})
	}
}

// isServerTime reports whether s is a time this document writes.
func isServerTime(s string) bool {
	at, err := time.Parse(time.RFC3339, s)
	return err == nil && !at.IsZero()
}

// TestStatePreviousVersionConverges is the whole of the update, end to end: a
// node written by the release before this one — version 1, and no ledger
// anywhere, because that release had none — is read by this one, comes back at
// version 2 with everything it carried intact, and is written back in a form
// that is read as it stands. A second read changes nothing, so `fugaro update`
// converges an installation rather than rewriting its node on every start-up.
//
// Mutation: drop the Migrate call from DecodeState and the first read is
// refused; make Migrate stamp the version without reading the document and the
// round trip below stops being byte for byte the same.
func TestStatePreviousVersionConverges(t *testing.T) {
	now := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	doc := document(t, readFixture(t))
	doc["schema_version"] = 1
	for _, raw := range listAt(t, doc, "work_items") {
		delete(raw.(map[string]any), "escalation")
	}
	previous := remarshal(t, doc)
	if bytes.Contains(previous, []byte("escalation")) {
		t.Fatal("the document still carries a ledger, so it is not a node from the release before this one")
	}
	before := mustDecode(t, previous, false)

	converged, err := orchestrator.DecodeState(previous)
	if err != nil {
		t.Fatalf("a node from the previous release did not decode: %v", err)
	}
	if converged.SchemaVersion != orchestrator.SchemaVersion {
		t.Fatalf("the node came back at schema version %d, want %d", converged.SchemaVersion, orchestrator.SchemaVersion)
	}
	for i, w := range converged.WorkItems {
		if w.Escalation != nil {
			t.Fatalf("the item %d came back carrying a ledger, and the release before this one wrote none", i)
		}
	}
	if converged.Rev != before.Rev || len(converged.WorkItems) != len(before.WorkItems) ||
		len(converged.Intents) != len(before.Intents) || len(converged.Decisions) != len(before.Decisions) {
		t.Fatalf("the raise changed more than the version: rev %d -> %d, %d items -> %d, %d intents -> %d",
			before.Rev, converged.Rev, len(before.WorkItems), len(converged.WorkItems), len(before.Intents), len(converged.Intents))
	}
	if got, want := orchestrator.SpecDigest(converged.WorkItems[0]), orchestrator.SpecDigest(before.WorkItems[0]); got != want {
		t.Fatalf("the migrated item digests to %s and the one it came from to %s", got, want)
	}
	if converged.WorkItems[0].Approved == nil || !converged.WorkItems[0].Approved.At.Equal(before.WorkItems[0].Approved.At) {
		t.Fatalf("the approval the node carried came back as %+v", converged.WorkItems[0].Approved)
	}

	written, err := orchestrator.EncodeState(converged)
	if err != nil {
		t.Fatalf("the converged node does not encode: %v", err)
	}
	again, err := orchestrator.DecodeState(written)
	if err != nil {
		t.Fatalf("the node this release writes is not one it reads: %v", err)
	}
	if _, changed := orchestrator.Migrate(again); changed {
		t.Fatal("a node this release wrote is still being migrated, so every start-up would rewrite it")
	}
	rewritten, err := orchestrator.EncodeState(again)
	if err != nil {
		t.Fatalf("the converged node does not encode a second time: %v", err)
	}
	if !bytes.Equal(written, rewritten) {
		t.Fatalf("the node keeps changing as it is read:\nfirst  %s\nsecond %s", written, rewritten)
	}

	// The raise leaves a node the ladder can be recorded on: the daemon writes a
	// ledger on an item of the migrated node, and it lands.
	withLedger := mustApply(t, again, orchestrator.Daemon, "daemon", now, escalationOp("wi-2", ledger(escalation.R0, 1,
		step("", escalation.R0, escalation.ReasonRebase, now, headA))))
	if got := withLedger.WorkItems[1].Escalation; got == nil || got.Rung != escalation.R0 {
		t.Fatalf("the ledger did not land on the migrated node: %+v", got)
	}
}

package orchestrator_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// TestAgentActorCannotResolveAPersonDecision is G2's fence inside the state
// machine: a decision that waits on a person is closed by a person-class
// actor and by nothing else. The classes below are the ones that act with
// nobody watching — the session carrying the work, the daemon's own tick and
// the decision agent — and each of them must be refused, or the fence holds
// only where the CLI remembers to check it (O5) and not where the state is
// actually changed.
//
// Mutation: make Apply ignore the waiting-on class (any actor closes any
// decision) and every class below closes the decision, so the loop fails;
// make it refuse every actor and the person's own close fails at the end.
func TestAgentActorCannotResolveAPersonDecision(t *testing.T) {
	now := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	s := stateWithAPersonDecision(t, now)

	closing := orchestrator.Patch{BaseRev: s.Rev, Ops: []orchestrator.Op{{
		Op:      orchestrator.OpDecisionResolve,
		Resolve: &orchestrator.ResolveOp{ID: "ask-1", Answer: "run it"},
	}}}

	for _, class := range []orchestrator.Class{orchestrator.Session, orchestrator.Daemon, orchestrator.DecisionAgent} {
		_, err := orchestrator.Apply(s, closing, orchestrator.Actor{Class: class, Name: "session:20261107-090000-abcd", Now: now})
		if !errors.Is(err, orchestrator.ErrActorNotAllowed) {
			t.Fatalf("a %s actor closed a decision that waits on a person: err = %v, want %v", class, err, orchestrator.ErrActorNotAllowed)
		}
	}

	next, err := orchestrator.Apply(s, closing, orchestrator.Actor{Class: orchestrator.Person, Name: "owner", Now: now})
	if err != nil {
		t.Fatalf("the person could not close their own decision: %v", err)
	}
	if got := next.Decisions[0].Answer; got != "run it" {
		t.Fatalf("the closed decision's answer is %q, want %q", got, "run it")
	}
	if next.Decisions[0].ResolvedAt == nil {
		t.Fatal("the closed decision carries no resolution time")
	}
}

// TestUnknownOpIsRefused is G1 at the wire: an agent's patch is data, so the
// vocabulary it may name is closed, and an op outside it — or one carrying a
// payload its kind does not take — is refused rather than read as the nearest
// thing. There is no `summary_replace`: the state's summary is generated from
// the state on every read (D1233).
//
// Mutation: make OpKind.Known accept anything and a patch naming a
// hand-written state replacement lands; drop the payload check and an upsert's
// payload reaches an approve op's slot.
func TestUnknownOpIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want error
	}{
		{"an op this release does not have", `{"base_rev":7,"ops":[{"op":"summary_replace","goal":{"goal":"x"}}]}`, orchestrator.ErrUnknownOp},
		{"a payload another kind takes", `{"base_rev":7,"ops":[{"op":"goal_set","fact":{"key":"k","value":"v"}}]}`, orchestrator.ErrOpPayload},
		{"two payloads at once", `{"base_rev":7,"ops":[{"op":"fact_set","fact":{"key":"k","value":"v"},"goal":{"goal":"g"}}]}`, orchestrator.ErrOpPayload},
		{"no payload at all", `{"base_rev":7,"ops":[{"op":"fact_set"}]}`, orchestrator.ErrOpPayload},
		{"a patch that changes nothing", `{"base_rev":7,"ops":[]}`, orchestrator.ErrInvalid},
		{"a field the op does not carry", `{"base_rev":7,"ops":[{"op":"fact_set","fact":{"key":"k","value":"v"},"digest":"sha256:x"}]}`, orchestrator.ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := orchestrator.DecodePatch([]byte(tc.raw))
			if !errors.Is(err, tc.want) {
				t.Fatalf("DecodePatch err = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestStaleBaseRevIsRejected is what makes two writers safe on one document:
// a patch built from a revision somebody else has since moved is refused, so
// a caller re-reads and rebuilds rather than overwriting a change it never
// saw. A refusal leaves the caller's own state exactly as it was, which is
// what lets it retry with the same value.
//
// Mutation: drop the revision comparison and the stale patch lands, taking the
// state back to what its author read.
func TestStaleBaseRevIsRejected(t *testing.T) {
	now := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	s := stateWithAQueuedItem(t, now)
	note := "waiting on the sweep to retire"
	setNote := func(rev int64) orchestrator.Patch {
		return orchestrator.Patch{BaseRev: rev, Ops: []orchestrator.Op{{
			Op:       orchestrator.OpWorkItemUpsert,
			WorkItem: &orchestrator.WorkItemOp{ID: "wi-1", Note: &note},
		}}}
	}

	next := mustApply(t, s, orchestrator.Session, "session:20261107-090000-abcd", now, setNote(s.Rev).Ops...)
	if next.Rev != s.Rev+1 {
		t.Fatalf("the revision went from %d to %d, want one step", s.Rev, next.Rev)
	}

	if _, err := orchestrator.Apply(next, setNote(s.Rev), orchestrator.Actor{Class: orchestrator.Session, Now: now}); !errors.Is(err, orchestrator.ErrStaleRev) {
		t.Fatalf("a patch built from the revision before the last write landed: err = %v, want %v", err, orchestrator.ErrStaleRev)
	}
	if s.WorkItems[0].Note != "" {
		t.Fatal("the refused patch changed the state its author passed in")
	}
	if again := mustApply(t, next, orchestrator.Session, "session:20261107-090000-abcd", now, setNote(next.Rev).Ops...); again.WorkItems[0].Note != note {
		t.Fatalf("the retry left the note as %q", again.WorkItems[0].Note)
	}
	if s.WorkItems[0].Note != "" {
		t.Fatal("a patch that landed changed the state its author passed in")
	}

	// And it lands on a copy: the maps and slices of the state the caller
	// passed in are not the ones the new state carries, so a caller holding
	// both cannot change one through the other.
	facts := mustApply(t, s, orchestrator.Session, "session:20261107-090000-abcd", now, factOp("sweep.state", "retired"))
	facts.Facts["sweep.state"] = orchestrator.Fact{Value: "changed", By: orchestrator.Person}
	if got := s.Facts["sweep.state"]; got.Value != "" {
		t.Fatalf("the two states share their fact map: the old one now says %q", got.Value)
	}
}

// TestOnlyAPersonSetsGoalOrApprovesAWorkItem is the other half of G2: the goal
// is the standing instruction every loop is measured against and an approval
// is a person's go, so neither is something a session, the daemon or the
// decision agent may write. Those three act with nobody watching; if any of
// them could set the goal or approve an item, the fence would be a convention.
//
// Mutation: let any class through either op and the refusals below stop
// happening, which is a session approving its own work.
func TestOnlyAPersonSetsGoalOrApprovesAWorkItem(t *testing.T) {
	now := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	s := stateWithAQueuedItem(t, now)
	digest := orchestrator.SpecDigest(s.WorkItems[0])

	for _, class := range []orchestrator.Class{orchestrator.Session, orchestrator.Daemon, orchestrator.DecisionAgent} {
		if _, err := applyAs(t, s, class, "session:20261107-090000-abcd", now, goalOp("ship the sweep")); !errors.Is(err, orchestrator.ErrActorNotAllowed) {
			t.Fatalf("a %s actor set the goal: err = %v, want %v", class, err, orchestrator.ErrActorNotAllowed)
		}
		if _, err := applyAs(t, s, class, "session:20261107-090000-abcd", now, approveOp("wi-1", digest)); !errors.Is(err, orchestrator.ErrActorNotAllowed) {
			t.Fatalf("a %s actor approved a work item: err = %v, want %v", class, err, orchestrator.ErrActorNotAllowed)
		}
	}

	approved := mustApply(t, s, orchestrator.Person, "owner", now, approveOp("wi-1", digest))
	if approved.WorkItems[0].Approved == nil {
		t.Fatal("a person's approval did not land")
	}
	if got := mustApply(t, s, orchestrator.Person, "owner", now, goalOp("ship the sweep")).Goal; got != "ship the sweep" {
		t.Fatalf("a person's goal landed as %q", got)
	}
	// An approval names the person who gave it: a class alone is not an actor.
	if _, err := applyAs(t, s, orchestrator.Person, "", now, approveOp("wi-1", digest)); !errors.Is(err, orchestrator.ErrInvalid) {
		t.Fatalf("an approval from nobody landed: err = %v, want %v", err, orchestrator.ErrInvalid)
	}
}

// TestFactValuesAreCappedAndCleaned is the free text rule (G3) at its smallest
// scale. A fact goes into the document through the same cleaning every other
// string does — the terminal-unsafe characters out, one line, cut to the cap —
// because a fact is printed back to whoever reads the state next.
//
// Mutation: store the value as it came and the escape sequence and the newline
// below survive into the document, and the length assertion fails.
func TestFactValuesAreCappedAndCleaned(t *testing.T) {
	now := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	s := stateWithAQueuedItem(t, now)

	long := "\x1b[31m" + strings.Repeat("v", orchestrator.MaxFactValueRunes+50) + "\nsecond line"
	kept := mustApply(t, s, orchestrator.Session, "session:20260707-090000-abcd", now, factOp("sweep.state", long))
	got := kept.Facts["sweep.state"].Value
	// The house cleaning, not an ad-hoc one: the same two calls every other
	// surface puts a string through on its way into a document or a terminal.
	if want := safetext.Clip(safetext.OneLine(long), orchestrator.MaxFactValueRunes); got != want {
		t.Fatalf("the stored fact is %q and the house cleaning of what was sent is %q", got, want)
	}
	if len([]rune(got)) > orchestrator.MaxFactValueRunes {
		t.Fatalf("a fact of %d runes was stored", len([]rune(got)))
	}
	if strings.ContainsAny(got, "\x1b\n") {
		t.Fatalf("the stored fact carries an escape or a newline: %q", got)
	}
	if kept.Facts["sweep.state"].By != orchestrator.Session {
		t.Fatalf("the fact says a %s wrote it", kept.Facts["sweep.state"].By)
	}

	// A delete names a fact that is there and carries no value: a patch that
	// means one and carries the other is refused rather than guessed at.
	if _, err := applyAs(t, kept, orchestrator.Session, "session:20261107-090000-abcd", now, factOp("sweep.state", "")); !errors.Is(err, orchestrator.ErrInvalid) {
		t.Fatalf("an empty value was stored as a fact: err = %v, want %v", err, orchestrator.ErrInvalid)
	}
	deleted := mustApply(t, kept, orchestrator.Session, "session:20261107-090000-abcd", now,
		orchestrator.Op{Op: orchestrator.OpFactDelete, Fact: &orchestrator.FactOp{Key: "sweep.state"}})
	if _, ok := deleted.Facts["sweep.state"]; ok {
		t.Fatal("the fact is still there after it was deleted")
	}
	if _, err := applyAs(t, deleted, orchestrator.Session, "session:20261107-090000-abcd", now,
		orchestrator.Op{Op: orchestrator.OpFactDelete, Fact: &orchestrator.FactOp{Key: "sweep.state"}}); !errors.Is(err, orchestrator.ErrNotFound) {
		t.Fatalf("deleting a fact that is not there: err = %v, want %v", err, orchestrator.ErrNotFound)
	}
}

// TestEditAfterApprovalRevokes is what an approval is bound to. The digest
// covers every declarative field, so changing any of them makes the item
// another spec and the approval off — the person approved what they read, not
// a title that has since been rewritten under it.
//
// Mutation: stop clearing Approved when the digest moves and an approved item
// is edited into work nobody approved, still carrying its go.
func TestEditAfterApprovalRevokes(t *testing.T) {
	now := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)

	// The state carries a second item so that a dependency can name one, which
	// is what makes the DependsOn case an edit rather than a no-op.
	s := stateWithAQueuedItem(t, now)
	s.WorkItems = append(s.WorkItems, workItemOf("wi-2"))
	approved := mustApply(t, s, orchestrator.Person, "owner", now, approveOp("wi-1", orchestrator.SpecDigest(s.WorkItems[0])))

	for _, tc := range []struct {
		name string
		op   orchestrator.Op
	}{
		{"the brief", upsertOp("wi-1", func(op *orchestrator.WorkItemOp) {
			op.Brief = ptr("Move the sweep to the daemon, and retire the script.")
		})},
		{"the title", upsertOp("wi-1", func(op *orchestrator.WorkItemOp) { op.Title = ptr("land the state migration, properly") })},
		{"the repository", upsertOp("wi-1", func(op *orchestrator.WorkItemOp) { op.Repo = ptr("owner/other") })},
		{"the recipe", upsertOp("wi-1", func(op *orchestrator.WorkItemOp) { op.Recipe = ptr("thorough") })},
		{"the rigor tier", upsertOp("wi-1", func(op *orchestrator.WorkItemOp) { op.RigorTier = ptr("large") })},
		{"a dependency", upsertOp("wi-1", func(op *orchestrator.WorkItemOp) { op.DependsOn = &[]string{"wi-2"} })},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := mustApply(t, approved, orchestrator.Session, "session:20261107-090000-abcd", now, tc.op)
			if next.WorkItems[0].Approved != nil {
				t.Fatalf("editing %s left the approval standing", tc.name)
			}
		})
	}

	// A note is not part of the spec: it is what the daemon leaves for whoever
	// reads the item next, and writing one revokes nothing.
	noted := mustApply(t, approved, orchestrator.Daemon, "orchestrator-a", now,
		upsertOp("wi-1", func(op *orchestrator.WorkItemOp) { op.Note = ptr("the run is in its review round") }))
	if noted.WorkItems[0].Approved == nil {
		t.Fatal("a note revoked the approval, and a note is not part of the spec")
	}
}

// TestApprovalBindsTheFullLaunchSpec is the same binding seen from the other
// side: a digest computed for a spec that is not the item's is refused, so a
// caller cannot approve one thing and launch another. There is no partial
// approval and no field the digest leaves out.
//
// Mutation: compare only the brief, and approving a spec whose repository has
// since changed lands.
func TestApprovalBindsTheFullLaunchSpec(t *testing.T) {
	now := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	s := stateWithAQueuedItem(t, now)

	for _, tc := range []struct {
		name  string
		other orchestrator.WorkItem
	}{
		{"a different brief", withBrief(s.WorkItems[0], "Move the sweep later.")},
		{"a different repository", withRepo(s.WorkItems[0], "owner/other")},
		{"a different tier", withTier(s.WorkItems[0], "large")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := applyAs(t, s, orchestrator.Person, "owner", now, approveOp("wi-1", orchestrator.SpecDigest(tc.other)))
			if !errors.Is(err, orchestrator.ErrDigestMismatch) {
				t.Fatalf("an approval carrying the digest of another spec landed: err = %v, want %v", err, orchestrator.ErrDigestMismatch)
			}
		})
	}
}

// TestApprovalCarriesTheAttemptsItCovers is G4's number in the record rather
// than in a comment: an approval covers the attempt it was given for and one
// automatic retry, and the field says so where the daemon reads it.
//
// Mutation: write 1 (or 3) into Approval.Attempts and the daemon's retry is
// either never planned or planned twice.
func TestApprovalCarriesTheAttemptsItCovers(t *testing.T) {
	now := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	approved := mustApprove(t, now)
	a := approved.WorkItems[0].Approved
	if a.Attempts != orchestrator.ApprovalAttempts {
		t.Fatalf("the approval covers %d attempts, want %d", a.Attempts, orchestrator.ApprovalAttempts)
	}
	if orchestrator.ApprovalAttempts != 2 {
		t.Fatalf("an approval covers %d attempts: the spec says the attempt plus one retry", orchestrator.ApprovalAttempts)
	}
	if a.By != "owner" || a.At.IsZero() || !orchestrator.DigestKnown(a.SpecDigest) {
		t.Fatalf("the approval names nobody, carries no time, or binds no spec: %+v", a)
	}
}

// TestApproveCarriesTheDigestThePersonSaw is why the digest is on the op at
// all. The person approves the item as the CLI showed it to them; the digest
// they were shown is what they send, and the state refuses it if the item has
// moved since — the same trick that keeps a session from approving by
// computing a digest of work it wrote itself.
//
// Mutation: store the item's digest whatever the op carries and an approval
// for a spec that was never read lands.
func TestApproveCarriesTheDigestThePersonSaw(t *testing.T) {
	now := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	s := stateWithAQueuedItem(t, now)
	read := orchestrator.SpecDigest(s.WorkItems[0])

	// The item changes between the read and the approval: the approval is
	// refused, because what the person read is not what is there now.
	moved := mustApply(t, s, orchestrator.Session, "session:20261107-090000-abcd", now,
		upsertOp("wi-1", func(op *orchestrator.WorkItemOp) { op.Brief = ptr("Move the sweep to the daemon, and then some.") }))
	if _, err := applyAs(t, moved, orchestrator.Person, "owner", now, approveOp("wi-1", read)); !errors.Is(err, orchestrator.ErrDigestMismatch) {
		t.Fatalf("an approval of the spec as it was read landed on the spec as it is now: err = %v, want %v", err, orchestrator.ErrDigestMismatch)
	}
	if again := mustApply(t, moved, orchestrator.Person, "owner", now, approveOp("wi-1", orchestrator.SpecDigest(moved.WorkItems[0]))); again.WorkItems[0].Approved == nil {
		t.Fatal("approving the item as it is now did not land")
	}
}

// TestSessionCannotRequeueAnApprovedItem is the phase fence at the upsert: the
// session that carries an item's work may write its declaration and nothing
// that decides whether it runs. Without it a session could put an approved
// item back in the queue, or mark a dependency done, and change what the
// daemon does next without anyone approving that.
//
// Mutation: drop the daemonOnly check and each of these lands.
func TestSessionCannotRequeueAnApprovedItem(t *testing.T) {
	now := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	approved := mustApprove(t, now)
	runID := "20261107-090000-abcd"

	for _, tc := range []struct {
		name string
		op   orchestrator.Op
	}{
		{"the phase", phaseOp("wi-1", ptr(orchestrator.PhaseQueued), nil)},
		{"the run id", upsertOp("wi-1", func(op *orchestrator.WorkItemOp) { op.RunID = &runID })},
		{"the attempt count", upsertOp("wi-1", func(op *orchestrator.WorkItemOp) { op.Attempts = ptr(2) })},
		{"the spent approval", upsertOp("wi-1", func(op *orchestrator.WorkItemOp) { op.SpentApprovalAt = ptr(now) })},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := applyAs(t, approved, orchestrator.Session, "session:20261107-090000-abcd", now, tc.op)
			if !errors.Is(err, orchestrator.ErrActorNotAllowed) {
				t.Fatalf("a session wrote %s: err = %v, want %v", tc.name, err, orchestrator.ErrActorNotAllowed)
			}
		})
	}

	// The daemon writes them, which is what makes the refusal a fence rather
	// than a hole: the phase and the run id move together with the intent.
	launched := mustApply(t, approved, orchestrator.Daemon, "orchestrator-a", now,
		phaseOp("wi-1", ptr(orchestrator.PhaseRunning), &runID))
	if launched.WorkItems[0].Phase != orchestrator.PhaseRunning || launched.WorkItems[0].RunID != runID {
		t.Fatalf("the daemon's phase and run id landed as %s %q", launched.WorkItems[0].Phase, launched.WorkItems[0].RunID)
	}
}

// TestOnlyTheDaemonWritesPhaseRunIDAndAttempts is the same fence for the rest
// of the bookkeeping, and for a decision agent: nothing but the daemon's own
// tick decides what is running, for how many attempts, or under which
// approval — an agent that could would be writing the state it is supposed to
// only propose to (G1).
//
// Mutation: make the daemonOnly check look at op.Phase alone and the other
// three fields pass from a session.
func TestOnlyTheDaemonWritesPhaseRunIDAndAttempts(t *testing.T) {
	now := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	s := stateWithAQueuedItem(t, now)
	runID := "20261107-090000-abcd"
	intent := intentDeclareOp("wi-1:1", runID, orchestrator.SpecDigest(s.WorkItems[0]), now.Add(-time.Minute))

	for _, class := range []orchestrator.Class{orchestrator.Session, orchestrator.DecisionAgent} {
		if _, err := applyAs(t, s, class, "session:20261107-090000-abcd", now, intent); !errors.Is(err, orchestrator.ErrActorNotAllowed) {
			t.Fatalf("a %s actor declared an intent: err = %v, want %v", class, err, orchestrator.ErrActorNotAllowed)
		}
	}
	// The daemon may declare it, and only under a standing approval.
	if _, err := applyAs(t, s, orchestrator.Daemon, "orchestrator-a", now, intent); !errors.Is(err, orchestrator.ErrActorNotAllowed) {
		t.Fatalf("an unapproved item was launched: err = %v, want %v", err, orchestrator.ErrActorNotAllowed)
	}
}

// TestIntentCarriesTheApprovalItSpends is intent-first (D1231) and G4's
// bookkeeping in one write. The run id is minted before anything starts and
// committed with the digest of the spec and the At of the approval it spends,
// so a daemon that dies before the start re-issues the same launch; and the
// attempt the approval covers last — its retry — spends the allowance in that
// same write, which is what stops a second failure from starting a third
// attempt without a person.
//
// Mutation: do not clear Approved on the last covered attempt and the loop
// retries forever off one approval; commit the intent without the approval's
// At and two approvals become indistinguishable in the record.
func TestIntentCarriesTheApprovalItSpends(t *testing.T) {
	now := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	approved, digest := approvedState(t, now)
	first := "20261107-090000-abcd"
	second := "20261107-100000-abcd"

	launched := mustApply(t, approved, orchestrator.Daemon, "orchestrator-a", now,
		intentDeclareOp("wi-1:1", first, digest, now))
	if len(launched.Intents) != 1 {
		t.Fatalf("the patch left %d intents", len(launched.Intents))
	}
	it := launched.Intents[0]
	if it.ID != "wi-1:1" || it.RunID != first || it.SpecDigest != digest {
		t.Fatalf("the intent is %+v", it)
	}
	if !it.ApprovalAt.Equal(approved.WorkItems[0].Approved.At) {
		t.Fatalf("the intent spends the approval from %s and the one standing is from %s", it.ApprovalAt, approved.WorkItems[0].Approved.At)
	}
	if !it.At.Equal(now) || it.Outcome != "" {
		t.Fatalf("the declaration carries at = %s and outcome %q", it.At, it.Outcome)
	}
	if got := launched.WorkItems[0]; got.Phase != orchestrator.PhaseRunning || got.RunID != first || got.Attempts != 1 {
		t.Fatalf("the launch left the item at %s, run %q, attempt %d", got.Phase, got.RunID, got.Attempts)
	}
	// The first attempt clears nothing: the retry is still the approval's.
	if launched.WorkItems[0].Approved == nil || launched.WorkItems[0].SpentApprovalAt != nil {
		t.Fatalf("the first attempt spent the approval: %+v", launched.WorkItems[0])
	}
	// The state the caller passed in is not the one that was changed.
	if approved.WorkItems[0].Phase != orchestrator.PhaseQueued || len(approved.Intents) != 0 {
		t.Fatal("Apply changed the state it was given")
	}

	failed := mustApply(t, launched, orchestrator.Daemon, "orchestrator-a", now.Add(time.Minute),
		phaseOp("wi-1", ptr(orchestrator.PhaseFailed), nil))
	// The retry goes back through queued, which is the only way failed and
	// blocked reach running: committing an intent starts a run, so it moves
	// the item by the same table a phase op does.
	if _, err := applyAs(t, failed, orchestrator.Daemon, "orchestrator-a", now.Add(time.Minute),
		intentDeclareOp("wi-1:2", second, digest, now)); !errors.Is(err, orchestrator.ErrInvalid) {
		t.Fatalf("an intent started a run on a failed item: err = %v, want %v", err, orchestrator.ErrInvalid)
	}
	requeued := mustApply(t, failed, orchestrator.Daemon, "orchestrator-a", now.Add(time.Minute),
		phaseOp("wi-1", ptr(orchestrator.PhaseQueued), nil))
	retried := mustApply(t, requeued, orchestrator.Daemon, "orchestrator-a", now.Add(time.Minute),
		intentDeclareOp("wi-1:2", second, digest, now))

	if len(retried.Intents) != 2 {
		t.Fatalf("the retry left %d intents", len(retried.Intents))
	}
	after := retried.WorkItems[0]
	if after.Approved != nil {
		t.Fatal("the retry left the approval standing, so a third attempt needs nobody")
	}
	if after.SpentApprovalAt == nil || !after.SpentApprovalAt.Equal(now) {
		t.Fatalf("the retry's spent marker is %v", after.SpentApprovalAt)
	}
	if after.Attempts != 2 {
		t.Fatalf("the item says %d attempts", after.Attempts)
	}

	// A third attempt under a spent approval is refused, and so is declaring
	// the same attempt twice.
	third := intentDeclareOp("wi-1:3", "20261107-110000-abcd", digest, now)
	if _, err := applyAs(t, retried, orchestrator.Daemon, "orchestrator-a", now.Add(2*time.Minute), third); !errors.Is(err, orchestrator.ErrActorNotAllowed) {
		t.Fatalf("a third attempt started under a spent approval: err = %v, want %v", err, orchestrator.ErrActorNotAllowed)
	}
	if _, err := applyAs(t, retried, orchestrator.Daemon, "orchestrator-a", now.Add(2*time.Minute), intentDeclareOp("wi-1:2", second, digest, now)); !errors.Is(err, orchestrator.ErrIntentDeclared) {
		t.Fatalf("an attempt was declared twice: err = %v, want %v", err, orchestrator.ErrIntentDeclared)
	}

	// An outcome is recorded on the intent that was declared, and it is what
	// the fold writes: the declaration is not repeated with it.
	ended := mustApply(t, retried, orchestrator.Daemon, "orchestrator-a", now.Add(3*time.Minute),
		intentOutcomeOp("wi-1:2", orchestrator.OutcomeFailed))
	if got := ended.Intents[1].Outcome; got != orchestrator.OutcomeFailed {
		t.Fatalf("the ended intent says %q", got)
	}
	if _, err := applyAs(t, ended, orchestrator.Daemon, "orchestrator-a", now.Add(4*time.Minute),
		intentOutcomeOp("wi-1:2", orchestrator.OutcomeFailed)); !errors.Is(err, orchestrator.ErrIntentDeclared) {
		t.Fatalf("an attempt that already ended ended again: err = %v, want %v", err, orchestrator.ErrIntentDeclared)
	}
}

// TestApprovalIsClearedWhenTheItemIsDoneOrCancelled pins the rule that keeps a
// standing approval from outliving its item. A finished item left with one is
// one a later tick could relaunch: the work is over, and the approval was for
// work that is not there any more.
//
// Mutation: drop the clearing in applyUpsert and a done item carries its
// approval, which the validator then refuses — a state the release would not
// read back.
func TestApprovalIsClearedWhenTheItemIsDoneOrCancelled(t *testing.T) {
	now := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)

	for _, phase := range []orchestrator.Phase{orchestrator.PhaseDone, orchestrator.PhaseCancelled} {
		t.Run(string(phase), func(t *testing.T) {
			runID := "20261107-090000-abcd"
			approved := mustApprove(t, now)
			running := mustApply(t, approved, orchestrator.Daemon, "orchestrator-a", now,
				phaseOp("wi-1", ptr(orchestrator.PhaseRunning), &runID))
			next := mustApply(t, running, orchestrator.Daemon, "orchestrator-a", now, phaseOp("wi-1", &phase, nil))
			if next.WorkItems[0].Approved != nil {
				t.Fatalf("an item moved to %s still carries its approval", phase)
			}
			if _, err := orchestrator.EncodeState(next); err != nil {
				t.Fatalf("the state after the move is not one this release reads back: %v", err)
			}
		})
	}

	// And the document the rule protects against is refused where it is read:
	// a done item with a standing approval is not a state this release reads.
	bad := mustApprove(t, now)
	phase := orchestrator.PhaseDone
	bad.WorkItems[0].Phase = phase
	raw, err := json.Marshal(bad)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := orchestrator.DecodeState(raw); !errors.Is(err, orchestrator.ErrInvalid) {
		t.Fatalf("a done item with a standing approval decoded: err = %v, want %v", err, orchestrator.ErrInvalid)
	}
}

// TestUpsertCannotSetApproved is the wire's half of the approval fence. There
// is no approval field on an upsert for any actor: an approval comes from
// work_item_approve and from nothing else, because the digest it binds is
// computable by anyone who can read the item. A patch that carries one is a
// field this release does not model, so the decode refuses it rather than
// applying the rest.
//
// Mutation: give WorkItemOp an Approved field and this patch decodes, which is
// a session approving through an upsert.
func TestUpsertCannotSetApproved(t *testing.T) {
	digest := "sha256:" + strings.Repeat("ab", 32)
	raw := `{"base_rev":7,"ops":[{"op":"work_item_upsert","work_item":{"id":"wi-1","approved":{"by":"owner","at":"2026-11-07T09:00:00Z","spec_digest":"` + digest + `","attempts":2}}}]}`
	if _, err := orchestrator.DecodePatch([]byte(raw)); !errors.Is(err, orchestrator.ErrInvalid) {
		t.Fatalf("an upsert carrying an approval decoded: err = %v, want %v", err, orchestrator.ErrInvalid)
	}

	// Nor is there a name for an approval on the daemon's own upsert: the
	// write that spends one is the intent's, and it is not this op.
	withoutTheDigest := `{"base_rev":7,"ops":[{"op":"work_item_upsert","work_item":{"id":"wi-1","approved":true}}]}`
	if _, err := orchestrator.DecodePatch([]byte(withoutTheDigest)); !errors.Is(err, orchestrator.ErrInvalid) {
		t.Fatalf("an upsert carrying anything named approved decoded: err = %v, want %v", err, orchestrator.ErrInvalid)
	}
}

// TestRetryCountsOnlyIntentsOfTheStandingApproval is why an approval is
// counted off the intents stamped with its own At rather than off the item's
// attempt number. A person who edits and re-approves an item gives it a new
// allowance, and the attempts the previous approval covered are not this one's
// to spend — while the attempt *numbers* run on, because an intent's id is the
// item and the attempt and stays unique for the item's life.
//
// Mutation: count the item's attempts instead (AttemptsUnder -> w.Attempts) and
// the re-approved item starts with its allowance already spent, so a person's
// second approval buys nothing.
func TestRetryCountsOnlyIntentsOfTheStandingApproval(t *testing.T) {
	first := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	second := first.Add(time.Hour)

	approved, digest := approvedState(t, first)
	launched := mustApply(t, approved, orchestrator.Daemon, "orchestrator-a", first,
		intentDeclareOp("wi-1:1", "20261107-090000-abcd", digest, first))
	failed := mustApply(t, launched, orchestrator.Daemon, "orchestrator-a", first, phaseOp("wi-1", ptr(orchestrator.PhaseFailed), nil))
	if got := orchestrator.AttemptsUnder(failed, "wi-1", first); got != 1 {
		t.Fatalf("the first approval covers %d committed attempts, want 1", got)
	}

	// The person edits the brief — which revokes — and approves again.
	edited := mustApply(t, failed, orchestrator.Person, "owner", second,
		upsertOp("wi-1", func(op *orchestrator.WorkItemOp) { op.Brief = ptr("Move the sweep to the daemon, in one step.") }))
	reapproved := mustApply(t, edited, orchestrator.Person, "owner", second,
		approveOp("wi-1", orchestrator.SpecDigest(edited.WorkItems[0])))
	if got := orchestrator.AttemptsUnder(reapproved, "wi-1", second); got != 0 {
		t.Fatalf("the new approval starts with %d committed attempts, want 0", got)
	}
	if got := orchestrator.AttemptsUnder(reapproved, "wi-1", first); got != 1 {
		t.Fatalf("the first approval covers %d committed attempts, want 1", got)
	}

	// The next launch is attempt 2 — the numbering runs on — and it is the new
	// approval's first, so it does not spend the allowance. It comes out of
	// queued, which is where the failed item is put back before a retry.
	next := orchestrator.SpecDigest(reapproved.WorkItems[0])
	requeued := mustApply(t, reapproved, orchestrator.Daemon, "orchestrator-a", second,
		phaseOp("wi-1", ptr(orchestrator.PhaseQueued), nil))
	retry := mustApply(t, requeued, orchestrator.Daemon, "orchestrator-a", second,
		intentDeclareOp("wi-1:2", "20261107-100000-abcd", next, second))
	if got := retry.WorkItems[0]; got.Attempts != 2 || got.Approved == nil {
		t.Fatalf("the re-approved item's first attempt left it at attempt %d with approval %v", got.Attempts, got.Approved)
	}
	if got := orchestrator.AttemptsUnder(retry, "wi-1", second); got != 1 {
		t.Fatalf("the new approval covers %d committed attempts, want 1", got)
	}

	// Its retry is attempt 3 and it is the one that spends.
	failedAgain := mustApply(t, retry, orchestrator.Daemon, "orchestrator-a", second, phaseOp("wi-1", ptr(orchestrator.PhaseFailed), nil))
	requeuedAgain := mustApply(t, failedAgain, orchestrator.Daemon, "orchestrator-a", second,
		phaseOp("wi-1", ptr(orchestrator.PhaseQueued), nil))
	spent := mustApply(t, requeuedAgain, orchestrator.Daemon, "orchestrator-a", second,
		intentDeclareOp("wi-1:3", "20261107-110000-abcd", next, second))
	if spent.WorkItems[0].Approved != nil || spent.WorkItems[0].SpentApprovalAt == nil {
		t.Fatalf("the new approval's retry left it standing: %+v", spent.WorkItems[0])
	}
	if got := orchestrator.AttemptsUnder(spent, "wi-1", second); got != 2 {
		t.Fatalf("the new approval covers %d committed attempts, want its two", got)
	}
}

// stateWithAPersonDecision is a state with one queued work item and one
// question waiting on a person.
func stateWithAPersonDecision(t *testing.T, now time.Time) orchestrator.State {
	t.Helper()
	s := stateWithAQueuedItem(t, now)
	s.Decisions = []orchestrator.Decision{{
		ID:        "ask-1",
		Question:  "the migration needs a person's call: keep the old node or delete it?",
		WaitingOn: orchestrator.OnPerson,
		At:        now.Add(-time.Hour),
	}}
	s.Facts = map[string]orchestrator.Fact{"repo.primary": {Value: "owner/repo", By: orchestrator.Daemon}}
	return s
}

// mustApprove is the state after a person has approved its one work item.
func mustApprove(t *testing.T, now time.Time) orchestrator.State {
	t.Helper()
	s := stateWithAQueuedItem(t, now)
	return mustApply(t, s, orchestrator.Person, "owner", now, approveOp("wi-1", orchestrator.SpecDigest(s.WorkItems[0])))
}

// approvedState is the same, with the digest the approval binds.
func approvedState(t *testing.T, now time.Time) (orchestrator.State, orchestrator.Digest) {
	t.Helper()
	s := stateWithAQueuedItem(t, now)
	digest := orchestrator.SpecDigest(s.WorkItems[0])
	return mustApply(t, s, orchestrator.Person, "owner", now, approveOp("wi-1", digest)), digest
}

// applyAs sends one patch as a class of actor and hands back what came of it.
func applyAs(t *testing.T, s orchestrator.State, class orchestrator.Class, name string, now time.Time, ops ...orchestrator.Op) (orchestrator.State, error) {
	t.Helper()
	return orchestrator.Apply(s, orchestrator.Patch{BaseRev: s.Rev, Ops: ops}, orchestrator.Actor{Class: class, Name: name, Now: now})
}

// mustApply is applyAs for the patches a test expects to land.
func mustApply(t *testing.T, s orchestrator.State, class orchestrator.Class, name string, now time.Time, ops ...orchestrator.Op) orchestrator.State {
	t.Helper()
	next, err := applyAs(t, s, class, name, now, ops...)
	if err != nil {
		t.Fatalf("a %s patch did not land: %v", class, err)
	}
	return next
}

func ptr[T any](v T) *T { return &v }

func upsertOp(id string, change func(*orchestrator.WorkItemOp)) orchestrator.Op {
	op := &orchestrator.WorkItemOp{ID: id}
	change(op)
	return orchestrator.Op{Op: orchestrator.OpWorkItemUpsert, WorkItem: op}
}

func phaseOp(id string, phase *orchestrator.Phase, runID *string) orchestrator.Op {
	return orchestrator.Op{Op: orchestrator.OpWorkItemUpsert, WorkItem: &orchestrator.WorkItemOp{ID: id, Phase: phase, RunID: runID}}
}

func approveOp(id string, digest orchestrator.Digest) orchestrator.Op {
	return orchestrator.Op{Op: orchestrator.OpWorkItemApprove, Approve: &orchestrator.ApproveOp{ID: id, SpecDigest: digest}}
}

func goalOp(goal string) orchestrator.Op {
	return orchestrator.Op{Op: orchestrator.OpGoalSet, Goal: &orchestrator.GoalOp{Goal: goal}}
}

func factOp(key, value string) orchestrator.Op {
	return orchestrator.Op{Op: orchestrator.OpFactSet, Fact: &orchestrator.FactOp{Key: key, Value: value}}
}

// intentDeclareOp is the upsert that declares an attempt: the item is the one
// the intent's id names, and the run id, the digest and the approval's At come
// with it.
func intentDeclareOp(id, runID string, digest orchestrator.Digest, approvalAt time.Time) orchestrator.Op {
	return orchestrator.Op{Op: orchestrator.OpWorkItemUpsert, WorkItem: &orchestrator.WorkItemOp{
		ID: orchestrator.IntentItem(id),
		Intent: &orchestrator.IntentOp{
			ID:         id,
			RunID:      &runID,
			SpecDigest: &digest,
			ApprovalAt: &approvalAt,
		},
	}}
}

// intentOutcomeOp is the later upsert that records how a declared attempt
// ended.
func intentOutcomeOp(id string, outcome orchestrator.Outcome) orchestrator.Op {
	return orchestrator.Op{Op: orchestrator.OpWorkItemUpsert, WorkItem: &orchestrator.WorkItemOp{
		ID:     orchestrator.IntentItem(id),
		Intent: &orchestrator.IntentOp{ID: id, Outcome: &outcome},
	}}
}

func withBrief(w orchestrator.WorkItem, brief string) orchestrator.WorkItem {
	w.Brief = brief
	return w
}

func withRepo(w orchestrator.WorkItem, repo string) orchestrator.WorkItem { w.Repo = repo; return w }

func withTier(w orchestrator.WorkItem, tier string) orchestrator.WorkItem {
	w.RigorTier = tier
	return w
}

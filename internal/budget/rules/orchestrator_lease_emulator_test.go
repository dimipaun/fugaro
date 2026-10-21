//go:build firebase

package rules

// The emulator cases for the orchestrators lease (central-orchestrator plan P3,
// decisions D1, D2, D3): the `orchestrators/<slug>/<job>/lease` subtree, the
// seat a shepherd holds while it acts on a job.
//
// This is the *orchestrator's* lease, and it is a different thing from the
// runner's: lease_emulator_test.go, beside this file, holds internal/budget's
// budget.Session client to the rules for the reservation a run takes, and this
// one holds the seat of P3's protocol. They share the harness and nothing else.
//
// The unit tests in internal/lease prove the *client's* half of the invariant —
// the compare-and-set, the fencing token, the database's clock — against
// internal/gcpfake, which implements no rules at all. What only the emulator
// can prove is the half that makes the seat a seat: that the deployed document
// refuses a takeover of a fresh lease, refuses a renewal by anyone but the
// recorded holder, refuses a generation that jumps, and refuses every delete —
// so an orchestrator that is not running internal/lease's code cannot take the
// seat by writing the node directly. This is the plan's U3: the race the
// protocol implies is made deterministic here, on the real rules engine.
//
// The cases are mutation-proven like the rest of the suite: drop LEASETAKE's
// staleness test, or the generation's `NEW == OLD + 1`, and watch the case that
// names it fail.
//
// One case cannot be made to reach its boundary through the harness: the
// exactly-at-the-grace takeover. The emulator stamps the Date header with
// one-second resolution and internal/rtdb's ServerNow projects it forward by
// the real time elapsed, so the client's reading is always a little past the
// instant a test asks the emulator for. The boundary itself is pinned in
// internal/lease (TestTakeoverIsRefusedWhileTheLeaseIsFresh, through a database
// whose clock is fixed) against the same clause of this template.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/rtdb"
)

// leasePath is one job's lease in the slug the harness's run tokens carry.
const leasePath = "orchestrators/aurora/r1/lease"

// leaseGraceMs is the number the rules bake in (rules.LeaseGraceMs). It is
// written here rather than referenced so that a change to the constant has to
// come through this file: two cases below seed a stamp on either side of it,
// and a grace that moved would make one of them pass for the wrong reason.
const leaseGraceMs int64 = 600_000

// leaseRecord is a record as internal/lease writes one: the four required
// fields, and the two optional ones when a caller adds them.
func leaseRecord(uid string, generation, renewedAt int64) map[string]any {
	return map[string]any{
		"holder": "orchestrator " + uid, "holder_uid": uid,
		"generation": generation, "renewed_at": renewedAt,
	}
}

// take is one client's write of a lease, as the compare-and-set sends it: the
// node's ETag read first, then a PUT carrying it. It returns the error, so a
// case can assert that a refused write is refused by the *rules* (401) rather
// than by the ETag (412) — the two are different failures, and only the first
// is what these cases are about.
func take(h *harness, c *rtdb.Client, rec map[string]any) error {
	h.t.Helper()
	var cur map[string]any
	etag, _, err := c.GetETag(context.Background(), leasePath, &cur)
	if err != nil {
		h.t.Fatalf("reading the lease's ETag: %v", err)
	}
	return c.PutIfMatch(context.Background(), leasePath, etag, rec)
}

// TestLeaseTakeRenewReleaseAndTakeAgain walks one seat through its whole life
// with the rules deployed: created at generation 1, renewed in place by the
// holder, released to an empty holder at generation 2, and taken again at
// generation 3 without waiting out a grace. Every step is a write the rules
// must accept, so a document that refused one of them would leave the protocol
// unable to run at all.
//
// Hand mutation this fails on: making the creation clause require data.exists()
// (no seat can ever be taken), or dropping LEASERELEASE (a holder cannot give
// the seat up), or requiring a grace before a takeover of a free seat (the next
// taker is refused for ten minutes after a release).
func TestLeaseTakeRenewReleaseAndTakeAgain(t *testing.T) {
	h := newHarness(t)
	alice, bob := h.phone("alice@example.invalid"), h.phone("bob@example.invalid")
	now := time.Now().UnixMilli()

	if err := take(h, alice, leaseRecord("alice@example.invalid", 1, now)); err != nil {
		t.Fatalf("taking a free seat at generation 1: %v", err)
	}
	h.mustRead(alice, leasePath)

	// The heartbeat: the same holder, the same generation, a new stamp.
	if err := take(h, alice, leaseRecord("alice@example.invalid", 1, time.Now().UnixMilli())); err != nil {
		t.Fatalf("renewing the seat: %v", err)
	}

	// The release: the generation moves on and the holder is emptied.
	free := map[string]any{"holder": "", "holder_uid": "", "generation": 2, "renewed_at": time.Now().UnixMilli()}
	if err := take(h, alice, free); err != nil {
		t.Fatalf("releasing the seat: %v", err)
	}

	// The seat is free, so the next orchestrator takes it without waiting.
	if err := take(h, bob, leaseRecord("bob@example.invalid", 3, time.Now().UnixMilli())); err != nil {
		t.Fatalf("taking a released seat at generation 3: %v", err)
	}
}

// TestLeaseTakeoverOfAFreshSeatIsRefused is the invariant on the real engine:
// another orchestrator writes generation 2 over a lease that is seconds old and
// is refused. This is the case internal/gcpfake cannot prove — the fake has no
// rules, so only the emulator says the *database* is what refuses it.
//
// Hand mutation this fails on: dropping LEASETAKE's `now - renewed_at >
// LeaseGraceMs` clause (any orchestrator may take any seat whenever it likes,
// which is two orchestrators on one job).
func TestLeaseTakeoverOfAFreshSeatIsRefused(t *testing.T) {
	h := newHarness(t)
	h.phone("alice@example.invalid")
	bob := h.phone("bob@example.invalid")
	h.set(leasePath, leaseRecord("alice@example.invalid", 1, time.Now().UnixMilli()))

	if err := take(h, bob, leaseRecord("bob@example.invalid", 2, time.Now().UnixMilli())); !errors.Is(err, rtdb.ErrPermission) {
		t.Fatalf("a takeover of a fresh seat: %v, want the rules to refuse it (401)", err)
	}
	// And the seat is still Alice's, at generation 1.
	if got := h.value(leasePath); !strings.Contains(got, `"holder_uid":"alice@example.invalid"`) || !strings.Contains(got, `"generation":1`) {
		t.Fatalf("after the refused takeover the lease is %s, want Alice at generation 1", got)
	}
}

// TestLeaseTakeoverOfAStaleSeatIsAllowed is the other side of the same clause:
// a seat whose holder has not renewed for longer than the grace is takeable,
// which is what makes a crashed orchestrator recoverable at all.
//
// Hand mutation this fails on: turning the staleness test around, or removing
// the takeover branch entirely (a dead holder's job is stuck forever).
func TestLeaseTakeoverOfAStaleSeatIsAllowed(t *testing.T) {
	h := newHarness(t)
	h.phone("alice@example.invalid")
	bob := h.phone("bob@example.invalid")
	stale := time.Now().UnixMilli() - leaseGraceMs - 60_000
	h.set(leasePath, leaseRecord("alice@example.invalid", 1, stale))

	if err := take(h, bob, leaseRecord("bob@example.invalid", 2, time.Now().UnixMilli())); err != nil {
		t.Fatalf("a takeover of a stale seat: %v, want it allowed", err)
	}
	if got := h.value(leasePath); !strings.Contains(got, `"holder_uid":"bob@example.invalid"`) || !strings.Contains(got, `"generation":2`) {
		t.Fatalf("after the takeover the lease is %s, want Bob at generation 2", got)
	}

	// Thirty seconds *inside* the grace, and the same write is refused: the
	// boundary is the grace, and not "some time has passed".
	h.set(leasePath, leaseRecord("alice@example.invalid", 1, time.Now().UnixMilli()-leaseGraceMs+30_000))
	if err := take(h, bob, leaseRecord("bob@example.invalid", 2, time.Now().UnixMilli())); !errors.Is(err, rtdb.ErrPermission) {
		t.Fatalf("a takeover 30 s inside the grace: %v, want the rules to refuse it", err)
	}
}

// TestLeaseOnlyTheRecordedHolderMayRenewOrRelease: the fencing token's
// enforcement. A renewal keeps the generation, and the rules allow that only
// when the writer is the identity already on the record — so a stale holder
// whose seat was taken over cannot renew it back, and nobody can free a seat
// they are not sitting in.
//
// Hand mutation this fails on: dropping the `data.parent().child('holder_uid')
// == auth.uid` conjunct from LEASEKEEP (any account keeps any generation), or
// from LEASERELEASE (any account frees anyone's seat, which hands the job to
// whoever asks next).
func TestLeaseOnlyTheRecordedHolderMayRenewOrRelease(t *testing.T) {
	h := newHarness(t)
	alice, bob := h.phone("alice@example.invalid"), h.phone("bob@example.invalid")
	now := time.Now().UnixMilli()
	h.set(leasePath, leaseRecord("alice@example.invalid", 1, now))

	// Bob renewing Alice's generation in place: refused.
	if err := take(h, bob, leaseRecord("bob@example.invalid", 1, now)); !errors.Is(err, rtdb.ErrPermission) {
		t.Fatalf("a renewal by another account: %v, want the rules to refuse it", err)
	}
	// Bob releasing Alice's seat: refused.
	free := map[string]any{"holder": "", "holder_uid": "", "generation": 2, "renewed_at": now}
	if err := take(h, bob, free); !errors.Is(err, rtdb.ErrPermission) {
		t.Fatalf("a release by another account: %v, want the rules to refuse it", err)
	}
	// Alice's own renewal and release are accepted, so the two refusals above
	// are the rules reading the holder and not the rules refusing everything.
	if err := take(h, alice, leaseRecord("alice@example.invalid", 1, time.Now().UnixMilli())); err != nil {
		t.Fatalf("the holder's own renewal: %v", err)
	}
	if err := take(h, alice, free); err != nil {
		t.Fatalf("the holder's own release: %v", err)
	}
}

// TestLeaseGenerationCannotJumpOrStandStill is the fencing token itself: the
// generation moves by one per takeover or release and by nothing else. A jump
// would let a writer land on a number that is not the successor of the one it
// read, which is a generation two orchestrators could both believe they hold; a
// standstill with a changed holder is a takeover with the token left behind,
// which is what makes the old holder's next call look valid.
//
// Hand mutation this fails on: dropping `NEW == OLD + 1` (any generation may be
// written over any other), or `NEW == OLD` from LEASEKEEP.
func TestLeaseGenerationCannotJumpOrStandStill(t *testing.T) {
	h := newHarness(t)
	bob := h.phone("bob@example.invalid")
	stale := time.Now().UnixMilli() - leaseGraceMs - 60_000
	h.set(leasePath, leaseRecord("alice@example.invalid", 5, stale))

	for _, c := range []struct {
		why string
		rec map[string]any
	}{
		{"a takeover that jumps to generation 7", leaseRecord("bob@example.invalid", 7, time.Now().UnixMilli())},
		{"a takeover that keeps generation 5 with a new holder", leaseRecord("bob@example.invalid", 5, time.Now().UnixMilli())},
		{"a takeover that moves the generation backwards", leaseRecord("bob@example.invalid", 4, time.Now().UnixMilli())},
		{"a creation on a node that already exists", leaseRecord("bob@example.invalid", 1, time.Now().UnixMilli())},
	} {
		if err := take(h, bob, c.rec); !errors.Is(err, rtdb.ErrPermission) {
			t.Errorf("%s: %v, want the rules to refuse it", c.why, err)
		}
	}
	// The successor is accepted, so the four refusals above are the clause and
	// not the rules refusing every write to a stale seat.
	if err := take(h, bob, leaseRecord("bob@example.invalid", 6, time.Now().UnixMilli())); err != nil {
		t.Fatalf("the successor generation: %v", err)
	}
}

// TestLeaseRecordShapeIsTheRulesOwn: the record is exactly the six fields
// internal/lease writes. A seventh is refused ($other), a missing one is
// refused (hasChildren), a holder_uid that is not the writer's own identity is
// refused — which is what stops an orchestrator claiming someone else's name on
// the record — and a stamp ahead of the database's clock is refused, which is
// what stops a writer whose machine is fast from holding a seat nobody can take
// over.
//
// Hand mutation this fails on: dropping `$other: false` (a hand-written node
// can carry anything, the expiry internal/lease refuses to store included), or
// the `newData.val() <= now` bound on renewed_at (a stamp in the year 3000).
func TestLeaseRecordShapeIsTheRulesOwn(t *testing.T) {
	h := newHarness(t)
	alice := h.phone("alice@example.invalid")
	now := time.Now().UnixMilli()

	with := func(k string, v any) map[string]any {
		m := leaseRecord("alice@example.invalid", 1, now)
		m[k] = v
		return m
	}
	for _, c := range []struct {
		why string
		rec map[string]any
	}{
		{"a field the record does not have", with("expires_at", now+leaseGraceMs)},
		{"a holder_uid that is not the writer's", leaseRecord("bob@example.invalid", 1, now)},
		{"a holder_uid that is empty on a held seat", map[string]any{"holder": "alice", "holder_uid": "", "generation": 1, "renewed_at": now}},
		{"a held seat with no name", map[string]any{"holder": "", "holder_uid": "alice@example.invalid", "generation": 1, "renewed_at": now}},
		{"a stamp an hour ahead of the database", leaseRecord("alice@example.invalid", 1, now+3_600_000)},
		{"a stamp at zero", leaseRecord("alice@example.invalid", 1, 0)},
		{"a generation that is not a whole number", map[string]any{"holder": "alice", "holder_uid": "alice@example.invalid", "generation": 1.5, "renewed_at": now}},
		{"a generation below one", leaseRecord("alice@example.invalid", 0, now)},
	} {
		if err := take(h, alice, c.rec); !errors.Is(err, rtdb.ErrPermission) {
			t.Errorf("%s: %v, want the rules to refuse it", c.why, err)
		}
	}
	// A record with a required field missing.
	missing := leaseRecord("alice@example.invalid", 1, now)
	delete(missing, "renewed_at")
	if err := take(h, alice, missing); !errors.Is(err, rtdb.ErrPermission) {
		t.Errorf("a record with no renewed_at: %v, want the rules to refuse it", err)
	}
	// The optional two are accepted, so the refusals above are the shape clause
	// and not the rules refusing every write this identity makes.
	ok := leaseRecord("alice@example.invalid", 1, now)
	ok["mode"], ok["pr"] = "enforce", 42
	if err := take(h, alice, ok); err != nil {
		t.Fatalf("a record with a mode and a PR: %v", err)
	}
}

// TestLeaseIsNeverDeleted: the generation must not restart, and the whole point
// of Release is that it writes an empty holder instead of deleting the node. A
// delete would take the fencing token with it, and the next taker would begin
// at generation 1 — a number the released holder's token still is.
//
// Hand mutation this fails on: dropping `newData.exists()` from the lease's
// .write (a client deletes the node and the token restarts), which is also the
// mutation TestWritesOnlyAtNumericLeaves exists for on the ledgers.
func TestLeaseIsNeverDeleted(t *testing.T) {
	h := newHarness(t)
	alice := h.phone("alice@example.invalid")
	h.set(leasePath, leaseRecord("alice@example.invalid", 1, time.Now().UnixMilli()))

	if err := take(h, alice, nil); !errors.Is(err, rtdb.ErrPermission) {
		t.Fatalf("deleting the lease: %v, want the rules to refuse it", err)
	}
	if got := h.value(leasePath); !strings.Contains(got, `"generation":1`) {
		t.Fatalf("after the refused delete the lease is %s, want generation 1 still there", got)
	}
}

// TestLeaseIsTheUserClaimsAndNobodyElses: the grant is USER and nothing else.
// A run token reads and writes nothing there — a job's own token must not be
// able to take the seat of the orchestrator shepherding it — and an account
// with no device node (D166) is admitted nowhere, lease included.
//
// The two write attempts go through rawDenied rather than take: a client that
// cannot read the node cannot read its ETag either, so the honest question for
// these identities is whether the rules accept a write *at all*, and the answer
// is measured on a PUT that carries no If-Match and is refused on its own.
//
// Hand mutation this fails on: widening the lease's .read or .write to TOKEN,
// RUN or `auth != null` (any run of the project takes the seat), or dropping
// USER's device conjunct.
func TestLeaseIsTheUserClaimsAndNobodyElses(t *testing.T) {
	h := newHarness(t)
	h.set(leasePath, leaseRecord("alice@example.invalid", 1, time.Now().UnixMilli()))

	body := `{"holder":"x","holder_uid":"x","generation":2,"renewed_at":1}`
	runner := h.run("aurora", "r1")
	h.mustReadDenied(runner, leasePath)
	h.rawDenied(claims{slug: "aurora", run: "r1"}, "PUT", leasePath, body)

	// A phone token that was never paired: mu is its own uid, there is no
	// device node, so USER is false and the seat is out of reach.
	unpaired := h.unpaired("carol@example.invalid")
	h.mustReadDenied(unpaired, leasePath)
	h.rawDenied(claims{mu: "carol@example.invalid", fp: "-"}, "PUT", leasePath, body)
}

// TestLeaseTwoOrchestratorsRaceForOneSeat is the plan's race, on the real
// engine: two orchestrators read the same ETag and both write. Exactly one
// write lands; the other is refused, and the seat ends at the generation the
// winner wrote with one holder in it.
//
// The loser is refused twice over, and which refusal arrives is the engine's
// business: the compare-and-set (ErrPrecondition — the ETag moved under it) is
// what makes it lose *this* write, and the rules (ErrPermission — a second
// generation-1 write on a node that now exists) would refuse it anyway. Both
// are accepted here; exactly one winner is not.
//
// Hand mutation this fails on: sending the write without If-Match (both land,
// the second silently overwrites the first, and the orchestrator that was
// overwritten still believes it holds the seat), which is the mutation
// internal/lease's TestEveryWriteIsConditional pins on the client side.
func TestLeaseTwoOrchestratorsRaceForOneSeat(t *testing.T) {
	h := newHarness(t)
	alice, bob := h.phone("alice@example.invalid"), h.phone("bob@example.invalid")

	// Both read the absent node's ETag before either writes, which is what
	// makes the race the race: one read, two writes.
	var cur map[string]any
	etagA, _, err := alice.GetETag(context.Background(), leasePath, &cur)
	if err != nil {
		t.Fatal(err)
	}
	etagB, _, err := bob.GetETag(context.Background(), leasePath, &cur)
	if err != nil {
		t.Fatal(err)
	}
	if etagA != etagB {
		t.Fatalf("the two readers saw different ETags (%q, %q) on an absent node: this is not the race the case is about", etagA, etagB)
	}

	racers := []struct {
		cl   *rtdb.Client
		etag string
		uid  string
	}{{alice, etagA, "alice@example.invalid"}, {bob, etagB, "bob@example.invalid"}}

	var wg sync.WaitGroup
	errs := make([]error, len(racers))
	for i, r := range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = r.cl.PutIfMatch(context.Background(), leasePath, r.etag, leaseRecord(r.uid, 1, time.Now().UnixMilli()))
		}()
	}
	wg.Wait()

	winners := 0
	for i, err := range errs {
		switch {
		case err == nil:
			winners++
		case errors.Is(err, rtdb.ErrPrecondition), errors.Is(err, rtdb.ErrPermission):
			// The loser: the ETag moved, or the rules caught the second write
			// to a node that now exists. Either way it did not land.
		default:
			t.Errorf("racer %d failed with %v, want nil, rtdb.ErrPrecondition or rtdb.ErrPermission", i, err)
		}
	}
	if winners != 1 {
		t.Fatalf("%d orchestrators won one seat, want exactly 1 (database now: %s)", winners, h.value(leasePath))
	}
	got := h.value(leasePath)
	if !strings.Contains(got, `"generation":1`) {
		t.Fatalf("the seat after the race is %s, want generation 1", got)
	}
	a := strings.Contains(got, `"holder_uid":"alice@example.invalid"`)
	b := strings.Contains(got, `"holder_uid":"bob@example.invalid"`)
	if a == b {
		t.Fatalf("the seat after the race is %s, want exactly one of the two holders in it", got)
	}
}

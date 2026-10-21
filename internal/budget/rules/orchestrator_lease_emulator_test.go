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
// recorded holder, refuses a generation that jumps, refuses a write to one
// field of the record, refuses a stamp the writer computed, and refuses every
// delete — so an orchestrator that is not running internal/lease's code cannot
// take the seat by writing the node directly. This is the plan's U3: the race
// the protocol implies is made deterministic here, on the real rules engine.
//
// Two things about the engine decide how the document is written, and both were
// measured here rather than assumed (TestLeaseFieldByFieldWritesAreRefused and
// its neighbours are what measure them):
//
//   - A `.validate` is consulted for the location written and below it, never
//     for an ancestor — so a transition on the lease node's `.validate` is not
//     read for a write to `lease/holder_uid`. A `.write` *is* read for the
//     location written and every ancestor, with data/newData at the ancestor,
//     which is why the transition is the lease node's `.write`.
//   - A grant cascades down: a `.write: false` on a child does not withdraw an
//     ancestor's grant. What refuses a field-by-field write is the ancestor's
//     predicate, not a denial below it.
//
// The cases are mutation-proven like the rest of the suite: drop a clause of
// leaseWrite, or the `NEW == now` bound on the stamp, and watch the case that
// names it fail.
//
// One boundary is not reachable through the harness: the exactly-at-the-grace
// takeover. The emulator stamps the Date header with one-second resolution and
// internal/rtdb's ServerNow projects it forward by the real time elapsed, so the
// client's reading is always a little past the instant a test asks the emulator
// for. The boundary itself is pinned in internal/lease
// (TestTakeoverIsRefusedWhileTheLeaseIsFresh, through a database whose clock is
// fixed) against the same clause of this template, character for character
// (TestTheRulesCarryTheLeaseAndTheGraceWeApply).

import (
	"context"
	"errors"
	"net/http"
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

// The device uids these cases use, and the mailboxes they are paired through.
// They are key-safe slugs and not email addresses: a uid becomes a database key
// (config/admin/devices/<uid>), and the database refuses a key with a dot in it
// — which is why the repo's own mailbox cases use `alice-uid` and `bob-uid`
// (emulator_mailbox_test.go). An email is a claim *value* and a holder's name;
// it is never a key.
const (
	aliceUID = "alice-uid"
	bobUID   = "bob-uid"
	carolUID = "carol-uid"
)

// holderName is the human-readable half of a holder: what a person reads on the
// record, as opposed to the uid the database checks the writer against.
func holderName(uid string) string { return "orchestrator " + uid }

// leaseRecord is a record as internal/lease writes one: the four required
// fields, and renewed_at carrying the database's own server value rather than
// any number. It is what every client write in these cases sends.
func leaseRecord(uid string, generation int64) map[string]any {
	return map[string]any{
		"holder": holderName(uid), "holder_uid": uid,
		"generation": generation, "renewed_at": rtdb.ServerTimestamp{},
	}
}

// leaseSeed is a record as the *admin* writes one for a case that needs a
// lease of a known age: the same fields with a stamp of the test's choosing.
// The rules are not consulted on this path (as IAM is not), which is the point:
// a case seeds the state it wants and then asks the rules about the write.
func leaseSeed(uid string, generation, renewedAt int64) map[string]any {
	rec := leaseRecord(uid, generation)
	rec["renewed_at"] = renewedAt
	return rec
}

// leaseFree is a released lease: the node stays (the generation must not
// restart), the holder is empty, and the seat is anyone's to take. It carries
// the database's stamp like every other write to the node — the rules ask for
// one on each, and a release is a write like any other: the record it leaves
// says when it was given up.
func leaseFree(generation int64) map[string]any {
	rec := leaseRecord("", generation)
	rec["holder"] = ""
	return rec
}

// stale is a stamp the grace has certainly passed: the grace plus a minute, so
// a case that means "expired" cannot be a case that means "nearly expired".
func stale() int64 { return time.Now().UnixMilli() - leaseGraceMs - 60_000 }

// createBody is a lease write the rules accept from a paired device: a create
// at generation 1 on an absent node, stamped by the database, naming the writer
// as its own holder. Cases that are about the *identity* use it so that the
// body cannot be the reason they are refused — a body the transition would
// refuse anyway would make such a case pass for the wrong reason.
func createBody(uid string) string {
	return `{"holder":"` + holderName(uid) + `","holder_uid":"` + uid + `","generation":1,"renewed_at":{".sv":"timestamp"}}`
}

// take is one client's write of a lease, as the compare-and-set sends it: the
// node's ETag read first, then a PUT carrying it. It returns the error, so a
// case can assert that a refused write is refused by the *rules* (401) rather
// than by the ETag (412) — the two are different failures, and only the first
// is what these cases are about.
func take(h *harness, c *rtdb.Client, path string, rec map[string]any) error {
	h.t.Helper()
	var cur map[string]any
	etag, _, err := c.GetETag(context.Background(), path, &cur)
	if err != nil {
		h.t.Fatalf("reading the lease's ETag: %v", err)
	}
	return c.PutIfMatch(context.Background(), path, etag, rec)
}

// mustTake is take that fails the test when the rules refuse the write.
func mustTake(h *harness, c *rtdb.Client, path string, rec map[string]any) {
	h.t.Helper()
	if err := take(h, c, path, rec); err != nil {
		h.t.Fatalf("writing the lease at generation %v: %v", rec["generation"], err)
	}
}

// TestLeaseTakeRenewReleaseAndTakeAgain walks one seat through its whole life
// with the rules deployed: created at generation 1, renewed in place by the
// holder, released to an empty holder at generation 2, and taken again at
// generation 3 without waiting out a grace. Every step is a write the rules
// must accept, so a document that refused one of them would leave the protocol
// unable to run at all.
//
// Hand mutation this fails on: making the creation clause require data.exists()
// (no seat can ever be taken), dropping the release branch (a holder cannot give
// the seat up), or requiring a grace before a takeover of a free seat (the next
// taker is refused for ten minutes after a release).
func TestLeaseTakeRenewReleaseAndTakeAgain(t *testing.T) {
	h := newHarness(t)
	alice, bob := h.phone(aliceUID), h.phone(bobUID)

	mustTake(h, alice, leasePath, leaseRecord(aliceUID, 1))
	h.mustRead(alice, leasePath)

	// The heartbeat: the same holder, the same generation, a new stamp.
	mustTake(h, alice, leasePath, leaseRecord(aliceUID, 1))

	// The release: the generation moves on and the holder is emptied.
	mustTake(h, alice, leasePath, leaseFree(2))

	// The seat is free, so the next orchestrator takes it without waiting.
	mustTake(h, bob, leasePath, leaseRecord(bobUID, 3))
	if got := h.value(leasePath); !strings.Contains(got, `"holder_uid":"`+bobUID+`"`) || !strings.Contains(got, `"generation":3`) {
		t.Fatalf("after the second take the lease is %s, want Bob at generation 3", got)
	}
}

// TestLeaseTakeoverOfAFreshSeatIsRefused is the invariant on the real engine:
// another orchestrator writes generation 2 over a lease that is seconds old and
// is refused. This is the case internal/gcpfake cannot prove — the fake has no
// rules, so only the emulator says the *database* is what refuses it.
//
// Hand mutation this fails on: dropping the `now - renewed_at >= LeaseGraceMs`
// clause of the takeover (any orchestrator may take any seat whenever it likes,
// which is two orchestrators on one job).
func TestLeaseTakeoverOfAFreshSeatIsRefused(t *testing.T) {
	h := newHarness(t)
	h.phone(aliceUID)
	bob := h.phone(bobUID)
	h.set(leasePath, leaseSeed(aliceUID, 1, time.Now().UnixMilli()))

	if err := take(h, bob, leasePath, leaseRecord(bobUID, 2)); !errors.Is(err, rtdb.ErrPermission) {
		t.Fatalf("a takeover of a fresh seat: %v, want the rules to refuse it (401)", err)
	}
	// And the seat is still Alice's, at generation 1.
	if got := h.value(leasePath); !strings.Contains(got, `"holder_uid":"`+aliceUID+`"`) || !strings.Contains(got, `"generation":1`) {
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
	h.phone(aliceUID)
	bob := h.phone(bobUID)
	h.set(leasePath, leaseSeed(aliceUID, 1, stale()))

	mustTake(h, bob, leasePath, leaseRecord(bobUID, 2))
	if got := h.value(leasePath); !strings.Contains(got, `"holder_uid":"`+bobUID+`"`) || !strings.Contains(got, `"generation":2`) {
		t.Fatalf("after the takeover the lease is %s, want Bob at generation 2", got)
	}

	// Thirty seconds *inside* the grace, and the same write is refused: the
	// boundary is the grace, and not "some time has passed".
	h.set(leasePath, leaseSeed(aliceUID, 1, time.Now().UnixMilli()-leaseGraceMs+30_000))
	if err := take(h, bob, leasePath, leaseRecord(bobUID, 2)); !errors.Is(err, rtdb.ErrPermission) {
		t.Fatalf("a takeover 30 s inside the grace: %v, want the rules to refuse it", err)
	}
}

// TestLeaseOnlyTheRecordedHolderMayRenewOrRelease: the fencing token's
// enforcement. A renewal keeps the generation, and the rules allow that only
// when the writer is the identity already on the record — so a stale holder
// whose seat was taken over cannot renew it back, and nobody can free a seat
// they are not sitting in.
//
// Hand mutation this fails on: dropping the `data.child('holder_uid') ==
// auth.uid` conjunct from the renew branch (any account keeps any generation),
// or from the release branch (any account frees anyone's seat, which hands the
// job to whoever asks next).
func TestLeaseOnlyTheRecordedHolderMayRenewOrRelease(t *testing.T) {
	h := newHarness(t)
	alice, bob := h.phone(aliceUID), h.phone(bobUID)
	h.set(leasePath, leaseSeed(aliceUID, 1, time.Now().UnixMilli()))

	// Bob renewing Alice's generation in place: refused.
	if err := take(h, bob, leasePath, leaseRecord(bobUID, 1)); !errors.Is(err, rtdb.ErrPermission) {
		t.Fatalf("a renewal by another account: %v, want the rules to refuse it", err)
	}
	// Bob releasing Alice's seat: refused.
	if err := take(h, bob, leasePath, leaseFree(2)); !errors.Is(err, rtdb.ErrPermission) {
		t.Fatalf("a release by another account: %v, want the rules to refuse it", err)
	}
	// Alice's own renewal and release are accepted, so the two refusals above
	// are the rules reading the holder and not the rules refusing everything.
	mustTake(h, alice, leasePath, leaseRecord(aliceUID, 1))
	mustTake(h, alice, leasePath, leaseFree(2))
}

// TestLeaseGenerationCannotJumpOrStandStill is the fencing token itself: the
// generation moves by one per takeover or release and by nothing else. A jump
// would let a writer land on a number that is not the successor of the one it
// read, which is a generation two orchestrators could both believe they hold; a
// standstill with a changed holder is a takeover with the token left behind,
// which is what makes the old holder's next call look valid.
//
// Hand mutation this fails on: dropping `data.child('generation').val() + 1`
// (any generation may be written over any other), or the renew branch's
// `newData.generation == data.generation`.
func TestLeaseGenerationCannotJumpOrStandStill(t *testing.T) {
	h := newHarness(t)
	bob := h.phone(bobUID)
	h.set(leasePath, leaseSeed(aliceUID, 5, stale()))

	for _, c := range []struct {
		why string
		rec map[string]any
	}{
		{"a takeover that jumps to generation 7", leaseRecord(bobUID, 7)},
		{"a takeover that keeps generation 5 with a new holder", leaseRecord(bobUID, 5)},
		{"a takeover that moves the generation backwards", leaseRecord(bobUID, 4)},
		{"a creation on a node that already exists", leaseRecord(bobUID, 1)},
	} {
		if err := take(h, bob, leasePath, c.rec); !errors.Is(err, rtdb.ErrPermission) {
			t.Errorf("%s: %v, want the rules to refuse it", c.why, err)
		}
	}
	// The successor is accepted, so the four refusals above are the clause and
	// not the rules refusing every write to a stale seat.
	mustTake(h, bob, leasePath, leaseRecord(bobUID, 6))
}

// TestLeaseFieldByFieldWritesAreRefused is the case the security review asked
// for, and it is the reason the transition is the lease node's `.write`. Every
// write here touches *part* of the record and nothing else — the shape a client
// with no compare-and-set sends, and the shape that stole the seat when the
// transition lived in the `generation` field's `.validate`, which a write to
// `holder_uid` never reaches.
//
// The seat is Alice's and fresh throughout. Bob is a paired device: he is
// admitted to the subtree by USER, so every refusal below is the transition and
// not the identity.
//
// Hand mutation this fails on: moving the transition back onto the children's
// `.validate` (the first two writes land: Bob is in the seat at Alice's
// generation, and a dead holder's lease can be kept alive or aged by anyone).
func TestLeaseFieldByFieldWritesAreRefused(t *testing.T) {
	h := newHarness(t)
	alice, bob := h.phone(aliceUID), h.phone(bobUID)
	h.set(leasePath, leaseSeed(aliceUID, 4, time.Now().UnixMilli()))

	// The seat itself, one field: Bob writing his own uid over Alice's. This is
	// the takeover the whole case exists for — it leaves the generation where
	// it was, so Alice's token still looks valid.
	h.mustDeny(bob, "another account writing holder_uid alone", map[string]any{leasePath + "/holder_uid": bobUID})
	// The same write as one raw PUT to the child, which is what a client that
	// does not go through internal/lease sends.
	h.rawDenied(claims{mu: bobUID, fp: "-"}, "PUT", leasePath+"/holder_uid", `"`+bobUID+`"`)
	// The stamp, one field: aging a fresh seat into a stale one (the value
	// below is the epoch, so a takeover of it would be allowed at once) or
	// keeping a dead holder's seat fresh forever.
	h.mustDeny(bob, "another account writing renewed_at alone", map[string]any{leasePath + "/renewed_at": 1})
	h.mustDeny(bob, "another account refreshing renewed_at alone", map[string]any{leasePath + "/renewed_at": rtdb.ServerTimestamp{}})
	// The attribution, three fields: the holder's name, the mode and the PR on
	// someone else's record.
	h.mustDeny(bob, "another account rewriting the name, the mode and the PR", map[string]any{
		leasePath + "/holder": "bob", leasePath + "/mode": "enforce", leasePath + "/pr": 7,
	})
	// A partial takeover: the seat and the generation, but no stamp of the
	// database's — the write the rules must not accept even though it moves the
	// token, because the seat it leaves is one nobody renewed.
	h.mustDeny(bob, "a takeover with a stamp of the writer's", map[string]any{
		leasePath + "/holder_uid": bobUID, leasePath + "/generation": 5, leasePath + "/renewed_at": time.Now().UnixMilli(),
	})
	// And the seat is still Alice's, at generation 4: none of the refusals
	// above changed anything.
	if got := h.value(leasePath); !strings.Contains(got, `"holder_uid":"`+aliceUID+`"`) || !strings.Contains(got, `"generation":4`) {
		t.Fatalf("after the refused field writes the lease is %s, want Alice at generation 4", got)
	}
	// The holder itself cannot leave its seat one field at a time either. A
	// release is the whole record with the generation moved on; dropping the
	// uid and keeping the generation would leave the token the holder's own
	// stale process still carries looking valid, which is the failure the
	// fencing token exists to prevent.
	h.mustDeny(alice, "the holder emptying its own holder_uid without moving the generation", map[string]any{leasePath + "/holder_uid": ""})
	h.mustDeny(alice, "the holder emptying its own name", map[string]any{leasePath + "/holder": ""})

	// The controls. Bob's field-by-field takeover of a *stale* seat is exactly
	// the write the rules must accept — the same paths, the same generation
	// step, a stamp from the database — so the refusals above are the
	// transition and not "a partial write is always refused".
	h.set(leasePath, leaseSeed(aliceUID, 4, stale()))
	h.mustAllow(bob, "a takeover of a stale seat as a multi-path update", map[string]any{
		leasePath + "/holder": holderName(bobUID), leasePath + "/holder_uid": bobUID,
		leasePath + "/generation": 5, leasePath + "/renewed_at": rtdb.ServerTimestamp{},
	})
	if got := h.value(leasePath); !strings.Contains(got, `"holder_uid":"`+bobUID+`"`) || !strings.Contains(got, `"generation":5`) {
		t.Fatalf("after the accepted takeover the lease is %s, want Bob at generation 5", got)
	}
	// And the holder's own heartbeat is one of these writes: the same uid, the
	// same generation, the database's stamp. Refusing it would leave the seat
	// unrenewable, which is a job nobody can hold.
	h.mustAllow(bob, "the holder's own heartbeat as a multi-path update", map[string]any{
		leasePath + "/renewed_at": rtdb.ServerTimestamp{},
	})
}

// TestLeaseStampIsTheDatabasesOwn: renewed_at is the database's clock and never
// a writer's. The rules require `newData.renewed_at == now` — the rules' own
// clock, read in the same write — and the only value that can satisfy that is
// the database's server value, which the database resolves itself.
//
// Hand mutation this fails on: relaxing the stamp's `.validate` to
// `newData.val() <= now` (a writer with a slow clock holds a seat the database
// thinks is expired, and one with a fast clock keeps a seat nobody can take
// over), or to nothing at all (a stamp in the year 3000).
func TestLeaseStampIsTheDatabasesOwn(t *testing.T) {
	h := newHarness(t)
	alice := h.phone(aliceUID)

	// The writer's own clock, to the millisecond, and refused: this is the
	// value the client used to send, and it is a time the database has no
	// reason to believe.
	own := time.Now().UnixMilli()
	for _, c := range []struct {
		why  string
		when any
	}{
		{"the writer's own clock", own},
		{"a stamp a second old", own - 1_000},
		{"a stamp a second ahead", own + 1_000},
		{"the epoch", 1},
		{"zero", 0},
	} {
		rec := leaseRecord(aliceUID, 1)
		rec["renewed_at"] = c.when
		if err := take(h, alice, leasePath, rec); !errors.Is(err, rtdb.ErrPermission) {
			t.Errorf("a create carrying %s (%v): %v, want the rules to refuse it", c.why, c.when, err)
		}
	}
	// The database's own value is accepted, and what the database stores is the
	// instant it answered in — not anything the writer sent.
	mustTake(h, alice, leasePath, leaseRecord(aliceUID, 1))
	var stored struct {
		RenewedAt int64 `json:"renewed_at"`
	}
	if _, err := h.admin.Get(context.Background(), leasePath, &stored); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(time.UnixMilli(stored.RenewedAt)); d < 0 || d > 30*time.Second {
		t.Errorf("the stored stamp is %s away from now: it is not the database's own reading", d.Round(time.Millisecond))
	}
	// And the holder's own heartbeat must send it too: a renewal with the
	// writer's clock is refused even though the generation and the holder are
	// right, so the stamp's rule cannot be satisfied by being the holder.
	rec := leaseRecord(aliceUID, 1)
	rec["renewed_at"] = time.Now().UnixMilli()
	if err := take(h, alice, leasePath, rec); !errors.Is(err, rtdb.ErrPermission) {
		t.Errorf("a renewal stamped by the writer: %v, want the rules to refuse it", err)
	}
	mustTake(h, alice, leasePath, leaseRecord(aliceUID, 1))
}

// TestLeaseRecordShapeIsTheRulesOwn: the record is exactly the six fields
// internal/lease writes. A seventh is refused ($other), a missing one is
// refused (hasChildren), a holder_uid that is not the writer's own identity is
// refused — which is what stops an orchestrator claiming someone else's name on
// the record — and a held seat with an empty name is refused.
//
// Hand mutation this fails on: dropping `$other: false` (a hand-written node
// can carry anything, the expiry internal/lease refuses to store included), or
// the holder's name clause (a seat whose holder is unnamed).
func TestLeaseRecordShapeIsTheRulesOwn(t *testing.T) {
	h := newHarness(t)
	alice := h.phone(aliceUID)

	with := func(k string, v any) map[string]any {
		m := leaseRecord(aliceUID, 1)
		m[k] = v
		return m
	}
	for _, c := range []struct {
		why string
		rec map[string]any
	}{
		{"a field the record does not have", with("expires_at", time.Now().UnixMilli()+leaseGraceMs)},
		{"a holder_uid that is not the writer's", leaseRecord(bobUID, 1)},
		{"a holder_uid that is empty on a held seat", map[string]any{"holder": "alice", "holder_uid": "", "generation": 1, "renewed_at": rtdb.ServerTimestamp{}}},
		{"a held seat with no name", map[string]any{"holder": "", "holder_uid": aliceUID, "generation": 1, "renewed_at": rtdb.ServerTimestamp{}}},
		{"a generation that is not a whole number", with("generation", 1.5)},
		{"a generation below one", with("generation", 0)},
		{"a generation past the token's bound", with("generation", 1<<53)},
		{"a pr below one", with("pr", 0)},
	} {
		if err := take(h, alice, leasePath, c.rec); !errors.Is(err, rtdb.ErrPermission) {
			t.Errorf("%s: %v, want the rules to refuse it", c.why, err)
		}
	}
	// A record with a required field missing.
	missing := leaseRecord(aliceUID, 1)
	delete(missing, "renewed_at")
	if err := take(h, alice, leasePath, missing); !errors.Is(err, rtdb.ErrPermission) {
		t.Errorf("a record with no renewed_at: %v, want the rules to refuse it", err)
	}
	// The optional two are accepted, so the refusals above are the shape clause
	// and not the rules refusing every write this identity makes.
	ok := leaseRecord(aliceUID, 1)
	ok["mode"], ok["pr"] = "enforce", 42
	mustTake(h, alice, leasePath, ok)
}

// TestLeaseIsNeverDeleted: the generation must not restart, and the whole point
// of Release is that it writes an empty holder instead of deleting the node. A
// delete would take the fencing token with it, and the next taker would begin
// at generation 1 — a number the released holder's token still is.
//
// Hand mutation this fails on: adding `newData.exists()` to the lease's .write
// as the only guard (a client deletes the node and the token restarts), which is
// also the mutation TestWritesOnlyAtNumericLeaves exists for on the ledgers.
func TestLeaseIsNeverDeleted(t *testing.T) {
	h := newHarness(t)
	alice := h.phone(aliceUID)
	h.set(leasePath, leaseSeed(aliceUID, 1, time.Now().UnixMilli()))

	if err := take(h, alice, leasePath, nil); !errors.Is(err, rtdb.ErrPermission) {
		t.Fatalf("deleting the lease: %v, want the rules to refuse it", err)
	}
	// A release that deletes instead of emptying: the same write, refused.
	h.rawDenied(claims{mu: aliceUID, fp: "-"}, "DELETE", leasePath, "")
	if got := h.value(leasePath); !strings.Contains(got, `"generation":1`) {
		t.Fatalf("after the refused delete the lease is %s, want generation 1 still there", got)
	}
}

// TestLeaseAValidWriteIsRefusedOnlyForTheMissingDevice is the case the security
// review asked for, and it is deliberately narrow. Widening the lease's grant
// from USER to any signed-in identity survives a suite whose write bodies are
// refused for another reason — an unpaired token sending a malformed record
// still gets a 401, so the case says nothing about the device conjunct. The
// node has to be *absent* as well, or the transition refuses the write for a
// reason that has nothing to do with the identity; both mistakes were made here
// and both were found by planting the mutation.
//
// So: the body is one the transition accepts in every respect (a create at
// generation 1 on an absent node, the writer's own uid on the record, the
// database's stamp), the node is absent for both writes, and the only
// difference between them is the device node. The paired device's write lands
// and the unpaired one's is refused.
//
// Hand mutation this fails on: dropping `root.child('config/admin/devices')
// .child(auth.uid).exists()` from userClaim (any account that signed in takes
// any seat), which is the D6 allowlist the whole subtree rests on.
func TestLeaseAValidWriteIsRefusedOnlyForTheMissingDevice(t *testing.T) {
	h := newHarness(t)

	// The paired device, first: the body is one the rules accept. Without this
	// the case below proves nothing about the body.
	alice := h.phone(aliceUID)
	if err := take(h, alice, leasePath, leaseRecord(aliceUID, 1)); err != nil {
		t.Fatalf("a paired device's create: %v, want it accepted", err)
	}
	// The node goes back to absent through the admin path, so the write below
	// is a create and not a change to a seat that is somebody's.
	h.adminDo(http.MethodDelete, leasePath, nil)

	// The same account, paired, and the same write: refused. Every conjunct of
	// the create is satisfied but the device node.
	h.rawDenied(claims{mu: carolUID, fp: "-"}, "PUT", leasePath, createBody(carolUID))

	// Pairing that account is the only change, and now the write lands — so the
	// refusal above was the device node and nothing else.
	h.pairDevice(carolUID)
	carol := h.as(claims{mu: carolUID, fp: "-"})
	if err := take(h, carol, leasePath, leaseRecord(carolUID, 1)); err != nil {
		t.Fatalf("the same account after pairing, on the same absent node: %v, want the create accepted", err)
	}
}

// TestLeaseIsTheUserClaimsAndNobodyElses: the grant is USER and nothing else.
// A run token reads and writes nothing there — a job's own token must not be
// able to take the seat of the orchestrator shepherding it.
//
// The node is absent when the three identities try, and the body each of them
// sends is the create its *own* identity would need (its own uid on the record,
// generation 1, the database's stamp), so the transition accepts every one of
// them and the only thing left to refuse them is the grant. A paired device's
// create lands first and is removed through the admin path, which is what makes
// the node absent and the body known-good.
//
// The write attempts go through rawDenied rather than take: a client that
// cannot read the node cannot read its ETag either, so the honest question for
// these identities is whether the rules accept a write *at all*, and the answer
// is measured on a PUT that carries no If-Match and is refused on its own.
//
// Hand mutation this fails on: widening the lease's .read or .write from USER
// to `auth != null` (any signed-in identity takes the seat).
func TestLeaseIsTheUserClaimsAndNobodyElses(t *testing.T) {
	h := newHarness(t)
	alice := h.phone(aliceUID)
	if err := take(h, alice, leasePath, leaseRecord(aliceUID, 1)); err != nil {
		t.Fatalf("a paired device's create: %v, want it accepted", err)
	}
	h.mustRead(alice, leasePath)
	h.adminDo(http.MethodDelete, leasePath, nil)

	runner := h.run("aurora", "r1")
	h.mustReadDenied(runner, leasePath)
	h.rawDenied(claims{slug: "aurora", run: "r1"}, "PUT", leasePath, createBody("r~aurora~r1"))

	// A pairing session's token (D165): it reaches the two pairing nodes and
	// nothing else, and the lease is not one of them.
	h.rawDenied(claims{pu: "p~pairing", fp: "-"}, "PUT", leasePath, createBody("p~pairing"))

	// And a consumer's (D113b): the mc claim serves a mailbox, not a seat.
	h.rawDenied(claims{mc: "mb-alice", fp: "-"}, "PUT", leasePath, createBody("mb-alice"))
}

// TestLeaseCreationIsTheProjectsAndNotTheJobs pins a gap rather than a
// property, and it is written to fail the day the gap closes.
//
// A lease is scoped to the project and not to the job: the rules grant
// `orchestrators/$slug/$job/lease` to every paired device of the installation,
// so a paired device may create a seat for a slug and a job its token never
// named — one it was never given, and (once P2's access record exists) one it
// may not be allowed to shepherd. The access record that would scope the create
// to the device's own repositories is P2's, and it is not deployed; D12's
// project-level commands and D19's one service per installation are what the
// rules can check today, and a project is what they check.
//
// What it costs while it is open: a paired device can take a seat for a job it
// was not meant to run, and a seat taken wrongly is recovered by the grace (ten
// minutes) — it cannot lock anyone out of a job for longer, and it cannot read
// or write anything else of that job. What it is not: a way in for anyone who
// is not a paired device of this project, which the case above pins.
//
// Hand mutation this fails on: scoping the create to the writer's own
// repositories, which is the change P2 makes and the change this case exists to
// announce.
func TestLeaseCreationIsTheProjectsAndNotTheJobs(t *testing.T) {
	h := newHarness(t)
	alice := h.phone(aliceUID)

	// A slug and a job the token does not name: the harness's tokens carry
	// fs=aurora, fr=r1, and this is neither.
	other := "orchestrators/some-other-repo/some-job/lease"
	if err := take(h, alice, other, leaseRecord(aliceUID, 1)); err != nil {
		t.Fatalf("a paired device creating a lease for a slug it was never told about: %v, want the current (unscoped) behaviour — if this now fails, the gap is closed and this case must be rewritten to assert the new scope", err)
	}
	// The seat is that job's, and it is the device's: the lease is a real one,
	// not a write that landed somewhere else.
	if got := h.value(other); !strings.Contains(got, `"holder_uid":"`+aliceUID+`"`) {
		t.Fatalf("the lease under the other slug is %s, want Alice's", got)
	}
	// A slug that is not a slug is still refused, so the case above is about
	// scope and not about there being no key check at all.
	long := strings.Repeat("k", 101)
	h.mustDeny(alice, "a slug past the key bound", map[string]any{
		"orchestrators/" + long + "/some-job/lease": leaseRecord(aliceUID, 1),
	})
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
	alice, bob := h.phone(aliceUID), h.phone(bobUID)

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
	}{{alice, etagA, aliceUID}, {bob, etagB, bobUID}}

	var wg sync.WaitGroup
	errs := make([]error, len(racers))
	for i, r := range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = r.cl.PutIfMatch(context.Background(), leasePath, r.etag, leaseRecord(r.uid, 1))
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
	a := strings.Contains(got, `"holder_uid":"`+aliceUID+`"`)
	b := strings.Contains(got, `"holder_uid":"`+bobUID+`"`)
	if a == b {
		t.Fatalf("the seat after the race is %s, want exactly one of the two holders in it", got)
	}
}

package creds

import (
	"testing"
	"time"
)

// A hold stands while its expiry is ahead, and stops the instant its expiry
// arrives: the boundary is the whole of the difference between Live and
// "Until is not before now". (Review finding M5: Live's comparison could be
// flipped to !Before, and a lease expiring exactly now would hold a slot for an
// instant no writer asked for, with no test to say so.)
func TestALeaseStopsWhenItsExpiryArrives(t *testing.T) {
	if (Lease{Until: now}).Live(now) {
		t.Fatal("a lease whose expiry has arrived still holds: the hold ends at its expiry, not after it")
	}
	if !(Lease{Until: now.Add(time.Nanosecond)}).Live(now) {
		t.Fatal("a lease expiring one nanosecond from now holds nothing")
	}
	// The same boundary through the cap: the credential is free at the instant
	// the last hold expires.
	leases := []Lease{{Slug: run.Slug, Run: "other", Credential: a1.Name, Until: now}}
	if !Free(leases, a1.Name, now, run, 1) {
		t.Fatal("the cap reads a lease that expired exactly now as still holding")
	}
	if Free(leases, a1.Name, now.Add(-time.Nanosecond), run, 1) {
		t.Fatal("the cap read a lease still in force as free")
	}
}

// A cap of 0 is D142's one opt-out and means no cap at all; a negative one is a
// value the policy refuses and cannot produce, and it must fail closed — "fewer
// than zero runs may hold this" — rather than be read as another way of writing
// "no cap", which is the one mistake that would silently uncap a project.
// (Review finding M5: Free's negative branch had no test.)
func TestZeroIsNoCapAndNegativeIsNoRoom(t *testing.T) {
	leases := []Lease{
		{Slug: run.Slug, Run: "other-1", Credential: a1.Name, Until: now.Add(time.Minute)},
		{Slug: run.Slug, Run: "other-2", Credential: a1.Name, Until: now.Add(time.Minute)},
	}
	if !Free(leases, a1.Name, now, run, 0) {
		t.Fatal("a cap of 0 refused a run: 0 is the written no-cap, not an empty room")
	}
	if Free(leases, a1.Name, now, run, -1) {
		t.Fatal("a negative cap admitted a run: it must read as no room at all, not as no cap")
	}
	// And the boundary of a real cap: exactly `limit` holders leave no room, one
	// fewer does.
	if Free(leases, a1.Name, now, run, 2) {
		t.Fatal("a credential holding its cap's worth of runs admitted one more")
	}
	if !Free(leases[:1], a1.Name, now, run, 2) {
		t.Fatal("a credential with room under its cap refused a run")
	}
	// Holds counts the credential's own live holds only, so another credential's
	// leases never fill this one's cap.
	other := append([]Lease{}, leases...)
	other[0].Credential = a2.Name
	if got := Holds(other, a1.Name, now, run); got != 1 {
		t.Fatalf("Holds counted %d holds on %q; want the one that names it", got, a1.Name)
	}
}

// A lease carries no owner of its own: the run it belongs to is its PATH
// (Lease's json:"-" fields), and a reader fills it in. A lease whose owner could
// not be read is therefore nobody's — it counts against every run — and the
// exemption for "a run's own hold" must not swallow it: the empty Run is what an
// absent owner looks like, and reading it as "this is that run's lease" would
// free a slot nobody released. (Review finding M5: belongsTo's Run{} guard could
// be dropped with no test to catch it.)
func TestALeaseWithNoOwnerIsNobodys(t *testing.T) {
	ownerless := []Lease{{Credential: a1.Name, Until: now.Add(time.Minute)}}

	if got := Holds(ownerless, a1.Name, now, Run{}); got != 1 {
		t.Fatalf("Holds with no run named counted %d; want the ownerless hold counted", got)
	}
	if got := Holds(ownerless, a1.Name, now, run); got != 1 {
		t.Fatalf("the ownerless lease was exempted as %+v's own: it belongs to nobody", run)
	}
	if Free(ownerless, a1.Name, now, run, 1) {
		t.Fatal("an ownerless hold left room under a cap of one")
	}
	if _, ok := SlotFrees(ownerless, a1.Name, now, Run{}, 1); !ok {
		t.Fatal("SlotFrees found no hold for an ownerless lease")
	}
	// A run's own lease is exempt, so the guard is not "nothing is ever its own".
	mine := []Lease{{Slug: run.Slug, Run: run.ID, Credential: a1.Name, Until: now.Add(time.Minute)}}
	if got := Holds(mine, a1.Name, now, run); got != 0 {
		t.Fatalf("Holds counted the asking run's own lease %d times; want it exempt", got)
	}
}

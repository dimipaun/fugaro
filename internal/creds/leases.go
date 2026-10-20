package creds

import (
	"sort"
	"time"
)

// Lease is one run's hold on one credential, written at creds/leases/<slug>/<run>:
// the credential the run is on and when the hold lapses.
//
// A lease is what makes D142's per-token concurrency cap safe on a stateless
// service. The cap says "at most N runs hold one shared credential at a time";
// counting runs needs a registry, and a registry needs a cleanup for an
// instance that dies mid-call — which is exactly the case a Cloud Run service
// has, where an instance is replaced under a request at any moment. So the hold
// is written with an expiry instead: an
// instance that dies, or is replaced by a deploy, releases its slot when its
// lease lapses, with nothing to sweep. A live run refreshes its lease on every
// selection, which is every call it makes, so a run that is working never
// lapses; a run that stops making calls releases its slot within the lease's
// TTL, which is the same thing a stopped run should do.
//
// There is one lease per run, keyed by the run, not one per (credential, run):
// a run that switches credentials replaces its own lease rather than leaving
// one behind, and no release call is needed for the switch or for the run's
// end. The credential is a field of the lease, so the runs holding a given
// credential are one scan of the subtree.
//
// # The cap is best effort, and here is the bound
//
// The cap is read-then-write with no serialization across runs: a selection
// reads the lease subtree, counts the live holds, and writes its OWN run's
// lease — the compare-and-set is on that node, and RTDB has no transaction
// spanning the credential's other holders. So two instances selecting two
// different runs against one credential at the same instant can both read the
// same count, both pass Free, and both take a slot. The cap can therefore be
// exceeded by up to the number of instances selecting at once, and it is
// exceeded for at most one lease TTL: the over-held slots lapse with the leases
// that made them, because a live run refreshes only its own.
//
// The lease TTL is the second bound and the one a long call tests (review
// finding m9): a run whose model call is still streaming when its lease lapses
// holds no slot on paper, so one more run may be admitted beside it until that
// call ends or its next selection refreshes the hold. A service whose calls can
// outlast ten minutes should set Settings.LeaseTTL from its own request timeout
// (C1.11), which is what the member is for; the alternative — refreshing from
// the forwarding path mid-call — is C1.11's to add, and this paragraph is the
// record that the number, not the mechanism, is what bounds it today.
//
// That is a deliberate trade and not an oversight. Counting exactly would take
// a per-credential counter node updated conditionally — a second contended
// write on every selection, in a path whose whole point is that a slot is never
// stuck by an instance that died. What the cap is for is D142's owner headroom:
// keeping N runs from hammering one donor's subscription. Best effort gets
// that; exactness would buy a bound of a handful of concurrent selections at
// the cost of a hot node. C1.11 is where the policy's number meets this
// behaviour; if an exact cap is ever required, it is a counter node with the
// same ETag discipline as creds/roundrobin, and this paragraph is what says so.
//
// # A lapsed lease holds nothing, and the reader sweeps it
//
// Lapsed leases are ignored by Live and by Holds, so a lease that has run out
// holds no slot whatever. It is still a node, though, and one node per run that
// ever made a call accumulates for the life of a project — which matters because
// every selection reads the whole subtree (Leases), and because the database
// caps one response at 16 MiB (rtdb/client.go), past which the read fails to
// decode and every selection in the project fails closed. So the read itself
// sweeps: RTDBStore.Leases deletes the oldest lapsed nodes it finds, once there
// are more than a small residue of them, which bounds what the next read must
// decode at (residue + the runs live within one lease TTL) instead of the
// project's whole history.
//
// The residue a sweep leaves is not the same as the slots a lease holds, and the
// difference is worth stating: nothing releases a slot early, so a run that has
// finished keeps its hold until its lease lapses (DefaultLeaseTTL) unless the
// service deletes the node when the run ends. The sweep is what keeps that
// leftover from being a memory of every run the project ever had; deleting a
// placement at a run's end is a task still owed to C1.10, which is the one place
// that knows a run has finished, and until then a dead run costs a slot for ten
// minutes and a node until it is swept.
type Lease struct {
	// Slug and Run are the run the lease belongs to. They are carried by the
	// node's path, not by its value (json:"-"), and a reader fills them in.
	Slug string `json:"-"`
	Run  string `json:"-"`
	// Credential is the credential's NAME, never a value (F27).
	Credential string `json:"credential"`
	// Until is when the hold lapses.
	Until time.Time `json:"until"`
}

// Live reports whether the hold still stands at now. It is a strict After on
// purpose: a lease whose expiry has arrived holds nothing, so a hold that ends
// exactly at now is over rather than current. (`!Before` instead would make
// every lease live for one instant longer than it was written for, which is the
// moment two runs and a cap of one argue about.)
func (l Lease) Live(now time.Time) bool { return l.Until.After(now) }

// Holds counts the runs other than except whose lease on credential is still
// live at now. except is the run asking: its own hold must never be the reason
// it cannot use the credential it is already on, which is what would happen on
// the sticky path of a run that holds the last slot.
func Holds(leases []Lease, credential string, now time.Time, except Run) int {
	n := 0
	for _, l := range leases {
		if l.Credential == credential && l.Live(now) && !l.belongsTo(except) {
			n++
		}
	}
	return n
}

// Free reports whether run may take credential under the cap: limit live holds
// by other runs. A limit of 0 is no cap at all (D142's documented opt-out, the
// one value in the two controls that widens rather than bounds), and a negative
// one — which the policy refuses and cannot produce — is read as no room at
// all, so a hand-built setting fails closed instead of silently uncapping.
func Free(leases []Lease, credential string, now time.Time, run Run, limit int) bool {
	if limit == 0 {
		return true
	}
	return Holds(leases, credential, now, run) < limit
}

// SlotFrees is the time the cap on credential lets run in, and whether there is
// one: the lapse that takes the holds on it below limit.
//
// It is not the earliest lapse among the other runs (which is what this returned
// before review finding m10, and what a limit of 1 hides). Free asks for
// strictly fewer than limit holders, so with a limit of 3 and five holders it
// takes three lapses to make room for a sixth — two lapses leave three holders,
// and three is not fewer than three, so the third is the one that frees the slot
// — and a caller told to wait for the first of the five would wake up to a
// credential that is still full and wait again, once per lapse, on a path whose
// whole purpose is to answer when the run should come back. The lapse that
// matters is therefore the (holders - limit + 1)-th earliest: the one after
// which fewer than limit holds are left. With a limit of 1 it is the last of
// them, which is the same answer as "all of them must go" and the same answer
// this function gave for that case before.
//
// A limit of 0 is no cap at all (Free), so there is nothing to wait for; a
// negative one is the opposite — Free reads it as no room at all, and no lapse
// frees room under a cap that refuses everything. Both are answered "no time",
// and the caller must not read that as "usable now": Free has already said the
// credential cannot be used, which is why earliest skips it rather than
// answering the zero time.
func SlotFrees(leases []Lease, credential string, now time.Time, except Run, limit int) (time.Time, bool) {
	if limit <= 0 {
		return time.Time{}, false
	}
	var expiries []time.Time
	for _, l := range leases {
		if l.Credential == credential && l.Live(now) && !l.belongsTo(except) {
			expiries = append(expiries, l.Until)
		}
	}
	need := len(expiries) - limit + 1
	if need <= 0 || need > len(expiries) {
		// Nothing is in the way (need <= 0), or the room the cap wants is more
		// than the runs holding it (which cannot free a slot): no time to answer
		// with, and the caller falls back to what the mark says.
		return time.Time{}, false
	}
	sort.Slice(expiries, func(i, j int) bool { return expiries[i].Before(expiries[j]) })
	return expiries[need-1], true
}

// belongsTo reports whether the lease is except's own. An empty Slug/Run is
// nobody's: a lease whose owner the path could not be read from is counting
// toward nobody's exemption, which holds a slot rather than freeing one.
func (l Lease) belongsTo(except Run) bool {
	return except != Run{} && l.Slug == except.Slug && l.Run == except.ID
}

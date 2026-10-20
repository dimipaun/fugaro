package creds

import "time"

// Lease is one run's hold on one credential, written at creds/leases/<slug>/<run>:
// the credential the run is on and when the hold lapses.
//
// A lease is what makes D142's per-token concurrency cap safe on a stateless
// service. The cap says "at most N runs hold one shared credential at a time";
// counting runs needs a registry, and a registry needs a cleanup for an
// instance that dies mid-call — which is exactly the case a Cloud Run service
// has (review finding M2). So the hold is written with an expiry instead: an
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

// Live reports whether the hold still stands at now.
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

// EarliestExpiry is the earliest time a hold blocking run's use of credential
// lapses, and whether there is one. It is the "ready" time of a credential that
// is free but for other runs' leases, which is what a caller waits for when the
// cap is the only thing in the way.
func EarliestExpiry(leases []Lease, credential string, now time.Time, except Run) (time.Time, bool) {
	var best time.Time
	found := false
	for _, l := range leases {
		if l.Credential != credential || !l.Live(now) || l.belongsTo(except) {
			continue
		}
		if !found || l.Until.Before(best) {
			best, found = l.Until, true
		}
	}
	return best, found
}

// belongsTo reports whether the lease is except's own. An empty Slug/Run is
// nobody's: a lease whose owner the path could not be read from is counting
// toward nobody's exemption, which holds a slot rather than freeing one.
func (l Lease) belongsTo(except Run) bool {
	return except != Run{} && l.Slug == except.Slug && l.Run == except.ID
}

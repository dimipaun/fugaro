// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The intake's throttle (§7.2): per-address and per-IP, so the forms cannot be
// used to mail-bomb a third party's address or to walk a list of domains asking
// whether each holds a key.
//
// The numbers are the deploy's and not this file's — a rate is a capacity
// decision and the owner's caps are D454's sibling in the abuse section — so
// the service refuses to start without them rather than running unlimited by
// accident.

import (
	"fmt"
	"sync"
	"time"
)

// Limits is how many requests one key may make in one window. Keys is how many
// buckets one request is counted in: the address it is about and the address it
// came from.
type Limits struct {
	Requests int
	Window   time.Duration
}

// throttle counts the requests each key was allowed, inside the current window.
//
// What it holds is bounded by the requests the deploy's own rate allows rather
// than by what a stranger posts: a request that is refused is counted nowhere,
// so knocking with a fresh email address — the one thing an unauthenticated
// caller controls — adds no entry, and an entry the window has emptied is
// dropped. What is left is one bucket per key that was allowed inside the
// window, which is what a per-key rate is; a bucket nobody asks about again is
// not swept, and the deploy's shared limiter is the store-backed one (S3.7's).
type throttle struct {
	limits Limits
	now    func() time.Time
	mu     sync.Mutex
	seen   map[string][]time.Time
}

func newThrottle(limits Limits, now func() time.Time) (*throttle, error) {
	if limits.Requests < 1 || limits.Window <= 0 {
		return nil, fmt.Errorf("the intake's throttle is %d request(s) per %s: a rate the deploy names, and an intake with none is one that mails on demand", limits.Requests, limits.Window)
	}
	return &throttle{limits: limits, now: now, seen: map[string][]time.Time{}}, nil
}

// allow answers whether one request may proceed. It refuses if any of the keys
// is at its limit, because the two buckets are two ways of asking the same
// question — and a refusal counts the request nowhere.
//
// Counting only what is allowed is what keeps knocking from buying a fresh
// allowance: the bucket that refused is at its limit and stays there while its
// entries are live, so the same key is refused for the rest of the window
// whether or not its refusals were written down. What it costs is one entry per
// *allowed* request, which is the rate the deploy named, rather than one per
// address a stranger invents.
func (t *throttle) allow(keys ...string) bool {
	now := t.now()
	cutoff := now.Add(-t.limits.Window)
	t.mu.Lock()
	defer t.mu.Unlock()

	live := make([][]time.Time, len(keys))
	allowed := true
	for i, key := range keys {
		live[i] = trimmed(t.seen[key], cutoff)
		if len(live[i]) >= t.limits.Requests {
			allowed = false
		}
	}
	for i, key := range keys {
		if !allowed {
			// A refused request writes nothing, and a bucket the window has
			// emptied goes rather than being kept as an empty one.
			if len(live[i]) == 0 {
				delete(t.seen, key)
			} else {
				t.seen[key] = live[i]
			}
			continue
		}
		t.seen[key] = append(live[i], now)
	}
	return allowed
}

// trimmed is what one bucket holds of the current window. It reuses the backing
// array, which is why every caller either stores the result back or drops it.
func trimmed(held []time.Time, cutoff time.Time) []time.Time {
	kept := held[:0]
	for _, at := range held {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	return kept
}

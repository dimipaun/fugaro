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

// throttle counts what each key has asked for, inside the current window. It
// holds every key it has seen — an unbounded map trimmed by the window is the
// honest shape for a single-instance service and a leak on a fleet, which is
// why the deploy's shared limiter is the store-backed one (S3.7's).
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

// allow answers whether one request may proceed, and counts it either way: a
// refused request is still a request the key made, so knocking does not buy a
// fresh allowance. It refuses if any of the keys is at the limit, because the
// two buckets are two ways of asking the same question.
func (t *throttle) allow(keys ...string) bool {
	now := t.now()
	cutoff := now.Add(-t.limits.Window)
	t.mu.Lock()
	defer t.mu.Unlock()
	allowed := true
	for _, key := range keys {
		kept := t.seen[key][:0]
		for _, at := range t.seen[key] {
			if at.After(cutoff) {
				kept = append(kept, at)
			}
		}
		t.seen[key] = append(kept, now)
		if len(t.seen[key]) > t.limits.Requests {
			allowed = false
		}
	}
	return allowed
}

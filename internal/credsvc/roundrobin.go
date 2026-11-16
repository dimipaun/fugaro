package credsvc

// Round robin over a pool (docs/model-crafts/spec.md §2, reconciliation F6, F7,
// F12, F22, D1625, D1632, D1634; plan task MC7): which of a pool's members a
// run goes out on once its owner asked for the rotation, and where the counter
// that decides it lives.
//
// **The strategy is the owner's; the rule is not this file's.** A pool's
// strategy is written in the published node — `fugaro accounts pool add
// --strategy round-robin`, config.Pool.Strategy, with failover the default
// (D1632) — and MC6's offering already hands this service the pool's members.
// What this file adds is the one thing the offering does not carry: which of
// them the run goes to when the owner asked for a rotation rather than for the
// cascade's own rule. The rule stays in internal/creds, where the tier order,
// the marks' holds, the cap, the probe, the wait-or-switch decision and the
// run's stickiness are one implementation; this service passes it the strategy
// as a word (creds.Selector.Strategy, creds.StrategyRoundRobin, the way it
// already passes the settings).
//
// **A call this service cannot attribute to one craft does not rotate.** The
// wire carries no craft (C1.10): a candidate list is the union of the five
// crafts' accesses (pool.go), so a project whose crafts go out on two different
// pools hands one call one list. Rotating that list would apply one pool's
// strategy to another pool's members — a member used out of the order its owner
// wrote, silently — so the rotation is taken up only when EVERY craft that
// contributed a candidate goes out on a pool whose strategy is round-robin. A
// mixture resolves to the cascade's own rule: it is the shape that hides
// nothing about which credential served (D1632) and the tighter of the two for
// a project that narrows a pool (F12, D1628).
//
// **Nothing outside this repository changes with it.** D1625 asks for one
// counter per project, written with compare-and-set so two instances selecting
// at once never lose an update, and that counter already exists: creds/
// roundrobin, read and advanced by creds.RTDBStore.RoundRobin under
// GetETag/PutIfMatch with a retry on rtdb.ErrPrecondition (creds.DefaultAttempts).
// It is where a tie between two equally-idle credentials is broken today, and
// it is per project because the state is the installation's database rather
// than one pool's node. So this task REUSES it instead of writing a second
// counter, and no rules or IAM change goes with that: `creds/roundrobin` is
// under the `/creds` subtree every run token is denied (O1, D136) and is
// written by the service's own account, which the instance's existing rules and
// the custom role `fugaroCredentialsDatabase` (`firebasedatabase.instances.get`
// and `.update`) already cover. deploy/terraform is untouched by MC7.
//
// **What is deliberately elsewhere.** The rotation is per run and not per call:
// a run that has a credential is answered by the sticky path before any
// strategy is consulted, so it takes one turn and keeps it (D154, D874). A
// review caller has one credential and no strategy at all — its list is its
// pairing's (reviewCandidates). A pool whose members are the runner path's (an
// OpenRouter key, the built-in vertex) never reaches this service (D1634), so
// this file rotates what internal/creds may select here and nothing beside it.

import (
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
)

// poolStrategy is the strategy one pool of the published node asks for, as
// internal/creds spells it: the owner's word, with failover for a pool that
// writes none (D1632) and for one that writes something this release does not
// know — a hand-edit of a node is read as the strategy that spreads nothing
// rather than as a rotation the owner never asked for.
func poolStrategy(pool config.Pool) creds.Strategy {
	if pool.StrategyOrDefault() == config.PoolStrategyRoundRobin {
		return creds.StrategyRoundRobin
	}
	return creds.StrategyFailover
}

// offeringStrategy is the strategy a call's candidate list goes out on, from
// what the crafts contributed to it: rotating is how many of them put a
// candidate in through a round-robin pool, and pinned how many did so through
// anything else — a pool of the other strategy, a credential named directly, or
// an access that is neither. Round-robin only when every contributor rotated,
// which is the rule roundrobin.go's header states; the counts are passed in
// rather than recomputed so that the list and the strategy are one walk of the
// crafts and cannot drift apart.
func offeringStrategy(rotating, pinned int) creds.Strategy {
	if rotating > 0 && pinned == 0 {
		return creds.StrategyRoundRobin
	}
	return creds.StrategyFailover
}

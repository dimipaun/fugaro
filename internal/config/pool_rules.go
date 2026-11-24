package config

import (
	"fmt"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/policy"
)

// This file holds the rules a pool's three new members are held to — the rule
// it is served by, the principal it belongs to and the optional threshold it
// moves off a member by (PL1, R5, R21) — and the one place their values are
// read back out of the config. It is separate from craft.go because the two
// members that carried a pool until now (its strategy and its owner) are judged
// there, and the rule set is not.
//
// # Why the vocabulary is declared here and not imported
//
// contract-pools.md puts a pool's rule, principal and threshold in
// internal/routing (C2), and PL1 is written against them. This package cannot
// import that one: internal/routing's resolver reads config.PatternMatches
// (RT1, resolve.go), so internal/routing already imports internal/config and
// the other direction is a cycle. What this file declares is therefore the same
// vocabulary stated a second time — the same wire spellings, held to routing's
// by TestPoolRuleOrdered and TestPoolPrincipalKinds — and the rule names alias
// internal/policy's, which is where the rule that orders them has to live for
// `internal/policy/craft.go` to rank them (the same arrangement
// PoolStrategyFailover already has, and for the same reason).
//
// Nothing here decides anything: the picker is PL8's, the lookup is PL4's and
// whose pool a run is served from is the service's, all of which read the
// shapes in internal/routing. This is the config's own spelling of them, and a
// caller that holds a pool in hand converts rather than reaching across.

// The pool rules, in routing's own order (R5, D3177): `ordered` is the spec's
// fixed order, `round-robin` and `failover` keep the meanings the strategies of
// those names already have. The first two spellings are policy's strategy
// constants because the two values keep their meanings (R21) — one name, one
// meaning — and `ordered` is policy's new rule constant beside them.
const (
	PoolRuleOrdered    = policy.PoolRuleOrdered
	PoolRuleRoundRobin = policy.PoolStrategyRoundRobin
	PoolRuleFailover   = policy.PoolStrategyFailover
)

// PoolRules are the rules a pool may be written with, so that validation
// renders one message from one list. They are NOT PoolStrategies: `ordered` is
// a rule and never a strategy, so a pool that wants it writes `rule`, and a
// `strategy` of anything but the two strategy names reads as failover
// (Pool.RuleOrDefault).
var PoolRules = []string{PoolRuleOrdered, PoolRuleRoundRobin, PoolRuleFailover}

// The principal kinds, mirroring routing.PrincipalKinds (D3181): a person, a
// verified service account, or the project itself. A person's id is their
// Google subject and a service account's is its address, which is what makes a
// principal an identity and never display text (R7).
const (
	PrincipalPerson         = "person"
	PrincipalServiceAccount = "service-account"
	PrincipalProject        = "project"
)

// PrincipalKinds are the kinds a pool's principal may be written with, again so
// that one list renders one message.
var PrincipalKinds = []string{PrincipalPerson, PrincipalServiceAccount, PrincipalProject}

// PoolPrincipal is who a pool belongs to: a kind and an id. It is the enforcing
// half of a pool's ownership and it is a NEW member — Owner on the pool below
// stays an email address and keeps its meaning (R21) — so a pool that names an
// owner and no principal is exactly what every installation holds today.
//
// A person's id is the Google subject the service verified, never the text a
// launch wrote about itself (R7): a forged requester resolves to its own pool
// or to none.
type PoolPrincipal struct {
	// Kind is one of PrincipalKinds.
	Kind string `yaml:"kind" json:"kind"`
	// ID is the principal's own identifier: a person's Google subject, a service
	// account's address, or the project's name. It is a name and never a value.
	ID string `yaml:"id" json:"id"`
}

// Valid reports whether p names a principal: a kind this release knows and a
// non-empty id. An empty id is refused rather than read as "no principal": a
// pool whose principal is missing is a pool nothing can resolve.
func (p PoolPrincipal) Valid() bool {
	return p.ID != "" && slices.Contains(PrincipalKinds, p.Kind)
}

// PoolThreshold is a pool's optional usage threshold (D3178, PL8): the fraction
// of a member's limit at which the pool moves off that member, before the limit
// is hit rather than after it.
type PoolThreshold float64

// Valid is the bound a threshold must meet: greater than 0 and at most 1. That
// is the bound the picker (PL8) reads too, so a value this admits is a value
// the picker was written for — 0 would move off every member always, which
// serves nothing, and a value at 1 fires exactly at the limit.
//
// Nothing admits a threshold yet: no rule set this release ships acts on one,
// so poolRuleProblems refuses any that is written and this decides nothing
// until PL8 lands. It is kept, and kept the picker's own answer, because that
// is the one thing the refusal must be lifted by.
func (t PoolThreshold) Valid() bool { return t > 0 && t <= 1 }

// RuleOrDefault is the rule this pool is served by: its own `rule`, or, where
// it names none, the `strategy` it was written with, and failover where it
// names neither. It is routing.Rule.RuleOrDefault with the pool's two members
// read into it (R21, contract-pools.md), so a caller holding a pool resolves
// its rule here rather than reading the two members itself, and the picker then
// acts on one resolved rule without reading the pool's configuration again
// (PL3, PL4).
//
// Its answer is never "".
func (p Pool) RuleOrDefault() string {
	if p.Rule != "" {
		return p.Rule
	}
	switch p.Strategy {
	case PoolRuleRoundRobin, PoolRuleFailover:
		return p.Strategy
	}
	return PoolRuleFailover
}

// poolRuleProblems reports the rules a pool's rule, principal and threshold are
// held to, named under at. Every one of them is optional: a pool an
// installation already holds carries none of the three, and the migration
// writes them (MG1, MG2) — so an empty rule, an absent principal and a nil
// threshold are all accepted, and only a value that is written and wrong is
// refused.
//
// The three answers are the contract's own: a rule that is not one of PoolRules
// is refused rather than read as one of them, because a rule no side serves is
// a call nobody can place; a principal whose kind this release does not know,
// or whose id is empty, is refused rather than read as "no principal"; and a
// threshold is refused outright, whatever its value, because no rule set this
// release ships acts on one — the member arrives with the rule that reads it
// (PL8), and until then a threshold that validated would be carried into
// `fugaro.yaml` and quietly do nothing. PoolThreshold.Valid is the bound that
// refusal is lifted by.
func poolRuleProblems(at string, p Pool) []Problem {
	var ps []Problem
	if p.Rule != "" && !slices.Contains(PoolRules, p.Rule) {
		ps = append(ps, Problem{Path: at + ".rule", Message: fmt.Sprintf("must be one of %s", strings.Join(PoolRules, ", "))})
	}
	if p.Principal != nil && !p.Principal.Valid() {
		ps = append(ps, Problem{Path: at + ".principal", Message: fmt.Sprintf("a principal names whose pool this is, written as a kind and an id: kind must be one of %s, and id is the person's Google subject, the service account's address or the project's name — a pool with an unknown kind or no id is a pool nothing can resolve (owner is a separate member and does not stand in for it)", strings.Join(PrincipalKinds, ", "))})
	}
	if p.Threshold != nil {
		ps = append(ps, Problem{Path: at + ".threshold", Message: "is not allowed yet: no rule set this release ships acts on a threshold, so a value written here would be carried in the file and never read — remove it until the rule that reads it ships; the value it will admit is greater than 0 and at most 1, the fraction of a member's limit at which a pool moves off that member"})
	}
	return ps
}

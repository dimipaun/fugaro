package config

import (
	"slices"
	"testing"

	"github.com/dimipaun/fugaro/internal/policy"
)

// TestPoolRuleOrdered pins the rule the routing table adds to a pool (R5,
// D3177): `rule` accepts `ordered`, `round-robin` and `failover`; `strategy`
// does not accept `ordered` and keeps its two values and their meanings, so a
// pool that wants the fixed order writes `rule`; and an absent rule resolves to
// the pool's own strategy and then to failover. The red step is the first
// assertion: before PL1, `ordered` is an unknown member whatever it is written
// under.
//
// The spellings are the contract's (contract-pools.md). This package cannot
// import internal/routing — that package's resolver reads this one
// (resolve.go) — so the two lists are held together from the other side, by
// TestConfigPoolVocabularyMatchesTheContract in internal/routing/pools_test.go.
func TestPoolRuleOrdered(t *testing.T) {
	if !slices.Contains(PoolRules, PoolRuleOrdered) {
		t.Fatalf("PoolRules = %v and `ordered` is the rule this plan adds", PoolRules)
	}
	if want := []string{"ordered", "round-robin", "failover"}; !slices.Equal(PoolRules, want) {
		t.Errorf("PoolRules = %v, want %v: these are the three the contract's rule column names", PoolRules, want)
	}
	for _, rule := range []string{PoolRuleOrdered, PoolRuleRoundRobin, PoolRuleFailover} {
		pools := []Pool{{Name: "team", Rule: rule, Members: []string{CredentialVertex}}}
		if ps := ValidatePools(pools, nil); len(ps) != 0 {
			t.Errorf("rule %s: ValidatePools = %v, and every one of PoolRules is a rule a pool may be written with", rule, ps)
		}
	}
	bad := []Pool{{Name: "team", Rule: "cheapest", Members: []string{CredentialVertex}}}
	ps := ValidatePools(bad, nil)
	if len(ps) != 1 || ps[0].Path != "pools.team.rule" {
		t.Fatalf("ValidatePools with rule cheapest = %v, want one problem at pools.team.rule: a value that is not one of the three is refused rather than read as one of them", ps)
	}
	// `ordered` is a rule and never a strategy (contract-pools.md): a value
	// never changes meaning, so a pool that writes it under `strategy` is
	// refused there rather than silently served `ordered`.
	asStrategy := []Pool{{Name: "team", Strategy: PoolRuleOrdered, Members: []string{CredentialVertex}}}
	if ps := ValidatePools(asStrategy, nil); len(ps) != 1 || ps[0].Path != "pools.team.strategy" {
		t.Fatalf("ValidatePools with strategy ordered = %v, want one problem at pools.team.strategy", ps)
	}

	for _, tc := range []struct {
		name string
		pool Pool
		want string
	}{
		{"the rule wins where it is written", Pool{Rule: PoolRuleOrdered, Strategy: PoolRuleRoundRobin}, PoolRuleOrdered},
		{"an absent rule is the pool's strategy", Pool{Strategy: PoolRuleRoundRobin}, PoolRuleRoundRobin},
		{"an absent pair is failover", Pool{}, PoolRuleFailover},
		{"a strategy this release does not know reads as failover", Pool{Strategy: "cheapest"}, PoolRuleFailover},
	} {
		if got := tc.pool.RuleOrDefault(); got != tc.want {
			t.Errorf("%s: RuleOrDefault = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestPoolPrincipalKinds pins a pool's principal (D3181, D3179 as amended,
// R7): it is a kind and an id, the kinds are the contract's three, and the
// spellings are the contract's. An unknown kind and an empty id are both
// refused — a pool nothing can resolve is a written mistake — and an absent
// principal is not, because it is the state every installation that has not
// migrated is in.
func TestPoolPrincipalKinds(t *testing.T) {
	if want := []string{"person", "service-account", "project"}; !slices.Equal(PrincipalKinds, want) {
		t.Fatalf("PrincipalKinds = %v, want %v: these are the three the contract's principal column names", PrincipalKinds, want)
	}
	for _, kind := range PrincipalKinds {
		pools := []Pool{{Name: "team", Members: []string{CredentialVertex},
			Principal: &PoolPrincipal{Kind: kind, ID: "1053"}}}
		if ps := ValidatePools(pools, nil); len(ps) != 0 {
			t.Errorf("kind %s: ValidatePools = %v, and every one of PrincipalKinds is a kind a pool may be written with", kind, ps)
		}
	}
	if ps := ValidatePools([]Pool{{Name: "team", Members: []string{CredentialVertex}}}, nil); len(ps) != 0 {
		t.Errorf("a pool with no principal: ValidatePools = %v, and an installation that has not migrated holds one — the migration writes the principal (MG1, MG2)", ps)
	}
	for _, tc := range []struct {
		name string
		p    PoolPrincipal
	}{
		{"an unknown kind", PoolPrincipal{Kind: "robot", ID: "1053"}},
		{"no id", PoolPrincipal{Kind: PrincipalProject}},
		{"neither", PoolPrincipal{}},
	} {
		pools := []Pool{{Name: "team", Members: []string{CredentialVertex}, Principal: &tc.p}}
		ps := ValidatePools(pools, nil)
		if len(ps) != 1 || ps[0].Path != "pools.team.principal" {
			t.Errorf("%s: ValidatePools = %v, want one problem at pools.team.principal", tc.name, ps)
		}
	}
}

// TestOwnerKeepsItsMeaning pins the one member of a pool this plan touches and
// must not change (R21): `owner` stays an email address, judged by the address
// rule it has always been judged by, and it decides nothing about which pool a
// run is served from — the principal is that half and it is a member of its
// own. A pool may carry both, and a pool that carries an owner and no principal
// is exactly what every installation holds today.
func TestOwnerKeepsItsMeaning(t *testing.T) {
	ok := Pool{Name: "team", Members: []string{CredentialVertex},
		Owner:     "dev@example.com",
		Principal: &PoolPrincipal{Kind: PrincipalPerson, ID: "1053"}}
	if ps := ValidatePools([]Pool{ok}, nil); len(ps) != 0 {
		t.Fatalf("a pool with an owner and a principal: ValidatePools = %v, want none", ps)
	}
	addressOnly := Pool{Name: "team", Owner: "dev@example.com", Members: []string{CredentialVertex}}
	if ps := ValidatePools([]Pool{addressOnly}, nil); len(ps) != 0 {
		t.Errorf("an owner and no principal: ValidatePools = %v, want none: owner keeps the meaning it has", ps)
	}
	notAnAddress := Pool{Name: "team", Owner: "1053", Members: []string{CredentialVertex}}
	ps := ValidatePools([]Pool{notAnAddress}, nil)
	if len(ps) != 1 || ps[0].Path != "pools.team.owner" {
		t.Fatalf("an owner that is a subject and not an address: ValidatePools = %v, want one problem at pools.team.owner: owner is display text and the roster's key, and the principal is where an identity belongs", ps)
	}
	// The two members are told apart: the owner's address is not read as the
	// principal's id, so a pool whose principal is the person its owner names
	// still has to carry the id the service keys on.
	if ok.Owner == ok.Principal.ID {
		t.Error("the fixture's owner and principal id are the same value, which is the confusion this test exists to rule out")
	}
}

// TestALoneSubscriptionIsNotRefusedOnRelease1 is D3176's release-1 bridge: a
// subscription credential that no pool lists is accepted by ValidatePools,
// because `fugaro update` has not wrapped it in a pool of one yet (MG1, MG2)
// and every installation holds one until it has. It is reported rather than
// refused in release 1 (routing.Problems, `fugaro validate`) and refused in
// release 2, by X2, once the migration has wrapped what it can.
func TestALoneSubscriptionIsNotRefusedOnRelease1(t *testing.T) {
	creds := []Credential{{Name: "claude-work", Type: CredentialTypeClaudeOAuth}}
	if ps := ValidatePools(nil, creds); len(ps) != 0 {
		t.Errorf("a subscription in no pool: ValidatePools = %v, want none: release 1 reports it and X2 refuses it", ps)
	}
}

// TestPoolThresholdIsRefusedOutsideItsBound pins the optional threshold
// (D3178, PL8): it is a fraction of a member's limit, so the bound is greater
// than 0 and at most 1, and both halves are the picker's bound as well as this
// validation's (PoolThreshold.Valid). An absent threshold is accepted — every
// installation that has not migrated holds one — and 0 and 1 are the two ends
// the bound decides: 0 is refused because it would move off every member
// always, 1 because it fires exactly at the limit.
func TestPoolThresholdIsRefusedOutsideItsBound(t *testing.T) {
	at := func(v float64) *PoolThreshold { x := PoolThreshold(v); return &x }

	for _, v := range []float64{0.5, 0.8, 1} {
		pools := []Pool{{Name: "team", Members: []string{CredentialVertex}, Threshold: at(v)}}
		if ps := ValidatePools(pools, nil); len(ps) != 0 {
			t.Errorf("threshold %v: ValidatePools = %v, want none", v, ps)
		}
	}
	if ps := ValidatePools([]Pool{{Name: "team", Members: []string{CredentialVertex}}}, nil); len(ps) != 0 {
		t.Errorf("no threshold: ValidatePools = %v, want none: the member is optional", ps)
	}
	for _, v := range []float64{0, -0.1, 1.5} {
		pools := []Pool{{Name: "team", Members: []string{CredentialVertex}, Threshold: at(v)}}
		ps := ValidatePools(pools, nil)
		if len(ps) != 1 || ps[0].Path != "pools.team.threshold" {
			t.Errorf("threshold %v: ValidatePools = %v, want one problem at pools.team.threshold", v, ps)
		}
		if PoolThreshold(v).Valid() {
			t.Errorf("PoolThreshold(%v).Valid = true, want false: the two read one bound", v)
		}
	}
}

// TestPolicyRanksOrdered holds the merge's copy of the rule vocabulary to this
// package's (internal/policy/craft.go). The two are separate declarations
// because internal/policy may not import this package, and the rule that orders
// a pool's values lives there: `ordered` must be a rule the merge knows, or a
// later layer that pins it would be dropped as a name this release does not
// know.
func TestPolicyRanksOrdered(t *testing.T) {
	if policy.PoolRuleOrdered != PoolRuleOrdered {
		t.Errorf("policy.PoolRuleOrdered = %q and config.PoolRuleOrdered = %q", policy.PoolRuleOrdered, PoolRuleOrdered)
	}
}

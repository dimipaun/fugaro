package routing_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/routing"
)

// The pools contract (C2): the rule a pool is served by (R5, D3177), the
// principal it belongs to (D3181), the optional threshold (D3178), the
// designation record (R26), the reference the lookup answers (PL4), the picker's
// signature (PL3) and the `fugaro accounts pool ls --json` document (PL2, PL10).
//
// Each of them is a name a reader OUTSIDE this binary reads — a published
// policy's `rule` and `principal`, the designation and pool nodes a launcher and
// the service both read, the documents the console's pools screen draws (VS2) —
// so they are pinned here rather than left to the sides that happen to agree
// today. A value never changes meaning: a member added is fine, a member removed
// or retyped is a new version of the shape that carries it, and this test is
// what makes that an edit somebody sees.

// TestPoolShapesAreStable pins the contract's names and the JSON of every shape
// it carries.
func TestPoolShapesAreStable(t *testing.T) {
	t.Run("the rule names are the contract's", func(t *testing.T) {
		// The three words a pool is published with, spelled as the policy, the
		// config and the console read them.
		if routing.RuleOrdered != "ordered" || routing.RuleRoundRobin != "round-robin" || routing.RuleFailover != "failover" {
			t.Errorf("the rule names are (%q, %q, %q), and the contract's are (ordered, round-robin, failover)",
				routing.RuleOrdered, routing.RuleRoundRobin, routing.RuleFailover)
		}
		if want := []routing.Rule{routing.RuleOrdered, routing.RuleRoundRobin, routing.RuleFailover}; !slices.Equal(routing.Rules, want) {
			t.Errorf("the rules a pool may be written with are %q, and the contract's are %q", routing.Rules, want)
		}
		for _, r := range routing.Rules {
			if !r.Valid() {
				t.Errorf("%q is one of the contract's rules and Valid refuses it", r)
			}
		}
		// A pool that names no rule is served as it is today, which is what
		// every pool written before a rule existed does (R21): failover, never
		// an unknown rule refused and never a rule of its own.
		if !routing.Rule("").Valid() {
			t.Error("a pool that names no rule is refused, and it is read as failover rather than refused")
		}
		if got := routing.Rule("").RuleOrDefault(); got != routing.RuleFailover {
			t.Errorf("a pool that names no rule is served by %q, and the contract reads it as %q", got, routing.RuleFailover)
		}
		for _, r := range routing.Rules {
			if got := r.RuleOrDefault(); got != r {
				t.Errorf("a pool that names the rule %q is served by %q, and RuleOrDefault must move no rule that is already named", r, got)
			}
		}
		for _, bad := range []routing.Rule{"sticky", "Ordered", "round_robin", "random"} {
			if bad.Valid() {
				t.Errorf("the rule %q is not one of the contract's and Valid accepts it: a value that is not a rule is refused, so no pool is served by a word nobody defined", bad)
			}
		}
	})

	t.Run("the principal kinds are the contract's", func(t *testing.T) {
		if routing.PrincipalPerson != "person" || routing.PrincipalServiceAccount != "service-account" || routing.PrincipalProject != "project" {
			t.Errorf("the principal kinds are (%q, %q, %q), and the contract's are (person, service-account, project)",
				routing.PrincipalPerson, routing.PrincipalServiceAccount, routing.PrincipalProject)
		}
		want := []routing.PrincipalKind{routing.PrincipalPerson, routing.PrincipalServiceAccount, routing.PrincipalProject}
		if !slices.Equal(routing.PrincipalKinds, want) {
			t.Errorf("the kinds a principal may be written with are %q, and the contract's are %q", routing.PrincipalKinds, want)
		}
		// A person's id is the Google subject and a service account's is its
		// address: both are identities, and an identity with no id names nobody.
		for _, p := range []routing.Principal{
			{Kind: routing.PrincipalPerson, ID: "104928374650192837465"},
			{Kind: routing.PrincipalServiceAccount, ID: "runner@fugaro.iam.gserviceaccount.com"},
			{Kind: routing.PrincipalProject, ID: "fugaro"},
		} {
			if !p.Valid() {
				t.Errorf("the principal %+v is one the contract names and Valid refuses it", p)
			}
		}
		for _, bad := range []routing.Principal{
			{},
			{Kind: routing.PrincipalPerson},
			{Kind: routing.PrincipalServiceAccount},
			{Kind: routing.PrincipalProject},
			{Kind: "team", ID: "fugaro"},
			{Kind: "service_account", ID: "runner@fugaro.iam.gserviceaccount.com"},
			{ID: "104928374650192837465"},
		} {
			if bad.Valid() {
				t.Errorf("the principal %+v names no principal this release knows and Valid accepts it", bad)
			}
		}
	})

	t.Run("a threshold is optional and bounded", func(t *testing.T) {
		// Greater than 0 and at most 1: the bound PL1's validation and PL8's
		// picker share, so a value the config accepts is one the picker acts on.
		good := []routing.Threshold{0.01, 0.5, 0.8, 0.99, 1}
		for _, th := range good {
			if !th.Valid() {
				t.Errorf("the threshold %v is inside the contract's bound and Valid refuses it", th)
			}
		}
		bad := []routing.Threshold{-1, -0.5, 0, 1.01, 2}
		for _, th := range bad {
			if th.Valid() {
				t.Errorf("the threshold %v is outside the contract's bound (greater than 0, at most 1) and Valid accepts it", th)
			}
		}
	})

	t.Run("the designation writes the members the contract names", func(t *testing.T) {
		raw, err := json.Marshal(routing.Designation{
			Principal: routing.Principal{Kind: routing.PrincipalPerson, ID: "104928374650192837465"},
			Project:   "fugaro",
			Pool:      "mine",
		})
		if err != nil {
			t.Fatalf("a designation does not marshal: %v", err)
		}
		assertMembers(t, "the designation", raw, "principal", "project", "pool")
		assertMembers(t, "the designation's principal", mustObject(t, raw, "principal"), "id", "kind")
	})

	t.Run("a pool reference writes the members the contract names", func(t *testing.T) {
		threshold := routing.Threshold(0.8)
		raw, err := json.Marshal(routing.PoolRef{
			Project:   "fugaro",
			Pool:      "mine",
			Rule:      routing.RuleRoundRobin,
			Threshold: &threshold,
			Principal: routing.Principal{Kind: routing.PrincipalPerson, ID: "104928374650192837465"},
		})
		if err != nil {
			t.Fatalf("a pool reference does not marshal: %v", err)
		}
		assertMembers(t, "the pool reference", raw, "project", "pool", "rule", "threshold", "principal")
		assertMembers(t, "the pool reference's principal", mustObject(t, raw, "principal"), "id", "kind")

		// A pool with no threshold says so rather than carrying a number that
		// was never written: the member is optional and null is how its absence
		// reaches a reader.
		bare, err := json.Marshal(routing.PoolRef{Project: "fugaro", Pool: "mine", Rule: routing.RuleOrdered})
		if err != nil {
			t.Fatalf("a pool reference without a threshold does not marshal: %v", err)
		}
		if got := string(mustObject(t, bare, "threshold")); got != "null" {
			t.Errorf("a pool that names no threshold writes %s, and an absent threshold is null", got)
		}
	})

	t.Run("the lookup is one method the service calls", func(t *testing.T) {
		// The interface the service holds: the principal the slip verified, the
		// pool a launch named ("" when it named none) and the pool it is served
		// from, with found false for "no pool designated" — a function shape
		// satisfies it, so a side can pass one without a type of its own.
		var _ routing.LauncherPool = launcherPoolFunc(func(context.Context, routing.Principal, string) (routing.PoolRef, bool, error) {
			return routing.PoolRef{}, false, nil
		})
	})

	t.Run("the pool listing writes names and figures and never a value", func(t *testing.T) {
		reset := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
		threshold := routing.Threshold(0.8)
		headroom := 0.42
		raw, err := json.Marshal(routing.PoolListDoc{
			Project: "fugaro",
			Pools: []routing.PoolEntry{{
				Name:      "team",
				Rule:      routing.RuleOrdered,
				Principal: routing.Principal{Kind: routing.PrincipalProject, ID: "fugaro"},
				Threshold: &threshold,
				Members: []routing.PoolMember{{
					Name:      "claude-work",
					State:     "available",
					NextReset: &reset,
					Usage:     &routing.PoolUsage{Calls: 12, Tokens: 3456},
					Headroom:  &headroom,
				}},
			}},
		})
		if err != nil {
			t.Fatalf("the pool listing does not marshal: %v", err)
		}
		assertMembers(t, "the pool listing", raw, "project", "pools")
		entry := mustObject(t, raw, "pools", 0)
		assertMembers(t, "a pool", entry, "name", "rule", "principal", "threshold", "members")
		assertMembers(t, "a pool's principal", mustObject(t, entry, "principal"), "id", "kind")
		member := mustObject(t, entry, "members", 0)
		assertMembers(t, "a pool member", member, "name", "state", "next_reset", "usage", "headroom")
		assertMembers(t, "a member's usage", mustObject(t, member, "usage"), "calls", "tokens")

		// The figures a member carries are what it has spent and what is left,
		// and the three that can be absent are null rather than a zero that
		// reads as a figure: a member nothing has counted, one the provider
		// reported no utilisation for (D3178: headroom is never guessed) and
		// one nothing holds.
		bare, err := json.Marshal(routing.PoolListDoc{
			Project: "fugaro",
			Pools: []routing.PoolEntry{{
				Name:      "team",
				Rule:      routing.RuleFailover,
				Principal: routing.Principal{Kind: routing.PrincipalProject, ID: "fugaro"},
				Members:   []routing.PoolMember{{Name: "claude-work", State: "available"}},
			}},
		})
		if err != nil {
			t.Fatalf("a pool listing with no figure does not marshal: %v", err)
		}
		bareEntry := mustObject(t, bare, "pools", 0)
		for _, key := range []string{"threshold"} {
			if got := string(mustObject(t, bareEntry, key)); got != "null" {
				t.Errorf("a pool with no %s writes %s, and it is null", key, got)
			}
		}
		bareMember := mustObject(t, bareEntry, "members", 0)
		for _, key := range []string{"next_reset", "usage", "headroom"} {
			if got := string(mustObject(t, bareMember, key)); got != "null" {
				t.Errorf("a member with no %s writes %s, and a figure nobody has is null", key, got)
			}
		}
	})
}

// launcherPoolFunc is a function shaped as the contract's lookup, so that the
// interface's own signature is pinned at compile time without a type.
type launcherPoolFunc func(context.Context, routing.Principal, string) (routing.PoolRef, bool, error)

func (f launcherPoolFunc) LauncherPool(ctx context.Context, principal routing.Principal, named string) (routing.PoolRef, bool, error) {
	return f(ctx, principal, named)
}

// assertMembers pins which members a shape writes and, in doing so, that it
// writes no other: a member added is an edit this test sees, and a member
// removed or renamed is an edit a reader outside this binary would have met.
func assertMembers(t *testing.T, what string, raw json.RawMessage, want ...string) {
	t.Helper()
	var got map[string]json.RawMessage
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("%s does not read as an object: %v: %s", what, err, raw)
	}
	keys := make([]string, 0, len(got))
	for k := range got {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	slices.Sort(want)
	if !slices.Equal(keys, want) {
		t.Errorf("%s writes the members %q, and the contract's are %q", what, keys, want)
	}
}

// mustObject is one value of an encoded shape: the member itself, or the nth
// item of a member that is a list.
func mustObject(t *testing.T, raw json.RawMessage, member string, item ...int) json.RawMessage {
	t.Helper()
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("reading %s as an object: %v", raw, err)
	}
	value, ok := doc[member]
	if !ok {
		t.Fatalf("the shape carries no %q: %s", member, raw)
	}
	if len(item) == 0 {
		return value
	}
	var list []json.RawMessage
	if err := json.Unmarshal(value, &list); err != nil {
		t.Fatalf("reading %q as a list: %v", member, err)
	}
	if item[0] >= len(list) {
		t.Fatalf("%q holds %d items, and item %d was asked for: %s", member, len(list), item[0], raw)
	}
	return list[item[0]]
}

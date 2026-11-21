package config

import (
	"strings"
	"testing"
)

// TestTheMarkerIsRefusedWhereNoCascadeServes: the cascade's marker is
// `fugaro update`'s own write, for the pool it migrates out of a project whose
// cascade serves (D3064 as amended), so a configuration that turns no cascade
// on is one where no such pool exists — and there the marker would be a claim
// about a cascade nothing serves, made by a pool that would then be served its
// subscriptions with the subscription-spread switch off (D1622) on the strength
// of a key an operator wrote. It is refused where the config is validated, at
// the key that wrote it, and the sentence names the two fixes.
//
// The routing that turns the cascade on is the same one the launcher asks
// before it launches a run of the project (routing.claude.via: cascade, D133
// and D150), and the other three routes are each refused: a marker admitted
// under `via: direct` would be the same false claim one route over.
func TestTheMarkerIsRefusedWhereNoCascadeServes(t *testing.T) {
	pools := []Pool{{
		Name:    "acme-cascade",
		Members: []string{"claude-sub", "claude-paid"},
		Cascade: true,
	}}
	creds := []Credential{
		{Name: "claude-sub", Type: CredentialTypeClaudeOAuth},
		{Name: "claude-paid", Type: CredentialTypeAnthropic},
	}
	// The marker's own rule and not the block's: a marked pool is a pool like
	// any other to ValidatePools.
	if ps := ValidatePools(pools, creds); len(ps) > 0 {
		t.Fatalf("the pool's own rules refuse the marker's pool: %s", problemsText(ps))
	}
	for _, tc := range []struct {
		name    string
		routing *Routing
		refused bool
	}{
		{"no routing block", nil, true},
		{"direct", &Routing{Claude: &RoutingClaude{Via: RouteDirect}}, true},
		{"a provider route", &Routing{Claude: &RoutingClaude{Via: RouteOpenRouter, Models: []string{"claude-sonnet-5-5"}}}, true},
		{"the cascade", &Routing{Claude: &RoutingClaude{Via: RouteCascade}}, false},
	} {
		ps := ValidateCascadePools(pools, tc.routing)
		if !tc.refused {
			if len(ps) > 0 {
				t.Errorf("%s: the pool the migration writes was refused: %s", tc.name, problemsText(ps))
			}
			continue
		}
		if len(ps) != 1 {
			t.Errorf("%s: %d problem(s), want the marker refused once: %s", tc.name, len(ps), problemsText(ps))
			continue
		}
		if got := ps[0].Path; got != "pools.acme-cascade.cascade" {
			t.Errorf("%s: the refusal is at %s, want the key that wrote the marker", tc.name, got)
		}
		for _, want := range []string{"cascade", RouteCascade, "fugaro update"} {
			if !strings.Contains(ps[0].Message, want) {
				t.Errorf("%s: the refusal does not name %q: %s", tc.name, want, ps[0].Message)
			}
		}
	}
	// An unmarked pool is none of this rule's business, wherever the routing
	// points: an ordinary pool of subscriptions is bounded by the spread switch
	// and by its owner, which is what the marker changes.
	plain := []Pool{{Name: "acme-pool", Members: []string{"claude-sub"}}}
	for _, r := range []*Routing{nil, {Claude: &RoutingClaude{Via: RouteCascade}}} {
		if ps := ValidateCascadePools(plain, r); len(ps) > 0 {
			t.Errorf("an unmarked pool was refused: %s", problemsText(ps))
		}
	}
}

// TestAMarkedPoolsOwnerIsRefused: the cascade's pool is served as the cascade
// serves and not to a person, so it names no owner and no allowed users — the
// legacy_open marker's rule one fact over (poolAccessProblems, D1942), and the
// rule that lets the publisher write the pool as an ordinary legacy_open one
// without asking for an owner (publishedCraftPools, F36). An owner written
// beside the marker is refused rather than dropped: the publish would otherwise
// publish a pool whose owner nobody reads.
func TestAMarkedPoolsOwnerIsRefused(t *testing.T) {
	serving := &Routing{Claude: &RoutingClaude{Via: RouteCascade}}
	base := Pool{Name: "acme-cascade", Members: []string{"claude-sub"}, Cascade: true}

	owned := base
	owned.Owner = "owner@example.com"
	ps := ValidateCascadePools([]Pool{owned}, serving)
	if len(ps) != 1 || ps[0].Path != "pools.acme-cascade.owner" {
		t.Fatalf("an owner beside the marker: %s, want one refusal at the owner key", problemsText(ps))
	}
	if !strings.Contains(ps[0].Message, "cascade") {
		t.Errorf("the refusal does not say what the owner is refused beside: %s", ps[0].Message)
	}

	shared := base
	shared.AllowedUsers = []string{"other@example.com"}
	ps = ValidateCascadePools([]Pool{shared}, serving)
	if len(ps) != 1 || ps[0].Path != "pools.acme-cascade.allowed_users" {
		t.Fatalf("an allowed user beside the marker: %s, want one refusal at the allowed_users key", problemsText(ps))
	}

	// The same owner is the owner's own business on a pool that carries no
	// marker, as it is on every other pool.
	plain := base
	plain.Cascade = false
	plain.Owner = "owner@example.com"
	if ps := ValidateCascadePools([]Pool{plain}, serving); len(ps) > 0 {
		t.Errorf("an unmarked pool's owner was refused: %s", problemsText(ps))
	}
}

// TestPublishedPoolsCarryNoMarker: the marker is the installation's own key,
// and the shared node carries none of it (R15, D3064 as amended) — what the
// node holds is the ordinary legacy_open pool the publisher writes there. The
// read refuses it rather than passing it over, because a reader that shrugged
// at the key would let a node written by hand keep a marker whose whole meaning
// is a rule the node's readers may not apply, and because this reader had an
// answer to the key before the field existed: it was an unknown member of
// craft_pools, and the node's decode refused it (DisallowUnknownFields).
func TestPublishedPoolsCarryNoMarker(t *testing.T) {
	creds := []Credential{{Name: "claude-sub", Type: CredentialTypeClaudeOAuth}}
	marked := []Pool{{Name: "acme-cascade", Members: []string{"claude-sub"}, Cascade: true}}
	ps := ValidatePublishedPools(marked, creds)
	if len(ps) != 1 || ps[0].Path != "pools.acme-cascade.cascade" {
		t.Fatalf("a published pool carrying the marker: %s, want one refusal at the key", problemsText(ps))
	}
	if !strings.Contains(ps[0].Message, "legacy_open") {
		t.Errorf("the refusal does not say what the node is served instead: %s", ps[0].Message)
	}
	// The node's own pool, the one the publisher writes for the migration, is
	// admitted: the marker is what is refused, never the pool.
	served := []Pool{{Name: "acme-cascade", Members: []string{"claude-sub"}, LegacyOpen: true}}
	if ps := ValidatePublishedPools(served, creds); len(ps) > 0 {
		t.Errorf("the pool the node is served was refused: %s", problemsText(ps))
	}
}

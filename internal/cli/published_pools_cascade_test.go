package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// cascadePoolYAML is the local config `fugaro update` leaves behind on a
// project whose cascade serves: the switch that turns the cascade on
// (routing.claude.via: cascade, the O2 switch), the credentials the cascade
// walks, and the pool marked as the cascade's (D3064 as amended). The paid key
// is the cascade's last tier, which is where it sits today for a project whose
// fallback is auto.
const cascadePoolYAML = "routing: { claude: { via: cascade } }\n" +
	"credentials:\n" +
	"  - { name: claude-sub, type: claude-oauth }\n" +
	"  - { name: claude-paid, type: anthropic }\n" +
	"pools:\n" +
	"  - { name: acme-cascade, strategy: failover, members: [claude-sub, claude-paid], cascade: true }\n"

// cascadePoolNoRoutingYAML is that config with the switch taken out: a machine
// that wrote the marker by hand, or one whose cascade the published node turned
// off since. Nothing here serves a cascade, so no pool may claim to be one.
const cascadePoolNoRoutingYAML = "credentials:\n" +
	"  - { name: claude-sub, type: claude-oauth }\n" +
	"  - { name: claude-paid, type: anthropic }\n" +
	"pools:\n" +
	"  - { name: acme-cascade, strategy: failover, members: [claude-sub, claude-paid], cascade: true }\n"

// publishedCraftPool is the node's pool of this name, and whether the node
// holds one.
func publishedCraftPool(t *testing.T, p *projectpolicy.Policy, name string) (config.Pool, bool) {
	t.Helper()
	for _, pool := range p.CraftPools {
		if pool.Name == name {
			return pool, true
		}
	}
	return config.Pool{}, false
}

// TestTheCascadesPoolIsPublishedLegacyOpen: the cascade's pool is published as
// an ordinary legacy_open pool — its members in the order the cascade tries
// them, the strategy it resolves to, and the migration's marker that says the
// pool is served to everyone rather than to an owner — because that is exactly
// what the cascade is (D3064 as amended, R15). It is the one exception to the
// publisher's rule that clears legacy_open on every pool a config states and
// asks for an owner (F36): the pool the migration wrote names neither an owner
// nor the migration's marker in the file, and a publish that asked it for an
// owner would leave a project's cascaded runs served nothing.
func TestTheCascadesPoolIsPublishedLegacyOpen(t *testing.T) {
	f := newProjectFixture(t, cascadePoolYAML)
	out, _, err := execute(t, "project", "policy", "publish")
	if err != nil {
		t.Fatalf("the publish of a project whose cascade serves was refused: %v\n%s", err, out)
	}
	pool, ok := publishedCraftPool(t, publishedPolicy(t, f), "acme-cascade")
	if !ok {
		t.Fatalf("the node holds no pool named acme-cascade:\n%s", out)
	}
	if !pool.LegacyOpen {
		t.Errorf("craft_pools acme-cascade = %+v, want the marker an ordinary legacy_open pool carries", pool)
	}
	if pool.Owner != "" || len(pool.AllowedUsers) != 0 {
		t.Errorf("craft_pools acme-cascade = %+v, want no owner: the pool is served as the cascade serves, not to a person", pool)
	}
	if len(pool.Members) != 2 || pool.Members[0] != "claude-sub" || pool.Members[1] != "claude-paid" {
		t.Errorf("craft_pools acme-cascade = %+v, want the cascade's members in the order the cascade tries them", pool.Members)
	}
	if pool.StrategyOrDefault() != config.PoolStrategyFailover {
		t.Errorf("craft_pools acme-cascade strategy = %s, want the strategy the file wrote", pool.StrategyOrDefault())
	}
	// The diff the operator reads says what the node now holds, marker and all:
	// a pool served to everyone is not one to read as a pool of one person's
	// tokens.
	if want := "+ craft_pools acme-cascade: members claude-sub, claude-paid, strategy failover, legacy_open (served to everyone: the migration's marker)"; !strings.Contains(out, want) {
		t.Errorf("the diff does not name the published cascade pool (%q):\n%s", want, out)
	}
}

// TestTheSharedNodeCarriesNoMarker: the marker lives in the installation's own
// config, where the launcher and the access record read it, and never in the
// shared node — the node's readers are job images (R15), which know no marker,
// and one that read the pool as the cascade's on the strength of a member it
// could see would be serving a pool by a rule the service is the only thing
// that may apply (CM36). What the node holds is the ordinary legacy_open pool
// the test above pins; what it must not hold is the key.
func TestTheSharedNodeCarriesNoMarker(t *testing.T) {
	f := newProjectFixture(t, cascadePoolYAML)
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	published := publishedPolicy(t, f)
	if _, ok := publishedCraftPool(t, published, "acme-cascade"); !ok {
		t.Fatal("the node holds no pool named acme-cascade, so there is nothing to assert the marker's absence in")
	}
	raw, err := json.Marshal(f.db.Value(projectpolicy.PathShared()))
	if err != nil {
		t.Fatal(err)
	}
	var node map[string]any
	if err := json.Unmarshal(raw, &node); err != nil {
		t.Fatal(err)
	}
	entries, ok := node["craft_pools"].([]any)
	if !ok {
		t.Fatalf("the node's craft_pools is %T, want the array the publish wrote", node["craft_pools"])
	}
	for _, e := range entries {
		entry, ok := e.(map[string]any)
		if !ok {
			t.Fatalf("craft_pools entry %v is not an object", e)
		}
		if _, found := entry["cascade"]; found {
			t.Errorf("craft_pools %v carries the cascade's marker, and the marker is the installation's alone (R15)", entry)
		}
	}
}

// TestAnUnmarkedPoolIsPublishedAsBefore: the pool that carries no marker is
// published by the rule that was there before this one — its members, its
// strategy and its owner, no legacy_open the file did not write, and an
// ownerless one still left out and named (D2608) — so the exception above moves
// nothing else.
func TestAnUnmarkedPoolIsPublishedAsBefore(t *testing.T) {
	f := newProjectFixture(t, craftMembersYAML)
	out, _, err := execute(t, "project", "policy", "publish")
	if err != nil {
		t.Fatalf("the publish of a config holding an ordinary pool was refused: %v\n%s", err, out)
	}
	pool, ok := publishedCraftPool(t, publishedPolicy(t, f), "acme-pool")
	if !ok {
		t.Fatalf("the node holds no pool named acme-pool:\n%s", out)
	}
	if pool.LegacyOpen {
		t.Errorf("craft_pools acme-pool = %+v, want no legacy_open: the config states an owner, not a pool that predates owners", pool)
	}
	if pool.Owner != "owner@example.com" || !strings.EqualFold(strings.Join(pool.Members, ","), "acme-main,vertex") {
		t.Errorf("craft_pools acme-pool = %+v, want the file's members and its owner", pool)
	}
	rewriteProjectConfig(t, f, craftLoosePoolYAML)
	out, _, err = execute(t, "project", "policy", "publish")
	if err != nil {
		t.Fatalf("a config holding an ownerless pool failed the publish: %v", err)
	}
	if !strings.Contains(out, "craft_pools acme-loose not published: owner is required") {
		t.Errorf("the diff does not name the ordinary ownerless pool the node cannot carry:\n%s", out)
	}
	if _, ok := publishedCraftPool(t, publishedPolicy(t, f), "acme-loose"); ok {
		t.Error("the publish wrote the ownerless pool, which no marker may excuse")
	}
}

// TestPublishedCraftPoolsRefuseTheMarkerWhereNoCascadeServes: the marker is
// refused where the installation's config is validated, and not at the publish
// — a config that carries it with no cascade behind it is not one any command
// of this installation may read (config.ValidateCascadePools). The refusal
// names the key that wrote it and the fix, and it reaches the operator through
// the size of the diff's own line: nothing is published, and nothing is
// published by halves.
func TestPublishedCraftPoolsRefuseTheMarkerWhereNoCascadeServes(t *testing.T) {
	newProjectFixture(t, cascadePoolNoRoutingYAML)
	out, errOut, err := execute(t, "project", "policy", "publish")
	if err == nil {
		t.Fatalf("a marked pool with no cascade serving was read: %s%s", out, errOut)
	}
	got := fmt.Sprintf("%v%s", err, errOut)
	for _, want := range []string{"pools.acme-cascade.cascade", config.RouteCascade} {
		if !strings.Contains(got, want) {
			t.Errorf("the refusal does not name %q: %s", want, got)
		}
	}
}

// newMemberPoolYAML is craftMembersYAML's pool with the members PL1 adds (R5,
// R21): the rule it is served by and the principal it belongs to. A threshold is
// not here because no file can carry one yet — validation refuses a threshold
// wherever it is written, since no rule set this release ships acts on one
// (poolRuleProblems, PL8) — so the strip below is exercised by the two members a
// pool can really hold, and the third assertion it makes is that the node
// carries no such key at all.
const newMemberPoolYAML = "credentials:\n" +
	"  - { name: acme-main, type: openrouter }\n" +
	"pools:\n" +
	"  - { name: acme-pool, strategy: failover, members: [acme-main, vertex], owner: owner@example.com, rule: ordered, principal: { kind: person, id: '1053' } }\n"

// TestPublishedCraftPoolsDropTheNewMembers: a pool's rule and its principal are
// the two members PL1 adds that a file can carry, and the node's craft_pools may
// not carry either — that block is decoded strictly by every job image of the
// installation, so a release behind this one refuses the whole node for a key
// it does not know. The pools in their new shape travel under the policy's
// `model_routing` key instead (RT4), and what craft_pools holds is the pool as
// its readers have always read it. The pool is still published, with the
// members and the owner the file states: dropping the pool would take a
// project's runs off their credentials to protect a reader from a key.
func TestPublishedCraftPoolsDropTheNewMembers(t *testing.T) {
	f := newProjectFixture(t, newMemberPoolYAML)
	out, _, err := execute(t, "project", "policy", "publish")
	if err != nil {
		t.Fatalf("the publish of a config holding a pool with a rule and a principal was refused: %v\n%s", err, out)
	}
	pool, ok := publishedCraftPool(t, publishedPolicy(t, f), "acme-pool")
	if !ok {
		t.Fatalf("the node holds no pool named acme-pool, and the two members are not a reason to leave the pool out:\n%s", out)
	}
	if pool.Rule != "" || pool.Principal != nil || pool.Threshold != nil {
		t.Errorf("craft_pools acme-pool = %+v, want none of rule, principal or threshold: a release behind this one decodes this block strictly and refuses a node carrying them", pool)
	}
	if pool.Owner != "owner@example.com" || strings.Join(pool.Members, ",") != "acme-main,vertex" {
		t.Errorf("craft_pools acme-pool = %+v, want the file's members and its owner", pool)
	}
	// The node's own bytes must not carry the three keys anywhere, the diff and
	// the published document alike: a `rule` written into craft_pools is exactly
	// the shape a reader behind this release refuses the whole node for.
	for _, key := range []string{"\"rule\"", "\"principal\"", "\"threshold\""} {
		if strings.Contains(out, key) {
			t.Errorf("the publish wrote %s into the node's craft_pools:\n%s", key, out)
		}
	}
}

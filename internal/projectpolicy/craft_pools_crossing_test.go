package projectpolicy

// The crossing at the node's own read (MC20, D1668, D1673) and the step that
// converges it (MC21). `Decode` runs `config.ValidatePublishedPools`, so a rule
// of that function is a rule of the runner's `Read`, of the credentials
// service's per-call re-read, of `doctor` and of `update` itself — every reader
// of the node at once. The rule is therefore held beside a chain step that
// rewrites the node: a node written by 0.25.0 may carry a crossing pool,
// because that release's `accounts pool add` wrote one and its `accounts pool
// ls` read it as valid, so a reader that refused it with no step to repair it
// would refuse a node nothing can converge — a broken installation, which
// CLAUDE.md's HARD RULE forbids, and `fugaro update` is one of those readers.
//
// The step is migrateV8ToV9: the version bump whose migration splits every pool
// whose members span the two halves into one pool per half, and repoints the
// crafts whose access named it to the half their own model routes to. What it
// derives, it derives from the node — the credentials' types, the pool's own
// member order, the crafts' models over the node's own routing switch and
// providers block — because a step of the chain may read nothing else.

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// crossPathNode is a version-8 node carrying one pool whose two members are
// served by different halves: openrouter-x by the run's own gateway and
// anthropic-y by the credentials service. The pool is written with everything a
// pool can carry — a strategy, an owner and an allowed user — and both crafts
// name it, each resolving to a different half.
const crossPathNode = `{"version":8,` +
	`"craft_credentials":[{"name":"openrouter-x","type":"openrouter"},{"name":"anthropic-y","type":"anthropic"}],` +
	`"craft_pools":[{"name":"mixed","strategy":"round-robin","owner":"owner@example.com","allowed_users":["other@example.com"],"members":["openrouter-x","anthropic-y"]}],` +
	`"crafts":{"coder":{"model":"claude-opus-5-5","access":"mixed"},"review":{"model":"deepseek/deepseek-v4-flash","access":"mixed"}},` +
	`"providers":{"openrouter":{"kind":"anthropic-compat","base_url":"https://openrouter.ai/api/v1","auth":"bearer","secret":"openrouter-key","models":["deepseek/*"]}},` +
	`"budget":{"mode":"observe"},"allow_repos":["github/acme/*"]}`

// TestTheNodeRefusesACrossPathPool: a node that carries a pool naming a member
// of each half is refused by every reader of it, with the one sentence that
// names the fix — two pools, one per half — because a pool's listed order is
// what failover walks and neither half of a run can walk the other's members.
// The refusal is the rule's, not the version's: the node below is at this
// binary's own version, so the only thing wrong with it is the crossing.
func TestTheNodeRefusesACrossPathPool(t *testing.T) {
	raw := []byte(strings.Replace(crossPathNode, `"version":8`, fmt.Sprintf(`"version":%d`, Version), 1))
	p, found, err := Decode(raw)
	if err == nil || p != nil {
		t.Fatalf("Decode of a node carrying a cross-path pool = (%+v, %v), want a refusal", p, err)
	}
	if !found {
		t.Error("the node exists and cannot be used, so found must be true (fail closed)")
	}
	for _, want := range []string{"openrouter-x", "anthropic-y", "name two pools, one per half"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not name %q: the operator has to be told which two members cross and what the fix is", err, want)
		}
	}
	// The two members are named in path order — the credentials service's
	// first — so the sentence reads the same whichever way the pool listed
	// them.
	if got := err.Error(); strings.Index(got, "anthropic-y") > strings.Index(got, "openrouter-x") {
		t.Errorf("the refusal %q names the run's own gateway's member first, want the credentials service's member first", got)
	}
}

// TestTheSplitStepCarriesACrossPathNode: the version-8 node becomes a
// version-9 one, its crossed pool becomes two pools, and every other member of
// the node is carried untouched — the credentials, the providers block, the
// budget and the allow-list come back as the node wrote them, which is what
// `Kept` claims and what the operator's diff shows.
func TestTheSplitStepCarriesACrossPathNode(t *testing.T) {
	migrated, changes, err := Migrate([]byte(crossPathNode))
	if err != nil {
		t.Fatalf("Migrate = %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("changes = %+v, want the one step from version 8", changes)
	}
	if changes[0].From != 8 || changes[0].To != 9 {
		t.Errorf("the step is %d to %d, want 8 to 9", changes[0].From, changes[0].To)
	}
	for _, want := range []string{"the version field: 8 to 9", "craft_pools", "mixed", "mixed-claude", "mixed-provider", "crafts"} {
		if !strings.Contains(changes[0].Changed, want) {
			t.Errorf("Changed = %q, want it to name %q: the diff is what the operator reads", changes[0].Changed, want)
		}
	}
	for _, kept := range []string{"craft_credentials", "providers", "budget", "allow_repos"} {
		if !slices.Contains(changes[0].Kept, kept) {
			t.Errorf("Kept = %v, want it to name %s: the step writes nothing outside the pools and the accesses it repoints", changes[0].Kept, kept)
		}
	}
	for _, wrote := range []string{"craft_pools", "crafts"} {
		if slices.Contains(changes[0].Kept, wrote) {
			t.Errorf("Kept = %v, want it to leave out %s, which the step rewrote", changes[0].Kept, wrote)
		}
	}

	p, found, err := Decode(migrated)
	if err != nil || !found || p == nil {
		t.Fatalf("the migrated node does not decode: (%+v, %v, %v)", p, found, err)
	}
	if p.Version != Version {
		t.Errorf("the migrated node is version %d, want %d", p.Version, Version)
	}
	if len(p.CraftPools) != 2 {
		t.Fatalf("the migrated node's pools = %+v, want two", p.CraftPools)
	}
	if len(p.CraftCredentials) != 2 || p.Providers["openrouter"].Secret != "openrouter-key" {
		t.Errorf("the node's other blocks came back as %+v / %+v, want them as written", p.CraftCredentials, p.Providers)
	}
	if !slices.Equal(p.AllowRepos, []string{"github/acme/*"}) || p.Budget.Mode != "observe" {
		t.Errorf("the migrated node's policy = %+v, want the policy the node carried", p)
	}
	// The bytes the step did not touch are the node's own: the migration edits
	// the owner's document rather than re-encoding it.
	for _, literal := range []string{`"strategy":"round-robin"`, `"allowed_users":["other@example.com"]`, `"base_url":"https://openrouter.ai/api/v1"`} {
		if !strings.Contains(string(migrated), literal) {
			t.Errorf("the migrated node does not hold %s as the fixture wrote it, so the step re-encoded the document", literal)
		}
	}
}

// TestTheNodeSplitKeepsEveryMemberAndItsOrder: each half holds the members that
// half serves, in the order the pool listed them, and carries the pool's
// strategy and its owner block — the split is a partition of the list and not a
// rewrite of it. The halves are named for the half they are served by, and the
// craft that named the pool names the half its own model routes to: the coder's
// Claude ID goes direct, which is the credentials service's half, and the
// review's DeepSeek model is claimed by the openrouter entry, which is the
// run's own gateway.
func TestTheNodeSplitKeepsEveryMemberAndItsOrder(t *testing.T) {
	migrated, _, err := Migrate([]byte(crossPathNode))
	if err != nil {
		t.Fatalf("Migrate = %v", err)
	}
	p, _, err := Decode(migrated)
	if err != nil {
		t.Fatalf("the migrated node does not decode: %v", err)
	}
	byName := map[string]struct {
		members []string
		owner   string
		users   []string
		strat   string
	}{}
	for _, pool := range p.CraftPools {
		byName[pool.Name] = struct {
			members []string
			owner   string
			users   []string
			strat   string
		}{pool.Members, pool.Owner, pool.AllowedUsers, pool.Strategy}
	}
	claude, ok := byName["mixed-claude"]
	if !ok {
		t.Fatalf("the migrated node's pools = %+v, want one named mixed-claude", p.CraftPools)
	}
	provider, ok := byName["mixed-provider"]
	if !ok {
		t.Fatalf("the migrated node's pools = %+v, want one named mixed-provider", p.CraftPools)
	}
	if !slices.Equal(claude.members, []string{"anthropic-y"}) {
		t.Errorf("mixed-claude.members = %v, want the credentials service's member alone", claude.members)
	}
	if !slices.Equal(provider.members, []string{"openrouter-x"}) {
		t.Errorf("mixed-provider.members = %v, want the run's own gateway's member alone", provider.members)
	}
	for name, half := range map[string]struct {
		members []string
		owner   string
		users   []string
		strat   string
	}{"mixed-claude": claude, "mixed-provider": provider} {
		if half.owner != "owner@example.com" || half.strat != "round-robin" || !slices.Equal(half.users, []string{"other@example.com"}) {
			t.Errorf("%s = %+v, want the pool's strategy, owner and allowed users carried over", name, half)
		}
	}
	if got := p.Crafts.Coder.Access; got != "mixed-claude" {
		t.Errorf("crafts.coder.access = %q, want mixed-claude: a Claude ID goes direct, which is the credentials service's half", got)
	}
	if got := p.Crafts.Review.Access; got != "mixed-provider" {
		t.Errorf("crafts.review.access = %q, want mixed-provider: the openrouter entry claims that model, and a provider route is the run's own gateway", got)
	}
	if p.Crafts.Coder.Model != "claude-opus-5-5" || p.Crafts.Review.Model != "deepseek/deepseek-v4-flash" {
		t.Errorf("the crafts' models = %+v, want them untouched: the step writes the access half alone", p.Crafts)
	}
}

// TestTheNodeSplitNeedsNothingOutsideTheNode: the step's only input is the node,
// which is the chain's own contract. Two nodes differing only in a member the
// step cannot place from inside them — a craft whose model resolves nothing —
// still split, and the craft keeps the half the pool's FIRST member is on: the
// member the listed order would have served first, which is a fact about the
// pool and not a guess at a spending path.
func TestTheNodeSplitNeedsNothingOutsideTheNode(t *testing.T) {
	raw := `{"version":8,` +
		`"craft_credentials":[{"name":"openrouter-x","type":"openrouter"},{"name":"anthropic-y","type":"anthropic"}],` +
		`"craft_pools":[{"name":"mixed","owner":"owner@example.com","members":["openrouter-x","anthropic-y"]}],` +
		`"crafts":{"coder":{"access":"mixed"}}}`

	migrated, _, err := Migrate([]byte(raw))
	if err != nil {
		t.Fatalf("Migrate = %v", err)
	}
	p, _, err := Decode(migrated)
	if err != nil {
		t.Fatalf("the migrated node does not decode: %v", err)
	}
	if len(p.CraftPools) != 2 {
		t.Fatalf("the migrated node's pools = %+v, want two: the split needs no member outside the node", p.CraftPools)
	}
	if got := p.Crafts.Coder.Access; got != "mixed-provider" {
		t.Errorf("crafts.coder.access = %q, want mixed-provider: a craft whose model resolves nothing keeps the half the pool's first member is on", got)
	}

	// The other direction of the same rule: the first member is the credentials
	// service's, so the craft that resolves nothing follows it there.
	flipped := strings.Replace(raw, `"members":["openrouter-x","anthropic-y"]`, `"members":["anthropic-y","openrouter-x"]`, 1)
	migrated, _, err = Migrate([]byte(flipped))
	if err != nil {
		t.Fatalf("Migrate = %v", err)
	}
	p, _, err = Decode(migrated)
	if err != nil {
		t.Fatalf("the migrated node does not decode: %v", err)
	}
	if got := p.Crafts.Coder.Access; got != "mixed-claude" {
		t.Errorf("crafts.coder.access = %q, want mixed-claude: the pool lists that half's member first", got)
	}

	// And the step is a function of the bytes alone: the same node migrates to
	// the same document twice, with no state carried between the two runs.
	first, _, err := Migrate([]byte(crossPathNode))
	if err != nil {
		t.Fatalf("Migrate = %v", err)
	}
	second, _, err := Migrate([]byte(crossPathNode))
	if err != nil {
		t.Fatalf("Migrate = %v", err)
	}
	if string(first) != string(second) {
		t.Errorf("two migrations of one node differ:\n%s\n%s", first, second)
	}
}

// TestANodeWithNoCrossPathPoolOnlyMovesItsNumber: the step is not shape-free,
// and that is a statement about the nodes it may rewrite rather than about
// every node of its version. A node whose pools each name one half's members
// comes back with its version field alone rewritten and its craft_pools
// reported as kept — byte for byte, which is what a caller that diffs the two
// documents reads.
func TestANodeWithNoCrossPathPoolOnlyMovesItsNumber(t *testing.T) {
	const raw = `{"version":8,` +
		`"craft_credentials":[{"name":"openrouter-x","type":"openrouter"},{"name":"anthropic-y","type":"anthropic"}],` +
		`"craft_pools":[{"name":"providers","owner":"owner@example.com","members":["openrouter-x"]},{"name":"subs","owner":"owner@example.com","members":["anthropic-y"]}],` +
		`"crafts":{"coder":{"model":"claude-opus-5-5","access":"subs"},"review":{"model":"deepseek/deepseek-v4-flash","access":"providers"}}}`

	migrated, changes, err := Migrate([]byte(raw))
	if err != nil {
		t.Fatalf("Migrate = %v", err)
	}
	if len(changes) != 1 || changes[0].Changed != "the version field: 8 to 9" {
		t.Fatalf("changes = %+v, want the version field alone: no pool of this node crosses", changes)
	}
	if !slices.Contains(changes[0].Kept, "craft_pools") || !slices.Contains(changes[0].Kept, "crafts") {
		t.Errorf("Kept = %v, want craft_pools and crafts: the step wrote nothing inside either", changes[0].Kept)
	}
	want := strings.Replace(raw, `"version":8`, `"version":9`, 1)
	if string(migrated) != want {
		t.Errorf("the migrated node is not the node with its version rewritten:\n got %s\nwant %s", migrated, want)
	}
}

// TestTheSplitStepRefusesANodeItCannotRead: a member the step cannot read is
// refused as a malformed node rather than skipped, because skipping it would
// leave a crossing pool in the document this step hands to Decode — which would
// then refuse the node with a sentence about the pool rather than about the
// block that could not be read. The input comes back untouched.
func TestTheSplitStepRefusesANodeItCannotRead(t *testing.T) {
	const raw = `{"version":8,"craft_pools":{"mixed":["openrouter-x","anthropic-y"]}}`
	migrated, _, err := Migrate([]byte(raw))
	var me *MalformedError
	if !asMalformed(err, &me) {
		t.Fatalf("Migrate = %v, want a *MalformedError", err)
	}
	if string(migrated) != raw {
		t.Errorf("Migrate returned %s, want the input untouched", migrated)
	}
	if !strings.Contains(err.Error(), "craft_pools") {
		t.Errorf("the refusal %q does not name the block it could not read", err)
	}
}

// asMalformed is errors.As for *MalformedError, so the test above reads as the
// class it is about.
func asMalformed(err error, target **MalformedError) bool {
	me, ok := err.(*MalformedError)
	if ok {
		*target = me
	}
	return ok
}

// TestTheSplitIsAFixedPoint is the property `fugaro update` depends on: the
// node the step writes is one the step has nothing to do to, so an installation
// that has converged says so on every later update instead of splitting its
// pools again.
func TestTheSplitIsAFixedPoint(t *testing.T) {
	migrated, _, err := Migrate([]byte(crossPathNode))
	if err != nil {
		t.Fatalf("Migrate = %v", err)
	}
	again, changes, err := Migrate(migrated)
	if err != nil {
		t.Fatalf("Migrate(the migrated node) = %v", err)
	}
	if len(changes) != 0 || string(again) != string(migrated) {
		t.Errorf("the second migration reported %+v, want the node back byte for byte", changes)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(migrated, &doc); err != nil {
		t.Fatalf("the migrated node is not a JSON object: %v", err)
	}
	if _, ok := doc["craft_pools"]; !ok {
		t.Error("the migrated node has no craft_pools: the split drops the block it rewrites")
	}
}

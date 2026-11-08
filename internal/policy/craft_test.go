package policy

import (
	"reflect"
	"testing"
)

// layers of docs/model-crafts: the project's own first, then what a
// repository's file says (F12, D1628). Only the project layer creates
// credentials and pools; every layer above it selects among them and narrows
// a pool.
func craftLayer(creds []string, pools map[string]CraftPool, access map[string]string) Layer {
	return Layer{Craft: CraftLayer{Credentials: creds, Pools: pools, Access: access}}
}

func pool(strategy string, members ...string) CraftPool {
	return CraftPool{Strategy: strategy, Members: members}
}

// TestRepoCannotAddCredential: a credential is a secret reference and so a
// spending and data-egress grant, and only the project (the merge's first
// layer) hands one out. A later layer's credential is dropped and recorded,
// and a layer that re-lists one the project already has is not a loosened
// value and is not recorded (F12, D1628).
func TestRepoCannotAddCredential(t *testing.T) {
	project := craftLayer([]string{"openrouter-main", "vertex"}, nil, nil)

	e := Merge(project, craftLayer([]string{"repo-key"}, nil, nil))
	if want := []string{"openrouter-main", "vertex"}; !reflect.DeepEqual(e.Craft.Credentials, want) {
		t.Fatalf("credentials = %v, want %v", e.Craft.Credentials, want)
	}
	if e.Sources[CraftCredentialKey("openrouter-main")] != SourceCeiling {
		t.Fatalf("sources = %v", e.Sources)
	}
	if want := []Ignored{{CraftCredentialKey("repo-key"), "repo-key", "", ""}}; !reflect.DeepEqual(e.Ignored, want) {
		t.Fatalf("ignored = %+v, want %+v", e.Ignored, want)
	}

	// A layer that lists a credential the project has adds nothing: it is not
	// an introduction and not a loosening, so nothing is recorded for it.
	if e := Merge(project, craftLayer([]string{"vertex"}, nil, nil)); len(e.Ignored) != 0 {
		t.Fatalf("ignored = %+v", e.Ignored)
	}

	// A merge of layers that carry no craft at all stays as silent as it was.
	if e := Merge(Layer{}, Layer{}, Layer{}); e.Craft != (CraftLayer{}) || len(e.Sources) != 0 || len(e.Ignored) != 0 {
		t.Fatalf("empty layers -> %+v", e)
	}
}

// TestRepoCanOnlyNarrowPool: a later layer may drop members of the project's
// pool and may not add one, repeat one or reorder them. The order kept is
// what "failover" walks.
func TestRepoCanOnlyNarrowPool(t *testing.T) {
	project := craftLayer(nil, map[string]CraftPool{"claude": pool("", "c1", "c2", "c3")}, nil)
	repo := craftLayer(nil, map[string]CraftPool{"claude": pool("", "c1", "c3")}, nil)

	e := Merge(project, repo)
	if got := e.Craft.Pools["claude"].Members; !reflect.DeepEqual(got, []string{"c1", "c3"}) {
		t.Fatalf("members = %v", got)
	}
	if e.Sources[CraftPoolMembersKey("claude")] != SourceBranch {
		t.Fatalf("sources = %v", e.Sources)
	}
	if len(e.Ignored) != 0 {
		t.Fatalf("ignored = %+v", e.Ignored)
	}

	// The project's own list is not aliased into the result.
	e.Craft.Pools["claude"].Members[0] = "changed"
	if project.Craft.Pools["claude"].Members[0] != "c1" {
		t.Fatalf("the merge wrote into a layer: %v", project.Craft.Pools["claude"].Members)
	}
}

// TestLooserPoolNeverWins: a pool that adds a member, or that a layer
// introduces whole, is a looser value and the project's pool stands (D1628).
func TestLooserPoolNeverWins(t *testing.T) {
	project := craftLayer(nil, map[string]CraftPool{"claude": pool(PoolStrategyFailover, "c1", "c2")}, nil)

	e := Merge(project, craftLayer(nil, map[string]CraftPool{"claude": pool("", "c1", "c2", "c3")}, nil))
	if got := e.Craft.Pools["claude"].Members; !reflect.DeepEqual(got, []string{"c1", "c2"}) {
		t.Fatalf("added member won: %v", got)
	}
	want := []Ignored{{CraftPoolMembersKey("claude"), "c1,c2,c3", "c1,c2", SourceCeiling}}
	if !reflect.DeepEqual(e.Ignored, want) {
		t.Fatalf("ignored = %+v, want %+v", e.Ignored, want)
	}

	// A pool only the later layer has is one it may not create at all.
	e = Merge(project, craftLayer(nil, map[string]CraftPool{"repo-pool": pool("", "c1")}, nil))
	if len(e.Craft.Pools) != 1 {
		t.Fatalf("pools = %+v", e.Craft.Pools)
	}
	if want := []Ignored{{CraftPoolKey("repo-pool"), "repo-pool", "", ""}}; !reflect.DeepEqual(e.Ignored, want) {
		t.Fatalf("ignored = %+v, want %+v", e.Ignored, want)
	}

	// A layer that repeats the project's own members is not a loosening.
	if e := Merge(project, craftLayer(nil, map[string]CraftPool{"claude": pool("", "c1", "c2")}, nil)); len(e.Ignored) != 0 {
		t.Fatalf("ignored = %+v", e.Ignored)
	}
}

// TestRepoCannotReorderOrChangeStrategy: reordering the project's members is a
// loosening like adding one is. Of the two strategies, failover is the tighter
// — a pool that walks its list spreads neither spend nor the repository's code
// across its members — so a repository may keep or choose it and may not choose
// round-robin over a project's failover (F12, D1628).
func TestRepoCannotReorderOrChangeStrategy(t *testing.T) {
	project := craftLayer(nil, map[string]CraftPool{"claude": pool(PoolStrategyFailover, "c1", "c2", "c3")}, nil)

	e := Merge(project, craftLayer(nil, map[string]CraftPool{"claude": pool("", "c3", "c1")}, nil))
	if got := e.Craft.Pools["claude"].Members; !reflect.DeepEqual(got, []string{"c1", "c2", "c3"}) {
		t.Fatalf("reordered members won: %v", got)
	}
	if len(e.Ignored) != 1 || e.Ignored[0].Key != CraftPoolMembersKey("claude") {
		t.Fatalf("ignored = %+v", e.Ignored)
	}

	e = Merge(project, craftLayer(nil, map[string]CraftPool{"claude": pool(PoolStrategyRoundRobin)}, nil))
	if got := e.Craft.Pools["claude"].Strategy; got != PoolStrategyFailover {
		t.Fatalf("looser strategy won: %q", got)
	}
	want := []Ignored{{CraftPoolStrategyKey("claude"), PoolStrategyRoundRobin, PoolStrategyFailover, SourceCeiling}}
	if !reflect.DeepEqual(e.Ignored, want) {
		t.Fatalf("ignored = %+v, want %+v", e.Ignored, want)
	}

	// The other direction is the repository's to take: a pool the project
	// round-robins may be pinned to an order.
	rr := craftLayer(nil, map[string]CraftPool{"claude": pool(PoolStrategyRoundRobin, "c1", "c2")}, nil)
	e = Merge(rr, craftLayer(nil, map[string]CraftPool{"claude": pool(PoolStrategyFailover)}, nil))
	if got := e.Craft.Pools["claude"].Strategy; got != PoolStrategyFailover {
		t.Fatalf("strategy = %q", got)
	}
	if e.Sources[CraftPoolStrategyKey("claude")] != SourceBranch || len(e.Ignored) != 0 {
		t.Fatalf("sources = %v ignored = %+v", e.Sources, e.Ignored)
	}
}

// TestCraftAccessSelectsWhatTheProjectMakesAvailable: a later layer selects a
// craft's credential or pool from what the project's layers created, and a
// name outside that set — a credential, a pool, whatever it is called — is
// never introduced by naming it (F12, D1628).
func TestCraftAccessSelectsWhatTheProjectMakesAvailable(t *testing.T) {
	project := craftLayer(
		[]string{"openrouter-main", "vertex"},
		map[string]CraftPool{"claude": pool("", "bmb")},
		map[string]string{"coder": "openrouter-main"},
	)

	e := Merge(project, craftLayer(nil, nil, map[string]string{"coder": "vertex", "senior-review": "claude"}))
	if got := e.Craft.Access["coder"]; got != "vertex" {
		t.Fatalf("coder access = %q", got)
	}
	if got := e.Craft.Access["senior-review"]; got != "claude" {
		t.Fatalf("senior-review access = %q", got)
	}
	if e.Sources[CraftAccessKey("coder")] != SourceBranch || len(e.Ignored) != 0 {
		t.Fatalf("sources = %v ignored = %+v", e.Sources, e.Ignored)
	}

	// A name the project does not make available is dropped rather than taken,
	// so the craft keeps what the project selected for it — and a craft the
	// project selected nothing for is left unset, for the resolver to answer
	// (MC2).
	e = Merge(project, craftLayer(nil, nil, map[string]string{"coder": "repo-key", "review": "repo-key"}))
	if got := e.Craft.Access["coder"]; got != "openrouter-main" {
		t.Fatalf("coder access = %q", got)
	}
	if got, ok := e.Craft.Access["review"]; ok {
		t.Fatalf("review access = %q", got)
	}
	want := []Ignored{
		{CraftAccessKey("coder"), "repo-key", "openrouter-main", SourceCeiling},
		{CraftAccessKey("review"), "repo-key", "", ""},
	}
	if !reflect.DeepEqual(e.Ignored, want) {
		t.Fatalf("ignored = %+v, want %+v", e.Ignored, want)
	}
}

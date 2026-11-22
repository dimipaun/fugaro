package routing

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The auth and via values the migration reads, spelled in the tests as the
// config spells them (config.AgentAuths, config.Route*).
const (
	testAuthOAuth   = "oauth"
	testAuthAPIKey  = "api-key"
	testAuthCascade = "cascade"
	testAuthVertex  = "vertex"

	testViaDirect     = "direct"
	testViaOpenRouter = "openrouter"
	testViaAuto       = "auto"
	testViaCascade    = "cascade"
)

func claude(id string) Model { return Model{ID: id, Claude: true} }
func viaProvider(m Model, p string) Model {
	m.Provider = p
	return m
}

// TestMigrate is one case per value of `agent.auth` and of `routing.claude.via`,
// plus the two shapes the acceptance names: two repositories that differ, and
// two repositories whose accounts land in the one shared pool.
func TestMigrate(t *testing.T) {
	cases := []struct {
		name string
		in   Installation
		want Table
		// carried are the pools the installation's own configuration already
		// has, as the migration must write them: by name, with their members and
		// the rule their strategy means.
		carried []PoolSpec
		// bridge is the set of "repo model" pairs the migration leaves on the
		// release-1 bridge, as "repo\x00model".
		bridge []string
		// moves are the calls whose first reachable source the migration must
		// report, in the order it sorts them: repo, then model.
		moves []Move
	}{{
		name: "oauth",
		in: Installation{
			Project:  "acme/widgets",
			Accounts: []Account{{Name: "ada", AllowDataTo: []string{"acme/api"}}},
			Repos: []Repo{{
				Name:     "acme/api",
				Auth:     testAuthOAuth,
				Accounts: []string{"ada"},
				Models:   []Model{claude("claude-haiku-4-5")},
			}},
		},
		want: Table{{
			Match: "claude-haiku-4-5",
			Chain: []Source{{Kind: KindPool, Pool: SharedPool}},
		}},
	}, {
		name: "oauth with no account named goes out on the shared pool",
		in: Installation{
			Project:        "acme/widgets",
			Accounts:       []Account{{Name: "ada", AllowDataTo: []string{"acme/api"}}},
			DefaultAccount: "ada",
			Repos: []Repo{{
				Name:   "acme/api",
				Auth:   testAuthOAuth,
				Models: []Model{claude("claude-haiku-4-5")},
			}},
		},
		want: Table{{
			Match: "claude-haiku-4-5",
			Chain: []Source{{Kind: KindPool, Pool: SharedPool}},
		}},
	}, {
		name: "api-key",
		in: Installation{
			Project: "acme/widgets",
			Repos: []Repo{{
				Name:   "acme/api",
				Auth:   testAuthAPIKey,
				Models: []Model{claude("claude-haiku-4-5")},
			}},
		},
		want: Table{{
			Match: "claude-haiku-4-5",
			Chain: []Source{{Kind: KindAnthropic, Admit: []string{"acme/api"}}},
		}},
	}, {
		name: "api-key with the mix's account beside it",
		in: Installation{
			Project:  "acme/widgets",
			Accounts: []Account{{Name: "hopper", AllowDataTo: []string{"acme/api"}}},
			Repos: []Repo{{
				Name:     "acme/api",
				Auth:     testAuthAPIKey,
				Accounts: []string{"hopper"},
				Models:   []Model{claude("claude-haiku-4-5")},
			}},
		},
		want: Table{{
			Match: "claude-haiku-4-5",
			Chain: []Source{
				{Kind: KindPool, Pool: SharedPool},
				{Kind: KindAnthropic, Admit: []string{"acme/api"}},
			},
		}},
	}, {
		name: "cascade without fallback auto",
		in: Installation{
			Project:        "acme/widgets",
			Accounts:       []Account{{Name: "ada", AllowDataTo: []string{"acme/batch"}}},
			DefaultAccount: "ada",
			Repos: []Repo{{
				Name:   "acme/batch",
				Auth:   testAuthCascade,
				Models: []Model{claude("claude-haiku-4-5")},
			}},
		},
		want: Table{{
			Match: "claude-haiku-4-5",
			Chain: []Source{{Kind: KindPool, Pool: SharedPool}},
		}},
	}, {
		name: "cascade with fallback auto",
		in: Installation{
			Project:        "acme/widgets",
			Accounts:       []Account{{Name: "ada", AllowDataTo: []string{"acme/batch"}}},
			DefaultAccount: "ada",
			FallbackAuto:   true,
			Repos: []Repo{{
				Name:   "acme/batch",
				Auth:   testAuthCascade,
				Models: []Model{claude("claude-haiku-4-5")},
			}},
		},
		want: Table{{
			Match: "claude-haiku-4-5",
			Chain: []Source{
				{Kind: KindPool, Pool: SharedPool},
				{Kind: KindAnthropic, Admit: []string{"acme/batch"}},
			},
		}},
	}, {
		name: "vertex",
		in: Installation{
			Project:       "acme/widgets",
			VertexSpelled: []string{"gemini-3-pro"},
			Repos: []Repo{{
				Name:   "acme/vertex",
				Auth:   testAuthVertex,
				Models: []Model{{ID: "gemini-3-pro"}},
			}},
		},
		want: Table{{
			Match: "gemini-3-pro",
			Chain: []Source{{Kind: KindVertex, Admit: []string{"acme/vertex"}}},
		}},
	}, {
		name: "an absent auth reaches nothing, never Vertex (F49)",
		in: Installation{
			Project:       "acme/widgets",
			VertexSpelled: []string{"gemini-3-pro"},
			Repos: []Repo{{
				Name:   "acme/vertex",
				Models: []Model{{ID: "gemini-3-pro"}},
			}},
		},
	}, {
		name: "a Vertex model with no spelling reaches nothing, even where the file names an account",
		in: Installation{
			Project:        "acme/widgets",
			Accounts:       []Account{{Name: "ada"}},
			DefaultAccount: "ada",
			Repos: []Repo{{
				Name:     "acme/vertex",
				Auth:     testAuthVertex,
				Accounts: []string{"ada"},
				Models:   []Model{{ID: "gemini-9-ultra"}},
			}},
		},
		bridge: []string{"acme/vertex\x00gemini-9-ultra"},
	}, {
		name: "vertex with no checked-in spelling goes on the bridge",
		in: Installation{
			Project: "acme/widgets",
			Repos: []Repo{{
				Name:   "acme/vertex",
				Auth:   testAuthVertex,
				Models: []Model{{ID: "gemini-9-ultra"}},
			}},
		},
		bridge: []string{"acme/vertex\x00gemini-9-ultra"},
	}, {
		name: "via direct keeps a Claude model on its own credential",
		in: Installation{
			Project:  "acme/widgets",
			Accounts: []Account{{Name: "ada", AllowDataTo: []string{"acme/web"}}},
			Via:      testViaDirect,
			Providers: []Provider{
				{Name: "openrouter", AllowDataTo: []string{"*"}},
			},
			Repos: []Repo{{
				Name:     "acme/web",
				Auth:     testAuthOAuth,
				Accounts: []string{"ada"},
				Models:   []Model{viaProvider(claude("claude-sonnet-5-5"), "openrouter")},
			}},
		},
		want: Table{{
			Match: "claude-sonnet-5-5",
			Chain: []Source{{Kind: KindPool, Pool: SharedPool}},
		}},
	}, {
		name: "via openrouter leaves a Claude model through a provider on the bridge",
		in: Installation{
			Project:   "acme/widgets",
			Via:       testViaOpenRouter,
			ViaModels: []string{"claude-sonnet-5-5"},
			Providers: []Provider{
				{Name: "openrouter", AllowDataTo: []string{"*"}},
			},
			Repos: []Repo{{
				Name:   "acme/web",
				Auth:   testAuthAPIKey,
				Models: []Model{viaProvider(claude("claude-sonnet-5-5"), "openrouter")},
			}},
		},
		bridge: []string{"acme/web\x00claude-sonnet-5-5"},
	}, {
		name: "via auto leaves a Claude model through a provider on the bridge",
		in: Installation{
			Project:   "acme/widgets",
			Via:       testViaAuto,
			ViaModels: []string{"claude-sonnet-5-5"},
			Providers: []Provider{
				{Name: "openrouter", AllowDataTo: []string{"*"}},
			},
			Repos: []Repo{{
				Name:   "acme/web",
				Auth:   testAuthAPIKey,
				Models: []Model{viaProvider(claude("claude-sonnet-5-5"), "openrouter")},
			}},
		},
		bridge: []string{"acme/web\x00claude-sonnet-5-5"},
	}, {
		name: "via cascade changes the credential, never the route",
		in: Installation{
			Project:        "acme/widgets",
			Accounts:       []Account{{Name: "ada", AllowDataTo: []string{"acme/batch"}}},
			DefaultAccount: "ada",
			Via:            testViaCascade,
			ViaModels:      []string{"claude-sonnet-5-5"},
			Providers: []Provider{
				{Name: "openrouter", AllowDataTo: []string{"*"}},
			},
			Repos: []Repo{{
				Name:   "acme/batch",
				Auth:   testAuthCascade,
				Models: []Model{viaProvider(claude("claude-sonnet-5-5"), "openrouter")},
			}},
		},
		want: Table{{
			Match: "claude-sonnet-5-5",
			Chain: []Source{{Kind: KindPool, Pool: SharedPool}},
		}},
	}, {
		name: "a model the switch does not name keeps its own credential",
		in: Installation{
			Project:   "acme/widgets",
			Via:       testViaOpenRouter,
			ViaModels: []string{"claude-sonnet-5-5"},
			Providers: []Provider{
				{Name: "openrouter", AllowDataTo: []string{"*"}},
			},
			Repos: []Repo{{
				Name:   "acme/api",
				Auth:   testAuthAPIKey,
				Models: []Model{viaProvider(claude("claude-haiku-4-5"), "openrouter")},
			}},
		},
		want: Table{{
			Match: "claude-haiku-4-5",
			Chain: []Source{{Kind: KindAnthropic, Admit: []string{"acme/api"}}},
		}},
	}, {
		name: "a non-Claude model goes to the provider that claims it",
		in: Installation{
			Project:   "acme/widgets",
			Providers: []Provider{{Name: "dashscope", AllowDataTo: []string{"acme/api"}}},
			Repos: []Repo{{
				Name:   "acme/api",
				Auth:   testAuthAPIKey,
				Models: []Model{{ID: "deepseek-v4-flash", Provider: "dashscope"}},
			}},
		},
		want: Table{{
			Match: "deepseek-v4-flash",
			Chain: []Source{{Kind: KindProvider, Provider: "dashscope", Admit: []string{"acme/api"}}},
		}},
	}, {
		name: "a provider that does not admit a repository reaches nothing there",
		in: Installation{
			Project: "acme/widgets",
			Providers: []Provider{{
				Name:               "dashscope",
				AllowDataTo:        []string{"acme/other"},
				AllowDataToWritten: true,
			}},
			Repos: []Repo{{
				Name:   "acme/api",
				Auth:   testAuthAPIKey,
				Models: []Model{{ID: "deepseek-v4-flash", Provider: "dashscope"}},
			}},
		},
	}, {
		name: "a pool the installation carries keeps its name against an account's",
		in: Installation{
			Project: "acme/widgets",
			Accounts: []Account{
				{Name: "ada"},
				{Name: "grace", AllowDataTo: []string{"acme/web"}},
			},
			Pools: []Pool{{
				Name:      "grace",
				Members:   []string{"ada", "grace"},
				Strategy:  "round-robin",
				Principal: Principal{Kind: PrincipalProject, ID: "acme/widgets"},
			}},
			Repos: []Repo{{
				Name:     "acme/web",
				Auth:     testAuthOAuth,
				Accounts: []string{"grace"},
				Models:   []Model{claude("claude-haiku-4-5")},
			}},
		},
		want: Table{{
			Match: "claude-haiku-4-5",
			Chain: []Source{{Kind: KindPool, Pool: SharedPool}},
		}},
		carried: []PoolSpec{{
			Name:    "grace",
			Rule:    RuleRoundRobin,
			Members: []string{"ada", "grace"},
		}},
	}, {
		name: "two repositories that differ",
		in: Installation{
			Project:  "acme/widgets",
			Accounts: []Account{{Name: "ada", AllowDataTo: []string{"acme/api", "acme/web"}}},
			Providers: []Provider{{
				Name:        "dashscope",
				AllowDataTo: []string{"acme/api"},
			}},
			Repos: []Repo{{
				Name:     "acme/api",
				Auth:     testAuthAPIKey,
				Accounts: []string{"ada"},
				Models: []Model{
					claude("claude-haiku-4-5"),
					{ID: "deepseek-v4-flash", Provider: "dashscope"},
				},
			}, {
				Name:     "acme/web",
				Auth:     testAuthOAuth,
				Accounts: []string{"ada"},
				Models:   []Model{claude("claude-haiku-4-5")},
			}},
		},
		want: Table{{
			Match: "claude-haiku-4-5",
			Chain: []Source{
				{Kind: KindPool, Pool: SharedPool},
				{Kind: KindAnthropic, Admit: []string{"acme/api"}},
			},
		}, {
			Match: "deepseek-v4-flash",
			Chain: []Source{{Kind: KindProvider, Provider: "dashscope", Admit: []string{"acme/api"}}},
		}},
	}, {
		name: "two accounts that land in the one shared pool",
		in: Installation{
			Project: "acme/widgets",
			Accounts: []Account{
				{Name: "ada", AllowDataTo: []string{"acme/api"}},
				{Name: "grace", AllowDataTo: []string{"acme/web"}},
			},
			Repos: []Repo{{
				Name:     "acme/api",
				Auth:     testAuthOAuth,
				Accounts: []string{"ada"},
				Models:   []Model{claude("claude-haiku-4-5")},
			}, {
				Name:     "acme/web",
				Auth:     testAuthOAuth,
				Accounts: []string{"grace"},
				Models:   []Model{claude("claude-sonnet-5-5")},
			}},
		},
		want: Table{{
			Match: "claude-haiku-4-5",
			Chain: []Source{{Kind: KindPool, Pool: SharedPool}},
		}, {
			Match: "claude-sonnet-5-5",
			Chain: []Source{{Kind: KindPool, Pool: SharedPool}},
		}},
	}, {
		// The finding this case answers: an api-key repository that names no
		// account but that an account consents to — its allow_data_to lists it —
		// reaches the shared pool once the chain names it, because a pool's
		// admission is its members' own (D3182, R20). Its own configuration
		// sends it to the paid key, so the migration reports the call rather
		// than moving it quietly.
		name: "an account's consent moves a key's call onto the pool, and is reported",
		in: Installation{
			Project: "acme/widgets",
			Accounts: []Account{{
				Name:        "ada",
				AllowDataTo: []string{"acme/web", "acme/api"},
			}},
			DefaultAccount: "ada",
			Repos: []Repo{{
				Name:   "acme/api",
				Auth:   testAuthAPIKey,
				Models: []Model{claude("claude-haiku-4-5")},
			}, {
				Name:     "acme/web",
				Auth:     testAuthOAuth,
				Accounts: []string{"ada"},
				Models:   []Model{claude("claude-haiku-4-5")},
			}},
		},
		want: Table{{
			Match: "claude-haiku-4-5",
			Chain: []Source{
				{Kind: KindPool, Pool: SharedPool},
				{Kind: KindAnthropic, Admit: []string{"acme/api"}},
			},
		}},
		moves: []Move{{
			Repo:  "acme/api",
			Model: "claude-haiku-4-5",
			From:  SourceName{Kind: KindAnthropic},
			To:    SourceName{Kind: KindPool, Name: SharedPool},
		}},
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Migrate(tc.in)
			if len(got.Table) != len(tc.want) {
				t.Fatalf("the table has %d entries, want %d: %+v", len(got.Table), len(tc.want), got.Table)
			}
			for i, entry := range tc.want {
				if got.Table[i].Match != entry.Match {
					t.Errorf("entry %d matches %q, want %q", i, got.Table[i].Match, entry.Match)
				}
				if !slices.EqualFunc(got.Table[i].Chain, entry.Chain, sameSource) {
					t.Errorf("entry %q has chain %+v, want %+v", entry.Match, got.Table[i].Chain, entry.Chain)
				}
			}
			var bridged []string
			for _, b := range got.Bridge {
				bridged = append(bridged, b.Repo+"\x00"+b.Model)
			}
			slices.Sort(bridged)
			if !slices.Equal(bridged, tc.bridge) {
				t.Errorf("the bridge holds %v, want %v", bridged, tc.bridge)
			}
			// Nothing moves silently: a call the table sends somewhere else than
			// the repository's own configuration does is reported by name, and a
			// call it keeps where it was is not.
			if !slices.Equal(got.Moves, tc.moves) {
				t.Errorf("the migration reports the moves %+v, want %+v", got.Moves, tc.moves)
			}
			// The shared pool holds every account in the installation's own order
			// (D3183), and it is written only where there is one to put in it: a
			// pool with no members is a shape the config refuses.
			if want := accountsOf(tc.in); len(want) > 0 {
				if !slices.Equal(members(t, got, SharedPool), want) {
					t.Errorf("the shared pool holds %v, want every account in the installation's own order: %v", members(t, got, SharedPool), want)
				}
			} else if slices.ContainsFunc(got.Pools, func(p PoolSpec) bool { return p.Name == SharedPool }) {
				t.Errorf("an installation with no account has no shared pool to write, and this one was written: %+v", got.Pools)
			}
			for _, want := range tc.carried {
				if p := pool(t, got, want.Name); !slices.Equal(p.Members, want.Members) || p.Rule != want.Rule {
					t.Errorf("the pool %s the installation carries is %+v, want %+v", want.Name, p, want)
				}
			}
			for _, a := range accountsOf(tc.in) {
				// A pool the installation carries under an account's own name keeps
				// it: the owner's pool is what that name means, and the pool of one
				// is this migration's own shape (D3176).
				if slices.ContainsFunc(tc.carried, func(p PoolSpec) bool { return p.Name == a }) {
					continue
				}
				if p := pool(t, got, a); !slices.Equal(p.Members, []string{a}) || p.Rule != RuleFailover {
					t.Errorf("the pool of one for %s is %+v, want one member served by failover (D3176)", a, p)
				}
			}
		})
	}
}

// accountsOf is every account an installation's configuration names, in the
// order the migration collects them: the installation's own, the default one,
// and the ones a repository's file names.
func accountsOf(in Installation) []string {
	var out []string
	for _, a := range in.Accounts {
		out = appendUnique(out, a.Name)
	}
	out = appendUnique(out, in.DefaultAccount)
	for _, r := range in.Repos {
		out = appendUnique(out, r.Accounts...)
	}
	return out
}

// sameSource compares two sources as the table carries them: the admission list
// is a set, so the order two repositories were admitted in is not part of the
// answer.
func sameSource(a, b Source) bool {
	if !sameCredential(a, b) {
		return false
	}
	x, y := slices.Clone(a.Admit), slices.Clone(b.Admit)
	slices.Sort(x)
	slices.Sort(y)
	return slices.Equal(x, y)
}

// sameCredential compares the credential two sources name. An admission list is
// the set of repositories one source serves, and one entry is shared by every
// repository that calls its model, so the list is a union rather than one
// repository's own admission.
func sameCredential(a, b Source) bool {
	return a.Kind == b.Kind && a.Pool == b.Pool && a.Provider == b.Provider && a.Model == b.Model
}

func members(t *testing.T, m Migration, pool string) []string {
	t.Helper()
	for _, p := range m.Pools {
		if p.Name == pool {
			return p.Members
		}
	}
	t.Fatalf("the migration wrote no pool %q: %+v", pool, m.Pools)
	return nil
}

// TestMigrateMovesNoRepository runs the migration over a whole installation
// under testdata/migrate — an installation's own config and five repository
// files, one per retired auth mode and the two-account shape — and checks the
// claim the migration exists for: every repository still goes out on the
// credential its own configuration gives it.
func TestMigrateMovesNoRepository(t *testing.T) {
	in := readFixture(t)
	got := Migrate(in)

	// What each repository goes out on, as its own configuration says today:
	// the shared pool its accounts are members of, the Anthropic key, or the
	// provider key that claims the model. Which member of the shared pool
	// serves a repository is that member's own allow_data_to (R20), which the
	// migration neither reads nor writes.
	want := map[string]Source{
		"acme/api\x00claude-haiku-4-5":       {Kind: KindAnthropic, Admit: []string{"acme/api"}},
		"acme/api\x00deepseek-v4-flash":      {Kind: KindProvider, Provider: "dashscope", Admit: []string{"acme/api"}},
		"acme/web\x00claude-haiku-4-5":       {Kind: KindPool, Pool: SharedPool},
		"acme/batch\x00claude-haiku-4-5":     {Kind: KindPool, Pool: SharedPool},
		"acme/mixed\x00claude-haiku-4-5":     {Kind: KindPool, Pool: SharedPool},
		"acme/mixed-key\x00claude-haiku-4-5": {Kind: KindPool, Pool: SharedPool},
		"acme/vertex\x00gemini-3-pro":        {Kind: KindVertex, Admit: []string{"acme/vertex"}},
	}
	bridged := map[string]bool{
		// The switch names it and a provider claims it: D3196 keeps it on the
		// release-1 bridge until Check 50.
		"acme/web\x00claude-sonnet-5-5": true,
		// The checked-in map spells no Vertex source for it.
		"acme/vertex\x00gemini-9-ultra": true,
	}
	for _, b := range got.Bridge {
		if !bridged[b.Repo+"\x00"+b.Model] {
			t.Errorf("%s of %s is on the bridge and this test does not expect it to be", b.Model, b.Repo)
		}
	}

	for _, repo := range in.Repos {
		for _, model := range repo.Models {
			key := repo.Name + "\x00" + model.ID
			if bridged[key] {
				if !slices.ContainsFunc(got.Bridge, func(b BridgeEntry) bool { return b.Repo == repo.Name && b.Model == model.ID }) {
					t.Errorf("%s of %s is not on the bridge and this test expects it to be", model.ID, repo.Name)
				}
				continue
			}
			entry, ok := entryFor(got.Table, model.ID)
			if !ok {
				t.Errorf("%s of %s has no entry: the run that calls it is refused", model.ID, repo.Name)
				continue
			}
			src, ok := firstReaching(entry, repo, in, got.Pools)
			if !ok {
				t.Errorf("%s of %s reaches no source of its chain %+v", model.ID, repo.Name, entry.Chain)
				continue
			}
			if !sameCredential(src, want[key]) {
				t.Errorf("%s of %s goes out on %+v, want %+v", model.ID, repo.Name, src, want[key])
			}
		}
	}

	// And nothing moves: the accounts consent to the repositories that go out
	// on them and to no other, so no call's first reachable source changes. A
	// consent that named acme/api — the key's repository — would move that call
	// onto the pool, and the migration has to report it rather than let it
	// happen quietly (D3182, R20).
	if len(got.Moves) != 0 {
		t.Errorf("the migration reports the moves %+v, and this fixture's accounts consent to no repository that does not go out on them", got.Moves)
	}

	// The pools: the shared pool holds every account in the installation's own
	// order (D3183), every account has a pool of one (D3176), and a pool the
	// installation carries is kept with the rule its strategy means (R21).
	if want := []string{"ada", "grace", "hopper"}; !slices.Equal(members(t, got, SharedPool), want) {
		t.Errorf("the shared pool holds %v, want %v", members(t, got, SharedPool), want)
	}
	if p := pool(t, got, "grace"); !slices.Equal(p.Members, []string{"grace"}) || p.Rule != RuleFailover {
		t.Errorf("the pool of one for grace is %+v, want one member served by failover", p)
	}
	if p := pool(t, got, "pair"); !slices.Equal(p.Members, []string{"ada", "grace"}) || p.Rule != RuleRoundRobin {
		t.Errorf("the carried pool pair is %+v, want its own members served by its strategy's rule", p)
	}
	// D3177: the spread switch is off, the shared pool is what the cascade
	// already rotates (acme/batch is on it), and pair rotates once migrated.
	if want := []string{"pair"}; !slices.Equal(got.Inert, want) {
		t.Errorf("the inert pools are %v, want %v", got.Inert, want)
	}
}

func pool(t *testing.T, m Migration, name string) PoolSpec {
	t.Helper()
	for _, p := range m.Pools {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("the migration wrote no pool %q: %+v", name, m.Pools)
	return PoolSpec{}
}

// firstReaching is the first source of a chain a repository's runs reach, as
// the contract decides it: a single-credential source admits a repository by
// name (D3182), and a pool carries no admission of its own — its members'
// allow_data_to decides (D3182, R20), so this test has to read the accounts to
// see the move a pool makes. A pool whose members consent to the repository
// takes it; a source nothing admits it to is skipped.
func firstReaching(entry Entry, repo Repo, in Installation, pools []PoolSpec) (Source, bool) {
	for _, s := range entry.Chain {
		switch s.Kind {
		case KindPool:
			if poolReaches(s.Pool, repo.Name, in, pools) {
				return s, true
			}
		case KindAnthropic, KindProvider, KindVertex:
			if slices.Contains(s.Admit, repo.Name) {
				return s, true
			}
		}
	}
	return Source{}, false
}

// poolReaches is the pool's admission: a member consents to a repository
// through its own allow_data_to, and an account the installation's config does
// not carry consents to nobody (config.ClaudeAccount.AllowsData).
func poolReaches(pool, repo string, in Installation, pools []PoolSpec) bool {
	for _, p := range pools {
		if p.Name != pool {
			continue
		}
		for _, member := range p.Members {
			for _, a := range in.Accounts {
				if a.Name == member && a.allowsData(repo) {
					return true
				}
			}
		}
	}
	return false
}

// The fixture's own shapes: testdata/migrate is an installation's config and a
// repository's file each, written the way a person writes them, and the
// migration's input types are deliberately not a format anything decodes — so
// the test carries the tags and the translation.
type fx struct {
	Project            string       `yaml:"project"`
	Accounts           []fxAccount  `yaml:"accounts"`
	DefaultAccount     string       `yaml:"default_account"`
	Providers          []fxProvider `yaml:"providers"`
	Pools              []fxPool     `yaml:"pools"`
	SubscriptionSpread bool         `yaml:"subscription_spread"`
	FallbackAuto       bool         `yaml:"fallback_auto"`
	Via                string       `yaml:"via"`
	ViaModels          []string     `yaml:"via_models"`
	VertexSpelled      []string     `yaml:"vertex_spelled"`
}

type fxAccount struct {
	Name        string   `yaml:"name"`
	AllowDataTo []string `yaml:"allow_data_to"`
}

type fxProvider struct {
	Name               string   `yaml:"name"`
	AllowDataTo        []string `yaml:"allow_data_to"`
	AllowDataToWritten bool     `yaml:"allow_data_to_written"`
}

type fxPool struct {
	Name      string      `yaml:"name"`
	Members   []string    `yaml:"members"`
	Strategy  string      `yaml:"strategy"`
	Principal fxPrincipal `yaml:"principal"`
	Cascade   bool        `yaml:"cascade"`
}

type fxPrincipal struct {
	Kind string `yaml:"kind"`
	ID   string `yaml:"id"`
}

type fxRepo struct {
	Auth     string    `yaml:"auth"`
	Accounts []string  `yaml:"accounts"`
	Models   []fxModel `yaml:"models"`
}

type fxModel struct {
	ID       string `yaml:"id"`
	Claude   bool   `yaml:"claude"`
	Provider string `yaml:"provider"`
}

// readFixture reads testdata/migrate: the installation's own config and one
// file per repository, whose name is the repository's own.
func readFixture(t *testing.T) Installation {
	t.Helper()
	dir := filepath.Join("testdata", "migrate")
	var f fx
	readYAML(t, filepath.Join(dir, "installation.yaml"), &f)
	in := Installation{
		Project:            f.Project,
		DefaultAccount:     f.DefaultAccount,
		SubscriptionSpread: f.SubscriptionSpread,
		FallbackAuto:       f.FallbackAuto,
		Via:                f.Via,
		ViaModels:          f.ViaModels,
		VertexSpelled:      f.VertexSpelled,
	}
	for _, a := range f.Accounts {
		in.Accounts = append(in.Accounts, Account{Name: a.Name, AllowDataTo: a.AllowDataTo})
	}
	for _, p := range f.Providers {
		in.Providers = append(in.Providers, Provider{
			Name:               p.Name,
			AllowDataTo:        p.AllowDataTo,
			AllowDataToWritten: p.AllowDataToWritten,
		})
	}
	for _, p := range f.Pools {
		in.Pools = append(in.Pools, Pool{
			Name:      p.Name,
			Members:   p.Members,
			Strategy:  p.Strategy,
			Principal: Principal{Kind: PrincipalKind(p.Principal.Kind), ID: p.Principal.ID},
			Cascade:   p.Cascade,
		})
	}
	names, err := filepath.Glob(filepath.Join(dir, "repos", "*", "*.yaml"))
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	if len(names) == 0 {
		t.Fatalf("%s holds no repository file", dir)
	}
	slices.Sort(names)
	for _, name := range names {
		var r fxRepo
		readYAML(t, name, &r)
		repo := Repo{
			// The file's own path is the repository's name: owner/name, one
			// directory per owner, as a repository is named everywhere else.
			Name:     filepath.ToSlash(filepath.Join(filepath.Base(filepath.Dir(name)), strings.TrimSuffix(filepath.Base(name), ".yaml"))),
			Auth:     r.Auth,
			Accounts: r.Accounts,
		}
		for _, m := range r.Models {
			repo.Models = append(repo.Models, Model{ID: m.ID, Claude: m.Claude, Provider: m.Provider})
		}
		in.Repos = append(in.Repos, repo)
	}
	return in
}

func readYAML(t *testing.T, path string, into any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(into); err != nil {
		t.Fatalf("decoding %s: %v", path, err)
	}
}

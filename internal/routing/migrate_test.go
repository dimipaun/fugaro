package routing

import (
	"fmt"
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
		// The mix's account beside an api-key repository's key (D1848): the
		// account serves the senior review, the adversarial review and the triage,
		// and the key every other stage — so the key is the credential this call
		// has (its auth), and the pool the account is folded into is where the
		// whole call goes after the migration. That is a move, and the migration
		// reports it rather than taking it quietly.
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
		moves: []Move{{
			Repo:  "acme/api",
			Model: "claude-haiku-4-5",
			From:  SourceName{Kind: KindAnthropic},
			To:    SourceName{Kind: KindPool, Name: SharedPool},
		}},
	}, {
		// The other side of the pair above, and the reason the key is what the
		// migration reads for an api-key repository: an account the file names
		// and that consents to nobody does not put the call on the pool — a pool
		// reaches a repository through its members' own allow_data_to alone
		// (D3182, R20) — so the call stays on the key and there is no move to
		// report. Reading the account as the credential rather than the key would
		// report one that does not happen.
		name: "an account that consents to nobody leaves a key's call where it is",
		in: Installation{
			Project:  "acme/widgets",
			Accounts: []Account{{Name: "hopper"}},
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
		// A pool the installation's own configuration holds is written back as
		// that configuration holds it, with only its rule resolved: its name wins
		// over an account's of the same name (D3176), its members keep their
		// owner's order, and the members a person wrote beside them — the owner,
		// the users it is served to besides them (D1936, R10) and the principal,
		// which is the identity the pool belongs to and never display text (R7,
		// R21) — are carried through rather than dropped. A pool a person wrote is
		// a person's, so the migration states no principal of its own here: it
		// writes back the one the config states, and none where it states none.
		name: "a pool the installation carries keeps its name against an account's",
		in: Installation{
			Project: "acme/widgets",
			Accounts: []Account{
				{Name: "ada"},
				{Name: "grace", AllowDataTo: []string{"acme/web"}},
			},
			Pools: []Pool{{
				Name:         "grace",
				Members:      []string{"ada", "grace"},
				Strategy:     "round-robin",
				Owner:        "ada@example.test",
				AllowedUsers: []string{"grace@example.test"},
				Principal:    Principal{Kind: PrincipalPerson, ID: "ada-subject"},
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
			Name:         "grace",
			Rule:         RuleRoundRobin,
			Members:      []string{"ada", "grace"},
			Owner:        "ada@example.test",
			AllowedUsers: []string{"grace@example.test"},
			Principal:    &Principal{Kind: PrincipalPerson, ID: "ada-subject"},
		}},
	}, {
		// The other side of that: the migration states no principal for a pool
		// whose config states none, which is every pool written before PL1 added
		// the member, and a half-stated one (a kind and no id) is no statement —
		// Principal.Valid refuses it, and writing it back would put a pool nothing
		// can resolve in the config. The legacy marker is carried too: a pool
		// written before pools carried an owner keeps saying so (D1942, F37).
		name: "a carried pool whose config states no principal is written with none",
		in: Installation{
			Project:  "acme/widgets",
			Accounts: []Account{{Name: "ada"}},
			Pools: []Pool{{
				Name:       "older",
				Members:    []string{"ada"},
				LegacyOpen: true,
			}, {
				Name:      "half",
				Members:   []string{"ada"},
				Principal: Principal{Kind: PrincipalPerson},
			}},
		},
		carried: []PoolSpec{{
			Name:       "older",
			Rule:       RuleFailover,
			Members:    []string{"ada"},
			LegacyOpen: true,
		}, {
			Name:    "half",
			Rule:    RuleFailover,
			Members: []string{"ada"},
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
		// acme/api calls its model on the key while ada, whose pool the entry
		// names for acme/web, consents to acme/api too: the call reaches the pool
		// after the migration where it reached the key before, and the migration
		// reports the pair rather than moving it quietly.
		moves: []Move{{
			Repo:  "acme/api",
			Model: "claude-haiku-4-5",
			From:  SourceName{Kind: KindAnthropic},
			To:    SourceName{Kind: KindPool, Name: SharedPool},
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
	}, {
		// A call the migration leaves on the release-1 bridge is not a move,
		// whatever the entry another repository's call of the same model
		// carries: the bridge is what serves it, and MG2 reports it by the
		// bridge entry's own name (D3196). Without that skip, the pool
		// acme/vertex's account consents to — acme/web's call is what names the
		// pool in this entry — would look like the Vertex repository's new
		// source, where its own configuration reaches Vertex, a source the table
		// holds for no unspelled model.
		name: "a bridged call is not a move, even where the pool consents to its repository",
		in: Installation{
			Project: "acme/widgets",
			Accounts: []Account{{
				Name:        "ada",
				AllowDataTo: []string{"acme/vertex", "acme/web"},
			}},
			DefaultAccount: "ada",
			Repos: []Repo{{
				Name:   "acme/vertex",
				Auth:   testAuthVertex,
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
			Chain: []Source{{Kind: KindPool, Pool: SharedPool}},
		}},
		bridge: []string{"acme/vertex\x00claude-haiku-4-5"},
	}, {
		// The shared pool's members are Claude subscriptions, so a chain names
		// the pool for a Claude model alone: an account a Vertex repository
		// names beside its own auth is not what serves a Vertex model, and a
		// chain naming the pool for one would send that call out on the
		// subscription the bridge above exists to keep it off.
		name: "an account beside a Vertex repository does not put its model on the pool",
		in: Installation{
			Project:        "acme/widgets",
			Accounts:       []Account{{Name: "ada", AllowDataTo: []string{"acme/vertex"}}},
			DefaultAccount: "ada",
			VertexSpelled:  []string{"gemini-3-pro"},
			Repos: []Repo{{
				Name:     "acme/vertex",
				Auth:     testAuthVertex,
				Accounts: []string{"ada"},
				Models:   []Model{{ID: "gemini-3-pro"}},
			}},
		},
		want: Table{{
			Match: "gemini-3-pro",
			Chain: []Source{{Kind: KindVertex, Admit: []string{"acme/vertex"}}},
		}},
	}, {
		// A craft or a grade that goes out on a credential of its own (D3062)
		// cannot be written: the table has one chain per model and none per craft
		// (D3171), and the credential such a craft names is not the repository's
		// own. The call stays on the release-1 bridge, which serves the craft what
		// it has today, where the entry below would otherwise move it onto the
		// pool acme/api's account consents to.
		name: "a craft that goes out on Vertex leaves the call on the release-1 bridge",
		in: Installation{
			Project: "acme/widgets",
			Accounts: []Account{{
				Name:        "ada",
				AllowDataTo: []string{"acme/api", "acme/web"},
			}},
			DefaultAccount: "ada",
			Repos: []Repo{{
				Name:     "acme/api",
				Auth:     testAuthAPIKey,
				Accesses: []string{"vertex"},
				Models:   []Model{claude("claude-haiku-4-5")},
			}, {
				Name:     "acme/web",
				Auth:     testAuthOAuth,
				Accounts: []string{"ada"},
				Models:   []Model{claude("claude-haiku-4-5")},
			}},
		},
		want: Table{{
			Match: "claude-haiku-4-5",
			Chain: []Source{{Kind: KindPool, Pool: SharedPool}},
		}},
		bridge: []string{"acme/api\x00claude-haiku-4-5"},
	}, {
		// The boundary of the rule above: a craft's access that names the auth
		// the repository already answers is not a credential of its own, and the
		// call goes to the table like any other.
		name: "a craft that names the repository's own auth adds nothing",
		in: Installation{
			Project: "acme/widgets",
			Repos: []Repo{{
				Name:     "acme/api",
				Auth:     testAuthAPIKey,
				Accesses: []string{"api-key"},
				Models:   []Model{claude("claude-haiku-4-5")},
			}},
		},
		want: Table{{
			Match: "claude-haiku-4-5",
			Chain: []Source{{Kind: KindAnthropic, Admit: []string{"acme/api"}}},
		}},
	}, {
		// The rule above holds for a model a provider claims as well, and this is
		// the repository whose craft goes out on another provider's key: the craft
		// reaches openrouter where the call would be written as dashscope's, so
		// writing the entry from the provider's branch would change the credential
		// that craft goes out on with neither a bridge entry nor a move. The whole
		// call stays on the release-1 bridge, dashscope and all.
		name: "a craft that goes out on another provider leaves a claimed model on the bridge",
		in: Installation{
			Project:  "acme/widgets",
			Accounts: []Account{{Name: "ada", AllowDataTo: []string{"acme/web"}}},
			Providers: []Provider{{
				Name:        "dashscope",
				AllowDataTo: []string{"acme/web"},
			}},
			Repos: []Repo{{
				Name:     "acme/web",
				Auth:     testAuthOAuth,
				Accesses: []string{"openrouter"},
				Models:   []Model{viaProvider(Model{ID: "gpt-5"}, "dashscope")},
			}},
		},
		bridge: []string{"acme/web\x00gpt-5"},
	}, {
		// One call, two lines: a repository's file names every model it calls by
		// `agent.model`, `agent.models.*` and `agent.crafts.*.model`, and two of
		// them can name the same one. A call is one pair of repository and model
		// however many lines name it, so it is one move and one line MG2 prints,
		// not two identical ones.
		name: "a call two lines name is one move",
		in: Installation{
			Project:  "acme/widgets",
			Accounts: []Account{{Name: "ada", AllowDataTo: []string{"acme/api"}}},
			Repos: []Repo{{
				Name:     "acme/api",
				Auth:     testAuthAPIKey,
				Accounts: []string{"ada"},
				Models:   []Model{claude("claude-haiku-4-5"), claude("claude-haiku-4-5")},
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
	}, {
		// The guard on the shared pool (m.shared): an installation with no
		// account at all writes no pool, because the config refuses a pool with
		// no members, and no chain may name one either — a table naming a pool
		// the config does not hold is refused whole. This repository is oauth,
		// so without the guard its chain would name the cascade pool that was
		// never written; as it is, it reaches nothing, which is what it reaches
		// today.
		name: "an oauth repository of an installation with no account reaches nothing",
		in: Installation{
			Project: "acme/widgets",
			Repos: []Repo{{
				Name:   "acme/api",
				Auth:   testAuthOAuth,
				Models: []Model{claude("claude-haiku-4-5")},
			}},
		},
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
				if p := pool(t, got, want.Name); !samePool(p, want) {
					t.Errorf("the pool %s the installation carries is %s, want %s", want.Name, poolString(p), poolString(want))
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
					t.Errorf("the pool of one for %s is %s, want one member served by failover (D3176)", a, poolString(p))
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

// poolString renders a pool for a failure message: the principal's own kind and
// id rather than the address of the one the migration returned.
func poolString(p PoolSpec) string {
	principal := "no principal"
	if p.Principal != nil {
		principal = string(p.Principal.Kind) + " " + p.Principal.ID
	}
	return fmt.Sprintf("{name:%s rule:%s principal:%s members:%v owner:%s allowed_users:%v legacy_open:%t}",
		p.Name, p.Rule, principal, p.Members, p.Owner, p.AllowedUsers, p.LegacyOpen)
}

// samePool compares two pools as the migration writes them: the members are a
// list — a pool's order is its owner's — and the principal is the pool's own,
// where one is written at all (a nil and a stated principal are different
// answers, not a missing value).
func samePool(a, b PoolSpec) bool {
	if a.Name != b.Name || a.Rule != b.Rule || a.Owner != b.Owner || a.LegacyOpen != b.LegacyOpen {
		return false
	}
	if !slices.Equal(a.Members, b.Members) || !slices.Equal(a.AllowedUsers, b.AllowedUsers) {
		return false
	}
	switch {
	case a.Principal == nil || b.Principal == nil:
		return a.Principal == nil && b.Principal == nil
	default:
		return *a.Principal == *b.Principal
	}
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
// under testdata/migrate — an installation's own config and a repository file
// each, one per retired auth mode and the shapes beside it — and checks the
// claim the migration exists for: every repository still goes out on the
// credential its own configuration gives it, and the calls an account's own
// consent moves are reported rather than taken quietly.
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
		// A craft goes out on Vertex while the repository's own auth is the key:
		// one chain per model holds no credential per craft (D3171), so the call
		// keeps the release-1 walk rather than moving the craft onto the key.
		"acme/crafted\x00claude-haiku-4-5": true,
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

	// And exactly one call moves, and it is reported: acme/mixed-key goes out on
	// the key, and hopper — the mix's account beside it (D1848), which serves the
	// senior review, the adversarial review and the triage alone — consents to
	// acme/mixed-key, so folding hopper into the shared pool puts that whole call
	// on the pool. The migration names the pair rather than taking the move
	// quietly, and the consent is a person's to change (D3182, R20). Every other
	// repository keeps the source its own configuration gives it, which the loop
	// above has checked one by one.
	if want := []Move{{
		Repo:  "acme/mixed-key",
		Model: "claude-haiku-4-5",
		From:  SourceName{Kind: KindAnthropic},
		To:    SourceName{Kind: KindPool, Name: SharedPool},
	}}; !slices.Equal(got.Moves, want) {
		t.Errorf("the migration reports the moves %+v, want %+v: the accounts' consent to a repository on another credential is a move, and the migration names it", got.Moves, want)
	}

	// The pools: the shared pool holds every account in the installation's own
	// order (D3183), every account has a pool of one (D3176), and a pool the
	// installation carries is kept with the rule its strategy means (R21).
	if want := []string{"ada", "grace", "hopper"}; !slices.Equal(members(t, got, SharedPool), want) {
		t.Errorf("the shared pool holds %v, want %v", members(t, got, SharedPool), want)
	}
	if p := pool(t, got, "grace"); !slices.Equal(p.Members, []string{"grace"}) || p.Rule != RuleFailover {
		t.Errorf("the pool of one for grace is %s, want one member served by failover", poolString(p))
	}
	if p, want := pool(t, got, "pair"), (PoolSpec{
		Name:         "pair",
		Rule:         RuleRoundRobin,
		Members:      []string{"ada", "grace"},
		Owner:        "ada@example.test",
		AllowedUsers: []string{"grace@example.test"},
	}); !samePool(p, want) {
		t.Errorf("the carried pool pair is %s, want %s: its own members, served by its strategy's rule, with its owner and the users beside them", poolString(p), poolString(want))
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
	Name         string      `yaml:"name"`
	Members      []string    `yaml:"members"`
	Strategy     string      `yaml:"strategy"`
	Owner        string      `yaml:"owner"`
	AllowedUsers []string    `yaml:"allowed_users"`
	LegacyOpen   bool        `yaml:"legacy_open"`
	Principal    fxPrincipal `yaml:"principal"`
	Cascade      bool        `yaml:"cascade"`
}

type fxPrincipal struct {
	Kind string `yaml:"kind"`
	ID   string `yaml:"id"`
}

type fxRepo struct {
	Auth     string    `yaml:"auth"`
	Accounts []string  `yaml:"accounts"`
	Accesses []string  `yaml:"accesses"`
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
			Name:         p.Name,
			Members:      p.Members,
			Strategy:     p.Strategy,
			Owner:        p.Owner,
			AllowedUsers: p.AllowedUsers,
			LegacyOpen:   p.LegacyOpen,
			Principal:    Principal{Kind: PrincipalKind(p.Principal.Kind), ID: p.Principal.ID},
			Cascade:      p.Cascade,
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
			Accesses: r.Accesses,
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

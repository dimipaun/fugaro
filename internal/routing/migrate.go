package routing

import (
	"slices"
	"sort"
	"strings"
)

// MG1: the migration, as a function.
//
// An installation upgrades from the world where a run's credential came from
// `agent.auth` and `routing.claude.via` to the world where it comes from this
// project's table. Migrate is that translation, and it is a pure function on
// purpose: the same configuration always answers the same table, a test can
// hold the whole of it, and nothing about it depends on a cloud, a database or
// a clock. MG2 is its caller — the side that reads the installation's own
// config and each repository's file, translates them into the shapes below and
// writes what this returns into the project's `model_routing` key and the
// pools the config carries.
//
// # Why the input is this package's own vocabulary
//
// This package imports no package of this tree (the pools contract's reason:
// internal/config is about to import it, PL1), so Migrate cannot be handed a
// config.Config or a localcfg.Repo. The shapes below are therefore the
// migration's own reading of those — one field per fact the migration acts on,
// named as the config names it — and the caller translates. That translation is
// where `IsClaudeModel`, `ProviderFor` and the retired account walk live, and
// it is deliberately the caller's: a migration that re-derived them would be a
// second answer to questions internal/config already answers.
//
// # What "keeps every run where it was" means
//
// The table is one document for the whole project, while the configuration it
// replaces was per repository, per stage and per grade. Two mechanisms carry
// that difference, and both are the contract's rather than this file's:
//
//   - a single-credential source — the Anthropic key, a provider's key, Vertex
//     — carries the repositories admitted to it (Source.Admit, D3182), so a
//     repository reaches the key it used and no other;
//   - a pool carries no admission list at all: its members' own `allow_data_to`
//     decides, and a member a repository is not admitted to is skipped
//     (D3182, R20). That is why the accounts a repository goes out on can share
//     one pool with accounts it may not use and still not move.
//
// The shared pool is what every subscription chain names, `oauth` and `cascade`
// alike: D3183 answers whose pools the existing accounts become — the shared
// pool, same members and order — and one chain per model cannot hold a pool per
// repository, per stage or per grade. So "`oauth` becomes the account's pool"
// reads as the pool the account became, which is the shared pool, and the
// per-account pool of one D3176 writes is not in any chain: it is what
// `--pool <account>` resolves "the launcher's pool" to, and what a person
// designates for their own runs later. A reading that put each account's pool
// of one in the chain instead would leave a model two repositories call with
// two pools whose order decides which of them serves which — the move this
// migration exists to prevent — while the cascade's rotation (D3177) would be
// lost to the pool of one that came first.
//
// The retired vocabulary this reproduces, value by value, is
// internal/craft/legacy.go's: `oauth` resolved its account by walking
// `stages.<stage>.claude_account` → `agent.oauth_token` →
// `default_claude_account` → the unsuffixed `default`, `api-key` used the mix
// account for review, adversarial and triage and the Anthropic key everywhere
// else, and `cascade` went to the cascade. Per-stage accounts and the mix are
// retired (D3171): their accounts are members of the pools below, which is what
// folding them into one chain means once the table has one chain per model. A
// file that writes no auth at all is the state that rule answers nothing for and
// never Vertex (F49), and this migration reproduces it: an empty Auth reaches
// nothing rather than the platform's own model, which is the answer the config
// layer's default would give it.
//
// # What the caller must do with the answer
//
// Write Table into the project's `model_routing` key and the pools into the
// config, and nothing else: the admissions are on the sources, which is where
// RT4 publishes them, so the table returned here is the whole of that half.
// Bridge is the part that could NOT be written — an entry RT3 refuses today —
// and it stays on the release-1 bridge by name (D3196); Inert is what MG2
// prints once, per pool, because those pools start rotating when the switch is
// gone (D3177).
//
// Moves is the one part of the answer that is not a document to write but a
// thing to say: the calls this table sends somewhere else than the
// configuration does today. A pool's admission is its members' own
// `allow_data_to` (D3182, R20) — an account's consent, which this migration
// reads and does not write — so a repository the accounts consent to and whose
// own configuration never used them reaches the pool after the migration where
// it reached a key before. That is the move "nothing moves on upgrade" forbids,
// and the answer to it is that MG2 names it: the migration reports the pair,
// the source it had and the source it gets, and the consent behind it is a
// person's to change.

// SharedPool is the name of the pool every account of the installation becomes
// a member of (D3183): the pool a subscription chain names, the pool the
// cascade goes out on, and the one a contributed token joins (D3194).
//
// It is the string config.PoolCascade carries, spelled here because this
// package imports no package of this tree; MG2's own test is what holds the two
// spellings to each other, because it is the only side that can see both.
const SharedPool = "cascade"

// The retired vocabulary, as this package reads it. Each is one value of
// config.AgentAuths, config.Route* or config.DefaultAgentAuth, spelled here for
// the same reason as SharedPool: the caller passes the config's own string and
// the migration compares it with these.
const (
	authVertex  = "vertex"
	authAPIKey  = "api-key"
	authOAuth   = "oauth"
	authCascade = "cascade"

	viaOpenRouter = "openrouter"
	viaAuto       = "auto"
)

// The reasons a configuration cannot be written as a table RT3 accepts (D3196).
// They are the answer's own words rather than free text, so MG2's report and
// its convergence line name the same cause the same way.
const (
	// BridgeClaudeThroughProvider is a Claude model a repository reaches
	// through a provider key: `routing.claude.via` with an open value, the
	// model named by the switch, and a provider entry claiming it. It is the
	// route D1a added, and Check 50 is what makes a table entry for it
	// acceptable — until it lands, such an entry is refused, so it stays on
	// the release-1 bridge.
	BridgeClaudeThroughProvider = "claude-through-provider"
	// BridgeVertexUnspelled is a model a Vertex repository calls that the
	// checked-in map spells for no Vertex source (D3175). The table sends what
	// the map spells and nothing else, so an unspelled model cannot be written
	// as an entry RT3 accepts; it stays on the release-1 bridge, which is what
	// sends the call the way the repository's configuration does today.
	BridgeVertexUnspelled = "vertex-unspelled"
	// BridgeCraftAccess is a call one of whose crafts or grades goes out on a
	// credential the repository's own `agent.auth` does not answer — a craft's
	// `access` naming Vertex, a pool, a provider key or another credential
	// (D3062: moved as written). The table has one chain per model and none per
	// craft (D3171), so no entry keeps such a craft where it is; the whole call
	// stays on the release-1 bridge, which serves it the credential it has
	// today rather than moving the craft onto the repository's own.
	BridgeCraftAccess = "craft-access"
)

// Installation is one installation's own configuration, as the migration reads
// it: what the project holds once, independent of any repository.
type Installation struct {
	// Project is the project's name, which is the id of every pool's principal
	// below.
	Project string
	// Accounts are every Claude account the installation names — the
	// `claude_accounts` block, the default account, a stage's account, a
	// grade's access where it names an account — in the order the accounts
	// block lists them, each with the consent its own `allow_data_to` carries.
	// Every one of them becomes a member of the shared pool, in this order
	// (D3183), and gets a pool of one named after it (D3176).
	Accounts []Account
	// DefaultAccount is the account a repository that names none goes out on:
	// the retired walk's last two steps (`default_claude_account`, then the
	// unsuffixed `default`), resolved by the caller. It is a member of the
	// shared pool like every other account.
	DefaultAccount string
	// Providers are the installation's provider keys, by name.
	Providers []Provider
	// Pools are the pools the installation already carries, which the
	// migration keeps as they are apart from the rule it resolves for them.
	Pools []Pool
	// SubscriptionSpread is whether the `subscription_spread` switch is on
	// today (F11): the switch is retired by the migration (D3177), and this is
	// what decides which pools are inert until it goes.
	SubscriptionSpread bool
	// FallbackAuto is whether the project policy's `fallback` is `auto` today:
	// the one setting that puts the paid key in a cascade's chain (D3064 as
	// amended).
	FallbackAuto bool
	// Via is `routing.claude.via`, and ViaModels is `routing.claude.models`:
	// the switch that says which Claude models are reached through a provider
	// key rather than directly (D1a, D3173). An empty Via is direct.
	Via       string
	ViaModels []string
	// VertexSpelled are the models the checked-in map spells for Vertex
	// (pricing.SpellingFor, D3175). The migration reads it for membership
	// alone: a model a Vertex repository calls that is not in it cannot be
	// written as a table entry and goes on the bridge instead.
	VertexSpelled []string
	// Repos are the repositories of the installation, each as its own file
	// resolved.
	Repos []Repo
}

// Account is one Claude account of the installation: the subscription a
// repository's stages go out on, and the consent that lets them.
type Account struct {
	// Name is the account's name, as the config's `claude_accounts` block
	// names it, and what the shared pool's member list carries.
	Name string
	// AllowDataTo are the repositories whose runs may choose this account —
	// config.ClaudeAccount.AllowDataTo, which D3065 made the consent (F9). It
	// is the whole of a pool's admission: a pool carries no admit list of its
	// own, its members' allow_data_to decides (D3182, R20), and this is what
	// tells the migration which calls its table would move.
	//
	// Empty means none, which is config.ClaudeAccount.AllowsData's own answer
	// and not ModelProvider.AllowsData's: a provider key nobody wrote an
	// allow_data_to for serves every repository, and an account nobody wrote one
	// for serves none. The caller hands each account the list its own config
	// holds, and an account the config does not carry at all — a name only a
	// repository's file or a carried pool mentions — consents to nobody.
	AllowDataTo []string
}

// allowsData is config.ClaudeAccount.AllowsData.
func (a Account) allowsData(repo string) bool {
	return slices.ContainsFunc(a.AllowDataTo, func(r string) bool {
		return strings.EqualFold(r, repo)
	})
}

// Provider is one provider key of the installation: the key a repository's
// provider route reaches, and the repositories whose code may be sent through
// it.
type Provider struct {
	// Name is the provider entry's name, which is what a source of kind
	// provider names and what Model.Provider carries.
	Name string
	// AllowDataTo are the repositories admitted to it, and AllowDataToWritten
	// records whether the key was written at all: an absent allow_data_to means
	// every repository, a written empty one nobody — the two answers
	// config.ModelProvider.AllowsData gives, spelled here because this package
	// imports no package of this tree.
	AllowDataTo        []string
	AllowDataToWritten bool
}

// allowsData is config.ModelProvider.AllowsData.
func (p Provider) allowsData(repo string) bool {
	if p.AllowDataTo == nil {
		return !p.AllowDataToWritten
	}
	return slices.ContainsFunc(p.AllowDataTo, func(r string) bool {
		return r == "*" || strings.EqualFold(r, repo)
	})
}

// Pool is one pool the installation already carries, as its own config holds
// it. The migration keeps it, resolves the rule it is served by and decides
// whether it is inert today; it writes no member into it, because a pool a
// person wrote is a person's.
type Pool struct {
	Name string
	// Members are the pool's members in the order its owner listed them.
	Members []string
	// Strategy is the pool's own `strategy` member, which the rule falls back
	// to (Rule.RuleOrDefault) for a pool written before `rule` existed.
	Strategy string
	// Owner and AllowedUsers are the pool's own members (D1936, R10): the person
	// a slip naming this pool is served to, and the others it is served to beside
	// them. They are carried through unchanged, because a write that dropped them
	// would leave a pool that serves nobody. An empty Owner is the state a pool
	// written before owners is in, which LegacyOpen records.
	Owner        string
	AllowedUsers []string
	// LegacyOpen is D1942's marker: a pool that existed before pools carried an
	// owner, which the migration of a published node writes. It is carried
	// through unchanged, because dropped, an open pool — one served to whoever
	// asks — would read as one whose owner is still to be named (F37).
	LegacyOpen bool
	// Principal is the `principal` member the installation's own config states
	// for this pool, which PL1 adds to config.Pool. The migration writes back
	// exactly what the config holds and states none where it holds none — which
	// is every pool written before PL1 — because a principal is an identity (R7)
	// and this migration is not the side that knows it.
	Principal Principal
	// Cascade records the marker D3064 gave the cascade's pool. Such a pool is
	// the shared pool: the migration keeps its members and order and adds every
	// account that is not already in it.
	Cascade bool
}

// Repo is one repository's file, as the migration reads it.
type Repo struct {
	// Name is the repository's owner/name, which is what an admission list and
	// an account's own allow_data_to carry.
	Name string
	// Auth is the repository's `agent.auth` as the layers in force resolve it,
	// one of config.AgentAuths: a repository that authenticates with Vertex says
	// `vertex` here, whether the file wrote it or the config layer's own default
	// (config.DefaultAgentAuth) did. An empty value is the state C1's rule is
	// written for — neither the file nor the project layer gives an auth, so
	// nothing answers and the run is refused (F49) — and it is never Vertex: the
	// default an empty value must not be read as is exactly the one F49 retires,
	// because it would send a repository to the platform's own model and bill.
	Auth string
	// Accounts are the Claude accounts the repository's file names: the
	// accounts its subscription stages resolve to — the retired walk's answer
	// for `agent.auth: oauth`, in the order the stages walk them — the mix's
	// account beside an api-key repository's key (D1848), and the accounts a
	// craft's or a grade's access names. Each of them is a member of the shared
	// pool, and the shared pool is what the chain names: one chain per model
	// cannot hold a pool per stage, per craft or per grade, which is what
	// retiring them means (D3171, F46), and the repository's own accounts
	// narrow the walk through their own allow_data_to (R20).
	Accounts []string
	// Accesses are the credentials the repository's crafts and grades go out on
	// that are not accounts: `agent.crafts.<craft>.access` and
	// `model_grades.<grade>.access`, resolved by the caller the way the retired
	// walk resolves them (craft.LegacyAccess's first two rungs, D3062: moved as
	// written). An access naming an account is in Accounts instead, because the
	// shared pool is what serves an account (D3183); what reaches the migration
	// here is every other name — the built-in Vertex, a pool, a provider key, one
	// of the retired `agent.auth` values — and a call one of them serves cannot be
	// written as an entry that keeps it, so it goes on the release-1 bridge
	// rather than moving that craft onto the repository's own credential
	// (D3171, D3196).
	Accesses []string
	// Models are the models a run of this repository can call: every model its
	// file names — `agent.model`, `agent.models.*`, `agent.crafts.*.model`, a
	// grade's model — resolved by the caller.
	Models []Model
}

// Model is one model a run of a repository can call.
type Model struct {
	// ID is the model ID as the configuration names it.
	ID string
	// Claude is whether the model is an Anthropic one (config.IsClaudeModel):
	// the Anthropic key and the account walk serve Claude models, and the
	// switch's opt-in is about them alone.
	Claude bool
	// Provider is the provider entry that claims the model, or "" where none
	// does (config.ProviderFor).
	Provider string
}

// Migration is the whole of what Migrate answers.
type Migration struct {
	// Table is the project's table: one entry per model the configuration
	// names, each chain carrying the admissions that keep every repository on
	// the credential it used (D3182).
	Table Table
	// Pools are the pools the migration writes or carries: the shared pool
	// (D3183) — written only where the installation has an account to put in it,
	// because the config refuses a pool with no members — a pool of one per
	// account (D3176), and every pool the installation already carried.
	Pools []PoolSpec
	// Bridge is what could not be written as a table RT3 accepts, by name: the
	// repository, the model and the reason. MG2 leaves each of them on the
	// release-1 bridge and reports them (D3196).
	Bridge []BridgeEntry
	// Inert are the pools that are inert today and rotate once migrated
	// (D3177): two or more members, the spread switch off, and not the pool the
	// cascade was marked with. MG2 prints one line per pool.
	Inert []string
	// Moves are the calls whose first reachable source changes: the ones the
	// table sends somewhere else than the repository's own configuration does
	// today. MG2 names every one of them, because "nothing moves on upgrade" is
	// the promise this migration is held to and a move nobody is told about is
	// the one thing it forbids; it repairs none of them, because what they turn
	// on is an account's own consent to a repository, which is a person's to
	// write (D3182, R20).
	Moves []Move
}

// Move is one call the migration's table sends somewhere else than the
// configuration it replaces does today: a repository's call of one model whose
// first reachable source changes.
//
// The shared pool is what moves a call, and a pool's admission is the reason it
// can: a pool carries no admit list of its own and reaches a repository through
// its members' `allow_data_to` (D3182, R20), so an account's consent to a
// repository is enough to put that repository on the pool — where the
// repository's own configuration never used those accounts, an api-key
// repository an account lists above all. The consent is a person's to write and
// this migration does not touch it, so the move is reported rather than
// prevented.
type Move struct {
	// Repo and Model are the repository whose call moves and the model it calls.
	Repo  string `json:"repo"`
	Model string `json:"model"`
	// From is the source the repository's own configuration gives it today, and
	// To the source the table's chain reaches for it. A zero SourceName on
	// either side is the call nothing serves: From where the repository's own
	// configuration reaches nothing — the state C1's rule refuses a run for
	// (F49) — and To where the chain reaches nothing for it.
	From SourceName `json:"from"`
	To   SourceName `json:"to"`
}

// PoolSpec is one pool as the migration writes it: a name, the rule it is
// served by, whose it is, and its members in order.
//
// It is not C2's PoolEntry: that is the console's document and carries each
// member's state, usage and headroom, none of which a migration knows or may
// invent. This is the shape the config is written with.
type PoolSpec struct {
	Name string `json:"name"`
	Rule Rule   `json:"rule"`
	// Principal is the pool's principal where one is written: the project's for
	// every pool the migration creates (D3183's shared pool, D3181's project
	// pool), and for a pool the installation already carries exactly the
	// principal its own config states — nil where it states none, which is every
	// pool written before PL1 added the member.
	//
	// The migration invents no principal for a carried pool, and the two answers
	// available to it are both wrong. A person's principal is the Google subject
	// the service verified, which no owner's email address supplies, so writing
	// the owner would put display text in the enforcing half (R7, R21). And the
	// project would make a pool a person wrote the project's, which a
	// service-account launcher may then be served from — where a project pool is
	// one an admin fills and a person's pool is a person's (D3181, D2976). An
	// installation's own config is where that identity is stated.
	Principal *Principal `json:"principal,omitempty"`
	Members   []string   `json:"members"`
	// Owner, AllowedUsers and LegacyOpen are carried through from the pool's own
	// config unchanged (D1936, R10, D1942, F37). The owner and the allowed users
	// are the roster — which slips this pool's tokens are served to (Pool.Serves)
	// — and a write that dropped them would leave a pool that serves nobody.
	// LegacyOpen is the marker that a pool predated owners and is served to
	// whoever asks: dropped, an open pool would read as one whose owner is still
	// to be named, and "no owner" and "open to everyone" would be one piece of
	// text (F37).
	Owner        string   `json:"owner,omitempty"`
	AllowedUsers []string `json:"allowed_users,omitempty"`
	LegacyOpen   bool     `json:"legacy_open,omitempty"`
}

// BridgeEntry is one thing the migration could not write as a table the checks
// accept, named so that the side keeping it on the release-1 bridge and the
// side reporting it agree on which one they mean (D3196).
type BridgeEntry struct {
	// Repo is the repository whose configuration the entry comes from.
	Repo string `json:"repo"`
	// Model is the model it is about.
	Model string `json:"model"`
	// Reason is one of the bridge constants above.
	Reason string `json:"reason"`
}

// Migrate answers the table, the pools, the admissions and the moves one
// installation's configuration becomes.
func Migrate(in Installation) Migration {
	m := &migrator{in: in, poolOf: map[string]bool{}, own: map[string]SourceName{}, bridged: map[string]bool{}}
	m.collectAccounts()
	m.buildPools()
	m.buildTable()
	m.buildMoves()
	m.buildInert()
	return Migration{Table: m.table, Pools: m.pools, Bridge: m.bridge, Inert: m.inert, Moves: m.moves}
}

type migrator struct {
	in     Installation
	pools  []PoolSpec
	table  Table
	bridge []BridgeEntry
	inert  []string
	moves  []Move
	// own is what each call's own configuration goes out on today: the first
	// source that call puts in its chain, by "repo\x00model". It is what
	// buildMoves compares the table's answer against.
	own map[string]SourceName
	// bridged records the calls the migration could not write as a table entry,
	// by the same key: their answer is the bridge entry of their own (D3196),
	// and no move is reported for them.
	bridged map[string]bool
	// poolOf records the pools written by name, so that an account and a pool
	// the installation carries are written once.
	poolOf map[string]bool
	// shared records whether the shared pool was written. It is not written for
	// an installation with no account, and a chain names it only where it is
	// there: a table naming a pool the config does not hold is refused whole.
	shared bool
	// accounts are every account the migration wraps, in order: the
	// installation's own, then any a repository's file names and the
	// installation's list does not.
	accounts []string
}

// collectAccounts gathers the accounts the installation names, the one a
// repository that names none goes out on, and the ones a repository's file
// names that the installation's own list does not: an account a repository goes
// out on is an account, and the shared pool it lands in has to hold it.
func (m *migrator) collectAccounts() {
	for _, a := range m.in.Accounts {
		m.accounts = appendUnique(m.accounts, a.Name)
	}
	m.accounts = appendUnique(m.accounts, m.in.DefaultAccount)
	for _, r := range m.in.Repos {
		m.accounts = appendUnique(m.accounts, r.Accounts...)
	}
}

// buildPools writes the shared pool, a pool of one per account, and every pool
// the installation already carries.
//
// A pool the installation carries keeps its own name: the pools of one below
// skip every name such a pool holds, so a pool a person wrote — its members, its
// order and the rule its strategy means — is never dropped in favour of a shape
// this migration invents. An account whose name a carried pool holds keeps its
// membership in the shared pool and goes without a pool of one, which is what
// `--pool <account>` then resolves to: the owner's own pool (D3176).
func (m *migrator) buildPools() {
	// The shared pool first: the members a pool the cascade was marked with
	// already had, in their owner's order, then every account the installation
	// names, in its own order (D3183: same members and order). The marked pool
	// IS this one — D3064 is what the shared pool replaces — so its owner's
	// order is what its runs go out on today.
	shared := []string{}
	for _, p := range m.in.Pools {
		if p.Cascade || p.Name == SharedPool {
			shared = appendUnique(shared, p.Members...)
		}
	}
	shared = appendUnique(shared, m.accounts...)
	// A pool with no members is a shape the config refuses, and an installation
	// with no account has nothing to put in this one.
	if len(shared) > 0 {
		m.pools = append(m.pools, PoolSpec{
			Name:      SharedPool,
			Rule:      RuleFailover,
			Principal: &Principal{Kind: PrincipalProject, ID: m.in.Project},
			Members:   shared,
		})
		m.poolOf[SharedPool] = true
		m.shared = true
	}
	// The names the installation's own pools hold, claimed before the pools of
	// one below.
	carried := map[string]bool{}
	for _, p := range m.in.Pools {
		if p.Name != "" {
			carried[p.Name] = true
		}
	}
	// A pool of one per account: the retired `--token <account>` becomes
	// `--pool <account>` (D3176), which needs a pool of that name, and every
	// subscription token of the installation then sits in a pool.
	ones := slices.Clone(m.accounts)
	sort.Strings(ones)
	for _, a := range ones {
		if m.poolOf[a] || carried[a] {
			continue
		}
		m.pools = append(m.pools, PoolSpec{
			Name:      a,
			Rule:      RuleFailover,
			Principal: &Principal{Kind: PrincipalProject, ID: m.in.Project},
			Members:   []string{a},
		})
		m.poolOf[a] = true
	}
	// Every other pool the installation carries, kept as it is, with the rule
	// it is served by resolved once (Rule.RuleOrDefault): a pool written with a
	// strategy and no rule keeps that strategy's meaning (R21).
	for _, p := range m.in.Pools {
		if p.Name == "" || m.poolOf[p.Name] {
			continue
		}
		m.pools = append(m.pools, PoolSpec{
			Name:         p.Name,
			Rule:         Rule("").RuleOrDefault(p.Strategy),
			Principal:    carriedPrincipal(p.Principal),
			Members:      slices.Clone(p.Members),
			Owner:        p.Owner,
			AllowedUsers: slices.Clone(p.AllowedUsers),
			LegacyOpen:   p.LegacyOpen,
		})
		m.poolOf[p.Name] = true
	}
}

// carriedPrincipal is the principal a pool the installation already carries is
// written with: the one its own config states, or none where it states none or
// states a half one (Principal.Valid). A config that states a half principal is
// a written mistake PL1 refuses before a migration reads it, and writing it back
// would put a pool nothing can resolve in the file.
func carriedPrincipal(p Principal) *Principal {
	if !p.Valid() {
		return nil
	}
	return &p
}

// buildTable writes one entry per model the configuration names, and the bridge
// entries for the models it cannot write.
func (m *migrator) buildTable() {
	// The repositories that can call each model, by model ID.
	callers := map[string]map[string]Model{}
	for _, repo := range m.in.Repos {
		for _, model := range repo.Models {
			if model.ID == "" {
				continue
			}
			if callers[model.ID] == nil {
				callers[model.ID] = map[string]Model{}
			}
			callers[model.ID][repo.Name] = model
		}
	}
	ids := sortedKeys(callers)
	for _, id := range ids {
		c := &chain{pools: map[string]bool{}, providers: map[string][]string{}}
		for _, name := range sortedKeys(callers[id]) {
			m.addCall(c, m.repo(name), callers[id][name], name)
		}
		if sources := c.sources(); len(sources) > 0 {
			m.table = append(m.table, Entry{Match: id, Chain: sources})
		}
	}
}

// addCall adds to one entry's chain what one repository's call of one model
// reaches.
func (m *migrator) addCall(c *chain, repo Repo, model Model, name string) {
	// A craft or a grade that goes out on a credential of its own (D3062) is a
	// fact the table cannot keep: one chain per model holds no credential per
	// craft (D3171), so the whole call stays on the release-1 bridge, which
	// serves that craft the credential it has today. An access that names the
	// very auth the repository already answers adds nothing, and an account is
	// not in this list at all — the shared pool is what serves it.
	//
	// It is checked before the model's provider, because it holds whichever route
	// the model takes: a call written into the table from a provider's branch
	// would change the credential that craft goes out on with neither a bridge
	// entry nor a move, and where the provider claims the model without admitting
	// this repository the bridge leaves the call reaching nothing, exactly as it
	// does today.
	for _, access := range repo.Accesses {
		if access != "" && access != repo.Auth {
			m.bridgeCall(name, model.ID, BridgeCraftAccess)
			return
		}
	}
	switch {
	case model.Provider == "":
		m.addAuthSource(c, repo, model, name)
	case !m.providerAllows(model.Provider, name):
		// The provider claims the model and does not admit this repository: it
		// reaches nothing here, exactly as today, and admitting it would be the
		// widening D3182 exists to prevent.
	case m.claudeThroughProvider(model):
		// D3196: a Claude model through a provider key is the route D1a added,
		// and a table entry for it is refused until Check 50 lands. The entry
		// is not written at all, so the run that calls this model keeps the
		// route its configuration gives it today.
		m.bridgeCall(name, model.ID, BridgeClaudeThroughProvider)
	case model.Claude:
		// A Claude model the switch does not opt in keeps its own credential
		// even where a provider entry claims it: the switch's list is the
		// opt-in, and a Claude ID the operator did not write is untouched
		// (D1a, RoutingClaudeFor).
		m.addAuthSource(c, repo, model, name)
	default:
		c.addProvider(model.Provider, name)
		m.ownSource(name, model.ID, SourceName{Kind: KindProvider, Name: model.Provider})
	}
}

// addAuthSource adds the source the repository's own `agent.auth` gives it.
func (m *migrator) addAuthSource(c *chain, repo Repo, model Model, name string) {
	// A Vertex model the checked-in map spells for no Vertex source cannot be
	// written as an entry RT3 accepts, and F65/D3196 keeps the whole of such a
	// call on the release-1 bridge: nothing of it reaches the table, the pool
	// below included — a chain carrying that pool would send a Vertex model out
	// on a Claude subscription.
	if repo.Auth == authVertex && !slices.Contains(m.in.VertexSpelled, model.ID) {
		m.bridgeCall(name, model.ID, BridgeVertexUnspelled)
		return
	}
	// The accounts this repository's file names — a stage's `claude_account`,
	// the mix's account, a craft's access, a grade's access — are folded into
	// the shared pool (D3171, F46), and the chain names that pool before the
	// credential the auth gives: a chain that could not reach them would move
	// exactly the runs those keys are for. An `oauth` or `cascade` repository
	// always names one, if only the installation's default account.
	//
	// It is named for a Claude model alone, because a Claude subscription is what
	// its members are: naming it for a Vertex model would write the very chain
	// the bridge above exists to prevent, whichever auth the repository has. And
	// it is named only where it was written: an installation with no account at
	// all has no shared pool to send a call to, and a chain naming one the config
	// does not hold would be refused whole. Such a repository then reaches
	// nothing, which is what it reaches today.
	pool := m.shared && model.Claude &&
		(len(repo.Accounts) > 0 || repo.Auth == authOAuth || repo.Auth == authCascade)
	if pool {
		c.addPool(SharedPool)
	}
	switch repo.Auth {
	case authOAuth:
		// Which member of the shared pool serves this repository is the
		// accounts' own allow_data_to, untouched here: a member a repository is
		// not admitted to is skipped (D3182, R20). The accounts are the
		// credential this repository's own calls have today.
		m.ownPool(name, model.ID, pool)
	case authCascade:
		m.ownPool(name, model.ID, pool)
		if m.in.FallbackAuto {
			// D3064 as amended: the paid key is in the cascade's chain only
			// where the policy's fallback is auto today.
			c.anthropic = appendUnique(c.anthropic, name)
			m.ownSource(name, model.ID, SourceName{Kind: KindAnthropic})
		}
	case authAPIKey:
		// The key is what the repository's own stages go out on beside the
		// accounts, and the walk falls to it when the pool holds nothing it
		// may use — the tier order the retired walk used. An account the file
		// names beside it is the mix's, and it serves the three mix stages
		// alone (D1848): the key is the credential this call has, so what the
		// pool does with the call is a move the migration reports.
		c.anthropic = appendUnique(c.anthropic, name)
		m.ownSource(name, model.ID, SourceName{Kind: KindAnthropic})
	case authVertex:
		// The map spells this model for Vertex, or the guard above would have
		// bridged the call.
		c.vertex = appendUnique(c.vertex, name)
		m.ownSource(name, model.ID, SourceName{Kind: KindVertex})
	default:
		// Nothing answers: an auth this release does not know — fail closed, as
		// the config's own validation refuses such a file before a run sees it —
		// and the empty value a file and the project layer both leave unwritten,
		// which C1's rule answers ok=false for and which is never Vertex (F49).
	}
}

// ownPool records the shared pool as a call's own source where the chain named
// it: an `oauth` or `cascade` repository goes out on its accounts, which the
// pool is. It is not recorded for an api-key repository's mix account (D1848)
// or a Vertex repository's, which sit beside the credential the auth gives
// rather than in place of it.
func (m *migrator) ownPool(repo, model string, named bool) {
	if named {
		m.ownSource(repo, model, SourceName{Kind: KindPool, Name: SharedPool})
	}
}

// claudeThroughProvider reports whether this model's call takes a provider
// route: `routing.claude.via` with an open value and the switch naming the
// model (D1a). A non-Claude model claimed by a provider always takes it; a
// Claude model takes it only where the operator opted it in.
func (m *migrator) claudeThroughProvider(model Model) bool {
	if !model.Claude {
		return false
	}
	switch m.in.Via {
	case viaOpenRouter, viaAuto:
		return slices.Contains(m.in.ViaModels, model.ID)
	}
	return false
}

func (m *migrator) providerAllows(provider, repo string) bool {
	for _, p := range m.in.Providers {
		if p.Name == provider {
			return p.allowsData(repo)
		}
	}
	// A provider a repository's file names and the installation does not carry
	// is a configuration the config's own validation refuses; it reaches
	// nothing here rather than being admitted by default.
	return false
}

func (m *migrator) repo(name string) Repo {
	for _, r := range m.in.Repos {
		if r.Name == name {
			return r
		}
	}
	return Repo{}
}

// account is the installation's own entry for an account, or the zero Account
// for one the config does not carry: a name only a repository's file or a
// carried pool mentions consents to nobody, which is the fail-closed direction
// an unwritten admission takes everywhere else.
func (m *migrator) account(name string) Account {
	for _, a := range m.in.Accounts {
		if a.Name == name {
			return a
		}
	}
	return Account{}
}

// bridgeCall records a call the migration leaves on the release-1 bridge: the
// entry MG2 reports it by (D3196), and the fact that it is no move — the table's
// answer for the model is another repository's call of it, which is not what
// serves this one.
func (m *migrator) bridgeCall(repo, model, reason string) {
	m.bridge = append(m.bridge, BridgeEntry{Repo: repo, Model: model, Reason: reason})
	m.bridged[repo+"\x00"+model] = true
}

// ownSource records the source one call's own configuration goes out on today:
// the first source that call puts in the chain is the one its auth gives it,
// and a later one is the fallback behind it, so the first recording is the one
// that counts and the order here is the chain's own order.
func (m *migrator) ownSource(repo, model string, src SourceName) {
	key := repo + "\x00" + model
	if _, ok := m.own[key]; !ok {
		m.own[key] = src
	}
}

// buildMoves reports every call whose first reachable source the table changes.
//
// The two sides are the two answers the migration has: what a repository's own
// call goes out on today (ownSource), and what the table's chain reaches for it
// (firstReaching, which walks a pool's admission rather than assuming it). They
// differ where a source another repository's call put in the chain also reaches
// this repository — the shared pool above all, whose members' `allow_data_to` is
// a consent the migration reads but does not write — and the pair is reported so
// that MG2 names it instead of letting the run move quietly (D3182, R20).
//
// A pair whose model was bridged has no entry to compare against — it is
// reported as a bridge entry of its own (D3196) — and a pair whose two sides
// both reach nowhere is the call nothing serves, which is what C1's rule
// answers for it today too (F49). A pair whose own configuration reaches
// nothing and whose chain now reaches a source is a move like any other, and
// reported: the run that was refused would go out on a credential after the
// migration.
func (m *migrator) buildMoves() {
	// Repo.Models is every model the repository's file names — `agent.model`,
	// `agent.models.*` and `agent.crafts.*.model` alike — so two lines can name
	// the same call. A call is one pair of repository and model however many
	// lines name it, and it is reported once: one move, one line MG2 prints.
	seen := map[string]bool{}
	for _, repo := range m.in.Repos {
		for _, model := range repo.Models {
			if model.ID == "" {
				continue
			}
			key := repo.Name + "\x00" + model.ID
			if seen[key] {
				continue
			}
			seen[key] = true
			// A call the migration left on the release-1 bridge is not a move,
			// whatever the entry another repository's call of the same model
			// carries: what serves it is the bridge, and MG2 reports it by that
			// entry's own name (D3196).
			if m.bridged[key] {
				continue
			}
			entry, ok := entryFor(m.table, model.ID)
			if !ok {
				continue
			}
			now := m.firstReaching(entry, repo)
			was := m.own[key]
			if now == was {
				continue
			}
			m.moves = append(m.moves, Move{Repo: repo.Name, Model: model.ID, From: was, To: now})
		}
	}
	slices.SortFunc(m.moves, func(a, b Move) int {
		if c := strings.Compare(a.Repo, b.Repo); c != 0 {
			return c
		}
		return strings.Compare(a.Model, b.Model)
	})
}

// firstReaching is the first source of an entry's chain a repository's runs
// reach: a single-credential source admits it by name (D3182), and a pool
// reaches it through its members' own allow_data_to (D3182, R20) — the
// admission a pool does not carry itself. The zero SourceName is the call
// nothing serves, the fail-closed answer C1 gives one.
func (m *migrator) firstReaching(entry Entry, repo Repo) SourceName {
	for _, s := range entry.Chain {
		switch s.Kind {
		case KindPool:
			if m.poolReaches(s.Pool, repo.Name) {
				return SourceName{Kind: KindPool, Name: s.Pool}
			}
		case KindAnthropic:
			if slices.Contains(s.Admit, repo.Name) {
				return SourceName{Kind: KindAnthropic}
			}
		case KindProvider:
			if slices.Contains(s.Admit, repo.Name) {
				return SourceName{Kind: KindProvider, Name: s.Provider}
			}
		case KindVertex:
			if slices.Contains(s.Admit, repo.Name) {
				return SourceName{Kind: KindVertex}
			}
		case KindLauncherPool:
			// The migration writes no launcher-pool source: which pool serves a
			// launcher is decided per run (D3176, D3179), so a chain carrying
			// one is not this migration's answer and nothing here reaches
			// through it.
		}
	}
	return SourceName{}
}

// poolReaches reports whether a pool reaches a repository: its members' own
// allow_data_to is its admission (D3182, R20), and a member that consents is
// enough. A pool this migration wrote no members for reaches nobody, which is
// the same fail-closed answer an unwritten admission gives.
//
// No account is exempt, DefaultAccount included: the reserved `default` is
// always a name a repository may choose (config.AllowedClaudeAccounts) and the
// retired walk falls back to it without consulting an allow_data_to, so an
// installation whose default account carries none reaches nothing through the
// pool — and the migration says so, per call, in Moves, rather than assuming a
// consent the config does not hold. Writing that consent is not this
// migration's: "each account keeps its own admissions untouched" (D3182).
func (m *migrator) poolReaches(pool, repo string) bool {
	for _, p := range m.pools {
		if p.Name != pool {
			continue
		}
		for _, member := range p.Members {
			if m.account(member).allowsData(repo) {
				return true
			}
		}
	}
	return false
}

// entryFor is the entry that routes a model: the migration writes one entry per
// model ID, so the match is the whole of the lookup.
func entryFor(table Table, model string) (Entry, bool) {
	for _, e := range table {
		if e.Match == model {
			return e, true
		}
	}
	return Entry{}, false
}

// buildInert lists the pools that are inert today and rotate once migrated
// (D3177): two or more members, the spread switch off, and not the pool the
// cascade was marked with. The shared pool is that marked pool wherever a
// cascade repository exists or a pool carries the marker; a project with
// neither has a shared pool whose accounts do not rotate today either, so it is
// listed like any other.
func (m *migrator) buildInert() {
	if m.in.SubscriptionSpread {
		return
	}
	marked := false
	for _, p := range m.in.Pools {
		if p.Cascade || p.Name == SharedPool {
			marked = true
		}
	}
	for _, r := range m.in.Repos {
		if r.Auth == authCascade {
			marked = true
		}
	}
	for _, p := range m.pools {
		if p.Name == SharedPool && marked {
			continue
		}
		if len(p.Members) >= 2 {
			m.inert = append(m.inert, p.Name)
		}
	}
}

// chain is the sources one entry accumulates over the repositories that call
// its model, in the order the walk takes them: the tier order the fixed walk
// replaced — subscription, the Anthropic key, a provider key, Vertex.
type chain struct {
	pools     map[string]bool
	anthropic []string
	providers map[string][]string
	vertex    []string
}

func (c *chain) addPool(name string) {
	if name != "" {
		c.pools[name] = true
	}
}

func (c *chain) addProvider(name, repo string) {
	c.providers[name] = appendUnique(c.providers[name], repo)
}

// sources is the entry's chain: the pools in name order, then the Anthropic
// key, then the providers in name order, then Vertex. Each single-credential
// source carries the repositories admitted to it (D3182); a pool carries none,
// because its members' own allow_data_to is its admission.
func (c *chain) sources() []Source {
	var out []Source
	for _, name := range sortedKeys(c.pools) {
		out = append(out, Source{Kind: KindPool, Pool: name})
	}
	if len(c.anthropic) > 0 {
		out = append(out, Source{Kind: KindAnthropic, Admit: slices.Clone(c.anthropic)})
	}
	for _, name := range sortedKeys(c.providers) {
		out = append(out, Source{Kind: KindProvider, Provider: name, Admit: slices.Clone(c.providers[name])})
	}
	if len(c.vertex) > 0 {
		out = append(out, Source{Kind: KindVertex, Admit: slices.Clone(c.vertex)})
	}
	return out
}

func appendUnique(list []string, items ...string) []string {
	for _, item := range items {
		if item != "" && !slices.Contains(list, item) {
			list = append(list, item)
		}
	}
	return list
}

// sortedKeys is one key order for every map this file walks, so the same
// configuration always writes the same document.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

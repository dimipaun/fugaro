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
	// block lists them. Every one of them becomes a member of the shared pool,
	// in this order (D3183), and gets a pool of one named after it (D3176).
	Accounts []string
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

// Pool is one pool the installation already carries. The migration keeps it,
// resolves the rule it is served by and decides whether it is inert today; it
// writes no member into it, because a pool a person wrote is a person's.
type Pool struct {
	Name string
	// Members are the pool's members in the order its owner listed them.
	Members []string
	// Strategy is the pool's own `strategy` member, which the rule falls back
	// to (Rule.RuleOrDefault) for a pool written before `rule` existed.
	Strategy string
	// Principal is whose pool it is.
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
}

// PoolSpec is one pool as the migration writes it: a name, the rule it is
// served by, whose it is, and its members in order.
//
// It is not C2's PoolEntry: that is the console's document and carries each
// member's state, usage and headroom, none of which a migration knows or may
// invent. This is the shape the config is written with.
type PoolSpec struct {
	Name      string    `json:"name"`
	Rule      Rule      `json:"rule"`
	Principal Principal `json:"principal"`
	Members   []string  `json:"members"`
}

// BridgeEntry is one thing the migration could not write as a table the checks
// accept, named so that the side keeping it on the release-1 bridge and the
// side reporting it agree on which one they mean (D3196).
type BridgeEntry struct {
	// Repo is the repository whose configuration the entry comes from.
	Repo string `json:"repo"`
	// Model is the model it is about.
	Model string `json:"model"`
	// Reason is one of the two bridge constants above.
	Reason string `json:"reason"`
}

// Migrate answers the table, the pools and the admissions one installation's
// configuration becomes.
func Migrate(in Installation) Migration {
	m := &migrator{in: in, poolOf: map[string]bool{}}
	m.collectAccounts()
	m.buildPools()
	m.buildTable()
	m.buildInert()
	return Migration{Table: m.table, Pools: m.pools, Bridge: m.bridge, Inert: m.inert}
}

type migrator struct {
	in     Installation
	pools  []PoolSpec
	table  Table
	bridge []BridgeEntry
	inert  []string
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
	m.accounts = appendUnique(nil, m.in.Accounts...)
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
			Principal: Principal{Kind: PrincipalProject, ID: m.in.Project},
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
			Principal: Principal{Kind: PrincipalProject, ID: m.in.Project},
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
			Name:      p.Name,
			Rule:      Rule("").RuleOrDefault(p.Strategy),
			Principal: p.Principal,
			Members:   slices.Clone(p.Members),
		})
		m.poolOf[p.Name] = true
	}
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
	switch {
	case model.Provider == "":
		m.addAuthSource(c, repo, model.ID, name)
	case !m.providerAllows(model.Provider, name):
		// The provider claims the model and does not admit this repository: it
		// reaches nothing here, exactly as today, and admitting it would be the
		// widening D3182 exists to prevent.
	case m.claudeThroughProvider(model):
		// D3196: a Claude model through a provider key is the route D1a added,
		// and a table entry for it is refused until Check 50 lands. The entry
		// is not written at all, so the run that calls this model keeps the
		// route its configuration gives it today.
		m.bridge = append(m.bridge, BridgeEntry{Repo: name, Model: model.ID, Reason: BridgeClaudeThroughProvider})
	case model.Claude:
		// A Claude model the switch does not opt in keeps its own credential
		// even where a provider entry claims it: the switch's list is the
		// opt-in, and a Claude ID the operator did not write is untouched
		// (D1a, RoutingClaudeFor).
		m.addAuthSource(c, repo, model.ID, name)
	default:
		c.addProvider(model.Provider, name)
	}
}

// addAuthSource adds the source the repository's own `agent.auth` gives it.
func (m *migrator) addAuthSource(c *chain, repo Repo, model, name string) {
	// A Vertex model the checked-in map spells for no Vertex source cannot be
	// written as an entry RT3 accepts, and F65/D3196 keeps the whole of such a
	// call on the release-1 bridge: nothing of it reaches the table, the pool
	// below included — a chain carrying that pool would send a Vertex model out
	// on a Claude subscription.
	if repo.Auth == authVertex && !slices.Contains(m.in.VertexSpelled, model) {
		m.bridge = append(m.bridge, BridgeEntry{Repo: name, Model: model, Reason: BridgeVertexUnspelled})
		return
	}
	// The accounts this repository's file names — a stage's `claude_account`,
	// the mix's account, a craft's access, a grade's access — are folded into
	// the shared pool (D3171, F46), and the chain names that pool before the
	// credential the auth gives: a chain that could not reach them would move
	// exactly the runs those keys are for. An `oauth` or `cascade` repository
	// always names one, if only the installation's default account.
	//
	// It is named only where it was written: an installation with no account at
	// all has no shared pool to send a call to, and a chain naming one the config
	// does not hold would be refused whole. Such a repository then reaches
	// nothing, which is what it reaches today.
	if m.shared && (len(repo.Accounts) > 0 || repo.Auth == authOAuth || repo.Auth == authCascade) {
		c.addPool(SharedPool)
	}
	switch repo.Auth {
	case authOAuth:
		// Which member of the shared pool serves this repository is the
		// accounts' own allow_data_to, untouched here: a member a repository is
		// not admitted to is skipped (D3182, R20).
	case authCascade:
		if m.in.FallbackAuto {
			// D3064 as amended: the paid key is in the cascade's chain only
			// where the policy's fallback is auto today.
			c.anthropic = appendUnique(c.anthropic, name)
		}
	case authAPIKey:
		// The key is what the repository's stages go out on beside the
		// accounts, and the walk falls to it when the pool holds nothing it
		// may use — the tier order the retired walk used.
		c.anthropic = appendUnique(c.anthropic, name)
	case authVertex:
		// The map spells this model for Vertex, or the guard above would have
		// bridged the call.
		c.vertex = appendUnique(c.vertex, name)
	default:
		// Nothing answers: an auth this release does not know — fail closed, as
		// the config's own validation refuses such a file before a run sees it —
		// and the empty value a file and the project layer both leave unwritten,
		// which C1's rule answers ok=false for and which is never Vertex (F49).
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

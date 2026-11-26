package policy

import (
	"maps"
	"slices"
	"strings"
)

// The credentials, the pools and the crafts of docs/model-crafts, as the merge
// sees them: what a project makes available to its repositories and what a
// repository selects among it (reconciliation F12, D1628).
//
// The vocabulary is config's — a credential is config.Credential, a pool
// config.Pool and a craft config.Crafts, read from fugaro.yaml, the project
// layer, the local config and the policy node — and what lives here is the one
// part of it the layers may disagree about, which is why this package carries
// three fields rather than those types: config imports this one (config's
// budget mode is this package's Mode, its governance block this package's
// Governance), so a build of this file that named config.Pool would be an
// import cycle. The layer builders — internal/runner's policyCeiling and
// FileLayer, the CLI's own — convert, and they are also what puts the built-in
// credentials (D1624) in the project layer's list: a credential every
// installation has is one the project makes available like any other, and no
// name of one is spelled here.

// The pool strategies, in D1628's direction of tightness: failover is the
// tighter of the two, because a pool that walks its list in order spreads
// neither spend nor the repository's code across its members, and round-robin
// is the looser. The names are the ones a layer file writes, and they are the
// wire spelling of a value in every installation's config — so they live here,
// where the rule that orders them is, and config aliases them
// (config.PoolStrategyFailover), the way it aliases this package's budget
// modes for the same reason.
const (
	PoolStrategyFailover   = "failover"
	PoolStrategyRoundRobin = "round-robin"
	// PoolRuleOrdered is the pool rule that arrived with the routing table
	// (R5, D3177): the first listed member that is usable, and no preference
	// beyond the order the pool's owner wrote it in. It is a RULE and not a
	// strategy — `ordered` is not one of the two values above and a pool that
	// wants it writes `rule` — so it lives here beside the rule that orders
	// the values rather than in the strategy block's own list.
	PoolRuleOrdered = "ordered"
)

// CraftPool is one layer's pool: the strategy and the ordered members, which
// are the two things D1628 compares. The members are credential names — the
// names config.ValidCredentialRef admits, the built-in vertex among them — and
// the order is meaningful for both strategies, which is why a "narrowed" pool
// has to keep it.
type CraftPool struct {
	// Strategy is the pool's strategy; "" is a pool that names none, which is
	// failover (config.Pool.StrategyOrDefault, D1632). It is kept as the layer
	// wrote it rather than normalized — the emptiness is a fact about the layer
	// — and the one value a merge renders is the strategy in force
	// (poolStrategyText).
	Strategy string
	// Rule is the pool's rule (R5): "" is a pool that names none, which is
	// served by its Strategy and by failover only where it names neither
	// (routing.Rule.RuleOrDefault). Like Strategy it is kept as the layer wrote
	// it, and it is a member of its own rather than a third value of Strategy:
	// `ordered` arrived as a rule and a value never changes meaning (R21).
	//
	// It is judged for tightness exactly as the strategy is, on its own key, so
	// a layer that pins `ordered` tightens a pool and one that loosens a rule
	// is dropped and recorded (tightenCraftPool).
	Rule string
	// Members is the pool's members, in the order it tries them.
	Members []string
	// Owner is the account the pool belongs to, by the email its roster
	// spells — the same spelling config.Pool.Owner holds, because a pool's
	// authorisation is decided from the published policy alone (D1936, R10)
	// and this is the form that node carries.
	Owner string
	// AllowedUsers is the roster the owner shares the pool with, by email. It
	// is a SET and not an ordered list: unlike Members, the order a layer
	// writes it in carries no meaning, which is why a later layer may narrow
	// it by dropping users rather than by keeping their order (tightenCraftPool).
	AllowedUsers []string
	// LegacyOpen is D1942's marker for a pool that predates owners: it is
	// served to everyone, which is what the migration writes so that a pool
	// the owner never gave an owner to keeps working (F37).
	LegacyOpen bool
}

// CraftLayer is one layer's craft block: the credentials and the pools the
// layer makes available and the credential or pool each craft's calls go out
// on.
//
// A craft's MODEL is not here. A model is neither a spending nor an egress
// grant, which is the whole of what D1628 calls tighter, and which craft
// inherits a model it does not set is the resolver's question (MC2).
//
// Credentials and pools are the PROJECT's: only the first layer of a merge —
// in a run the ceiling, which is the owner's policy node or the copy of his
// config the job was deployed with, never the runs-bucket shared file (F25) —
// may define one, and every layer above it may only select among the names it
// created and narrow a pool (F12, D1628). A name is a secret reference, and so
// a spending and data-egress grant: a repository file that could write one
// could hand a run's calls to a credential the owner never admitted.
type CraftLayer struct {
	// Credentials are the credential names this layer makes available, in the
	// order it lists them, the built-in credentials (D1624) named like any
	// other because a name is all this block ever compares. A later layer's
	// credentials are dropped and recorded.
	Credentials []string
	// Pools are this layer's pools by name. A later layer may narrow one and
	// may not introduce one.
	Pools map[string]CraftPool
	// Access is the credential or pool each craft's calls go out on, by craft
	// name — the five of config.CraftNames, kept as the layer wrote them: a
	// name outside that vocabulary is refused where the block is written, by
	// the strict decoder that owns it, and a merge that quietly dropped one it
	// did not know would answer an unset craft. The first layer selects among
	// its own and is taken as written; a later layer selects among the
	// effective set and is dropped, and recorded, when it names anything else.
	// A craft left out is unset, and what an unset access resolves to — the
	// craft it follows, then the project's default credential — is the
	// resolver's answer (MC2, D1624).
	Access map[string]string
}

// The two prefixes every keyed craft value is built from, under the same rule
// a tier's is (TierKey): the name a craft's access speaks is what a key has to
// carry, so a table of the spellings would be a second vocabulary to keep.
//
// There is no plain "crafts.credentials" or "crafts.pools" key: F12's "a
// repository may never introduce one" is a rule over a name, and a key for a
// whole block would read in the record as if the block itself had an effective
// value.
const (
	craftCredentialsPrefix = "crafts.credentials"
	craftPoolsPrefix       = "crafts.pools"
)

// CraftCredentialKey is the policy key of one credential
// ("crafts.credentials.openrouter-main").
func CraftCredentialKey(name string) string { return craftCredentialsPrefix + "." + name }

// CraftPoolKey is the policy key of a pool a layer may not introduce
// ("crafts.pools.claude").
func CraftPoolKey(name string) string { return craftPoolsPrefix + "." + name }

// CraftPoolMembersKey is the policy key of a pool's members
// ("crafts.pools.claude.members").
func CraftPoolMembersKey(name string) string { return CraftPoolKey(name) + ".members" }

// CraftPoolStrategyKey is the policy key of a pool's strategy
// ("crafts.pools.claude.strategy").
func CraftPoolStrategyKey(name string) string { return CraftPoolKey(name) + ".strategy" }

// CraftPoolRuleKey is the policy key of a pool's rule
// ("crafts.pools.claude.rule"). It is a key of its own and not a third spelling
// under the strategy's: a rule is a new member (R21), so a layer that writes
// one and a layer that writes a strategy are two statements a reader keeps
// apart.
func CraftPoolRuleKey(name string) string { return CraftPoolKey(name) + ".rule" }

// CraftPoolOwnerKey is the policy key of a pool's owner
// ("crafts.pools.claude.owner").
func CraftPoolOwnerKey(name string) string { return CraftPoolKey(name) + ".owner" }

// CraftPoolAllowedUsersKey is the policy key of a pool's roster
// ("crafts.pools.claude.allowed_users").
func CraftPoolAllowedUsersKey(name string) string { return CraftPoolKey(name) + ".allowed_users" }

// CraftPoolLegacyOpenKey is the policy key of a pool's legacy-open marker
// ("crafts.pools.claude.legacy_open").
func CraftPoolLegacyOpenKey(name string) string { return CraftPoolKey(name) + ".legacy_open" }

// CraftAccessKey is the policy key of one craft's access
// ("crafts.senior-review.access").
func CraftAccessKey(craft string) string { return "crafts." + craft + ".access" }

// tightenCraft applies one layer's craft block. project is whether the layer
// is the merge's first: the project's own, the one layer that creates
// credentials and pools (F12, F25, D1628).
//
// Nothing here is ever recorded against the project layer. The ceiling is
// never ignored (Merge): the project's list of credentials IS what a
// credential is, and a name of its own that no other layer could have written
// is refused where the layer is written (config.ValidCredentialRef), not
// dropped here where the only reader is a merge that cannot tell a typo from a
// name the installation holds.
func (e *Effective) tightenCraft(l CraftLayer, src string, project bool) {
	if project {
		e.defineCraftCredentials(l.Credentials, src)
		e.defineCraftPools(l.Pools, src)
		e.selectCraftAccess(l.Access, src, true)
		return
	}
	e.refuseCraftCredentials(l.Credentials)
	for _, name := range slices.Sorted(maps.Keys(l.Pools)) {
		if name != "" {
			e.tightenCraftPool(name, l.Pools[name], src)
		}
	}
	e.selectCraftAccess(l.Access, src, false)
}

// defineCraftCredentials takes the project's credentials as the effective set.
// An empty name is not a credential and is dropped: config.ValidateCredentials
// refuses one, and a name nothing can have would otherwise sit in the set and
// admit a craft's access to nothing.
func (e *Effective) defineCraftCredentials(names []string, src string) {
	for _, name := range names {
		if name == "" || slices.Contains(e.Craft.Credentials, name) {
			continue
		}
		e.Craft.Credentials = append(e.Craft.Credentials, name)
		e.Sources[CraftCredentialKey(name)] = src
	}
}

// refuseCraftCredentials records every credential a later layer tried to add.
// A name the effective set already holds is neither an introduction nor a
// loosening and is not recorded: the layer said nothing new about it.
func (e *Effective) refuseCraftCredentials(names []string) {
	for _, name := range names {
		if name == "" || slices.Contains(e.Craft.Credentials, name) {
			continue
		}
		key := CraftCredentialKey(name)
		e.Ignored = append(e.Ignored, Ignored{key, name, "", e.craftSource(key)})
	}
}

// craftSource is the layer a refused project-owned value lost to, as a record
// reads it: the layer that set the effective value where one did, and the
// project's own — the merge's first layer — where none did. The project's set of
// credentials and pools is the whole answer to "may this name be used", so its
// silence about a name is the answer a later layer may not loosen, and it is that layer
// the value was measured against. A refused grade is measured the same way
// (tightenGrades): a value with no effective counterpart was measured against
// the layer that owns the block.
//
// It is never "": a record's ignored[].source is the layer of the effective
// value, and a schema pins it to the four layer names
// (schemas/result.schema.json; internal/runner's policyRecord publishes this
// string verbatim), so a value with no effective counterpart still names the
// layer that answers for it.
func (e *Effective) craftSource(key string) string {
	if src := e.Sources[key]; src != "" {
		return src
	}
	return SourceCeiling
}

// defineCraftPools takes the project's pools as they stand, each member list
// copied and stripped of repeats: the layer it came from is not this merge's to
// write to.
func (e *Effective) defineCraftPools(pools map[string]CraftPool, src string) {
	for _, name := range slices.Sorted(maps.Keys(pools)) {
		if name == "" {
			continue
		}
		if e.Craft.Pools == nil {
			e.Craft.Pools = make(map[string]CraftPool, len(pools))
		}
		p := pools[name]
		e.Craft.Pools[name] = CraftPool{Strategy: p.Strategy, Rule: p.Rule, Members: dedupe(p.Members),
			Owner: p.Owner, AllowedUsers: dedupe(p.AllowedUsers), LegacyOpen: p.LegacyOpen}
		e.Sources[CraftPoolMembersKey(name)] = src
		if p.Strategy != "" {
			e.Sources[CraftPoolStrategyKey(name)] = src
		}
		if p.Rule != "" {
			e.Sources[CraftPoolRuleKey(name)] = src
		}
		if p.Owner != "" {
			e.Sources[CraftPoolOwnerKey(name)] = src
		}
		if len(p.AllowedUsers) > 0 {
			e.Sources[CraftPoolAllowedUsersKey(name)] = src
		}
		if p.LegacyOpen {
			e.Sources[CraftPoolLegacyOpenKey(name)] = src
		}
	}
}

// tightenCraftPool applies a later layer's entry for a pool the project
// defines. A pool it does not define is one the layer may not create: the
// entry is dropped whole, strategy and members together, because a pool's name
// is what a craft's access speaks and a second definition of it would leave
// that name meaning two pools.
//
// The members may only narrow, and the strategy and the rule — which are one
// scale of tightness (poolStrategyRank) — may only tighten. Each is judged on
// its own key, so a layer that narrows the list keeps the project's strategy
// and its rule, and one that pins the order keeps the project's members. A
// value equal to the running one moves nothing and is not recorded (Merge).
//
// Both entries name a layer and carry the value in force, an empty effective
// value being reserved for the refusals that have none at all (craftSource,
// poolStrategyText): a reader of the record tells the two apart by that field
// and nothing else.
func (e *Effective) tightenCraftPool(name string, p CraftPool, src string) {
	cur, defined := e.Craft.Pools[name]
	if !defined {
		key := CraftPoolKey(name)
		e.Ignored = append(e.Ignored, Ignored{key, name, "", e.craftSource(key)})
		return
	}
	out := cur
	if len(p.Members) > 0 && !slices.Equal(cur.Members, p.Members) {
		if narrows(cur.Members, p.Members) {
			out.Members, e.Sources[CraftPoolMembersKey(name)] = dedupe(p.Members), src
		} else {
			e.Ignored = append(e.Ignored, Ignored{CraftPoolMembersKey(name),
				strings.Join(p.Members, ","), strings.Join(cur.Members, ","),
				e.craftSource(CraftPoolMembersKey(name))})
		}
	}
	// Both are judged against the value in force (ruleInForce), not against the
	// member they were written under: the rule and the strategy are one scale of
	// tightness and one of them serves the pool, so a layer's rule must be able
	// to tighten a pool the project wrote as a strategy and to lose to a pool it
	// wrote as a rule.
	inForce := ruleInForce(out)
	if p.Strategy != "" {
		switch rank, running := poolStrategyRank(p.Strategy), poolStrategyRank(inForce); {
		case rank > running:
			out.Strategy, e.Sources[CraftPoolStrategyKey(name)] = p.Strategy, src
		case rank < running:
			e.Ignored = append(e.Ignored, Ignored{CraftPoolStrategyKey(name),
				p.Strategy, poolStrategyText(inForce), e.craftSource(CraftPoolStrategyKey(name))})
		}
	}
	// The rule is judged on its own key and by the same order of tightness, so
	// a layer that pins `ordered` moves the pool and one that widens a rule is
	// dropped and recorded. An empty effective rule is `failover`, which is what
	// a pool that names none is served by (poolStrategyText, R21).
	if p.Rule != "" {
		switch rank, running := poolStrategyRank(p.Rule), poolStrategyRank(inForce); {
		case rank > running:
			out.Rule, e.Sources[CraftPoolRuleKey(name)] = p.Rule, src
		case rank < running:
			e.Ignored = append(e.Ignored, Ignored{CraftPoolRuleKey(name),
				p.Rule, poolStrategyText(inForce), e.craftSource(CraftPoolRuleKey(name))})
		}
	}
	// The roster may only narrow: every name on it is a grant of the pool's
	// spend, so a layer that drops one is tighter and one that adds one is
	// looser, whatever else it keeps. The order is not compared — a roster is a
	// set — so a layer that re-lists the project's users in another order moves
	// nothing and is not recorded (Merge).
	if len(p.AllowedUsers) > 0 && !sameUsers(cur.AllowedUsers, p.AllowedUsers) {
		if subsetOf(cur.AllowedUsers, p.AllowedUsers) {
			out.AllowedUsers, e.Sources[CraftPoolAllowedUsersKey(name)] = dedupe(p.AllowedUsers), src
		} else {
			e.Ignored = append(e.Ignored, Ignored{CraftPoolAllowedUsersKey(name),
				strings.Join(p.AllowedUsers, ","), strings.Join(cur.AllowedUsers, ","),
				e.craftSource(CraftPoolAllowedUsersKey(name))})
		}
	}
	// An owner and the legacy-open marker are not a later layer's to write at
	// all. D1936 makes the owner the one account a pool's spend is the owner's
	// own, and D1942's marker is what a pool the migration found ownerless
	// carries: a repository that could set either would hand itself a shared
	// token the owner never gave, or reopen a pool the owner had narrowed.
	if p.Owner != "" && p.Owner != cur.Owner {
		e.Ignored = append(e.Ignored, Ignored{CraftPoolOwnerKey(name), p.Owner, cur.Owner,
			e.craftSource(CraftPoolOwnerKey(name))})
	}
	if p.LegacyOpen && !cur.LegacyOpen {
		e.Ignored = append(e.Ignored, Ignored{CraftPoolLegacyOpenKey(name), "true", "false",
			e.craftSource(CraftPoolLegacyOpenKey(name))})
	}
	e.Craft.Pools[name] = out
}

// sameUsers reports whether two rosters hold the same names, order aside: a
// roster is a set, so a layer that re-lists the project's users in another
// order has said nothing new (tightenCraftPool).
func sameUsers(have, want []string) bool {
	if len(have) != len(dedupe(want)) {
		return false
	}
	return subsetOf(have, want)
}

// subsetOf reports whether every name of want is a name of have: the roster
// rule, which is a set's and not the member list's (narrows).
func subsetOf(have, want []string) bool {
	for _, u := range want {
		if !slices.Contains(have, u) {
			return false
		}
	}
	return true
}

// selectCraftAccess applies a layer's crafts: project is whether it is the
// merge's first and may select among its own, and a later layer selects among
// what the effective set holds.
//
// A later layer's name that is neither a credential nor a pool of the
// effective set is dropped and recorded — a repository may not introduce a
// credential by naming one, which is the half of F12 that is easy to miss: a
// name written where the project defines none is a reference to nothing, and
// taking it would leave a craft whose calls have nowhere to go while the
// policy read as if the project had granted something.
func (e *Effective) selectCraftAccess(access map[string]string, src string, project bool) {
	for _, craft := range slices.Sorted(maps.Keys(access)) {
		name := access[craft]
		if craft == "" || name == "" {
			continue
		}
		cur := e.Craft.Access[craft]
		if cur == name {
			continue
		}
		if !project && !e.craftAccessAvailable(name) {
			key := CraftAccessKey(craft)
			e.Ignored = append(e.Ignored, Ignored{key, name, cur, e.craftSource(key)})
			continue
		}
		if e.Craft.Access == nil {
			e.Craft.Access = make(map[string]string, len(access))
		}
		e.Craft.Access[craft], e.Sources[CraftAccessKey(craft)] = name, src
	}
}

// craftAccessAvailable reports whether name is a credential or a pool the
// project's own layer makes available. The project's credential set is the
// merge's first layer's, so a pool is the only name a later layer ever had a
// hand in, and a narrowed pool keeps its name.
func (e *Effective) craftAccessAvailable(name string) bool {
	if slices.Contains(e.Craft.Credentials, name) {
		return true
	}
	_, ok := e.Craft.Pools[name]
	return ok
}

// narrows reports whether want is a narrowing of have: every name of want is a
// name of have, in the order have holds them (F12, D1628). A member have does
// not hold, a repeat of one, and a pair out of order all fail, which is what
// makes "the relative order kept" a rule of this function rather than a note
// beside it.
func narrows(have, want []string) bool {
	i := 0
	for _, m := range want {
		j := slices.Index(have[i:], m)
		if j < 0 {
			return false
		}
		i += j + 1
	}
	return true
}

// poolStrategyText is a pool's strategy or rule as an Effective.Ignored entry
// renders it: the value in force, which is failover where the layer named none
// (D1632, config.Pool.StrategyOrDefault, routing.Rule.RuleOrDefault) — the
// reading poolStrategyRank's failover case already starts from, for the rule as
// for the strategy (R5, R21).
//
// CraftPool.Strategy keeps the layer's own spelling, but a dropped value is
// read by documents this package does not own — the run's warning, the PR
// body, `fugaro validate`, the console — and an empty effective value would
// ask each of them to know a default of their own: worse, it is how a reader
// that prints one tells a value that lost to another value from a name the
// project makes available nowhere at all (craftSource), which is the one thing
// the entry's effective field answers.
func poolStrategyText(s string) string {
	if s == "" {
		return PoolStrategyFailover
	}
	return s
}

// ruleInForce is what serves a pool: its rule where it names one and its
// strategy otherwise, and failover where it names neither (R5, R21) — the
// resolve config.Pool.RuleOrDefault and routing.Rule.RuleOrDefault make, read
// here on the pool the merge keeps. The rule and the strategy are one scale of
// tightness, so a later layer's value is judged against this and not against
// the member it was written under: judged against `out.Rule` alone, a
// repository's `failover` beside a project's `strategy: round-robin` is dropped
// as a widening although the pool is served round-robin, and its `round-robin`
// is recorded as a value that lost to an effective failover although the pool
// is not served failover.
//
// A pool that names neither member answers "" — the D1632 default — and that
// answer is not a hole: poolStrategyRank ranks it with failover, the tightness
// a pool that names nothing is served at, and poolStrategyText prints it as
// failover.
func ruleInForce(p CraftPool) string {
	if p.Rule != "" {
		return p.Rule
	}
	return p.Strategy
}

// poolStrategyRank is how tight a written strategy or rule is: `ordered` is
// the tightest, because it takes the first listed member that is usable and
// spreads neither spend nor the repository's code past it; failover — and the
// empty string, which is a pool that names none and takes failover (D1632) —
// comes next; round-robin is the looser, because it shares the load across
// every usable member; and a value that is none of the three has no rank at
// all, the answer this package gives a mode it does not know.
//
// It ranks the two members together on purpose: they are one scale of
// tightness (R5, R21), and a pool whose rule and strategy are compared as one
// family is what keeps `ordered` — a rule — judgeable by the same merge that
// judges a strategy. A value is refused where the pool is written
// (config.ValidatePools), so an unranked one only ever comes from the project
// layer, whose pool is the one the merge keeps as it stands: what this rank
// decides is that a later layer's unranked name loses to it, the direction a
// name this release does not know must fail in.
func poolStrategyRank(s string) int {
	switch s {
	case PoolRuleOrdered:
		return 3
	case "", PoolStrategyFailover:
		return 2
	case PoolStrategyRoundRobin:
		return 1
	}
	return 0
}

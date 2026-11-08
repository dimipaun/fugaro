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
// budget mode is this package's ModeOff, its governance block this package's
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
	// Members is the pool's members, in the order it tries them.
	Members []string
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
	// name — the four of config.CraftNames, kept as the layer wrote them: a
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

// craftSource is the layer a refused craft value lost to, as a record reads it:
// the layer that set the effective value where one did, and the project's own —
// the merge's first layer — where none did. The project's set of credentials
// and pools is the whole answer to "may this name be used", so its silence
// about a name is the answer a later layer may not loosen, and it is that layer
// the value was measured against.
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
		e.Craft.Pools[name] = CraftPool{Strategy: p.Strategy, Members: dedupe(p.Members)}
		e.Sources[CraftPoolMembersKey(name)] = src
		if p.Strategy != "" {
			e.Sources[CraftPoolStrategyKey(name)] = src
		}
	}
}

// tightenCraftPool applies a later layer's entry for a pool the project
// defines. A pool it does not define is one the layer may not create: the
// entry is dropped whole, strategy and members together, because a pool's name
// is what a craft's access speaks and a second definition of it would leave
// that name meaning two pools.
//
// The members may only narrow and the strategy may only tighten. Each is
// judged on its own key, so a layer that narrows the list keeps the project's
// strategy and one that pins the order keeps the project's members. A value
// equal to the running one moves nothing and is not recorded (Merge).
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
	if p.Strategy != "" {
		switch rank, running := poolStrategyRank(p.Strategy), poolStrategyRank(out.Strategy); {
		case rank > running:
			out.Strategy, e.Sources[CraftPoolStrategyKey(name)] = p.Strategy, src
		case rank < running:
			e.Ignored = append(e.Ignored, Ignored{CraftPoolStrategyKey(name),
				p.Strategy, poolStrategyText(out.Strategy), e.craftSource(CraftPoolStrategyKey(name))})
		}
	}
	e.Craft.Pools[name] = out
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

// poolStrategyText is a pool's strategy as an Effective.Ignored entry renders
// it: the strategy in force, which is failover where the layer named none
// (D1632, config.Pool.StrategyOrDefault) — the reading poolStrategyRank's
// failover case already starts from.
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

// poolStrategyRank is how tight a written strategy is: failover — and the
// empty string, which is a pool that names none and takes failover (D1632) —
// is the tighter, round-robin the looser, and a value that is neither has no
// rank at all, the answer this package gives a mode it does not know. A
// strategy is refused where the pool is written (config.ValidatePools), so an
// unranked one only ever comes from the project layer, whose pool is the one
// the merge keeps as it stands: what this rank decides is that a later layer's
// unranked name loses to it, the direction a name this release does not know
// must fail in.
func poolStrategyRank(s string) int {
	switch s {
	case "", PoolStrategyFailover:
		return 2
	case PoolStrategyRoundRobin:
		return 1
	}
	return 0
}

// Package routing holds the routing table's own shapes and nothing that
// computes over them.
//
// The table is how a project says where a model is reached (spec.md): an
// ordered list of entries, each naming a model or a pattern and the credential
// sources that may serve it, first usable one wins. Four sides meet on those
// shapes — the service that walks a chain, the run that records which source
// served a step, the console and the watch feed that draw it, and the
// migration that writes a project's first table — so they are declared once,
// here, rather than spelled again per side and drifting.
//
// Nothing here decides anything, and that is deliberate: the resolver is RT1's,
// the table's checks are RT3's, the migration is MG1's and the pool's own
// members are C2's. A shape that also answered a question would be an answer
// none of those tasks could change without changing the contract, which is
// exactly the coupling this package exists to avoid. The one thing that looks
// like logic here, ValidKind, is not a check on a table: it is the closed set
// of kinds stating itself, so that the decoder and the checks that must both
// refuse a sixth kind read one list instead of two.
//
// No member of any shape here is ever a credential value. A Source names a
// kind and, per kind, a pool, a provider or a model; a SourceName is the one
// thing the answer, the record and the console carry. That is what lets a
// table be a document a project publishes where every launcher of the
// installation can read it (R21).
package routing

import "errors"

// Kind is the kind of credential source an entry's chain names. It is a closed
// set (Kinds, ValidKind): the five ways this build reaches a model, and a
// sixth is a change to this file and to every reader's switch, which is where
// the set is written down.
type Kind string

const (
	// KindLauncherPool is the pool of credentials the launcher of this run
	// designated for its principal (D3176, D3179 as amended). A source of this
	// kind names no pool in the table: which pool serves is decided per run, so
	// the table says only that the launcher's own credentials come first.
	KindLauncherPool Kind = "launcher-pool"
	// KindPool is a pool the project names, as C2's pools carry it. A source of
	// this kind names the pool in Pool.
	KindPool Kind = "pool"
	// KindAnthropic is the project's Anthropic key: one credential of the
	// project, so a source of this kind names nothing further.
	KindAnthropic Kind = "anthropic"
	// KindProvider is a provider key of the project (a bundle entry, a
	// provider's own API key). A source of this kind names the provider in
	// Provider.
	KindProvider Kind = "provider"
	// KindVertex is the project's Vertex, reached through the installation's
	// service account and the run's own GCP project. Like the Anthropic key it
	// is one credential of the project, so it names nothing further.
	KindVertex Kind = "vertex"
)

// Kinds is the closed set, in the order spec.md lists it, so a reader that
// validates a table, a settings registry that renders it and a schema that
// enumerates it all read one list.
var Kinds = []Kind{KindLauncherPool, KindPool, KindAnthropic, KindProvider, KindVertex}

// ValidKind reports whether k is one of the five. It is the rule RT2's decoder
// and RT3's checks write by, and the reason a table carrying an unknown kind is
// refused rather than skipped: a kind no side serves is a call nobody can
// place, and skipping it would silently widen the chain.
func ValidKind(k Kind) bool {
	for _, known := range Kinds {
		if k == known {
			return true
		}
	}
	return false
}

// Source is one credential source in an entry's chain: which credential may
// serve this model, and which model it serves when that is not the model the
// call asked for.
//
// Which members are set follows the kind:
//
//	launcher-pool  nothing further: the launcher's designation decides it
//	pool           Pool is the project's pool name
//	anthropic      nothing further: the project has one Anthropic key
//	provider       Provider is the provider's name
//	vertex         nothing further: the project has one Vertex
//
// Model is orthogonal to those and may be set on a source of any kind: it names
// the model this source serves when that is not the model the call asked for —
// a written downgrade, which spec.md requires to be written explicitly ("then
// Sonnet via OpenRouter") because nothing is downgraded silently.
//
// Model is never a spelling. Which model is sent is Model when it is set and the
// requested model otherwise, and how that model is written on the source is the
// checked-in map's answer alone (D3175, pricing.SpellingFor). One authority for
// the spelling is what keeps the walk (CS1), the Vertex translation (CS4) and
// the checks (RT3) from disagreeing about the model id a live call carries: the
// map spells the model this member names, and a model the map does not spell for
// that source is refused at validation (RT3) rather than sent as something
// nobody measured.
//
// A member a kind cannot use — a provider on a pool source, an admit list on a
// pool — is RT2's decoder to refuse rather than something stored and never read.
//
// The yaml tags name the same members as the json ones, and state the same
// options. They are here because the project's own `model_routing:` block is
// this Table and nothing else (RT2, contract-table.md): a second spelling of
// the shape in the config's package would be free to drift from the wire's, and
// omitempty is what keeps a source's unused members out of a document a person
// reads. Reading is unaffected — yaml.v3 already read the lowercased field name
// — so this is one spelling stated twice, held together by
// TestModelRoutingYAMLEqualsTheContract (internal/localcfg).
type Source struct {
	Kind     Kind   `json:"kind" yaml:"kind"`
	Pool     string `json:"pool,omitempty" yaml:"pool,omitempty"`
	Provider string `json:"provider,omitempty" yaml:"provider,omitempty"`
	Model    string `json:"model,omitempty" yaml:"model,omitempty"`
	// Admit is the list of repositories this source may serve (D3182), for the
	// kinds that are a single credential of the project — anthropic, provider
	// and vertex. It is per source and not per kind, because the same provider
	// key may be admitted to one repository and not another.
	//
	// An empty list admits nobody: the fail-closed direction, and the same rule
	// a Claude account's own allow_data_to already has (config.ClaudeAccount.
	// Admits). A source with no repository admitted is inert rather than an
	// error, so a project can publish the shape before it decides the
	// admissions.
	//
	// A pool carries no admit list here. Its admission is its members' own: a
	// subscription account keeps its allow_data_to, and a member the repository
	// is not admitted to is skipped (D3182, R20). RT2's decoder therefore
	// refuses the member on launcher-pool and pool rather than storing
	// something nothing reads.
	Admit []string `json:"admit,omitempty" yaml:"admit,omitempty"`
}

// Entry is one row of the table: the model or pattern it matches, and the chain
// of sources that may serve it in the order they are tried.
type Entry struct {
	// Match is the model or pattern this entry answers (D3172). The most
	// specific match wins, and config.PatternMatches is the one matcher, so
	// this member is a string here and never a compiled expression.
	Match string `json:"match" yaml:"match"`
	// Chain is the sources, first usable one wins (D3180: a source with no
	// designated pool is skipped, not an error).
	Chain []Source `json:"chain" yaml:"chain"`
}

// Table is the ordered list of entries a project publishes. It is a slice and
// not a struct with an entries member so that the project's `model_routing:`
// block is the table itself, which is what a person writing it expects to see.
type Table []Entry

// SourceName is which source served a call, as the answer, the run's record and
// the console carry it: a kind and the name of the one that kind names.
//
// It is never a value: for a pool it is the pool's name, for a provider the
// provider's, and for the two kinds that are a single credential of the project
// — anthropic and vertex — the name is empty, because there is nothing to
// distinguish and a name invented to fill the member would be a fact nobody
// checked.
//
// Name is not omitempty: the member is always written, so a reader sees both
// halves of the shape and never has to decide what an absent name means.
type SourceName struct {
	Kind Kind   `json:"kind"`
	Name string `json:"name"`
}

// ResolveFunc is the resolver's signature: the entry a model is routed by, or
// an error. RT1 supplies the body; this declaration is here so that both sides
// of the contract can be written and compiled before it does.
type ResolveFunc func(table Table, model string) (Entry, error)

// Resolve answers the entry that routes model in table: the most specific
// matching entry wins (D3172), and a model no entry matches is ErrNoEntry.
//
// A caller must not invent a chain when Resolve fails: a model with no entry is
// a model this project has not said how to reach, and the fail-closed direction
// — refuse the call — is the point of the table. Falling back to "whatever
// credential is around" is the behaviour the table replaces.
//
// It is a variable because the contract ships before the resolver: C1 declares
// it and a stub answers the first entry (resolve_stub.go), RT1 assigns the real
// one, and I1 deletes the stub. The stub's file sorts after the real
// resolver's, so the real assignment wins.
var Resolve ResolveFunc

// ErrNoEntry is the answer for a model no entry of the table matches. It is a
// sentinel so that a caller can tell "this project routes no such model" from
// "the table could not be read", which are different refusals with different
// fixes.
var ErrNoEntry = errors.New("routing: no entry of the table matches this model")

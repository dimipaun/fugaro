package config

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/policy"
)

// A craft is what the spec calls a model role: one model and one credential or
// pool, per job role (docs/model-crafts/spec.md §3). The word is the owner's
// (reconciliation D1620): "role" already means a person's permissions here
// (docs/roles-scopes) and config.Role already means the stage a call belongs
// to, so neither could name this.
//
// Nothing here decides anything at launch: these are the keys a configuration
// layer writes and the rules a credential, a pool and a craft are held to. The
// resolver that turns the layers into one answer per craft is internal/craft
// (reconciliation MC2), and the merge that keeps a repository from loosening
// the project's is internal/policy (MC3).
const (
	// CraftCoder is the craft a run cannot do without: implement and fix.
	CraftCoder = "coder"
	// CraftReview is review level 1, the first-line review.
	CraftReview = "review"
	// CraftSeniorReview is review level 2, the senior review.
	CraftSeniorReview = "senior-review"
	// CraftSecurityReview is review level 3, the adversarial review. It stays
	// optional, and no craft key turns it on: the switch is the review levels'
	// (F14).
	CraftSecurityReview = "security-review"
	// CraftMergeJudge is the merge judge (E6, D2031): the stage a judge run
	// charges its one call to. It follows the coder, and both halves of a craft
	// have to answer for one model: the judge stage runs the coder's role slot
	// (config.StageRole knows no merge_judge case, and the ladder's grade is
	// written into the coder's slot, E4), so a judge that took the senior
	// review's access while running the coder's model would be served by a pool
	// whose members are judged against a model it does not run (F23,
	// poolMember) — a project whose senior review goes out on a pool of its own
	// would then refuse every judge stage, and a refused judge is a veto on
	// every head.
	CraftMergeJudge = "merge-judge"
)

// CraftNames are the five crafts, in the order a reader meets them.
var CraftNames = []string{CraftCoder, CraftReview, CraftSeniorReview, CraftSecurityReview, CraftMergeJudge}

// craftParent is the craft a craft follows when it sets no model or access of
// its own: the first-line review and the merge judge follow the coder, and the
// security review follows the senior review. The coder and the senior review
// follow nobody — the spec makes both required — and a craft no key of this
// table names is no craft at all, so an unknown name inherits nothing.
var craftParent = map[string]string{
	CraftReview:         CraftCoder,
	CraftSecurityReview: CraftSeniorReview,
	CraftMergeJudge:     CraftCoder,
}

// CraftParent is the craft name follows: the craft whose model and access it
// takes where it sets none of its own. It is "" for the coder and the senior
// review, which the spec makes required — and for a name that is no craft, so a
// caller asking about one inherits nothing by mistake.
//
// It is exported because a reader of the resolution has to say which craft a
// half came from ("inherited from coder"), and a second copy of this chain in
// internal/craft (the resolver, MC2) would be a second rule: this one and
// Crafts.Inherited walk the same map.
func CraftParent(name string) string { return craftParent[name] }

// Craft is one craft's two halves, each set or inherited on its own: the model
// a stage runs and the credential or pool its calls go out on (spec §3). An
// empty half is unset, and what an unset half finally resolves to — the
// project's own default credential — is not this block's business.
type Craft struct {
	// Model is the model ID this craft runs, "" for the craft it inherits from.
	Model string `yaml:"model,omitempty" json:"model,omitempty"`
	// Access names the credential or the pool this craft's calls go out on, ""
	// for the craft it inherits from.
	Access string `yaml:"access,omitempty" json:"access,omitempty"`
}

// Crafts is fugaro.yaml's `agent.crafts` and the project layer's
// `defaults.agent.crafts`: the crafts by name.
//
// It is a struct with a field per craft rather than a map for config.ModelRoles'
// reason — the crafts are a closed set, so a file that misspells one is
// refused by the strict decoder instead of quietly setting nothing — and each
// field is a block rather than a scalar because a craft sets its model and its
// access independently.
type Crafts struct {
	Coder          Craft `yaml:"coder,omitempty" json:"coder,omitempty"`
	Review         Craft `yaml:"review,omitempty" json:"review,omitempty"`
	SeniorReview   Craft `yaml:"senior-review,omitempty" json:"senior-review,omitempty"`
	SecurityReview Craft `yaml:"security-review,omitempty" json:"security-review,omitempty"`
	MergeJudge     Craft `yaml:"merge-judge,omitempty" json:"merge-judge,omitempty"`
}

// Of is the craft name's own settings, with nothing inherited: the zero Craft
// for a name that is no craft.
func (c Crafts) Of(name string) Craft {
	switch name {
	case CraftCoder:
		return c.Coder
	case CraftReview:
		return c.Review
	case CraftSeniorReview:
		return c.SeniorReview
	case CraftSecurityReview:
		return c.SecurityReview
	case CraftMergeJudge:
		return c.MergeJudge
	}
	return Craft{}
}

// Inherited is the craft name's model and access with inheritance applied
// inside one block: each half the craft does not set is the half it inherits,
// model and access separately, so a craft may change only its model and keep
// the access it follows (spec §3's own example, the security review).
//
// Inheritance is a chain, not a lookup of one parent: a security review that
// sets nothing is the senior review's settings whether the senior review set
// them itself or took them from nobody at all — the senior review follows no
// craft, so the chain ends there. What a block that sets neither half resolves
// to is the project's own default credential, which no layer's craft block can
// answer (D1624): this returns the empty Craft and the resolver supplies it.
//
// It answers about ONE block. A craft whose model is set in the project layer
// and whose access is set in the repository's file inherits across the layers,
// which is the resolver's business (MC2) and not this function's.
func (c Crafts) Inherited(name string) Craft {
	// A name that is no craft inherits nothing: it is not the coder by
	// default, or a misspelled craft would quietly take the coder's settings.
	if !slices.Contains(CraftNames, name) {
		return Craft{}
	}
	out := c.Of(name)
	for parent := craftParent[name]; parent != ""; parent = craftParent[parent] {
		p := c.Of(parent)
		if out.Model == "" {
			out.Model = p.Model
		}
		if out.Access == "" {
			out.Access = p.Access
		}
		if out.Model != "" && out.Access != "" {
			break
		}
	}
	return out
}

// Credential types (docs/model-crafts/spec.md §1). The first three are
// definable: a named entry whose secret the installation stores. Vertex is
// built in (CredentialTypeVertex).
const (
	CredentialTypeOpenRouter  = "openrouter"
	CredentialTypeAnthropic   = "anthropic"
	CredentialTypeClaudeOAuth = "claude-oauth"
	CredentialTypeVertex      = "vertex"
)

// The two types the spec lists as future work: an OpenAI API key and a Codex
// OAuth credential. They refuse in words that say so (D1623). The harness is
// Claude Code, which speaks the Anthropic Messages API, and the gateway never
// rewrites a request body — so a credential of either type could not drive a
// run at all, and they need a design of their own (a translating gateway or a
// second harness) rather than being accepted here and failing in a job.
const (
	CredentialTypeOpenAI     = "openai"
	CredentialTypeCodexOAuth = "codex-oauth"
)

// CredentialVertex is the built-in credential's name: type vertex, no secret,
// present in every installation (D1624). It carries its project and region
// from the project config, and a craft names it like any other credential —
// including as a pool's last fallback.
const CredentialVertex = "vertex"

// DefaultCredential is what a craft that sets no access anywhere resolves to:
// the project's fallback credential, which is vertex today. It is a separate
// name on purpose (D1624): a later change of the default then never changes
// what the credential called `vertex` means.
const DefaultCredential = CredentialVertex

// Credential is one named credential: a name a craft's access speaks and the
// type of the provider that serves it. The value it authenticates with is
// never here — it is a secret, stored under a name derived from this one
// (spec §1).
type Credential struct {
	Name string `yaml:"name" json:"name"`
	// Type is one of the types this release ships. Kept as a string rather
	// than an enum because the refusal of a type that does not ship yet is a
	// sentence an operator acts on (ValidateCredentials), not a decode error.
	Type string `yaml:"type" json:"type"`
}

// Pool strategies (spec §2). Failover is the default: it is the shape that
// exists today and it hides nothing about which credential served (D1632).
//
// The two names live in internal/policy, beside the rule that orders them —
// failover is the tighter of the two for a repository that narrows a pool
// (F12, D1628) — and are aliased here the way this file aliases that package's
// budget modes, so a strategy written in a file and a strategy the merge
// compares cannot be two vocabularies.
const (
	PoolStrategyFailover   = policy.PoolStrategyFailover
	PoolStrategyRoundRobin = policy.PoolStrategyRoundRobin
)

// PoolStrategies are the strategies a pool may be written with.
var PoolStrategies = []string{PoolStrategyFailover, PoolStrategyRoundRobin}

// Pool is one named, ordered list of credentials with a strategy: failover
// tries them in order and moves to the next when one is exhausted, round-robin
// shares the load and skips a member that fails (spec §2).
type Pool struct {
	Name string `yaml:"name" json:"name"`
	// Strategy is a value of PoolStrategies; "" is failover.
	Strategy string `yaml:"strategy,omitempty" json:"strategy,omitempty"`
	// Members are the credentials' names, in the order the pool tries them.
	// The order is meaningful for both strategies — it is what failover walks
	// and what round-robin starts from — so it is a list and never a set.
	Members []string `yaml:"members" json:"members"`

	// Owner is the pool's owner, spelled by email (D1936, R10): the person a
	// slip naming this pool is served to, and the pool's administrator. It is
	// the roster's key, not a `sub` — the roster is keyed by email, and the
	// service resolves it to the `sub` of the person asking for a slip at
	// grant time — so a value here is a person's address and never a secret.
	Owner string `yaml:"owner,omitempty" json:"owner,omitempty"`
	// AllowedUsers are the other people the pool is served to, by email, in
	// the same vocabulary as Owner. Empty is D1936's default: the owner alone,
	// which is what a pool of one person's own tokens is (R11 — a per-user
	// token is never lent, and sharing is a project secret under a pool's
	// allowed users).
	AllowedUsers []string `yaml:"allowed_users,omitempty" json:"allowed_users,omitempty"`
	// LegacyOpen marks a pool that existed before pools carried an owner: the
	// migration of a published node writes it (D1942), so a pool an
	// installation already holds is not read as one whose owner is still to be
	// named. It is a
	// NEW NAME and not the absence of one: a pool written now must name its
	// owner where it is published (ValidatePublishedPools), so "no owner" and
	// "open to everyone" cannot be the same piece of text (F37).
	LegacyOpen bool `yaml:"legacy_open,omitempty" json:"legacy_open,omitempty"`
	// Cascade marks the pool as the cascade's (D3064 as amended): it is the
	// marker `fugaro update` writes for the pool it migrates out of a project
	// whose cascade serves, whose members are that cascade's credentials in the
	// order the cascade tries them (docs/credentials-belong-to-models).
	//
	// Only that write may carry it, so it is refused anywhere it could not be
	// the migration's: ValidateCascadePools refuses it where the configuration
	// holding it turns no cascade on, and refuses an owner or an allowed user
	// written beside it. What it buys the pool is the cascade's own reach — its
	// subscriptions are served although the subscription-spread switch is off,
	// exactly as the cascade serves them today (D1622) — which is why a key an
	// operator wrote may not claim it, and why the pool is nobody's in
	// particular: an ordinary pool of subscriptions is bounded by that switch
	// and by its owner, and this one is bounded by neither.
	//
	// It never reaches the published node, where the pool is an ordinary
	// legacy_open one (publishedCraftPools, R15, F36): no reader of the node
	// sees it. What reads it is the launcher (CM14) and the access record the
	// service reads (C1, CM5, CM36) — the two places a marker written in the
	// installation's own config is meant to travel to.
	Cascade bool `yaml:"cascade,omitempty" json:"cascade,omitempty"`
}

// poolEmailRE is the shape a pool's owner and its allowed users are held to:
// a person's email address, which is the roster's key (D1940, R10). It is
// deliberately loose — the roster decides who exists, and this refuses only a
// value no address could be — and it is the local config's own rule for a
// service account, this package having no second vocabulary for an address.
var poolEmailRE = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// Serves reports whether the pool is served to the person with this verified
// email address, and it is where D1936's two defaults live: the owner alone
// where the pool names no allowed users, the owner and the allowed users where
// it names some, and everyone where the pool carries the migration's marker
// (D1942, LegacyOpen).
//
// An ownerless pool that is not legacy-open serves nobody. That is the state a
// pool is in while it is being written — this package does not refuse it in an
// installation's own config (ValidatePools' comment says why) — and fail
// closed is the direction a pool with no one answerable for it must fail in.
func (p Pool) Serves(email string) bool {
	if p.LegacyOpen {
		return true
	}
	if email == "" {
		return false
	}
	return email == p.Owner || slices.Contains(p.AllowedUsers, email)
}

// StrategyOrDefault is the pool's strategy with failover applied: a pool that
// names none is an ordered failover pool, which is what D1632 answers for a
// new pool and what the older, implicit account list already was.
func (p Pool) StrategyOrDefault() string {
	if p.Strategy == "" {
		return PoolStrategyFailover
	}
	return p.Strategy
}

// BuiltinCredentials are the credentials every installation has without
// defining one (D1624): the vertex credential, which needs no secret.
func BuiltinCredentials() []Credential {
	return []Credential{{Name: CredentialVertex, Type: CredentialTypeVertex}}
}

// ValidCredentialRef checks a name written where a credential is REFERENCED —
// a craft's access, a pool's member — and returns "" for a valid one, else the
// sentence saying what is wrong with it. It is the shape rule only: whether a
// project defines the name is answered by the block that would have to hold
// it, where that block is in hand.
//
// The built-in vertex is valid here and nowhere else: an installation states it
// and never defines it (ValidateCredentials), the credential under it being the
// platform's own rather than one this file derives.
//
// The message never echoes the name: a value that fails this check reached it
// exactly because it is untrusted text, and the sentence is printed to a
// terminal (the same treatment ValidClaudeAccountRef gives one).
func ValidCredentialRef(name string) string {
	switch {
	case name == "":
		return `a credential name must not be empty: name the credential or pool this craft's calls go out on, or drop the key to inherit it`
	case name == CredentialVertex:
		return ""
	case name == DefaultClaudeAccount:
		return `"default" is not a credential's name: a craft that sets no access resolves to the project's default credential, so an entry under this name could never be reached — drop the key to inherit, or name a credential`
	}
	return credentialNameProblem(name)
}

// credentialNameProblem is the name rule ValidateCredentials and ValidatePools
// both answer a DEFINITION with, so a credential and a pool of one name cannot
// be held to two shapes: a name is a ClaudeAccountNameRE name — the same shape
// an account's name has, because a credential generalises one (D1621) — and it
// is not one of the names the platform already holds a credential under.
//
// The reserved names are refused here and admitted by ValidCredentialRef where
// that is the right answer for a reference: `vertex` is the built-in
// credential, so an entry of that name would be a second credential no run
// could tell it apart from; `default` is what a craft that sets no access
// resolves to, so a definition of that name is an entry nothing can reach (it is
// refused as a definition for the reason claude_accounts refuses it: the
// reference and the definition cannot mean two things); and a name ending in
// -review would derive the review-only job variant's own job name and service
// account for a credential that mounts the coding job's key
// (reviewAccountNameMessage).
func credentialNameProblem(name string) string {
	switch {
	case name == "":
		return "name is required: this is the name a craft's access and a pool's members speak, and an entry without one names nothing"
	case name == DefaultClaudeAccount:
		return `"` + DefaultClaudeAccount + `" is the default credential rather than a name for one: a craft that sets no access resolves to it already, so an entry under this name could never be reached — name the credential, or drop the entry`
	case name == CredentialVertex:
		return `"` + CredentialVertex + `" is the built-in credential: it needs no secret and exists in every installation, so an entry under this name would name a second credential no run could tell apart — pick a different name`
	case name == AnthropicKeyAccount:
		return anthropicKeyAccountNameMessage
	case reservedClaudeAccountName(name):
		return reservedAccountNameMessage
	case len(name) > MaxClaudeAccountNameLen:
		return fmt.Sprintf("name must be at most %d characters", MaxClaudeAccountNameLen)
	case !ClaudeAccountNameRE.MatchString(name):
		return fmt.Sprintf("name must match %s", ClaudeAccountNameRE)
	case strings.HasSuffix(name, ReviewVariantSuffix):
		return reviewAccountNameMessage
	}
	return ""
}

// ValidateCredentials reports every rule a credentials block breaks, the same
// shape as ValidateClaudeAccounts. creds is a document's own block: a name it
// repeats is refused here, because a list holds one entry per name and a
// second entry of one name would leave `credentials.<name>` meaning two
// credentials.
//
// The type is checked against the types this release ships, and the two the
// spec lists as future work refuse in words that name them and say they are
// not yet supported rather than as an unknown type: an operator who wrote one
// has not made a typo, and the answer they need is that the type is coming
// (D1623, F3).
func ValidateCredentials(creds []Credential) []Problem {
	var ps []Problem
	seen := map[string]bool{}
	for i, c := range creds {
		at := fmt.Sprintf("credentials[%d]", i)
		if c.Name != "" {
			at = "credentials." + pluginwire.Printable(c.Name)
		}
		if msg := credentialNameProblem(c.Name); msg != "" {
			ps = append(ps, Problem{Path: at, Message: msg})
		} else if seen[c.Name] {
			ps = append(ps, Problem{Path: at, Message: "this name is defined twice; a credential is named once per block"})
		}
		seen[c.Name] = true
		if msg := credentialTypeProblem(c.Type); msg != "" {
			ps = append(ps, Problem{Path: at + ".type", Message: msg})
		}
	}
	return ps
}

// credentialTypeProblem is the type rule, spelled once so a credential
// definition and a pool's member cannot disagree about what a type is.
func credentialTypeProblem(typ string) string {
	switch typ {
	case CredentialTypeOpenRouter, CredentialTypeAnthropic, CredentialTypeClaudeOAuth:
		return ""
	case CredentialTypeVertex:
		return `vertex is built in: it needs no secret and exists in every installation, so it is never defined as an entry — reference it by name in a craft or a pool`
	case CredentialTypeOpenAI, CredentialTypeCodexOAuth:
		return fmt.Sprintf("%s is not yet supported: Claude Code speaks the Anthropic Messages API and the gateway never rewrites a request body, so a credential of this type could not serve a run — the types this release ships are %s", typ, strings.Join(SupportedCredentialTypes(), ", "))
	case "":
		return fmt.Sprintf("type is required: name the provider this credential authenticates with, one of %s", strings.Join(SupportedCredentialTypes(), ", "))
	}
	return fmt.Sprintf("must be one of %s", strings.Join(SupportedCredentialTypes(), ", "))
}

// SupportedCredentialTypes are the types a credential may be DEFINED with: the
// three that need a secret. Vertex is in the spec's list and is not here,
// because no installation defines it (CredentialVertex), and the two the spec
// calls future work are not either: a registry row that printed this list as a
// default would otherwise offer a type ValidateCredentials refuses (D1623).
func SupportedCredentialTypes() []string {
	return []string{CredentialTypeOpenRouter, CredentialTypeAnthropic, CredentialTypeClaudeOAuth}
}

// KeyCredentialType reports whether a credential of this type authenticates with
// a key the installation stores under the credential's own derived secret name
// (CredentialSecret): the two types whose key a call sends to a provider's API.
// claude-oauth is not one — its token is a named Claude account's, stored under
// that account's own name — and neither is the built-in vertex, which carries no
// key at all.
//
// It is the one place "which type's value lives under CredentialSecret" is
// decided, so the command that stores such a value (fugaro accounts add --type),
// the store path's own eligibility rules and anything that reads the value back
// cannot name two containers for one credential.
func KeyCredentialType(typ string) bool {
	return typ == CredentialTypeOpenRouter || typ == CredentialTypeAnthropic
}

// CredentialNamed is the credential of creds named name, and whether the block
// defines one. A block that defines two entries under one name cannot reach a
// caller (ValidateCredentials refuses it), so the first is the answer.
func CredentialNamed(creds []Credential, name string) (Credential, bool) {
	for _, c := range creds {
		if c.Name == name {
			return c, true
		}
	}
	return Credential{}, false
}

// ValidatePools reports every rule a pools block breaks. creds is the
// credentials block of the same document: a pool's members name credentials of
// it, or the built-in vertex, so a pool that names a credential the document
// does not define is refused here rather than at a run, where the name would
// quietly match nothing.
//
// A pool and a credential share one namespace, because a craft's access names
// either without saying which: a pool whose name is a credential's would leave
// that name meaning two things.
func ValidatePools(pools []Pool, creds []Credential) []Problem {
	var ps []Problem
	defined := definedCredentialNames(creds)
	poolNames := map[string]bool{}
	for i, p := range pools {
		at := fmt.Sprintf("pools[%d]", i)
		if p.Name != "" {
			at = "pools." + pluginwire.Printable(p.Name)
		}
		switch {
		case p.Name != "" && poolNames[p.Name]:
			ps = append(ps, Problem{Path: at, Message: "this name is defined twice; a pool is named once per block"})
		case p.Name != "" && defined[p.Name]:
			ps = append(ps, Problem{Path: at, Message: "a credential of this name is defined in the same block: a craft's access names a credential or a pool and does not say which, so one name may mean only one of them — rename the pool or the credential"})
		default:
			if msg := credentialNameProblem(p.Name); msg != "" {
				ps = append(ps, Problem{Path: at, Message: msg})
			}
		}
		poolNames[p.Name] = true
		if p.Strategy != "" && !slices.Contains(PoolStrategies, p.Strategy) {
			ps = append(ps, Problem{Path: at + ".strategy", Message: fmt.Sprintf("must be one of %s", strings.Join(PoolStrategies, ", "))})
		}
		if len(p.Members) == 0 {
			ps = append(ps, Problem{Path: at + ".members", Message: "a pool with no members serves nothing: list at least one credential"})
		}
		seen := map[string]bool{}
		for j, m := range p.Members {
			mAt := fmt.Sprintf("%s.members[%d]", at, j)
			if msg := ValidCredentialRef(m); msg != "" {
				ps = append(ps, Problem{Path: mAt, Message: msg})
				continue
			}
			if seen[m] {
				ps = append(ps, Problem{Path: mAt, Message: "this credential is listed twice; a pool lists each member once"})
				continue
			}
			seen[m] = true
			if !defined[m] {
				ps = append(ps, Problem{Path: mAt, Message: fmt.Sprintf("no credential of this name is defined in this block: a pool may list only the credentials the same document defines, or the built-in %s", CredentialVertex)})
			}
		}
		ps = append(ps, poolAccessProblems(at, p)...)
	}
	return ps
}

// ValidateCascadePools reports the rules a pool carrying the cascade's marker
// is held to in a configuration: it is refused where the same document turns no
// cascade on, and it names neither an owner nor allowed users.
//
// The two rules say one thing. The marker is what `fugaro update` writes for the
// pool it migrates out of a project whose cascade serves (D3064 as amended), and
// a pool nobody's migration wrote is a pool whose marker would be a false claim:
// its subscriptions would be served with the subscription-spread switch off
// (D1622) on the strength of a key an operator wrote, which is the widening the
// switch exists to prevent. The switch that says a cascade serves is the
// routing's (RoutingServesTheCascade) — the same statement `fugaro run` asks
// before it launches a run of the project — and it is in this file and not in
// ValidatePools because the routing is not a document a node read holds: a
// published node's pools never carry the marker anyway (publishedCraftPools
// clears it, R15), and a rule there would be a rule about a document that
// cannot state it.
//
// A marker is not an owner's either: the pool is served as the cascade serves,
// not to a person, which is why the publisher writes it as an ordinary
// legacy_open pool and asks for no owner (F36). An owner written beside the
// marker is refused rather than dropped, the same way poolAccessProblems
// refuses one beside legacy_open (D1942): the publish would otherwise write a
// pool whose owner nothing reads.
func ValidateCascadePools(pools []Pool, routing *Routing) []Problem {
	var ps []Problem
	serves := RoutingServesTheCascade(routing)
	for i, p := range pools {
		if !p.Cascade {
			continue
		}
		at := fmt.Sprintf("pools[%d]", i)
		if p.Name != "" {
			at = "pools." + pluginwire.Printable(p.Name)
		}
		if !serves {
			ps = append(ps, Problem{Path: at + ".cascade", Message: fmt.Sprintf("a pool carries the cascade's marker only where the same configuration turns the cascade on, and this one does not: the marker is what `fugaro update` writes for the pool it migrates out of a cascade that serves (D3064), because that pool's subscriptions are served with the subscription-spread switch off exactly as the cascade serves them — turn the cascade on (routing.claude.via: %s), or drop the key and let the pool obey the switch like any other", RouteCascade)})
			continue
		}
		if p.Owner != "" {
			ps = append(ps, Problem{Path: at + ".owner", Message: "a pool marked as the cascade's has no owner: it is served as the cascade serves rather than to the people a pool names, and it is published as an ordinary legacy_open pool, which names neither an owner nor allowed users (D3064, D1942) — drop the key, or drop cascade and name the pool's owner"})
		}
		if len(p.AllowedUsers) > 0 {
			ps = append(ps, Problem{Path: at + ".allowed_users", Message: "a pool marked as the cascade's names no allowed users: it is served as the cascade serves rather than to the people a pool names, and it is published as an ordinary legacy_open pool, which names neither an owner nor allowed users (D3064, D1942) — drop the key, or drop cascade and name the pool's owner and the users it is for"})
		}
	}
	return ps
}

// RoutingServesTheCascade reports whether a routing block turns the cascade on
// for the project: `routing.claude.via: cascade` (D133, D150), the switch that
// admits a run of the project to the cascade at all. An absent block, and every
// other via, serve no cascade — an absent switch is the direct route (D1b).
//
// It is one function because three readers ask it and may not drift: the config
// loader's own rules (ValidateCascadePools), `fugaro project routing` and the
// publisher's refusal to drop the switch from the node in silence
// (routingTurnsTheCascadeOn, nodeTurnsTheCascadeOn).
func RoutingServesTheCascade(r *Routing) bool {
	return r != nil && r.Claude != nil && r.Claude.ViaValue() == RouteCascade
}

// ValidatePoolPaths reports D1668's rule for the pools a command is about to
// WRITE: every member of a pool is served by one half of a run, so a member
// list naming both is refused rather than written, its sentence naming the two
// members, the half each is served by and the fix. `fugaro accounts pool add` is
// its one caller, and it hands it the pool it would write alone rather than the
// block it would write it into: a pool that already exists is not this
// function's business — poolCrossingProblem says why the config's own load
// leaves one to the split that converges it — and a crossing an earlier release
// wrote elsewhere in the block must not stand in the way of the next
// declaration.
func ValidatePoolPaths(pools []Pool, creds []Credential) []Problem {
	return pathCrossingProblems(pools, creds)
}

// pathCrossingProblems is D1668's rule, applied to a whole block: the two doors
// that hold it — the one a pool is WRITTEN through (ValidatePoolPaths) and the
// one a PUBLISHED node is read through (ValidatePublishedPools) — ask it here so
// the sentence and the path it is reported at cannot be two.
func pathCrossingProblems(pools []Pool, creds []Credential) []Problem {
	defined := definedCredentialNames(creds)
	var ps []Problem
	for i, p := range pools {
		at := fmt.Sprintf("pools[%d]", i)
		if p.Name != "" {
			at = "pools." + pluginwire.Printable(p.Name)
		}
		if prob, ok := poolCrossingProblem(at, p, creds, defined); ok {
			ps = append(ps, prob)
		}
	}
	return ps
}

// definedCredentialNames is the names a pool may list: the credentials block of
// the same document, and the built-in vertex.
func definedCredentialNames(creds []Credential) map[string]bool {
	defined := map[string]bool{CredentialVertex: true}
	for _, c := range creds {
		if c.Name != "" {
			defined[c.Name] = true
		}
	}
	return defined
}

// poolCrossingProblem is D1668's rule: every member of a pool is served by one
// path. Its listed order is what failover walks (D1632), and only one half of a
// run can walk a list — the credentials service selects by tier and the listed
// order only breaks a tie (F7, D154), while a run's own gateway holds one
// upstream per stage and never retries a call across a secret (F20, D1635) — so
// a pool that names members of both halves is a pool whose order can mean
// nothing, and it is refused where it is written rather than resolved to one of
// them. It is refused rather than failed over inside a stage because the fix
// for that is a rewrite of the gateway's one-upstream-per-stage model, bought
// for a case two pools express exactly.
//
// It is the same one sentence wherever it is said — `fugaro accounts pool add`,
// and the read of a published node — because the operator meets it at whichever
// door a pool was written through, and it has to name the fix there.
//
// The two members the sentence names are named in path order, the credentials
// service's first, so it reads the same whichever way the pool listed them, and
// the path it is reported at is where the pool's own order crosses: the first
// member whose half is not the half the first member is on, which is the pair
// the operator has to separate.
//
// A member this document does not define is left out of the crossing: the
// member's own problem reports it, and a name no layer places is not a half
// either. The built-in vertex is not one of those — being built in is what
// defines it — so a subscription beside vertex is refused like any other
// crossing.
//
// It is the one rule of this file that a pool an earlier release wrote cannot
// satisfy, so it is not one of the rules the READERS of a pool hold: 0.25.0's
// `accounts pool add` wrote a pool naming both halves and its `accounts pool ls`
// read it as valid, so a config or a node that carries one is a document no
// validation can repair, and refusing it at a read would leave an installation
// that cannot read its own config — the broken installation CLAUDE.md's HARD
// RULE forbids, `fugaro update` reading the project config through the same
// load. A pool that already crosses is therefore CONVERGED and not refused
// (D1668, D1673): `fugaro update` splits it in the installation's own config
// (internal/cli's derivePathPools, on every write of that file) and in the
// published node, through a step of the policy chain
// (internal/projectpolicy's migrateV8ToV9, which is the version bump this rule
// ships with). The rule itself is held at two doors, and the two are the
// activation plan's MC20 and MC21 together: the door a NEW pool is written
// through (ValidatePoolPaths, whose one caller is `fugaro accounts pool add`)
// and the read of a PUBLISHED node (ValidatePublishedPools, which
// projectpolicy.Decode runs), so a node that already carries a crossing is
// refused by the runner's Read, the credentials service's re-read, `doctor` and
// `update` itself rather than served to a selector whose listed order it cannot
// honour.
//
// It is deliberately NOT a rule of ValidatePools, which is the load of an
// installation's own config: localcfg's validate asks it on every read of that
// file, so a rule there would make `fugaro update` unable to read the very
// config its own split is written into — the installation that cannot start,
// for the length of the update. The config side is protected by the door a new
// pool is written through and converged by derivePathPools instead.
func poolCrossingProblem(at string, p Pool, creds []Credential, defined map[string]bool) (Problem, bool) {
	members := PoolMembers(p, creds)
	credsvc, runner, crossed, first := -1, -1, -1, ""
	for i, m := range members {
		if m.Path == "" || !defined[m.Name] {
			continue
		}
		if first == "" {
			first = m.Path
		} else if crossed < 0 && m.Path != first {
			crossed = i
		}
		if m.Path == CredentialPathCredsvc && credsvc < 0 {
			credsvc = i
		}
		if m.Path == CredentialPathRunner && runner < 0 {
			runner = i
		}
	}
	if credsvc < 0 || runner < 0 || crossed < 0 {
		return Problem{}, false
	}
	svc, run := pluginwire.Printable(members[credsvc].Name), pluginwire.Printable(members[runner].Name)
	return Problem{
		Path: fmt.Sprintf("%s.members[%d]", at, crossed),
		Message: fmt.Sprintf("members %s and %s are served by different halves of a run (%s's type is served by the credentials service, %s's by the run's own gateway), and a pool's order can therefore mean nothing: name two pools, one per half, and let each craft's access name the one its stages need",
			svc, run, svc, run),
	}, true
}

// poolAccessProblems reports the rules a pool's owner block is held to wherever
// a pool is written: the owner and the allowed users are email addresses, the
// list holds each of them once, and a pool is either owned or carries the
// migration's marker and never both (D1936, D1942).
//
// Whether an owner is REQUIRED is not one of them: that is a rule of the
// published policy node alone (ValidatePublishedPools), because an
// installation's own config is not a thing `fugaro update` may rewrite — a
// config an upgrade starts refusing is a broken installation, which CLAUDE.md's
// HARD RULE forbids — and what a pool in it serves is not decided until the
// pool is published.
func poolAccessProblems(at string, p Pool) []Problem {
	var ps []Problem
	if p.LegacyOpen {
		if p.Owner != "" {
			ps = append(ps, Problem{Path: at + ".owner", Message: `a pool marked legacy_open has no owner: the marker is what the migration writes for a pool that predates owners (D1942), and it means everyone the pool served before still is — drop legacy_open and name the owner instead`})
		}
		if len(p.AllowedUsers) > 0 {
			ps = append(ps, Problem{Path: at + ".allowed_users", Message: `a pool marked legacy_open names no allowed users: the marker is what the migration writes for a pool that predates owners, and such a pool is already served to everyone — drop legacy_open, name the owner and the users the pool is for`})
		}
		return ps
	}
	if msg := poolEmailProblem(p.Owner); msg != "" {
		ps = append(ps, Problem{Path: at + ".owner", Message: msg})
	}
	seen := map[string]bool{}
	for j, u := range p.AllowedUsers {
		uAt := fmt.Sprintf("%s.allowed_users[%d]", at, j)
		if msg := poolEmailProblem(u); msg != "" {
			ps = append(ps, Problem{Path: uAt, Message: msg})
			continue
		}
		if seen[u] {
			ps = append(ps, Problem{Path: uAt, Message: "this address is listed twice; a pool lists each allowed user once"})
			continue
		}
		seen[u] = true
	}
	return ps
}

// poolEmailProblem is the shape rule the owner and the allowed users are held
// to, "" for a value that is an address and for the empty owner of a pool that
// names none (ValidatePublishedPools is what requires one). The message never
// echoes the value: it is untrusted text, the same treatment ValidCredentialRef
// gives a name.
func poolEmailProblem(email string) string {
	if email == "" || poolEmailRE.MatchString(email) {
		return ""
	}
	return "must be an email address: a pool's owner and its allowed users are people in the project's roster, which is keyed by email, and the credentials service resolves the address to the account it signs in with"
}

// ValidatePublishedPools reports every rule a policy node's craft_pools block
// breaks: ValidatePools' rules, D1668's one-path rule (pathCrossingProblems,
// which the node's own version-8 step converges a crossing pool with before this
// read ever sees one) and the two only a PUBLISHED pool is held to — it names an
// owner, or it carries the marker the migration wrote (D1936, D1942, F37), and
// it carries no marker of the cascade's, which is the installation's alone
// (D3064 as amended, R15).
//
// The two functions exist because the two documents are not the same thing. The
// installation's own config is the owner's own pools and survives an upgrade
// untouched; the published node is what every run of the project resolves its
// credentials through, and there a pool nobody owns would be a pool nobody
// answers for. `fugaro project policy publish` reads the node it is about to
// replace (publishedPolicyOf) and refuses it here, so a node carrying such a
// pool is refused rather than served.
func ValidatePublishedPools(pools []Pool, creds []Credential) []Problem {
	ps := ValidatePools(pools, creds)
	ps = append(ps, pathCrossingProblems(pools, creds)...)
	for _, p := range pools {
		at := fmt.Sprintf("pools.%s", pluginwire.Printable(p.Name))
		// The cascade's marker is the installation's own and the node never
		// carries it (R15, D3064 as amended): the node's readers are job images
		// and the service, and the pool they are served is the ordinary
		// legacy_open one the publisher writes (publishedCraftPools). The key is
		// refused here rather than ignored because ignoring it is how a node
		// written by hand would keep a marker this reader then serves by a rule
		// it may not apply — and before this field existed the same node was
		// refused as an unknown key, so refusing it is what keeps this reader's
		// answer to it what it has always been.
		if p.Cascade {
			ps = append(ps, Problem{Path: at + ".cascade", Message: "the cascade's marker is the installation's own: a published node is served the pool as an ordinary legacy_open one, because the marker says the pool is served although the subscription-spread switch is off, and no reader of this node may apply that rule (D3064 as amended, R15) — remove the key here and write it in the installation's config, where `fugaro update` writes it for the pool it migrated"})
			// And no more of this pool: the owner rule below would answer a
			// marked pool with the fix that keeps it (mark it legacy_open),
			// which is not what a node carrying the marker needs to hear.
			continue
		}
		if p.Name == "" || p.LegacyOpen || p.Owner != "" {
			continue
		}
		ps = append(ps, Problem{Path: at, Message: "owner is required: a pool is served to its owner and to the users it names, so a pool with no owner serves nobody — name the pool's owner, or mark a pool that predates owners as legacy_open if it is one `fugaro update` migrated"})
	}
	return ps
}

// ValidateCrafts reports every rule a crafts block breaks that its own block
// can answer: each set access is a credential or pool name of the right shape.
// Whether the name is defined is not asked here — a repository's file names
// credentials the project's layers hold, and a document that has no
// credentials block has no answer to give (the resolver's business, MC2).
func ValidateCrafts(c Crafts) []Problem {
	var ps []Problem
	for _, name := range CraftNames {
		craft := c.Of(name)
		if craft.Access == "" {
			continue
		}
		if msg := ValidCredentialRef(craft.Access); msg != "" {
			ps = append(ps, Problem{Path: "agent.crafts." + name + ".access", Message: msg})
		}
	}
	return ps
}

package config

import (
	"fmt"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/pluginwire"
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
)

// CraftNames are the four crafts, in the order a reader meets them.
var CraftNames = []string{CraftCoder, CraftReview, CraftSeniorReview, CraftSecurityReview}

// craftParent is the craft a craft follows when it sets no model or access of
// its own: the first-line review follows the coder and the security review
// follows the senior review. The coder and the senior review follow nobody —
// the spec makes both required — and a craft no key of this table names is no
// craft at all, so an unknown name inherits nothing.
var craftParent = map[string]string{
	CraftReview:         CraftCoder,
	CraftSecurityReview: CraftSeniorReview,
}

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
// `defaults.agent.crafts`: the four crafts by name.
//
// It is a struct with four fields rather than a map for config.ModelRoles'
// reason — the crafts are a closed set, so a file that misspells one is
// refused by the strict decoder instead of quietly setting nothing — and each
// field is a block rather than a scalar because a craft sets its model and its
// access independently.
type Crafts struct {
	Coder          Craft `yaml:"coder,omitempty" json:"coder,omitempty"`
	Review         Craft `yaml:"review,omitempty" json:"review,omitempty"`
	SeniorReview   Craft `yaml:"senior-review,omitempty" json:"senior-review,omitempty"`
	SecurityReview Craft `yaml:"security-review,omitempty" json:"security-review,omitempty"`
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
const (
	PoolStrategyFailover   = "failover"
	PoolStrategyRoundRobin = "round-robin"
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
// could tell it apart from, and a name ending in -review would derive the
// review-only job variant's own job name and service account for a credential
// that mounts the coding job's key (reviewAccountNameMessage).
func credentialNameProblem(name string) string {
	switch {
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
	defined := map[string]bool{CredentialVertex: true}
	for _, c := range creds {
		if c.Name != "" {
			defined[c.Name] = true
		}
	}
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

package routing

import (
	"errors"
	"fmt"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/pricing"
)

// The table's checks (RT3 of docs/model-credentials-routing/implementation-plan.md).
//
// A table is a document a project publishes and every run of it is served by
// (R1, contract-table.md), so what makes it unusable has to be found before a
// run is launched rather than by the run that cannot start. This file is the
// one function that finds it: Problems answers every problem the table has
// against the project's own credentials, pools, providers and budget, and every
// problem names the fix.
//
// **Two kinds of question, asked of two different things.** The table's own
// structure is judged once, entry by entry, because it is true of an entry
// whatever model it answers: a chain with no source at all (D3180), a chain
// whose only source is the launcher's pool (D3180), a paid source after a pool
// with no enforcing budget behind it (R17), and a pool or a provider the
// project does not declare. The rest is judged
// once per model a run can call (R22): whether an entry matches it at all, and
// — for the entry that does match — whether each of the chain's sources reaches
// that model, has a spelling for it, and may serve it at all (D3175, R3, and
// the rule Vertex carries for a model that is not Claude's).
//
// The second half is per model and not per entry because a pattern answers many
// models and every one of these is a property of the model: an entry `claude-*`
// whose chain names a provider key is a different answer for claude-sonnet-5-5,
// which the map spells for a route, and for claude-sonnet-5, which it does not.
//
// **What is not a rule here.** Admission is not one: a source the repository is
// not admitted to is skipped by the walk rather than refused (D3182, R20), so a
// table is judged without knowing which repository will run it. Neither is the
// table's shape: a sixth kind, a `provider` on a pool source and an `admit`
// list on a pool are RT2's decoder's refusals, and a second answer to them here
// would be a second reading of one rule.
//
// **The resolver's answer is the one judged.** A model is routed by Resolve
// (RT1) and by nothing else in this file, so a check cannot disagree with the
// walk about which entry a model goes out on — which is the whole point of the
// resolver being one function (D3172). A table whose two entries claim one
// model with one pattern is refused by the resolver, and that refusal is
// reported here as the table's problem it is.

// Problem is one thing wrong with a table, and the fix.
//
// It is not config.Problem: a table's problems are not all a document's — a
// model no entry matches is a fact about the project's crafts and the table
// together, and a lone subscription token is a fact about its credentials —
// and a caller that prints both lists (RT6's `validate`) keeps them apart
// anyway. What the two share is the shape: a path where the fix is written, a
// sentence that names the fix, and a code a caller acts on without reading the
// sentence.
type Problem struct {
	// Path is where in the table the problem is, in the block's own spelling:
	// "model_routing[2]", "model_routing[2].chain[1]", or "model_routing" for
	// the block itself. It is what a caller prints and what `fugaro routing
	// set` writes the fix at.
	Path string `json:"path"`
	// Message names the problem and the fix, in the terms of the thing that is
	// wrong, and it is what a person reads.
	Message string `json:"message"`
	// Code names the problem for a caller that acts on it rather than printing
	// it: MG1 returns the entries the refusals select, RT7 reports a table
	// problem apart from a lone subscription token. It is not part of the JSON.
	Code string `json:"-"`
	// Entry is the index in the table of the entry the problem is about, and -1
	// for a problem that is not about one entry (a model no entry matches, a
	// lone subscription token). A caller that has to name the entry — MG2
	// prints the entries left on the release-1 bridge by their pattern — reads
	// table[Entry].Match.
	Entry int `json:"-"`
}

// The codes, one per rule, so a caller can act on the rule without reading a
// sentence written to be read. They are stable strings: a reader outside this
// binary consumes none of them, but a task that names one (MG1's D3196 pair,
// GV4's lift of the Vertex rule) names it here.
const (
	// CodeNoEntry is a model a run can call that no entry of the table matches
	// (R1, R22).
	CodeNoEntry = "no_entry"
	// CodeTwinPattern is two entries claiming one model with one pattern, which
	// the resolver refuses (D3172).
	CodeTwinPattern = "twin_pattern"
	// CodeEmptyChain is an entry naming no source at all, so every model it
	// matches is routed to nothing (D3180).
	CodeEmptyChain = "empty_chain"
	// CodeLauncherPoolOnly is a chain whose only source is the launcher's pool
	// (D3180).
	CodeLauncherPoolOnly = "launcher_pool_only"
	// CodePaidAfterPool is a paid source after a pool with no enforcing budget
	// and per-run cap behind it (R17).
	CodePaidAfterPool = "paid_after_pool"
	// CodeUndeclared is a source naming a pool or a provider this project does
	// not declare.
	CodeUndeclared = "undeclared"
	// CodeNoSpelling is a source with no mapped spelling for the model it would
	// serve (D3175).
	CodeNoSpelling = "no_spelling"
	// CodeNoReach is a source that cannot serve the model at all (R2's reach,
	// craft.ByReach's rule).
	CodeNoReach = "no_reach"
	// CodeVertexModel is a model that is not Claude's routed to Vertex, which
	// GV4 lifts for a mapped and priced Gemini model.
	CodeVertexModel = "vertex_model"
	// CodeCheck50 is a Claude model through a provider key before Check 50 is
	// recorded as passed (R3, D3013).
	CodeCheck50 = "check_50"
	// CodeLoneSubscription is a subscription token in no pool (X2), which
	// release 1 reports and release 2 refuses.
	CodeLoneSubscription = "lone_subscription"
)

// Checks is what a table is judged against: the table, every model a run of the
// project can call, and the project's own credentials, pools, providers and
// budget. Every one of them is the project's and not a repository's, because
// the table is the project's (contract-table.md).
//
// No member is ever a credential value: a credential is a name and a type, a
// pool is a list of names, and a provider entry is a claim and an endpoint —
// which is all a reach question needs, and all a table is ever written with.
type Checks struct {
	// Table is the table to judge.
	Table Table
	// Models are every model a run of this project can call: every model the
	// rungs can choose (craft.ModelRungs — the task's grade, a level's model, a
	// craft's model, a pinned role of agent.models, agent.model) and a --model
	// override (R22). The caller resolves them, because the rungs are the run's
	// and a package that assembled its own ladder would be a second copy of it
	// (craft's own rule for ModelRungs). An empty model is skipped: a rung that
	// resolves to no model is a model nothing is called with, and the run's own
	// defaults answer it (F17's carve-out, one file over).
	Models []string
	// Credentials are the project's credentials: what a pool's members are
	// looked up in (config.PoolMembers) and what the lone-subscription rule is
	// asked of (X2).
	Credentials []config.Credential
	// Pools are the project's pools, by the name a source of kind pool names.
	Pools []config.Pool
	// Providers are the project's provider entries by name (config.ModelProvider):
	// what a source of kind provider is looked up in, and what a pool's
	// OpenRouter members reach by.
	Providers map[string]config.ModelProvider
	// BudgetMode and BudgetPerRunUSD are the budget the project runs under, and
	// they are what R17 asks about a chain that names a paid source after a
	// pool: mode enforce with a per-run cap above zero, exactly as D152 asked of
	// `fallback: auto` (config.BudgetEnforce, config.Budget.PerRunUSD).
	BudgetMode      string
	BudgetPerRunUSD float64
	// Check50Passed records that Check 50 has been run and passed, which is what
	// lifts R3's refusal of a Claude model through a provider key. Nothing in
	// this release records it (reconciliation O3: the check has not been run),
	// so it is false wherever it is left unset — and the refusal is the
	// fail-closed direction a caller that has not read the result must get.
	Check50Passed bool
}

// Problems reports every problem the table has against the project in, and
// every problem it reports as a warning rather than a refusal.
//
// The two lists are the two things a caller does with an answer: the first is
// what a caller refuses a table for (`fugaro routing set` writes nothing while
// it is not empty, and a policy publisher refuses it), and the second is what a
// caller prints and goes on with — today exactly one rule, X2's lone
// subscription token, which release 1 reports and release 2 refuses. Both are
// the table's problems; only the first is a refusal.
//
// The problems are in the order the table is read: an entry's own chain first,
// then what the models a run can call make of the entries they resolve to, then
// the credentials the table is written beside. A problem two models produce the
// same way is reported once.
func Problems(in Checks) (problems, warnings []Problem) {
	problems = tableProblems(in)
	problems = append(problems, modelProblems(in)...)
	warnings = loneSubscriptionProblems(in)
	return dedupeProblems(problems), dedupeProblems(warnings)
}

// tableProblems are the problems an entry's chain has whatever model it
// answers: the rules that are true of the entry itself.
func tableProblems(in Checks) []Problem {
	var ps []Problem
	for i, e := range in.Table {
		ps = append(ps, chainProblems(in, i, e)...)
	}
	return ps
}

// chainProblems are one entry's own problems: a chain with no source at all
// (D3180), a chain of only the launcher's pool (D3180), a paid source after a
// pool (R17), and a source naming a pool or a provider the project does not
// declare.
//
// They are asked of the entry and not of a model, which is what keeps them
// about the table a person wrote: a chain that cannot serve anybody is worth
// saying whether or not a rung of this project happens to choose it, and the
// fix is written at the entry.
func chainProblems(in Checks, i int, e Entry) []Problem {
	var ps []Problem
	at := func(j int) string { return fmt.Sprintf("model_routing[%d].chain[%d]", i, j) }
	if len(e.Chain) == 0 {
		// A run's model resolves to this entry and the entry names nothing, so
		// which credential serves the call is a question with no answer at all:
		// there is no source to skip and no reset for the walk to report an
		// exhausted call against. R22 puts the refusal at launch, and this is
		// where the fix is written (the table's own loader refuses an entry with
		// no match and says nothing about a missing chain).
		ps = append(ps, Problem{
			Path:    fmt.Sprintf("model_routing[%d].chain", i),
			Entry:   i,
			Code:    CodeEmptyChain,
			Message: fmt.Sprintf("the entry %q names no source at all, so every model it matches is routed to nothing: a run cannot be served by it and there is no source for the walk to fall through to. Name the source that serves this model — a pool, the project's Anthropic key, a provider key or Vertex (D3180)", e.Match),
		})
	}
	if len(e.Chain) == 1 && e.Chain[0].Kind == KindLauncherPool {
		ps = append(ps, Problem{
			Path:    fmt.Sprintf("model_routing[%d].chain", i),
			Entry:   i,
			Code:    CodeLauncherPoolOnly,
			Message: fmt.Sprintf("the entry %q goes out on the launcher's pool and nothing else, and a run's launcher has not always designated one: the pool is then skipped and the chain has nothing left, which is a run that cannot start. Add the source that serves when no pool is designated — a project pool, the project's Anthropic key, a provider key or Vertex — after the launcher's pool (D3180)", e.Match),
		})
	}
	// R17: a paid source is reached only once the pool before it is exhausted,
	// and that spend is real money. The rule is D152's, read for a chain instead
	// of for `fallback: auto`: the budget must be enforcing, with a ceiling
	// above zero (an enforcing budget with a cap of zero is nothing charged
	// against it).
	if paid, after := paidAfterPool(e.Chain); paid >= 0 && !(in.BudgetMode == config.BudgetEnforce && in.BudgetPerRunUSD > 0) {
		ps = append(ps, Problem{
			Path:  at(paid),
			Entry: i,
			Code:  CodePaidAfterPool,
			Message: fmt.Sprintf("%s follows %s in this chain, and a paid source after a pool is reached the moment the pool is exhausted: that spend is real money, and R17 admits it only where the budget bounds it — budget.mode: enforce with a per-run cap above zero, the rule D152 already has for `fallback: auto` (this project's budget is mode %q with per_run_usd %v). Write budget.mode: enforce and a budget.per_run_usd above zero, or take the paid source out of the chain",
				sourceName(e.Chain[paid]), sourceName(e.Chain[after]), in.BudgetMode, in.BudgetPerRunUSD),
		})
	}
	for j, s := range e.Chain {
		switch s.Kind {
		case KindPool:
			if _, ok := poolNamed(in.Pools, s.Pool); !ok {
				ps = append(ps, Problem{
					Path:    at(j),
					Entry:   i,
					Code:    CodeUndeclared,
					Message: fmt.Sprintf("the source names the pool %q, and this project declares no pool by that name, so the source reaches no credential at all: declare the pool in the project's pools, or point the source at one that exists (a source of kind pool names a pool, and a launcher's own pool is kind %s)", s.Pool, KindLauncherPool),
				})
			}
		case KindProvider:
			if _, ok := in.Providers[s.Provider]; !ok {
				ps = append(ps, Problem{
					Path:    at(j),
					Entry:   i,
					Code:    CodeUndeclared,
					Message: fmt.Sprintf("the source names the provider %q, and this project declares no provider entry by that name, so the source reaches no credential at all: declare it in the project's providers, or point the source at one that exists", s.Provider),
				})
			}
		}
	}
	return ps
}

// paidAfterPool is the first paid source of a chain that a pool precedes, and
// the pool it follows: (-1, -1) when the chain spends nothing after a pool.
//
// A pool is a subscription's home — the launcher's pool or a project's — and
// the three other kinds are pay-per-token (spec.md: "a fallback to a
// pay-per-token source (Anthropic key, OpenRouter, Vertex) counts against
// project spending caps like any paid usage"). The first pool and the first
// paid source after it are the pair the problem is about; a chain that pays
// with nothing before it pays from its first call, which is a budget's ordinary
// subject and not R17's.
func paidAfterPool(chain []Source) (paid, pool int) {
	pool = -1
	for j, s := range chain {
		switch {
		case s.Kind == KindLauncherPool || s.Kind == KindPool:
			if pool < 0 {
				pool = j
			}
		case paidKind(s.Kind):
			if pool >= 0 {
				return j, pool
			}
		}
	}
	return -1, -1
}

// paidKind reports whether a source of this kind spends money per token.
func paidKind(k Kind) bool {
	switch k {
	case KindAnthropic, KindProvider, KindVertex:
		return true
	}
	return false
}

// modelProblems are the problems the models a run can call have: a model no
// entry matches (R1, R22), and — for the entry each model resolves to — the
// chain's own sources, judged against that model.
func modelProblems(in Checks) []Problem {
	var ps []Problem
	seen := make(map[string]bool, len(in.Models))
	for _, model := range in.Models {
		if model == "" || seen[model] {
			continue
		}
		seen[model] = true
		entry, err := Resolve(in.Table, model)
		if err != nil {
			// Two answers, and they are not the same problem: no entry matches
			// the model at all (the typed ErrNoEntry), and the table's own
			// refusal of a model two entries claim with one pattern (D3172).
			// The second is the resolver's sentence, which names its fix.
			code, message := CodeNoEntry, fmt.Sprintf("no entry of the table matches %q, and a model a run can call with no entry is a run that cannot start: a model no entry matches is refused rather than served by whatever credential is around (R1, R22). Write an entry that matches it (`fugaro routing set %s <source>...`)", model, model)
			if !errors.Is(err, ErrNoEntry) {
				code, message = CodeTwinPattern, err.Error()
			}
			ps = append(ps, Problem{Path: "model_routing", Code: code, Entry: -1, Message: message})
			continue
		}
		i := entryIndex(in.Table, entry)
		for j, s := range entry.Chain {
			if p, ok := sourceProblem(in, i, j, s, model); ok {
				ps = append(ps, p)
			}
		}
	}
	return ps
}

// entryIndex is where in the table the entry Resolve answered with sits. It is
// found by its pattern rather than by its position, because Resolve answers a
// value: two entries with one pattern are refused by the resolver when both
// claim the model asked for (D3172), so the pattern of the entry it answered
// with is written once and locates it.
func entryIndex(table Table, e Entry) int {
	for i, cand := range table {
		if cand.Match == e.Match {
			return i
		}
	}
	return -1
}

// sourceProblem is the first problem one source of an entry has for a model the
// entry answers, and whether it has one at all.
//
// One problem and not a list, because the rules are a ladder and a source that
// fails one fails the ones under it for the same reason: a Vertex source for a
// model that is not Claude's has no Vertex spelling either, and a source with
// no spelling cannot be judged on whether it reaches the model. Two sentences
// about one source would be one sentence a person reads past.
//
// The ladder is: the model Vertex may carry at all (GV4's rule), then the
// source's spelling of the model (D3175), then whether the source reaches it
// (R2's reach, the predicate craft.ByReach and a pool's members share), then
// R3's gate on a Claude model reached through a provider route — a source of
// kind provider, and the same route reached through a member of a pool.
func sourceProblem(in Checks, i, j int, s Source, asked string) (Problem, bool) {
	at := fmt.Sprintf("model_routing[%d].chain[%d]", i, j)
	// Which model this source would be sent: the one it names (a written
	// downgrade, which spec.md requires to be written explicitly) and the model
	// the call asked for otherwise (contract-table.md).
	served := s.Model
	if served == "" {
		served = asked
	}
	if s.Kind == KindVertex && !config.IsClaudeModel(served) {
		return Problem{
			Path:    at,
			Entry:   i,
			Code:    CodeVertexModel,
			Message: fmt.Sprintf("this source is Vertex and the model is %q, which is not one of Anthropic's: Vertex serves the project's own GCP project, whose models this build sends Claude to, and a model that is not Claude's needs a translation and a price of its own before Vertex may carry it. Point the entry at the source that serves this model (a provider key or a pool), or run a model Vertex serves (D3193)", served),
		}, true
	}
	spelling, ok := pricing.SpellingFor(string(s.Kind), served)
	if !ok {
		return Problem{
			Path:    at,
			Entry:   i,
			Code:    CodeNoSpelling,
			Message: fmt.Sprintf("no spelling of %q is mapped for %s, and a source the checked-in map does not spell the model for may not be sent it: a spelling derived here would be a call to a model nobody measured (D3175). Point the entry at a source the map spells this model for, or run a model it spells for this one (pricing.SpellingFor, internal/pricing/canonical.go)", served, sourceName(s)),
		}, true
	}
	switch s.Kind {
	case KindLauncherPool:
		// Nothing to judge: which pool the launcher designated is a per-run
		// fact, and the walk skips an empty one rather than refusing the call
		// (D3180). What a chain of only that source costs is chainProblems'.
	case KindPool:
		pool, ok := poolNamed(in.Pools, s.Pool)
		if !ok {
			// Reported by chainProblems already; the message is the same, so
			// the answer carries it once.
			return Problem{}, false
		}
		if !poolReaches(pool, in, served) {
			return Problem{
				Path:    at,
				Entry:   i,
				Code:    CodeNoReach,
				Message: fmt.Sprintf("no member of the pool %q can be sent %q: a pool is reached by its members' own credentials, and a member serves the models its own credential serves (the project's accounts and key reach Anthropic's models, a provider key the models its entry claims). List members that serve %q, or point the entry at the source that does", s.Pool, served, served),
			}, true
		}
		// R3 is about a chain that reaches a Claude model through OpenRouter, and
		// a pool is one: a member of kind provider is the same route a source of
		// that kind is, and the pool's own rule may put the call on it (a
		// round-robin pool picks a member nobody chose). So the gate is asked of
		// the pool's members and not only of a source written as a provider.
		if config.IsClaudeModel(served) && !in.Check50Passed {
			if m, ok := routedMember(pool, in, served); ok {
				return Problem{
					Path:    at,
					Entry:   i,
					Code:    CodeCheck50,
					Message: fmt.Sprintf("the pool %q holds %s, which reaches the Claude model %q through OpenRouter, and a chain that reaches a Claude model through OpenRouter is refused until Check 50 has been recorded as passed: the check measures whether the route reports the prompt-cache markers a Claude call is charged by, and until it has run a routed Claude is charged at a rate nobody measured (D3013, R3). Run Check 50 and record it, or take %s out of the pool, or point the entry at a pool that reaches Anthropic itself (an account or the project's key)", s.Pool, m.Name, served, m.Name),
				}, true
			}
		}
	case KindAnthropic, KindVertex:
		if !config.PoolMemberServesModel(config.PoolMember{Type: credentialType(s.Kind)}, served, in.Providers) {
			return Problem{
				Path:    at,
				Entry:   i,
				Code:    CodeNoReach,
				Message: fmt.Sprintf("%s reaches Anthropic's models, and %q is not one of them: point the entry at the source that serves this model, or run a model this one reaches (R2)", sourceName(s), served),
			}, true
		}
	case KindProvider:
		p, ok := in.Providers[s.Provider]
		if !ok {
			return Problem{}, false // chainProblems' own, reported once
		}
		// The claim is judged against what the provider would be SENT: a
		// provider's models list carries the route's own IDs, which is the
		// spelling the map just answered with.
		if !p.Matches(spelling) {
			return Problem{
				Path:    at,
				Entry:   i,
				Code:    CodeNoReach,
				Message: fmt.Sprintf("the provider %q does not claim %q, the model this source would be sent as: a provider key reaches only the models its own entry lists (its `models`, exact IDs or a prefix ending in `*`), so this source reaches nothing for this model. Add %q to that entry's models, or point the entry at the source that serves this model", s.Provider, spelling, spelling),
			}, true
		}
		if config.IsClaudeModel(served) && !in.Check50Passed {
			return Problem{
				Path:    at,
				Entry:   i,
				Code:    CodeCheck50,
				Message: fmt.Sprintf("%s carries the Claude model %q, and a Claude model through a provider key is refused until Check 50 has been recorded as passed: the check measures whether the route reports the prompt-cache markers a Claude call is charged by, and until it has run a routed Claude is charged at a rate nobody measured (D3000, D3013, R3). Run Check 50 and record it, or point the entry at a source that reaches Anthropic itself (a pool, the project's Anthropic key or Vertex)", sourceName(s), served),
			}, true
		}
	}
	return Problem{}, false
}

// credentialType is the config credential a source of this kind is judged as,
// for the one predicate that answers whether a credential reaches a model
// (config.PoolMemberServesModel). The two kinds that are a single credential of
// the project are the two that have one: a source of kind anthropic is the
// project's key, and one of kind vertex is the project's Vertex.
func credentialType(k Kind) string {
	if k == KindVertex {
		return config.CredentialTypeVertex
	}
	return config.CredentialTypeAnthropic
}

// poolNamed is the project's pool by name.
func poolNamed(pools []config.Pool, name string) (config.Pool, bool) {
	for _, p := range pools {
		if p.Name == name {
			return p, true
		}
	}
	return config.Pool{}, false
}

// poolReaches reports whether any member of the pool can be sent the model.
//
// A pool serves a model when it has a member that serves it, and the member's
// own credential is what answers (config.PoolMemberServesModel, the predicate
// craft.ByReach shares, so a craft and a table cannot disagree about which
// credential serves which model). The stricter rule — every member of a pool
// serves the model, D1637's — is config's own (PoolModelProblems) and stays
// there: it is about the pool a craft names, and one mistake under two keys is
// the drift this file exists to avoid.
func poolReaches(p config.Pool, in Checks, model string) bool {
	for _, m := range config.PoolMembers(p, in.Credentials) {
		if m.Path != "" && config.PoolMemberServesModel(m, model, in.Providers) {
			return true
		}
	}
	return false
}

// routedMember is the member of a pool that reaches the model over a provider
// route: an OpenRouter member whose own provider entry claims it. It is what
// R3's gate is asked of, because that member is the route a source of kind
// provider names — the pool's own rule may put the call on it, so a chain
// naming the pool reaches Claude through OpenRouter as surely as one naming the
// provider directly (D3013, reconciliation R3).
func routedMember(p config.Pool, in Checks, model string) (config.PoolMember, bool) {
	for _, m := range config.PoolMembers(p, in.Credentials) {
		if m.Path == "" || m.Type != config.CredentialTypeOpenRouter {
			continue
		}
		if config.PoolMemberServesModel(m, model, in.Providers) {
			return m, true
		}
	}
	return config.PoolMember{}, false
}

// loneSubscriptionProblems is X2's rule in the shape release 1 gives it: a
// subscription token that is in no pool.
//
// OAuth tokens live only in pools (spec.md), and a chain reaches a subscription
// through one — the launcher's pool or a project pool — so a token in no pool
// is a token no call is served by and no quota is spent from: it is money
// somebody pays for and nothing uses. Release 1 reports it and refuses nothing,
// because `update` has not wrapped the installation's lone accounts yet
// (D3176, PL1), and release 2 refuses it (X2); the fix is the same in both, so
// it is written here once.
func loneSubscriptionProblems(in Checks) []Problem {
	var ps []Problem
	for _, c := range in.Credentials {
		if c.Type != config.CredentialTypeClaudeOAuth || inSomePool(in.Pools, c.Name) {
			continue
		}
		ps = append(ps, Problem{
			Path:    "model_routing",
			Entry:   -1,
			Code:    CodeLoneSubscription,
			Message: fmt.Sprintf("the subscription token %s is in no pool, and a chain reaches a subscription only through one — the launcher's designated pool or a project pool — so no entry of the table can be served by it and its quota is spent by nothing. Put it in a pool, which may be a pool of one: that is what `fugaro update` writes for a lone account (D3176). A token in no pool is reported here in release 1 and refused in release 2 (X2)", c.Name),
		})
	}
	return ps
}

// inSomePool reports whether a credential is a member of any of the project's
// pools.
func inSomePool(pools []config.Pool, name string) bool {
	for _, p := range pools {
		for _, m := range p.Members {
			if m == name {
				return true
			}
		}
	}
	return false
}

// sourceName spells a source for a message a person reads: the kind, and the
// name the kind carries. It is never a credential's value — a source names a
// pool, a provider or one of the project's own credentials, and a message
// carrying more would be about something the table does not hold.
func sourceName(s Source) string {
	switch s.Kind {
	case KindLauncherPool:
		return "the launcher's pool"
	case KindPool:
		return fmt.Sprintf("the pool %q", s.Pool)
	case KindAnthropic:
		return "the project's Anthropic key"
	case KindProvider:
		return fmt.Sprintf("the provider key %q", s.Provider)
	case KindVertex:
		return "Vertex"
	}
	return fmt.Sprintf("a source of kind %q", s.Kind)
}

// dedupeProblems drops a problem already reported, by its path and its
// sentence: a table whose several models resolve to one entry reports the same
// broken source once per model, and a person reading the list needs it once.
// The order is the order the problems were found in, so the table is read
// top to bottom.
func dedupeProblems(ps []Problem) []Problem {
	if len(ps) == 0 {
		return nil
	}
	seen := make(map[Problem]bool, len(ps))
	out := make([]Problem, 0, len(ps))
	for _, p := range ps {
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

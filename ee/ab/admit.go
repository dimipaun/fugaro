// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

// Package ab is the policy half of model A/B testing (docs/model-ab-testing,
// D550): the sampler, the challenger admission and the pair launch. The wire,
// the project layer's ab: keys and the runner's shadow behaviour are the
// core's, so a build that links no ee/ code still decodes a pair, runs the one
// arm it is given and samples nothing.
package ab

import (
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/routing"
)

// The closed reasons Admit answers with: one word per way a named challenger
// cannot be sampled, printed by the launcher's skip line and carried in
// `fugaro run --json` (A3.3). The vocabulary is closed on purpose: a skip line
// is read by the owner and matched by a script, and neither should have to
// parse a sentence built from a model ID or from a provider's own message.
const (
	// ReasonNoChallenger is a project layer that names no challenger at all:
	// sampling is on and there is nothing to run beside the champion.
	ReasonNoChallenger = "no-challenger"
	// ReasonBitbucket is a Bitbucket repository, which is never sampled in
	// v1: a challenger's clone needs a read-only installation token and
	// Bitbucket has none, so the shadow arm cannot exist there (A2.1).
	ReasonBitbucket = "bitbucket"
	// ReasonServiceHeldKey is a GitHub repository whose App key is the
	// credentials service's: the job opens its provider keyless, so there is
	// no in-job key to sign with and no read-only installation token to mint
	// for a challenger's clone (A2.1). Like ReasonBitbucket it is a fact about
	// the repository rather than about the challenger, and the arm would be
	// refused at the same place for the same reason.
	ReasonServiceHeldKey = "service-held-key"
	// ReasonChampionModel is a challenger that is the model the pair varies:
	// the champion's own coder model for a coder pair (D555), and the model
	// that level runs for a pair whose slot is a review level (the owner's
	// ruling of 2026-11-16, docs/review-levels-ab-jobmax O12, where its own
	// words and where they came from are). Either way the arms would differ
	// in nothing.
	ReasonChampionModel = "champion-model"
	// ReasonNotAllowed is a challenger the run's allow-list leaves out: the
	// merge of every layer that writes one, so a repository can only tighten
	// the project's and a model outside the result is one this run may not use
	// at all (D556).
	ReasonNotAllowed = "not-allowed"
	// ReasonUnrouted is a challenger this repository cannot reach: on a project
	// with no table, no provider route serves it — no provider claims it, or
	// the one that does has an allow_data_to that does not name the repository
	// (F18, D556); on a project with one, no entry of the table matches it or
	// none of the winning entry's sources admits this repository (R22, D3182).
	ReasonUnrouted = "unrouted"
	// ReasonVariant is a challenger spelled as a variant (a ":" suffix) or as
	// an alias, rather than the plain model ID the price table and the gateway
	// route by. Both halves matter: a provider's variant is refused by the
	// provider policy, and a bare alias ("opus", "sonnet") is refused by the
	// pin rule a run under a budget applies to its coder, so neither could run
	// as an arm however the checks below came out.
	ReasonVariant = "variant"
	// ReasonUnpriced is a challenger the price table cannot price: the run's
	// gateway charges every call from that table, so an unpriced model is
	// refused rather than guessed at.
	ReasonUnpriced = "unpriced"
	// ReasonNeedsAPIKey is a provider-served challenger under a credential
	// that cannot carry a provider's key: an oauth token is a Claude
	// subscription credential that is never sent to another provider, vertex
	// serves Claude only, and a cascade run's calls are carried by whatever
	// credential the cascade chose. Only an api-key run may send a call to
	// another provider, so a challenger there is one the run would refuse
	// (config.CheckProviderAuth, F18, D556's "the job already mounting the
	// route's key"). It is a question about the repository's own access, so it
	// is asked only on a project with no table: where there is one, the entry
	// that matches the model is where its calls go out (D3170, R1).
	ReasonNeedsAPIKey = "needs-api-key"
	// ReasonNeedsEnforce is a provider-served challenger under a budget mode
	// that is not enforce: the gateway holds the provider's key and routes the
	// call, so without it the call would go to Anthropic (F18). Like
	// ReasonNeedsAPIKey it is asked only where the project publishes no table.
	ReasonNeedsEnforce = "needs-enforce"
)

// Admit picks the challenger of a sampled launch: the first model of the
// project layer's ab.challengers that is not the champion's own coder model
// and passes, for this repository, the checks a run's own model passes — the
// run's allow-list, the provider policy (a route claims the model, that
// route's allow_data_to names the repository, and the ID is plain and priced),
// the pin rule a run under a budget applies to its coder (an explicit model
// ID, never an alias), and the way this project reaches the model: the entry
// of its routing table that matches, or, on a project that publishes none, the
// credential that may carry a provider's key and the budget gateway a
// provider's calls need (D556, F18, D3170). No challenger reaches a provider
// this repository could not already use.
//
// Every one of them is a check the run itself makes at bootstrap, which is the
// point: a challenger admitted here that the run would refuse there would burn
// a daily pair slot (config.AB.MaxPairs) and a job execution to produce a
// refusal instead of an arm.
//
// Two of the answers are not in cfg at all, because a repository's file is
// only one of the layers a run resolves:
//
//   - eff is the run's own policy merge (internal/policy), which is what the
//     runner re-reads at bootstrap. Its Mode is the mode a provider's calls
//     are judged against and its AllowedModels the intersection of every list
//     that was written — the project's ceiling included, which is where a
//     project config's budget.mode: enforce lives. Reading the file's own
//     budget block instead would refuse every provider challenger as
//     needs-enforce on an installation whose runs all enforce, and would admit
//     a model the published list drops. The routing switch is the one other
//     half of the runner's own allow-list check (config.CheckAllowed) that
//     this does not mirror: a challenger spelled as a route's own ID is
//     refused here even where the switch would route it, which is the safe
//     direction and never a launched arm.
//   - table is the project's routing table, published (RT4) and read by this
//     installation's launcher. It is what the repository's access used to
//     answer: a project that publishes one has its models reached by the chain
//     of the entry that matches them, so the challenger's own chain is the
//     question and cfg.Agent's auth says nothing about it. A project that
//     publishes none is on the release-1 bridge, and the credential checks
//     below are its answer. It is the same split the run's own gate makes
//     (internal/cli's chainRoutingGate) and the same one a plan reads for
//     whether a repository is the service's at all (infra.ChainPlanFor).
//   - serviceHeldKey is whether the credentials service holds this
//     installation's GitHub App key, so that the job opens its provider
//     keyless (internal/gitprov/providers.FromEnv) and mints no read-only
//     installation token. The launch knows the fact — it is what decides
//     whether the job is handed the service's address
//     (internal/cli's credentialsServiceLaunchEnv) — and the runner refuses
//     the arm on it (internal/gitprov/github's GitAuthReadOnly, reached from
//     runner.go's mintGitAuth), so it is refused here rather than after a pair
//     slot and a job execution.
//
// ok is false with one of the reasons above when nothing is admitted, and the
// caller then launches the champion alone: a pair is never started for a
// challenger the owner has not allowed where this repository's code goes. The
// list is the owner's and its order is his priority order, so a refused
// challenger is skipped rather than ending the search — one model this
// repository may not use is no reason to sample nothing — and why is the first
// refusal of the list, the candidate the owner reads first. It is "" when a
// challenger was admitted.
//
// These are the launcher's checks, made before any task is written. The runner
// re-checks the challenger the task carries when it bootstraps (A2.1), which
// is what covers a replayed or hand-edited task; a refusal there halts the
// shadow and never the champion.
//
// A nil prices table skips the price rule, exactly as config.CheckProviderPolicy
// does with one: a caller that has no table has no price question to ask.
func Admit(cfg config.Config, layer config.AB, repo string, table routing.Table, eff policy.Effective, providers map[string]config.ModelProvider, prices *pricing.Table, serviceHeldKey bool) (model string, ok bool, why string) {
	return admitAgainst(cfg, layer, repo, table, eff, providers, prices, serviceHeldKey, cfg.Agent.ModelFor(config.RoleCoder))
}

// admitAgainst is Admit's body with the model the pair varies given rather than
// read off the coder role. A coder pair varies the coder's model (D555, the
// call above) and a pair whose slot is a review level varies the model that
// level runs (the owner's ruling of 2026-11-16, docs/review-levels-ab-jobmax
// O12): a reviewer arm replays its level over the champion's patch with the
// challenger's model, so what it must not be is that model, and judging it
// against the coder's would refuse a challenger that varies the review — the
// whole point of the arm — while admitting one that is the very model the
// champion's own review ran.
//
// Everything else is shared, and deliberately: the owner's list, the run's
// allow-list, the provider policy, the pin rule, the credential and the
// gateway are the same questions about the same repository whichever slot a
// challenger's code goes to.
func admitAgainst(cfg config.Config, layer config.AB, repo string, table routing.Table, eff policy.Effective, providers map[string]config.ModelProvider, prices *pricing.Table, serviceHeldKey bool, varied string) (model string, ok bool, why string) {
	if cfg.Git.Provider == gitprov.KindBitbucket {
		return "", false, ReasonBitbucket
	}
	if serviceHeldKey {
		return "", false, ReasonServiceHeldKey
	}
	why = ReasonNoChallenger
	refuse := func(reason string) {
		if why == ReasonNoChallenger {
			why = reason
		}
	}
	for _, c := range layer.Challengers {
		if c == "" {
			// An entry names a model, which the layer's own validation holds
			// it to (config.CheckModelPattern); an empty one names nothing and
			// is not a challenger to skip the rest of the list for.
			continue
		}
		if c == varied {
			refuse(ReasonChampionModel)
			continue
		}
		if why := refuseModel(cfg, c, repo, table, eff, providers, prices); why != "" {
			refuse(why)
			continue
		}
		return c, true, ""
	}
	return "", false, why
}

// refuseModel is why this repository may not send its code to model as a coder
// in this run, in the closed vocabulary above, or "" when it may: the run's
// allow-list, the provider policy, the pin rule and the way the project reaches
// the model — its routing table's chain where it publishes one, the credential
// and gateway a provider's call needs where it does not. It is the per-model
// half of Admit, and it is the same half the judge model passes (A5.2's
// JudgeAdmit, O2): a judge is one more place a pair's code goes, so it answers
// to the same rule a challenger does, and the two callers share it rather than
// each keeping a copy that could drift.
//
// The champion's own model is not asked about here: that the challenger is the
// champion's model (ReasonChampionModel) is a fact about the pair, and D555's
// rule, while the judge has no arm to be the same as.
func refuseModel(cfg config.Config, model, repo string, table routing.Table, eff policy.Effective, providers map[string]config.ModelProvider, prices *pricing.Table) string {
	if !eff.Allows(model) {
		return ReasonNotAllowed
	}
	agent := coderAgent(cfg.Agent, model)
	if ps := config.CheckProviderPolicy(agent, nil, repo, providers, prices); len(ps) > 0 {
		return providerReason(model, repo, providers, prices)
	}
	// The provider policy answers for the models it is about: a name with no
	// route and no "/" is not its business, and that is where Claude Code's own
	// aliases live ("opus", "sonnet", "haiku"). A run under a budget refuses
	// such a coder pin (config.CheckPins, "is an alias: with a budget, name a
	// model ID"), and a sampled run always has a budget (D573), so an alias is
	// refused here rather than halted there.
	if pricing.IsAlias(model) {
		return ReasonVariant
	}
	// A project that publishes a table reaches its models by that table and by
	// nothing else (R1, R2): the entry that matches this model is its answer,
	// and a model no entry matches is one this launch may not use however the
	// repository's own access reads (R22). Reading the access here instead
	// would admit an arm the run's own gate refuses (chainRoutingGate) under a
	// credential nothing carries any more (D3170) — the pair slot and the job
	// execution this whole function exists to save.
	if len(table) > 0 {
		if chainServes(table, model, repo) {
			return ""
		}
		return ReasonUnrouted
	}
	if ps := config.CheckProviderAuth(agent, providers); len(ps) > 0 {
		return ReasonNeedsAPIKey
	}
	if ps := config.CheckProviderGateway(agent, "", providers, policyMode(eff)); len(ps) > 0 {
		return ReasonNeedsEnforce
	}
	return ""
}

// chainServes reports whether the project's table reaches model for repo: the
// entry the model resolves to (RT1), and a source of that entry's chain this
// repository is admitted to (D3182). Both halves are asked because both refuse
// the call: a model no entry matches has no source to walk (R22), and a chain
// whose every source is admitted to another repository is one the run cannot
// send this repository's code on.
//
// The walk is the plan's own (infra.ChainSourceServes), and it is shared rather
// than restated here: whether a source admits a repository is one rule with two
// readers — the plan that decides whether these runs are the credentials
// service's at all, and this admission — and a second copy would be free to
// drift from it, which is exactly the drift the table exists to prevent.
func chainServes(table routing.Table, model, repo string) bool {
	entry, err := routing.Resolve(table, model)
	if err != nil {
		return false
	}
	for _, s := range entry.Chain {
		if infra.ChainSourceServes(s, repo) {
			return true
		}
	}
	return false
}

// coderAgent is the champion's agent with model in the coder slot, which is the
// run D555 describes: the runner applies ab.challenger to the coder role only,
// so the reviewer, the background model and the auth are the champion's, and the
// checks above judge the model as the run that will make the calls. A judge
// model is judged the same way, from the run it is launched for.
func coderAgent(a config.Agent, model string) config.Agent {
	return config.Agent{Auth: a.Auth, Models: config.ModelRoles{Coder: model}}
}

// policyMode is the run's effective budget mode as the runner reads it, asked
// through the one vocabulary that answers what a mode string means
// (docs/always-a-budget/contract-mode.md, C1): a merge that set no mode is
// observe and not off, and every other string that names no live mode is
// observe too. config.CheckProviderGateway admits a provider's call only under
// enforce, so a provider-served challenger is refused under either answer —
// but which answer it is given, and the words of the refusal that answer
// picks, are the vocabulary's rather than this file's. It is read off the
// merge rather than off the file because the mode is a project key: a file
// that writes none is the ordinary shape, and its runs enforce.
func policyMode(eff policy.Effective) string {
	return policy.Mode(eff.Mode).Effective()
}

// providerReason is why config.CheckProviderPolicy refused model for repo, in
// the closed vocabulary above. It asks the questions that function's own
// switch asks, in the same order, because a config.Problem carries a sentence
// and no code while a skip line prints one word. The two are held together by
// TestAdmitAgreesWithTheProviderPolicy, which fails if a model the policy
// refuses is admitted, or one it admits is refused.
//
// It is reached only for a model CheckProviderPolicy refused, so a model that
// rule leaves alone — a Claude ID or an alias, which is not its business — is
// never classified here. The switch's last case is the price table's: a nil
// table is the price rule skipped, which is a model the policy does not
// refuse, so Lookup is never asked about one here.
func providerReason(model, repo string, providers map[string]config.ModelProvider, prices *pricing.Table) string {
	_, p, served := config.ProviderFor(providers, model)
	switch {
	case !served || !p.AllowsData(repo):
		return ReasonUnrouted
	case strings.Contains(model, ":") || pricing.IsAlias(model):
		return ReasonVariant
	case !priced(prices, model):
		return ReasonUnpriced
	}
	return ReasonUnrouted
}

// priced reports whether the table has a row for model. A nil table has none.
func priced(t *pricing.Table, model string) bool {
	_, ok := t.Lookup(model)
	return ok
}

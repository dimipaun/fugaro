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
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/pricing"
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
	// ReasonChampionModel is a challenger that is the champion's own coder
	// model: the arms would differ in nothing (D555).
	ReasonChampionModel = "champion-model"
	// ReasonNotAllowed is a challenger the run's allow-list leaves out: the
	// merge of every layer that writes one, so a repository can only tighten
	// the project's and a model outside the result is one this run may not use
	// at all (D556).
	ReasonNotAllowed = "not-allowed"
	// ReasonUnrouted is a challenger no provider route serves for this
	// repository: no provider claims it, or the one that does has an
	// allow_data_to that does not name the repository (F18, D556).
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
	// route's key").
	ReasonNeedsAPIKey = "needs-api-key"
	// ReasonNeedsEnforce is a provider-served challenger under a budget mode
	// that is not enforce: the gateway holds the provider's key and routes the
	// call, so without it the call would go to Anthropic (F18).
	ReasonNeedsEnforce = "needs-enforce"
)

// Admit picks the challenger of a sampled launch: the first model of the
// project layer's ab.challengers that is not the champion's own coder model
// and passes, for this repository, the checks a run's own model passes — the
// run's allow-list, the provider policy (a route claims the model, that
// route's allow_data_to names the repository, and the ID is plain and priced),
// the pin rule a run under a budget applies to its coder (an explicit model
// ID, never an alias), the credential that may carry a provider's key, and the
// budget gateway a provider's calls need (D556, F18). No challenger reaches a
// provider this repository could not already use.
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
func Admit(cfg config.Config, layer config.AB, repo string, eff policy.Effective, providers map[string]config.ModelProvider, prices *pricing.Table, serviceHeldKey bool) (model string, ok bool, why string) {
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
	champion := cfg.Agent.ModelFor(config.RoleCoder)
	for _, c := range layer.Challengers {
		if c == "" {
			// An entry names a model, which the layer's own validation holds
			// it to (config.CheckModelPattern); an empty one names nothing and
			// is not a challenger to skip the rest of the list for.
			continue
		}
		if c == champion {
			refuse(ReasonChampionModel)
			continue
		}
		if why := refuseModel(cfg, c, repo, eff, providers, prices); why != "" {
			refuse(why)
			continue
		}
		return c, true, ""
	}
	return "", false, why
}

// refuseModel is why this repository may not send its code to model as a coder
// in this run, in the closed vocabulary above, or "" when it may: the run's
// allow-list, the provider policy, the pin rule and the credential and gateway
// a provider's call needs. It is the per-model half of Admit, and it is the
// same half the judge model passes (A5.2's JudgeAdmit, O2): a judge is one more
// place a pair's code goes, so it answers to the same rule a challenger does,
// and the two callers share it rather than each keeping a copy that could drift.
//
// The champion's own model is not asked about here: that the challenger is the
// champion's model (ReasonChampionModel) is a fact about the pair, and D555's
// rule, while the judge has no arm to be the same as.
func refuseModel(cfg config.Config, model, repo string, eff policy.Effective, providers map[string]config.ModelProvider, prices *pricing.Table) string {
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
	if ps := config.CheckProviderAuth(agent, providers); len(ps) > 0 {
		return ReasonNeedsAPIKey
	}
	if ps := config.CheckProviderGateway(agent, "", providers, policyMode(eff)); len(ps) > 0 {
		return ReasonNeedsEnforce
	}
	return ""
}

// coderAgent is the champion's agent with model in the coder slot, which is the
// run D555 describes: the runner applies ab.challenger to the coder role only,
// so the reviewer, the background model and the auth are the champion's, and the
// checks above judge the model as the run that will make the calls. A judge
// model is judged the same way, from the run it is launched for.
func coderAgent(a config.Agent, model string) config.Agent {
	return config.Agent{Auth: a.Auth, Models: config.ModelRoles{Coder: model}}
}

// policyMode is the run's effective budget mode as the runner reads it: a
// merge that set no mode is off (internal/runner's resolvePolicy fills the
// same value), and "" and "off" are the same answer to
// config.CheckProviderGateway, which admits a provider's call only under
// enforce. It is read off the merge rather than off the file because the mode
// is a project key: a file that writes none is the ordinary shape, and its
// runs enforce.
func policyMode(eff policy.Effective) string {
	if eff.Mode == "" {
		return policy.ModeOff
	}
	return eff.Mode
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

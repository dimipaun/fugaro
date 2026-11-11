// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

// Package ab is the policy half of model A/B testing (docs/model-ab-testing,
// D550): the sampler, the challenger admission and the pair launch. The wire,
// the project layer's ab: keys and the runner's shadow behaviour are the
// core's, so an untagged build decodes a pair and publishes nothing of it.
package ab

import (
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
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
	// ReasonChampionModel is a challenger that is the champion's own coder
	// model: the arms would differ in nothing (D555).
	ReasonChampionModel = "champion-model"
	// ReasonNotAllowed is a challenger the repository's own allowed_models
	// leaves out. A repository can only tighten the owner's list, so a model
	// outside it is one this repository's runs may not use at all (D556).
	ReasonNotAllowed = "not-allowed"
	// ReasonUnrouted is a challenger no provider route serves for this
	// repository: no provider claims it, or the one that does has an
	// allow_data_to that does not name the repository (F18, D556).
	ReasonUnrouted = "unrouted"
	// ReasonVariant is a challenger spelled as a variant (a ":" suffix) or as
	// an alias, rather than the plain vendor/model ID the price table and the
	// gateway route by.
	ReasonVariant = "variant"
	// ReasonUnpriced is a challenger the price table cannot price: the run's
	// gateway charges every call from that table, so an unpriced model is
	// refused rather than guessed at.
	ReasonUnpriced = "unpriced"
	// ReasonNeedsEnforce is a provider-served challenger under a budget mode
	// that is not enforce: the gateway holds the provider's key and routes the
	// call, so without it the call would go to Anthropic (F18).
	ReasonNeedsEnforce = "needs-enforce"
)

// Admit picks the challenger of a sampled launch: the first model of the
// project layer's ab.challengers that is not the champion's own coder model
// and passes, for this repository, the checks a run's own model passes — the
// repository's allowed_models, the provider policy (a route claims the model,
// that route's allow_data_to names the repository, and the ID is plain and
// priced) and the budget gateway a provider's calls need (D556, F18). No
// challenger reaches a provider this repository could not already use.
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
func Admit(cfg config.Config, layer config.AB, repo string, providers map[string]config.ModelProvider, prices *pricing.Table) (model string, ok bool, why string) {
	if cfg.Git.Provider == gitprov.KindBitbucket {
		return "", false, ReasonBitbucket
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
		if !allowedByRepo(cfg, c) {
			refuse(ReasonNotAllowed)
			continue
		}
		agent := challengerAgent(cfg.Agent, c)
		if ps := config.CheckProviderPolicy(agent, nil, repo, providers, prices); len(ps) > 0 {
			refuse(providerReason(c, repo, providers, prices))
			continue
		}
		if ps := config.CheckProviderGateway(agent, "", providers, budgetMode(cfg)); len(ps) > 0 {
			refuse(ReasonNeedsEnforce)
			continue
		}
		return c, true, ""
	}
	return "", false, why
}

// challengerAgent is the champion's agent with the challenger in the coder
// slot, which is the run D555 describes: the runner applies ab.challenger to
// the coder role only, so the reviewer, the background model and the auth are
// the champion's, and the checks above judge the challenger as the run that
// will make the calls.
func challengerAgent(a config.Agent, model string) config.Agent {
	return config.Agent{Auth: a.Auth, Models: config.ModelRoles{Coder: model}}
}

// allowedByRepo reports whether the repository's own budget.allowed_models
// admits model. The file's list is the one visible here and the only one a
// repository can write: it may only tighten the owner's, so a model outside it
// is one this repository's runs may not use (D556). A file that writes no list
// sets none, and the project's list — which this function cannot see — is
// applied by the launch's own policy merge and re-read by the runner.
func allowedByRepo(cfg config.Config, model string) bool {
	if cfg.Budget == nil || cfg.Budget.AllowedModels == nil {
		return true
	}
	return slices.Contains(cfg.Budget.AllowedModels, model)
}

// budgetMode is the budget mode the launcher can see for this repository. The
// project's ceiling may be stricter than the file's (internal/policy merges
// the two, and only tightening wins), and a stricter answer than this one only
// ever refuses a sample: a file that reads as enforce here is enforce after
// the merge too, so a provider challenger is never admitted on a mode the run
// would not actually have.
func budgetMode(cfg config.Config) string {
	if cfg.Budget == nil {
		return ""
	}
	return cfg.Budget.Mode
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

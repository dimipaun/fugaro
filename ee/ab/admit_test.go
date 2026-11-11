// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package ab

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/pricing"
)

// The fixtures are the owner's side of the checks: a provider route that
// claims deepseek/* and names the sandbox repository, a table that prices the
// challenger, and a repository whose coder is a Claude model under an enforced
// budget. That is the shape D556 admits a challenger in; every test below
// changes one thing about it and reads the refusal.
const (
	admitRepo       = "acme/sandbox"
	admitOtherRepo  = "acme/other"
	admitChampion   = "claude-sonnet-5-5"
	admitChallenger = "deepseek/chat-x"
	admitClaudeAlt  = "claude-opus-5-5"
)

func admitProviders() map[string]config.ModelProvider {
	return map[string]config.ModelProvider{"openrouter": {
		Kind: config.ModelProviderKind, BaseURL: "https://openrouter.ai/api", Auth: "bearer",
		Secret: "openrouter-api-key", Models: []string{"deepseek/*"}, AllowDataTo: []string{admitRepo},
	}}
}

// admitProvidersClosed is the same route with allow_data_to naming another
// repository: the owner opened the provider, but not to this repository's code
// (F18).
func admitProvidersClosed() map[string]config.ModelProvider {
	ps := admitProviders()
	p := ps["openrouter"]
	p.AllowDataTo = []string{admitOtherRepo}
	ps["openrouter"] = p
	return ps
}

func admitPrices() *pricing.Table {
	return &pricing.Table{Models: map[string]pricing.Model{
		admitChallenger: {ID: admitChallenger, Rates: pricing.Rates{InputPerM: 1, OutputPerM: 2}},
	}}
}

func admitConfig() config.Config {
	return config.Config{
		Git:    config.Git{Provider: gitprov.KindGitHub},
		Agent:  config.Agent{Auth: "api-key", Model: admitChampion},
		Budget: &config.Budget{Mode: "enforce"},
	}
}

func admitLayer(challengers ...string) config.AB {
	return config.AB{Challengers: challengers}
}

// TestAdmitTakesTheFirstChallengerThatPasses is the happy path: the list is
// the owner's priority order, so the first model that clears every check is
// the one the pair runs, and the ones before it are skipped rather than
// ending the search.
func TestAdmitTakesTheFirstChallengerThatPasses(t *testing.T) {
	model, ok, why := Admit(admitConfig(), admitLayer(admitChampion, "openai/gpt-9", admitChallenger, admitClaudeAlt),
		admitRepo, admitProviders(), admitPrices())
	if !ok || model != admitChallenger || why != "" {
		t.Fatalf("Admit = %q %v %q, want %q admitted with no reason", model, ok, why, admitChallenger)
	}
	// A Claude model is the champion's own route and needs no provider, no
	// allow_data_to and no price row: the checks are about egress, and a
	// direct model is where every run already goes.
	model, ok, why = Admit(admitConfig(), admitLayer(admitClaudeAlt), admitRepo, nil, nil)
	if !ok || model != admitClaudeAlt || why != "" {
		t.Errorf("a direct model was refused: %q %v %q", model, ok, why)
	}
}

// TestAdmitRefusesUnroutedChallenger is the egress rule of D556 and F18: a
// challenger reaches only a provider the repository could already use, so a
// model no route serves, or one whose route's allow_data_to does not name the
// repository, is refused rather than sent.
func TestAdmitRefusesUnroutedChallenger(t *testing.T) {
	for _, tc := range []struct {
		name, model string
		providers   map[string]config.ModelProvider
	}{
		{"no provider claims it", "openai/gpt-9", admitProviders()},
		{"the route does not name the repository", admitChallenger, admitProvidersClosed()},
		// A variant of a model the route does not serve this repository is
		// unrouted, not a variant: the reason is asked in the policy's own
		// order, and where the code may go is the sharper truth about it.
		{"a variant of a model the route does not serve here", admitChallenger + ":free", admitProvidersClosed()},
	} {
		model, ok, why := Admit(admitConfig(), admitLayer(tc.model), admitRepo, tc.providers, admitPrices())
		if ok || model != "" || why != ReasonUnrouted {
			t.Errorf("%s: Admit = %q %v %q, want no model and %q", tc.name, model, ok, why, ReasonUnrouted)
		}
	}
}

// TestAdmitRefusesUnpricedChallenger pins the price rule: the run's gateway
// charges every call from the table, so a model it cannot price is refused
// rather than guessed at. A table that prices the model admits it, which is
// what makes the refusal the price rule and not something else.
func TestAdmitRefusesUnpricedChallenger(t *testing.T) {
	empty := &pricing.Table{Models: map[string]pricing.Model{}}
	if model, ok, why := Admit(admitConfig(), admitLayer(admitChallenger), admitRepo, admitProviders(), empty); ok || model != "" || why != ReasonUnpriced {
		t.Errorf("Admit = %q %v %q, want no model and %q", model, ok, why, ReasonUnpriced)
	}
	if model, ok, why := Admit(admitConfig(), admitLayer(admitChallenger), admitRepo, admitProviders(), admitPrices()); !ok || model != admitChallenger || why != "" {
		t.Errorf("a priced challenger was refused: %q %v %q", model, ok, why)
	}
}

// TestAdmitRefusesDisallowedModel is the repository's own allow-list: it may
// only tighten the owner's, and a challenger outside it is a model this
// repository's runs may not use at all (D556). The list is the file's, so a
// repository that names the challenger admits it.
func TestAdmitRefusesDisallowedModel(t *testing.T) {
	cfg := admitConfig()
	cfg.Budget.AllowedModels = []string{admitChampion}
	if model, ok, why := Admit(cfg, admitLayer(admitChallenger), admitRepo, admitProviders(), admitPrices()); ok || model != "" || why != ReasonNotAllowed {
		t.Errorf("Admit = %q %v %q, want no model and %q", model, ok, why, ReasonNotAllowed)
	}
	cfg.Budget.AllowedModels = []string{admitChampion, admitChallenger}
	if model, ok, why := Admit(cfg, admitLayer(admitChallenger), admitRepo, admitProviders(), admitPrices()); !ok || model != admitChallenger || why != "" {
		t.Errorf("a listed challenger was refused: %q %v %q", model, ok, why)
	}
}

// TestAdmitSkipsChampionModel pins D555: the pair differs in the coder model,
// so a challenger that is the champion's own coder is no challenger at all.
// It is skipped, not fatal — a list that names it first still finds the model
// behind it — and a list that names nothing else admits nothing.
func TestAdmitSkipsChampionModel(t *testing.T) {
	if model, ok, why := Admit(admitConfig(), admitLayer(admitChampion, admitChallenger), admitRepo, admitProviders(), admitPrices()); !ok || model != admitChallenger {
		t.Errorf("Admit = %q %v %q, want the challenger behind the champion's own model", model, ok, why)
	}
	model, ok, why := Admit(admitConfig(), admitLayer(admitChampion), admitRepo, admitProviders(), admitPrices())
	if ok || model != "" || why != ReasonChampionModel {
		t.Errorf("Admit = %q %v %q, want no model and %q", model, ok, why, ReasonChampionModel)
	}
}

// TestAdmitNeedsEnforceForProvider pins the gateway half of D556's checks: a
// provider's calls go through the budget gateway, which holds the key and
// routes the call, so a provider-served challenger under any other mode would
// be a call to Anthropic. A direct model needs none of it.
func TestAdmitNeedsEnforceForProvider(t *testing.T) {
	for _, mode := range []string{"", "off", "observe"} {
		cfg := admitConfig()
		cfg.Budget.Mode = mode
		if model, ok, why := Admit(cfg, admitLayer(admitChallenger), admitRepo, admitProviders(), admitPrices()); ok || model != "" || why != ReasonNeedsEnforce {
			t.Errorf("mode %q: Admit = %q %v %q, want no model and %q", mode, model, ok, why, ReasonNeedsEnforce)
		}
	}
	cfg := admitConfig()
	cfg.Budget = nil
	if model, ok, why := Admit(cfg, admitLayer(admitChallenger), admitRepo, admitProviders(), admitPrices()); ok || why != ReasonNeedsEnforce {
		t.Errorf("no budget block: Admit = %q %v %q, want %q", model, ok, why, ReasonNeedsEnforce)
	}
	cfg = admitConfig()
	cfg.Budget.Mode = "observe"
	if model, ok, why := Admit(cfg, admitLayer(admitClaudeAlt), admitRepo, admitProviders(), admitPrices()); !ok || model != admitClaudeAlt || why != "" {
		t.Errorf("a direct model under observe was refused: %q %v %q", model, ok, why)
	}
}

// TestAdmitSkipsBitbucket pins the v1 limit A2.1 states: a challenger's clone
// needs a read-only installation token, and Bitbucket has none, so a Bitbucket
// repository is never sampled however good its challenger is.
func TestAdmitSkipsBitbucket(t *testing.T) {
	cfg := admitConfig()
	cfg.Git.Provider = gitprov.KindBitbucket
	if model, ok, why := Admit(cfg, admitLayer(admitChallenger), admitRepo, admitProviders(), admitPrices()); ok || model != "" || why != ReasonBitbucket {
		t.Errorf("Admit = %q %v %q, want no model and %q", model, ok, why, ReasonBitbucket)
	}
}

// TestAdmitNeedsAChallenger pins the empty cases: a layer that names none, and
// a layer whose only entry is empty, admit nothing and say so.
func TestAdmitNeedsAChallenger(t *testing.T) {
	for _, layer := range []config.AB{admitLayer(), admitLayer("")} {
		if model, ok, why := Admit(admitConfig(), layer, admitRepo, admitProviders(), admitPrices()); ok || model != "" || why != ReasonNoChallenger {
			t.Errorf("layer %+v: Admit = %q %v %q, want no model and %q", layer, model, ok, why, ReasonNoChallenger)
		}
	}
}

// TestAdmitAgreesWithTheProviderPolicy holds the reason vocabulary to the rule
// it classifies: the checks Admit makes for a challenger are
// config.CheckProviderPolicy's, and the closed reason is a second reader of
// them, so the two must answer the same question the same way. A model the
// policy refuses is never admitted, and one it admits is admitted here.
func TestAdmitAgreesWithTheProviderPolicy(t *testing.T) {
	cfg, providers, prices := admitConfig(), admitProviders(), admitPrices()
	for _, model := range []string{admitChallenger, "openai/gpt-9", "deepseek/chat-x:free", "deepseek/unpriced", admitClaudeAlt} {
		agent := config.Agent{Auth: cfg.Agent.Auth, Models: config.ModelRoles{Coder: model}}
		ps := config.CheckProviderPolicy(agent, nil, admitRepo, providers, prices)
		_, ok, why := Admit(cfg, admitLayer(model), admitRepo, providers, prices)
		switch {
		case len(ps) > 0 && ok:
			t.Errorf("%s: Admit admitted a model CheckProviderPolicy refuses: %v", model, ps)
		case len(ps) > 0 && why != ReasonUnrouted && why != ReasonVariant && why != ReasonUnpriced:
			t.Errorf("%s: CheckProviderPolicy refuses it but Admit said %q", model, why)
		case len(ps) == 0 && !ok:
			t.Errorf("%s: CheckProviderPolicy admits it but Admit said %q", model, why)
		}
	}
}

package creds

import (
	"errors"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

// This file is the model-aware half of the tier-C gate (D3000, F5, R3). What a
// provider credential IS and how the credentials service builds a list of them
// is select.go's and internal/credsvc's; what is proved here is the one thing
// the selection needs to know about the call: its model. A Claude model through
// a provider key waits for Check 50 — the call whose only credential is a
// provider's is refused with that sentence — and every other model is served by
// that credential.
//
// It is a file of its own because select_test.go's cases are all about the
// cascade's own tiers, and its candidate lists are the two the cascade was
// built for (a shared token and the project's paid key). Every list here names
// a provider's key at tier C, which no coding caller could be offered before
// this work (F5) and which is what makes the model the question.

var (
	// claudeModel, otherModel and unknownModel are the three answers a caller
	// can give about a call's model: a Claude one (either spelling a pin may
	// carry, config.IsClaudeModel's own ladder), a provider's model, and the
	// empty string a caller that read no model gives — which reads as a Claude
	// one, the fail-closed direction (checkCandidates).
	claudeModel  = "claude-sonnet-5"
	otherModel   = "deepseek/deepseek-v4.1"
	unknownModel = ""

	// openrouter is one named provider credential of C1's family, spelled as
	// the credentials service offers it: the name it is entered under in the
	// bundle (provider_wire.RepoCredentialKey), which is what an operator reads
	// in a refusal and what the selector only ever carries.
	openrouter = Credential{Name: "repo-credential--gh-acme-app-1a2b--openrouter", Tier: TierC}
)

// TestAClaudeModelThroughAProviderWaitsForCheck50 is D3000's first half, and
// the shape the service's provider route builds: a call for a CLAUDE model
// whose one offered credential is a provider's key. It is refused — the gate
// D150 put there, measured by Check 50 — and the refusal is the sentence that
// names the check and the decision, because the fix is the owner's run of a
// check and not a token an operator could clear.
//
// The same call is refused when the caller named no model at all: a side that
// cannot say which model a call is for has no business serving a Claude model
// through a third party, and "" is the answer every caller gave before a
// selection could be told the model (Selector.Model).
func TestAClaudeModelThroughAProviderWaitsForCheck50(t *testing.T) {
	for _, model := range []string{claudeModel, unknownModel} {
		s := selector(newFakeStore(), noWindow())
		s.Model = model
		_, _, err := s.Select(ctx, run, []Credential{openrouter})
		if err == nil {
			t.Fatalf("a %s model was served by a provider key: the selection answered no error", modelText(model))
		}
		if errors.Is(err, ErrNoCredential) || errors.Is(err, ErrNoCandidates) {
			t.Fatalf("a %s model through a provider = %v; want the refusal that names Check 50 and not a sentence about accounts or the bundle", modelText(model), err)
		}
		for _, want := range []string{"tier C", "openrouter", "Check 50", "D150"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("the refusal of a %s model through a provider says %q, and does not say %q: an operator reading it must learn which credential, which check and which decision", modelText(model), err, want)
			}
		}
	}
}

// TestANonClaudeProviderModelIsServed is the other half of D3000, and the red
// step this task was written from: before it, a provider credential was refused
// whatever the model was. A model a provider runs is served by its key, and by
// nothing else in the list — the Anthropic tiers cannot serve it, and the
// provider's key is what the call's own policy named for it.
//
// It holds under the protective settings as well (the zero Settings, which is
// `oauth-only`): the fallback D152 governs is what moves a CLAUDE call off a
// shared subscription token onto the project's own paid ANTHROPIC key, and a
// non-Claude provider model was never on a subscription token to be moved off
// one — so D3007's rule (a repository's auth is for the models, not the other
// way round) is why a project whose Claude calls run on donated tokens can
// still have its provider models served by its own key (tiers).
func TestANonClaudeProviderModelIsServed(t *testing.T) {
	for _, set := range []Settings{noWindow(), {}, {Fallback: Auto}} {
		s := selector(newFakeStore(), set)
		s.Model = otherModel
		c, wait, err := s.Select(ctx, run, []Credential{openrouter})
		if err != nil {
			t.Fatalf("a provider's own model under %+v = %v; want the provider key it was offered: a non-Claude provider model is served without waiting (D3000)", set, err)
		}
		if c.Name != openrouter.Name || c.Tier != TierC || wait != 0 {
			t.Fatalf("a provider's own model = %q (tier %s), wait %s; want %q (tier C) at once", c.Name, c.Tier, wait, openrouter.Name)
		}
	}
}

// The tier-C candidate is the model's business and never the rest of the
// call's: a Claude call whose list also holds the credentials that CAN serve it
// is served by one of them. The alternative — refusing the whole call because a
// provider credential is somewhere in its list — would stop every Claude call a
// repository that holds a provider key makes, which is a claim F40 does not
// make: what stops there is a Claude model ROUTED through a provider, and that
// call's list is the provider credential alone.
func TestAProviderCredentialInAClaudeCallsListIsNotTheCallsAnswer(t *testing.T) {
	s := selector(newFakeStore(), noWindow())
	s.Model = claudeModel
	c, _, err := s.Select(ctx, run, []Credential{a1, openrouter})
	if err != nil {
		t.Fatalf("a Claude call offered a shared token beside a provider key = %v; want the shared token", err)
	}
	if c.Name != a1.Name {
		t.Fatalf("a Claude call offered a shared token beside a provider key was served %q; want %q", c.Name, a1.Name)
	}

	// And the refusal is still what a list of provider keys and nothing else
	// answers, so the skip above is not a quiet way past D150's gate.
	s2 := selector(newFakeStore(), noWindow())
	s2.Model = claudeModel
	if _, _, err := s2.Select(ctx, run, []Credential{openrouter}); err == nil {
		t.Fatal("a Claude call whose only credential is a provider key was served")
	} else if errors.Is(err, ErrNoCredential) {
		t.Fatalf("a Claude call whose only credential is a provider key = %v; want the refusal that names Check 50", err)
	}
}

// Which models are Claude's is the pin vocabulary the config already carries
// (R3, config.IsClaudeModel over ClaudePatterns) and never a list of this
// package's: the two spellings a pin may carry for one model are one answer, a
// provider's model is the other, and a caller that named none is the refusing
// one.
func TestWhichModelsAreClaudesIsThePinsOwnVocabulary(t *testing.T) {
	for _, model := range []string{"claude-sonnet-5", "claude-opus-5-5", "anthropic/claude-opus-5-5"} {
		if providerServes(model) {
			t.Errorf("the model %q is admitted for a provider key: it is a Claude model in one of the two spellings a pin may carry (config.IsClaudeModel)", model)
		}
	}
	for _, model := range []string{otherModel, "deepseek/*", "gpt-5"} {
		if !providerServes(model) {
			t.Errorf("the model %q is refused a provider key: it is no Claude model, and D3000 gates a Claude model alone", model)
		}
	}
	// The vocabulary is config's, so a model it reads one way and this package
	// reads the other would be two answers to one question.
	for _, model := range []string{"claude-sonnet-5", "anthropic/claude-opus-5-5", otherModel, "gpt-5"} {
		if got, want := providerServes(model), model != "" && !config.IsClaudeModel(model); got != want {
			t.Errorf("providerServes(%q) = %v, and config.IsClaudeModel says %v", model, got, want)
		}
	}
}

// modelText names the model a case is about without inventing a spelling for
// the empty one: a caller that named none is a case of its own and reads as
// that in a failure message.
func modelText(model string) string {
	if model == "" {
		return "named-no-model"
	}
	return model
}

package creds

import (
	"errors"
	"os"
	"path/filepath"
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
// that credential, and by no Anthropic one: which class of credential can run a
// call at all is the model's question in both directions (D3000, D3007).
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
	// in a refusal and what the selector only ever carries. It carries the
	// provider it is the key OF as well (Credential.Provider): the policy's
	// `providers` block is what named it, and a key serves its own provider's
	// models alone.
	openrouter = Credential{Name: "repo-credential--gh-acme-app-1a2b--openrouter", Tier: TierC, Provider: "openrouter"}
	// deepseek is a second one of the same family: a project may name several
	// providers, so one caller's list holds several keys and the tie is the
	// only thing that tells them apart (checkCandidates).
	deepseek = Credential{Name: "repo-credential--gh-acme-app-1a2b--deepseek", Tier: TierC, Provider: "deepseek"}
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

// TestCheck50sResultDecidesTheClaudeGate is KS20 in the state the owner ruled
// on 2026-11-26: the release ships WITHOUT Check 50's result, so the gate D150
// put there stands exactly as KS3 wrote it and the checklist's own row still
// reads not run. The two are one answer read in two places, which is what this
// test holds together — a row that says not run with a selection that already
// serves a Claude model through a provider is a release shipping a decision
// nobody made, and a row recording a result with the refusal still there is a
// result not applied. When the owner's run lands, the flip is the row and this
// test in one change (D3013).
//
// The state is read from the checklist's own section header, which is the line
// that says NOT RUN to the owner reading it: the fact this test is about is
// what that file records, and not a second copy of it here.
func TestCheck50sResultDecidesTheClaudeGate(t *testing.T) {
	ran := check50RecordsAResult(t)

	s := selector(newFakeStore(), noWindow())
	s.Model = claudeModel
	c, _, err := s.Select(ctx, run, []Credential{openrouter})
	if !ran {
		if err == nil {
			t.Fatalf("Check 50's row still reads not run and a Claude model was served through a provider key as %q: only a recorded result lifts the gate D150 put there (D3013)", c.Name)
		}
		for _, want := range []string{"tier C", "Check 50"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("the refusal of a Claude model through a provider says %q, and does not say %q: an operator reading it must learn which credential stopped the call and which check decides it", err, want)
			}
		}
		return
	}
	if err != nil {
		t.Fatalf("the checklist records Check 50's result and the refusal is still there: %v\nKS20 is the change that applies the result: the refusal goes, and doctor's unmeasured-cost warning with it", err)
	}
}

// check50RecordsAResult reports whether the live checklist's own Check 50
// section records a result, by the NOT RUN its header carries while none has
// been written into it.
func check50RecordsAResult(t *testing.T) bool {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "gcp-live-checklist.md"))
	if err != nil {
		t.Fatalf("the live checklist could not be read: %v", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "## Check 50:") {
			return !strings.Contains(line, "NOT RUN")
		}
	}
	t.Fatal("docs/gcp-live-checklist.md has no `## Check 50:` section: the row Check 50's result is recorded in is gone")
	return false
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
		s.Provider = openrouter.Provider
		c, wait, err := s.Select(ctx, run, []Credential{openrouter})
		if err != nil {
			t.Fatalf("a provider's own model under %+v = %v; want the provider key it was offered: a non-Claude provider model is served without waiting (D3000)", set, err)
		}
		if c.Name != openrouter.Name || c.Tier != TierC || wait != 0 {
			t.Fatalf("a provider's own model = %q (tier %s), wait %s; want %q (tier C) at once", c.Name, c.Tier, wait, openrouter.Name)
		}
	}
}

// TestANonClaudeModelIsServedByNothingButTheProvider is the same rule read the
// other way, and the list is what makes it a defect rather than a preference:
// the offering puts a repository's provider credential beside the credentials
// its Claude calls use (F32), so a call whose model is a provider's reaches the
// provider's key only if the two Anthropic tiers are out of its way — the
// cascade walks tier A first, and a shared subscription token cannot run a
// model that is not Claude's, nor can the project's own Anthropic key
// (checkCandidates, D3000, D3007). Before this, a DeepSeek call of such a
// repository would have gone to Anthropic on donated quota.
func TestANonClaudeModelIsServedByNothingButTheProvider(t *testing.T) {
	for _, set := range []Settings{noWindow(), {Fallback: Auto}} {
		s := selector(newFakeStore(), set)
		s.Model = otherModel
		s.Provider = openrouter.Provider
		c, wait, err := s.Select(ctx, run, []Credential{a1, b1, openrouter})
		if err != nil {
			t.Fatalf("a provider's own model offered %+v = %v; want its provider key: the Anthropic tiers cannot run it (D3000, D3007)", set, err)
		}
		if c.Name != openrouter.Name || c.Tier != TierC || wait != 0 {
			t.Fatalf("a provider's own model offered a shared token and the project's key beside its provider key = %q (tier %s), wait %s; want %q (tier C) at once", c.Name, c.Tier, wait, openrouter.Name)
		}
	}
}

// A run already placed on one of them is moved off it, and this is the path
// that made the rule urgent: D154's stickiness answers first, so without the
// model there every call after the first would keep going to the token.
func TestANonClaudeModelIsMovedOffASharedToken(t *testing.T) {
	st := newFakeStore()
	st.setPlacement(run, a1.Name, Lease{Credential: a1.Name, Until: now.Add(DefaultLeaseTTL)})
	s := selector(st, noWindow())
	s.Model = otherModel
	s.Provider = openrouter.Provider
	c, _, err := s.Select(ctx, run, []Credential{a1, openrouter})
	if err != nil {
		t.Fatalf("a provider's own model whose run sits on a shared token = %v; want its provider key", err)
	}
	if c.Name != openrouter.Name {
		t.Fatalf("a provider's own model whose run sits on a shared token was served %q; want %q: stickiness keeps a call on the credential it IS served by, and this call's model cannot be run there", c.Name, openrouter.Name)
	}
}

// And the fail-closed direction, which is what dropping the Anthropic tiers
// costs: a provider's model whose call offers no provider credential at all is
// refused, never run on a token or on the project's key. That list is what a
// repository whose policy names a provider the bundle holds no entry for
// produces, and it is a caller's mistake — the refusal names the credential it
// left and the rule that left it, rather than sending the operator to the
// bundle or to a policy.
func TestANonClaudeModelWithNoProviderKeyIsRefused(t *testing.T) {
	for _, set := range []Settings{noWindow(), {Fallback: Auto}} {
		s := selector(newFakeStore(), set)
		s.Model = otherModel
		s.Provider = openrouter.Provider
		c, _, err := s.Select(ctx, run, []Credential{a1, b1})
		if err == nil {
			t.Fatalf("a provider's own model offered no provider credential under %+v was served %q (tier %s); want a refusal (D3000, D3007)", set, c.Name, c.Tier)
		}
		if errors.Is(err, ErrNoCredential) || errors.Is(err, ErrNoCandidates) {
			t.Fatalf("a provider's own model offered no provider credential = %v; want the refusal that names the model's own rule and not a sentence about accounts or the bundle", err)
		}
		for _, want := range []string{"tier a", "shared-1", "D3000", "D3007"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("the refusal of a provider's own model says %q, and does not say %q: an operator reading it must learn which credential was left and why", err, want)
			}
		}
	}
}

// TestOnlyTheProvidersKeyTheCallNamesIsServed is the other half of the tier-C
// question, and the one the model cannot answer on its own (D3000, R2). A
// project's `providers` block may name several providers, so the offering puts
// several keys in one list — and a provider's key is the key of ONE provider:
// serving one for another's model sends a bearer token to an endpoint it was
// not issued for, or a model to a route that cannot run it, and the selection's
// own order would decide which one that was.
//
// So the caller names the provider that runs this call's model
// (Selector.Provider), and the list narrows to that provider's key alone. The
// two fail-closed directions are here as well, and they are the reason the tie
// is a tie and not a preference: a call that named no provider gets no tier-C
// credential at all — a list holding one provider's key is no evidence that
// that provider runs the model, and a model whose provider's key the bundle
// does not hold would otherwise be sent to whichever key was left — and a call
// whose named provider's key is not in the list is refused rather than served
// another's.
func TestOnlyTheProvidersKeyTheCallNamesIsServed(t *testing.T) {
	for _, tc := range []struct {
		name     string
		provider string
		list     []Credential
		want     string
		wantSays []string
	}{
		{"the first provider named", "openrouter", []Credential{openrouter, deepseek}, openrouter.Name, nil},
		{"the second provider named", "deepseek", []Credential{openrouter, deepseek}, deepseek.Name, nil},
		{"the named provider's key beside a shared token and the project's key", "deepseek", []Credential{a1, b1, openrouter, deepseek}, deepseek.Name, nil},
		{"no provider named", "", []Credential{openrouter, deepseek}, "", []string{"tier C", "names no provider", "D3000"}},
		{"a provider the list holds no key of", "mistral", []Credential{openrouter, deepseek}, "", []string{"openrouter", "mistral", "D3000"}},
	} {
		s := selector(newFakeStore(), noWindow())
		s.Model = otherModel
		s.Provider = tc.provider
		c, wait, err := s.Select(ctx, run, tc.list)
		if tc.want == "" {
			if err == nil {
				t.Errorf("%s: the call was served %q (tier %s), and no key in its list belongs to the provider the call named", tc.name, c.Name, c.Tier)
				continue
			}
			if errors.Is(err, ErrNoCredential) || errors.Is(err, ErrNoCandidates) {
				t.Errorf("%s: %v; want the refusal that names the tie and not a sentence about accounts or the bundle", tc.name, err)
				continue
			}
			for _, want := range tc.wantSays {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("%s: the refusal %q does not say %q: it must name the tie and the providers it held apart", tc.name, err, want)
				}
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v; want %q, the key of the provider the call named", tc.name, err, tc.want)
			continue
		}
		if c.Name != tc.want || c.Tier != TierC || wait != 0 {
			t.Errorf("%s: the call was served %q (tier %s), wait %s; want %q (tier C) at once", tc.name, c.Name, c.Tier, wait, tc.want)
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
		if ProviderServes(model) {
			t.Errorf("the model %q is admitted for a provider key: it is a Claude model in one of the two spellings a pin may carry (config.IsClaudeModel)", model)
		}
	}
	for _, model := range []string{otherModel, "deepseek/*", "gpt-5"} {
		if !ProviderServes(model) {
			t.Errorf("the model %q is refused a provider key: it is no Claude model, and D3000 gates a Claude model alone", model)
		}
	}
	// The vocabulary is config's, so a model it reads one way and this package
	// reads the other would be two answers to one question.
	for _, model := range []string{"claude-sonnet-5", "anthropic/claude-opus-5-5", otherModel, "gpt-5"} {
		if got, want := ProviderServes(model), model != "" && !config.IsClaudeModel(model); got != want {
			t.Errorf("ProviderServes(%q) = %v, and config.IsClaudeModel says %v", model, got, want)
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

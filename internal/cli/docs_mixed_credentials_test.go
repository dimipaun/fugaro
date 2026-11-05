package cli

import (
	"fmt"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/recipe"
)

// The B0 ruling of 2026-11-04 (release 0.19.0) reverses the "a run does not
// mix credentials" rule for exactly one case, and these tests are what hold
// the three documents that state it to the rule: the design note that records
// it (docs/design/mixed-credentials.md, whose §11 is new), the credential
// section and the refusal list of the user doc (docs/multi-model.md), and
// SECURITY.md's residual risk. The ruling is docs-only; the tasks B1-B5 build
// the behaviour, and until they do these documents say so.
//
// The rule itself, in one sentence: a run whose own credential is a provider
// key (`agent.auth: api-key`) may declare ONE stage-scoped Claude OAuth
// account — a name of the owner's local `claude_accounts`, chosen by that
// stage's own key, `stages.<stage>.claude_account` (accounts-add spec
// §11.1-§11.2, rung 2; `default_claude_account` is the account an `oauth` run
// gives every stage and never a way into this mix) — and use it for the senior
// `review` (level 2) and the adversarial `adversarial` (level 3) stages only.
// The stage rule is an allow-list rather than a denylist, so every other stage
// is refused: `implement`, `review_first` (level 1 is the cheap first line and
// runs on the coder's model), `fix`, `plan`, and any stage a later recipe adds.
// A second provider's key is still never mixed in. What the mix costs is stated
// rather than hidden: the account's token sits in the runner's process
// environment like the provider key already does, so a prompt-injected coder
// can read it from `/proc/<pid>/environ` — the same exposure class, and the
// whole of the reason the coder's own environment is an allow-list that carries
// neither.
//
// Mutation (run, restored on 2026-11-04): the rule sentence dropped from the
// design note's §11, the exception dropped from docs/multi-model.md's §5
// refusal bullet or from its credential section, or the residual bullet
// dropped from SECURITY.md's "### Known limits", each fails here naming the
// document that lost it. The tests read each claim out of its own section
// (docSection) rather than out of the whole file, because a claim that lives
// only in a neighbouring section is not the section's claim — the same rule
// TestSecurityMdStatesTheCredentialsService was narrowed for.

// TestDocsMixedCredentialsRecordsTheRuling: the design note carries the rule
// as its own section, with the two stages it may serve, the two it may never
// serve, the second-provider refusal, the threat model and the failure
// behaviour the tasks B1-B5 follow.
func TestDocsMixedCredentialsRecordsTheRuling(t *testing.T) {
	const path = "docs/design/mixed-credentials.md"
	section := flattenDoc(docSection(t, path, readDoc(t, path), "## 11. "))

	// The stages are read from the recipe's own step names, so a step renamed
	// in the tree fails here rather than leaving the document naming a stage
	// no run has.
	for _, want := range []string{
		"`" + string(recipe.StepReview) + "`",
		"`" + string(recipe.StepAdversarial) + "`",
		"`implement`",
		"`review_first`",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("%s §11 never names %s: the rule is about which stages the account may serve", path, want)
		}
	}
	// The two stages the account may serve are the whole of the allow-list, and
	// the sentence that permits them is where a reader learns the rule is a
	// permission rather than a ban: read with "and no other stage", a rule that
	// named a third stage and said nothing else would still fail here.
	permitted := fmt.Sprintf("the account serves `%s` and `%s` and no other stage",
		recipe.StepReview, recipe.StepAdversarial)
	if !strings.Contains(section, permitted) {
		t.Errorf("%s §11 does not state the allow-list as %q: the account may serve exactly those two stages, and a rule that lists refusals instead lets the next stage a recipe adds read as permitted", path, permitted)
	}
	for _, want := range []string{
		// The rule, the one credential it permits and the one key that chooses
		// it — the stage's own. `default_claude_account` is named to refuse it:
		// it is an oauth run's default for every stage, and reading it here
		// would turn every api-key repository the account's allow_data_to
		// already covers into a mix nobody declared.
		"one stage-scoped Claude OAuth account",
		"`stages.<stage>.claude_account`",
		"never opts an `api-key` run into this mix",
		"`agent.auth` is `api-key`",
		// The rule is an allow-list, so a stage the tree does not have yet is
		// refused by the rule rather than by omission.
		"An allow-list, not a denylist",
		"any stage a later recipe adds",
		"`fix`",
		"`plan`",
		// What the run may not do: the coder's stages are the coder's
		// credential, and a second provider's key is still never mixed in.
		"never `implement`",
		"never `review_first`",
		"never a second provider's key",
		// The rule that stays: a provider pin under oauth, vertex or cascade
		// is refused as it was (CheckProviderAuth), and an OAuth token still
		// only ever reaches Anthropic's own endpoint.
		"CheckProviderAuth",
		// The threat model: who holds the token, what a hostile coder can
		// reach, and what is allow-listed. The redaction is the run's from
		// bootstrap rather than the stage's, and it builds on the check that
		// already refuses to pair an oauth environment with a gateway.
		"### The threat model",
		"`/proc`",
		"allow-list",
		"BuildEnv",
		"from bootstrap, for every stage",
		"auth: oauth never goes through the gateway",
		// Failure behaviour: no silent fallback to another credential and no
		// ready verdict on a review that never ran.
		"### A review stage that fails",
		"no silent fallback",
		// And what the note deliberately does not build.
		"### The decisions the tasks B1-B5 follow",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("%s §11 never says %q: the ruling is not recorded and the tasks B1-B5 have nothing to build against", path, want)
		}
	}

	// The ruling reverses a rule stated elsewhere in the same document, so
	// the two may not be left contradicting each other: §5's own summary line
	// and the "no decision here is still open" claim of §8 are the two that
	// go stale when §11 lands.
	whole := flattenDoc(readDoc(t, path))
	for _, want := range []string{
		"Owner ruling (2026-11-04)", // §5's Path B summary names the reversal
		"B0",
	} {
		if !strings.Contains(whole, want) {
			t.Errorf("%s never says %q: a ruling that reverses a rule the document already states has to be readable from the document's own summary", path, want)
		}
	}
}

// TestDocsMultiModelRecordsTheReviewAccountException: the user doc's refusal
// list (§5) and its credential section (§3) both carry the exception, because
// a reader meets the rule in either place: §5 states what is refused, §3 what
// a key is. The refusal list's own enumeration is pinned elsewhere
// (TestDocsCredentialsAuthListNamesCascade, which reads the values out of
// CheckProviderAuth); this test is the exception the same bullet now has to
// state, and the section that explains it.
func TestDocsMultiModelRecordsTheReviewAccountException(t *testing.T) {
	const path = "docs/multi-model.md"
	doc := readDoc(t, path)

	var bullet string
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, "- **`agent.auth` is") {
			bullet = line
			break
		}
	}
	if bullet == "" {
		t.Fatalf("%s §5 carries no `agent.auth` is refusal bullet: the list moved and this scan no longer reads it", path)
	}
	for _, want := range []string{
		"api-key",
		"one",
		"`review`",
		"`adversarial`",
		"`review_first`",
		"design/mixed-credentials.md",
	} {
		if !strings.Contains(bullet, want) {
			t.Errorf("%s §5's refusal bullet never says %q: a reader who takes it for the whole rule reads a mixed run as refused", path, want)
		}
	}

	key := flattenDoc(docSection(t, path, doc, "## 3. The key"))
	for _, want := range []string{
		"stage-scoped Claude OAuth account",
		"`review`",
		"`adversarial`",
		"`implement`",
		"`review_first`",
		"a second provider's key",
		"`allow_data_to`",
		"the runner's environment",
		"allow-list",
		"design/mixed-credentials.md",
		// The one key that admits the mix, and the one that does not: the
		// project default an oauth run uses for every stage is not a way in.
		"chosen by the stage's own key and no other",
		"stages.review.claude_account",
		"never a way into this mix",
		// The run's own anthropic-api-key mount is not a third credential of
		// this kind: it is what an api-key job already carries for the gateway.
		"anthropic-api-key",
		// The gateway does not see the stage, so its cap does not bound it:
		// the limit §6 and §7 of the design note state, said where a reader
		// meets the credential.
		"not priced or capped by the gateway",
	} {
		if !strings.Contains(key, want) {
			t.Errorf("%s §3 never says %q: the credential section is where a reader learns what a run may hold", path, want)
		}
	}
}

// TestSecurityMdStatesTheMixedRunResidual: SECURITY.md states the residual the
// ruling accepts, in the same words the rest of the document uses for the
// exposure class it belongs to — the token in the runner's environment, the
// coder's own environment an allow-list, and the `/proc` read the mix does not
// narrow. The two overclaims a security document drifts into here are refused
// by name: that the stage's credential is out of the agent's reach, and that
// the separate execution exists.
func TestSecurityMdStatesTheMixedRunResidual(t *testing.T) {
	const path = "SECURITY.md"
	limits := flattenDoc(docSection(t, path, readDoc(t, path), "### Known limits"))
	for _, want := range []string{
		"A mixed-credential run holds the review account's OAuth token in the runner's environment, the same class as the provider key it already holds",
		"`review`",
		"`adversarial`",
		"the coder's own environment is an allow-list",
		"the coder's stages' environment carries neither",
		"`/proc`",
		"the same exposure class",
		"no dollar cap of the gateway's on the stages that use it",
		"revocable at the account",
		"design/mixed-credentials.md",
		// The ruling is a ruling, not behaviour: a security document that
		// states an unbuilt mechanism in the present tense, inside a list of
		// today's gaps, claims a boundary the tree does not have yet.
		"not built yet (B1",
	} {
		if !strings.Contains(limits, want) {
			t.Errorf("%s's \"### Known limits\" never says %q: the residual the ruling accepts is a limit this document states, not behaviour it leaves a reader to infer", path, want)
		}
	}
	// The mitigation that does not exist may not be described as if it did:
	// a separate job execution for the review stage is §6.4's optional
	// hardening, and §11 keeps it unbuilt.
	if !strings.Contains(limits, "it is not built") {
		t.Errorf("%s's residual says the review stage's own job execution without saying it is not built: the fix a reader could assume exists is the one this document has to deny", path)
	}
}

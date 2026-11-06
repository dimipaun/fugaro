package cli

import (
	"fmt"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/recipe"
)

// The B0 ruling of 2026-11-04 (release 0.19.0) reverses the "a run does not
// mix credentials" rule for exactly one case, and the owner's ruling of
// 2026-11-05 widens the rung that may name the account; these tests are what
// hold the documents that state both to the rule: the design note that
// records it (docs/design/mixed-credentials.md, whose §11 is new), the
// credential section and the refusal list of the user doc
// (docs/multi-model.md), SECURITY.md's residual risk, and — since the tasks
// B1-B6 shipped in 0.19.0 — the repository's own fugaro.yaml comment and the
// owner-run smoke test (Check 290 of docs/gcp-live-checklist.md) that
// verifies the mix live. The rule is built end to end: the config rule and
// the launch (B1, B1b), the account's own job and the token it mounts (B2),
// the per-stage environment (B3), the account stage's notional accounting and
// its bound (B4) and the failure text a stage reports (B5); B6 is these
// documents, that comment and the check. What is still not built is the
// mitigation §6.4/§11 record as optional — a separate job execution for the
// review stage — and every document here says so.
//
// The rule itself, in one sentence: a run whose own credential is a provider
// key (`agent.auth: api-key`) may declare ONE stage-scoped Claude OAuth
// account — a name of the owner's local `claude_accounts`, chosen by that
// stage's own key, `stages.<stage>.claude_account` (accounts-add spec
// §11.1-§11.2, rung 2), or, since 2026-11-05, by the project's
// `default_claude_account` where that stage's own model is an Anthropic one
// (rung 4, which an `oauth` run also uses for every stage) — and use it for
// the senior `review` (level 2) and the adversarial `adversarial` (level 3)
// stages only.
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
// behaviour the tasks B1-B6 follow.
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
		// The rule, the one credential it permits and the two keys that may
		// name it: the stage's own, and — the owner's ruling of 2026-11-05,
		// which amends the 2026-11-04 reading this section recorded — the
		// project's default where that stage's own model is an Anthropic one.
		// The amendment has to be readable as an amendment: a reader who found
		// only the old sentence would take the default for a way in that
		// nothing bounds.
		"one stage-scoped Claude OAuth account",
		"`stages.<stage>.claude_account`",
		"opts such a run in too under the owner's ruling of 2026-11-05",
		"amending the conservative reading this section recorded on 2026-11-04",
		"then we get mixed runs",
		"only where that stage's own model is an Anthropic one",
		// The sentinel: a project may spell the key `default_claude_account:
		// default`, which names the unsuffixed default account and no account
		// of its own. The rule has to say that this declares no mix — a
		// document that left it to the reader would leave a project
		// wondering whether it had asked for one.
		"**no mix at all**",
		"`default` is the unsuffixed default account",
		"`agent.auth` is `api-key`",
		// The rule is an allow-list, so a stage the tree does not have yet is
		// refused by the rule rather than by omission.
		"An allow-list, not a denylist",
		"any stage a later recipe adds",
		"`fix`",
		"`plan`",
		// What the run may not do: the coder's stages are the coder's
		// credential, a second provider's key is still never mixed in, and the
		// mix needs the gateway that holds the provider key (the clause B0's
		// reviewer asked for: a provider-claimed pin under budget.mode:
		// enforce, and nothing else).
		"never `implement`",
		"never `review_first`",
		"never a second provider's key",
		"never without the gateway",
		"The mix applies only where the run can carry it",
		"budget.mode: enforce",
		"`config.CheckMixBudget`",
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
		"### The decisions the tasks B1-B6 follow",
		// Where the keys live and what carries them to a run: the file is the
		// only store of the two, the node is the only thing a run reads, and
		// `fugaro update` is the one command that closes the gap (B7). A note
		// that named the keys and never said how they reach the node would
		// leave an operator setting them and getting a mix the runner never
		// sees.
		"### Where the two keys live, and what carries them to a run",
		"`fugaro update` publishes them",
		"the project's **published policy node**",
		"a mix no run of the project has",
		"no second `fugaro project policy publish`",
		// And the one step that command cannot take for these members: the D854
		// re-seed refuses the account keys by name, and the doc says which
		// command writes them, so the operator who meets that refusal has a path
		// rather than a failure naming members no documented command sets.
		"refuses the account keys **by name**",
		"fugaro settings set default_claude_account <name>",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("%s §11 never says %q: the ruling is not recorded and the tasks B1-B6 have nothing to build against", path, want)
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
		// The two keys that admit the mix, and the condition on the second: the
		// project default an oauth run uses for every stage is a way in for the
		// two mixed stages where their own model is an Anthropic one, and no
		// further (the owner's ruling of 2026-11-05).
		"by the project's `default_claude_account` where that stage's own model is an Anthropic one",
		"stages.review.claude_account",
		"the default is a way in for those two stages and for no others",
		// The run's own anthropic-api-key mount is not a third credential of
		// this kind: it is what an api-key job already carries for the gateway.
		"anthropic-api-key",
		// The gateway does not see the stage, so its cap does not bound it:
		// the limit §6 and §7 of the design note state, said where a reader
		// meets the credential.
		"not priced or capped by the gateway",
		// Where the two keys live and what carries them to a run: the reader
		// who sets one has to learn that the file alone is not enough and that
		// the one command they already run is what publishes it (B7).
		"`fugaro update` publishes them",
		"the project's published policy node",
		"there is no second publish to run",
		// And that the credential is not a plan any more: the reader who
		// meets the rule here has to learn that the keys it names work, and
		// which owner-run check the claim rests on.
		"the whole rule is built",
		"Check 290",
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
		// The ruling is a ruling, and the mechanism it rules on shipped: a
		// security document that describes a built mechanism as unbuilt, inside
		// a list of today's gaps, tells a reader their exposure is smaller than
		// it is. B1–B5 are built (the config rule and the launch, the mount,
		// the stage's own environment and its managed settings, the notional
		// accounting, and the usage-limit text), and the only half still unbuilt
		// is the separate job execution the section says is not.
		"its tasks B1–B5 shipped in 0.19.0",
		"the job's mount of the account's token (B2)",
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

// TestDocsMixedCredentialsRecordsWhatShipped: the note was written as a ruling
// the tasks build against, and the tasks are built (0.19.0). Every place the
// document says so has to say it: the status line a reader meets first, §11's
// own status and its decision list, §5's proposal for the shape the build
// answered to, §9's "if built" bullet and §10's phase 3. A document left
// reading "nothing here is built" while the tree holds the rule, the mount,
// the per-stage environment and the notional record is the one failure this
// test exists for: it makes an owner who does have the setting believe they do
// not, and an agent believe a shipped refusal is still to come.
func TestDocsMixedCredentialsRecordsWhatShipped(t *testing.T) {
	const path = "docs/design/mixed-credentials.md"
	doc := readDoc(t, path)

	stale := "Nothing here is built"
	if whole := flattenDoc(doc); strings.Contains(whole, stale) {
		t.Errorf("%s still says %q: B1-B6 shipped in 0.19.0 and the status line is where a reader learns it", path, stale)
	}
	head := flattenDoc(doc[:strings.Index(doc, "## 1. ")])
	for _, want := range []string{
		"the tasks B1–B6",
		"0.19.0",
		"[gcp-live-checklist.md](../gcp-live-checklist.md)",
	} {
		if !strings.Contains(head, want) {
			t.Errorf("%s's status line never says %q: the ruling is a description of behaviour the tree has now, and the document's first lines have to say which behaviour it is", path, want)
		}
	}

	eleven := flattenDoc(docSection(t, path, doc, "## 11. "))
	if !strings.Contains(eleven, "built: the tasks B1–B6 shipped in 0.19.0") {
		t.Errorf("%s §11's status still reads as a ruling nothing implements: the section is the rule the tree runs on", path)
	}
	// One shipped line per area, so a reader can tell which of the five
	// decisions the tree answers and where. The area names are pinned, not the
	// sentences: what the task did is the tree's to describe.
	for _, want := range []string{"Shipped (B1", "Shipped (B2", "Shipped (B3", "Shipped (B4"} {
		if !strings.Contains(eleven, want) {
			t.Errorf("%s §11's decision list carries no %q line: the decisions read as work outstanding", path, want)
		}
	}

	// §5 proposed a shape under `stage_auth`; the build answered it under the
	// accounts design's per-stage keys. The section has to say which parts of
	// its own list shipped and which did not, or a reader takes the future
	// tense for the state of the tree.
	five := flattenDoc(docSection(t, path, doc, "## 5. Path B"))
	for _, want := range []string{"Built (B1–B6)", "Check 290", "the gateway still cannot cap it"} {
		if !strings.Contains(five, want) {
			t.Errorf("%s §5 never says %q: the section is the one that proposed this shape, and it has to read as built rather than as outstanding", path, want)
		}
	}

	// §9 ends its B bullet with the sandbox run the section asks for, and that
	// run is a check with a number: an owner who wants the evidence has to be
	// able to find it.
	nine := flattenDoc(docSection(t, path, doc, "## 9. Testing and live checks"))
	for _, want := range []string{"B, shipped", "Check 290", "never `belong`"} {
		if !strings.Contains(nine, want) {
			t.Errorf("%s §9 never says %q: the section's own B bullet is where the live run it asks for is named", path, want)
		}
	}

	// Phase 3 is the B work, and it is done: a phases list that still lists it
	// as a phase to come reads as a plan, not as a record.
	ten := flattenDoc(docSection(t, path, doc, "## 10. Phases"))
	if !strings.Contains(ten, "shipped in 0.19.0") {
		t.Errorf("%s §10's phase 3 still reads as work to come: the phase shipped as the tasks B1–B6", path)
	}
}

// TestFugaroYAMLStatesTheShippedReviewSettings: the repository's own
// fugaro.yaml comment is the file's account of why these models are what they
// are, and it drifts when the tree moves under it. It said the L1/L2 model
// guardrail would refuse this file's pair "once it ships" and that the
// independent review stays outside the cloud loop "until the per-stage OAuth
// reviewer setting exists" — and both now exist: the guard is shipped
// (`internal/config/levels.go`, D721) and the per-stage account setting is, so
// a comment that still promises them reads as a tree two releases behind.
// What the file may not do is make the owner's decision for them: the account
// is the owner's and lives in the local project config, so this file names the
// model that account serves and points at Check 290 as where the mix is read
// back from a real run.
func TestFugaroYAMLStatesTheShippedReviewSettings(t *testing.T) {
	const path = "fugaro.yaml"
	doc := readDoc(t, path)
	for _, gone := range []string{
		"once it ships",
		"until the per-stage OAuth reviewer setting exists",
	} {
		if strings.Contains(doc, gone) {
			t.Errorf("%s still says %q: the guard is shipped and the per-stage account setting exists, so the comment claims the tree is missing what it has", path, gone)
		}
	}
	// The models are the owner's and this comment may not change them: the
	// claim the comment makes about the pair is only true while it names them.
	for _, want := range []string{
		"coder: deepseek/deepseek-v4.1-flash",
		"reviewer: claude-opus-5-5",
		"D721",
		"stages.<stage>.claude_account",
		"docs/design/mixed-credentials.md",
		"Check 290",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s never says %q: the file's own comment is where the pair, the guard that judges it and the setting that would change it are stated", path, want)
		}
	}
}

// TestDocsCheck290IsTheMixedRunsSmokeTest: the owner-run check that verifies
// the mix live. It is the only place the whole shape meets a real run, so the
// facts it is written to read back are pinned here one by one: the coder's
// stage priced through the gateway, the review stage on the account with its
// model an Opus one and its name in `stage_accounts`, the verdict that says
// the review ran, the absence of the credential failure that would mean the
// stage fell back, the split that puts the account's dollars inside
// `cost.model_usd` and outside `cost.total_usd`, and the coder's environment
// holding no token at all. A check that stopped naming one of them would pass
// with that half unverified.
func TestDocsCheck290IsTheMixedRunsSmokeTest(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)

	// The row, and the section it points at.
	if !strings.Contains(doc, "\n| 290 |") {
		t.Errorf("%s's table carries no row for 290: a check an owner is meant to run is listed where every other one is", path)
	}
	section := flattenDoc(docSection(t, path, doc, "## Check 290:"))
	for _, want := range []string{
		// Who runs it, and where.
		"OWNER-RUN",
		"NOT RUN",
		"never `belong`",
		"no agent runs any step of it",
		// The coder stays where it was: a provider route, priced by the
		// gateway, and the stage's own `model call` lines are what say so.
		"provider route",
		"`model call`",
		"budget.mode: enforce",
		// The review stage is the account's, direct to Anthropic, on an Opus
		// model, and its name is in the record.
		"`review`",
		"Opus",
		"stage_accounts",
		"credential",
		"oauth",
		// The verdict: the review ran and shipped, and it did not fall back.
		"ship",
		"Invalid API key",
		// The accounting: notional inside the model figure and outside the
		// billed total, and the stage's own two figures equal.
		"model_notional_usd",
		"total_usd",
		"model_usd",
		// The coder's own environment holds no model credential.
		"CLAUDE_CODE_OAUTH_TOKEN",
		"ANTHROPIC_API_KEY",
		// What the owner pastes back.
		"Paste back",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("%s's `## Check 290` never says %q: the smoke test's whole point is the fact it reads back", path, want)
		}
	}

	// A step is runnable only if it names the right command, and that is what
	// these pins hold: making the config reach a run (the stage keys are the
	// machine's, and the runner reads the published policy node, which
	// `fugaro update` republishes for these keys since B7), committing and pushing
	// the repository's own change (the run fetches the ref), and telling the
	// refusals apart — three of them are not `fugaro validate`'s, and a check
	// that attributes a refusal to a command which never raises it sends the
	// owner looking in the wrong place.
	for _, want := range []string{
		"there is no second publish command to run",
		"`fugaro update --repo`, which publishes the two keys to the policy node",
		"it republishes the removal to the policy node",
		"policyConfigDrift",
		"Commit the `fugaro.yaml` change and push",
		"config.CheckStageAccountMix",
		"mixedStageAccount",
		"config.CheckMixBudget",
		"the agent made no commits",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("%s's `## Check 290` never says %q: a step that does not name the command behind it, or names the wrong one, is a step whose FACT an owner cannot read", path, want)
		}
	}

	// Every place the check states who runs it, and not only the section: the
	// row is what an owner skims before deciding to run anything, and the
	// register paragraph is where the number is recorded. A check whose "no
	// agent runs any step of it" lives only in a section 11,000 lines down is
	// one an agent can be pointed at by the two lines a reader meets first.
	start := strings.Index(doc, "\n| 290 |")
	if start < 0 {
		t.Fatalf("%s has no `| 290 |` row: this test's marker has moved", path)
	}
	rest := doc[start+1:]
	row := flattenDoc(rest[:strings.Index(rest, "\n")])
	for _, want := range []string{"no agent runs any step of it", "never `belong`", "NOT RUN"} {
		if !strings.Contains(row, want) {
			t.Errorf("%s's row for 290 never says %q: how a check is run, and by whom, is what its row states first", path, want)
		}
	}

	// The register paragraph is the record of the number, and 290 is a
	// reservation taken above the register's own top rather than a number this
	// work held: it is free because the console auto-update plan declared it
	// so, and a section written without that stated is a number nobody can
	// audit.
	const register290Marker = "**Check 290 is this change's"
	regStart := strings.Index(doc, register290Marker)
	regEnd := strings.Index(doc, "**That is the end of the register.**")
	if regStart < 0 || regEnd < regStart {
		t.Fatalf("%s has no register paragraph for 290 (%q … %q): the record of the number is what this test reads", path, register290Marker, "**That is the end of the register.**")
	}
	register := flattenDoc(doc[regStart:regEnd])
	for _, want := range []string{"Check 290", "290–299", "declared unused", "274–289", "Check 273", "no agent runs any step of it"} {
		if !strings.Contains(register, want) {
			t.Errorf("%s's register paragraph for 290 never says %q: the number's provenance, and who runs the check, are what make it a claim rather than a collision", path, want)
		}
	}
}

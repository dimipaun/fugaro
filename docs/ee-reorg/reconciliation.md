# Governance, the cost report and the coordinator move under `ee/`: reconciliation

Checked against `origin/main` at `028b46357` on 2026-10-28; the owner's correction of 2026-10-29 is
folded in (§0). **Where this file disagrees with [spec.md](spec.md), it wins.** It amends, and does
not replace, the subscriptions plan's Slice S1 ([../subscriptions/implementation-plan.md](../subscriptions/implementation-plan.md)),
which already plans the coordinator's and the report's moves and has not started.

Numbers: decisions **D520–D549** (D520–D536 written, D537–D549 reserved and unused), checks
**200–209** (200–201 proposed, 202–209 reserved and unused). Findings `F1`…, resolutions `R1`… and
open items `O1`… are local to this folder.

## 0. The ruling, as corrected

The spec (verbatim, 2026-10-29) moves governance, the cost reports and the coordinator into `ee/`,
and its answers line counted digest reports as governance, reversing the subscriptions ruling T11.

**Correction, 2026-10-29 (the owner: "sorry, my mistake"): the digest stays MIT.** T11 stands.
`fugaro runs digest`, the `run-digest` recipe, the runner's digest step (`internal/runner/digest.go`),
`internal/digest`, the periodic-reviews skill text and `--submit-draft` stay MIT, with their own
MIT copy of what they use from `internal/report`. The subscriptions plan's `TestDigestNeedsNoEE`
(S1.5) stands as written. `spec.md` keeps the original wording because it is verbatim; this section
and the README record the correction.

So **what moves** is:

- **governance:** budgets, tiers, tripwires, checkpoints and stage timeouts — the configuration
  block, its policy and defaults, the tier expectations, the tripwire and checkpoint halts, the stage
  timeout, the tier fields' meaning, and the recorded figures that only governance reads;
- **`fugaro report`** (A15/D444) and **the coordinator**, command and library (A5/D434, T10).

**What stays MIT:** the digest (above); the credentials service's audit log (D444); spend caps, the
kill switch and rigor modes (subscriptions R4/F8, and D520 below).

Owner's answers taken as given (not re-asked): hold CLI 0.13.0 until governance is under `ee/`;
in-flight governance work finishes in `internal/` and moves once; T27 is ruled **(b): paid commands
warn, not refuse, until enforcement ships** (the subscriptions docs still list T27 as open: F4).

## 1. Headline: what the repository already has

| Ruling's concept | What exists on `main` |
| --- | --- |
| `ee/` folder, licence, boundary | `ee/LICENSE` (counsel's text), `ee/README.md`; `internal/eeboundary/boundary_test.go` (the one route `cmd/fugaro/ee_on.go` → `ee/register`, the two-line notice); `.claude/rules/ee.md`. No code under `ee/`; `cmd/fugaro/ee_on.go` does not exist |
| The `ee` build tag in a release | Planned only: subscriptions S1.6. `.goreleaser.yaml`, `scripts/release.sh`, `.github/workflows/ci.yml` set no `ee` tag. `scripts/notices_test.go` `TestEETagNeedsTheLabels` already couples the tag to the labels |
| The coordinator and report moves | Planned only: subscriptions S1.1–S1.5, S1.9; none merged |
| A command registry for paid commands | Planned only: S1.2 (`internal/cli/extend.go`) |
| A key verifier | Planned only: S2.3 (`ee/licence`); no key exists |
| Governance | Merged in `internal/`, spread through shared packages (F7); **never released** except the 80% warning and three record fields (F3) |
| A build with `ee/` deleted | Staged only: `docs/pending-workflows/core-without-ee.yml`, pinned by `internal/cli/docs_core_without_ee_workflow_test.go` |

## 2. Findings

### Rulings and documents

**F1 — Rule: three texts say nothing moves out of the core.** `ee/README.md` ("It is not a place
for anything that lives in the core today: what is already published under MIT stays there"; "The
folder holds no code yet"); `.claude/rules/ee.md` ("Nothing moves out of the core into `ee/`
(D302)"); `internal/cli/docs_ee_reserved_test.go` `TestDocsEELicensed`, which pins that `ee/` holds
exactly `LICENSE` and `README.md`. The README sentence was written by an agent, not ruled; D302 was
the owner's (licensing, 2026-10-24) and was already reversed for the coordinator by D434.
`docs/licensing/spec.md` carries the owner's own line "Nothing gets moved out of the core to be
sold"; it stays verbatim, superseded by this ruling. *Consequence:* task E0.2 rewrites the README
and the rules file; S0.6's planned "`ee/` stops being empty" change is folded into it.

**F2 — Rule: the Subscription Terms say the coordinator is Core.** `docs/licensing/subscription-terms.md`
and `website/content/subscription-terms.md` both say "the optional credentials service and the
coordinator … (both are part of the Core today, not of the Software)". After A5 the coordinator
half is false; the credentials-service half stays true (D444). That text is counsel's; no test pins
the sentence. *Consequence:* D531 (answered: edited now, E3.6).

**F3 — Exists: almost none of the governance that moves was ever released.** By `git tag
--contains` and `git show <tag>:<file>`: released under MIT are `calls` and `agents` per stage
(first in v0.11.6), `review_scope` and the 80% daily-cap warning (`internal/budget/warn.go`,
`dailyWarnNotes` in `internal/budget/precheck.go`, `internal/watch`; v0.12.1), and `fugaro runs
digest` (v0.12.0) and the `run-digest` recipe text (v0.12.1); the runner's digest step
(`internal/runner/digest.go`, `33fd7c81f`) is in no tag. The digest stays MIT anyway. Not released: the `governance:` block (#1185, #1194),
the governance halt and checkpoint report (#1182 and follow-ups), the stage timeout (T7.6, #1207),
`repeats` (T1.4, #1204), the tier vocabulary and fields (T8.1a, #1212). `governanceSince` and
`digestSince` in `internal/cli/recipes_skew.go` both name `0.13.0`. `fugaro report` has shipped
under MIT since v0.1.0 (`newReportCmd` in v0.1.0's `internal/cli/root.go`) and the coordinator in
every release from v0.11.0; both through v0.12.1 (subscriptions R3 says "v0.11.0 through v0.12.0";
it must say every release through v0.12.1). *Consequence:* the hold means
the governance features proper never ship under MIT. R1.

**F4 — Gap: T27 is recorded open in the subscriptions docs.** `docs/subscriptions/reconciliation.md`
("A consequence the rulings create") and the plan's S1.1/S1.6 "wait on the owner's ruling on T27".
The ruling given with this spec is (b). S1.1's gating model, "a paid command is present and refuses
without a key", contradicts it. *Consequence:* E0.1 records T27 (b) and replaces the gating model
(R4).

**F5 — Gap: A-numbers are not defined in `docs/`.** A5, A6, A15, A18 appear only in `spec.md`. By
the subscriptions table, An = D(429+n): A5 = D434, A6 = D435, A15 = D444, A18 = D447. This file
cites the D numbers.

### The code that moves

**F6 — Exists: `fugaro report` and the coordinator, as subscriptions S1.1 lists them, plus three
gaps.** S1.1's file lists hold (re-verified: `internal/report/`, `internal/cli/report.go`
`newReportCmd`; `internal/coordinator/`, `internal/cli/coordinator.go` `newCoordinatorCmd`,
`coordinator_view.go`). Gaps:
- the digest also uses `report.USD6` (`internal/digest/patterns.go`, `render.go`), which S1.5's MIT
  copy list (`Dim`, `ParseDim`, `PeriodKey`, the period keys, `USD`) omits;
- `internal/cli/project.go` `consoleReadPaths` names `"fugaro report"` (the console's read table);
- the coordinator's sweep runs in the **history image** (`images/history/Dockerfile`, untagged,
  `COPY cmd internal images deploy/terraform`, no `ee/`), started by
  `deploy/terraform/gcp/modules/installation/coordinator.tf` (`/usr/local/bin/fugaro coordinator
  sweep`); and `internal/infra` (`CoordinatorSpec`, `CoordinatorImageExists`), `internal/infra/tf/cover.go`
  (`toCoordinator`) and `--coordinator` (`internal/cli/init.go`) are core code that set the job up.
  Those stay core (they deploy a job; the job's binary is what is licensed).
*Consequence:* E2.1/E2.2 carry S1.4/S1.5 with the additions; D524 decides the sweep's image.

**F7 — Gap: governance is not a package; it is fields and branches in eleven shared packages.**
Whole files that are governance only: `internal/tier/tier.go`; tests `internal/config/governance_test.go`,
`internal/config/tier_test.go`, `internal/policy/governance_test.go`, `internal/policy/tier_test.go`,
`internal/task/tier_test.go`, `internal/runner/stage_timeout_test.go`,
`internal/cli/governance_skew_test.go`. Mixed into shared files:
- `internal/config`: `Config.Governance`, types `Governance`, `GovernanceTiers`, `TierLimits`,
  `StageBudget`, `StageTokens`, `StageTimeout` (`config.go`); `ProjectLayer.Governance`,
  `SetsGovernance` (`layer.go`); `setsGovernance` (`resolve.go`); `validateGovernance` (`validate.go`);
  the `governance.*` rows of `scope.go`;
- `internal/policy`: `DefaultGovernance`, `DefaultTierExpectations`, `Effective.Governance`
  (`policy.go`); `tightenGovernance`, `tightenTier` (`merge.go`);
- `internal/task`: `Spec.Tier`, `Spec.ExpectedScope` (`task.go`, imports `internal/tier`);
- `internal/runstore`: `StageTiming.Calls/Agents/Repeats`, `Record.ReviewScope`, `Record.Checkpoint`
  (`Checkpoint`), halt reasons `HaltStageBudget`, `HaltReviewRatio`, `HaltFanout`, `HaltReviewScope`,
  `HaltExpectedExceeded`;
- `internal/runner`: `governanceHalt`, `checkpointPR` (`halt.go`), `checkpointReportPart`
  (`prflow.go`), `checkpointPrompt` (`prompts.go`), `stageTimeout` and the stage loop's
  `governanceHalt(…, HaltStageBudget, "time")` (`runner.go`), the governance ceiling (`policy.go`),
  `readReviewScope` (`diffbase.go`), `stageTiming(…, subAgents, repeats)` (`cost.go`);
- `internal/agent`: `SubAgents`, `Repeats` (`relay.go`, `agent.go`);
- `internal/gateway`: `StageReport.Agents` (`gateway.go`, `ledger.go`), never recorded;
- `internal/cli`: `governanceSince`, `refuseGovernanceSkew`, `checkGovernanceImage`,
  `governanceImageWarnings` (`recipes_skew.go`), `gateGovernance` (`init.go`), the launch gates
  (`run.go`), `validate.go`, `diagnose.go` (`checkpointBlock`);
- schemas: `schemas/fugaro.schema.json` and `project-layer.schema.json` (`governance`),
  `result.schema.json` (`calls`, `agents`, `repeats`, the five reasons, `checkpoint`,
  `review_scope`), `task.schema.json` (`tier`, `expected_scope`).
Only `stage_timeout` is enforced today; the other keys are decoded and validated, and nothing reads
them until T7.2. `internal/runner/checkpoint.go` and `checkpoint_schedule.go` are the older
**checkpoint pushes** (design `checkpoint-pushes.md`, October 8), not governance: they stay core.
`runstore.Review.Tier` and the credentials service's tiers A/B/C are other "tiers". *Consequence:*
"moving governance" is a mechanism/policy split, not a `git mv` (D521).

**F8 — Gap: the digest (MIT) reads governance's data.** `internal/digest/digest.go` counts the five
governance halt reasons, reads `calls`, `agents`, `repeats` and `review_scope`, and raises two
report-only flags (a stage at five repeats or more; review-scope drift, `87b032613`). The planned
T8.3 adds per-tier calibration and an `ExpectedScope` drift flag to the digest. *Consequence:* the
record fields and the halt enum stay core so the MIT digest can read them (D521); the flags (D528).

**F9 — Gap: strict decoding makes the configuration block a compatibility surface.** `config.go`,
`layer.go`, `resolve.go` decode with `KnownFields(true)`; `task.Parse` with
`DisallowUnknownFields`. A runner before `governanceSince` refuses `governance:` at bootstrap, and in
the project layer that stops every repository of the project (`recipes_skew.go`'s comment). If the
untagged core stopped knowing the key, an untagged 0.13.0+ runner would do the same to a free user
whose project layer or repository carries a block. *Consequence:* the core keeps decoding the block
(D521) and an untagged build warns and ignores it (D522).

**F10 — Rule: R4/F8 (subscriptions) keep spend caps free; the ruling says "budgets".** Spend caps,
the per-run cap, daily caps, the kill switch (`internal/budget`, `newBudgetKillCmd`) and rigor modes
(`internal/rigor`, with its per-mode budgets, #1018) are "the safety features … free core" (F8) and
D436 keeps the MIT CLI launching and cancelling. Governance's budgets are the stage budgets, the
review ratio, the fan-out limit, stage tokens and the tier expectations (task-governance D330–D332).
*Consequence:* D520.

### Builds, images, release

**F11 — Gap: governance runs inside the job, and the run images are untagged by rule.** Stage
timeouts (runner), the halt and checkpoint (runner), and the planned stage budgets (gateway, T7.2)
execute in the job's `fugaro`, which `images/base/Dockerfile` builds from source (`go build`, no
tags, `COPY cmd internal images deploy/terraform`, no `ee/`). Subscriptions R1 (as narrowed by T10)
says the run images stay untagged and S1.7 pins it (`TestRunImagesBuildFugaroWithoutTheEETag`).
F46 there already noted the strain for the digest. With governance in `ee/`, an untagged base image
carries no governance. *Consequence:* D524 (amends R1 and S1.7).

**F12 — Gap: the job image's version is not its edition.** `checkGovernanceImage` and
`refuseGovernanceSkew` judge the base image by release version. A self-built or core 0.13.0 image
would pass the version gate and silently ignore the block. *Consequence:* R3 (an edition marker).

**F13 — Exists: the labels gate.** `TestEETagNeedsTheLabels` (`scripts/notices_test.go`) refuses a
release that sets the tag while any licence label says MIT. *Consequence:* E3.2 sets the tag and the
labels together (S1.6), in 0.13.0 (D526).

**F14 — Gap: the base image is public.** `images/verify-public.sh` proves every published image
reads without a login; the base image is `ghcr.io/dimipaun/fugaro-base`. A tagged base image
publishes compiled `ee/` code to anyone. The source is public too (`gh repo view`: PUBLIC), so this
adds distribution of a compiled form, which `ee/LICENSE` covers ("in source form and in every
compiled form built from it, including code from those directories compiled into a released
binary"). *Consequence:* D524 and O2 (the owner and counsel confirm).

**F15 — Exists: who wrote the code that moves.** `git log` of `internal/coordinator`,
`internal/report`, `internal/tier`, `internal/config`, `internal/policy`, `internal/gateway`,
`internal/runner` and the moving `internal/cli` files: authors are the owner, the owner's
`fugaro-dev[bot]` app and `Fugaro <fugaro@users.noreply.invalid>` (the product's own runs); trailers
add the owner and Claude models. `dependabot[bot]` appears only as a co-author trailer on two merge
commits (`f01887f37`, `86fb57c9f`) that carried its dependency bumps through, not as an author of
these files. The CLA (`docs/licensing/CLA.md`) is final but not in force (no bot; CONTRIBUTING holds
outside PRs to `ee/`, `console/`, `mobile/`). *Consequence:* no outside contribution needs a grant
for the move; the copyright status of agent-written code is a legal reading for the owner (O3).

**F16 — Gap: open PRs and runs under the files that move.** #1216 (T7.6 follow-up:
`internal/runner/runner.go`, `halt.go`, `stage_timeout_test.go`, `internal/cli/validate.go`,
`schemas/result.schema.json`) is governance. #1215 (T3.3 follow-up: `internal/runner/digest.go`,
`prflow.go`, `internal/gitops/gitops.go`) is the digest, MIT now, but touches `prflow.go`, which the
move edits. #1196 (`report --by mode` leftovers: `internal/cli/report.go`, `report_test.go`,
`console/docs/json-contract.md`, the working skill) edits a moving file. #1188 (console Run detail
draws diagnose's spend) reads record fields that stay core. T8.1b (`--tier`) has no PR yet.
*Consequence:* D527 (the cut-off) and the plan's first level.

**F16a — Gap: the launch declares to the coordinator through the library.** `internal/cli/run.go`
calls `coordinator.Reporter{…}.Report` on every launch. S1.2's launch hook "defaults to nothing", so
a hook that lands before the move would stop every launch's declaration, and after the move an
untagged CLI's launches are never declared to the coordinator. *Consequence:* E1.1's default is
today's declarer until E2.1 moves it; D535 records the after-move consequence.

**F16b — Gap: the build context drops `ee/`.** The root `.dockerignore` admits only `go.mod`,
`go.sum`, `cmd/`, `internal/`, `images/` and `deploy/terraform/`, so a `COPY ee` in a Dockerfile
fails. *Consequence:* E1.4 edits `.dockerignore` and tests the context.

**F16c — Gap: the label gate does not see the images.** `eeTagLabelGaps` (`scripts/notices_test.go`)
reads `.goreleaser.yaml` and `release.yml` only; `images.yml` publishes the public base and history
images on any release tag. A tagged image published before the relabel would carry `ee/` code under
MIT labels with no gate to stop it. *Consequence:* the images take the tag only after E3.2, and E3.2
extends the gate to the image builds.

**F17 — Exists: the console consumes two moving commands by argv.** `console/src/main/spawn.ts`
(`report --json`, `coordinator status --from-store --all --json`), `src/source/cli.ts`, screens
`spend.ts` and `coordinator.ts`, contract `console/docs/json-contract.md`. It reads `diagnose --json`
(`stages`, `checkpoint`), which stays core. Mobile and `shared/` consume none of these.
*Consequence:* a tagged CLI keeps both screens working under T27 (b), provided the warning goes to
stderr (R4). An untagged CLI (a source build) shows the console a refusal (D522).

**F18 — Exists: the plugin and the docs name the moving surface.** `plugin/skills/working/SKILL.md`
(`fugaro report --by …`), `reference/diagnose.md` ("Governance halts", five reasons),
`plugin/skills/setup/reference/decisions.md` (planned T7.4), `plugin/skills/routing/SKILL.md` (the
0.13.0 gate); `website/content/cli-reference.md`, generated by `internal/clidoc`
(`TestCliReferenceIsCurrent`) and checked by `internal/cli/docs_website_cliref_test.go`;
`docs/project-layer.md` (governance rows, pinned by `internal/config` `TestScopeTableMatchesDocs`);
`docs/recipes.md`. No skill or website page says governance is free. The reference generator
(`internal/clidoc/cliref.go`, `cliref_test.go`) and the plugin's command checks
(`plugin/routing_skill_test.go`, `plugin/setup_skill_test.go`) build `cli.NewRootCmd()` in core, and
no core file may import `ee/register` under any tag, so they can never see a registered command's
flags: after the move they would see only the stubs, and the working skill's `fugaro report --by …`
would fail the plugin's flag check. *Consequence:* E3.1; D536 and E1.7 (the tagged tree).

## 3. Resolutions (one defensible answer each)

- **R1 — Only future versions move.** Every CLI release up to v0.12.1 stays MIT, including the
  coordinator, `fugaro report`, the `calls`/`agents`/`review_scope` fields and the 80% warning
  (extends subscriptions R3 to v0.12.1). Anyone may keep using and forking those. The governance
  features proper were never released (F3), so under the hold their first release is under
  `ee/LICENSE`.
- **R2 — The route is the planned one.** Paid code reaches the binary only through
  `cmd/fugaro/ee_on.go` → `ee/register` (subscriptions S1.3); no boundary-test edit. The core exposes
  hooks with core defaults (no-ops); `ee/register` fills them. Hooks this ruling adds beyond S1.2:
  a governance policy hook (defaults and tighten-only merge), a runner stage hook (the stage timeout
  and governance halts), a gateway stage-limit hook (for T7.2 onward), and a digest section hook
  (D528).
- **R3 — An edition marker; the job image's edition decides.** Governance is enforced by the job
  image's `fugaro`, not by the launcher's (F11): a source-built untagged CLI that launches onto the
  official tagged base still gets governance enforced, so no launch-time warning may say "ignored"
  because of the launcher's own edition. The binary knows its edition (`core` unless `ee/register` set it),
  and the base image's build records it where the governance gates can read it (E1.3 chooses the
  carrier: today `checkImageSince` judges the image by its release version only, and `fugaro
  version` has no machine-readable form, while the console's `cli.version` operation parses its
  text, so the text does not change). The launch and `fugaro update --repo` gates
  (`checkGovernanceImage`, `checkCloneGovernanceImage` for a `checkout: clone` workflow,
  `refuseGovernanceSkew`) and `fugaro validate` (`governanceImageWarnings`, which today takes no image
  argument) then warn when a repository or the project layer
  sets `governance:` and the job image is `core`. A warning, not a refusal: a core runner ignores the
  block (D522), so nothing breaks. An image with **no** carrier (every image before E1.4, and every
  dev image) is "unknown" and gets no edition warning, which is today's behaviour. The marker is advisory: a self-built image can claim `ee` without
  the tag and silence the warning (R5's point); the carrier is written from the image's own `fugaro`
  at build time, so it at least never disagrees with an honest build.
- **R4 — T27 (b) in practice.** A paid command or governance in a tagged binary runs and prints one
  notice on **stderr** (never stdout, so `--json` stays parseable for the console). With no key
  mechanism shipped, the notice cannot be conditional on a key (D523). Cancel and the kill switch
  are core and never touched (subscriptions R4/T12).
- **R5 — Source-available, in this public repository.** `ee/` stays here (D431 kept counsel's
  source-available licence and rejected closed binaries). Key-gating is a licence instrument, not a
  security boundary: anyone can build the source with or without the tag or patch a check out; the
  licence, not the code, is what binds them. Stated for the owner, neutrally.
- **R6 — No secret enters a job.** Under T27 (b) the job needs no key. When enforcement ships, the
  entitlement is decided by the launcher (where the organisation's key lives, D438) and travels in
  the task as a claim the runner obeys, never as a key; a user who controls their own bucket can
  forge that claim, which is the R5 point again. Subscriptions S1.9's key secret on the coordinator
  job is dropped for 0.13.0 (D524).
- **R7 — Wire formats do not change.** The move renames no JSON or YAML field, adds no required
  field and keeps every schema in `schemas/` (MIT: a format is not a feature). Older CLIs read the
  new records with plain `json.Unmarshal` as before.
- **R8 — Tests move with their code.** Moved tests run with `-tags ee`; core tests that exercised
  governance through the runner keep only the core-default path (hook absent → nothing enforced).

## 4. Decisions

All answered (2026-10-29). D523, D531 and D533 are NOT the recommendation; the rest took it. Recommendation first.

| # | Question | Recommendation and why | Alternative and its cost | Owner's answer |
| --- | --- | --- | --- | --- |
| **D520** | What does "budgets" cover? | **Governance's budgets only: stage budgets, stage tokens, the review ratio, the fan-out limit and tier expectations. Spend caps (per-run, daily), the 80% daily-cap warning, the kill switch and rigor's per-mode budgets stay MIT.** The caps are the free product's safety net (R4/F8, D436), and the 80% warning shipped under MIT in v0.12.1 | Move the caps too: a free install has no spend ceiling, which contradicts R4/D436 and the released MIT code anyone can keep | 2026-10-29, the recommendation taken: governance budgets only move; spend caps, the 80% warning, the kill switch and rigor's per-mode budgets stay MIT, because they are the free product's safety net. |
| **D521** | Where is the line between core and `ee/` inside governance? | **Core keeps the wire and the mechanism; `ee/` takes the policy.** Core: the `governance:` YAML types and their strict decoding and shape validation, the schemas, `task.Spec.Tier`/`ExpectedScope` and `internal/tier` (the four-word enum the task validator uses), the record fields (`calls`, `agents`, `repeats`, `review_scope`, `checkpoint`), the halt-reason enum, and the runner's generic halt-with-checkpoint-report plumbing that `diagnose` and the digest read. `ee/`: `DefaultGovernance` and the tier defaults, `tightenGovernance`/`tightenTier`, the stage timeout's enforcement, every tripwire that halts (stage budget, tokens, ratio, fan-out, review-scope stop, expected cost), the checkpoint model call (T7.2–T7.3), the tier expected-cost checkpoint (T8.2) and the tier calibration (T8.3). The launch-time skew gates (`checkGovernanceImage`, `refuseGovernanceSkew`) stay core: they protect a free user from an old runner refusing the block (F9). `--tier` (T8.1b) stays a core flag, because it only writes the core `tier` field; what a tier means is `ee/`'s. Why: the MIT digest and `diagnose` read the records (F8), and strict decoding needs the core to know the block (F9) | **Move the decoders too** (ee registers the block): an untagged runner refuses `governance:` and stops every repository of a project that publishes one (F9). **Move the plumbing too:** the core halt path forks in two | 2026-10-29, the recommendation taken: core keeps the wire and the mechanism, `ee/` takes the policy, so the MIT digest and `diagnose` keep reading the records. |
| **D522** | How does an untagged build treat the paid surface? | **Config and task fields: an untagged runner (a core job image) decodes them, writes one warning to the run log, and ignores them** (`governance:` in `fugaro.yaml` or the project layer; `tier`, `expected_scope` on a task). The launcher, `fugaro validate` and `--tier` warn **only by the job image's edition** (R3), never by their own. The `expected_scope` drift flag of the MIT digest still reads the field (D528). **Commands whose code is absent** (`fugaro report`, `fugaro coordinator …`): a core stub that exits non-zero with "part of Fugaro Enterprise; this build does not include it" — nothing can run, so warn-and-continue (T27) does not apply | Leave them unknown to cobra: "unknown command" misleads a source builder and the console |  2026-10-29, the recommendation taken: an untagged runner decodes the fields, writes one warning to the run log and ignores them; the paid commands are core stubs that exit non-zero with the Enterprise message. Read with D523: "warn once" is a capability statement for UNTAGGED builds (the governance block is ignored in this build), not a licensing warning. |
| **D523** | What does a tagged binary warn, before any key exists? | **An unconditional one-line stderr notice on each paid command (`report`, `coordinator`), naming `ee/LICENSE`, and one line in the run log of a tagged job that enforces `governance:`; no verifier in 0.13.0.** S2.3 and the key service are not needed to ship the hold | **Ship S2.3 first and warn only without a valid key:** 0.13.0 then waits on the verifier, the signing keys (S3.8) and hand-minted keys (S2.10) |  2026-10-29, NOT THE RECOMMENDATION: no notice at all (no stderr licence line on `report` or `coordinator`, no run-log licence line) until the verifier, the key-minting service and the launch are ready; the owner decides later, and for now everything is available to all. Consequences: the plan's warn-related tasks (the always-on stderr notice, the run-log line, and subscriptions T27's warn-not-refuse behaviour) are DEFERRED with the key work, and the 0.13.0 notes carry no licence warning. |
| **D524** | Which images carry the `ee` tag? | **The base image and the history image (the coordinator's sweep) are built with `-tags ee` and `COPY ee` (with `.dockerignore` admitting `ee/`, F16b), and only once the relabel has landed (F16c); the credentials image stays untagged. No key secret is mounted (R6).** Amends subscriptions R1, S1.7 (the scan now requires the tag on base and history and its absence on credentials) and S1.9 (no separate coordinator image, no key secret until enforcement). Governance must run where the job runs (F11) | **Two base images (core and ee):** doubles the image builds, scans, signing and mirror entries, and every repository chooses one. **Untagged images:** governance never runs in a job, so the move removes the feature | 2026-10-29, the recommendation taken: base and history images built with `-tags ee` and `COPY ee`, only after the relabel; the credentials image stays untagged; no key mounted, because governance must run where the job runs. |
| **D525** | The run images are public (F14): may anyone pull a tagged image? | **Yes, as with the public source; the licence governs use.** The owner and counsel confirm (O2) | A private registry for the tagged images: every installation then needs registry credentials | 2026-10-29, the recommendation taken: tagged images stay public and the licence governs use; counsel to confirm, so O2 stays open. |
| **D526** | What is 0.13.0? | **The first tagged release: governance, `fugaro report` and the coordinator under `ee/`, the relabel of S1.6 (one binary, MIT core plus business-licensed parts), shipping while `ee/LICENSE` still shows the CLA as a bracketed placeholder and the Subscription Terms are not in force** — the owner and counsel confirm that a release may carry that text (O1) | **0.13.0 untagged:** governance, `report` and the coordinator are absent from the release; the console's Spend and project views break. **Hold 0.13.0 until counsel signs off the Terms:** a longer hold | 2026-10-29: the owner agreed that 0.13.0 is the first tagged, relabelled release, shipping while `ee/LICENSE` still shows placeholders; O1 is ANSWERED (below): the CLA Gist URL is published first, then the release. The placeholder question narrows to the Subscription Terms, a live draft page; counsel's sign-off on shipping with the Terms not in force stays a stated open item and does not block the tag (the owner). The new task E3.5 replaces the CLA placeholder. |
| **D527** | What counts as "in flight", and when is the cut-off? | **In flight: #1216, #1215, #1196, T8.1b and any governance task the owner has approved before the move series' first move PR (E2.x) opens. After that, new governance tasks (T7.2–T7.5, T8.2, T8.3) are born in `ee/`, and nothing new is launched against the moving files** | Finish the whole governance plan in `internal/` first: 0.13.0 waits on T7.2–T8.3, the money-path work | 2026-10-29: consistent with the owner's 2026-10-29 ruling that in-flight governance work finishes and merges in `internal/` and is moved once; the cut-off list stays as proposed, not separately answered. |
| **D528** | The digest's governance-flavoured parts | **The two report-only flags (repeats ≥ 5, review-scope drift) stay in the MIT digest: they halt nothing (task-governance G8). T8.3's per-tier calibration is born in `ee/` and joins the digest through a section hook; an untagged digest omits that section. The `ExpectedScope` drift flag stays MIT with the digest** | Move the flags as tripwires: the free digest loses its only pattern signals |  2026-10-29, the recommendation taken: the two report-only flags and the `ExpectedScope` drift flag stay MIT; T8.3's calibration is born in `ee/` and joins the digest through a section hook. |
| **D529** | `fugaro diagnose` and the console's run detail show governance halts and checkpoints | **They stay MIT and keep rendering the record** (diagnose is core, F8; the record is core under D521) | Gate the rendering: a free user sees a halt they cannot read |  2026-10-29, the recommendation taken: `diagnose` and the console's run detail keep rendering halts and checkpoints (MIT). |
| **D530** | Where does the moved code live? | **`ee/governance/` (policy, defaults, tripwires, timeout, checkpoint call, tier calibration), `ee/report/`, `ee/coordinator/` (library) with `ee/report/cli`, `ee/coordinator/cli`, `ee/governance/cli`; `ee/register` wires all three.** Mirrors S1.4/S1.5 | One `ee/cli` package: a single import hub every paid feature edits |  2026-10-29, the recommendation taken: `ee/governance`, `ee/report`, `ee/coordinator` with their per-area cli packages; `ee/register` wires them. |
| **D531** | The Subscription Terms say the coordinator is Core (F2) | **Counsel's next revision (subscriptions S0.4) drops "and the coordinator"; until then the website keeps the text and the README of this folder lists the discrepancy** | Edit the website text now: changes counsel's wording without counsel |  2026-10-29, NOT THE RECOMMENDATION: the owner ruled to EDIT the website's Subscription Terms text NOW to drop "and the coordinator", without waiting for counsel's next revision (a docs run makes the edit; the owner tells counsel in the next revision round). Recorded as task E3.6. |
| **D532** | `ee/README.md` and `.claude/rules/ee.md` | **Replace "net-new organisation features … what is already published under MIT stays there" with: `ee/` holds the business-licensed features, new ones and those the owner moved from the core (D434, D444, this ruling); released MIT versions stay MIT (R1). Keep the three rules** | — (one defensible text; written as a decision because the README is the public statement) |  2026-10-29: decided by the owner's 2026-10-29 ruling that there is no rule that what is already published under MIT stays there; the sentence in `ee/README.md` and `.claude/rules/ee.md` is replaced by the one defensible text of the recommendation. |
| **D533** | Release notes for 0.13.0 | **A "Licensing" highlight and a `### For operators` entry: per installation, `fugaro update` (re-pins the base and copies the history image the coordinator's sweep runs; `fugaro update --base base` for the base alone); per repository, `fugaro image refresh` in each repository's checkout so its workflow images rebuild on the tagged base; an installation that keeps a core (self-built) image gets the warnings of D522** | No operator step: an installation on a 0.12.x image keeps refusing `governance:` (F9), which is today's behaviour anyway |  2026-10-29, NOT THE RECOMMENDATION: the 0.13.0 notes say only that licensing updates, with NO operator steps ("nothing to do for now"). The tension this leaves is open item O7 (not decided here). |
| **D534** | The CLA and outside contributions to the moved files | **None needed: no outside author (F15). The owner confirms the legal reading of agent-written code (O3)** | Ask counsel before moving: delays nothing technical but holds 0.13.0 |  2026-10-29, the recommendation taken: nothing needed, no outside author; the owner's confirmation of the agent-code reading stays open as O3. |
| **D535** | After the move, an untagged CLI's launches are not declared to the coordinator (F16a) | **Accept: the released binary is tagged (T21), so only source builds are affected; their runs still appear through the sweep, which reads the runs bucket** | Keep the declarer in core: a piece of the coordinator stays MIT, against D434 |  2026-10-29, the recommendation taken: accepted. |
| **D536** | Which command tree do the CLI reference and the plugin checks read (F18)? | **The tagged tree, through `ee/`: a generator `go run ./ee/clidoc` (it imports `ee/register` and a new importable `internal/clidoc/cliref` package split out of `internal/clidoc`, which is `package main`) writes `website/content/cli-reference.md`, and an `ee/clidoc` test holds it current; the core test keeps checking that every core command and flag is in the file. The plugin's flag checks for a paid command run in an `ee/` test; the core plugin tests accept a paid command name through the stub list of E1.1.** One binary ships (T21), so the reference describes it | **The untagged tree:** the reference and the skills lose `fugaro report` and `coordinator`, which the released binary has | 2026-10-29, the recommendation taken: the tagged tree, through `ee/`; the `go run ./ee/clidoc` generator writes the reference and an `ee/clidoc` test holds it current. |

## 5. Open tensions and open items for the owner

- **O7 (D533 against D523 and D524). OPEN, the owner's to decide; not decided here.** D524 ships
  governance in a tagged base image, so on an installation that never refreshes its images
  governance stays refused (F9) although D523 says everything is available to all: it is available
  only after the operator refreshes images, and the 0.13.0 notes (D533) give no operator step. The
  choices: (a) accept it; (b) add a one-line "refresh images to get governance" note to the notes
  without making it a required step.
- **O1 (D526). ANSWERED 2026-10-29 by the owner: publish the CLA Gist URL first, then release**
  (not the recommendation, which was to ask counsel and hold). The owner creates the Gist from
  `docs/licensing/CLA.md` and sends its Raw URL; a docs PR replaces `[CLA URL TO BE PUBLISHED]`
  everywhere it appears (task E3.5); the 0.13.0 tag stays held until that PR has merged and the
  re-org's E-slices are done. Consequence for D526: the placeholder question narrows to the
  Subscription Terms, which are a live draft page; counsel's sign-off on shipping with the Terms not
  in force remains a stated open item, and the owner does not make it block the tag.
- **O2 (D524, D525). Answered (the recommendation), 2026-10-29:** public tagged run images, the licence governs
  use. Counsel's confirmation is still wanted and stays recorded as such.
- **O3 (D534). STILL OPEN: the owner's confirmation (D534 answered: nothing needed).** The copyright reading of code written by the product's own agent runs and by Claude
  sessions, which the owner relicenses for future versions. Stated neutrally; not a finding.
- **O4 (D523 against T17). Resolved by D523 as answered:** there is no notice, so no screen can break.
  (Earlier text: Console 0.113.0 is honour-based (T17) and spawns `report --json` and
  `coordinator status`; under D523 a tagged CLI only adds a stderr line, so no screen breaks. No
  tension remains if D523 is answered as recommended; if the owner prefers S2.3 first, T27's original
  question returns.)
- **O6.** Between the governance move (E2.3) and the tagged images (E1.4), images built from `main`
  ignore `governance:` and the history image's sweep hits the stub; a dogfood installation should
  not refresh from `main` in that window. The owner confirms that, or asks for E1.4 to be reordered
  (which needs the relabel earlier, F16c).
- **O5.** `docs/subscriptions/` must record T27 (b), the reversal and the correction of T11 (none),
  D524's amendment of R1/S1.7/S1.9 and the moved scope; E0.1 does it once the decisions are answered.

## UNVERIFIED

- **U1.** Whether `images.yml` (the workflow that builds the base and history images) passes build
  arguments that a `-tags ee` change in the Dockerfiles needs; read only through
  `images/build-base.sh` and the Dockerfiles. A workflow change is staged, never edited (E1.4).
- **U2.** A derived image built from a repository's own saved Dockerfile (`images/derived/Dockerfile.tmpl`
  invites saving and editing it) can override the edition carrier or replace `/usr/local/bin/fugaro`.
  That predates the move and only silences a warning (R3, R5); how many repositories do so is not
  known.
- **U3.** The cost of `-tags ee` on the base image's size budget (`images/size-budget.sh`); the
  governance code is small, but the coordinator and report libraries pull `internal/rtdb` and
  Firestore clients that the binary already links.
- **U5.** Whether `fugaro update` alone refreshes the history image an installation's coordinator
  sweep runs (`internal/cli/init_images.go` copies it in the images stage), or a further flag is
  needed; E3.3 confirms before the notes are written.
- **U6.** Whether the sweep reads every run (the premise of D535's recommendation); `internal/coordinator/sweep.go`
  reads the runs bucket, but whether a run with no declared intent is joined into the view was not
  traced.
- **U4.** Whether the owner's "and later" in the in-flight list covers runs launched after this PR
  merges; D527 proposes a cut-off.

## Owner ruling of 2026-10-29: the key-minting service belongs under `ee/`

The licensing key-minting service (owner-operated: Cloud Run plus a non-exportable KMS signing key
in a dedicated Fugaro GCP project) belongs under `ee/` (for example `ee/keysvc/`); its deploy is
owner-run, and it is deferred with the key work (D523). Subscriptions S3 places it elsewhere; a
dated pointer in `docs/subscriptions/README.md` says so, and the plan's task E4.1 records it.

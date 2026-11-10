# A vision-capable model for a run on a text-only coder: implementation plan

**Status: plan only; nothing is implemented; no task is approved, and none has a release slot.** The
owner has answered none of D2000–D2016, so every task below is written to the recommendation
[reconciliation.md](reconciliation.md) §4 gives, and each task names the decisions it carries. Where a
task and the reconciliation disagree, **the reconciliation's findings (F1–F19) and decisions
(D2000–D2016) win and the task is wrong**.

**Written to:** [spec.md](spec.md) — issue #1865, reproduced verbatim — read through
[reconciliation.md](reconciliation.md), reconciled against `origin/main` at `256106ecf1` and the 36
open pull requests of 2026-11-10.

Nine tasks, **V0–V8**, each with its files (at most 12, except V1 at 13, which says why), its
interfaces, its named tests and the command that runs them, its **red step**, what it depends on, its
**reviewer tier** and whether it is
**SEC**, and its commit. Then the levels and the critical path (§1), the shared-files table (§2), what
is deliberately not in the plan (§3), the release (§4), the owner-run checks (§5) and the gates every
task opens with (§6).

## Rules every task obeys

This repository's own rules, not this plan's invention:

- a **red step** first: the named test fails for the reason the task exists, and the failure is quoted
  in the pull request;
- **targeted tests only** — `internal/cli` and `internal/runner` cost about 25 minutes each in full, so
  every command below names its tests and carries `-count=1`, and the runner's carry `-timeout 40m`
  (`GOFLAGS=-p=2` on a loaded machine);
- **hermetic tests only**: `internal/gcpfake`, `internal/testutil`, `t.TempDir()`, a checkout made with
  `git init` in the temp dir; no network, no bucket, no registry, no home directory;
- **no secret-shaped literal** anywhere — a fixture model ID, never a key;
- **a field never changes meaning** (F19, and `docs/design/update-in-flight.md`): `agent.models.vision`
  and `model_prices.*.image_tokens` are new members on old carriers; `result.json`'s version, the
  policy node's version and `failure_class`'s enum are untouched;
- **docs are tested**: whoever moves a pinned sentence, count or line in `docs/`, `README.md`,
  `SECURITY.md` or the website moves it in the same commit, and runs
  `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1 -timeout 40m` (plus `npm run check` in
  `website/` when website content moved);
- **comments explain why, never what**; no TODO, no section header, no restatement of a name;
- **`fugaro update` stays the whole upgrade** (§4);
- **`.github/workflows/` is never touched** by any task;
- **a fix round for a review finding adds no functionality**: a finding that needs a new behaviour is
  reported to the owner, not fixed in a fix round.

---

## 1. The order, and what forces it

| Level | Tasks | What forces the level |
| --- | --- | --- |
| **L0** | V0 — the interim guard; V1 — the vision slot and its key | Nothing. V1 is the key every other task reads; V0 shares no file with it and ships alone (D2014) |
| **L1** | V2 — the image rate on the owner's price lever; V5 — the bootstrap check | V2 is pricing alone (F4); V5 needs V0's extension list (F8, F11) and V1's key (F17) |
| **L2** | V3 — `visionSince` and the image-age guard; V4 — the refusal's sentence; V6 — the surfaces (doctor, `models ls`/`use`) | V3 needs both new members to guard (F19); V4 needs both to name them (F15); V6 needs the slot and a priceable vision model to show them (D2011–D2013) |
| **L3** | V7 — the stage that can see images runs the vision model | V1 (the slot), V2 (a provider vision model needs the image rate), V5 (the check must already know a vision model is set) |
| **L4** | V8 — the documents, the skill and the release notes | V1–V7; it writes no behaviour |

**Edges** (from each task's "Depends on"): V1 → V2; V1 → V5; V0 → V5; V1 → V4; V2 → V4; V1+V2 → V3;
V1+V2 → V6; V1+V2+V5 → V7; V7 → V8.

**Critical path: V1 → V2 → V3 → V7 → V8.** V0, V5, V4 and V6 hang off it: V0 is independent and first
(it is the mitigation the issue asks for, D2014), V5 and V4 can be built while V3 waits, and V6 is
surfaces.

**Which tasks can run at the same time.** No level holds two writers of one file (§2), so a level's
tasks are parallel by construction: **V0 and V1**, then **V2 and V5**, then **V3, V4 and V6**. A build
with three runs available takes **five rounds** rather than the nine a serial order of the same tasks
takes.

**Two files are written in three levels each, and that is the plan's real serialisation.** Both new
members land in `schemas/settings.schema.json` (V1 the key, V2 the price member, V3 the release each
names), and `internal/runner/gateway.go` is written by V5 (the bootstrap check), V4 (the stage spec's
pin field) and V7 (the stage spec's model). Those are level-by-level rebases, not conflicts: §2's table
names every one.

---

## 2. Shared files

**No level holds two writers of one file.** The writers, with their level:

| File | Writers (level) | Note |
| --- | --- | --- |
| `internal/agent/settings.go` | V0 (L0) | `DenyRules`'s second parameter stops meaning "review" and starts meaning "this stage's reads of binary files are denied", plus the extension list V5 reads |
| `internal/runner/agentenv.go` | V0 (L0) | the call at `:369` decides the deny from the stage's model's image rate |
| `internal/runner/prompts.go` | V0 (L0) | the coder's `SystemPrompt` line |
| `internal/config/config.go`, `pins.go`, `scope.go`, `validate.go`, `providers.go` | V1 (L0) | `ModelRoles.Vision`, `pinPaths`, the scope row, `validateAgentModels`' list, and the three role lists in `providers.go` (`:936`, `:974`, `:1025`) |
| `schemas/fugaro.schema.json` | V1 (L0), V3 (L2) | the key's row, then the release its description names |
| `schemas/project-layer.schema.json` | V1 (L0), V3 (L2) | the same pair |
| `schemas/settings.schema.json` | V1 (L0), V2 (L1), V3 (L2) | the project config's key (V1), `model_prices.*.image_tokens` (V2), the two releases' descriptions (V3) — **three levels, one row each** |
| `internal/pricing/overrides.go`, `internal/pricing/pricing.go` | V2 (L1) | the wire member and `Table.With`'s row (`:362`) |
| `internal/localcfg/localcfg.go` | V2 (L1) | `ModelPrice.ImageTokens` and `Overrides()` (`:918`) |
| `internal/projectpolicy/policy.go` | V2 (L1) | `Price.ImageTokens` (`:609`), which the credentials service's own table reads through `CatalogJSON` (`internal/credsvc/request.go:163`) |
| `internal/runner/images.go` (new) | V5 (L1) | the scan and its extension list, read from `internal/agent` (search before writing a second list) |
| `internal/gateway/gateway.go`, `forward.go`, `export.go` | V4 (L2) | the `Stage`/`RequestPin` field the sentence reads, `pinOf`, and the sentence itself — the credentials service's own caller leaves the field empty and needs no change |
| `internal/runner/gateway.go`, `internal/runner/gateway_test.go` | V5 (L1), V4 (L2), V7 (L3) | `checkBudget` (V5), `stageSpec` setting the pin's new field (V4), the stage's model arms (V7) — **three writers, three levels** |
| `internal/cli/recipes_skew.go`, `docs/release.md` | V3 (L2) | the constant, the launch check and the Before-you-tag bullet |
| `internal/cli/doctor.go`, `internal/cli/models_ls.go`, `internal/cli/models_use.go`, `internal/cli/models_write.go`, `schemas/models-ls.schema.json` | V6 (L2) | the doctor check, the fourth role row, the fourth role name (`modelsRoleNames`, `models_write.go:108`) and the pinning-repository scan it drives (`:158`), and the schema's role enum |
| `internal/runner/routing.go`, `internal/runner/policy.go`, `internal/runner/providers.go` | V7 (L3) | the vision resolution beside `applyRouting` (`policy.go:204`), the pin rewrite, and the route list (`providers.go:113`) |
| `docs/multi-model.md`, `docs/vision-model/README.md`, `docs/status.md`, `plugin/skills/working/SKILL.md`, `docs/releases/vX.Y.Z.md`, `website/data/release-summaries.yaml` | V8 (L4) | the hand edit's new key, this folder's status line, the status.md row, the skill's model switch, and the release notes |

---

## 3. What is deliberately not in the plan

- **`fugaro models add --image-tokens`.** `models add` writes a `model_prices` entry (`docs/models-commands`
  D780), so the flag is the obvious companion to V2. It is not carried: the flag's own validation
  (what a ceiling may be, whether a model can be added for images alone) is a product decision nobody
  has taken, and an owner who needs the member writes it — V2's decode accepts a hand-written block,
  and a refused line names the shape.
- **A per-image record field, a per-call `model_by` for an image call, and an image count in the
  ledger** (D2010). The serving model already carries the fact; a field that two models in one stage
  would have to define is a wire change for a question the record answers.
- **The gateway substituting a model on a request that carries an image** (D2002's alternative). The
  plan is written for the recommendation; if the owner answers the other way, V7 becomes a gateway task
  over `forward.go`'s body handling and D2010 grows a record task.
- **A craft, a credential or a pool of its own for vision calls** (D2003, D2008): the vision model uses
  the coder craft's access. `docs/model-crafts` owns crafts, and a fifth one re-decides that folder's
  vocabulary here.
- **Pricing an image in the built-in provider catalogue.** `docs/design/provider-price-catalog.md` D8
  refuses a catalog-priced model that prices an image, and V2 changes nothing there: the catalog's rows
  stay rate-only, so a catalog model still refuses an image (F4).
- **`docs/design/v1.md`, `docs/design/m9-budget-and-dashboard.md` and `docs/multi-model.md`'s design
  sections**: they describe a gateway that never rewrites a body, which the recommendation keeps. V8
  adds the operational paragraph, not a design change.
- **Anything that reads an image**: no screenshot rendering, no OCR, no console screenshot work. The
  run's agent reads the file it always read; this work only makes a model that can accept it available.

---

## 4. The release

**The owner names the release**; the plan fixes only its shape.

- **V1, V2 and V3 ship in one release.** A strict decoder refuses the two new members rather than
  ignoring them (F19), so the release that writes either one carries the `visionSince` guard that keeps
  a run whose job image is older from failing on it. Shipping V1 or V2 without V3 is the one ordering
  this plan forbids.
- **`### For operators` is one line**: "run `fugaro update` — it rebuilds the job images and republishes
  the policy; nothing else is needed." No deletion, no second command, no manual repair (D2015).
- **A run in flight is untouched** (`docs/design/update-in-flight.md`): nothing here rewrites a node a
  run holds or a record it wrote, the policy node keeps its version, and the guard refuses a *launch*,
  which is the next run rather than one already going.

---

## 5. Checks 520–521 (owner-run, **NOT RUN**)

Both are live: they need a real repository, a real model and a real image, so no test in the tree can
stand in for them.

**Check 520 — a text-only coder reads a screenshot.** A repository whose `agent.models.coder` is a
provider model with an image rate and `agent.models.vision` set, and a task whose fixture work involves
a console `__screenshots__` PNG (the issue's own case, run `20261107-104613-b2af`). PASSED when the run
passes `implement` and `fix`, no stage fails with the gateway's image refusal, `result.json`'s
`cost.model_by` carries the vision model's ID and the stage timings name it, and `fugaro diagnose`
prints that split. NOT RUN.

**Check 521 — the early refusal.** A repository with a text-only coder, **no** `agent.models.vision`
and a task whose text names a `.png`. PASSED when the run refuses at bootstrap, before any stage, with
a sentence naming `agent.models.vision` and how to price a model for it, and when the record shows the
run spent nothing. NOT RUN.

**Checks 522–529 are reserved** by this work and unused (README, "Numbers this work holds").

---

## 6. Gates (every task)

`go build ./... && gofmt -l . && go vet ./...` (gofmt prints nothing); the task's named tests with
`GOFLAGS=-p=2 go test ./internal/<pkg>/ -run '<names>' -count=1 -timeout 40m`; the Docs run
`GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1 -timeout 40m` when a doc or a schema changed,
and `npm run check` in `website/` when website content changed. Never the whole `internal/cli` or
`internal/runner` package.

---

## V0. A text-only coder is refused the read, and its brief says why (D2014)

**Level 0. Reviewer tier 2. Not SEC.** It tightens what the agent may read; a mistake loosens a
permission, and the deny list is the one that already exists (F8).

- **Files (7):** `internal/agent/settings.go`, `internal/agent/settings_test.go`,
  `internal/runner/agentenv.go`, `internal/runner/agentenv_test.go`, `internal/runner/prompts.go`,
  `internal/runner/prompts_test.go`, `internal/runner/prompts_internal_test.go`.
- **Interface:** `agent.DenyRules(denyWeb, denyBinaries bool)` — the second parameter's name is the
  change; `agent.ImageReadExtensions() []string` is the one list of image kinds, exported for V5;
  `r.denyBinaryReads(stage string) bool` in the runner returns true for a review stage or for a stage
  whose model's price row has no image rate (`r.spend.Prices`).
- **Depends on:** nothing. It ships alone, and it is the mitigation the issue's last line asks for
  (D2014).
- **Red step:** `TestTextOnlyCoderIsDeniedBinaryReads` fails while the deny list is review-only: in a
  stage pinned to a model with no image rate the managed settings carry no `Read(**/*.png)` rule.
- **Tests:** `TestTextOnlyCoderIsDeniedBinaryReads`, `TestReviewStageKeepsItsDenyList`,
  `TestClaudeCoderKeepsItsTools` (a model with an image rate gains no deny rule),
  `TestTextOnlyCoderBriefNamesImageFilesAndTheFix`, `TestSystemPromptIsUnchangedWithAnImageRate`.
- **Commit:** `runner: a text-only coder is denied image reads, and its brief says why (D2014)`

## V1. `agent.models.vision`, resolved and checked like the other slots (D2003, D2007)

**Level 0. Reviewer tier 2. Not SEC.**

- **Files (13 — one over the cap, and the one over is `model_providers_test.go`: this key belongs to
  three `{path, model}` role lists in `providers.go`, and that is the file `CheckProviderAuth`'s own
  list is already tested in):** `internal/config/config.go`, `internal/config/pins.go`,
  `internal/config/scope.go`, `internal/config/validate.go`, `internal/config/providers.go`,
  `internal/config/config_test.go`, `internal/config/pins_test.go`, `internal/config/validate_test.go`,
  `internal/config/scope_test.go`, `internal/config/model_providers_test.go`, `schemas/fugaro.schema.json`,
  `schemas/settings.schema.json`, `schemas/project-layer.schema.json`.
- **Interface:** `ModelRoles.Vision string` (`yaml:"vision"`), `Agent.VisionModel() string` (the slot,
  else `""` — it never falls back to `agent.model`: a coder model is exactly what must not answer),
  `Agent.PinnedModels()` gains it as a fourth entry, `config.pinPaths` gains
  `"agent.models.vision"`, `config.Scopes` a row (`InProject | InRepo`, like the coder's), and
  `validateAgentModels`' list and `providers.go`'s three role lists the fourth pair.
- **Depends on:** nothing. It is the key every other task reads, and it changes nothing for a
  configuration that never writes it.
- **Red step:** `TestVisionSlotIsCheckedLikeTheOthers` fails while the key is unknown: a repository that
  writes `agent.models.vision: deepseek/deepseek-v4.1-flash` is refused, not resolved.
- **Tests:** `TestVisionSlotIsCheckedLikeTheOthers` (priced, allowed, an ID),
  `TestVisionSlotIsNotAModelForAnyExistingRole` (a run with no vision key behaves byte for byte as
  today), `TestVisionSlotIsJudgedOnlyWhenSet` (an empty slot is not a pin problem),
  `TestPinPathsNameTheVisionSlot`, `TestScopeRefusesTheVisionKeyInAProfile`,
  `TestProviderAuthJudgesTheVisionPin` (the credential rule reaches the fourth slot),
  `TestScopesGoldenTableNamesTheVisionKey` (`scope_test.go`'s table is an exact two-way match, so the
  row reddens it until it is written),
  `TestSchemasDecodeTheVisionKey` (all three schemas, the key, and nothing else).
- **Note:** the calendar-decided slice of the `...Since` discipline (the release the descriptions name)
  is V3's, not this task's.
- **Commit:** `config: agent.models.vision, resolved like the other model slots (D2003, D2007)`

## V2. The image rate on the owner's price lever (D2001). **SEC**

**Level 1. Reviewer tier 3.** A price decides what a run may spend, and a wrong image ceiling is a
reservation the budget holds against the owner.

- **Files (9):** `internal/pricing/overrides.go`, `internal/pricing/pricing.go`,
  `internal/pricing/overrides_test.go`, `internal/pricing/pricing_test.go`,
  `internal/localcfg/localcfg.go`, `internal/localcfg/localcfg_test.go`,
  `internal/projectpolicy/policy.go`, `internal/projectpolicy/policy_test.go`,
  `schemas/settings.schema.json`.
- **Interface:** `image_tokens` on the overrides' wire shape (`wireRates.ImageTokens *int64`), carried
  into the table row by `Table.With` (`Model{ID: key, Rates: r, ImageTokens: …}`), a negative or zero
  value refused or read as "no images" exactly as the embedded table's `0` means it;
  `localcfg.ModelPrice.ImageTokens *int64` → `pricing.Overrides`; `projectpolicy.Price.ImageTokens *int64`,
  which the credentials service's table reaches through `CatalogJSON` → `ParseOverrides`
  (`internal/credsvc/request.go:163`) with no change of its own.
- **Depends on:** V1, for the slot a priced vision model is written into — and for nothing else: the
  member itself is pricing alone.
- **Red step:** `TestModelPricesCarryAnImageRate` fails while the member is unknown: a project config
  that prices a model's images is refused by the strict decoder.
- **Tests:** `TestModelPricesCarryAnImageRate`, `TestImageRateReachesTheTableRow`,
  `TestImageRateIsReservedAsWorstCase` (the input bound grows by `images × ceiling`),
  `TestImageRateZeroStillRefusesAnImage` (the absence and the explicit zero mean the same),
  `TestProjectPriceBlockDecodesTheImageRate` (the node's spelling, escaped key and all),
  `TestCatalogRowsStillPriceNoImage` (D8 unchanged: a catalog model refuses an image).
- **Commit:** `pricing: an image rate on model_prices, so an owner can price a provider's images (D2001)`

## V3. `visionSince`: the guard the new members force (D2015, F19). **SEC**

**Level 2. Reviewer tier 2.** It decides whether a run launches; getting it wrong refuses runs that
would have worked.

- **Files (8):** `internal/cli/recipes_skew.go` (the constant and the launch check),
  `internal/cli/recipes_skew_test.go`, `internal/cli/init_policy_publish.go` (the publish-time check
  beside `layeredSince`'s, `config_publish.go:695`), `internal/cli/init_policy_publish_test.go`,
  `docs/release.md` (the Before-you-tag bullet), `schemas/fugaro.schema.json`,
  `schemas/project-layer.schema.json`, `schemas/settings.schema.json` (the release each description
  names — V1's row and V2's, one sentence each).
- **Interface:** `const visionSince = "X.Y.0"`, a `checkImageSince`-shaped refusal at launch naming the
  key and the image to move, and the publish-time refusal that keeps a policy carrying the price member
  from being stored for an installation whose images cannot read it.
- **Depends on:** V1 and V2. It guards both members, and it has nothing to guard before they exist.
- **Red step:** `TestVisionSinceRefusesAnOlderImage` fails while the constant does not exist: a task
  whose image predates the release launches against a config carrying the new key and is refused by the
  runner instead of by the CLI.
- **Tests:** `TestVisionSinceRefusesAnOlderImage`, `TestVisionSinceAllowsTheReleaseItself`,
  `TestImageTokensInAPolicyIsRefusedForAnOlderImage` (the node's carrier is guarded too),
  `TestVisionSinceIsTheReleaseTheDocsName` (the `docs/release.md` bullet and the three schemas, the
  `planningSince` pattern).
- **Commit:** `cli: visionSince, the guard the two new members force (D2015, F19)`

## V4. The refusal's sentence names the fix that exists (D2009). **SEC**

**Level 2. Reviewer tier 3.** The gateway holds the budget and the credential; its refusals are the
owner's only explanation of a dead run.

- **Files (8):** `internal/gateway/gateway.go` (the `Stage` field), `internal/gateway/forward.go`,
  `internal/gateway/forward_test.go`, `internal/gateway/export.go`, `internal/gateway/export_test.go`,
  `internal/runner/gateway.go` (the stage spec sets it), `internal/runner/gateway_test.go`,
  `internal/credsvc/request_test.go`.
- **Interface:** `Stage.Vision` and `RequestPin.Vision`, carrying the run's configured vision model
  (`""` when the slot is empty, and absent altogether for the credentials service's pin:
  `requestModels`/`requestPinWhere`, `internal/credsvc/request.go:129`, needs no change). The image
  sentence then names the fix for the case it met: **no vision model** → set `agent.models.vision`
  (V1); **a vision model that has no image rate** → price that ID's images under `model_prices` (V2);
  **neither the field nor the caller's own stage** (the credentials service) → the price sentence
  alone. Never a sentence a caller cannot act on.
- **Depends on:** V1 and V2. The sentence names the slot and the price member, so both must exist
  before it points at them.
- **Red step:** `TestImageRefusalNamesModelPricesImageTokens` fails while the sentence still says
  "model_prices sets token rates only" and names no key the owner can set.
- **Tests:** `TestImageRefusalNamesModelPricesImageTokens`,
  `TestImageRefusalNamesTheVisionSlotWhenItIsEmpty` (a run with no slot),
  `TestImageRefusalPricesTheConfiguredVisionModel` (a slot set, unpriced: the sentence names
  `image_tokens` for that ID), `TestImageRefusalNamesNoSlotForTheCredentialsService`,
  `TestImageRefusalStaysARefusal` (the call is still refused: this task changes a sentence, not a rule).
- **Commit:** `gateway: the image refusal names a fix the owner has (D2009)`

## V5. The early check: a text-only coder, no vision model, and images (D2005, D2006)

**Level 1. Reviewer tier 2. Not SEC.**

- **Files (6):** `internal/runner/images.go` (new), `internal/runner/images_test.go` (new),
  `internal/runner/gateway.go`, `internal/runner/gateway_test.go`,
  `internal/runner/runner_test.go` (the bootstrap site), `internal/runner/testdata/` (a fixture task
  and a fixture checkout's file list, placeholder names only).
- **Interface:** `imageNamesInTask(text string) []string` and `imageFilesInTree(ctx, repo) ([]string, bool)`
  (the second bounded, and reusing `agent.ImageReadExtensions()`, the list V0 owns);
  `r.checkImages()` called from `checkBudget` **after** `CheckPins` (`internal/runner/gateway.go:787`):
  a task naming an image, a coder whose price row has no image rate and `agent.models.vision` empty →
  a `config_invalid` refusal naming the slot and V2's price member; a repository that tracks an image
  in that same state → the `r.d.Log.Warn` line the pin warnings already use.
- **Depends on:** V0 (the one list of image kinds the scan reuses) and V1 (the slot the refusal names).
- **Red step:** `TestBootstrapRefusesATextOnlyCoderWithImagesAndNoVisionModel` fails while nothing reads
  the task: the run reaches `implement` and dies there, which is the issue's own complaint.
- **Tests:** `TestBootstrapRefusesATextOnlyCoderWithImagesAndNoVisionModel`,
  `TestBootstrapRefusalNamesTheSlotAndThePriceMember`, `TestBootstrapWarnsForARepositoryWithImages`,
  `TestBootstrapSaysNothingWithAVisionModelSet` (V1's slot), `TestBootstrapSaysNothingForACoderWithAnImageRate`,
  `TestBootstrapSaysNothingForATaskThatNamesNoImage` (the false-positive direction: the word "png" in
  prose about a palette is still a refusal — the check refuses a name, and D2005 is the row that decides
  this), `TestImageScanIsBounded` (a checkout of many files is not read whole).
- **Commit:** `runner: the bootstrap refuses a text-only coder with images and no vision model (D2005, D2006)`

## V6. The surfaces: `doctor`, `models ls` and `models use` (D2011, D2013)

**Level 2. Reviewer tier 2. Not SEC.**

- **Files (10):** `internal/cli/doctor.go`, `internal/cli/doctor_test.go`, `internal/cli/models_ls.go`,
  `internal/cli/models_ls_test.go`, `internal/cli/models_use.go`, `internal/cli/models_use_test.go`,
  `internal/cli/models_write.go` (`modelsRoleNames`), `internal/cli/models_write_test.go`,
  `internal/cli/models_rm_test.go`, `schemas/models-ls.schema.json`.
- **Interface:** a `vision-coder` doctor check (`doctor.go:397`/`:580`'s registration shape) reporting
  the coder's image rate, whether the vision slot is set and whether the repository tracks image files;
  `models ls`'s role row for the vision slot; `models use <repo> vision <id>` writing
  `agent.models.vision` through the same `yamledit.SetScalar` path and the same checks (priced, a route,
  no whitespace). `modelsRoleNames` (`models_write.go:108`) gains the fourth name — its own comment
  ("the three keys `agent.models` has") moves with it — and so does everything that list drives: the
  roles `models use` may name and the pinning-repository scan `models rm` reports from
  (`models_write.go:158`), which is why the vision pin is one more pin a removal names.
- **Depends on:** V1 and V2. A slot nothing resolves and a model nothing can price would be two rows
  that report a state no run can reach.
- **Red step:** `TestModelsUseWritesTheVisionSlot` fails while `modelsRoleNames` has three entries.
- **Tests:** `TestModelsUseWritesTheVisionSlot`, `TestModelsUseRefusesAnUnpricedVisionModel`,
  `TestModelsLsShowsTheVisionSlot`, `TestModelsRmNamesTheVisionPin`,
  `TestDoctorWarnsWhenTheCoderCannotSeeAndTheRepoHasImages`, `TestDoctorSaysNothingWhenTheCoderSees`,
  `TestModelsLsJSONMatchesSchema`.
- **Commit:** `cli: doctor and the models commands know the vision slot (D2011, D2013)`

## V7. The stage that can see images runs the vision model (D2000, D2002, D2004, D2016). **SEC**

**Level 3. Reviewer tier 3.** It decides which model serves a run's calls and therefore the spend, the
egress and the credential a stage's code goes out on.

- **Files (7):** `internal/runner/routing.go`, `internal/runner/routing_internal_test.go`,
  `internal/runner/policy.go`, `internal/runner/policy_test.go`, `internal/runner/providers.go`,
  `internal/runner/providers_test.go`, `internal/runner/gateway.go` (the stage spec's own arm).
- **Interface:** `r.applyVision(prices *pricing.Table) error`, called inside `resolvePolicy` **after**
  `applyRouting` and **before** `resolveLevels` (`internal/runner/policy.go:204`): for each coder-role
  slot whose model's price row has no image rate and with `agent.models.vision` set, it rewrites the
  slot to the vision model — the same "write the resolved model where the whole run reads one answer"
  rule `applyRouting` follows, so the pins, the plan, level 1's own answer, the gateway's routes
  (`gatewayRoutes`, `providers.go:113`), the priced table and the record all see the vision model
  without a second resolution. A coder whose price row has an image rate is left alone (D2016), and a
  reviewer is never rewritten (D2004).
- **Depends on:** V1 (the slot), V2 (a provider vision model needs the image rate to be priceable) and
  V5, whose check must already know a vision model is set before a run starts rewriting pins for it.
- **Red step:** `TestACoderWithoutAnImageRateRunsTheVisionModel` fails while nothing rewrites a pin: the
  gateway admits the coder's model for the stage and an image call still dies (F1, F6).
- **Tests:** `TestACoderWithoutAnImageRateRunsTheVisionModel`,
  `TestACoderWithAnImageRateKeepsItsModel`, `TestTheReviewerIsNeverRewritten`,
  `TestAVisionModelIsRoutedAndPriced` (its route is registered: `gatewayRoutes`),
  `TestTheVisionModelIsPinnedForTheStagesItRuns` (`StageTiming.Model` and the request pin),
  `TestNoVisionModelLeavesTheRunUnchanged`.
- **Commit:** `runner: the stage that can see images runs the vision model (D2000, D2002, D2004, D2016)`

## V8. The documents, the skill and the release notes

**Level 4. Reviewer tier 1. Not SEC.** It writes no behaviour: its red step is the Docs run.

- **Files (7):** `docs/multi-model.md` (the key beside the other three roles, and what "a model with an
  image rate" means), `docs/vision-model/README.md` (the status line), `docs/status.md` (this folder's
  row), `plugin/skills/working/SKILL.md` (the switch and the early refusal), `docs/releases/vX.Y.Z.md`
  (`### For operators`: the one line), `website/data/release-summaries.yaml` (no backticks),
  `website/content/cli-reference.md` (regenerated, never hand-edited — only if a command's help moved).
- **Depends on:** V1–V7. It documents behaviour, so the behaviour has to exist: the slot, the price
  member, the guard, the sentence, the check and the stage model are each one paragraph of it.
- **Red step:** the Docs run fails while `docs/multi-model.md` does not name the slot a run's own
  choice and the refusal's sentence both point at; it turns green in this commit.
- **Tests:** `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1 -timeout 40m`, the plugin's
  command test, `npm run check` in `website/`.
- **Commit:** `docs: the vision slot in multi-model, the status row and the release notes`

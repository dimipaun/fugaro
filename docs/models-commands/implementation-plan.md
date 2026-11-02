# `fugaro models`: implementation plan

**The decisions are answered; the tasks queue behind CLI 0.14.1 (0.15.0).** It is written to the owner's answers (D780–D783, 2026-10-30; D784–D795, 2026-10-31, all as recommended)
([reconciliation.md](reconciliation.md) §3). A decision answered the other way changes the tasks that
cite it. Each task touches at most 12 files, names its tests, starts with a red step and ends at the
gates below.

## Order, and what this plan does not own

- **External dependency E1 (update-converge, part b of UC1):** the `fugaro update` stage that publishes and
  migrates the policy node. Without it, `models add` would leave the owner a second command (F8). M3 and M4
  may merge before it but **must not ship in a release without it**: the release that carries them carries E1.
- **External dependency E2 (D787):** `fugaro update` re-converges every recorded repository whose job's
  provider copy has drifted, behind its one confirmation. It belongs to the update-converge work (it is the
  same "update is the whole upgrade" rule, and touches `internal/cli/init*.go`, which that work owns). SEC.
  Without it, a `models add` that changes a route is refused from shipping for the same reason as E1.
- **Files other work owns:** registering the command in `internal/cli/root.go`, the generated
  `website/content/cli-reference.md`, and the plugin skills are each owned by another open work stream at
  the time of writing. M1 and M6 name them, and the task that touches them waits for the owner's go for
  those files.

| Level | Tasks | Needs |
| --- | --- | --- |
| 0 | M0, M1 | nothing |
| 1 | M2, M3, M5 | M1 (M3 also M0) |
| 2 | M4 | M3 |
| 3 | M6 | M1–M5; E1 and E2 merged before the release |

## Gates (every task)

`go build ./... && gofmt -l . && go vet ./...` (gofmt prints nothing); the task's named tests with
`GOFLAGS=-p=2 go test ./internal/<pkg>/ -run '<names>' -count=1`; `GOFLAGS=-p=2 go test ./internal/cli/ -run
Docs -count=1 -timeout 40m` when a doc changed; `go test ./internal/eeboundary/... -count=1`. Never the whole
`internal/cli` package.

## M0. A comment-preserving editor for the local project config (D790). **SEC**

The project config decides where a repository's code may go and what a run may cost, and a lossy rewrite
can turn a refused `allow_data_to` into "every repository" (F11).

- **Files (4):** `internal/localcfg/edit.go` (new), `internal/localcfg/edit_test.go` (new),
  `internal/localcfg/testdata/hand-edited-project.yaml` (new; placeholder names only),
  `internal/localcfg/testdata/hand-edited-commented-allow.yaml` (new).
- **Interface:** `Edit(data []byte, ops ...EditOp) ([]byte, error)` over a `yaml.Node` tree, with ops
  `AppendRouteModel(route, id)`, `RemoveRouteModel(route, id)`, `SetModelPrice(id, in, out)`,
  `RemoveModelPrice(id)`, `AppendAllowedModel(id)`, `RemoveAllowedModel(id)`. The output must `Parse`
  strictly; a refusal returns the input untouched.
- **Red step:** `TestEditKeepsCommentsAndOrder` fails while `Edit` does not exist; the same fixture
  through `Parse` then `Marshal` is shown to lose its comments (the reason the task exists).
- **Tests:** `TestEditKeepsCommentsAndOrder`, `TestEditKeepsCommentedOutAllowDataTo`,
  `TestEditDecodesToTheIntendedChangeOnly` (decoded old config plus the op equals the decoded new config),
  `TestEditRefusesWhatParseRefuses`, `TestEditRemovingTheLastAllowedModelIsRefused`.

## M1. The shared row builder and `fugaro models ls` (D784, D793, D794)

- **Files (9):** `internal/cli/models.go` (new: the command group), `internal/cli/models_ls.go` (new),
  `internal/cli/models_ls_test.go` (new), `internal/cli/budget.go` (the price-row builder moved behind one
  function both commands call; no output change), `internal/cli/budget_test.go` (pins `budget prices
  --json`'s rows figure by figure, so a changed price is red there), `schemas/models-ls.schema.json` (new),
  `schemas/schemas_test.go` (the new schema loads and the command's output validates), `internal/cli/root.go`
  (registration; owned elsewhere, see "Order"), `docs/multi-model.md` (a short "With `fugaro models`" pointer).
- **Red step:** `TestModelsLsListsRoutesModelsAllowListsAndUsage` fails while the command does not exist.
- **Tests:** `TestModelsLsListsRoutesModelsAllowListsAndUsage`, `TestModelsLsUnknownWithoutACheckout`,
  `TestModelsLsShowsCatalogAgeAndSpread`, `TestModelsLsJSONMatchesSchema`, `TestModelsLsIsOffline` (the
  cloud seams panic if reached), `TestBudgetPricesJSONPinsTheFigures` (the rows pinned by value: the shared
  builder agreeing with itself cannot catch a changed figure).
- **Hermetic:** `t.TempDir()` config home, a fake checkout made with `git init` in the temp dir, the embedded
  catalog behind `loadCatalog`.

## M2. `fugaro models search` (D788)

- **Files (3):** `internal/cli/models_search.go` (new), `internal/cli/models_search_test.go` (new),
  `internal/cli/models.go` (adds the subcommand).
- **Red step:** `TestModelsSearchFindsACatalogRow` fails while the subcommand does not exist.
- **Tests:** `TestModelsSearchFindsACatalogRow`, `TestModelsSearchFindsAnEmbeddedClaudeRow`,
  `TestModelsSearchMissSaysHowToPrice`, `TestModelsSearchNamesTheClaimingRoute`, `TestModelsSearchIsOffline`.

## M3. `fugaro models add` (D780, D782, D785, D786, D789, D795). **SEC**

It widens where code may go (a route's claim) and what a run may cost (a price).

- **Files (6):** `internal/cli/models_add.go` (new), `internal/cli/models_add_test.go` (new),
  `internal/cli/models_write.go` (new: diff, confirmation, agent-session refusal, `backupFile`, `writeFileAtomic`,
  shared with M4), `internal/cli/models_write_test.go` (new), `internal/cli/models.go`,
  `internal/localcfg/edit.go` (only if an op M0 did not foresee is needed).
- **Red step:** `TestModelsAddRefusesAnUnpricedModelWithoutPrices` fails while the subcommand does not exist.
- **Tests:** `TestModelsAddRefusesAClaudeID`, `TestModelsAddRefusesAVariant`,
  `TestModelsAddRefusesAnUnpricedModelWithoutPrices`, `TestModelsAddAppendsTheExactIDNeverAPattern`,
  `TestModelsAddLeavesAClaimingRouteUnchanged`, `TestModelsAddAppendsToASetAllowList`,
  `TestModelsAddLeavesAnAbsentAllowListAbsent`, `TestModelsAddExplainsTheSpreadAndWritesNoSwitch`,
  `TestModelsAddRefusesInAnAgentSessionEvenWithYes`, `TestModelsAddBacksUpAndWritesAtomically`,
  `TestModelsAddPrintsOnlyFugaroUpdateNext`, `TestModelsAddWritesNothingInTheCloud`.

## M4. `fugaro models rm` (D783, D789). **SEC**

D783 was answered against the recommendation: `rm` warns and removes, it never refuses a pinned model. The consequence is stated in the warning: the next run of each pinning repository fails with a no-provider error.

- **Files (3):** `internal/cli/models_rm.go` (new), `internal/cli/models_rm_test.go` (new),
  `internal/cli/models.go`.
- **Red step:** `TestModelsRmWarnsNamingEachPinningRepository` fails while the subcommand does not exist.
- **Tests:** `TestModelsRmWarnsNamingEachPinningRepository` (the warning names repository, role and the no-provider failure; the write still happens), `TestModelsRmWarnsForRepositoriesWithoutACheckout`,
  `TestModelsRmLeavesAClaimingPatternAndSaysSo`, `TestModelsRmRefusesEmptyingTheAllowList`,
  `TestModelsRmRefusesInAnAgentSession`, `TestModelsRmKeepsTheRestOfTheFile`.

## M5. `fugaro models use` (D781, D791, D792)

It edits a repository's own file in its working tree, which the user reviews and commits; it widens nothing
the owner has not opened.

- **Files (4):** `internal/cli/models_use.go` (new), `internal/cli/models_use_test.go` (new),
  `internal/cli/models.go`, `internal/cli/init_fugaroyaml.go` (only if the line-edit helper beside
  `setGCPProjectLine` is shared rather than copied; search before writing a helper).
- **Red step:** `TestModelsUseEditsOnlyTheRoleLine` fails while the subcommand does not exist.
- **Tests:** `TestModelsUseEditsOnlyTheRoleLine`, `TestModelsUseInsertsAgentModelsWhenAbsent`,
  `TestModelsUseRefusesAModelNoRouteOpensToTheRepository`, `TestModelsUseRefusesAnUnpricedModel`,
  `TestModelsUseRefusesAProviderModelUnderOAuth`, `TestModelsUseNeverCommits`,
  `TestModelsUseRefusesWithoutACheckout`, `TestModelsUseRunsInAnAgentSession`.

## M6. Documentation, skill and release notes

- **Files (6):** `docs/multi-model.md` (§2 and §4 lead with the commands; the hand edit stays as the
  reference), `docs/models-commands/README.md` (status line), `plugin/skills/working/SKILL.md` (the model
  switch is `fugaro models use`, then `fugaro update` when the owner added a model; owned elsewhere, see
  "Order"), `docs/releases/vX.Y.Z.md` (`### For operators`: "run `fugaro update`" and what it will ask),
  `website/data/release-summaries.yaml`, the generated `website/content/cli-reference.md` (regenerated, not
  hand-edited).
- **Red step:** the plugin's command test fails while the skill names `fugaro models use` before M5 is
  merged; it turns green on M5's branch.
- **Tests:** `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs`, the plugin tests, `npm run check` in
  `website/`.

## Live checks

None. Every behaviour above is a local file edit or an offline read and is tested hermetically.

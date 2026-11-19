# Every model key through the credentials service: implementation plan

**Status: plan only. Nothing is implemented and no task is approved; each task needs the owner's
explicit go by name.** It builds [spec.md](spec.md) as amended by
[reconciliation.md](reconciliation.md), which wins where the two disagree.

The answers that shaped it: only a Claude model through a provider key waits for Check 50 (D3000);
every model key lives in the project's one bundle and `fugaro update` copies the existing ones
there (D3001); the gateway always runs (D3002), and a budget-off run opens an identity like any
other, an installation with no database keeping its mounts (D3004); the service enforces the
policy's rules and the cap stays in the gateway (D3003); an old key secret is deleted once copied
(D3005, not the recommendation); only a project owner copies and sets keys (D3006); no check ties
a provider key to a repository's auth mode (D3007, the owner's own answer). The owner's ruling
D2975 reverses D1634 and D1670.

**It ships in two releases** (A4): release A serves every model key through the service with the
mounts still there (C1, KS1 to KS12, I1); release B drops the mounts and deletes the old secrets
(KS13 to KS16). KS13 is not launched before release A is cut.

## 1. Conventions every task follows

Point, don't restate: `CLAUDE.md`, `.claude/rules/credentials-service.md` (every task under
`internal/creds`, `internal/credsvc`, `SECURITY.md`), `.claude/rules/terraform.md` (KS13 to KS16),
`docs/no-prompt-policy/`, `docs/design/update-in-flight.md`.

- **Always.** Targeted tests, never a whole package. Hermetic: `internal/gcpfake`, `anthropicfake`,
  `t.TempDir()`. A task that makes a pinned sentence of `SECURITY.md`, `docs/multi-model.md` or the
  credentials runbook false changes the sentence, the test that pins it and
  `website/content/security.md` in the same change, and runs `npm run check` in `website/` (F12).
  A new command, flag or update stage is added to the censuses that count it: the CLI reference,
  the closed-stdin harness, the list of writing commands, the stage refusal table and
  `internal/apply/steps.go`. Every write of a named provider entry goes through the existing guard
  for an older deployed service (F22).
- **Ask first.** A new dependency; a change to the contract's files (raise a contract change, §7).
- **Never.** A secret-shaped literal; a live cloud call from a test or an agent; a confirmation
  step; an edit under `.github/workflows/`; a key's value in a log, an error, a header, a test
  name or the terminal; the deletion of any secret that is not a copied, unmounted model
  container (R7).
- **Before launching** a task that edits a file R8 lists: check that no task of workspace service
  or secrets brokering that edits the same file is live.

## 2. Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| The service refuses a call the policy's model and provider rules do not allow, whoever built it | KS4 `TestAHandBuiltCallOutsideThePolicyIsRefused` |
| A run never obtains another repository's provider key: the wire carries the provider's name and the service derives the entry | KS4 `TestTheEntryIsDerivedFromTheVerifiedRepository` |
| A Claude model through a provider key is refused until Check 50; any other model is served | KS3 `TestAClaudeModelThroughAProviderWaitsForCheck50`, `TestANonClaudeProviderModelIsServed` |
| The fee, the price check and the provider-cost settle are the gateway's and are unchanged | KS5 `TestARoutedCallThroughTheServiceCostsWhatItCostBefore` |
| Between the releases a run that is not ready still uses its mount | KS5 `TestARunThatIsNotReadyUsesItsMount` |
| A budget-off run has an identity, is charged nothing and holds no key; a local run is unchanged | KS7 `TestABudgetOffRunHasAnIdentity`; KS8 `TestBudgetOffRunsThroughThePassThroughGateway`, `TestALocalRunStillReadsItsOwnEnvironment` |
| An installation with no database keeps its mounts and is told so | KS13 `TestNoDatabaseKeepsItsMounts` |
| The copy prints no value, runs only for a project owner, overwrites nothing silently, is idempotent and follows a rotation | KS12 `TestTheCopyPrintsNoValue`, `TestAnOperatorsUpdateSkipsTheCopy`, `TestTheCopyNeverOverwritesAnEntryItDidNotWrite`, `TestTheCopyTwiceChangesNothing`, `TestTheCopyFollowsANewerVersion` |
| No named provider entry is written for a deployed service that cannot read it | KS2 `TestAnOlderServiceIsNotWrittenANamedEntry`; KS12 `TestTheCopyRunsAfterTheServiceIsDeployed` |
| A mount is dropped only where the bundle holds every key the repository needs | KS13 `TestAMountGoesOnlyWhenTheBundleIsReady` |
| A launch on an image older than release A is refused with the fix | KS13 `TestAnOlderImageIsRefusedAtLaunch` |
| Only a copied, unmounted model container is ever deleted, and only by a project owner's update | KS15 `TestOnlyACopiedUnmountedModelContainerIsDeleted`, `TestAnOperatorsUpdateDeletesNothing`, `TestANonModelSecretIsNeverDeleted` |
| `fugaro update` converges an installation at the previous release with a run in flight | KS13 `TestUpdateConvergesTheMountsFromThePreviousRelease`; KS15 `TestUpdateDeletesTheOldSecretsWithARunInFlight` |

## 3. The contract

### C1: the provider wire
- Acceptance: one place declares how a routed call names the provider it is for (a header and the
  route's path, carrying the provider's name and never an entry's), the typed refusals the service
  answers with, the bundle's entry for a named provider credential with the rule that names it
  from a repository and a provider (R2, U2), and the accessor the service reads it through. The
  names the gateway needs live in a gateway file, because the gateway cannot import the service
  (F26). A stub accessor answers "no such credential".
- Files: `internal/gateway/providerwire.go` (new), `internal/credsvc/provider_wire.go` (new),
  `internal/credsvc/provider_wire_stub.go` (new, stub),
  `docs/keys-through-service/contract-provider.md` (new).
- Interfaces: produces the names, the refusal types, the entry's naming rule and the accessor's
  signature; consumed by KS2, KS3, KS4, KS5, KS12. KS1 builds its other side.
- Tests: `TestStubAccessorHoldsNothing`, `TestAnEntryNameCarriesItsRepository`.
- Command: `go test ./internal/credsvc/ -run 'StubAccessor|AnEntryNameCarries' -count=1 && go test ./internal/gateway/ -run ProviderWire -count=1`.
- Red step: two repositories with one provider name produce one entry name.
- Depends on: none.
- Commit: `credsvc: the provider wire contract (C1, R1, R2)`.
- Reviewer tier: opus, mandatory independent review.

## 4. Slices and tasks

**Release A. Slice 1, the service serves a named provider key:** KS1 to KS4.
**Slice 2, the run asks for it:** KS5, KS6, KS11. **Slice 3, every run has a gateway:** KS7 to KS10.
**Slice 4, the keys move:** KS12. **Integration:** I1.
**Release B. Slice 5, the mounts and the old secrets go:** KS13 to KS16.
**Checks and documents:** KS17, KS18.

### KS1: named provider entries in the bundle
- Acceptance: the bundle decodes and serves one key per named provider credential, with its base
  address and auth form, beside today's entries; a bundle of today's shape still loads; the schema
  and the loader agree; a bundle at the stated worst case stays under the secret's size limit (U5).
- Files: `internal/credsvc/bundle.go`, `schemas/creds-bundle.schema.json`,
  `internal/credsvc/bundle_provider_test.go` (new).
- Interfaces: builds C1's other side (the real accessor).
- Tests: `TestBundleServesANamedProviderEntry`, `TestTodaysBundleStillLoads`,
  `TestBundleSchemaMatchesTheLoader`, `TestAWorstCaseBundleFits`.
- Command: `go test ./internal/credsvc/ -run 'BundleServesANamed|TodaysBundleStill|BundleSchemaMatches|AWorstCaseBundle' -count=1`.
- Red step: a named provider entry is refused as an unknown key.
- Depends on: C1 (it starts after C1 merges).
- Commit: `credsvc: the bundle holds named provider keys (KS1, F3, R2)`.
- Reviewer tier: opus, mandatory independent review.

### KS2: every writer of a provider key writes the bundle
- Acceptance: `fugaro creds set credential-<name>-key` accepts an `openrouter` credential;
  `fugaro accounts add --type openrouter` and `fugaro secrets set` of a provider's secret write the
  bundle and no container; each goes through the guard for an older service, with a new
  since-constant; each reads the value from stdin only, asks nothing, and refuses a person who is
  not a project owner with the fix (D3006).
- Files: `internal/cli/creds.go`, `internal/cli/accounts_add.go`, `internal/cli/secrets.go`,
  `internal/cli/initsecrets.go`, `internal/cli/recipes_skew.go`,
  `internal/cli/accounts_add_key_test.go`, `website/content/cli-reference.md`. It is past five
  files because a provider key has three writers (F27).
- Interfaces: consumes C1 (the entry's naming rule).
- Tests: `TestCredsSetAcceptsAnOpenRouterKey`, `TestAccountsAddOpenRouterWritesTheBundle`,
  `TestSecretsSetOfAProviderSecretWritesTheBundle`, `TestNoWriterCreatesAContainer`,
  `TestAnOlderServiceIsNotWrittenANamedEntry`.
- Command: `go test ./internal/cli/ -run 'CredsSetAcceptsAnOpenRouter|AccountsAddOpenRouter|SecretsSetOfAProvider|NoWriterCreatesAContainer|AnOlderServiceIsNotWritten|CliReferenceNames' -count=1`.
- Red step: `creds set` refuses the name with `credsKeyCredentialHalfRefusal`.
- Depends on: C1.
- Commit: `cli: a provider key goes to the bundle (KS2, D1670 reversed, D3006)`.
- Reviewer tier: opus, mandatory independent review.

### KS3: the gate, narrowed
- Acceptance: a coding caller is offered a named provider credential its policy admits; selection
  refuses it for a Claude model with the refusal that names Check 50, and admits it for any other
  model; the single `provider-key` entry behaves as today.
- Files: `internal/creds/select.go`, `internal/creds/select_provider_test.go` (new),
  `internal/credsvc/cascade.go`.
- Interfaces: consumes C1 (the accessor and the refusal type).
- Tests: `TestAClaudeModelThroughAProviderWaitsForCheck50`, `TestANonClaudeProviderModelIsServed`,
  `TestSelectRefusesReservedTierC` (moved to the Claude case).
- Command: `go test ./internal/creds/ -run 'AClaudeModelThrough|ANonClaudeProvider|SelectRefusesReserved' -count=1 && go test ./internal/credsvc/ -run Cascade -count=1`.
- Red step: a non-Claude model is refused with the Check 50 text.
- Depends on: C1.
- Commit: `creds: only a Claude model through a provider waits for Check 50 (KS3, D3000, F5)`.
- Reviewer tier: opus, mandatory independent review.

### KS4: the service's provider route takes a provider's name
- Acceptance: the provider route reads the provider's name from the call, derives the bundle entry
  from the run's verified repository, builds the provider routes from the published policy and
  refuses a call its model and provider rules do not allow, whoever built the request (R11); it
  then attaches that key and forwards to that provider's address, streaming both ways and holding
  nothing, the body untouched. `SECURITY.md` states that a hand-built call escapes the cap (D3003).
- Files: `internal/credsvc/forward.go`, `internal/credsvc/request.go`, `internal/credsvc/server.go`,
  `internal/credsvc/forward_provider_test.go` (new), `SECURITY.md` with
  `website/content/security.md` and the test that pins the sentence.
- Interfaces: consumes C1.
- Tests: `TestAHandBuiltCallOutsideThePolicyIsRefused`, `TestAnHonestRoutedModelIsNotRefusedAsUnpinned`,
  `TestTheEntryIsDerivedFromTheVerifiedRepository`, `TestTheNamedKeyIsAttachedAndTheBodyUntouched`,
  the existing `TestOpenRouterCallSwapsKeyAndStreams` and `TestOpenRouterKeyNeverReachesJob` unchanged.
- Command: `go test ./internal/credsvc/ -run 'AHandBuiltCall|AnHonestRoutedModel|TheEntryIsDerived|TheNamedKeyIsAttached|OpenRouter' -count=1 && go test ./internal/cli/ -run SecurityMd -count=1 && (cd website && npm run check)`.
- Red step: the route forwards with the single `provider-key` whatever provider is asked.
- Depends on: C1; KS3 (kept: KS4 calls the selection KS3 changes).
- Commit: `credsvc: the provider route serves a named provider under the policy's rules (KS4, R1, R11, D3003)`.
- Reviewer tier: opus, mandatory independent review.

### KS5: a provider route goes through the service
- Acceptance: for a run whose repository is ready (R10) the gateway sends a routed call to the
  service's provider route with the provider's name, keeping its own routing, fee, price check and
  settle, and taking the service's refusals, its unreachable halt and its retry as a service call
  does today; a run that is not ready uses its mounted key as today (R12); the runner reads a
  provider key from its environment only on that second path.
- Files: `internal/gateway/route.go`, `internal/gateway/forward.go`, `internal/gateway/service.go`,
  `internal/runner/providers.go`, `internal/runner/providerkey.go`,
  `internal/gateway/route_service_test.go` (new).
- Interfaces: consumes C1.
- Tests: `TestARoutedCallGoesThroughTheService`, `TestARoutedCallThroughTheServiceCostsWhatItCostBefore`,
  `TestARunThatIsNotReadyUsesItsMount`, `TestAServiceRefusalOnARouteHaltsAsAServiceCallDoes`.
- Command: `go test ./internal/gateway/ -run 'ARoutedCall|ARunThatIsNotReady|AServiceRefusalOnARoute' -count=1 && go test ./internal/runner/ -run Provider -count=1`.
- Red step: `validateRoutes` refuses the route with `errServiceOnRoute`.
- Depends on: C1.
- Commit: `gateway: a provider route is served through the credentials service (KS5, D1634 reversed, R1, R12)`.
- Reviewer tier: opus, mandatory independent review.

### KS6: no auth mode ties a provider key to a repository
- Acceptance: `config.CheckProviderAuth` admits a provider pin under every auth the service
  serves; `config.CheckProviderGateway` (a provider model needs `enforce`) is unchanged; Vertex is
  unchanged.
- Files: `internal/config/providers.go`, `internal/config/providers_auth_test.go` (new).
- Interfaces: none.
- Tests: `TestAProviderPinIsAdmittedUnderEveryServedAuth`, `TestAProviderModelStillNeedsEnforce`.
- Command: `go test ./internal/config/ -run 'AProviderPinIsAdmitted|AProviderModelStillNeeds' -count=1`.
- Red step: a provider pin under `oauth` is refused.
- Depends on: KS4 and KS5 (kept, behaviour: the pin may be admitted only once the service can
  serve it to a job that mounts no provider key).
- Commit: `config: a provider pin is admitted under every served auth (KS6, D3007)`.
- Reviewer tier: opus, mandatory independent review.

### KS7: a budget-off run has an identity
- Acceptance: the launcher mints the run's token and the run opens its session with the budget
  mode off, where the installation has a database; nothing is reserved, charged or capped; an
  installation with no database launches as today.
- Files: `internal/cli/launch_budget.go`, `internal/runner/budget_session.go`,
  `internal/runner/budget_off_identity_test.go` (new).
- Interfaces: none new.
- Tests: `TestABudgetOffRunHasAnIdentity`, `TestABudgetOffSessionChargesNothing`,
  `TestNoDatabaseLaunchesAsToday`.
- Command: `go test ./internal/runner/ -run 'ABudgetOffRunHas|ABudgetOffSession' -count=1 && go test ./internal/cli/ -run 'NoDatabaseLaunchesAsToday|LaunchBudget' -count=1`.
- Red step: `serviceUpstream` refuses the run for having no identity.
- Depends on: none.
- Commit: `runner: a budget-off run opens its identity (KS7, D3004, F18)`.
- Reviewer tier: opus, mandatory independent review.

### KS8: the gateway runs with the budget off
- Acceptance: a budget-off run that has an identity starts its gateway as a pass-through to the
  service: no cap is read and no key is in the agent's environment; `serviceSwitchModeRefusal` is
  gone; a local run, and a run held on its mounts, still take the key from the environment as
  today; observe and enforce are unchanged.
- Files: `internal/runner/gateway.go`, `internal/runner/routing.go`, `internal/agent/env.go`,
  `internal/runner/gateway_off_test.go` (new).
- Interfaces: none new.
- Tests: `TestBudgetOffRunsThroughThePassThroughGateway`, `TestALocalRunStillReadsItsOwnEnvironment`,
  `TestARunHeldOnItsMountsStillReadsThem`, `TestObserveAndEnforceAreUnchanged`.
- Command: `go test ./internal/runner/ -run 'BudgetOffRunsThrough|ARunHeldOnItsMounts|ObserveAndEnforce' -count=1 && go test ./internal/agent/ -run 'Env|ALocalRun' -count=1`.
- Red step: a budget-off run puts the key in the agent's environment.
- Depends on: KS7 (kept, behaviour: the pass-through needs the identity).
- Commit: `runner: the gateway runs with the budget off, as a pass-through (KS8, D3002, F7)`.
- Reviewer tier: opus, mandatory independent review.

### KS9: the service serves a stage's account
- Acceptance: for a repository whose record names a stage account beside its main credential, the
  service admits that account for that stage's calls and no other, and keeps refusing an account
  the record does not name.
- Files: `internal/credsvc/cascade.go`, `internal/credsvc/cascade_stage_test.go` (new).
- Interfaces: none new.
- Tests: `TestAStageAccountIsServedForItsStage`, `TestAStageAccountIsRefusedForAnotherStage`,
  `TestAnUndeclaredAccountIsStillRefused`.
- Command: `go test ./internal/credsvc/ -run 'AStageAccount|AnUndeclaredAccount|Cascade' -count=1`.
- Red step: `narrowToRepoAuth` refuses the stage account.
- Depends on: KS3 (kept: both edit `cascade.go`).
- Commit: `credsvc: a stage's account is served for its stage (KS9, R9, F23)`.
- Reviewer tier: opus, mandatory independent review.

### KS10: the runner holds no stage token
- Acceptance: the gateway carries the stage's account per stage and asks the service for it; the
  agent's environment holds no token for it where the repository is ready; `SECURITY.md`'s residual
  on the mixed run is changed with its test.
- Files: `internal/gateway/gateway.go`, `internal/agent/env.go`, `internal/cli/run.go`,
  `internal/runner/stage_account_test.go` (new), `SECURITY.md` with `website/content/security.md`
  and the test that pins the sentence.
- Interfaces: none new.
- Tests: `TestTheStageAccountIsAskedOfTheService`, `TestTheAgentHoldsNoStageToken`.
- Command: `go test ./internal/runner/ -run 'TheStageAccountIsAsked|TheAgentHoldsNoStage' -count=1 && go test ./internal/cli/ -run 'MixedStage|SecurityMd' -count=1 && (cd website && npm run check)`.
- Red step: the agent's environment still carries the stage account's token.
- Depends on: KS8 (kept: both edit `agent/env.go`); KS9 (kept, behaviour: the service must admit
  the account first); KS4 (kept: both edit `SECURITY.md`).
- Commit: `runner: the stage account is served by the credentials service (KS10, R9)`.
- Reviewer tier: opus, mandatory independent review.

### KS11: the serving path of a provider credential flips
- Acceptance: `config.CredentialServingPath` answers the service for `openrouter`, and its readers
  follow: the accounts commands, the craft and pool code and the service's pool; a pool's failover
  between provider members is unchanged, at a stage boundary (F24); Vertex still answers the runner.
- Files: `internal/config/stage_accounts.go`, `internal/config/craft.go`,
  `internal/craft/pathpool.go`, `internal/credsvc/pool.go`, `internal/cli/accounts.go`,
  `internal/cli/accounts_add_converge.go`, `internal/cli/accounts_pool.go`. It is past five files
  because one constant has that many readers.
- Interfaces: none new.
- Tests: `TestAnOpenRouterCredentialIsServedByTheService`, `TestAPoolStillFailsOverAtAStageBoundary`,
  `TestVertexStaysOnTheRunner`.
- Command: `go test ./internal/config/ ./internal/craft/ -run 'AnOpenRouterCredentialIsServed|APoolStillFailsOver|VertexStays' -count=1 && go test ./internal/credsvc/ -run Pool -count=1 && go test ./internal/cli/ -run 'Accounts' -count=1`.
- Red step: the path of an `openrouter` credential is the runner's.
- Depends on: KS5 (kept, behaviour: the path may say "service" only once the gateway takes it);
  KS2 (kept: both edit the accounts commands' shared helpers).
- Commit: `config: a provider credential is served by the service (KS11, D1634 reversed, F24)`.
- Reviewer tier: opus, mandatory independent review.

### KS12: `fugaro update` copies the keys into the bundle
- Acceptance: one new stage, after the stage that deploys the service, reads for each model key
  the installation holds outside the bundle its newest version and writes it into the bundle under
  C1's name, through the guard for an older service, in memory, printing no value; it is
  idempotent; it copies again when a container holds a newer version; an entry it did not write
  and that differs is left alone and named in one line; a person who is not a project owner gets
  the stage skipped with a line saying who must run it. `credentialsBundleServes` comes to require
  every named provider entry a repository needs (R10).
- Files: `internal/cli/update_keys.go` (new), `internal/cli/update_keys_test.go` (new),
  `internal/cli/init_stages.go`, `internal/initflow/stage.go`,
  `internal/cli/setup_refusals_test.go`, `internal/apply/steps.go`,
  `internal/cli/init_credentials.go`. It is past five files for the stage's censuses.
- Interfaces: consumes C1 (the entry's naming rule).
- Tests: `TestTheCopyWritesEveryModelKey`, `TestTheCopyPrintsNoValue`,
  `TestAnOperatorsUpdateSkipsTheCopy`, `TestTheCopyNeverOverwritesAnEntryItDidNotWrite`,
  `TestTheCopyTwiceChangesNothing`, `TestTheCopyFollowsANewerVersion`,
  `TestTheCopyRunsAfterTheServiceIsDeployed`, `TestABundleMissingANamedEntryIsNotReady`.
- Command: `go test ./internal/cli/ -run 'TheCopy|AnOperatorsUpdateSkips|ABundleMissingANamedEntry|SetupRefusals' -count=1 && go test ./internal/apply/ -run StepsMatches -count=1`.
- Red step: after an update the bundle lacks a key that a repository's container holds.
- Depends on: C1; KS2 (kept: it uses the since-constant and the guard KS2 extends).
- Commit: `update: the model keys are copied into the bundle (KS12, D3001, D3006, R4)`.
- Reviewer tier: opus, mandatory independent review.

### KS13: release B: no model mount where the bundle is ready
- Acceptance: for a repository that is ready (R10), `SecretMounts` yields no model mount of any
  kind and `CredentialsServiceServes` is true for every auth but Vertex in every budget mode; a
  repository that is not ready, and an installation with no database, keep their mounts and are
  told why in one line; a launch on a job image older than release A is refused with the command
  that rebuilds it; an installation at the previous release converges through `fugaro update`. The
  pinned sentences that say a provider key the job mounts stays its own are changed with their
  tests.
- Files: `internal/infra/spec.go`, `internal/infra/readiness.go`, `internal/infra/spec_test.go`,
  `internal/cli/recipes_skew.go`, `internal/cli/update_converge_test.go`, `SECURITY.md` with
  `website/content/security.md`, `docs/multi-model.md`, `docs/credentials-service/runbook.md` and
  the tests that pin them. It is past five files for those pins.
- Interfaces: none new.
- Tests: `TestAMountGoesOnlyWhenTheBundleIsReady`, `TestNoDatabaseKeepsItsMounts`,
  `TestAnOlderImageIsRefusedAtLaunch`, `TestTheServiceServesEveryAuthButVertex`,
  `TestUpdateConvergesTheMountsFromThePreviousRelease`; the existing
  `TestFilledServiceServedAuthsKeepsProviderKeys` and
  `TestSwitchedMixedRepositoryKeepsItsStageAccountToken` are reversed.
- Command: `go test ./internal/infra/ -run 'AMountGoesOnlyWhen|NoDatabaseKeeps|TheServiceServesEveryAuth|FilledServiceServedAuths|SwitchedMixedRepository' -count=1 && go test ./internal/cli/ -run 'AnOlderImageIsRefused|UpdateConvergesTheMounts|SecurityMd|DocsMultiModel|DocsCredentialsRunbook' -count=1 && (cd website && npm run check)`.
- Red step: an `api-key` job still mounts its provider key.
- Depends on: KS5, KS6, KS8, KS10, KS11 and KS12 (kept, behaviour: a mount may go only once every
  path serves that credential and its value is in the bundle); KS2 (kept: both edit
  `recipes_skew.go`). **Not launched before release A is cut.**
- Commit: `infra: no model key is mounted where the bundle is ready (KS13, R5, R10)`.
- Reviewer tier: opus, mandatory independent review.

### KS14: the workflow module loses its model mounts
- Acceptance: `modules/workflow` holds no model env entry, no job accessor on a model secret and
  no service accessor on one; `var.model_secrets_served` and its locals are gone, with the Go
  validation that mirrors them and the tfvars that set them; the secret-readers list and its test
  match.
- Files: `deploy/terraform/gcp/modules/workflow/job.tf`, `sa.tf` and `variables.tf` beside it,
  `internal/infra/tfvars.go`, `deploy/terraform/terraform_test.go`,
  `deploy/terraform/secret_readers_test.go` with `docs/secrets-brokering/secret-readers.md`.
- Interfaces: none.
- Tests: `TestWorkflowModuleMountsNoModelSecret`, `TestSecretReadersDocMatchesTheTable`;
  `TestServiceHeldSecretsMatchTheCredentialsService` and `TestModelSecretsServedValidationMatchesGo`
  are removed with what they held.
- Command: `go test ./deploy/terraform/ -count=1 && go test ./internal/infra/ -run Tfvars -count=1 && terraform fmt -check -recursive deploy/terraform`.
- Red step: the module still declares the model env entries.
- Depends on: KS13 (kept: the variable goes only after nothing sets it).
- Commit: `terraform: the workflow module mounts no model key (KS14, F9)`.
- Reviewer tier: opus, mandatory independent review.

### KS15: the old model secrets are deleted
- Acceptance: for a per-repository model container whose newest value is in the bundle and that no
  job mounts, a project owner's `fugaro update` takes it out of Terraform's state and deletes it
  through the API; the module no longer declares it; an operator's update deletes nothing and says
  so; no other secret is ever deleted; with a run in flight the update names the deletion in one
  short line and the run finishes. The retained-types note says which class the ruling of
  2026-11-06 no longer covers, and why (D3005).
- Files: `deploy/terraform/gcp/modules/repo/secrets.tf`, `internal/infra/retire.go`,
  `internal/infra/tf/retire.go`, `internal/cli/init_retire.go`,
  `internal/cli/update_delete_keys_test.go` (new), `internal/cli/update_converge_test.go`,
  `deploy/terraform/terraform_test.go`. It is past five files because a deletion is state, API
  and module together.
- Interfaces: none.
- Tests: `TestOnlyACopiedUnmountedModelContainerIsDeleted`, `TestAnOperatorsUpdateDeletesNothing`,
  `TestANonModelSecretIsNeverDeleted`, `TestUpdateDeletesTheOldSecretsWithARunInFlight`.
- Command: `go test ./internal/cli/ -run 'OnlyACopiedUnmounted|AnOperatorsUpdateDeletes|ANonModelSecret|UpdateDeletesTheOldSecrets' -count=1 && go test ./deploy/terraform/ -count=1 && go test ./internal/infra/... -run Retir -count=1`.
- Red step: the plan refuses to destroy the container.
- Depends on: KS14 (kept: a container goes only after its accessors, and both edit
  `terraform_test.go`); KS12 (kept: the copy must exist first).
- Commit: `update: a repository's old model secrets are deleted once copied (KS15, D3005, R7)`.
- Reviewer tier: opus, mandatory independent review.

### KS16: shared project jobs
- Acceptance: after secrets brokering's B5.7 has dropped a shared job's mounted token, a provider
  key a shared job mounted as a profile secret is served through the service like any other, and
  the project-wide model containers follow KS15's rule. **This task waits on B5.7, which is another
  plan's and is not started.**
- Files: `deploy/terraform/gcp/modules/installation/secrets.tf`,
  `deploy/terraform/gcp/modules/project/job.tf`, `internal/infra/projectjobs.go`,
  `internal/cli/update_delete_project_keys_test.go` (new), `deploy/terraform/terraform_test.go`.
- Interfaces: none.
- Tests: `TestASharedJobMountsNoProviderKey`, `TestAProjectModelContainerIsDeletedOnlyAfterItsCopy`.
- Command: `go test ./deploy/terraform/ -count=1 && go test ./internal/infra/ -run ProjectJob -count=1 && go test ./internal/cli/ -run AProjectModelContainer -count=1`.
- Red step: the shared job still mounts the profile's provider key.
- Depends on: KS15 (kept: the same deletion, and both edit `terraform_test.go`); secrets
  brokering's B5.7 (external).
- Commit: `terraform: shared project jobs mount no model key (KS16, F27)`.
- Reviewer tier: opus, mandatory independent review.

### KS17: the live checks, as scripts
- Acceptance: Check 50 and Checks 780 to 782 exist as one command each under
  `scripts/live-checks/`, each with its hermetic self-test and its row in
  `docs/gcp-live-checklist.md`; Check 50's row keeps the facts its documents test pins; none is run
  by an agent.
- Files: `scripts/live-checks/check50.sh`, `check780.sh`, `check781.sh`, `check782.sh` (new) with
  their `_test.sh`, `docs/gcp-live-checklist.md`.
- Interfaces: none.
- Tests: the four `_test.sh`, `TestChecklistHasCheck50`.
- Command: `for n in 50 780 781 782; do bash scripts/live-checks/check${n}_test.sh; done && go test ./internal/cli/ -run 'ChecklistHasCheck50|Docs' -count=1`.
- Red step: each self-test fails on a missing script.
- Depends on: none.
- Commit: `checks: 50 and 780 to 782 as scripts (KS17, F13)`.
- Reviewer tier: standard.
- Owner-run: `bash scripts/live-checks/check50.sh` and the three like it. No agent performs any
  step of them.

### KS18: the documents and the operator notes
- Acceptance: the model-crafts documents mark D1634 and D1670 reversed by D2975;
  `docs/workspace-credentials/spec.md` says the gate is narrowed (O1); the rules file and
  `docs/release.md` no longer describe a runner path for provider keys; each release's
  `### For operators` is "run `fugaro update`" and what it will say: in release A that a project
  owner's update copies the model keys into the bundle, in release B that it removes the mounts
  and deletes the old secrets, and that a run on an image older than release A is refused with the
  rebuild command.
- Files: `docs/model-crafts/reconciliation.md`, `docs/workspace-credentials/spec.md`,
  `.claude/rules/credentials-service.md`, `docs/release.md`, the release notes files of the two
  releases.
- Interfaces: none.
- Tests: the Docs filter.
- Command: `go test ./internal/cli/ -run Docs -count=1`.
- Red step: a documents test that pins the runner path for a provider key fails until its pin moves.
- Depends on: KS13 (kept: the statements become true only then).
- Commit: `docs: every model key is served by the credentials service (KS18)`.
- Reviewer tier: opus, mandatory independent review (a rules file).

### I1: integration
- Acceptance: the stub `provider_wire_stub.go` is deleted; one hermetic end-to-end test drives a
  ready repository's run with a non-Claude provider coder through its gateway to the service and a
  fake provider: the call is served and charged what it cost, with the key copied by the update's
  stage and none read from the job's environment; the same run with a Claude model through the
  provider is refused naming Check 50.
- Files: `internal/credsvc/provider_wire_stub.go` (deleted),
  `internal/cli/keys_end_to_end_test.go` (new).
- Interfaces: consumes every side of C1.
- Tests: `TestAProviderModelIsServedEndToEndFromACopiedKey`,
  `TestAClaudeModelThroughAProviderIsRefusedEndToEnd`.
- Command: `go test ./internal/cli/ -run 'AProviderModelIsServedEndToEnd|AClaudeModelThroughAProviderIsRefusedEndToEnd' -count=1 && go build ./... && go vet ./...`.
- Red step: the test fails to build while the stub is still referenced.
- Depends on: C1 and every side of an edge it broke: KS1, KS2, KS3, KS4, KS5, KS12.
- Commit: `keys: a provider model served end to end (I1)`.
- Reviewer tier: opus, mandatory independent review.

## 5. Parallel execution

The decoupling pass is on (`planning.decoupling` is `true` for this repository), and the
repository's mode is `startup`.

**Levels.** Tasks in one level run at once. **A launcher follows this table.**

| Level | Tasks |
| --- | --- |
| 0 | C1, KS7, KS17 |
| 1 | KS1, KS2, KS3, KS5, KS8 |
| 2 | KS4, KS9, KS11, KS12 |
| 3 | KS6, KS10, I1 |
| 4 | KS13 (after release A is cut) |
| 5 | KS14, KS18 |
| 6 | KS15 |
| 7 | KS16 (after B5.7) |

**Critical path.** By these levels: C1, KS3, KS4, KS10, KS13, KS14, KS15, KS16. The last four are a
true sequence: a mount, its Terraform and its secret each go only after the one before. The graph
read reports `critical path (8 tasks): C1 -> KS3 -> KS4 -> KS10 -> KS13 -> KS14 -> KS15 -> KS16`, with C1
the bottleneck, and `gain: 5 broken, 23 kept, 3 added; before 9 tasks, weight 22; after 8 tasks, weight 17.5;
improves`.

**Where the graph and this table differ.** The graph format records a contract's consumers only,
so KS1, which builds C1's other side, carries no edge to C1 in the graph and reads there as ready
at once. It is not: it starts after C1 merges. The graph also cannot hold the two outside waits:
the release between KS12 and KS13, and B5.7 before KS16.

**Edges broken by C1 and rejoined in I1.** KS2, KS3, KS4 and KS12 after KS1: each needs the entry's
naming rule and the accessor's signature, not the bundle's code. KS5 after KS4: the gateway needs
the wire, not the route's code.

**Edges kept.** Each is stated in its task: behaviour (KS6 after KS4 and KS5; KS8 after KS7; KS10
after KS9; KS11 after KS5; KS13 after KS5, KS6, KS8, KS10, KS11 and KS12; KS14 after KS13; KS15
after KS12 and KS14; KS16 after KS15), a shared file (KS4 and KS9 after KS3; KS10 after KS4 and
KS8; KS11 and KS12 after KS2; KS13 after KS2), and another plan (KS16 after B5.7).

**Shared files.** No level holds two writers of one file.

| File | Writers, by level |
| --- | --- |
| `internal/credsvc/cascade.go` | KS3 (1), KS9 (2) |
| `internal/credsvc/forward.go`, `request.go`, `server.go` | KS4 (2) |
| `internal/credsvc/bundle.go`, `schemas/creds-bundle.schema.json` | KS1 (1) |
| `internal/credsvc/pool.go` | KS11 (2) |
| `internal/gateway/route.go`, `forward.go`, `service.go` | KS5 (1) |
| `internal/gateway/gateway.go` | KS10 (3) |
| `internal/runner/gateway.go`, `routing.go` | KS8 (1) |
| `internal/runner/budget_session.go`, `internal/cli/launch_budget.go` | KS7 (0) |
| `internal/agent/env.go` | KS8 (1), KS10 (3) |
| `internal/config/providers.go` | KS6 (3) |
| `internal/config/stage_accounts.go`, `craft.go`, `internal/craft/pathpool.go` | KS11 (2) |
| `internal/cli/creds.go`, `accounts_add.go`, `secrets.go`, `initsecrets.go` | KS2 (1) |
| `internal/cli/accounts.go`, `accounts_add_converge.go`, `accounts_pool.go` | KS11 (2) |
| `internal/cli/recipes_skew.go` | KS2 (1), KS13 (4) |
| `internal/cli/run.go` | KS10 (3) |
| `internal/cli/init_stages.go`, `internal/initflow/stage.go`, `internal/apply/steps.go`, `internal/cli/init_credentials.go` | KS12 (2) |
| `internal/infra/spec.go`, `readiness.go`, `spec_test.go` | KS13 (4) |
| `internal/infra/tfvars.go` | KS14 (5) |
| `internal/cli/update_converge_test.go` | KS13 (4), KS15 (6) |
| `deploy/terraform/terraform_test.go` | KS14 (5), KS15 (6), KS16 (7) |
| `SECURITY.md`, `website/content/security.md` and their tests | KS4 (2), KS10 (3), KS13 (4) |
| `docs/multi-model.md`, `docs/credentials-service/runbook.md` and their tests | KS13 (4) |
| `docs/gcp-live-checklist.md` | KS17 (0) |
| `website/content/cli-reference.md` | KS2 (1) |

R8 lists the files other plans also write, and how the plans are kept apart.

## 6. The estimate

As `fugaro plan graph --plan docs/keys-through-service/implementation-plan.md` printed it:

`ready in 555–2220 min, no history yet for 21 tasks (21 tasks; seeds for 21; from the seeds: no history is cached on this machine yet)`

It is informational and reserves no budget: it is what the tasks' tiers and this machine's history
predict, and it moves as runs finish. It does not count the release between KS12 and KS13 or the
wait for B5.7. The graph counts one node more than this plan's 20 runs: the contract change of §7,
which runs only if raised.

## 7. Contract changes anticipated

- **R-entry-name** (C1, raised by KS12): if U2 shows a provider key's three sources cannot be named
  as C1 wrote the rule. Reaches KS1, KS2, KS4.

## 8. Checks the plan takes

From the reserved block 780–789, and Check 50 written as a script. All owner-run (KS17).

| Check | What it measures |
| --- | --- |
| **50** | As its checklist row says: whether Claude's cache markers survive a provider's endpoint. Passing it lifts KS3's refusal for a Claude model, in a change of its own |
| **780** | After release B, on a real installation that was at the release before A: a project owner's `fugaro update` copies the model keys, drops the mounts and deletes the old secrets, with a run in flight that finishes; an operator's update does none of it (U3) |
| **781** | After release A: a non-Claude provider model served through the service end to end, charged the provider's reported cost, with the key read from the bundle |
| **782** | After release A: a budget-off run through the pass-through gateway: served, nothing charged, no key in the agent's environment |

## 9. After-deploy steps

**Release A: `fugaro update`, run by a project owner.** It copies the model keys into the bundle.
An operator's update skips that step and says so; nothing breaks, because the mounts are still
there.

**Release B: `fugaro update`, run by a project owner.** It rebuilds the job images behind its
existing confirmation, removes the model mounts of every repository whose bundle is ready, and
deletes the old key secrets. A run launched on an image older than release A is refused with the
rebuild command. An installation with no database keeps its mounts.

KS18's release notes say what each update prints.

## 10. Considered and declined

- **Everything behind Check 50** (the owner's first answer, replaced by D3000).
- **The owner re-entering each key, or keeping the per-repository containers** (D3001's other
  options).
- **Dropping the old secret from Terraform and keeping it** (the recommendation of D3005): the
  owner chose deletion.
- **Metering provider calls in the service** (D3003's other option): a second price table, ledger
  and fee logic.
- **Budget-off keeping its mounts everywhere** (D3004's other option), **or removing the mode**.
- **Letting operators copy and set keys** (D3006's other option): it ends "operators never read a
  value".
- **Moving routing, the fee and the settle into the service**: only the last hop moves (R1).
- **Dropping a repository's mounts when its own image is rebuilt** (the first draft of R5): that
  fact is read at launch, per workflow, and a catalog image is not rebuilt by `fugaro update`. Two
  releases and a launch gate, as the earlier flips did.
- **Retiring `agent.auth` as a repository's setting** (D3007 read in full): its own spec if the
  owner wants it (O4).
- **Serving Vertex through the service**: there is no key to move (A2).

# Every model key through the credentials service: implementation plan

**Status: plan only. Nothing is implemented and no task is approved; each task needs the owner's
explicit go by name.** It builds [spec.md](spec.md) as amended by
[reconciliation.md](reconciliation.md), which wins where the two disagree.

The answers that shaped it: only a Claude model through a provider key waits for Check 50 (D3000);
every model key lives in the project's one bundle and `fugaro update` copies the existing ones
there (D3001); the gateway always runs, as a pass-through when the budget is off (D3002). The
owner's ruling D2975 reverses D1634 and D1670.

## 1. Conventions every task follows

Point, don't restate: `CLAUDE.md`, `.claude/rules/credentials-service.md` (every task under
`internal/creds`, `internal/credsvc`, `SECURITY.md`), `.claude/rules/terraform.md` (KS9 to KS12),
`docs/no-prompt-policy/`, `docs/design/update-in-flight.md`.

- **Always.** Targeted tests, never a whole package. Hermetic: `internal/gcpfake`, `anthropicfake`,
  `t.TempDir()`. A task that makes a pinned sentence of `SECURITY.md`, `docs/multi-model.md` or the
  credentials runbook false changes the sentence, the test that pins it and
  `website/content/security.md` in the same change, and runs `npm run check` in `website/` (F12).
  A new command, flag or update stage is added to the censuses that count it: the CLI reference,
  the closed-stdin harness, the list of writing commands, the stage refusal table and
  `internal/apply/steps.go`.
- **Ask first.** A new dependency; a change to the contract's files (raise a contract change, §7).
- **Never.** A secret-shaped literal; a live cloud call from a test or an agent; a confirmation
  step; an edit under `.github/workflows/`; a key's value in a log, an error, a header, a test
  name or the terminal; a mount dropped for a repository whose image cannot do without it (R5).
- **Before launching** a task that edits `internal/credsvc/cascade.go`, `forward.go` or
  `internal/gateway/service.go`: check no workspace-service task that edits the same file is live
  (R8).

## 2. Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| A run cannot name a provider credential its policy does not give its repository | KS4 `TestANamedCredentialOutsideThePolicyIsRefused` |
| A Claude model through a provider key is refused until Check 50; any other model is served | KS3 `TestAClaudeModelThroughAProviderWaitsForCheck50`, `TestANonClaudeProviderModelIsServed` |
| The fee, the price check and the provider-cost settle are the gateway's and are unchanged | KS5 `TestARoutedCallThroughTheServiceCostsWhatItCostBefore` |
| No provider key is in the job's environment | KS5 `TestTheProviderKeyNeverReachesTheJob`; I1 |
| With the budget off there is no key in the agent's environment and nothing is charged | KS6 `TestBudgetOffRunsThroughThePassThroughGateway` |
| The copy prints no value, overwrites nothing silently and is idempotent | KS8 `TestTheCopyPrintsNoValue`, `TestTheCopyNeverOverwritesADifferentEntry`, `TestTheCopyTwiceChangesNothing` |
| A mount is dropped only for a repository whose image is at or after this release | KS9 `TestAnOlderImageKeepsItsMounts` |
| A container is retired only after its value is in the bundle and no job mounts it | KS11 `TestAContainerIsRetiredOnlyAfterItsCopy` |
| `fugaro update` converges an installation at the previous release with a run in flight | KS9 `TestUpdateConvergesTheMountsFromThePreviousRelease`; KS11 `TestUpdateRetiresTheContainersWithARunInFlight` |

## 3. The contract

### C1: the provider wire
- Acceptance: one place declares how a routed call names the provider credential it is for (the
  header and the route's path), the typed refusals the service answers it with, the bundle's entry
  for a named provider credential and its name (R2, U2), and the accessor the service reads it
  through. A stub accessor answers "no such credential". The bundle's JSON Schema carries the
  entry.
- Files: `internal/credsvc/provider_wire.go` (new), `internal/credsvc/provider_wire_stub.go` (new,
  stub), `schemas/creds-bundle.schema.json`, `docs/keys-through-service/contract-provider.md` (new).
- Interfaces: produces the names, the refusal types and the accessor's signature; consumed by KS2,
  KS3, KS4, KS5, KS8. KS1 builds its other side.
- Tests: `TestStubAccessorHoldsNothing`, `TestBundleSchemaCarriesANamedProviderEntry`.
- Command: `go test ./internal/credsvc/ -run 'StubAccessor|BundleSchema' -count=1`.
- Red step: the schema test fails until the entry is declared.
- Depends on: none.
- Commit: `credsvc: the provider wire contract (C1, R1, R2)`.
- Reviewer tier: opus, mandatory independent review.

## 4. Slices and tasks

**Slice A, the service serves a named provider key:** KS1 to KS4.
**Slice B, the run asks for it:** KS5 to KS7.
**Slice C, the keys move and the mounts go:** KS8 to KS12.
**Slice D, checks and documents:** KS13, KS14. **Integration:** I1.

### KS1: named provider entries in the bundle
- Acceptance: the bundle decodes and serves one key per named provider credential, with its base
  address and auth form, beside the entries it has today; a bundle of today's shape still loads; a
  bundle at the stated worst case stays under the secret's size limit (U5).
- Files: `internal/credsvc/bundle.go`, `internal/credsvc/bundle_provider_test.go` (new).
- Interfaces: builds C1's other side (the real accessor).
- Tests: `TestBundleServesANamedProviderEntry`, `TestTodaysBundleStillLoads`,
  `TestAWorstCaseBundleFits`.
- Command: `go test ./internal/credsvc/ -run 'BundleServesANamed|TodaysBundleStill|AWorstCaseBundle' -count=1`.
- Red step: a named provider entry is refused as an unknown key.
- Depends on: C1 (it starts after C1 merges).
- Commit: `credsvc: the bundle holds named provider keys (KS1, F3, R2)`.
- Reviewer tier: opus, mandatory independent review.

### KS2: `creds set` and `accounts add` write the bundle
- Acceptance: `fugaro creds set credential-<name>-key` accepts an `openrouter` credential and
  writes the bundle; `fugaro accounts add --type openrouter` writes the bundle and no job
  container; both read the value from stdin only and ask nothing.
- Files: `internal/cli/creds.go`, `internal/cli/accounts_add.go`,
  `internal/cli/accounts_add_key_test.go`, `website/content/cli-reference.md`.
- Interfaces: consumes C1 (the entry's name).
- Tests: `TestCredsSetAcceptsAnOpenRouterKey`, `TestAccountsAddOpenRouterWritesTheBundle`,
  `TestAccountsAddOpenRouterWritesNoContainer`.
- Command: `go test ./internal/cli/ -run 'CredsSetAcceptsAnOpenRouter|AccountsAddOpenRouter|CliReferenceNames' -count=1`.
- Red step: `creds set` refuses the name with `credsKeyCredentialHalfRefusal`.
- Depends on: C1.
- Commit: `cli: an openrouter key goes to the bundle (KS2, D1670 reversed)`.
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

### KS4: the service's provider route takes a name
- Acceptance: the provider route reads the credential's name from the call, checks it against the
  calling repository's published policy and its `allow_data_to`, attaches that key and forwards to
  that provider's address, streaming both ways and holding nothing; a name the policy does not
  give the repository is refused before any key is read.
- Files: `internal/credsvc/forward.go`, `internal/credsvc/forward_provider_test.go` (new).
- Interfaces: consumes C1.
- Tests: `TestANamedCredentialOutsideThePolicyIsRefused`, `TestTheNamedKeyIsAttachedAndTheBodyUntouched`,
  the existing `TestOpenRouterCallSwapsKeyAndStreams` and `TestOpenRouterKeyNeverReachesJob` unchanged.
- Command: `go test ./internal/credsvc/ -run 'ANamedCredentialOutside|TheNamedKeyIsAttached|OpenRouter' -count=1`.
- Red step: the route forwards with the single `provider-key` whatever name is asked.
- Depends on: C1; KS3 (kept: both edit the candidate path, and KS4 calls the selection KS3 changes).
- Commit: `credsvc: the provider route serves a named credential (KS4, R1)`.
- Reviewer tier: opus, mandatory independent review.

### KS5: a provider route goes through the service
- Acceptance: on a repository the service serves, the gateway sends a routed call to the service's
  provider route with the credential's name, keeping its own routing, fee, price check and settle;
  `config.CheckProviderAuth` admits a provider pin under every auth the service serves; the runner
  reads no provider key from its environment. The pinned sentences that say a provider key the job
  mounts stays its own are changed with their tests.
- Files: `internal/gateway/route.go`, `internal/gateway/service.go`, `internal/runner/providers.go`,
  `internal/runner/providerkey.go`, `internal/config/providers.go`,
  `internal/gateway/route_service_test.go` (new), `SECURITY.md` with `website/content/security.md`
  and `docs/multi-model.md` and the tests that pin them.
- Interfaces: consumes C1.
- Tests: `TestARoutedCallGoesThroughTheService`, `TestARoutedCallThroughTheServiceCostsWhatItCostBefore`,
  `TestTheProviderKeyNeverReachesTheJob`, `TestAProviderPinIsAdmittedUnderOAuth`.
- Command: `go test ./internal/gateway/ -run 'ARoutedCall|TheProviderKeyNever' -count=1 && go test ./internal/config/ -run AProviderPinIsAdmitted -count=1 && go test ./internal/cli/ -run 'SecurityMd|DocsMultiModel|Docs' -count=1 && (cd website && npm run check)`.
- Red step: `validateRoutes` refuses the route with `errServiceOnRoute`.
- Depends on: C1.
- Commit: `gateway: a provider route is served through the credentials service (KS5, D1634 reversed, R1)`.
- Reviewer tier: opus, mandatory independent review.

### KS6: the gateway always runs
- Acceptance: with the budget mode off the run starts its gateway as a pass-through to the
  service: nothing is reserved or charged, no cap is read, and no key is in the agent's
  environment; `serviceSwitchModeRefusal` is gone; the other modes are unchanged.
- Files: `internal/runner/gateway.go`, `internal/runner/routing.go`, `internal/agent/env.go`,
  `internal/runner/gateway_off_test.go` (new).
- Interfaces: none new.
- Tests: `TestBudgetOffRunsThroughThePassThroughGateway`, `TestBudgetOffChargesNothing`,
  `TestObserveAndEnforceAreUnchanged`.
- Command: `go test ./internal/runner/ -run 'BudgetOffRuns|BudgetOffCharges|ObserveAndEnforce' -count=1 && go test ./internal/agent/ -run Env -count=1`.
- Red step: a budget-off run puts the key in the agent's environment.
- Depends on: none.
- Commit: `runner: the gateway runs with the budget off, as a pass-through (KS6, D3002, F7)`.
- Reviewer tier: opus, mandatory independent review.

### KS7: the stage account goes through the service
- Acceptance: the mixed variant's second account is selected by the service through the gateway;
  the runner's environment holds no token for it; `SECURITY.md`'s residual on the mixed run is
  changed with its test. The first step settles U4.
- Files: `internal/cli/run.go`, `internal/runner/gateway.go`, `internal/config/providers.go`,
  `internal/runner/stage_account_test.go` (new), `SECURITY.md` with `website/content/security.md`
  and its test.
- Interfaces: none new.
- Tests: `TestTheStageAccountIsServedByTheService`, `TestTheRunnerHoldsNoStageToken`,
  `TestSwitchedMixedRepositoryKeepsItsStageAccountToken` (replaced by the two).
- Command: `go test ./internal/runner/ -run 'TheStageAccountIsServed|TheRunnerHoldsNoStage' -count=1 && go test ./internal/cli/ -run 'MixedStage|SecurityMd' -count=1`.
- Red step: the runner's environment still carries the stage account's token.
- Depends on: KS6 (kept: both edit `runner/gateway.go`); KS5 (kept: both edit
  `internal/config/providers.go`).
- Commit: `runner: the stage account is served by the credentials service (KS7, R9, F6)`.
- Reviewer tier: opus, mandatory independent review.

### KS8: `fugaro update` copies the keys into the bundle
- Acceptance: one new stage reads, for each model key the installation holds outside the bundle,
  its newest version, and writes it into the bundle under R2's name, in memory, printing no value;
  it is idempotent; an entry already in the bundle with a different value is left alone and named
  in one line; it runs before any stage that drops a mount; with runs in flight it changes nothing
  a run holds. The first step settles U1.
- Files: `internal/cli/update_keys.go` (new), `internal/cli/update_keys_test.go` (new),
  `internal/cli/init_stages.go`, `internal/initflow/stage.go`,
  `internal/cli/setup_refusals_test.go`, `internal/apply/steps.go`.
- Interfaces: consumes C1 (the entry's name).
- Tests: `TestTheCopyWritesEveryModelKey`, `TestTheCopyPrintsNoValue`,
  `TestTheCopyNeverOverwritesADifferentEntry`, `TestTheCopyTwiceChangesNothing`,
  `TestTheCopyRunsBeforeAnyMountIsDropped`.
- Command: `go test ./internal/cli/ -run 'TheCopy|SetupRefusals' -count=1 && go test ./internal/apply/ -run StepsMatches -count=1`.
- Red step: after an update the bundle lacks a key that a repository's container holds.
- Depends on: C1.
- Commit: `update: the model keys are copied into the bundle (KS8, D3001, R4)`.
- Reviewer tier: opus, mandatory independent review.

### KS9: no model mount for a repository that can do without
- Acceptance: for a repository whose job image is at or after this release, `SecretMounts` yields
  no model mount of any kind, and `CredentialsServiceServes` is true for every auth but Vertex in
  every budget mode; a repository on an older image keeps its mounts; the tfvars carry the result;
  an installation at the previous release converges in one `fugaro update`.
- Files: `internal/infra/spec.go`, `internal/infra/readiness.go`, `internal/infra/tfvars.go`,
  `internal/cli/recipes_skew.go`, `internal/infra/spec_nomount_test.go` (new),
  `internal/cli/update_converge_test.go`.
- Interfaces: none new.
- Tests: `TestNoModelMountAfterThisRelease`, `TestAnOlderImageKeepsItsMounts`,
  `TestTheServiceServesEveryAuthButVertex`, `TestUpdateConvergesTheMountsFromThePreviousRelease`,
  the existing `TestFilledServiceServedAuthsKeepsProviderKeys` (reversed).
- Command: `go test ./internal/infra/ -run 'NoModelMount|AnOlderImageKeeps|TheServiceServesEveryAuth|FilledServiceServedAuths' -count=1 && go test ./internal/cli/ -run UpdateConvergesTheMounts -count=1`.
- Red step: an `api-key` job still mounts its provider key.
- Depends on: KS5, KS6, KS7 and KS8 (kept, behaviour: a mount may go only once every path serves
  that credential and its value is in the bundle); KS2 (kept: the write path must not create a
  container this task stops mounting).
- Commit: `infra: no model key is mounted where the image can do without (KS9, R5, R10)`.
- Reviewer tier: opus, mandatory independent review.

### KS10: the workflow module loses its model mounts
- Acceptance: `modules/workflow` holds no model env entry, no job accessor on a model secret and
  no service accessor on one; `var.model_secrets_served` and its locals are gone, with the Go
  validation that mirrors them; the secret-readers list and its test match.
- Files: `deploy/terraform/gcp/modules/workflow/job.tf`, `sa.tf` and `variables.tf` beside it,
  `internal/infra/tfvars.go`, `deploy/terraform/terraform_test.go`,
  `deploy/terraform/secret_readers_test.go` with `docs/secrets-brokering/secret-readers.md`.
- Interfaces: none.
- Tests: `TestWorkflowModuleMountsNoModelSecret`, `TestSecretReadersDocMatchesTheTable`,
  `TestServiceHeldSecretsMatchTheCredentialsService` and `TestModelSecretsServedValidationMatchesGo`
  (removed with what they held).
- Command: `go test ./deploy/terraform/ -count=1 && go test ./internal/infra/ -run Tfvars -count=1 && terraform fmt -check -recursive deploy/terraform`.
- Red step: the module still declares the model env entries.
- Depends on: KS9 (kept: the variable goes only after nothing sets it, and both edit `tfvars.go`).
- Commit: `terraform: the workflow module mounts no model key (KS10, F9)`.
- Reviewer tier: opus, mandatory independent review.

### KS11: the per-repository model containers are retired
- Acceptance: a per-repository model container is removed by `fugaro update`'s own retirement,
  only after its value is in the bundle and no job mounts it; `prevent_destroy` no longer blocks
  that one case; with a run in flight the update names the retirement in one short line and the
  run finishes; non-model containers are untouched.
- Files: `deploy/terraform/gcp/modules/repo/secrets.tf`, `internal/infra/tf/retire.go`, `internal/cli/update_retire_keys_test.go` (new),
  `internal/cli/update_converge_test.go`, `deploy/terraform/terraform_test.go`.
- Interfaces: none.
- Tests: `TestAContainerIsRetiredOnlyAfterItsCopy`, `TestUpdateRetiresTheContainersWithARunInFlight`,
  `TestANonModelContainerIsNeverRetired`.
- Command: `go test ./internal/cli/ -run 'AContainerIsRetiredOnly|UpdateRetiresTheContainers|ANonModelContainer' -count=1 && go test ./deploy/terraform/ -count=1 && go test ./internal/infra/tf/ -run Retir -count=1`.
- Red step: the plan refuses to destroy the container.
- Depends on: KS10 (kept: both edit `terraform_test.go`, and a container goes only after its
  accessors); KS8 (kept: the copy must exist first).
- Commit: `update: a repository's model containers are retired once copied (KS11, R7)`.
- Reviewer tier: opus, mandatory independent review.

### KS12: shared project jobs
- Acceptance: once secrets brokering's B5.7 has moved a shared project job's token to the service,
  the project-wide model containers follow KS11's rule and `modules/project` holds no model mount.
  **This task waits on B5.7, which is another plan's and is not started.**
- Files: `deploy/terraform/gcp/modules/project/job.tf` and `sa.tf`,
  `deploy/terraform/gcp/modules/installation/secrets.tf`, `internal/infra/projectjobs.go`,
  `deploy/terraform/terraform_test.go`.
- Interfaces: none.
- Tests: `TestProjectModuleMountsNoModelSecret`, `TestAProjectContainerIsRetiredOnlyAfterItsCopy`.
- Command: `go test ./deploy/terraform/ -count=1 && go test ./internal/infra/ -run ProjectJob -count=1`.
- Red step: the project module still mounts the account's token.
- Depends on: KS11 (kept: the same retirement, and both edit `terraform_test.go`); secrets
  brokering's B5.7 (external).
- Commit: `terraform: shared project jobs mount no model key (KS12, F6)`.
- Reviewer tier: opus, mandatory independent review.

### KS13: the live checks, as scripts
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
- Commit: `checks: 50 and 780 to 782 as scripts (KS13, F13)`.
- Reviewer tier: standard.
- Owner-run: `bash scripts/live-checks/check50.sh` and the three like it. No agent performs any
  step of them.

### KS14: the documents and the operator note
- Acceptance: the model-crafts documents mark D1634 and D1670 reversed by D2975;
  `docs/workspace-credentials/spec.md` says the gate is narrowed (O1);
  `.claude/rules/credentials-service.md`, the credentials runbook and `docs/release.md` no longer
  describe a runner path for provider keys; the release note's `### For operators` is "run
  `fugaro update`" and what it will say: that it copies the model keys into the bundle and, once
  each repository's image is rebuilt, removes its mounts and containers.
- Files: `docs/model-crafts/reconciliation.md`, `docs/workspace-credentials/spec.md`,
  `.claude/rules/credentials-service.md`, `docs/credentials-service/runbook.md`, `docs/release.md`,
  the release notes file of the release that carries it.
- Interfaces: none.
- Tests: the Docs filter.
- Command: `go test ./internal/cli/ -run Docs -count=1`.
- Red step: `TestDocsCredentialsRunbookSaysWhatTheLaunchWires` fails on the changed sentence until
  its pin moves.
- Depends on: KS9 (kept: the statements become true only then).
- Commit: `docs: every model key is served by the credentials service (KS14)`.
- Reviewer tier: opus, mandatory independent review (a rules file).

### I1: integration
- Acceptance: the stub `provider_wire_stub.go` is deleted; one hermetic end-to-end test drives an
  `api-key` repository's run with a non-Claude provider coder through its gateway to the service
  and a fake provider: the call is served, charged what it cost, and no provider key is in the
  job's environment; the same run with a Claude model through the provider is refused naming
  Check 50.
- Files: `internal/credsvc/provider_wire_stub.go` (deleted),
  `internal/cli/keys_end_to_end_test.go` (new).
- Interfaces: consumes every side of C1.
- Tests: `TestAProviderModelIsServedEndToEndWithNoKeyInTheJob`,
  `TestAClaudeModelThroughAProviderIsRefusedEndToEnd`.
- Command: `go test ./internal/cli/ -run 'AProviderModelIsServedEndToEnd|AClaudeModelThroughAProviderIsRefusedEndToEnd' -count=1 && go build ./... && go vet ./...`.
- Red step: the test fails to build while the stub is still referenced.
- Depends on: C1 and every side of an edge it broke: KS1, KS2, KS3, KS4, KS5, KS8.
- Commit: `keys: a provider model served end to end (I1)`.
- Reviewer tier: opus, mandatory independent review.

## 5. Parallel execution

The decoupling pass is on (`planning.decoupling` is `true` for this repository), and the
repository's mode is `startup`.

**Levels.** Tasks in one level run at once. **A launcher follows this table.**

| Level | Tasks |
| --- | --- |
| 0 | C1, KS6, KS13 |
| 1 | KS1, KS2, KS3, KS5, KS8 |
| 2 | KS4, KS7 |
| 3 | KS9, I1 |
| 4 | KS10, KS14 |
| 5 | KS11 |
| 6 | KS12 |

**Critical path.** C1, KS5, KS7, KS9, KS10, KS11, KS12: seven steps, the last five a true
sequence: a mount, its Terraform and its container each go only after the one before. The graph
read reports `critical path (7 tasks): C1 -> KS5 -> KS7 -> KS9 -> KS10 -> KS11 -> KS12`, with C1 the
bottleneck, and `gain: 5 broken, 13 kept, 3 added; before 9 tasks, weight 20; after 7 tasks, weight 14.5;
improves`.

**Where the graph and this table differ.** The graph format records a contract's consumers only,
so KS1, which builds C1's other side, carries no edge to C1 in the graph and reads there as ready
at once. It is not: it starts after C1 merges.

**Edges broken by C1 and rejoined in I1.** KS2, KS3, KS4 and KS8 after KS1: each needs the entry's
name and the accessor's signature, not the bundle's code. KS5 after KS4: the gateway needs the
wire, not the route's code.

**Edges kept.** Each is stated in its task: behaviour (KS9 after KS5, KS6, KS7, KS8 and KS2; KS10
after KS9; KS11 after KS8 and KS10; KS12 after KS11), a shared file (KS4 after KS3; KS7 after KS5
and KS6), and another plan (KS12 after B5.7).

**Shared files.** No level holds two writers of one file.

| File | Writers, by level |
| --- | --- |
| `internal/credsvc/cascade.go` | KS3 (1) |
| `internal/credsvc/forward.go` | KS4 (2) |
| `internal/credsvc/bundle.go` | KS1 (1) |
| `internal/gateway/service.go`, `route.go` | KS5 (1) |
| `internal/runner/gateway.go` | KS6 (0), KS7 (2) |
| `internal/config/providers.go` | KS5 (1), KS7 (2) |
| `internal/cli/creds.go`, `accounts_add.go` | KS2 (1) |
| `internal/cli/run.go` | KS7 (2) |
| `internal/cli/init_stages.go`, `internal/initflow/stage.go`, `internal/apply/steps.go` | KS8 (1) |
| `internal/infra/tfvars.go` | KS9 (3), KS10 (4) |
| `internal/cli/update_converge_test.go` | KS9 (3), KS11 (5) |
| `deploy/terraform/terraform_test.go` | KS10 (4), KS11 (5), KS12 (6) |
| `SECURITY.md`, `website/content/security.md` | KS5 (1), KS7 (2) |
| `docs/gcp-live-checklist.md` | KS13 (0) |
| `website/content/cli-reference.md` | KS2 (1) |

`internal/credsvc/cascade.go`, `forward.go` and `internal/gateway/service.go` are also written by
the workspace-service plan; R8 says how the two are kept apart.

## 6. The estimate

As `fugaro plan graph --plan docs/keys-through-service/implementation-plan.md` printed it:

`ready in 435–1740 min, no history yet for 17 tasks (17 tasks; seeds for 17; from the seeds: no history is cached on this machine yet)`

It is informational and reserves no budget: it is what the tasks' tiers and this machine's history
predict, and it moves as runs finish. The graph counts one node more than this plan's 16 runs: the
contract change of §7, which runs only if raised.

## 7. Contract changes anticipated

- **R-entry-name** (C1, raised by KS8): if U2 shows a repository's own provider secret cannot be
  named as C1 wrote it. Reaches KS1, KS2, KS4.

## 8. Checks the plan takes

From the reserved block 780–789, and Check 50 written as a script. All owner-run (KS13).

| Check | What it measures |
| --- | --- |
| **50** | As its checklist row says: whether Claude's cache markers survive a provider's endpoint. Passing it lifts KS3's refusal for a Claude model, in a change of its own |
| **780** | On a real installation at the previous release: `fugaro update` copies the model keys into the bundle with the owner's own access, drops the mounts after the image rebuild and retires the containers, with a run in flight that finishes (U1, U3) |
| **781** | A non-Claude provider model served through the service end to end, charged the provider's reported cost, with no key in the job |
| **782** | A budget-off run through the pass-through gateway: served, nothing charged, no key in the job |

## 9. After-deploy steps

`fugaro update`, and nothing else. It copies the model keys into the bundle, rebuilds the job
images behind its existing confirmation, and on the update that finds a repository's image rebuilt
it removes that repository's model mounts and containers. KS14's release note says what it prints
at each step.

## 10. Considered and declined

- **Everything behind Check 50** (the owner's first answer, replaced by D3000): the check measures
  nothing about a non-Claude model.
- **The owner re-entering each key, or keeping the per-repository containers** (D3001's other
  options): an operator step beyond `fugaro update`, or the Terraform this work is meant to remove.
- **Budget-off as an exception, or removing the mode** (D3002's other options).
- **Moving routing, the fee and the settle into the service**: they work in the gateway today and
  the service would need a second copy of the price table and the ledger. Only the last hop moves
  (R1).
- **Serving Vertex through the service**: there is no key to move (A2).
- **Changing how a pool fails over between provider members**: unchanged, at a stage boundary (F15).

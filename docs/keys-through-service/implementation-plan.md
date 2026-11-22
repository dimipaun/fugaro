# Every model key through the credentials service: implementation plan

**Status: plan only. Nothing is implemented and no task is approved; each task needs the owner's
explicit go by name.** It builds [spec.md](spec.md) as amended by
[reconciliation.md](reconciliation.md), which wins where the two disagree.

The answers that shaped it: only a Claude model through a provider key waits for Check 50 (D3000),
and the owner runs Check 50 before release A is cut (D3013);
every model key lives in the project's one bundle and `fugaro update` copies the existing ones
there, one entry per repository (D3001, D3008); the service enforces the
policy's rules and the cap stays in the gateway (D3003); an old key secret is deleted once copied
(D3005, not the recommendation); only a project owner copies and sets keys (D3006); no check ties
a provider key to a repository's auth mode (D3007, the owner's own answer); budget mode off is
removed, `observe` stays, and the removal is a spec of its own (D3009 to D3011, which withdraw
D3002 and D3004). The owner's ruling D2975 reverses D1634 and D1670.

**It ships in two releases** (A4): release A serves every model key through the service with the
mounts still there (C1, KS1 to KS5, KS12, KS19, KS20, I1); release B drops the mounts
and deletes the old secrets (KS11, KS13 to KS16). KS13 is not launched before release A is cut.
**Release A is not cut before the owner has run Check 50 and KS20 has applied its result**
(D3013): a Claude model through a provider works on the mounted key today, and release A would
otherwise stop it.

**Three tasks wait on another plan.** KS13 is not launched before
[model credentials routing](../model-credentials-routing/README.md)'s release 1 is integrated:
KS6, KS9 and KS10, which it waited on, are withdrawn and replaced by credentials belong to models'
CM16 and CM15, CM6, and CM9 (F48, F49), and those tasks are themselves withdrawn with every task
of that plan that is not merged (2026-11-22, that plan's D3170), so the wait is on model
credentials routing's I1 (its A1). KS14 and KS15 are not launched before the plan that
removes budget mode off has merged its removal (R13). Until then a budget-off repository keeps its
mounted keys. **KS6, KS9 and KS10 are withdrawn, and KS7 and KS8 are dropped** with D3002 and
D3004 (F48); their numbers are not reused.

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
  for an older deployed service (F22). "A project owner" is always KS2's one probe (F36), never a
  second test of it.
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
| A repository is never served another repository's Anthropic key | KS3 `TestARepositoryIsNeverServedAnotherRepositorysAnthropicKey` |
| The provider's address is the bundle entry's, never the caller's | KS4 `TestTheUpstreamIsTheEntrysNotTheCallers` |
| No subscription token is in the agent's environment where the job carries the served fact (KS10 is withdrawn, F48) | CM9 `TestNoTokenInTheAgentsEnvironmentWhereServed`, `TestTheTokenStaysWhereNotServed` |
| Every deletion of a secret leaves an audit line in the step's record | KS15 `TestEachDeletionIsRecorded` |
| The fee, the price check and the provider-cost settle are the gateway's and are unchanged | KS5 `TestARoutedCallThroughTheServiceCostsWhatItCostBefore` |
| Between the releases a run whose job does not carry the fact still uses its mount | KS5 `TestARunWithoutTheFactUsesItsMount` |
| Release A turns the switch off for no repository | KS19 `TestTheSwitchOfASwitchedRepositoryIsUnchanged` |
| No provider key is entered under a name the deployed service reads as an Anthropic key | C1 `TestAProviderEntryIsNeverAnAnthropicKeyName` |
| A craft whose credential is not served is refused at launch with the fix (KS6 is withdrawn, F48) | CM15 `TestACraftWhoseCredentialIsNotServedIsRefusedAtLaunch` |
| A budget-off repository, and an installation with no database, keep their mounts and are told so | KS13 `TestABudgetOffRepositoryKeepsItsMounts`, `TestNoDatabaseKeepsItsMounts` |
| The copy prints no value, runs only for a project owner, overwrites nothing silently, is idempotent and follows a rotation | KS12 `TestTheCopyPrintsNoValue`, `TestAnOperatorsUpdateSkipsTheCopy`, `TestTheCopyNeverOverwritesAnEntryItDidNotWrite`, `TestTheCopyTwiceChangesNothing`, `TestTheCopyFollowsANewerVersion` |
| No named provider entry is written for a deployed service that cannot read it | KS2 `TestAnOlderServiceIsNotWrittenANamedEntry`; KS12 `TestTheCopyRunsAfterTheServiceIsDeployed` |
| A mount is dropped only where the bundle holds every key the repository needs | KS13 `TestAMountGoesOnlyWhenTheBundleIsReady` |
| A launch on an image older than release A is refused with the fix | KS13 `TestAnOlderImageIsRefusedAtLaunch` |
| An update never takes the last mount from a repository whose keys were not copied: it stops first and changes nothing | KS14 `TestAnUpdateStopsBeforeTerraformWhileARepositoryIsNotReady`, `TestTheStoppedUpdateChangesNothing` |
| Only a copied, unmounted model container is ever deleted, and only by a project owner's update | KS15 `TestOnlyACopiedUnmountedModelContainerIsDeleted`, `TestAnOperatorsUpdateDeletesNothing`, `TestANonModelSecretIsNeverDeleted` |
| `fugaro update` converges an installation at the previous release with a run in flight | KS13 `TestUpdateConvergesTheMountsFromThePreviousRelease`; KS15 `TestUpdateDeletesTheOldSecretsWithARunInFlight` |

## 3. The contract

### C1: the provider wire
- Acceptance: one place declares how a routed call names the provider it is for (a header and the
  route's path, carrying the provider's name and never an entry's), the typed refusals the service
  answers with, the bundle's entry for a named provider credential with the rule that names it
  from a repository and a provider (R2, U2), and the accessor the service reads it through. The
  entry's name is of a new family and can never equal a `credential-<name>-key` (F33). The same
  rule names a repository's own Anthropic key (D3008); an account's OAuth token keeps its entry by
  account name, as today (D3015). An entry the copy writes carries where it came from (the
  container and its version), so "an entry the copy did not write" is one with no such marker.
  The entry holds the provider's address, and the wire has no field for one. The contract also
  names R12's fact ("provider keys are served"): the job entry a run reads it from, and the
  document says where it is recorded (U6). **C1's second fact, "the stage account is served"
  (F44), is not needed** (F50): one fact per repository, "its credentials are served", covers it,
  which is credentials belong to models' C1.
  The names the gateway needs live in a gateway file, because the gateway cannot import the
  service (F26). A stub accessor answers "no such credential".
- Files: `internal/gateway/providerwire.go` (new), `internal/gateway/providerwire_test.go` (new),
  `internal/credsvc/provider_wire.go` (new), `internal/credsvc/provider_wire_stub.go` (new, stub),
  `internal/config/provider_served.go` (new, the fact's names),
  `docs/keys-through-service/contract-provider.md` (new).
- Interfaces: produces the names, the refusal types, the entry's naming rule, the accessor's
  signature and the fact's names; consumed by KS2, KS3, KS4, KS5, KS12, KS19. KS1 builds the
  accessor's other side.
- Tests: `TestStubAccessorHoldsNothing`, `TestAnEntryNameCarriesItsRepository`,
  `TestAProviderEntryIsNeverAnAnthropicKeyName`, `TestProviderWireNamesAreDistinct` (in the gateway).
- Command: `go test ./internal/credsvc/ -run 'StubAccessor|AnEntryNameCarries|AProviderEntryIsNever' -count=1 && go test ./internal/gateway/ -run ProviderWireNames -count=1`.
- Red step: two repositories with one provider name produce one entry name.
- Depends on: none.
- Commit: `credsvc: the provider wire contract (C1, R1, R2)`.
- Reviewer tier: opus, mandatory independent review.

## 4. Slices and tasks

**Release A. Slice 1, the service serves a named provider key:** KS1 to KS4.
**Slice 2, the run asks for it:** KS5, KS19. **Slice 3, the keys move:** KS12. **Integration:** I1.
**Release B. Slice 5, the mounts and the old secrets go:** KS13, KS11, KS14 to KS16.
**Checks and documents:** KS17, KS18.

**KS6, KS9 and KS10 are withdrawn** (F48), and their sections are gone from this plan and their
nodes from its graph. They planned a check by a repository's auth mode and a stage account served
beside it, and the plan this one is amended by — credentials belong to models — retires the auth
mode itself: **KS6 is replaced by that plan's CM16 and CM15**, **KS9 by its CM6**, **KS10 by its
CM9**, and **KS13 waits on its I1 in their place** (F49). **Amended 2026-11-22:** credentials
belong to models is itself superseded in part by
[model credentials routing](../model-credentials-routing/README.md) (its D3170), which withdraws
the CM16, CM15, CM6 and CM9 that replaced them, with every other task of that plan that is not
merged, so **KS13 now waits on model credentials routing's I1** (its A1).

### KS1: named provider entries in the bundle
- Acceptance: the bundle decodes and serves one key per repository and named provider credential,
  with its base address, its auth form and the copy's source marker, beside today's entries, and
  one entry per repository for its own Anthropic key (D3008); account tokens are unchanged
  (D3015); `Bundle.Credentials` offers a coding caller the
  provider entries of its own repository; a bundle of today's shape still loads; the schema and
  the loader agree; a bundle at the stated worst case, counted as repositories times credentials,
  stays under the secret's size limit (U5).
- Files: `internal/credsvc/bundle.go`, `schemas/creds-bundle.schema.json`,
  `internal/credsvc/bundle_provider_test.go` (new).
- Interfaces: builds C1's other side (the real accessor).
- Tests: `TestBundleServesANamedProviderEntry`, `TestBundleOffersACallerItsOwnRepositorysEntries`,
  `TestTodaysBundleStillLoads`, `TestBundleSchemaMatchesTheLoader`, `TestAWorstCaseBundleFits`.
- Command: `go test ./internal/credsvc/ -run 'BundleServesANamed|BundleOffersACaller|TodaysBundleStill|BundleSchemaMatches|AWorstCaseBundle' -count=1`.
- Red step: a named provider entry is refused as an unknown key.
- Depends on: C1 (it starts after C1 merges).
- Commit: `credsvc: the bundle holds named provider keys (KS1, F3, R2)`.
- Reviewer tier: opus, mandatory independent review.

### KS2: every writer of a provider key writes the bundle
- Acceptance: `fugaro creds set` accepts the key of an `openrouter` credential and writes it under
  C1's name for each repository named, several in one command (O8); `fugaro accounts add --type
  openrouter` and `fugaro secrets set` of a provider's secret write the bundle and no container;
  each goes through the guard for an older service, with a new since-constant; each reads the
  value from stdin only and asks nothing. One probe answers "is this person a project owner"
  (`testIamPermissions` on the bundle's secret, F36), and each writer refuses anyone else with the
  fix, in the three ways `credsOwnerOnly` refuses today (D3006).
- Files: `internal/cli/creds.go`, `internal/cli/accounts_add.go`, `internal/cli/secrets.go`,
  `internal/cli/initsecrets.go`, `internal/cli/recipes_skew.go`,
  `internal/cli/owner_probe.go` (new), `internal/cli/accounts_add_key_test.go`,
  `website/content/cli-reference.md`. It is past five files because a provider key has three
  writers (F27).
- Interfaces: consumes C1 (the entry's naming rule).
- Tests: `TestCredsSetAcceptsAnOpenRouterKey`, `TestAccountsAddOpenRouterWritesTheBundle`,
  `TestSecretsSetOfAProviderSecretWritesTheBundle`, `TestNoWriterCreatesAContainer`,
  `TestAnOlderServiceIsNotWrittenANamedEntry`, `TestOnlyAProjectOwnerWritesAProviderKey`.
- Command: `go test ./internal/cli/ -run 'CredsSetAcceptsAnOpenRouter|AccountsAddOpenRouter|SecretsSetOfAProvider|NoWriterCreatesAContainer|AnOlderServiceIsNotWritten|OnlyAProjectOwnerWrites|CliReferenceNames' -count=1`.
- Red step: `creds set` refuses the name with `credsKeyCredentialHalfRefusal`.
- Depends on: C1.
- Commit: `cli: a provider key goes to the bundle (KS2, D1670 reversed, D3006)`.
- Reviewer tier: opus, mandatory independent review.

### KS3: the gate, narrowed
- Acceptance: a coding caller is offered a named provider credential its policy admits, wherever
  the repository's access record names it — the clause F32 planned as "under every auth the
  service serves and not only `api-key`" is credentials belong to models' CM6, which narrows every
  call by that record (F51); selection
  refuses it for a Claude model with the refusal that names Check 50, and admits it for any other
  model; a repository with an Anthropic entry of its own is served that one, a repository with
  none the project's, and never another repository's (D3008); the single `provider-key` entry and
  the review path that serves it (`reviewOfferings`, `entryFor`) behave as today. It does not edit
  `bundle.go`: what it needs from the bundle is C1's accessor.
- Files: `internal/creds/select.go`, `internal/creds/select_test.go`,
  `internal/creds/select_provider_test.go` (new), `internal/credsvc/cascade.go`,
  `internal/credsvc/cascade_provider_test.go` (new).
- Interfaces: consumes C1 (the accessor and the refusal type).
- Tests: `TestAClaudeModelThroughAProviderWaitsForCheck50`, `TestANonClaudeProviderModelIsServed`,
  `TestAnOAuthRepositoryIsOfferedItsProviderCredential`,
  `TestARepositoryIsNeverServedAnotherRepositorysAnthropicKey`,
  `TestSelectRefusesReservedTierC` (moved to the Claude case).
- Command: `go test ./internal/creds/ -run 'AClaudeModelThrough|ANonClaudeProvider|SelectRefusesReserved' -count=1 && go test ./internal/credsvc/ -run 'Cascade|AnOAuthRepositoryIsOffered|ARepositoryIsNeverServed' -count=1`.
- Red step: a non-Claude model is refused with the Check 50 text.
- Depends on: C1.
- Commit: `creds: only a Claude model through a provider waits for Check 50 (KS3, D3000, F5)`.
- Reviewer tier: opus, mandatory independent review.

### KS4: the service's provider route takes a provider's name
- Acceptance: the provider route reads the provider's name from the call, derives the bundle entry
  from the run's verified repository, builds the provider routes from the published policy and
  refuses a call its model and provider rules do not allow, whoever built the request (R11); it
  then attaches that key and forwards to the address the entry holds and never one the call
  carries, streaming both ways and holding nothing, the body untouched. `SECURITY.md` states that a hand-built call escapes the cap (D3003).
- Files: `internal/credsvc/forward.go`, `internal/credsvc/request.go`, `internal/credsvc/server.go`,
  `internal/credsvc/forward_provider_test.go` (new), `SECURITY.md` with
  `website/content/security.md` and the test that pins the sentence.
- Interfaces: consumes C1.
- Tests: `TestAHandBuiltCallOutsideThePolicyIsRefused`, `TestAnHonestRoutedModelIsNotRefusedAsUnpinned`,
  `TestTheEntryIsDerivedFromTheVerifiedRepository`, `TestTheNamedKeyIsAttachedAndTheBodyUntouched`,
  `TestTheUpstreamIsTheEntrysNotTheCallers`,
  the existing `TestOpenRouterCallSwapsKeyAndStreams` and `TestOpenRouterKeyNeverReachesJob` unchanged.
- Command: `go test ./internal/credsvc/ -run 'AHandBuiltCall|AnHonestRoutedModel|TheEntryIsDerived|TheNamedKeyIsAttached|TheUpstreamIsTheEntrys|OpenRouter' -count=1 && go test ./internal/cli/ -run SecurityMd -count=1 && (cd website && npm run check)`.
- Red step: the route forwards with the single `provider-key` whatever provider is asked.
- Depends on: C1; KS3 (kept: KS4 calls the selection KS3 changes).
- Commit: `credsvc: the provider route serves a named provider under the policy's rules (KS4, R1, R11, D3003)`.
- Reviewer tier: opus, mandatory independent review.

### KS5: a provider route goes through the service
- Acceptance: for a run whose job carries R12's fact the gateway sends a routed call to the
  service's provider route with the provider's name, keeping its own routing, fee, price check and
  settle, and taking the service's refusals, its unreachable halt and its retry as a service call
  does today; a run whose job does not carry it uses its mounted key as today; the runner reads a
  provider key from its environment only on that second path, and a pool's member counts as
  available by the fact where the job carries it, not by a key in the environment.
- Files: `internal/gateway/route.go`, `internal/gateway/forward.go`, `internal/gateway/service.go`,
  `internal/runner/providers.go`, `internal/runner/providerkey.go`,
  `internal/runner/poolchoice.go`, `internal/gateway/route_service_test.go` (new),
  `internal/runner/providers_fact_test.go` (new).
- Interfaces: consumes C1.
- Tests: `TestARoutedCallGoesThroughTheService`, `TestARoutedCallThroughTheServiceCostsWhatItCostBefore`,
  `TestARunWithoutTheFactUsesItsMount`, `TestAServiceRefusalOnARouteHaltsAsAServiceCallDoes`.
- Command: `go test ./internal/gateway/ -run 'ARoutedCall|AServiceRefusalOnARoute' -count=1 && go test ./internal/runner/ -run 'Provider|ARunWithoutTheFact' -count=1`.
- Red step: `validateRoutes` refuses the route with `errServiceOnRoute`.
- Depends on: C1 (the wire and the fact's name; KS19 records the fact, an edge C1 broke).
- Commit: `gateway: a provider route is served through the credentials service (KS5, D1634 reversed, R1, R12)`.
- Reviewer tier: opus, mandatory independent review.

### KS11: the serving path of a provider credential flips (release B)
- Acceptance: `config.CredentialServingPath` answers the service for `openrouter`, and every
  reader follows: the accounts commands, the craft and pool code, the service's pool and bundle,
  the policy migration, and `runnerKeyCredentialName`, which stops computing a provider mount
  (F29); a pool's failover between provider members is unchanged, at a stage boundary (F24);
  Vertex still answers the runner.
- Files: `internal/config/stage_accounts.go`, `internal/config/craft.go`,
  `internal/craft/pathpool.go`, `internal/credsvc/pool.go`, `internal/credsvc/bundle.go`,
  `internal/projectpolicy/migrate.go`, `internal/infra/spec.go`, `internal/cli/accounts.go`,
  `internal/cli/accounts_add.go`, `internal/cli/accounts_add_converge.go`,
  `internal/cli/accounts_pool.go`, `internal/cli/creds.go`, `internal/cli/recipes_skew.go`. It is
  past five files because one constant has that many readers; the change in each is one arm.
- Interfaces: none new.
- Tests: `TestAnOpenRouterCredentialIsServedByTheService`, `TestAPoolStillFailsOverAtAStageBoundary`,
  `TestVertexStaysOnTheRunner`.
- Command: `go test ./internal/config/ ./internal/craft/ -run 'AnOpenRouterCredentialIsServed|APoolStillFailsOver|VertexStays' -count=1 && go test ./internal/credsvc/ -run Pool -count=1 && go test ./internal/cli/ -run 'Accounts' -count=1`.
- Red step: the path of an `openrouter` credential is the runner's.
- Depends on: KS13 (kept, behaviour and a shared file: the path may say "service" only once no
  mount is computed from it, and both edit `infra/spec.go` and `recipes_skew.go`); KS2 (kept: both
  edit `creds.go` and `accounts_add.go`); KS1 (kept: both edit `bundle.go`).
- Commit: `config: a provider credential is served by the service (KS11, D1634 reversed, F24)`.
- Reviewer tier: opus, mandatory independent review.

### KS12: `fugaro update` copies the keys into the bundle
- Acceptance: one new stage, after the stage that deploys the service, reads for each model key
  the installation holds outside the bundle its newest version and writes it into the bundle under
  C1's name, through the guard for an older service, in memory, printing no value: a provider key
  once for each repository that uses it, and a repository's own Anthropic key or OAuth token under
  that repository's entry (D3008); it is idempotent; when a container holds a newer version than
  the one an entry's marker names, it copies again and prints one line naming the container
  (D3014); an entry with no marker that differs is left alone and named in one line; a provider
  entry takes its address from the provider's declaration in the owner's config;
  a person who is not a project owner (KS2's probe) gets the stage skipped with a line saying who
  must run it.
- Files: `internal/cli/update_keys.go` (new), `internal/cli/update_keys_test.go` (new),
  `internal/cli/init_stages.go`, `internal/initflow/stage.go`,
  `internal/cli/setup_refusals_test.go`, `internal/apply/steps.go`,
  `internal/credsvc/dynamicsecrets.go` (its comment says no value is ever copied between the
  stores, which stops being true). It is past five files for the stage's censuses.
- Interfaces: consumes C1 (the entry's naming rule).
- Tests: `TestTheCopyWritesEveryModelKey`, `TestTheCopyPrintsNoValue`,
  `TestAnOperatorsUpdateSkipsTheCopy`, `TestTheCopyNeverOverwritesAnEntryItDidNotWrite`,
  `TestTheCopyTwiceChangesNothing`, `TestTheCopyFollowsANewerVersion`,
  `TestTheCopyRunsAfterTheServiceIsDeployed`, `TestASharedKeyIsCopiedOncePerRepository`,
  `TestTheCopyNamesAContainerWhoseNewerVersionItCopied`.
- Command: `go test ./internal/cli/ -run 'TheCopy|AnOperatorsUpdateSkips|ASharedKeyIsCopied|RefusalMatrix' -count=1 && go test ./internal/apply/ -run StepsMatches -count=1`.
- Red step: after an update the bundle lacks a key that a repository's container holds.
- Depends on: C1; KS2 (kept: it uses the since-constant, the guard and the owner probe KS2 adds).
- Commit: `update: the model keys are copied into the bundle (KS12, D3001, D3006, R4)`.
- Reviewer tier: opus, mandatory independent review.

### KS19: the fact that a repository's provider keys are served
- Acceptance: the owner's `fugaro update` reads, beside today's switch, whether the bundle holds an
  entry for every provider credential the repository's config names, whether the deployed service
  can read that entry kind, and whether the project publishes an allow-list (F32); it records the
  answer on the repository's entry in the local config, under a new field, and the job is planned
  with C1's entry from that record; C1's second fact, "the stage account is served" (F44), is not
  recorded, because it is not needed (F50); a run of another person, or a failed read, leaves the record
  as it was, as the switch does; today's `ready` and the switch of every repository are unchanged
  (F30); `doctor` reports the fact where it reports the switch.
- Files: `internal/cli/init_credentials.go`, `internal/localcfg/localcfg.go`,
  `internal/infra/spec.go`, `internal/cli/doctor_brokering.go`,
  `internal/cli/init_provider_served_test.go` (new),
  `internal/infra/spec_provider_served_test.go` (new).
- Interfaces: consumes C1 (the fact's names); builds the side KS5 reads.
- Tests: `TestTheFactIsRecordedWhenEveryProviderEntryIsThere`, `TestTheFactNeedsAnAllowList`,
  `TestAnOperatorsRunLeavesTheFactAsItWas`, `TestTheSwitchOfASwitchedRepositoryIsUnchanged`,
  `TestTheJobCarriesTheFactFromTheRecord`.
- Command: `go test ./internal/cli/ -run 'TheFactIsRecorded|TheFactNeedsAnAllowList|AnOperatorsRunLeavesTheFact|TheSwitchOfASwitchedRepository' -count=1 && go test ./internal/infra/ -run TheJobCarriesTheFact -count=1`.
- Red step: a repository whose bundle holds every provider entry has a job with no such entry.
- Depends on: C1.
- Commit: `update: record that a repository's provider keys are served (KS19, R12, F31)`.
- Reviewer tier: opus, mandatory independent review.

### KS13: release B: no model mount where the bundle is ready
- Acceptance: `credentialsBundleServes` comes to require R12's fact in full (every provider
  entry, a service that reads them, a published allow-list), so no mount goes where the gateway
  would still fall back to it (R10); for a
  repository that is ready, `SecretMounts` yields no model mount of any kind, the
  stage account's included; a repository that is not ready, a repository in budget mode off and an
  installation with no database keep their mounts and are told why in one line (R13); a launch on a job image older than release A is refused with the command
  that rebuilds it; an installation at the previous release converges through `fugaro update`. The
  pinned sentences that say a provider key the job mounts stays its own are changed with their
  tests.
- Files: `internal/infra/spec.go`, `internal/infra/readiness.go`, `internal/infra/spec_test.go`,
  `internal/cli/init_credentials.go`, `internal/cli/recipes_skew.go`,
  `internal/cli/update_converge_test.go`, `internal/cli/update_in_flight.go` with
  `docs/design/update-in-flight.md` and the tests that count its lines, `SECURITY.md` with
  `website/content/security.md`, `docs/multi-model.md`, `docs/credentials-service/runbook.md` and
  the tests that pin them. It is past five files for those pins.
- Interfaces: none new.
- Tests: `TestAMountGoesOnlyWhenTheBundleIsReady`, `TestABundleMissingAProviderEntryIsNotReady`,
  `TestABudgetOffRepositoryKeepsItsMounts`, `TestNoDatabaseKeepsItsMounts`,
  `TestAnOlderImageIsRefusedAtLaunch`,
  `TestUpdateConvergesTheMountsFromThePreviousRelease`; the existing
  `TestFilledServiceServedAuthsKeepsProviderKeys` and
  `TestSwitchedMixedRepositoryKeepsItsStageAccountToken` are reversed.
- Command: `go test ./internal/infra/ -run 'AMountGoesOnlyWhen|ABudgetOffRepositoryKeeps|NoDatabaseKeeps|FilledServiceServedAuths|SwitchedMixedRepository' -count=1 && go test ./internal/cli/ -run 'ABundleMissingAProviderEntry|AnOlderImageIsRefused|UpdateConvergesTheMounts|UpdateInFlight|SecurityMd|DocsMultiModel|DocsCredentialsRunbook' -count=1 && (cd website && npm run check)`.
- Red step: an `api-key` job still mounts its provider key.
- Depends on: KS5 and KS12 (kept, behaviour: a mount may go only once every path
  serves that credential and its value is in the bundle); KS19 (kept: both edit `infra/spec.go`
  and `init_credentials.go`); KS2 (kept: both edit `recipes_skew.go`). **Not launched before
  release A is cut, and not before model credentials routing's I1 has merged** (F49, amended
  2026-11-22 by its D3170): KS6, KS9 and KS10 are withdrawn, replaced by credentials belong to
  models' CM16 and CM15, CM6, and CM9, and those tasks are themselves withdrawn with every task of
  that plan that is not merged and carried by model credentials routing's tasks, so a mount may go
  only once their work is in the tree, at that plan's I1.
- Commit: `infra: no model key is mounted where the bundle is ready (KS13, R5, R10)`.
- Reviewer tier: opus, mandatory independent review.

### KS14: the workflow module loses its model mounts
- Acceptance: `modules/workflow` holds no model env entry, no job accessor on a model secret and
  no service accessor on one; `var.model_secrets_served` and its locals are gone from the module,
  from `modules/repo` and from the repository root that pass it down, with the Go constants and
  validation that mirror them, the Go that sets them and the tfvars and test fixtures that carry
  them. R12's fact stays as C1 wrote it, and the second fact of F44 is not recorded (F50);
  retiring it is a later cleanup. Because a repository
  would have no mount left to fall back to, an update stops before the Terraform step, changes
  nothing and says that a project owner must run `fugaro update` once (D3012) when, among the
  repositories this update applies, one has a deployed job that still mounts a model key and is
  not ready. A repository with no model mount, a Vertex one for instance, never stops it. The secret-readers list and its test match.
- Files: `deploy/terraform/gcp/modules/workflow/job.tf`, `sa.tf` and `variables.tf` beside it,
  `deploy/terraform/gcp/modules/repo/workflows.tf` and `variables.tf`,
  `deploy/terraform/gcp/roots/repo/variables.tf`, the root's `tests/repo.tftest.hcl` and
  `tests/testdata/*.tfvars.json`, the golden plans, `internal/infra/tfvars.go`, `spec.go` and
  `readiness.go`, `internal/config/providers.go`, `internal/cli/update_mounts_gate.go` (new,
  D3012's stop) with its test, `internal/cli/setup_refusals_test.go`,
  `deploy/terraform/terraform_test.go`, `deploy/terraform/proxied_git_test.go`,
  `deploy/terraform/secret_readers_test.go` with `docs/secrets-brokering/secret-readers.md`. It is
  past five files because one variable is declared at three levels.
- Interfaces: none.
- Tests: `TestWorkflowModuleMountsNoModelSecret`, `TestSecretReadersDocMatchesTheTable`,
  `TestAnUpdateStopsBeforeTerraformWhileARepositoryIsNotReady`,
  `TestTheStoppedUpdateChangesNothing`, `TestARepositoryWithNoModelMountNeverStopsTheUpdate`;
  `TestServiceHeldSecretsMatchTheCredentialsService` and `TestModelSecretsServedValidationMatchesGo`
  are removed with what they held.
- Command: `go test ./deploy/terraform/ -count=1 && go test ./internal/infra/ -run Tfvars -count=1 && go test ./internal/cli/ -run 'AnUpdateStopsBeforeTerraform|TheStoppedUpdateChanges|ARepositoryWithNoModelMount|RefusalMatrix' -count=1 && terraform fmt -check -recursive deploy/terraform`, and every check `.claude/rules/terraform.md` lists for a module change: each root's init, validate and `terraform test`, `scripts/gen-golden-plans.sh`, and `go test -tags terraform ./internal/infra/...`.
- Red step: the module still declares the model env entries.
- Depends on: KS13 (kept: the variable goes only after nothing sets it); KS11 (kept, behaviour:
  no mount is computed from the serving path). **Not launched before the plan that removes budget
  mode off has merged its removal (R13).**
- Commit: `terraform: the workflow module mounts no model key (KS14, F9)`.
- Reviewer tier: opus, mandatory independent review.

### KS15: the old model secrets are deleted
- Acceptance: for a per-repository model container whose newest value is in the bundle and that no
  job mounts, a project owner's `fugaro update` (KS2's probe) works in R7's order: after the apply,
  it takes the container and its operator grants out of Terraform's state and then deletes the
  container through the API; discovery does not bring a deleted container back; no `.tf` file
  changes (F35); each deletion writes one line to the step's audit record, naming the container
  and never its value, since a deletion after a state removal passes no Terraform plan check; an
  operator's update deletes nothing and says so; no other secret is ever deleted; with a run in flight the update names the deletion in one
  short line and the run finishes. The retained-types note says which class the ruling of
  2026-11-06 no longer covers, and why (D3005).
- Files: `internal/infra/retire.go`, `internal/infra/tf/retire.go`, `internal/infra/discover.go`,
  `internal/cli/init_retire.go`, `internal/cli/init_retire_test.go`,
  `internal/cli/update_delete_keys_test.go` (new), `internal/cli/update_converge_test.go`,
  `internal/cli/update_in_flight.go` with `docs/design/update-in-flight.md`,
  `docs/config-transitions/spec.md`, and the operator line of the release notes that carry it.
  It is past five files because a deletion is state, API and discovery together.
- Interfaces: none.
- Tests: `TestOnlyACopiedUnmountedModelContainerIsDeleted`, `TestAnOperatorsUpdateDeletesNothing`,
  `TestANonModelSecretIsNeverDeleted`, `TestUpdateDeletesTheOldSecretsWithARunInFlight`,
  `TestTheGrantsLeaveStateBeforeTheContainerIsDeleted`, `TestDiscoveryDoesNotBringADeletedContainerBack`,
  `TestEachDeletionIsRecorded`.
- Command: `go test ./internal/cli/ -run 'OnlyACopiedUnmounted|AnOperatorsUpdateDeletes|ANonModelSecret|UpdateDeletesTheOldSecrets|TheGrantsLeaveState|EachDeletionIsRecorded|UpdateInFlight|Retire|Docs' -count=1 && go test ./internal/infra/... -run 'Retir|DiscoveryDoesNotBring' -count=1`.
- Red step: the apply after a deletion fails on a grant whose container is gone.
- Depends on: KS14 (kept: a container goes only after its accessors); KS12 (kept: the copy must
  exist first). It waits on the other plan with KS14 (R13).
- Commit: `update: a repository's old model secrets are deleted once copied (KS15, D3005, R7)`.
- Reviewer tier: opus, mandatory independent review.

### KS16: shared project jobs
- Acceptance: after secrets brokering's B5.7 has dropped a shared job's mounted token, a provider
  key a shared job mounted as a profile secret is served through the service like any other, and
  the project-wide model containers follow KS15's rule. **This task waits on B5.7, which is another
  plan's and is not started.**
- Files: `deploy/terraform/gcp/modules/project/job.tf`, `internal/infra/projectjobs.go`,
  `internal/infra/retire.go`, `internal/cli/update_delete_project_keys_test.go` (new),
  `deploy/terraform/terraform_test.go`.
- Interfaces: none.
- Tests: `TestASharedJobMountsNoProviderKey`, `TestAProjectModelContainerIsDeletedOnlyAfterItsCopy`.
- Command: `go test ./deploy/terraform/ -count=1 && go test ./internal/infra/ -run ProjectJob -count=1 && go test ./internal/cli/ -run AProjectModelContainer -count=1`, and the checks `.claude/rules/terraform.md` lists for a module change, the installation root's included.
- Red step: the shared job still mounts the profile's provider key.
- Depends on: KS15 (kept: the same deletion, and both edit `retire.go`); KS14 (kept: both edit
  `terraform_test.go`); secrets brokering's B5.7 (external).
- Commit: `terraform: shared project jobs mount no model key (KS16, F27)`.
- Reviewer tier: opus, mandatory independent review.

### KS17: the live checks, as scripts
- Acceptance: Check 50 and Checks 780 and 781 exist as one command each under
  `scripts/live-checks/`, each with its hermetic self-test and its row in
  `docs/gcp-live-checklist.md`; Check 50's row keeps the facts its documents test pins; none is run
  by an agent.
- Files: `scripts/live-checks/check50.sh`, `check780.sh`, `check781.sh` (new) with
  their `_test.sh`, `scripts/live_checks_test.go` (its table of scripts),
  `docs/gcp-live-checklist.md`.
- Interfaces: none.
- Tests: the three `_test.sh`, `TestLiveCheckScripts`, `TestChecklistHasCheck50`.
- Command: `for n in 50 780 781; do bash scripts/live-checks/check${n}_test.sh; done && go test ./scripts/ -run 'TestLiveCheckScripts|TestLiveCheckCIShard' -count=1 && go test ./internal/cli/ -run 'ChecklistHasCheck50|Docs' -count=1`.
- Red step: each self-test fails on a missing script.
- Depends on: none.
- Commit: `checks: 50, 780 and 781 as scripts (KS17, F13)`.
- Reviewer tier: standard.
- Owner-run: `bash scripts/live-checks/check50.sh` and the two like it. No agent performs any
  step of them.

### KS18: the documents and the operator notes
- Acceptance: the model-crafts documents mark D1634 and D1670 reversed by D2975;
  `docs/workspace-credentials/spec.md` says the gate is narrowed (O1); the rules file and
  `docs/release.md` no longer describe a runner path for provider keys; each release's
  `### For operators` is "run `fugaro update`" and what it will say: in release A that a project
  owner's update copies the model keys into the bundle, in release B that it removes the mounts of
  every ready repository, that a budget-off repository keeps its own, and that a run on an image
  older than release A is refused with the rebuild command. KS15 writes its own operator line,
  in its own change.
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

### KS20: Check 50's result is applied
- Acceptance: after the owner has run Check 50, its row in the checklist records the result. If it
  passed, KS3's refusal for a Claude model through a provider is removed and `doctor`'s warning
  that the cost is unmeasured goes with it. If it failed, the refusal stays, and `doctor` and
  `fugaro update` name each repository that pins a Claude model through a provider, with the fix,
  before release A stops it. **Release A is not cut before this task has merged** (D3013).
- Files: `internal/creds/select.go`, `internal/creds/select_provider_test.go`,
  `internal/cli/doctor.go`, `docs/gcp-live-checklist.md`.
- Interfaces: none.
- Tests: `TestCheck50sResultDecidesTheClaudeGate`, `TestDoctorNamesARepositoryTheGateWillStop`.
- Command: `go test ./internal/creds/ -run 'Check50sResult|AClaudeModelThrough' -count=1 && go test ./internal/cli/ -run 'DoctorNamesARepositoryTheGate|ChecklistHasCheck50|Docs' -count=1`.
- Red step: the checklist row says not run while the code treats the gate as decided.
- Depends on: KS3 (kept: it edits the refusal KS3 narrows); KS17 (kept: it records the result of
  the script KS17 writes); the owner's run of Check 50 (external).
- Commit: `creds: apply Check 50's result to the Claude-through-a-provider gate (KS20, D3013)`.
- Reviewer tier: opus, mandatory independent review.

### I1: integration
- Acceptance: the stub `provider_wire_stub.go` is deleted and the service is wired to the bundle's
  real accessor; one hermetic end-to-end test drives a run of a repository whose provider keys are
  served with a non-Claude provider coder through its gateway to the service and a
  fake provider: the call is served and charged what it cost, with the key copied by the update's
  stage and none read from the job's environment; the same run with a Claude model through the
  provider does what Check 50's result decided (KS20).
- Files: `internal/credsvc/provider_wire_stub.go` (deleted),
  `internal/credsvc/provider_wire_bundle.go` (new, the rewiring),
  `internal/cli/keys_end_to_end_test.go` (new).
- Interfaces: consumes every side of C1.
- Tests: `TestAProviderModelIsServedEndToEndFromACopiedKey`,
  `TestAClaudeModelThroughAProviderEndToEnd`.
- Command: `go test ./internal/cli/ -run 'AProviderModelIsServedEndToEnd|AClaudeModelThroughAProviderEndToEnd' -count=1 && go build ./... && go vet ./...`.
- Red step: the test fails to build while the stub is still referenced.
- Depends on: C1 and every side of an edge it broke: KS1, KS2, KS3, KS4, KS5, KS12, KS19; KS20
  (kept, behaviour: its second test asserts the result KS20 applied).
- Commit: `keys: a provider model served end to end (I1)`.
- Reviewer tier: opus, mandatory independent review.

## 5. Parallel execution

The decoupling pass is on (`planning.decoupling` is `true` for this repository), and the
repository's mode is `startup`.

**Levels.** Tasks in one level run at once. **A launcher follows this table.**

| Level | Tasks |
| --- | --- |
| 0 | C1, KS17 |
| 1 | KS1, KS2, KS3, KS5, KS19 |
| 2 | KS4, KS12, KS20 (after the owner has run Check 50) |
| 3 | KS13 (after release A is cut and model credentials routing's I1), I1 |
| 4 | KS11, KS18 |
| 5 | KS14 (after the plan that removes budget mode off) |
| 6 | KS15 |
| 7 | KS16 (after B5.7) |

**Critical path.** The graph read reports `critical path (8 tasks): C1 -> KS2 -> KS12 -> KS13 -> KS11 -> KS14 -> KS15 -> KS16`,
with C1 the bottleneck, and `gain: 6 broken, 18 kept, 3 added; before 9 tasks, weight 22; after 8 tasks, weight 17.5; improves`.
The last five are a true sequence: a mount, the path that computes it, its Terraform and its
secret each go only after the one before.

**Where the graph and this table differ.** The graph format records a contract's consumers only,
so KS1 and KS19, which build C1's other sides, carry no edge to C1 in the graph and the graph
reads them as ready at once, in level 0 with C1. This table does not: each starts after C1 merges,
so it puts them in level 1, beside KS2, KS3 and KS5, which wait only on C1. The graph also cannot
hold the five outside waits: the owner's run of Check 50 before KS20, the release before KS13,
model credentials routing's I1 before KS13 (F49, amended 2026-11-22), the other plan before KS14,
and B5.7 before KS16.

**Edges broken by C1 and rejoined in I1.** KS2, KS3, KS4 and KS12 after KS1: each needs the entry's
naming rule and the accessor's signature, not the bundle's code. KS5 after KS4: the gateway needs
the wire, not the route's code. KS5 after KS19: the gateway needs the fact's name, not the code
that records it.

**Edges kept.** Each is stated in its task: behaviour (KS11 after KS13; KS13 after KS5 and KS12;
KS14 after KS13 and KS11; KS15 after KS12 and KS14; KS16 after KS15; KS20 after KS17; I1 after
KS20), a shared file (KS4 and KS20 after KS3; KS11 after KS1 and KS2; KS12 after KS2; KS13 after
KS2 and KS19; KS16 after KS14), and something outside the plan (KS20 after the owner's run of
Check 50; KS13 after model credentials routing's I1; KS14 after the removal of budget mode off;
KS16 after B5.7).

**Shared files.** No level holds two writers of one file.

| File | Writers, by level |
| --- | --- |
| `internal/creds/select.go`, `select_provider_test.go` | KS3 (1), KS20 (2) |
| `internal/credsvc/cascade.go` | KS3 (1) only |
| `internal/credsvc/forward.go`, `request.go`, `server.go` | KS4 (2) |
| `internal/credsvc/bundle.go` | KS1 (1), KS11 (4) |
| `schemas/creds-bundle.schema.json` | KS1 (1) |
| `internal/credsvc/dynamicsecrets.go` | KS12 (2) |
| `internal/credsvc/pool.go`, `internal/projectpolicy/migrate.go` | KS11 (4) |
| `internal/gateway/route.go`, `forward.go`, `service.go`, `internal/runner/poolchoice.go` | KS5 (1) |
| `internal/config/providers.go` | KS14 (5) |
| `internal/config/stage_accounts.go`, `craft.go`, `internal/craft/pathpool.go` | KS11 (4) |
| `internal/cli/creds.go`, `accounts_add.go` | KS2 (1), KS11 (4) |
| `internal/cli/secrets.go`, `initsecrets.go` | KS2 (1) |
| `internal/cli/accounts.go`, `accounts_add_converge.go`, `accounts_pool.go` | KS11 (4) |
| `internal/cli/recipes_skew.go` | KS2 (1), KS13 (3), KS11 (4) |
| `internal/cli/init_stages.go`, `internal/initflow/stage.go`, `internal/apply/steps.go` | KS12 (2) |
| `internal/cli/setup_refusals_test.go` | KS12 (2), KS14 (5) |
| `internal/cli/init_credentials.go` | KS19 (1), KS13 (3) |
| `internal/localcfg/localcfg.go`, `internal/cli/doctor_brokering.go` | KS19 (1) |
| `internal/cli/doctor.go` | KS20 (2) |
| `internal/infra/spec.go` | KS19 (1), KS13 (3), KS11 (4), KS14 (5) |
| `internal/infra/readiness.go` | KS13 (3), KS14 (5) |
| `internal/infra/spec_test.go` | KS13 (3) |
| `internal/infra/tfvars.go` | KS14 (5) |
| `internal/infra/retire.go` | KS15 (6), KS16 (7) |
| `internal/cli/update_converge_test.go`, `update_in_flight.go`, `docs/design/update-in-flight.md` | KS13 (3), KS15 (6) |
| `deploy/terraform/terraform_test.go` | KS14 (5), KS16 (7) |
| `SECURITY.md`, `website/content/security.md` and their tests | KS4 (2), KS13 (3) |
| `docs/multi-model.md`, `docs/credentials-service/runbook.md` and their tests | KS13 (3) |
| `docs/gcp-live-checklist.md` | KS17 (0), KS20 (2) |
| `website/content/cli-reference.md` | KS2 (1) |
| the release notes files | KS18 (4), KS15 (6) |

R8 lists the files other plans also write, and how the plans are kept apart.

## 6. The estimate

As `fugaro plan graph --plan docs/keys-through-service/implementation-plan.md` printed it:

`ready in 555–2220 min, no history yet for 18 tasks (18 tasks; seeds for 18; from the seeds: no history is cached on this machine yet)`

It is informational and reserves no budget: it is what the tasks' tiers and this machine's history
predict, and it moves as runs finish. It does not count the owner's run of Check 50, the release
before KS13, the wait for model credentials routing's I1, the wait for the
plan that removes budget mode off, or the wait for B5.7. The graph counts one node more than this
plan's 17 runs: the contract change of §7, which runs only if raised.

## 7. Contract changes anticipated

- **R-entry-name** (C1, raised by KS12): if U2 shows a provider key's three sources cannot be named
  as C1 wrote the rule. Reaches KS1, KS2, KS4.

## 8. Checks the plan takes

From the reserved block 780–789 (780 and 781 used), and Check 50 written as a script. All owner-run (KS17).

| Check | What it measures |
| --- | --- |
| **50** | As its checklist row says: whether Claude's cache markers survive a provider's endpoint. **The owner runs it before release A is cut** (D3013), and KS20 applies the result |
| **780** | After the release that carries KS15, on a real installation that was at the release before A: a project owner's `fugaro update` copies the model keys, drops the mounts and deletes the old secrets, with a run in flight that finishes; an operator's update does none of it (U3) |
| **781** | After release A: a non-Claude provider model served through the service end to end, charged the provider's reported cost, with the key read from the bundle |

Check 782 (a budget-off run through a pass-through gateway) is withdrawn with D3002; its number is
not reused.

## 9. After-deploy steps

**Before release A is cut:** the owner runs Check 50 (`bash scripts/live-checks/check50.sh`), and
KS20 merges its result.

**Release A: `fugaro update`, run by a project owner.** It copies the model keys into the bundle.
An operator's update skips that step and says so; nothing breaks, because the mounts are still
there.

**Release B: `fugaro update`, run by a project owner.** It rebuilds the job images behind its
existing confirmation and removes the model mounts of every repository whose bundle is ready. A
run launched on an image older than release A is refused with the rebuild command. A repository
in budget mode off, and an installation with no budget database, keep their mounts (R13).

**The release that carries KS14 and KS15**, after the plan that removes budget mode off:
`fugaro update`. It deletes the old key secrets. Where a repository's keys were never copied, the
update stops before the Terraform step, changes nothing and says a project owner must run it
(D3012).

KS18's release notes say what each update prints.

## 10. Considered and declined

- **Everything behind Check 50** (the owner's first answer, replaced by D3000).
- **The owner re-entering each key, or keeping the per-repository containers** (D3001's other
  options).
- **Dropping the old secret from Terraform and keeping it** (the recommendation of D3005): the
  owner chose deletion.
- **Metering provider calls in the service** (D3003's other option): a second price table, ledger
  and fee logic.
- **A pass-through gateway for budget mode off, and an identity for a budget-off run** (D3002 and
  D3004, tasks KS7 and KS8): withdrawn by D3009. The mode is removed under its own spec (D3011).
- **Removing `observe` as well** (D3010's other option): a cap nobody chose would start refusing
  runs, and Vertex under `enforce` is refused today.
- **Applying KS14's Terraform and refusing launches, or letting operators copy** (D3012's other
  options).
- **Serving a Claude model through a provider with today's warning, or keeping those
  repositories on their mounts** (D3013's other options; the first was the recommendation).
- **Copying a key once and ignoring later versions, or removing operators' right to add a version
  in release A** (D3014's other options).
- **One bundle entry per repository for an account's OAuth token** (D3015's other option).
- **Narrowing the review path's provider entry** (`reviewOfferings`): it keeps the single
  `provider-key` entry, unchanged by this plan.
- **Retiring R12's fact once every run takes the service**: a later cleanup, since it is the
  contract's.
- **Flipping the serving path in release A** (the first draft of KS11): it drops the mounts
  release A relies on (F29).
- **Entering a provider key as `credential-<name>-key`** (the first draft of R2): the deployed
  service reads that name as an Anthropic key (F33).
- **Letting operators copy and set keys** (D3006's other option): it ends "operators never read a
  value".
- **Moving routing, the fee and the settle into the service**: only the last hop moves (R1).
- **Dropping a repository's mounts when its own image is rebuilt** (the first draft of R5): that
  fact is read at launch, per workflow, and a catalog image is not rebuilt by `fugaro update`. Two
  releases and a launch gate, as the earlier flips did.
- **Retiring `agent.auth` as a repository's setting** (D3007 read in full): it is its own spec,
  [credentials belong to models](../credentials-belong-to-models/README.md), which carries out O4.
- **A check by a repository's auth mode, and a stage account served beside it** (KS6, KS9 and
  KS10): withdrawn once that plan retired the auth mode (F48); its CM16 and CM15, CM6, and CM9
  replace them.
- **Serving Vertex through the service**: there is no key to move (A2).

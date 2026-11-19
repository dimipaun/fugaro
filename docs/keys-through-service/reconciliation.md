# Every model key through the credentials service: reconciliation

Checked against `origin/main` at `45658d8a8` on 2026-11-19. **Where this file disagrees with
[spec.md](spec.md), this file wins.** It cites paths and symbols, never line numbers. Decisions are
**D3000–D3002**, all answered by the owner on 2026-11-19. It reverses **D1634** and **D1670**
([model crafts](../model-crafts/reconciliation.md)) and narrows the code behind **D150**
([credentials service](../credentials-service/reconciliation.md)); each is marked where it bites.

## 1. What the spec asks for, and what the repository already has

| The spec's concept | What the repository has |
| --- | --- |
| "Served by the credentials service" | True today for a Claude OAuth token and an Anthropic key on a switched repository (`config.CredentialServingPath` answers `CredentialPathCredsvc` for `claude-oauth` and `anthropic`) |
| A provider key (OpenRouter and the like) | The runner path: mounted in the job as `FUGARO_PROVIDER_KEY_*` and spent by the run's own gateway (`CredentialPathRunner`; `infra.SecretMounts`, `runnerKeyCredential`, `runner.ProviderCredential`) |
| Vertex | No key at all: the job's own service account, through `google.DefaultTokenSource` |
| "The switch" | Per repository and conditional: `infra.CredentialsServiceServes(installed, ready, auth, mode)` |
| The Terraform that mounts a key | One Secret Manager container per logical secret per repository with operator grants, one accessor per job per secret, one env entry per mount, and a three-valued `model_secrets_served` that selects among them |

## 2. Assumptions

- **A1.** "Keys" means model credentials: Claude OAuth tokens, Anthropic keys and provider keys.
  Git tokens, the GitHub App key, workflow secrets and signing keys are not in scope; secrets
  brokering owns them (B4.9, B5.7's git half, C2.3).
- **A2.** Vertex is not a key and nothing is mounted for it (D498). "Served through the service"
  has nothing to move, so Vertex is unchanged.
- **A3.** A local run (`fugaro exec`, `fugaro troubleshoot`) uses the developer's own environment
  and is unchanged (D149).
- **A4.** "No key is mounted" is the end state for every repository whose job image is at or after
  the release that carries this work. A repository on an older image keeps its mounts until its
  image is rebuilt, which `fugaro update` does (secrets brokering's rule G17: a job never loses a
  mount before the service serves that credential to the image it runs).
- **A5.** The provider models a project uses today keep working with no config change by the
  owner: the same `providers:` and `credentials:` blocks, the same pins.

## 3. Findings

**F1 — Exists.** A switched repository's Claude OAuth token and Anthropic key are already served
by the service, and their job mounts are already dropped (`infra.ModelSecretsServed`,
`dropModelMounts` in `internal/infra/readiness.go`, `config.ServiceServedAuths`). Consequence: the
work is the remainder, listed in F2 to F6.

**F2 — Gap: provider keys.** Every `api-key` job still mounts each provider key it uses
(`ModelSecretsServed` answers `anthropic` and keeps them; `TestFilledServiceServedAuthsKeepsProviderKeys`).
`config.CheckProviderAuth` refuses a provider-served pin under every auth but `api-key`.
Consequence: KS1 to KS5.

**F3 — Gap: the service cannot serve a named provider key.** The bundle holds one `provider-key`
entry (`Bundle.providerKey`), where the runner path holds one key per declared provider.
`fugaro creds set` refuses the key of an `openrouter` credential (`credsKeyCredentialHalfRefusal`),
and `fugaro accounts add --type openrouter` writes the job's own container. Consequence: KS1, KS2.

**F4 — Rule: the gateway does the provider's work, and the service does none of it.** For a
provider model the run's gateway routes by model pattern (`routeFor`), refuses unpriced fields
(`checkRequest`), adds the route's fee (`Route.FeePct`), settles on the provider's reported cost
(`fromUsage`), applies `RequireZDR`, `PricePolicy` and `allow_data_to`, and keeps a ledger per
route (`internal/gateway/forward.go`, `route.go`). The service's `providerRoute`
(`internal/credsvc/forward.go`) leaves the models and the fee at zero by its own comment. The
gateway forbids the combination: `validateRoutes` and `errServiceOnRoute` say a provider route is
never reached through the service. Consequence: R1.

**F5 — Wrong against D150's wording.** `creds.checkCandidates` (`internal/creds/select.go`) refuses
every tier C candidate without looking at the model, and a coding caller is never offered one
(`Bundle.Credentials` leaves it out). D150 gates a Claude model through a provider, for the reason
that prompt-cache behaviour there is unmeasured. Check 50 measures exactly that and nothing about
a non-Claude model. Consequence: D3000, KS3.

**F6 — Gap: the other mounts that remain.**
- A repository the switch does not cover: `CredentialsServiceServes` is false for budget mode off
  (D2071), for a root not re-applied and for a bundle that is not ready.
- The mixed stage-account variant keeps its OAuth token in the runner's environment and hands it
  to Anthropic outside the gateway (`WorkflowSpec.StageAccount`; `mixedStageAccount` in
  `internal/cli/run.go`; `TestSwitchedMixedRepositoryKeepsItsStageAccountToken`).
- Every shared project job mounts its account's token (`projectAccountMount`,
  `internal/infra/projectjobs.go`), which is secrets brokering's B5.7, not started.
Consequence: D3002, KS6, KS7, and KS12 waits on B5.7.

**F7 — Gap: budget mode off has no path to the service.** With the budget off the run has no
gateway, and `agent.BuildEnv` (`internal/agent/env.go`) puts the key in the agent's own
environment. `serviceSwitchModeRefusal` (`internal/runner/gateway.go`) refuses a switched
repository in that mode. Consequence: D3002.

**F8 — Gap: nothing copies a key into the bundle.** `fugaro creds set` is the bundle's only writer.
An installation's existing keys sit in per-repository containers
(`google_secret_manager_secret.this`, `modules/repo/secrets.tf`, with `prevent_destroy`) and in
the project-wide ones (`google_secret_manager_secret.project`, `modules/installation/secrets.tf`).
Consequence: D3001, KS8.

**F9 — Rule: what Terraform holds per credential.** Per repository: one container and two operator
grants per operator. Per job: one accessor for the job's account
(`google_secret_manager_secret_iam_member.job`, `modules/workflow/sa.tf`), one for the service's
account where it reads the mount (`.service`, D2074), and one env entry (`modules/workflow/job.tf`).
The selection among them is `var.model_secrets_served` and the locals `is_run_model_secret`,
`is_served_model_secret` and `is_service_held`. Shared jobs repeat it in `modules/project`.
Consequence: KS10 and KS11 remove the model half of all of it.

**F10 — Rule: an older image.** A job image built before a release cannot use a route that release
adds, and `internal/cli/recipes_skew.go` already gates on it (`serviceAuthSince`,
`providerEntrySince`, `keyCredentialSince`). Consequence: A4, R5.

**F11 — Rule: a run in flight.** An execution runs the job template it started with
(`docs/design/update-in-flight.md`), so a dropped mount reaches the next execution, never a
running one. Whether a running execution survives its job being replaced is UNVERIFIED there.
Consequence: R6, Check 780.

**F12 — Rule: pinned sentences.** `SECURITY.md`, `docs/multi-model.md` and the credentials runbook
state, under test, that a provider key the job mounts stays its own
(`TestSecurityMdStatesTheCredentialsService`, `TestSecurityMdNamesTheCredentialsServicesKnownLimits`,
`TestDocsMultiModelDocumentsTheCascadeSwitch`, `TestDocsCredentialsRunbookSaysWhatTheLaunchWires`,
`TestSecurityMdStatesTheMixedRunResidual`). The Terraform tests hold the three arms
(`TestServiceHeldSecretsMatchTheCredentialsService`, `TestModelSecretsServedValidationMatchesGo`,
`TestSecretReadersDocMatchesTheTable`). Consequence: the task that makes a sentence false changes
it and its test.

**F13 — Rule: Check 50 is not a script and has not been run.** It is inline shell in
`docs/gcp-live-checklist.md`, marked not run, and it exercises the runner's gateway route, not the
service. Consequence: KS13 writes it as a script, and D3000 keeps only Claude-through-a-provider
behind it.

**F14 — Rule: another plan edits the same files.** The workspace-service plan rewrites
`internal/credsvc/cascade.go` and `forward.go` (its WS1, WS8) and `internal/gateway/service.go`
(its WS11, WS12). Consequence: R8.

**F15 — Rule: D1634's second reason goes away.** D1634 kept provider keys on the runner path partly
so that a pool's secrets would not all sit in one job (its F20). Served by the service, none sits
in any job. A pool's failover between provider members stays at a stage boundary, as today; this
work does not change it.

## 4. Decisions

All answered by the owner on 2026-11-19.

| # | Question | Answer | Mark |
| --- | --- | --- | --- |
| **D3000** | Which provider calls wait for Check 50 (F5)? | Only a Claude model through a provider key. A non-Claude provider model is served by the service without waiting | as recommended. **It replaces the owner's answer of earlier the same day** ("run Check 50 first" for every provider key), given on the first reading of the gate; the two facts of F5 were found afterwards |
| **D3001** | Where do model keys live at the end, and how do they get there (F8)? | In the project's one bundle. `fugaro update` copies each existing key there itself, in memory on the owner's machine, and then retires the per-repository containers | as recommended |
| **D3002** | What happens to budget mode off (F7)? | The gateway always runs: with the budget off it is a pass-through to the service, with no budget enforced and no key in the job | as recommended |

**Reversed by the owner's ruling D2975:** D1634 (provider members on the runner path) and D1670 (an
`openrouter` key goes to the job's own mounts). D498's Vertex is untouched (A2).

## 5. Resolutions

- **R1. The gateway keeps the provider's work; the service holds the key.** Routing by model, the
  fee, the price check, the settle on the provider's reported cost, `RequireZDR`, `PricePolicy`,
  `allow_data_to` and the per-route ledger stay in the run's gateway, unchanged. What changes is
  the last hop: a routed call goes to the service's provider route, naming the provider credential
  it is for, and the service attaches that key and forwards to the provider's own address. The
  service checks the name against the member's published policy for the calling repository before
  it attaches anything, so a run cannot name a credential its policy does not give it. Contract C1.
- **R2. One entry per provider credential, named for its owner.** The bundle carries one key per
  declared provider credential. A project-wide one keeps the name `credential-<name>-key`; a
  repository's own provider secret is entered under a name that carries the repository, so two
  repositories that use one name for different keys never collide and `fugaro update` never has to
  choose between them.
- **R3. The gate, narrowed.** The service refuses a provider credential for a Claude model until
  Check 50 passes, with the refusal that names the check, and serves it for any other model
  (D3000). Which models are Claude's is the existing pin vocabulary, not a new list.
- **R4. The copy.** `fugaro update` gains one stage that, for each model key the installation
  holds outside the bundle, reads the newest version, writes it into the bundle under R2's name
  and records that it did, holding the value only in memory and printing none of it. It is
  idempotent, it never overwrites a bundle entry that differs without saying so, and it runs
  before any stage that drops a mount. It asks nothing (D3001).
- **R5. A mount goes only when its image can do without it.** For each repository the mounts are
  dropped by the `fugaro update` that finds its job image at or after this release, using the
  existing skew gate; until then the repository keeps working on its mounts (A4, F10).
- **R6. Runs in flight.** A running execution keeps its template and so its mounts. `fugaro update`
  names, in one short line, the step that can reach a run: none of the copy, and the retirement of
  a container only after no job references it.
- **R7. Containers are retired through the update's own retirement.** A per-repository model
  container is removed by the mechanism `tf.RetirableTypes` already gives the accessors, after R4
  has copied its value and no job mounts it. No flag, no other command.
- **R8. Two plans, one set of files.** A task of this plan that edits `internal/credsvc/cascade.go`,
  `forward.go` or `internal/gateway/service.go` is not launched while a workspace-service task
  that edits the same file is live, and whichever lands second rebases. Neither plan waits for the
  other as a whole.
- **R9. The stage account goes through the service.** The mixed variant's second account is
  selected by the service like any named account, through the gateway, so the runner holds no
  token for it (F6).
- **R10. One switch for every auth but Vertex.** `CredentialsServiceServes` stops depending on the
  budget mode (D3002) and on the bundle's readiness as a separate fact: the copy of R4 is what
  makes a bundle ready.

## 6. Open tensions and open items

No two answers pull against each other.

- **O1.** D3000 replaces an answer the owner gave earlier the same day. It is recorded in
  [workspace credentials](../workspace-credentials/spec.md) as "D150's gate stays"; KS14 amends that
  sentence to the narrowed gate.
- **O2.** D3001 has `fugaro update` read secret values on the owner's machine. The precedent is the
  doctor's token check, which reads the bundle in memory in the owner's terminal. It is named here
  for the independent security review of KS8.
- **O3.** Shared project jobs lose their mounted token only when secrets brokering's B5.7 lands,
  which is not started. Until then "no model key is mounted" is true of every job but those.

## 7. UNVERIFIED

- **U1.** Whether the owner's account can read the existing per-repository containers' values (the
  operator grants in `modules/repo/secrets.tf` were not read role by role). If it cannot, R4 needs
  a grant applied by the same update. Settled by KS8's first step, and live by Check 780.
- **U2.** How a repository's own provider secret is declared against a project-wide credential of
  the same name, and so the exact form of R2's repository-carrying name. Settled by contract C1.
- **U3.** Whether a running execution survives its job's template being replaced
  (`docs/design/update-in-flight.md` marks it unverified). Settled by Check 780, owner-run.
- **U4.** Which files hold the stage account's selection in the runner. Settled by KS7's first step.
- **U5.** The bundle's size against Secret Manager's limit for one secret, for an installation with
  many provider keys. Settled by KS1's test of a bundle at a stated worst case.

# Implementation plan: the credentials service

**Goal.** Build what [spec.md](spec.md) asks for **as amended by [reconciliation.md](reconciliation.md)**, the owner's answers of 2026-10-17 (D130–D154) and the owner's two rulings of 2026-10-17 (**O1: skip Slice 1, go straight to the service**; **O2: `agent.auth: cascade` with no switch in the project policy is refused**): first a **separately deployed Cloud Run service** that alone holds the model credentials and alone writes the cascade's shared state, running the **credential cascade** (tier A, shared subscription OAuth tokens, then tier B, an Anthropic API key) with exhaustion and broken marks, probes, wait-or-switch and per-call accounting, reached by a job through the in-runner gateway with its run identity (Slice 1); then **GitHub App installation tokens minted by the service** per job (Slice 2). Where the reconciliation and the spec disagree, the reconciliation wins.

**Status: Slice 0 is built; Slice 1 and later are plan only.** **The owner approved Slice 0 on 2026-10-18, and C0.2, C0.3 and C0.6 are built**: the supersession notes in the three design documents and the OAuth-reviewer-stage plan, Checks 90 and 91 written into `docs/gcp-live-checklist.md`, and the `creds/` denial pinned against today's rules by the emulator cases in `internal/budget/rules/creds_emulator_test.go` (whose `firebase`-tagged runs need an emulator, so they are run where one is available), and every sentence those two docs tasks wrote pinned by `internal/cli/docs_credentials_service_test.go`. C0.4 and C0.5 are the owner's own runs of those two checks and produce no commit; C0.1 is this file. Nothing else was written: **no line of any Slice 1, 2 or 3 task below has been written or run.** **The owner answered every decision on 2026-10-17** — all as recommended except **D132** (answered "proceed and record the risk as accepted") — ruled on O1 and O2 the same day, and answered O3 and confirmed O4 the same day. **Answering a decision or ruling on an item is not approving a task, and the one approval this plan records is the owner's own of Slice 0 on 2026-10-18: **no task of Slice 1, 2 or 3 is approved.**** Each task starts only on the owner's explicit go for that task (or for a named group of tasks); the gate at the end of this file is not passed. No item is open in [reconciliation.md](reconciliation.md) §4: **O3** is answered — the cascade's switch is `routing.claude: {via: cascade}`, which needs no provider route while tier C for Claude is off and leaves `via: auto` unchanged — and **O4** is confirmed — the service learns each repository's admitted accounts from what `fugaro project policy publish` publishes.

**What waits for 0.9.0.** Because the service is now the first product slice, **every product task of this plan waits for the seal — both halves — which is held for 0.9.0** (D134): PR #417 (the seal's runner half, Slice A, merged into `next-0.9` on 2026-10-17) **and** PR #523 (its launcher half, the seal design's Slice B, D19's second step: the launcher seals the token object and hands the key to the execution it starts; a `--retry` re-mints and re-seals, D17; an open draft on `next-0.9`), both reaching `main` with 0.9.0. **What can be done earlier, each still on the owner's go:** Slice 0 (the supersession notes, Checks 90 and 91 written and run by the owner), docs-only work, and one piece of rules groundwork that needs no rules change and no run-token grant (C0.6: emulator tests pinning that today's rules already deny every run token all of `creds/`).

**Checked against.** `origin/main` at `021728c1` (2026-10-17), the same commit as [reconciliation.md](reconciliation.md). Every path and symbol named below was located with `git grep` (there is no `graphify-out/` for this repository) and re-checked to exist before it was written; a path marked **new** does not exist yet. Line numbers are never cited.

**Spec.** [spec.md](spec.md) (verbatim). **Reconciliation.** [reconciliation.md](reconciliation.md) — F1–F37, D130–D154, O1–O4, U1–U7. **Index.** [README.md](README.md).

**Related.** [mixed-credentials.md](../design/mixed-credentials.md) · [2026-10-14-oauth-reviewer-stage.md](../plans/2026-10-14-oauth-reviewer-stage.md) (superseded by D130) · [project-centric.md](../design/project-centric.md) §7 (D131) · [m9-budget-and-dashboard.md](../design/m9-budget-and-dashboard.md) §11 · [budget-token-channel.md](../design/budget-token-channel.md) §7.1 (the seal) · [named-oauth-tokens.md](../design/named-oauth-tokens.md) · [gcp-live-checklist.md](../gcp-live-checklist.md) · [SECURITY.md](../../SECURITY.md)

---

## Considered and declined: the in-runner cascade (the former Slice 1)

The plan's first version built the cascade inside the runner first (the existing gateway learning the OAuth upstream and the A→B switch, with the tier-A tokens mounted in the job), and moved it into the service second. **The owner declined it on 2026-10-17 (O1: "skip Slice 1; go straight to the service").** Why: in that slice the only writer of a shared mark was the run's own gateway, so the rules had to give every run token a **temporary write grant on shared marks** — and a hostile agent in one run could then mark an admitted account exhausted or broken for every run. That is worse than a denial of service: with `fallback: auto`, false tier-A marks push every run of the project onto paid tier B, a **cost shift bounded only by the caps** (review finding W3). The grant could not even be narrowed as written: the rules cannot check "its own repository's admitted set" (admissions live only in the operator's local config, `config.ClaudeAccount.AllowDataTo`, and nothing publishes them to the database), and marks are shared across runs by design and not monotonic (an exhausted mark is replaced by a later one), so no `.validate` expression can tell an honest mark from a false one (review finding M1). The slice also mounted several tier-A tokens in one job, readable through `/proc` (F6). **With the service first, no run token ever holds a write on `creds/`**: the service's own account is the only writer of the cascade's state (D136 exactly), and no job mounts a tier-A token at all.

---

## Builds on merged code

| What exists | Where | Used by |
|---|---|---|
| The loopback gateway: per-run token, upstream credential attach, SSE pump, no header or body logging | `internal/gateway` — `Upstream` (`Kind` "anthropic" or "vertex"), `Route.attachCredential`, `validateRoutes`, `anthropicBase`, `Server.upstreamRequest`, `forward`, `pump`, `isSSE`, `scrubToken`, `redactWriter` (F1, F32) | C1.7, C1.8, C1.11, C1.15 |
| The ledger: reserve, settle, caps | `internal/gateway/ledger.go` (`reserve`, `settle`), `internal/budget/lease.go`, `internal/budget/model.go` (`Caps`, `AgentEntry`) (F25) | C1.16 |
| Notional accounting for subscription calls | `run.reportNotional` (`internal/runner/budget_session.go`), `budget.Session.AddNotional` (F25) | C1.16 |
| The routing switch, read once per run from the project policy | `config.RoutingClaude` (`Via`: `direct`, `openrouter`, `auto`), `config.CheckRouting`, `config.CheckRoutingShape`, `config.RoutingClaudeFor`, `run.applyRouting` and `RoutingInertReason` (`internal/runner/routing.go`), `GatewayOn` (F13) | C1.1, C1.2, C1.19 |
| The project policy and its version skew | `projectpolicy.Policy` (`config/policy/shared`, `config/policy/repos/$slug`), `projectpolicy.Version`, `projectpolicy.RulesVersion`, `VersionError`, `SkewSentence` (`internal/projectpolicy/version.go`) | C1.2, C1.3, C1.11 |
| The CLI's version-skew constants | `internal/cli/recipes_skew.go` (`policySince`, `artifactsSince`, …), `gcpProjectFieldSince` (`internal/cli/init_fugaroyaml.go`) | C1.1 |
| The rules as a generated artifact | `internal/budget/rules/rules.json.tmpl`, `rules.Generate` (takes no input), `testdata/rules.golden.json`, `TestRulesGolden`, `TestRulesParity`, the `firebase`-tagged emulator tests (`emulator_test.go`, `lease_emulator_test.go`); the template is a closed allowlist (root `.read`/`.write` false; `$other` refused in nested nodes) (F16) | C0.6, C1.3, C1.4 |
| The write-once run outcome | `outcomes/$day/$slug/$run` (rules: `RUN && DAYOK && !data.exists()`), `budget.PathOutcome`, written at the run's end (`internal/budget/registry.go`) and by the sweeper for a dead run (`internal/budget/sweep.go`, `Sweeper.Sweep`) | C1.10 |
| RTDB compare-and-set | `rtdb.Client.GetETag`, `rtdb.Client.PutIfMatch` (412 → `ErrPrecondition`), precedent `budget.SetKill` (F17) | C1.9 |
| The run's budget identity | `token.Mint`, `token.ExpiryFX`, `token.IAMSigner`, `token.Exchange`, `token.Session` (`internal/budget/token`) (F29) | C1.10 |
| Sessions and `--resume` | `agent.Args`, `run.noteSession`, `run.saveSession`, `run.restoreSession` (`internal/runner/session.go`) (F22) | C1.17 |
| Named OAuth accounts per repository | `config.ClaudeAccountSecret`, `config.AllowedClaudeAccounts`, `config.ClaudeAccount.AllowDataTo`, `claudeAccountFor` (`internal/cli/run.go`), `fugaro accounts ls` (`internal/cli/accounts.go`) (F4) | C1.2, C1.21 |
| Secret mounts and readiness | `infra.SecretMounts` (`internal/infra/spec.go`), `infra.OptionalSecret` (`internal/infra/readiness.go`), `google_secret_manager_secret_iam_member "job"` (`deploy/terraform/gcp/modules/workflow/sa.tf`) (F1, F2) | C1.18, C1.20, C2.3 |
| GitHub installation tokens, minted in the job | `MintInstallationToken`, `tokenSource.Token`, `tokenPermissions`, `buildTokenPermissions` (`internal/gitprov/github/apptoken.go`), `Provider.GitAuth`, `run.refreshGitAuth` (F7) | C2.1, C2.2 |
| The coding-agent refusal for live applies | `agentMarker` (`internal/cli/initsecrets.go`) | C1.20 |
| The opt-in init stage precedent | `fugaro init --provision-batch` (`internal/cli/init.go`, `internal/cli/init_compute.go`) | C1.20 |
| The trusted-repository gates | `config.Followup.AllowPublic`, `run.checkReviewAuthor` (`internal/runner/reviewmode.go`) (F11) | C1.1 |
| What a run's record says | `runstore.Record.OAuthAccount` (declared, unwritten), `runview.Join`, `runstore.HaltBudgetUnavailable` (F26) | C1.15, C1.16, C1.19, C1.21 |
| Owner-run live tests | `internal/e2e` under the `live` build tag (`live_budget_test.go` `TestLiveBudget`, `live_gateway_test.go`) | C1.4 |
| The docs guards | `internal/cli/docs_security_test.go`, `docs_website_security_test.go`, `docs_usage_limit_switch_test.go`, `docs_named_oauth_tokens_test.go`, `docs_project_centric_test.go`, `docs_credentials_service_test.go`; `plugin/skills_lint_test.go` (`TestLintSizeBudget`, `TestLintByteBudget`, `TestLintRejectsSecretShaped`) | every docs and skill edit |

---

## Slice summary

| Slice | What it is | Tasks | Mergeable alone? | When |
|---|---|---|---|---|
| **0 — Verify first** | The supersession notes; Checks 90 and 91 written and **run by the owner**; the `creds/` denial pinned against today's rules | C0.1–C0.6 | Yes — docs and one test file; C0.4 and C0.5 are owner-run and produce no commit but the recorded result | **Before 0.9.0**, on the owner's go |
| **1 — The service and the cascade** | The `cascade` mode, the policy settings, the rules (no run-token grant on `creds/`), the OAuth upstream (**TIER A**), the state package, run-identity verification, the service (forward, cascade, probe, audit), its image, the runner's service upstream, accounting, waiting, Terraform, mounts and the init stage, wiring, visibility, SECURITY.md, Checks 92, 94, 95 and 99 | C1.1–C1.24 | Yes — opt-in per installation (D147); with it off nothing changes, and a `cascade` workflow is refused on an installation without the service | **After the seal (both halves) has merged — 0.9.0** |
| **2 — GitHub tokens through the service** | The mint endpoint, the runner's fetch, the App key's grant moved, docs, Check 96 | C2.1–C2.5 | Yes — behind Slice 1's opt-in | After Slice 1 |
| **3 — Later, unscheduled** | Resume after job death, alerts, the share model, egress, Batch, tier C for Claude, refresh, proxied push, shared jobs | C3.1–C3.9 | Unscheduled by construction | — |

The former Slices 2, 3 and 4 are the new Slices 1, 2 and 3; the former Slice 1 is declined (above).

---

## Check numbers this plan takes (90–99)

**The block 90–99 was reserved for this work.** Each number below is assigned once; **a reserved number is never reused and never renumbered** — so a number whose subject was declined stays reserved and unused rather than taking a new subject. **All are owner-run:** no agent performs any step, none is a CI job. A check is written into `docs/gcp-live-checklist.md` (its register row and its section) **before** anyone runs it, and every section states exactly what the owner types, as the register's existing checks do (a hard-coded project guard, `${P:?}` on every `gcloud`, installation `fugaro` only, **never `belong`**, sandbox repository only, a Restore that ends in an all-`0` assertion). The register's highest row today is 58; other plans have claimed 59, 60–69 and 80–89, and 90–99 collides with none of them.

| # | The check | Written by | Run at | Where |
|---|---|---|---|---|
| **90** | **Header swap.** Claude Code on the owner's own machine, with the owner's own `claude setup-token` token, against a throwaway local proxy that strips the incoming credential and attaches the real one; records which request headers must pass through, the response status/type/headers of a usage limit if one can be observed (U3), Claude Code's client timeout and retries and its own wording for a usage limit when one can be observed (U4 — the wording stays UNVERIFIED if none is), and the token's stated expiry (U2) | C0.3 | C0.4 | The owner's machine; no cloud; no Fugaro installation |
| **91** | **Cloud Run streaming.** A throwaway Cloud Run service on installation `fugaro` forwarding one long streaming call: request timeout, body limit (HTTP/1 vs end-to-end HTTP/2), SSE unbuffered through the front end, cold start (U5) | C0.3 | C0.5 | Installation `fugaro`, sandbox only |
| **92** | **The rules deployed.** `fugaro init --firebase <id>` deploys the rules; `config/limits/policyRules` still reads `RulesVersion` 1 and the new `config/limits/credsRules` reads 1; a sandbox run's identity can read and write **nothing** under `creds/`; the new validated children (current credential, waiting-until, by-tier counters) accept their own run's well-formed writes and refuse anything else | C1.5 | C1.6 | Installation `fugaro` |
| **93** | **Reserved, unused.** It was the declined in-runner cascade's end-to-end check (O1); its subject is gone and the number is not reused | — | — | — |
| **94** | **The service deployed.** The owner-run init stage applies the service; job SAs have **no** `secretAccessor` on model secrets and **only** `run.invoker` on the service; the service SA holds `secretAccessor` on model secrets only; the audit log carries names, never values | C1.23 | C1.24 | Installation `fugaro` |
| **95** | **A run through the service.** A sandbox run's calls go gateway → service → upstream; `/proc` of the runner holds no model key; a run whose outcome is written is refused; the service made unreachable halts the run `budget_unavailable`-style, never falling back | C1.23 | C1.24 | Installation `fugaro`, sandbox repository |
| **96** | **GitHub tokens through the service.** The job holds no App key; the run's installation token is minted by the service for one repository with `tokenPermissions`; a run identity for repository X cannot mint for Y | C2.4 | C2.5 | Installation `fugaro`, sandbox repository |
| **97** | **The Batch backend through the service** (D149), when the backend is selectable | C3.5 | C3.5 | Installation `fugaro` |
| **98** | **Resume after job death** (C3.1) | C3.1 | C3.1 | Installation `fugaro`, sandbox repository |
| **99** | **The cascade end to end through the service.** Two admitted test accounts and the sandbox API key: a forced exhaustion of A1 switches to A2, then to B; the probe clears a backed-off mark; a wait under the threshold resumes the stage with `--resume`; `fallback: oauth-only` shows the run waiting with its expected resume time; a revoked token is marked `broken` and shown by `watch`, `fugaro accounts ls` and `doctor`, then cleared by the operator; per-call tiers and the switch log in the record | C1.23 | C1.24 | Installation `fugaro`, sandbox repository |

Check 47 (the superseded plan's — superseded by D130, 2026-10-17) stays reserved and unused.

---

## Conventions every task follows

1. **Owner-run live checks.** Every live step — a rules deploy, a Terraform apply, an init stage, a check — is the owner's, on installation `fugaro` and a sandbox repository only. **`belong` is never touched by any step of this work** (D147); it migrates only when the owner says so, by hand. `agentMarker` refuses live applies inside a coding agent; no task weakens it.
2. **No secret-shaped literals** in tests, fixtures, docs or skills. Fake credentials are built at run time (for example from a constant prefix and `t.Name()`), never a literal that looks like a key or token; the plugin lint (`TestLintRejectsSecretShaped`) and the image scan's `images/trivy-secret.yaml` already enforce this for their areas.
3. **Skills stay inside their budget.** Any edit under `plugin/skills/` passes `go test ./plugin/...` (`TestLintSizeBudget`, `TestLintByteBudget`, the command and flag lint).
4. **Docs guards.** Every task that edits a document runs `GOFLAGS=-p=2 go test ./internal/cli ./internal/config ./images -run 'Docs|Migration|BaseImageDoc|Design|SecurityMd|RecipesGuide|NoLegacy|Website|CliRef' -count=1` (the list CI's docs-only path names) — and updates the guard test in the same commit when the pinned text changes on purpose.
5. **Version skew.** A new `fugaro.yaml` value is a version-skew break in a strictly decoded config: it gets a `…Since` constant in `internal/cli/recipes_skew.go` and `fugaro init --repo` refuses it against a base image that predates it (the held plan's P9, carried over by D130). A policy change bumps `projectpolicy.Version` (and uses `SkewSentence`) **only when an older reader must refuse the node**; a new top-level member is tolerated by older readers (`projectpolicy.Decode` ignores unknown top-level keys) and does not bump it. A rules change bumps `projectpolicy.RulesVersion` **only when it changes the policy nodes** (`version.go`): `projectpolicy.Read` treats a marker below `RulesVersion` as "no policy here", so a bump drops every published policy (caps, `allow_repos`, routing, prices) on an installation until its owner redeploys the rules, and `requirePolicyRules` refuses to publish there. This work's rules change does not touch the policy nodes and does not bump it (C1.3).
6. **Strict decoding and schemas.** New config and wire formats decode strictly; `schemas/fugaro.schema.json` changes with the config.
7. **Rules changes** ship as template + regenerated golden + parity + emulator **denial** cases in one PR, and are deployed by the owner (`fugaro init --firebase <id>`).
8. **No agent edits `.github/workflows/`.** A workflow change (the service image's build) is staged under `docs/pending-workflows/` and copied in by the owner.
9. **Hermetic tests.** No network, no real cloud; use `internal/gcpfake`, `internal/testutil`, `internal/gateway/anthropicfake` and `t.TempDir()`. Runner and gateway changes run with `GOFLAGS=-p=2`. The one exception is C1.4's helper, which is built under the `live` tag, never runs in CI, and is run by the owner only.
10. **TIER A isolation (D132).** Every piece of code that exists only for subscription OAuth tokens through a proxy lives behind its own upstream kind and in its own files, and the task is marked **TIER A**. Removing tier A means deleting those files and one switch case, never unpicking shared code.
11. **The service is the only writer of cascade state (O1, D136).** No run token, runner or in-runner gateway writes under `creds/`; the runner side only calls the service and records what the service answers on its own run's nodes.

---

## Global Constraints

1. **This pull request changes nineteen files, and nothing else.** The fourteen documents are: the three credential documents — `docs/credentials-service/implementation-plan.md`, `docs/credentials-service/README.md` and `docs/credentials-service/reconciliation.md`; the register, `docs/gcp-live-checklist.md` (the register's rows 90 and 91, Check 47's entry, and the two check sections); the four documents D130 and D131 supersede or amend — `docs/design/mixed-credentials.md`, `docs/design/project-centric.md`, `docs/design/m9-budget-and-dashboard.md` and `docs/plans/2026-10-14-oauth-reviewer-stage.md`; and six the reviews found asserting the opposite of what the tree does, each because the behaviour landed in a commit that touched no document: `docs/git-providers.md` and `docs/project-layer.md` (both said a shared job's environment carries no `FUGARO_GITHUB_APP_ID` and that a GitHub run on one is refused when it opens its provider, where `internal/infra`'s `projectJobEnv` has set it for every GitHub-provider profile since 21e67bb8 and the project module pins it as a precondition), `docs/checkout.md` (which pointed readers at Check 38's FACT lines in the checklist, where none were ever recorded), and — from the security review of 2026-10-19 — `SECURITY.md`, `docs/design/project-named-oauth-tokens.md` and `docs/design/project-centric.md` again, which promised that the credentials service replaces a shared job's static token mount while the same documents said, and the plan's own Global Constraint 6 confirms, that the service as written serves per-repository runs only and the task that would replace it (C3.9) is unscheduled: the mount is now stated as a stopgap that stays, with no slot promised for the work that ends it. `docs/design/bucket-iam.md` **is not among them**: its corrected text (H5's condition title `fugaro-launchers-runs-tokens` and its `runs/ || tokens/` expression, after the sealed-token widening) arrived with the base merge of `origin/integration-0.9.0`, so this pull request carries the guard that pins it and not the document — and that merge also brought `gcp.LegacyLauncherBucketCondition`, so that guard now holds the document to naming the runs/-only title only as the pre-0.9.0 form. `website/content/security.md`, the generated page, is the last: SECURITY.md is its source, so its shared-job bullet could not be corrected in one and not the other (`go run ./internal/clidoc` writes it). `docs/credentials-service/spec.md`, the owner's spec verbatim, is **already on the base** (`#512` added it) and this pull request does not touch it. The five test files are `internal/cli/docs_credentials_service_test.go` (the docs guards, which C0.1–C0.3 add — the pins for Checks 90 and 91, the register's rows and block, and these documents' own claims); the three guard files that pin the corrected documents above — `internal/cli/docs_project_layer_test.go`, `internal/cli/docs_bucket_iam_test.go`, `internal/cli/docs_checkout_test.go` (**test only**); and `internal/budget/rules/creds_emulator_test.go` (C0.6: **test only**, behind the `firebase` tag, changing no rule and adding no run-token grant). **No production Go, Terraform, rules, schema, skill or workflow change**, and no file beyond those nineteen: the shipping tasks below edit files outside `docs/` and `SECURITY.md`; this pull request does not. This constraint is what the status line's "C0.2, C0.3 and C0.6 are built" means — docs, and those five test-only files.
2. **No task is approved but Slice 0's (2026-10-18); each of the others** starts on the owner's explicit go. A recommendation, an answered decision, a ruling or a passing check is not a go.
3. **Slice order is the owner's (D137 as amended by O1).** Slice 0 first. **Every Slice 1 task starts only after the seal — #417 (merged into `next-0.9`) and #523 (the launcher half, open draft) — is on `main` (D134, held for 0.9.0)** and Checks 90 and 91 are recorded (D151). Slice 2 follows Slice 1.
4. **If Check 90 fails** (subscription traffic does not survive a header swap), the TIER A tasks (C1.7 and the TIER A parts of C1.11 and C1.12) are dropped, and Slice 1 ships the service with a B-only cascade, waiting and visibility; the plan is amended in a docs PR before any further task starts.
5. **The invariant (D133).** An OAuth token is only ever sent to Anthropic's own endpoint. It is asserted in code (C1.7) and by a test that fails if any route with a provider base URL can carry an OAuth credential.
6. **Per-repository jobs only** (C3.9). The service admits a run only on a per-repository job: its C1.10 refuses a shared job's run (`TestVerifyRefusesSharedJobRun`), and the task that would admit one — the phase-3 shared-job rules reaching `main` (C3.9) — is in Slice 3, **unscheduled**. **This is a constraint on the service, not a refusal of OAuth on a shared job:** the phase-3 refusal of an OAuth launch onto a shared job (Task 3.4a, PR #511, merged into `next-0.8.5`) is **lifted in 0.9.0** by the owner's ruling of 2026-10-18, which carries a project's own named tokens into a shared job through the static `secret_env` mount ([project-named-oauth-tokens.md](../design/project-named-oauth-tokens.md) §3) — a **stopgap**, and accepted for its lesser exposure **on its own terms, not against a replacement**: **it cannot be said to be accepted because this service replaces it, because this service does not replace it as this plan is written.** The service serves per-repository runs only (this constraint), so a shared job's runs never reach it, and C3.9 — the task that would take those tokens over — is unscheduled: **the mount stays until C3.9 is given a slot, and no slot is promised**, which is why the same sentence cannot both call the mount temporary and point at a replacement that has no date. See [project-centric.md](../design/project-centric.md) §7 and [SECURITY.md](../../SECURITY.md)'s carrier paragraph.
7. **The managed-projects deployer (D75: `fugaroDeployer`, `fugaroDeployerScoped`) is never used** for any of this work, and `fugaro managed apply` never deploys the service (D146). The service is Fugaro's own infrastructure, in the installation root.
8. **Unreachable service halts, never falls back** (D143): no code path mounts a model key in the job when the service is down.
9. **No refresh, no alerts, no egress restriction, no share model** in Slices 1–2 (D139, D141, D142, D144).
10. **Every task marked SECURITY-SENSITIVE, and every task that changes a SECURITY.md security claim or writes `creds/` as the operator, gets opus and a mandatory independent review** by a reviewer that did not write it, before merge. That is nearly every code task here.
11. **No run-token write on `creds/`, ever (O1).** No task adds one, even temporarily.

---

## Review Focus

A reviewer of **this pull request** — the fourteen documents and the five test files that pin them (Global Constraint 1) — should check:

- That every answer cell in [reconciliation.md](reconciliation.md) §3 matches the owner's answers, that **D132** reads as an accepted owner risk with no view on Anthropic's terms, and that D136 and D137 carry the dated O1 amendment with the original answer still visible.
- That O1 and O2 are recorded as the owner's rulings, O3 as answered (`via: cascade`) and O4 as confirmed, and that no task still waits on any of them or names `via: auto` as the cascade's switch.
- That every path and symbol below exists on `origin/main` (or is marked **new**), and that no task claims a test protects what it does not.
- That every decision has a consequence in a task (the table "Decisions → tasks" below), every decision whose consequence lived in the declined slice lands in a Slice 1 task, and that no task needs something a later task builds.
- That the levels contain no two writers of one file, every "Depends on" line is an edge below and every edge is a "Depends on" line, and the critical path is the one named.

A reviewer of a **task** reads its own "Reviewer" line and the Review notes in it.

---

## Decisions → tasks

| Decision | Where it lands |
|---|---|
| D130 | C0.2 (supersession notes); C1.1 (trusted-repository opt-in, `followup.allow_public` refusal, skew constant); C1.16 (per-stage notional record) |
| D131 | C0.2 (project-centric §7 note); C3.9 (shared jobs) |
| D132 | Convention 10, Global Constraint 4, the TIER A marks (C1.7, C1.11, C1.12), the Risks section |
| D133 | C1.1 (the mode), C1.7 (the invariant) |
| D134 | C1.10 (verification; liveness from the write-once outcome), C1.11 (the repository's admission); Global Constraint 3 (both halves of the seal first) |
| D135 | C1.15 (gateway → service with an ID token), C1.18 (invoker) |
| D136 | C0.6, C1.3 (`creds/` denied to every run token), C1.9 (CAS, written by the service only), C1.11 — as amended by O1 |
| D137 | The slice order — as amended by O1 (Slice 1 skipped) |
| D138 | C1.10 (the typed answer's wire form), C1.11 (the service answers it), C1.17 (review sessions saved, wait, `--resume`); C3.1 (job death) |
| D139 | C1.11 (401 → broken); C3.7 |
| D140 | C1.16 (tier per call, notional A, real B/C, by-tier counters, switch log), C1.19 (`Record.OAuthAccount`, current credential), C1.21 |
| D141 | C1.11 (the service writes `broken`), C1.21 (visible; cleared by the operator); C3.2 |
| D142 | C1.2 (the two controls' settings), C1.9 (their enforcement: per-window stop and per-token concurrency leases, service-written), C1.11; C3.3 |
| D143 | C1.11 (no admin surface), C1.15 (halt when unreachable), C1.18 |
| D144 | C1.22 (the known limit stays); C3.4 |
| D145 | C1.18/C1.20 (model keys), C2.1–C2.3 (App key); Bitbucket and workflow `secrets:` untouched |
| D146 | C1.18, C1.20, C1.24 |
| D147 | C1.18 (opt-in variable), C1.20 |
| D148 | C1.22 |
| D149 | C1.15 (local `fugaro exec` untouched), C3.5 |
| D150 | C1.2 (the switch, `via: cascade` per O3), C1.19 (the launch reads it); C3.6 |
| D151 | C0.3–C0.5 |
| D152 | C1.2 (the setting and its validation), C1.11 (the service honours it) |
| D153 | C1.2 (policy only), C1.9 (backoff), C1.12 (the probe) |
| D154 | C1.9 (stickiness and the round-robin counter in `creds/`, service-written), C1.11 |
| O1 (ruling) | The whole plan; Convention 11; Global Constraint 11; C0.6, C1.3 |
| O2 (ruling) | C1.1 (`fugaro validate`), C1.19 (the launch) |
| O3 (answered: `via: cascade`) | C1.1 (the O2 error names it), C1.2 (`CheckRouting` accepts it without a provider route), C1.19 (the launch reads it) |
| O4 (confirmed) | C1.2 (admissions published), C1.11 (the service reads them) |

---

## File structure

### Created

| Path | Task | What it is |
|---|---|---|
| `internal/budget/rules/creds_emulator_test.go` (**new**) | C0.6 (extended by C1.3) | The emulator's denial cases for `creds/` |
| `internal/cli/docs_credentials_service_test.go` (**new**) | C0.1–C0.3, and the later tasks that write their checks into the register | The docs guards for this work: Checks 90 and 91, the register's block and rows, §7's lifting, m9 §11 and the plan's own status lines |
| `internal/budget/rules/marker.go` (**new**) | C1.3 | `CredsRulesVersion` and `config/limits/credsRules`, the marker a `cascade` launch requires |
| `internal/config/routing_shape_v1_test.go` (**new**) | C1.2 | A frozen, test-only copy of today's `CheckRoutingShape`, pinning what an older reader does with `via: cascade` |
| `internal/e2e/live_creds_rules_test.go` (**new**, tag `live`) | C1.4 | The owner-run helper for Check 92 |
| `internal/gateway/oauth.go` (**new**, **TIER A**) | C1.7 | The Anthropic-OAuth upstream kind and its header handling |
| `internal/gateway/export.go` (**new**) | C1.8 | The exported forwarding helpers the service reuses |
| `internal/creds/` (**new**) | C1.9 | The credential state: tiers, marks (available, exhausted until T, broken), selection (least-recently-exhausted, ties round-robin), stickiness, the two controls, backoff and the probe schedule; RTDB CAS through an interface, written as the service's own account |
| `internal/credsvc/` (**new**) | C1.10–C1.13, C2.1 | The service: run-identity verification and the wire contract (C1.10), the forward and the cascade (C1.11), the probe (C1.12), the audit log (C1.13), the GitHub mint endpoint (C2.1) |
| `internal/cli/credentials_serve.go` (**new**) | C1.14 | A hidden `fugaro` subcommand that runs the service in its image |
| `images/credentials/Dockerfile` (**new**) | C1.14 | The service image, on the `images/history` precedent |
| `deploy/terraform/gcp/modules/installation/credentials.tf` (**new**) | C1.18 | The opt-in service, its SA and its grants (the job SAs' invoker is in `modules/workflow/sa.tf`) |
| `internal/cli/init_credentials.go` (**new**) | C1.20 | The owner-run init stage, on the `init_compute.go` precedent |

### Edited (the shared ones; every task lists its own)

| Path | Written by (level) |
|---|---|
| `internal/budget/rules/rules.json.tmpl`, `testdata/rules.golden.json`, `creds_emulator_test.go` | C0.6 (L0, the test only), C1.3 (L2) |
| `internal/infra/firebasedb.go` | C1.3 (L2, writes the `credsRules` marker after the rules deploy) |
| `internal/gateway/gateway.go`, `route.go` | C1.7 (L2), C1.15 (L3), C1.11 (L4, `gateway.go`'s package comment only) |
| `internal/gateway/ledger.go` | C1.16 (L4) — and no other task |
| `internal/budget/model.go` | C1.3 (L2, `AgentEntry`), C1.16 (L4, by-tier paths) |
| `internal/runner/gateway.go` | C1.15 (L3), C1.19 (L4) |
| `internal/runner/runner.go` | C1.17 (L3), C1.19 (L4), C2.2 (L7) |
| `internal/runstore/runstore.go` | C1.17 (L3), C1.16 (L4) |
| `internal/infra/spec.go`, `readiness.go` | C1.20 (L4) |
| `internal/cli/accounts.go` | C1.21 (L4) |
| `deploy/terraform/gcp/modules/workflow/sa.tf` | C1.18 (L2), C2.3 (L3) |
| `deploy/terraform/gcp/modules/installation/credentials.tf` | C1.18 (L2, creates), C2.3 (L3) |
| `docs/design/m9-budget-and-dashboard.md` | C0.2 (L0), C1.3 (L2) |
| `SECURITY.md` | C1.22 (L5), C2.4 (L8) |
| `docs/gcp-live-checklist.md` | C0.3 (L0), C1.5 (L4), C1.23 (L6), C2.4 (L8) |
| `internal/cli/recipes_skew.go` | C1.1 (L3) |

---

## Parallel execution

### The edges

Every line is exactly one task's "Depends on" line below, and nothing else; **G** is the seal gate (both halves on `main` with 0.9.0 — #417, merged into `next-0.9`, and #523, open draft — and Checks 90 and 91 recorded).

```
C0.2  ← nothing                      C0.3 ← nothing           C0.6 ← nothing
C0.4  ← C0.3 (owner)                 C0.5 ← C0.3 (owner)
G     = #417 + #523 on main (0.9.0) + C0.4 + C0.5

C1.1  ← G, C1.2                      C1.3  ← G, C0.6
C1.2  ← G                            C1.4  ← C1.3
C1.5  ← C1.3, C1.4                   C1.6  ← C1.5 (owner)
C1.7  ← G                            C1.8  ← C1.7
C1.9  ← G, C1.2                      C1.10 ← G
C1.11 ← C1.2, C1.8, C1.9, C1.10      C1.12 ← C1.11, C1.13
C1.13 ← C1.11                        C1.14 ← C1.11
C1.15 ← C1.7, C1.10                  C1.16 ← C1.3, C1.15, C1.17
C1.17 ← C1.2, C1.3, C1.10            C1.18 ← G
C1.19 ← C1.1, C1.2, C1.15, C1.17     C1.20 ← C1.1, C1.3, C1.18
C1.21 ← C1.3, C1.9                   C1.22 ← C1.19, C1.20, C1.21
C1.23 ← C1.6, C1.14, C1.19, C1.20, C1.21
C1.24 ← C1.12, C1.13, C1.16, C1.22, C1.23 (owner; plus a release)

C2.1  ← C1.11, C1.13                 C2.2  ← C2.1, C1.15
C2.3  ← C1.18                        C2.4  ← C2.2, C2.3, C1.22, C1.23
C2.5  ← C2.4, C1.24 (owner; plus a release)
```

### Levels

| Level | Tasks | Reviewer | Files (disjoint within the level) |
|---|---|---|---|
| **L0** | C0.2 supersession notes · C0.3 Checks 90, 91 written · C0.6 `creds/` denial pinned | standard · standard · opus + mandatory | `docs/plans/2026-10-14-oauth-reviewer-stage.md`, `docs/design/mixed-credentials.md`, `docs/design/project-centric.md`, `docs/design/m9-budget-and-dashboard.md` · `docs/gcp-live-checklist.md` · `internal/budget/rules/creds_emulator_test.go` · `internal/cli/docs_credentials_service_test.go` (**new**) |
| **L1** | C0.4 Check 90 · C0.5 Check 91 | owner · owner | none (results recorded by the owner) |
| — | **G: the seal (both halves) merged — 0.9.0** | | |
| **L2** | C1.2 the policy settings · C1.3 the rules · C1.7 OAuth upstream (TIER A) · C1.10 identity verification · C1.18 Terraform | all SEC | `internal/projectpolicy/policy.go`, `internal/cli/project_cmd.go`, `internal/config/routing.go`, `routing_shape_v1_test.go` · `internal/budget/rules/*`, `internal/infra/firebasedb.go`, `internal/budget/model.go`, `docs/design/m9-budget-and-dashboard.md` · `internal/gateway/oauth.go`, `gateway.go`, `route.go` · `internal/credsvc/auth.go`, `wire.go` · `deploy/terraform/gcp/modules/*`, `deploy/terraform/gcp/roots/*/tests/` |
| **L3** | C1.1 the mode · C1.4 the live helper · C1.8 exported helpers · C1.9 the state package · C1.15 runner's service upstream · C1.17 waiting and review sessions · C2.3 App key grant | SEC · SEC · SEC · SEC · SEC · SEC · SEC | `internal/config/validate.go`, `layer.go`, `providers.go`, `config.go`, `schemas/fugaro.schema.json`, `internal/cli/recipes_skew.go`, `internal/cli/validate.go` · `internal/e2e/live_creds_rules_test.go` · `internal/gateway/export.go`, `export_test.go` · `internal/creds/` · `internal/gateway/gateway.go`, `route.go`, `internal/runner/gateway.go` · `internal/runner/session.go`, `runner.go`, `internal/runstore/runstore.go` · `deploy/terraform/gcp/modules/installation/secrets.tf`, `credentials.tf`, `modules/workflow/sa.tf` |
| **L4** | C1.5 Check 92 written · C1.11 the service forward and cascade · C1.16 accounting · C1.19 the wiring · C1.20 mounts and init stage · C1.21 visibility and clearing a mark | standard · SEC · SEC · SEC · SEC · opus + mandatory | `docs/gcp-live-checklist.md` · `internal/credsvc/forward.go`, `server.go`, `cascade.go`, `internal/gateway/gateway.go` (package comment only), `docs/plans/2026-09-30-m9a-gateway-and-project-identity.md` · `internal/gateway/ledger.go`, `internal/budget/lease.go`, `internal/budget/model.go`, `internal/runstore/cost.go`, `runstore.go` · `internal/runner/routing.go`, `internal/runner/gateway.go`, `agentenv.go`, `runner.go`, `internal/agent/env.go`, `internal/cli/run.go` · `internal/infra/spec.go`, `readiness.go`, `internal/cli/init.go`, `internal/cli/init_credentials.go`, `internal/cli/upgrade_cloud.go` · `internal/cli/accounts.go`, `doctor.go`, `internal/watch/json.go`, `render.go`, `console/docs/json-contract.md` |
| **L5** | C1.6 rules deploy and Check 92 (owner) · C1.13 audit · C1.14 cmd and image · C1.22 SECURITY.md, docs and skill | owner · SEC · SEC · opus + mandatory | none · `internal/credsvc/audit.go` · `internal/cli/credentials_serve.go`, `internal/cli/root.go`, `images/credentials/`, `docs/pending-workflows/` · `SECURITY.md`, `plugin/skills/working/reference/diagnose.md`, `docs/multi-model.md`, the guard tests |
| **L6** | C1.12 the probe · C1.23 Checks 94, 95, 99 written · C2.1 GitHub mint | SEC · standard · SEC | `internal/credsvc/probe.go` · `docs/gcp-live-checklist.md` · `internal/credsvc/github.go` |
| **L7** | C1.24 Checks 94, 95, 99 and the init stage (owner) · C2.2 runner fetches GitHub token | owner · SEC | none · `internal/gitprov/github/*`, `internal/runner/runner.go` |
| **L8** | C2.4 docs and Check 96 written | opus + mandatory | `SECURITY.md`, `docs/git-providers.md`, `docs/gcp-live-checklist.md` |
| **L9** | C2.5 Check 96 (owner) | owner | none |

**Widest levels: L3 (seven) and L4 (six).** No two tasks in a level write one file (the table above). Placements forced only by a shared file, not by logic: C1.15 sits at L3 because it shares `gateway.go` and `route.go` with C1.7; C1.16 at L4 because it shares `internal/runstore/runstore.go` with C1.17. **C1.8 writes only its two new files and never `ledger.go`** (review finding U2), and C1.16 — the only writer of `ledger.go` — is a level later. C1.11 touches `internal/gateway/gateway.go`'s package comment at L4, where no other task writes it. C1.20 needs C1.14's image only at the owner's apply (C1.24), not to be written.

### The critical path

**C0.3 → C0.4 (owner) → G (0.9.0's seal) → C1.2 → C1.9 → C1.11 → C1.13 → C2.1 → C2.2 → C2.4 → C2.5 (owner)** — ten steps, two of them owner-run (C0.4, C2.5), and the seal's wait between C0.4 and C1.2. An equal-length alternative runs C1.7 → C1.8 → C1.11. Slice 1's own end, C1.3 → C1.4 → C1.5 → C1.6 (owner) → C1.23 → C1.24 (owner), is two steps shorter (C1.24 at L7, C2.5 at L9). The longest wait on it is not a task at all: **the seal, held for 0.9.0**. Two things shorten the rest: C1.9 is a pure package (keep it small), and the owner-run checks are scheduled as soon as their text is written. The release that carries a runner change is a hidden step before every owner-run check of runner behaviour (C1.24, C2.5): the check runs a released or owner-built image.

### What must be sequential

1. **C0.4 and C0.5 before Slice 1** (D151). C1.7's header list *is* Check 90's FACT lines.
2. **The seal — #417 and #523 — on `main` before every Slice 1 task** (D134, O1).
3. **C1.3 before C1.4 before C1.5 before C1.6 before C1.24.** The deploy ships rules the emulator proved; the live checks need them deployed.
4. **C1.2 before every task that reads its settings** (threshold, `fallback`, backoff, the two controls, admissions, the `via: cascade` switch): C1.1, C1.9, C1.11, C1.17, C1.19 (review finding U1).
5. **C1.10 before C1.11, C1.15 and C1.17.** The wire contract (headers, the typed "exhausted until T" answer, the tier and credential-name response headers) is defined once.
6. **C1.17 before C1.19.** The wiring turns waiting on; waiting needs review-stage sessions saved.
7. **C1.18 before C1.20 before C1.24.** Terraform, then the init stage that applies it, then the owner's apply.

---

## Slice 0 — Verify first

*Can be done before 0.9.0, each on the owner's go.*

### Task C0.1: The owner's answers and rulings, written back — performed by this pull request

**Files.** `docs/credentials-service/reconciliation.md`, `README.md`, this file, `internal/cli/docs_credentials_service_test.go` (**new**: the guards for what this task writes back).
**Tests.** `internal/cli/docs_credentials_service_test.go`: `TestCredentialsStatusLinesRecordTheC0Approval` pins this task's own output — the status lines of `README.md` and this file, and the four other places that carried the same claim before the approval (this file's rules of engagement, its gate and the README's row for it, each anchored to its own line, plus `reconciliation.md`'s status line, which keeps its old wording with the correction beside it) — and `TestCredentialsPlanRecordsTheOpenDesignFindings` and `TestCredentialsDocsNeverStateTheSharedJobRefusalStays` pin the findings section and the lifting it records; all are red when the sentence they pin goes.
**Red step.** Each is mutation-proven: the sentence is edited back to what it said before (the status line back to "plan only", the correction removed from the constraint), the named guard fails, and the edit is reverted.
**Depends on.** The owner's answers, the O1/O2 rulings and the O3/O4 answers (2026-10-17). **Verification.** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1`. **Reviewer.** standard.
Writing the answers approves no task.

### Task C0.2: The supersession notes (D130, D131) — docs

**Files.** `docs/plans/2026-10-14-oauth-reviewer-stage.md` (its status banner: "superseded by docs/credentials-service (D130, 2026-10-17); Check 47 stays reserved, unused"; what carries over: P5/P6's trusted-repository opt-in and `followup.allow_public` refusal, P9's skew constant, P12's per-stage notional record), `docs/design/mixed-credentials.md` (Path B superseded; Paths A and C are tiers B and C), `docs/design/project-centric.md` §7 ("Mounting" and "Failover" superseded; "Storage" and "Reference" kept), `docs/design/m9-budget-and-dashboard.md` §11 (the external gateway is this work; the sidecar is dropped, F28).
**Interfaces / schema / rules.** None.
**Tests.** Whatever guard pins the edited text is updated in the same commit (`docs_project_centric_test.go`, `docs_named_oauth_tokens_test.go`), and `internal/cli/docs_credentials_service_test.go` (**new**) gains this task's own guards: `TestProjectCentricSection7RecordsTheLifting` (the owner's ruling of 2026-10-18, the static mount, and that the service cannot replace the stopgap) and `TestM9Section11AttributesTheSidecarDropToF28` (the finding that drops the sidecar, and the operator's write beside the service's).
**Red step.** Run the docs guards after the edit and before touching any guard; a red guard names the pinned sentence, which is then changed on purpose. Each guard added here is mutation-proven in the same way: the note is edited back to the superseded wording ("stays until then", "Dropped (D130"), the named guard fails, and the edit is reverted.
**Verification.** Convention 4's command.
**Depends on.** Nothing. **Reviewer.** standard.
**Commit.** `docs: mark mixed-credentials Path B and the OAuth reviewer-stage plan superseded by the credentials service (D130, D131)`

### Task C0.3: Checks 90 and 91, written into the register — docs

**Files.** `docs/gcp-live-checklist.md` (two register rows and two sections; the paragraph that records reserved numbers gains this work's block 90–99, with 93 reserved and unused), `internal/cli/docs_credentials_service_test.go` (**new**: the guards for both checks, one per claim below).
**What the owner types.**
- **Check 90** (no cloud): the guard clears the five environment variables Claude Code reads **before** the setup-token (`ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_BASE_URL`, `CLAUDE_CODE_USE_VERTEX`, `CLAUDE_CODE_USE_BEDROCK` — with Vertex set, as on the owner's machine, the control call authenticates through the cloud identity and never carries the credential this check swaps) and **stops** when a settings file configures an `apiKeyHelper`, which no environment variable clears, so the control can only succeed on the setup-token; build the throwaway proxy from a scratch directory outside any repository (a short program the section gives in full, listening on loopback, **refusing with 403 and not forwarding** any request whose `Host` is not that loopback address and port — loopback is reachable from any page the browser loads, so an unchecked proxy could be made to spend the owner's quota or, via DNS rebinding, read what it relays — replacing the incoming `Authorization`/`x-api-key` with the token read from a file the owner created with `claude setup-token`, logging header **names** only, never a header value (including which credential header the client actually sent, and — with its opt-in `--error-fields`, which step 7 turns on — the error object's own `type` and `message` of an error response, the only two keys of a body it reads); start it; run `ANTHROPIC_BASE_URL=http://127.0.0.1:<port> claude -p "say ok"` with a placeholder credential in the environment; then a long prompt; then the same with the proxy dropping each optional header in turn. `FACT:` lines: the call succeeded or not; which of the five variables the guard cleared (or that there was none) and that no `apiKeyHelper` is configured; the `FACT credential-header` line, which names the credential header the client sent (`authorization` vs `x-api-key` or neither) and is what says whether the control ran on the setup-token at all; the header names that had to pass; the status, the error object's own `type` and `message` and the reset headers of any usage-limit or rate-limit response seen (U3 — the two fields `--error-fields` reads, and the fix for a FACT that asked for a body no flag of the proxy could surface); Claude Code's timeout and retry behaviour on a proxy that delays, and — only if a usage limit is met — the client's own wording for it, which is U4's third part, F15's UNVERIFIED wording until a run records it (U4); the token's stated validity — which is U2's evidence, not its answer, since **U2 is settled by Anthropic's documentation** read by the owner. If no usage limit can be met in the session, the FACT records exactly that: **U3 stays open** and the 15-minute default back-off applies until Check 99 meets one. Restore: delete the scratch directory; the token file is the owner's to keep or delete.
- **Check 91** (installation `fugaro`, sandbox): a hard-coded `P` guard; deploy a throwaway Cloud Run service (`gcloud run deploy fugaro-chk91 … --project="${P:?}" --no-allow-unauthenticated --timeout=3600`) running a minimal streaming relay to a fake upstream that emits SSE for N minutes; create the **Artifact Registry repository** its image tag names — the baseline taken before anything is created **stops the check** when a repository of that name is already in the project, and the record it writes (`no`) is the ownership record step 3 and the Restore read back, so a repository the installation already had is never adopted, written into or deleted — and build and deploy from it; when `gcloud builds submit` offers to create its staging bucket (`gs://<project>_cloudbuild`) or enable an API on a Terraform-managed project, the step says no, stops and reports it rather than accepting on the owner's behalf; call it with a **fresh** `gcloud auth print-identity-token` **per request** (one minted token expires inside the hour, and this check waits longer than that); record time-to-first-byte (cold and warm, where **a cold figure counts only when the relay reports a different instance** from the one that answered before the 35-minute wait — the relay's `/` body carries its own process start time, and a matching pair is recorded as warm and repeated), whether events arrive unbuffered, the longest stream that completed, and the largest request body accepted on **HTTP/1.1 end to end** and what the **front end** does with an HTTP/2 request to it. **U5's end-to-end HTTP/2 half stays UNVERIFIED:** the second row is not an end-to-end HTTP/2 request, because the check deploys neither `--use-http2` nor a container that serves h2c, and the section says what a check that settles it must deploy. Restore: delete the service, its image and the registry repository it created — only after reading the ownership record, and **only when all three counts of the final assertion read `0`**; a non-zero count is a failure that **keeps** the scratch directory (it holds `service-mine`, `repo-existed` and the record), because removing it would make a re-run treat the check's own surviving service and repository as things it never created.
**Tests.** `internal/cli/docs_credentials_service_test.go` (**new**) — each assertion anchored to the sentence or the command that carries the claim, never to a section's heading: `TestChecklistCheck90GuardClearsTheClientCredentialEnvironment` and `TestChecklistCheck90ScratchDirectoryIsTheChecksOwn` and `TestChecklistCheck91ScratchDirectoryIsTheChecksOwn` (the guard's hard stops and the two Restore scratch-directory guards: the self-made `mktemp -d` directory, its marker, its record, both refusals ahead of the `rm -rf`, and the record removed with it), `TestChecklistCheck90RowDoesNotOverclaimU2AndU3` (what the register's row 90 may claim of U2 and U3), `TestChecklistCheck90ProxyPrintsOnlyTheErrorTypeAndMessage` (step 3's program, run out of the fenced block the owner copies: its error reader returns the error object's `type` and `message` and reads no other key, and a body carrying a credential-shaped value, a body that is not JSON, a body with no error object and an error object whose `type` is not a string each print only what it may) and `TestChecklistCheck90StepSevenNamesTheProxyFlagThatRecordsTheErrorType` (step 3's prose and step 7's command carrying the flag, the section's "never a value" sentence and its paste-back line bounding what a FACT may take from a body, and reconciliation's U3 row naming the flag) and `TestChecklistCheck90RecordsU4sThirdPartOnlyWhenALimitIsMet` (which parts of U4 the row, the section, this plan and reconciliation may say the check settles — the timeout and the retry count, with Claude Code's own usage-limit wording recorded only if step 7 meets a limit), `TestChecklistCheck91CreatesItsOwnRegistryRepository`, `TestChecklistCheck91MintsAnIdentityTokenPerProbe`, `TestChecklistCheck91BodyProbeReadsTheRelayBody`, `TestChecklistCheck91ColdStartProvesAFreshInstance`, `TestChecklistCheck91PinsTheProtocolOfEachRow` and `TestChecklistCheck91DoesNotClaimEndToEndHTTP2` (step 3's repository, the per-request token, step 6's body and its two rows, step 4's cold figure), `TestChecklistCheck91NeverAdoptsWhatItDidNotCreate` (hard stop 4, the `service-mine` record step 3 writes, the baseline's **stop** on a repository of this name that is already there — the deleted `yes` answer with it — and the order step 7 reads the record in, so neither the service nor the registry repository a check did not create is adopted, written into or deleted), `TestChecklistCheck91RestoreKeepsItsRecordsUntilTheCountsAreZero` (the three counts and the guard that stands between them and `rm -rf "$CHK91"`, so a failed delete does not lose the records a re-run needs), `TestChecklistCheck91BuildDoesNotAdoptWhatTheInstallationOwns` (the staging bucket and the API prompt: refuse, stop, report), `TestChecklistCheck90ProxyRefusesEveryHostButItsOwnLoopback` (the `Host` decision run out of the fenced program: its own address and port served, everything else refused, and the 403 ahead of the upstream connection) and `TestChecklistCheck90ProxyRecordsTheCredentialHeaderName` (the credential header's **name**, never a value, run out of the same program, and the step-4 FACT that asks for the line), `TestChecklistChecksSayTheirHardStopsEndTheShell` (both sections saying what `exit 1` does to the shell and carrying the subshell that keeps the window), `TestChecklistCheck91CommandsPassTheProjectExplicitly` (every `gcloud` command but `print-identity-token` naming `--project="${P:?}"`, and the guard pinning `P` to `fugaro-dev` with hard stops 1 and 2), `TestChecklistChecks90And91NeverNameTheOtherInstallations` (no `belong`, EdgeWeb or EdgeServer in either section's code, and only on a line that says they are never touched), `TestChecklistCheck91ServesAuthenticatedCallsOnly` (`--no-allow-unauthenticated`, never the flag without it, and a fresh identity token per probe), `TestChecklistChecks90And91PrintNoCredentialValue` (no command of either check prints the setup-token or a minted identity token), `TestChecklistCredentialsBlockIsCarvedOutOfTheWebsiteRange` (the 90–99 block against the website work's 69-and-up) and `TestChecklistCheck47IsSupersededNotHeld` (the three places the register held Check 47 against phase 5, which C0.2's D130 supersession moots — the paragraph, the row and the section banner, each anchored to its own line).
**Red step.** Every guard above is mutation-proven, which is what replaces the "no document has a red step" this task used to state: each defect is planted in the check (the guard loop losing `ANTHROPIC_AUTH_TOKEN`, the scratch path taken from the reader, the `rm -rf` moved above its two refusals in either Restore, the marker refusal deleted from a guard, the registry repository not created, hard stop 4 deleted, the `service-mine` record deleted, the `repo-existed` baseline deleted and its read moved below the scratch delete, the repository delete's `REPO_EXISTED` gate dropped, one identity token reused by every probe, the relay's body discarded by `-o /dev/null`, the row claiming to be end to end, five minutes of wait in place of thirty-five, `--http2` dropped from the second row, the client's own wording dropped from step 7's FACT while row 90 still claims the check settles U4, that wording dropped from reconciliation's U4 row alone, the error reader returning the whole parsed body or a third key, its `type` placeholder replaced by the value it holds, `--error-fields` dropped from step 7's command or from step 3's prose, the paste-back line back to an unqualified "never a body", the baseline answering `yes` to a repository it found, `rm -rf "$CHK91"` moved above the three-count guard, the `Host` refusal deleted from the proxy's forward path, the `FACT credential-header` line deleted, `CLAUDE_CODE_USE_VERTEX` (or the `apiKeyHelper` stop) deleted from Check 90's guard, `--project="${P:?}"` deleted from the build, the deploy, an image delete or a list, the guard's `P` renamed to another installation, hard stop 1 or 2 deleted, `--allow-unauthenticated` for its `no-` form, the proxy bound to `0.0.0.0`, a command printing the token — the named guard fails, and the edit is reverted. A guard that does not go red is not a guard. `TestCredentialsPlanC03ListsEveryChecklistGuardItsFileHas` reads the guard file and requires every checklist guard in it to be named in this block, so a guard added to the file and not to this line fails the docs guards.
**Verification.** Convention 4.
**Depends on.** Nothing. **Reviewer.** standard.
**Commit.** `docs(gcp-live-checklist): Checks 90 (OAuth header swap) and 91 (Cloud Run streaming), owner-run`

### Task C0.4: Check 90 — OWNER-RUN

**Files.** None (the owner records the FACT lines in the register). **Depends on.** C0.3. **Verification.** The owner runs Check 90 on the owner's own machine and Check 90 is recorded in the register. **Reviewer.** owner. **No agent performs any step.**
**If it fails:** Global Constraint 4.

### Task C0.5: Check 91 — OWNER-RUN

**Files.** None. **Depends on.** C0.3. **Verification.** The owner runs Check 91 on installation `fugaro`, its Restore ends in the all-`0` assertion, and Check 91 is recorded in the register. **Reviewer.** owner. **No agent performs any step.**

### Task C0.6: `creds/` denied to every run token, pinned against today's rules — SECURITY-SENSITIVE (test only)

**Files.** `internal/budget/rules/creds_emulator_test.go` (**new**, tag `firebase`).
**Interfaces / schema / rules.** None: the template is not changed. Today's root `.read`/`.write` false already denies every run token all of `creds/`; this task pins it, so the later rules change (C1.3) cannot open it by accident. It adds **no run-token grant** — the only rules groundwork the owner allowed before the seal.
**Tests.** `TestCredsRunCannotReadAnything`, `TestCredsRunCannotWriteAnything`, `TestCredsRunCannotDelete` (each against a seeded mark written as admin), `TestCredsTokenOfAnotherRepositoryDenied` (a second repository's token, and the owning repository's own token, are denied the same subtree — this is not a slug mismatch) and `TestCredsMailboxIdentitiesDenied` (the phone's signed-in owner and the mailbox consumer, so the closed subtree is closed to every non-admin token and not only to a run's). `TestCredentialsPlanC06ListsEveryCaseTheFileHas` (`internal/cli/docs_credentials_service_test.go`) reads that file and requires every case in it to be named here, so a case added to the file and not to this line fails the docs guards.
**Red step.** None possible: the tests pass on today's template by design. The review checks each would fail if a grant were added (by temporarily adding one locally, not committed).
**Verification.** `go test -tags firebase -count=1 ./internal/budget/rules/...` (needs the pinned `firebase-tools`; CI's `rules` job runs it).
**Depends on.** Nothing. **Reviewer.** opus + mandatory independent review.
**Commit.** `rules: pin that no run token can read or write creds/ (O1 groundwork, no rules change)`

---

## Slice 1 — The service and the cascade

*Starts after the seal — #417 (merged into `next-0.9`) and #523 (the launcher half, open draft) — is on `main` (D134; 0.9.0) and Checks 90 and 91 are recorded (D151). Opt-in per installation (D147). Per-repository jobs only (C3.9 for shared). The service is the only writer of the cascade's state (O1, Convention 11). No job mounts a tier-A token.*

### Task C1.1: `agent.auth: cascade`, its refusals and its skew constant — SECURITY-SENSITIVE

**Files.** `internal/config/validate.go` (`Validate`'s auth list), `internal/config/layer.go` (its auth list), `internal/config/providers.go` (`CheckProviderAuth`), `internal/config/config.go` (doc comment), `schemas/fugaro.schema.json`, `internal/cli/recipes_skew.go` (**new** constant `cascadeSince`), `internal/cli/validate.go` (the O2 refusal).
**Interfaces.** `agent.auth` gains `cascade`. A `cascade` workflow: needs budget mode `observe` or `enforce` (the gateway must be on; refused otherwise, naming the mode); is refused when `followup.allow_public` is true (D130 carry-over); keeps review mode's trusted-author gate; may name `agent.oauth_token` accounts only from its admitted set. `CheckProviderAuth` keeps refusing `oauth` with any provider route and refuses a `cascade` workflow whose provider route would carry a tier-A credential. **O2 (the owner's ruling, 2026-10-17):** a `cascade` workflow whose project policy does not turn the cascade on is **refused** — by `fugaro validate` here and at launch (C1.19) — with an error naming exactly what to set in the project policy and that only the operator can set it (`routing.claude: {via: cascade}`, the spelling O3 settled): a repository may ask for the cascade, only the operator-written project policy enables it. When `fugaro validate` cannot read the published policy it says so and does not pass the workflow silently.
**Tests.** `TestValidateAcceptsCascade`, `TestCascadeRefusedWithBudgetOff`, `TestCascadeRefusedWithAllowPublic`, `TestLayerAcceptsCascade`, `TestCheckProviderAuthCascadeNeverSendsOAuthToAProvider`, `TestValidateRefusesCascadeWithoutPolicySwitch` (O2; the message names the setting), `TestInitRepoRefusesCascadeOnOlderBaseImage` (the skew), schema corpus case.
**Red step.** Write `TestValidateAcceptsCascade` and `TestInitRepoRefusesCascadeOnOlderBaseImage` first; both fail on today's auth list.
**Verification.** `GOFLAGS=-p=2 go test ./internal/config/... ./schemas/... ./internal/cli -run 'Cascade|Skew|Validate|Schema' -count=1`.
**Depends on.** G; C1.2 (`TestValidateRefusesCascadeWithoutPolicySwitch` needs `via: cascade` and the admissions it publishes). **Reviewer.** opus + mandatory independent review.
**Commit.** `config: agent.auth cascade, refused without the gateway, with followup.allow_public, without the policy's switch (O2), and on an older base image`

### Task C1.2: The cascade's policy settings and the admissions, no policy version bump — SECURITY-SENSITIVE

**Files.** `internal/projectpolicy/policy.go` (a `Credentials` member: `fallback` = `oauth-only` | `auto`; `wait_threshold`; `backoff` (initial, cap); `stop_after_exhaustion_per_window`; `concurrency_per_token`; and, per repository in `config/policy/repos/$slug`, the **names** of its admitted Claude accounts — O4), `internal/cli/project_cmd.go` (`fugaro project policy publish`: validation, and publishing each repository's admitted account names from `config.ClaudeAccount.AllowDataTo`), `internal/config/routing.go` (`RoutingClaude.Via` gains `cascade`; `CheckRouting` accepts it with no provider claim while tier C for Claude is off — O3).
**Interfaces.** D152: `fallback` absent means `oauth-only`; `auto` is refused unless the policy's `budget.mode` is `enforce` with `per_run_usd` > 0. D153: these live only in the project policy (no `fugaro.yaml` key). Defaults: threshold 15 min, backoff 15 min doubling, capped at 5 h (probed, C1.12). D150 + O2: the policy's switch turns the cascade on for `cascade` workflows; for every other auth today's meaning of `routing.claude` is unchanged; tier C for Claude stays off (C3.6). **O3 (answered 2026-10-17):** the switch is `routing.claude: {via: cascade}`. It turns the cascade on for `cascade` workflows and needs no provider route while tier C for Claude is off (C3.6); `via: auto` and `CheckRouting`'s rule for it are unchanged, so an `api-key` workflow's routing does not move. **O4:** admissions are names only, never values; publishing them is how the service (an admin reader) learns which accounts each repository may use; a repository's run can read its own node today and gains nothing it could not already infer from its own job.
**Version skew — no bump.** `Version` stays 1 (owner's ruling, 2026-10-17). What an older reader does with the new members, verified in code: **an older runner** ignores them — `projectpolicy.Decode` lists an unknown top-level key in `UnknownKeys` instead of refusing it, and the runner logs the list and goes on (`internal/runner/projectpolicy.go`) — so it sees no cascade; nothing on `main` reads `config/policy/repos/$slug`, so the admissions reach no older reader at all. **An older CLI refuses, closed:** a shared node that lists a key it does not know makes `fugaro run` refuse **every launch of the project, cascade or not** ("lists credentials, which this Fugaro does not know … upgrade Fugaro", `internal/cli/launch_budget.go`), and makes `fugaro project policy publish` refuse (`internal/cli/project_cmd.go`). So the rationale for no bump holds for the runner only; for the CLI the effect is fail-closed, and **publishing a `credentials` member means every launcher upgrades the CLI first** — with the member unpublished, nothing changes (D147). **`via: cascade` is stricter:** it is a new value inside the known `routing` block, and an older reader's `config.CheckRoutingShape` (reached through `CheckRouting` from `Decode`) refuses any `via` other than `direct`, `openrouter` or `auto`, so a shared node carrying it is refused whole — by an older CLI at launch and publish, and by an older runner as a policy it cannot decode, which halts the run — with that routing error, not `SkewSentence`. Fail closed, but every run of the project on an older CLI or image stops until it is upgraded. The review of this task decides whether `fugaro project policy publish` warns before publishing either.
**Tests.** `TestPolicyCredentialsDefaults`, `TestPolicyFallbackAutoRefusedWithoutEnforceCap`, `TestPolicyCredentialsUnknownKeyRefused`, `TestPolicyAdmissionsPublishedNamesOnly`, `TestPolicyCredentialsIgnoredByVersion1Reader` (a node carrying `credentials` decodes as today with `Version` 1), `TestRoutingCascadeRefusedByOlderShapeCheck` (pins the flagged behaviour against a **frozen, test-only copy of today's `CheckRoutingShape`**, `internal/config/routing_shape_v1_test.go`, so the old reader's refusal stays pinned after this task changes the real function), `TestRoutingAutoMeaningUnchangedForAPIKey`, `TestRoutingCascadeNeedsNoProviderClaim` (O3).
**Red step.** `TestPolicyFallbackAutoRefusedWithoutEnforceCap` first.
**Verification.** `GOFLAGS=-p=2 go test ./internal/projectpolicy/... ./internal/config/... -count=1 && GOFLAGS=-p=2 go test ./internal/cli -run 'Policy|Routing' -count=1`.
**Depends on.** G. **Reviewer.** opus + mandatory independent review.
**Commit.** `projectpolicy: credentials fallback, threshold, backoff, the two controls and the admitted accounts (D142, D152, D153); policy Version unchanged`

### Task C1.3: The rules — `creds/` denied, the run's new children, by-tier counters — SECURITY-SENSITIVE

**Files.** `internal/budget/rules/rules.json.tmpl`, `internal/budget/rules/testdata/rules.golden.json`, `internal/budget/rules/parity_test.go` (if a mirror is needed), `internal/budget/rules/creds_emulator_test.go` (extended), `internal/budget/rules/marker.go` (**new**: `CredsRulesVersion` = 1 and the path `config/limits/credsRules`), `internal/infra/firebasedb.go` (`DB.Plan` writes that marker after `PutRules` succeeds, beside `policyRules`, and refuses a `credsRules` above `CredsRulesVersion`), `internal/budget/model.go` (`AgentEntry` gains the two optional fields below), `docs/design/m9-budget-and-dashboard.md` §6.2/§6.4.
**Rules change.** (1) An explicit `creds` node, `.read`/`.write` false, like today's `fugaro` node — **no run token reads or writes anything under it**; the service writes it as its own account (admin, rules bypassed, F17). (2) `agents/$slug/$run` gains two validated children its own run writes: `credential` (the current credential's **name**, never a value) and `waiting_until` (a timestamp, for the `oauth-only` "waiting, expected resume time" view — spec, D152). (3) The per-run and per-day counters gain a **by-tier split** of real spend (tier B and tier C nodes under `runs/$slug/$run` and `spend/$day/…`, validated like today's `spent`; tier A stays on the existing `notional` nodes) — the template is a closed allowlist, so these must be added here (review finding M5).
**Non-adopters (review finding M3), and no `RulesVersion` bump.** `rules.Generate()` takes no input, so one template serves every installation. **`projectpolicy.RulesVersion` stays 1**: it gates the policy nodes (`version.go`), which this change does not touch, and bumping it would make `projectpolicy.Read` (`gen < RulesVersion` → not found) drop every published policy — caps, `allow_repos`, routing, prices — on any installation whose owner has not redeployed the rules yet, and make `requirePolicyRules` refuse to publish there: not harmless to an installation that has not opted in. Instead: `creds/` is denied to everyone (no behaviour change: it is denied today); the new children are optional, validated children of nodes a run already writes; **only a `cascade` run writes them** (C1.16, C1.17, C1.19), so no non-cascade run ever needs the new rules; and the same owner-run deploy records a separate marker, `config/limits/credsRules` (readable by every run token, like `config/limits`), which the `cascade` launch (C1.19), the init stage (C1.20) and `doctor` (C1.21) require — a `cascade` run on an installation whose rules predate the children is refused before launch. The marker is written the way `policyRules` is: after `PutRules` succeeds, and also on the no-deploy path when it is absent (the deployed rules already equal the generated ones). A test pins that every pre-existing path's rule is unchanged. **KNOWN GAP — a rules downgrade.** An older Fugaro running `fugaro init --firebase` reads `policyRules` 1, equal to its own `RulesVersion`, so its downgrade refusal (`markerGen > RulesVersion` in `infra/firebasedb.go`) does not fire; it redeploys its older rules, which lack the new children, and leaves `credsRules` at 1, which it does not know. `cascade` launches then pass the marker gate and the run's first write of `credential`, `waiting_until` or a by-tier counter is denied mid-run. Mitigation: that denial is a **named halt** (C1.17's `HaltCredsRulesDenied`: the run fails with a message naming the rules redeploy, no silent retry), never a silent drop. This task's own refusal in `DB.Plan` of a `credsRules` marker above `CredsRulesVersion` guards only **future** releases; releases before this one cannot be taught it.
**Tests.** `TestRulesGolden`, `TestGeneratedRulesParse`, `TestRulesParity`, `TestRulesPreexistingPathsUnchanged` (every path of today's golden has a byte-identical rule after the change), `TestRulesVersionUnchanged` (`RulesVersion` is still 1), `TestFirebaseDeployWritesCredsMarker` and `TestFirebaseNoDeployWritesAbsentCredsMarker` (`internal/infra`), `TestFirebasePlanRefusesNewerCredsMarker`; emulator: `TestRunCannotWriteCredsRulesMarker` and `TestRunCannotWritePolicyRulesMarker` (a run token cannot write either marker under `config/limits`; `policyRules` has no such pin today, so this adds it); emulator (tag `firebase`): the C0.6 cases still pass, plus `TestAgentEntryCredentialNameValidated`, `TestAgentEntryWaitingUntilValidated`, `TestByTierCountersOwnRunOnly`, `TestByTierCountersMonotonic`, and the existing emulator suite unchanged (a non-opted-in run's writes behave identically).
**Red step.** `TestAgentEntryCredentialNameValidated` first (fails: no such child).
**Verification.** `go test ./internal/budget/rules/... -count=1 && GOFLAGS=-p=2 go test ./internal/infra/... -count=1 && go test -tags firebase -count=1 ./internal/budget/rules/...`.
**Depends on.** G; C0.6. **Reviewer.** opus + **MANDATORY** independent review (it changes what the database permits).
**Commit.** `rules: creds/ denied to every run token, the agent entry's credential name and waiting time, by-tier counters; the credsRules marker, RulesVersion unchanged`

### Task C1.4: The owner-run helper for Check 92 — SECURITY-SENSITIVE (test only)

**Files.** `internal/e2e/live_creds_rules_test.go` (**new**, build tag `live`, on the `live_budget_test.go` precedent).
**Interfaces.** `TestLiveCredsRules`: given the owner's environment variables (the database URL, the Firebase API key, the token signer — the same shape as Check 21's), mint a sandbox run's identity, exchange it, and attempt each denied read and write under `creds/` and each allowed and refused write of the new children; print `FACT:` lines; write nothing that survives (it uses a throwaway run id and deletes what it wrote as the owner).
**Tests.** It is the test; `go vet -tags live ./internal/e2e/...` compiles it in CI's lint (no run).
**Red step.** n/a (owner-run). **Verification.** `go vet -tags live ./internal/e2e/... && go test ./internal/e2e/... -count=1` (the untagged package still builds).
**Depends on.** C1.3. **Reviewer.** opus + mandatory independent review.
**Commit.** `e2e: the owner-run live helper for Check 92`

### Task C1.5: Check 92 written — docs

**Files.** `docs/gcp-live-checklist.md`. **What the owner types:** the hard-coded guard; `fugaro init --firebase <id>` (the Firebase project's ID: `--firebase` takes the project, it is not a switch), run at the owner's terminal against installation `fugaro`; the marker read; `FUGARO_LIVE_… go test -tags live -run TestLiveCredsRules -v ./internal/e2e/` (C1.4); Restore: nothing to undo but the throwaway run id's nodes, which the helper removes; the final assertion reads `0`. **Verification.** Convention 4. **Depends on.** C1.3, C1.4. **Reviewer.** standard.
**Commit.** `docs(gcp-live-checklist): Check 92, the rules deployed`

### Task C1.6: The rules deploy and Check 92 — OWNER-RUN

**Files.** None. **Depends on.** C1.5. **Verification.** The owner runs `fugaro init --firebase <id>` on installation `fugaro`, `config/limits/policyRules` still reads 1 and `config/limits/credsRules` reads 1, Check 92 is recorded. **Reviewer.** owner. **No agent performs any step.**

### Task C1.7: The Anthropic-OAuth upstream — **TIER A**, SECURITY-SENSITIVE

**Files.** `internal/gateway/oauth.go` (**new**), `internal/gateway/gateway.go` (`Upstream.Kind` gains `anthropic-oauth`; validation), `internal/gateway/route.go` (`attachCredential`'s OAuth case), `internal/gateway/oauth_test.go` (**new**).
**Interfaces.** An upstream that attaches a subscription OAuth credential with exactly the headers Check 90 recorded, to `anthropicBase` only. It runs **in the service** (C1.11), never in a job. **Invariant (D133):** `validateRoutes` and `attachCredential` refuse an OAuth credential on any route whose base URL is not Anthropic's; a credential is never logged (existing `scrubToken`/`redactWriter`).
**Tests.** `TestOAuthUpstreamAttachesRecordedHeaders` (against `anthropicfake`), `TestOAuthNeverSentToProviderRoute`, `TestOAuthCredentialRedactedFromErrors`, `TestOAuthUpstreamStreamsUnbuffered`. Fake tokens built at run time (Convention 2).
**Red step.** `TestOAuthNeverSentToProviderRoute` first.
**Verification.** `GOFLAGS=-p=2 go test ./internal/gateway/... -count=1`.
**Depends on.** G (C0.4's header list). **Reviewer.** opus + mandatory independent review.
**Commit.** `gateway: an Anthropic-OAuth upstream (tier A), and the invariant that an OAuth token goes only to Anthropic`

### Task C1.8: The exported forwarding helpers the service reuses — SECURITY-SENSITIVE

**Files.** `internal/gateway/export.go` (**new**), `internal/gateway/export_test.go` (**new**). **No other file**: in particular not `ledger.go`, `gateway.go` or `route.go`.
**Interfaces.** Thin exported wrappers over `Upstream`, `Route.attachCredential`, `validateRoutes`, `pump` and `isSSE`, so `internal/credsvc` forwards with the same code and the same no-logging rule; no new forwarding logic, no behaviour change in the gateway.
**Tests.** `TestExportedHelpersMatchInternal` (same headers, same refusals as the unexported path), `TestExportedAttachRefusesOAuthOnProviderRoute`.
**Red step.** `TestExportedAttachRefusesOAuthOnProviderRoute` first.
**Verification.** `GOFLAGS=-p=2 go test ./internal/gateway/... -count=1`.
**Depends on.** C1.7. **Reviewer.** opus + mandatory independent review.
**Commit.** `gateway: export the forwarding helpers for the credentials service, no behaviour change`

### Task C1.9: The credential state package — SECURITY-SENSITIVE

**Files.** `internal/creds/` (**new**: `state.go`, `select.go`, `marks.go`, `leases.go`, `errors.go` and tests).
**Interfaces.** `Tier` (A, B; C reserved); `Mark{State, Until, Since, Backoff}`; `Select(run, candidates, state, now) (Credential, wait time.Duration, err)` — sticky per run (D154: the run's current credential, stored under `creds/` by the service), new runs least-recently-exhausted with round-robin ties by a counter in `creds/`; **the two controls (D142)**: "stop for the rest of the window after one exhaustion" from the mark's history, and a **per-token concurrency cap** enforced by per-run leases under `creds/` with an expiry, so a stateless instance that dies never holds a slot past the lease (review finding M2); backoff doubling to its cap when no reset time is known, with the mark held **until a probe succeeds** (C1.12); `ExhaustedUntil`, the typed error. **Every write is the service's**, through an interface whose RTDB implementation uses `GetETag`/`PutIfMatch` as the service's own account, retrying on `ErrPrecondition`; nothing here is ever linked into the runner. `broken` is cleared only by the operator (C1.21).
**Tests.** `TestSelectSticky`, `TestSelectLeastRecentlyExhausted`, `TestSelectRoundRobinTies`, `TestStopAfterOneExhaustionPerWindow`, `TestConcurrencyCapLeases`, `TestLeaseExpiresAfterInstanceDeath`, `TestBackoffDoublesToCap`, `TestBackedOffMarkWaitsForProbe`, `TestMarkCASRetriesOnPrecondition` (fake DB), `TestBrokenNeverSelected`.
**Red step.** `TestSelectSticky` first.
**Verification.** `go test ./internal/creds/... -count=1`.
**Depends on.** G; C1.2 (the settings it enforces). **Reviewer.** opus + mandatory independent review.
**Commit.** `creds: credential state, selection, stickiness, the two controls and backoff, written by the service only`

### Task C1.10: Run-identity verification and the wire contract — SECURITY-SENSITIVE

**Files.** `internal/credsvc/auth.go`, `internal/credsvc/wire.go` (**new**: header names, error shapes, the response headers that carry the tier and the credential's name, and the wire form of `ExhaustedUntil` — an HTTP status, error type and reset field the runner reads), tests.
**Interfaces.** Verify the Firebase ID token for uid `r~<slug>~<run>` against Google's public keys (cached), `fp` against the service's project, `fx` not passed, and **the run's liveness from a node the run cannot undo**: the write-once outcome `outcomes/$day/$slug/$run` (rules `RUN && DAYOK && !data.exists()`: once written, no run token can rewrite or delete it), checked for every day from the run's launch day to today; an outcome present means the run has ended and is refused. **Not** `agents/<slug>/<run>`: the rules give the run token `.write: "RUN"` on that node, so a hostile agent can rewrite or recreate it, and it proves nothing (review finding W2). The outcome is written by the runner at its end (`internal/budget/registry.go`) and by the sweeper for a run that died (`internal/budget/sweep.go`); **until one of them has written it — a job killed before its end and not yet swept — `fx` is the only real bound.** The job SA's Google ID token is checked by Cloud Run IAM, not by this code. The repository's admission (O4) is checked by C1.11, when it would choose a tier-A credential, from the admissions C1.2 publishes; this task needs nothing from C1.2.
**Tests.** `TestVerifyAcceptsLiveRun`, `TestVerifyRefusesRunWithOutcome`, `TestVerifyIgnoresAgentsNode` (a rewritten `agents/` entry does not revive an ended run), `TestVerifyRefusesOtherProject`, `TestVerifyRefusesExpiredFX`, `TestVerifyRefusesUnknownKeyID`, `TestVerifyRefusesSharedJobRun` (C3.9's gate), `TestExhaustedUntilWireRoundTrip` — keys generated in the test.
**Red step.** `TestVerifyRefusesRunWithOutcome` first.
**Verification.** `go test ./internal/credsvc/... -count=1`.
**Depends on.** G. **Reviewer.** opus + **MANDATORY** independent review.
**Commit.** `credsvc: verify the run identity and its liveness from the write-once outcome (D134)`

### Task C1.11: The service's forward and the cascade — SECURITY-SENSITIVE (TIER A parts marked)

**Files.** `internal/credsvc/forward.go`, `internal/credsvc/server.go`, `internal/credsvc/cascade.go` (**new**), tests; `internal/gateway/gateway.go` (**the package comment only**) and `docs/plans/2026-09-30-m9a-gateway-and-project-identity.md` (a dated note on its two "never retries" lines), both for the retry below.
**Interfaces.** Reads secrets from Secret Manager as its own SA, `latest` with a short cache (D147); reads the project policy and each repository's admissions (O4) as its own account; **a tier-A credential is chosen only from the run's repository's admitted accounts** — the admission check applies only when a tier-A credential would be chosen, so a repository with none is never given tier A (under `fallback: auto` it goes to tier B; under `oauth-only` it is refused with a named reason), and with tier A dropped (Global Constraint 4) admissions are not consulted at all; no shell, no admin endpoint (D143); never logs a header or a body. Per call: `creds.Select` (sticky); attach (**TIER A**: the OAuth upstream of C1.7 for an admitted account); forward with C1.8's helpers. On a usage-limit response (status, type and reset headers per Check 90/U3) mark `exhausted until T` (or backed off, C1.9) and **retry the same request on the next credential only when no byte has been streamed to the client**; on 401 mark `broken` (D139); when nothing is usable and the wait is under the threshold, or `fallback` is `oauth-only` (threshold ignored, D152), answer the typed `ExhaustedUntil` (C1.10) with the earliest reset; every answer carries the tier and the credential's name (C1.10's headers), and a switch carries `{from, to, reason}`.
**The retry and the gateway's rule (review finding C2).** `internal/gateway`'s package comment and the m9a plan both say the gateway "never retries". The in-runner gateway still never retries; the **service** retries, once per credential and only before the first byte. This task changes the package comment to say so (the helpers it exports are reused by a forwarder that retries before the first byte, `internal/credsvc`) and adds the dated note to the m9a plan. **The reservation:** the in-runner gateway reserves once per client request, before it calls the service, at the worst case of the paid tiers; the service's internal retry happens inside that one request, so it is covered by that one reservation and settles once (C1.16) from the usage of the attempt that answered, at that attempt's tier; a usage-limit answer that was retried streamed nothing and is not settled.
**Tests.** `TestServiceForwardsWithSelectedCredential`, `TestServiceStreamsUnbuffered`, `TestServiceRefusesUnverified`, `TestServiceNeverGivesTierAToUnadmittedRepository` (O4), `TestServiceTierBOnlyIgnoresAdmissions` (Global Constraint 4), `TestServiceHasNoAdminRoutes`, `TestServiceNeverLogsHeaders`, `TestCascadeSwitchesOnUsageLimitBeforeFirstByte`, `TestCascadeNeverRetriesAfterStreamStarted`, `TestCascade401MarksBroken`, `TestCascadeOAuthOnlyWaits`, `TestCascadeAutoFallsToB`, `TestCascadeStickyAcrossCalls`, `TestCascadeUsesAdmittedAccountsOnly`.
**Red step.** `TestCascadeNeverRetriesAfterStreamStarted` first.
**Verification.** `GOFLAGS=-p=2 go test ./internal/credsvc/... ./internal/gateway/... -count=1` and Convention 4 (the plan note).
**Depends on.** C1.2, C1.8, C1.9, C1.10. **Reviewer.** opus + **MANDATORY** independent review.
**Commit.** `credsvc: forward with the selected credential, switch before the first byte, exhausted and broken marks, wait-or-switch`

### Task C1.12: The probe — SECURITY-SENSITIVE (TIER A parts marked)

**Files.** `internal/credsvc/probe.go` (**new**), tests.
**Interfaces.** The spec's resolved default: a credential backed off with no reset time is **probed with a minimal call before real work is routed to it again**; a failed probe doubles the wait to the 5 h cap, a successful probe clears the mark (C1.9). One probe per credential at a time (a lease under `creds/`); a probe is audited (C1.13) and accounted notional for tier A. Called from the selection path when a backed-off mark's time has passed; never on a credential marked `broken`.
**Tests.** `TestProbeClearsOnSuccess`, `TestProbeDoublesOnFailure`, `TestProbeCappedAtWindow`, `TestOneProbeAtATime`, `TestNoProbeOnBroken`.
**Red step.** `TestProbeClearsOnSuccess` first.
**Verification.** `go test ./internal/credsvc/... -count=1`.
**Depends on.** C1.11, C1.13 (a probe is audited). **Reviewer.** opus + mandatory independent review.
**Commit.** `credsvc: probe a backed-off credential before routing work to it (D153)`

### Task C1.13: The audit log — SECURITY-SENSITIVE

**Files.** `internal/credsvc/audit.go`, tests. **Interfaces.** One structured line per issuance and per probe: run, repository, credential **name**, purpose, tier, status; never a value. **Tests.** `TestAuditCarriesNamesOnly`, `TestAuditOnRefusal`. **Red step.** `TestAuditCarriesNamesOnly`. **Verification.** `go test ./internal/credsvc/... -count=1`. **Depends on.** C1.11. **Reviewer.** opus + mandatory independent review.
**Commit.** `credsvc: an audit line per issuance, names only`

### Task C1.14: The command and the image — SECURITY-SENSITIVE

**Files.** `internal/cli/credentials_serve.go` (**new**, hidden), `internal/cli/root.go` (one registration), `images/credentials/Dockerfile` (**new**), `images/` tests on the `images/history` precedent, `.goreleaser.yaml` if the image is released with Fugaro's, `docs/pending-workflows/images.yml` (**new**: the staged copy of `.github/workflows/images.yml` with the service image's build; the owner copies it, Convention 8).
**Tests.** `TestCredentialsServeHidden`, the image's size and scan tests (`images/size_budget_test.go`, `scan_test.go` patterns). **Red step.** `TestCredentialsServeHidden`. **Verification.** `GOFLAGS=-p=2 go test ./internal/cli ./images -run 'Credentials|Size|Scan' -count=1`. **Depends on.** C1.11. **Reviewer.** opus + mandatory independent review.
**Commit.** `credsvc: the hidden serve command and its image; the workflow change staged for the owner`

### Task C1.15: The runner's service upstream — SECURITY-SENSITIVE

**Files.** `internal/gateway/gateway.go`, `route.go` (an upstream kind `service`: the job SA's ID token from the metadata server in `Authorization`, the run identity in the wire contract's header; the tier, credential name and `ExhaustedUntil` read from the service's answer and handed to the ledger hook and the runner), `internal/runner/gateway.go` (choose it when the installation has the service on), tests.
**Interfaces.** Service unreachable → the run halts like `budget_unavailable` (D143), never an in-job key. The in-runner gateway still never retries. Local `fugaro exec` unchanged (D149).
**Tests.** `TestServiceUpstreamSendsIDTokenAndRunIdentity`, `TestServiceUpstreamReadsTierAndCredential`, `TestServiceUnreachableHalts`, `TestNoInJobFallback`, `TestExecUnchanged`.
**Red step.** `TestNoInJobFallback`. **Verification.** `GOFLAGS=-p=2 go test ./internal/gateway/... ./internal/runner/... -count=1`. **Depends on.** C1.7 (shares `gateway.go`, `route.go`), C1.10. **Reviewer.** opus + mandatory independent review.
**Commit.** `gateway, runner: the service upstream, halting when it is unreachable (D135, D143)`

### Task C1.16: Per-call accounting and the switch log — SECURITY-SENSITIVE

**Files.** `internal/gateway/ledger.go`, `internal/budget/lease.go`, `internal/budget/model.go` (the by-tier paths C1.3's rules allow), `internal/runstore/cost.go` (per-stage notional and real split, the held plan's P12 shape), `internal/runstore/runstore.go` (switch log in the record), tests.
**Interfaces.** The tier comes from the service's answer (C1.15). A tier-A call settles as notional (`AddNotional`), B/C as real against the caps; per-run and per-day counters gain the by-tier split, **written only by a `cascade` run** (C1.3: no other run needs the new rules); the switch log carries `{from, to, reason, estimated cache-miss cost}` computed here from `internal/pricing` (F24: "unmeasured" for C). One reservation per client request covers the service's retry (C1.11). `cost.model_usd`'s documented sum invariant holds.
**Tests.** `TestSettleTierANotional`, `TestSettleTierBReal`, `TestCapIgnoresNotional`, `TestOneReservationCoversServiceRetry`, `TestSwitchLogRecorded`, `TestModelUSDSumInvariantMixedRun`.
**Red step.** `TestCapIgnoresNotional` first.
**Verification.** `GOFLAGS=-p=2 go test ./internal/gateway/... ./internal/budget/... ./internal/runstore/... -count=1`.
**Depends on.** C1.3, C1.15, C1.17 (shares `runstore.go`). **Reviewer.** opus + mandatory independent review.
**Commit.** `gateway, budget: per-call tier accounting, notional A and real B/C, and the switch log (D140)`

### Task C1.17: Review-stage sessions saved, and the runner's wait — SECURITY-SENSITIVE

**Files.** `internal/runner/session.go` (save the review stage's session too, redacted), `internal/runner/runner.go` (`stage`: on `ExhaustedUntil` end the stage, wait within the job's remaining timeout, resume with `--resume`; under `oauth-only` wait for the earliest reset whatever the threshold; write `waiting_until` on the run's agent entry while waiting (a `cascade` run only, C1.3) and clear it on resume; halt with a named reason when the wait would exceed the timeout), `internal/runstore/runstore.go` (the new halt reason), tests.
**Interfaces.** Two new halt reasons: one for "no credential before the job's timeout", and `HaltCredsRulesDenied` for a `cascade` run whose write of `credential`, `waiting_until` or a by-tier counter is refused by the deployed rules (C1.3's known gap: the run fails with a message naming `fugaro init --firebase` with this release, and the write is not retried); C1.16 and C1.19 halt with it too. No resume after job death (C3.1). The runner only reacts to the service's answer; it writes no mark.
**Tests.** `TestReviewSessionSaved`, `TestStageWaitsAndResumes`, `TestOAuthOnlyIgnoresThreshold`, `TestWaitingUntilWrittenAndCleared`, `TestWaitBeyondTimeoutHalts`, `TestDeniedCascadeWriteHaltsNamed`, `TestSessionRedacted` (existing redaction applies).
**Red step.** `TestStageWaitsAndResumes` first.
**Verification.** `GOFLAGS=-p=2 go test ./internal/runner/... ./internal/runstore/... -count=1`.
**Depends on.** C1.2 (threshold, `fallback`), C1.3 (`waiting_until`), C1.10 (the error's wire form). **Reviewer.** opus + mandatory independent review.
**Commit.** `runner: save review-stage sessions and wait for a credential with --resume (D138)`

### Task C1.18: Terraform — the service, its SA, the grants moved — SECURITY-SENSITIVE

**Files.** `deploy/terraform/gcp/modules/installation/credentials.tf` (**new**: Cloud Run service with ingress requiring IAM, its image a variable, its SA, its `secretAccessor` on model secrets only), `modules/installation/variables.tf` (`credentials_service` opt-in, default off), `modules/workflow/sa.tf` — where the job SAs are created (`google_service_account "job"`) and the only job `secretAccessor` grant lives (`google_secret_manager_secret_iam_member "job"`, one per mounted secret; `modules/repo/secrets.tf` grants operators only): model secrets dropped from it **only when** the opt-in is on, and the job SA's `run.invoker` on the service added there — `modules/workflow/variables.tf` and `modules/repo/workflows.tf` (the service's name and region passed down to the workflow module), `roots/installation/tests/installation.tftest.hcl` and the repo root's tests.
**Tests.** Terraform tests: off → plan identical to today; on → job SA has no model-secret accessor and has invoker; service SA has nothing on the runs bucket and no `actAs` granted to non-operators.
**Red step.** The "on" test first. **Verification.** `terraform fmt -check -recursive deploy/terraform && go test -tags terraform ./internal/infra/... -count=1` and `terraform test` in `roots/installation` and `roots/repo`. **Depends on.** G. **Reviewer.** opus + **MANDATORY** independent review.
**Commit.** `terraform: an opt-in credentials service in the installation root; model-secret access moves to it (D146, D147)`

### Task C1.19: The wiring — gateway on, routing, the record — SECURITY-SENSITIVE

**Files.** `internal/runner/routing.go` (`RoutingInertReason` and `applyRouting` for `cascade`; **the O2 refusal at launch**, naming the setting), `internal/runner/gateway.go` (`gatewayOn` true for `cascade`; the only upstream is the service), `internal/runner/agentenv.go` (no credential reaches the agent's env), `internal/runner/runner.go` (write `Record.OAuthAccount` and the current credential's name on the agent entry from the service's answer — a `cascade` run only, C1.3), `internal/agent/env.go` (`GatewayVars` for `cascade`), `internal/cli/run.go` (one job per workflow, no per-account variant for `cascade`; refused on an installation without the service, and without the `credsRules` marker, C1.3), tests.
**Interfaces.** A `cascade` run mixes credentials only inside the service; no tier-A token is mounted in the job.
**Tests.** `TestCascadeRefusedWithoutPolicySwitch` (O2, at launch), `TestCascadeRefusedWithoutService`, `TestCascadeRefusedWithoutCredsRulesMarker`, `TestCascadeAgentEnvHoldsNoCredential`, `TestCascadeRecordsOAuthAccount`, `TestCascadeWritesCurrentCredentialName`, `TestGatewayOnForCascade`.
**Red step.** `TestCascadeAgentEnvHoldsNoCredential` first.
**Verification.** `GOFLAGS=-p=2 go test ./internal/runner/... ./internal/agent/... ./internal/cli -run 'Cascade|Routing|Gateway|Account' -count=1`.
**Depends on.** C1.1, C1.2, C1.15, C1.17. **Reviewer.** opus + **MANDATORY** independent review.
**Commit.** `runner: wire agent.auth cascade — the service upstream, the policy switch (O2), the record`

### Task C1.20: The mounts and the owner-run init stage — SECURITY-SENSITIVE

**Files.** `internal/infra/spec.go`, `readiness.go` (no model secret mounted when the service is on; a `cascade` workflow mounts no tier-A token in any case), `internal/cli/init.go` and `internal/cli/init_credentials.go` (**new**, on the `init_compute.go` precedent: a flag like `--provision-batch`, typed at a real terminal, never covered by `--yes`, refused under `agentMarker`; **refused without the `credsRules` marker of C1.3**, naming `fugaro init --firebase`; the service's image is a Terraform variable of C1.18's `credentials.tf`, on the `history.tf` precedent (`var.history.image`), which the stage fills with the released image's reference, so this task needs C1.14's image only when the owner applies it, C1.24), the upgrade note in `internal/cli/upgrade_cloud.go` (a note on `fugaro upgrade`'s cloud step, on the `jobConfigCopyPolicyNote` precedent), tests.
**Tests.** `TestNoModelMountsWithService`, `TestCascadeMountsNoOAuthToken`, `TestInitCredentialsStageRefusedInAgent`, `TestInitCredentialsStageNotCoveredByYes`, `TestInitCredentialsStageRefusedWithoutCredsMarker`, `TestUpgradeNoteNamesTheStage`. **Red step.** `TestInitCredentialsStageRefusedInAgent`. **Verification.** `GOFLAGS=-p=2 go test ./internal/infra/... ./internal/cli -run 'Credentials|Mount|Init|Upgrade' -count=1`. **Depends on.** C1.1, C1.3 (the marker), C1.18. **Reviewer.** opus + mandatory independent review.
**Commit.** `infra, init: no model-secret mounts with the service on; an owner-run init stage to apply it`

### Task C1.21: Visibility — `watch`, `fugaro accounts ls`, `doctor`; clearing a mark — SECURITY-SENSITIVE

**Files.** `internal/cli/accounts.go` (state column: available, exhausted until T, backed off, broken; and a subcommand that **clears** a `broken` or `exhausted` mark after the operator replaced the token — name settled in review — writing `creds/` as the operator, an admin, since the service has no admin endpoint), `internal/cli/doctor.go` (a broken admitted account is a finding; so is the service on without the `credsRules` marker), `internal/watch/json.go` and `render.go` (the current credential's name; a waiting run shown as "waiting for a credential, expected resume T" from `waiting_until`), `console/docs/json-contract.md` (the two new fields), the plugin's command lint follows any new subcommand, tests.
**Interfaces.** Names and states only, never values (F27). The clear writes only the named account's mark in the operator's own installation's database, through `PutIfMatch`.
**Tests.** `TestAccountsLsShowsStates`, `TestAccountsClearMark`, `TestAccountsClearOnlyNamedAccount`, `TestDoctorReportsBroken`, `TestWatchJSONCurrentCredential`, `TestWatchShowsWaitingUntil`, the plugin lint.
**Red step.** `TestAccountsLsShowsStates` first.
**Verification.** `GOFLAGS=-p=2 go test ./internal/cli ./internal/watch/... ./plugin/... -count=1`.
**Depends on.** C1.3, C1.9. **Reviewer.** opus + **MANDATORY** independent review (the operator's write to `creds/`; review finding C1).
**Commit.** `cli: account states in accounts ls, doctor and watch; the waiting view; clearing a mark (D141)`

### Task C1.22: SECURITY.md, docs and skill — SECURITY-SENSITIVE (security claims)

**Files.** `SECURITY.md` (D148's sentence, "the only Fugaro runtime identity with access to model credentials", and the others named: the build SA, operators, Project Owners/Editors; F34's claims rewritten for an installation with the service on; the invariant; the run identity's revocation stated exactly — the write-once outcome, `fx` as the only bound until it is written; egress stays a known limit, D144), `plugin/skills/working/reference/diagnose.md` §5 (the relaunch advice does not apply to a `cascade` run), `docs/multi-model.md` (the mode and the policy switch), the guard tests that pin changed text (`docs_security_test.go`, `docs_website_security_test.go`, `docs_usage_limit_switch_test.go`).
**Tests.** The guards, updated on purpose; `go test ./plugin/...`.
**Red step.** Edit, run the guards, update only the pins that name the changed claims.
**Verification.** Convention 4's command, plus `go test ./plugin/... -count=1`.
**Depends on.** C1.19, C1.20, C1.21. **Reviewer.** opus + **mandatory** independent review (it changes SECURITY.md's security claims; review finding C1).
**Commit.** `SECURITY.md, docs: the credentials service and the cascade, stated exactly (D148)`

### Task C1.23: Checks 94, 95 and 99 written — docs

**Files.** `docs/gcp-live-checklist.md`; each step's exact command — Check 94: the init stage, `gcloud … get-iam-policy` reads, the audit log read; Check 95: a sandbox run, the `/proc` inspection from the run's own log, the ended-run refusal, the unreachable-service case by scaling the service to zero instances; Check 99: two admitted test accounts and the sandbox API key, the policy published with the switch and `credentials.fallback: auto` with an enforce cap, a forced exhaustion by the operator's mark, a review run (`fugaro run …`), the probe, the `oauth-only` waiting view, `fugaro watch`, `fugaro accounts ls`, `fugaro doctor` read back, a revoked test token for the broken case and the operator's clear; Restore re-publishes the recorded policy, clears marks, and ends in the all-`0` assertion.
**Verification.** Convention 4's command.
**Depends on.** C1.6, C1.14, C1.19, C1.20, C1.21. **Reviewer.** standard.
**Commit.** `docs(gcp-live-checklist): Checks 94, 95 and 99, the credentials service and the cascade`

### Task C1.24: The init stage, Checks 94, 95 and 99 — OWNER-RUN

**Files.** None. **Depends on.** C1.12, C1.13, C1.16, C1.22, C1.23, and a release (or owner-built image) carrying Slice 1. **Verification.** The owner runs the init stage and Checks 94, 95 and 99 on installation `fugaro`, each Restore ends in the all-`0` assertion, and Checks 94, 95 and 99 are recorded in the register. **Reviewer.** owner. **No agent performs any step.**

---

## Slice 2 — GitHub App tokens through the service

### Task C2.1: The mint endpoint — SECURITY-SENSITIVE

**Files.** `internal/credsvc/github.go` (**new**), reusing `github.MintInstallationToken` and `tokenPermissions` (no permission added); the repository is bound to the run identity's `fs`. **Tests.** `TestMintForOwnRepositoryOnly`, `TestMintPermissionsAreTokenPermissions`, `TestMintRefusesEndedRun`, `TestMintAudited`. **Red step.** `TestMintForOwnRepositoryOnly`. **Verification.** `go test ./internal/credsvc/... ./internal/gitprov/... -count=1`. **Depends on.** C1.11, C1.13 (`TestMintAudited`). **Reviewer.** opus + mandatory independent review.
**Commit.** `credsvc: mint a GitHub installation token for the run's own repository (D145)`

### Task C2.2: The runner fetches its GitHub token from the service — SECURITY-SENSITIVE

**Files.** `internal/gitprov/github/apptoken.go`, `github.go` (`GitAuth` from the service when it is on), `internal/runner/runner.go` (`refreshGitAuth`), tests. Bitbucket unchanged. **Tests.** `TestGitAuthFromService`, `TestNoAppKeyReadWithService`, `TestBitbucketUnchanged`. **Red step.** `TestNoAppKeyReadWithService`. **Verification.** `GOFLAGS=-p=2 go test ./internal/gitprov/... ./internal/runner/... -count=1`. **Depends on.** C2.1, C1.15. **Reviewer.** opus + mandatory independent review.
**Commit.** `github, runner: installation tokens from the service; the job reads no App key`

### Task C2.3: The App key's grant moves — SECURITY-SENSITIVE

**Files.** `deploy/terraform/gcp/modules/installation/secrets.tf`, `credentials.tf`, `modules/workflow/sa.tf` (job SAs lose `secretAccessor` on `github-app-key` when the opt-in is on; the build SA keeps what `modules/repo/build.tf` gives it, F3), Terraform tests. **Verification.** As C1.18. **Depends on.** C1.18. **Reviewer.** opus + **MANDATORY** independent review.
**Commit.** `terraform: the GitHub App key is read by the service only, with the service on`

### Task C2.4: Docs and Check 96 written — security claims

**Files.** `SECURITY.md`, `docs/git-providers.md` ("None of these variables reaches the agent", made exact, F34), `docs/gcp-live-checklist.md` (Check 96), guards. **Verification.** Convention 4's command. **Depends on.** C2.2, C2.3, C1.22, C1.23. **Reviewer.** opus + **mandatory** independent review (SECURITY.md's claims; review finding C1).
**Commit.** `docs: GitHub tokens through the credentials service; Check 96`

### Task C2.5: Check 96 — OWNER-RUN

**Files.** None. **Depends on.** C2.4, C1.24, a release carrying Slice 2. **Verification.** The owner runs Check 96 on installation `fugaro` and the sandbox repository, its Restore ends in the all-`0` assertion, and Check 96 is recorded in the register. **Reviewer.** owner.

---

## Slice 3 — Later, unscheduled

Each needs its own plan amendment and the owner's go.

- **C3.1 Resume after job death** (D138): periodic session upload during a stage, a resumable-run record, a relauncher taking over the run id and branch; Check 98.
- **C3.2 Alerts** to a token's owner and project admins (D141), once a notification channel and phase 5's owner record exist.
- **C3.3 The share model** — N sessions per window, per-token counters (D142).
- **C3.4 Egress restriction** — a VPC with egress rules (D144).
- **C3.5 The Batch backend** — VM SA gets invoker only (D149) when `backend: cloud-batch` is selectable; Check 97.
- **C3.6 Tier C for a Claude model** — only after Check 50 has measured cache markers (D150).
- **C3.7 OAuth refresh** with a per-token lock and Secret Manager version CAS — only if U2 shows a refreshable credential (D139).
- **C3.8 Proxied push and PR creation** (D145, optional).
- **C3.9 Shared jobs** — the service admits runs on phase-3 profile jobs only after the phase-3 shared-job rules are on `main`, and the reference record of project-centric §7 then becomes the tier-A registry and the source of admissions (D131, O4). **Task 3.4a's refusal does not wait for this**: the owner's ruling of 2026-10-18 lifts it in 0.9.0 through the static `secret_env` mount ([project-named-oauth-tokens.md](../design/project-named-oauth-tokens.md) §3), accepted as a stopgap. What waits for C3.9 is the service taking a shared job's tokens over — before it, no run on a shared job reaches the service at all (Global Constraint 6), so 0.9.0 runs the two carriers side by side.

---

## Open design findings

**Findings, not decisions.** The independent review of 2026-10-19 (PR #584), reading this plan against the code, raised the findings below against the service's trust model. None is a decision — they carry no D-number — and none is a task, and none blocks Slice 0. **Each must be answered, by a ruling or an amendment or a task added where it belongs, before C1.10, C1.11 and C1.16 are built**, because every one of them is a property of what those three tasks implement. Each is stated with the reasoning that raised it and the options, without a recommendation.

1. **The caps are enforced on the attacker's side.** D134 has a run's calls through the service bounded by the run's own budget. But the identity a run presents is not a secret the agent cannot reach: the agent process *is* the run, so it can read its own run identity (`r~<slug>~<run>`, `fp`, `fx`) out of `/proc` and the job account's Google ID token off the metadata server, and then call the service **directly** — its own request, its own loop, no gateway and no ledger. Verification (C1.10) proves the run is live; it does not prove the call went through the gateway, and it cannot tell the service's own forwarding from the agent's. **So "bounded by caps" holds only if the service itself reserves and settles** on every call it forwards, which is not how C1.16 as written splits the work. Options: move the reservation and the settlement into the service; keep them in the runner and state plainly that real spend through a hostile direct caller is bounded by nothing but the credential's own limits; or bind a forwarded call to a gateway-held secret the agent never sees (a per-run nonce minted in the gateway and checked by the service, which the runner's own report must then carry).
2. **There is no binding between the caller's account and the run.** C1.10 verifies the run identity and C1.11 picks the credential from the repository's admitted accounts, and nothing ties the two: any run of the project can present its identity and be served a tier-A credential the repository admits, and — the other direction — any job account's ID token of the same project pairs with any live run identity, because C1.10 checks the token's project (`fp`) and not that the run's job is the job the token belongs to. Under phase 3 (one job per (profile, provider, account), its account fixed by the job's own name) the job *is* the account and the pairing is the point; per repository it makes a run's tier a property of the repository rather than of the job that asked. Options: bind the presentation to the presenting service account (Cloud Run IAM hands the service the caller's identity, so C1.10 can compare it with the run record's job); or state the property as intended — tier A is a repository-level admission and the project's runs are trusted with one another to the extent the policy admits.
3. **Launcher reach.** Anyone who can launch in the project can start a run on any repository whose profile uses a shared job, and through it reach **any repository's admitted tier-A accounts**: the admissions C1.2 publishes are per repository, and the launcher check is the CLI's and trusted by design (project-centric.md's D10), so a launch check alone is no boundary — the same reason the runner repeats every gate. Options: state the reach as accepted with the launcher's trust, beside the OAuth-spend risk this plan already carries; admit per launcher; or admit only a repository's own accounts until the phase-3 rules land.
4. **Timing windows.** A streaming call can **outlive the run** it belongs to by as much as Cloud Run's request timeout: liveness (C1.10) is checked when the call arrives, and a run that ends mid-stream leaves the response flowing, billed to a credential and a run that is over. The window's size is whatever Check 91 measures as the longest completed stream. Options: state the window as accepted and give it the measured number; cap a forwarded call at a lifetime the service sets, shorter than the run's; or re-check liveness periodically while a stream is open.
5. **No break-glass.** No task here stops tier A, or one named token, for the whole project in a single operator action. The two controls of D142 (C1.2, C1.9) are per token — stop for the window after one exhaustion, concurrency per token; the operator's clear (C1.21) clears a mark rather than disabling a credential; and retiring an account is a policy publish. Options: an operator-written project policy switch that turns tier A off project-wide; a per-account `disabled` mark the service and the launcher both honour; or both, with a `doctor` finding for each.
6. **How the service gets read access to a newly added token.** The service is the only reader of Secret Manager (D148), so its own service account needs `secretAccessor` on every (person, account) secret — including the ones added after it is deployed. If that grant is scoped by an IAM **condition** on the secret's name prefix, the condition is the whole boundary and needs the scrutiny the 0.8.6 deployer fence got: IAM conditions allow at most 12 logical operators, only `startsWith`, `endsWith`, `extract`, `==` and `!=` on `resource.name`, and only for the services that support them — what Secret Manager supports is UNVERIFIED here. Options: a prefix condition reviewed as a fence in its own right; one grant per secret written by the operator's script when the reference is accepted (project-centric.md §7's operator step); or a container project holding the secrets, with the service granted at the container.
7. **Callers the service does not cover today.** Three designs in flight give it callers it has no path for: the voice work (PR #636 — provider keys at project level and short-lived **provider** tokens, whose caller is not a run), the **coordinator** (PR #638 — a **long-lived** caller, which C1.10's "the run is live and has not ended" refuses by construction) and the **orchestrator** (PR #632). Cross-reference them here, so the shape is decided once with the service's identity model in hand instead of per feature: whether the service accepts callers that are not runs, carries credentials that are not Anthropic's, and serves a principal that outlives a run.

**Two wording defects the same review found were fixed in place rather than listed open:** [m9-budget-and-dashboard.md](../design/m9-budget-and-dashboard.md) §11 called the service "the only writer" of `creds/`, which the operator's own clear (C1.21) contradicts — it now says the only writer **at run time** — and attributed the gateway sidecar's drop to D130, when the finding that drops it is **F28**.

## Risks

- **D132 — accepted owner risk.** The owner chose to proceed without first confirming Anthropic's current terms for sharing individuals' subscription OAuth tokens across a project and sending their traffic through a Fugaro-operated proxy. This plan states no view on what those terms say. Tier-A-through-a-proxy work may later have to be removed, and the token holders' accounts are exposed in the meantime. Mitigation by construction: TIER A code is isolated (Convention 10), so removing it is deleting C1.7's files and the TIER A parts of C1.11 and C1.12.
- **The whole work waits for 0.9.0** (O1 + D134): no product value before the seal, both halves, has merged.
- **False marks are a cost shift, not only a denial of service** (review finding W3). With `fallback: auto`, a credential wrongly marked exhausted or broken pushes every run onto paid tier B, bounded only by the caps. Under O1 no run can write a mark, so this needs the service itself to misjudge a response; the classification of usage-limit responses (C1.11, Check 90's U3) is reviewed with that cost in mind.
- **Liveness is bounded by `fx` until an outcome is written** (W2): a run killed before its end and not yet swept keeps a valid identity until `fx`.
- **The service is a single point** of compromise and availability (D143), accepted with its bounds.

---

## OWNER APPROVAL GATE

**Passed for Slice 0, which is built; not passed for anything else — no task of Slice 1, 2 or 3 is approved.** The owner answered D130–D154, ruled on O1 and O2, answered O3 and confirmed O4 on 2026-10-17; that answers the design, not the work. To start any task the owner names it (or a slice) and says go. Before 0.9.0, only Slice 0 can start. Before any Slice 1 task: the seal — #417 and #523 — on `main`, Checks 90 and 91 recorded (C0.4, C0.5). No open item holds any task.

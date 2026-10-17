# Implementation plan: the credentials service

**Goal.** Build what [spec.md](spec.md) asks for **as amended by [reconciliation.md](reconciliation.md)** and the owner's answers of 2026-10-17 (D130–D154): first an **in-runner credential cascade** (tier A, shared subscription OAuth tokens, then tier B, an Anthropic API key) with shared exhaustion and broken marks, wait-or-switch and per-call accounting (Slice 1); then the same logic moved into a **separately deployed Cloud Run service** that alone holds the model credentials, reached by a job through the in-runner gateway with its run identity (Slice 2); then **GitHub App installation tokens minted by the service** per job (Slice 3). Where the reconciliation and the spec disagree, the reconciliation wins.

**Status: plan only; nothing is implemented.** No line of any task below has been written or run. **The owner answered every decision on 2026-10-17** — all as recommended except **D132** (answered "proceed and record the risk as accepted"). **Answering a decision is not approving a task: no task in this plan is approved.** Each task starts only on the owner's explicit go for that task (or for a named group of tasks); the gate at the end of this file is not passed. Two new open items (O1, O2) sit in [reconciliation.md](reconciliation.md) §4 and hold the tasks named there.

**Checked against.** `origin/main` at `054de91d` (2026-10-17). Every path and symbol named below was located with `git grep` (there is no `graphify-out/` for this repository) and re-checked to exist before it was written; a path marked **new** does not exist yet. Line numbers are never cited.

**Spec.** [spec.md](spec.md) (verbatim). **Reconciliation.** [reconciliation.md](reconciliation.md) — F1–F37, D130–D154, O1–O2, U1–U7. **Index.** [README.md](README.md).

**Related.** [mixed-credentials.md](../design/mixed-credentials.md) · [2026-10-14-oauth-reviewer-stage.md](../plans/2026-10-14-oauth-reviewer-stage.md) (superseded by D130) · [project-centric.md](../design/project-centric.md) §7 (D131) · [m9-budget-and-dashboard.md](../design/m9-budget-and-dashboard.md) §11 · [budget-token-channel.md](../design/budget-token-channel.md) §7.1 (the seal) · [named-oauth-tokens.md](../design/named-oauth-tokens.md) · [gcp-live-checklist.md](../gcp-live-checklist.md) · [SECURITY.md](../../SECURITY.md)

---

## Builds on merged code

| What exists | Where | Used by |
|---|---|---|
| The loopback gateway: per-run token, upstream credential attach, SSE pump, no header or body logging | `internal/gateway` — `Upstream` (`Kind` "anthropic" or "vertex"), `Route.attachCredential`, `Server.upstreamRequest`, `forward`, `pump`, `isSSE`, `scrubToken`, `redactWriter` (F1, F32) | C1.5, C1.7, C2.2, C2.5 |
| The ledger: reserve, settle, caps | `internal/gateway/ledger.go` (`reserve`, `settle`), `internal/budget/lease.go`, `internal/budget/model.go` (`Caps`, `AgentEntry`) (F25) | C1.8 |
| Notional accounting for subscription calls | `run.reportNotional` (`internal/runner/budget_session.go`), `budget.Session.AddNotional` (F25) | C1.8 |
| The routing switch, read once per run from the project policy | `config.RoutingClaude` (`Via`: `direct`, `openrouter`, `auto`), `config.CheckRouting`, `config.RoutingClaudeFor`, `run.applyRouting` and `RoutingInertReason` (`internal/runner/routing.go`), `GatewayOn` (F13) | C1.1, C1.2, C1.10 |
| The project policy and its version skew | `projectpolicy.Policy`, `projectpolicy.Version`, `projectpolicy.RulesVersion`, `VersionError`, `SkewSentence` (`internal/projectpolicy/version.go`) | C1.2, C1.3, C2.8 |
| The CLI's version-skew constants | `internal/cli/recipes_skew.go` (`policySince`, `artifactsSince`, …), `gcpProjectFieldSince` (`internal/cli/init_fugaroyaml.go`) | C1.1 |
| The rules as a generated artifact | `internal/budget/rules/rules.json.tmpl`, `rules.Generate`, `testdata/rules.golden.json`, `TestRulesGolden`, `TestRulesParity`, the `firebase`-tagged emulator tests (`emulator_test.go`, `lease_emulator_test.go`) (F16) | C1.3, C2.8 |
| RTDB compare-and-set | `rtdb.Client.GetETag`, `rtdb.Client.PutIfMatch` (412 → `ErrPrecondition`), precedent `budget.SetKill` (F17) | C1.6 |
| The run's budget identity | `token.Mint`, `token.ExpiryFX`, `token.IAMSigner`, `token.Exchange`, `token.Session` (`internal/budget/token`) (F29) | C2.1 |
| Sessions and `--resume` | `agent.Args`, `run.noteSession`, `run.saveSession`, `run.restoreSession` (`internal/runner/session.go`) (F22) | C1.9 |
| Named OAuth accounts per repository | `config.ClaudeAccountSecret`, `config.AllowedClaudeAccounts`, `claudeAccountFor` (`internal/cli/run.go`), `fugaro accounts ls` (`internal/cli/accounts.go`) (F4) | C1.10, C1.11 |
| Secret mounts and readiness | `infra.SecretMounts` (`internal/infra/spec.go`), `infra.OptionalSecret` (`internal/infra/readiness.go`), `google_secret_manager_secret_iam_member "job"` (`deploy/terraform/gcp/modules/workflow/sa.tf`) (F1, F2) | C1.10, C2.6, C2.7, C3.3 |
| GitHub installation tokens, minted in the job | `MintInstallationToken`, `tokenSource.Token`, `tokenPermissions`, `buildTokenPermissions` (`internal/gitprov/github/apptoken.go`), `Provider.GitAuth`, `run.refreshGitAuth` (F7) | C3.1, C3.2 |
| The coding-agent refusal for live applies | `agentMarker` (`internal/cli/initsecrets.go`) | C2.7 |
| The opt-in init stage precedent | `fugaro init --provision-batch` (`internal/cli/init.go`) | C2.7 |
| The trusted-repository gates | `config.Followup.AllowPublic`, `run.checkReviewAuthor` (`internal/runner/reviewmode.go`) (F11) | C1.1 |
| What a run's record says | `runstore.Record.OAuthAccount` (declared, unwritten), `runview.Join`, `runstore.HaltBudgetUnavailable` (F26) | C1.10, C1.11, C2.5 |
| The docs guards | `internal/cli/docs_security_test.go`, `docs_website_security_test.go`, `docs_usage_limit_switch_test.go`, `docs_named_oauth_tokens_test.go`, `docs_project_centric_test.go`; `plugin/skills_lint_test.go` (`TestLintSizeBudget`, `TestLintByteBudget`, `TestLintRejectsSecretShaped`) | every docs and skill edit |

---

## Slice summary

| Slice | What it is | Tasks | Mergeable alone? |
|---|---|---|---|
| **0 — Verify first** | The supersession notes; Checks 90 and 91 written; both **run by the owner** | C0.1–C0.5 | Yes — docs; C0.4 and C0.5 are owner-run and produce no commit but the recorded result |
| **1 — The in-runner cascade** | The `cascade` mode, the policy settings, the `creds/` rules, the OAuth upstream (**TIER A**), the state package, per-call selection, accounting, waiting, wiring, visibility, docs, Checks 92 and 93 | C1.1–C1.14 | Yes — each task is inert until C1.10 wires it, and C1.10 is inert until a workflow declares `agent.auth: cascade` and the project policy turns the switch on |
| **2 — The service** | Run-identity verification, the service, its audit log, its image, the runner's service upstream, Terraform, mounts and the init stage, the rules tightened, SECURITY.md, Checks 94 and 95 | C2.1–C2.11 | Yes — opt-in per installation (D147); with it off nothing changes |
| **3 — GitHub tokens through the service** | The mint endpoint, the runner's fetch, the App key's grant moved, docs, Check 96 | C3.1–C3.5 | Yes — behind Slice 2's opt-in |
| **4 — Later, unscheduled** | Resume after job death, alerts, the share model, egress, Batch, tier C for Claude, refresh, proxied push, shared jobs | C4.1–C4.9 | Unscheduled by construction |

---

## Check numbers this plan takes (90–99)

**The block 90–99 was reserved for this work.** Each number below is assigned here once; **a reserved number is never reused and never renumbered.** **All are owner-run:** no agent performs any step, none is a CI job. A check is written into `docs/gcp-live-checklist.md` (its register row and its section) **before** anyone runs it, and every section states exactly what the owner types, as the register's existing checks do (a hard-coded project guard, `${P:?}` on every `gcloud`, `--project fugaro`, **never `belong`**, sandbox repository only, a Restore that ends in an all-`0` assertion).

| # | The check | Written by | Run at | Where |
|---|---|---|---|---|
| **90** | **Header swap.** Claude Code on the owner's own machine, with the owner's own `claude setup-token` token, against a throwaway local proxy that strips the incoming credential and attaches the real one; records which request headers must pass through, the response status/type/headers of a usage limit if one can be observed (U3), Claude Code's client timeout and retries (U4), and the token's stated expiry (U2) | C0.3 | C0.4 | The owner's machine; no cloud; no Fugaro installation |
| **91** | **Cloud Run streaming.** A throwaway Cloud Run service on installation `fugaro` forwarding one long streaming call: request timeout, body limit (HTTP/1 vs end-to-end HTTP/2), SSE unbuffered through the front end, cold start (U5) | C0.3 | C0.5 | Installation `fugaro`, sandbox only |
| **92** | **The `creds/` rules deployed.** `fugaro init --firebase` deploys the rules; the marker reads back the new `RulesVersion`; a sandbox run's identity can write only the narrow mark of O1 and cannot read or write anything else under `creds/` | C1.13a | C1.4 | Installation `fugaro` |
| **93** | **The in-runner cascade end to end.** A sandbox review run with two admitted accounts and an API key: a forced exhaustion of account A1 switches to A2, then to B; a wait under the threshold resumes the stage with `--resume`; a revoked token is marked `broken` and shown by `watch`, `fugaro accounts ls` and `doctor`; per-call tiers and the switch log in the record | C1.13b | C1.14 | Installation `fugaro`, sandbox repository |
| **94** | **The service deployed.** The owner-run init stage applies the service; job SAs have **no** `secretAccessor` on model secrets and **only** `run.invoker` on the service; the service SA holds `secretAccessor` on model secrets only; the audit log carries names, never values | C2.10 | C2.11 | Installation `fugaro` |
| **95** | **A run through the service.** A sandbox run's calls go gateway → service → upstream; `/proc` of the runner holds no model key; a finalised run's identity is refused; the service made unreachable halts the run `budget_unavailable`-style, never falling back | C2.10 | C2.11 | Installation `fugaro`, sandbox repository |
| **96** | **GitHub tokens through the service.** The job holds no App key; the run's installation token is minted by the service for one repository with `tokenPermissions`; a run identity for repository X cannot mint for Y | C3.4 | C3.5 | Installation `fugaro`, sandbox repository |
| **97** | **The Batch backend through the service** (D149), when the backend is selectable | C4.5 | C4.5 | Installation `fugaro` |
| **98** | **Resume after job death** (C4.1) | C4.1 | C4.1 | Installation `fugaro`, sandbox repository |
| **99** | Unused, reserved | — | — | — |

Check 47 (the superseded held plan's) stays reserved and unused (D130).

---

## Conventions every task follows

1. **Owner-run live checks.** Every live step — a rules deploy, a Terraform apply, an init stage, a check — is the owner's, on installation `fugaro` and a sandbox repository only. **`belong` is never touched by any step of this work** (D147); it migrates only when the owner says so, by hand. `agentMarker` refuses live applies inside a coding agent; no task weakens it.
2. **No secret-shaped literals** in tests, fixtures, docs or skills. Fake credentials are built at run time (for example from a constant prefix and `t.Name()`), never a literal that looks like a key or token; the plugin lint (`TestLintRejectsSecretShaped`) and the image scan's `images/trivy-secret.yaml` already enforce this for their areas.
3. **Skills stay inside their budget.** Any edit under `plugin/skills/` passes `go test ./plugin/...` (`TestLintSizeBudget`, `TestLintByteBudget`, the command and flag lint).
4. **Docs guards.** Every task that edits a document runs `GOFLAGS=-p=2 go test ./internal/cli ./internal/config ./images -run 'Docs|Migration|BaseImageDoc|Design|SecurityMd|RecipesGuide|NoLegacy|Website|CliRef' -count=1` (the list CI's docs-only path names) — and updates the guard test in the same commit when the pinned text changes on purpose.
5. **Version skew.** A new `fugaro.yaml` value is a version-skew break in a strictly decoded config: it gets a `…Since` constant in `internal/cli/recipes_skew.go` and `fugaro init --repo` refuses it against a base image that predates it (the held plan's P9, carried over by D130). A policy change bumps `projectpolicy.Version` and uses `SkewSentence`; a rules change bumps `projectpolicy.RulesVersion`.
6. **Strict decoding and schemas.** New config and wire formats decode strictly; `schemas/fugaro.schema.json` changes with the config.
7. **Rules changes** ship as template + regenerated golden + parity + emulator **denial** cases in one PR, and are deployed by the owner (`fugaro init --firebase`).
8. **No agent edits `.github/workflows/`.** A workflow change (the service image's build) is staged under `docs/pending-workflows/` and copied in by the owner.
9. **Hermetic tests.** No network, no real cloud; use `internal/gcpfake`, `internal/testutil`, `internal/gateway/anthropicfake` and `t.TempDir()`. Runner and gateway changes run with `GOFLAGS=-p=2`.
10. **TIER A isolation (D132).** Every piece of code that exists only for subscription OAuth tokens through a proxy lives behind its own upstream kind and in its own files, and the task is marked **TIER A**. Removing tier A means deleting those files and one switch case, never unpicking shared code.

---

## Global Constraints

1. **Docs only in this pull request.** It adds `docs/credentials-service/implementation-plan.md` and edits `README.md` and `reconciliation.md` in that folder. No Go, Terraform, rules, schema, skill or workflow change.
2. **No task is approved.** Each starts on the owner's explicit go. A recommendation, an answered decision or a passing check is not a go.
3. **Slice order is the owner's (D137).** Slice 1 starts after Checks 90 and 91 are recorded (D151). Slice 2 starts after **#417 (the seal) has merged** (D134, held for 0.9.0). A Slice 2 task with no code dependency on Slice 1 may be written in parallel only if the owner approves it explicitly; by default it follows Slice 1.
4. **If Check 90 fails** (subscription traffic does not survive a header swap), the TIER A tasks (C1.5 and the TIER A parts of C1.10) are dropped, and Slice 1 ships as a B-only cascade with waiting and visibility; the plan is amended in a docs PR before any further task starts.
5. **The invariant (D133).** An OAuth token is only ever sent to Anthropic's own endpoint. It is asserted in code (C1.5) and by a test that fails if any route with a provider base URL can carry an OAuth credential.
6. **Per-repository jobs only** until the seal and the phase-3 shared-job rules are on `main` (C4.9). The phase-3 refusal of an OAuth launch onto a shared job (Task 3.4a, open PR #511) stays until then.
7. **The managed-projects deployer (D75: `fugaroDeployer`, `fugaroDeployerScoped`) is never used** for any of this work, and `fugaro managed apply` never deploys the service (D146). The service is Fugaro's own infrastructure, in the installation root.
8. **Unreachable service halts, never falls back** (D143): no code path re-mounts a key in the job when the service is down.
9. **No refresh, no alerts, no egress restriction, no share model** in Slices 1–3 (D139, D141, D142, D144).
10. **Every task marked SECURITY-SENSITIVE gets a mandatory independent opus review** by a reviewer that did not write it, before merge. That is nearly every code task here.

---

## Review Focus

A reviewer of **this pull request** (docs only) should check:

- That every answer cell in [reconciliation.md](reconciliation.md) §3 matches the owner's answers, and that **D132** reads as an accepted owner risk with no view on Anthropic's terms.
- That **O1** and **O2** are real tensions, correctly stated, and that the tasks they hold say so.
- That every path and symbol below exists on `origin/main` (or is marked **new**), and that no task claims a test protects what it does not.
- That every decision has a consequence in a task (the table "Decisions → tasks" below), and that no task needs something a later task builds.
- That the levels contain no two writers of one file, and the critical path is the one named.

A reviewer of a **task** reads its own "Reviewer" line and the Review notes in it.

---

## Decisions → tasks

| Decision | Where it lands |
|---|---|
| D130 | C0.2 (supersession notes); C1.1 (trusted-repository opt-in, `followup.allow_public` refusal, skew constant); C1.8 (per-stage notional record) |
| D131 | C0.2 (project-centric §7 note); C4.9 (shared jobs) |
| D132 | Convention 10, Global Constraint 4, the TIER A marks (C1.5, C1.10), the Risks section |
| D133 | C1.1 (the mode), C1.5 (the invariant) |
| D134 | C2.1 (verification), Global Constraint 3 (#417 first) |
| D135 | C2.5 (gateway → service with an ID token), C2.6 (invoker) |
| D136 | C1.3, C1.6, C2.8 (and O1) |
| D137 | The slice order |
| D138 | C1.9 (review sessions saved, wait, `--resume`); C4.1 (job death) |
| D139 | C1.7 (401 → broken); C4.7 |
| D140 | C1.8, C1.10 (`Record.OAuthAccount`), C1.11 |
| D141 | C1.11; C4.2 |
| D142 | C1.2 (the two controls' settings), C1.6 (their enforcement); C4.3 |
| D143 | C2.2, C2.5, C2.6 |
| D144 | C1.12, C2.9 (the known limit stays); C4.4 |
| D145 | C2.6/C2.7 (model keys), C3.1–C3.3 (App key); Bitbucket and workflow `secrets:` untouched |
| D146 | C2.6, C2.7, C2.11 |
| D147 | C2.6 (opt-in variable), C2.7 |
| D148 | C2.9 |
| D149 | C2.5 (local `fugaro exec` untouched), C4.5 |
| D150 | C1.2, C1.10 (and O2); C4.6 |
| D151 | C0.3–C0.5 |
| D152 | C1.2 |
| D153 | C1.2 |
| D154 | C1.6 |

---

## File structure

### Created

| Path | Task | What it is |
|---|---|---|
| `internal/creds/` (**new**) | C1.6 | The credential state: tiers, marks (available, exhausted until T, broken), selection (least-recently-exhausted, ties round-robin), stickiness, the two controls, the typed "exhausted until T" error's wire form; RTDB CAS through an interface |
| `internal/gateway/oauth.go` (**new**, **TIER A**) | C1.5 | The Anthropic-OAuth upstream kind and its header handling |
| `internal/gateway/cascade.go` (**new**) | C1.7 | Per-call selection and switching in the forward path |
| `internal/budget/rules/creds_emulator_test.go` (**new**) | C1.3 | The emulator's denial cases for `creds/` |
| `internal/credsvc/` (**new**) | C2.1–C2.3, C3.1 | The service: run-identity verification and the wire contract (C2.1), the forward (C2.2), the audit log (C2.3), the GitHub mint endpoint (C3.1) |
| `internal/cli/credentials_serve.go` (**new**) | C2.4 | A hidden `fugaro` subcommand that runs the service in its image |
| `images/credentials/Dockerfile` (**new**) | C2.4 | The service image, on the `images/history` precedent |
| `deploy/terraform/gcp/modules/installation/credentials.tf` (**new**) | C2.6 | The opt-in service, its SA, its grants, invoker for job SAs |

### Edited (the shared ones; every task lists its own)

| Path | Written by (level) |
|---|---|
| `internal/projectpolicy/version.go` | C1.3 (L2, `RulesVersion`), C1.2 (L3, `Version`), C2.8 (L6, `RulesVersion`) |
| `internal/budget/rules/rules.json.tmpl`, `testdata/rules.golden.json` | C1.3 (L2), C2.8 (L6) |
| `internal/gateway/gateway.go`, `route.go` | C1.5 (L2), C2.5 (L5) |
| `internal/gateway/forward.go` | C1.7 (L3) |
| `internal/gateway/ledger.go` | C1.8 (L4) |
| `internal/runner/runner.go` | C1.9 (L3), C1.10 (L4), C3.2 (L6) |
| `internal/runner/routing.go` | C1.2 (L3), C1.10 (L4) |
| `internal/runstore/runstore.go` | C1.9 (L3), C1.8 (L4) |
| `internal/budget/model.go` | C1.3 (L2), C1.8 (L4) |
| `internal/runner/gateway.go` | C1.10 (L4), C2.5 (L5) |
| `internal/infra/spec.go`, `readiness.go` | C1.10 (L4), C2.7 (L5) |
| `deploy/terraform/gcp/modules/workflow/sa.tf` | C2.6 (L2) |
| `deploy/terraform/gcp/modules/installation/secrets.tf` | C3.3 (L3) |
| `SECURITY.md` | C1.12 (L5), C2.9 (L6), C3.4 (L7) |
| `docs/gcp-live-checklist.md` | C0.3 (L0), C1.13a (L3), C1.13b (L5), C2.10 (L6), C3.4 (L7) |
| `internal/cli/recipes_skew.go` | C1.1 (L2) |

---

## Parallel execution

### The edges (every arrow is a "Depends on" line below)

```
C0.3 ─► C0.4 (owner) ─┐
     └► C0.5 (owner) ─┴─► every Slice 1 task (the D151 gate)
C0.2 (no edges)

C1.1 ─────────────────────────────┐
C1.3 ─► C1.2 ─────────────────────┼─► C1.10 ─┬─► C1.12 ─┐
  │                               │          └─► C1.13b ┼─► C1.14 (owner)
  ├──► C1.13a ─► C1.4 (owner) ────┼─────────────────────┘
  └──► C1.11 ──────────────────────┼─► C1.12, C1.13b
C1.5 (TIER A) ─┐                  │
C1.6 ─────────┴─► C1.7 ─┬─► C1.8 ─┘ (C1.8 before C1.14)
C1.6 ─► C1.9 ───────────┼─► C1.10
C1.6 ─► C1.11           └─► C2.2, C2.5

#417 merged ─► C2.1 ─► C2.2 ─┬─► C2.3
                             ├─► C2.4 ─► C2.10
                             └─► C3.1 ─► C3.2
C2.1 + C1.7 + C1.10 ─► C2.5 ─► C2.8 ─► C2.11 (owner)
#417 merged ─► C2.6 ─► C2.7 ─► C2.9, C2.10 ─► C2.11
C2.6 ─► C3.3
C3.2 + C3.3 + C2.9 + C2.10 ─► C3.4 ─► C3.5 (owner)
```

### Levels

| Level | Tasks | Reviewer | Files (disjoint within the level) |
|---|---|---|---|
| **L0** | C0.2 supersession notes · C0.3 Checks 90, 91 written | standard · standard | `docs/plans/2026-10-14-oauth-reviewer-stage.md`, `docs/design/mixed-credentials.md`, `docs/design/project-centric.md`, `docs/design/m9-budget-and-dashboard.md` · `docs/gcp-live-checklist.md` |
| **L1** | C0.4 Check 90 · C0.5 Check 91 | owner · owner | none (results recorded by the owner) |
| **L2** | C1.1 the mode · C1.3 the rules · C1.5 OAuth upstream (TIER A) · C1.6 the state package · *(early, only with the owner's explicit go and #417 merged:)* C2.1 identity verification · C2.6 Terraform | all SECURITY-SENSITIVE | `internal/config/*`, `schemas/fugaro.schema.json`, `internal/cli/recipes_skew.go`, `internal/cli/validate.go` · `internal/budget/rules/*`, `internal/projectpolicy/version.go` · `internal/gateway/oauth.go`, `gateway.go`, `route.go` · `internal/creds/` · `internal/credsvc/auth.go`, `wire.go` · `deploy/terraform/gcp/modules/*` |
| **L3** | C1.2 the policy settings · C1.7 the cascade · C1.9 waiting and review sessions · C1.11 visibility · C1.13a Check 92 written · *(C3.3 if Slice 2 runs early)* | SEC · SEC · SEC · standard · standard | `internal/projectpolicy/policy.go`, `version.go`, `internal/cli/project_cmd.go`, `internal/runner/routing.go` · `internal/gateway/cascade.go`, `forward.go` · `internal/runner/session.go`, `runner.go`, `internal/runstore/runstore.go` · `internal/cli/accounts.go`, `doctor.go`, `internal/watch/*` · `docs/gcp-live-checklist.md` |
| **L4** | C1.8 accounting · C1.10 the wiring · C1.4 rules deploy (owner) · C2.2 the service forward | SEC · SEC · owner · SEC | `internal/gateway/ledger.go`, `internal/budget/*`, `internal/runstore/cost.go`, `runstore.go` · `internal/runner/routing.go`, `gateway.go`, `agentenv.go`, `runner.go`, `internal/infra/spec.go`, `readiness.go`, `internal/agent/env.go` · none · `internal/credsvc/forward.go` |
| **L5** | C1.12 docs and skill · C1.13b Check 93 written · C2.3 audit · C2.4 cmd and image · C2.5 runner service upstream · C2.7 mounts and init stage · C3.1 GitHub mint | standard · standard · SEC · SEC · SEC · SEC · SEC | `SECURITY.md`, `plugin/skills/working/reference/diagnose.md`, `docs/multi-model.md` · `docs/gcp-live-checklist.md` · `internal/credsvc/audit.go` · `internal/cli/credentials_serve.go`, `images/credentials/`, `docs/pending-workflows/` · `internal/gateway/gateway.go`, `route.go`, `internal/runner/gateway.go` · `internal/infra/spec.go`, `readiness.go`, `internal/cli/init*.go` · `internal/credsvc/github.go` |
| **L6** | C1.14 Check 93 (owner) · C2.8 rules tightened · C2.9 SECURITY.md · C2.10 Checks 94, 95 written · C3.2 runner fetches GitHub token | owner · SEC · SEC · standard · SEC | none · `internal/budget/rules/*`, `version.go` · `SECURITY.md`, `docs/git-providers.md` · `docs/gcp-live-checklist.md` · `internal/gitprov/github/*`, `internal/runner/runner.go` |
| **L7** | C2.11 Checks 94, 95 and the rules deploy (owner) · C3.4 docs and Check 96 written | owner · standard | none · `SECURITY.md`, `docs/git-providers.md`, `docs/gcp-live-checklist.md` |
| **L8** | C3.5 Check 96 (owner) | owner | none |

**Widest levels: L2 (four Slice 1 tasks, six with the early Slice 2 pair) and L5 (seven).** The width is real: no two tasks in a level write one file (the table above). C1.2 sits at L3, not L2, only because it shares `internal/projectpolicy/version.go` with C1.3.

### The critical path

**C0.3 → C0.4 (owner) → C1.6 → C1.7 → C1.10 → C2.5 → C2.8 → C2.11 (owner) → C3.5 (owner)** — nine steps, three of them owner-run. C1.5 runs beside C1.6 at the same depth. Two things shorten it: C1.6 is a pure package (keep it small: state, selection, marks, the error's wire form, nothing else), and the owner-run checks are scheduled as soon as their text is written. The release that carries a runner change is a hidden step before every owner-run check of runner behaviour (C1.14, C2.11, C3.5): the check runs a released or owner-built image.

### What must be sequential

1. **C0.4 and C0.5 before Slice 1** (D151). C1.5's header list *is* Check 90's FACT lines.
2. **C1.3 before C1.4 before C1.14.** The deploy ships rules the emulator proved; the cascade's live check needs them deployed.
3. **C1.7 before C1.8 and C1.10.** Accounting and wiring consume the per-call tier the cascade decides.
4. **C1.9 before C1.10.** The wiring turns waiting on; waiting needs review-stage sessions saved.
5. **#417 before Slice 2** (D134).
6. **C2.5 before C2.8.** The runner stops writing marks before the rules stop allowing it (O1).
7. **C2.6 before C2.7 before C2.11.** Terraform, then the init stage that applies it, then the owner's apply.

---

## Slice 0 — Verify first

### Task C0.1: The owner's answers, written back — performed by this pull request

**Files.** `docs/credentials-service/reconciliation.md`, `README.md`, this file. **Depends on.** The owner's answers (2026-10-17). **Verification.** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1`. **Reviewer.** standard.
Writing the answers approves no task.

### Task C0.2: The supersession notes (D130, D131) — docs

**Files.** `docs/plans/2026-10-14-oauth-reviewer-stage.md` (its status banner: "superseded by docs/credentials-service (D130, 2026-10-17); Check 47 stays reserved, unused"; what carries over: P5/P6's trusted-repository opt-in and `followup.allow_public` refusal, P9's skew constant, P12's per-stage notional record), `docs/design/mixed-credentials.md` (Path B superseded; Paths A and C are tiers B and C), `docs/design/project-centric.md` §7 ("Mounting" and "Failover" superseded; "Storage" and "Reference" kept), `docs/design/m9-budget-and-dashboard.md` §11 (the external gateway is this work; the sidecar is dropped, F28).
**Interfaces / schema / rules.** None.
**Tests.** Whatever guard pins the edited text is updated in the same commit (`docs_project_centric_test.go`, `docs_named_oauth_tokens_test.go`).
**Red step.** Run the docs guards after the edit and before touching any guard; a red guard names the pinned sentence, which is then changed on purpose.
**Verification.** `GOFLAGS=-p=2 go test ./internal/cli ./internal/config ./images -run 'Docs|Migration|BaseImageDoc|Design|SecurityMd|RecipesGuide|NoLegacy|Website|CliRef' -count=1`.
**Depends on.** Nothing. **Reviewer.** standard.
**Commit.** `docs: mark mixed-credentials Path B and the OAuth reviewer-stage plan superseded by the credentials service (D130, D131)`

### Task C0.3: Checks 90 and 91, written into the register — docs

**Files.** `docs/gcp-live-checklist.md` (two register rows and two sections).
**What the owner types.**
- **Check 90** (no cloud): build the throwaway proxy from a scratch directory outside any repository (a short program the section gives in full, listening on loopback, logging header **names** only, never values, replacing the incoming `Authorization`/`x-api-key` with the token read from a file the owner created with `claude setup-token`); start it; run `ANTHROPIC_BASE_URL=http://127.0.0.1:<port> claude -p "say ok"` with a placeholder credential in the environment; then a long prompt; then the same with the proxy dropping each optional header in turn. `FACT:` lines: the call succeeded or not; the header names that had to pass; the status, error type and reset headers of any usage-limit or rate-limit response seen (U3); Claude Code's timeout and retry behaviour on a proxy that delays (U4); the token's stated validity (U2). Restore: delete the scratch directory; the token file is the owner's to keep or delete.
- **Check 91** (installation `fugaro`, sandbox): a hard-coded `P` guard; deploy a throwaway Cloud Run service (`gcloud run deploy fugaro-chk91 … --project="${P:?}" --no-allow-unauthenticated --timeout=3600`) running a minimal streaming relay to a fake upstream that emits SSE for N minutes; call it with `gcloud auth print-identity-token`; record time-to-first-byte (cold and warm), whether events arrive unbuffered, the longest stream that completed, and the largest request body accepted on HTTP/1 and on HTTP/2. Restore: delete the service and its image; the final assertion's lines read `0`.
**Tests.** None (a document). **Red step.** n/a (docs).
**Verification.** The docs guards (Convention 4).
**Depends on.** Nothing. **Reviewer.** standard.
**Commit.** `docs(gcp-live-checklist): Checks 90 (OAuth header swap) and 91 (Cloud Run streaming), owner-run`

### Task C0.4: Check 90 — OWNER-RUN

**Files.** None (the owner records the FACT lines in the register). **Depends on.** C0.3. **Reviewer.** owner. **No agent performs any step.**
**If it fails:** Global Constraint 4.

### Task C0.5: Check 91 — OWNER-RUN

**Files.** None. **Depends on.** C0.3. **Reviewer.** owner. **No agent performs any step.**

---

## Slice 1 — The in-runner cascade

*Per-repository jobs only; trusted repositories only; only accounts `config.AllowedClaudeAccounts` already admits for that repository. Slice 1's real cost (D137): several tokens mounted in one job widen the `/proc` exposure (F6) until Slice 2.*

### Task C1.1: `agent.auth: cascade`, its refusals and its skew constant — SECURITY-SENSITIVE

**Files.** `internal/config/validate.go` (`Validate`'s auth list), `internal/config/layer.go` (its auth list), `internal/config/providers.go` (`CheckProviderAuth`), `internal/config/config.go` (doc comment), `schemas/fugaro.schema.json`, `internal/cli/recipes_skew.go` (**new** `cascadeSince`), `internal/cli/validate.go` (the O2 refusal, best effort).
**Interfaces.** `agent.auth` gains `cascade`. A `cascade` workflow: needs budget mode `observe` or `enforce` (the gateway must be on; refused otherwise, naming the mode); is refused when `followup.allow_public` is true (D130 carry-over); keeps review mode's trusted-author gate; may name `agent.oauth_token` accounts only from its admitted set. `CheckProviderAuth` keeps refusing `oauth` with any provider route and refuses a `cascade` workflow whose provider route would carry a tier-A credential. **O2:** `fugaro validate` warns, and the launch refuses (C1.10), a `cascade` workflow whose project policy has no `routing.claude: {via: auto}`.
**Schema / rules.** `schemas/fugaro.schema.json` only.
**Tests.** `TestValidateAcceptsCascade`, `TestCascadeRefusedWithBudgetOff`, `TestCascadeRefusedWithAllowPublic`, `TestLayerAcceptsCascade`, `TestCheckProviderAuthCascadeNeverSendsOAuthToAProvider`, `TestInitRepoRefusesCascadeOnOlderBaseImage` (the skew), schema corpus case.
**Red step.** Write `TestValidateAcceptsCascade` and `TestInitRepoRefusesCascadeOnOlderBaseImage` first; both fail on today's auth list.
**Verification.** `GOFLAGS=-p=2 go test ./internal/config/... ./schemas/... ./internal/cli -run 'Cascade|Skew|Validate|Schema' -count=1`.
**Depends on.** C0.4, C0.5 (the D151 gate); O2 confirmed. **Reviewer.** opus + mandatory independent review.
**Commit.** `config: agent.auth cascade, refused without the gateway, with followup.allow_public, and on an older base image`

### Task C1.2: The cascade's policy settings and the policy version bump — SECURITY-SENSITIVE

**Files.** `internal/projectpolicy/policy.go` (a `Credentials` member: `fallback` = `oauth-only` | `auto`; `wait_threshold`; `backoff` (initial, cap); `stop_after_exhaustion_per_window`; `concurrency_per_token`), `internal/projectpolicy/version.go` (`Version` 1 → 2), `internal/cli/project_cmd.go` (`fugaro project policy publish` validation), `internal/runner/routing.go` (reading the new member; no behaviour yet).
**Interfaces.** D152: `fallback` absent means `oauth-only`; `auto` is refused unless the policy's `budget.mode` is `enforce` with `per_run_usd` > 0. D153: these live only in the project policy (no `fugaro.yaml` key). Defaults: threshold 15 min, backoff 15 min doubling, capped at 5 h. D150 + O2: under a `cascade` workflow `routing.claude: {via: auto}` turns the cascade on; for every other auth `auto` keeps today's meaning; tier C for Claude stays off (C4.6).
**Version skew.** A node at version 2 read by an older CLI or runner yields today's `SkewSentence` text; tests pin both directions.
**Tests.** `TestPolicyCredentialsDefaults`, `TestPolicyFallbackAutoRefusedWithoutEnforceCap`, `TestPolicyCredentialsUnknownKeyRefused`, `TestPolicyVersion2SkewSentences`, `TestRoutingAutoMeaningUnchangedForAPIKey`.
**Red step.** `TestPolicyFallbackAutoRefusedWithoutEnforceCap` first.
**Verification.** `GOFLAGS=-p=2 go test ./internal/projectpolicy/... ./internal/runner -run 'Policy|Routing|Skew' -count=1 && GOFLAGS=-p=2 go test ./internal/cli -run 'Policy' -count=1`.
**Depends on.** C1.3 (shares `version.go` only); O2 confirmed. **Reviewer.** opus + mandatory independent review.
**Commit.** `projectpolicy: credentials fallback, threshold, backoff and the two controls (D142, D152, D153); policy version 2`

### Task C1.3: The `creds/` subtree and the current-credential field in the rules — SECURITY-SENSITIVE

**Files.** `internal/budget/rules/rules.json.tmpl`, `internal/budget/rules/testdata/rules.golden.json`, `internal/budget/rules/parity_test.go` (if a mirror is needed), `internal/budget/rules/creds_emulator_test.go` (**new**), `internal/projectpolicy/version.go` (`RulesVersion` 1 → 2), `internal/budget/model.go` (`AgentEntry` gains the current-credential **name**), `docs/design/m9-budget-and-dashboard.md` §6.2/§6.4.
**Rules change.** `creds/<project>/<account>/…` with **O1's** narrow run-token grant: a run token may write only `mark` = `{state: exhausted|broken, until, by_run}` on an account of its own repository's admitted set, validated, no delete, no clearing; may read the marks of those accounts; nothing else. `agents/$slug/$run` gains one validated child, the current credential's **name** (never a value). Everything else under `creds/` is denied to every run token.
**Tests.** `TestRulesGolden`, `TestGeneratedRulesParse`, `TestRulesParity`; emulator (tag `firebase`): `TestCredsRunCannotClearAMark`, `TestCredsRunCannotMarkAnotherRepositorysAccount`, `TestCredsRunCannotWriteOtherFields`, `TestCredsRunCannotDelete`, `TestCredsRunCannotReadOtherAccounts`, positive `TestCredsRunMarksItsAdmittedAccount`, `TestAgentEntryCredentialNameValidated`.
**Red step.** The emulator denial cases first, against today's template: the positive case fails (no subtree).
**Verification.** `go test ./internal/budget/rules/... -count=1 && go test -tags firebase -count=1 ./internal/budget/rules/...` (needs the pinned `firebase-tools`; CI's `rules` job runs it).
**Depends on.** The D151 gate; **O1 confirmed**. **Reviewer.** opus + **MANDATORY** independent review (it changes what the database permits).
**Commit.** `rules: creds/ marks with a narrow run-token write (O1), the current credential's name on the agent entry; RulesVersion 2`

### Task C1.4: The rules deploy and Check 92 — OWNER-RUN

**Files.** None. **Depends on.** C1.3, C1.13a. **Verification.** The owner runs `fugaro init --firebase` on installation `fugaro`, the marker reads `RulesVersion` 2, Check 92 is recorded. **Reviewer.** owner. **No agent performs any step.**

### Task C1.5: The Anthropic-OAuth upstream — **TIER A**, SECURITY-SENSITIVE

**Files.** `internal/gateway/oauth.go` (**new**), `internal/gateway/gateway.go` (`Upstream.Kind` gains `anthropic-oauth`; validation), `internal/gateway/route.go` (`attachCredential`'s OAuth case), `internal/gateway/oauth_test.go` (**new**).
**Interfaces.** An upstream that attaches a subscription OAuth credential with exactly the headers Check 90 recorded, to `anthropicBase` only. **Invariant (D133):** `validateRoutes` and `attachCredential` refuse an OAuth credential on any route whose base URL is not Anthropic's; a credential is never logged (existing `scrubToken`/`redactWriter`).
**Tests.** `TestOAuthUpstreamAttachesRecordedHeaders` (against `anthropicfake`), `TestOAuthNeverSentToProviderRoute`, `TestOAuthCredentialRedactedFromErrors`, `TestOAuthUpstreamStreamsUnbuffered`. Fake tokens built at run time (Convention 2).
**Red step.** `TestOAuthNeverSentToProviderRoute` first.
**Verification.** `GOFLAGS=-p=2 go test ./internal/gateway/... -count=1`.
**Depends on.** C0.4 (the header list). **Reviewer.** opus + mandatory independent review.
**Commit.** `gateway: an Anthropic-OAuth upstream (tier A), and the invariant that an OAuth token goes only to Anthropic`

### Task C1.6: The credential state package — SECURITY-SENSITIVE

**Files.** `internal/creds/` (**new**: `state.go`, `select.go`, `marks.go`, `errors.go` and tests).
**Interfaces.** `Tier` (A, B; C reserved); `Mark{State, Until, ByRun}`; `Select(run, candidates, marks, now) (Credential, wait time.Duration, err)` — sticky per run (D154), new runs least-recently-exhausted with round-robin ties by a counter; the two controls (D142); backoff doubling to its cap when no reset time is known (D153); `ExhaustedUntil` — the typed error and its wire form (an HTTP status, error type and a reset field the runner reads). Marks are read and written through an interface whose RTDB implementation uses `GetETag`/`PutIfMatch`, retrying on `ErrPrecondition`.
**Tests.** `TestSelectSticky`, `TestSelectLeastRecentlyExhausted`, `TestSelectRoundRobinTies`, `TestStopAfterOneExhaustionPerWindow`, `TestConcurrencyCap`, `TestBackoffDoublesToCap`, `TestMarkCASRetriesOnPrecondition` (fake DB), `TestBrokenNeverSelected`, `TestExhaustedUntilWireRoundTrip`.
**Red step.** `TestSelectSticky` first.
**Verification.** `go test ./internal/creds/... -count=1`.
**Depends on.** The D151 gate. **Reviewer.** opus + mandatory independent review.
**Commit.** `creds: credential state, selection, stickiness, the two controls and the exhausted-until error`

### Task C1.7: Per-call selection and switching in the gateway — SECURITY-SENSITIVE

**Files.** `internal/gateway/cascade.go` (**new**), `internal/gateway/forward.go` (choose the credential per request; classify the upstream response), tests.
**Interfaces.** Before each call, `creds.Select`; on a usage-limit response (status, type and reset headers per Check 90/U3) mark `exhausted until T` and retry the **same request** on the next credential only when no byte has been streamed to the client; on 401 mark `broken` (D139); when nothing is usable and the wait is under the threshold, or `fallback` is `oauth-only`, answer the typed `ExhaustedUntil`. Every switch is reported to the ledger hook (C1.8) with `{from, to, reason, estimated cache-miss cost}` (F24: "unmeasured" for C).
**Tests.** `TestCascadeSwitchesOnUsageLimitBeforeFirstByte`, `TestCascadeNeverRetriesAfterStreamStarted`, `TestCascade401MarksBroken`, `TestCascadeOAuthOnlyWaits`, `TestCascadeAutoFallsToB`, `TestCascadeStickyAcrossCalls`.
**Red step.** `TestCascadeNeverRetriesAfterStreamStarted` first.
**Verification.** `GOFLAGS=-p=2 go test ./internal/gateway/... -count=1`.
**Depends on.** C1.5, C1.6. **Reviewer.** opus + mandatory independent review.
**Commit.** `gateway: per-call credential selection and switching, exhausted and broken marks, wait-or-switch`

### Task C1.8: Per-call accounting and the switch log — SECURITY-SENSITIVE

**Files.** `internal/gateway/ledger.go`, `internal/budget/lease.go`, `internal/budget/model.go` (by-tier counters), `internal/runstore/cost.go` (per-stage notional and real split, the held plan's P12 shape), `internal/runstore/runstore.go` (switch log in the record), tests.
**Interfaces.** A tier-A call settles as notional (`AddNotional`), B/C as real against the caps; per-run and per-day counters gain a by-tier split; the record carries the switch log. `cost.model_usd`'s documented sum invariant holds.
**Tests.** `TestSettleTierANotional`, `TestSettleTierBReal`, `TestCapIgnoresNotional`, `TestSwitchLogRecorded`, `TestModelUSDSumInvariantMixedRun`.
**Red step.** `TestCapIgnoresNotional` first.
**Verification.** `GOFLAGS=-p=2 go test ./internal/gateway/... ./internal/budget/... ./internal/runstore/... -count=1`.
**Depends on.** C1.7. **Reviewer.** opus + mandatory independent review.
**Commit.** `gateway, budget: per-call tier accounting, notional A and real B/C, and the switch log (D140)`

### Task C1.9: Review-stage sessions saved, and the runner's wait — SECURITY-SENSITIVE

**Files.** `internal/runner/session.go` (save the review stage's session too, redacted), `internal/runner/runner.go` (`stage`: on `ExhaustedUntil` end the stage, wait within the job's remaining timeout, resume with `--resume`; halt with a named reason when the wait would exceed it), tests.
**Interfaces.** A new halt reason in `runstore` for "no credential before the job's timeout". No resume after job death (C4.1).
**Tests.** `TestReviewSessionSaved`, `TestStageWaitsAndResumes`, `TestWaitBeyondTimeoutHalts`, `TestSessionRedacted` (existing redaction applies).
**Red step.** `TestStageWaitsAndResumes` first.
**Verification.** `GOFLAGS=-p=2 go test ./internal/runner/... ./internal/runstore/... -count=1`.
**Depends on.** C1.6 (the error's wire form). **Reviewer.** opus + mandatory independent review.
**Commit.** `runner: save review-stage sessions and wait for a credential with --resume (D138)`

### Task C1.10: The wiring — mounts, gateway on, routing, the record — SECURITY-SENSITIVE (TIER A parts marked)

**Files.** `internal/runner/routing.go` (`RoutingInertReason` and `applyRouting` for `cascade`; O2 refusal at launch), `internal/runner/gateway.go` (`gatewayOn` true for `cascade`; the upstream set: B always when `fallback: auto`, **TIER A**: one `anthropic-oauth` upstream per admitted account), `internal/runner/agentenv.go` (no credential reaches the agent's env), `internal/runner/runner.go` (write `Record.OAuthAccount` and the current credential), `internal/infra/spec.go` (`SecretMounts` for `cascade`: the admitted accounts' secrets under distinct env names — **TIER A** — plus `anthropic-api-key`), `internal/infra/readiness.go`, `internal/agent/env.go` (`GatewayVars` for `cascade`), `internal/cli/run.go` (one job per workflow, no per-account variant for `cascade`), tests.
**Interfaces.** A `cascade` run never mixes outside the gateway; the redaction set (`addMountedSecrets`) covers every mounted token.
**Tests.** `TestCascadeMountsAdmittedAccountsOnly`, `TestCascadeAgentEnvHoldsNoCredential`, `TestCascadeRefusedWithoutAutoSwitch` (O2), `TestCascadeRecordsOAuthAccount`, `TestCascadeAllTokensRedacted`, `TestGatewayOnForCascade`.
**Red step.** `TestCascadeAgentEnvHoldsNoCredential` first.
**Verification.** `GOFLAGS=-p=2 go test ./internal/runner/... ./internal/infra/... ./internal/agent/... ./internal/cli -run 'Cascade|Routing|Gateway|Mount|Account' -count=1`.
**Depends on.** C1.1, C1.2, C1.7, C1.9; O2 confirmed. **Reviewer.** opus + **MANDATORY** independent review.
**Commit.** `runner, infra: wire agent.auth cascade — mounts, gateway, routing switch, the record`

### Task C1.11: Visibility — `watch`, `fugaro accounts ls`, `doctor`, clearing a mark

**Files.** `internal/cli/accounts.go` (state column: available, exhausted until T, broken; and a subcommand that clears a `broken` or `exhausted` mark after the operator replaced the token — name settled in review), `internal/cli/doctor.go` (a broken admitted account is a finding), `internal/watch/json.go` and `render.go` (the current credential's name), `console/docs/json-contract.md` (the new field), `plugin/skills/` command lint follows any new subcommand, tests.
**Interfaces.** Names and states only, never values (F27).
**Tests.** `TestAccountsLsShowsStates`, `TestAccountsClearMark`, `TestDoctorReportsBroken`, `TestWatchJSONCurrentCredential`, the plugin lint.
**Red step.** `TestAccountsLsShowsStates` first.
**Verification.** `GOFLAGS=-p=2 go test ./internal/cli ./internal/watch/... ./plugin/... -count=1`.
**Depends on.** C1.3, C1.6. **Reviewer.** standard (opus); the clear subcommand writes to `creds/` as the operator, so its review checks it cannot clear another installation's marks.
**Commit.** `cli: account states in accounts ls, doctor and watch; clearing a mark (D141)`

### Task C1.12: Slice 1's docs and skill

**Files.** `SECURITY.md` ("What one run's agent can reach": a `cascade` job mounts several tokens, readable through `/proc` until Slice 2; the invariant; egress stays a known limit, D144), `plugin/skills/working/reference/diagnose.md` §5 (the relaunch advice does not apply to a `cascade` run), `docs/multi-model.md` (the mode), the guard tests that pin changed text (`docs_security_test.go`, `docs_website_security_test.go`, `docs_usage_limit_switch_test.go`).
**Tests.** The guards, updated on purpose; `go test ./plugin/...`.
**Red step.** Edit, run the guards, update only the pins that name the changed claims.
**Verification.** Convention 4's command, plus `go test ./plugin/... -count=1`.
**Depends on.** C1.10, C1.11. **Reviewer.** standard.
**Commit.** `docs: the cascade mode, its residual exposure and the usage-limit advice`

### Task C1.13a: Check 92 written — docs

**Files.** `docs/gcp-live-checklist.md`. **What the owner types:** the hard-coded guard; `fugaro init --firebase --project fugaro --gcp-project "${P:?}"`; the marker read; a sandbox run's identity used (via the check's own small `go test -tags live` helper, named in the section) to attempt each denied write and the one allowed mark; Restore clears the mark. **Verification.** Convention 4. **Depends on.** C1.3. **Reviewer.** standard.
**Commit.** `docs(gcp-live-checklist): Check 92, the creds/ rules deployed`

### Task C1.13b: Check 93 written — docs

**Files.** `docs/gcp-live-checklist.md`. **What the owner types:** sandbox repository, two admitted test accounts and the sandbox API key; the policy published with `routing.claude: {via: auto}` and `credentials.fallback: auto` with an enforce cap; a forced exhaustion by `fugaro accounts` marking A1 exhausted from the operator's terminal; a review run (`fugaro run …`); `fugaro watch`, `fugaro accounts ls`, `fugaro doctor` read back; a revoked test token for the broken case; Restore re-publishes the recorded policy and clears marks. **Verification.** Convention 4. **Depends on.** C1.10, C1.11. **Reviewer.** standard.
**Commit.** `docs(gcp-live-checklist): Check 93, the in-runner cascade`

### Task C1.14: Check 93 — OWNER-RUN

**Files.** None. **Depends on.** C1.4, C1.8, C1.12, C1.13b, and a release (or owner-built image) carrying C1.1–C1.11. **Reviewer.** owner. **No agent performs any step.**

---

## Slice 2 — The service

*Starts after #417 has merged (D134). Opt-in per installation (D147). Per-repository jobs only (C4.9 for shared).*

### Task C2.1: Run-identity verification and the wire contract — SECURITY-SENSITIVE

**Files.** `internal/credsvc/auth.go`, `internal/credsvc/wire.go` (**new**: header names, error shapes), tests.
**Interfaces.** Verify the Firebase ID token for uid `r~<slug>~<run>` against Google's public keys (cached), `fp` against the service's project, `fx` not passed, the run's live state in RTDB (`agents/<slug>/<run>` present, not finalised), the repository admitted. The job SA's Google ID token is checked by Cloud Run IAM, not by this code.
**Tests.** `TestVerifyAcceptsLiveRun`, `TestVerifyRefusesFinalisedRun`, `TestVerifyRefusesOtherProject`, `TestVerifyRefusesExpiredFX`, `TestVerifyRefusesUnknownKeyID`, `TestVerifyRefusesSharedJobRun` (C4.9's gate) — keys generated in the test.
**Red step.** `TestVerifyRefusesFinalisedRun` first.
**Verification.** `go test ./internal/credsvc/... -count=1`.
**Depends on.** #417 merged. **Reviewer.** opus + **MANDATORY** independent review.
**Commit.** `credsvc: verify the run identity and the run's live state (D134)`

### Task C2.2: The service's forward and selection — SECURITY-SENSITIVE

**Files.** `internal/credsvc/forward.go`, `internal/credsvc/server.go` (**new**), tests. Reuses `internal/creds` and the gateway's upstream code (`Upstream`, `attachCredential`, `pump`) through exported helpers added in this task; no new forwarding logic.
**Interfaces.** Reads secrets from Secret Manager as its own SA, `latest` with a short cache (D147); no shell, no admin endpoint (D143); never logs a header or body; `creds/` written as its own SA.
**Tests.** `TestServiceForwardsWithSelectedCredential`, `TestServiceStreamsUnbuffered`, `TestServiceRefusesUnverified`, `TestServiceHasNoAdminRoutes`, `TestServiceNeverLogsHeaders`.
**Red step.** `TestServiceRefusesUnverified` first.
**Verification.** `GOFLAGS=-p=2 go test ./internal/credsvc/... ./internal/gateway/... -count=1`.
**Depends on.** C2.1, C1.7. **Reviewer.** opus + mandatory independent review.
**Commit.** `credsvc: forward with the selected credential, streaming, no admin surface`

### Task C2.3: The audit log — SECURITY-SENSITIVE

**Files.** `internal/credsvc/audit.go`, tests. **Interfaces.** One structured line per issuance: run, repository, credential **name**, purpose, tier, status; never a value. **Tests.** `TestAuditCarriesNamesOnly`, `TestAuditOnRefusal`. **Red step.** `TestAuditCarriesNamesOnly`. **Verification.** `go test ./internal/credsvc/... -count=1`. **Depends on.** C2.2. **Reviewer.** opus + mandatory independent review.
**Commit.** `credsvc: an audit line per issuance, names only`

### Task C2.4: The command and the image — SECURITY-SENSITIVE

**Files.** `internal/cli/credentials_serve.go` (**new**, hidden), `internal/cli/root.go` (one registration), `images/credentials/Dockerfile` (**new**), `images/` tests on the `images/history` precedent, `.goreleaser.yaml` if the image is released with Fugaro's, `docs/pending-workflows/images.yml` (the staged workflow change; the owner copies it, Convention 8).
**Tests.** `TestCredentialsServeHidden`, the image's size and scan tests (`images/size_budget_test.go`, `scan_test.go` patterns). **Red step.** `TestCredentialsServeHidden`. **Verification.** `GOFLAGS=-p=2 go test ./internal/cli ./images -run 'Credentials|Size|Scan' -count=1`. **Depends on.** C2.2. **Reviewer.** opus + mandatory independent review.
**Commit.** `credsvc: the hidden serve command and its image; the workflow change staged for the owner`

### Task C2.5: The runner's service upstream — SECURITY-SENSITIVE

**Files.** `internal/gateway/gateway.go`, `route.go` (an upstream kind `service`: the job SA's ID token from the metadata server in `Authorization`, the run identity in the wire contract's header), `internal/runner/gateway.go` (choose it when the installation has the service on; the runner no longer writes marks), tests.
**Interfaces.** Service unreachable → the run halts like `budget_unavailable` (D143), never an in-job key. Local `fugaro exec` unchanged (D149).
**Tests.** `TestServiceUpstreamSendsIDTokenAndRunIdentity`, `TestServiceUnreachableHalts`, `TestNoInJobFallback`, `TestExecUnchanged`.
**Red step.** `TestNoInJobFallback`. **Verification.** `GOFLAGS=-p=2 go test ./internal/gateway/... ./internal/runner/... -count=1`. **Depends on.** C2.1, C1.7, C1.10. **Reviewer.** opus + mandatory independent review.
**Commit.** `gateway, runner: the service upstream, halting when it is unreachable (D135, D143)`

### Task C2.6: Terraform — the service, its SA, the grants moved — SECURITY-SENSITIVE

**Files.** `deploy/terraform/gcp/modules/installation/credentials.tf` (**new**: Cloud Run service with ingress requiring IAM, its SA, `secretAccessor` on model secrets only, `run.invoker` for job SAs), `modules/installation/variables.tf` (`credentials_service` opt-in, default off), `modules/workflow/sa.tf` and `modules/repo/secrets.tf` (job SAs' `secretAccessor` on model secrets dropped **only when** the opt-in is on), `roots/installation/tests/installation.tftest.hcl` and the repo root's tests.
**Tests.** Terraform tests: off → plan identical to today; on → job SA has no model-secret accessor and has invoker; service SA has nothing on the runs bucket and no `actAs` granted to non-operators.
**Red step.** The "on" test first. **Verification.** `terraform fmt -check -recursive deploy/terraform && go test -tags terraform ./internal/infra/... -count=1` and `terraform test` in `roots/installation` and `roots/repo`. **Depends on.** #417 merged. **Reviewer.** opus + **MANDATORY** independent review.
**Commit.** `terraform: an opt-in credentials service in the installation root; model-secret access moves to it (D146, D147)`

### Task C2.7: The mounts and the owner-run init stage — SECURITY-SENSITIVE

**Files.** `internal/infra/spec.go`, `readiness.go` (no model secret mounted when the service is on), `internal/cli/init.go` and a **new** stage file on the `internal/cli/init_compute.go` precedent (a flag like `--provision-batch`, typed at a real terminal, never covered by `--yes`, refused under `agentMarker`), the upgrade note, tests.
**Tests.** `TestNoModelMountsWithService`, `TestInitCredentialsStageRefusedInAgent`, `TestInitCredentialsStageNotCoveredByYes`, `TestUpgradeNoteNamesTheStage`. **Red step.** `TestInitCredentialsStageRefusedInAgent`. **Verification.** `GOFLAGS=-p=2 go test ./internal/infra/... ./internal/cli -run 'Credentials|Mount|Init|Upgrade' -count=1`. **Depends on.** C2.6, C1.10. **Reviewer.** opus + mandatory independent review.
**Commit.** `infra, init: no model-secret mounts with the service on; an owner-run init stage to apply it`

### Task C2.8: The rules tightened (O1 undone) — SECURITY-SENSITIVE

**Files.** As C1.3; `RulesVersion` 2 → 3. Run tokens lose every access to `creds/` (D136 exactly). **Tests.** The C1.3 denial cases, now with the narrow write denied too; golden, parity. **Red step.** `TestCredsRunCannotMarkEvenItsOwnAccount` first. **Verification.** As C1.3. **Depends on.** C2.5, C1.4. **Reviewer.** opus + **MANDATORY** independent review.
**Commit.** `rules: creds/ is the service's alone; RulesVersion 3 (D136)`

### Task C2.9: SECURITY.md and the claims the service changes

**Files.** `SECURITY.md` (D148's sentence, "the only Fugaro runtime identity with access to model credentials", and the others named: the build SA, operators, Project Owners/Editors; F34's claims rewritten for an installation with the service on), `docs/git-providers.md` if touched, the guards. **Verification.** Convention 4. **Depends on.** C2.7, C1.12. **Reviewer.** opus + mandatory independent review (security claims).
**Commit.** `SECURITY.md: the credentials service, stated exactly (D148)`

### Task C2.10: Checks 94 and 95 written — docs

**Files.** `docs/gcp-live-checklist.md`; each step's exact command (the init stage, `gcloud … get-iam-policy` reads, a sandbox run, the `/proc` inspection from the run's own log, the unreachable-service case by scaling the service to zero instances, Restore). **Depends on.** C2.4, C2.7, C1.13b. **Reviewer.** standard.
**Commit.** `docs(gcp-live-checklist): Checks 94 and 95, the credentials service`

### Task C2.11: Checks 94, 95 and the rules deploy — OWNER-RUN

**Files.** None. **Depends on.** C2.8, C2.9, C2.10, C1.14, a release carrying Slice 2. **Reviewer.** owner. **No agent performs any step.**

---

## Slice 3 — GitHub App tokens through the service

### Task C3.1: The mint endpoint — SECURITY-SENSITIVE

**Files.** `internal/credsvc/github.go` (**new**), reusing `github.MintInstallationToken` and `tokenPermissions` (no permission added); the repository is bound to the run identity's `fs`. **Tests.** `TestMintForOwnRepositoryOnly`, `TestMintPermissionsAreTokenPermissions`, `TestMintRefusesFinalisedRun`, `TestMintAudited`. **Red step.** `TestMintForOwnRepositoryOnly`. **Verification.** `go test ./internal/credsvc/... ./internal/gitprov/... -count=1`. **Depends on.** C2.2. **Reviewer.** opus + mandatory independent review.
**Commit.** `credsvc: mint a GitHub installation token for the run's own repository (D145)`

### Task C3.2: The runner fetches its GitHub token from the service — SECURITY-SENSITIVE

**Files.** `internal/gitprov/github/apptoken.go`, `github.go` (`GitAuth` from the service when it is on), `internal/runner/runner.go` (`refreshGitAuth`), tests. Bitbucket unchanged. **Tests.** `TestGitAuthFromService`, `TestNoAppKeyReadWithService`, `TestBitbucketUnchanged`. **Red step.** `TestNoAppKeyReadWithService`. **Verification.** `GOFLAGS=-p=2 go test ./internal/gitprov/... ./internal/runner/... -count=1`. **Depends on.** C3.1, C2.5. **Reviewer.** opus + mandatory independent review.
**Commit.** `github, runner: installation tokens from the service; the job reads no App key`

### Task C3.3: The App key's grant moves — SECURITY-SENSITIVE

**Files.** `deploy/terraform/gcp/modules/installation/secrets.tf`, `credentials.tf`, `modules/workflow/sa.tf` (job SAs lose `secretAccessor` on `github-app-key` when the opt-in is on; the build SA keeps what `modules/repo/build.tf` gives it, F3), Terraform tests. **Verification.** As C2.6. **Depends on.** C2.6. **Reviewer.** opus + **MANDATORY** independent review.
**Commit.** `terraform: the GitHub App key is read by the service only, with the service on`

### Task C3.4: Docs and Check 96 written

**Files.** `SECURITY.md`, `docs/git-providers.md` ("None of these variables reaches the agent", made exact, F34), `docs/gcp-live-checklist.md` (Check 96), guards. **Depends on.** C3.2, C3.3, C2.9, C2.10. **Reviewer.** standard.
**Commit.** `docs: GitHub tokens through the credentials service; Check 96`

### Task C3.5: Check 96 — OWNER-RUN

**Files.** None. **Depends on.** C3.4, C2.11, a release carrying Slice 3. **Reviewer.** owner.

---

## Slice 4 — Later, unscheduled

Each needs its own plan amendment and the owner's go.

- **C4.1 Resume after job death** (D138): periodic session upload during a stage, a resumable-run record, a relauncher taking over the run id and branch; Check 98.
- **C4.2 Alerts** to a token's owner and project admins (D141), once a notification channel and phase 5's owner record exist.
- **C4.3 The share model** — N sessions per window, per-token counters (D142).
- **C4.4 Egress restriction** — a VPC with egress rules (D144).
- **C4.5 The Batch backend** — VM SA gets invoker only (D149) when `backend: cloud-batch` is selectable; Check 97.
- **C4.6 Tier C for a Claude model** — only after Check 50 has measured cache markers (D150).
- **C4.7 OAuth refresh** with a per-token lock and Secret Manager version CAS — only if U2 shows a refreshable credential (D139).
- **C4.8 Proxied push and PR creation** (D145, optional).
- **C4.9 Shared jobs** — the service admits runs on phase-3 profile jobs only after #417 and the phase-3 shared-job rules are on `main`; Task 3.4a's refusal is lifted then, and the reference record of project-centric §7 becomes the tier-A registry (D131).

---

## Risks

- **D132 — accepted owner risk.** The owner chose to proceed without first confirming Anthropic's current terms for sharing individuals' subscription OAuth tokens across a project and sending their traffic through a Fugaro-operated proxy. This plan states no view on what those terms say. Tier-A-through-a-proxy work may later have to be removed, and the token holders' accounts are exposed in the meantime. Mitigation by construction: TIER A code is isolated (Convention 10), so removing it is deleting C1.5's files and the TIER A parts of C1.10.
- **Slice 1 widens the in-job exposure** (D137): several tokens mounted in one job, readable through `/proc`, until Slice 2.
- **O1** lets a trusted repository's run mark an admitted account exhausted or broken in Slice 1 (denial of service, never disclosure).
- **The service is a single point** of compromise and availability (D143), accepted with its bounds.

---

## OWNER APPROVAL GATE

**Not passed. No task is approved.** The owner answered D130–D154 on 2026-10-17; that answers the design, not the work. To start any task the owner names it (or a slice) and says go. Before any Slice 1 code task: Checks 90 and 91 recorded (C0.4, C0.5), O1 and O2 confirmed or corrected. Before any Slice 2 task: #417 merged.

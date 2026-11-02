# The coordinator agent, reconciled against the code

**Checked against `origin/main` at `c6009b5f` (`release: v0.8.6 (#637)`), on 2026-10-19.**
Every path and symbol below was read at that commit. Where a claim is about an open
pull request rather than `main`, the pull request is named.

This document sets the owner's spec ([spec.md](spec.md)) against what this repository
actually has. It is the same shape as the earlier reconciliations
([docs/credentials-service/reconciliation.md](../credentials-service/reconciliation.md),
[docs/mobile/reconciliation.md](../mobile/reconciliation.md)): a concept table first,
then findings each classified **Wrong / Exists / Gap / Rule / Open**, then the
questions the owner had to answer, and the owner's answers to them, then what could not be
verified.

**Nothing here is implemented, and nothing here is approved.** No Go, no TypeScript, no
Terraform, no rules change, no live action follows from this document. The findings that needed the owner were numbered **Q1–Q16**, and **the owner answered all sixteen on 2026-10-19** (§3), each by taking option (A), the recommended one — **an answer picks an option; it allocates nothing.** Planning the work afterwards found five things the answers do not settle, and those are decisions rather than questions, so they are numbered **D193–D197** and written in **§6**, each **recommendation first**. They are taken from the **D193 onward** block the owner's register records as free ([README.md](README.md)); **the owner answered all five on 2026-10-21** — four as recommended, **D196 against it** — and the plan that follows is written to those answers ([implementation-plan.md](implementation-plan.md)).

---

## 0. Corrections to the brief this reconciliation was written from

Three statements in the brief are wrong against `c6009b5f`, and one is a path that does
not exist. Recording them here so the next reader does not repeat them.

1. **"the budget database … verify whether it is Firestore or RTDB."** It is the
   **Realtime Database**, and Firestore is a second, separate store. See finding **#6**.
2. **"`fugaro runs ls` / `runs stats` (per-person stats in 0.9.0)."** There is no
   per-person field in `fugaro stats` and the doc says so in as many words:
   `internal/cli/stats.go:62-64` (`statsDoc`) — *"There is no field for a repository, run
   ID, branch, pull request, task, person or commit"*, repeated at
   `internal/stats/stats.go:9-13`. Per-person money exists only in Firestore
   `spendDaily.byPerson`, read by `fugaro report --by person`
   (`internal/cli/report.go:499`). See finding **#12**.
3. **"`docs/design/credentials-service*.md`."** Those documents are at
   `docs/credentials-service/`; `docs/design/` has no such subdirectory. The service they
   describe **does not exist in code** (finding **#22**).
4. **"Read CLAUDE.md and the .claude/rules files."** Neither exists at `c6009b5f`: there
   is no root `CLAUDE.md`, and `.claude/` holds only `.claude/skills/new-release/SKILL.md`. The
   authority used instead was `CONTRIBUTING.md` and the shape of the existing
   `internal/cli/docs_*_test.go` files.

---

## 1. The spec's concepts, and what the repository has

| Spec concept (§) | What the repository has |
| --- | --- |
| A **Fugaro project** (§2, §4) | Defined: *"one GCP project, plus (from M9) one Firebase project … its repositories; its people lists …; one local config"* (`docs/design/v1.md`). The spec's "one coordinator per Fugaro project, deployed into that project's Firebase/GCP project" is therefore well-formed — and it is the same object the repo calls an **installation** (`docs/design/v1.md`). |
| **Cloud Run service, scale-to-zero** (§4) | **No Cloud Run *service* exists anywhere.** Every workload is `google_cloud_run_v2_job` (history `modules/installation/history.tf:70`; project jobs `modules/project/job.tf:10`; workflow jobs `modules/workflow/job.tf:4`; repo check `modules/repo/check.tf:16`). The type is not even in the plan allowlist (`internal/infra/tf/cover.go:22`). See **#2**. |
| **Provisioning: "part of project init's existing Terraform"** (§4) | `fugaro init` applies the **installation** root (`internal/cli/init.go`'s `installRoot`, the function that plans and applies it), behind one interactive confirmation that covers a plan only if every resource type is allowlisted (`internal/infra/tf/cover.go:22 allowedTypes`, `:109 grantRules`) and every grant is in `grantRules`; anything else gets its own typed confirmation (`internal/cli/init_review.go:77 runCovers`, `:127 notCovered`). See **#3**. |
| **Access: "whoever can reach the project's coordinator is in the project; no extra permission model in v1"** (§4) | The opposite is the standing design: the compute module **refuses** `allUsers`, `allAuthenticatedUsers` and `domain:` members (`deploy/terraform/gcp/modules/compute/variables.tf:122,144`), launchers are per-person IAM members on one job (`modules/project/iam.tf:7`), and `SECURITY.md:40` puts the trust boundary at the repository and the task source, never at network reach. See **#4**. |
| **Store: Firestore; "Firestore and RTDB are already available"** (§5) | True, both exist — and they are **two different stores with two different jobs**. RTDB is the budget/policy/kill/registry/mailbox store (`internal/budget/model.go:1-2`); Firestore holds `spendDaily` history only (`internal/firestore/client.go` package doc, `internal/budget/rollover.go:48 SpendCollection`). See **#6**. |
| **"Firestore's real-time listeners drive live updates in the console"** (§5) | Wrong twice: the Firestore client has no listeners (hand-written REST: `Get`, `Patch`, `Delete`, `Query`, `GetDatabase`, `CreateDatabase`), and the console renderer's CSP is `connect-src 'none'` (`console/src/main/protocol.ts:132`) — it never talks to the network at all. See **#7**. |
| **GitHub as ground truth** (§6) | `gitprov.Provider` reads `Repository`, `PullRequest`, `Comments`, `FetchPRHead` (`internal/gitprov/gitprov.go:208-270`), and `github.MintInstallationToken(ctx, o, perms)` mints a token **scoped to one repository** (`internal/gitprov/github/apptoken.go`'s `tokenSource.Token`, whose body names one). See **#9**, **#10**. |
| **Intent signals from local orchestrators, before code exists** (§6) | No channel exists. The only per-person fact written today is `launch.json`'s `launched_by` (`internal/runstore/launch.go`), a **self-declared email** (`internal/cli/run.go:1731`, from `localcfg.Config.Me`). See **#11**. |
| **MCP server over HTTP** (§7) | No MCP server, no MCP dependency. The only HTTP server in the repository is the per-run budget gateway, bound to loopback (`internal/gateway/gateway.go:240-252`, `127.0.0.1:0`). See **#8**. |
| **Two model tiers; "reuses the project's existing key management and security service"** (§8) | The security service does not exist (`docs/credentials-service/README.md:3` — *"plan only; nothing is implemented"*). Model configuration today is per **slot**, not per tier (`agent.models.coder|reviewer|background`, `docs/design/m10-multi-model.md`) plus the routing switch (`internal/config/routing.go:42-61`). See **#21**, **#22**, **#23**. |
| **Concurrency: Firestore transactions or a per-project lock** (§9) | The Firestore client has **no transactions**; the only lock is per-branch, in GCS (`internal/lock/lock.go:1-5`, key `locks/<slug>/<branch-hash>`). The repo's concurrency idiom is generation-matched GCS writes (`internal/blobx/blobx.go:238,334,365`), RTDB `PutIfMatch` ETag CAS (`internal/budget/kill.go:141`) and deterministic doc IDs with update-time preconditions (`internal/budget/rollover.go:511,526`). See **#16**, **#17**. |
| **Daily token-spend cap that "counts toward the project's existing spend caps"** (§9) | The existing caps are **per run**, keyed to a run's Firebase identity (`internal/budget/token/token.go:83-88 Claims`; `internal/budget/evaluate.go:135 Evaluate`). A long-lived service has no run identity. See **#25**. |
| **Drift reconciliation: a scheduled job via the same Terraform** (§9) | This shape exists: Cloud Scheduler → HTTP POST to the Cloud Run Admin `jobs:run` API (`deploy/terraform/gcp/modules/installation/history.tf:180`, `*/15 * * * *`; a daily rollover at `:223`). It targets a **job**, not a service. See **#18**. |
| **Console project-wide view; "ask the coordinator"** (§10) | The console's nav is Live / Spend / Projects (`console/src/renderer/entry.ts:490-496`); its data plane is one interface over the `fugaro` CLI as a sidecar (`console/src/source/cli.ts:1-8`), with thirteen read operations and one `link.openExternal` (`console/src/main/ipc.ts:307-558`, fourteen entries). The v2 *spec*'s "Overview" is unbuilt, and v2's ruling is one write only — launch (D22, `docs/design/desktop-console-v2.md`). See **#27**, **#28**. |
| **Advisory, not authoritative; "never blocks or reorders merges"** (§3) | Consistent with the repo, and it has a hard consequence: the coordinator must hold **no GitHub write permission**. The App's per-run token asks for `contents: write, pull_requests: write, issues: read, metadata: read` (`internal/gitprov/github/apptoken.go`'s `tokenPermissions`); a read-only set is not merely expressible, it is **minted today** — `{contents: read, metadata: read}` (`buildTokenPermissions`, exported as `BuildTokenPermissions`) — by two callers, both of which only clone (`internal/cli/gitcred.go:107`, `internal/cli/imagecheck.go:517`). See **#9**, **Q3**. |
| **Self-hosted; no Fugaro-operated central service** (§3) | Consistent: everything is per-installation, and managed projects were gated off in the release this table was written against (`internal/cli/recipes_skew.go`'s `managedDeployerShipped`; 0.10.0 ships it true, with the deployer's fence unmeasured — Check 68 is owner-run after the release). |
| **"Works with any MCP-capable agent, not only Claude"** (§7) | The repository drives **only Claude Code** (`docs/base-image.md`). A front door for arbitrary MCP clients is a new integration and a new untrusted-input surface. See **#8**, **#13**. |

---

## 2. Findings

Numbered **#1–#30**. Each cites a path and a symbol and states its consequence. Where a
finding needed a decision from the owner, the question it feeds is named — and the owner's
answer to that question is in §3. The findings themselves are unchanged by the answers.

### Deployment and provisioning

**#1 — "One coordinator per Fugaro project, deployed into that project's Firebase/GCP project" is well-formed, and it names the installation. — Exists.**
`docs/design/v1.md` defines a Fugaro project as *"one GCP project, plus (from M9) one
Firebase project … its repositories; its people lists (`launchers`, `operators`, and from
M9 `budget_admins`); one local config"*, and the same document's **Installation** entry
defines the **installation** as *"the project's cloud-side setup, as `fugaro init` creates
it"*. So the spec's unit of
deployment and the repo's unit of cloud setup are the same object under two names.
*Consequence:* the spec's "per project" and the mobile design's "per installation"
(`docs/design/always-on-orchestrator.md` §4.1, PR #632) are **not** in conflict — but a
reader who does not know that will read them as a conflict. It is a glossary problem, not
a design one (**#29**).

**#2 — There is no Cloud Run *service* in Fugaro, and adding one is a decision the code enforces. — Gap (the load-bearing one).**
Every deployed workload is `google_cloud_run_v2_job`: `modules/installation/history.tf:70`,
`modules/project/job.tf:10`, `modules/workflow/job.tf:4`, `modules/repo/check.tf:16`. A grep
for `google_cloud_run_v2_service` over `deploy/` returns nothing, and the string is
**absent from the plan allowlist** `internal/infra/tf/cover.go:22 allowedTypes` — whose own
comment says *"TestAllowedTypesCoverTheModules fails when a module adds a type that is not
listed here, so adding one is a decision, not an accident."* The grant table beside it
(`cover.go:109 grantRules`) is likewise an allowlist, and a `*_iam_member` type is not even
present for a service.
*Consequence:* the coordinator would be **the first request-serving workload in Fugaro**.
It needs a new allowlisted type, a new grant row, and a decision about which `fugaro init`
confirmation covers it. It also needs everything a service needs that a job does not: an
ingress setting, an invoker grant on a *service*, `min_instance_count`, a request timeout,
and a concurrency setting. Three of those five are not wholly absent, and the distinction is
the difference between "the tree has never heard of this" and "the tree knows it and has no
resource that states it":
`internal/infra/tf/managed_cost.go` already names `min_instance_count` **and**
`manual_instance_count` on `google_cloud_run_v2_service` — the first entry of the managed
cost guard, whose line is stated once as `managedCostMaxMinInstances = 0` and whose comment
says *"Zero is the provider's own default"*. The knob, its zero, and what it costs are all
in the tree; what is missing is a module that sets it. An invoker grant exists as a
mechanism but only ever on a job: `google_cloud_run_v2_job_iam_member` is on the plan
allowlist (`internal/infra/tf/cover.go:28`) and in the grant table (`:145`), and three
modules use it (`modules/installation/history.tf:154`, `modules/project/iam.tf:7`,
`modules/repo/check.tf:102`). `ingress` appears in the tree only as the run VMs' **firewall**
deny-ingress rule (`modules/compute/network.tf:58`), which is network ingress rather than
Cloud Run's own. A timeout exists as a job's *task* timeout
(`internal/backend/backend.go:336 DefaultTaskTimeout`, ten minutes), not as a service's
request timeout. A concurrency setting has no precedent anywhere. → **Q1**.

**#3 — "Part of project init's existing Terraform" is the right shape with the wrong noun, and the plan gate is an allowlist that a new service breaks. — Wrong (the noun) / Rule (the gate).**
`fugaro init` applies the **installation** root (`internal/cli/init.go`'s `installRoot`, the
function that plans and applies it — cited by symbol rather than by line, because a line
number here drifts with every edit to that file and this document's contract is that every
citation can be followed). There is a Terraform module called `project`
(`deploy/terraform/gcp/modules/project/`) — but it is the *shared project jobs* module: it
creates one Cloud Run job and one job service account per (profile, provider, account)
(`modules/project/job.tf:10`, `modules/project/sa.tf:5`) and nothing else. It creates no
Firestore, no RTDB, no bucket, no scheduler job, no secret. So "project init's existing
Terraform" is ambiguous in a way that matters: the coordinator belongs to the
**installation** root, beside the history job and the scheduler jobs.
The gate: `init`'s one confirmation covers a plan only when every resource type is in
`allowedTypes` and every grant is in `grantRules` (`internal/cli/init_review.go:77
runCovers`, `:127 notCovered`); anything outside gets its own typed confirmation, and a
coding agent is refused outright (`:143-144`, `&initflow.AgentError`). A Cloud Run service
is outside the allowlist, so it would be **uncovered** — which is the mechanism working as
designed, not a defect. → **Q1**, **Q10**.

**#4 — "Whoever can reach the coordinator is in the project; no extra permission model" contradicts the standing design and has no plumbing to reuse. — Wrong / Rule.**
The repository refuses reach-based membership everywhere it can. The compute module's own
variable help says it in as many words: *"NEVER a domain: member, allUsers or
allAuthenticatedUsers"* (`deploy/terraform/gcp/modules/compute/variables.tf:131`), and the
validation error at `:122` explains why. Launchers are per-person IAM members on a single
job (`modules/project/iam.tf:7 google_cloud_run_v2_job_iam_member "launcher"`, with the
comment *"on this job only"*). `SECURITY.md:40` states the assumption the whole system
rests on: *"Everyone who can push to the repository, everyone who can launch a run, and
(for follow-up runs) the people listed in `followup.trusted` can steer an agent … Fugaro is
built for a team running it on its own code."* There is no sentence anywhere in the tree
that makes network reach a membership test.
And there is nothing to build the alternative from quickly: the CLI authenticates with
**user ADC**, not an ID token (`internal/backend/gcp/gcp.go:86-88` — *"Application Default
Credentials (`gcloud auth application-default login`)"*), the only ID token minted anywhere
is the managed deployer's impersonation with audience `fugaro-managed-apply`
(`internal/cli/managed_apply.go:810-822`), and the console deliberately **holds no
credential at all** (`console/src/main/spawn.ts:1-8`).
*Consequence:* v1 cannot ship without an authentication answer, and the answer is a new
decision, not a reuse. → **Q2**.

**#5 — Offboarding a person is manual and incomplete, so a "no extra permission model" v1 would have no way to remove a departed developer. — Gap.**
`README.md:201` still carries the unchecked roadmap item *"One-command offboarding of a
repository"*, and no page states an offboarding remedy any more: the runbook that carried one,
`docs/gcp-setup.md`, is the short setup page now. Removing a **person** means editing the
launcher/operator lists and re-running `init` with `--allow-delete`, or a hand-run
`gcloud iam service-accounts remove-iam-policy-binding`. There is no expiry and no per-person audit trail. (The phone's
identity is the one exception: deleting the Firebase Auth user revokes it,
`internal/budget/token/token.go:57-59 ErrRevoked`.)
*Consequence:* every new reachable surface inherits an offboarding story that is a
documented manual procedure. → **Q2**.

### State

**#6 — The budget database is the Realtime Database; Firestore is a second store used only for daily history. Both exist; neither is provisioned the way the spec assumes. — Exists, with a correction.**
RTDB: `internal/budget/model.go:1-2` — *"Package budget is the data model of the shared
budget database (a Firebase Realtime Database …)"*; client `internal/rtdb/client.go:1-9`
(*"a small REST client for a Firebase Realtime Database … The Firebase Admin SDK cannot be
used: its Go client authenticates as an admin, but a run holds only its own ID token"*).
It holds `config/caps/*`, `config/policy/*`, `config/kill/*`, `config/admin`,
`fugaro/{project,mark}`, `runs/<slug>/<run>/*`, `spend/<day>/*`, `agents/*`, `outcomes/*`
and the mailbox. No one file lists the whole tree: the path constants for `runs/`, `spend/`
and `outcomes/` are `internal/budget/model.go:55,57,81`; the four top-level nodes a rollover
moves are `internal/budget/rollover.go:342`; the policy and admin nodes are described in
`internal/budget/rules/rules.go:44-51`; the agents registry is read at
`internal/budget/sweep.go:121`; and the mailbox is a rules macro rather than a package
constant (`internal/budget/rules/rules.go:375-398`).
Firestore: `internal/firestore/client.go` package doc — *"a small REST client for Cloud
Firestore (native mode), built for the spend history"* — and the one collection is
`spendDaily` (`internal/budget/rollover.go:48 SpendCollection`).
Provisioning differs, and the difference is deliberate: RTDB **is** Terraform
(`modules/firebase/firebase.tf:20 google_firebase_database_instance "this"`,
`prevent_destroy = true`, *"It holds the budget counters and the run registry"*), while
Firestore is **not** — `deploy/terraform/terraform_test.go:945-955
TestNoFirestoreDatabaseResource` forbids it, with the reason *"the Firestore database is an
idempotent REST ensure step (a Terraform create of a singleton does not adopt)"*, and the
Go side is `internal/cli/init_firebase.go` (`firestoreStep`, `deployDatabase`).
*Consequence:* choosing Firestore for coordinator state (spec §5) means choosing the store
that has no rules path for a client, no Terraform, and a client with no transactions. See
**#7**, **#16**, **Q5**.

**#7 — "Firestore's real-time listeners drive live updates in the console" is wrong twice over. — Wrong.**
(i) The client cannot listen: `internal/firestore/client.go`'s package doc lists its
operations as `Get`, `Patch`, `Delete`, `Query`, `GetDatabase`/`CreateDatabase` — there is
no listener, no `Listen`, no streaming read. (ii) The console renderer **cannot** connect:
`console/src/main/protocol.ts:132-143 CONTENT_SECURITY_POLICY` is
`default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:;
font-src 'self'; connect-src 'none'; form-action 'none'; base-uri 'none';
frame-ancestors 'none'; object-src 'none'`, and the comment at `:126-128` says why —
*"`connect-src 'none'` is the load-bearing one: the renderer never talks to the network,
because every byte it draws arrives over IPC from the main process."* The renderer reaches
data only through `window.fugaro` → `ipcRenderer.invoke` (`console/src/preload/index.ts:238`)
→ a fixed table of fourteen operations, thirteen of them reads plus `link.openExternal`
(`console/src/main/ipc.ts:307-558`). **Eleven** of the fourteen spawn the `fugaro` binary
with an exact argv (`console/src/main/spawn.ts:326+`); the other three carry `command: null`
and never spawn anything: `startup.compatibility` and `installation.current` are reads of
the console's *own* state, answered in the main process
(`console/src/main/ipc.ts:526-548` — the source comment says of the first that *"it is the
one read that is not a read of the CLI"*), and `link.openExternal` is the one path to the
user's browser. So the coordinator's console view joins an eleven-command data plane, not
fourteen, and two of the three exceptions are the console's own handshake rather than the
project's data.
*Consequence:* live updates in the console are a **CLI-sidecar polling** problem, exactly
like today's `live.watch` (`['watch','--json']`), not a listener problem. Whatever the
coordinator stores, the console sees it only if a `fugaro` command can read it. → **Q8**.

**#8 — There is no MCP server, no MCP dependency, and one loopback HTTP server in the whole repository. — Gap.**
`internal/gateway/gateway.go:1-7` is the only HTTP server: *"a loopback proxy between
Claude Code and the model API"*, started at `:298 Start`, bound at `:308` to
`127.0.0.1:0`. Its routes are `HEAD /api/hello`, `POST /v1/messages`,
`POST /v1/messages/count_tokens`. Nothing else in the tree serves a request.
The spec's own claims about the protocol also need care: **MCP authorization is
OAuth 2.1-based in the published specification** — this could not be verified from this
container (see the UNVERIFIED table), so it is recorded as the spec's own statement, not as
a checked fact. What is checkable from here is the consequence: the repo has **no OAuth
2.1 authorization server, no dynamic client registration and no resource-metadata
endpoint**, so an MCP-conformant authorization story is new work, not a reuse. And the
spec's own §7 says the front door is open to *"any MCP-capable agent, not only Claude"*,
which widens the untrusted-input surface beyond Fugaro's own runs. → **Q2**, **Q16**.

**#9 — A read-only installation token exists and is minted today; what does not exist is a *project-wide* one, and the per-repository scope is baked into the token call. — Exists / Gap.**
`github.MintInstallationToken(ctx, o, perms)` takes the permissions as an argument
(`internal/gitprov/github/apptoken.go`'s `MintInstallationToken`), and the read-only set is not
a proposal: it is `buildTokenPermissions`, `{contents: read, metadata: read}`, exported as
`BuildTokenPermissions()` for the callers that keep a copy. Two callers mint with
it — `internal/cli/gitcred.go:107` (the image build's credential step, `mintBuildToken`,
whose doc says the token *"can only read the one repository"*) and
`internal/cli/imagecheck.go:517` (the daily image check, which also only clones) — against a
run's own `tokenPermissions` set, which asks for `contents: write, pull_requests: write`
(`internal/gitprov/github/apptoken.go`'s `tokenPermissions`).
So the coordinator's read-only credential is a **reuse of a mechanism that already runs in
production**, not a new one: the named-set pattern, the accessor, and the two call sites are
all there, and the change is to name a third set wide enough to read pull requests
(`pull_requests: read` and `metadata: read`, the two reads **Q3**'s answer names — see the
narrowing note there) and call it. What the existing
read-only token cannot give is reach across repositories: the body posted to GitHub is
`{"repositories": []string{s.repo}, "permissions": perms}`
(`internal/gitprov/github/apptoken.go`'s `tokenSource.Token`) — **one repository per token**.
A project-wide reader therefore needs either N tokens (one per repository) or a
project-wide installation token with `repositories` omitted, which `tokenSource` cannot
express.
The credential is already project-scoped, and this is decided, not incidental:
`internal/infra/secretmounts_test.go:93-101 TestGitSecretScopeIsDecidedPerProvider` — *"a
provider's credential is either per repository … (`config.PerRepositorySecret`, the owner's
ruling of 2026-10-15, which is `bitbucket-token`) — or the project's (`github-app-key`, one
App serving a team's repositories)."* The logical secret name is `github-app-key`
(mounted as `FUGARO_GITHUB_APP_PRIVATE_KEY`, `internal/infra/secretmounts_test.go:65`).
*Consequence:* GitHub is the one provider where a project-wide coordinator has a credential
model that already exists. What is missing is a named read-only permission set and a caller
that mints across repositories. → **Q3**, **Q12**.

**#10 — No PR record, no dependency graph, no version roadmap, no override log exists. — Gap.**
`runstore` records **runs**, not pull requests (`internal/runstore/launch.go`,
`internal/runstore/list.go`); `gitprov.Provider` reads **one** pull request at a time
(`internal/gitprov/gitprov.go:208-270`); releases are git tags plus
`docs/releases/vX.Y.Z.md` and `docs/release.md`. Nothing in the tree maps a PR to a
release, or a PR to another PR. The v2 console spec proposes a dependency graph and an
Overview view, and both are explicitly unbuilt
(`docs/design/desktop-console-v2.md` — *"**Nothing in v2 is approved or built**"*).
*Consequence:* four of the spec's six state items (§5) are greenfield. They are also the
four that need **no** LLM, which is what makes finding **#28** possible.

**#11 — "Which developer is working on what" rests on a self-declared, unauthenticated string today. — Rule.**
`launch.json` carries `LaunchedBy` (`internal/runstore/launch.go`), set once at launch from
`spec.RequestedBy` (`internal/cli/run.go:1731`), which is `plan.me` =
`env.lc.Me(ctx)` = `localcfg.Config.Me` — the config's `user:` field, else
`git config user.email`. It is written, never verified, and the same value reaches the
budget token's `rb` claim (`internal/budget/token/token.go:83-88 Claims.RB`).
*Consequence:* a coordinator that aggregates "who is working on what" out of run records is
aggregating a string any launcher can write. That is fine for a dashboard and not fine for
anything the coordinator *tells* people, which is a reason to keep v1 advisory and
read-only. → **Q2**, **Q13**.

**#12 — What already exists of "active work": the run registry, the mailbox, and a per-person money rollup — and nothing that is cross-repository in one view. — Exists (partly).**
Present: the RTDB run registry `runs/<slug>/<run>/*` (`internal/budget/model.go:30-86`),
the live counters `agents/<slug>/<run>` and `outcomes/<day>/*`, the mailbox
(`internal/budget/rules/rules.json.tmpl:193-231`, `inbox`/`outbox`/`heartbeat`, macros
`MAILBOX`/`CONSUMER` at `internal/budget/rules/rules.go:375-398`), `fugaro watch`,
`fugaro runs ls`, `fugaro logs`, `fugaro diagnose`. Per-person money: Firestore
`spendDaily.byPerson{<requested_by>}`, read by `fugaro report --by person`
(`internal/cli/report.go:499`).
Absent: any **project-wide** view. Every one of these is per repository slug, and
`fugaro stats` refuses the question outright (`internal/cli/stats.go:62-64`, quoted in §0).
*Consequence:* the smallest useful v0 is a **join across slugs**, not a new store — which
is exactly what the CLI can do today from the two stores it already reads. → **Q13**.

### Authentication, the front door and prompt injection

**#13 — Prompt injection is the central threat, and the spec has no rule about what the coordinator may read. — Rule.**
The coordinator's inputs are, by the spec's own §6, *"intent signals from local
orchestrators"* — free text written by whoever can reach the endpoint — plus GitHub PR
titles, bodies, comments, labels and branch names, which are written by whoever can push.
Its outputs are suggestions and, at §10, prose answers. There is no trust boundary in the
spec between "text the coordinator reads" and "instructions the coordinator follows".
The repo's own position is the opposite of casual: `SECURITY.md:6` — *"The design assumes
you trust the repository and the source of the task: anyone who can push to it, launch a
run, or is listed in `followup.trusted` can steer an agent that holds the credentials
below."* That is a boundary the coordinator's cross-repository reach would **remove**:
today a repository's text steers only that repository's runs.
*Consequence:* v1 needs a written rule — the coordinator may read GitHub text and intent
signals as **data**, may never act on an instruction found in them, may never write to
GitHub, and may never call a tool because a document told it to. → **Q4**, **Q14**.

**#14 — The coordinator must hold no GitHub write permission, and that is a hard rule the code can enforce. — Rule.**
Spec §3: *"Advisory, not authoritative … It never blocks or reorders merges."* The
mechanical form of that sentence is a read-only GitHub credential. `MintInstallationToken`
already takes the permission set as an argument (`internal/gitprov/github/apptoken.go`'s
`MintInstallationToken`),
so the rule is enforceable by never minting anything but a read-only set — and the repo does
not merely have the precedent for a named, narrower set beside the run's, it **runs one
today**: `buildTokenPermissions` (`internal/gitprov/github/apptoken.go`'s own,
`{contents: read, metadata: read}`) exists
precisely because *"it only clones the repository"*, and it is minted by two callers
(`internal/cli/gitcred.go:107`, `internal/cli/imagecheck.go:517`). A coordinator that holds
no write permission is therefore not asking for a new kind of credential; it is asking for a
third named set in a pattern the tree already uses.
What could **not** be verified from here: the permissions the App itself has been granted on
GitHub. `tokenPermissions` is what a run *asks for*, and GitHub caps it at the App's own
grant; the App's grant is configured in GitHub's UI, not in this repository.
*Consequence:* a named `coordinatorTokenPermissions` set — proposed here on 2026-10-19 as
`{contents: read, pull_requests: read, issues: read, metadata: read}` and, since the
2026-10-25 narrowing, `{pull_requests: read, metadata: read}`, and no `write` anywhere — is
the rule's implementation, and the App's own grant has to be checked by the owner. → **Q3**.

**#15 — The existing App can be reused for a read-only coordinator, but "reused" has a caveat the owner must accept. — Open.**
Reuse is plausible for three reasons: `github-app-key` is already the project-scoped git
credential (finding **#9**), the App is already one App per team's repositories
(`internal/infra/secretmounts_test.go:97`), and the token call takes its permissions as an
argument. The caveat: **the App is also the credential a run uses to write pull requests**
(`internal/gitprov/github/apptoken.go`'s `tokenPermissions`), so the same private key would
sit in two places with two different permission ceilings, and the coordinator would hold a
key that *can* mint a write-capable token even if it never does. The alternative is a
second, read-only App.
*Consequence:* this is a blast-radius question, not a convenience one — it decides what a
compromise of the coordinator is worth. → **Q3**.

### Long-running behaviour, events and idempotency

**#16 — "Firestore transactions" is not available; a per-project lock does not exist; the repository's real concurrency idiom is CAS. — Wrong / Gap.**
`internal/firestore/client.go` has no transaction support (no `BeginTransaction`,
`Commit`, or `RunTransaction`; the package doc lists its operations and they are
`Get`/`Patch`/`Delete`/`Query`). `internal/rtdb/client.go` likewise: the only conditional
write is `PutIfMatch` (ETag/If-Match), used by `budget.SetKill`
(`internal/budget/kill.go:141`) and by the sweeper (`internal/budget/sweep.go:284-295`).
The one distributed lock is per **branch**, not per project: `internal/lock/lock.go:1-5` —
*"the one-run-per-branch lock … an object `locks/<slug>/<branch-hash>` … Creation is
create-if-absent; an expired or unreadable lock is taken over with a generation-matched
overwrite."*
*Consequence:* the coordinator's "never contradicts itself" property (spec §9) has to be
built from ETag CAS, generation-matched writes and deterministic document IDs — the
idiom that already exists — or from a new per-project lock, which would be the second lock
in the system. → **Q6**.

**#17 — "The same event delivered twice must be harmless" has a house idiom, and it is not an `Idempotency-Key` header. — Exists (as a pattern).**
There is no `Idempotency-Key` anywhere in the tree. What exists, and what the coordinator
should copy: generation-matched GCS writes (`internal/blobx/blobx.go:238,334,365`), the
publish guard that refuses a lost race rather than overwriting
(`internal/cli/config_publish.go:94-98,145,151`), RTDB ETag CAS with `SetAttempts = 5`
(`internal/budget/kill.go:94,141`), deterministic Firestore doc IDs with
`MustNotExist()`/`IfUpdateTime` preconditions (`internal/budget/rollover.go:511,526,548`),
and deterministic run IDs (`internal/task/task.go:328 NewRunID`, shape pinned by
`internal/runstore/list.go:17 runIDRE`).
*Consequence:* the spec's idempotency requirement is satisfied by deriving an event's
document ID from the event itself, not by adding a header. Worth saying in the plan so
nobody invents a fourth mechanism.

**#18 — "Drift reconciliation via a scheduled job" is exactly the pattern the repo already runs; what does not exist is a scheduler target that is a service. — Exists.**
`deploy/terraform/gcp/modules/installation/history.tf:180 google_cloud_scheduler_job
"history_sweep"` fires `*/15 * * * *` as an HTTP POST to the Cloud Run Admin API's
`jobs:run` for the `history` job; `:223` adds a daily `--rollover` at `30 0 * * *`, paused.
The job itself is `modules/installation/history.tf:70 google_cloud_run_v2_job "history"`
(`timeout = "600s"`, `max_retries = 0`), entered through `fugaro budget history`
(`internal/cli/history.go:70`, `--sweep` at `:101`, `--rollover` in
`internal/cli/history_rollover.go:25`). Two more scheduler jobs exist the same way
(`modules/compute/preload.tf:110`, `modules/repo/check.tf:116`).
*Consequence:* the reconciliation job needs **nothing new** — it is a job and a scheduler
entry. This is the cheapest half of the spec to build. → **Q6**.

**#19 — Event intake has three candidate mechanisms and none of them exists. — Gap.**
There is **no Pub/Sub** (no `google_pubsub_*` resource, no `cloud.google.com/go/pubsub`
dependency), **no webhook receiver** and **no GitHub webhook** anywhere in the tree. The
three candidates each need something:
- *GitHub webhooks* need a public HTTPS endpoint, a webhook secret in Secret Manager, and
  signature verification — and they would make the coordinator the **first public surface
  Fugaro has ever had**, which is precisely what finding **#4** says the design refuses.
- *Polling* needs only the existing Scheduler → `jobs:run` pattern (finding **#18**).
- *The reconciliation job* is the same mechanism as polling, with a different cadence.
*Consequence:* polling is the option that adds no reachable surface and no new credential.
→ **Q7**.

**#20 — Cloud Run request timeout is a service-only concern, and the repo has no service; its jobs run up to 168 hours. — Exists (a ceiling worth knowing).**
`internal/backend/gcp/limits.go:12-16` — *"MaxTaskTimeout is the longest task timeout a
Cloud Run job accepts (168 hours …)"*; the default when a job sets none is
`internal/backend/backend.go:336 DefaultTaskTimeout = 10 * time.Minute`; a job's timeout is
the workflow's `timeouts.total` plus `internal/backend/backend.go:29 TaskTimeoutSlack`
(2 minutes). This repository's own `fugaro.yaml` sets `total: 2h`.
*Consequence:* the spec's worry about long LLM calls behind a request timeout is real **for
a service** and has no precedent here — a service's request timeout is a new knob with a new
failure mode (a client that gives up while the work continues, or work that is killed
mid-flight). → **Q1**.

### Models, credentials and cost

**#21 — "Reuses the project's existing key management and security service" names a service that does not exist, and "no new secrets path" is false. — Wrong.**
`docs/credentials-service/README.md:3` — *"**Status: plan only; nothing is implemented;
decisions answered and O1–O4 settled 2026-10-17; no open items; no task approved.**"* The
directory's only commit is `49712fed`, and a grep for `creds/`, `Cascade` or
`credentials-service` across `internal/`, `cmd/` and `deploy/` returns nothing. The service
it describes is a **standalone Cloud Run service per project** holding the model keys
(`docs/credentials-service/spec.md:57` — *"It is the only identity with Secret Manager
access to the OAuth tokens and API keys"*).
So the coordinator's §8 sentence points at a document, not a service. And the second half
is false on its own terms: any Cloud Run service needs its own service account, its own
`secretAccessor` grants and its own IAM invoker grant — which is a new secrets path even if
it reuses the same Secret Manager.
*Consequence:* the coordinator's model credential is a **new decision**, and the
credentials-service decisions do not already cover it. → **Q4**.

**#22 — Which of D130–D154 cover "a long-lived service holding a model credential"? D143 and D145 together, and neither is worded for this. — Open.**
The governing pair, from `docs/credentials-service/reconciliation.md`:
- **D143** (`:183`) accepts the service as a single point of compromise *"with these
  bounds: its SA holds `secretAccessor` on model and App secrets only; no human holds
  `actAs` on it except operators; no shell, no admin endpoints, secrets-only boundary
  (F36); every issuance audited by name; per-run caps still enforced by the gateway in the
  job. Availability: if the service is unreachable the run halts like
  `budget_unavailable` … never falls back to an in-job key."*
- **D145** (`:185`) — the service holds *"Model credentials (slice 2) and the GitHub App
  key (slice 3)"*.
Neither says "a long-lived service may hold a model credential for its **own** use". D143
bounds a service that issues credentials **to runs**; the coordinator would spend a model
credential **itself**. The nearest other statement is F36 (`:154`), which keeps the service
*"secrets-only"* and sends other always-on features to sibling services that call it.
*Consequence:* the coordinator is either a **second** long-lived secret holder (a new
decision, with its own D143-style bounds) or a **client** of the credentials service (a
dependency on unbuilt work). There is no third reading. → **Q4**.

**#23 — A "two-tier" model config has no home in the config schema; the nearest concept is a per-slot model. — Gap.**
Today's model configuration is per **slot**: `agent.model` and
`agent.models.{coder,reviewer,background}` (`docs/design/m10-multi-model.md`), with the
routing switch `routing.claude: {via: direct|openrouter|auto, models: [...]}`
(`internal/config/routing.go:42-61`, values at `:14-24`, validated by `CheckRouting` at
`:98`). A published project policy can carry `providers`, `model_prices` and `routing`
(`internal/projectpolicy/policy.go:128-134`, `Policy{... Providers, Prices, Routing}`).
There is no "tier" concept, and no place a coordinator's two tiers would be written: the
policy's `budget` block carries `Mode`, `PerRunUSD`, `AllowedModels`, `MaxRunTokens`
(`internal/projectpolicy/policy.go:192`), which is a run's ceiling, not a service's slots.
*Consequence:* a two-tier config needs a new key — most naturally two named slots in the
published policy, matching the `agent.models` shape rather than inventing a third. → **Q9**.

**#24 — `allow_data_to` is the gate a project-wide coordinator has to widen, and widening it is an owner-level change. — Rule.**
`internal/config/providers.go:74 AllowDataTo` — *"the repositories (owner/name) whose code
may be sent here. Absent (the zero value) or `["*"]` mean every repository"* — enforced by
`internal/config/providers.go:194 AllowsData`, which is *"the single gate"* for both the
key's mount and the routing check. The owner set this deliberately: `["*"]` means *every*
repository, and a written-but-empty list is refused as ambiguous.
A project-wide coordinator reads **every** repository's PR text and sends it to a model.
So its provider must name every repository — in practice `["*"]` — which is a wider
statement than any repository has made so far, and it is the difference between "my code
goes to OpenRouter" and "everyone's code goes to OpenRouter".
*Consequence:* this is the one line in the spec that silently widens a per-repository
consent into a project-wide one. It belongs in front of the owner. → **Q9**.

**#25 — The daily cap: the existing model is per run, keyed to a run identity a long-lived service cannot have. — Rule.**
`internal/budget/evaluate.go:135 Evaluate` checks kill switches, the per-run cap, the
repository daily cap and the project daily cap, in that order (`:156-161`, `:188-193`,
`:196-204`, `:205-210`), against counters keyed by day and slug
(`internal/budget/model.go:57-70 PathSpendGlobal/PathSpendRepo/PathSpendRun`). The identity
behind all of it is the run's Firebase token: uid `r~<slug>~<run>`, claims `fs`/`fr`/`fp`/
`rb`/`fx` (`internal/budget/token/token.go:83-95`), where `fx` is *"launch + job timeout +
slack"* (`token.ExpiryFX`) — and D134 makes the credentials service verify exactly that
token, *"revoked when the runner finalises the run (and by `fx` as a backstop)"*
(`docs/credentials-service/reconciliation.md:174`).
A long-lived coordinator has no run: no `fs`, no `fr`, and an `fx` it cannot honour. And the
RTDB rules gate `spend/` and `runs/` on the `RUN` macro
(`internal/budget/rules/rules.go:396`, `internal/budget/rules/rules.json.tmpl:28-33`), so no non-run identity may
write the counters.
The spec says the cap is a **token** cap that *"counts toward the project's existing spend
caps"*. The repo caps **dollars** (`DailyMicros` at `internal/budget/model.go:151,164` — the
global and per-repo cap fields — and `ReasonGlobalDailyCap` at
`internal/budget/evaluate.go:32`); the only token cap is a per-run policy field
(`MaxRunTokens`).
*Consequence:* "counts toward the project's existing spend caps" needs a new mechanism —
a new node, a new writer identity, or a synthetic run — and it is not a configuration
change. → **Q11**.

**#26 — Cost arithmetic at the spec's own scale (10 developers, 50 services), from the repo's own numbers. — Open.**
From the price catalog the repo ships (`internal/pricing/catalog/catalog.json`,
`checked_at 2026-10-23`): `deepseek/deepseek-v4.1-flash` is $0.07–$0.60 per million
input tokens and $0.18–$2.40 per million output; the catalog's own note is that *"a run
that charges $5 may really cost up to about $189"* under the cheapest-endpoint
undercharge (`docs/design/provider-price-catalog.md`, on *"`cheapest` can undercharge"*). The built-in Claude table
(`internal/pricing/table.go Embedded()`) is Haiku 4.5 $1/$5, Sonnet 5.5 $2/$10,
Opus 5.5 $4/$20, Fable 5.1 $10/$50 (cited by symbol rather than by line: the function's body
is a list that gains a model at a time, and a line range here was already wrong once — Fable
5.1 sits at `:61-62`, outside the range this sentence used to name).
A rough arithmetic, stated as arithmetic and not as a quote:
- **Events per day.** 10 developers, each opening or updating a handful of PRs a day, plus
  branches, labels and merges: call it **50–200 events/day** at the project scale the spec
  names.
- **Routine tier.** At Haiku 4.5 rates, a 2,000-token event plus a 300-token answer is
  about **$0.0035**; 200 events is under **$1/day**. This tier is not the cost problem.
- **Reasoning tier.** At Opus 5.5 rates, a 20,000-token context with a 1,000-token answer
  is about **$0.10**; run it on every event and 200 events is **$20/day**, roughly
  **$600/month** — which is the number that matters, and it is some thirty times the
  routine tier's. Rebuilding a project-wide context on each wake-up (spec §9) is what
  makes the context large.
- **Idle cost.** A scale-to-zero Cloud Run service costs nothing while idle; Cloud
  Scheduler is free at these volumes; RTDB and Firestore at this scale are within the free
  tier or single-digit dollars a month. The idle cost is **not** the risk.
*Consequence:* the cap in the spec is a **reasoning-tier** control, and the arithmetic says
so. Anything that runs the reasoning tier on every event is the whole cost. → **Q11**,
**Q13**.

### The console, the sibling design, providers and naming

**#27 — The console gains no project-wide view for free, and its write surface is already ruled. — Gap / Rule.**
The nav is three views (`console/src/renderer/entry.ts:490-496`) and the data plane is one
interface (`console/src/source/source.ts`, `console/src/source/cli.ts:1-8`) whose v1
implementation spawns the installed `fugaro`. The IPC table is fourteen entries — thirteen
reads plus `link.openExternal` (`console/src/main/ipc.ts:307-558`). v1 is *"strictly
read-only"*
(`docs/design/desktop-console.md`, D2), and v2's ruling is **one** write — launch,
through `fugaro run`, *"with no command queue"* (`docs/design/desktop-console-v2.md`,
D22). So the console's §10 "ask the coordinator" is a **new** interaction, and it is the
only one in the spec that makes the console send free text to a model.
*Consequence:* the console can gain the project view as new **read** operations over the
CLI sidecar without touching the write ruling — but "ask the coordinator" is a write-shaped
operation and a new ruling. → **Q8**.

**#28 — All model output is untrusted text, and the console's rule is text, never markup. — Rule.**
`docs/design/desktop-console.md` §5.1 rule 7 — *"Job-written text is text"* — and the code
follows it: `console/src/renderer/app.ts` writes through `element.textContent`
(`:124,138,159,174`), never `innerHTML`; the sanitizers are
`console/src/shared/sanitize.ts` (`sanitizeText:2415`, `sanitizeRefusal:2652`,
`safeUrl:2684`); navigation and window-open are denied
(`console/src/main/window.ts:231 denyNavigation`, `:242 denyWindowOpen`), and there is
exactly one validated `shell.openExternal`.
A coordinator answering *"what's holding up the payments release?"* produces free text that
a model wrote from untrusted inputs. If that text were rendered as markdown with live links,
a PR title could make the console open a URL.
*Consequence:* the rule is mechanical and should be written into the coordinator's design,
not left to the renderer's existing discipline: coordinator answers are plain text, never
markdown, never a link the user did not ask for. → **Q8**, **Q14**.

**#29 — The close sibling is the always-on orchestrator (mobile P5.1, PR #632), and it makes two choices that contradict this spec. — Open.**
`docs/design/always-on-orchestrator.md` (PR #632, not on `main`) designs **one Cloud Run
service per installation**, `min-instances = 0`, woken by a Cloud Scheduler tick, running
as a dedicated `orch` service account with the launcher's grants and nothing else, and it
answers the credential question with *"(B) today, (A) when it lands — and never a model
credential in the resident service."* It also allocates **no** decision numbers, asking
**Q1–Q7** instead, and records *"the next free live-check number is 110."*
Two contradictions with this spec:
- **Per installation vs per project.** Not a real conflict (finding **#1**): they are the
  same object. It is a wording collision that will confuse a reader.
- **A model credential in the resident service.** PR #632 forbids it; this spec's §8
  requires it. That one is real.
What the two share, and should not each invent separately: the identity model, credential
handling, spend caps, and the hosting module. `docs/mobile/reconciliation.md` (D115)
already sets the conditions for phase 2 — *"the model credential lives in a secret manager
**in the installation's own project**, the service runs as a dedicated identity with a
narrow IAM role, it is behind the **same launch claim** (`runstore.Claim`) and the same
budget caps"* — and `docs/mobile/implementation-plan.md` (P5.1) records it as
**UNSCHEDULED** and **SECURITY-SENSITIVE** precisely *"because it holds a model credential
in the cloud … and it holds `fugaro run` authority."*
*Consequence:* shipping the coordinator's LLM half without a ruling **decides D115's phase
2 implicitly**, which is exactly what the deferral was written to prevent. → **Q10**.

**#30 — Multi-tenancy: GitHub has a project-scoped credential and Bitbucket does not, by the owner's own ruling. — Rule.**
`internal/infra/secretmounts_test.go:93-101` is the record: a provider's git credential is
either per repository — *"(`config.PerRepositorySecret`, the owner's ruling of 2026-10-15,
which is `bitbucket-token`)"* — or the project's — *"(`github-app-key`, one App serving a
team's repositories)"*. The Bitbucket adapter exists (`internal/gitprov/bitbucket`, opened
by `internal/gitprov/providers/providers.go` from `FUGARO_BITBUCKET_TOKEN`, *"repository
access token"*), and its token is a repository access token: it cannot be widened to a
project.
*Consequence:* v1 can aggregate GitHub projects and **cannot** aggregate a Bitbucket
project, not for lack of code but for lack of a credential that a project could hold. A
Bitbucket project must be refused with a line that says so, rather than silently seeing one
repository. → **Q12**.

---

## 3. The owner's answers (Q1–Q16)

Sixteen questions. Each names what it changes in [spec.md](spec.md). They were questions, not
decisions: an answer picks an option and allocates nothing, and no task starts on the strength
of an answer alone. (The five decisions the planning afterwards found are not these: they are
**D193–D197**, and they are in **§6**.)

**The owner answered all sixteen on 2026-10-19, each by taking option (A) — the recommendation — and an answer is not an approval.** The answer is recorded under each question
below, in the owner's own choice of option; the wording of every option and of every
"Consequence" line is this document's, written before the answer, and is left as it was so
that the answer can be read against what it was choosing between. **None of the sixteen has
been accepted, approved or given a decision number** — they are questions, and §6's five
decisions are the separate, numbered ones — and the answers choose a
direction rather than approving work: they are the owner's decision to proceed on that
option, and the plan that follows is a separate document with its own gate. A reader who finds
one of these answers described elsewhere as an approved plan, or as a D-number, should treat
that as an error in the other document.

Two things the answers do not settle, and one thing they do not change, said here so they are
not lost:

- **U2 stays an owner check.** Q3's answer (A) reuses the existing App and asks it for
  `pull_requests: read`; whether the App has actually been granted that on GitHub is
  configured in GitHub's UI and could not be read from this container (§4, U2). The answer is
  contingent on it.
- **U6 and U7 are still drafts**, not merged: Q10's answer (A) was one resident service per
  installation, and the sibling design in PR #632 is where that service is specified (§4).
  **D196's answer of 2026-10-21 leaves U6 and U7 exactly where they were**: the coordinator is
  not tied to that design, so what PR #632 finally rules no longer decides this work's front
  door — the amendment under Q10 records what changed and what did not.
- **The findings in §2 are unchanged.** An answer decides the question it belongs to; it does
  not re-classify a finding, and none of the "Wrong" findings becomes "Exists" because the
  owner chose an option. §0, §1, §4 and §5 are likewise untouched.

### Q1 — Where does the coordinator run, given Fugaro has never run a request-serving Cloud Run service?
**Changes spec §4.**
- **(A) Recommended — a Cloud Run *service* in the installation root for the front door, with the aggregation running as a Cloud Run *job* on a schedule.** The MCP front door must answer a caller synchronously, and only a service can; the aggregation is the part that has an existing pattern (finding **#18**) and no reason to be request-driven. This adds `google_cloud_run_v2_service` and its `*_iam_member` to `internal/infra/tf/cover.go` (a decision the test enforces, finding **#2**), and it means the first service-shaped knobs in the tree: ingress, invoker grants, `min_instance_count = 0`, a request timeout, concurrency.
- **(B) A Cloud Run *job* only, driven by Cloud Scheduler, with no HTTP front door at all in v1.** No new resource type, no new reachable surface, nothing for finding **#4** to answer. Cost: the spec's §7 interface does not exist — no `report_intent`, no `check_conflicts` — so local orchestrators have no way to send intent before code exists, which is the spec's §6 premise.
- **(C) The service first, everything else after.** Highest risk: it makes the hardest part (authentication, finding **#4**) the first thing built, before the deterministic value exists.
*Consequence of (A):* the first service in Fugaro, and the first `fugaro init` stage that adds a reachable resource. It needs its own typed confirmation (`internal/cli/init_review.go`), because it is outside the covered allowlist.

**Answered 2026-10-19: option (A)** — a Cloud Run *service* for the MCP front door in the installation root, with the aggregation as a scheduled Cloud Run *job*: the first service in Fugaro, and the first `fugaro init` stage that adds a reachable resource, so it needs its own typed confirmation.

### Q2 — Who may call the coordinator, and what is the caller's identity?
**Changes spec §4 (the "no extra permission model in v1" line) and §7.**
- **(A) Recommended — Cloud Run IAM invoker with per-person IAM members, exactly as a launcher is granted today, plus a small `fugaro` command that mints the Google ID token so nobody types `gcloud auth print-identity-token`.** It is the only model the repository already validates (`modules/compute/variables.tf:122,144`) and it makes offboarding the launcher-list edit that already exists (finding **#5**). It also reuses the credentials service's own answer for a service's front door (D135, `docs/credentials-service/reconciliation.md:175`). Cost: every developer becomes an IAM member on one more resource, and the ID-token plumbing does not exist yet (finding **#4**).
- **(B) Firebase Auth ID token as the caller identity, checked in the app, with IAM invoker as a second fence.** Reuses the identity world the phone already plans (D111, `docs/mobile/reconciliation.md`) and the `mu` user claim the rules already understand (`internal/budget/rules/rules.go:362`). Cost: Firebase Auth is a *plan*, not a build, and it puts an authorisation decision in application code rather than in IAM.
- **(C) A public URL with an application-level shared secret.** **Refused as written**: `allUsers` and `allAuthenticatedUsers` are refused everywhere in this repository (`modules/compute/variables.tf:122`), and a shared secret has no per-person revocation. Listed only so the refusal is on the record.
*Consequence:* v1's "no extra permission model" is replaced by "the project's existing IAM members list", and the access list (launchers / operators / budget admins) has to be said explicitly — today they are three different things (`internal/infra/firebase.go:117-120`).

**Answered 2026-10-19: option (A)** — Cloud Run IAM invoker with per-person IAM members, as a launcher is granted today, plus a small `fugaro` command that mints the Google ID token: v1's "no extra permission model" becomes "the project's existing IAM members list", and the access list has to be said explicitly.

### Q3 — What GitHub credential does the coordinator hold, and is read-only a hard rule?
**Changes spec §3, §6, §7.**
- **(A) Recommended — reuse the existing App (`github-app-key`) and mint a read-only installation token with a new named permission set `{contents: read, pull_requests: read, issues: read, metadata: read}`, and never a `write`.** This is the smallest step of the three, because the mechanism is already built and running: `BuildTokenPermissions()` (`internal/gitprov/github/apptoken.go`'s exported copy) hands out `{contents: read, metadata: read}` and two callers mint with it today (`internal/cli/gitcred.go:107`, `internal/cli/imagecheck.go:517`), so the coordinator's set is a second var beside `buildTokenPermissions` plus the two read permissions it needs beyond a clone, and its call site is a third caller of the same function. The App key is already project-scoped by the owner's 2026-10-15 ruling (finding **#30**), and `MintInstallationToken` takes the set as an argument (finding **#9**). Cost: the same private key then sits in three places with two ceilings (finding **#15**), the token is still scoped to **one repository** (`internal/gitprov/github/apptoken.go`'s `tokenSource.Token`), so a project-wide reader is N tokens, and the App's own grant on GitHub has to include `pull_requests: read` — which could not be verified from this container.
- **(B) A second, read-only GitHub App, used only by the coordinator.** Cleanest blast radius: a compromise of the coordinator cannot mint a write-capable token at all. Cost: a second App to create, install on every repository, and rotate — and a second secret to manage.
- **(C) A fine-grained personal access token per project.** Cost: a long-lived human credential, no per-repository scoping guarantee, and it dies with the person who made it.
*Consequence:* under any option, "the coordinator holds no GitHub write power" becomes a **hard rule** the code enforces by never asking for a write permission — the mechanical form of spec §3's "advisory, not authoritative".

**Answered 2026-10-19: option (A)** — the existing App, minting a new read-only named permission set and never a `write`, with the App's own grant on GitHub needing `pull_requests: read` (U2, still an owner check): under this option "the coordinator holds no GitHub write power" is a hard rule the code enforces.

**Narrowed 2026-10-25: the set is `{pull_requests: read, metadata: read}`** — the two reads the project-wide view makes, and not the four (A) named on 2026-10-19. The reader opens no file and reads no issue: every pull request is read one at a time through `gitprov.Provider.PullRequest`, which is `pull_requests: read`, and `metadata: read` is what GitHub requires of any request at all. A permission asked for and never used is reach a stolen token would have for nothing, which is the half of this question's hard rule that a "no write" check cannot see. P5's aggregation job re-reads through this same reader, so it asks for no more than these two. The set is `internal/gitprov/github/apptoken.go`'s `coordinatorTokenPermissions`, and the plan's P2 Status note records the narrowing where the part is.

### Q4 — Does the coordinator hold a model credential, or wait for the credentials service?
**Changes spec §8.**
- **(A) Recommended — no model credential in v1 at all.** The deterministic half (findings **#10**, **#12**, **#13**, **#28**) ships first and needs none; the LLM half waits. This is also PR #632's answer for the sibling service (*"never a model credential in the resident service"*, finding **#29**), so it keeps the two designs compatible.
- **(B) The coordinator becomes a second long-lived secret holder, as its own opt-in installation-root resource set.** Cost: it needs its own D143-style bounds (SA holds `secretAccessor` on the model secret only; no shell; no admin endpoints; every use audited), and D143/D145 do not already grant it (finding **#22**). It also decides mobile D115's deferred phase-2 question implicitly.
- **(C) The coordinator calls the credentials service as its model front door, once that service exists.** Cleanest long-term: one identity holds model keys. Cost: it makes the coordinator's LLM half depend on unbuilt work (`docs/credentials-service/README.md:3`), and the service's F36 boundary is *"secrets-only"* — a caller that spends a credential is a different shape from one that issues it.
*Consequence:* (A) is the only option that does not need a new security decision; (B) and (C) each need one, and (B) also needs a ruling on mobile D115.

**Answered 2026-10-19: option (A)** — no model credential in v1 at all: the deterministic half ships first and needs none, and this is the only option that needs no new security decision.

### Q5 — Does the coordinator's state live in the Realtime Database or in Firestore?
**Changes spec §5.**
- **(A) Recommended — the Realtime Database, beside the budget, policy, kill and mailbox nodes.** It is the store the whole system already reads; it has a rules language the console and the phone can be granted into (as D112 already plans for the phone); it has ETag CAS for the coordinator's own writes (finding **#16**); and the CLI already has a client for it (`internal/rtdb`). Cost: RTDB is a tree, so a PR dependency graph is a tree of edges rather than a queryable table, and there is no multi-document transaction — only CAS.
- **(B) Firestore, as the spec says.** Cost: the existing client has **no transactions and no listeners** (findings **#7**, **#16**); the deployed rules deny every client (`internal/infra/firestore.go:36-40 FirestoreRules` — `allow read, write: if false`), so neither the console nor the phone could ever read it directly; and it is not Terraform (`TestNoFirestoreDatabaseResource`).
- **(C) Both — Firestore for the graph, RTDB for the live pointers the console and the phone read.** Cost: two stores to keep consistent, and the consistency mechanism is the one thing neither store gives you.
*Consequence:* (A) keeps the console's project view reachable through the CLI, which is the only path the renderer has (finding **#7**).

**Answered 2026-10-19: option (A)** — the Realtime Database, beside the budget, policy, kill and mailbox nodes, so the console's project view stays reachable through the CLI, the only path the renderer has.

### Q6 — Concurrency and idempotency: which of the repo's existing mechanisms does the coordinator use?
**Changes spec §9.**
- **(A) Recommended — the house idiom: deterministic event document IDs, ETag/`If-Match` CAS for the coordinator's own state, generation-matched writes for anything in GCS, and no new lock.** All four already exist (finding **#17**), and the first two are enough for "never contradicts itself" at one service.
- **(B) A new per-project lock, in the shape of `internal/lock`.** Cost: a second distributed lock in the system, with its own expiry and takeover semantics, for a service that is already a single writer.
- **(C) Firestore transactions.** Not available: the client has none (finding **#16**). Listed because the spec names it.
*Consequence:* (A) means spec §9's "Firestore transactions or a per-project lock" is replaced by the mechanism the repository actually has.

**Answered 2026-10-19: option (A)** — the house idiom: deterministic event document IDs, ETag/`If-Match` CAS, generation-matched writes, and no new lock.

### Q7 — Event intake: webhooks, polling, or the reconciliation job?
**Changes spec §4 ("wakes on requests and events") and §9.**
- **(A) Recommended — Cloud Scheduler polling, on the existing `history_sweep` pattern.** Adds no reachable surface, no webhook secret, and no signature verification; it is the one mechanism the tree already runs three times (finding **#18**).
- **(B) GitHub webhooks to a public HTTPS endpoint.** Cost: the **first public surface Fugaro has ever had** — a webhook secret in Secret Manager, signature verification, replay protection, and a reachable endpoint that finding **#4** says the design refuses. Latency is the only thing it buys.
- **(C) Both: polling for the reconciliation sweep, webhooks for latency-sensitive events.** Cost: two paths into the same state, which is two idempotency stories to get right.
*Consequence:* (A) means the coordinator's "wakes on events" is a *tick*, and the spec's §6 "intent signals" arrive over the MCP front door (which is request-driven) rather than as events.

**Answered 2026-10-19: option (A)** — Cloud Scheduler polling on the existing `history_sweep` pattern: "wakes on events" is a tick, and the spec's §6 intent signals arrive over the MCP front door rather than as events.

### Q8 — What does the console gain, and does "ask the coordinator" ship in v1?
**Changes spec §10.**
- **(A) Recommended — read-only, through the CLI sidecar: new read operations in the IPC table over new `fugaro` commands, exactly as every other screen works.** It needs no change to the console's no-credential D1/D2 or to v2's one-write D22 ruling (finding **#27**), and it makes the project view reachable whichever store Q5 picks.
- **(B) Read-only plus "ask the coordinator" as a new operation.** Cost: a new interaction that sends free text to a model and renders a model's answer — which is exactly the untrusted-output case (finding **#28**) — and it is a second write-shaped operation beside v2's single launch.
- **(C) The project view as a separate window or app.** Cost: a new Electron surface with its own CSP, IPC table and hardening review, for a view the existing renderer can draw.
*Consequence:* under (A), spec §10's "ask the coordinator" is deferred to a second ruling, and its answer must be plain text, never markdown with links.

**Answered 2026-10-19: option (A)** — read-only, through the CLI sidecar, as new read operations over new `fugaro` commands, with no change to the console's no-credential D1/D2 or to v2's one-write D22: spec §10's "ask the coordinator" is deferred to a second ruling.

### Q9 — How is a two-tier model configuration expressed, and what does the coordinator's provider consent say?
**Changes spec §8.**
- **(A) Recommended — two named slots in the published project policy, in the shape `agent.models` already uses, and an explicit `allow_data_to` naming every repository the coordinator may read.** The policy already carries `providers`, `model_prices` and `routing` (`internal/projectpolicy/policy.go:128-134`), so a slot pair is an addition rather than a new concept; and `allow_data_to` is the gate that has to be widened deliberately (finding **#24**).
- **(B) Two workflow profiles in the project layer, one per tier.** Reuses `profiles:` (`internal/projectpolicy/profiles.go:31`) but means the coordinator's model choice is expressed as a job profile, which it is not.
- **(C) A per-installation setting outside the policy.** Cost: it is invisible to every run and to `fugaro doctor`, and the policy is where every other project-wide model decision lives.
*Consequence:* under (A), `allow_data_to: ["*"]` for the coordinator's provider is an explicit, visible owner-level statement that every repository's text may go to that provider.

**Answered 2026-10-19: option (A)** — two named slots in the published project policy, in the shape `agent.models` already uses, plus an explicit `allow_data_to` naming every repository the coordinator may read, so that `allow_data_to: ["*"]` is an explicit owner-level statement.

### Q10 — Does this design absorb the always-on orchestrator (mobile P5.1, PR #632), or the other way round?
**Changes spec §2, §4, §8.**
- **(A) Recommended — one resident service per installation, with the coordinator as a role it plays.** PR #632's design already puts it per installation with a scheduler tick and a dedicated service account, which is the same unit as "per Fugaro project" (finding **#1**); the coordinator adds the project-wide state and the MCP front door. Cost: PR #632's *"never a model credential in the resident service"* and this spec's §8 cannot both hold — so this option means **Q4 (A)** for v1 and the model credential arrives only when the credentials service does.
- **(B) Two services: the orchestrator (per installation, `fugaro run` authority, no model credential) and the coordinator (advisory, project-wide state).** Cost: two always-on surfaces, two service accounts, two sets of grants — and the coordinator still needs its own answer to Q4.
- **(C) The coordinator absorbs the orchestrator's `fugaro run` authority too.** **Refused as written**: that is mobile D115's deferred question (`docs/mobile/reconciliation.md`), and answering it here decides it implicitly.
*Consequence:* (A) is the option that does not decide D115 by accident, and it is the reason this question exists at all.

**Answered 2026-10-19: option (A)** — one resident service per installation, with the coordinator as a role it plays, which means **Q4 (A)** for v1 and the model credential arriving only when the credentials service does.

**Amended 2026-10-21** — the owner answered **D196** (§6) **option (B)**, against that decision's
recommendation and against this answer's reading: the coordinator is **not** tied to the central
orchestrator, because *"there can be multiple central orchestrators in the near future"* — **the
owner's words of 2026-10-21**, written down in this folder and nowhere else, because the answer
arrived as a ruling rather than as a document — so it gets its own resident service and the plan
builds it. The answer above is the owner's, given on 2026-10-19, and it is **left as it was
given** — this note is what records what the later decision changed, and the plan's P7 and P8 are
rewritten to it rather than to the answer above.

### Q11 — How is the coordinator's spend capped, and charged to the project?
**Changes spec §9.**
- **(A) Recommended — no cap needed in v1, because there is no model credential in v1 (Q4 A), and the deterministic half costs nothing per event.** This is the only option that requires no new budget mechanism, and it defers the question to the release that has something to cap.
- **(B) A new daily-cap node and a new spend path for the coordinator, with the project's global daily cap becoming the sum of runs plus coordinator.** Cost: a new writer identity on `spend/` (no non-run identity may write it today, finding **#25**), a rules change, and a decision about what happens when the coordinator's own cap is hit.
- **(C) The coordinator mints a synthetic run identity per wake-up and is charged as a run.** Cost: it reuses the per-run machinery exactly, but it means a long-lived service mints run tokens for itself — the one thing D134 ties to a real run's lifetime.
*Consequence:* the spec's *"counts toward the project's existing spend caps"* is not satisfiable by configuration under any option; it needs a mechanism, and (A) is the option that does not need one yet.

**Answered 2026-10-19: option (A)** — no cap in v1, because there is no model credential in v1: the only option that requires no new budget mechanism, and the question is deferred to the release that has something to cap.

### Q12 — Which git providers does v1 support?
**Changes spec §6.**
- **(A) Recommended — GitHub only, and a Bitbucket project is refused with a line naming the reason.** GitHub is the provider whose credential is project-scoped by the owner's 2026-10-15 ruling; Bitbucket's is per repository and cannot be widened (finding **#30**).
- **(B) GitHub and Bitbucket.** Cost: a Bitbucket project would have to be read with one repository access token per repository, held by the coordinator — a per-repository credential in a project-wide service, which is the shape the owner's ruling refuses.
- **(C) GitHub now, Bitbucket behind a per-provider switch, off by default.** Cost: a switch that cannot be turned on is a switch nobody tests.
*Consequence:* under (A), the coordinator's own refusals become part of its design, in the way `fugaro validate` already refuses things with a named reason.

**Answered 2026-10-19: option (A)** — GitHub only, with a Bitbucket project refused in a line that names the reason.

**Amended 2026-11-01: option (B), for the coordinator's reads, and only for a single-workspace project.** The owner ruled on the `bitbucket-credentials` work's question B5 that Bitbucket comes into the coordinator: `coordinator show` first, then `coordinator status` and the sweep. The credential is not the one (A) refused. It is not a per-repository access token held by the coordinator job: it is one **read-scoped OAuth consumer** in the credentials service's bundle, minted to a person through a **person caller class** (`coordinator show`) or to the coordinator account through a **coordinator class** (`status` and the sweep), never to a run and never a write token — Q3's "no write anywhere" is unchanged. Two limits stay, and they are why this is an amendment and not a reversal. A repository whose workspace is not the read consumer's is refused with a named reason, so a project spanning two workspaces still meets (A)'s refusal. And with no credentials service deployed, every Bitbucket path behaves exactly as (A) describes. What is not avoided is the reach: the token the service mints is workspace-wide, so the read reaches everything the installing person can read there, which the plan's SECURITY.md task states plainly. The design is the `bitbucket-credentials` work's plan, whose S2 is retargeted at this, and its BB4 and BB5 are the tasks: `docs/bitbucket-credentials/implementation-plan.md`.

### Q13 — What is v0?
**Changes spec §2, §5, §7, §8, §10.**
- **(A) Recommended — a read-only aggregation with no LLM, no service and no MCP: the project's pull requests joined with its run records, drawn in the console by new `fugaro` read commands.** Every input already exists (findings **#9**, **#12**); nothing new is provisioned; it answers the spec's §1 problem ("nobody holds the project-wide view") for the deterministic half; and it is the only version that can ship before Q1–Q12 were answered.
- **(B) The MCP front door first (`report_intent`, `check_conflicts`).** Cost: it needs the service, the authentication answer and the state store before it can return anything useful — the three hardest questions first.
- **(C) The full spec at once.** Cost: it decides Q1–Q12 in one go, which is what the owner's gate exists to prevent.
*Consequence:* under (A), the coordinator's first release is a **view**, not an agent — which is also the smallest thing that proves the state model before anything depends on it.

**Answered 2026-10-19: option (A)** — v0 is a read-only aggregation with no LLM, no service and no MCP: the project's pull requests joined with its run records, drawn in the console by new `fugaro` read commands, so the coordinator's first release is a **view**, not an agent.

### Q14 — Does override learning ship, and what is the coordinator forbidden to do with what it reads?
**Changes spec §3 ("Learns from overrides") and §6.**
- **(A) Recommended — `record_override` writes and is shown; nothing is fed back into a prompt in v1; and the rules of finding #13 and #28 are written into the design as hard rules.** Override reasons are free text written by anyone who can reach the endpoint, and retrieval makes them a prompt-injection channel; the same is true of every PR title the coordinator reads.
- **(B) Retrieval into the prompt in v1.** Cost: it is a documented injection path from any launcher to every future suggestion, and it needs a retention cap, a poison story and a weighting rule — three decisions, none of which exists.
- **(C) No override log at all in v1.** Cost: the spec's §3 principle is dropped, and the reasons people give are exactly the business context the coordinator is supposed to be short of.
*Consequence:* under (A), the coordinator may **read** GitHub text and intent signals as data and may never act on an instruction found in them; it may never write to GitHub; and its answers are plain text.

**Answered 2026-10-19: option (A)** — `record_override` writes and is shown, nothing is fed back into a prompt in v1, and findings #13's and #28's rules are hard rules: the coordinator may read GitHub text and intent signals as data and never act on an instruction found in them.

### Q15 — Is the coordinator the only writer of its state, and who else may read it?
**Changes spec §5, §7, §10.**
- **(A) Recommended — the coordinator is the only writer; the CLI and the phone get a rules read grant, as mobile D112 already plans for the phone.** The console's project view then works through the CLI sidecar (finding **#7**) without asking the coordinator anything, and the phone's "hide old jobs" list works the same way it does today.
- **(B) The coordinator is the only reader and writer; the console asks it for everything.** Cost: a table read becomes an LLM-shaped call, and the console's data plane stops being the CLI sidecar it is today.
- **(C) Local orchestrators write their own intent nodes directly.** **Refused as written**: spec §7 says *"Local agents never touch Firestore directly"*, and a direct write path is a direct poisoning path.
*Consequence:* under (A), the rules change is an addition to the RTDB template (`internal/budget/rules/rules.json.tmpl`) with its own golden file, which is how every rules change in this repository works.

**Answered 2026-10-19: option (A)** — the coordinator is the only writer; the CLI and the phone get a read grant in the rules, as mobile D112 already plans for the phone, and the change is an addition to the RTDB template with its own golden file.

### Q16 — Which MCP transport, and what does "MCP authorization" mean here?
**Changes spec §7.**
- **(A) Recommended — the streamable HTTP transport, with Cloud Run IAM invoker as the authorization (Q2), recorded as a deliberate departure from the MCP specification's OAuth 2.1-based authorization model.** No OAuth server, no dynamic client registration, no resource metadata — because the repository has none and building one is a project of its own.
- **(B) Implement the specification's OAuth 2.1 authorization model.** Cost: an authorization server, client registration and resource metadata, none of which exists, for a front door whose callers are the project's own developers.
- **(C) `stdio` transport only, no HTTP at all.** Cost: it removes the front door entirely — a local process cannot be reached by another developer's orchestrator — which makes the whole §7 interface pointless. Listed because it is the option that needs no service at all.
*Consequence:* under (A), the departure is written down rather than discovered by the first non-Claude MCP client that tries to authorize against it.

**Answered 2026-10-19: option (A)** — the streamable HTTP transport, with Cloud Run IAM invoker as the authorization (Q2), recorded as a deliberate departure from the MCP specification's OAuth 2.1-based authorization model, so the departure is written down rather than discovered by the first non-Claude MCP client.

---

## 4. UNVERIFIED

| # | What could not be verified | Why, and what would settle it |
| --- | --- | --- |
| U1 | **The MCP specification's authorization model.** The spec's §7 describes "an MCP server over HTTP"; the published MCP specification's authorization section is OAuth 2.1-based. | The public specification is not reachable from this container. Reading it would settle whether Q16's option (A) is a small departure or a large one. |
| U2 | **The permissions the GitHub App has actually been granted on GitHub.** `tokenPermissions` is what a *run asks for* (`internal/gitprov/github/apptoken.go`'s `tokenPermissions`); GitHub caps it at the App's own grant, which is configured in GitHub's UI. | Only the owner can read the App's settings. It decides whether Q3's option (A) works at all: the App must already have `pull_requests: read`. |
| U3 | **Whether an installation token with `repositories` omitted is acceptable in this design.** `tokenSource.Token` always posts one repository (`internal/gitprov/github/apptoken.go`). | A read of GitHub's API documentation, or one owner-run experiment. It decides whether a project-wide reader is N tokens or one. |
| U4 | **The real cost of a reasoning-tier call at the spec's scale.** The arithmetic in finding **#26** is arithmetic from the shipped price catalog, not a measurement. | `fugaro report` and `fugaro budget preview` measure runs, not a service. Only running one would settle it. |
| U5 | **Whether the RTDB rules can express a read grant for a coordinator's own state without widening anything else.** The template's macros are `TOKEN`, `RUN`, `USER`, `MAILBOX`, `CONSUMER` (`internal/budget/rules/rules.go:375-398`). | The rules have an emulator and a golden file (`internal/budget/rules/testdata/rules.golden.json`), so this is answerable by writing the change — but it is a change, and none is proposed here. |
| U6 | **What `docs/design/always-on-orchestrator.md` (PR #632) finally rules.** It is an open draft and it asks Q1–Q7 rather than deciding. | Reading it at the commit it merges with. Q10's option (A) depends on its answers. |
| U7 | **What PR #634 (the console styling design) settles.** It is open and not on `main`; `docs/design/console-styling.md` does not exist locally. | Reading the merged file. It adds a `SERVED_FILES` entry and a Phase 6, and it does not change the CSP — so it should not affect Q8, but that is a claim about a draft. |

---

## 5. Glossary

The spec's words collide with words this repository already uses, and the collisions are of
two kinds: some are the same object under two names (a Fugaro project **is** an
installation, finding **#1**), and one is a real conflict (finding **#29**). Proposed, for
the README and for whatever plan follows.

| Word | What it means **here** | Where it is already used |
| --- | --- | --- |
| **Fugaro project** | A semantic project of several repositories and people: one GCP project, one Firebase project, its repositories, its people lists, one local config. | `docs/design/v1.md`. The spec's §2/§4 unit. |
| **Installation** | *The same object as a Fugaro project*, named from the cloud side: what `fugaro init` creates. | `docs/design/v1.md`; `deploy/terraform/gcp/roots/installation/`. The mobile and orchestrator designs say "per installation" where this spec says "per project" — the same thing (finding **#1**). |
| **Orchestrator** | **The local agent a developer runs**, one per developer (or per thread of work). | `docs/design/desktop-console-spec-v2.md`, on *"Multiple orchestrators: typically one per thread of work"*; `docs/mobile/spec.md`, on *"Phase 1: orchestrator runs on the owner's laptop"*. The spec's §1/§6 use. |
| **Coordinator** | **This design's new per-project service.** Aggregates what every orchestrator is doing, owns project-level state, is advisory. | New. **But the word is already taken twice in-tree** for something else: a review-process role (`docs/plans/2026-09-30-m9a-gateway-and-project-identity.md:1173`) and a designation in a test fixture (`shared/mailbox/index.test.js:1683`). |
| **Always-on orchestrator** | The mobile design's phase-2 resident service: one per installation, `fugaro run` authority, no model credential. The coordinator's close sibling. | `docs/design/always-on-orchestrator.md` (PR #632); `docs/mobile/spec.md`, on *"Phase 2: orchestrator moves to an always-on cloud service"*; D115. |
| **Front agent** | The mobile design's P5.4: the phone's conversational agent. | `docs/mobile/implementation-plan.md`, under **Task P5.4**. Distinct from both of the above. |
| **Summarising agent** | The mobile design's agent beside the orchestrator, which condenses task events into spoken updates. | `docs/mobile/spec.md`, on *"**Summarising agent** sits beside the orchestrator"*. The nearest existing thing to a "coordinator" in the mobile design, and not the same. |
| **Run** | One execution of a coding agent in a fresh Cloud Run container, ending in a pull request. | `internal/runstore/`, `internal/backend/`. The unit every budget cap is keyed to. |
| **Launcher / operator / budget admin** | The three people lists. Launchers start runs; operators hold the run bucket and the preload account; budget admins may write the database. | `internal/infra/firebase.go:117-120`; `internal/localcfg/localcfg.go:360-365`. Q2's answer is that callers are per-person IAM members on the coordinator, as a launcher is granted today, so the access list has to be said explicitly. |
| **Project job** | A **shared** Cloud Run job one repository's workflow can run in another's name, created by the `project` Terraform module. | `deploy/terraform/gcp/modules/project/job.tf:10`; `SECURITY.md:93`. **Not** the coordinator, and **not** "the project's job". |

---

## 6. The decisions planning found: D193–D197

Added on **2026-10-21**, when [implementation-plan.md](implementation-plan.md) was written, and
re-read against `main` at `1e0953b7`. §3's answers of 2026-10-19 settle **what the coordinator
is**; they do not settle five things **how it is built** turns on, and each of those is a choice
between real alternatives with a security consequence rather than a detail. They are therefore
**decisions, not questions**, and they are numbered in the owner's global block: the README's
numbers table records **D193 onward as free**, and a folder-local `D1` here would be a fifth
meaning for one number beside the four folders that already use one.

**Taking this block makes other works' readings of it stale**, and [README.md](README.md) records
that drift site by site — three other folders read **D193 onward as free** at their own base
commits, and one of them offers "move this work to D193–D218" as a live alternative that now
overlaps **D193–D197**. Those documents are deliberately not edited from here.

**Each is written recommendation first.** The plan is written to the recommendation; a part whose
decision is answered the other way is rewritten before it starts, not after.
**The owner answered all five on 2026-10-21** — four by taking the recommendation, and **D196
against it** — and each answer is recorded under its decision below, with what it means for the
plan. The wording of every recommendation, alternative and "Where it lands" line is this
document's, written before the answer, and is left as it was so that the answer can be read
against what it was choosing between.

Every path and symbol named below was read at `1e0953b7`; none is a line number.

### D193 — How far does one coordinator's GitHub credential reach?

**Recommendation — (A): one read-only installation token per repository, minted through
`MintInstallationToken` and cached per repository for the token's life.** The mechanism is
already in production: `internal/gitprov/github/apptoken.go`'s `buildTokenPermissions` is the
read-only set, `BuildTokenPermissions()` exports it, and two callers mint it today
(`internal/cli/gitcred.go`, `internal/cli/imagecheck.go`). What the tree **cannot** do today is
one token for several repositories: `tokenSource.Token` posts one repository, always
(`"repositories": []string{s.repo}`), and that is **U3** — whether GitHub accepts the field
omitted is unverified. Option (A) needs no new mechanism, mints nothing the tree does not mint
already, and mints a token that a leak makes worthless for every repository but one.

**Alternative — (B): one token for the whole project, once U3 is settled.** One mint instead of
N per sweep, and a simpler cache; the cost is that one leak reads every repository the App can
see, and it depends on an unverified API behaviour. It also widens Q3's answer from "the project's
repositories" to "everything the App is installed on", which the answer does not say.

**Where it lands.** The plan's **P2**, and the credential Q3(A) chose. **U2 stays open either
way**: the App must already be granted `pull_requests: read`, and only the owner can read that.

**Answered 2026-10-21: option (A)** — one read-only installation token per repository, minted
through `MintInstallationToken` and cached per repository for the token's life, with the
project-wide token waiting until **U3** is settled. *Why:* it is the smallest step of the two
that are buildable — the tree mints this token today and mints nothing else — and a leak is
worthless for every repository but one.

### D194 — Who is the database's writer?

**Recommendation — (A): the aggregation job's own service account holds
`roles/firebasedatabase.admin`, beside the history account's, and the rules change is a read
grant only.** This is the house precedent, not an invention: the history job in
`deploy/terraform/gcp/modules/firebase/iam.tf` holds exactly this role, with the comment that
records its blast radius — the role bypasses the rules, so only the code keeps the writer to its
own subtree — and an `#trivy:ignore:AVD-GCP-0007` beside it. A second account of the same shape
means the rules are widened by a **read** and by nothing else (Q15 A), and that a compromised
coordinator cannot write a node it was never meant to write only because the account cannot be
made narrower. **The comment that says "only this account (and no job account) holds one on the
FP" stops being true in this part and is amended with it** — a claim like that left standing is
how the next reader gets the blast radius wrong.

**Alternative — (B): a rules-mediated write, under a new non-run macro.** The rules would keep
the writer to `coordinator/**` by construction, which is strictly stronger. The cost is that it
is a **new authorization macro in the rules the whole installation runs on** — a wider change to
a security-sensitive file than the read grant the answer asked for, and one whose failure mode
(a macro that matches more than intended) is worse than the failure mode of (A).

**Where it lands.** The plan's **P3** and **P5**. **U5** — whether the rules can express the read
grant — is answered by P3, by writing it.

**Answered 2026-10-21: option (A)** — the aggregation job's own service account holds
`roles/firebasedatabase.admin` beside the history account's, the rules change is the **read**
grant only, and the comment that records the history account as the only account holding such a
role is **amended with the part**. *Why:* it is the house precedent rather than an invention —
the history job holds exactly this role, with its blast radius written beside it — and it keeps
the rules change a read.

### D195 — Where does the coordinator's state live?

**Recommendation — (A): a new top-level `coordinator/` subtree, `coordinator/project/` and
`coordinator/repos/<slug>/`, with deterministic document IDs and `internal/rtdb/client.go`'s
`GetETag` + `PutIfMatch`.** It is a separate tree from `config/`, which is the policy every run
reads, so a bug in the coordinator cannot move a policy node; the layout makes the read grant of
Q15(A) one prefix rather than a list; and the write idiom is the tree's own (finding #16/#17) —
no new lock and no new concurrency mechanism.

**Alternative — (B): under `config/coordinator/**`.** One fewer top-level key, and the read grant
reuses the `config/` shape. The cost is material: `config/` is the subtree the rules' `USER` and
`RUN` macros are built around, so the coordinator's nodes would sit inside the tree the policies
are authorized on, and every future widening of `config/` would silently reach the coordinator's
state as well.

**Where it lands.** The plan's **P3**, **P4** and **P5**.

**Answered 2026-10-21: option (A)** — a new top-level `coordinator/` subtree, `coordinator/project/`
and `coordinator/repos/<slug>/`, every document keyed by a deterministic ID and written with
`GetETag` + `PutIfMatch`. *Why:* it is a separate tree from `config/`, which is the policy every
run reads, so a bug in the coordinator cannot move a policy node — and the read grant becomes one
prefix rather than a list.

### D196 — Which service is the coordinator's front door?

**Recommendation — (A): the resident service the central orchestrator plan builds is the one; the
coordinator is a role it plays.** Q10(A) already rules that there is **one resident service per
installation**, and the sibling plan's **P4** is where that service is built; its **P9** records
the coordinator's half as blocked on this folder's plan existing. So this work admits
`google_cloud_run_v2_service` to the allow-list, mints the ID token and grants the per-person
invoker — and **builds no second service**, because two would be two identities, two scale-to-zero
configurations and two things to keep in step.

**Alternative — (B): a second service, built here.** The coordinator ships without waiting for an
unapproved sibling plan, which is the whole of the argument for it. The cost is the opposite of
Q10's answer: two resident services per installation, and a front door whose lifecycle is decided
in this folder while the one next door decides when it is deployed.

**Where it lands.** The plan's **P7** and **P8**. **It is the one decision that allocates work
between two documents**, and shipping either plan without it decides the other's question.

**Answered 2026-10-21: option (B), against the recommendation** — the coordinator is **not** tied
to the central orchestrator, because *"there can be multiple central orchestrators in the near
future"* — **the owner's words of 2026-10-21**, recorded in this folder and nowhere else, because
the answer arrived as a ruling rather than as a document: it gets **its own resident service**, built in this work, with its own identity, its own
scale-to-zero configuration and its own ID-token front door. **P7 and P8 no longer wait on the
central orchestrator plan's P4 and P5**, and the allow-list admission of
`google_cloud_run_v2_service` is shared with that plan's **P1** — **whichever part lands first
admits it and the other cites it**. *Why:* the owner ruled the two services independent, and a
front door that waits on an unapproved sibling plan is a front door that may never be built.

### D197 — How much of the override log is kept?

**Recommendation — (A): the last 200 per project, newest first, with no expiry in v1.** The log
is the one node whose content is written by whoever can call the front door (Q14 A), so its size
is the size of the text an attacker can make the coordinator store; 200 is small enough to read
and to bound, and it is a cap in one place. No expiry, because expiry needs the sweep to delete
and a deletion path is a second thing that can go wrong in v1.

**Alternative — (B): keep everything.** The history is complete, and a coordinator with no model
(Q4 A) never spends a token on it. The cost is an unbounded, attacker-writable node in the
project's own database, with no reader that can use all of it.

**Alternative — (C): keep with an expiry.** Bounded storage without a silent loss of the oldest
entries. The cost is that it is the only option that needs the sweep to delete, in v1.

**Where it lands.** The plan's **P8**, and the read the console and the CLI show.

**Answered 2026-10-21: option (A)** — the last **200** overrides per project, newest first, with
**no expiry in v1**. *Why:* the log is the one node whose content is written by whoever can call
the front door, so its size is the size of the text an attacker can make the coordinator store —
and 200 is a cap in one place, with no deletion path to go wrong.

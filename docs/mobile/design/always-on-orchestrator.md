# Phase 2 hosting: the always-on orchestrator

**Status: design only; nothing is implemented.** This document is the design Task P5.1 of [the mobile plan](../implementation-plan.md) asks for ("Phase 2 hosting — the always-on orchestrator — UNSCHEDULED"). It writes no code, no Terraform, no workflow file and no rules change, it approves nothing, and **P5.1 stays unscheduled**: the owner answered **D115** on 2026-10-17 with "decide phase 2 later" ([reconciliation.md](../reconciliation.md) §3), and answering a decision is not approving a task. The plan's OWNER APPROVAL GATE therefore stands over every part of this.

**The one-paragraph answer to the owner.** The phone's tasks should keep running while the laptop sleeps, and they can — but not by putting the laptop's job into one always-on cloud process. Split the thing the task entry calls "the always-on orchestrator" into the two functions it conflates: **the mailbox's other side** (a small consumer that polls the inbox, holds the lease, writes the outbox and the heartbeat, and launches runs) and **the agent work** (which already runs as a Cloud Run **Job** execution, one container per run, discarded at the end — the boundary `SECURITY.md` relies on). The resident part runs as **one Cloud Run service per installation, in the installation's own GCP project**, `min-instances = 0`, woken by a Cloud Scheduler tick, with a dedicated service account that holds the **launcher's** grants and **no model credential at all**; the model credential stays where it is today — in the job that needs it, per-repository, per-secret — until the [credentials service](../../credentials-service/README.md) lands and jobs stop mounting it. That split is what makes the envelope answerable: a stolen phone can ask, a stolen account cannot sign, a poisoned message cannot verify, and nothing any of them sends can move money past the caps and the kill switch, which the database — not the orchestrator — enforces.

---

## 0. What this document is, and how its claims were checked

**The baseline.** Every claim about the tree below was checked against **`origin/main`** as this branch finds it (`82ba600`), which carries the mobile work merged to date. Symbols are cited as `file` + **symbol**, never by line number, because line numbers move and symbols do not.

**What this document does.** It answers the design questions P5.1 names — where the always-on process runs, what identity it has, what it may not do, how it holds a model credential, how it authenticates the phone, how approvals work when the phone is the only human, what fails and what it costs — as **prose a later implementer can build from and a reviewer can falsify**, with the attacker capabilities stated rather than implied.

**What this document does not do.** It **allocates no decision number** (D110–D129, D160–D179 and their neighbours are not this document's to assign; where a question is the owner's it is written as **Q1, Q2 …** in [§10](#10-open-for-the-owner), for the owner to answer and a later pass to number). It **writes no check**: it names the number a live check would take ([§7](#7-the-checks-it-would-need)) and leaves the register untouched. It **reverses no ruling**: where this design departs from one it says so in its own paragraph and leaves the departure for the owner.

**What was not done.** No cloud was queried, no service was deployed, no account was touched, and no `belong` installation was looked up (F31). Every claim about a live GCP behaviour is either quoted from a document that recorded it or listed in [§11 UNVERIFIED](#11-unverified).

---

## 1. Corrections to the brief

The brief that came with this task names three things about the tree, and two of its three statements are not true of this branch. Correcting them changes the design, so they are corrected here rather than worked around.

### 1.1 There is no rules generation 3, no device node and no PR #601 on `main`

The brief asks this design to cover "device nodes of rules generation 3, #601". **`main` has no generation 3 and no device node.** `internal/projectpolicy/version.go`'s `RulesVersion` is **2** — "Generation 2 is the mailbox", the version the owner-run `fugaro init --firebase` deploy ships today — and `VersionSince` has rows for 1 and 2 and no third. `git log --all` finds no commit or merge for #601; the highest mobile-adjacent merges on this branch are #624, #619 and #615.

The device node **is** designed, and it is designed as a Phase 6 proposal: `internal/budget/rules/rules.go` carries a comment naming "a device node under `config/admin/devices/$uid` that `USER` will require (D166, plan P6.4)", and `TestMailboxResidualMintedTokenPassesUntilPhase6` pins the state it is waiting for — a minted `mc` token passing until Phase 6 adds it. The rules template has `config/admin` as `{".read": false, ".write": false}` and no `devices` child at all.

**Consequence for this design.** Revocation cannot be designed as "delete the device node", because there is no device node to delete. [§4.7](#47-revocation) designs revocation as it is **today** (delete the Auth user, rotate the pairing key) and then as it would be **after P6.4**, and [Q6](#10-open-for-the-owner) asks the owner whether P5.1 may ship before P6.4 — this design's recommendation is that it may not, because the device node is the only mechanism that makes revocation immediate for *both* reads and writes.

### 1.2 The direction-bound MAC exists — in JavaScript, and nothing in Go verifies it

This one is real, and it is the strongest thing the mailbox has. `shared/mailbox-schema/mac.js` exports `macOf(record, key, direction)` and `verifyMac(record, key, direction)` — HMAC-SHA-256 over `canonicalBytes(record, direction)`, timing-safe on verify, over every field except `mac` itself and the phone-written `acked`. `shared/mailbox-schema/index.js` exports `DIRECTIONS` (`inbox`, `outbox`, `heartbeat`) and injects the direction into the covered bytes, so a record reflected from the inbox into the outbox does not verify (the owner's ruling of 2026-10-18 on PR #579).

What is **not** true yet: **no Go code verifies a MAC.** `shared/README.md` says "The package has no consumer today", no `fugaro mailbox` command exists (`internal/cli/root.go` registers none), and the rules' mailbox subtree does not require a `mac` field — `TestMailboxRulesAgreeWithTheSharedSchema` records that gap by name. The consumer that would verify the MAC is plan Task P4.1, and its Go half is Phase 6's P6.7 (proposed, NOT APPROVED).

**Consequence.** The always-on orchestrator is the **first consumer that will exist**, and the MAC verification it needs is the part that does not exist. That is why [§4.6](#46-how-it-authenticates-the-phone) separates "what authenticates the phone today" (the rules' user claim) from "what authenticates the *message*" (the MAC, unbuilt in Go), and why [§9](#9-the-alternatives-priced) prices a laptop-only phase 2 as a real alternative rather than as a fallback.

### 1.3 "The approval model" is not a mechanism in the tree

P5.1's own text says an always-on orchestrator inherits "the approval model (F17)". No mechanism by that name exists: `grep` over `internal/` finds no `approv*` machinery except `fugaro allow-delete` (`internal/cli/allow_delete.go`), which is an operator-recorded list of Terraform addresses approved for cloud deletes and has nothing to do with a run. "The approval model" is the mobile work's name for the console's confirmation-before-write rule (D22), the D128 amendment of 2026-10-17 (destructive voice commands allowed only with a read-back and an explicit "confirm") and managed-projects' plan-then-apply gate.

**Consequence.** This design does not invent an approval service. It writes down what the existing pieces would enforce for a phone that is the only human ([§4.8](#48-approvals-when-the-phone-is-the-only-human)) and leaves the unbuilt pieces (the spoken-confirmation machine, `shared/confirm`, Phase 6 P6.2) named as dependencies.

---

## 2. The problem

Phase 1 puts the orchestrator on the operator's laptop: the phone writes an inbox message, and a consumer running beside the orchestrator on that machine reads it, acts, and writes the outbox and the heartbeat. The mailbox is designed for a consumer that is **sometimes absent** — at-least-once delivery, a lease with a TTL, dedupe, a replay window, a 72-hour TTL on stored messages — so nothing is lost when the laptop sleeps. But the phone's primary feature does not work when it does: a launch waits, a prompt waits, and the heartbeat D117 shows as presence goes stale, so the app honestly reports the installation **offline** while the owner is away from the laptop.

Phase 2 is the fix, and it is not a small one. The laptop's consumer runs under the **operator's own identity**: `internal/cli/cloud.go`'s `google.DefaultTokenSource` gives it the operator's application-default credential, and D113b records the honest consequence — that credential is an admin/editor on the database and **bypasses the RTDB rules** (`internal/infra/firebasedb.go`'s `dbErr` says so in as many words), so for the laptop "the outbox and the heartbeat and nothing else" is a rule in the consumer's code, not a fence the database enforces. A cloud consumer under a **narrow service account** is the first mailbox writer the rules can actually fence — and it is also the first one that needs a cloud identity, a cloud posture and a spend story. That is D115, and it is why the owner deferred it.

---

## 3. What exists today (verified against `origin/main`)

### 3.1 The mailbox's other side

| Piece | Where it is | What it decides |
|---|---|---|
| Message schema | `shared/mailbox-schema/index.js` (`validateMessage`, `canonicalBytes`, `REQUIRED_FIELDS`, `FIELDS`, `DIRECTIONS`) | A closed field set — the field set **is** the version. `mac` is required on every message, inbox and outbox alike. |
| The tag | `shared/mailbox-schema/mac.js` (`macOf`, `verifyMac`) | HMAC-SHA-256 over the canonical bytes, **direction included**, verified timing-safe. Nothing in Go verifies it yet. |
| State machine, dedupe, windows | `shared/mailbox/index.js` (`step`, `STATE`, `REFUSAL`, `DEFAULT_WINDOW_MS`, `MAX_WINDOW_MS`, `MAX_PROMPT_TTL_MS`, `defaultMaxPerSender`) | At-least-once with a consumer-side ledger keyed `(sender, id)`, 6-hour default command window (7 days maximum), a required `promptTtlMs`, and an eviction horizon of `max(windowMs, promptTtlMs)`. |
| Consumer lease | `shared/lease/index.js` (`takeLease`, `releaseLease`, `CLAIM_TTL`, `ALREADY_CLAIMED`, `MAX_CLOCK_SKEW`) | The mailbox's mutual exclusion, in the shape of `runstore.Claim`: a 10-minute TTL, an `already-claimed` answer, and a 5-minute clock-skew band. |
| Presence | `shared/presence/index.js` (`evaluatePresence`, `DEFAULT_OFFLINE_INTERVALS`) | Staleness and the offline state, from the heartbeat's age. |
| The database side | `internal/budget/rules/rules.json.tmpl`, `internal/budget/rules/rules.go` (`MAILBOX`, `CONSUMER`, `userClaim`, `USER`) | `USER` is `auth.token.mu == auth.uid`; `CONSUMER` is `auth.token.mc == $mb`; the phone writes its own inbox and the `acked` child of an outbox message it has spoken (`TestMailboxWritesAreExactlyTheFour`). |
| Rules generation | `internal/projectpolicy/version.go` (`RulesVersion = 2`) | Generation 2 is the mailbox. There is no generation 3. |

Two properties matter for everything below. **Delivery is at-least-once, so acting exactly once is the consumer's problem**, not the phone's; and **the tag is over the message and its direction**, so an attacker who can *write* a node the phone or the consumer reads still cannot make either of them act without the pairing key.

### 3.2 The launch path and the launch claim

A run today is launched by the CLI on the operator's machine:

1. **The claim.** `internal/runstore/launch.go`'s `Claim`/`ClaimKey`/`ClaimTTL`/`ReadClaim` — a GCS object under the run's prefix, created **if absent** (`launching`), taken in `internal/cli/run.go`'s `takeClaim`, and readable only after a win. `ClaimTTL` is 10 minutes, and a stale or unreadable claim can be taken over by a `ReplaceIf` on the generation read. F4 and `desktop-console-v2.md` F11 record this as **the tree's only "two actors cannot do the same thing twice" mechanism** — "the **only** reason two launchers cannot start the same run twice".
2. **The mint.** Only a CLI holding the claim mints: `internal/cli/launch_budget.go`'s `mintBudgetToken` signs a custom token through `token.IAMSigner` (`iam.serviceAccounts.signJwt`, the `token_minter` custom role in `deploy/terraform/gcp/modules/firebase/iam.tf` — "signJwt and nothing else"), and `internal/budget/token/object.go`'s `PutObject` writes it create-if-absent to `runs/<slug>/<run>/budget-token`, with a bucket lifecycle rule deleting an untaken one after a day. **"May mint" is exactly "may launch".**
3. **The take.** The container takes it once — `internal/runner/projectpolicy.go`'s `takeBudgetToken` reads and deletes, and `checkNames` refuses a token whose own `fs`/`fr` claims are not the names used. A marker without a token refuses (`TokenMarked`, `internal/runner/budget_session.go`); a malformed marker refuses (`MalformedEnv`).
4. **The launch.** `internal/backend/backend.go`'s `LaunchSpec` carries one environment marker into the Cloud Run Job execution override; the job's own service account runs the container.

None of this needs a laptop. All of it needs an identity that may **sign, write the bucket, and run the job** — which is exactly what a service account in the installation's own project can be given.

### 3.3 The budget envelope

The caps are enforced **twice, and the second time is by the database**:

- **Per-run cap:** `internal/budget/evaluate.go`'s `Evaluate` compares `Outstanding()+Amount` against `caps.PerRun()`, and `internal/budget/lease.go` clamps the lease to it — *and* `internal/budget/rules/rules.go`'s `perRunOK` generates the rules' own comparison into the `reserved` leaf's `.validate`. The effective cap is the **minimum** of the repository's own/default and the global one, fail-closed.
- **Daily caps:** the same shape, `REPODAILY`/`GLOBALDAILY` in the rules and `ReasonRepoDailyCap` in `Evaluate`.
- **The kill switch:** `config/kill/global` and `config/kill/<slug>` (`internal/budget/model.go`'s `PathKillGlobal`/`PathKillRepo`, written by `internal/budget/kill.go`'s `SetKill`), and the rules' `KILLS_OFF` macro refuses a lease while either is on.
- **The seal:** `docs/design/budget-token-channel.md` §7 — the token object is sealed AES-256-GCM under a per-launch key that travels in the existing marker, so a run that steals a peer's object cannot use it ("a capability, not a boundary"). That design's D14–D19 are answered and its implementation rides 0.9.0.

A compliant-but-hostile client therefore cannot write an over-cap ledger; the caps are not a promise the launcher makes, they are a rule the database applies. **That is the load-bearing fact for [§4.5](#45-the-hard-envelope-a-stolen-phone-a-stolen-account-a-poisoned-message)**: an always-on orchestrator inherits exactly these caps and cannot loosen any of them, because loosening one is a `config/caps/**` write, which is owner-run.

### 3.4 Where a model credential lives today

`agent.auth` in a repository's `fugaro.yaml` is one of `vertex`, `oauth` or `api-key`. `internal/infra/spec.go`'s `SecretMounts` is "the one place that decides" what a job mounts: the git secret always; for `oauth`, `config.ClaudeAccountSecret(...)` → `CLAUDE_CODE_OAUTH_TOKEN`; for `api-key`, `anthropic-api-key` → `ANTHROPIC_API_KEY` plus each allowed provider key; for `vertex`, **nothing** — the job's service account gets `roles/aiplatform.user` and the SDK uses the metadata server. `deploy/terraform/gcp/modules/workflow/job.tf` builds the Cloud Run Job with a static `secret_env` → `secret_key_ref`, and `deploy/terraform/gcp/modules/workflow/sa.tf` grants `roles/secretmanager.secretAccessor` **per secret, to that job's service account**.

Two limits follow, and both are recorded in the tree: a job-level mount is **per job, not per execution** (Cloud Run execution overrides cannot carry a secret reference — `docs/design/named-oauth-tokens.md` U1), so every execution of one job sees the same value; and the agent shares the container with the runner, so it can read the value from the runner's `/proc`, which `SECURITY.md` lists among the known limits along with "the GitHub App's private key".

Named OAuth tokens (shipped 0.6.5) are per-repository secrets named `claude-oauth-token-<name>`; the account **names and their allow-lists live only in the operator's local config** (`internal/config/claude_accounts.go`'s `ClaudeAccountNameRE`, `claude_accounts:`) and are never published to the project, so selection happens at **deploy** time by choosing a job variant, not at run time.

### 3.5 The planned credentials service

`docs/credentials-service/` designs the opposite arrangement: a **standalone Cloud Run service per project** whose service account is the only identity holding `secretAccessor` on model keys; jobs keep `run.invoker` and **no** `secretAccessor` on those secrets, and reach it through the in-runner gateway with a short-lived per-job token that **is** the run's existing budget identity (D134). The service never returns the real credential to the job — it swaps the header and forwards. Its bounds are its own decisions: D143 accepts the single point with bounds and a fail-closed halt (an unreachable service halts the run, never falls back in-job), D146/D147 make it per-installation and owner-applied from the installation's Terraform root, and **F36 draws the boundary this design has to respect**: the service "stays secrets-only", and other always-on features — *"the phone client backend"* is named explicitly — go in **sibling services that call it**. The service is plan-only, waits on the budget-token seal, and is not scheduled.

### 3.6 The managed-projects gate

D115 is answered as "decide phase 2 later, after managed-projects' D55–D78 gate", so those decisions bind this design even though none of them is about a mobile feature:

- **D75** — the identity that applies a managed plan is a **dedicated deployer service account**, impersonated per apply, with **no project-level `setIamPolicy`** and no `iam.roles.*`, and resource-level `setIamPolicy`/`actAs` only on resources and accounts it created (the reserved-name-prefix condition), fail-closed, and **nothing on the runs bucket**.
- **D56/D57/D58** — the "this project is managed" authority is a runs-bucket-root marker written **only by an owner-run command**; the bucket root sits outside the jobs' prefix-conditioned grants, and no run-held credential has a write path to it.
- **D72/D74** — what a managed apply may never do (delete or replace without a per-address owner approval recorded at the bucket root, touch another project, org IAM or the billing link, write a secret value, create a billing-admin credential, touch `belong`).
- **D63** — **no hard billing stop**, deliberately, because one "would need an always-on billing-admin credential (finding 25)". Managed projects adopted **no always-on component**, and D71 adds no RTDB node.
- **D78** — the monitoring types are dropped from the managed allowlist and are owner-run.

**Consequence for this design: managed projects deliberately introduced no always-on identity, and this design must not be read as the one it refused.** The orchestrator's service account is not the deployer and cannot impersonate it; it does not hold `setIamPolicy`; it is not a billing identity. Where D63 refused an always-on credential with money-adjacent power, this design keeps that refusal and gets its spend bound from the caps and the kill switch instead.

---

## 4. The proposal

### 4.1 Where it runs

**One Cloud Run service per installation, in the installation's own GCP project, `min-instances = 0`, request-based billing, woken by a Cloud Scheduler tick.**

- **Per installation, not shared.** An account is an installation (D114), one Firebase project per installation is the standing rule, `belong` is never touched (F31), and the credentials service took the same shape for the same reasons (D146/D147). A Fugaro-hosted service holding every customer's model credentials and every phone's pairing keys would be the single largest blast radius in the project, and would make "the operator's own project" — the property the whole budget and rules design rests on — meaningless. [Q3](#10-open-for-the-owner) puts the alternative in front of the owner with its cost.
- **A service, because the consumer is event-shaped, not job-shaped** — it holds a lease, answers a prompt, writes a heartbeat. A Cloud Run **Job** on a schedule can do exactly the same work (see [§9](#9-the-alternatives-priced), alternative A3) and is *cheaper*; the service is chosen because it can also answer a request, which is what a synchronous "what is pending?" would need and what the phone's own presence view may eventually want. If the mailbox stays the only path, A3 is the better build and the owner should take it.
- **Scale-to-zero, because "always on" means available, not busy.** The laptop is asleep; the mailbox already tolerates an absent consumer for the whole 72-hour TTL (D118/O5). A scheduler tick every minute gives availability at no idle cost, and a cold start of about a second (the figure the credentials service's own design records for the same shape) is invisible next to a network round trip from a phone. `min-instances = 1` buys sub-tick latency and a permanently listening socket for a fixed monthly cost per installation; this design does not take it, and [Q1](#10-open-for-the-owner) asks for the tick cadence rather than for the choice.
- **Cloud Scheduler, because the installation already has the module.** `deploy/terraform/gcp/modules/installation/scheduler.tf` creates the scheduler service account and each repository module grants it run access on that repository's check job — the same shape a tick on the orchestrator takes.
- **The same `fugaro` binary.** The orchestrator is the CLI run with flags, not a terminal: `internal/cli/cloud.go`'s `google.DefaultTokenSource` resolves the service account from the metadata server, so the launch path is unchanged and the "one container per run, discarded at the end" boundary `SECURITY.md` relies on is preserved.
- **A new, owner-applied resource.** Following D146's pattern for the credentials service: a new module in the **installation** Terraform root, applied by the owner with the installation's own tooling — never by `fugaro managed apply`, never by an agent, never in a test. [§6](#6-what-it-must-never-do) rule 2 makes that explicit.

### 4.2 What it is made of: a consumer, a launcher, and jobs that are the agent

The always-on service is **two functions and no more**:

1. **The consumer** — reads the inbox, verifies the tag, holds the lease, acts, writes the outbox and the heartbeat. This is the phase-1 consumer of Task P4.1 (`fugaro mailbox`, D113c's answer), running in the cloud instead of on the laptop.
2. **The launcher** — for a launch request, takes the launch claim, mints the budget token, and starts the job execution. That is `fugaro run`'s existing path with a service account behind it.

**The agent does not run in the resident service.** A task is a Cloud Run **Job execution** — the container that already exists, with the credential mount it already has, discarded when the run ends. This is the design's central decision, and it is what makes [§4.4](#44-how-it-holds-a-model-credential-three-candidates-priced) short: the process that is always available is never the process that holds a model credential.

### 4.3 Its identity, and what it may never do

**The identity.** One dedicated service account per installation (the `orch` account), created by the installation module in the owner's apply. Its grants are the **launcher's** grants, assembled from resources that already exist, and nothing else:

| Grant | Where it comes from today | Why it is exactly this |
|---|---|---|
| `iam.serviceAccounts.signJwt` on the signer account | `deploy/terraform/gcp/modules/firebase/iam.tf`'s `token_minter` custom role, granted to "the installation's launchers" | "May mint" is exactly "may launch". Nothing else may mint a budget identity for any repository — `SECURITY.md` records that as a known limit a launcher already has. |
| `run.jobs.run`, `run.jobs.runWithOverrides` on the installation's jobs | `deploy/terraform/gcp/modules/installation/iam.tf`'s `job_runner` custom role, granted per workflow job by the repository module | It starts jobs. It cannot create, update or delete one. |
| `run.jobs.get`/`list`, `run.executions.get`/`list`/`cancel`, `secretmanager.secrets.list`/`versions.list` | the `launcher` custom role, same file | So `fugaro ls`/`watch`/`cancel` work. Metadata only: reading a secret **value** needs `secretAccessor`, which this account never gets. |
| Object access on the runs bucket under the job prefixes (`runs/`, `cache/`, `locks/`, `builds/`), conditioned | `deploy/terraform/gcp/modules/installation/iam.tf`'s `runs_launcher`, `google_storage_bucket_iam_member` with the launcher condition (`internal/backend/gcp/names.go`'s `LauncherBucketCondition`) | The claim, the task spec and the budget token live there. **The bucket root is outside the condition**, so the marker objects are unreachable. |
| `roles/run.invoker` on the credentials service, once it exists | `docs/credentials-service/` D135 | The job's job token is presented by the run; the orchestrator itself calls the service only if [§4.4](#44-how-it-holds-a-model-credential-three-candidates-priced) ever needs it to. |
| The mailbox: `CONSUMER`'s two writes | the rules' `CONSUMER` macro (`auth.token.mc == $mb`) | It writes the outbox and the heartbeat through a minted `mc` token, **and the rules fence it** — unlike the laptop consumer, whose admin credential bypasses them (D113b). |

**What it may never do.** The never-list is [§6](#6-what-it-must-never-do); the three that cost the most to get wrong are here:

1. **No IAM.** The account holds no `setIamPolicy` anywhere and no `iam.roles.*` — the D75 fence for the deployer, applied to the orchestrator for a different reason: an orchestrator that could grant itself a role could widen its own envelope, and then none of [§4.5](#45-the-hard-envelope-a-stolen-phone-a-stolen-account-a-poisoned-message) is true. It also cannot impersonate the deployer, so it cannot reach managed applies indirectly.
2. **No managed applies.** `fugaro managed apply` is the deployer's path (D75/D76), the marker that permits it is written only by an owner-run command (D56/D57), and the bucket root is outside this account's object condition. The orchestrator cannot become the thing that creates and deletes cloud resources.
3. **No destructive commands.** Never a branch deletion, a PR close, a `fugaro allow-delete`, a `config/kill` write, a `config/caps` write, a `fugaro budget` write, a `fugaro secrets set`/`rm`, a rules deploy (`fugaro init --firebase`), a `fugaro restore`. The D128 amendment allows three destructive **verbs** — cancel a job, approve/merge, confirm an action — each behind the spoken confirmation; **every other destructive verb stays refused** (the kill switch, budget writes and deletes are named explicitly in that ruling), and this design does not widen the list.

### 4.4 How it holds a model credential: three candidates, priced

D115's own sentence is "the model credential lives in a secret manager **in the installation's own project**, the service runs as a dedicated identity with a narrow IAM role". The candidates below are what that sentence could mean, and the recommendation is narrower than any of them.

**(A) The planned credentials service.** The orchestrator holds **no** model credential; the runs it launches present their budget identity as a per-job token to a sibling service that swaps in the real credential and forwards. *For:* it is the design that exists for exactly this problem, it has thought about the identity (D143), the accounting (D140), the fail-closed halt, and the customer boundary (F36); it removes the credential from the container the agent shares, which closes the `/proc` gap `SECURITY.md` lists; and D134 makes the per-job token something the orchestrator already has. *Against:* plan-only, unscheduled, gated on the budget-token seal; nothing here can depend on it shipping; and it does not hold a `vertex` credential at all (F1), so a Vertex installation is unchanged.

**(B) A Secret Manager secret mounted into one job only.** The mechanism that exists **today**: `infra.SecretMounts` + `modules/workflow/job.tf`'s `secret_env` + `modules/workflow/sa.tf`'s per-secret `secretAccessor`. *For:* shipped, per-job, per-secret, nothing new to invent; the credential never reaches the resident service, because the resident service is not the job. *Against:* the value is in the container the agent shares; Cloud Run mounts it **per job, not per execution** (U1), so one value serves every run of that job; and it is the exact arrangement the credentials service exists to replace.

**(C) Named OAuth tokens per project.** *Against, and this is disqualifying:* `docs/design/named-oauth-tokens.md` ships account names in the **operator's local config only**, never published to the project, and selects them by choosing a job variant at **deploy** time. A resident service has no operator-local config, and a phone must not be able to name an account without a new published mapping — which would be a new decision (whose allows-list? whose headroom?), not a consequence of this one. Cloud Run cannot carry a secret reference in an execution override (U1), so "pick the account per task" is not expressible anyway.

**Recommendation: (B) today, (A) when it lands — and never a model credential in the resident service.** Concretely: the always-on service holds **no model credential**; the runs it launches take theirs exactly as laptop-launched runs do; when the credentials service ships, the jobs move to it and the mount disappears without this design changing. **This is the point worth arguing about**, so it is stated as a claim a reviewer can falsify: *if the resident service holds a model credential, then the envelope in [§4.5](#45-the-hard-envelope-a-stolen-phone-a-stolen-account-a-poisoned-message) is false, because a compromise of the resident process yields a credential that outlives every run, while a compromise of a job container yields one that dies with the container.* [Q2](#10-open-for-the-owner) puts the choice to the owner.

### 4.5 The hard envelope: a stolen phone, a stolen account, a poisoned message

Three attackers, three distinct envelopes. Each is stated as **what it can do**, then **what stops it**, then **what is left**.

**E1 — a stolen, unlocked phone.** It holds the two items D111 allows: the Firebase Auth ID token (`mu` claim) and the per-pairing MAC key.
*Can:* read the four granted families (`config/caps/**`, `spend/$day/**`, `config/kill/**`, its own mailbox — tension (b1)); write its own inbox messages, correctly signed; ack an outbox message it has spoken; and **say "confirm"** — the read-back does not stop a thief.
*Stopped by:* the spend caps and the kill switch, which are enforced by the database's rules and cannot be raised by any message (`config/caps/**` is owner-run); the D128 amendment's closed verb list, which leaves every other destructive verb refused; the confirmation machine's one-confirmation-one-action rule, its timeout and its "the issuing utterance can never confirm" rule, so a thief cannot turn one "yes" into many actions; and the **time to revoke**.
*Left, honestly:* cancel and merge are not bounded by money — a thief can cancel a job and merge a PR, and the caps do not touch either. §3.5 of the functional-spec reconciliation accepts this as the residual risk D169 takes together with F38, and names the real bound: **the time to revoke.**

**E2 — a stolen phone account** (the Auth uid, without the device: an exfiltrated refresh token, a shared login, a deleted-but-unexpired ID token).
*Can:* read the four families; write inbox messages to its own mailbox, because the rules' `MAILBOX` conjunct is `userClaim && $mb == auth.uid`.
*Stopped by:* it **cannot sign**. The MAC key never left the device and the laptop keychain, so the consumer refuses every unsigned or mis-signed inbox message before acting (the MAC covers every field and the direction). It cannot write the outbox or the heartbeat — those are `CONSUMER`, not `MAILBOX`. And an outbox message it could inject would not be spoken, because the phone verifies the tag before it speaks a word.
*Left, honestly:* **reads.** A stolen account reads caps, spend and kill state until the Auth user is deleted; the deletion is immediate for new tokens but an issued ID token stays valid until its `exp`, which is U2 and live check 101. Tension (b1) already shrank what that exposes to numbers and states with no run titles and no repositories.

**E3 — a poisoned mailbox message** (an attacker who can *write* a node the consumer reads: a project owner/editor, an admin service account, or any launcher holding a minted token — `TestMailboxResidualMintedTokenPassesUntilPhase6` records that the rules cannot tell a minted `mc` token from the real consumer **today**).
*Can:* write into the mailbox nodes the rules cannot distinguish; today it can even write as the consumer.
*Stopped by:* the pairing MAC, verified **before anything else and before any action** (P6.7's rule), over every field including the direction — so a message reflected from the outbox into the inbox, or re-targeted at another run or PR, fails; the replay windows (6 hours for commands, 72 hours for prompt answers) and the ledger keyed `(sender, id)`; the action-id and confirmation-id dedupe, which survives a restart, so a signed record re-sent under a new message id is refused; and D169's rule that a destructive prompt is answered only by a MAC-verified **action record**, never by free text and never by a plain `answer`.
*Left, honestly:* the MAC is **unbuilt in Go** ([§1.2](#12-the-direction-bound-mac-exists--in-javascript-and-nothing-in-go-verifies-it)) and the rules do not yet require the field (P6.4) — so **until P4.1 and P6.4/P6.7 ship, E3 succeeds against a deployed phase-1 mailbox**, and that is the strongest reason this design does not recommend a phase 2 before phase 1's consumer exists.

**What bounds all three, and is worth saying once: the envelope is not the MAC.** The MAC decides **who may ask**; the caps and the kill switch decide **how much can be spent**; and the confirmation machine decides **which changes are allowed at all**. A design that relied on the MAC for spend would be relying on the phone's own key custody; this one does not.

### 4.6 How it authenticates the phone

Two questions are answered by two different mechanisms, and conflating them is the common mistake.

**"Is this phone a paired device of this installation?"** — the **rules**, through the user claim. `internal/budget/rules/rules.go`'s `userClaim` is `auth != null && auth.token.mu != null && auth.token.mu == auth.uid`; the mailbox's own nodes use `MAILBOX` (`userClaim && $mb == auth.uid`), and `USER` — which "names no mailbox, so it cannot be used to reach one" — carries the four read grants of tension (b1). The phone signs in with a custom token minted at pairing (D165's two tokens, T5's "no public sign-in provider"), so the `mu` claim is carried in its ID token. **This is what fences reads and the ack, and it is the only part of the fence the database enforces.**

**"Is this *message* really from that phone?"** — the **MAC**, verified by the consumer (and by the phone for the outbox and the heartbeat). `shared/mailbox-schema/mac.js` over `canonicalBytes(record, direction)`, one per-pairing key, in the phone's secure storage and the laptop's OS keychain with only the item's **name** in settings (D113b option (a) / tension (b2), Voice D42(2)). The rules can require the field's presence (P6.4) but cannot verify an HMAC, which is why verification is in the consumer.

**Neither of these is a device node.** The device node `config/admin/devices/$uid` is **P6.4's proposal, not on `main`** ([§1.1](#11-there-is-no-rules-generation-3-no-device-node-and-no-pr-601-on-main)). Until it lands, "is this phone still allowed?" is answered by the Auth user's existence and the pairing key's existence, not by a node the rules consult.

**The cloud consumer's own identity is fenced, and that is new.** The laptop consumer writes as the operator's admin credential, which bypasses the rules (D113b's honesty note, `dbErr`). The orchestrator writes as a service account, so it must satisfy `CONSUMER` (`auth.token.mc == $mb`) to write the outbox and the heartbeat at all. **The phase-2 consumer is the first mailbox writer the rules can actually hold to "the outbox and the heartbeat and nothing else"** — a property worth naming, because it is the one place where moving to the cloud *strengthens* the fence rather than widening it.

### 4.7 Revocation

**Today (generation 2, no device node).** Four steps, in order, and the owner runs all of them:

1. **Delete the Firebase Auth user** for the pairing. Immediate for new sign-ins; an already-issued ID token remains valid until its `exp`, which is U2 and live check 101.
2. **Delete the laptop keychain item** for the pairing's key. The consumer is configured with the item's **name**, so this is what makes every message the phone signs stop verifying.
3. **The consumer drops the pairing's pending prompts**, delivering each one's safe answer once, and deletes its outbox messages.
4. **Re-pair** if the phone is replaced: `fugaro pair` issues a new device uid and a new key, and the old pairing is revoked.

**After P6.4 lands** (proposed, NOT APPROVED), step 1 becomes "delete the device node", which the rules consult — immediate for **reads and writes** alike, rather than only for new sign-ins.

**What revocation cannot do, stated plainly.** A launch already claimed continues: the budget token was minted and the job started before the phone was revoked, and the caps — not the revocation — bound what that run spends. That is the same residual risk [§4.5](#45-the-hard-envelope-a-stolen-phone-a-stolen-account-a-poisoned-message) E1 accepts.

**What the orchestrator's death changes about revocation: nothing.** Because the pairing key is held by the phone and the keychain — not by the service — revoking the key works whether or not the orchestrator is running, and the orchestrator cannot resurrect a revoked pairing by redeploying itself.

### 4.8 Approvals when the phone is the only human

Phase 1's model assumes a human at a laptop for the things the phone will not do: writes to `config/caps/**` and `config/kill/**`, rules deploys, secret changes, managed applies. **When the phone is the only human, "someone at the laptop" is not a control — it is a latency.** So the envelope has to be mechanical, and the pieces are these:

- **The spoken-confirmation machine** (Phase 6 P6.2, proposed, NOT APPROVED; `shared/confirm/index.js` exists in draft): a read-back of the **resolved target**, the fixed short phrase of Voice D47's reading, a timeout on an **injected** clock, one confirmation authorises **exactly one** action, and the utterance that issues a command can never confirm it.
- **Confirm targets, and why they are the whole argument.** A target is a run id, or a repository and a pull-request number, or — for a merge — that plus the PR head SHA (§3.5 of the functional-spec reconciliation). **An ambiguous target cannot be read back and therefore cannot be confirmed.** That is what stops a mishearing from becoming a merge: the machine has nothing to read back, so it refuses rather than guesses.
- **The consumer re-validates.** The phone's read-back is what the human heard; the consumer's own check is what actually runs — a MAC-verified **action record**, inside the command window, against a target that still exists, once per action id. The confirmation **timeout** is checked on the phone, where the pending state lives; the **windows** are checked on the consumer, where they can be enforced (commands 6 hours, prompt answers at the TTL).
- **The only-human rules this design adds.** (1) The orchestrator **never** synthesizes an owner confirmation and never treats its own prompt as answered — an unanswered prompt expires at the TTL and takes the **non-destructive** answer (D168 as amended). (2) It never escalates its own authority: no IAM, no caps, no kill, no self-deploy ([§6](#6-what-it-must-never-do)). (3) Launches take **no** confirmation (D162, answered against the recommendation) but do take the command window and **are** bounded by the caps — a launch is the one action the phone can take that spends money, and money is the one thing the caps bound mechanically.
- **What is not solvable here.** If the phone is the only human and the phone is stolen, the thief is the human ([§4.5](#45-the-hard-envelope-a-stolen-phone-a-stolen-account-a-poisoned-message) E1). This design does not pretend otherwise; it bounds it by time-to-revoke and by the caps.

### 4.9 Failure modes

| Failure | What happens | What bounds it |
|---|---|---|
| **The orchestrator is down** | Nothing is lost. Messages sit in the mailbox for the whole TTL (72 hours, D118/O5); the phone's presence view shows offline from the heartbeat's age (`shared/presence`); a prompt waits. The laptop's `fugaro mailbox` can consume in the meantime. | The TTL, the lease, and the phone's honest "offline" state. |
| **Two consumers act on one message** (the laptop wakes while the orchestrator ticks) | At most one acts. Three fences, in order: the mailbox's ledger keyed `(sender, id)` (`shared/mailbox`'s `step`); the consumer lease (10-minute TTL, `already-claimed`, `shared/lease`); and — for a launch — the launch claim itself. | The lease is the mailbox's fence; the launch claim is the run's. |
| **A duplicated launch** | One execution. `internal/runstore/launch.go`'s `Claim` is a create-if-absent object under the run's prefix; only the holder mints (`mintBudgetToken` runs after the claim loop in `internal/cli/run.go`); a second launcher's mint either fails the claim or, at worst, writes a token no execution will take, which `dropBudgetToken` removes and the lifecycle rule deletes within a day. | F4's claim, and the mint-after-claim ordering. |
| **A replayed message** | Refused. Commands older than 6 hours are `REPLAY`; prompt answers past the TTL are `EXPIRED`; a signed record re-sent under a **new** id is refused by action-id dedupe; the ledger survives a restart and is retained at least the TTL plus one sweep interval. The id is inside the MAC, so it cannot be changed without the key. | The windows, the ledger, and the tag. |
| **The orchestrator dies between the claim and the job start** | The claim goes stale after 10 minutes and a later tick takes it over by `ReplaceIf` on the read generation; an untaken budget token object is removed by `dropBudgetToken` or by the bucket's one-day lifecycle rule. | `ClaimTTL`, the generation precondition, the lifecycle rule. |
| **The clock differs** | A phone more than 5 minutes ahead of the consumer is refused (`DEFAULT_FUTURE_TOLERANCE_MS`, `MAX_CLOCK_SKEW`); windows are computed on the consumer's clock, so a phone ahead extends a window by the skew and a phone behind gets refusals early. | The tolerance band, and check 84 which measures the real skew. |
| **The orchestrator is older than the deployed rules** | It refuses rather than misreading. The rules marker carries the generation (`internal/projectpolicy/version.go`'s `RulesVersion`, `PathRulesMarker`), and `internal/cli/doctor.go` and `internal/infra/firebasedb.go` both refuse a marker **above** the binary's version. | Fail-closed skew refusal, the repository's existing pattern. |
| **The service is deployed but the module is wrong** (no signJwt, no job_runner on a job) | Launches fail at the claim or the mint, and **no run starts** — the failure is before any spend. A prompt answer still fails closed if the `CONSUMER` grant is missing (the rules refuse the write). | Fail-closed by construction: every missing grant removes a capability rather than granting one. |

### 4.10 Cost model

**Idle cost.** With `min-instances = 0` and request-based billing, the service bills nothing between ticks — the figure the credentials service's design records for the same shape. The idle cost is therefore the tick's own work plus the fixed pieces: **Cloud Scheduler** (a handful of jobs; on the documented free allowance), **RTDB** reads and the heartbeat writes whose volume D117 asks to measure (check 84's interval), **the runs bucket** and its lifecycle rules, **Artifact Registry** storage for the image, **Secret Manager** versions, and **Cloud Run Jobs**, which cost nothing while not running. `min-instances = 1` would replace "nothing" with a fixed monthly charge per installation — the price of sub-tick latency — and is not taken here.

**Per-task cost.** Three parts: (1) **the run's compute**, a Cloud Run Job execution billed per vCPU-second and GiB-second (the tree's one measured figure is a different backend — check 57a-5 records a 143-second `e2-standard-4` window at about $0.0053 on the VM backend, and it is cited as an order of magnitude, not as a price for this); (2) **the model spend**, which is bounded by the per-run and daily caps and by the kill switch, and by the provider account's own limit — `SECURITY.md`'s standing advice; (3) **the orchestrator's own request time**, a few hundred milliseconds of a 1-vCPU instance per tick, plus the launch.

**Prices are not in this document, deliberately.** The repository's own precedent is to fetch them when a plan commits (`docs/plans/2026-09-27-m4-gcp-backend.md` fetches the Cloud Run pricing page before committing), and a number invented here would be a number a later reader would trust. [§11](#11-unverified) lists the two figures a plan must measure.

---

## 5. The threat model, stated once

**What is assumed.** The attacker model is `SECURITY.md`'s, plus the mailbox's:

1. **The repository and the task text are trusted**, and anyone who can push to the repository, launch a run, or is listed in `followup.trusted` can steer an agent that holds the credentials that job's service account may hold (`SECURITY.md`, "The assumption"). Fugaro remains not a sandbox for code you would not run on a developer's laptop.
2. **An agent inside a run is hostile-capable**: it writes code, reads the job's environment, reaches the metadata server, and shares the container and user with the runner, so it can read the runner's `/proc` — including a mounted model credential and the GitHub App private key (`SECURITY.md`, "Known limits").
3. **A launcher is trusted to launch, and that is already a wide power**: any launcher can mint a budget identity for any repository, override a job's environment, and write `runs/` for every repository (`SECURITY.md`, "What is not on by default"). This design does not narrow that; it inherits it.
4. **Network egress is unrestricted** and the caps bound only traffic that goes through the gateway (`SECURITY.md`).
5. **The phone is an untrusted holder of exactly two items** (D111): an Auth ID token and a per-pairing MAC key. Anyone with the unlocked device has them; anyone with the Auth account has the first.
6. **Any project owner/editor or admin service account can write the mailbox nodes**, and a minted `mc` token is indistinguishable from the real consumer today (`TestMailboxResidualMintedTokenPassesUntilPhase6`).
7. **The owner's own machine is not a boundary**: the orchestrator agent runs with the operator's identity (F38's accepted risk), and the keychain item is readable by any process running as the operator — the orchestrator agent included.
8. **No live cloud was read for this document** and no `belong` installation was looked up (F31).

**What this design does not defend against, and says so.** A project-level owner credential (out of `SECURITY.md`'s scope entirely); an agent spending outside the gateway with a real key it read from `/proc` (the standing OEM limit — set a credit limit on every key); a launcher's existing write reach over `runs/`; a compromise of the owner's laptop that reads the pairing key out of the OS keychain; a compromised GCP project itself. **And the one this design newly owns:** if the resident service were ever given a model credential, the compromise of a long-lived process would yield a credential that outlives every container — which is why [§4.4](#44-how-it-holds-a-model-credential-three-candidates-priced) recommends it holds none.

---

## 6. What it must never do

1. **No IAM.** Never `setIamPolicy` on the project or on any resource; never `iam.roles.*`; never an `actAs` on the deployer service account; never a new service account, key or binding. Its grants arrive with the owner's apply and change only with another owner's apply.
2. **No managed applies.** Never `fugaro managed apply`, never import the deployer, never write the runs-bucket-root marker that permits one (D56/D57), never write `fugaro/allow-delete.json` (D74) — the bucket root is outside its object condition.
3. **No destructive commands.** Never delete a branch, close a PR, run `fugaro allow-delete`, write `config/kill/**` or `config/caps/**`, run `fugaro budget`'s writes, `fugaro secrets set`/`rm`, `fugaro init --firebase` (a rules deploy), or `fugaro restore`. The D128 amendment's three verbs are allowed **only** through the spoken confirmation, and this list is not widened.
4. **No model credential, ever, in the resident process.** It launches runs; the runs hold the credential, in the container that dies with them, until the credentials service takes it away from them too.
5. **Never read a secret value.** `secretmanager.secrets.list`/`versions.list` and nothing more — no `secretAccessor` on model, git or App secrets.
6. **Never `belong`.** Not a target, not a fixture, not a name in a test (F31).
7. **Never act on an unverified message.** No MAC, no action: not for a `message`, not for an `answer`, not for an action record, and never for free text aimed at a destructive prompt.
8. **Never answer its own prompt, and never synthesize a confirmation.**
9. **Never widen itself.** No self-deploy, no new revision from a phone request, no Terraform, no workflow file, no rules change from the mailbox. Every deploy is the owner's.
10. **Never a second installation's nodes.** One service account, one installation's project, one mailbox.

---

## 7. The checks it would need

**The next free live-check number is 110.** Read from the register rather than derived from a grep of the free numbers, as the register's own ruling requires: [gcp-live-checklist.md](../../gcp-live-checklist.md) numbers 80–89 as this work's reservation (all ten named in the plan), **90–99** as the credentials service's, and **100–109** as the functional spec v0.1's (100–104 proposed, 105–109 reserved and unused); 70–79 are "unused and deliberately not taken". **110 is therefore the first number free past every reservation**, which is the phrase the register itself uses for the numbers it took (58, and 61). [Q5](#10-open-for-the-owner) asks the owner to confirm the allocation.

**This document writes no check.** No row, no `## Check 110` section, and no edit to the register: the register's own rule is that a check's text is in the register **before** it is run, and its row is written when its plan lands. What a check would have to prove, listed here so a later writer has the shape rather than the discovery:

- The service, deployed from the installation's module and woken by its tick, **takes a message from a real phone and acts on it once** (the end-to-end version of check 80, against the cloud consumer).
- **The envelope holds**: a revoked pairing's message is refused; an unsigned message is refused; a message reflected from the outbox into the inbox is refused; a replayed message is refused and its action is not repeated.
- **The caps hold**: a launch whose run would exceed the per-run cap is refused by the **rules**, with the orchestrator running, and the kill switch stops the next lease.
- **The identity is what it claims**: reading the service account's actual IAM, the runs-bucket root is refused, `setIamPolicy` is refused, and the deployer is not impersonable.
- **The failure modes are honest**: with the service stopped, the phone shows offline and nothing is lost inside the TTL; with two consumers running, a message is acted on once.

**All of it is owner-run**, like 80–89 and for the same reason (F30): it needs a real project, a real service and a real phone.

---

## 8. What this adds to the plan (Phase 5, UNSCHEDULED)

This change edits **Task P5.1's entry** in [the plan](../implementation-plan.md)'s Phase 5 — its **Files**, its **Verification** and its **Reviewer tier** — and adds no task. P5.1 stays **UNSCHEDULED**, and nothing in this document is approved. The tasks a scheduled phase 2 would need — the installation module, the orchestrator's consumer entry, the check — are named in the plan's entry as the shape of the work, not as tasks that may start.

---

## 9. The alternatives, priced

**A1 — stay laptop-only, with a wake helper.** The phone's message wakes the laptop (Wake-on-LAN on the local network, or a small always-on helper at home that receives and then starts the work), and phase 1's consumer does the rest. *For:* no new cloud identity, no new credential in the cloud, no new service to audit, D115's own recommendation (decide phase 2 later) stays intact, and the operator's machine is where the operator already is. *Cost, stated plainly:* the phone's primary feature then depends on a home network, a laptop that can be woken, and a helper that is itself always-on and must be patched; "the laptop is asleep" becomes "the laptop must be wakable", which is a different product claim; and the failure is silent to the phone, which will show stale presence with no explanation.

**A2 — a user-scoped orchestrator in the owner's own cloud project, against a Fugaro-shared service.** *User-scoped, per installation (recommended):* one service per installation, in the installation's project, with the installation's own service account — the shape the credentials service took (D146/D147), required by the account model (D114) and by F31. *Fugaro-shared:* one service holding many installations' model credentials and pairing keys. Cost: it is the single largest blast radius in the project; it makes "the operator's own project" — the property the caps, the rules and the token channel all rest on — meaningless; it puts `belong` and every other installation behind one credential; and it makes the F36 boundary ("secrets-only, sibling services") impossible to keep. Its only advantage is operational simplicity for the owner, which the opt-in module already provides.

**A3 — a scheduled job, not a service.** The same consumer code as a Cloud Run **Job** started by Cloud Scheduler, with no service at all. *For:* a smaller surface (nothing listens), a cheaper tick, and no ingress configuration; the lease's 10-minute TTL comfortably exceeds a per-minute tick, so the lease can simply expire. *Cost:* no request path — the phone's every interaction must go through the mailbox (which is D112/D124's model anyway), and the job's cold start is paid per tick. **If the mailbox is the only path, this is the better build and the owner should take it over §4.1's service**; the service is chosen only for the synchronous path a later feature would want.

**The recommendation.** **A1 until P5.1 is scheduled; then A2's user-scoped shape with A3's deployment if the mailbox stays the only path, and §4.1's service if it does not.** The one thing this design argues against outright is a shared, credential-holding service, and the one thing it argues against inside a user-scoped build is a model credential in the resident process.

---

## 10. Open for the owner

Answering these is what lets a later pass assign numbers and a plan write tasks. Each carries the options, this design's recommendation and why.

**Q1 — Does phase 2 ship at all, and as what?** *(a)* A resident Cloud Run service, scale-to-zero, one per installation, woken by a scheduler tick (§4.1) — **recommended if a synchronous phone path is wanted**; *(b)* the same consumer as a **scheduled Cloud Run Job** (A3) — **recommended if the mailbox stays the only path**, which is the smaller surface and the cheaper tick; *(c)* no phase 2: stay laptop-only (A1). *Why the recommendation is conditional:* everything the phone does today is a mailbox message, so (b) is enough; the service earns its extra surface only if the phone gets a request path, and that is a product question, not a hosting one.

**Q2 — Does the resident orchestrator hold a model credential, or only launch runs that do?** *(a)* **No credential in the resident process** (recommended): the agent runs in a job, with the credential it has today, moving to the credentials service when it lands; *(b)* a model credential mounted into the orchestrator itself, so it can run an agent in-process. *Why (a):* it is the difference between a compromise that dies with a container and one that outlives every run, and it is the property [§4.5](#45-the-hard-envelope-a-stolen-phone-a-stolen-account-a-poisoned-message)'s envelope rests on.

**Q3 — Per installation, or one Fugaro-shared service?** *(a)* **Per installation, in the installation's own project** (recommended); *(b)* a shared multi-tenant service. *Why (a):* D114's account model, F31's `belong` rule, the credentials service's own D146/D147 shape, and the F36 boundary — a shared service holds every customer's keys, which is the blast radius the whole design exists to avoid.

**Q4 — May the orchestrator's tick be the only writer of the heartbeat, and at what cadence?** *(a)* One write per tick, cadence chosen with check 84's measurement (recommended); *(b)* a separate higher-frequency tick just for presence. *Why (a):* D117 asks for the write-cost measurement before the interval is fixed; a second tick is a second scheduled job for a number nobody has measured yet.

**Q5 — Is Check 110 this work's?** *(a)* **Yes — the next free number past every reservation** (recommended); *(b)* fold the always-on checks into 80–89. *Why (a):* all ten of 80–89 are already named for other things (mailbox end-to-end, the rules deploy, replay, presence, STT, hands-free audio, multi-account, push, EAS), so folding would mean renumbering a reserved block, which the register forbids.

**Q6 — May P5.1 ship before the device node (P6.4, proposed)?** *(a)* **No** (recommended): revocation is the envelope's only time bound, and without the device node it is four owner-run steps with an ID-token window (U2); *(b)* yes, accepting "delete the Auth user and rotate the key" as the revocation story. *Why (a):* the mobile reconciliation already accepts time-to-revoke as E1's residual risk; shipping without the mechanism that makes it short would make that acceptance much larger than the owner agreed to.

**Q7 — Who deploys it, and may it deploy itself?** *(a)* **Owner-run, from the installation's Terraform root, following D146's pattern for the credentials service** (recommended); *(b)* an agent-run apply. *Why (a):* it is the first always-on identity this work introduces, and D63 refused an always-on credential for a weaker reason than this one has.

---

## 11. UNVERIFIED

| # | Claim | Why unverified | What settles it |
|---|---|---|---|
| U1 | A Cloud Run service at `min-instances = 0` with a per-minute Cloud Scheduler tick wakes fast enough for the phone's perceived latency, and costs nothing idle | No service was deployed and no price was read | A deploy in the sandbox, and the pricing page fetched at plan time (the `m4-gcp-backend` plan's own precedent) |
| U2 | The heartbeat's write volume at the chosen cadence is negligible against RTDB cost | D117 asks for the measurement and it has not been made | Check 84 (presence, the interval and the clock skew) |
| U3 | A deleted Auth user's already-issued ID token stays valid until its `exp`, so E2's read window is the token's remaining lifetime | Firebase behaviour, not this tree's code | Live check 101 (proposed, P6.11) |
| U4 | `fugaro run` driven with flags and no terminal behaves identically from a service account on Cloud Run (the metadata path of `google.DefaultTokenSource`, and every interactive branch) | No service exists | A sandbox run launched from a deployed service, which is part of check 110's shape |
| U5 | A Cloud Run **Job** on a schedule (A3) holds the consumer lease cleanly across its 10-minute TTL | No job exists | The same check, if the owner takes Q1(b) |
| U6 | The four mailbox writes are the whole surface the cloud consumer needs, i.e. nothing it must do requires a write the rules do not grant the `CONSUMER` claim | `TestMailboxResidualMintedTokenPassesUntilPhase6` records that a minted token passes today, so the grant's *shape* is unproven against a narrow writer | The emulator cases of P2.2, then check 81 against the deployed rules |

---

**Related.** [../implementation-plan.md](../implementation-plan.md) (Task P5.1, Phase 5 — UNSCHEDULED) · [../reconciliation.md](../reconciliation.md) (D110–D129; F4, F13, F14, F17, F20, F21, F30, F31, F35) · [../functional-spec-reconciliation.md](../functional-spec-reconciliation.md) (§3.5 the MAC, the ledger and a stolen phone; §3.6 the windows) · [../../design/managed-projects.md](../../design/managed-projects.md) (D55–D78, and D63's refusal of an always-on credential) · [../../design/budget-token-channel.md](../../design/budget-token-channel.md) (§7 the seal; D14–D19) · [../../design/named-oauth-tokens.md](../../design/named-oauth-tokens.md) (U1, and why account choice is deploy-time) · [../../credentials-service/README.md](../../credentials-service/README.md) (D130–D159; F36's boundary) · [../../design/voice.md](../../design/voice.md) (D40, D42, D54b, D47) · [../../../SECURITY.md](../../../SECURITY.md) · [../../gcp-live-checklist.md](../../gcp-live-checklist.md) (the register, and the numbering rule).

*Status of this document: design only; nothing is implemented. D115 is answered ("decide phase 2 later"); this document is the design that answer asked for, and it approves nothing. Task P5.1 remains unscheduled. No decision number is allocated and no check is written by this change.*

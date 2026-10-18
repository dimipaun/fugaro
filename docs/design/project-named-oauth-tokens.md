# Project-level named Claude OAuth tokens

Status: design, 2026-10-18, step 1 of the feature the owner ruled on 2026-10-18 (release 0.9.0): an
audit of what the project (shared-job) path does with a Claude OAuth credential today, and the
design that lifts the refusal. **This document decides nothing by itself**: §8 lists the owner
decisions with a recommendation each, and the plan
[../plans/2026-10-18-project-named-oauth-tokens.md](../plans/2026-10-18-project-named-oauth-tokens.md)
is written to be vetoed task by task. SECURITY-SENSITIVE — it designs a credential's carrier, and
the tasks that implement it are flagged for an independent review.

Every fact in §1 and §2 is cited to code read on the integration branch `integration-0.9.0`
(`origin/next-0.8.5`, head `13a5a48`), which is what this branch is cut from; the one exception is
the seal, which lives on `origin/next-0.9` and is marked as such throughout. **No cloud call was
made**, no secret was read, and no secret-shaped literal appears in this document. §7 lists what
would need a live check, as owner-run probes.

Built on: [named-oauth-tokens.md](named-oauth-tokens.md) (the individual level),
[project-centric.md](project-centric.md) §4.1 and §7 (the shared job and the phase-5 sketch),
[project-secrets.md](project-secrets.md) (project-scope containers),
[budget-token-channel.md](budget-token-channel.md) (the channel this design borrows).

---

## 1. What exists today, per path (verified in the code)

The individual level is built and works. The project level refuses in two independent places, and
one of the two is a hard-coded empty axis rather than a rule. Read together:

| # | path | what happens today | where |
| --- | --- | --- | --- |
| 1 | `agent.auth: oauth` on a **per-repository** job, default account | works: the job mounts exactly `claude-oauth-token` as `CLAUDE_CODE_OAUTH_TOKEN`, and nothing else — no Anthropic key is mounted beside it | `internal/infra/spec.go:582-602` (`SecretMounts`, the `case "oauth"` branch at :585-586) |
| 2 | `agent.auth: oauth` on a per-repository job, **named** account | works: one job *variant* per (workflow, account), named by `gcp.WorkflowVariant(workflow, token)`, its service account per variant too | `internal/infra/spec.go:609-614` (`WorkflowAccounts`), `:870-896` (the `for _, tok := range WorkflowAccounts(...)` loop), `internal/backend/gcp/names.go:349-357` (`WorkflowVariant`) |
| 3 | which repositories may use which named account, per-repository | `claude_accounts.<name>.allow_data_to` in the **local config only**; `fugaro.yaml`'s `agent.oauth_token` picks among the names the local config allow-lists; checked at launch and again by `fugaro doctor` | `internal/config/claude_accounts.go:63-67, 71-73, 144-159`, `internal/cli/run.go:945-983` (`claudeAccountFor`, `claudeAccountAllowed`), `internal/cli/doctor.go:1296-1325` (`oauthTokenAllowedCheck`) |
| 4 | `claude_accounts:` reaching the published shared config | **stripped**: `Shared()` sets `s.ClaudeAccounts = nil`, and `fugaro project policy publish`'s `sharedForbidden` list refuses the key and the value | `internal/localcfg/shared.go:44` (with the reasoning at :22-27), `internal/cli/sharedcfg.go:180, 214-218, 247-248` |
| 5 | storing a **named** account's token project-wide | **already allowed**: `secrets set claude-oauth-token-<name> --project-wide` is accepted when that account's `allow_data_to` is non-empty, and a project-scope Secret ID is published for it | `internal/cli/secrets.go:631-645` (`projectWideAccepted`, the named-account branch at :641-643), `internal/infra/tfvars.go:271-276` (`projectSecretContainers`) |
| 6 | storing the **default** account's token project-wide | allowed only through an explicit `project_secrets: [claude-oauth-token]` entry with a non-empty `allow_data_to`; the D2 sweep deliberately skips the whole `claude-oauth-token` namespace | `internal/config/project_secrets.go:63-71` (`ProjectScopeByDefault`), `internal/cli/secrets.go:622-631` (point 1 of `projectWideAccepted`) |
| 7 | a **profile** mounting any Claude OAuth token in `secrets:` | **refused**, by the policy's own profile rule, before any job is built | `internal/projectpolicy/profiles.go:276-292` inside `validProfiles` (`CheckProfiles` at :412), predicate `isClaudeOAuthSecret` at :328-331 |
| 8 | `fugaro init` deploying a job for such a profile | **refused**: `ProjectJobs` calls `projectpolicy.CheckProfiles` first and returns the policy's own error | `internal/infra/projectjobs.go:57-68` |
| 9 | a project job's **account axis** | hard-coded empty: `const account = ""`, so the job name and the job service account carry no account segment | `internal/infra/projectjobs.go:92-103`, `internal/backend/gcp/names.go:179-191` (`ProjectJobName`, `ProjectServiceAccountID`) |
| 10 | `fugaro run` with `agent.auth: oauth` **onto a shared project job** | **refused up front**, before the claim is taken and before any budget token is minted | `internal/cli/project_job.go:184-233` (`checkProjectJobOAuth`), called at `:105`, after the account check at `:99` |
| 11 | `fugaro run` selecting an account that is not the job's | refused by `checkProjectJobAccount`, which compares the run's selection against the account computed from the profile's `secrets:` | `internal/cli/project_job.go:131-176` (`checkProjectJobAccount`, `projectJobAccount`) |
| 12 | the **runner**'s half of the job model | already account-aware: `jobIdentity` matches the run's job name against every profile's computed job name and returns `(profile, provider, account)`; `matchJobName` takes `spec.OAuthToken` | `internal/runner/projectjob.go:76-101` (`jobIdentity`), `:60-74` (its doc), `internal/runner/projectjob.go`'s `projectJobCandidates` / `matchJobName` |
| 13 | the **account of a project job**, computed on both sides | one function computes it from the profile's mounts: `config.ClaudeAccountOfSecrets(pr.Secrets)`, used by the launcher and by the runner's candidate list | `internal/config/claude_accounts.go:112-122`, `internal/cli/project_job.go:98`, `internal/runner/projectjob.go` |
| 14 | the **budget-token seal** | **not on this branch.** On `origin/next-0.9` the seal is built: `ObjectPrefix = "tokens"`, `ObjectKey(slug, run) = tokens/<slug>/<run>/budget-token`, `PutObject(ctx, b, slug, run, tok, key)` sealing under a per-launch key, `TakeObject(ctx, b, slug, run, key)` unsealing and refusing a sealed object presented without a key (`ErrSealedNoKey`) or a plaintext one (`ErrNotSealed`) | `origin/next-0.9:internal/budget/token/seal.go`, `:internal/budget/token/object.go:14-175`; on this branch the same API is the pre-seal one (`runs/<slug>/<run>/budget-token`, no key) — `internal/budget/token/object.go:15, 21, 76` |

Two conclusions from the table, and they are the reason this feature is small:

- **The job axis is built.** Everything that would have to know about a `(profile, provider,
  account)` job already does: the name and service-account derivations take the account
  (`names.go:179-191`), the deployer's `ProjectJob` carries an `Account` field
  (`projectjobs.go:32`), the launcher checks the run's selection against the job's account
  (`project_job.go:131-176`), and the runner computes and returns it (`runner/projectjob.go:76-101`).
  What is missing is one hard-coded `""` and two refusals.
- **The storage is built.** A named account's token can already be stored project-wide and already
  gets a project-scope Secret ID (`secrets.go:641-643`, `tfvars.go:271-276`). The gap is not
  storage; it is that no profile may name one and no shared job may carry one.

## 2. What is refused, exactly, and by which rule

Three refusals, in the order a launch meets them. They are independent — lifting one does not lift
the others — and each is stated in the code's own words:

1. **The profile may not mount a Claude OAuth token** (`internal/projectpolicy/profiles.go:292`):
   "a Claude OAuth token is person-owned (design D2), and its `allow_data_to` is a per-repository
   list a shared job cannot honour — a profile is shared by every repository admitted to it — so no
   profile mounts one until phase 5 decides how an account reaches a shared job." The predicate is
   `isClaudeOAuthSecret` (`:328-331`): the bare `claude-oauth-token` **and** the whole
   `claude-oauth-token-` prefix. This is the rule `fugaro project policy publish` holds a profile
   to, and the same rule `fugaro init` hits through `ProjectJobs` (`projectjobs.go:66`).
2. **A launch whose `agent.auth` is `oauth` may not land on a shared job**
   (`internal/cli/project_job.go:223-233`): "a Claude OAuth account is not supported on a shared
   project job until phase 5". This is Task 3.4a, added under the owner's ruling of 2026-10-17. It
   refuses *before* the claim and before the mint, so nothing is left behind.
3. **The account axis is empty by construction** (`internal/infra/projectjobs.go:92-96`): the
   comment says it plainly — "empty until phase 5, because no profile may mount a Claude OAuth
   token yet". This is not a check; it is the absence of one, and it is why refusal 1 is the one
   that matters.

The second rule is the reason the audit's first conclusion is worth stating carefully. `secrets:`
on a profile is documented and implemented as **env mounts** — `projectJobSecrets` resolves each
name to a variable through `projectSecretEnv` (`projectjobs.go:211-251`), and a Claude token has no
variable there because `projectSecretEnv` does not know it (`:243-251`, and its own doc says so).
So even if refusal 1 were lifted, a Claude token in `secrets:` would be refused by refusal 2's
function (`projectJobSecrets`) or would be mounted as a static `secret_env` — which is the one
outcome the next section argues against.

## 3. Why a static `secret_env` is the wrong carrier (the design's central claim)

The obvious minimal change is: let a profile name `claude-oauth-token-<name>`, teach
`projectSecretEnv` to map it to `CLAUDE_CODE_OAUTH_TOKEN`, and let the job's static `secret_env`
carry it. It is the smallest diff and it is wrong, for three reasons that are properties of the
shared job, not of the token:

1. **The policy's gate would be a lie.** A project job serves *every* repository admitted to its
   profile (§4.1 of [project-centric.md](project-centric.md)). With the token in the job's static
   environment, every execution of that job has it — including a run of a repository the account's
   allow-list does not cover. The gate could then only be enforced at launch, by the launcher, and
   the launcher is trusted (the channel doc's D10 and its §3 constraint 5, in
   [budget-token-channel.md](budget-token-channel.md)): a run that started by any other route would
   still hold the token. The owner's clause "a
   policy gates which repositories may use which token" asks for a gate that is real at the point
   of use.
2. **The job's whole environment is readable by every launcher and by the history account.**
   `internal/backend/gcp/run.go`'s `JobFullEnv` reads a job's entire environment through the Cloud
   Run API, and the accounts that may read an execution are the sweep's `fugaroHistory` and the
   launchers' own `fugaroLauncher` ([budget-token-channel.md](budget-token-channel.md) §3
   constraint 1). A standing copy in the job template is a standing copy for that audience, and it
   survives the run.
3. **It is a second credential path to maintain beside the one being built.** The budget token
   already answers exactly this shape of problem: a per-run credential, out of the job's
   environment, delivered to one execution. The owner's ruling names it — the token reaches the run
   through the existing budget-token seal channel.

So the design's carrier is the seal, and the rest of this document is about what that costs.

## 4. The design

### 4.1 The policy gains an `accounts` block, and a profile may name one

Today the local config's `claude_accounts:` is the anchor for the per-repository choice, and
`Shared()` strips it — with the reasoning written out at `internal/localcfg/shared.go:22-27`:
T1's operator-only trust anchor "must not round-trip through the runs bucket either, which any
launcher with `objectAdmin` on it can write". That reasoning is about the **runs bucket**, and it
does not apply to the **policy node**: `config/policy/shared` in RTDB is written by
`fugaro project policy publish`, which is an operator command, and read by the runner, which fails
closed on it. A shared job is already a function of the published policy — `ProjectJobs` builds from
the local config's profiles and `fugaro project policy publish` holds them to the same rules — so
the account a shared job uses has to be a policy fact.

The policy therefore gains:

```yaml
# config/policy/shared, beside profiles:
accounts:
  acme-team:
    allow_repos: ["acme/app", "acme/tools"]   # which repositories of this project may use this token
profiles:
  standard:
    provider: github
    claude_account: acme-team                 # NEW: the account this profile's job serves
    secrets: [anthropic-api-key]
```

- `accounts.<name>.allow_repos` is **derived at publish time** from the local config's
  `claude_accounts.<name>.allow_data_to` — one anchor, one edit, no second list to keep in sync.
  The key is spelled `allow_repos` because that is the policy's own vocabulary (`Policy.AllowRepos`,
  `profiles.<p>.allow_repos`), and the publish path is where the translation happens, in one place.
- `profiles.<p>.claude_account` is a **new field**, not a new entry in `secrets:`. Keeping it out of
  `secrets:` is what lets refusal 1 stand unchanged: a Claude token is still never an env mount on
  a shared job, and the existing rule (`profiles.go:292`) stays true and stays in force for
  `secrets:`. The new field is a narrower, separately-checked permission beside it.
- Publish **fails closed** on a profile whose `allow_repos` is not covered by its account's list,
  naming both lists. Without that rule, a repository admitted to the profile but not to the account
  is a job it can never successfully run on, which is a footgun rather than a policy.
- `config.JobProfile` gains the field (`internal/config/`), and the local config validates it
  against `lc.ClaudeAccounts` at load, as it validates everything else about a profile.

### 4.2 The secret-id scheme does not change

The token's logical name is exactly what `config.ClaudeAccountSecret(name)` already returns
(`internal/config/claude_accounts.go:80-85`): `claude-oauth-token` for the default account,
`claude-oauth-token-<name>` otherwise — inside `SecretNameRE`
(`internal/config/validate.go:27`), and the whole prefix is reserved for the purpose
(`internal/config/validate.go:215-226`, `internal/cli/secrets.go:378-382`). The container is
`gcp.ProjectSecretID(fugaroProject, logical)` (`internal/backend/gcp/names.go:207-209`), which is
domain-separated with `"project\x00"` and already used for every project-scope secret. **No new
helper, no new prefix, no rename.** The `claude-` prefix the owner asked for is already there, and
it is what leaves room for the parallel `codex-oauth-token[-<name>]` scheme (see §7 U6).

Storage eligibility does not change either: a named account is accepted project-wide when its
`allow_data_to` is non-empty (`secrets.go:641-643`), and the default account needs the explicit
`project_secrets` entry it already needs (`project_secrets.go:63-71`). §8 D5 and D6 put the two
remaining choices about that to the owner.

### 4.3 The token reaches the run as a second sealed object

The carrier, recommended:

- The launcher seals the account's token under the **same per-launch key** the budget token uses,
  into a second object with a fixed name beside it —
  `tokens/<slug>/<run>/claude-oauth-token` — written create-if-absent, with the budget token's
  retry re-seal mirrored.
- The runner takes it once, unseals it, registers the plaintext with its redactor
  (`internal/runner/runner.go:609-616`, `addSecret`), and puts it in the agent's environment as
  `CLAUDE_CODE_OAUTH_TOKEN`. The key travels by the carrier the seal built for it, and it is **not**
  a plain `LaunchSpec.Env` entry: `next-0.9`'s Slice B adds a second field, `LaunchSpec.SecretEnv`
  (`origin/next-0.9:internal/backend/backend.go:80-90`), and writes the key as
  `lspec.SecretEnv = []string{sealMarker(sealKey)}` (`origin/next-0.9:internal/cli/run.go:1623`).
  The name is still `runner.BudgetTokenEnv` (`internal/runner/budget_session.go:50`); what changed
  is which list it is in. On this branch there is no `SecretEnv` at all — the marker is a plain
  `LaunchSpec.Env` entry carrying one bit, `internal/cli/run.go`'s
  `lspec.Env = []string{runner.BudgetTokenEnv + "=1"}` — so the two must not be confused.

  That second field is load-bearing rather than cosmetic, and Task 6 must use it.
  `internal/backend/launchenv.go`'s `LaunchEnv` parses `Env` and then `SecretEnv`, marks a
  `SecretEnv` entry `Secret`, and requires a backend whose only place for such an entry is readable
  by a wider audience than the execution's override — Batch's job variables — to **refuse the
  launch** rather than carry it. So the carrier is not reused unchanged: it is extended by one
  field, and the extension is exactly what makes a backend that cannot keep the key private say so
  instead of leaking it. What this design adds to the carrier is nothing beyond that field — no new
  variable, no new permission and no new resource, which is
  [budget-token-channel.md](budget-token-channel.md) §7.1's own point — but an implementation that
  wrote the key into `LaunchSpec.Env` would undo Slice B's whole reason for the field.
- A second object, **not** a second field in the existing envelope. The budget token's envelope is
  versioned, reviewed and mutation-tested (`seal.go`, `seal_aad_test.go`, `seal_test.go` on
  `next-0.9`), and its take is the consume. Widening its payload would couple the credential's
  failure modes to the budget channel's; a second object reuses `Seal`/`Unseal` and the AAD rule
  (the object's own name) with no change to either.

What this costs, stated plainly: **the launcher has to read the account's secret value.** Today it
cannot — SECURITY.md's launcher paragraph says reading a value needs `secretAccessor`, "which only
the job and build accounts hold" — so the design adds one member resource per account the
installation publishes: `roles/secretmanager.secretAccessor` for the launcher account on
`gcp.ProjectSecretID(project, ClaudeAccountSecret(name))`, bounded to the accounts a published
profile names. This is a real widening and it is the design's most important single decision
(§8 D1 and D2). Three things bound it and one does not:

- it is bounded to the accounts the installation actually serves, not to every account the
  operator's local config defines;
- it is a member resource, never a binding or a policy, which is the rule every grant in this
  repository already follows (`deploy/terraform/gcp/modules/installation/iam.tf` says so at the
  top of the file, and [budget-token-channel.md](budget-token-channel.md) §3 constraint 2 quotes
  it);
- it grants read of a **secret's value**, which is the same class of power the launcher already has
  over the budget token's minting key — but it is strictly more than that, because the budget token
  is a per-run identity while this is a long-lived credential;
- **what does not bound it**: the seal. A principal holding both halves recovers the token, and
  with this grant the launcher is exactly such a principal. The seal's own doc names this
  ([budget-token-channel.md](budget-token-channel.md) §7.1: "a capability, not a boundary"). The
  seal buys protection against the adversary that matters here — a **peer run** of the shared job,
  which holds the bucket grant and no Secret Manager access — not against a launcher.

§8 D2 records the alternative the owner may prefer: the **job's own** service account already holds
`secretAccessor` on exactly its account's secret (the axis exists for that), so the runner could
read the secret with its own identity and the seal would carry only the account name. That avoids
the launcher widening entirely, at the cost of a live Secret Manager read on the run's hot path and
of the credential entering the job's process environment by a route the seal does not cover. It is
a real alternative, not a straw man, and the plan is written so the choice is made before any code
is written.

### 4.4 Failure naming, and no silent fallback

Every path fails closed, names the thing the operator must fix, and never degrades to another
credential. The names, in the order a run meets them:

| when | condition | what is said |
| --- | --- | --- |
| publish / `fugaro init` | a profile names an account the policy's `accounts` block does not define | name `profiles.<p>.claude_account`, the account, and the fact that the policy's `accounts` block is where it is defined |
| publish / `fugaro init` | the profile's `allow_repos` is not covered by the account's | name both lists and the repository that is in one and not the other |
| publish / `fugaro init` | a profile names an account **and** mounts an Anthropic API key | refuse: the two credentials answer the same question, and with both present the agent's choice would be implicit |
| launch | `agent.auth: oauth` on a shared job whose profile names no account | refuse, naming the profile, `profiles.<p>.claude_account`, and `claude_accounts` in the local config |
| launch | the repository is not in the account's `allow_repos` | refuse, naming the repository, the account, `accounts.<name>.allow_repos` and the profile's `allow_repos` |
| launch | the account's token has no project-wide container | refuse, naming the logical secret, `project_secrets:` and `fugaro secrets set <logical> --project-wide` |
| runner | the marker says a token was sealed and the object is absent | refuse to start; never run without the credential |
| runner | the object is sealed and no key was presented, the key is wrong, or the AAD name does not match | refuse — `ErrSealedNoKey`'s own words on `next-0.9` are the model: "it is not read as a token: a plaintext fallback is the exposure the seal exists to close" |
| runner | the token is present but the agent cannot use it | the run fails; it does **not** fall back |

The no-fallback property is worth stating as a mechanism rather than an intention, because the
per-repository path gets it for free and the project path does not: `SecretMounts` under
`auth: oauth` mounts the OAuth secret **and nothing else** (`internal/infra/spec.go:585-586`), so
on a per-repository job there is no API key present to fall back to. A shared job's environment is
whatever the profile lists, so a profile that mounted an Anthropic key beside its account would
leave `ANTHROPIC_API_KEY` set while `CLAUDE_CODE_OAUTH_TOKEN` was being delivered — and which one
Claude Code prefers is not something this document claims to know (§7 U2). The design removes the
question instead of answering it: **a profile that names `claude_account` may not mount an
Anthropic API key**, checked at publish and at `init`, and the runner refuses a run whose marker
promises a token it did not get.

### 4.5 The job axis, populated

`ProjectJobs` (`internal/infra/projectjobs.go:92-103`) stops hard-coding `const account = ""` and
computes it the way the launcher and the runner already do — from the profile's account field, with
one function (`config.ClaudeAccountOfSecrets`'s sibling, or that function extended to take the
profile) so that the three sides cannot drift. The job's service account gains
`secretAccessor` on its account's secret **only if** §8 D2's alternative is chosen; under the
recommended carrier it needs none, and the axis is then what the runner binds the run's identity
to (`internal/runner/projectjob.go:76-101`) and what keeps a job's name a function of what it
serves.

One consequence is worth flagging to the owner before the plan runs: populating the axis **renames
existing jobs** for every installation that already has a profile, because
`gcp.ProjectJobName` (`names.go:179-182`) omits the account segment when it is empty. `RefuseProjectJobDeletes`
(`projectjobs.go:267-282`) will therefore stop the next apply, which is the guard working as
designed — but it means the rollout is not transparent, and §8 D9 puts the sequencing to the owner.

## 5. Are phases 5.1 and 5.2 prerequisites? (the owner asked; the evidence says no)

The owner's instruction was to revisit this if the answer changed. It did, and the evidence is
one-sided:

- **5.2's shape is already built.** Task 5.2 is "one job per (profile, shared account)". The job
  name and service account already take the account (`names.go:179-191`), `ProjectJob` already
  carries an `Account` field (`projectjobs.go:32`), the launcher already refuses a run whose
  selection is not the job's account (`project_job.go:131-176`), and the runner already returns the
  account it bound (`runner/projectjob.go:76-101`). The remaining work is one hard-coded `""` and
  the two refusals of §2 — which is *this* feature, not a prerequisite for it. Treating 5.2 as a
  prerequisite would mean building the axis a second time.
- **5.1 is a different feature.** Task 5.1 is *person-owned* secrets: one Secret Manager secret per
  **(person, account)**, an opaque ID (`gcp.PersonSecretID`), `fugaro accounts add` and
  `fugaro accounts share`, a reference record in the run-readable policy node with no identity in
  it, and failover moved into `fugaro run`
  ([project-centric.md](project-centric.md) §7, plan Tasks 5.1-5.3). The owner's ruling of
  2026-10-18 describes the opposite ownership model: **project-level named tokens**, one per
  account, owned by the installation, exactly like the individual level's
  ([named-oauth-tokens.md](named-oauth-tokens.md) §3). Building 5.1 first would build a
  per-person credential store and then not use it. The one thing 5.1 contributes is the observation
  that a project-scope Claude token should not be a static env mount; this design reaches that
  conclusion by the seal instead, and §3 gives the three reasons.
- **What *is* a prerequisite is the seal.** The carrier of §4.3 is `next-0.9`'s Slice A and B: 24
  commits, whose base commit is an ancestor of this branch, so the merge is expected to be clean
  and is a real, checkable step. It is Task 1 of the plan, and nothing else in the plan can land
  before it.
- **What is explicitly out of scope** by the owner's rulings of 2026-10-18: the Cloud Batch backend
  (excluded from 0.9.0) and the project-centric phases 4 and 6. Nothing here depends on either.

## 6. Threat model

| adversary | what stops it | what does not |
| --- | --- | --- |
| a **peer run** of the same shared job — the §4 adversary of the channel doc: it holds the job's bucket grant, so it can read the sealed object | the per-launch key: the object is ciphertext under a key delivered to one execution, and a sideways copy unseals under nothing | it can still *delete* the object (denial of service), and it can still read the object. The seal answers theft, not denial — the channel doc's §7.2 says so |
| a **launcher** | nothing, by construction: it seals the object, so under the recommended carrier it must be able to read the token | this is the channel doc's D10 and it is unchanged; the widening is priced in §4.3 |
| a **project log viewer** holding an override view | the key is not in the object, and the object's bytes are ciphertext | if such a principal holds *both* an execution's override and the object, it recovers the token — the channel doc's §7.1 prices this case in the same words |
| the **agent process itself** | nothing | this is the residual §6.1 states, and it is the honest cost of an OAuth credential |
| a repository admitted to the account's list but hostile | the account's blast radius is not per repository | it gets a credential that works for **every** repository in the list, for as long as the token is valid |
| a repository **not** in the list | the launch gate, and the absence of the object (nothing sealed one) | nothing else, and that is the point of the seal: the gate is not the only thing standing between it and the token |

### 6.1 The residual SECURITY.md has to carry

An OAuth token is a bearer credential with no per-repository scoping, no gateway in front of it and
no revocation finer than the account's. So SECURITY.md has to say, in the owner's own terms:

- **A hostile run can use the token during its own run.** The agent's process holds
  `CLAUDE_CODE_OAUTH_TOKEN` in its environment for the duration of the run; it can read it, copy it
  into an artifact, a PR body or a log line (the redactor reduces the last of these, and is not a
  boundary), and use it after the run ends, until the owner revokes it. The seal protects the token
  **in the bucket**, not in the process.
- **The account is the blast radius.** `allow_data_to` and `allow_repos` gate *who may use* a
  token; they do not scope what the token *can* do. Every repository in an account's list shares
  one credential, and a compromise anywhere in that list is a compromise of all of it.
- **Nothing prices an OAuth run the way the gateway prices a provider-key run.**
  [named-oauth-tokens.md](named-oauth-tokens.md) §1 records that accounting has no per-account
  dimension; the budget channel attributes spend to the *run*. Usage limits are the account's own,
  which is what `fugaro accounts ls` and the usage-limit switch exist for.
- **The launcher reads the account's secret** (under the recommended carrier), which is more than
  it can do today, and is the price of the seal.

## 7. What is verified, and what is not

**Verified in the code** (read, cited above): everything in §1, §2 and §4.2; the account axis's
existence on all three sides; the project-wide storage of a named account; the two refusals and
where they live; the seal's API and its AAD rule on `origin/next-0.9`.

**Not verified — owner-run, live, sandbox only** (no probe was run for this document):

- **U1. Whether an execution's own override can be read back through the Cloud Run API.** This is
  the channel doc's U2 / Check 52 step 3, still unrun, and it decides how wide the seal's audience
  is. Everything in §6 that depends on it is marked as a hypothesis there and here.
- **U2. Which credential Claude Code prefers when both `CLAUDE_CODE_OAUTH_TOKEN` and
  `ANTHROPIC_API_KEY` are set.** §4.4 removes the question by refusing the combination; the probe
  is what would let the refusal be relaxed later, and it is not needed to ship.
- **U3. Whether one account's OAuth token can serve several concurrent runs.** The design puts N
  repositories on one account's token. If the account refuses concurrent use, the feature is a
  serialization bottleneck and `profiles` have to be split per account — a sizing question, not a
  security one, but it decides whether the feature is useful.
- **U4. Whether the seal's per-launch key survives a Cloud Run retry** exactly as the budget token
  assumes; the retry re-seal is built for the budget token, and this design mirrors it rather than
  re-deriving it.
- **U5. Whether `roles/secretmanager.secretAccessor` for the launcher can be added without
  disturbing the secret container.** Expected: yes, an IAM member is additive and the container's
  `prevent_destroy` is untouched — but the golden plans in the implementing PR are what confirm it,
  not this document.
- **U6. The `codex-` scheme being built in parallel (Task C3).** This document assumes the reserved
  names will be `codex-oauth-token[-<name>]` and `<provider>-api-key`, and that the `claude-`
  prefix is what leaves room for them. Nothing here depends on C3 landing, and C3 does not depend
  on this: the two meet only in the naming convention, which §4.2 leaves exactly as it is.

## 8. Owner decisions

Each with a recommendation first. The plan's tasks are cut so that any of these can be answered the
other way without rewriting the others.

**Numbering.** D1 to D10 below are **this document's own** decisions. They are not
[budget-token-channel.md](budget-token-channel.md)'s D14-D19, and in particular this document's
**D10 is not that document's D10**: the channel doc's D10 is its "the launcher stays trusted"
decision, and every reference in this document to *the channel doc's D10* means that one, not the
inspection decision below. The plan restates these ten as its own Decisions section, so the same
reading applies there.

**D1. The carrier.** *Recommend:* a second sealed object, `tokens/<slug>/<run>/claude-oauth-token`,
written by the launcher under the same per-launch key, taken once by the runner, unsealed into
`CLAUDE_CODE_OAUTH_TOKEN`, with the budget token's create-if-absent and retry re-seal mirrored.
*Alternatives:* (a) the job's service account reads the secret and the seal carries only the
account name — no launcher widening, but a live Secret Manager read on the hot path and the
credential entering the job's process environment outside the seal; (b) the static `secret_env`
mount — smallest diff, and §3 gives the three reasons it is wrong.

**D2. The launcher's read.** *Recommend:* grant the launcher
`roles/secretmanager.secretAccessor` on `gcp.ProjectSecretID(project, ClaudeAccountSecret(name))`
for each account a published profile names, as a member resource, and say so in SECURITY.md.
*Alternative:* D1(a), which avoids the grant. *Not recommended:* granting it on every account in
the local config, which would widen it past what the installation serves.

**D3. The policy's gate.** *Recommend:* `accounts.<name>.allow_repos` in the policy, derived at
publish time from `claude_accounts.<name>.allow_data_to`, with publish refusing a profile whose
`allow_repos` its account's list does not cover. *Alternative:* no separate list — the profile's own
`allow_repos` is the gate, which is coarser (one account per profile) but has one list fewer.

**D4. The profile field.** *Recommend:* `profiles.<p>.claude_account: <name>`, a new field, leaving
the `secrets:` refusal exactly as it is, and refusing at publish a profile that both names an
account and mounts an Anthropic API key. *Alternative:* let the account be named in `secrets:` after
all, which would require the mount to be suppressed for that one name — a special case in the one
function that resolves mounts.

**D5. The default account on a shared job.** *Recommend:* allowed, with the bare
`claude-oauth-token` stored project-wide through the explicit `project_secrets:` entry it already
requires (`project_secrets.go:63-71` unchanged). *Alternative:* refuse the default account on
shared jobs, requiring every shared installation to name its accounts — more explicit, and one more
thing to configure.

**D6. A named account's project-wide container.** *Recommend:* keep requiring the account's
non-empty `allow_data_to`, which is today's rule (`secrets.go:641-643`); no change to
`ProjectScopeByDefault`. *Alternative:* flip `ProjectScopeByDefault` for the
`claude-oauth-token-` prefix so `--project-wide` needs no `claude_accounts` entry — fewer steps for
the operator, and one more thing stored by default.

**D7. The retry re-seal.** *Recommend:* mirror the budget token's exactly, including its failure
mode (a re-seal that cannot overwrite refuses the launch rather than reusing the object).
*Alternative:* none worth pricing; the two objects share a key and a lifecycle.

**D8. SECURITY.md's wording.** *Recommend:* the four sentences of §6.1, in the owner's terms, plus
one line in the launcher paragraph for D2's grant. *The owner should read this one first*: it is
the only place where the feature's cost is visible to a user who never reads a design doc.

**D9. Sequencing and the job rename.** *Recommend:* merge the seal (`next-0.9` Slice A+B) first as
its own change, then land this feature; accept that populating the account axis renames the jobs of
installations that already have profiles, and make the rollout a documented, one-time step
(`RefuseProjectJobDeletes` will stop the first apply, by design). *Alternative:* keep the account
out of the job's *name* and carry it only in the seal's payload and the policy — no rename, at the
cost of the job name no longer being a function of what it serves, which is what the runner binds a
run's identity to (`runner/projectjob.go:76-101`).

**D10. How a project account is inspected.** *Recommend:* `fugaro project policy show` prints the
`accounts` block as it prints `profiles` (it is the same node, and it is a cloud read either way);
leave `fugaro accounts ls` local-only and unchanged. *Alternative:* extend `accounts ls` with a
project column, which makes one command answer two questions from two sources.

## 9. What this document does not decide

It does not design per-person ownership (that is phase 5.1, and §5 argues it is a different
feature); it does not design failover or usage-limit switching for project accounts (phase 5.3's
subject, and `fugaro accounts ls`'s); it does not design Codex support (Task C3's, in parallel);
it does not decide the Cloud Batch backend (excluded from 0.9.0) or the project-centric phases 4
and 6 (out of 0.9.0 by the owner's ruling); and it makes no cloud call, reads no secret and writes
no secret-shaped literal. The plan is
[../plans/2026-10-18-project-named-oauth-tokens.md](../plans/2026-10-18-project-named-oauth-tokens.md).

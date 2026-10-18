# Project-level named Claude OAuth tokens

Status: design, 2026-10-18, step 1 of the feature the owner ruled on 2026-10-18 (release 0.9.0): an
audit of what the project (shared-job) path does with a Claude OAuth credential today, and the
design that lifts the refusal. **This document decides nothing by itself**: §8 lists the owner
decisions with a recommendation each, and the plan
[../plans/2026-10-18-project-named-oauth-tokens.md](../plans/2026-10-18-project-named-oauth-tokens.md)
is written to be vetoed task by task. SECURITY-SENSITIVE — it designs a credential's carrier, and
the tasks that implement it are flagged for an independent review.

Every fact in §1 and §2 is cited to code read on `origin/integration-0.9.0` (head `13a5a48`, the
same commit as `origin/next-0.8.5`), which is what this branch is based on. The one exception is
the seal, which is not on that branch yet: it lives on `origin/next-0.9` and is being merged into
`integration-0.9.0` by PR #563 (head `39d5d44` when this was written), and every citation of it says
which of the two it was read on. **No cloud call was made**, no secret was read, and no
secret-shaped literal appears in this document. §7 lists what would need a live check, as
owner-run probes.

The owner's rulings of 2026-10-18 this design implements (authority, not open questions):
**multiple named** OAuth tokens per project, exactly as the individual level has them; a shared job
is keyed by `(profile, provider, account)`; a policy gates which repositories may use which token;
the token reaches the run through the budget-token seal; names carry the `claude-` prefix; Cloud
Batch is excluded from 0.9.0; the shared-job service account reaching every repository's `runs/`,
`cache/` and `locks/` is accepted; project-centric phases 4 and 6 are out of 0.9.0; and whether
phases 5.1/5.2 are prerequisites is for this audit to show (§5).

Built on: [named-oauth-tokens.md](named-oauth-tokens.md) (the individual level),
[project-centric.md](project-centric.md) §4.1 and §7 (the shared job and the phase-5 sketch),
[project-secrets.md](project-secrets.md) (project-scope containers),
[budget-token-channel.md](budget-token-channel.md) (the channel this design borrows).

---

## 1. What exists today, per path (verified in the code)

The individual level is built and works. The project level refuses in two independent places, and
the account axis is a hard-coded empty value rather than a rule. Read together:

| # | path | what happens today | where |
| --- | --- | --- | --- |
| 1 | `agent.auth: oauth` on a **per-repository** job, default account | works: besides the git credential and the workflow's own declared secrets, the job mounts exactly `claude-oauth-token` as `CLAUDE_CODE_OAUTH_TOKEN` — no Anthropic key and no provider key is mounted beside it | `internal/infra/spec.go:582-602` (`SecretMounts`, the `case "oauth"` branch at :585-586) |
| 2 | `agent.auth: oauth` on a per-repository job, **named** account | works: one job *variant* per (workflow, account), named by `gcp.WorkflowVariant(workflow, token)`, its service account per variant too; a run picks among the accounts allowed to its repository with `agent.oauth_token` or `--token` | `internal/infra/spec.go:609-614` (`WorkflowAccounts`), `:870-887` (the `for _, tok := range WorkflowAccounts(...)` loop), `internal/backend/gcp/names.go:349-357` (`WorkflowVariant`) |
| 3 | which repositories may use which named account, per-repository | `claude_accounts.<name>.allow_data_to` in the **local config only**; `fugaro.yaml`'s `agent.oauth_token` picks among the names the local config allow-lists; checked at launch and again by `fugaro doctor` | `internal/config/claude_accounts.go:63-73, 144-152`, `internal/cli/run.go:945-983` (`claudeAccountFor`, `claudeAccountAllowed`), `internal/cli/doctor.go:1296-1325` (`oauthTokenAllowedCheck`) |
| 4 | `claude_accounts:` reaching the published **shared config** (the runs-bucket file, not the policy node) | **stripped**: `Shared()` sets `s.ClaudeAccounts = nil`, and the shared config's reader, `ParseShared`, refuses the key (`sharedForbidden`) and the decoded value | `internal/localcfg/shared.go:44` (with the reasoning at :22-27), `internal/cli/sharedcfg.go:180, 214-218, 247-248` |
| 5 | storing a **named** account's token project-wide | **already allowed**: `secrets set claude-oauth-token-<name> --project-wide` is accepted when that account's `allow_data_to` is non-empty, and a project-scope Secret ID is published for it | `internal/cli/secrets.go:631-645` (`projectWideAccepted`, the named-account branch at :641-643), `internal/infra/tfvars.go:271-276` |
| 6 | storing the **default** account's token project-wide | allowed only through an explicit `project_secrets: [claude-oauth-token]` entry with a non-empty `allow_data_to`; the D2 sweep deliberately skips the whole `claude-oauth-token` namespace | `internal/config/project_secrets.go:63-71` (`ProjectScopeByDefault`), `internal/cli/secrets.go:615-637` (point 1 of `projectWideAccepted`) |
| 7 | a **profile** mounting any Claude OAuth token in `secrets:` | **refused**, by the policy's own profile rule, before any job is built | `internal/projectpolicy/profiles.go:276-293` inside `validProfiles` (`CheckProfiles` at :412), predicate `isClaudeOAuthSecret` at :328-331 |
| 8 | `fugaro init` deploying a job for such a profile | **refused**: `ProjectJobs` calls `projectpolicy.CheckProfiles` first and returns the policy's own error | `internal/infra/projectjobs.go:57-68` |
| 9 | a project job's **account axis** | hard-coded empty: `const account = ""`, so the job name and the job service account carry no account segment | `internal/infra/projectjobs.go:92-103`, `internal/backend/gcp/names.go:152-158` (`projectStem` adds the segment only when the account is non-empty), `:179-191` (`ProjectJobName`, `ProjectServiceAccountID`) |
| 10 | `fugaro run` with `agent.auth: oauth` **onto a shared project job** | **refused up front**, before the claim is taken and before any budget token is minted | `internal/cli/project_job.go:184-233` (`checkProjectJobOAuth`), called at `:105`, after the account check at `:99` |
| 11 | `fugaro run` selecting an account that is not the job's | refused by `checkProjectJobAccount`, which compares the run's selection against the account computed from the profile's `secrets:`; the default account and "no account" are the same empty value there (`projectJobAccount`) | `internal/cli/project_job.go:131-182` |
| 12 | the **runner**'s half of the job model | already account-aware: `jobIdentity` matches the run's job name against every profile's computed job name and returns `(profile, provider, account)`; `matchJobName` takes `spec.OAuthToken` | `internal/runner/projectjob.go:59-100` (`jobIdentity` and its doc), `:143` (`matchJobName`), `:175-180` (`projectJobCandidates`) |
| 13 | the **account of a project job**, computed on both sides | one function computes it from the profile's mounts: `config.ClaudeAccountOfSecrets(pr.Secrets)`, used by the launcher and by the runner's candidate list — and, because row 7 refuses every Claude token in `secrets:`, it returns `""` for every profile that can be published today | `internal/config/claude_accounts.go:87-122`, `internal/cli/project_job.go:98`, `internal/runner/projectjob.go:180` |
| 14 | the **agent's environment** under `auth: oauth` | an **allow-list**: `agent.BuildEnv` passes the base variables, `CLAUDE_CODE_OAUTH_TOKEN` and the workflow's declared secrets, and nothing else — an `ANTHROPIC_API_KEY` or a Vertex variable in the runner's own environment never reaches the agent of an OAuth run | `internal/agent/env.go:14-25` (`baseEnv`), `:103-137` (the `switch spec.Auth`) |
| 15 | the **budget-token seal** | **not on this branch.** On `origin/next-0.9` (and PR #563's head) the seal is built: `ObjectPrefix = "tokens"`, `ObjectKey(slug, run) = tokens/<slug>/<run>/budget-token`, `PutObject(ctx, b, slug, run, tok, key)` sealing under a per-launch key, `TakeObject(ctx, b, slug, run, key)` unsealing and refusing a sealed object presented without a key (`ErrSealedNoKey`) or a plaintext one (`ErrNotSealed`). `TakeObject` **never deletes**: the job account's grant on `tokens/` is read-only (D16), and consume-once rests on the key, not on the object | `origin/next-0.9:internal/budget/token/seal.go`, `origin/next-0.9:internal/budget/token/object.go:14-175` (the take's doc at :110-135); on this branch the API is the pre-seal one (`runs/<slug>/<run>/budget-token`, no key) — `internal/budget/token/object.go:15, 21, 76` |

Two conclusions from the table, and they are the reason this feature is small:

- **The job axis is built.** Everything that would have to know about a `(profile, provider,
  account)` job already does: the name and service-account derivations take the account
  (`names.go:179-191`), the deployer's `ProjectJob` carries an `Account` field
  (`projectjobs.go:32`), the launcher checks the run's selection against the job's account
  (`project_job.go:131-182`), and the runner computes and returns it (`runner/projectjob.go:74-100`).
  What is missing is the source of the account (one hard-coded `""`, and a derivation from
  `secrets:` that can only ever answer `""`) and two refusals.
- **The storage is built.** A named account's token can already be stored project-wide and already
  gets a project-scope Secret ID (`secrets.go:641-643`, `tfvars.go:271-276`). The gap is not
  storage; it is that no profile may name one and no shared job may carry one.

## 2. What is refused, exactly, and by which rule

Two refusals and one empty axis, in the order a launch meets them. They are independent — lifting
one does not lift the others — and each is stated in the code's own words:

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

What lifting refusal 1 alone would do, read from `projectJobSecrets` and `projectSecretEnv`
(`projectjobs.go:211-251`): the **default** account's `claude-oauth-token` is in
`config.ReservedSecrets` (`internal/config/config.go:481-486`), so `projectSecretEnv` maps it to
`CLAUDE_CODE_OAUTH_TOKEN` and it would be mounted as a static `secret_env`; a **named** account's
`claude-oauth-token-<name>` is neither reserved nor a provider's key, so `projectSecretEnv` returns
`ok == false` and `projectJobSecrets` refuses it ("neither a secret Fugaro reserves nor the key of a
configured model provider", `:221-224`). So lifting refusal 1 would give the default account a
static mount and refuse every named one — neither of which is the owner's ruling.

## 3. The carrier: the seal, as ruled — and what the static mount really costs

The owner ruled that the token reaches the run through the budget-token seal. This section does not
re-argue that ruling; it states, correctly, how it compares with the obvious alternative — let a
profile name `claude-oauth-token-<name>`, teach `projectSecretEnv` to map it, and let the job's
static `secret_env` carry it, as the per-repository job does — because §8 D1 and D2 depend on the
comparison being right.

1. **What the static mount exposes.** Every execution of the account's job holds the token in its
   container environment from start, so every run of every repository admitted to that job's
   profile holds it. With §4.1's rule that a profile's repositories must all be in the account's
   list, those are all repositories the policy allows to use the token anyway; the runner's
   admission check refuses any other repository before the agent starts, and `agent.BuildEnv`
   (row 14) hands the token to the agent only on an OAuth run. The token is **not** readable by
   launchers through the job: a `secret_env` entry is a `secret_key_ref`, and `JobFullEnv` returns
   only plain-value entries — "the API answers those with Value == "" and ValueSource set"
   (`internal/backend/gcp/run.go:361-373`). Reading the value needs `secretAccessor`, which only
   the job and build accounts hold (SECURITY.md's launcher paragraph).
2. **What the seal buys over it.** Per-execution delivery: the token is present only in an
   execution whose launch sealed it, under a key handed to that execution alone. Against a run of
   *another* shared job — every project job account reads the whole of `tokens/` once PR #563 lands
   (`modules/project/sa.tf`'s `job_tokens`, commit `cda64c5a`) — the object is ciphertext. Against
   a peer run of the *same* account's job it buys little: that run is on the account's list and
   holds the same token from its own object.
3. **What the seal costs.** The launcher has to read the token's value in order to seal it, and
   the launcher is **every human member in `launchers`** (`deploy/terraform/gcp/modules/installation/iam.tf:66`,
   `for_each = toset(var.launchers)`; `variables.tf:139-141`, "Members who launch and watch runs,
   such as user:someone@example.com"). A grant that lets the launcher's CLI read the secret lets
   that person read it at will, from their own machine, for as long as the grant stands — whatever
   repository they were onboarded to and whatever the account's allow-list says. This is a
   strictly wider standing audience for the long-lived credential than the static mount has, and it
   is the design's most important cost (§4.3, §8 D2).
4. **One path, not two.** The per-repository job keeps its static mount (`spec.go:585-586`), so
   under the ruling the project level is the one place an OAuth token travels by the seal. This is
   a second delivery path to maintain, and the owner's ruling chose it.

## 4. The design

### 4.1 The policy gains an `accounts` block, and a profile may name accounts

Today the local config's `claude_accounts:` is the anchor for the per-repository choice, and
`Shared()` strips it from the shared config — with the reasoning written out at
`internal/localcfg/shared.go:22-27`: T1's operator-only trust anchor "must not round-trip through
the runs bucket either, which any launcher with `objectAdmin` on it can write". That reasoning is
about the **runs bucket**, and it does not apply to the **policy node**: `config/policy/shared` in
RTDB has `".write": false` in the rules (`internal/budget/rules/rules.json.tmpl:15-17`), so only
the operator's admin path — `fugaro project policy publish` — writes it, and the runner reads it
and fails closed on it. A shared job is already a function of the published policy — `ProjectJobs`
builds from the local config's profiles and `fugaro project policy publish` holds them to the same
rules — so the accounts a shared job serves have to be a policy fact.

The policy therefore gains (shape recommended; the spellings are §8 D3 and D4):

```yaml
# config/policy/shared, beside profiles:
accounts:
  acme-team:
    allow_repos: ["github/acme/app", "github/acme/tools"]   # which repositories may use this token
  ops:
    allow_repos: ["github/acme/infra"]
profiles:
  standard:
    provider: github
    claude_accounts: [acme-team, ops]   # NEW: one job per (profile, provider, account)
    allow_repos: ["github/acme/app", "github/acme/tools", "github/acme/infra"]
```

- **Several accounts per profile, one job each.** The owner's rulings are "multiple named tokens
  per project, exactly like the individual level" and "a shared job is keyed by (profile, provider,
  account)". The individual level deploys one variant per (workflow, account) and lets each run pick
  among its allowed accounts (row 2). The project level mirrors it: a profile lists the accounts it
  serves, the deployer builds one job per (profile, provider, account) through the existing
  `gcp.ProjectJobName(profile, provider, account)`, and the launcher picks the job whose account is
  the run's own selection (`agent.oauth_token` or `--token`), refusing a selection the profile does
  not list. A single `claude_account` field would make a repository that needs two accounts need
  two profiles, which is not "exactly like the individual level" (§8 D4).
- `accounts.<name>.allow_repos` is **derived at publish time** from the local config's
  `claude_accounts.<name>.allow_data_to` — one anchor, one edit, no second list to keep in sync.
  The two vocabularies differ and the translation is explicit: `allow_data_to` holds `owner/name`
  slugs (`claude_accounts.go:173-176`), while a policy `allow_repos` entry is a provider-qualified
  pattern that may carry `*` wildcards (`internal/projectpolicy/match.go:105-120`,
  `ValidPattern`). Publish qualifies each slug with the provider of the profile(s) that name the
  account and never writes a wildcard into an account's list.
- **The subset rule.** Publish **fails closed** on a profile that names an account whose
  `allow_repos` does not cover every repository the profile admits, naming both lists and the
  repository that is in one and not the other. Two inputs make "cover" non-trivial and the design
  states both: a profile's `allow_repos` may be absent, which means "the project's `allow_repos`
  applies" (`internal/config/config.go:525-533`), and either list may hold wildcards. Recommended:
  a profile that names an account must write its own `allow_repos` with exact entries only, so the
  subset is decidable; the alternative is §8 D3's.
- `profiles.<p>.claude_accounts` is a **new field**, not a new entry in `secrets:`. Keeping it out
  of `secrets:` is what lets refusal 1 stand unchanged: a Claude token is still never an env mount
  on a shared job, and the existing rule (`profiles.go:292`) stays true and in force for
  `secrets:`.
- `config.JobProfile` (`internal/config/config.go:519`) gains the field — the policy's profile is
  the same type (`projectpolicy.Profile` embeds it, `projectjobs.go:120-129`), so the field reaches
  publish, the deployer and the runner from one declaration — and the local config validates it
  against `lc.ClaudeAccounts` at load.
- **The default account** cannot be defined in `claude_accounts` ("default" is reserved there,
  `claude_accounts.go:166-167`), so it has no `allow_data_to` to derive from. Under §8 D5's
  recommendation a profile lists it as `default`, and its policy list is derived from
  `project_secrets.claude-oauth-token.allow_data_to` — the same entry that already decides whether
  its project-wide container exists (row 6).

### 4.2 The secret-id scheme does not change

The token's logical name is exactly what `config.ClaudeAccountSecret(name)` already returns
(`internal/config/claude_accounts.go:80-85`): `claude-oauth-token` for the default account,
`claude-oauth-token-<name>` otherwise — inside `SecretNameRE`
(`internal/config/validate.go:27`), and the whole prefix is reserved for the purpose
(`internal/config/validate.go:215-226` for a workflow's secrets, `internal/cli/secrets.go:433-443`
for `fugaro secrets set`). The container is `gcp.ProjectSecretID(fugaroProject, logical)`
(`internal/backend/gcp/names.go:207-209`), which is domain-separated with `"project\x00"` and already
used for every project-scope secret. **No new helper, no new prefix, no rename.** The `claude-`
prefix the owner asked for is already there, and it is what leaves room for the parallel
`codex-oauth-token[-<name>]` scheme (see §7 U6).

Storage eligibility does not change either: a named account is accepted project-wide when its
`allow_data_to` is non-empty (`secrets.go:641-643`), and the default account needs the explicit
`project_secrets` entry it already needs (`project_secrets.go:63-71`). §8 D5 and D6 put the two
remaining choices about that to the owner.

### 4.3 The token reaches the run as a second sealed object

The carrier, per the owner's ruling:

- The launcher reads the account's token from its project-scope container (a **new** capability
  for the CLI: nothing under `internal/` reads a Secret Manager value today — there is no
  `AccessSecretVersion` call outside tests) and seals it under the **same per-launch key** the
  budget token uses, into a second object with a fixed name beside it —
  `tokens/<slug>/<run>/claude-oauth-token` — written create-if-absent, with the budget token's retry
  re-seal mirrored and the object deleted by the launcher when the launch fails, as
  `dropBudgetToken` does for the budget token (`origin/next-0.9:internal/cli/run.go:1604-1605, 1636-1637`).
- **The key exists only when a budget token was minted.** On `next-0.9`,
  `sealKey, err := env.mintBudgetToken(...)` and `minted := sealKey != nil`, and the marker is set
  only `if minted` (`origin/next-0.9:internal/cli/run.go:1587-1588, 1612-1623`; PR #563's head `:1600-1642`). A
  shared job requires a published policy, so a launch onto one normally mints; but the design does
  not assume it: an OAuth launch onto a shared job whose launch minted no key is **refused** (§4.4),
  never sealed under some other key and never sent in plaintext.
- The runner reads it once, unseals it, registers the plaintext with its redactor
  (`internal/runner/runner.go:609-616`, `addSecret`), and puts it where `agent.BuildEnv`'s oauth
  branch reads `CLAUDE_CODE_OAUTH_TOKEN` from (row 14). Like the budget token's object, it is
  **not deleted by the run** — the job account's `tokens/` grant is read-only — so it stays until the
  bucket's lifecycle rule removes it.
- **The lifecycle rule must be widened.** The rule that reaps token objects matches
  `matches_prefix = ["runs/", "tokens/"]` and `matches_suffix = ["/budget-token"]`, and exists only
  when the budget is enabled (`deploy/terraform/gcp/modules/installation/bucket.tf:46-62` on
  `next-0.9`). An object named `/claude-oauth-token` would never be reaped: a long-lived credential's
  ciphertext would accumulate in the bucket for ever, under keys whose audience (§7 U1) is not yet
  known. The rule gains the second suffix, and the installation tftest that pins "exactly that
  suffix" changes with it.
- The key travels by the carrier the seal built for it, and it is **not** a plain `LaunchSpec.Env`
  entry: `next-0.9`'s Slice B adds a second field, `LaunchSpec.SecretEnv`
  (`origin/next-0.9:internal/backend/backend.go:80-90`), and writes the key as
  `lspec.SecretEnv = []string{sealMarker(sealKey)}` (`origin/next-0.9:internal/cli/run.go:1623`).
  The name is still `runner.BudgetTokenEnv` (`internal/runner/budget_session.go:50`); what changed
  is which list it is in. On this branch there is no `SecretEnv` at all — the marker is a plain
  `LaunchSpec.Env` entry carrying one bit, `internal/cli/run.go:1626`'s
  `lspec.Env = []string{runner.BudgetTokenEnv + "=1"}` — so the two must not be confused.

  That second field is load-bearing rather than cosmetic, and the implementation must use it.
  `internal/backend/launchenv.go`'s `LaunchEnv` (on `next-0.9`) parses `Env` and then `SecretEnv`,
  marks a `SecretEnv` entry `Secret`, and requires a backend whose only place for such an entry is
  readable by a wider audience than the execution's override — Batch's job variables — to **refuse
  the launch** rather than carry it. This design adds no new variable to the carrier: the one key
  opens both objects. It does add a permission (the launcher's read, below) and a lifecycle suffix
  (above), so [budget-token-channel.md](budget-token-channel.md) §7.1's "no new permission" is true
  of the seal, not of this feature.
- A second object, **not** a second field in the existing envelope. The budget token's envelope is
  versioned, reviewed and mutation-tested (`seal.go`, `seal_aad_test.go`, `seal_test.go` on
  `next-0.9`). Widening its payload would couple the credential's failure modes to the budget
  channel's; a second object reuses `Seal`/`Unseal` and the AAD rule (the object's own name) with
  no change to either.
- **How the runner knows to expect it.** The budget marker says a *budget* token was sealed; it
  says nothing about an OAuth object. The runner expects the OAuth object exactly when its job
  identity (row 12) is a project job whose profile lists the run's account and the run's
  `agent.auth` is `oauth` — facts it already holds from the policy and the task — and refuses the
  run when the object is then absent.

What this costs, stated plainly: **every launcher has to be able to read the account's secret
value.** Today none can — SECURITY.md's launcher paragraph says reading a value needs
`secretAccessor`, "which only the job and build accounts hold" — so the design adds
`roles/secretmanager.secretAccessor` on `gcp.ProjectSecretID(project, ClaudeAccountSecret(name))`
for **each launcher member, for each account a published profile names** (a `setproduct` of
`var.launchers` and the served accounts, or a narrower list under §8 D2). This is a real widening
and it is the design's most important single decision (§8 D1 and D2). What bounds it and what does
not:

- it is bounded to the accounts the installation actually serves, not to every account the
  operator's local config defines;
- it is a member resource, never a binding or a policy, which is the rule every grant in this
  repository already follows (`deploy/terraform/gcp/modules/installation/iam.tf` says so at the
  top of the file, and [budget-token-channel.md](budget-token-channel.md) §3 constraint 2 quotes
  it);
- **it is not bounded by the allow-list**: a launcher onboarded only to a repository the account
  does not cover can still `gcloud secrets versions access` the token directly. The account's
  `allow_repos` gates which *runs* get the token, not which *people* can read it;
- **it is not bounded by the seal**: a principal holding both halves recovers the token, and with
  this grant every launcher holds the first half outright. The seal's own doc names this
  ([budget-token-channel.md](budget-token-channel.md) §7.1: "a capability, not a boundary"). The
  seal buys protection against a run of *another* job reading `tokens/` (§3 point 2), not against
  a launcher.

§8 D2 records the narrowings the owner may prefer, including the one that departs from the ruling's
wording: the **job's own** service account reads the secret at run start, and the seal carries only
a per-run authorization. It is written up as a real alternative, and the plan is cut so the choice
is made before any code is written.

### 4.4 Failure naming, and no silent fallback

Every path fails closed, names the thing the operator must fix, and never degrades to another
credential. The names, in the order a run meets them:

| when | condition | what is said |
| --- | --- | --- |
| publish / `fugaro init` | a profile lists an account the policy's `accounts` block does not define (or the local config's `claude_accounts` does not) | name `profiles.<p>.claude_accounts`, the account, and where it is defined |
| publish / `fugaro init` | the profile admits a repository the account's `allow_repos` does not cover, or the profile's own list is absent or holds a wildcard (D3) | name both lists and the repository that is in one and not the other |
| publish / `fugaro init` | a profile lists accounts **and** mounts an Anthropic API key (D4) | refuse, naming which of the two to drop |
| launch | `agent.auth: oauth` on a shared job whose profile lists no account, or not the run's selected one | refuse, naming the profile, `profiles.<p>.claude_accounts`, and `claude_accounts` in the local config |
| launch | the repository is not in the account's `allow_repos` | refuse, naming the repository, the account, `accounts.<name>.allow_repos` and the profile's `allow_repos` |
| launch | the account's token has no project-wide container | refuse, naming the logical secret, `project_secrets:` / `claude_accounts.<name>.allow_data_to` and `fugaro secrets set <logical> --project-wide` |
| launch | the launch minted no seal key | refuse: there is no key to seal the token under, and it is never written in plaintext |
| launch | the launcher cannot read the secret (no grant: D2) | refuse, naming the account and that the launcher's read is an operator grant, before the claim is held longer than the read |
| runner | the run's profile lists its account, its auth is `oauth`, and the object is absent | refuse to start; never run without the credential |
| runner | the object is sealed and no key was presented, the key is wrong, or the AAD name does not match | refuse — `ErrSealedNoKey`'s own words on `next-0.9` are the model: "it is not read as a token: a plaintext fallback is the exposure the seal exists to close" |
| runner | the token is present but the agent cannot use it | the run fails; it does **not** fall back |

The no-fallback property has a mechanism, and it is already built: `agent.BuildEnv` builds the
agent's environment from an allow-list keyed on the run's auth (row 14), so an OAuth run's agent
gets `CLAUDE_CODE_OAUTH_TOKEN` and never `ANTHROPIC_API_KEY` or a Vertex variable, whatever the
job's own environment holds. The question "which credential does Claude Code prefer when both are
set" (§7 U2) therefore does not arise for the agent. The rule that a profile listing accounts may
not mount an Anthropic API key (D4) is kept for a different reason: **least privilege** — an account
job's container would hold a key no run on it can use — and to keep "which credential does this job
serve" answerable from the profile alone.

### 4.5 The job axis, populated

`ProjectJobs` (`internal/infra/projectjobs.go:92-103`) stops hard-coding `const account = ""` and
deploys, for each profile, its existing no-account job **plus** one job per listed account, computed
through one function shared with the launcher and the runner (`config.ClaudeAccountOfSecrets`'s
replacement, reading the profile's new field) so that the three sides cannot drift. The no-account
job is the one the profile has today, unchanged; the default account, whose axis value is `""`
(`project_job.go:171-182`, `projectJobAccount`), rides that job too.

**No existing job is renamed or deleted.** `projectStem` adds the account segment only for a
non-empty account (`names.go:152-158`) and `projectKey` hashes the account as its own part
(`:165-167`), so the name of every no-account job is byte-for-byte what it is today; adding an
account to a profile only *adds* a job and its service account, and `RefuseProjectJobDeletes`
(`projectjobs.go:258-282`) does not fire. It would fire if a profile's no-account job were dropped
when the profile gains accounts — §8 D9 records that alternative and recommends against it.

Under the recommended carrier an account job's service account needs no Secret Manager grant at
all (the token arrives sealed); it gains `secretAccessor` on its account's secret **only** under
D2's job-reads alternative. Either way the axis is what the runner binds the run's identity to
(`internal/runner/projectjob.go:74-100`).

## 5. Are phases 5.1 and 5.2 prerequisites? (the owner asked; the evidence says no)

The evidence is one-sided:

- **5.2's shape is already built.** Task 5.2 is "one job per (profile, shared account)". The job
  name and service account already take the account (`names.go:179-191`), `ProjectJob` already
  carries an `Account` field (`projectjobs.go:32`), the launcher already refuses a run whose
  selection is not the job's account (`project_job.go:131-182`), and the runner already returns the
  account it bound (`runner/projectjob.go:74-100`). The remaining work is the account's source and
  the two refusals of §2 — which is *this* feature, not a prerequisite for it. Treating 5.2 as a
  prerequisite would mean building the axis a second time.
- **5.1 is a different feature.** Task 5.1 is *person-owned* secrets: one Secret Manager secret per
  **(person, account)**, an opaque ID (`gcp.PersonSecretID`), `fugaro accounts add` and
  `fugaro accounts share`, a reference record in the run-readable policy node with no identity in
  it, and failover moved into `fugaro run`
  ([project-centric.md](project-centric.md) §7, plan Tasks 5.1-5.3). The owner's ruling of
  2026-10-18 describes the opposite ownership model: **project-level named tokens**, owned by the
  installation, exactly like the individual level's
  ([named-oauth-tokens.md](named-oauth-tokens.md) §3). Building 5.1 first would build a
  per-person credential store and then not use it.
- **What *is* a prerequisite is the seal.** The carrier of §4.3 is `next-0.9`'s Slice A and B: 24
  commits whose base, `13a5a48`, is this branch's base, being merged into `integration-0.9.0` by
  PR #563 — which also adds what a *shared* job needs to read the seal at all (the project job
  account's read-only grant on `tokens/`, commit `cda64c5a`; `next-0.9` alone has no
  `modules/project`). Nothing in the plan can land before #563.
- **What is explicitly out of scope** by the owner's rulings of 2026-10-18: the Cloud Batch backend
  (excluded from 0.9.0) and the project-centric phases 4 and 6. Nothing here depends on either.

## 6. Threat model

| adversary | what stops it | what does not |
| --- | --- | --- |
| a run of **another** shared job (another profile, or another account's job): with PR #563 every project job account reads the whole of `tokens/` | the per-launch key: the object is ciphertext under a key delivered to one execution, and a sideways copy unseals under nothing | it can read the ciphertext. It cannot delete it: the job grant on `tokens/` is read-only (D16) |
| a **peer run** of the same account's job | nothing that matters: it is on the account's list and receives the same token through its own object | it cannot delete another run's object (read-only grant); this is the shared-job reach the owner accepted for `runs/`, `cache/` and `locks/` |
| a **launcher** (every member of `launchers`) | nothing, by construction: under the recommended carrier and D2(a) it holds `secretAccessor` on the account's secret and can read the token at will, whatever repositories it was onboarded to | project-centric.md's D10 (cited by the channel doc's §3 constraint 5: the launcher stays trusted) is unchanged; the widening is priced in §3 and §4.3 |
| a **project log viewer** holding an override view | the key is not in the object, and the object's bytes are ciphertext | if such a principal holds *both* an execution's override and the object, it recovers the token — the channel doc's §7.1 prices this case in the same words, and §7 U1 is the probe |
| the **agent process itself** | nothing | this is the residual §6.1 states, and it is the honest cost of an OAuth credential |
| a repository admitted to the account's list but hostile | the account's blast radius is not per repository | it gets a credential that works for **every** repository in the list, for as long as the token is valid |
| a repository **not** in the list | the launch gate, the runner's own check, and the absence of the object (nothing sealed one) | a launcher can still launch it (the launcher is trusted), and the runner's check is what refuses it |

### 6.1 The residual SECURITY.md has to carry

An OAuth token is a bearer credential with no per-repository scoping, no gateway in front of it and
no revocation finer than the account's. So SECURITY.md has to say, in the owner's own terms:

- **A hostile run can use the token during its own run.** The agent's process holds
  `CLAUDE_CODE_OAUTH_TOKEN` in its environment for the duration of the run; it can read it, copy it
  into an artifact, a PR body or a log line (the redactor reduces the last of these, and is not a
  boundary), and use it after the run ends, until the owner revokes it. The seal protects the token
  **in the bucket**, not in the process.
- **The account is the blast radius.** `allow_data_to` and `allow_repos` gate *which runs* get a
  token; they do not scope what the token *can* do. Every repository in an account's list shares
  one credential, and a compromise anywhere in that list is a compromise of all of it.
- **Nothing prices an OAuth run the way the gateway prices a provider-key run.**
  [named-oauth-tokens.md](named-oauth-tokens.md) §1 records that accounting has no per-account
  dimension; the budget channel attributes spend to the *run*. Usage limits are the account's own,
  which is what `fugaro accounts ls` and the usage-limit switch exist for.
- **Launchers can read a served account's token** (under the recommended carrier and D2), at will
  and independent of the allow-list — which is more than any launcher can do today, and is the
  price of the seal.

## 7. What is verified, and what is not

**Verified in the code** (read, cited above): everything in §1, §2 and §4.2; the account axis's
existence on all three sides; the project-wide storage of a named account; the two refusals and
where they live; `agent.BuildEnv`'s allow-list; `JobFullEnv`'s plain-value-only read; the launcher
members' grant shape; the seal's API, its AAD rule, its read-only take and its lifecycle rule on
`origin/next-0.9`; PR #563's `tokens/` grant for project jobs.

**Not verified — owner-run, live, sandbox only** (no probe was run for this document):

- **U1. Whether an execution's own override can be read back through the Cloud Run API.** This is
  the channel doc's U2 / Check 52 step 3, still unrun, and it decides how wide the seal's audience
  is. Everything in §6 that depends on it is marked as a hypothesis there and here.
- **U2. Which credential Claude Code prefers when both `CLAUDE_CODE_OAUTH_TOKEN` and
  `ANTHROPIC_API_KEY` are set.** Not needed: `agent.BuildEnv` never sets both for an agent (§4.4).
  Recorded only so nobody re-opens it as a blocker.
- **U3. Whether one account's OAuth token can serve several concurrent runs.** The design puts N
  repositories on one account's token. If the account refuses concurrent use, the feature is a
  serialization bottleneck and accounts have to be split — a sizing question, not a security one,
  but it decides whether the feature is useful.
- **U4. Whether the seal's per-launch key survives a Cloud Run retry** exactly as the budget token
  assumes; the retry re-seal is built for the budget token, and this design mirrors it rather than
  re-deriving it.
- **U5. Whether the launchers' `roles/secretmanager.secretAccessor` members can be added without
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
[budget-token-channel.md](budget-token-channel.md)'s D14-D19, and this document's **D10 is not
[project-centric.md](project-centric.md)'s D10** ("launcher environment overrides: accepted and
documented", the decision the channel doc's §3 constraint 5 cites as "the launcher stays trusted").
Every reference here to that one says *project-centric.md's D10*. The plan restates these ten as
its own Decisions section, so the same reading applies there.

**D1. The carrier.** *Per the ruling, recommend:* a second sealed object,
`tokens/<slug>/<run>/claude-oauth-token`, written by the launcher under the same per-launch key,
read once by the runner, unsealed into `CLAUDE_CODE_OAUTH_TOKEN`, with the budget token's
create-if-absent, retry re-seal and failed-launch delete mirrored, and the bucket's lifecycle rule
widened to reap it. *Alternatives:* (a) the job's service account reads the secret at run start and
the seal carries only a per-run authorization — no launcher read at all, a live Secret Manager read
on the run's hot path, and a departure from the ruling's wording that the owner would have to make;
(b) the static `secret_env` mount — the per-repository path's own carrier, readable by no launcher
(§3 point 1), held by every execution of the account's job.

**D2. Who may read the token so the launcher can seal it.** The ruling's carrier needs it; §3
point 3 and §4.3 price it. *Recommend (b):* (a) every member of `launchers`, on each account a
published profile names — the simplest, and every launcher can then read every served token at
will; (b) **a per-account launcher list in the local config** (`claude_accounts.<name>.launchers`,
default empty, operator-only like the rest of the block), so only the people the operator names can
launch OAuth runs on that account and read its token, and an OAuth launch by anyone else is refused
at the read with a message naming the list; (c) D1(a), which needs no launcher read. *Not
recommended:* granting it on every account in the local config.

**D3. The policy's gate.** *Recommend:* `accounts.<name>.allow_repos` in the policy, derived at
publish from `claude_accounts.<name>.allow_data_to` (provider-qualified with the profile's
provider, never a wildcard), with publish refusing a profile whose admitted repositories the
account's list does not cover; a profile that lists accounts must write its own `allow_repos`, with
exact entries only, so the subset is decidable. *Alternative:* no subset rule — the gate is the
intersection (profile admits the repository **and** the account lists it), checked at launch and by
the runner; wildcards stay allowed, and a repository admitted to the profile but not the account is
refused at launch instead of at publish.

**D4. The profile field.** *Recommend:* `profiles.<p>.claude_accounts: [<name>, ...]`, a list, one
job per (profile, provider, account), the run choosing among them with `agent.oauth_token` /
`--token` as on the individual level; the `secrets:` refusal stays exactly as it is; a profile that
lists accounts may not also mount an Anthropic API key (least privilege, §4.4). *Alternatives:* a
single `claude_account` field (one account per profile — a repository needing two accounts needs
two profiles, which departs from "exactly like the individual level"); naming the account in
`secrets:` after all, which needs the mount suppressed for that one name — a special case in the one
function that resolves mounts.

**D5. The default account on a shared job.** *Recommend:* allowed, listed as `default`, riding the
profile's no-account job; its token is stored through the explicit `project_secrets:
[claude-oauth-token]` entry it already requires (`project_secrets.go:63-71` unchanged), and that
entry's `allow_data_to` is what publish derives `accounts.default.allow_repos` from.
*Alternative:* refuse the default account on shared jobs, requiring every shared installation to
name its accounts — more explicit, and one more thing to configure.

**D6. A named account's project-wide container.** *Recommend:* keep requiring the account's
non-empty `allow_data_to`, which is today's rule (`secrets.go:641-643`); no change to
`ProjectScopeByDefault`. *Alternative:* flip `ProjectScopeByDefault` for the
`claude-oauth-token-` prefix so `--project-wide` needs no `claude_accounts` entry — fewer steps for
the operator, and one more thing stored by default.

**D7. The retry re-seal and the object's lifetime.** *Recommend:* mirror the budget token's
exactly, including its failure mode (a re-seal that cannot overwrite refuses the launch rather than
reusing the object), and add `/claude-oauth-token` to the lifecycle rule's `matches_suffix` so the
object goes after a day like the budget token's. *Alternative:* a shorter age for the OAuth object
alone — a fourth lifecycle rule, and a tftest that no longer pins "exactly three".

**D8. SECURITY.md's wording.** *Recommend:* the four sentences of §6.1, in the owner's terms, plus
one line in the launcher paragraph for D2's grant (replacing "which only the job and build accounts
hold" for the served accounts). *The owner should read this one first*: it is the only place where
the feature's cost is visible to a user who never reads a design doc.

**D9. Sequencing and the job set.** *Recommend:* PR #563 (the seal, merged into
`integration-0.9.0`) lands first as its own change; then this feature. A profile keeps its
no-account job and gains one job per listed account, so no existing job is renamed or deleted and
`RefuseProjectJobDeletes` never fires on the rollout. *Alternative:* drop a profile's no-account job
once it lists accounts — a smaller job set, a one-time `RefuseProjectJobDeletes` stop on every
installation that adds an account to an existing profile, and the rollout becomes a documented
operator step (retiring a project job is still unsupported).

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

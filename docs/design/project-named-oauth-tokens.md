# Project-level named Claude OAuth tokens

Status: **built, release 0.9.0 (released; the released CLI is 0.15.0)**, 2026-10-19. Was: design,
2026-10-18, **RULED** (release 0.9.0). The three implementation tasks are merged — the policy's
`accounts` block and the profile field (policy version 3), the account jobs and their static mount,
and the two gates with doctor and SECURITY.md — and the operator guide is
[../project-claude-accounts.md](../project-claude-accounts.md). No live cloud check was run for
any of it (§7's U3 and U5 are still the owner's). Step 1 of the feature the owner ruled on
2026-10-18: an audit of what the project (shared-job) path does with a Claude OAuth credential
today, and the design that lifts the refusal. The owner answered every decision in §8 on
2026-10-18; none is open. The plan is
[../plans/2026-10-18-project-named-oauth-tokens.md](../plans/2026-10-18-project-named-oauth-tokens.md).
SECURITY-SENSITIVE — it chooses a credential's carrier and adds IAM grants, and the tasks that
implement those are flagged for an independent review.

**The carrier is a stopgap.** The token reaches a shared job the way it already reaches a
per-repository job: the platform mounts the account's secret into the job's environment as a static
`secret_env` entry, and only that account's job service account can read that one secret. This is
the simplest carrier and the owner accepted its lesser security (§3) *because it is temporary*: it
will be replaced by the planned **security service** — the credentials service of
[../credentials-service/](../credentials-service/README.md) (PR #512), a cascade proxy that is the
only reader of Secret Manager and gives jobs short-lived keys. §4.6 states what the swap changes
and what it leaves alone; the policy, the accounts, the profile field and the job set do not change
when the carrier does.

Every fact in §1 and §2 is cited to code read on `origin/integration-0.9.0` (head `13a5a48`), which
is what this branch is based on. **No cloud call was made**, no secret was read, and no
secret-shaped literal appears in this document. §7 lists what would need a live check, as owner-run
probes.

The owner's rulings of 2026-10-18 this design implements (authority, not open questions):
**multiple named** OAuth tokens per project, exactly as the individual level has them; a shared job
is keyed by `(profile, provider, account)`; a policy gates which repositories may use which token;
names carry the `claude-` prefix; the carrier is the static mount as a stopgap pending the security
service (§3, §8 D1/D2); Cloud Batch is excluded from 0.9.0; the shared-job service account reaching
every repository's `runs/`, `cache/` and `locks/` is accepted; project-centric phases 4 and 6 are
out of 0.9.0; and whether phases 5.1/5.2 are prerequisites is for this audit to show (§5). The
budget-token seal is **not** used by this feature.

Built on: [named-oauth-tokens.md](named-oauth-tokens.md) (the individual level),
[project-centric.md](project-centric.md) §4.1 and §7 (the shared job and the phase-5 sketch),
[project-secrets.md](project-secrets.md) (project-scope containers),
[../credentials-service/spec.md](../credentials-service/spec.md) (the security service that
replaces the carrier).

---

## 1. What exists today, per path (verified in the code)

The individual level is built and works. The project level refuses in two independent places, and
the account axis is a hard-coded empty value rather than a rule. Read together:

| # | path | what happens today | where |
| --- | --- | --- | --- |
| 1 | `agent.auth: oauth` on a **per-repository** job, default account | works: besides the git credential and the workflow's own declared secrets, the job mounts exactly `claude-oauth-token` as `CLAUDE_CODE_OAUTH_TOKEN` — no Anthropic key and no provider key is mounted beside it | `internal/infra/spec.go:582-602` (`SecretMounts`, the `case "oauth"` branch at :585-586) |
| 2 | `agent.auth: oauth` on a per-repository job, **named** account | works: one job *variant* per (workflow, account); a run picks among the accounts allowed to its repository with `agent.oauth_token` or `--token` | `internal/infra/spec.go:604-614` (`WorkflowAccounts`) |
| 3 | which repositories may use which named account, per-repository | `claude_accounts.<name>.allow_data_to` in the **local config only**; checked at launch and again by `fugaro doctor` | `internal/config/claude_accounts.go:63-73`, `internal/cli/run.go:945` (`claudeAccountFor`), `internal/cli/doctor.go:1281-1325` (`oauthTokenAllowedCheck`) |
| 4 | `claude_accounts:` reaching the published **shared config** (the runs-bucket file, not the policy node) | **stripped**: `Shared()` sets `s.ClaudeAccounts = nil`, with the reasoning at :22-27 | `internal/localcfg/shared.go:22-27, 44` |
| 5 | storing a **named** account's token project-wide | **already allowed** when that account's `allow_data_to` is non-empty, and the installation already creates a project-scope container for it | `internal/cli/secrets.go:631-645` (`projectWideAccepted`, the named-account branch at :641-643), `internal/infra/tfvars.go:259-278` (`projectSecretContainers`, the account loop at :271-276) |
| 6 | storing the **default** account's token project-wide | allowed only through an explicit `project_secrets: [claude-oauth-token]` entry with a non-empty `allow_data_to`; the D2 sweep deliberately skips the whole `claude-oauth-token` namespace | `internal/config/project_secrets.go:63-71` (`ProjectScopeByDefault`), `internal/cli/secrets.go:615-637` |
| 7 | a **profile** mounting any Claude OAuth token in `secrets:` | **refused**, by the policy's own profile rule, before any job is built | `internal/projectpolicy/profiles.go:291-293` inside `validProfiles` (`CheckProfiles` at :412), predicate `isClaudeOAuthSecret` at :328-331 |
| 8 | `fugaro init` deploying a job for such a profile | **refused**: `ProjectJobs` calls `projectpolicy.CheckProfiles` first | `internal/infra/projectjobs.go:57-68` |
| 9 | a project job's **account axis** | hard-coded empty: `const account = ""` | `internal/infra/projectjobs.go:92-103`, `internal/backend/gcp/names.go:152-158` (`projectStem` adds the segment only when the account is non-empty), `:179-191` (`ProjectJobName`, `ProjectServiceAccountID`) |
| 10 | `fugaro run` with `agent.auth: oauth` **onto a shared project job** | **refused up front**, before the claim and before any budget token is minted | `internal/cli/project_job.go:184-233` (`checkProjectJobOAuth`), called at `:105`, after the account check at `:99` |
| 11 | `fugaro run` selecting an account that is not the job's | refused by `checkProjectJobAccount`; the default account and "no account" are the same empty value there (`projectJobAccount`) | `internal/cli/project_job.go:131-182` |
| 12 | the **runner**'s half of the job model | already account-aware: `jobIdentity` returns `(profile, provider, account)`; `matchJobName` takes the run's token selection | `internal/runner/projectjob.go:59-100`, `:143` (`matchJobName`), `:175-180` (`projectJobCandidates`) |
| 13 | the **account of a project job**, computed on both sides | `config.ClaudeAccountOfSecrets(pr.Secrets)`, used by the launcher and by the runner's candidate list — which returns `""` for every profile that can be published today, because row 7 refuses every Claude token in `secrets:` | `internal/config/claude_accounts.go:87-122`, `internal/cli/project_job.go:98`, `internal/runner/projectjob.go:180` |
| 14 | the **agent's environment** under `auth: oauth` | an **allow-list**: `agent.BuildEnv` passes `CLAUDE_CODE_OAUTH_TOKEN` and the declared secrets on an OAuth run, and an `ANTHROPIC_API_KEY` or Vertex variable in the runner's own environment never reaches its agent | `internal/agent/env.go:103-137` (the `switch spec.Auth`) |
| 15 | a project job's **secret mounts and their grants** | each entry of the job's `secret_env` is a `value_source.secret_key_ref`, and the job's own service account gets `roles/secretmanager.secretAccessor` on **exactly** the secrets its `secret_env` names, at the secret level, one member per secret, none by prefix | `deploy/terraform/gcp/modules/project/job.tf:61-74`, `deploy/terraform/gcp/modules/project/sa.tf:48-63` (`google_secret_manager_secret_iam_member.job`, `for_each = toset(values(var.job.secret_env))`) |
| 16 | a launcher reading a mounted secret | **cannot**: `JobFullEnv` returns only plain-value entries, and reading a value needs `secretAccessor`, "which only the job and build accounts hold"; nothing under `internal/` reads a Secret Manager value (no `AccessSecretVersion` call) | `internal/backend/gcp/run.go:361-373`, `SECURITY.md:56` |

Three conclusions from the table, and they are the reason this feature is small:

- **The job axis is built.** The name and service-account derivations take the account
  (`names.go:179-191`), `ProjectJob` carries an `Account` field (`projectjobs.go:32`), the launcher
  checks the run's selection against the job's account (`project_job.go:131-182`), and the runner
  returns it (`runner/projectjob.go:59-100`). What is missing is the source of the account and two
  refusals.
- **The storage is built.** A named account's container is already created project-wide
  (`tfvars.go:271-276`).
- **The carrier is built.** A project job's static mount and its one-secret-per-member grant
  (row 15) are exactly what the per-repository path uses. Giving an account job one more
  `secret_env` entry gives its service account — and only it — read access to that one secret,
  with no new Terraform resource type.

## 2. What is refused, exactly, and by which rule

Two refusals and one empty axis, in the order a launch meets them:

1. **The profile may not mount a Claude OAuth token** (`internal/projectpolicy/profiles.go:291-293`):
   "a Claude OAuth token is person-owned (design D2), and its `allow_data_to` is a per-repository
   list a shared job cannot honour … so no profile mounts one until phase 5 decides how an account
   reaches a shared job." The predicate `isClaudeOAuthSecret` (`:328-331`) covers the bare
   `claude-oauth-token` and the whole `claude-oauth-token-` prefix. **This refusal stays**: the
   design adds a field beside `secrets:` rather than lifting it (§4.1).
2. **A launch whose `agent.auth` is `oauth` may not land on a shared job**
   (`internal/cli/project_job.go:223-233`, Task 3.4a). It refuses before the claim and the mint.
   This is the refusal the feature replaces with a real gate.
3. **The account axis is empty by construction** (`internal/infra/projectjobs.go:92-96`).

Lifting refusal 1 alone would not do what the owner ruled: `projectSecretEnv`
(`projectjobs.go:243-251`) maps the default account's `claude-oauth-token` (it is in
`config.ReservedSecrets`, `internal/config/config.go:481-486`) and refuses a named account's
`claude-oauth-token-<name>` ("neither a secret Fugaro reserves nor the key of a configured model
provider", `:221-224`), and every profile still gets one job. The accounts have to be a profile
field the deployer turns into jobs.

## 3. The carrier: the static mount, a stopgap — what it costs

The owner ruled the carrier on 2026-10-18 (§8 D1/D2): each account's job gets the token by the
static `secret_env` mount, its service account can read only its own account's secret, no launcher
reads it, and the seal is not involved. The cost, stated as the owner accepted it:

1. **What the static mount exposes.** Every execution of the account's job holds the token in its
   container environment from start, so every run of every repository admitted to that job's
   profile holds it — in the runner's process, and in the agent's on an OAuth run (row 14). Under
   §4.1's subset rule those are all repositories the policy allows to use the token anyway, and the
   runner's admission check refuses any other repository before the agent starts. **A hostile run
   can use the token during its own run**, and can copy it out and use it afterwards until the
   token is revoked.
2. **What it does not expose.** The token is **not** readable by launchers: a `secret_env` entry is
   a `secret_key_ref`, `JobFullEnv` returns only plain values, and no launcher holds
   `secretAccessor` (row 16). It is **not** readable by another job: the member is per (job service
   account, secret) (row 15), so the job of another profile or another account cannot read it.
3. **Why it is temporary.** The security service ([../credentials-service/spec.md](../credentials-service/spec.md),
   "Security service (credential proxy)") makes the job's service account hold no Secret Manager
   access and gives the job a short-lived per-job token instead; the service is "the single holder
   of every secret a job needs". That closes point 1's residual for OAuth tokens as it does for
   every other key, which is why this design does not build a second, intermediate carrier (the
   seal) that the service would replace as well.
4. **One path, not two.** The project level uses the per-repository level's own carrier
   (`spec.go:585-586`), so there is no second delivery path to maintain until the security service
   replaces both.

## 4. The design

### 4.1 The policy gains an `accounts` block, and a profile may name accounts

`Shared()` strips `claude_accounts` from the runs-bucket shared config, with reasoning that is
about the **runs bucket** — "any launcher with `objectAdmin` on it can write" it
(`internal/localcfg/shared.go:22-27`). It does not apply to the **policy node**:
`config/policy/shared` has `".write": false` in the rules (`internal/budget/rules/rules.json.tmpl:15-17`),
so only `fugaro project policy publish` writes it, and the runner reads it and fails closed on it.
A shared job is already a function of the published policy, so the accounts a shared job serves
are a policy fact:

```yaml
# config/policy/shared, beside profiles:
accounts:
  acme-team:
    allow_repos: ["github/acme/app", "github/acme/tools"]   # which repositories may use this token
  ops:
    allow_repos: ["github/acme/app", "github/acme/tools", "github/acme/infra"]
profiles:
  standard:
    provider: github
    claude_accounts: [acme-team, ops]   # NEW: one job per (profile, provider, account)
    allow_repos: ["github/acme/app", "github/acme/tools"]   # every account above covers every entry
  infra:
    provider: github
    claude_accounts: [ops]
    allow_repos: ["github/acme/infra"]
```

- **Several accounts per profile, one job each** (D4). The deployer builds one job per (profile,
  provider, account) through the existing `gcp.ProjectJobName(profile, provider, account)`, and the
  launcher picks the job whose account is the run's own selection (`agent.oauth_token` or
  `--token`), refusing a selection the profile does not list — as the individual level does
  (row 2).
- **Derived, never hand-written** (D3). `accounts.<name>.allow_repos` is derived at publish time
  from the local config's `claude_accounts.<name>.allow_data_to` — one anchor, one edit. The
  translation is explicit: `allow_data_to` holds `owner/name` slugs
  (`claude_accounts.go:173-176`); a policy `allow_repos` entry is a provider-qualified pattern that
  may carry `*` (`internal/projectpolicy/match.go:105-120`, `ValidPattern`). Publish qualifies each
  slug with the provider of the profile(s) naming the account and never writes a wildcard into an
  account's list. Only accounts some profile names are published.
- **The subset rule** (D3). Publish and `fugaro init` **fail closed** on a profile that names an
  account whose `allow_repos` does not cover every repository the profile admits, naming both
  lists and the repository in one and not the other. A profile that names an account must write its
  own `allow_repos` with exact entries only (an absent list means "the project's `allow_repos`
  applies", `internal/config/config.go:519-533`, and a wildcard makes the subset undecidable).
- **No API key beside the accounts** (D4). A profile that lists accounts may not also mount an
  Anthropic API key, in either spelling (`anthropic-api-key`, or a provider whose key variable is
  `ANTHROPIC_API_KEY`). Least privilege: an account job's container would hold a key no OAuth run
  can use (row 14), and "which credential does this job serve" stays answerable from the profile.
- **A new field, not a `secrets:` entry.** `profiles.<p>.claude_accounts` sits beside `secrets:`,
  so refusal 1 stays true and in force: a Claude token is never something an operator lists in a
  profile's mounts; the deployer adds the one mount an account job carries (§4.3).
- `config.JobProfile` (`internal/config/config.go:519`) gains the field — `projectpolicy.Profile`
  embeds it (`projectjobs.go:120-129`), so it reaches publish, the deployer and the runner from one
  declaration — and the local config validates it against `lc.ClaudeAccounts` at load.
- **The default account** (D5) cannot be defined in `claude_accounts` ("default" is reserved there,
  `claude_accounts.go:166-167`). A profile lists it as `default`, and its policy list is derived from
  `project_secrets.claude-oauth-token.allow_data_to` — the entry that already decides whether its
  container exists (row 6).

**Schema version.** `profiles.<p>.claude_accounts` is a new field inside a profile entry, and the
policy's rule for that case is written in the decoder: the profiles block is decoded with
`DisallowUnknownFields`, and "a newer profile field bumps Version, which is what refuses a node
this binary cannot read" (`internal/projectpolicy/policy.go:501-519`). So `projectpolicy.Version`
goes from 2 to 3 (`internal/projectpolicy/version.go:24`). The `accounts` block itself is a new
top-level key, which an older reader tolerates and lists in `UnknownKeys` (`policy.go:28-31,
183-187`); the bump is what keeps an older runner from running past a gate it does not know, the
reason version 2 exists (`version.go:15-23`). The cost is the one the 1→2 bump had, and the
existing skew messages already name the remedies (`version.go:123-172`): a version-2 runner image
meeting a version-3 node refuses with `RunnerSkew` ("refresh the runner image (fugaro upgrade)"),
and a version-3 reader meeting a version-2 node refuses with `CLISkewNewer`/`RunnerSkewNewer`
(remove the node with `fugaro project policy publish --delete` and publish again). The rollout is
therefore: upgrade the CLI, `publish --delete` and republish, refresh the runner images — written
into the operator docs by the plan, never a silent migration.

### 4.2 The secret-id scheme does not change

The token's logical name is what `config.ClaudeAccountSecret(name)` already returns
(`internal/config/claude_accounts.go:75-85`): `claude-oauth-token` for the default account,
`claude-oauth-token-<name>` otherwise, a prefix reserved for the purpose. The container is
`gcp.ProjectSecretID(fugaroProject, logical)` (`internal/backend/gcp/names.go:207-209`),
domain-separated with `"project\x00"`. **No new helper, no new prefix, no rename.** The `claude-`
prefix leaves room for the parallel `codex-oauth-token[-<name>]` scheme (§7 U6).

Storage eligibility does not change (D6): a named account is accepted project-wide when its
`allow_data_to` is non-empty (`secrets.go:641-643`), and the default account needs the explicit
`project_secrets` entry it already needs (`project_secrets.go:63-71`).

### 4.3 The token reaches the run by the static mount (stopgap)

- **One carrier function.** The deployer gives each account job its profile's `secret_env`
  (`projectJobSecrets`, `projectjobs.go:211-236`, unchanged) **plus one entry**:
  `CLAUDE_CODE_OAUTH_TOKEN` → `config.ClaudeAccountSecret(account)`. That entry is added in exactly
  one function (the plan calls it `projectAccountMount`), so the security service's swap (§4.6)
  replaces one function and nothing else. `projectSecretEnv` is not taught a named account's secret:
  `secrets:` keeps refusing it (refusal 1), and the mount comes from the field, not the list.
- **The grant follows the mount.** `google_secret_manager_secret_iam_member.job`
  (`modules/project/sa.tf:56-63`) iterates the job's own `secret_env` values, so the account job's
  service account gets `secretAccessor` on its own account's secret and on nothing another account
  owns. No new resource, no launcher member, no binding, no project-level grant
  (`TestNoProjectLevelSecretAccessor`, `deploy/terraform/terraform_test.go:129`, keeps passing).
  A tftest pins "account job A's service account has no member on account B's secret".
- **The container must exist.** `projectJobSecrets` already refuses a mount whose container the
  installation does not create (`projectjobs.go:218-220`); the account's container exists exactly
  when row 5 or row 6 says so, so a profile naming an account with no container fails at `init`,
  naming `claude_accounts.<name>.allow_data_to` / `project_secrets`.
- **The default account** (D5) rides the profile's no-account job (`projectJobAccount` maps
  `default` to `""`, `project_job.go:171-182`): when a profile lists `default`, that job's
  `secret_env` gains `CLAUDE_CODE_OAUTH_TOKEN` → `claude-oauth-token`. Every run of that job then
  holds the default token — which D4's no-API-key rule makes consistent: such a profile's runs are
  OAuth or Vertex runs, never API-key ones.
- **Nothing on the launch path changes for the token.** The launcher never reads it, never seals
  it, never puts it in an override; `LaunchSpec` gains nothing. The launcher only *selects* the
  account's job.

### 4.4 Failure naming, and no silent fallback

Every path fails closed, names the thing the operator must fix, and never degrades to another
credential:

| when | condition | what is said |
| --- | --- | --- |
| publish / `fugaro init` | a profile lists an account the local config's `claude_accounts` does not define (or, for `default`, has no `project_secrets` entry) | name `profiles.<p>.claude_accounts`, the account, and where it is defined |
| publish / `fugaro init` | the profile admits a repository the account's `allow_repos` does not cover, or the profile's own list is absent or holds a wildcard (D3) | name both lists and the repository that is in one and not the other |
| publish / `fugaro init` | a profile lists accounts **and** mounts an Anthropic API key (D4) | refuse, naming which of the two to drop |
| `fugaro init` | the account's project-wide container is not created | refuse, naming the logical secret and `claude_accounts.<name>.allow_data_to` / `project_secrets:` |
| launch | `agent.auth: oauth` on a shared job whose profile lists no account, or not the run's selected one | refuse, naming the profile, `profiles.<p>.claude_accounts`, and `claude_accounts` in the local config |
| launch | the repository is not in the account's `allow_repos` | refuse, naming the repository, the account, `accounts.<name>.allow_repos` and the profile's `allow_repos` |
| launch | a non-OAuth run would land on an account job | never: the launcher selects an account job only for an OAuth run |
| runner | the run's job is an account job and the run's auth is not `oauth`, or its selected account is not the job's | refuse to start |
| runner | the repository is not in the policy's `accounts.<name>.allow_repos` for the job's account | refuse to start, independently of the launcher (the launcher is trusted, project-centric.md's D10, so a launch check alone is not a boundary) |
| runner | the token is absent from the environment, or the agent cannot use it | the run fails; it does **not** fall back |

The no-fallback property has a mechanism, already built: `agent.BuildEnv` builds the agent's
environment from an allow-list keyed on the run's auth (row 14).

### 4.5 The job axis, populated

`ProjectJobs` (`internal/infra/projectjobs.go:92-103`) stops hard-coding `const account = ""` and
deploys, for each profile, its existing no-account job **plus** one job per listed non-default
account, computed through one function shared with the launcher (`project_job.go:98`) and the
runner (`runner/projectjob.go:180`), which replaces their `config.ClaudeAccountOfSecrets` use so the
three sides cannot drift.

**No existing job is renamed or deleted** (D9). `projectStem` adds the account segment only for a
non-empty account (`names.go:152-158`) and `projectKey` hashes the account as its own part
(`:165-167`), so every no-account job's name is byte-for-byte what it is today; adding an account to
a profile only *adds* a job and its service account, and `RefuseProjectJobDeletes`
(`projectjobs.go:258-282`) does not fire. Removing an account from a profile later would plan that
job's delete and be stopped there — retiring a project job is still unsupported (plan, known
follow-ups).

### 4.6 The swap to the security service

When the security service lands, the carrier changes and nothing else does:

| stays | changes |
| --- | --- |
| the policy's `accounts` block, its derivation and the subset rule | the account job's `CLAUDE_CODE_OAUTH_TOKEN` `secret_env` entry is removed (`projectAccountMount` stops adding it) |
| `profiles.<p>.claude_accounts` and the no-API-key rule | the account job's `secretAccessor` member goes with it (it follows `secret_env`, §4.3) |
| one job per (profile, provider, account), and every job name (the name does not depend on the mounts, `names.go:179-191`) | the job receives the service's short-lived per-job key instead, by the service's own mechanism |
| the launch gate, the runner's gate, the failure names of §4.4 | SECURITY.md's stopgap paragraph (D8) is replaced by the service's own claims |

Because no job is renamed, the swap plans no `RefuseProjectJobDeletes` stop. This is a design
constraint on the implementation, not just a hope: the plan's tests pin that the mount is added in
one function and that the job name is independent of `secret_env`.

## 5. Are phases 5.1 and 5.2 prerequisites? (the owner asked; the evidence says no)

- **5.2's shape is already built.** Task 5.2 is "one job per (profile, shared account)". The job
  name and service account already take the account (`names.go:179-191`), `ProjectJob` carries an
  `Account` field (`projectjobs.go:32`), the launcher refuses a run whose selection is not the job's
  account (`project_job.go:131-182`), and the runner returns the account it bound
  (`runner/projectjob.go:59-100`). The remaining work is the account's source and the two refusals
  of §2 — *this* feature.
- **5.1 is a different feature.** Task 5.1 is *person-owned* secrets: one secret per (person,
  account), `gcp.PersonSecretID`, `fugaro accounts add`/`share`, failover moved into `fugaro run`
  ([project-centric.md](project-centric.md) §7). The owner's ruling describes installation-owned
  named tokens, exactly like the individual level's ([named-oauth-tokens.md](named-oauth-tokens.md)
  §3).
- **Nothing else is a prerequisite.** The carrier is the static mount the project module already
  has (row 15); the feature does **not** depend on the seal or on PR #563.
- **Out of scope** by the owner's rulings: the Cloud Batch backend and the project-centric phases 4
  and 6.

## 6. Threat model

| adversary | what stops it | what does not |
| --- | --- | --- |
| a run of **another** shared job (another profile, or another account's job) | its service account has no member on this account's secret (row 15); an execution's secret env is not readable through the API (row 16) | — |
| a **peer run** of the same account's job | nothing: it is on the account's list and holds the same token | this is the shared-job reach the owner accepted |
| a **launcher** (every member of `launchers`) | no `secretAccessor` (row 16); the launcher never reads, seals or forwards the token | a launcher is trusted (project-centric.md's D10, "launcher environment overrides: accepted and documented") and can launch the account job; that reach is the per-repository job's today, unchanged |
| the **agent process itself** on any run of the account's job | nothing | the residual §6.1 states, and the reason the carrier is a stopgap |
| a repository admitted to the account's list but hostile | the account's blast radius is not per repository | it gets a credential that works for **every** repository in the list, until the token is revoked |
| a repository **not** in the list | the launch gate, and the runner's own check of the profile and the account | — |

### 6.1 The residual SECURITY.md has to carry (D8)

- **A hostile run can use the token during its own run.** Every run of an account's shared job
  holds that account's token in its environment, from the start of the run; its agent can read it,
  copy it into an artifact, a PR body or a log line (the redactor reduces the last, and is not a
  boundary), and use it after the run ends, until the owner revokes it.
- **The account is the blast radius.** `allow_data_to` and `allow_repos` gate *which runs* get a
  token; they do not scope what the token *can* do.
- **No launcher reads it.** Reading the value still needs `secretAccessor`, which only the job and
  build accounts hold — an account job's service account on its own account's secret, and nothing
  else (SECURITY.md:56's sentence stays true).
- **This carrier is a stopgap.** It is replaced by the security service
  ([docs/credentials-service/](../credentials-service/README.md)), after which no job holds the
  token at all.
- **Nothing prices an OAuth run the way the gateway prices a provider-key run**
  ([named-oauth-tokens.md](named-oauth-tokens.md) §1); usage limits are the account's own.

## 7. What is verified, and what is not

**Verified in the code** (read, cited above, on `origin/integration-0.9.0`): everything in §1, §2,
§4.2 and §4.3's grant mechanism; the account axis on all three sides; the policy decoder's
version rule and skew messages; `agent.BuildEnv`'s allow-list; `JobFullEnv`'s plain-value-only
read; the absence of any Secret Manager value read under `internal/`.

**Not verified — owner-run, live, sandbox only** (no probe was run for this document):

- **U2. Which credential Claude Code prefers when both `CLAUDE_CODE_OAUTH_TOKEN` and
  `ANTHROPIC_API_KEY` are set.** Not needed: `agent.BuildEnv` never sets both for an agent, and D4
  keeps both off one job. Recorded only so nobody re-opens it.
- **U3. Whether one account's OAuth token can serve several concurrent runs.** A sizing question:
  if the account refuses concurrent use, accounts have to be split.
- **U5. Whether the account jobs' `secretAccessor` members plan as pure creates** beside the
  existing containers. Expected: yes (an IAM member is additive and the container's
  `prevent_destroy` is untouched); the golden plans of the implementing PR confirm it.
- **U6. The `codex-` scheme being built in parallel (Task C3).** The two meet only in the naming
  convention, which §4.2 leaves as it is.

U1 (an execution override read back through the API) and U4 (the seal's key surviving a retry) of
the previous draft are **moot**: this feature puts nothing in an override and does not use the seal.

## 8. Owner decisions — RULED 2026-10-18

**Numbering.** D1 to D10 are **this document's own**. They are not
[budget-token-channel.md](budget-token-channel.md)'s D14-D19, and this document's D10 is not
[project-centric.md](project-centric.md)'s D10 ("launcher environment overrides: accepted and
documented"), which every reference here calls *project-centric.md's D10*. No decision is open.

- **D1. The carrier — RULED: the static `secret_env` mount, a stopgap.** The account's job gets the
  token from the platform (the per-repository path's own carrier); no seal, no second sealed object.
  Labelled a stopgap, replaced by the security service ([../credentials-service/](../credentials-service/README.md),
  #512) as a clean swap (§4.6). The lesser security (§3 point 1) is accepted by the owner.
- **D2. Who may read the token — RULED: only the account job's own service account, on only its own
  account's secret.** No launcher read, no per-account launcher list.
- **D3. The policy's gate — RULED as recommended:** `accounts.<name>.allow_repos` derived at publish
  from `claude_accounts.<name>.allow_data_to` (provider-qualified, never a wildcard), and the subset
  rule: a profile that lists accounts writes its own exact `allow_repos`, covered by every listed
  account's list.
- **D4. The profile field — RULED as recommended:** `profiles.<p>.claude_accounts: [<name>, ...]`,
  one job per (profile, provider, account), the run choosing with `agent.oauth_token` / `--token`;
  the `secrets:` refusal stays; no Anthropic API-key mount beside accounts.
- **D5. The default account — RULED as recommended: allowed**, listed as `default`, riding the
  profile's no-account job; its list derives from `project_secrets.claude-oauth-token.allow_data_to`.
- **D6. A named account's container — RULED as recommended:** keep requiring a non-empty
  `allow_data_to`; `ProjectScopeByDefault` unchanged.
- **D7. Retry re-seal and the object's lifetime — MOOT.** There is no sealed object, so no re-seal,
  no failed-launch delete and no lifecycle suffix.
- **D8. SECURITY.md's wording — RULED: rewritten for the static mount:** no launcher read; every
  run of an account's job holds its token; a hostile run can use the token during its own run; the
  account is the blast radius; a stopgap pending the security service (§6.1).
- **D9. Sequencing and the job set — RULED:** no dependency on PR #563 or on the seal. A profile
  keeps its no-account job and gains one job per listed account; nothing is renamed or deleted, and
  `RefuseProjectJobDeletes` never fires on the rollout.
- **D10. Inspection — RULED as recommended:** `fugaro project policy show` prints the `accounts`
  block; `fugaro accounts ls` stays local-only and unchanged.

## 9. What this document does not decide

It does not design the security service (that is [../credentials-service/](../credentials-service/README.md));
per-person ownership (phase 5.1); failover or usage-limit switching for project accounts (phase
5.3's subject); Codex support (Task C3); the Cloud Batch backend (excluded from 0.9.0); or the
project-centric phases 4 and 6. It makes no cloud call, reads no secret and writes no secret-shaped
literal.

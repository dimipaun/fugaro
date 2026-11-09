# Dynamic secret delivery: reconciliation

**Status: the owner answered D1930–D1937 on 2026-11-09, all as recommended, and D1938–D1940 (review round 1) the same day, all as recommended. Plan only; nothing is implemented; no task approved.**
Checked against `origin/main` at `934bb23f8`, 2026-11-09. **Where this file disagrees with [spec.md](spec.md), it wins.**

The addendum asked to be merged with the existing secrets and credentials brokering spec as one unified
design, with every conflict flagged. [../spec.md](../spec.md) (the owner's brokering spec, D490–D506) and
[../../credentials-service/spec.md](../../credentials-service/spec.md) (D130–D154, Q6) are the two it
overlaps. §4 is the unified design, topic by topic; §3 lists every conflict. The addendum is newer, so
where it conflicts it governs unless a decision below says otherwise, and each such case is a decision the
owner answered.

## 1. Summary

The addendum is mostly a re-shaping of what exists, not greenfield:

- **The LLM proxy already exists** (F1): the service's `messages` and `count_tokens` routes, behind the
  runner's loopback gateway, with a published `budget.allowed_models` and budget caps.
- **Slips are the run identity** (F2): the D134 budget identity plus the job account's ID token, with
  liveness from the write-once outcome. What is new is the grant step (a launcher-requested record the
  service owns), not the idea.
- **Pools already exist** (F3): `config.Pool`, `craft_pools`, failover and round-robin; what is missing is
  an owner and an allowed-users list.
- **What is genuinely new:** the intake function, the per-user and per-project secret names, the service's
  prefix-conditioned read, the slip grant and claim, and token ownership.

Four things in the addendum are wrong or unsafe as written and are resolved here: the secret name grammar
collides across users (F6), an execution ID cannot be verified from the job's token (F7), the intake's
write access is an integrity risk the spec does not name (F8), and "today a secret is mounted by Terraform"
is only partly true (F4).

## 2. Spec concept → what the repo has

| Spec concept | What the repository has |
| --- | --- |
| Credential service as sole reader | `internal/credsvc` reads one bundle secret (`bundle.go`, Q6) as its own account; the job accounts still hold accessor grants on mounted secrets (the flip lists `config.ServiceHeldSecretNames` and the proxied-git list are empty) |
| LLM proxy inside the service | `internal/credsvc` routes `MessagesPath`/`CountTokensPath` (`servedRoute`), the cascade (`cascade.go`), `forward.go`; the job reaches it through `internal/gateway` |
| Spend caps, model allowlist, metering | The gateway's budget (`internal/budget`), the policy's `budget.allowed_models` (`internal/gateway/service.go` names it in its refusal) |
| Slips, claim, bind | The run identity: a Firebase ID token for `uid r~<slug>~<run>` plus the job SA's Google ID token, verified by `Authenticator` in `internal/credsvc/auth.go`; liveness from `outcomes/$day/$slug/$run` |
| Slip store only the service writes | RTDB `creds/` (`internal/creds`: marks, leases, state), which the database rules deny every run identity (`internal/budget/rules/rules.json.tmpl`, `internal/e2e/creds_rules_guard_test.go`) |
| Slip cache, 5 minutes | None for runs; the bundle has a short cache (`bundle.go`) |
| Pools | `config.Pool` (`internal/config/craft.go`), `craft_pools` in the published policy (`internal/projectpolicy/policy.go`) |
| Token owner, job records its token | `oauth_account` and `stage_accounts` on the run record (docs/accounts-add D876); no owner of a token |
| Person callers | The person class in `internal/credsvc/auth.go` (read mints only), keyed on a verified email |
| Intake function | Nothing. `fugaro creds set` writes the bundle from the owner's machine with their own credentials |
| Project admins in config | `terraform.budget_admins` (`internal/localcfg/localcfg.go`) for Firebase; the roles-scopes roster is on `main` (`internal/access`, `config/admin/access`), read over IAM with a database-wide grant |
| Warm instance | The service sets `min_instance_count = 0` (`deploy/terraform/gcp/modules/installation/credentials.tf`, D501) |
| Offboarding | invoker grants are Terraform-applied in `iam.tf` of the installation module |

## 3. Conflicts with the existing specs (flagged, per the addendum's note)

| # | Addendum says | Existing ruling | Handled by |
| --- | --- | --- | --- |
| C1 | Per-secret names `fugaro-u-/fugaro-p-`, service reads by prefix | Q6 (2026-10-19): one bundle, one grant, "not a name-prefix IAM fence" | **D1931** |
| C2 | Slips in Firestore | D494 (2026-10-27): no new token store; service state is RTDB `creds/` | **D1932** |
| C3 | Jobs point at the service's proxy with a per-job token | Brokering spec §2 and D495: the agent never calls the service; the runner's forwarder does | **D1933** |
| C4 | One warm instance while jobs run | D501 (2026-10-27): the service keeps scaling to zero | **D1934** |
| C5 | Admins "listed in the project config" | roles-scopes D1580–D1593: one admin role | **D1935** |
| C6 | Pool owner and allowed users | `craft_pools` has neither; the HARD RULE of CLAUDE.md (update migrates) applies | **D1936** |
| C7 | Slip binds the execution ID | The job's ID token carries no execution claim (F7) | **D1937** |
| C8 | "Removed: Terraform mounting; `fugaro update` as a step" | D491 and D500: staged, per secret class, flip lists gated on live checks; `belong` never touched (D147) | **R2** |
| C9 | Jobs "have no secrets mounted" | D491 kept workflow secrets and builds as listed exceptions with exit slices; the project workflow secret (docs/project-workflow-secrets) mounts by design | **R2**: the end state, not a first release |
| C10 | Mandatory model keys removed | Already true for the cascade: the service is default-on (credentials-service/default-on.md) and a key is only needed by the auth mode | Exists; no change |

## 4. The unified design (what the plan builds)

1. **Two stores.** The bundle keeps the platform credentials (GitHub App key, Bitbucket token, review
   signing keys). User-supplied credentials (model tokens, API keys, later workflow secrets) become
   per-secret names written by the intake: `fugaro_u_<id>_<name>` and `fugaro_p_<name>` (R1, R7).
2. **The service reads both.** The bundle by its one grant; the per-secret names by an accessor whose IAM
   condition is the name prefix. It has no write access.
3. **The intake** is a separate Cloud Run service with its own account: create, add version, list (names
   and dates) and delete, all conditioned to `fugaro_u_`/`fugaro_p_` names; no accessor, no IAM rights (R3).
4. **Slips** are written by the service under RTDB `creds/slips/<id>`; claimed once, bound to the verified
   job account; cached 5 minutes; ended by the outcome or the max runtime.
5. **The agent-facing endpoint stays the runner's gateway.** The slip is not exported to the agent, which can
   still read the runner's environment (F22); the slip is bound to its run (R8).
6. **Pools** gain `owner` and `allowed_users`; a slip naming a token or pool is checked against them at
   issue time.
7. **The service stays default-on, scale-to-zero, with a runner heartbeat** keeping an instance warm while
   runs live.

## 5. Findings

| # | Class | Finding and consequence |
| --- | --- | --- |
| F1 | Exists | The LLM proxy is `internal/credsvc` `messages`/`count_tokens` plus the loopback `internal/gateway`; the spend cap and `allowed_models` enforcement exist. The addendum's proxy section needs OpenRouter support and the slip token, not a new proxy. UNVERIFIED: whether the service's forward serves an OpenRouter upstream today (U1) |
| F2 | Exists | A slip is the run identity under another name (`credsvc/auth.go` `Authenticator`: the run's Firebase token for `r~<slug>~<run>` plus the job SA's token, liveness from `outcomes`). The new part is the **grant**: a launcher-requested record naming the credentials the run may use |
| F3 | Exists | Pools exist (`config.Pool`, `craft_pools`, `CraftPools`). No owner, no allowed users |
| F4 | Wrong | "Today a secret is mounted by Terraform" holds for the non-cascade modes (`modules/workflow/job.tf` `secret_key_ref`, `secretVersionAdder` in `modules/installation/secrets.tf`), the GitHub App key and workflow secrets. A `cascade` run's model credential is already read by the service from the bundle, which `fugaro creds set` writes with no Terraform. The pain is real for the other classes |
| F5 | Gap | The service's person class admits read mints only, keyed on a verified email. The grant request and the intake need a caller identity keyed on the Google **`sub`**, so the person class is extended, not reused as is. UNVERIFIED: that a user's Google ID token `sub` is the stable numeric account ID in every sign-in path the CLI uses (U2) |
| F6 | Wrong | The collision is with the **existing** secret names, not between ids (a digits-only id already splits at `-`): `gcp.SecretID` builds `fugaro-<readableSlug>-<logical>-<hash>` from a slug of `[a-z0-9-]`, so a repository owned by `p`, `u` or `p-labs` produces `fugaro-p-labs-app-…`, inside the intake's `fugaro-p-` IAM prefix. An admin could overwrite or delete a repository's mounted token through the intake. **Resolution R1** (prefixes the existing scheme never emits) |
| F7 | Wrong | Step 4 binds the "Cloud Run execution ID" to the slip, but a job's ID token carries the account and no execution claim, so the service cannot verify an execution ID a caller states. The job SA email is the verified fact (`Authenticator`, one SA per job); **D1937** |
| F8 | Gap | The intake cannot read, but write is an integrity power: adding a version to a shared or platform secret replaces a key with one an attacker holds (model prompts then go to the attacker's account), and delete is a denial. IAM can condition version-add and delete on the name prefix and cannot condition create. **R3** |
| F9 | Gap | The agent on a person's machine holds that person's credentials, so the server cannot tell a person from a coding agent at the intake. The refusal in an agent's session is client-side only (as `fugaro creds set` today). For an ordinary member the own-prefix and no-read limits bound the damage; **for an admin they do not**: an admin's agent could write `fugaro_p_` and delete another member's prefix. That is a power the admin's machine already holds as a role, recorded as an accepted limit (SECURITY.md in DS4.3) and narrowed by R7's reserved names |
| F10 | Rule | D501 and the addendum disagree (C4). A cold start sits on the path of the first call; the heartbeat is the owner's answer (D1934) |
| F11 | Rule | CLAUDE.md's HARD RULE: the migration of every existing secret (bundle entries, per-repository and project-wide secrets with mounts) to the new names happens inside `fugaro update`, safely while runs are in flight, with a convergence test. **R2** |
| F12 | Rule | `fugaro creds set` is the bundle's only writer and is refused in an agent session; it stays so for the platform credentials. The intake client keeps the same refusal (`.claude/rules/credentials-service.md`) |
| F13 | Gap | The slip needs the runner to claim at first call and to refresh: the runner already heartbeats (`internal/runner/budget_session.go`); the claim and the keep-warm ride that |
| F14 | Gap | Cache of 5 minutes means a revoked or finished slip works up to 5 minutes. The existing path reads the outcome on every call; the cache weakens that. Accepted by the addendum; the plan keeps the **outcome check uncached** (it is one RTDB read) and caches only the slip body (R4) |
| F15 | Gap | Max runtime: the service has per-run caps that are advisory (credentials-service Q6 further answers); the slip's max runtime ends issuance but not a call in flight |
| F16 | Gap | Token ownership: the run record has `oauth_account`/`stage_accounts`. Recording the owner is an additive field with a **new name** (`credential_owner`), never a changed meaning (CLAUDE.md) |
| F17 | Rule | Policy shape changes (pool owner, allowed users) bump the policy version and need the migration in `update` (`internal/projectpolicy/version.go`, `migrate.go`); **D1936** |
| F18 | Rule | The CI secret-reader guard (`deploy/terraform/secret_readers_test.go`, `docs/secrets-brokering/secret-readers.md`) lists today's readers; every new reader (the intake's writes are not reads; the service's prefix accessor is) needs a row |
| F19 | Rule | Security-sensitive tree: `.claude/rules/credentials-service.md`: independent review, one audit line per refusal, no secret-shaped literals, owner runs live steps |
| F20 | Gap | Offboarding "revokes invoker rights": the grants are Terraform-applied today (`iam.tf`); revocation is `fugaro update`-class work and must also end the person's open slips |
| F22 | Wrong | The addendum, and this plan's first draft, treat the slip ID and job token as hidden from the agent. SECURITY.md ("What one run's agent can reach") already states the agent shares the runner's container and user, reads its environment through `/proc` and reaches the job account through the metadata server. So U5 is already answered (it can), and the slip's protection is its **binding**, not secrecy (R8) |
| F23 | Gap | A slip holding only a job account is claimable by another run or job: runs of one repository, and every run of a shared job, share an account, and launchers can override a job's environment. **R8** |
| F24 | Gap | A launcher that omits the slip would downgrade to the old path and bypass pool authorisation. **R9** |
| F25 | Gap | The person class is the operators' addresses and "not its launchers" (`internal/credsvc/auth.go`, `PersonAccounts`). Non-operator launchers cannot request a slip as it stands. **D1938** |
| F26 | Gap | A person's ID token carries only gcloud's audience, so a token the user sends to any Cloud Run service is replayable at the intake for about an hour. **D1939** |
| F27 | Gap | The roster is read over IAM with a database-wide grant and keyed by email; the prefix is keyed by `sub`. **D1940** |
| F28 | Wrong | Binding a slip to slug, run and account does not fence launchers from each other: any launcher can mint any run's budget identity, read a job's environment (`run.executions.get` is in the launcher role) and share an account with it. **R8 amended, D1941** |
| F29 | Wrong | R7 as first written reserved every bundle name, including the model token names (`anthropic-api-key`, `claude-oauth-token[-<name>]`), so R6's rotation through the intake could never happen. Only the platform kinds are reserved (R7 amended) |
| F30 | Wrong | Secret Manager label values cannot hold an email; the owner's identity is an **annotation** (R10 amended). `secrets.list` is checked on the project, so a name-prefix condition admits nothing: list is granted unconditioned to the intake and, for matching the annotation, to the service (metadata only); Check 510 measures it |
| F31 | Wrong | "Invoker follows the roster" is false today: the service's invoker is `var.operators` (`credentials.tf`) and nothing derives grants from the roster. Launchers need an invoker grant for the slip route and an allow-list for the launcher class (fail closed); DS2.2 and DS1.2 carry them |
| F32 | Gap | The derived admin list must hold project-scoped admins only (roster grants carry scopes), live outside any path a launcher can write (`runs/`, `tokens/`, `issues/`), and be readable by the intake |
| F33 | Gap | Database admins bypass the rules on `creds/slips` and can forge a slip. **D1943** |
| F34 | Gap | Existing pools have no owner; owner-only defaults plus R9 would fail every run on one after `update`. **D1942** |
| F35 | Gap | The managed deployer holds unconditioned `secrets.create`, so it could pre-create a `fugaro_u_*` container and block or mislabel it. The intake writes only containers it created (annotation `created_by`), and the service reads only containers carrying it (R12) |
| F21 | Open | Workspaces (docs/workspaces W6) plan a workspace credentials service; this design is per project. Not decided here; recorded, not decided here |

## 6. Decisions

| # | Question | Recommendation and reason | Alternative and cost | Owner's answer |
| --- | --- | --- | --- | --- |
| **D1930** | Folder shape | Subfolder with a unified design here (the old spec stays verbatim) | Separate folder: drift; edit in place: breaks the verbatim rule | **2026-11-09: the recommendation.** One place, nothing rewritten |
| **D1931** | Storage vs Q6 | **Hybrid:** platform credentials stay in the bundle; user-supplied credentials per secret, read by a prefix-conditioned accessor. Reverses Q6 for that class only | Replace the bundle: bigger migration. Keep the bundle and drop the intake: needs read access | **2026-11-09: the recommendation.** The intake cannot read, so per-secret is forced; the platform class keeps Q6's one grant |
| **D1932** | Slip store | RTDB `creds/slips`: same subtree, rules and service role as marks and leases | Firestore: new database, role, rules | **2026-11-09: the recommendation.** Departs from the spec's letter, D494's precedent |
| **D1933** | Agent endpoint | The gateway stays; the slip lives in the runner | Direct jobs: agent holds a token, budget role moves | **2026-11-09: the recommendation.** The sandbox rule holds |
| **D1934** | Warm instance | A runner heartbeat keeps an instance up only while runs live; no `min_instance_count` change | min=1: idle billing; scale-to-zero: cold start | **2026-11-09: the recommendation.** D501 stands |
| **D1935** | Shared writers | Reuse the roles-scopes admin role (on `main`; read through D1940's derived list) | A new config list: two lists | **2026-11-09: the recommendation.** |
| **D1936** | Pool owner | Additive `owner` and `allowed_users`, default owner-only, policy version bump with `update` migrating | A separate node: two nodes for one pool | **2026-11-09: the recommendation.** |
| **D1938** | Who may request a slip (F25) | A new launcher caller class admitted on the grant route only; the person class stays operators-only | Operators only: members cannot launch; widen the person class: launchers gain the workspace-wide read mints | **2026-11-09: the recommendation.** |
| **D1939** | Token replay (F26) | Accept and document in SECURITY.md; the own-prefix, no-read bounds stay | A short-lived intake-audience token: an extra IAM grant per user, outside the update-only rule | **2026-11-09: the recommendation.** |
| **D1940** | Admin check (F27) | `update` publishes a derived admin list (email and sub) to a bucket object the intake reads; the intake never touches the database | Direct roster read: database-wide read; the service writes shared secrets: breaks its least privilege | **2026-11-09: the recommendation.** The list lags a role change until `update` runs |
| **D1941** | Slip theft between launchers (F28) | The claim returns a claim secret kept only in the runner's memory and required on every call; the runner claims at start, not at the first model call | Document owner-only as policy, not a boundary | **2026-11-09: the recommendation.** It fences other launchers only; the agent can still read the runner's memory (F22) |
| **D1942** | Existing pools (F34) | Migrated pools get no owner and stay usable as today (legacy-open), with a doctor line asking an admin to name one | Owner = the operator who ran `update`: silently blocks other launchers | **2026-11-09: the recommendation.** |
| **D1943** | Slip forgery by database admins (F33) | Accept and document in SECURITY.md | MAC each slip: a signing key to hold and rotate | **2026-11-09: the recommendation.** |
| **D1937** | Slip binding | Verified job SA plus one-time claim; execution name audit-only | Launcher-written execution ID: unverifiable | **2026-11-09: the recommendation.** F7 |

No answer deviates from a recommendation, and no two answers conflict. D1938–D1940 were raised by review round 1.

### Resolutions (one defensible answer; not questions)

- **R1: name grammar.** Prefixes the existing scheme never emits (it emits no `_`): `fugaro_u_<id>_<name>` and `fugaro_p_<name>`, the id digits only and `<name>` letters, digits and `-` (no `_`), so the split is unique. A test pins that no output of `gcp.SecretID`, the project-secret and bundle ID builders can match either prefix, and a name over the Secret Manager limit is refused with a sentence that names the limit. The IAM conditions use these prefixes.
- **R2: migration and removal are staged.** Per secret class, behind flip lists gated on owner-run live
  checks (D500, D491), with `belong` never touched (D147). Order: model credentials of the cascade
  first (they are already service-read), then the other `agent.auth` modes, then workflow secrets and
  builds last (listed exceptions until their exit slices). Each flip is one release, converges inside
  `fugaro update`, is safe while runs are in flight (docs/design/update-in-flight.md) and has an entry
  in the release's `### For operators`.
- **R3: the intake's integrity bounds.** The intake account holds create at project level (IAM cannot
  condition create), and version-add, list and delete only under a name-prefix condition. It derives the
  prefix from the verified `sub` and never from the request, refuses a name outside R1's grammar, refuses
  `fugaro_p_` for a non-admin, and writes one audit line per call. The platform bundle and every name
  outside the two prefixes are out of its reach by IAM, not by code.
- **R6: nothing moves; the service resolves in order.** The service cannot be asked to move a value
  `fugaro update` cannot read (the bundle's values are write-only to people). So a user-supplied credential
  is resolved by its per-secret name first and by its bundle entry second, and a bundle entry is replaced
  only when the owner rotates it through the intake. Every installation keeps working after `update`
  alone (HARD RULE), and nothing is deleted or re-entered.
- **R7: reserved names and read order.** The intake refuses a `fugaro_p_`/`fugaro_u_` name equal to a **platform** credential kind (GitHub App key, git token, Bitbucket token and read consumer, review-signing keys); the model token names are not reserved, so rotating one through the intake works (R6). A credential name maps to its per-secret name by the one grammar function (`secretname`), and a missing per-secret name (or one with no version) falls to the bundle, and a read error fails closed. A per-user token is always owner-only: a launcher is served only their own `fugaro_u_<sub>_<name>` secrets (a pool of per-user tokens is one person's own, as the spec's example is), and sharing is `fugaro_p_` secrets under a pool's `allowed_users` (R11).
- **R8: a slip is bound to its run, and to a claim secret (D1941).** The slip records the slug, the run and the expected job account; the claim and every call require the matching verified run identity (D134) and account, and the claim returns a claim secret (stored only as a hash) that the runner keeps in memory and presents on every call. The slip is not a secret from the agent (F22) and run identities are mintable by any launcher (SECURITY.md), so this fences other launchers, not the agent.
- **R9: no downgrade.** A per-user, pool-owned or shared credential is served only under a claimed slip; a call without one fails closed. The slip-less path serves only what it serves today.
- **R10: the rest of review round 1.** The grant's max runtime is capped by a service constant. Pool `owner` and `allowed_users` are spelled by email in the policy (the roster's key) and resolved to `sub` at grant time. Each secret carries its owner's `sub` and verified email as an annotation written by the intake (not a label: label values cannot hold an email), which is how offboarding and pool resolution find a prefix, so the service holds `secrets.list` (metadata) beside its prefix accessor; the invoker grant on the intake follows the same roster as the service's, and is removed by `update`. The OpenRouter upstream gets its own task. The roster is a bucket object (D1940), so the intake holds no database role.
- **R11: pools.** `allowed_users` apply to `fugaro_p_` shared tokens; `owner` is the pool's administrator; migrated pools are legacy-open (D1942). The publisher (a project admin) sets them; per-user tokens are never lent.
- **R12: containers the intake did not create.** The intake writes only secrets whose annotation says it created them and the service reads only those, so a container pre-created by another principal (the deployer can create) is refused, not trusted.
- **R4: the slip cache.** The slip body is cached 5 minutes per the spec; the run's liveness (the outcome)
  and the stop switches are read per call as today (F14), so the 5-minute window bounds only a change to
  the slip's own fields.
- **R5: audit.** Every issued credential and proxied call writes the existing audit line with the added
  fields `slip`, `owner` and `credential`, as new names (no existing field changes meaning).

## 7. Open tensions and open items for the owner

None open beyond the owner's confirmation below. One note for the owner's confirmation: D1931 reverses Q6's "not a name-prefix IAM fence" for
user-supplied credentials only, which the owner's recommended answer already accepts; the intake's
`create` permission cannot be fenced by IAM, which R3 states plainly.

## 8. UNVERIFIED

| # | What |
| --- | --- |
| U1 | Whether the service's forward serves an OpenRouter upstream (config has model providers; credsvc's routes are Anthropic's) |
| U2 | That a user ID token's `sub` is the numeric account ID in every CLI sign-in path |
| U3 | Secret Manager's maximum secret-ID length and whether IAM conditions on `resource.name` apply to `secretmanager.versions.add` and `secrets.delete` as the plan assumes |
| U4 | That a runner heartbeat reliably keeps one Cloud Run instance up (request-based billing, instance idle timeout) |
| U5 | (answered by F22: the agent can reach it; kept for the record) |

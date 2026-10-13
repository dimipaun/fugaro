# Project-level secrets

Status: design, 2026-10-13. Source: the owner's question, "Can we have project level API keys?", raised while moving the sandbox to a new project and re-entering `anthropic-api-key`, `openrouter-api-key` and `bitbucket-token` once per repository. Every fact below is cited to the code read for it; no cloud call was made (Section 7 lists what *would* need one, as owner-run live checks). Plan: [../plans/2026-10-13-project-secrets.md](../plans/2026-10-13-project-secrets.md); its decisions can be vetoed one by one. This PR is docs only.

## 1. What exists today (verified in the code)

A Fugaro project is one GCP project, one Firebase project, many repositories and many people (`docs/project-layer.md:1`). Two things already live at **project** scope, both operator-only, local-config:

- **`providers:`** (`internal/config/providers.go`), each with its own `allow_data_to: [owner/name, ...]` — the owner's single, centralized decision about which repositories may send code to a non-Anthropic model. `docs/multi-model.md` §2 is explicit that this list is the one place the decision lives.
- **`claude_accounts:`** (`internal/config/claude_accounts.go`, shipped 0.6.5, [named-oauth-tokens.md](named-oauth-tokens.md)), each with its own `allow_data_to`, the identical shape, for named Claude OAuth accounts.

**But storage of the value stays per repository for both.** `docs/multi-model.md` §3: "Then run `fugaro secrets set openrouter-api-key` from a checkout of the repository (any listed repository)" — once per repository the provider lists. `named-oauth-tokens.md` §4a: "init --repo prints `fugaro secrets set claude-oauth-token-<name> --repo X`" — again, once per allowed repository. The reason is purely mechanical, not a policy choice: `gcp.SecretID(slug, logical)` (`internal/backend/gcp/names.go:143`) derives every secret's ID from the repository's own slug, and `internal/infra/spec.go:628` (`ws.SecretIDs[m.Logical] = gcp.SecretID(slug, m.Logical)`) is unconditional — **every logical secret name a workflow mounts gets its own Secret Manager secret, created by that one repository's Terraform module** (`deploy/terraform/gcp/modules/repo/secrets.tf`, `for_each = var.repo.secrets`, `prevent_destroy = true`), whether or not its definition (the provider, the account, or nothing at all for a reserved name) is centralized. `workflows.tf:14` feeds the workflow submodule's `var.secrets` exclusively from `google_secret_manager_secret.this`, the repo module's own resource — there is today no code path that lets a job mount a secret any other Terraform root created.

The four **reserved** secrets (`config.ReservedSecrets`, `internal/config/config.go:378`: `bitbucket-token`, `github-app-key`, `claude-oauth-token`, `anthropic-api-key`) have no centralized definition or allow-list at all — any repository may store its own, with no check, which is correct today (a repository's own key needs no permission from anyone). This is exactly why the owner hit the wall at these four names specifically.

What already exists at **project** (GCP-project) Terraform scope, for comparison: the `installation` module (`deploy/terraform/gcp/modules/installation/`, applied by plain `fugaro init`, no `--repo`) — the runs bucket (`bucket.tf`), the `launcher`/`operator`/`job_runner`/`build_submitter`/`tag_mover` custom roles and their project-level `google_project_iam_member` grants (`iam.tf`), the registries, the budget gateway's history account. Notably, the `fugaroLauncher` role already holds **project-wide** `secretmanager.secrets.list` and `secretmanager.versions.list` (`iam.tf:14-19`, comment: "metadata only: reading a value needs secretAccessor, which only the jobs and build accounts hold, per secret") — every launcher can already see the name of every secret in the project, repo-owned or not; only a value read is secret-scoped. This matters for §3: `fugaro secrets ls` needs no new IAM to list a project secret's metadata.

A secret's ID is a flat, project-level namespace in Secret Manager (`Projects.Secrets.Get`/`Create` take `projects/<p>/secrets/<id>`, `internal/backend/gcp/secrets.go:94-128`) — nothing about "which Terraform root created it" is visible to the API. A `secret_id = "some-string"` IAM grant (`google_secret_manager_secret_iam_member`, `deploy/terraform/gcp/modules/workflow/sa.tf:34-41`, `modules/repo/build.tf:13-20`) works identically whether that string names a secret the *same* Terraform state created or one a *different* root (the installation root) owns. This single fact is what makes the rest of this design a Go-and-IAM change, not a new Terraform resource type that crosses module boundaries.

`fugaro secrets set NAME` (`internal/cli/secrets.go:192`) already creates the secret itself if missing (`gcp.Secrets.Set`, `internal/backend/gcp/secrets.go:97-113`: a 404 on Get creates it with the given labels), with Terraform *adopting* a pre-existing, correctly-labelled secret rather than racing it (`secrets.tf`'s own comment: "they hold no value here... The adopted secrets are replicated automatically"). The value is always read from stdin only, never an argument, flag or environment variable (`secrets.go:74-113`), and `secrets ls` has no `Access` call at all — only `List` (metadata).

## 2. The feature

Let a secret's value be stored **once per project** instead of once per repository, for the repositories the owner opts in, while a repository may still store its own value that overrides the project one.

## 3. Decision: the opt-in model (threat model)

**Must stay impossible, whatever is built:**
- a repository the owner has not allowed getting a project secret;
- a provider's key reaching a repository outside that provider's own `allow_data_to` (`CheckProviderPolicy`, `providers.go:324`, is untouched — a shared key does not change who may *pin a model* on it);
- `anthropic-api-key` or `claude-oauth-token` reaching a repository the owner did not opt in;
- a PR author's code reading more than a repo-level secret already lets it read today (the mount is still one env var in one job's environment; nothing changes about what the agent's own process can reach, SECURITY.md "What one run's agent can reach").

**Recommended: reuse what already exists, name-by-name, never widen it (option (c), an explicit allow-list per secret name).**

- **A provider's key or a named Claude account's token already has exactly the needed allow-list**: `providers.<name>.allow_data_to` / `claude_accounts.<name>.allow_data_to`. No new list is introduced for these — the *owner already made this decision once*, centrally, and duplicating it into a second list would only create two sources of truth that could drift. Storing the value with `--project-wide` (§4) makes that *existing* list also govern project-sharing; nothing about who may pin the model, or who may choose the account, changes.
- **The four reserved names have no existing per-name object to reuse**, so a new local-config block, parallel in shape to `providers:`/`claude_accounts:`:

  ```yaml
  # ~/.config/fugaro/projects/<name>.yaml (operator-only, never published — see §6)
  project_secrets:
    anthropic-api-key:  { allow_data_to: [acme/app, acme/sandbox] }
    bitbucket-token:    { allow_data_to: [acme/app, acme/sandbox, acme/web] }
    github-app-key:     { allow_data_to: [acme/app, acme/web] }
    claude-oauth-token: { allow_data_to: [acme/app] }   # the DEFAULT account only; a named one uses claude_accounts' own list
  ```

  `config.ProjectSecret{ AllowDataTo []string }`, `AllowsData`, `ValidateProjectSecrets`, `sortedKeys` — the exact shape of `config.ClaudeAccount` (`claude_accounts.go:63-73,117-143`), reusing `repoSlugRE`/`hasDotSegment`/`pluginwire.Printable`. **Default: no entry, nothing shared** — an installation that adds nothing here is byte-for-byte unchanged (§6).
- `project_secrets` may hold **only** a key of `config.ReservedSecrets` (4 names). `ValidateProjectSecrets` refuses a key that is a provider's `.Secret` or any `config.ClaudeAccountSecret(name)` for a configured account — directing the owner to that object's own `allow_data_to` instead, so there is never a second list to keep in sync for the same secret.
- **`workflows.*.secrets` (a workflow's own, `fugaro.yaml`-declared secrets) stay repo-only.** Recommended, no new mechanism: `layered-config.md`'s scope table already says why — "secrets belong to one repository's Secret Manager entries" (`layered-config.md:112`) — and a workflow secret is reviewed, per repository, in a pull request; two repositories happening to name a secret `db-password` is coincidence, never a shared credential. `SecretMounts` (`spec.go:539-543`) already refuses a workflow secret whose name collides with a provider's; it gains an identical refusal for a `project_secrets` name.
- **The project layer (`fugaro/project-layer.yaml`) is not the vehicle.** It is explicitly a non-secret file — `layered-config.md:165`: "no credential-shaped value... anywhere" is already refused by its own parser — and `agent.oauth_token` is already Repo/Override-only *in that table*, "it names a credential identity that only the repository's own, PR-reviewed `fugaro.yaml` may choose among names the local config (never the project layer) allow-lists" (`layered-config.md:90`). This design does not touch `fugaro.yaml` or the project layer at all: a repository's file and the project layer's `defaults:`/`profiles:` are completely unaware that a secret it mounts came from a project container instead of its own — exactly as a repository is unaware whether its `anthropic-api-key` secret came from `fugaro secrets set --repo` three months ago or was adopted from a bootstrap.

*Veto alternatives considered:*
- **(a) Per-repository opt-in via a `use_shared_secrets` list in each repo's local config entry.** Rejected: it inverts the existing, reviewed shape (allow-list *per secret name*, lived in `providers:`/`claude_accounts:` since 0.6.0/0.6.5) for no gain — the owner would still write exactly the same list of repository names, just keyed the other way, and now split across two kinds of block instead of one per-secret table `fugaro secrets ls`/`doctor` (§4) can show in one place.
- **(b) The project secret applies to every repository by default, with a per-repo opt-out.** Rejected outright: it fails the one non-negotiable rule the owner stated — "the DEFAULT must be safe (nothing shared until the owner says so)" — and would make onboarding a new, possibly-outside-contributor repository silently inherit every existing project key.

## 4. Decision: storage

**Where the container lives.** A new `secrets.tf` in the `installation` module (`deploy/terraform/gcp/modules/installation/`), parallel to the repo module's:

```hcl
# installation/secrets.tf (new)
resource "google_secret_manager_secret" "project" {
  for_each = var.project_secrets   # logical name -> secret ID, Go-derived

  project   = var.project
  secret_id = each.value
  labels = {
    fugaro         = "managed"
    fugaro_project = var.fugaro_project   # reuses the existing label (names.go:66), used today only on the runs bucket
    fugaro_secret  = each.key
  }
  replication { auto {} }
  lifecycle { prevent_destroy = true }
}
# operator grants: secretVersionAdder + viewer, the identical shape to
# deploy/terraform/gcp/modules/repo/secrets.tf's operator_secrets local.
```

`gcp.ProjectSecretID(fugaroProject, logical string) string`, the same `derive()` helper as `gcp.SecretID` (`names.go:101-112`, `sha256(fugaroProject + "\x00" + logical)`), so a project name and repo slug can never collide into the same ID.

**Who writes a version.** `fugaro secrets set NAME --project-wide` (a new flag on the existing command, `internal/cli/secrets.go`), keeping every one of today's guarantees unmodified: stdin-only, hidden prompt on a terminal, never echoed, never an argument or flag value (§9.1's CLI contract). It is refused unless `NAME` is one of `config.ReservedSecrets` with a `project_secrets` entry listing at least one repository, a provider's `.Secret` (its own `allow_data_to` must be non-empty), or a `config.ClaudeAccountBySecret` name (same rule) — never a workflow's own secret, which has no project-wide meaning (§3).

**Who reads.** Never the operator (same as today: `secretVersionAdder`+`viewer`, no `Access` on the secret itself — `secrets.tf`'s own comment, unchanged in kind). Never the launcher/build-submitter roles beyond the metadata already granted project-wide (§1). Only a job's own service account, granted `secretAccessor` **per secret, per job**, exactly as today (`sa.tf:34-41`) — the grant is made from the *repository's own* Terraform root (the installation root does not, and should not, enumerate job service accounts: those are created per repository, in a separate state, §1's "flat namespace" fact is what makes this possible with no new resource type). Concretely: `internal/infra`'s Go code (not Terraform) decides, per logical secret per workflow, which secret ID (`gcp.SecretID(slug, name)` or `gcp.ProjectSecretID(project, name)`) a job mounts (§5); whichever one it picks is what `sa.tf`'s existing, unmodified `google_secret_manager_secret_iam_member.job` grants `secretAccessor` on. **No new Terraform resource for IAM, in either module.**

**`fugaro secrets ls`.** The existing per-repository command (`internal/cli/secrets.go:391-428`) gains a `SCOPE` column: `repo` (today, unchanged, for any secret with no project counterpart or no allow-listing), `project` (this repository mounts the project container), or `project, repo override stored` (a repo-level version exists and wins, §5) — names and version counts only, never a value, for both containers (the metadata call already needs no new IAM, §1). A new `fugaro secrets ls --project-wide` lists the project's own containers: name, versions, and the repositories currently allowed (from local config, not a live call) — an operator-only view in the same spirit as `fugaro accounts ls` (`named-oauth-tokens.md` §12).

**Versions and rotation.** Unchanged in kind: `fugaro secrets set NAME --project-wide` adds a new version the same way a repo-scoped `set` does; every allowed repository's job sees the new `latest` on its next execution (`secret_key_ref { version = "latest" }`, `job.tf:63-64` — unchanged).

**Precedence and deletion (repo overrides project; falls back once the override is gone).** See §5 — this is a live, Go-computed decision at `fugaro init --repo` apply time, not a Terraform-expressible conditional, and deliberately so: see §6's migration argument for why.

## 5. Decision: mounting and resolution

**At deploy time (`internal/infra`), nothing about `rs.Secrets` (the repo module's own, `prevent_destroy`'d containers) changes.** `BuildRepoSpec`/`workflow()` still puts every logical name a workflow mounts into `rs.Secrets` **unconditionally**, exactly as today — this is the one choice that keeps a scope change from ever becoming a Terraform delete (§6). What is new is a **live, apply-time precedence check**, in `internal/infra.Readiness` (`internal/infra/readiness.go`), which already makes exactly this kind of call (`hasEnabledVersion`, used today to gate `DeployJob` per missing secret, `readiness.go:76-106`):

- For each logical name the local config's `project_secrets`/`providers`/`claude_accounts` allow this repository to use project-wide: check (live) whether the repository's **own** container (`rs.Secrets[name]`, already in the spec) has an enabled version.
  - **Yes → unchanged.** The job keeps mounting the repo-owned ID, exactly as today; nothing in `rs.Secrets`'s for_each or the job's `secret_env` changes. This *is* "repo overrides project," expressed as "do nothing different."
  - **No → mount the project ID instead.** `Readiness` rewrites that one workflow's resolved `SecretIDs[name]` (not `rs.Secrets[name]` — the repo container keeps existing, inert, in Terraform state) to `gcp.ProjectSecretID(project, name)`, which the repo's own `sa.tf` IAM grant (§4) then targets. If *that* also has no version, `Missing{Kind: MissingSecret}` names the actual fix: `fugaro secrets set NAME --project-wide` instead of today's `--repo` message.
- **This decision is re-evaluated only on the next `fugaro init --repo`**, the same way `DeployJob` and the job's image are (`readiness.go:55-72`'s own doc comment). Storing a repo override, or deleting its last version, takes effect at the operator's next apply — not at the next run, since Cloud Run Jobs mount secrets at the job, not the execution (the same fact [named-oauth-tokens.md](named-oauth-tokens.md) §1 already established for Claude accounts). This is stated as a limitation, not hidden: `fugaro doctor` warns when a repository's resolved scope (what `Readiness` would compute now) differs from what its deployed job currently mounts, the same shape as the project-layer "last run launched on an older generation" warning (`layered-config.md:179`).

**Named Claude accounts.** Yes, project-level, with no special-casing: `claude-oauth-token-<name>` is just another `config.ReservedSecrets`-shaped name once `ClaudeAccountBySecret` resolves it, and the resolution above is keyed on the logical secret string, not on which object (`ReservedSecrets`, a provider, or a claude account) defined it.

**Workflow `secrets:`** stay repo-only (§3); no resolution logic applies to them.

**Build-time secrets (Cloud Build `--secret id=…,env=…`).** The git credential (`bitbucket-token`/`github-app-key`) is a `BuildSecret` too, and `build.tf`'s `google_secret_manager_secret_iam_member.build` (`modules/repo/build.tf:13-20`) is the **one place that does not go through `var.secrets`** — it indexes `google_secret_manager_secret.this[each.value].secret_id` directly, the repo module's own resource, unlike `sa.tf`'s job grant. **This needs an actual Terraform edit**, not just a Go one: `build.tf:17` must resolve through the same Go-computed, per-name map `sa.tf` already uses (expose it as a `local.secrets` in the repo module, built once, fed to both `build.tf` and `workflows.tf`), so a project-shared git credential's build-time read follows the identical precedence as its job-time read. **Flagged for Terraform-sensitive review (§8):** granting a *build* service account `secretAccessor` on a secret a *different* Terraform root created needs the same scrutiny as the job grant, with an explicit test that a build account never gets broader access than the one secret its own repository currently resolves to.

**`fugaro doctor`/`diagnose`.** Two offline-ish checks, in the existing `doctorCheck` family (`internal/cli/doctor.go`), mirroring named-oauth-tokens' two (`named-oauth-tokens.md` §6f): (1) a `project_secrets`/provider/account name with an allowed repository has no version anywhere reachable (neither its own nor the project's) — red; (2) a repository's last deployed job resolved a different scope than `Readiness` would compute today (the apply-time-only lag above) — a warning, not a failure. Both print the name and the scope, never a value.

## 6. Decision: migration (no backward compatibility needed before release)

Two installs exist today: `dimipaun/fugaro` on project `fugaro`, and the sandbox. **Never `belong`** (the live-checklist's own installation, retired — `docs/gcp-live-checklist.md`'s "Numbering ruling" and its results sections name it; it is explicitly off-limits for any live step, repeated in this task's own instructions).

**There is no `prevent_destroy` trap, by construction — the one subtlety worth spelling out exactly.** Because `rs.Secrets` keeps including a logical name unconditionally even after it becomes project-shared (§5), Terraform's `for_each` over `google_secret_manager_secret.this` **never shrinks** as a side effect of this feature: nothing a plan produces is ever a `delete` of a repo-owned secret resource, so `prevent_destroy` is never tripped by an `init --repo` that follows a `project_secrets`/`allow_data_to` edit. This was an explicit, considered trade-off against the alternative (drop the logical name from `rs.Secrets` once it is project-shared, which plans a `delete` that `prevent_destroy` then refuses outright — not merely a `tf.Guard` refusal requiring `--allow-delete`, but a Terraform provider-level plan error that blocks the apply from doing *anything* until the operator runs `terraform state rm` by hand): the chosen design costs a handful of inert, version-less Secret Manager "secret" resources (effectively free) per repository per project-shared name, in exchange for a migration that can never corrupt or orphan a resource.

**The exact owner sequence, for an existing name (e.g. `anthropic-api-key`) moving from repo-scope to project-scope:**
1. Add `project_secrets.anthropic-api-key.allow_data_to: [...]` to the local config.
2. `fugaro init` (the installation root, no `--repo`): plans **1 create** (`google_secret_manager_secret.project["anthropic-api-key"]`), 0 destroys. Apply it.
3. `fugaro secrets set anthropic-api-key --project-wide`: stores the value once (creates the Secret Manager secret itself if step 2 has not yet run, exactly as `secrets set` does today for a repo-owned one — §4's point that `Set` adopts rather than requires Terraform first).
4. For each allowed repository: `fugaro init --repo` plans **0 destroys** (§5's guarantee) — at most an update to that workflow's job (`secret_env`'s `secret_key_ref.secret` value changes from the repo ID to the project ID, a Cloud Run job update, not a secret delete) **only for a repository whose own copy currently has no version**; a repository that already has its own stored value plans no change at all (it keeps mounting its own, §5).
5. **Optional, separate, owner-initiated cleanup**: once satisfied the project copy is in use, `fugaro secrets set`'s sibling has no "delete the now-redundant repo container" command in this design (deliberately — see §9's non-goals); the operator may `terraform state rm`/delete it by hand, at their own pace, outside any automated flow.

`fugaro init`'s plan output, for both the installation and the repo root, already distinguishes creates from destroys (the live checklist's own "plan 1 create, 2 updates, 0 destroys" lines, `gcp-live-checklist.md:1224`) — this design adds no new plan-summary code, only new resources/fields that summary already knows how to print.

## 7. Compatibility and gates

- **Three edits, as with every new config key**: `localcfg.Config.ProjectSecrets map[string]config.ProjectSecret` (Go struct), `Validate` wiring `ValidateProjectSecrets` (mirroring `ValidateClaudeAccounts`'s call site), and — since `project_secrets` lives in the **local config**, never `fugaro.yaml` — **no JSON Schema edit** (`schemas/fugaro.schema.json` validates `fugaro.yaml` only; the local config has no published schema, matching `providers:`/`claude_accounts:` today).
- **`localcfg.Config.Shared()` strips it**, alongside `Providers`/`ClaudeAccounts` (`internal/localcfg/shared.go:37-38`) — never published to the runs bucket, operator-only (the precedent two tests already pin this shape: `TestMergeSharedNeverCarriesProviders`/`...ClaudeAccounts`, `internal/localcfg/shared_test.go:217,245` — this design adds a third).
- **Strict parsers refuse an unknown key.** `task.Spec` is unaffected — this feature adds no new task field (the resolved secret ID is a Terraform/job-spec concern only, never a per-run choice the runner sees, unlike `OAuthToken`). The `*Since` gate pattern (`recipesSince`/`layeredSince`/`namedTokensSince`, `internal/cli/recipes_skew.go`) therefore does **not** apply here in the same way: an older runner's environment simply has the variable name it already expects (`ANTHROPIC_API_KEY`, `CLAUDE_CODE_OAUTH_TOKEN`, …), pointed at a different secret ID — nothing about the runner's own code needs to know the difference. The gate that *does* matter is a plain Terraform-version one: a repository whose deployed job predates this feature's `infra`/Terraform code needs `fugaro init --repo` re-run before project-sharing takes effect for it (§5) — already true of every `infra` change, with no new constant needed.
- **`docs/release.md`'s checklist** gains one bullet, in the same family as the `gcpProjectFieldSince`/`recipesSince` bullets (`docs/release.md:55-58`): confirm the release note names the sequence in §6, and that an installation applying the installation root for the first time after this feature ships sees the new `secrets.tf` resources as **creates**, never **updates to an unrelated resource** (a regression in `installation`'s other resources would show as an unexpected diff there).
- **The job-image version gate does not change.** The runner's own code and `task.Parse` are untouched (previous bullet); no image rebuild is required purely for this feature (only a Terraform re-apply, §6).
- **Shared config publication**: non-secret only, as already true of everything in `fugaro/config.yaml` — `project_secrets`' *names and allow-lists* are never published (they are local-config, operator-only, same as `providers:`/`claude_accounts:`, previous bullet); only the *fact* that a repository resolves a given scope could in principle be surfaced through `doctor`/`ls` (§5), never through the shared config object.

## 8. Live checks (owner tasks, `fugaro`/sandbox installations only — never `belong`)

Reserving the next free number: `docs/gcp-live-checklist.md`'s "Numbering ruling" (`gcp-live-checklist.md:183-189`) has Check 38 as the last taken (the generic-tool plan's clone-workflow check, after the named-account checks 34-37; Check 39 is the retire-image-mirror design's `39a`/`39b`/`39c`). **This design reserves Check 40**, added to the checklist by the plan (its own task), covering:

- **40a.** A project secret, stored once with `--project-wide`, reaches an opted-in repository's job (the variable is set, from the project ID, confirmed via `jobs.get`'s `secret_key_ref`, never by reading the value).
- **40b.** A repository **outside** the allow-list is refused at `fugaro secrets set --project-wide` with a message naming the missing `allow_data_to` entry, and its job plans/deploys with no such variable at all.
- **40c.** A repository with its **own** stored version keeps mounting it (precedence holds) even though the project copy also exists and it is listed in `allow_data_to`; deleting the repo version's last enabled version and re-running `fugaro init --repo` flips it to the project copy (§5's fallback).
- **40d.** The data-policy gate is unweakened: a provider model pinned by a repository outside that provider's `allow_data_to` is still refused by `CheckProviderPolicy` even when the provider's key is project-shared and that repository could otherwise reach the secret through some other workflow.
- **40e.** No value appears in any log, `diagnose`, `secrets ls` output, or Terraform plan/state diff printed to a terminal, for either container.

## 9. Release placement and non-goals

**0.7.x**, after the base-image hard cut already on `main`/`next` (the current line, per this PR's own target). Depends on: the owner applying the installation root's Terraform once (a one-time, operator-run `fugaro init`, §6 step 2) — no functional dependency on any other in-flight design. No new Check-number conflicts (§8 reserves 40 cleanly after 38/39).

**Not in this design:** a command to delete/decommission a now-redundant repo-owned container (§6 step 5 stays a manual, operator-paced action); a per-project secret for `workflows.*.secrets` (§3); any change to the project layer, `fugaro.yaml`'s schema, or `task.Spec`; a budget/accounting dimension (none of these secrets are dollar-metered beyond what already exists per repository).

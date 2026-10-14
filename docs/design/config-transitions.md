# Config transitions that retire cloud resources

Status: design, 2026-10-14, **docs only — no product code in this PR**. Source: a real transition the owner performed on 2026-10-14, converting the `dimipaun/fugaro` repository from `agent.auth: oauth` to `api-key` with DeepSeek models. Every fact below is cited to the code or the doc read for it; no cloud call was made (Section 6 says which facts only a live run can settle, as owner-run checks). Plan: [../plans/2026-10-14-config-transitions.md](../plans/2026-10-14-config-transitions.md); its decisions can be vetoed one by one. This PR is docs only.

Related: [project-secrets.md](project-secrets.md) (#329) shares this design's hazards — a scope move replaces IAM members and never destroys a container, and a narrowed `allow_data_to` can shrink a secret map — and this document keeps its vocabulary and cross-references it throughout. [v1.md](v1.md) §8.2 is the authority on `fugaro init`'s flow; this design changes nothing there that it does not name.

## 1. The origin (2026-10-14, verified against the code)

The owner changed `agent.auth` from `oauth` to `api-key` (DeepSeek through OpenRouter) in the `dimipaun/fugaro` checkout's `fugaro.yaml`, and ran `fugaro init --repo .`. What they got was raw Terraform text, before any plan list:

```
Error: Instance cannot be destroyed
  on secrets.tf line 8, in resource "google_secret_manager_secret" "this":
   8: resource "google_secret_manager_secret" "this" {
Resource module.repo.google_secret_manager_secret.this["claude-oauth-token"] has
lifecycle.prevent_destroy set, but the plan calls for this resource to be destroyed.
```

and the same for `claude-oauth-token-bmb`. **The user could not see what else the plan wanted to do**, because Terraform aborts a plan on `prevent_destroy` before it prints the resource list.

What the plan actually wanted — visible only after the two secrets were forgotten by hand — was **2 imports, 6 creates, 3 updates and 11 deletes**:

- **2 imports**: the new api-key secrets (`anthropic-api-key`, and the provider key), created by `fugaro secrets set` through the Secret Manager API and adopted by label.
- **6 creates**: operator and job-accessor IAM members.
- **3 updates**: the surviving jobs' specs.
- **11 deletes**: the old named-account variant `module.repo.module.workflow["go_bmb"]` (its Cloud Run job, its service account, its launcher grant, two secret accessors and its runs-bucket grant — six addresses) and the IAM bindings tied to the two OAuth secrets.

The owner resolved it by hand, in this order:

1. `terraform state rm` of the two OAuth secret containers and of `module.repo.module.workflow["go_bmb"].google_cloud_run_v2_job.this[0]`, in the repository's workdir (`$XDG_STATE_HOME/fugaro/terraform/<gcp-project>/repos/<slug>/gcp/roots/repo`, after `terraform init -backend-config=backend.hcl` with `TF_CLI_CONFIG_FILE=<workdir>/terraformrc`). Two different hazards forced this: a secret has `prevent_destroy`, and **a job whose workflow has left the configuration keeps `deletion_protection = true` in the state**, so `--allow-job-delete` can no longer lower it (`--allow-job-delete` is a *variable* of an apply that still contains the job; once the job is gone from the configuration there is no resource left to carry the variable).
2. `gcloud run jobs delete` of the orphaned job.
3. `fugaro init --repo . --allow-delete <11 addresses>`.

The OAuth tokens stayed in Secret Manager: never destroyed, and now unmanaged. Nothing in the product told the user any of this. The only documentation is the manual outline in [gcp-setup.md](../gcp-setup.md) ("Turning checks off, or removing a workflow", "Offboarding a repository"), which says the order matters — lower deletion protection **before** changing `fugaro.yaml` — and which a user who has already changed the file, as this one had, cannot follow.

**The three failures this design closes.** (1) The failure is surfaced as Terraform's own text, which names a lifecycle attribute rather than the config change that caused it. (2) The failure comes *before* the resource list, so the user cannot see the blast radius, and cannot decide. (3) There is no supported path at all: the only way through is `terraform state rm` by hand in a workdir that `PrepareWorkdir` overwrites on the next run (`internal/infra/workdir.go`, which `RemoveAll`s `dir/gcp` and rewrites the embedded tree afresh).

## 2. Inventory: which config changes retire resources

Read from `deploy/terraform/gcp/modules/{repo,workflow,installation}` and `internal/infra/spec.go`, not guessed. The address prefix `module.repo` is the repository root; `module.repo.module.workflow["<variant>"]` is one workflow variant.

**What one workflow variant owns** (`modules/workflow`): `google_service_account.job`; `google_service_account_iam_member.operator[<operator>]` (one per operator); `google_storage_bucket_iam_member.job` (the runs-bucket condition); `google_secret_manager_secret_iam_member.job[<logical>]` (one per mounted secret); `google_project_iam_member.vertex[0]` (only when `agent.auth: vertex`); `google_cloud_run_v2_job.this[0]` (`deletion_protection = !var.allow_job_delete`, `job.tf:15`); `google_cloud_run_v2_job_iam_member.launcher[<launcher>]` (one per launcher).

**What the repository root owns besides those** (`modules/repo`): `google_secret_manager_secret.this[<logical>]` for every logical name in `rs.Secrets` (**`prevent_destroy = true`**, `secrets.tf:22`); `google_secret_manager_secret_iam_member.operator[…]` (two per secret × operator: `secretVersionAdder` and `viewer`); `google_artifact_registry_repository.images` (**`prevent_destroy = true`**, `registry.tf:59`); `google_artifact_registry_repository_iam_member.operator_images[<operator>]`; the build account and its grants (`google_service_account.build`, `…_secret_iam_member.build[…]`, `…_registry_repository_iam_member.{build_images,build_tag_mover,build_base}`, `google_project_iam_member.build{log_writer,build_submitter}`, `google_service_account_iam_member.{build_self,operator_build[…]}`); and, when any workflow is checked, `google_cloud_run_v2_job.check[0]` (**`deletion_protection = !var.allow_job_delete`**, `check.tf:27`), `google_cloud_run_v2_job_iam_member.check_invoker[0]` and `google_cloud_scheduler_job.check[0]`.

The rows below use these two abbreviations: **PD** = a `prevent_destroy` container (Terraform refuses the plan outright, before `tf.Guard` ever sees it), **DP** = a `deletion_protection` job (the *provider* refuses the delete at apply time unless the protection was lowered by an earlier apply, while the resource was still in the configuration).

| # | Config change | Resources that leave | PD / DP | Data at stake | Reverse transition |
|---|---|---|---|---|---|
| **A** | `agent.auth: oauth` → `api-key` | Every named-account variant `module.workflow["<wf>_<account>"]` (`gcp.WorkflowVariant` joins with `_`, `internal/backend/gcp/names.go:285`; `Repo()` only builds them when `Auth == "oauth"`, `spec.go:797-800`) — job, SA, launcher/operator grants, bucket grant, secret accessors. Each surviving variant's `…_secret_iam_member.job["claude-oauth-token"]`. `rs.Secrets` loses `claude-oauth-token` and every `claude-oauth-token-<name>` (`ClaudeAccountSecret`, `claude_accounts.go:80`), so their containers, their operator grants and any build grants go. | **PD** on each secret container. **DP** on each retired variant job. | The OAuth **token values**, in Secret Manager, never in Terraform. Fugaro never deletes them. | Yes, and cleanly: the containers still exist and carry the `fugaro=managed` / `fugaro_repo` / `fugaro_secret` labels `fugaro secrets set` writes, so `DiscoverRepo` re-imports them; the variants come back as creates. But it retires the *api-key* container in turn, so it is the same shape again. |
| **B** | `api-key` or `oauth` → `vertex` | The Claude credential mount and its accessors (`SecretMounts` has no `vertex` case, `spec.go:561-566`), so `anthropic-api-key` (or the OAuth names) leaves `rs.Secrets` → container. `oauth` → `vertex` also retires the named variants. | **PD**; **DP** for the variants. | The provider/Claude key value. | Yes: `vertex` → `api-key` re-adopts the labelled container and re-creates the mount. The installation also gains/loses `enable_vertex` (`v1.md` §8.1) — an update, not a retirement. |
| **C** | Removing a workflow from `fugaro.yaml` | Every variant of that workflow. If it was the last **checked** workflow, the check job, its invoker grant and its Scheduler job too (`Repo()` only sets `rs.Check` when some workflow has `checkout != clone` and `rebuild.check != off`, `spec.go:811-816`). Any secret only it mounted leaves `rs.Secrets` → container. | **PD**; **DP** on the workflow job and the check job. | None: the derived image stays in the registry (nothing deletes it) and `runs/` objects are untouched. The workflow-only secret's **value** is at stake if the container were destroyed — it is not. | Yes: re-adding re-creates the job and variants; a secret that was forgotten is re-adopted by label. The check job comes back **paused** (`v1.md` §8.1). |
| **D** | Removing one `secrets:` entry from a workflow | That variant's `…_secret_iam_member.job["<logical>"]`. If no other workflow mounts it, the logical name leaves `rs.Secrets` → container, its two operator grants per operator, and any `…_secret_iam_member.build[…]`. | **PD** on the container. | The secret's **value**, which Fugaro must never delete. | Yes: re-adding the entry re-adopts the labelled container. If the container *was* destroyed the value is gone — which is exactly why it must not be. |
| **E** | A workflow `checkout: baked` ↔ `clone` | `baked` → `clone` drops the workflow from `checked`; if it was the last, the check job, invoker grant and Scheduler job go. `clone` → `baked` creates them (paused). The job's `image` changes in place (a `⚠` sensitive update, `internal/infra/tf/summary.go`'s `containers.image`). | **DP** on the check job when it leaves. No PD. | Image versions stay in the registry; the base registry keeps its images. | Yes: the check job is re-created and its schedule stays paused until every checked workflow has a build record. |
| **F** | A provider, or a named Claude account, dropped from the **local** config (or its `allow_data_to` narrowed) | The provider's key (or the account's `claude-oauth-token-<name>`) leaves `SecretMounts` → leaves `rs.Secrets` → container, operator grants, build grants. A narrowed account list also retires that account's workflow variants. | **PD**; **DP** for retired variants. | The key's **value**. | Yes, by label. **This row is the identical hazard [project-secrets.md](project-secrets.md) §5 N3/N4 already fixes**, by discovering and unioning every correctly-labelled existing secret back into the computed map so it can only grow, never shrink. This design adds no second mechanism and must not: §7 says how the two ship together. |
| **G** | `--base-image KIND=IMAGE`, `--registry-cleanup dry-run\|on\|off` | **Nothing.** `base_images` is installation tfvars; a cleanup change updates `cleanup_policy_dry_run` (a `⚠` sensitive update). | The base registry and every repository registry keep `prevent_destroy`, which matters only at offboarding. | Image **versions**, but by Artifact Registry's own cleanup policy, never by Terraform's plan. | n/a — not a retirement. Listed so the inventory is complete and a reader does not expect a summary here. |
| **H** | A project-level secret **scope move** (#329) | Nothing of ours leaves: `rs.Secrets` never shrinks by construction. But `google_secret_manager_secret_iam_member`'s `secret_id` **forces replacement**, so each affected address plans `["delete","create"]`. | Neither PD nor DP — the refusal is `tf.Guard`'s, at the plan level. | None: the value never moves. | Yes: moving back replaces the same addresses again. |

**What this inventory settles.** Only two protections are in play, and they fail at different times and in different ways: `prevent_destroy` fails **at plan time, before anything is listed**, and `deletion_protection` fails **at apply time, after the plan was shown and confirmed**. Every retirement in rows A–F has one or both. That asymmetry is why §3 must compute the list itself rather than read it off Terraform's output, and why §4(b) needs its own mechanic for jobs.

## 3. Detection and explanation

### 3.1 Detect from the state and the spec, before Terraform runs

`fugaro init --repo` already computes, in Go, the complete spec and therefore the complete set of addresses Terraform will manage: the tfvars come from `infra.Repo` (`spec.go:747`) and the imports from discovery (`internal/infra/imports.go`, whose `importTable` is a per-kind address template). It already reads the state (`TF.ShowState`, `internal/infra/tf/tf.go:246`, giving `State.Addresses()`, `tf.go:219`).

So the retirement set is a set difference, computed with no Terraform run at all:

```go
// package infra
// Retirement is one resource the new configuration no longer produces.
type Retirement struct {
    Address string
    Kind    string // "secret container", "workflow job", "check job", …
    Cause   string // the config change, in the user's words
    Kept    string // what survives: "the secret and its value stay in Secret Manager"
}

// Retirements is the addresses the state manages that spec no longer
// produces. Both sides are computed by fugaro; the user types neither.
func Retirements(state []string, spec RepoSpec) []Retirement
```

`spec` needs one new method — the addresses it will produce — which is the same shape `importTable` already uses:

```go
func (rs RepoSpec) Addresses() []string
```

**Why this and not Terraform's plan.** Terraform's plan is authoritative for what is *applied*, but it is exactly what is unavailable in the origin case: the plan aborts on `prevent_destroy` before listing anything. The Go-side set is available earlier, is deterministic, and is what the consent is about. It is a **prediction used for explanation and consent**, never a substitute for the guard: §4(d) still runs `tf.Guard` on the real plan, and §4(e) still refuses anything the prediction did not name.

### 3.2 The retirement summary

Printed **instead of** Terraform's text, before the plan, whenever `Retirements` is non-empty. Shape (this is the origin case, with the numbers the owner actually saw):

```
⚠ This change retires 11 resources (project dimipaun, GCP project dimipaun-fugaro, repository dimipaun/fugaro).

Why: agent.auth changed from oauth to api-key, so the named Claude account
"bmb" is no longer a variant this repository deploys, and the OAuth secrets
are no longer mounted.

Going away (11):
  module.repo.module.workflow["go_bmb"]            the job, account and grants of workflow "go"
                                                   as account "bmb" (6 addresses)
  module.repo.google_secret_manager_secret_iam_member.operator[…]  grants tied to the two OAuth secrets

KEPT — Fugaro never deletes a secret or a secret version:
  claude-oauth-token       stays in Secret Manager, unmanaged, with its value
  claude-oauth-token-bmb   stays in Secret Manager, unmanaged, with its value
  Their containers carry the fugaro labels, so re-adding oauth later re-adopts them.

Order of operations:
  1. forget the 2 secret containers and the 1 orphaned job (they carry
     prevent_destroy / deletion_protection, so Terraform cannot delete them)
  2. apply: 2 imports, 6 creates, 3 updates, 8 deletes
  3. delete the 1 orphaned Cloud Run job that step 1 left unmanaged

Next command:
  fugaro init --repo . --retire
```

Every line is required by the origin's three failures: *what* is going (the address list, which Terraform never printed), *why* (the config change, which Terraform's text never named), *what is kept* (so the user does not fear for their tokens), and *the exact next command*.

### 3.3 When Terraform aborts early anyway

Two guards, both cheap:

- **The summary is computed before `t.Plan`**, so in the normal path the raw text is never shown.
- **If `t.Plan` fails**, `fugaro init --repo` matches the failure against the `prevent_destroy` signature (`has lifecycle.prevent_destroy set, but the plan calls for this resource to be destroyed`) and, when it matches, prints the retirement summary and the next command **instead of** Terraform's stderr, and exits 1. This is the fallback for the case where the Go-side prediction and the real plan disagree — for example a hand-edited workdir, or a `prevent_destroy` trip this design's inventory does not model. Terraform's own text stays in the `--json` result's `failed[].error` and in the workdir's logs, so nothing is hidden, only re-framed.
- **To see the full delete list when nothing else can produce it**, `fugaro init --repo --retire --plan-only` writes the forgets (§4a), plans, prints the complete summary and applies nothing. This is the same discipline `--plan-only` already holds everywhere else (`init.go:364`), and it is the one place the two-phase plan is allowed to run without a confirmation, because it changes nothing.

## 4. A supported, guarded path

### 4.1 Command shape: three options

- **Option A — a mode of `fugaro init --repo` (`--retire`).** *Recommended.* The state, the workdir, the spec, the plan file, the guard and the confirmation all already live in `init --repo`; the user is already in that flow; and the config change that caused the retirement is the same checkout's `fugaro.yaml`, so the two cannot drift apart. The cost is one more flag on a command that already has many, and a mode inside a flow whose existing confirmations must not be weakened.
- **Option B — a dedicated `fugaro repo retire`.** Cleaner separation and its own banner, and it reads better in documentation. But it duplicates the workdir, spec, discovery and plan plumbing (all of which are `init --repo`'s, not a subcommand's), and it splits one intention — "my config changed, make the cloud match" — into two commands a user must sequence correctly, with the risk that it is run without the config change, or that the config change is applied by a later plain `init --repo` that the user never associated with it.
- **Option C — document it only.** *Rejected.* The origin is the evidence: the documentation exists (gcp-setup.md), the user had already passed the point where it could be followed, and the only feedback the product gave was a Terraform lifecycle error. Documentation is not a supported path.

### 4.2 Mechanics

**(a) Forgetting a retired secret container.** Fugaro writes a `removed` block into its own workdir — a generated file beside `imports.tf.json`, written the same way (`WriteImports`, `internal/infra/imports.go:142`, is the precedent: `imports.tf.json`, sorted, rewritten on every run so an earlier run's file is replaced):

```json
{ "removed": [ { "from": "module.repo.google_secret_manager_secret.this[\"claude-oauth-token\"]",
                 "lifecycle": { "destroy": false } } ] }
```

The addresses are **computed by fugaro from `Retirements`**, never typed. `removed` needs Terraform ≥ 1.7, which is already the supported floor (`tf.go:81`, `>= 1.7.0, < 2.0.0`). A `removed` block with `destroy = false` produces the plan action `forget`, which `tf.Guard`'s `knownActions` already knows and already allows — its own comment says so: "forget, from a removed block, destroys nothing and is allowed" (`internal/infra/tf/guard.go:14-16`). **So this mechanic does not weaken the guard, and needs no new action.** The typed confirmation names every secret that will be left in Secret Manager, by logical name and by Secret Manager ID, so the user consents to what stays behind, not merely to what goes. `removed.tf.json` is emptied on every later run, exactly like `imports.tf.json`, because once the forget is applied the address is out of state and Terraform ignores the live container entirely.

**(b) A job whose workflow has already left the configuration.** Three options, all considered:

- *(i) An ordering pre-step.* `fugaro init --repo --allow-job-delete` before editing `fugaro.yaml` — an apply whose only change is `deletion_protection = false`. This is what gcp-setup.md already prescribes, and it is the **cheapest and least invasive** path. But it is usable **only if the user has not yet changed the file**, which is precisely the case the origin is not. Keep it as the recommended order for anyone who reads ahead; it cannot be the only path.
- *(ii) Forget, then delete through the API.* Write a `removed` block for the job address with `destroy = false`, apply, then delete the live job. *Recommended as the fallback, and the one that covers the origin.* It is the hand procedure the owner ran (`state rm` + `gcloud run jobs delete`), made supported.
- *(iii) Keep the resource in the configuration for one apply.* Impossible: the workflow has left `fugaro.yaml`, so `Repo()` cannot produce it, so there is no resource for the variable to act on. Rejected as unbuildable, not as unsafe.

**(c) The unmanaged leftovers after a forget.** Two options: Fugaro deletes the orphaned job through the Cloud Run Admin API after a typed confirmation, or it prints the exact `gcloud` command. **Recommended: Fugaro deletes it**, because the whole point of this design is a guided path and because the job is unambiguously ours — it carries `fugaro=managed`, `fugaro_repo=<label>` and `fugaro_workflow=<name>` (`modules/workflow/job.tf:11-14`). The delete is guarded the same way discovery's refusals are (`v1.md` §8.2: a resource under one of our names that exists **and** carries our mark is adopted; one without the mark is refused): **Fugaro reads the job's labels first and refuses if they are not ours**, so this can never delete someone else's job. The `gcloud` command is still printed, in the summary, as the manual fallback and as the record of what happened. *This is one of §8's open questions: an owner may prefer printing the command only.*

**(d) The `--allow-delete` list, derived automatically.** The retirement mode computes the guard's allow-list from the real plan's deletes and replaces **intersected with `Retirements`** — so an address the summary did not name is never allowed, and the guard still refuses it. This is the same shape as two existing, shipped precedents: `infra.ForgetAllowDelete` (the rollback's allow-list, `internal/infra/forget.go:46`) and `infra.HardeningAllowDelete` (0.7.0's bucket hardening, `internal/infra/hardening.go:160`), the latter of which also prints its own banner naming what the deletes mean (`HardeningBanner`). The user never pastes 11 addresses; they read one list and type one confirmation.

**(e) The guard stays.** Nothing in this design weakens an existing guard:

- `tf.Guard` is untouched. `--allow-delete` keeps its exact meaning; retirement only *computes* it, from a list the user confirmed.
- Retirement is an **explicit consent mode with its own banner** (`⚠ RETIRE`, not `⚠ CONFIRM`), in the family of the 0.7.0 hardening banner and `--forget`'s two-step confirmation.
- It follows the covered/uncovered rules like every other step: `--yes` **never** covers it, `--non-interactive` never covers it, `--json` never covers it, and it needs the user's own terminal — the same rule `canConfirmRepo`/`confirmRepo` already implement for onboarding a repository (`init_repo_gate.go`) and the secrets stage implements for values.
- **It is refused from a coding agent's session exactly like applies are today**, through the same `agentEnv(os.Getenv)` marker (`initsecrets.go:146`), and for the same stated reason (`init.go:371`): the marker is a mitigation, not a barrier, and the real control is the typed confirmation.
- `--allow-job-delete` keeps its meaning and its own `--repo`-only guard (`init.go:685`); retirement does not set it behind the user's back. Where a job must be forgotten rather than unprotected (§4b(ii)), the summary says why.

## 5. Where users learn

1. **Error and warning texts** — the retirement summary (§3.2) and the `prevent_destroy` re-framing (§3.3). Neither shows raw Terraform text; both name the cause and the command. The `--json` document gains `retire` (the `[]Retirement` list) so a scripted user sees the same facts, and keeps `failed[].error` as the raw Terraform text.
2. **A docs section** in `docs/gcp-setup.md`: "Changing auth, or removing a workflow, secret or provider", with the ordered steps and this worked example (the 2026-10-14 oauth→api-key transition, its 11 deletes and the two secrets that stayed). The existing "Turning checks off, or removing a workflow" and "Offboarding a repository" sections are **edited to point at it** rather than kept as a second, divergent outline; the "do it in this order" advice stays, restated as "the order matters, and `fugaro init --repo` now tells you when you have passed it".
3. **The setup and upgrade skills** (`plugin/skills/setup`, `plugin/skills/upgrade`): each gains the refusal text an agent will see, with the instruction not to attempt the transition from an agent session and to hand the user the `--retire` command instead. The skills lint (`plugin/skills_lint_test.go`, `internal/cli/skills_test.go`) is updated with them.
4. **`fugaro validate` and `fugaro init --plan-only`** warn when the checkout's configuration, compared against what the project's state holds, would retire resources — the same warning the summary gives, in one line, so the user learns before the failure rather than at it. `fugaro doctor` reports it where it can: doctor has no state access today, so this is a **plan-only/validate** warning and a doctor note that names `init --plan-only`, not a new doctor state read.
5. **`docs/gcp-live-checklist.md` Check 42** (owner-run, sandbox): the oauth → api-key → oauth round trip with the secrets retained and re-adopted (§6).

## 6. Safety and testing

**Safety invariants.**

- **Fugaro never deletes a secret or a secret version.** No new code path calls `secrets.delete`, `versions.destroy` or `versions.delete`; the `removed` blocks this design writes carry `destroy = false`, and the containers keep `prevent_destroy`. A forgotten secret stays in Secret Manager with its value.
- **A forgotten secret must be re-adoptable by label.** `DiscoverRepo`'s existing labelled-secret discovery is what makes the reverse transition clean; this design adds no new label, no new mark and no registry of its own, so re-adoption is the *existing* path, unchanged.
- **Nothing weakens a guard** (§4e).
- **State and cloud never disagree silently.** §6's failure injection is the test of this, not an aspiration.

**Tests, in the repository's existing patterns.**

- **A fake terraform runner** (`internal/infra/tf/fake_test.go`, `internal/cli/init_test.go`'s fakes) plus **golden plan JSON** (`internal/infra/tf/cover_golden_test.go` is the pattern): the origin transcript as a fixture — the `prevent_destroy` failure text, the two secret addresses, the `go_bmb` variant and the 11-delete plan — asserting that `init --repo` prints the retirement summary and never the raw text.
- **The user-changed-the-file-first order**, which is the origin: the workflow has already left `fugaro.yaml` and the job still carries `deletion_protection = true` in state. Asserts the forget-then-delete-by-API path is chosen, and that the `--allow-job-delete` pre-step is *not* offered as if it were still available.
- **A baked/clone transition** (row E), asserting the check job, its invoker grant and its Scheduler job appear in the retirement set exactly when the last checked workflow leaves.
- **A reverse transition** (row A in reverse), asserting the labelled containers are re-imported and the named variants come back as creates, with **zero** deletes of any `google_secret_manager_secret.*` resource.
- **Failure injection**: a `removed`/`state rm` that fails half-way. The rule to pin: the run reports exactly which addresses were forgotten and which were not, writes nothing else, and a rerun resumes rather than re-forgetting — never leaving state and cloud disagreeing without saying so. The two-phase shape (`removed` blocks → apply → API delete) makes this checkable, because each phase is idempotent and the second names what the first left.
- **The guard's own behaviour when the plan aborts early** is pinned by a test, not merely documented: a plan that cannot be produced prints the summary, exits 1, and leaves the workdir's state untouched.

**What only a live run can settle** (owner-run, Check 42): that a real `prevent_destroy` plan failure is matched by the signature this design parses; that a `removed` block with `destroy = false` really produces the `forget` action the guard allows, on the real provider; that `gcloud run jobs delete` (or the API call) succeeds on a job Cloud Run still considers protected once it is out of state; and that the oauth→api-key→oauth round trip leaves both secrets in Secret Manager with their values and re-adopts them.

## 7. Release placement and compatibility

**Placement: 0.7.2**, and its one real dependency is the project-secrets work ([#329](project-secrets.md), plan `2026-10-13-project-secrets.md`). Row F of the inventory is the same hazard that design's N3/N4 fix closes, and its `Existing.ExtraSecrets` union is what makes "a secret leaves `rs.Secrets`" impossible for a config-driven reason. Shipping this design **before** that one would mean two mechanisms for one invariant, which is exactly what this design's §2 refuses to do. If the owner prefers to ship the detection and explanation alone — §3, which needs no discovery change at all — that is a coherent **0.7.1**, with §4's retirement mechanics following in 0.7.2. *This is §8's open question.*

**Compatibility.** No new `fugaro.yaml` key, so the three-edit rule (schema, docs, examples) is not triggered and no `*Since` gate is needed for a repository's own file. `--retire` is a CLI flag, not a configuration key, so an older binary simply does not know it — and an older binary keeps failing the way it does today, which is the status quo, not a regression. If a `*Since` constant turns out to be needed (for example if the forget mechanism must be gated on a repository's last `init --repo` having been run by a binary that writes `removed.tf.json`), it follows `docs/release.md`'s existing format, and §4's plan says where.

## 8. Open questions for the owner

1. **§7 — 0.7.1 (detection and explanation only) or 0.7.2 (with the retirement mechanics)?** The detection half has no dependency on #329; the mechanics half has one.
2. **§4(c) — does Fugaro delete the orphaned job through the Cloud Run API after the typed confirmation, or only print the exact `gcloud` command?** The recommendation is to delete it, behind the label ownership check; printing only is the more conservative alternative and costs the user one copy-paste.
3. **§4(b) — should the ordering pre-step (`--allow-job-delete` before editing `fugaro.yaml`) be actively *offered* when the file has not yet changed**, or left to the documentation? Offering it is friendlier and cheaper (no forget, no API delete); leaving it out keeps one path rather than two.
4. **§3.1 — is a Go-side address inventory (`RepoSpec.Addresses`) acceptable as the source of the consent list**, given it is a prediction and the guard still runs on the real plan? The alternative is to always run the two-phase plan (write the forgets, plan, read the real list), which is authoritative but slower and touches the workdir on every run.
5. **§5 — the `docs/gcp-setup.md` restructuring**: this design proposes rewriting the two existing manual sections to point at one new section. The owner may prefer to keep them as they are and add the new section alongside.

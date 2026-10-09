# Runs-bucket IAM hardening: only operators write `fugaro/`

Status: design for review (2026-10-08). The owner ruled that this ships in release **0.7.0**, together with the base image consolidation ([base-image.md](base-image.md), [plans/2026-10-08-base-image.md](../plans/2026-10-08-base-image.md)). It is the "follow-up hardening" that layered config's decision L7, option A, names ([layered-config.md](layered-config.md) §11). Every choice below is a decision the owner can veto (H1 to H14, §12). The plan is [plans/2026-10-08-bucket-iam.md](../plans/2026-10-08-bucket-iam.md).

**Ruling (2026-10-08).** Decisions H1 to H14 are **all taken as recommended**. No veto alternative in §12 is chosen.

## 1. Problem

The runs bucket `fugaro-runs-<gcp_project>` holds two kinds of data:

- **Run data,** which every launcher must write: `runs/<slug>/<run>/`.
- **Installation data,** which steers what runs everywhere:
  - `fugaro/project-layer.yaml`, the project layer. Its profiles may carry `commands.*`, `image.apt` and `image.setup` (L7, option A).
  - `fugaro/config.yaml`, the shared config.
  - `fugaro/recipes/`, the project recipes.
  - `fugaro/project.json`, the project marker.
  - `builds/<slug>/`: the build records, `check.json`, and the per-repository layer copy `project-layer.yaml` (L6). The check job and Cloud Build read that copy.
  - `cache/<slug>/`, which runs restore, and `locks/<slug>/`.

Today `modules/installation/iam.tf` grants `roles/storage.objectAdmin` on the whole bucket to every launcher and every operator. So anyone allowed to launch can, with `gcloud storage cp`, do any of the following:

- **Rewrite the project layer** and change the build and test commands of every repository that takes a profile. Or add an `image.setup` that runs in Cloud Build, with the build secrets mounted, at the next daily rebuild, without any launch.
- **Rewrite a repository's layer copy** in `builds/<slug>/`. The check job resolves against the copy and hands its sha256 to the build (layered-config §8), so this has the same effect for that repository. The canonical object stays untouched, so the change is not visible there.
- **Poison `cache/<slug>/`,** which other people's runs restore into their workspace.
- **Relax the shared config's budget block** for teammates without a local config ([shared-config.md](shared-config.md) §13).
- **Replace a recipe or the project marker.**

The 0.6.0 guard `config publish --executable-changes` is a CLI check. It binds only people who go through the CLI.

**Goal.** Only operators (and the accounts the modules already scope) write installation data. Launchers keep exactly what they need: reading everything they read today, and writing their runs' objects under `runs/`.

## 2. What exists today (verified in the code, `origin/main` 65a6a24)

### 2.1 Principals and their grants on the runs bucket

| Principal | Grant on the runs bucket | Where |
|---|---|---|
| Launchers (`terraform.launchers`) | `roles/storage.objectAdmin`, no condition | `modules/installation/iam.tf`, `google_storage_bucket_iam_member.runs`, `for_each = toset(concat(var.launchers, var.operators))` |
| Operators (`terraform.operators`) | the same resource (a member in both lists gets one grant) | same |
| Each workflow's job account | `roles/storage.objectUser`, conditioned on `runs/<slug>/`, `cache/<slug>/`, `locks/<slug>/` | `modules/workflow/sa.tf`; the expression is `gcp.BucketCondition(bucket, gcp.JobBucketPrefixes, slug)` (`internal/backend/gcp/names.go`), passed byte for byte |
| Each repository's build account (Cloud Build and the daily check job, `modules/repo/check.tf` runs as it) | `roles/storage.objectUser`, conditioned on `builds/<slug>/` | `modules/repo/build.tf`, `gcp.BuildBucketPrefixes` |
| The history account (the sweeper and the rollover) | `roles/storage.objectViewer`, no condition | `modules/installation/history.tf`, `history_runs_reader` |
| The scheduler account | none | |
| Project Owners and Editors | `roles/storage.legacyBucketOwner` through GCS's convenience bindings (`projectOwner:`/`projectEditor:`), plus their project-level roles | GCS default; `internal/gcpfake/gcs.go` `ConvenienceBindings` models it |
| Project Viewers | the convenience read bindings, which `fugaro init` removes (`infra.RemoveProjectViewers`) | `internal/infra/statebucket.go` |

The bucket has `uniform_bucket_level_access = true` and `public_access_prevention = "enforced"`. It has **no versioning** and no retention policy (`modules/installation/bucket.tf`). The Terraform state is in a separate bucket, where only operators hold `objectAdmin`.

People are made launchers or operators by `fugaro init --launcher/--operator`, stored in the local config's `terraform.launchers` and `terraform.operators`. A new config defaults both to the person running `init` ([gcp-setup.md](../gcp-setup.md), precondition 8). So on a one-person installation, which is the case for both of the owner's installations, every launcher is also an operator.

### 2.2 Who reads and writes which objects

| Object | Written by | Read by |
|---|---|---|
| `runs/<slug>/<run>/task.json` | launcher (`runstore.CreateTask`, `internal/cli/run.go`); the job (`WriteTask` in `internal/cli/exec.go`) | job; launchers (`ls`, `diagnose`, `logs`, `watch`) |
| `runs/.../launching` (claim) | launcher: `Claim` (create-if-absent), takeover `ReplaceIf`, release `DeleteIf` (`run.go`) | launchers; `cancel` |
| `runs/.../launch.json` | launcher (`WriteLaunch`) | launchers, `cancel`, `watch` |
| `runs/.../cancel` | launcher (`RequestCancel`, `internal/cli/cancel.go`) | job |
| `runs/.../budget-token` | launcher creates it, and deletes it on a failed launch (`internal/budget/token/object.go`); the job deletes it at start | job |
| `runs/.../result.json`, transcripts, `session/`, `followup.md`, `comments.json` | job (and the runner's `ReplaceRecordIf`) | launchers, the history account, later runs of the same repository |
| `cache/<slug>/...` | job (`internal/cache`, `Touch` sets custom time) | job |
| `locks/<slug>/...` | job (`internal/lock`) | job; launcher reads it (`checkBranchLock`, `internal/cli/followup.go`) |
| `builds/<slug>/<wf>/image.json`, `gate.json` | build account (`fugaro image gate/record`, `internal/cli/image_record.go`, in Cloud Build) | launchers (`recipes_skew.go`, `image status`, `image_refresh_rules.go`, `init_fugaroyaml.go`), the check job |
| `builds/<slug>/<wf>/check.json` | the check job as the build account (`internal/cli/imagecheck.go` `writeState`); an operator running `fugaro image check` without `--dry-run` | launchers, operators |
| `builds/<slug>/project-layer.yaml` | (0.6.0, layered-config plan Tasks 13 and 14) `config publish`, `image build`, `init --repo` and `image refresh`, all operators; the build account may also write it (L6) | the render step and the check job (build account) |
| `fugaro/project.json` | Terraform (`google_storage_bucket_object.project_marker`), run by the operator running `fugaro init` | every cloud command (`infra.ReadProjectMarker`) |
| `fugaro/config.yaml` | `fugaro init`, `init --repo`, `init --base`, `image refresh`, `init --publish-config` (`publishSharedWarn`, `internal/cli/sharedcfg.go`), all operator steps | every launcher without a local config (`sharedcfg.go`) |
| `fugaro/recipes/<name>.yaml` | `fugaro recipes publish` (`internal/cli/recipes.go` `publishRecipe`) | every launch that names a recipe (`recipes_resolve.go`) |
| `fugaro/project-layer.yaml` | (0.6.0) `fugaro config publish` | every launch and most in-checkout commands (layered-config §7) |

**No launcher command writes outside `runs/`.** The only writers of `fugaro/` and `builds/` that a person runs are `init` (all stages), `recipes publish`, `config publish`, `image build` and `image refresh` (operator steps: they need `fugaroBuildSubmitter` or Owner rights anyway), and a non-dry-run `image check`. The `cache/` and `locks/` writers are the runner, which runs as the job account. There is only the `gcp` backend, so no local mode writes `cache/` or `locks/` with a person's credentials. Every bucket write a person makes goes through `internal/blobx` or `gocloud.dev/blob`.

### 2.3 Facts about the clients that shape the design (verified in the code)

- **gocloud maps every 403 to `NotFound`,** on writes as well as reads (`gcsblob.(*bucket).ErrorCode`). `blobx.write` maps only `FailedPrecondition`, so today a refused write comes back as an unclassified error whose `gcerrors.Code` is `NotFound`. `blobx.isForbidden` (unexported) finds the `*googleapi.Error` underneath. `cli.isAccessDenied` checks `gcerrors.PermissionDenied` first, which never matches for GCS, and then the `googleapi.Error`.
- **A conditional grant gives no listing.** The job and build accounts lack `storage.objects.list` through their conditional grant, so a GET of a missing object answers 403 (the `blobx.Read` doc comment). Live check 7 ([gcp-live-checklist.md](../gcp-live-checklist.md), results of the third run) recorded "listing `runs/` gets 403" for the job account.
- **Overwrites and deletes are part of a launcher's job.** A launcher overwrites the claim (`ReplaceIf`, generation-matched) and deletes the claim and the token object (`DeleteIf`, `Delete`). So the launcher's write grant needs `storage.objects.delete` as well as `create`. `objectCreator` would break the claim takeover and the token cleanup.
- **The install guard (`internal/infra/tf/cover.go`)** has these properties:
  - `grantRules` covers `roles/storage.objectAdmin` only to people, and `objectUser` and `objectViewer` only to accounts.
  - It checks the bucket of a bucket grant, but **not its condition**. An `objectUser` grant to a Fugaro account with no condition, or with any other condition, would be covered. This is a gap today, independent of this design.
  - `tf.Guard` refuses every delete or replace unless `--allow-delete` names it, and `infra.DeleteHints` blames a missing `--launcher`/`--operator` for any deleted bucket grant.
- **doctor** already reads the project's IAM policy (`preflight.IAMPolicy`) and service accounts' policies (`doctor_signers.go`). Each degrades to a warning when it is refused.

## 3. The end state

| Principal | `fugaro/` | `builds/<slug>/` | `runs/` | `cache/`, `locks/` | How |
|---|---|---|---|---|---|
| Operator | read, write | read, write | read, write | read, write | `objectAdmin`, no condition (unchanged) |
| Launcher (not an operator) | read | read | **read, write** | read | `objectViewer`, no condition; plus `objectUser` with the condition `resource.name.startsWith("projects/_/buckets/<bucket>/objects/runs/")` |
| Job account | none | none | its slug only | its slug only | unchanged |
| Build account | none | its slug only | none | none | unchanged |
| History account | read | read | read | read | unchanged |

So:

- `fugaro/` is written only by operators (and by whoever holds project-level storage write: §9).
- `builds/<slug>/` is written by operators and that repository's own build account, which keeps L6's copy rule.
- `cache/` and `locks/` are written by operators and each repository's own job account.

## 4. How to express it (H1 to H5)

**H1: IAM Conditions on the existing bucket (recommended).** Launchers get two grants:

1. `roles/storage.objectViewer` with no condition, which brings `get` and `list` on the whole bucket.
2. `roles/storage.objectUser` with the launcher condition, which brings `create`, `delete`, `update` and `get` under `runs/`.

This is the same mechanism the job and build accounts have used since M4. Live check 7 verified it for the job account: allowed under its own prefixes, 403 elsewhere, list denied. The bucket already has uniform bucket-level access, which IAM Conditions require. Nothing moves; every reader and writer path stays.

The alternatives:

| Option | What | Why not first |
|---|---|---|
| **Managed folders** (veto alternative) | Launchers get `objectViewer` on the bucket and `objectUser` on a managed folder `runs/` (`google_storage_managed_folder` and `_iam_member`) | It brings two new resource types into the guard's allowlist. Creating a managed folder over a prefix with millions of existing objects, and its interplay with lifecycle rules, is unverified. No current code uses it |
| A separate config bucket | `fugaro-config-<p>`, with operators writing and launchers reading | It moves every reader (the shared-config anchor `fugaro-runs-<gcp_project>`, the marker, recipes, the layer, the L6 copies), and needs a data migration and two buckets per installation. Too large for a release that already hard-cuts the bases |
| IAM deny policies | Deny `storage.objects.create` on `fugaro/**` to launchers | Deny-policy conditions on Cloud Storage object names are unverified. Attaching one needs `iam.denyPolicies.*` at the project and a new Terraform resource kind. The guard forbids policy-level resources |
| `objectCreator` for launchers | create-only under `runs/` | It breaks the claim takeover (`ReplaceIf` overwrites need `delete`) and the token cleanup (§2.3) |

**H2: Launchers write all of `runs/` (recommended).** Launchers write `runs/` for every repository, not `runs/<slug>/` per repository. Launchers are installation-wide in the config (`terraform.launchers`), so a per-slug condition would need per-repository launcher lists and one more conditional binding per repository.

Veto alternative: **leaf names only.** The condition becomes `startsWith(".../objects/runs/") && (endsWith("/task.json") || endsWith("/launching") || endsWith("/launch.json") || endsWith("/cancel") || endsWith("/budget-token"))`. Launchers could then no longer forge another run's `result.json`, or plant a `session/` file or `followup.md` that a later follow-up of the same repository reads (§10, item L3). The cost: every new launcher-written object needs an IAM change and an apply, and `endsWith` on `resource.name` is unverified for Cloud Storage (live check §11, item V6).

**H3: Launchers read the whole bucket, unconditioned (recommended).** Listing `runs/` (`ls`, `watch`, `runstore.ListSlugs`) needs `storage.objects.list`. A condition on `resource.name` does not grant a list (§2.3), so the read grant must be unconditioned, which makes it bucket-wide. That is today's read access exactly, so nothing a launcher reads breaks.

Veto alternative: narrow the reads to `fugaro/`, `builds/` and `runs/` with a conditional `objectViewer`. Listing would then be granted through the `storage.googleapis.com/objectListPrefix` request attribute (unverified, V7). Launchers would lose read of `cache/` (build caches) and `locks/`. But `checkBranchLock` reads `locks/`, so that prefix would have to stay.

**H4: Operators keep `objectAdmin`; a member on both lists gets only the operator grant (recommended).** The `runs` resource keeps its name and becomes `for_each = toset(var.operators)`. Two new resources take `setsubtract(var.launchers, var.operators)`:
- `runs_reader`, `objectViewer`;
- `runs_launcher`, `objectUser` with the condition.

A one-person installation (both of the owner's installations) then plans **no change at all**. On a larger one, the plan deletes `runs["<launcher>"]` for each launcher who is not an operator and creates that launcher's two new grants.

Veto alternative: give the launcher grants to every launcher, operators included. That is redundant, and it would plan creates even on a one-person installation.

**H5: The condition is defined once, in Go, and passed byte for byte (recommended).**
- `gcp.LauncherBucketCondition(bucket)` returns `resource.name.startsWith("projects/_/buckets/<bucket>/objects/runs/")`.
- `gcp.LauncherBucketConditionTitle` is `fugaro-launchers-runs`, with no description, as for the job grants.
- The installation spec carries them as `launcher_bucket_condition = {title, expression}`. A module variable validation requires the expression to begin with the runs bucket's own `runs/` path.

This is the convention of the job and build conditions ("a condition that differs in one byte is another binding", `names.go`). It lets the guard and doctor compare against the same string.

Every launcher carries the identical role, title and expression, so IAM keeps all of them as members of **one** conditional binding. The hardening adds one conditional binding to the bucket policy, whatever the number of launchers (V3).

Veto alternative: build the expression in HCL from `var.runs_bucket`. That gives one fewer variable, but the guard and doctor would then duplicate the string.

## 5. The install guard and the migration (H6, H7)

**H6: The guard checks bucket-grant conditions (recommended).** `Cover.checkGrant` gains a condition check for `google_storage_bucket_iam_member`. This also closes the §2.3 gap for accounts.

| Role | Principal | Covered only when |
|---|---|---|
| `objectAdmin` | person | no condition |
| `objectViewer` | person | no condition |
| `objectUser` | person | the condition is exactly `{title: fugaro-launchers-runs, expression: LauncherBucketCondition(b)}` for a bucket `b` in `Cover.Buckets` |
| `objectUser` | Fugaro account | a condition whose every `||` clause is `resource.name.startsWith("projects/_/buckets/<b>/objects/<p>/<slug>/")`, with `<b>` in `Cover.Buckets`, `<p>` in `runs`, `cache`, `locks` or `builds`, and a non-empty `<slug>` |
| `objectViewer` | Fugaro account | no condition (the history account) |

A condition known only after apply is not covered. `grantRules` moves `objectUser` and `objectViewer` to `toPeople | toAccounts`. The other checks still apply: a person must be on the review screen (`Listed`) and the bucket must be this run's.

**H7: The migration's deletes are allowed exactly, and are never covered by the one confirmation (recommended).** `infra.HardeningAllowDelete(plan, launchers, operators)` returns the address `module.installation.google_storage_bucket_iam_member.runs["<m>"]` for each `<m>` that meets all of these:

- the plan deletes it (`["delete"]`, never a replace);
- its `before.role` is `roles/storage.objectAdmin`;
- `<m>` is a launcher and not an operator;
- the same plan creates both `runs_reader["<m>"]` and `runs_launcher["<m>"]`.

Nothing else is allowed: a member dropped from both lists still needs `--allow-delete`, with today's hint. The installation stage passes `append(r.o.allowDelete, HardeningAllowDelete(...)...)` to `guard`.

A delete is never covered (`Cover.NotCovered` refuses every action but create, update, no-op and read), so this step asks its own typed project name (or takes `--yes`) after a banner:

```
bucket access (0.7.0): these launchers keep reading the runs bucket and writing runs/, and lose write access to fugaro/, builds/, cache/ and locks/:
  user:a@example.com
  user:b@example.com
```

`DeleteHints` already skips allowed addresses, so the misleading "pass --launcher again" hint never appears for these deletes.

Veto alternative: no automatic allow. The operator passes `--allow-delete 'module.installation.google_storage_bucket_iam_member.runs["user:a@example.com"]'` once per launcher, as the refusal prints.

**Apply order.** Terraform may destroy `runs["<m>"]` before it creates the two new grants. A launcher who launches during the apply can then get a 403 for a few seconds, or for as long as IAM takes to propagate (V5). The fix is to retry. No data is at risk: the claim protocol fails closed. The 0.7.0 notes say to apply when nobody is launching.

## 6. What launchers lose, and how the CLI says so (H8)

These commands become operator-only in effect. They already were operator steps in the docs, but nothing stopped a launcher before:

- `fugaro recipes publish`;
- `fugaro config publish` and its `builds/<slug>/project-layer.yaml` fan-out;
- `fugaro init --publish-config`, and the shared-config publish that every `init` stage runs;
- `fugaro image check` without `--dry-run` (its `check.json` write);
- a hand edit of `fugaro/config.yaml` (gcp-setup.md tells "an operator" to do it).

**H8: one refusal text, from a classified 403 (recommended).**

- **`blobx`.** The write path (`write`, `ReplaceIf`, `DeleteIf`, `DeleteExisting`) wraps an HTTP 403 as `blobx.ErrForbidden` (`errors.Is`), keeping the cause. Today it surfaces as an unclassified error whose `gcerrors.Code` is `NotFound`.
- **`internal/cli`.** A new helper, `operatorWriteErr(url, key string, err error) error`, returns a user error (exit 1) when `errors.Is(err, blobx.ErrForbidden) || isAccessDenied(err)`:

  ```
  nothing was published: writing fugaro/recipes/review.yaml in gs://fugaro-runs-acme needs the operator role (since 0.7.0 launchers read the runs bucket but write only runs/); ask an operator to publish it, or to add you with fugaro init --operator
  ```

  Other failures keep today's `remote(...)` (exit 2).
- **Callers.** `publishRecipe`, `publishLayer`, `writeLayerCopy` (where the fan-out reports per repository, a 403 on the first copy stops the fan-out with this message), `publishSharedWarn`'s callers (`--publish-config` fails with it; the other stages warn with it), and `writeState` (named for a person running `image check`).
- **No pre-check.** Each publish reads, diffs and then writes, so a launcher sees the diff and then the refusal. That is acceptable: nothing is half-written, because every write is one object or a create-if-absent.

Veto alternative: a pre-flight `buckets.testIamPermissions` before the diff. That costs one more call and one more unverified behaviour (V4).

**What launchers keep:**

- every launch, follow-up, retry, cancel and `ls`/`logs`/`diagnose`/`watch`;
- `config show`, `config layer`, `validate` and `doctor`;
- reading recipes, the shared config and the layer;
- `image status`.

The 0.6 CLI keeps working for launchers after the apply, because a 0.6 launch writes only `runs/`. So the IAM change and the CLI upgrade can happen in either order.

## 7. Detection: `fugaro doctor` (H9)

**H9: a `runs-bucket-iam` check, read-only, that degrades rather than fails (recommended).** It runs in doctor's cloud mode, after the shared-config checks, never under `--plugin`.

1. **The bucket policy.** Doctor reads it with `storage.buckets.getIamPolicy` (`optionsRequestedPolicyVersion=3`, `infra.BucketPolicy`). When the read works, which needs project Owner, `storage.admin` or `iam.securityReviewer`, the pure `infra.RunsBucketFindings(policy, bucket, launchers, operators)` reports the following. Each warning fails only under `--strict`.

   | Finding | Severity | Fix shown |
   |---|---|---|
   | A non-operator member holds a write-capable role with no condition (`objectAdmin`, `objectUser`, `objectCreator`, `legacyBucketWriter`, `legacyBucketOwner`, `storage.admin`, or any custom role, which is flagged as "cannot tell") | warning | `fugaro init` (0.7.0) when the member is a launcher; else `gcloud storage buckets remove-iam-policy-binding gs://<b> --member=<m> --role=<r>` |
   | A conditional write grant to a person that is not the launcher condition | warning | the same command with `--condition-from-file` / `--all` |
   | A launcher who is not an operator lacks `runs_reader` or `runs_launcher` | warning | `fugaro init` |
   | `projectOwner:`/`projectEditor:` convenience `legacyBucketOwner` | info: "project Owners and Editors can write fugaro/; treat them as operators" | none |

   Without `terraform.launchers`/`operators` (a teammate on the shared config), the member lists are unknown. Doctor then reports only unconditioned write grants, as info, and lists their members.
2. **The caller's own access.** When the policy read is refused (the usual case for a launcher), doctor calls `storage.buckets.testIamPermissions` for `storage.objects.create` and `storage.objects.delete` on the bucket. A conditional grant cannot match the bucket resource (V4), so a "held" answer means an unconditioned write grant:
   The caller is identified only by the local config's self-asserted `user`, matched as `user:<user>` against the known lists:
   - held, and the caller is on the operators list: ok, "operator: writes the whole bucket";
   - held, and the caller is a launcher who is not an operator: **warning**, "your credentials can write anywhere in gs://<b>: this installation predates the 0.7.0 bucket hardening; ask an operator to run fugaro init";
   - held, and the caller cannot be placed (no `user`, or no lists on a shared-config machine): info with the same text, prefixed "expected for an operator;";
   - not held: ok, "writes only runs/".

   The operators' own `objectAdmin` lacks `storage.buckets.getIamPolicy`, so most operators land here too. That is why an operator's held answer is ok and not a warning.
3. **Project-level grants.** When the project policy is readable (it is already read by `preflight.IAMPolicy`), each launcher who is not an operator and holds `roles/owner`, `roles/editor`, `roles/storage.admin`, `roles/storage.objectAdmin`, `roles/storage.objectUser` or `roles/storage.objectCreator` on the project is a warning: the bucket hardening does not bind them. Groups are not expanded, and doctor says so.
4. **A test configuration** (`endpoints.no_auth` with no `endpoints.storage`) is skipped, so no real bucket is ever read from one.
5. **Unreachable or unauthenticated** gives a warning, "could not check the runs bucket's IAM: <reason>". It is never an error and never a non-zero exit without `--strict`. Doctor stays usable on a plane.

Every member string is printed through `pluginwire.Printable`.

## 8. Rollout relative to 0.7.0 (H11) and rollback (H10)

**H11: it ships inside 0.7.0, applied by `fugaro init` as the last operator step (recommended).** The 0.7.0 order (base-image.md §10) is:

1. operators upgrade the CLI;
2. each repository gets its pull request;
3. each repository runs `image refresh`;
4. doctor shows a clean result.

This design appends two steps:

5. **One operator runs `fugaro init`** (the installation stage) in their own terminal, reads the plan and the banner, and types the project name. On a one-person installation the plan has no bucket change (H4).
6. **Every operator and launcher** runs `fugaro doctor`, which reports `runs-bucket-iam` ok.

The bucket change does not depend on steps 1 to 4. Pre-0.7.0 CLIs keep launching after it (§6). The code is independent of the base cut too, so its PR can merge before or after the cut PR (group 4 of the base-image plan), as long as it merges before `/new-release 0.7.0`. Claude never applies it. The user reviews and applies the plan at their own terminal.

**H10: no toggle, and rollback by hand (recommended, following the no-backward-compat rule).** There is no setting that keeps launchers on `objectAdmin`. To roll back one member, outside Terraform:

```
gcloud storage buckets add-iam-policy-binding gs://fugaro-runs-<p> --member=user:a@example.com --role=roles/storage.objectAdmin
```

The member resources are non-authoritative, so later applies keep that grant, and doctor flags it (H9).

Rolling back the CLI to 0.6.x and re-running its `fugaro init` is different. That plan would delete `runs_reader`/`runs_launcher` and recreate `runs["<m>"]`. The 0.6 guard refuses the deletes without `--allow-delete`, so nothing changes unseen.

Veto alternative: a local-config switch `terraform.launcher_bucket_access: full` that keeps today's grant. That is more code, and it is a backward-compatibility path the project avoids before release.

## 9. What IAM on the bucket cannot stop (H12, H13)

**H12: project-level grants stay the project's business; Fugaro reports them and does not change them (recommended).** Some principals write the bucket through the project, whatever the bucket's policy says:

- project Owners and Editors: `roles/editor` carries `storage.objects.*`, and they also hold the `legacyBucketOwner` convenience binding;
- holders of `roles/storage.admin` or `roles/storage.object*` on the project;
- folder- and organization-level grants.

`gcp-setup.md` says so in the roles section: such people are operators in effect, and should be listed as operators. Doctor reports them (H9.3).

Veto alternative: `init` removes the `projectEditor:` convenience binding as it removes the Viewers' bindings. That buys nothing, because `roles/editor` writes objects at the project level anyway, and it surprises Editors who read the bucket in the console.

**H13: the audit trail is documented, not managed (recommended).** Cloud Storage Data Access audit logs (`DATA_WRITE`) are off by default. With them on, every write to `fugaro/` is attributable. `gcp-setup.md` gains an optional operator step with the one `gcloud projects get-iam-policy`/`set-iam-policy` recipe for `storage.googleapis.com` `DATA_WRITE`, and its log-volume cost note.

Veto alternative: a Terraform `google_project_iam_audit_config`. The guard refuses audit configs by design (`cover.go`), and an audit config is project-wide and authoritative for its service.

## 10. Threat model

**A launcher (not an operator) after the hardening can still do the following:**

- **L1.** Launch any task text against any onboarded repository, with that repository's git credential and the workflow's secrets. The agent runs what it is asked. This is unchanged, by design.
- **L2.** Embed any project layer text in its own `task.json`: the runner trusts the embedded text (layered-config §8). This is the same capability as L1.
- **L3.** Write any object under `runs/`, for any repository, any run:
  - overwrite another launcher's queued `task.json` (delete and recreate) before the job reads it;
  - forge `result.json` or `launch.json`, which `ls`, `report` and the history rollover read;
  - plant `session/` files or `followup.md` that a later follow-up of that repository restores ([v1.md](v1.md) §6, "A restored session is replayed");
  - set the `cancel` marker on anyone's run.

  The H2 leaf-names alternative removes all but the queued `task.json` and the `cancel` marker. A launcher can also cancel any execution through `fugaroLauncher` (`run.executions.cancel`).
- **L4.** Read everything in the bucket: transcripts, others' sessions, caches, records. This is unchanged.
- **L5.** Mint budget tokens for any run (R5 in gcp-setup.md), unchanged.
- **L6.** Fill `runs/` with junk. The lifecycle rule deletes it after `runs_days`, and it costs storage meanwhile.

**What it can no longer do:**

- write the project layer, the shared config, recipes or the marker;
- rewrite a build record, `check.json` or a layer copy;
- poison `cache/`;
- forge or steal `locks/`.

So the stealth paths of layered-config §11 (items 1, 2 and 6 across repositories, and the daily-rebuild path with no launch) are closed for launchers. The project marker becomes a boundary against launchers as well as against job accounts. Comments in `bucket.tf` and `infra/project.go` that call it "a safety label, not a boundary" because "a launcher could" write it are updated.

**A compromised agent inside a run** (the job account) is unchanged. It can write its own repository's `runs/`, `cache/` and `locks/`. So it can poison that repository's cache and plant session files for that repository's follow-ups (existing residuals, [v1.md](v1.md) §6).

**A compromised build** (a repository's Dockerfile or `image.setup` running in Cloud Build as the build account) is unchanged. It can rewrite its own `builds/<slug>/`, its layer copy included. That copy then steers its own next rebuild, where it already runs code (L6's argument).

**A stolen operator credential is full control of the installation.** Bucket IAM does not limit it. It can:

- write all of the bucket: the layer's commands for every repository, the shared config's budget, recipes, build records, caches;
- write the Terraform state (`objectAdmin` on the state bucket), steering the next owner's apply;
- push base images to `fugaro-base`, a supply chain into every repository's next build;
- submit builds as any build account, which reads its secrets;
- add secret versions.

What limits the damage:

- a short operator list, ideally a group with enforced 2-step verification;
- short-lived credentials (ADC from `gcloud auth application-default login`, no service account keys);
- the `--executable-changes` banner and the run-time `commands: from profile <p>` line;
- doctor's drift checks (layer copy versus canonical, H9);
- H13's audit trail.

An operator's own coding agent with the operator's ADC can also write `fugaro/` directly: `config publish` refuses inside an agent session, but `gcloud storage cp` does not. This is a mitigation, not a barrier, as everywhere else in Fugaro.

## 11. Verified from the code, and not verified (live checklist)

**Verified from the code** (§2): the grants, the object paths and their writers, the absence of launcher writes outside `runs/`, gocloud's 403 mapping, the guard's missing condition check, and the bucket's uniform access with no versioning or retention.

**Verified live earlier, by analogy:** a prefix condition with `objectUser` allows writes and deletes under the prefix and denies writes, reads and listing elsewhere, for the job account (check 7).

**Unverified GCP behaviour.** These are user-run items, sandbox repository only, collected as **Check 32** in [gcp-live-checklist.md](../gcp-live-checklist.md) by the plan's live-check task. The number is chosen after the upgrade plan's Check 31; renumber if it is taken.

| ID | Assumption | Consequence if wrong |
|---|---|---|
| V1 | For a person, an unconditioned `objectViewer` plus a conditional `objectUser` behaves as for the job account: a create, overwrite (generation-matched) and delete under `runs/` work; a create, overwrite or delete under `fugaro/`, `builds/`, `cache/` and `locks/` answers 403; list and get work everywhere | the design fails: stop and revisit H1 |
| V2 | A create with `ifGenerationMatch=0` and a replace with `ifGenerationMatch=<gen>` under `runs/` need only the conditional grant (create plus delete) | launches fail at the claim: widen the condition's role |
| V3 | Several members with the same role, title and expression land in one conditional binding, and the bucket policy's limit on conditional bindings is not reached by one more. Today each workflow job account and each build account already adds one conditional binding, so record the count on the sandbox installation and the documented limit | big installations hit the limit: this is an existing risk, recorded |
| V4 | `buckets.testIamPermissions` is answered for a caller without any bucket-level permission, and reports `storage.objects.create` only for an unconditioned grant | doctor's step 2 says "unknown" instead: degrade the check's wording |
| V5 | How long a removed `objectAdmin` keeps working after the apply (IAM propagation), and that the apply order causes no error beyond retries | the notes give the real figure |
| V6 | (only with the H2 veto) `resource.name.endsWith` is accepted in a Cloud Storage condition | the leaf-names alternative is impossible |
| V7 | (only with the H3 veto) the `storage.googleapis.com/objectListPrefix` attribute grants a prefix-limited list | narrowed reads are impossible |
| V8 | The 403 on a write reaches the CLI as a `*googleapi.Error` under gocloud's writer (`errors.As`), so `blobx.ErrForbidden` classifies it. The unit tests prove it against `gcpfake` only | the refusal falls back to the generic remote error |

The check runs against the installation **`belong`**, exercising only the **sandbox** repository. The apply, though, changes `belong`'s bucket policy for everyone, EdgeWeb's launchers included. **The owner must agree to that apply.** If every `belong` launcher is an operator, the plan is "no changes" (H4), and V1/V2 then need a second test identity added as a launcher only. Using a throwaway installation instead is the safer veto alternative (H14).

## 12. Decisions (veto any before execution starts)

| ID | Decision | Recommendation | Veto alternative |
|---|---|---|---|
| H1 | Mechanism | IAM Conditions on the existing bucket | managed folders; a separate config bucket |
| H2 | Launcher write scope | all of `runs/` | leaf names only (`task.json`, `launching`, `launch.json`, `cancel`, `budget-token`) |
| H3 | Launcher read scope | whole bucket, unconditioned | `fugaro/`, `builds/`, `runs/`, `locks/` with a list-prefix condition |
| H4 | Operators and overlap | operators keep `objectAdmin`; launcher grants for launchers who are not operators | launcher grants for every launcher |
| H5 | Where the condition lives | Go (`gcp.LauncherBucketCondition`), passed as a tfvar, title `fugaro-launchers-runs` | HCL-computed |
| H6 | Guard | checks every bucket grant's condition (launchers exact; accounts by shape) | launcher condition only, accounts as today |
| H7 | Migration deletes | allowed exactly by `HardeningAllowDelete`, behind the step's own typed confirmation and a banner | `--allow-delete` per launcher |
| H8 | Refusal | `blobx.ErrForbidden` plus one operator-role message, exit 1, no pre-check | pre-flight `testIamPermissions` |
| H9 | Detection | doctor `runs-bucket-iam`: the policy when readable, else the caller's own test; warnings, never errors | policy only (no self test) |
| H10 | Rollback | no toggle; a documented `gcloud ... add-iam-policy-binding` | `terraform.launcher_bucket_access: full` |
| H11 | Rollout | in 0.7.0; one operator's `fugaro init` as the last step of the 0.7.0 notes | a separate 0.7.1 |
| H12 | Project-level writers | documented and reported by doctor, not changed | remove the `projectEditor:` convenience binding |
| H13 | Audit trail | documented optional `DATA_WRITE` audit logs | Terraform-managed audit config |
| H14 | Live check | user-run Check 32 on `belong`, sandbox repository only, with the owner's go-ahead for the `belong` apply | a throwaway installation |

## 13. Non-goals

- Per-repository launcher lists, or per-repository `runs/<slug>/` grants for people.
- Signing or verifying the project layer, recipes or `task.json` content (a possible later layer of defence against L3).
- Changing the job, build, history or scheduler accounts' grants.
- Changing the state bucket.
- Any live apply by Claude.

## 14. Docs

These docs change:

- **`docs/gcp-setup.md`:** the roles paragraph (what launchers and operators read and write), the shared-file paragraph ("launchers and operators already hold objectAdmin" becomes "launchers read it; operators write it"), project-level writers (H12), the audit-log recipe (H13), the rollback line (H10), and the doctor finding table.
- **The design docs** that state the old grant: `docs/design/v1.md` §6.1 and its "Who can do what" list, `shared-config.md` §3 and §13, `watch-queued.md`, and `layered-config.md` §11 ("the hardening shipped in 0.7.0").
- **`docs/recipes.md`:** publishing is operator-only.
- **`docs/release.md` and `docs/releases/v0.7.0.md`:** steps 5 and 6 of §8.
- **`docs/gcp-live-checklist.md`:** Check 32.

A docs test pins that no doc but this one, the plans and the release notes says launchers hold `objectAdmin`.

# M5 — Infrastructure Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Terraform module and `fugaro init` replace the M4 bootstrap. `fugaro init` stands up an installation in a fresh project, or adopts the live one (`<project>`, `us-east5`) with `import` blocks and **no destroy or recreate**. The resources keep M4's names, labels and IAM. Each repository gets its own build service account. GitHub repositories can build in Cloud Build. `fugaro run` takes a per-run timeout. Compute prices can be overridden locally, and the launcher's IAM is written down. Base images move to their own registry, and each repository gets its own derived-image registry, so one repository's build identity can't overwrite another's images. A cheap daily **image check** rebuilds a derived image only when a trigger fires: dependencies changed, the image config changed, the base moved, or the image is too old. The rebuild goes candidate → smoke → promote, so a bad build never replaces `:latest`. Nothing billable starts unattended until the user has seen a check's decision. Every run records how old its image was.

**Architecture:**

```
                    ┌──────────── laptop ─────────────────────────────────────────────┐
fugaro.yaml ───────►│ fugaro init [--repo]                                              │
local config ──────►│   internal/infra: spec (names from internal/backend/gcp)          │
                    │   discover + ownership checks ─► imports.tf.json                  │
                    │   readiness gates (secret versions, image) ─► deploy_job          │
                    │   tf wrapper: init → plan → show -json → GUARD (no delete) →      │
                    │               ⚠ CONFIRM → apply <saved plan>                      │
                    └──────┬──────────────────────────────────────────────┬─────────────┘
                           │ gs://fugaro-tfstate-<project>                 │ writes local config
                           ▼   fugaro/installation, fugaro/repos/<slug>    ▼
   roots/installation ─► modules/installation: APIs, runs bucket, the base-image registry
                         `fugaro-base` (build SAs read only), custom roles, scheduler SA,
                         log bucket + view + exclusion, optional budget, alert
   roots/repo ─────────► modules/repo: secret containers (labels, no values), the repo's own
                         image registry, build SA, check job + daily schedule (paused until
                         a build record exists) ─► modules/workflow (for_each): job SA,
                         bucket condition, secret accessors, Cloud Run job (maxRetries 0)

   daily: Cloud Scheduler (us-east4) ─► check job (us-east5, runs as the repo's build SA)
            reads builds/<slug>/<wf>/image.json, blobless clone of the base branch, base digest
            ─► skip | back off (last build failed, same inputs) | Cloud Build:
               credential → source → render → build → push candidate → smoke (--network none)
               → gate (newer record? stop) → promote :latest by digest → write image.json
               → untag candidate (best effort)
```

- Terraform never computes a name. `fugaro init` computes every name, label, env var, IAM condition and Cloud Build request through the Go code that `fugaro run` uses, and passes them in as `terraform.tfvars.json`. Terraform checks their shape, not their value.
- Terraform owns the secret *containers*, their labels and their IAM. It never holds a secret value: the module has no `google_secret_manager_secret_version`, and a CI check enforces that. `fugaro secrets set` stores values, as in M4.
- Every Terraform run goes through `fugaro init`'s wrapper. It applies only a saved plan the user has seen, and refuses any plan that deletes or replaces something, unless the user named that address.

**Tech stack:**
- Go 1.27. **No new Go module.** The REST clients for IAM (`iam/v1`), Artifact Registry (`artifactregistry/v1`) and Cloud Resource Manager come from `google.golang.org/api`, which is already direct. Terraform runs through a small `os/exec` wrapper, not `hashicorp/terraform-exec` (ruling R10).
- Terraform `>= 1.7, < 2.0` (import `for_each`, `removed` blocks, `mock_provider` in `terraform test`). CI and the live runbook use 1.16.4, the version installed locally.
- Provider `hashicorp/google` at an **exact** version (the newest release when Task 2 starts; look it up in the registry, don't guess). Lock files are committed for `linux_amd64`, `linux_arm64`, `darwin_amd64` and `darwin_arm64`.
- CI tools, each pinned: `hashicorp/setup-terraform` (by SHA, `terraform_wrapper: false`), `tflint` with `tflint-ruleset-google`, and `trivy config` (Trivy is already the image scanner; `images/scan.sh`).

**Spec:** [docs/design/v1.md](../design/v1.md). Read these first:
- §3.2 (names, ownership marks), §3.3 (bucket layout, lifecycle), §3.4 (launch IAM)
- §4.5 (task timeout), §5.1 (fugaro.yaml), §5.4 (local config)
- §6.1 (every IAM rule), §7.2 (Cloud Build, the metadata boundary)
- §8 (Terraform and `fugaro init`), §10.1 (prices), §12 (module distribution), §14 (the M5 line), §15
- [gcp-bootstrap.md](../gcp-bootstrap.md) and `deploy/bootstrap/gcp-m4.sh`: the rules below were learned there
- [gcp-live-checklist.md](../gcp-live-checklist.md): the live facts, especially checks 7, 11, 11b and 1b

The M4 plan ([2026-09-27-m4-gcp-backend.md](2026-09-27-m4-gcp-backend.md)) explains the code this plan changes: `gcp.BuildRequest` (Task 15), `fugaro gcp job-spec` (Task 17) and `checkMaxParallel` (Task 10).

**Decisions already made (user):**
- **GCP:** project `<project>`, region `us-east5`. Billing is linked, with a 70 CAD budget created by hand. The project is **shared** with unrelated services (Firebase, App Engine), so nothing in M5 may take authoritative ownership of project-wide IAM, sinks or APIs.
- **Live resources exist, created by the bootstrap:** `gs://fugaro-runs-<project>` (labelled `fugaro=managed`, lifecycle rules, public access prevention, UBLA), the Artifact Registry repository `fugaro` (labelled), `fugaro-build` with its bindings, and, for `acme/sandbox` and `acme/webapp` (both Bitbucket, workflow `web`), the job service accounts, secrets, derived images, conditional bucket bindings and Cloud Run jobs. M5 adopts them with `import`. **No destroy, no recreate.** M5 must also work in an empty project.
- **Model auth:** `oauth` (`claude-oauth-token`). `vertex` and `api-key` exist too and must keep working.
- **Secret values** never go into Terraform, its variables, its plan or its state.
- **The user runs anything that costs money or changes real resources.** Every such step is **⚠ CONFIRM** in this plan. Hermetic tests are the default. No test needs real GCP.
- **The image rebuild is a daily *check*, not a daily rebuild** (user, 2026-09-29). It rebuilds only when a trigger fires: the dependencies changed, the image config changed, the base moved, the image is older than `max_age` (**default 14 days until M7**; controller ruling, since the base doesn't move before M7), a configured path changed, or someone forced it. Every decision is logged with its reason, and a failed rebuild is visible to the user. A rebuild pushes a candidate, smoke-tests it, and only then promotes it to `:latest`.
- **Review rulings (controller, 2026-09-29):**
  - Cloud Scheduler runs in a Go-computed `scheduler_region` (default `us-east4`; Scheduler isn't offered in `us-east5`). The check job stays in `us-east5`.
  - A repository's schedule is created **paused** until a build record exists, adopted repositories included. The user sees a `fugaro image check --dry-run` decision before unpausing.
  - Base images move now to their own registry, `fugaro-base`, where build accounts only read and only operators write.
  - Cut from M5: `smoke: full`, a local `image check --build`, the `buildlog/` history copy, and T11's `commits_behind` and `diagnose` line. `smoke: basic` runs with `--network none`.
  - Terraform's environment is built from an allowlist, with a CLI config file fugaro writes itself.

**Out of scope for M5:**
- **M6:** follow-up runs.
- **M7:** publishing base images to ghcr.io, the remaining skills and the v0.1.0 release. Until then `base_image` stays a dev tag in our own registry (ruling R14).
- **M8:** a second compute backend and its Terraform.
- **Offboarding a repository with one command.** `docs/gcp-setup.md` documents it: an apply with `--allow-job-delete` first sets the jobs' `deletion_protection = false`, a `removed` block keeps the secrets, then a guarded destroy with `--allow-delete`. A `fugaro init --repo --remove` can come later.
- **`smoke: full`** (running `fugaro verify build` in the candidate): deferred to M7 at the earliest. It would put workflow secrets into repository code inside the smoke container.
- **Restricting egress** (design §6.1, a later hardening option).

## Why the image check exists (design question 4)

A derived image bakes in the repository's checkout at build time and its dependencies installed from that checkout. **Correctness never depends on the image being fresh:** at bootstrap the runner fetches the ref and force-checks it out (§4.1), and whatever the agent or `fugaro verify` runs installs what changed. So a stale image costs three things, and none of them is a wrong result:

1. **Run time and compute.** A larger fetch, and a dependency install that has more to do when the lockfile has moved since the build. The content-addressed GCS caches (§7.2) soften this, but a warm image is still the cheapest start. The web repo's first run spent about 2 minutes before the container started and 22 minutes in all. Task 11 measures how much of a run's time goes to catching up with a stale image.
2. **Delayed security fixes.** OS and toolchain patches reach a derived image only when it is rebuilt on a newer base.
3. **Wrong images, not just stale ones.** When `image:`, a `.fugaro/*.Dockerfile` or the setup steps change, the old image no longer matches the config the runner reads from the ref. Runs then fail in confusing ways, such as a missing apt package. That trigger isn't optional.

What each schedule costs, using the web repo. The arithmetic:
- **One rebuild** is a 12-minute build plus about 1 minute of smoke and promote on `E2_HIGHCPU_8` at about $0.016 a minute: 13 × $0.016 ≈ **$0.21**.
- **Registry storage** for the versions the cleanup policy keeps besides `:latest` is 2 older versions of a ~2 GB image (layers largely shared, so count about 2–3 GB) at $0.10 per GB-month: **about $0.20–$0.30 a month**.
- **The check** is a 1–3 minute Cloud Run job on 1 vCPU and 2 GiB: 3 × 60 s × ($0.000018 + 2 × $0.000002) ≈ $0.004 a run, 30 runs **≈ $0.12 a month** at most, and likely inside the free tier. Cloud Scheduler adds $0.10 a month per job beyond the billing account's three free ones, with one job per repository.

| Policy | Rebuilds a month | Cost per repository and month | Staleness | Ruling |
|---|---|---|---|---|
| Rebuild every night | 30 | 30 × $0.21 ≈ $6.30, plus storage | ≤ 1 day, often for no reason | no |
| Rebuild weekly | 4.3 | 4.3 × $0.21 ≈ $0.90, plus storage | up to 7 days, even right after a lockfile change | no |
| **Daily check, rebuild on a trigger, `max_age: 14d`** | at least 30 / 14 ≈ 2.1 (the age floor). An active repository whose lockfile moves 1–2 times a week rebuilds after each move, and each move resets the age clock, so about 4–9 | 2.1–9 × $0.21 ≈ $0.45–$1.90, plus storage ≈ $0.25, plus the check ≈ $0.10: **about $1–3 a month per active repository**. The ceiling is the nightly $6.30, reached only if a trigger fired every day; a failing rebuild backs off (T10), so a broken build doesn't pay it every night | a day after a trigger; `max_age` caps the rest | **default** |
| Manual only (`check: off`) | as requested | $0.21 per `fugaro image build` | unbounded | for sandboxes |

## Global Constraints

- **Names come from Go.** Terraform receives every name through `terraform.tfvars.json` written by `internal/infra`: every job, service account, secret, image and registry name; the scheduler job and its region; the installation singletons (the scheduler account, the custom role IDs, the base registry, and the log bucket, view, sink and exclusion); every label value; the bucket condition expression and title; and the job env. HCL may check shapes with `validation` blocks (such as `^[a-z][a-z0-9-]{4,28}[a-z0-9]$` for an account ID), but never derives, truncates or hashes a name. There is no `external` data source: a plan must not depend on a checkout or on the `fugaro` binary.
- **The names don't change.** For the Bitbucket sandbox fixture, `internal/infra` produces the same job, service account and secret names, the same labels, env and bucket condition, and the same condition title (`fugaro-<sa-id>`) as M4's `fugaro gcp job-spec`. The one deliberate change is the image path, which moves to the repository's own registry (ruling R6). An adopted job keeps its M4 image until the new path has been built (Task 13), and `LegacyImage` still gives the M4 path. A golden test pins this (Task 1). A condition that differs in one byte is a *different* binding, so Terraform would add a second one next to the bootstrap's.
- **Ownership marks, exactly as the bootstrap sets them:**
  - Jobs: `fugaro=managed`, `fugaro_repo=<RepoLabel(slug)>`, `fugaro_workflow=<workflow>`. The repository's check job carries `fugaro=managed`, `fugaro_repo`, and `fugaro_role=check` in place of `fugaro_workflow`.
  - Secrets: `fugaro=managed`, `fugaro_repo`, `fugaro_secret=<logical name>`. `fugaro secrets set` checks that these are a subset of the secret's labels, so the provider's `goog-terraform-provisioned` label does no harm.
  - The runs bucket, the legacy `fugaro` registry and the base registry: `fugaro=managed`. A repository's image registry: `fugaro=managed` and `fugaro_repo`. The state bucket: `fugaro=tfstate`.
  - Service accounts can't carry labels, so their display name is the mark. Job accounts: `Fugaro job <slug> <workflow>`, with the M4 form `Fugaro M4 job <slug> <workflow>` accepted and **kept as is** on adoption (Task 13), so M4's `gcp-m4.sh` still recognizes them during a rollback. Build accounts: `Fugaro build <slug>`. The scheduler account: `Fugaro scheduler`.
  - Ownership checks run before anything is adopted (imported), granted on, or allowed to be deleted. A resource under one of our names without our mark is **refused**: `fugaro init` exits 1 and names it. It never generates an import for it, and Terraform's own create then fails with 409, so nothing is adopted by accident.
- **The IAM posture M4's reviews established stays, and gets stricter:**
  - Each job service account gets `roles/storage.objectUser` on the runs bucket, conditioned on `runs/<slug>/`, `cache/<slug>/` and `locks/<slug>/`, trailing slashes included. It gets `secretmanager.secretAccessor` on each of its own secrets, at the secret level and never at the project level. It gets `roles/aiplatform.user` on the project only when `agent.auth: vertex`.
  - Each **build** service account (new, one per repository) gets:
    - accessor on its repository's provider credential and workflow secrets only
    - `artifactregistry.writer` on **its repository's own image registry** only
    - `artifactregistry.reader` on `fugaro-base`
    - `logging.logWriter`
    - the custom role `fugaroBuildSubmitter` (`cloudbuild.builds.create`, `cloudbuild.builds.get`)
    - `roles/iam.serviceAccountUser` on **itself only**
    - `roles/storage.objectUser` conditioned on `builds/<slug>/` (trailing slash)
  - Only operators write `fugaro-base`. Nothing writes the legacy `fugaro` registry after migration.
  - The runs bucket loses its `projectViewer` convenience bindings, as the state bucket does (ruling R2), because it holds transcripts and caches.
  - Jobs: `maxRetries 0`, one task, and `deletion_protection = true` (lowered only by an explicit offboarding apply).
- **Nothing authoritative on shared things.** Only `*_iam_member` resources, never `*_iam_binding` or `*_iam_policy`. `google_project_service` always has `disable_on_destroy = false` and `disable_dependent_services = false`. There is no management of the `_Default` sink itself, only an additive exclusion (Task 16).
- **The project is always explicit.** The generated provider block sets `project`, `region`, `billing_project` and `user_project_override = true` (user ADC with a quota project, as in M4). `fugaro init` refuses to run when `GOOGLE_PROJECT`, `GOOGLE_CLOUD_PROJECT` or `CLOUDSDK_CORE_PROJECT` is set to another project. Buckets are pinned: before generating an import for, or granting on, a bucket, discovery checks that the bucket's `projectNumber` is the project's. Bucket names are global.
- **The runs bucket's name starts with `fugaro-runs-`**, as the bootstrap and the live tests require.
- **Confirmation.** `fugaro init` prints the plan summary and a `⚠ CONFIRM (project <p>): …` banner, then asks for the project ID to be typed, unless `--yes` is given. Without a terminal it refuses, doing nothing, unless `--yes` is given. The controller passes `--yes` only after the user has approved that exact step. The same applies to creating the state bucket and to submitting an image build.
- **Terraform's environment is an allowlist.** The wrapper builds the child environment from scratch. It copies only `PATH`, `HOME`, `USER`, `LOGNAME`, `LANG`, `LC_ALL`, `TZ`, `TMPDIR`, the `XDG_*` variables, `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY` (either case), `SSL_CERT_FILE` and `SSL_CERT_DIR` (so a TLS-inspecting proxy's CA still works), `CLOUDSDK_CONFIG`, and `GOOGLE_APPLICATION_CREDENTIALS` (the same ADC fugaro itself uses). Then it sets:
  - `TF_IN_AUTOMATION=1`, `TF_INPUT=0`, `CHECKPOINT_DISABLE=1`
  - `TF_DATA_DIR=<workdir>/.terraform`
  - `TF_CLI_CONFIG_FILE=<workdir>/terraformrc`: a file fugaro writes holding only `plugin_cache_dir = "$XDG_CACHE_HOME/fugaro/terraform-plugins"`, so a user's `~/.terraformrc` (`dev_overrides`, `provider_installation`, credentials) is never read

  Everything else is dropped. That includes every `TF_*` (`TF_CLI_ARGS*`, `TF_LOG*`, `TF_VAR_*`, `TF_REATTACH_PROVIDERS`, `TF_WORKSPACE`, `TF_PLUGIN_CACHE_MAY_BREAK_DEPENDENCY_LOCK_FILE`, `TF_REGISTRY_*`, `TF_IGNORE`), `TERRAFORM_CONFIG`, and the provider's `GOOGLE_CREDENTIALS`, `GOOGLE_OAUTH_ACCESS_TOKEN`, `GOOGLE_BILLING_PROJECT`, `GOOGLE_REGION`, `GOOGLE_ZONE` and `USER_PROJECT_OVERRIDE`.
  - `GOOGLE_IMPERSONATE_SERVICE_ACCOUNT` is **refused** (exit 1), not silently dropped, so the user knows the apply would run as someone else.
  - Why: `TF_LOG` at trace level would log bearer tokens (like `GODEBUG=http2debug`, design §6.1); `TF_CLI_ARGS` could add `-lock=false`, `-target` or `-auto-approve`; `dev_overrides` or `TF_REATTACH_PROVIDERS` would swap in a provider the lock file never checked.
- **TDD.** Each task writes the failing test first and runs it to see it fail. `go test ./...` never needs Docker, network, credentials or a `terraform` binary: a fake `terraform` (Task 12) stands in. Tests that need the real `terraform` carry `//go:build terraform`, and CI's `terraform` job runs them. HCL is tested with `terraform test` and `mock_provider "google"`, which needs no credentials.
- **Subprocesses** use `exec.CommandContext` with `cmd.WaitDelay = 5 * time.Second`, as in M4.
- **Exit codes:** 0 for ok, 1 for a user error or a refusal (a guard, an ownership check, a missing confirmation), and 2 for a remote failure (a GCP API error, or a Terraform plan or apply that failed).
- **No company- or repo-specific values** in engine code, module defaults or examples (design §2). `<project>`, `us-east5`, `acme/sandbox`, `acme/webapp` and the state bucket's name appear only in this plan, the live runbook and the live-test constants.
- CI runs `gofmt -l`, `go vet` (including `-tags docker`, `-tags live` and `-tags terraform`) and `go test -race ./...`. The new `terraform` CI job runs `terraform fmt -check`, `init -backend=false -lockfile=readonly`, `validate` and `test` in both roots, then `tflint --recursive`, `trivy config`, and `go test -tags terraform ./internal/infra/...`. All of them pass after every task.

## Rulings on the questions

Each ruling says what it costs if it's wrong.

**R1. Where the Terraform lives, and how it's structured.** Under `deploy/terraform/gcp/`:

```
deploy/terraform/
  terraform.go                 # package terraform: //go:embed gcp (like images/images.go)
  gcp/
    modules/installation/      # shared: APIs, runs bucket, base registry, custom roles, scheduler SA,
                               #   log bucket/view/exclusion, optional budget and alert
    modules/repo/              # one repository: secret containers, its image registry, build SA
                               #   and its IAM, check job + schedule, module "workflow" for_each
    modules/workflow/          # one (repository, workflow): job SA, bucket condition,
                               #   secret accessors, aiplatform.user, Cloud Run job, launcher IAM
    roots/installation/        # backend "gcs" {}, provider, module call, outputs, .terraform.lock.hcl, tests/
    roots/repo/                # the same; the installation's outputs arrive as tfvars, not remote state
    .tflint.hcl
```

- There is **one Terraform state per repository** (`fugaro/repos/<slug>`) and one for the installation (`fugaro/installation`), not a single state holding a map of every repository.
  - An operator who onboards repository X plans and applies X alone. That operator's local config can't omit repository Y and so plan Y's deletion, and the blast radius of any apply is one repository.
- **The repository root is self-contained.** `fugaro init --repo` reads the installation root's `terraform output -json` and passes the values it needs in `terraform.tfvars.json`: the runs bucket, base registry, scheduler account, role IDs, launchers and operators. There is no `terraform_remote_state`.
  - So the golden tfvars describe the whole input, tests need no `override_data`, and teams that run their own Terraform don't depend on our state layout.
- `fugaro init` embeds the module and writes the root into a disposable workdir, so the binary and its module always match (as `cloudbuild.yaml` is embedded).
- Teams that run Terraform themselves use `roots/*` as examples, and `github.com/dimipaun/fugaro//deploy/terraform/gcp/modules/repo?ref=vX.Y.Z` as the source, with the inputs from `fugaro init --print-vars`.
- *Cost if wrong:* a single state is a new root that calls `modules/repo` with `for_each`, and `terraform state mv` for each address. The modules don't change.

**R2. State.** A GCS bucket, `fugaro-tfstate-<project>` by default. `--state-bucket` overrides it, and the local config records it as `terraform.state_bucket`.
- `fugaro init` creates it through the GCS JSON API before any Terraform runs: a tiny separate step, with its own ⚠ CONFIRM, that avoids the chicken-and-egg.
- It is created in the installation's region with versioning on, uniform bucket-level access, public access prevention enforced, the label `fugaro=tfstate`, and one lifecycle rule that keeps the 20 newest noncurrent versions.
- **Its `projectViewer` convenience bindings are removed at creation**, so project Viewers can't read the state. Owners and editors keep access through the convenience bindings, which can't be avoided.
- Operators get `roles/storage.objectAdmin` on it through a `google_storage_bucket_iam_member` in the installation module (a member resource on a bucket Terraform doesn't own), so operators added later get it too.
- The GCS backend locks the state itself.
- The state holds no secret values (Global Constraints). It does hold every name, service account email, IAM condition and the job env, so it is operator-only.
- Terraform never manages the bucket itself.
- *Cost if wrong:* if the bucket should also be in Terraform, add an import of it into the installation root.

**R3. `fugaro init`.**
- **It does:** write the local config; compute every name and the tfvars; run the read-only discovery and ownership checks (including the live IAM bindings) and generate `imports.tf.json`; decide the readiness gates and the schedule's paused state; create the state bucket; remove the `projectViewer` convenience bindings from the runs and state buckets (⚠ CONFIRM); run `terraform init`, `plan` and `show -json`; apply the guard; ask; apply the saved plan; submit the first image build (⚠ CONFIRM, billable); and print the `fugaro secrets set` commands still needed.
- **It never:** reads or writes a secret value; applies without a confirmation; applies anything but the plan it showed; lets a plan delete or replace a resource the user didn't name with `--allow-delete <address>`; enables billing; edits project-wide IAM authoritatively; or starts a billable build unattended.
- **Commands:**
  - `fugaro init` (the installation)
  - `fugaro init --repo [PATH]` (one repository, from its checkout)
  - `--plan-only`: stop after showing the plan
  - `--print-vars`: print the tfvars JSON and exit, with no cloud calls
  - `--config-only`: write the local config from flags and the installation's outputs, with no plan
  - `--forget`: the rollback (ruling R9)

**R4. The image check and rebuild.** A per-repository **Cloud Run job** (`gcp.CheckJobName(slug)`, prefix `fugarochk-`, so the `fugaro-` job listings in `ls` and `max_parallel` ignore it).
- It runs `fugaro image check --job` from the configured base image, **as the repository's build service account**, in the installation's region.
- Cloud Scheduler starts it once a day through `jobs.run`, from `scheduler_region`: a Go-computed input, since Scheduler isn't offered in every Cloud Run region. `gcp.SchedulerRegion(region)` returns the region itself when it is in the known list `gcp.SchedulerRegions`, else a fixed nearest one (`us-east5` → `us-east4`). The local config records it, and `--scheduler-region` overrides it. The Scheduler job's URI names the job's own location, so a cross-region start works.
- **The schedule is created paused** until the workflow's `builds/<slug>/<workflow>/image.json` exists, adopted repositories included. The M4 images have no record, so an unpaused first check would fire `no-record` and start a billable build that moves a live `:latest`, with nobody asked.
  - `init --repo` computes `paused` from discovery.
  - The runbook shows the user a `fugaro image check --dry-run` decision and runs a confirmed `fugaro image build`, which writes the record. The next `init --repo` then unpauses the schedule.
- **What it reads:**
  - the record `builds/<slug>/<workflow>/image.json` in the runs bucket
  - a blobless, no-checkout clone of the base branch (trees only, plus the few blobs the evaluation reads)
  - the base image's current digest, through a registry `HEAD`
- **What it does:** it evaluates each workflow's `rebuild:` triggers (Task 9).
  - When one fires, it submits the same `gcp.BuildRequest` that `fugaro image build` builds, **generated at check time by current code**, so no stale request body sits in Scheduler.
  - It backs off when the last build failed and its inputs are unchanged (T10).
  - It logs one JSON line per workflow (`event: image-check`, `decision`, `reasons`), and writes `builds/<slug>/<workflow>/check.json`.
  - When the check itself fails, it logs `decision: check-failed` before exiting 2.
- **Why not the alternatives:**
  - Scheduler calling the Cloud Build API directly with a static body can't check anything and goes stale when fugaro changes.
  - A Cloud Build trigger can't hold the per-workflow secrets without an inline copy of the request, and needs a repository connection.
  - A check inside Cloud Build pays for a build machine every night.
- **Cost:** about $0.004 a run at most (see the cost arithmetic above).
- **Security:** the check needs the provider credential, which the build account already reads. What it adds is `cloudbuild.builds.create` at project level, and that is **not** "nothing new": a request that names no service account runs as the project's default build identity, which needs no `actAs` (ruling R6).
- *Cost if wrong:* the check logic is a CLI command. Moving it into Cloud Build means changing the Scheduler target.

**R5. The build record lives in the bucket; labels are for people.**
- `builds/<slug>/<workflow>/image.json` holds the source commit, the key-file blob IDs, the image-config hash, the base ref and digest, `built_at`, the image digest, the build ID and the fugaro version.
- The build's `record` step writes it, after promotion, as the build account, whose condition covers `builds/<slug>/` and nothing else. The job accounts can't write there, since their condition has no `builds/`.
- Cloud Build's own history is the build log. There is no `buildlog/` copy.
- Why the bucket: a bucket object is one authenticated GET through the existing blob code, it can hold the check's decisions next to it, and its schema can change without a rebuild. Reading labels would mean a registry manifest plus a config-blob fetch, and new fields only after a rebuild.
- The build *also* sets OCI labels (`org.opencontainers.image.revision`, `org.opencontainers.image.created`, `dev.fugaro.base.digest`) for people and the Artifact Registry console.
- *Cost if wrong:* reading labels instead is one function in Task 10. Both are written anyway.

**R6. Per-repository build accounts and registries; base images in their own registry.**
- `gcp.BuildServiceAccountID(slug)` is built like the job accounts, but its hash is over a separate domain (`"build\x00" + slug`), so it can't collide with a job account, even of a workflow named `build`.
- `fugaro image build` and the check submit as it, and `build.service_account` in the local config is deprecated: accepted, ignored, with a warning. `fugaro-build` is retired in the runbook, disabled first and deleted a week later.
- **Base images** move to their own registry, `fugaro-base` (an installation singleton, named in the tfvars). Build accounts get `artifactregistry.reader` on it, and only the operators (and owners) write. Otherwise a leaked build token could overwrite the base that every repository's `credential` step runs while holding that repository's provider credential. The runbook pushes the M5 base there, and `base_image` points at it.
- **Derived images** move to one registry per repository, `gcp.RegistryRepoID(slug)` (`fugaro-<readable>-<12 hex>`, at most 63 characters, a separate hash domain). Its build account is its only writer.
  - Ruling: take it now. It is cheap: one resource in `modules/repo`, a registry argument to `gcp.ImageName`, and one rebuild per repository at migration (two, about $0.42), which the paused-schedule step needs anyway to write the first record.
  - Without it, the per-repository build accounts protect secrets but not images: repository A's leaked token could overwrite repository B's `:latest`, which B's job then runs with B's secrets.
  - The job of an adopted repository keeps its current image path (the legacy `fugaro` registry) until the new path has a `:latest` (T13 readiness), so the switch never points a job at a missing image.
  - The legacy `fugaro` registry stays imported, unwritten and without a cleanup policy, for the rollback. It is declared only when adopting one (`adopt_legacy_registry`, an explicit installation input, default `false`, which `fugaro init` sets from discovery), so a fresh project never gets an empty `fugaro` registry.
- **The remaining risks,** which the metadata boundary (live check 11b) and the `--network none` smoke keep closed:
  - a build account's token, if it ever leaked, could submit builds as the project's **default build identity**, since a request naming no service account needs no `actAs`. The runbook reads what that identity is and what it holds (a precondition in Task 18), and the result goes into §6.1. If it is the legacy Cloud Build account or an Editor, §6.1 documents the path from one repository to the project.
  - Cloud Build logs (`resource.type="build"`) stay in `_Default` (ruling R13).
- *Cost if wrong:* dropping the per-repository registries means one registry argument and an image-path change back.

**R7. GitHub builds.**
- Every build gets a `credential` step on the base image, running `fugaro image git-credential`. It writes the git credential file into a **Cloud Build volume** (`fugaro-creds`, mounted at `/creds`) that only the `credential`, `source` and `build` steps mount.
  - Bitbucket: the credential comes from `GIT_TOKEN`, the `bitbucket-token` secret, as in M4.
  - GitHub: the step mints an installation token from the `github-app-key` secret and `_GITHUB_APP_ID`, with the permissions `contents: read` and `metadata: read` and a life of at most an hour.
- A `prep` step (the git builder, root) creates `/creds` as mode 0700, owned by uid 1000, because the base image runs as `fugaro`.
- Nothing is written to `/workspace` or `/builder/home`, and `render` doesn't mount the volume. The bash `urlenc` copies in `cloudbuild.yaml` go away; Go formats the line.
- **The GitHub App ID** is not a secret. It is `repos.<r>.github_app_id` in the local config, set with `--github-app-id`. It is a dedicated `github_app_id` variable of the repository root, and an output of it, so `init --repo` can read it back even when every workflow has `check: off`. It reaches the job as `FUGARO_GITHUB_APP_ID` and the build as `_GITHUB_APP_ID`.
- *Cost if wrong:* if a volume is refused, the minted token can go into a Secret Manager version written by the step. That is worse, because it's a stored credential. The live check in Task 18 decides.

**R8. The per-run timeout, and price overrides.**
- `fugaro run --total-timeout 45m` writes `overrides.total_timeout` into `task.json` (the field already exists and the runner already applies it).
- `backend.LaunchSpec` gains `Timeout time.Duration`, which the GCP backend sends as the `jobs.run` override `timeout = total + TaskTimeoutSlack`.
- `Launch` derives it from the **stored** task spec, so `--retry` and a repeated `--run-id` launch with the same timeout.
- Validation happens in the CLI: a Go duration, more than the checkout's `finalize_reserve` when the checkout is the repository's, and at most `OverrideCap` (24 h). The runner checks again at bootstrap (`Spec.Apply`).
- Prices: local config `compute_prices: {<region>: {vcpu_second_usd, gib_second_usd}}`. `localcfg` validates it: both fields set, finite, greater than 0 and at most 0.001. It is applied in the CLI's cost (`ls`, `diagnose`), and it reaches the jobs as `FUGARO_COMPUTE_PRICES=<vcpu>,<gib>` through the tfvars, so the runner's PR report agrees with `ls`. The `Source` field says `local override`.
- *Cost if wrong:* the flag is one field. The price env var can be dropped, and the report would fall back to list prices.

**R9. Migration and rollback.** Adopt, don't recreate (Task 18). The order is the installation, then the sandbox, then the web repo, each a separate plan with zero deletes.
- **Rollback at any point before `fugaro-build` is retired is `fugaro init --forget`.** It undoes the behaviour M5 added, not only the state:
  1. Its first phase is a normal guarded, confirmed apply with `log_isolation = false` and `registry_cleanup.enabled = false`. That deletes the `_Default` exclusion, the sink, the view, the log bucket and every cleanup policy. `--forget` passes exactly those addresses to the guard as allowed deletes, and nothing else.
     - A deleted log bucket stays pending deletion for 7 days, and its ID can't be reused meanwhile. `--forget` prints this, together with the undelete command: `gcloud logging buckets undelete <bucket> --location=global --project <p>`. A migration retried within the week runs that undelete first (**⚠ CONFIRM**), and `fugaro init` then imports the bucket.
  2. Its second phase is `terraform state rm` of every remaining address, after a second ⚠ CONFIRM. It destroys nothing.
  - For a repository root, `init --repo --forget` does only phase 2, since a repository root holds no exclusion, and its registry's cleanup policies are harmless to M4, which reads the legacy registry.
- The runbook then restores the backed-up local config and, **always** (a checklist item, not "if broken"), redeploys each job with the M4 binary's `gcp-m4.sh --apply job`. That puts back the M4 image path, env and order, and drops `FUGARO_COMPUTE_PRICES`.
- *Cost if wrong:* nothing is lost. The legacy registry still holds the M4 images, and the bootstrap's accounts and bindings are untouched.

**R10. Terraform through `os/exec`, not `terraform-exec`.** We need exact control of argv and the environment (the allowlist), a trivial fake for hermetic tests, and nothing but `init`, `plan -out`, `show -json`, `apply <planfile>`, `output -json`, `state rm` and `version -json`. The design's §8.2 changes to match. *Cost if wrong:* swap the one file `internal/infra/tf/tf.go`.

**R11. Budget, billing and APIs.**
- Terraform enables the APIs with `disable_on_destroy = false`: Run, Storage, Secret Manager, Artifact Registry, Cloud Build, Cloud Scheduler, Logging, Monitoring, IAM and Cloud Resource Manager, plus Vertex AI when `enable_vertex` is set. Project IAM members depend on `cloudresourcemanager.googleapis.com`, which isn't enabled in the live project today.
- Terraform leaves these to the user, as preconditions the runbook checks: linking billing, and `serviceusage.googleapis.com`, which enabling APIs needs.
- The budget is **optional** (`budget = null` by default). `fugaro init --budget <amount> --budget-currency <cur> --billing-account <id>` creates one with 50%, 90% and 100% alerts, which needs billing-account permissions.
- The live 70 CAD budget stays hand-made and unmanaged.
- *Cost if wrong:* import it later, by its budget ID.

**R12. Launcher and operator IAM** (design §3.4 and §6.1). There are two lists of members, both empty by default, since an owner-only install needs neither.
- **`launchers`**, for `run`, `ls`, `logs`, `diagnose` and `cancel`:
  - the project custom role `fugaroLauncher`: `run.jobs.get`, `run.jobs.list`, `run.executions.get`, `run.executions.list`, `run.executions.cancel`, `run.operations.get`
  - the custom role `fugaroJobRunner` (`run.jobs.run`, **`run.jobs.runWithOverrides`**), granted **on each workflow job**
  - `roles/storage.objectAdmin` on the runs bucket
  - `roles/logging.viewAccessor` on the Fugaro log view (Task 16)
- **`operators`** also get, for onboarding:
  - `roles/secretmanager.secretVersionAdder` and `roles/secretmanager.viewer` on each of the repository's secrets
  - `roles/iam.serviceAccountUser` on the repository's build account (which amounts to its secrets, design §6.1), **on each job account and on `fugaro-scheduler`**, since creating jobs and the Scheduler job needs `actAs` on them
  - `fugaroBuildSubmitter`
  - `objectAdmin` on the state bucket (ruling R2)
  - `artifactregistry.writer` on `fugaro-base`
- Creating the service accounts, custom roles and project member bindings themselves still needs project-level IAM admin, so the one who runs `fugaro init` for the **installation** must be an Owner (or hold `roles/resourcemanager.projectIamAdmin`, `roles/iam.serviceAccountAdmin` and `roles/iam.roleAdmin`). §6.1 records this.
- *Cost if wrong:* the roles are one file each.

**R13. Log access is secret access.**
- In a shared project, M5 can't take `roles/logging.viewer` away from anyone. Instead, Fugaro jobs' logs (the workflow jobs and the check jobs, both labelled `fugaro=managed`) go to a dedicated log bucket through a sink: `fugaro`, location `global`, 30-day retention, all names from the tfvars.
- A project-level exclusion keeps them out of `_Default`.
- Readers need `roles/logging.viewAccessor` on the bucket's view, which Logs Viewer doesn't grant. Owners and editors can still read everything.
- `fugaro logs` and `diagnose` read through the view named in the local config (`log_view`).
- The switch is `log_isolation` (default `true`). `--forget` turns it off first (ruling R9).
- **Recorded exception:** Cloud Build logs (`resource.type="build"`) stay in `_Default`. They carry the repository's build output, never a secret value by design (secrets are file mounts; the credential never reaches a log). A filter on build tags is possible but unverified; this can move under isolation later.
- *Cost if wrong:* set `log_isolation` to `false`, and logs go back to `_Default`. The M4 code path stays.

**R14. The base image until M7.**
- `base_image` stays a dev tag, now in `fugaro-base` (`<region>-docker.pkg.dev/<p>/fugaro-base/fugaro-web-node:dev-<commit>`). The render step, the check job and the `base` trigger all use it.
- A dev tag doesn't move, so the `base` trigger fires only when `base_image` itself changes (run `fugaro init --repo` after changing it) or when that tag is repushed.
- `max_age` (14 days) is what bounds how long security fixes wait.
- From M7, `base_image` is unset, the published `:X.Y.Z` tag is refreshed nightly, the trigger does its job, and `max_age` can go back to 7 days.
- The runbook rebuilds the base from the M5 branch and pushes it to `fugaro-base`, since the check and credential commands live in the base image's `fugaro`. The bootstrap-era docs' `base` step changes to match (Task 17).
- *Cost if wrong:* none. This is configuration.

**R15. `checkMaxParallel`.**
- Live check 1b showed the `jobs/-` listing is sorted newest first across jobs, so the listing can stop at the first execution older than the horizon.
- The horizon becomes the longest task timeout among the region's `fugaro-` jobs (one `jobs.list` call), or the per-run override cap, whichever is longer, plus one hour for queueing, instead of 168 h + 24 h. `Exhaustive` goes away.
- With the web repo's 92-minute timeout and at most 24 h of overrides, that is about 25 h of executions, usually one page.
- *Cost if wrong:* the limit is soft. An execution older than the horizon that is still running goes uncounted, which can only happen with a longer override than the cap allows.

**R16. Scheduling and smoke.** Per workflow, in `fugaro.yaml`:
- `check: daily | off`. There is no `weekly`: a daily check costs nothing, and `max_age` expresses "at least every two weeks".
- The time is derived from the repository (a minute between 05:00 and 06:59 UTC), so repositories don't all build at once.
- The smoke is always the structural selftest (`basic`), run with `--network none`: with `dockerfile:` the repository controls the whole image, its `fugaro` binary included, so the smoke runs repository code. There is no `smoke:` setting in M5.
- *Cost if wrong:* `smoke: full` comes back as a new enum value, gated on live check 11b extended to `docker run`.

## Review Focus

These are the failure modes that are easiest to miss, most likely first. Each is pinned by a named test.

1. **A cleanup policy deleting a live image.** Artifact Registry cleanup deletes *versions*, not tags. So a delete rule matching the `candidate-` tag would delete the promoted version that also carries `latest`.
   - Every image registry has `KEEP` policies for the tag prefixes `latest` and (on `fugaro-base`) `dev-`, plus `most_recent_versions = 3`. Keep wins over delete.
   - The build tries to remove the candidate tag after promoting, **best-effort**. The build account holds only `artifactregistry.writer`, which lacks `artifactregistry.tags.delete`, so this normally fails with a logged warning. A leftover candidate tag is harmless: the keep rules protect the version while it is `latest` (or among the 3 newest), and the `candidate-` delete rule removes it once it isn't.
   - Pinned by T2/T3 tftests `cleanup_keeps_latest`, `cleanup_keeps_dev` and `build_role_set_on_own_registry` (writer only, no admin), T8 `TestUntagFailureIsOnlyAWarning`, and the runbook's dry-run audit check that no `latest` or `dev-` version would be deleted.
2. **A plan that destroys or replaces a live resource.** An import whose config differs in a force-new attribute is the classic case: a secret's `replication`, an account's `account_id`, or a job's `location`. Terraform plans a replace, which deletes first.
   - The wrapper's guard refuses any `delete` in `resource_changes[].change.actions` unless `--allow-delete` names the address (Task 12 `TestGuardRefusesReplace`, `TestGuardRefusesDelete`, `TestGuardAllowsNamedDelete`, `TestGuardAllowsForget`).
   - Secrets, the runs bucket and every registry have `prevent_destroy` (the Task 2 text test `TestPreventDestroy`), and jobs have `deletion_protection`.
   - `fugaro init` applies only the saved plan file it showed (Task 12 `TestApplyUsesSavedPlanOnly`).
   - The summary lists risky in-place updates first (a job's `service_account` or `image`, a bucket's `lifecycle_rule`, a registry's `cleanup_policies`), with the changed attributes (Task 12 `TestSummaryHighlightsSensitiveUpdates`).
3. **Names drifting from M4, and live IAM drifting from the code.**
   - Task 1 `TestSpecMatchesM4JobSpec` compares `internal/infra` with a golden copy of the M4 binary's `fugaro gcp job-spec --json` for the sandbox fixture, captured *before* the refactor. It includes the condition expression, byte for byte, and the title.
   - The live bindings were made by whichever M4 commit ran `gcp-m4.sh`, so discovery also reads the bucket and secret IAM policies. It requires exactly one conditional binding per job account with the spec's title and expression, or refuses with the diff (Task 13 `TestDiscoverRefusesMismatchedBinding`, `TestDiscoverCountsAdoptedBindings`).
4. **Adopting something that isn't ours.** Discovery generates an import only for a resource that exists **and** carries our mark. A marked resource of *another* repository or workflow is refused, as is a bucket in another project (Task 13 `TestDiscoverRefusesForeignSecret`, `TestDiscoverRefusesOtherRepoJob`, `TestDiscoverRefusesSADisplayName`, `TestDiscoverRefusesBucketInOtherProject`, `TestDiscoverAcceptsLegacySADisplayName`).
5. **Something billable starting unattended.** The schedule stays paused until a record exists (Task 13 `TestScheduleOnlyWithRecord`). A failed rebuild with unchanged inputs backs off rather than building again every night (Task 10 `TestCheckBacksOffAfterFailure`).
6. **The readiness gate removing a live job or pointing it at a missing image.** A job is deployed when its image exists and every mounted secret has an enabled version, **or when the job already exists**. An existing job keeps its current image path until the new path has a `:latest` (Task 13 `TestReadinessKeepsExistingJob`, `TestReadinessKeepsImageUntilBuilt`, plus the guard).
7. **A secret value reaching Terraform.** No `google_secret_manager_secret_version`, resource or data source, anywhere under `deploy/terraform` (Task 2 `TestNoSecretVersionsInTerraform`). The tfvars carry secret IDs only (Task 1 `TestTfvarsHoldNoValues`, which seeds a fake secret value and greps for it).
8. **The build credential or build token reaching repository code.** The credential lives only in the `fugaro-creds` volume, and `render` and `smoke` don't mount it. The candidate's smoke runs through `docker run --network none`, never as a Cloud Build step, which would be on the `cloudbuild` network with the metadata server (Task 7 `TestCloudBuildCredentialOnlyInVolume`, Task 8 `TestSmokeIsNotAStep`).
9. **A rebuild that breaks every run, or fails silently.**
   - `:latest` moves, by digest, only after the candidate's smoke passes and only when no newer record exists (Task 8 `TestPromoteAfterSmoke`, `TestFailedSmokeLeavesLatest`, `TestGateSkipsWhenRecordNewer`).
   - A failed rebuild **and a failed check** show up in `fugaro ls`, `fugaro image status` and the optional alert (Task 10 `TestCheckReportsFailedRebuild`, `TestCheckFailureIsLogged`; Task 11 `TestLsWarnsRebuildFailed`, `TestLsWarnsCheckStale`).
10. **A trigger that misses.**
    - The image-config trigger covers `image:`, `dockerfile:`, the Dockerfile's blob, the `base` name and `setup`.
    - The key-file trigger covers every file the workflow's cache key globs match, including the per-base defaults.
    - The salt covers the embedded `cloudbuild.yaml` and the fugaro version.
    - Pinned by Task 10's table tests, one per trigger, plus `TestCheckRebuildsWhenRecordMissing` and `TestCheckRebuildsWhenBuiltCommitNotOnBranch`.
11. **`--retry` dropping the timeout override** (Task 5 `TestRetryKeepsTimeoutOverride`).
12. **Terraform's environment.** Only allowlisted variables reach the child, `TF_CLI_CONFIG_FILE` is fugaro's own file, and impersonation is refused (Task 12 `TestTerraformEnvAllowlist`, `TestRefusesImpersonation`).

## File Structure

| Path | Responsibility | Task |
|---|---|---|
| `internal/config/{config,validate,example.yaml}`, `schemas/fugaro.schema.json` | the `rebuild:` block of a workflow | 9 |
| `internal/localcfg/localcfg.go` | M5 fields: `terraform`, `compute_prices`, `log_view`, `scheduler_region`, `registry_host`, `repos.<r>.github_app_id`; `build.service_account` and `registry` deprecated | 4 |
| `internal/cli/{exec,ls,diagnose,cloud}.go`, `internal/runview/cost.go` | price overrides in the runner's estimate (`exec.go` builds it today from `gcp.ListPrices`) and in `ls` and `diagnose` | 4 |
| `internal/backend/gcp/names.go` | `BuildServiceAccountID`, `RegistryRepoID`, `CheckJobName`, `SchedulerRegion`, display names, `BucketCondition`, `BucketConditionTitle` | 1 |
| `internal/infra/spec.go`, `tfvars.go` | the installation, repository and workflow specs; the tfvars JSON | 1 |
| `internal/cli/gcpcmd.go` | `job-spec` becomes a printer over `internal/infra` | 1 |
| `deploy/terraform/terraform.go`, `deploy/terraform/gcp/**` | the modules, roots, tests, lint config | 2, 3, 16 |
| `.github/workflows/ci.yml` | the `terraform` job | 2, 12 |
| `internal/backend/backend.go`, `internal/backend/gcp/run.go`, `internal/gcpfake/run.go`, `internal/cli/run.go` | the timeout override; the cheaper `max_parallel` | 5, 6 |
| `images/derived/cloudbuild.yaml`, `images/cloudbuild_test.go`, `internal/backend/gcp/build.go`, `internal/gcpfake/build.go`, `internal/cli/image.go`, `internal/gitprov/github/apptoken.go` | the credential step, GitHub, build accounts; candidate → smoke → gate → promote → record | 7, 8 |
| `internal/imagecheck/*`, `internal/gcpfake/registry.go`, `internal/cli/imagecheck.go` | `fugaro image check`, `fugaro image status` | 8 (record), 10 |
| `images/derived/Dockerfile.tmpl`, `internal/runner/image.go`, `schemas/result.schema.json`, `internal/runview/*`, `internal/cli/ls.go` | image age at launch; rebuild and check status in `ls` | 11 |
| `internal/infra/tf/*`, `internal/infra/tf/faketerraform/main.go` | the Terraform wrapper, the guard, the environment allowlist, the fake | 12 |
| `internal/infra/discover.go`, `imports.go`, `readiness.go`, `internal/gcpfake/{iam,artifactregistry,crm}.go` | ownership checks, live IAM checks, imports, readiness, the paused schedule | 13 |
| `internal/infra/statebucket.go`, `installation.go`, `workdir.go`, `internal/cli/init.go`, `internal/cli/root.go` | `fugaro init` for the installation, `--forget` | 14 |
| `internal/infra/repo.go`, `internal/cli/init.go`, `internal/e2e/init_test.go` | `fugaro init --repo` | 15 |
| `internal/backend/gcp/logs.go`, `internal/gcpfake/logging.go` | reading logs through the Fugaro log view | 16 |
| `docs/design/v1.md`, `docs/gcp-setup.md` (new), `docs/gcp-bootstrap.md`, `docs/gcp-live-checklist.md`, `plugin/skills/onboard/SKILL.md`, `README.md` | docs | 17 |
| — (controller-run) | live bring-up and migration | 18 |

## Task dependency graph and parallelism

Each task's tests run at the merge point where its dependencies are already on `m5`:

| Task | Depends on | Why |
|---|---|---|
| T1 infra spec, names | T4, T9 | prices go into the job env; `rebuild:` into the check spec |
| T2 installation module, CI | — | its tftest uses an inline `variables {}` block, not a T1 fixture |
| T3 repo and workflow modules | T1, T2 | T1's golden repo tfvars; T2's roots and CI |
| T4 localcfg, prices | — | |
| T5 timeout override | — | |
| T6 `max_parallel` | T5 | `run.go` and `gcp/run.go` after T5 |
| T7 credential step, GitHub, build accounts, per-repo registries | T1 | `BuildServiceAccountID`, `RegistryRepoID`, the specs |
| T8 candidate, smoke, gate, promote, record | T7 | `cloudbuild.yaml` after T7 |
| T9 `rebuild:` config | — | |
| T10 `image check`, `image status` | T8, T9 | the record's format; the trigger settings |
| T11 image age, status in `ls` | T10 | `check.json`'s format |
| T12 Terraform wrapper, guard, environment | — | |
| T13 discovery, imports, readiness | T1, T3, T12 | specs; the module addresses the import tests validate against; the import file format |
| T14 `fugaro init` (installation) | T2, T4, T12, T13, (T16) | module; local config; wrapper; discovery; T16's logging addresses are `--forget`'s allow-list, checked at M3 |
| T15 `fugaro init --repo` | T3, T7, T8, T10, T14 | module; builds; the check job's command |
| T16 log isolation | T2 | the installation module |
| T17 design and docs | T11, T15, T16 | records every ruling |
| T18 live runbook | all | |

```
T9 ─► T4 ─► T1 ─┬─► T7 ─► T8 ─► T10 ─► T11 ──────────────┐
                │                 ▲ (T9)                 │
T2 ─────────────┴─► T3 ─► T13 ─► T14 ───────────────────┼─► T15 ─► T17 ─► T18
T12 ────────────────────▲ (T13 needs T12)                │
T2 ─► T16 ───────────────────────────────────────────────┘
T5 ─► T6   (independent)
```

If only one stream runs, go in the order T9, T4, T1, T2, T3, T5, T6, T7, T8, T10, T11, T12, T13, T14, T16, T15, T17, T18.

## Parallel lanes

Three worktree lanes branch from `m5` and merge back at three points. Each lane rebases on `m5` after each merge point.

| Lane | Branch | Tasks, in order | Count |
|---|---|---|---|
| **A: Terraform and CLI carry-overs** | `m5-a` | T2 → T5 → T6 → *M1* → T3 → *M2* → T16 → *M3* | 5 |
| **B: specs and `init`** | `m5-b` | T9 → T4 → T1 → T7 → *M1* → *M2* → T13 → T14 → *M3* | 6 |
| **C: wrapper, builds and the check** | `m5-c` | T12 → *M1* → T8 → *M2* → T10 → T11 → *M3* | 4 |
| on `m5` | — | *M3* → T15 → T17 → T18 | |

Lane B is idle between M1 and M2 (T13 needs T3's modules). Its worker reviews T3 and T8 there.

**Merge points:**
- **M1:** T1, T2, T4, T5, T6, T7, T9 and T12. T3 needs T1 and T2; T8 needs T7.
- **M2:** T3 and T8. T13 needs T3's modules; T10 needs T8's record.
- **M3:** T10, T11, T13, T14 and T16. T15 needs them all. **At the M3 merge**, the merger adds `internal/infra/forget_addresses_test.go` (`TestForgetAddressesExist`): every address in T14's `--forget` allow-list (the data in `internal/infra/forget.go`) exists in the embedded module, checked with T13's matcher. It needs T16's `logging.tf`, which is why it waits for M3. T14 depends on T16 for that list; its own tests use the list as data.

**Files each task touches:**

| Task | Files |
|---|---|
| T1 | `internal/backend/gcp/{names,names_test}.go`, `internal/infra/{spec,tfvars,spec_test}.go`, `internal/infra/testdata/m4-jobspec-sandbox.json`, `internal/infra/testdata/m4-jobspec-sandbox.fields.tsv`, `internal/infra/testdata/capture-m4-jobspec.sh`, **`deploy/terraform/gcp/roots/repo/tests/testdata/*.tfvars.json`** (written by `go test -update` only), `internal/cli/{gcpcmd,gcpcmd_test}.go` |
| T2 | `deploy/terraform/terraform.go`, `deploy/terraform/terraform_test.go`, `deploy/terraform/gcp/modules/installation/*`, `deploy/terraform/gcp/roots/installation/*`, `deploy/terraform/gcp/.tflint.hcl`, **`.github/workflows/ci.yml`** |
| T3 | `deploy/terraform/gcp/modules/{repo,workflow}/*`, `deploy/terraform/gcp/roots/repo/*` (except T1's testdata) |
| T4 | **`internal/localcfg/localcfg.go`**, `localcfg_test.go`, **`internal/cli/exec.go`**, **`internal/cli/{ls,diagnose,cloud}.go`** and tests, `internal/runner/prices.go` (`PricesFromEnv`), `internal/runview/cost.go` |
| T5 | `internal/backend/backend.go`, **`internal/backend/gcp/run.go`**, `run_test.go`, **`internal/gcpfake/run.go`**, **`internal/cli/run.go`**, `run_test.go` |
| T6 | **`internal/backend/backend.go`**, **`internal/backend/gcp/run.go`**, **`internal/gcpfake/run.go`**, **`internal/cli/run.go`**, `internal/cli/lifecycle_test.go` |
| T7 | **`images/derived/cloudbuild.yaml`**, **`images/cloudbuild_test.go`**, **`internal/backend/gcp/build.go`**, `build_test.go`, **`internal/gcpfake/build.go`**, **`internal/cli/image.go`**, `image_cloud_test.go`, `internal/cli/gitcred.go`, `internal/gitprov/github/apptoken.go` |
| T8 | **the same five as T7**, plus `internal/image/selftest.go` (spec output), `internal/imagecheck/record.go`, **`images/derived/Dockerfile.tmpl`** (declares `ARG FUGARO_BUILT_AT` only) |
| T9 | `internal/config/{config,validate,config_test,example.yaml}`, `schemas/fugaro.schema.json`, `testdata/config/{valid,invalid}/rebuild-*.yaml` |
| T10 | `internal/imagecheck/*` (not `record.go`'s types), `internal/gcpfake/registry.go`, `internal/cli/imagecheck.go`, **`internal/cli/image.go`** (registering the subcommands) |
| T11 | **`images/derived/Dockerfile.tmpl`**, `internal/image/render_test.go`, **`internal/runner/runner.go`**, `internal/runner/image.go`, `schemas/result.schema.json`, `internal/runview/*`, **`internal/cli/ls.go`** and its test |
| T12 | `internal/infra/tf/*`, `internal/infra/tf/faketerraform/main.go`, **`.github/workflows/ci.yml`** (one step: `go test -tags terraform ./internal/infra/...`) |
| T13 | `internal/infra/{discover,imports,readiness}.go` and tests, `internal/gcpfake/{iam,artifactregistry,crm}.go` (new), **`internal/gcpfake/{gcs,secrets,run}.go`** (bucket and secret `getIamPolicy`, bucket `get` with `projectNumber`, `jobs.get` where missing) |
| T14 | `internal/infra/{statebucket,installation,workdir}.go` and tests, **`internal/cli/init.go`**, **`internal/cli/root.go`**, **`internal/gcpfake/gcs.go`** (bucket create, `setIamPolicy`) |
| T15 | `internal/infra/repo.go`, **`internal/cli/init.go`**, `internal/e2e/init_test.go` |
| T16 | `deploy/terraform/gcp/modules/installation/logging.tf` and its tftest cases, `internal/backend/gcp/{logs,logs_test}.go`, `internal/gcpfake/logging.go`, **`internal/cli/cloud.go`** (passes `log_view`) |
| T17 | `docs/**`, `plugin/skills/onboard/SKILL.md`, `README.md` |

**Hot files** (bold above), and how they're kept safe:
- **`.github/workflows/ci.yml`:** T2 (A) creates the `terraform` job, and T12 (C) adds one step to it, both before M1. At M1 keep both: T2's job layout, plus T12's step at its end.
- **`internal/localcfg/localcfg.go`:** T4 only. T14 and T15 call it and never edit it.
- **`internal/runner/runner.go`:** T11 only (C, after M2). T4 adds `internal/runner/prices.go` as a new file and wires it in `internal/cli/exec.go`.
- **`internal/cli/exec.go`, `ls.go`, `diagnose.go`:** T4 (B, before M1), then T11 edits `ls.go` (C, after M2). The merge points keep them in order.
- **`internal/cli/cloud.go`:** T4 (B, before M1), then T16 (A, after M2). Each adds one field to `cloudEnv` and one line to `openCloud`. On a conflict, keep both.
- **`internal/cli/run.go`, `internal/backend/gcp/run.go`, `internal/backend/backend.go`:** T5, then T6, both in lane A before M1.
- **`internal/gcpfake/run.go`:** T5 and T6 (A, before M1), then T13 (B, after M2).
- **`internal/gcpfake/gcs.go`, `secrets.go`:** lane B only, T13 then T14, both after M2. T8 and T10 test their records against `memblob` and don't edit the GCS fake.
- **`cloudbuild.yaml`, `images/cloudbuild_test.go`, `internal/backend/gcp/build.go`, `internal/gcpfake/build.go`, `internal/cli/image.go`:** T7 (B, before M1), then T8 (C, M1–M2), then T10 (C, after M2). Strictly in that order through the merge points.
- **`images/derived/Dockerfile.tmpl`:** T8 declares the `ARG`, then T11 adds the step that writes `/etc/fugaro/image.json`. Both are in lane C, in that order.
- **`internal/imagecheck/`:** T8 creates `record.go` (the `Record` type, `KeyFiles`, `ImageConfigHash`). T10 adds the rest of the package. Both are in lane C.
- **`internal/cli/root.go`:** T14 only (`newInitCmd`). The image subcommands go on `newImageCmd` in `image.go`.
- **`internal/cli/init.go`:** T14 creates it. T15 extends it on `m5` after M3.
- **`internal/infra/`:** lane B owns it, except `internal/infra/tf/`, which T12 (lane C) creates before M1. No file is shared.
- **The T1 golden tfvars** (`deploy/terraform/gcp/roots/repo/tests/testdata/`): written only by `go test ./internal/infra -run TestGoldenTfvars -update`. Lane A never edits them by hand. If T3 needs another field, lane A asks lane B, and B regenerates them before M2.

---

The tasks below appear in single-stream order: 9, 4 and 1 come first, because T1 depends on both.

### Task 9: The `rebuild:` block of a workflow in `fugaro.yaml`

**Files:**
- Modify: `internal/config/config.go` (`Rebuild` on `Workflow`), `internal/config/validate.go`, `internal/config/example.yaml`, `internal/config/config_test.go`
- Modify: `schemas/fugaro.schema.json`
- Create: `testdata/config/valid/rebuild-full.yaml`, `testdata/config/invalid/rebuild-{bad-check,bad-age,bad-glob,unknown-field}.yaml`

**Interfaces:**
- Produces: `config.Rebuild{Check string; MaxAge Duration; Lockfiles, Base *bool; Paths []string}` and `(Rebuild).Defaults() Rebuild`, which gives `check: daily`, `max_age: 14d`, `lockfiles: true`, `base: true` and `paths: []`. The 14-day default holds until M7 publishes a moving base (ruling R14); M7 revisits it.

```yaml
    rebuild:                 # optional; the daily image check (design §7.2)
      check: daily           # daily | off (off: rebuilt only by fugaro image build)
      max_age: 14d           # rebuild an image older than this; 0 turns the age trigger off
      lockfiles: true        # rebuild when a cache key file changed on the base branch
      base: true             # rebuild when the base image's digest moved
      paths: []              # globs; a change under one on the base branch forces a rebuild
```

A change to `image:`, `dockerfile:` (or the file it names), `base` or `image.setup` always rebuilds, and can't be turned off: it makes the image wrong, not just stale.

- **Validation**, each problem reported at `workflows.<w>.rebuild.<field>`:
  - `check` is `daily` or `off`.
  - `max_age` is `0`, or between `1h` and `90d`. `d` is accepted as days, as `ls --since` accepts it.
  - Each `paths` entry is a doublestar pattern that `doublestar.ValidatePattern` accepts. It is relative, contains no `..` component, doesn't start with `/` and isn't empty.
  - There is no `smoke` field (ruling R16). An unknown field is an error, as everywhere in `fugaro.yaml`.
- The schema mirrors all of this. `fugaro config example` documents it, commented out.

- [ ] **Step 1: Write the failing tests.** `TestRebuildDefaults` (no block gives the defaults), `TestRebuildValidation` (a table over the invalid fixtures, asserting each `Problem.Path`), and `TestExampleValidates`, which exists and must keep passing.
- [ ] **Step 2:** Run `go test ./internal/config/ ./schemas/ -run 'Rebuild|Example|Schema'`. Expected: FAIL.
- [ ] **Step 3: Implement.** A `Duration` that accepts `d` already exists for timeouts; reuse it.
- [ ] **Step 4:** `go test -race ./internal/config/ ./schemas/`. Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "config: a workflow's rebuild settings for the daily image check"`

---

### Task 4: The local config's M5 fields, and compute price overrides

**Files:**
- Modify: `internal/localcfg/localcfg.go`, `internal/localcfg/localcfg_test.go`
- Create: `internal/runner/prices.go` (`PricesFromEnv`) and its test
- Modify: `internal/cli/exec.go`, which builds the runner's cost from `gcp.ListPrices(os.Getenv("FUGARO_REGION"))` today (around line 142) and now calls `PricesFromEnv` first
- Modify: `internal/cli/ls.go`, `internal/cli/diagnose.go`, `internal/cli/cloud.go` (`env.prices(region)`), `internal/runview/cost.go`, and their tests

**Interfaces:**
- Produces:
  - `localcfg.Config.Terraform struct{ StateBucket string \`yaml:"state_bucket"\` }`
  - `localcfg.Config.ComputePrices map[string]localcfg.Price`, with `Price{VCPUSecondUSD, GiBSecondUSD float64}`
  - `localcfg.Config.LogView string`: `projects/<p>/locations/global/buckets/<b>/views/<v>`, or empty for M4 behaviour
  - `localcfg.Config.SchedulerRegion string`: set by `fugaro init` from `gcp.SchedulerRegion`
  - `localcfg.Config.RegistryHost string`: `<region>-docker.pkg.dev/<project>`, the prefix of every registry. `registry` stays readable as the **legacy** shared registry, which M5 only reads (rollback and the discovery of existing job images).
  - `localcfg.Repo.GitHubAppID string \`yaml:"github_app_id,omitempty"\``
  - `(*Config).Prices(region string) backend.Prices`: the override (with `Source: "local override"`), else `gcp.ListPrices`
  - `(*Config).Warnings() []string`, which reports a set `build.service_account` as deprecated (ruling R6)
  - `runner.PricesFromEnv(getenv) (backend.Prices, bool, error)`, which reads `FUGARO_COMPUTE_PRICES=<vcpu>,<gib>`
- Validation:
  - Price: both fields finite, greater than 0 and at most 0.001, and the key a valid region.
  - `state_bucket` matches `bucketRE`.
  - `log_view` matches `^projects/[a-z][a-z0-9-]{4,28}[a-z0-9]/locations/[a-z0-9-]+/buckets/[a-z0-9_-]+/views/[A-Za-z0-9_-]+$`.
  - `scheduler_region` matches `regionRE`.
  - `registry_host` is `<region>-docker.pkg.dev/<project>`, and names this config's project.
  - `github_app_id` is 1–20 digits.
  - The `Load` error for a missing file now says "run fugaro init".

- [ ] **Step 1: Write the failing tests.**
  - `TestPricesOverride`: an override for `us-east5` is returned with its `Source`; `us-central1` gets list price.
  - `TestPricesValidation`: `0`, a negative value, `NaN` and `1.0` are each refused, with the path in the message.
  - `TestBuildServiceAccountDeprecated`: a set `build.service_account` parses, with one warning.
  - `TestGitHubAppIDValidation`.
  - `TestRegistryHostValidation`.
  - `TestRunnerPricesFromEnv`: a well-formed value; a malformed one is an error (the runner logs a warning and uses list prices); unset means list price.
  - `TestExecUsesPriceEnv` in `internal/cli`.
  - `TestLsUsesPriceOverride` and `TestDiagnoseUsesPriceOverride` in `internal/cli`, through `newCloudFixture` with an override in the config.
- [ ] **Step 2:** Run `go test ./internal/localcfg/ ./internal/runner/ ./internal/cli/ -run 'Price|Deprecated|GitHubAppID|RegistryHost'`. Expected: FAIL.
- [ ] **Step 3: Implement.**
  - Strict decoding stays on.
  - `ls` and `diagnose` get their prices through `env.lc.Prices(region)` instead of `gcp.ListPrices`.
  - `exec` builds the runner's prices with `PricesFromEnv`, falling back to `gcp.ListPrices(FUGARO_REGION)`.
- [ ] **Step 4:** `go test -race ./internal/localcfg/ ./internal/runner/ ./internal/runview/ ./internal/cli/`. Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "localcfg: M5 fields and compute price overrides, applied by ls, diagnose and the runner"`

---

### Task 1: One spec for every name: `internal/infra`, new naming helpers, and the tfvars

**Files:**
- Modify: `internal/backend/gcp/names.go`, `internal/backend/gcp/names_test.go`
- Create: `internal/infra/spec.go`, `internal/infra/tfvars.go`, `internal/infra/spec_test.go`
- Create: `internal/infra/testdata/capture-m4-jobspec.sh`, `internal/infra/testdata/m4-jobspec-sandbox.json`, `internal/infra/testdata/m4-jobspec-sandbox.fields.tsv`
- Create (generated): `deploy/terraform/gcp/roots/repo/tests/testdata/{bitbucket-oauth,github-vertex}.tfvars.json`. The installation root's test has its own inline variables (Task 2), so T1 writes nothing there.
- Modify: `internal/cli/gcpcmd.go` (a printer over `infra.Workflow`), `internal/cli/gcpcmd_test.go`

**Step 0, before any code change.** Capture the golden output with the current binary. `internal/infra/testdata/capture-m4-jobspec.sh` does it, is committed, and can be rerun (it overwrites, never appends):

```bash
#!/usr/bin/env bash
# Captures the M4 job spec of the sandbox fixture. Run from the repository root, before T1 changes any code.
set -euo pipefail
out=internal/infra/testdata
bin=$(mktemp -d)/fugaro-m4
go build -o "$bin" ./cmd/fugaro
work=$(mktemp -d)
trap 'rm -rf "$work" "$(dirname "$bin")"' EXIT
cp -R deploy/bootstrap/sandbox "$work/sandbox"
git -C "$work/sandbox" init -q -b master
git -C "$work/sandbox" remote add origin https://bitbucket.org/acme/sandbox.git
cat > "$work/config.yaml" <<'YAML'
version: 1
project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234
registry: us-east5-docker.pkg.dev/proj-1234/fugaro
build: { service_account: fugaro-build@proj-1234.iam.gserviceaccount.com }
user: test@example.com
repos:
  acme/sandbox: { provider: bitbucket, base_branch: master, workflows: [web] }
YAML
cd "$work/sandbox"
export FUGARO_CONFIG="$work/config.yaml" HOME="$work" GIT_CONFIG_GLOBAL=/dev/null
"$bin" gcp job-spec --repo acme/sandbox --workflow web --json > "$OLDPWD/$out/m4-jobspec-sandbox.json"
for f in bucket-condition sa-display-name secret-names build-secret-ids labels env secrets; do
  printf '%s\t%s\n' "$f" "$("$bin" gcp job-spec --repo acme/sandbox --workflow web --field "$f" | tr '\n' ';')"
done > "$OLDPWD/$out/m4-jobspec-sandbox.fields.tsv"
```

Commit these three files first, alone: `git commit -m "infra: golden M4 job spec of the sandbox fixture"`.

**Interfaces:**
- Produces, in `gcp`:
  - `BuildServiceAccountID(slug) string`: `derive(sanitize("fugaro-b-"+readableSlug(slug)), "build\x00"+slug, "", 30, 8)`, a separate hash domain from `ServiceAccountID`
  - `RegistryRepoID(slug) string`: `fugaro-<readable>-<12 hex>`, at most 63 characters, over the domain `"registry\x00"+slug`
  - `CheckJobName(slug) string`: a `fugarochk-` prefix, a separate domain, at most 49 characters
  - `SchedulerJobName(slug) string`, built the same way
  - `SchedulerRegions []string` (the Cloud Scheduler locations list, with the date checked and a link in a comment, as `prices.go` does) and `SchedulerRegion(region) string`: the region when it is listed, else a fixed nearest one (`us-east5` → `us-east4`), else an error naming `--scheduler-region`
  - `JobSADisplayName(slug, wf)` (`Fugaro job …`), `LegacyJobSADisplayName(slug, wf)` (`Fugaro M4 job …`), `BuildSADisplayName(slug)`
  - `BucketCondition(bucket, prefixes []string, slug) string`: the job's with `runs`, `cache` and `locks`, and the build account's with `builds`
  - `BucketConditionTitle(saID) string`, which is `"fugaro-" + saID`, as the bootstrap set it
- Produces, in `infra`:
  - `type Inputs struct{ LC *localcfg.Config; Repo string; Cfg *config.Config; RepoURL string; GitHubAppID string; Installation InstallationOutputs }`
  - `Workflow(in Inputs, name string) (WorkflowSpec, error)`, `Repo(in Inputs) (RepoSpec, error)`, `Installation(lc, InstallOptions) (InstallationSpec, error)`
  - `InstallationSpec` names the singletons: `scheduler_service_account_id` (`fugaro-scheduler`), `role_ids` (`fugaroLauncher`, `fugaroJobRunner`, `fugaroBuildSubmitter`), `base_registry` (`fugaro-base`), `legacy_registry` (`fugaro`), and the log `bucket` (`fugaro`), `view` (`fugaro-runs`), `sink` (`fugaro-jobs`) and `exclusion` (`fugaro-jobs-from-default`). The module validates their shapes.
  - `RepoVars(spec RepoSpec) ([]byte, error)` and `InstallationVars(spec) ([]byte, error)`, which give deterministic, sorted-key JSON
- The tfvars shape (repository root). The object attributes match the HCL variables of Task 3 one to one:

```json
{
  "project": "proj-1234", "region": "us-east5",
  "installation": {
    "runs_bucket": "fugaro-runs-proj-1234", "registry_host": "us-east5-docker.pkg.dev/proj-1234",
    "base_registry": "fugaro-base", "scheduler_service_account": "fugaro-scheduler@proj-1234.iam.gserviceaccount.com",
    "role_ids": { "job_runner": "projects/proj-1234/roles/fugaroJobRunner", "build_submitter": "projects/proj-1234/roles/fugaroBuildSubmitter" },
    "launchers": [], "operators": []
  },
  "github_app_id": null,
  "repo": {
    "name": "acme/sandbox", "provider": "bitbucket", "slug": "acme-sandbox-…", "label": "acme-sandbox-…",
    "secrets": { "bitbucket-token": "fugaro-acme-sandbox-bitbucket-token-…", "claude-oauth-token": "…", "sandbox-probe": "…" },
    "registry": { "repository_id": "fugaro-acme-sandbox-…", "cleanup_dry_run": true },
    "build_service_account": { "account_id": "fugaro-b-acme-sandbo-…", "display_name": "Fugaro build acme-sandbox-…" },
    "build_secrets": ["bitbucket-token", "sandbox-probe"],
    "build_bucket_condition": { "title": "fugaro-fugaro-b-…", "expression": "resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/builds/acme-sandbox-…/\")" },
    "check": { "job": "fugarochk-acme-sandbox-…", "image": "<base_image>", "scheduler_job": "fugarochk-acme-sandbox-…",
               "scheduler_region": "us-east4", "schedule": "17 5 * * *", "paused": true,
               "env": { … }, "secret_env": { "FUGARO_BITBUCKET_TOKEN": "bitbucket-token" } },
    "workflows": {
      "web": {
        "job": "fugaro-acme-sandbox-web-…",
        "service_account": { "account_id": "fugaro-acme-sandbox-web-…", "display_name": "Fugaro job acme-sandbox-… web" },
        "image": "us-east5-docker.pkg.dev/proj-1234/fugaro-acme-sandbox-…/acme-sandbox-web-…:latest",
        "cpu": "1", "memory": "2Gi", "task_timeout_s": 1320,
        "env": { "FUGARO_BACKEND": "cloud-run", "FUGARO_BUCKET": "gs://…", "FUGARO_PROJECT": "…", "FUGARO_REGION": "…", "FUGARO_SECRET_ENVS": "…" },
        "secret_env": { "CLAUDE_CODE_OAUTH_TOKEN": "claude-oauth-token", "FUGARO_BITBUCKET_TOKEN": "bitbucket-token", "SANDBOX_PROBE": "sandbox-probe" },
        "bucket_condition": { "title": "fugaro-fugaro-acme-sandbox-web-…", "expression": "… || … || …" },
        "vertex": false, "deploy_job": true
      }
    }
  }
}
```

- **`secret_env` maps env → logical name**, and `build_secrets` lists logical names. The module looks each up in its `secrets` map for the ID. The container env uses the secret's `.secret_id` (the same-project short form the live jobs store), never `.id`, which would plan an update.
- `check` is `null` when every workflow has `rebuild.check: off`.
  - `schedule` is `M H * * *`, with the minute and hour from `sha256(slug)` in 05:00–06:59 UTC.
  - `paused` defaults to `true`. Task 13 sets it to `false` only when every checked workflow has a build record.
  - `scheduler_region` comes from `lc.SchedulerRegion`, else `gcp.SchedulerRegion(region)`.
- `check.env` holds:
  - the M4 platform variables
  - `FUGARO_CHECK_SPEC`: JSON with the repository, provider, `https` URL, base branch, the workflows the check covers, the registry, the build account's email, the build machine type and region, and the base image
  - `FUGARO_GITHUB_APP_ID` for GitHub
  - `FUGARO_SECRET_ENVS`
- GitHub repositories mount `github-app-key` (`FUGARO_GITHUB_APP_PRIVATE_KEY`) and set `FUGARO_GITHUB_APP_ID`, and `github_app_id` is set at the top level. That lifts M4's "Bitbucket only" refusal from the spec, though not from `gcp-m4.sh`.
- `FUGARO_COMPUTE_PRICES` is in `env` when the local config has an override for the region.
- `deploy_job` is `true` in the spec, and `image` is the new per-repository path. Task 13's readiness gate may clear `deploy_job`, or keep an existing job's current image.

- [ ] **Step 1: Write the failing tests** in `internal/infra/spec_test.go`:
  - `TestSpecMatchesM4JobSpec`: from the same fixture, every field of `m4-jobspec-sandbox.json` and every row of the `.fields.tsv` equals the spec's. The condition expression and the title are compared byte for byte. The job account's display name equals the M4 one only through `LegacyJobSADisplayName`. The image path is the one exception: the spec's is the new per-repository registry, and `LegacyImage` (the M4 path) equals the golden.
  - `TestBuildConditionCoversRecordPaths`: `builds/<slug>/<workflow>/image.json` and `check.json` fall inside the build condition, with the trailing slash. `builds/<slug>-x/…` and `runs/<slug>/…` don't.
  - `TestBuildSAIDDisjoint`: for 2,000 random slugs and the workflows `build`, `web`, `b` and `x`, `BuildServiceAccountID(slug)` never equals `ServiceAccountID(slug, wf)`, stays within 6–30 characters of `[a-z0-9-]`, and starts with a letter.
  - `TestRegistryRepoID`: 1–63 characters of `[a-z0-9-]`, starts with a letter, doesn't end in `-`, isn't `fugaro` or `fugaro-base`, and is distinct across 2,000 slugs.
  - `TestCheckJobNameNotFugaroPrefix`: it never starts with `fugaro-`.
  - `TestSchedulerRegion`: `us-east5` gives `us-east4`, a listed region gives itself, and an unknown one is an error.
  - `TestGitHubSpec`: mounts `github-app-key`, sets the App ID env and `github_app_id`, uses `x-access-token` as the git user, and needs an App ID (a missing one is a user error naming `--github-app-id`).
  - `TestVertexSpec`: `vertex: true`, plus the `CLOUD_ML_REGION` and `ANTHROPIC_VERTEX_PROJECT_ID` env.
  - `TestTfvarsHoldNoValues`: the tfvars JSON contains no byte sequence of a fake secret value put in the environment and in the gcpfake secrets store, and no key named `value`, `secret_data` or `payload`.
  - `TestGoldenTfvars` (with `-update`): the fixtures in `deploy/terraform/gcp/roots/repo/tests/testdata/` equal the generated JSON. This is the Go side of the Go↔Terraform contract.
  - `TestJobSpecCommandUnchanged` in `internal/cli`: `fugaro gcp job-spec --json` still prints the golden M4 output for Bitbucket, the legacy image path included, so `gcp-m4.sh` works for a rollback.
- [ ] **Step 2:** Run `go test ./internal/infra/ ./internal/backend/gcp/ ./internal/cli/ -run 'Spec|BuildSA|BuildCondition|RegistryRepo|CheckJob|SchedulerRegion|Tfvars|JobSpec'`. Expected: FAIL.
- [ ] **Step 3: Implement.** Move `buildJobSpec`'s logic into `infra.Workflow`, unchanged except where the interfaces above say. `jobSpec` becomes a view that maps `WorkflowSpec` onto the M4 JSON field names, with `LegacyJobSADisplayName` and `LegacyImage`. Then run `go test ./internal/infra -run TestGoldenTfvars -update`.
- [ ] **Step 4:** `go test -race ./internal/infra/ ./internal/backend/gcp/ ./internal/cli/ ./deploy/bootstrap/`. Expected: PASS. The bootstrap's tests prove that the script still gets the same names.
- [ ] **Step 5: Commit.** `git commit -m "infra: one spec for every name, label, env and IAM condition; build accounts, registries and check jobs"`

---

### Task 2: The installation module, the roots skeleton, and CI

**Files:**
- Create: `deploy/terraform/terraform.go` (package `terraform`, `//go:embed all:gcp`, `FS embed.FS`), `deploy/terraform/terraform_test.go`
- Create: `deploy/terraform/gcp/modules/installation/{versions,variables,apis,bucket,registry,iam,scheduler,budget,alert,outputs}.tf`
- Create: `deploy/terraform/gcp/roots/installation/{main.tf,variables.tf,outputs.tf,.terraform.lock.hcl}`, `deploy/terraform/gcp/roots/installation/tests/installation.tftest.hcl`
- Create: `deploy/terraform/gcp/.tflint.hcl`, `deploy/terraform/.trivyignore` (empty unless a finding is justified in a comment)
- Modify: `.github/workflows/ci.yml` (the new `terraform` job)

**Interfaces:**
- Module `installation` variables:
  - `project`, `region`
  - `runs_bucket` (validated `^fugaro-runs-`)
  - `names`, an object of the installation singletons (T1's `InstallationSpec`): `legacy_registry` (`fugaro`), `base_registry` (`fugaro-base`), `scheduler_service_account_id`, `role_ids`, and `log = {bucket, view, sink, exclusion}`. Each is validated for shape only.
  - `lifecycle = {runs_days = 90, cache_custom_time_days = 30, cache_age_days = 180}`: the same three rules the bootstrap set, so the adopted bucket's lifecycle doesn't change
  - `enable_vertex` (false), `manage_apis` (true)
  - `launchers`, `operators` (lists of IAM members, validated `^(user|group|serviceAccount|domain):`)
  - `state_bucket` (for the operators' `objectAdmin` member, ruling R2)
  - `budget` (nullable object: `billing_account`, `amount`, `currency_code`, `thresholds`)
  - `alert_email` (nullable)
  - `registry_cleanup = {enabled = true, dry_run = true, untagged_days = 14, candidate_days = 2, keep_versions = 3}`. `dry_run` defaults to `true`; the runbook turns it off after a day of audit logs. It is also an output, `registry_cleanup_dry_run`, which `init --repo` copies into each repository's `registry.cleanup_dry_run`, so one setting drives every registry.
  - `adopt_legacy_registry` (bool, default `false`): `fugaro init` sets it to `true` only when discovery finds a marked `fugaro` registry to adopt.
- Outputs:
  - `runs_bucket`, `registry_host` (`<region>-docker.pkg.dev/<p>`), `base_registry`, `legacy_registry`
  - `scheduler_service_account`
  - `role_ids = {launcher, job_runner, build_submitter}`
  - `launchers`, `operators`, `log_view` (from Task 16; `null` until then)
- The resources:
  - `google_project_service` for each API in the R11 list, `for_each`, with `disable_on_destroy = false` and `disable_dependent_services = false`.
  - `google_storage_bucket.runs`:
    - `name`, and `location = upper(var.region)`. The provider suppresses case diffs on a bucket's `location`, so this only keeps the plan text the same as the API's `US-EAST5`; it isn't what prevents a replace.
    - `uniform_bucket_level_access = true`, `public_access_prevention = "enforced"`, `labels = {fugaro = "managed"}`, `force_destroy = false`, `lifecycle { prevent_destroy = true }`
    - three `lifecycle_rule`s, as the bootstrap set them: `age = runs_days` on `runs/`; `days_since_custom_time = cache_custom_time_days` on `cache/`; `age = cache_age_days` on `cache/`
    - `builds/` has no rule: `image.json` must outlive any age.
  - `google_artifact_registry_repository.legacy` (`fugaro`, imported; `count = var.adopt_legacy_registry ? 1 : 0`): `format = "DOCKER"`, `labels = {fugaro = "managed"}`, `lifecycle { prevent_destroy = true }`, and **no cleanup policy**. After migration nothing writes it, and it holds the M4 images the rollback needs.
  - `google_artifact_registry_repository.base` (`fugaro-base`, new): `format = "DOCKER"`, `labels = {fugaro = "managed"}`, `prevent_destroy`. With `registry_cleanup.enabled`:
    - `KEEP` for `tag_prefixes = ["dev-", "latest"]`
    - `KEEP` for `most_recent_versions { keep_count = keep_versions }`
    - `DELETE` for `UNTAGGED` older than `untagged_days`
    - `cleanup_policy_dry_run = var.registry_cleanup.dry_run`
    - Keep wins over delete, so no tagged base can be deleted.
  - `artifactregistry.writer` on `fugaro-base` for the operators. **Build accounts get only `reader` there** (granted in `modules/repo`).
  - `google_project_iam_custom_role`, IDs from `names.role_ids`:
    - `fugaroLauncher`: `run.jobs.get`, `run.jobs.list`, `run.executions.get`, `run.executions.list`, `run.executions.cancel`, `run.operations.get`
    - `fugaroJobRunner`: `run.jobs.run`, `run.jobs.runWithOverrides`
    - `fugaroBuildSubmitter`: `cloudbuild.builds.create`, `cloudbuild.builds.get`
  - `google_project_iam_member` for `launchers` × `fugaroLauncher`.
  - Bucket-level `objectAdmin` for `launchers` and `operators` on the runs bucket, and for `operators` on the state bucket (`google_storage_bucket_iam_member`).
  - `google_service_account.scheduler` (`Fugaro scheduler`), and `roles/iam.serviceAccountUser` on it for the operators. The run grants on each check job are made in the repository module.
  - `google_billing_budget` with `count = var.budget == null ? 0 : 1`.
  - With `alert_email` set (`count = var.alert_email == null ? 0 : 1`): `google_monitoring_notification_channel` (email) and `google_monitoring_alert_policy`, with two `condition_matched_log` conditions:
    - the check's lines `jsonPayload.event="image-check" AND jsonPayload.decision=("rebuild-failed" OR "check-failed")`
    - a failed check-job execution: `resource.type="cloud_run_job" AND resource.labels.job_name=starts_with("fugarochk-") AND severity>=ERROR`
- The root: `terraform { required_version = ">= 1.7.0, < 2.0.0"; required_providers { google = { source = "hashicorp/google", version = "= <exact>" } }; backend "gcs" {} }`, then `provider "google" { project, region, billing_project = var.project, user_project_override = true }`, then `module "installation" { source = "../../modules/installation" … }`. The module's `versions.tf` uses the range `>= <exact>, < <next major>`, and the roots pin exactly.

- [ ] **Step 1: Write the failing tests.**
  - `deploy/terraform/terraform_test.go` (plain `go test`, no `terraform`):
    - `TestEmbeddedTree`: `terraform.FS` holds both roots, all three modules and both lock files.
    - `TestNoSecretVersionsInTerraform`: walks `FS`, and fails on `google_secret_manager_secret_version` or `secret_data`.
    - `TestNoAuthoritativeIAM`: fails on `_iam_binding"`, `_iam_policy"` or `google_project_iam_audit_config`.
    - `TestNoProjectLevelSecretAccessor`: fails on `secretmanager.secretAccessor` inside any `google_project_iam_member` block. A plan assertion can't enumerate every resource, so this is a text check.
    - `TestProjectServicesKeepOnDestroy`: every `google_project_service` block sets `disable_on_destroy = false`.
    - `TestPreventDestroy`: the blocks of the secrets, the runs bucket and every Artifact Registry repository contain `prevent_destroy = true`.
  - `roots/installation/tests/installation.tftest.hcl`, with `mock_provider "google" {}`, `command = plan`, and an **inline `variables {}` block** of its own (no T1 fixture, so this test runs before M1):
    - `bucket_is_marked`: the labels, UBLA, PAP, and the three lifecycle prefixes.
    - `registries_are_marked`: both repositories are labelled; `legacy` has no cleanup policy.
    - `legacy_only_when_adopting`: with `adopt_legacy_registry = false`, no `legacy` repository is planned.
    - `cleanup_keeps_latest` and `cleanup_keeps_dev`: `fugaro-base` has `KEEP` policies covering `latest` and `dev-`, and a `most_recent_versions` keep.
    - `cleanup_dry_run_default`.
    - `roles`: `fugaroJobRunner` contains `run.jobs.runWithOverrides`.
    - `budget_optional`: the count 0 and 1 cases.
    - `alert_optional`: both conditions exist when `alert_email` is set.
    - `bad_bucket_name`: `expect_failures = [var.runs_bucket]` for `runs-foo`.
- [ ] **Step 2: Run them to see them fail.**
  ```bash
  go test ./deploy/terraform/
  terraform -chdir=deploy/terraform/gcp/roots/installation init -backend=false
  terraform -chdir=deploy/terraform/gcp/roots/installation test
  ```
  Expected: FAIL (no module yet).
- [ ] **Step 3: Implement.**
  - Write the HCL.
  - Pick the exact provider version, then run `terraform -chdir=… providers lock -platform=linux_amd64 -platform=linux_arm64 -platform=darwin_amd64 -platform=darwin_arm64`.
  - Write `.tflint.hcl`: `plugin "terraform" { preset = "recommended" }` and `plugin "google" { enabled = true; version = "<exact>"; source = "github.com/terraform-linters/tflint-ruleset-google" }`.
  - Add the CI job:
    ```yaml
      terraform:
        runs-on: ubuntu-latest
        timeout-minutes: 20
        steps:
          - uses: actions/checkout@<sha> # pinned like the test job
            with: { persist-credentials: false }
          - uses: hashicorp/setup-terraform@<sha> # vX
            with: { terraform_version: 1.16.4, terraform_wrapper: false }
          - uses: terraform-linters/setup-tflint@<sha> # vX
            with: { tflint_version: v<exact> }
          - run: terraform fmt -check -recursive deploy/terraform
          - name: validate and test the roots
            run: |
              for r in installation repo; do
                [ -d "deploy/terraform/gcp/roots/$r" ] || continue
                terraform -chdir=deploy/terraform/gcp/roots/$r init -backend=false -lockfile=readonly -input=false
                terraform -chdir=deploy/terraform/gcp/roots/$r validate
                terraform -chdir=deploy/terraform/gcp/roots/$r test
              done
          - run: tflint --chdir=deploy/terraform/gcp --init && tflint --chdir=deploy/terraform/gcp --recursive
          - run: sh deploy/terraform/scan.sh   # the same pinned Trivy images/scan.sh installs: trivy config --exit-code 1 --severity HIGH,CRITICAL deploy/terraform
    ```
    `deploy/terraform/scan.sh` reuses `images/scan.sh`'s pinned Trivy install. **No step has credentials.** `terraform init -backend=false` only downloads the provider.
  - Trivy findings: fix them, or justify each in `.trivyignore` with a comment. The expected justified ones: bucket versioning (the runs bucket is intentionally unversioned, since it holds runs with lifecycle deletion), customer-managed keys (Google-managed keys are the design), and bucket access logging.
- [ ] **Step 4: Run them to see them pass.** The same commands, plus `tflint --chdir=deploy/terraform/gcp --recursive` and `terraform fmt -check -recursive deploy/terraform`. Expected: PASS, and no tflint issues.
- [ ] **Step 5: Commit.** `git commit -m "terraform: the installation module and root, embedded, with fmt, validate, tflint, trivy and terraform test in CI"`

---

### Task 3: The repository and workflow modules

**Files:**
- Create: `deploy/terraform/gcp/modules/repo/{versions,variables,secrets,registry,build,check,outputs}.tf`
- Create: `deploy/terraform/gcp/modules/workflow/{versions,variables,sa,job,outputs}.tf`
- Create: `deploy/terraform/gcp/roots/repo/{main.tf,variables.tf,outputs.tf,.terraform.lock.hcl}`, `deploy/terraform/gcp/roots/repo/tests/repo.tftest.hcl`

**Interfaces:**
- Consumes: T1's tfvars shape. The golden files are the contract, and the installation's outputs arrive in them under `installation` (ruling R1). There is no `terraform_remote_state`.
- The resources, `modules/repo`:
  - `google_secret_manager_secret.this`, `for_each = var.repo.secrets` (logical name → ID): `secret_id = each.value`, `labels = {fugaro = "managed", fugaro_repo = var.repo.label, fugaro_secret = each.key}`, `replication { auto {} }` (the import shows `automatic: {}`; a different replication would replace it), and `lifecycle { prevent_destroy = true }`.
  - `google_artifact_registry_repository.images`: `repository_id = var.repo.registry.repository_id`, `format = "DOCKER"`, `labels = {fugaro = "managed", fugaro_repo = var.repo.label}`, and `prevent_destroy`. Its cleanup policies:
    - `KEEP` for `tag_prefixes = ["latest"]`
    - `KEEP` for `most_recent_versions { keep_count = 3 }`
    - `DELETE` for `UNTAGGED` older than 14 days
    - `DELETE` for `tag_prefixes = ["candidate-"]` older than 2 days. The build's untag is best-effort and normally refused, so this rule is what clears old candidates. Keep wins while the version is `latest` or among the 3 newest.
    - `cleanup_policy_dry_run = var.repo.registry.cleanup_dry_run`
  - `google_service_account.build`, with `account_id` and `display_name` from the tfvars.
  - Build account IAM, every grant a member:
    - accessor on each secret in `build_secrets`, looked up in `this`
    - `artifactregistry.writer` on **its own `images` repository only**
    - `artifactregistry.reader` on the installation's `base_registry`
    - `logging.logWriter` on the project
    - `fugaroBuildSubmitter` on the project
    - `roles/iam.serviceAccountUser` on **itself**
    - `roles/storage.objectUser` on the runs bucket, with `build_bucket_condition`
  - Operators: `secretVersionAdder` and `viewer` on each secret, `serviceAccountUser` on the build account, and `artifactregistry.reader` on `images`.
  - The check job (`count = var.repo.check == null ? 0 : 1`): `google_cloud_run_v2_job.check`:
    - `name`, `location = var.region`, `labels = {fugaro = "managed", fugaro_repo = …, fugaro_role = "check"}`, `deletion_protection = true`, `launch_stage` unset
    - `template { task_count = 1; template { max_retries = 0; timeout = "900s"; service_account = build SA; execution_environment = "EXECUTION_ENVIRONMENT_GEN2"; containers { image = check.image; command = ["fugaro"]; args = ["image", "check", "--job"]; resources { limits = { cpu = "1", memory = "2Gi" } }; env … ; env { value_source { secret_key_ref { secret = …secret_id, version = "latest" } } } } } }`
    - `lifecycle { ignore_changes = [client, client_version] }`
  - `google_cloud_run_v2_job_iam_member` giving `roles/run.invoker` (which holds `run.jobs.run`) on the check job to the scheduler account.
  - `google_cloud_scheduler_job.check`:
    - `name = check.scheduler_job`, `region = check.scheduler_region` (`us-east4` for this installation; ruling R4), `schedule`, `time_zone = "Etc/UTC"`, `paused = check.paused`
    - `retry_config { retry_count = 0 }`
    - `http_target { http_method = "POST"; uri = "https://run.googleapis.com/v2/projects/<p>/locations/${var.region}/jobs/${check.job}:run"; oauth_token { service_account_email = scheduler SA; scope = "https://www.googleapis.com/auth/cloud-platform" } }`. The URI names the job's own region, so a Scheduler job in `us-east4` starts a job in `us-east5`.
  - `module "workflow"`, `for_each = var.repo.workflows`.
- `modules/workflow`:
  - `google_service_account.job`, and `roles/iam.serviceAccountUser` on it for the operators
  - `google_storage_bucket_iam_member.job`: `roles/storage.objectUser`, `condition { title, expression }` from the tfvars, byte for byte, and no `description`, since the bootstrap set none
  - accessor on each secret named in `secret_env` (env → logical name), looked up in the repository's `google_secret_manager_secret.this`, so the grant depends on the container
  - `roles/aiplatform.user` on the project when `vertex`
  - `google_cloud_run_v2_job.this` (`count = deploy_job ? 1 : 0`):
    - labels `fugaro=managed`, `fugaro_repo`, `fugaro_workflow`; `deletion_protection = !var.allow_job_delete` (`false` only for offboarding; default `true`)
    - `template.template`: `max_retries = 0` (the provider's default is 3, so it is always set), `timeout = "${task_timeout_s}s"`, `service_account`, `execution_environment = "EXECUTION_ENVIRONMENT_GEN2"` (the live jobs are gen2), one container with the image, `limits = {cpu, memory}`, `env` sorted, and secrets as `secret_key_ref { secret = <secret>.secret_id, version = "latest" }`
    - `lifecycle { ignore_changes = [client, client_version] }`
  - `google_cloud_run_v2_job_iam_member` with the installation's `fugaroJobRunner` role for each launcher.
- The root: `variable "github_app_id"` (nullable, digits), passed to nothing but an output. Root `repo` outputs: `jobs`, `service_accounts`, `build_service_account`, `registry`, `check_job`, and `github_app_id`.

- [ ] **Step 1: Write the failing tests.** `roots/repo/tests/repo.tftest.hcl` with `mock_provider "google" {}`, `command = plan`, and T1's golden tfvars:
  - `names_are_inputs`: every resource name equals its tfvars value, one assertion each for the job, both accounts, every secret, the registry, the check job and the Scheduler job.
  - `labels`: jobs `fugaro=managed`, `fugaro_repo`, `fugaro_workflow`; secrets with `fugaro_secret`; the check job `fugaro_role=check`; the registry `fugaro_repo`.
  - `max_retries_zero`: both jobs.
  - `bucket_condition_exact`: the member's `condition[0].expression` and `title` equal the input.
  - `secret_ref_short_form`: the job's `secret_key_ref.secret` equals the secret ID, not a `projects/…` path.
  - `cleanup_keeps_latest`: `images` has a `KEEP` for `latest` and a `most_recent_versions` keep.
  - `build_writes_own_registry_only`: the build account's `artifactregistry.writer` member targets `images`, and its member on the base registry is `reader`.
  - `build_role_set_on_own_registry`: the build account's members on `images` are exactly `{roles/artifactregistry.writer}`: no `repoAdmin`, no `admin`.
  - `cleanup_dry_run_from_input`.
  - `scheduler_region_and_paused`: `region == "us-east4"`, the URI contains `locations/us-east5/jobs/`, and `paused == true` in the golden.
  - `vertex_only_when_asked`: the `github-vertex` fixture has an `aiplatform.user` member; the Bitbucket one has none.
  - `deploy_job_false`: with `deploy_job = false` (a variable override in the run), no job is planned, while the account and IAM still are.
  - The project-level accessor check and `prevent_destroy` are Go text checks (Task 2), since `terraform test` can't enumerate every planned resource or exercise a destroy.
- [ ] **Step 2:** `terraform -chdir=deploy/terraform/gcp/roots/repo init -backend=false && terraform -chdir=deploy/terraform/gcp/roots/repo test`. Expected: FAIL.
- [ ] **Step 3: Implement.** Then lock the root (the same `providers lock` command as Task 2).
- [ ] **Step 4:** Run the CI job's commands locally for both roots, then `go test ./deploy/terraform/`. Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "terraform: the repository and workflow modules, with names and marks as inputs"`

---

### Task 5: The per-run timeout override

**Files:** `internal/backend/backend.go`, `internal/backend/gcp/run.go` and its test, `internal/gcpfake/run.go`, `internal/cli/run.go` and its test

**Interfaces:**
- `backend.LaunchSpec.Timeout time.Duration`: zero means the job's own. `gcp.Backend.Launch` sets `Overrides.Timeout = fmt.Sprintf("%ds", int((spec.Timeout + backend.TaskTimeoutSlack) / time.Second))` when it isn't zero.
- `fugaro run --total-timeout D`. It is refused with `--retry` (it is part of `taskFlags`). It writes `spec.Overrides.TotalTimeout = D.String()`.
- `launchRun` derives `LaunchSpec.Timeout` from `spec.Overrides.TotalTimeout`, the stored task, never from the flag.
- CLI validation, as exit-1 errors: `time.ParseDuration`, greater than 0, at most `gcp.MaxTaskTimeout - backend.TaskTimeoutSlack`, and at most `OverrideCap = 24h` (ruling R15; `--total-timeout` above it is refused, naming the cap). It must also be more than the checkout's workflow `timeouts.finalize_reserve` when the checkout is the run's repository.

- [ ] **Step 1: Write the failing tests.**
  - `TestLaunchSendsTimeoutOverride`: the fake records `overrides.timeout == "2820s"` for 45m.
  - `TestLaunchNoTimeoutByDefault`: absent.
  - `TestRunTotalTimeoutFlag`: the stored `task.json` has `overrides.total_timeout: "45m0s"`.
  - `TestRunTotalTimeoutValidation`: `0`, `-1m`, `25h`, `abc`, and `2m` against a 5m finalize reserve.
  - `TestRetryKeepsTimeoutOverride`: launch fails with a rejection, then `--retry`; the fake saw the same timeout both times.
  - `TestRetryRefusesTotalTimeout`.
- [ ] **Step 2:** `go test ./internal/backend/gcp/ ./internal/cli/ -run 'Timeout'`. Expected: FAIL.
- [ ] **Step 3: Implement.** Have the fake store `Overrides.Timeout` on the execution it creates, as it stores the env.
- [ ] **Step 4:** `go test -race ./internal/backend/... ./internal/gcpfake/ ./internal/cli/`. Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "run: --total-timeout, sent as the execution's task timeout and kept by --retry"`

---

### Task 6: A cheaper `max_parallel` check

**Files:** `internal/backend/gcp/run.go`, `internal/gcpfake/run.go`, `internal/cli/run.go`, `internal/cli/lifecycle_test.go`, `internal/backend/backend.go` (drop `Exhaustive`)

**Interfaces:**
- `gcp.(*Backend).LongestTaskTimeout(ctx) (time.Duration, error)`: one `jobs.list` over the region, paged. It takes the maximum `template.template.timeout` over jobs whose name starts with `fugaro-`, so check jobs (`fugarochk-`) are excluded.
- `checkMaxParallel`: `horizon = max(longest, OverrideCap) + backend.TaskTimeoutSlack + 1h`, then `List(ActiveOnly, Since: now-horizon)` with the early stop. Live check 1b confirmed the global order, so update the comment in `run.go`'s `list` to cite it.

- [ ] **Step 1: Write the failing tests.**
  - `TestMaxParallelStopsAtHorizon`: the fake holds 500 old executions and 3 active ones; the check makes at most two `executions.list` page requests. The fake counts requests.
  - `TestMaxParallelHorizonFromJobs`: a job with a 30h timeout widens the horizon to cover an active execution 29h old.
  - `TestMaxParallelIgnoresCheckJobs`.
  - Keep the existing `TestMaxParallel*` tests.
- [ ] **Step 2:** `go test ./internal/cli/ ./internal/backend/gcp/ -run 'MaxParallel|LongestTask'`. Expected: FAIL.
- [ ] **Step 3: Implement.** Remove `ListFilter.Exhaustive` and its branch.
- [ ] **Step 4:** `go test -race ./internal/...`. Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "run: bound the max_parallel listing by the longest job timeout, now that jobs/- is known to be sorted"`

---

### Task 7: The Cloud Build credential step, GitHub builds, per-repository build accounts and registries

**Files:**
- Modify: `images/derived/cloudbuild.yaml`, `images/cloudbuild_test.go`, `internal/backend/gcp/build.go` and its test, `internal/gcpfake/build.go`, `internal/cli/image.go`, `internal/cli/image_cloud_test.go`
- Create: `internal/cli/gitcred.go` (the hidden `fugaro image git-credential`) and its test
- Modify: `internal/gitprov/github/apptoken.go`. `tokenSource` takes a permissions map: the runner keeps `tokenPermissions`, and the build uses `buildTokenPermissions = {contents: read, metadata: read}`. It is exported as `github.MintInstallationToken(ctx, Options, perms)`.

**Interfaces:**
- `gcp.BuildSpec` gains `GitHubAppID string`, and `GitSecretID` names the `github-app-key` secret for GitHub.
- `ServiceAccount` is now the repository's build account email, and `Image` is under the repository's own registry (`gcp.ImageName(lc.RegistryHost+"/"+RegistryRepoID(slug), slug, wf)`), both from `infra`.
- `BuildRequest` **always** sets `serviceAccount`, and refuses an empty one: a request without one would run as the project's default build identity (ruling R6).
- The `cloudbuild.yaml` steps become:
  1. `prep` (`gcr.io/cloud-builders/git`, root): `install -d -m 0700 -o 1000 -g 1000 /creds`. Volume `fugaro-creds`.
  2. `credential` (`${_FUGARO_BASE}`, as `fugaro`): `fugaro image git-credential --provider "$PROVIDER" --repo-url "$REPO_URL" --out /creds/git-credentials`. Volume `fugaro-creds`. It writes one credential-store line, mode 0600, and prints nothing.
     - Bitbucket: `secretEnv: [GIT_TOKEN]`, with the user from `_GIT_USER`.
     - GitHub: `secretEnv: [GITHUB_APP_KEY]` and the env `GITHUB_APP_ID`. It mints a token.
  3. `source` (git builder): the clone uses `credential.helper='store --file=/creds/git-credentials'`. Volume `fugaro-creds`. No `secretEnv`.
  4. `render`: unchanged. **No volume.**
  5. `build`: passes `--secret id=git-credentials,src=/creds/git-credentials`. Workflow secrets are unchanged (M4's file secrets in its `mktemp` directory). Volume `fugaro-creds`. It removes `/creds/git-credentials` on exit.
  - `BuildRequest` chooses the credential step's `secretEnv` and env by provider, and the rest of the file is shared. For GitHub, `availableSecrets[0]` becomes `GITHUB_APP_KEY`.
- `fugaro image git-credential` (hidden) reads the token or key from the env only. It refuses a non-`https` or credential-carrying URL, or a host that isn't the provider's (the `BuildSpec.check` rule), and percent-encodes the line itself. The minted GitHub token is scoped to the repository, as `tokenSource` does.
- `fugaro image build` (cloud):
  - accepts GitHub repositories
  - takes the build account from `infra` (`BuildServiceAccountID`); a set `build.service_account` in the local config gives a warning and is ignored
  - pushes to the repository's registry, and fails with "run fugaro init --repo" when that registry doesn't exist yet (a 404 from Artifact Registry on the push shows as the build's failure; the CLI checks first, with one `repositories.get`)

- [ ] **Step 1: Write the failing tests.**
  - `TestCloudBuildCredentialOnlyInVolume`: `render` has no `volumes`, and no step has `GIT_TOKEN` or `GITHUB_APP_KEY` in `secretEnv` except `credential`. No script mentions `/workspace/`, `git-credentials` or `/builder/home` together. `source`, `credential` and `build` mount `fugaro-creds` at `/creds`.
  - `TestBuildRequestGitHub`: `availableSecrets` holds the `github-app-key` version as `GITHUB_APP_KEY`, and `_GITHUB_APP_ID` is set.
  - `TestBuildRequestUsesBuildSA` and `TestBuildRequestRefusesNoServiceAccount`.
  - `TestBuildRequestImageInRepoRegistry`.
  - `TestGitCredentialBitbucket`: the line's format and mode, and nothing on stdout.
  - `TestGitCredentialGitHubMints`: an `httptest` GitHub with the recorded fixtures of M2; asserts `permissions = {contents: read, metadata: read}` and `repositories: [name]`.
  - `TestGitCredentialRefusesForeignHost`.
  - `TestImageBuildCloudGitHub` (replacing `TestImageBuildCloudGitHubNeedsM5`).
  - `TestImageBuildIgnoresDeprecatedBuildSA`.
- [ ] **Step 2:** `go test ./images/ ./internal/backend/gcp/ ./internal/cli/ ./internal/gitprov/github/ -run 'CloudBuild|BuildRequest|GitCredential|ImageBuildCloud|Mint'`. Expected: FAIL.
- [ ] **Step 3: Implement.** Keep `TestCloudBuildNoSubstitutionsInScripts` passing: values reach a script only through `env:`.
- [ ] **Step 4:** `go test -race ./images/ ./internal/...`. Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "image: a credential step in a private build volume; GitHub App builds; one build account and registry per repository"`

---

### Task 8: Candidate, smoke, gate, promote, and the build record

**Files:** the same as Task 7, plus:
- `internal/image/selftest.go` (`SpecForCloud`)
- `internal/imagecheck/record.go` (the `Record` type, `KeyFiles`, `ImageConfigHash`; Task 10 fills in the rest of the package)
- the hidden `fugaro image record` and `fugaro image gate` in `internal/cli/image.go`
- `images/derived/Dockerfile.tmpl` (declares `ARG FUGARO_BUILT_AT=""` only)

**Interfaces:**
- `imagecheck.Record`, in JSON:
  - `version`, `repo`, `workflow`, `built_at`, `build_id`
  - `source_commit`, `source_commit_time`, `base_branch`
  - `key_files` (map from path to git blob ID), `image_config_hash`
  - `base_ref`, `base_digest`, `image`, `image_digest`
  - `fugaro_version`, `template_salt`
  - `adopted` (reserved; `false`)
- The record path is `builds/<slug>/<workflow>/image.json`. There is no history copy: Cloud Build's own history serves (ruling R5).
- `imagecheck.ImageConfigHash(cfg, workflow, tree)` and `imagecheck.KeyFiles(cfg, workflow, tree)` are **defined here** and reused by the check. They take a `Tree` interface (`BlobID(path)`, `Glob(pattern)`, `ReadFile(path)`) with two implementations: a directory checkout (the build) and a git tree (the check, Task 10).
  - The key files are every path the workflow's cache key globs match, after the per-base defaults (`config.DefaultCache`).
  - The config hash is SHA-256 over the canonical JSON of `{base, image, dockerfile, dockerfile_blob}`, where `dockerfile_blob` is the blob ID of the repository Dockerfile when one is set.
- `gcp.TemplateSalt()`: SHA-256 of the embedded `cloudbuild.yaml` plus the fugaro version.
- The `cloudbuild.yaml` steps after `render`:
  - `render` also writes `/workspace/selftest.json` (`image.SpecForCloud`: `SelftestSpec` with `CheckInit`, `CheckHardening` and the commit, and never `Verify`) and `/workspace/record.json` (the record's source-side fields). New flag: `fugaro image render --workflow W --cloud-outputs /workspace`.
  - `build` tags `"$IMAGE:candidate-$BUILD_ID"` only, adds `--label org.opencontainers.image.revision=… --label org.opencontainers.image.created=… --label dev.fugaro.base.digest=…` and `--build-arg FUGARO_BUILT_AT=…`, pushes the candidate, and writes the pushed digest (`docker inspect --format '{{index .RepoDigests 0}}'`) to `/workspace/image-digest`.
  - `smoke` (the docker builder, **no volumes**) runs two containers, `docker run --rm -i --network none "$CANDIDATE" fugaro image selftest < /workspace/selftest.json`, and the root scan with `--user 0 --network none`, as `image.BuildLocal` does.
    - It fails the build on a failed report.
    - With `dockerfile:` the repository controls the whole image, its `fugaro` included, so this runs repository code: hence `--network none`, and no secrets.
    - If the structural selftest turns out to need the network (`CheckInit` shouldn't), T8 stops and reports, rather than loosening it.
  - `gate` (`${_FUGARO_BASE}`, the build account): `fugaro image gate --record /workspace/record.json --bucket "$BUCKET"` reads the current `image.json`. When that record is **newer**, it writes `/workspace/superseded` and logs it. Then `promote`, `record` and `untag` do nothing, and the build still succeeds. This stops an older, slower build from promoting over a newer one.
    - "Newer" compares **times**, not ancestry. `source` clones with `--depth 1` and the credential is gone by then, so the gate has no history and doesn't fetch any. The record carries `source_commit_time` (the commit's committer time), and a record is newer when its `source_commit_time` is later than ours, or it is the same commit with a later `built_at`.
    - What it can get wrong: committer times that don't follow history (clock skew on a committer's machine, a rebase or cherry-pick that keeps an old time) or a rewritten history. Then the gate may keep an older image, or promote over a newer one.
    - Either way the next check compares the record with the branch head. A record whose commit isn't head, or isn't on the branch, counts as **changed** (the `lockfiles`, `paths`, `built-commit-gone` and `latest-drift` triggers), so the error is corrected by a rebuild, never by keeping a wrong image.
  - `promote` (`gcr.io/google.com/cloudsdktool/cloud-sdk:slim`, the build account), unless superseded:
    - `gcloud artifacts docker tags add "$IMAGE@$(cat /workspace/image-digest)" "$IMAGE:latest"`: a registry-side retag **by digest**, so the smoked digest is provably the promoted one
  - `promote` never untags; the untag is the last step.
  - `record` (`${_FUGARO_BASE}`, network `cloudbuild`, the build account), unless superseded: `fugaro image record --in /workspace/record.json --digest-from /workspace/image-digest --bucket "$BUCKET"` writes `image.json`. It is generation-matched against the generation `gate` read, so a concurrent writer between `gate` and `record` makes `record` fail. The build then fails visibly, and the next check sees the newer record. The remaining window is a promote race between two builds finishing within seconds of each other, which the next check detects (the `latest` digest differs from `image_digest`) and fixes with a rebuild.
  - `untag` (`cloud-sdk:slim`, the build account), unless superseded, after `record`: `gcloud artifacts docker tags delete "$IMAGE:candidate-$BUILD_ID" --quiet || echo "warning: could not remove candidate-$BUILD_ID (harmless; the cleanup policy clears it)"`. It always exits 0. With only `artifactregistry.writer` (no `tags.delete`), the delete is normally refused. The step stays so a future role change needs no template change, and **no broader role is granted for it**.
- The top-level `images:` field is removed, since `build` pushes. `Builder.Wait` reads the digest from `/workspace/image-digest` through `results.buildStepOutputs` of `build` (written to `$BUILDER_OUTPUT/output`), and falls back to `results.images`.
- `fugaro image build` gets `--no-smoke`, which skips `smoke`. It is refused when the check submits the build.

- [ ] **Step 1: Write the failing tests.**
  - `TestPromoteAfterSmoke`: the step order is `prep, credential, source, render, build, smoke, gate, promote, record, untag`, and no step has `waitFor: ["-"]`.
  - `TestBuildPushesOnlyCandidate`: the `build` script's `--tag` is only `candidate-`, and there is no top-level `images:`.
  - `TestSmokeIsNotAStep`: no step's `name` is `$IMAGE…`/`_IMAGE`. The candidate runs only through `docker run` in the `smoke` step, and every such line carries `--network none`.
  - `TestPromoteByDigest`: `promote` uses `tags add` with `@sha256` from `/workspace/image-digest`.
  - `TestUntagFailureIsOnlyAWarning`: the fake build fails the `untag` command (a 403, as `writer` gets). The build ends `SUCCESS`, `image.json` is written, `latest` points at the candidate's digest, and the log has the warning.
  - `TestUntagScriptAlwaysExitsZero`: the `untag` script ends in `|| echo …`.
  - `TestFailedSmokeLeavesLatest`: the fake build honours `FailStep: "smoke"`, and the fake registry (gcpfake build keeps a map of tags) still has the old `latest` digest.
  - `TestGateSkipsWhenRecordNewer`: a current record with a later `source_commit_time`, then `gate` writes `superseded`, and the fake sees no `tags add` and no record write.
  - `TestGateSameCommitLaterBuiltAt` and `TestGatePromotesWhenRecordOlder`.
  - `TestRecordGenerationMatched`: `image.json` changes between `gate`'s read and `record`'s write, and the write is refused.
  - `TestWaitReadsStepOutputDigest`.
  - `TestKeyFilesDefaultsWebNode`: the yarn lockfile and `package.json`'s `packageManager`.
  - `TestImageConfigHashChanges`, a table: `image.apt`, `image.setup`, `dockerfile` content and `base` each change the hash; the task text or `commands` don't.
- [ ] **Step 2:** `go test ./images/ ./internal/backend/gcp/ ./internal/cli/ ./internal/imagecheck/ ./internal/image/ -run 'Promote|Candidate|Smoke|Gate|StepOutput|Record|KeyFiles|ImageConfigHash'`. Expected: FAIL.
- [ ] **Step 3: Implement.** The records are tested against `memblob` through `blobx`, not the GCS fake (lane B owns `gcpfake/gcs.go`).
- [ ] **Step 4:** `go test -race ./images/ ./internal/...`, and `<repo root>/.superpowers/heavy.sh go test -tags docker ./images/ ./internal/image/`, because the template gained the `ARG`. Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "image: build a candidate, smoke-test it offline, promote it by digest unless superseded, then record what it was built from"`

---

### Task 10: `fugaro image check` and `fugaro image status`

**Files:**
- Create: `internal/imagecheck/{check,triggers,gittree,registry,status}.go` and tests
- Create: `internal/gcpfake/registry.go`: a fake OCI distribution API (`HEAD /v2/<name>/manifests/<ref>` with `Docker-Content-Digest`, and the token dance for an anonymous ghcr-style pull)
- Create: `internal/cli/imagecheck.go`
- Modify: `internal/cli/image.go` (it registers `check` and `status`)

**Interfaces:**
- `imagecheck.Decide(ctx, in Inputs) []Decision`
  - `Inputs{Record *Record (nil when missing); LastCheck *CheckState; LastBuildStatus string; Head string; Tree Tree; BuiltCommitReachable bool; Changed func(from, to string, globs []string) (bool, []string, error); BaseDigest string; LatestDigest string; Now time.Time; Rebuild config.Rebuild; Force bool}`
  - `Decision{Workflow, Decision string; Reasons []string; Inputs InputsFingerprint}`, where the decision is `rebuild`, `skip`, `rebuild-failed-last` or `check-failed`
- The triggers, in order. Every reason that fires is recorded, not just the first.
  1. `force`
  2. `no-record`
  3. `image-config` (the hash differs; always on)
  4. `lockfiles` (the key-file blob IDs differ; when `lockfiles: true`)
  5. `base` (the digest differs, or the base ref changed; when `base: true`)
  6. `paths` (`git diff --name-only <recorded commit> <head>` matches a glob; when not empty)
  7. `max-age` (`now - built_at > max_age`; when `max_age > 0`)
  8. `built-commit-gone` (the recorded commit isn't an ancestor of head, after a force-push)
  9. `latest-drift` (the registry's `latest` digest isn't the record's `image_digest`, after a promote race)
- **Back-off after a failure.** When the previous check submitted a build that ended `FAILURE`, `TIMEOUT` or `INTERNAL_ERROR`, and this night's inputs fingerprint equals the one that build ran with (source commit, config hash, key-file IDs, base digest), the decision is `rebuild-failed-last` and nothing is submitted, unless `force`. It is logged every night at `ERROR`, so the alert and `ls` still show it, but the failure isn't paid for again until something changes.
- The git side, `gittree.go`, uses the M2 credential helper pattern with the token from the env:
  - `git clone --filter=blob:none --no-checkout --single-branch --branch <b> -- <url> <tmp>`
  - Blob IDs come from `git ls-tree -r <head>`. `ReadFile` uses `git cat-file blob <head>:<path>`, which fetches that one blob on demand.
  - `Changed` uses `git diff --name-only`. It never checks out the working tree.
- `registry.go`: digests by `HEAD` on the manifest, with `Accept` for the OCI index and the Docker manifest list and v2.
  - For `*-docker.pkg.dev` the bearer is the job account's metadata-server token, through `google.golang.org/api/option`'s default credentials, the same ADC the rest of fugaro uses.
  - For `ghcr.io` it is the anonymous token.
  - Any other registry is tried anonymously; failing that, the `base` trigger is skipped with the reason `base-unknown`.
- `fugaro image check --job` (in the check job):
  - It reads `FUGARO_CHECK_SPEC`, and reads the repository's `fugaro.yaml` **from head**, so `rebuild:` edits apply the next day without an `init`.
  - It first reads the previous `check.json`'s `build_id` status (`builds.get`).
  - It runs `Decide` for each workflow the spec lists, and for `rebuild` submits `gcp.BuildRequest` through `Builder.Submit`, without waiting.
  - It writes `builds/<slug>/<workflow>/check.json` (`checked_at`, `decision`, `reasons`, `build_id`, `build_inputs`, `last_build_status`, `last_build_at`) generation-matched.
  - It logs one JSON line per workflow: `{"event":"image-check","repo":…,"workflow":…,"decision":…,"reasons":[…],"build_id":…}`. `rebuild-failed-last` and `check-failed` are logged at `severity: ERROR`, which the alert filter matches.
  - A workflow in head's `fugaro.yaml` that the spec doesn't list is logged as `decision: not-installed`, with the reason `run fugaro init --repo`. So is a `rebuild.check` whose value no longer matches what is installed.
  - **When the check itself fails** (it can't clone, the token has expired, a secret has no version, the bucket isn't reachable), it logs `decision: check-failed` with the error, best-effort writes `check.json` with that decision, and exits 2, so the execution shows failed. The alert's second condition also catches an execution that dies before it can log.
- **Locally**, run from a checkout: `fugaro image check [--workflow W] [--force]` evaluates the same triggers with the operator's ADC and a local blobless clone, and **only prints the decision**. It never submits a build; a manual rebuild is `fugaro image build`. `--dry-run` is accepted and is the default meaning (the runbook spells it out). In `--job` mode, `--dry-run` also suppresses the submission, for testing.
- `fugaro image status [--repo R] [--json]` reads `image.json` and `check.json` for each workflow of the local config's repositories. It prints the build time and age, the source commit, the base digest, the last check's time, decision and reasons, and the last build's status.

- [ ] **Step 1: Write the failing tests.**
  - `TestDecideTriggers`: a table, one row per trigger, plus "nothing changed" → `skip`, and "two triggers" → both reasons.
  - `TestCheckRebuildsWhenRecordMissing`.
  - `TestCheckRebuildsWhenBuiltCommitNotOnBranch`: a fixture remote with a rewritten history (`testutil.NewRemote`).
  - `TestCheckBacksOffAfterFailure`: the last build `FAILURE` and the same inputs give `rebuild-failed-last` with no submission; changed inputs give `rebuild`; `force` gives `rebuild`.
  - `TestPathsTrigger`: `packages/**` matches `packages/a/src/x.ts`.
  - `TestLockfilesIgnoresUnrelatedCommits`.
  - `TestLatestDriftTrigger`.
  - `TestBaseDigestAR` and `TestBaseDigestAnonymous`, against the registry fake.
  - `TestCheckJobSubmitsBuild`: the gcpfake build gets one request, equal to the one `gcp.BuildRequest` gives.
  - `TestCheckJobSkipsAndLogs`: the JSON line and `check.json`.
  - `TestCheckReportsFailedRebuild`: the previous build `FAILURE` gives an `ERROR` line.
  - `TestCheckFailureIsLogged`: a clone failure gives `decision: check-failed` at `ERROR`, and exit 2.
  - `TestCheckNotInstalledWorkflow`.
  - `TestLocalCheckNeverSubmits`: the build fake sees no request, even when the triggers fire.
  - `TestImageStatusJSON`.
  - `TestCheckNeverReadsFullCheckout`: the fake git counts `cat-file blob` calls, which must be at most `len(key files) + 2`.
- [ ] **Step 2:** `go test ./internal/imagecheck/ ./internal/cli/ -run 'Decide|Check|PathsTrigger|Lockfiles|LatestDrift|BaseDigest|ImageStatus'`. Expected: FAIL.
- [ ] **Step 3: Implement.** The bucket side is tested against `memblob`.
- [ ] **Step 4:** `go test -race ./internal/imagecheck/ ./internal/gcpfake/ ./internal/cli/`. Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "image: the daily check: triggers, back-off after a failure, rebuild submission, decision log, and image status"`

---

### Task 11: Image age at launch, and rebuild status in `ls`

**Files:** `images/derived/Dockerfile.tmpl` (its last step writes `/etc/fugaro/image.json` from `FUGARO_BUILT_AT` and the checkout's HEAD; the `ARG` exists since Task 8), `internal/image/render_test.go`, `internal/runner/{runner,image}.go`, `schemas/result.schema.json`, `internal/runview/*`, `internal/cli/ls.go` and its test

**Interfaces:**
- `result.json` gains `image: {built_at?, baked_commit}`. The runner reads it in bootstrap **before** the sync changes HEAD: the baked commit is `/work/repo`'s HEAD at start, and `built_at` comes from `/etc/fugaro/image.json` when present. It is never fatal: a failure leaves the block out, with a warning. (The commits-behind count and a `diagnose` line are cut to M7, with the status skill.)
- `ls --json` rows gain `image_age_s`: `started_at - built_at`, absent when unknown. The human table doesn't change. This is enough to judge a rebuild's value against each run's `bootstrap` stage duration, which `result.json` already records.
- Before its table, `ls` prints one warning line for each listed repository and workflow when:
  - its `check.json` has a failed `last_build_status`, or a decision of `rebuild-failed-last` or `check-failed`: `warning: acme/webapp web: the image rebuild of 2026-10-02 failed (FAILURE); runs still use the image built 2026-09-28. See fugaro image status.`
  - or it is on `check: daily` and `check.json`'s `checked_at` is more than 48 h old (the check isn't running): `warning: acme/webapp web: the daily image check hasn't run since 2026-10-01.`
  - A missing `check.json` is not a warning while the schedule is paused. `ls` can't tell, so it warns only when `image.json` exists and `check.json` doesn't, 48 h after `built_at`.
  - With `--json` the warnings go into a top-level `warnings` array.

- [ ] **Step 1: Write the failing tests.**
  - `TestRunnerRecordsImageAge`: a fixture image with `image.json`.
  - `TestRunnerImageAgeOptional`: no file and no git gives no block and a successful run.
  - `TestResultSchemaImageBlock`.
  - `TestTemplateWritesImageJSON`: a render test.
  - `TestLsImageAgeJSON`.
  - `TestLsWarnsRebuildFailed`.
  - `TestLsWarnsCheckStale`.
- [ ] **Step 2:** `go test ./internal/runner/ ./internal/image/ ./schemas/ ./internal/cli/ -run 'ImageAge|ImageJSON|ImageBlock|RebuildFailed|CheckStale'`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4:** `go test -race ./...`, then `<repo root>/.superpowers/heavy.sh go test -tags docker ./internal/e2e/ -run Docker`, which checks that the template's new step works in a real build. Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "runner: record how old the image was at launch; ls warns about failed rebuilds and a stalled check"`

---

### Task 12: The Terraform wrapper, the guard, the environment allowlist, and a fake `terraform`

**Files:** `internal/infra/tf/{tf,guard,env,summary}.go` and tests; `internal/infra/tf/faketerraform/main.go`; `.github/workflows/ci.yml` (one step: `go test -tags terraform ./internal/infra/...`)

**Interfaces:**
- `tf.New(bin, dir string, env []string) (*TF, error)`: checks `terraform version -json`, which must be `>= 1.7.0` and `< 2.0.0`.
- `(*TF).Init(ctx, backendConfig map[string]string)` runs `init -input=false -no-color -lockfile=readonly -backend-config=…`.
- `(*TF).Plan(ctx, out string) (changed bool, err)` runs `plan -input=false -no-color -lock-timeout=60s -detailed-exitcode -out=<out> -var-file=terraform.tfvars.json`. **Exit 0 means no changes, exit 2 means changes (not a failure), and exit 1 is the only failure.**
- `(*TF).Show(ctx, planFile) (*Plan, error)` runs `show -json`. `Plan` holds just what the guard and the summary need: `ResourceChanges[{Address, Type, Change{Actions []string; Before, After map[string]any; Importing *struct{ID string}}}]`.
- `(*TF).Apply(ctx, planFile)` runs `apply -input=false -no-color <planFile>`: **only a plan file, never `-auto-approve` without one.**
- `(*TF).Output(ctx) (map[string]json.RawMessage, error)` and `(*TF).StateRm(ctx, addrs ...string)`.
- `tf.Guard(p *Plan, allowDelete []string) error` refuses any change whose actions contain `delete` (which covers `["delete","create"]` and `["create","delete"]` replaces) unless its exact address is in `allowDelete`. `forget` (a `removed` block) is allowed. The error lists every refused address and its actions.
- `tf.Summary(p *Plan) string`:
  - the counts (import, create, update, forget)
  - then **the sensitive in-place updates first**, highlighted, each with its changed attribute paths (from a `Before`/`After` diff): a job's `service_account`, `image`, `env` or `max_retries`; a bucket's `lifecycle_rule`; a registry's `cleanup_policies` or `cleanup_policy_dry_run`; the Scheduler job's `paused`
  - then one line per other address, with imports first and each update's changed attributes
- `tf.Env(parent []string, workdir, cache string) ([]string, error)` builds the allowlisted environment of the Global Constraints, writes `<workdir>/terraformrc`, and returns an error when `GOOGLE_IMPERSONATE_SERVICE_ACCOUNT` is set.
- The fake reads `FAKE_TERRAFORM_SCRIPT` (JSON: per subcommand, an exit code, stdout, and files to write), appends its argv and environment to `FAKE_TERRAFORM_LOG`, and writes `-out` files with a marker.

- [ ] **Step 1: Write the failing tests.**
  - `TestGuardRefusesReplace`, `TestGuardRefusesDelete`, `TestGuardAllowsNamedDelete`, `TestGuardAllowsForget`, `TestGuardAllowsImportsAndUpdates`, with plan JSON fixtures in `testdata/`, copied from real `terraform show -json` output (Step 3 captures them with the `terraform`-tagged test).
  - `TestSummaryHighlightsSensitiveUpdates`: a job `image` update and a label-only update; the first is listed first, marked, with `template.template.containers[0].image`.
  - `TestPlanExitCodeTwoIsChanges`.
  - `TestApplyUsesSavedPlanOnly`: the fake log's `apply` argv ends in the plan path and contains no `-auto-approve`, `-target`, `-var` or `-replace`.
  - `TestTerraformEnvAllowlist`: with `TF_CLI_ARGS_apply=-auto-approve`, `TF_LOG=trace`, `TF_VAR_project=x`, `TF_WORKSPACE=w`, `TF_REATTACH_PROVIDERS=…`, `TF_CLI_CONFIG_FILE=/evil`, `GOOGLE_CREDENTIALS=…`, `GOOGLE_BILLING_PROJECT=…` and `FOO=bar` in the parent, none of them reaches the fake's environment. `TF_CLI_CONFIG_FILE` is the workdir's own file, and `TF_IN_AUTOMATION=1` and `PATH` are present.
  - `TestTerraformIgnoresUserRC`: a `$HOME/.terraformrc` with `dev_overrides`; the fake sees `TF_CLI_CONFIG_FILE` pointing at a file with only `plugin_cache_dir`.
  - `TestTerraformEnvKeepsTLSVars`: `SSL_CERT_FILE` and `SSL_CERT_DIR` from the parent reach the fake unchanged.
  - `TestRefusesImpersonation`.
  - `TestVersionTooOld`.
  - `//go:build terraform`, `TestRealTerraformPlanJSONShape`: runs the real `terraform` (from `PATH`) on a tiny config with the `terraform_data` resource and no providers, and captures a replace, an update and an import plan into `testdata/`, so the fixtures stay true to the real format.
- [ ] **Step 2:** `go test ./internal/infra/tf/`. Expected: FAIL.
- [ ] **Step 3: Implement.** Then run `go test -tags terraform ./internal/infra/tf/ -run RealTerraform -update`, which needs `terraform` on `PATH` and no credentials.
- [ ] **Step 4:** `go test -race ./internal/infra/tf/ && go test -tags terraform ./internal/infra/tf/`. Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "infra: a Terraform wrapper that applies only a reviewed plan, refuses deletes, and runs with an allowlisted environment"`

---

### Task 13: Discovery, ownership checks, live IAM checks, imports, and readiness gates

**Files:** `internal/infra/{discover,imports,readiness}.go` and tests; `internal/gcpfake/{iam,artifactregistry,crm}.go` (new: IAM `serviceAccounts.get`; Artifact Registry `repositories.get`, `packages.tags.get` and `tags.list`; Resource Manager `projects.get` for `projectNumber`). It extends `internal/gcpfake/{gcs,secrets,run}.go`: bucket `get` with `projectNumber` and labels, and `getIamPolicy`; secret `get` and `getIamPolicy`; `jobs.get` with the template, where missing.

**Interfaces:**
- `infra.Clients{IAM, AR, Run, Secrets, Storage, CRM}`, built from `gcp.Options` (the local config's endpoints, so the fakes can stand in).
- `DiscoverInstallation(ctx, c, InstallationSpec) (Imports, error)`: the runs bucket (`projectNumber` must equal the project's, and it must carry `fugaro=managed`), the legacy `fugaro` registry and `fugaro-base` (both `fugaro=managed`). A marked `fugaro` registry sets `adopt_legacy_registry = true`; none leaves it `false`.
- `DiscoverRepo(ctx, c, RepoSpec) (Imports, Existing, error)`, checking each resource in the spec:
  - A job must carry `fugaro=managed`, the same `fugaro_repo` and the same `fugaro_workflow` (the check job: `fugaro_role=check`). `Existing` records its current image.
  - A job account's display name must be `JobSADisplayName` or `LegacyJobSADisplayName`. When it is the legacy one, `WorkflowSpec.ServiceAccount.DisplayName` is set **to that legacy value**, so the plan doesn't rename it.
  - A build account must have `BuildSADisplayName`. The repository's image registry must carry `fugaro=managed` and the same `fugaro_repo`.
  - A secret must carry `fugaro=managed`, the same `fugaro_repo` and `fugaro_secret` equal to its logical name.
  - What exists and passes becomes an `Import{To, ID}`. What exists and fails is a `*ForeignError` (exit 1) naming the resource and its marks. What doesn't exist is created.
- **The live IAM bindings are checked too** (the golden only compares code with code; the live bindings were made by whichever M4 commit ran `gcp-m4.sh`). For each existing job account:
  - The runs bucket's IAM policy must hold, for `roles/storage.objectUser` and that member, **either no binding or exactly one**, whose condition title and expression equal the spec's byte for byte, with an empty description.
  - A binding with another condition, or more than one, is refused with a diff of expected against found: the "create" would add a second grant next to it.
  - Each secret's policy is read, and its accessor members are compared with the spec's.
  - The result is a count for the summary: `IAM: 7 bindings match the live ones (adopted), 5 new`.
- Import addresses and IDs (keep this table in `imports.go` as data):

| Resource | `to` | `id` |
|---|---|---|
| runs bucket | `module.installation.google_storage_bucket.runs` | `<project>/<bucket>` |
| legacy registry (only with `adopt_legacy_registry`) | `module.installation.google_artifact_registry_repository.legacy[0]` | `projects/<p>/locations/<r>/repositories/fugaro` |
| base registry (on a rerun) | `module.installation.google_artifact_registry_repository.base` | `projects/<p>/locations/<r>/repositories/fugaro-base` |
| secret | `module.repo.google_secret_manager_secret.this["<logical>"]` | `projects/<p>/secrets/<id>` |
| repository registry | `module.repo.google_artifact_registry_repository.images` | `projects/<p>/locations/<r>/repositories/<id>` |
| job account | `module.repo.module.workflow["<wf>"].google_service_account.job` | `projects/<p>/serviceAccounts/<email>` |
| job | `module.repo.module.workflow["<wf>"].google_cloud_run_v2_job.this[0]` | `projects/<p>/locations/<r>/jobs/<name>` |
| build account, check job | `module.repo.google_service_account.build`, `module.repo.google_cloud_run_v2_job.check[0]` | as above |

- **IAM members are not imported.** Creating a member that already exists is idempotent: the provider reads, modifies and writes the policy, and the API merges an identical role, condition and member. So the bootstrap's bindings are adopted by a planned "create" that changes nothing. The live check above is what makes that safe to assume.
- `WriteImports(dir, Imports)` writes `imports.tf.json`: `{"import":[{"to":"…","id":"…"}]}`.
- `Readiness(ctx, c, RepoSpec, Existing) (RepoSpec, []Missing)`:
  - `deploy_job = true` when the job exists (**it never removes one**), or when the image's `latest` tag exists in the repository's registry and every mounted secret has an enabled version. Otherwise it is false, with `Missing` entries: `secret <logical> has no version: fugaro secrets set …`, or `image not built yet`.
  - **An existing job keeps its current image** (`Existing` records it: the legacy `fugaro/…` path for adopted jobs) until the spec's new image path has a `latest` tag. Only then does the spec's `image` take effect, so no apply ever points a job at a missing image.
  - **The schedule stays paused until a record exists:** `check.paused = true` unless every workflow the check covers has `builds/<slug>/<workflow>/image.json`. This holds for adopted and new repositories alike.

- [ ] **Step 1: Write the failing tests.**
  - `TestDiscoverImportsOwned`.
  - `TestDiscoverRefusesForeignSecret`: another repository's `fugaro_repo`.
  - `TestDiscoverRefusesSecretOtherLogicalName`.
  - `TestDiscoverRefusesOtherRepoJob`.
  - `TestDiscoverRefusesSADisplayName`.
  - `TestDiscoverAcceptsLegacySADisplayName`: the spec keeps the legacy name.
  - `TestDiscoverRefusesBucketInOtherProject`.
  - `TestDiscoverRefusesUnlabelledRegistry`.
  - `TestDiscoverRefusesMismatchedBinding`: a conditional binding whose expression differs by one space is refused with a diff.
  - `TestDiscoverCountsAdoptedBindings`.
  - `TestReadinessKeepsExistingJob`: the job exists and a secret has no enabled version, yet `deploy_job` stays true and a warning is returned.
  - `TestReadinessKeepsImageUntilBuilt`: an adopted job keeps the legacy image path until the new path has `latest`, then switches.
  - `TestReadinessWaitsForImageAndSecrets`.
  - `TestScheduleOnlyWithRecord`: no `image.json` gives `paused: true`; with it, `false`.
  - `TestImportAddressesExistInModule`: a cheap check with no new module. For each `To`, it finds `resource "<type>" "<name>"` and the `module "<name>"` calls by regular expression in the embedded files, which T3 has merged by M2.
  - `terraform`-tagged, `TestGeneratedRootValidates`: writes a repository root and an installation root with every import kind, and runs `terraform init -backend=false` and `terraform validate`. Validate fails on an import whose target block doesn't exist.
- [ ] **Step 2:** `go test ./internal/infra/ -run 'Discover|Readiness|Schedule|ImportAddresses'`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4:** `go test -race ./internal/infra/... ./internal/gcpfake/`, then `go test -tags terraform ./internal/infra/ -run GeneratedRootValidates`. Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "infra: adopt only what carries our marks and matches the live IAM, generate imports, gate jobs and schedules"`

---

### Task 14: `fugaro init` for the installation

**Files:** `internal/infra/{statebucket,installation,workdir}.go` and tests; `internal/cli/init.go` and `init_test.go`; `internal/cli/root.go` (`newInitCmd`); `internal/gcpfake/gcs.go` (bucket create and `setIamPolicy`)

**Interfaces:**
- `fugaro init`, with these flags:
  - `--project P --region R` (default: the local config's) and `--scheduler-region S` (default `gcp.SchedulerRegion(R)`)
  - `--runs-bucket B` (default `fugaro-runs-<project>`) and `--state-bucket S` (default `fugaro-tfstate-<project>`)
  - `--base-image IMG`
  - `--launcher MEMBER…` and `--operator MEMBER…`
  - `--budget N --budget-currency C --billing-account ID`, and `--alert-email E`
  - `--no-log-isolation`, `--registry-cleanup=dry-run|on|off` (default `dry-run` on the first apply; ruling in T2)
  - `--plan-only`, `--print-vars`, `--config-only`, `--forget`
  - `--allow-delete ADDR…`, `--yes`, `--json`
- The steps:
  1. **Environment guards**, which refuse:
     - `GODEBUG` with `http2debug` (as every cloud command does)
     - a project env var naming another project
     - `GOOGLE_IMPERSONATE_SERVICE_ACCOUNT`
     - no `terraform`, or one outside `>= 1.7, < 2`
  2. The workdir `$XDG_STATE_HOME/fugaro/terraform/<project>/installation` (default `~/.local/state/…`), mode 0700. Write the embedded module tree and root, `terraform.tfvars.json`, `backend.hcl`, `terraformrc` (T12), and `imports.tf.json` from `DiscoverInstallation`.
  3. **The state bucket:**
     - If it exists, it must belong to the project (`projectNumber`) and carry `fugaro=tfstate`, else refuse.
     - If it doesn't exist, **⚠ CONFIRM** "creates gs://S in R with versioning, for Terraform state (cents a month)". Then create it, and remove its `projectViewer` convenience bindings (ruling R2).
  4. **The runs bucket's viewers.** If the runs bucket exists, belongs to the project, carries `fugaro=managed` and still has `projectViewer` convenience bindings: **⚠ CONFIRM** "removes project Viewers' read access to gs://B, which holds transcripts and caches". Then one generation-matched `setIamPolicy` that removes only those two bindings. It is recorded in §6.1. Declining is allowed and noted in the summary.
  5. `Init`, `Plan`, `Show`, `Guard`, then print the summary. With `--plan-only`, stop here, exit 0.
  6. **⚠ CONFIRM** "(project P) applies N imports, M creates, K updates". Type the project ID, or pass `--yes`. Without a terminal and without `--yes`: exit 1, nothing applied.
  7. `Apply`, then `Output`.
  8. **Write the local config:**
     - from the outputs: `runs_bucket`, `registry_host`, `log_view`
     - `scheduler_region`, `terraform.state_bucket`, and `base_image` when given
     - existing `repos` and `registry` (now legacy) kept; `build.service_account` dropped
     - The existing file is backed up to `config.yaml.bak-<timestamp>` (mode 0600), and the diff is shown before writing (the same confirmation, unless nothing else changed).
- `--config-only`: steps 1 and 8 only. Outputs come from `terraform output -json` when the state is reachable, else from flags.
- `--print-vars`: prints the tfvars JSON, with no cloud calls and no Terraform.
- `--forget` (**rollback**, ruling R9):
  1. A guarded apply with `log_isolation = false` and `registry_cleanup = off`, whose plan may delete exactly the log sink, view, view IAM, exclusion and log bucket, and change the cleanup policies. `--forget` passes exactly those addresses as `allowDelete`, shows the plan, and asks (⚠ CONFIRM).
  2. Then `terraform state rm` of every remaining address, after a second ⚠ CONFIRM. It destroys nothing else.
  - It refuses while any repository root still has state (`fugaro/repos/*` in the state bucket): run `init --repo --forget` for each first.
  - Its output warns that the log bucket is now pending deletion for 7 days, and prints the `gcloud logging buckets undelete` command a retried migration needs first.
  - The allow-list is data in `internal/infra/forget.go`. Its addresses are checked against the module at M3 (`TestForgetAddressesExist`, see the merge points).

- [ ] **Step 1: Write the failing tests.** They use the fake `terraform` on `PATH` (`testutil.BuildFake…`, the same pattern as `FakeClaude`), the gcpfake servers through `endpoints`, and a temp `XDG_*`:
  - `TestInitRefusesWithoutConfirmation`: stdin not a terminal, no `--yes`; exit 1, and the fake log has no `apply`.
  - `TestInitAppliesSavedPlan`, with the fake's `plan` exiting 2 (changes).
  - `TestInitRefusesDeletePlan`: the fake's `show -json` has a replace of the bucket; exit 1, no `apply`.
  - `TestInitCreatesStateBucketAfterConfirm`: the IAM policy set has no `projectViewer`.
  - `TestInitRemovesRunsBucketViewers`: only the two `projectViewer` bindings are removed, generation-matched.
  - `TestInitRefusesForeignStateBucket`.
  - `TestInitRefusesOtherProjectEnv` and `TestInitRefusesImpersonation`.
  - `TestInitWritesLocalConfigAndBackup`.
  - `TestInitPlanOnly`.
  - `TestInitPrintVarsNoCalls`: the fakes see no requests.
  - `TestInitForgetUndoesExclusionThenStateRm`: the first apply's plan deletes only the log addresses, which are allowed, and then comes `state rm`.
  - `TestInitForgetRefusesWithRepoStates`.
  - `TestInitForgetWarnsLogBucketUndelete`.
  - `TestInstallationVarsMatchModule` (plain `go test`): every key of `InstallationVars`' JSON is a `variable "…"` of the embedded installation root, and every required variable has a key. Every `output "…"` of the root has an `InstallationOutputs` JSON tag, and the reverse. Names are found with T13's matcher.
  - `terraform`-tagged, `TestInstallationVarsPlan`: writes `InstallationVars` for the live-shaped fixture (with and without `adopt_legacy_registry`) into a temporary copy of the root, with a generated `.tftest.hcl` using `mock_provider "google"` and `command = plan`, and runs `terraform init -backend=false` and `terraform test`. A type or validation mismatch fails in CI, not in a live plan. CI's `terraform` job already runs `go test -tags terraform ./internal/infra/...`.
- [ ] **Step 2:** `go test ./internal/infra/ ./internal/cli/ -run 'Init|StateBucket'`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4:** `go test -race ./internal/...`. Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "init: stand up or adopt the installation through Terraform, with a guarded, confirmed apply and a behaviour-restoring forget"`

---

### Task 15: `fugaro init --repo`

**Files:** `internal/infra/repo.go` and test, `internal/cli/init.go`, `internal/e2e/init_test.go`

**Interfaces:**
- `fugaro init --repo [PATH] [--github-app-id N] [--no-build] [--plan-only] [--print-vars] [--forget] [--allow-delete ADDR…] [--allow-job-delete] [--yes]`. `--allow-job-delete` sets the root's `allow_job_delete` variable, which lowers the jobs' `deletion_protection` for offboarding; it is highlighted in the summary. It is run from, or pointed at, the repository's checkout.
- The steps:
  1. `loadCheckout`, which requires a valid `fugaro.yaml`. Without one it exits 1, pointing to `/fugaro:onboard` and `fugaro config example`, as design §8.2 says.
  2. The installation must exist: the state bucket holds `fugaro/installation`. Otherwise: "run fugaro init first". Read the installation root's `terraform output -json` into `Inputs.Installation` (ruling R1).
  3. `infra.Repo`, then `DiscoverRepo` (ownership and live IAM), then `Readiness` (jobs, images, the paused schedule).
  4. The workdir `…/repos/<slug>`. Write the root, the tfvars, `terraformrc` and the imports.
  5. Plan, guard, summary (including the IAM adoption count), ⚠ CONFIRM, apply (as in Task 14).
  6. For each workflow with no build record (a new repository, or an adopted one whose image is still on the legacy path), when every secret has a version and without `--no-build`: **⚠ CONFIRM** "submits a Cloud Build for <repo>/<wf> (billable, about $0.21: 13 minutes on E2_HIGHCPU_8 for the web repo)". Then submit and wait. That runs the whole pipeline (candidate, smoke, gate, promote, record) into the repository's own registry. Then re-run steps 3–5, so the job is deployed or switched to the new image, and the schedule is **unpaused**, since the record now exists. That second apply is its own ⚠ CONFIRM, and its summary highlights the job's `image` change and the Scheduler's `paused` change.
  7. Print what is still missing: each `fugaro secrets set <name> --repo <owner/name>` command, as the bootstrap's `secrets` step printed them, with `claude setup-token` for `claude-oauth-token` to be run in the user's own terminal. Then "rerun fugaro init --repo when they are stored".
  8. Add the repository to the local config's `repos` (`provider`, `base_branch`, `workflows`, `github_app_id`).
- `--forget`: `terraform state rm` of every address in the repository's state, after a ⚠ CONFIRM. It destroys nothing. The repository root has no exclusion, and its registry's cleanup policies don't touch the legacy registry M4 reads, so no undo apply is needed (ruling R9).
- It is idempotent: a second run with nothing changed plans no changes and exits 0 without asking.

- [ ] **Step 1: Write the failing tests.**
  - `internal/e2e/init_test.go` (hermetic: fake `terraform`, gcpfake, fixture checkout), `TestInitRepoAdoptsBootstrapResources`:
    - Seed the fakes with exactly what `gcp-m4.sh` makes for the sandbox fixture: the job with the M4 labels and the legacy image, the account with the legacy display name, the labelled secrets, and the conditional bucket binding and secret accessors.
    - `init --repo --yes --no-build` generates imports for the job, the account and every secret, and no other. The tfvars keep the legacy display name and the legacy image. The schedule, if any, is paused. The summary counts the adopted bindings. The guard passes.
  - `TestInitRepoFreshPrintsSecrets`: no secrets, so no job is planned and the commands are printed.
  - `TestInitRepoBuildsThenDeploys`: secrets have versions and there is no image. After the confirmation one build is submitted, and the second plan includes the job with the new image and `paused = false`.
  - `TestInitRepoAdoptedSwitchesImageAfterBuild`.
  - `TestInitRepoRefusesForeign`.
  - `TestInitRepoIdempotent`.
  - `TestInitRepoGitHubNeedsAppID`.
- [ ] **Step 2:** `go test ./internal/e2e/ ./internal/cli/ -run 'InitRepo'`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4:** `go test -race ./...` and `go test -tags terraform ./internal/infra/...`. Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "init: onboard a repository: adopt or create its resources, build its first image, deploy its jobs"`

---

### Task 16: Log isolation

**Files:** `deploy/terraform/gcp/modules/installation/logging.tf` and its tftest cases; `internal/backend/gcp/{logs,logs_test}.go`; `internal/gcpfake/logging.go`; `internal/cli/cloud.go`

**Interfaces:**
- HCL, all under `count = var.log_isolation ? 1 : 0`. Every name comes from `var.names.log` (T1):
  - `google_logging_project_bucket_config.fugaro`: `location = "global"`, `retention_days = 30`.
  - `google_logging_project_sink.fugaro`: `destination = "logging.googleapis.com/projects/<p>/locations/global/buckets/<bucket>"`, `filter = "resource.type=\"cloud_run_job\" AND labels.\"fugaro\"=\"managed\""` (workflow jobs and check jobs alike), `unique_writer_identity = true`. Check the provider docs for whether a same-project log bucket destination needs a `roles/logging.bucketWriter` grant for the writer identity; if so, add it as a member.
  - `google_logging_project_exclusion.fugaro_from_default`, with the same filter. **Check in the provider and Cloud Logging documentation that a project-level exclusion applies to the `_Default` sink** (it is additive, and we never manage `_Default`). If it doesn't, `log_isolation` can only add the dedicated bucket, and the runbook records that the logs stay readable in `_Default`.
  - `google_logging_log_view.runs` (no filter) and `google_logging_log_view_iam_member` giving `roles/logging.viewAccessor` to the launchers and operators.
  - Output `log_view`.
  - Cloud Build logs stay in `_Default` (ruling R13's recorded exception).
- **The alert and the exclusion.** Task 2's log-match alert must still fire on lines routed to the `fugaro` bucket and excluded from `_Default`. Check in the Cloud Monitoring documentation whether log-based alerting policies evaluate entries before exclusion and routing. If they don't, scope the policy's log condition to the `fugaro` bucket's view, or keep the `image-check` lines out of the exclusion (`AND NOT jsonPayload.event="image-check"`, which carries no agent output). T16 adds a tftest for whichever is chosen, and the runbook proves it live.
- Go: `gcp.Options.LogView string`. `readLogs` uses `ResourceNames: []string{LogView}` when it is set, else `projects/<p>`. The log URL `fugaro run` prints scopes the console query to that view's storage when it is set.

- [ ] **Step 1: Write the failing tests.** tftest `log_isolation_on` and `log_isolation_off` (no logging resource planned). `TestReadLogsThroughView`: the fake asserts `resourceNames`.
- [ ] **Step 2:** `terraform -chdir=deploy/terraform/gcp/roots/installation test` and `go test ./internal/backend/gcp/ -run View`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4:** The CI job's commands and `go test -race ./internal/...`. Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "terraform: keep Fugaro job logs in their own log bucket, readable only through its view"`

---

### Task 17: Design and docs edits M5 requires

**Files:** `docs/design/v1.md`, a new `docs/gcp-setup.md`, `docs/gcp-bootstrap.md`, `docs/gcp-live-checklist.md`, `plugin/skills/onboard/SKILL.md`, `README.md`

**`docs/design/v1.md`:**
- **§3.1 and §11:**
  - The layout is `deploy/terraform/gcp/{modules/{installation,repo,workflow},roots/{installation,repo}}`, embedded by `deploy/terraform/terraform.go`.
  - New packages: `internal/infra` (with `tf/`) and `internal/imagecheck`.
- **§3.2:**
  - The build account `BuildServiceAccountID(slug)`, the repository registry `RegistryRepoID(slug)`, and the check job `CheckJobName(slug)` (prefix `fugarochk-`, `fugaro_role=check`), each in its own hash domain.
  - The display names, and the legacy M4 form accepted and kept.
  - Terraform receives names, including the installation singletons; it never derives them.
- **§3.3:** `builds/<slug>/<workflow>/{image.json,check.json}`, never deleted by a lifecycle rule. The lifecycle ages are Terraform variables.
- **§3.4:** `LaunchSpec.Timeout`. The launcher's IAM is the custom roles of ruling R12 (`run.jobs.runWithOverrides` on each job). `Exhaustive` is removed.
- **§4.1, §4.6:** the `image` block in `result.json` (`built_at`, `baked_commit`), and that it is never fatal.
- **§4.5:** the per-run timeout override (`--total-timeout`, at most 24 h, kept by `--retry`).
- **§5.1:** the `rebuild:` block (`check`, `max_age` default 14 days until M7, `lockfiles`, `base`, `paths`) and its validation.
- **§5.4:** the new local config fields; `build.service_account` deprecated; `registry` legacy. Price overrides are no longer "arrive with M5".
- **§6.1:**
  - Per-repository build accounts and registries, `fugaro-base` with read-only build access, and their exact IAM.
  - The project's default build identity, as recorded in Task 18's precondition, and what it means for a leaked build token (ruling R6).
  - The `fugaro-creds` volume.
  - The smoke runs via `docker run --network none`, never as a step, because a `dockerfile:` image is repository code.
  - Log isolation (ruling R13), replacing "M5 records this in its IAM", and its Cloud Build logs exception.
  - The state and runs buckets' readers: `projectViewer` removed from both.
  - The scheduler account, and who must run `fugaro init` for the installation (ruling R12).
- **§7.2:**
  - Builds: candidate → smoke → gate (commit times; its limits) → promote by digest → record → best-effort untag, with the cleanup policies' keep rules, and why the build account stays `writer` only.
  - The daily check replaces "a Cloud Scheduler → Cloud Build trigger", with the "Why the image check exists" rationale and its cost arithmetic in short, the back-off after a failure, the paused-until-a-record rule and the Scheduler region.
  - GitHub builds.
  - Remove "GitHub repositories need M5".
  - Base images live in `fugaro-base`.
- **§8:** rewrite §8.1 and §8.2 to rulings R1–R4 and R9–R11. One state per repository, with installation outputs passed in as variables. `fugaro init`'s commands and flags, what it does and never does, the guard, the environment allowlist, the confirmation, and `--forget`.
- **§9.1:** rows for `init`, `image check` (print-only locally) and `image status`; `run --total-timeout`; `image build --no-smoke`; the `ls` warning lines and `image_age_s`.
- **§10.1:** local price overrides and their `Source`; the runner reads `FUGARO_COMPUTE_PRICES`.
- **§12:** the module's source path now ends in `modules/repo`.
- **§14:** mark M5 delivered, with a pointer to this plan, listing what it delivered and what it deferred: offboarding, `smoke: full`, and the M7 items.
- **§15:** add a "Resolved in M5" block: the image rebuild policy, state layout, registries, and log access.

**The other docs:**
- **`docs/gcp-setup.md`** (new): the `fugaro init` runbook for a fresh project, with the preconditions (billing, `serviceusage`, ADC and its quota project, Terraform), each command, what it creates, what it costs and how to undo it. It also covers:
  - **offboarding:** first an apply that sets the repository's jobs' `deletion_protection = false` (a flag, `--allow-job-delete`, that `init --repo` passes as a variable), then a `removed` block for the secrets, then a guarded destroy with `--allow-delete`
  - the `fugaro-build` retirement
  - pushing a base image to `fugaro-base`: `images/build-base.sh web-node <region>-docker.pkg.dev/<p>/fugaro-base/fugaro-web-node:dev-<commit>` and `docker push`, as an operator
- **`docs/gcp-bootstrap.md`:** a banner saying it is superseded by `gcp-setup.md` and kept only as M5's rollback path until the bootstrap is retired (Task 18, step 14). Its `base` step and "Setting the base image" gain a note that after M5 bases live in `fugaro-base`, and that `gcp-m4.sh base` pushes to the legacy `fugaro` repository, which only the rollback reads.
- **`docs/gcp-live-checklist.md`:**
  - check 11b gains a `docker run --network none` probe and a `docker run` default-network probe (the smoke path)
  - a new check 15, the GitHub credential mint, run only if a GitHub sandbox exists
  - a new check 16: a skipped image check, a back-off after a forced failure, and one forced rebuild
  - a new check 17: the cleanup policy's dry run never selects a `latest` or `dev-` version
  - a "Results of the third live run (M5)" section for Task 18 to fill in
- **`plugin/skills/onboard/SKILL.md`:** provisioning is `fugaro init --repo`; the skill may propose `rebuild.paths`, with evidence.

- [ ] **Step 1: Make the edits.** Check every `§` reference this plan cites.
- [ ] **Step 2:** `go test ./internal/cli/ -run TestSkillCommandsExist ./plugin/`.
- [ ] **Step 3: Commit.** `git commit -m "docs: record M5's infrastructure, image check and IAM decisions"`

---

### Task 18: Live bring-up and migration runbook (controller-run, with user confirmations)

This task writes no code. The controller runs it one step at a time and asks the user before every **⚠ CONFIRM** step. Each step is its own question: one approval never covers the next. Record every outcome, plan summary and `FACT` in the M5 PR. Anything that fails goes back to its task as a bug, with a hermetic test first.

**Preconditions (read-only, no confirmation needed):**
- `gcloud auth list` and ADC are set up, with ADC's quota project set to `<project>` (as in M4). `GOOGLE_IMPERSONATE_SERVICE_ACCOUNT` is unset.
- `terraform version` shows 1.16.4.
- `gcloud billing projects describe <project>` shows billing enabled.
- `gcloud services list --enabled --project <project>` shows `serviceusage.googleapis.com`.
- `fugaro ls --since 1d` shows **no active run**. Updating a job doesn't touch a running execution, but wait for any to finish anyway.
- **The project's default build identity** (review finding 12): `gcloud builds get-default-service-account --project <project>`, then that account's roles from `gcloud projects get-iam-policy <project> --flatten='bindings[].members' --filter='bindings.members:<account>'`. Record both in the PR and in §6.1 (Task 17 fills in the text after this step). If it is the legacy `<number>@cloudbuild.gserviceaccount.com` or holds `roles/editor`, the escalation path of ruling R6 is real in this project; tell the user before going on.
- **Snapshot for rollback**, into `~/fugaro-m5-backup/` (mode 700):
  - `gcloud run jobs describe <job> --region us-east5 --format=export` for both jobs
  - `gcloud storage buckets get-iam-policy`, `buckets describe`
  - `gcloud secrets get-iam-policy` for each of the 5 secrets
  - `gcloud artifacts repositories get-iam-policy fugaro`
  - the project's IAM policy filtered to `fugaro`
  - a copy of `~/.config/fugaro/config.yaml`
- `FUGARO` is set to a binary built from `m5`.
- `scheduler_region` will be `us-east4`: Cloud Scheduler isn't offered in `us-east5` (ruling R4). The check jobs stay in `us-east5`.

**Steps:**
1. `fugaro init --plan-only`. This is read-only apart from **creating the state bucket** (**⚠ CONFIRM**, `gs://fugaro-tfstate-<project>`, cents a month) and removing the runs bucket's `projectViewer` bindings (**⚠ CONFIRM**; it can be declined).
   - The plan must show:
     - **imports:** the runs bucket and the legacy `fugaro` registry
     - **creates:** the APIs (enabling an enabled API changes nothing, and adds `cloudresourcemanager`), `fugaro-base` with its cleanup policies in **dry run**, the custom roles, `fugaro-scheduler`, the log bucket, sink, exclusion and view, and the member bindings
     - **updates in place:** only labels on the imported resources, or nothing. No `lifecycle_rule` change (the three rules equal the bootstrap's) and no cleanup policy on `fugaro`. `goog-terraform-provisioned` is added only to *created* resources, never to imports.
     - **zero deletes**
   - Show the summary to the user.
2. **⚠ CONFIRM** `fugaro init --yes` with the same flags. Then, all read-only:
   - `fugaro ls`
   - `fugaro logs <a recent run>`, through the new log view
   - `gcloud logging read` on `_Default` for a new Fugaro job line: there should be none once the exclusion applies
   - the diff of the rewritten local config against the backup
3. **⚠ CONFIRM** Build and push the base from `m5` into `fugaro-base`, since the credential, check, gate and record commands live in the base image's `fugaro` (about 1.5 GB). Through `heavy.sh`: `images/build-base.sh web-node <region>-docker.pkg.dev/<project>/fugaro-base/fugaro-web-node:dev-<commit>`, then `docker push` of that tag. Then **⚠ CONFIRM** `fugaro init --base-image <that tag> --yes` (a plan with no resource change; it rewrites `base_image` in the local config). The old dev tag in `fugaro` stays for the rollback.
4. **The sandbox:** first **⚠ CONFIRM** a commit to the sandbox repository setting `rebuild.check: off` in its `fugaro.yaml` (ruling R16), so it gets no check job or schedule. Then, in a checkout of `acme/sandbox`, run `fugaro init --repo --plan-only`.
   - **Imports:** the job, the job account and 3 secrets.
   - **Creates:** the repository registry, the build account and its IAM, and the job's member bindings, adopted: the summary says "IAM: n bindings match the live ones". Discovery refuses first if any live binding differs from the spec.
   - **Updates in place, which are acceptable:**
     - the job's env order (gcloud's order against the sorted one)
     - the CPU form (`"1"` against `"1000m"`)
     - client annotations and template labels (`client.knative.dev/nonce`)
     - `deletion_protection`, which is only in state
     - The job keeps the **legacy** image path, and its `service_account` is unchanged. The highlight section must show no image or account change.
   - **Zero deletes.** The display name is not changed.
5. **⚠ CONFIRM** `fugaro init --repo --yes` (without `--no-build`), after checking the plan above. Then read-only:
   - `gcloud storage buckets get-iam-policy`: still **one** conditional binding for the sandbox account, not two
   - `gcloud run jobs describe`: `maxRetries: 0`
6. `init --repo` then offers the first build: **⚠ CONFIRM** the Cloud Build (about $0.05 for the sandbox). It runs as the sandbox's **build account** into its own registry, so it proves the credential volume, candidate, smoke (`--network none`), gate, promote and record steps.
   - The second apply switches the job to the new image. **⚠ CONFIRM**, and check the highlighted `image` change.
   - Then, read-only:
     - `fugaro image status --repo acme/sandbox` shows the record: `builds/<slug>/web/image.json` exists
     - `gcloud artifacts docker tags list` on the new registry shows that `latest` moved to the record's `image_digest`
     - a leftover `candidate-` tag, with the untag warning in the build log, is expected and fine
7. **⚠ CONFIRM** Live checks (Task 18 of M4, updated). Grant Token Creator as before, then run:
   - `TestLiveCloudBuildSecretAndDigest`, whose check 11b now also probes `docker run --network none` and `docker run` on the default network: both must be `blocked`
   - `TestLiveSandboxRun`, now with `--total-timeout 15m`: the execution's timeout is 17m
   - `TestLiveGCPCleanup`
   Then **⚠ CONFIRM** remove the Token Creator grant.
8. **The web repo:** `fugaro init --repo --plan-only`, with the same expectations as the sandbox, plus the check job and a **paused** Scheduler job in `us-east4`. Then **⚠ CONFIRM** apply.
9. **The web repo's first image and its check, with the user watching:**
   - Read-only: `fugaro image check --dry-run` from the checkout. It prints `rebuild` with the reason `no-record`. Show it to the user.
   - **⚠ CONFIRM** the offered first build (about $0.21). Then **⚠ CONFIRM** the second apply, which switches the job's image and **unpauses** the schedule (both highlighted in the summary).
   - Read-only again: `fugaro image check --dry-run`, which must now print `skip`. Record check 16's `FACT`s.
   - **⚠ CONFIRM** `gcloud scheduler jobs run <scheduler job from the repo root's outputs> --location us-east4`. The check job logs `decision: skip`, and `check.json` appears. No build is started.
10. **The cleanup policies' dry run**, a day after step 1: read-only, query the Artifact Registry audit logs for `fugaro-base` and the two repository registries (check 17). No version tagged `latest` or `dev-` may be listed. If one is, stop: the keep rules are wrong. Otherwise **⚠ CONFIRM** `fugaro init --registry-cleanup=on --yes`, a plan that changes only `fugaro-base`'s `cleanup_policy_dry_run`. Then **⚠ CONFIRM** `fugaro init --repo --yes` in each repository's checkout, which copies the new `registry_cleanup_dry_run` output into `registry.cleanup_dry_run` and flips that repository's registry. Both repository registries and `fugaro-base` end up with dry run off.
11. **⚠ CONFIRM** One real web repo run with a small task the user chooses. It opens a PR that notifies the default reviewer. Its `ls --json` row has `image_age_s`.
12. **The alert**, if the user gave an email: **⚠ CONFIRM** one failed check on the sandbox (temporarily disable the sandbox's `bitbucket-token` latest version, run its check job once, then re-enable the version). The alert email must arrive, which proves the alert sees lines routed away from `_Default` (T16). Skip this if the sandbox has `check: off`; use the web repo only with the user's explicit approval.
13. **Retire `fugaro-build`**, only after steps 6 and 9 built successfully with per-repository accounts:
    - **⚠ CONFIRM** remove its accessor bindings on the 5 secrets, its `artifactregistry.writer` on `fugaro` and its project `logging.logWriter` (`gcloud … remove-iam-policy-binding`, with the arguments from the snapshot)
    - **⚠ CONFIRM** `gcloud iam service-accounts disable fugaro-build@…`
    - A week later, **⚠ CONFIRM** `gcloud iam service-accounts delete fugaro-build@…`
14. **Retire the bootstrap in code:** a follow-up PR deletes `deploy/bootstrap/gcp-m4.sh`, `bootstrap_test.go`, the hidden `fugaro gcp job-spec` (and its golden test) and `docs/gcp-bootstrap.md`. It keeps `deploy/bootstrap/sandbox/`, moved to `deploy/sandbox/`, and its README. Open it only after step 13's deletion, since until then the script is the rollback.

**Rollback**, at any step before 13, as a checklist:
0. **⚠ CONFIRM, first:** pause every Fugaro Scheduler job (`gcloud scheduler jobs pause <name> --location us-east4 --project <project>`, names from each repository root's outputs), so no check submits a billable build while the rollback runs.
1. **⚠ CONFIRM** `fugaro init --repo --forget` for each onboarded repository (state only).
2. **⚠ CONFIRM** `fugaro init --forget`. Its first phase applies `log_isolation = false` and `registry_cleanup = off`, which **deletes the `_Default` exclusion**, so M4's `fugaro logs` finds new log lines again, and removes every cleanup policy. It also deletes the `fugaro` log bucket, which stays pending deletion for 7 days. To retry the migration within that week, first **⚠ CONFIRM** `gcloud logging buckets undelete fugaro --location=global --project <project>`, and `fugaro init` then imports it. Its second phase removes the state.
3. Restore `~/.config/fugaro/config.yaml` from the backup. That brings back `base_image` pointing at the legacy dev tag in `fugaro`.
4. **⚠ CONFIRM, always:** `gcp-m4.sh --apply job` with the M4 binary for each repository. This redeploys the job from the M4 spec, which puts back the legacy image path, the env order and no `FUGARO_COMPUTE_PRICES`. The legacy display names were kept, so the script's ownership checks pass.
5. Check `fugaro logs` on a new run, and `gcloud run jobs describe` for `maxRetries: 0`.
6. Leftovers are additive and don't affect M4: the build accounts, the new registries, `fugaro-base`, the scheduler account and its Scheduler jobs (paused in step 0, now unmanaged), the custom roles, and the state bucket. Remove them with `gcloud` from the snapshot's diff, each **⚠ CONFIRM**.
7. The runs bucket's `projectViewer` bindings, if they were removed in step 1, can be restored from the snapshot (**⚠ CONFIRM**). M4 doesn't need them.

After step 13, a rollback also needs `fugaro-build` re-enabled (`gcloud iam service-accounts enable`) and its bindings restored from the snapshot.

---

## Open questions for the user

The controller ruled on the rest (rebuild defaults with `max_age: 14d`, the sandbox `check: off`, the Scheduler region, the paused schedule, `fugaro-base`). Still open:

1. **The alert email.** Which address should rebuild and check failures go to? My suggestion is the local config's `user`. Without one, failures show only in `fugaro ls`, `fugaro image status` and the check's `ERROR` log line.
2. **Who else should be a launcher or an operator?** Both lists are empty by default, and you are the owner.
3. **Is there a GitHub sandbox repository** (and a GitHub App) to test GitHub builds live (checklist check 15)? If not, M5 ships the GitHub path tested hermetically only; the token-minting code is M2's.

## After M5

- **A measurement (not an M5 task):** bake the web repo's internal `packages/*` libraries into its image (an `image.setup` step such as `yarn build:packages`, with `rebuild.paths: ["packages/**"]`). Most of a web repo run's `build` time goes to building them on every run, so this is probably where a rebuild pays off most. Measure first: `image_age_s` and the `bootstrap` stage duration from Task 11, plus one run's `verify build` time with and without the step.
- **M6:** follow-up runs use the same jobs, and launch with `LaunchSpec.Timeout` when the task carries an override.
- **M7:**
  - Publishing the base images makes `base_image` optional again, makes the `base` trigger meaningful, and lets `max_age` return to 7 days (ruling R14).
  - The `fugaro:status` skill reads `image status --json` and `ls` warnings; the commits-behind count and a `diagnose` image line come with it.
  - `smoke: full`, if wanted, gated on live check 11b.
  - The module's `?ref=vX.Y.Z` source is tested against a tag.
- **Later:**
  - offboarding with one command (`init --repo --remove`)
  - Cloud Build logs under log isolation
  - an egress-restricted VPC
  - importing the hand-made budget

## Execution

Subagent-driven is recommended, with a fresh reviewer per task. The Go↔Terraform contract (the tfvars shape, the import addresses, the condition strings) crosses lanes, and drift there shows up only as a live plan with a replace in it. Tasks 1, 8, 12 and 13 deserve the closest review. Task 18 is controller-only: it changes real resources, and the user confirms every ⚠ step.

# GCP setup with `fugaro init`

`fugaro init` sets up Fugaro in a Google Cloud project with Terraform. It plans, shows you the plan, and applies exactly that plan once you confirm. This runbook takes a fresh project from nothing to a repository's first run, one confirmed step at a time. It also covers adopting what M4's bootstrap made, rolling back, offboarding a repository, retiring `fugaro-build`, and pushing a base image.

Design §8 says how `init` works and why (the state layout, the guard, the environment allowlist); §6.1 has the IAM it creates. This page says what to run.

**What you end up with:**

- **The installation** (`fugaro init`, once per project): the enabled APIs, the runs bucket, the base registry `fugaro-base`, the custom roles, the scheduler account, log isolation, and optionally a budget and an alert.
- **Each repository** (`fugaro init --repo`, from its checkout): its secret containers, its image registry, its build account, one job and job account per workflow, and a daily image check (a Cloud Run job and a Scheduler job).
- **The local config** (`~/.config/fugaro/config.yaml`), written from the installation's outputs, with the diff shown first and the old file kept as `config.yaml.bak-<time>`.

## How every step behaves

- **Nothing is applied unseen.** `init` prints a summary of the plan (risky in-place updates first) and a `⚠ CONFIRM (project <project>): …` banner, then asks you to type the project ID. `--yes` answers for you, and should be used only after you have read what that exact step does. Without a terminal, `init` refuses and does nothing unless `--yes` is given.
- **It applies the plan it showed, and only that.** It saves the plan to a file and applies the file.
- **It never deletes.** A plan that would delete or replace anything is refused (exit 1) unless `--allow-delete <address>` names that resource.
- **`--plan-only`** stops after the plan. It can still create the state bucket, behind its own confirmation, because the plan needs it.
- **`--print-vars`** prints the Terraform variables and exits, with no cloud calls, so you can read what would be sent. With `--repo` the values are **ungated**: no discovery or readiness check ran, so every workflow has `deploy_job = true`, its job uses the new image path (which may not be built yet), and its account gets the new display name (an adopted bootstrap account would be renamed). It says so on stderr. Adopt a repository through `fugaro init --repo`, not by applying these values.
- **It never touches a secret value.** Secret containers are Terraform's; their values are `fugaro secrets set`'s.
- **Exit codes:** 0 ok, 1 a user error or refusal, 2 a remote failure.
- **Reruns are safe.** Running a step again with the same inputs plans no change. Flags are not remembered, except the ones the local config stores (see "Flags to pass again").

## Preconditions

These are read-only checks, and yours to run and fix.

1. **Your account.** `gcloud auth list` shows it, and `gcloud auth application-default login` has been done: `fugaro` and Terraform both use Application Default Credentials.
2. **ADC's quota project.** `gcloud auth application-default set-quota-project <project>`. The generated provider sets `user_project_override`, so this is the project that is billed for API calls.
3. **No overriding environment.** `GOOGLE_IMPERSONATE_SERVICE_ACCOUNT` is unset (`init` refuses to run with it, rather than apply as someone else), and `GOOGLE_PROJECT`, `GOOGLE_CLOUD_PROJECT` and `CLOUDSDK_CORE_PROJECT` are unset or name `<project>`. `GODEBUG` doesn't contain `http2debug`.
4. **Billing** is linked: `gcloud billing projects describe <project>` shows `billingEnabled: true`. Terraform doesn't link billing.
5. **`serviceusage.googleapis.com`** is enabled: `gcloud services list --enabled --project <project>`. Terraform needs it to enable the other APIs, and can't enable it itself.
6. **Terraform** 1.7 or newer (before 2.0) is on `PATH`: `terraform version`.
7. **Your role.** For the installation step you must be an Owner. The three IAM administration roles (`roles/resourcemanager.projectIamAdmin`, `roles/iam.serviceAccountAdmin` and `roles/iam.roleAdmin`) are needed on top of the rights to create buckets, registries, secrets, jobs, log buckets and Scheduler jobs and to enable APIs (for example Editor, plus the Run, Secret Manager and Artifact Registry admin roles for their IAM), and are not enough alone. `fugaro init --repo` creates service accounts and project-level bindings as well, so run it as an Owner too. The operator role that the installation step grants (design §6.1) lets people work with what exists (store secrets, act as the accounts, push base images); it doesn't let them create it.
8. **Who else needs access.** Both member lists are empty by default. Decide whether anyone besides you launches runs (`--launcher user:a@example.com`) or onboards repositories (`--operator …`). Members are `user:`, `group:`, `serviceAccount:` or `domain:` IAM members.
9. **The project's default build identity.** A build request that names no service account runs as it, and a leaked build token could submit one (design §6.1). Read it, and what it holds, before you onboard anything:

   ```bash
   gcloud builds get-default-service-account --project <project>
   gcloud projects get-iam-policy <project> --flatten='bindings[].members' \
     --filter='bindings.members:<that account>' --format='table(bindings.role)'
   ```

   If it is the legacy `<number>@cloudbuild.gserviceaccount.com` or holds `roles/editor`, that path is real in your project: weigh that before you onboard a repository whose code you don't trust.
10. **Provider credentials.** Each repository's Bitbucket repository access token, or GitHub App (its ID and a private key file), exists. Name it when you create it, for example `Fugaro`: the name is shown as the author of the pull requests and comments Fugaro creates, and can't be changed later (git-providers.md).
11. **On an installation that ran the M4 bootstrap:** `fugaro ls --since 1d` shows no active run, and you have a snapshot to roll back to (see "Adopting an M4 installation").

## The steps

### 1. The installation, planned

```bash
fugaro init --project <project> --region <region> --plan-only
```

Add `--launcher`, `--operator`, `--alert-email`, `--budget`, `--budget-currency` and `--billing-account` as you need them (see "Optional pieces").

- **It creates, after its own confirmation:** the Terraform state bucket `gs://fugaro-tfstate-<project>` (`--state-bucket` overrides the name), in `<region>`, with versioning, uniform bucket-level access, public access prevention, the label `fugaro=tfstate`, and no project-Viewer read access.
- **It offers to remove project Viewers' read access to the runs bucket,** if that bucket already exists (on a fresh project it does this after the apply, step 2), after another confirmation. Declining is allowed and leaves a warning; `--plan-only` never removes it.
- **Cost:** the state bucket is cents a month.
- **Undo:** delete the bucket (`gcloud storage rm -r gs://<bucket>`), once nothing is in it you need. The Viewer removal is undone by granting the bucket's `roles/storage.legacyBucketReader` and `roles/storage.legacyObjectReader` back to `projectViewer:<project>`.
- **The plan shows:** the APIs, the runs bucket (an import if it exists), `fugaro-base` with its cleanup policies in **dry run**, the custom roles, `fugaro-scheduler`, the log bucket, sink, exclusion and view, and the member bindings. Read it. On a fresh project it is all creates and no deletes.

### 2. The installation, applied

```bash
fugaro init --project <project> --region <region>
```

Give it the same flags as step 1. It asks for the project ID once more, applies the saved plan, and writes the local config.

- **Creates** what step 1's plan showed.
- **Cost:**
  - The APIs are free to enable.
  - Service accounts, custom roles and IAM bindings are free.
  - The base registry is storage, about $0.10 per GB-month.
  - The log bucket is Cloud Logging's usual ingestion and retention charge beyond its free allotment (30-day retention).
  - The runs bucket is storage.
  - A budget and its alert policy add nothing.
- **Undo:** `fugaro init --forget` stops Terraform managing all of it and turns log isolation and cleanup off first (see "Rolling back"). It destroys nothing else. Deleting the resources is a manual `gcloud` job, since the runs bucket and every registry are protected on purpose.
- **The runs bucket's Viewer access.** On a fresh project the apply creates the runs bucket, so `init` then makes the confirmed removal of project Viewers' read access after the apply. If you declined it or it was interrupted, rerun `fugaro init`: a rerun is harmless.
- **Check:** `fugaro ls` works, and the local config has `runs_bucket`, `registry_host`, `log_view`, `scheduler_region` and a `terraform:` block. **Rerun it whenever you change `--launcher`, `--operator` or `--alert-email`** (or the flags in "Flags to pass again").

### 3. The base image, in `fugaro-base`

Until the base images are published (M7), an operator builds the base from a Fugaro checkout and pushes it to `fugaro-base`. Only operators can write there, and build accounts only read it, so a leaked build token can't replace the image every repository's `credential` step and check job run. The `credential`, `gate`, `record` and `check` commands live in the base image's own `fugaro`, so build it from a version that has them.

```bash
gcloud auth configure-docker <region>-docker.pkg.dev            # edits ~/.docker/config.json
commit=$(git -C <fugaro checkout> rev-parse --short HEAD)
tag=<region>-docker.pkg.dev/<project>/fugaro-base/fugaro-web-node:dev-$commit
sh <fugaro checkout>/images/build-base.sh web-node "$tag"
docker push "$tag"                                   # ⚠ CONFIRM: about 1.5 GB of billable registry storage
fugaro init --base-image "$tag"
```

- **The push** needs `roles/artifactregistry.writer` on `fugaro-base`, which the operator role gives.
- **`init --base-image`** is a plan with no resource change: it rewrites `base_image` in the local config. Rerun `fugaro init --repo` in each repository afterwards, so the check job's image follows it.
- **Cost:** about 1.5 GB of registry storage per base image.
- **Undo:** delete the image (`gcloud artifacts docker images delete <tag> --delete-tags`), and remove the `credHelpers` entry from `~/.docker/config.json`.
- **A dev tag doesn't move,** so the `base` rebuild trigger fires only when `base_image` itself changes or the tag is pushed again. `rebuild.max_age` (14 days) bounds how long a base security fix waits.

### 4. A repository

1. **Write and commit its `fugaro.yaml`.** Use the `fugaro:onboard` skill, or `fugaro config example > fugaro.yaml` and edit it, then `fugaro validate` and `fugaro image build --local`. Merge it. For a sandbox that should never rebuild by itself, set `rebuild: { check: off }` on its workflows before its first `init --repo`: it then gets no check job and no schedule. Turning the checks off later deletes a protected job: see "Turning checks off, or removing a workflow".
2. **Plan it, from the repository's checkout:**

   ```bash
   fugaro init --repo --plan-only
   ```

   For a GitHub repository add `--github-app-id <id>` (not a secret; it is recorded in the local config). The plan shows the registry, the build account, and per workflow the job account, the secret containers and the job (created only once its image exists and its secrets have a version), plus the check job and a **paused** Scheduler job. Discovery refuses first if anything under our names isn't marked as ours, and prints warnings for what it leaves alone.
3. **Apply it:**

   ```bash
   fugaro init --repo
   ```

   It applies the plan and prints the `fugaro secrets set` commands still needed. For a repository with no image yet, the job isn't deployed until the image and secrets exist.
4. **Store the secrets,** each with its own command, in your own terminal (design §6.1): `fugaro secrets set bitbucket-token --repo acme/webapp < <token file>`, `fugaro secrets set github-app-key --repo acme/webapp < <key.pem>`, `claude setup-token` then `fugaro secrets set claude-oauth-token --repo acme/webapp` at the hidden prompt, and the workflow's own secrets. Never put a value in a conversation with an agent.
5. **Rerun `fugaro init --repo`.** Now it offers each workflow's **first image build**, behind its own confirmation. It is billable: a 13-minute build on `E2_HIGHCPU_8` is about $0.21. It runs as the repository's build account into its own registry, and proves the credential volume, the candidate, the smoke test without network, the gate, promotion and the record. Declining it just prints the `fugaro image build` command.
6. **The second apply** switches the job to the new image and, once every workflow has a record, **unpauses the daily check**. It has its own confirmation, and the summary highlights the job's `image` and the schedule's `paused`.

- **Cost:**
  - Each build is billed per build-minute.
  - A job costs nothing until it runs.
  - A check is about $0.004 a run, about $0.12 a month.
  - Cloud Scheduler is $0.10 a month per job beyond its free ones.
  - A repository's registry is storage. An active repository costs about $1 to $3 a month all told (design §7.2).
- **Vertex AI:** when any workflow has `agent.auth: vertex`, `init --repo` records `vertex: true` for the repository in the local config. `fugaro init` enables the Vertex AI API whenever a recorded repository has it. So for the first such repository, `init --repo` warns that the installation hasn't enabled Vertex AI yet: **rerun `fugaro init`** afterwards. The API stays enabled while any recorded repository has the flag.
- **Undo:** `fugaro init --repo --forget` (state only), or offboarding, below.
- **Check:**
  - `fugaro image status --repo acme/webapp` shows the record.
  - From the checkout, `fugaro image check --dry-run` prints `skip` for a current image (locally it only prints, and needs `base_image`).
  - `fugaro run …` launches a run.

### 5. Turning registry cleanup on

Cleanup starts in **dry run**: Artifact Registry logs what it would delete and deletes nothing. A day or more after the first images exist, audit the log for `fugaro-base` and each repository's registry (docs/gcp-live-checklist.md, check 17). It must never list a version tagged `latest` or `dev-`. If one appears, stop: the keep rules are wrong, and it is a bug to report. If not:

```bash
fugaro init --registry-cleanup on          # only fugaro-base's dry-run flag changes in the plan
fugaro init --repo                         # in each repository's checkout, no flag needed
```

The repository step copies the installation's setting (its `registry_cleanup_dry_run` output) into that repository's registry, so it takes no flag of its own; both registries and `fugaro-base` end with dry run off. Keep rules always win over delete rules: versions tagged `latest` (and `dev-` in `fugaro-base`) and the 3 newest are never deleted. Cost: none; it lowers registry storage. Undo: `--registry-cleanup dry-run`.

## Optional pieces

- **A budget:** `--budget 100 --budget-currency USD --billing-account <id>` creates one with 50%, 90% and 100% alerts. It needs billing-account permissions. **Pass the same three flags on every later `fugaro init`**: the budget isn't stored, and a plan without them would delete it (the guard refuses that and names the flags). A budget made by hand stays unmanaged.
- **An alert:** `--alert-email <address>` emails a failed image check or rebuild (and stores the address in the local config). Without it, failures still show in `fugaro ls`, `fugaro image status` and the check job's `ERROR` log line.
- **Launchers and operators:** `--launcher` and `--operator`, repeatable, stored in the local config; rerun `fugaro init` and `fugaro init --repo` to change who has what. The installation step grants project-level and bucket-level roles, and each repository step the per-repository ones.
- **The Scheduler region:** Cloud Scheduler isn't offered in every Cloud Run region, so the daily check's Scheduler job may run elsewhere (`us-east5` uses `us-east4`). `--scheduler-region` overrides it; the check job stays in `<region>`.
- **Log isolation off:** `--no-log-isolation` leaves job logs in `_Default`.
- **Running Terraform yourself:** use `deploy/terraform/gcp/roots/*` as examples, and `github.com/dimipaun/fugaro//deploy/terraform/gcp/modules/repo?ref=vX.Y.Z` as the module source, with the inputs from `fugaro init --print-vars` (for a repository these are ungated, see above: set `deploy_job` and the image yourself until the image exists). Then `fugaro init --config-only` writes the local config from the installation's outputs.

## Turning checks off, or removing a workflow

Setting every workflow of an onboarded repository to `check: off` makes the plan delete its check job, its Scheduler job and the scheduler's invoker grant. Removing a workflow from `fugaro.yaml` makes it delete that workflow's job, its account and grants, and any secret only it used. Neither works in one run:

- the guard refuses every delete until `--allow-delete <address>` names it
- a job keeps `deletion_protection = true` in the state, and once it has left the configuration `--allow-job-delete` can no longer lower it, so the apply fails
- a secret has `prevent_destroy`, so the plan fails before the guard sees it

So do it in this order:

1. **Before changing `fugaro.yaml`,** run `fugaro init --repo --allow-job-delete` from the checkout. Its only change is `deletion_protection = false` on the repository's jobs.
2. **A secret only the removed workflow used:** Terraform must forget it rather than delete it. Take it out of the state as in "Offboarding a repository", step 2 (a `removed` block, or `terraform state rm '<address>'`, run by hand in the repository's working directory, outside fugaro's guard); the secret and its value stay in Secret Manager, and `gcloud secrets delete` removes it if you want it gone. Skip this when no secret is used by that workflow alone.
3. **Change `fugaro.yaml`,** merge it, and run `fugaro init --repo --allow-delete <address> …`, naming each address the guard listed (the refusal prints them). Read the plan: it must delete only what you removed. The remaining jobs get their protection back in the same apply.

## Flags to pass again

`init` doesn't remember every flag. The local config keeps the state bucket, launchers, operators, alert email, base image and scheduler region. The budget flags (`--budget`, `--budget-currency`, `--billing-account`), `--registry-cleanup` and `--no-log-isolation` are not kept: leave one out and the plan removes or resets what it set. The guard refuses a plan that deletes the budget, alert channel or member grants, with a hint naming the flags that keep them. `fugaro init --repo` needs `--github-app-id` only the first time (it is stored), and records whether the repository uses Vertex AI, which `fugaro init` reads. Read the plan every time.

## Adopting an M4 installation

`fugaro init` adopts what `deploy/bootstrap/gcp-m4.sh` made, under the same names, and changes nothing you didn't ask for. Do it in this order, each its own plan and confirmation, with **zero deletes** in every plan: the installation, then a sandbox repository, then the others.

1. **Snapshot for rollback**, into a directory only you can read (mode 700): `gcloud run jobs describe <job> --region <region> --format=export` for each job; `gcloud storage buckets get-iam-policy` and `describe` for the runs bucket; `gcloud secrets get-iam-policy` for each secret; `gcloud artifacts repositories get-iam-policy fugaro`; the project's IAM policy filtered to `fugaro`; and a copy of `~/.config/fugaro/config.yaml`.
2. **The installation:** steps 1 and 2 above. The plan should import the runs bucket and the legacy `fugaro` registry, create the rest, and update in place only labels. The three lifecycle rules equal the bootstrap's, so the bucket's `lifecycle_rule` shouldn't change, and the legacy registry gets no cleanup policy.
3. **The base image:** step 3. The M4 dev tag in `fugaro` stays for a rollback.
4. **The sandbox, then each repository:** step 4. Discovery imports the job, the job account and the secrets, after checking that each carries our mark and that the live bucket and secret bindings equal the spec's byte for byte. The summary says `IAM: n bindings match the live ones (adopted), m new`. Acceptable in-place updates on an adopted job are the env's order, the CPU's form (`"1"` against `"1000m"`), client annotations and template labels, and `deletion_protection`, which lives only in state. The job keeps its **M4 image path and its legacy service account display name** (`Fugaro M4 job …`, which the bootstrap still recognizes), and the highlighted section must show no `image` or `service_account` change until the new registry has an image. If it shows a delete, stop.
5. **The first build, then the switch:** as in step 4.5 and 4.6. An adopted image has no record, so its schedule stays paused until the first build writes one; an unpaused first check would start a billable build that moves a live `:latest`.
6. **Retire `fugaro-build`** once the new build accounts have built successfully, below.

## Rolling back

At any point before `fugaro-build` is retired:

0. **First,** pause every Fugaro Scheduler job so no check submits a billable build during the rollback: `gcloud scheduler jobs pause <name> --location <scheduler region> --project <project>` (the names are in `fugaro init --repo --print-vars`, or `gcloud scheduler jobs list --location <scheduler region>`).
1. `fugaro init --repo --forget` in each onboarded repository. It removes the repository from Terraform's state and deletes its now-empty state object (the state bucket is versioned, so it stays recoverable); it destroys nothing.
2. `fugaro init --forget`. Its first phase is a normal guarded, confirmed apply with log isolation and registry cleanup turned off, which **deletes the `_Default` exclusion** (so M4's `fugaro logs` finds new lines in `_Default` again), the sink, the view, the log bucket and `fugaro-base`'s cleanup policies (the repositories' registries keep theirs, which M4 never reads), and nothing else. **A deleted log bucket stays pending deletion for 7 days and its ID can't be reused meanwhile.** To retry the migration within the week, first run `gcloud logging buckets undelete fugaro --location=global --project <project>` (confirmed); `fugaro init` then imports it, and refuses to plan while it is still pending deletion. Its second phase, after another confirmation, runs `terraform state rm` for everything left.
3. **Restore** the backed-up `~/.config/fugaro/config.yaml`.
4. **Always,** redeploy each job with the M4 binary's `gcp-m4.sh --apply job`. It puts back the M4 image path, env and order, and drops `FUGARO_COMPUTE_PRICES`. The legacy display names were kept, so its ownership checks pass.
5. Check `fugaro logs` on a new run, and `gcloud run jobs describe` for `maxRetries: 0`.
6. What `--forget` leaves is additive and doesn't affect M4: the build accounts, the new registries, `fugaro-base`, the scheduler account and its (paused) Scheduler jobs, the custom roles and the state bucket. Remove them with `gcloud` from the snapshot's diff, each confirmed.
7. **Retrying the migration later:** `fugaro init` and `fugaro init --repo` import again what `--forget` left behind and still carries our mark:
   - the runs bucket and the registries
   - the three custom roles, matched by title (a role with another title is refused; a deleted one is not imported, and the plan's create restores it)
   - `fugaro-scheduler`, matched by its display name
   - the log bucket, while log isolation is on. Log buckets carry no labels, so its description is the mark: a `fugaro` log bucket whose description isn't Fugaro's is refused (exit 1), with the `gcloud logging buckets update` command that marks it if it is Fugaro's, or `--no-log-isolation` if it isn't (a log bucket can't be renamed). Ours still pending deletion is refused (exit 1) with the `gcloud logging buckets undelete` command to run first. A change of its retention is listed under "⚠ Review these first".
   - each repository's accounts, secrets and jobs, and its Scheduler check job, which must target that repository's check job and run as `fugaro-scheduler` (anything else is refused)

   The `_Default` exclusion and the sink aren't imported: the rollback's apply deleted them, so the plan creates them again. **Not** the alert's notification channel and policy, or the budget: they have no name Fugaro can find them by, so a retry creates a second of each. Delete them by hand before retrying (`gcloud alpha monitoring policies delete`, `gcloud alpha monitoring channels delete`, `gcloud billing budgets delete`, each confirmed), or accept the duplicates.
8. If you removed the runs bucket's project-Viewer bindings, M4 doesn't need them; restore them from the snapshot if you want them back.

After `fugaro-build` is retired, a rollback also needs it re-enabled (`gcloud iam service-accounts enable`) and its bindings restored from the snapshot.

## Retiring `fugaro-build`

M4's single build account, `fugaro-build`, could read every repository's secrets and write every registry. After steps 4.5 and 4.6 have built successfully with per-repository accounts, retire it:

1. **⚠ CONFIRM** remove its accessor bindings on each secret, its `roles/artifactregistry.writer` on the legacy `fugaro` registry and its project `roles/logging.logWriter` (`gcloud … remove-iam-policy-binding`, with the arguments from your snapshot).
2. **⚠ CONFIRM** disable it: `gcloud iam service-accounts disable fugaro-build@<project>.iam.gserviceaccount.com`. This is reversible with `enable`.
3. A week later, **⚠ CONFIRM** delete it: `gcloud iam service-accounts delete fugaro-build@<project>.iam.gserviceaccount.com`.

Then the bootstrap script has no job left: it can be deleted from the code, with [gcp-bootstrap.md](gcp-bootstrap.md).

## Offboarding a repository

There is no offboarding command. **Only step 1 is implemented.** Steps 2 and 3 are an unsupported outline: no fugaro command produces a removal or destroy plan (`init --repo` always plans the whole `fugaro.yaml`), so they mean running `terraform` by hand in the workdir, outside fugaro's guard and environment allowlist, and `PrepareWorkdir` overwrites hand edits the next time `init --repo` runs. Every resource that could lose data is protected on purpose: jobs by `deletion_protection`, and secrets, the runs bucket and every registry by `prevent_destroy`, which makes a destroy plan fail at plan time until you edit the module.

1. **Lower the jobs' protection:** `fugaro init --repo --allow-job-delete`, an apply whose only change is `deletion_protection = false` on the repository's jobs (workflow jobs and the check job). The flag is a variable of that apply, and the next run without it puts the protection back.
2. **Keep the secrets.** They hold values Terraform never saw, and `prevent_destroy` would refuse to delete them. Take them out of the repository's state first, with a `removed { from = … lifecycle { destroy = false } }` block for each secret in the repository's working directory (`$XDG_STATE_HOME/fugaro/terraform/<project>/repos/<slug>`), so that Terraform forgets them and leaves them in Secret Manager. Delete them yourself later with `gcloud secrets delete` if you want them gone.
3. **Destroy the rest, by hand.** The repository's registry has `prevent_destroy`, so this needs the module edited first, and nothing checks the plan for you: read it line by line. The registry, its images, the job accounts, the build account and the check job and schedule go with it, and the runs bucket keeps the repository's `runs/`, `cache/` and `builds/` objects until their lifecycle rules delete them (`builds/` never expires: delete it by hand).

Finish with `fugaro init --repo --forget` if any state remains, and remove the repository from the local config's `repos`. Steps 2 and 3 haven't been exercised live.

## What it costs, and what it never does

| Item | Cost |
|---|---|
| APIs, service accounts, custom roles, IAM | free |
| State bucket, runs bucket, registries | storage, per GB-month |
| Log bucket (30-day retention) | Cloud Logging's usual charge beyond its free allotment |
| A daily check | about $0.004 a run; Scheduler $0.10 a month per job beyond the free ones |
| An image build | about $0.21 for a 13-minute build on `E2_HIGHCPU_8` |
| A run | Cloud Run and model usage, reported by `fugaro ls` |

`fugaro init` never enables billing, edits project-wide IAM authoritatively (every grant is an additive member resource), deletes anything you didn't name, or starts a billable build without a confirmation.

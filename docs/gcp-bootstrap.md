# GCP bootstrap (M4)

> **Superseded by [gcp-setup.md](gcp-setup.md).** `fugaro init` (M5) sets a project up with Terraform, adopting what this script made. This page and `gcp-m4.sh` are kept only as M5's rollback path (gcp-setup.md, "Rolling back": `fugaro init --forget`, then `gcp-m4.sh --apply job` with the M4 binary), until the bootstrap is retired. Don't use them to set up a new project.

**This bootstrap is throwaway.** `deploy/bootstrap/gcp-m4.sh` creates, by hand and one step at a time, the Google Cloud resources M4 needs to run Fugaro for real. M5's Terraform modules (design §8) replaced it: they adopt every resource this script makes, with the same names. Don't build on the script; build on the names, which come from `fugaro gcp job-spec` and so from the same code M5 uses.

This runbook describes each step: the command, what it creates, whether it costs money, and how to undo it. Design §3.2 covers the names, and §6.1 the IAM.

## How the script behaves

- **Dry run by default.** Without `--apply`, a step prints each command it would run, prefixed with `+`, and runs only the read-only `fugaro gcp job-spec`. Read the dry run before every `--apply`.
- **Every step that changes something confirms first.** Each prints a `⚠ CONFIRM (project <project>)` banner saying what it creates and what it costs. With `--apply` it then asks you to type the project ID, unless you pass `--yes`. When stdin is not a terminal, `--apply` needs `--yes` and does nothing without it.
- **`config` runs first, and binds everything else.** It writes the local config (design §5.4). Every other step refuses unless that file exists and names the same `PROJECT`, and the same `REGION` and `BUCKET` when they are set. `PROJECT` must be a well-formed project ID.
- **The project is always explicit.** Every `gcloud` call passes `--project`, and gcloud prompts are disabled (`CLOUDSDK_CORE_DISABLE_PROMPTS=1`), so gcloud's active project never matters.
- **The bucket is pinned.** Bucket names are global, so before any bucket write, IAM change or deletion, a read-only describe checks that the bucket belongs to `PROJECT`.
- **Steps can be rerun.** A create skips a resource that already exists (a bucket only when it is in the same project and region), and a delete skips what is already gone.
- **Ownership is checked before reuse or deletion.** An existing job must carry `fugaro=managed` and this repository's `fugaro_repo` and `fugaro_workflow` labels; a job's service account this repository's and workflow's display name (`fugaro gcp job-spec --field sa-display-name`); and a secret the labels `fugaro secrets set` gives it, `fugaro=managed`, this repository's `fugaro_repo` and its own logical name as `fugaro_secret`. Otherwise the step refuses.
- **The shared resources carry a mark too.** The script labels the bucket and the `fugaro` registry `fugaro=managed` when it creates them, and gives `fugaro-build` the display name `Fugaro image builds (M4 bootstrap)`. A bucket, registry or `fugaro-build` that exists under the same name without that mark is someone else's: `bucket`, `registry`, `build-sa`, `job-sa`, `secrets-access`, `teardown` and `teardown-all` refuse to adopt it, rewrite its lifecycle, grant on it or delete it. A bucket created before the label existed (or whose create succeeded but whose label update didn't) is refused too; if it is really the bootstrap's, **⚠ CONFIRM** label it by hand with `gcloud storage buckets update gs://<bucket> --update-labels fugaro=managed --project <project>` (and `gcloud artifacts repositories update fugaro --location <region> --update-labels fugaro=managed --project <project>` for the registry).
- **Only Bitbucket repositories.** The job spec needs a GitHub App's ID and installation, which M5's Terraform provides, so the script refuses a GitHub repository. It also doesn't grant `roles/aiplatform.user`, so a job with `agent.auth: vertex` can't reach the model; use `oauth` or `api-key`.

### Environment

| Variable | Used by | Meaning |
|---|---|---|
| `PROJECT` | every step | the GCP project ID |
| `REGION` | most steps | the region of the jobs, the bucket, the registry and Cloud Build |
| `BUCKET` | `config`, `bucket`, `job-sa`, `teardown*` | the runs bucket's name, which must start with `fugaro-runs-` (for example `fugaro-runs-<project>`): the live tests refuse any other runs bucket, and a bucket can't be renamed, so `config` and `bucket` refuse another name before anything is written |
| `REPOS` | `config` | the repositories, space-separated, each `owner/name:base_branch:workflow[:provider]` (provider `bitbucket`, the default, or `github`) |
| `BASE_IMAGE`, `FORCE` | `config` | an optional `base_image`; `FORCE=1` replaces an existing local config (the script shows the diff first) |
| `REPO`, `WORKFLOW`, `CHECKOUT` | per-repository steps | the repository (`owner/name`), its workflow, and a checkout of it, whose `fugaro.yaml` the names come from |
| `FUGARO` | per-repository steps | the `fugaro` binary (default `fugaro`); build it from the branch you are testing |
| `FUGARO_SRC`, `HEAVY` | `base` | the Fugaro checkout to build the base image from; `HEAVY` optionally wraps the Docker commands (for example a lock script that serializes Docker work); by default they run directly |

`FUGARO_CONFIG` moves the local config, for the script and for `fugaro` alike.

### Before you start

These are read-only, and yours to run:

- `gcloud auth list` shows your account, and `gcloud auth application-default login` has been done: `fugaro` uses Application Default Credentials.
- `gcloud auth application-default set-quota-project <project>` has been run, so your ADC carries a quota project.
- `gcloud billing projects describe <project>` shows `billingEnabled: true`, and a billing budget with an alert exists for the project.
- Each repository's Bitbucket repository access token (git-providers.md) is in a file of mode 600 outside any checkout. Name it when you create it, for example `Fugaro`: that name is shown as the author of the pull requests and comments Fugaro creates, and it can't be changed later.

## The steps

Run them in this order: `gcp-m4.sh STEP` for the dry run, then `gcp-m4.sh --apply STEP`.

| Step | Creates | Billable | Undo |
|---|---|---|---|
| `config` | the local config: project, region, `runs_bucket`, `registry` (`<region>-docker.pkg.dev/<project>/fugaro`), `build.service_account` (`fugaro-build@<project>.iam.gserviceaccount.com`), `user` (from `git config user.email`), `repos`, and `base_image` when `BASE_IMAGE` is set. Written with mode 600. | no, local only | delete the file |
| `apis` | enables the Run, Storage, Secret Manager, Artifact Registry, Cloud Build, Logging and IAM APIs | free to enable; billable once used | not done by any step: `gcloud services disable <api> --project <project>` |
| `bucket` | `gs://<bucket>` in `REGION`, with uniform bucket-level access, public access prevention, the label `fugaro=managed`, and the lifecycle rules (which replace the bucket's whole lifecycle configuration, so it refuses a bucket without the label): `runs/` deleted after 90 days, `cache/` 30 days after its custom time (last written or restored), and any `cache/` object 180 days after creation | storage, per GB-month | `teardown-all --all` |
| `registry` | the Artifact Registry Docker repository `fugaro` in `REGION`, labelled `fugaro=managed` | storage, per GB-month | `teardown-all --all` |
| `build-sa` | the `fugaro-build` service account (display name `Fugaro image builds (M4 bootstrap)`), with `roles/artifactregistry.writer` on the `fugaro` repository and `roles/logging.logWriter` on the project | no | `teardown-all --all` |

Then, for each repository and workflow, with `REPO`, `WORKFLOW` and `CHECKOUT` set:

| Step | Creates | Billable | Undo |
|---|---|---|---|
| `job-sa` | the job's service account (its ID from `fugaro gcp job-spec --field sa-id`, its display name naming the repository's slug and the workflow), and `roles/storage.objectUser` on the bucket with the condition that limits it to its own `runs/<slug>/`, `cache/<slug>/` and `locks/<slug>/` prefixes (design §6.1) | no | `teardown` |
| `secrets` | nothing: it prints the `fugaro secrets set` command for each secret the job mounts (see below) | — | — |
| `secrets-access` | `roles/secretmanager.secretAccessor` for the job's service account on each secret it mounts, and for `fugaro-build` on the provider token and the workflow secrets, which Cloud Build needs. It first checks that every secret exists and carries the labels `fugaro secrets set` gives it: `fugaro=managed`, this repository's `fugaro_repo`, and its logical name as `fugaro_secret`. | no | `teardown` removes the job account's bindings; `fugaro-build`'s go with the secrets (`teardown --secrets`) |
| `base` | builds the `web-node` base image from `FUGARO_SRC` with local Docker, runs `gcloud auth configure-docker <region>-docker.pkg.dev` (which edits your `~/.docker/config.json`), and pushes `<region>-docker.pkg.dev/<project>/fugaro/fugaro-web-node:dev-<commit>`. Then set `base_image` (below). **After M5 bases live in the `fugaro-base` registry** (gcp-setup.md, step 3), and this step still pushes to the legacy `fugaro` repository, which only the rollback reads. | about 1.5 GB of registry storage | delete the image, or `teardown-all --all`; remove the `credHelpers` entry from `~/.docker/config.json` by hand |
| `image` | runs `fugaro image build --repo REPO --workflow WORKFLOW --json` from `CHECKOUT`: a Cloud Build of the derived image, pushed to the registry as `:latest` | per build-minute, on `E2_HIGHCPU_8` | the registry holds it: delete the image, or `teardown-all --all` |
| `job` | the Cloud Run job (`gcloud run jobs deploy`) with the derived image's `:latest`, the job's service account, the workflow's CPU and memory, a task timeout of `timeouts.total + 2m`, `--max-retries 0`, one task, the labels `fugaro=managed`, `fugaro_repo`, `fugaro_workflow`, the env `FUGARO_BUCKET`, `FUGARO_BACKEND`, `FUGARO_PROJECT`, `FUGARO_REGION` and `FUGARO_SECRET_ENVS` (the comma list of every mounted secret's variable, which the runner registers for redaction before anything else) (plus `CLOUD_ML_REGION` and `ANTHROPIC_VERTEX_PROJECT_ID` for `agent.auth: vertex`), and the secrets mounted as env vars at their `latest` version. Rerunning it updates the job. | per execution-second; free until it runs | `teardown` |

`image` must succeed before `job`, since the job names the image. The job, service account and secret names, the labels, the env and the IAM condition all come from `fugaro gcp job-spec` (hidden, development only), so the script never re-implements the naming contract.

### Secrets

**⚠ CONFIRM** Each `fugaro secrets set` below creates or versions a billable Secret Manager secret in the project of the local config, and the CLI asks nothing first: check the project (`--project`, or the local config's) before you run it.

Secret values never pass through the script, a command line, or a conversation with an agent. Each is stored with `fugaro secrets set`, which reads the value only from stdin: a pipe, a redirect, or a hidden prompt at a terminal. It labels the secret with the repository (design §6.1), and it refuses to add a version to a secret that exists without those labels. Each version costs a few cents a month.

- **The Claude subscription token** (`agent.auth: oauth`): run `claude setup-token` in your own terminal, then, in the same terminal, `fugaro secrets set claude-oauth-token --repo <owner/name>` and paste the token at the hidden prompt. Never paste it into an agent's conversation.
- **Tokens held in files**, such as the repository access token, go through a `<` redirect, so the value never reaches argv: `fugaro secrets set bitbucket-token --repo <owner/name> < <token file>`.
- **Workflow secrets** (a workflow's `secrets:` in `fugaro.yaml`) must be declared there, and `fugaro secrets set` must run from a checkout of the repository to check that. A value that isn't secret can be piped, as in `openssl rand -hex 16 | fugaro secrets set <name> --repo <owner/name>`.
- `fugaro secrets ls --repo <owner/name>` lists what is stored: IDs, labels and version counts, never values.

A new version reaches new executions, which mount `latest`; `secrets-access` needs to run only once per secret.

### Setting the base image

After M5, base images live in `fugaro-base`, pushed by an operator as gcp-setup.md's step 3 says, and `fugaro init --base-image` records the tag. The `base` step above pushes to the legacy `fugaro` repository, and what follows sets a `base_image` that only the rollback (M4's `image build` and `job`) reads.

After `base`, put the printed tag in the local config, so cloud image builds use it:

```bash
PROJECT=<project> REGION=<region> BUCKET=<bucket> REPOS='<as before>' BASE_IMAGE=<the printed tag> FORCE=1 \
  gcp-m4.sh --apply config
```

`base` prints this command filled in with the values in its environment.

`config` rewrites the whole file, so pass the same `REPOS` as before; it shows the diff before asking.

## The development loop

To try a change to the runner (anything under `fugaro exec`) on Cloud Run:

1. **⚠ CONFIRM** Rebuild and push the base from your branch: `gcp-m4.sh --apply base` with `FUGARO_SRC` at the branch. The tag carries the commit.
2. Set `base_image` to the printed tag, as above.
3. **⚠ CONFIRM** (a billable Cloud Build) Rebuild the derived image from the repository's checkout: `fugaro image build` (or `gcp-m4.sh --apply image`). It builds `FROM` the new base, pinned by digest.
4. **⚠ CONFIRM** (a billable execution that opens a PR) Launch: `fugaro run --repo <owner/name> "<task>"`, then follow it with `fugaro logs -f <run>`, `fugaro ls` and `fugaro diagnose <run>`.

The job runs the image's `:latest` tag. If a run still starts the previous image, redeploy the job with `gcp-m4.sh --apply job`; it is safe to rerun.

A change to the CLI alone (`run`, `ls`, `logs` and so on) needs none of this: rebuild `fugaro` locally.

## Live tests

With the resources in place, the `live`-tagged tests in `internal/backend/gcp` and `internal/e2e` exercise the real backend and one sandbox run. They take their project and sandbox repository from `FUGARO_LIVE_PROJECT` and `FUGARO_LIVE_REPO`, refuse to run unless the local config names the same ones (and run in its region), and the file headers and docs/gcp-live-checklist.md give the commands. **⚠ CONFIRM** Running them launches billable executions and a Cloud Build, and opens a PR in the sandbox repository; the checklist says what each check does. The prefix-denial check impersonates the sandbox job's service account, so it needs you to hold `roles/iam.serviceAccountTokenCreator` on it for the duration. **⚠ CONFIRM** Grant it with `gcloud iam service-accounts add-iam-policy-binding <sa> --member user:<you> --role roles/iam.serviceAccountTokenCreator --project <project>`, and remove it again afterwards with `remove-iam-policy-binding` and the same arguments (the checklist's "Undo").

## Teardown

Nothing is torn down automatically; M5 takes the resources over. Both teardown steps confirm like the others.

- **`teardown`** (one repository's workflow, with `REPO`, `WORKFLOW`, `CHECKOUT`, `REGION` and `BUCKET` set) checks the job's labels, the service account's display name and the secrets' labels, then deletes the job, removes the account's conditional bucket binding and its bindings on the secrets, and deletes the account. The secrets and `fugaro-build`'s bindings on them are kept, since the repository's other workflows share them. With `--secrets` it deletes the secrets too, and refuses while another job of the repository exists.
- **`teardown-all --all`** deletes the bucket with every run and cache, the `fugaro` registry with every image, `fugaro-build`'s project binding, and `fugaro-build`. It first checks the bucket's and the registry's `fugaro=managed` label and `fugaro-build`'s display name, and refuses, deleting nothing, if any is missing. It also refuses while any Fugaro job remains in `REGION`. The APIs stay enabled.

# Dogfooding: Fugaro on its own repository

The repository's root `fugaro.yaml` points a Fugaro installation at this repository: project `fugaro`, one workflow `go` on the `go` base image. This page is the one-time setup. Everything below is from a checkout of this repository, as the person who owns the GCP project. Commands and flags were read against the code; nothing here has been run against a live project yet, so the first pass is **untested** end to end. The step-by-step behaviour of `init` (what each step plans, confirms and costs) is in [gcp-setup.md](gcp-setup.md); this page only says what is different here.

## What a run does, and what it doesn't

A run's verify step runs the workflow's `build` and `test` commands in the container: `go build`, `go vet`, a gofmt check, and `go test` without `-race` over every package except `internal/runner` and `internal/e2e`. It targets about 20 minutes on 4 vCPU and 8 GiB, which is **not measured** on Cloud Run yet (the command's package list and `timeouts.verify` are the knobs to tune after the first run).

The merge gate stays GitHub CI: `go test -race ./...`, the docker-tests job, the Firebase rules emulator (`rules`) and the Terraform job. A green run is not a green CI. The image has no Docker daemon (Cloud Run has none), no Java or Node (the rules emulator), and no tflint, so a run can't prove those; an agent changing `internal/runner`, the images or the rules should say so in the PR.

## Setup

1. **Projects.** Create the GCP project `fugaro-dev` with billing, and the Firebase project for the budget backend. `init --firebase` adopts a Firebase project that exists and is billed; it never creates one. Whether it is `fugaro-dev` itself or a separate project is your decision.
2. **Preconditions** in [gcp-setup.md](gcp-setup.md) "Preconditions" (ADC with a quota project, Terraform 1.7+, an Owner role). For the Firebase step, log in with the extra scopes in "Turning the shared budget on".
3. **The installation:**

   ```bash
   fugaro init --name fugaro --gcp-project fugaro-dev --region us-east5 --plan-only
   fugaro init --name fugaro --gcp-project fugaro-dev --region us-east5
   ```

4. **The base image into `fugaro-base`.** `fugaro-go` is published only from the first release tag after the one that adds it (v0.1.0 predates it), so until then build it from this checkout and push it, as [gcp-setup.md](gcp-setup.md) step 3 does for `web-node`:

   ```bash
   gcloud auth configure-docker us-east5-docker.pkg.dev
   tag=us-east5-docker.pkg.dev/fugaro-dev/fugaro-base/fugaro-go:dev-$(git rev-parse --short HEAD)
   sh images/build-base.sh go "$tag"
   docker push "$tag"
   fugaro init --base-image "$tag"
   ```

   After a release, the same image can be mirrored instead of built: `docker pull ghcr.io/dimipaun/fugaro-go:<version>`, `docker tag` it to the registry path, `docker push`. The base image is a single value in the project config, so this project holds only `go` workflows (`web-node` ones would build from the wrong base).
5. **The budget backend** (the history image it needs is built by hand, as in gcp-setup.md "The history image"):

   ```bash
   fugaro init --firebase <firebase-project-id> --budget-mode observe
   ```

6. **The repository.** `fugaro.yaml` is read from `main` at every run, so merge it first. Then, from the checkout:

   ```bash
   fugaro validate
   fugaro init --repo --github-app-id <id> --plan-only
   fugaro init --repo --github-app-id <id>
   ```

   The GitHub App (its ID and a private key) must exist and be installed on `dimipaun/fugaro`; see [git-providers.md](git-providers.md). Store the key with `fugaro secrets set github-app-key --repo dimipaun/fugaro < key.pem`.
7. **The Claude credential.** `agent.auth` is `oauth`: you run `claude setup-token` and then `fugaro secrets set claude-oauth-token --repo dimipaun/fugaro` at the hidden prompt, in your own terminal. The value never goes through an agent.
8. **Rerun `fugaro init --repo`** to build the first derived image (billable: it bakes the module and build caches), then once more to switch the job to it, as in gcp-setup.md "A repository".
9. **Reviewers.** `git.pr.reviewers` and `labels` in `fugaro.yaml` are empty, with a `TODO(user)` for your GitHub login. A change there is a normal PR to `main`.

## First run

```bash
fugaro run --workflow go "<task>"
```

Then `fugaro ls`, `fugaro watch`, `fugaro logs` and `fugaro diagnose`, or the plugin's `launch`, `status` and `logs` skills.

## What goes through the loop

Through Fugaro: ordinary features, fixes, refactors, tests and docs, and PR follow-ups (`fugaro run --pr N`, with `followup.trusted` set to your GitHub user ID first).

Through the subagent review loop in a local session instead, with your eyes on it: anything that handles money or security. That is the budget and policy code (`internal/budget`, `internal/policy`, `internal/gateway`, `internal/pricing`), the database rules and token minting, credential and secret handling, the runner's hardening and the image selftest, IAM and the Terraform that grants it, and `fugaro.yaml`, the workflows and `images/` themselves. A run can open a PR for these, but the review gate there is you and the full CI, not the run's own two review rounds.

## Left out of the `go` image, on purpose

- **Docker.** There is no daemon on Cloud Run, so the docker-tests job can't be a verify step.
- **Node and npm, Java.** Only the Firebase rules emulator job needs them (Node 22, npm 11.19.0, Java 21 and a downloaded emulator jar); its tests skip without the emulator, and CI runs them.
- **tflint.** The Terraform lint is a CI job. Terraform itself is in the image (1.16.4, the CI version) for `go test -tags terraform ./internal/infra/...` and `terraform validate`, which need provider downloads at run time.
- **Go JUnit reports.** `go test` writes none, so a run has no per-test results and `rerun_failed` is not configured; the verify step reports pass or fail from the exit status only. Adding `gotestsum` is a possible follow-up.

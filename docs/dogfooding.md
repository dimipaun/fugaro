# Dogfooding: Fugaro on its own repository

The repository's root `fugaro.yaml` points a Fugaro installation at this repository: project `fugaro`, one workflow `go` on the `go` base image. This page is the guide to running Fugaro on its own code, as it was done on the GCP project `fugaro-dev` from 2026-10-04: what to set up, what a run is and is not trusted with, what stays out of runs, and what the first weeks taught. The step-by-step behaviour of `init` is in [gcp-setup.md](gcp-setup.md); this page says what is different here.

**What is recorded and what is not.** The layout, the first runs and the lessons below were observed on `fugaro-dev` by its maintainer; they are not a test. The simple setup this page leads with (`fugaro init`, `/fugaro:setup`, `fugaro init`) is merged but has **not** been run from a clean project: the first install on `fugaro-dev` used the manual steps and hit the frictions that M11 removes. That clean run is the milestone's acceptance and is written up as Check 27 in [gcp-live-checklist.md](gcp-live-checklist.md), **NOT RUN**.

## The layout

- **One project holds everything.** The installation (runs bucket, registries, jobs, secrets, scheduler) and the Firebase budget backend are in the same GCP and Firebase project, `fugaro-dev`, Fugaro project name `fugaro` (the "same-project layout", design `m9-budget-and-dashboard.md` §6.0). The budget runs in `observe` mode; Firestore history is on, its location (`us-east5`) permanent.
- **Remove `roles/editor` from the default service accounts.** In this layout anything that runs as the default Compute Engine, Cloud Build or App Engine account and holds `roles/editor` can reach the budget backend, so on `fugaro-dev` the user removed it from the default Compute account by hand. `fugaro doctor` and `init` flag it (`default-service-accounts`); see gcp-setup.md, precondition 9.
- **The GitHub App** is created by the user in a browser and installed on `dimipaun/fugaro` ([git-providers.md](git-providers.md)). Its name is unique across GitHub, so the bare `Fugaro` was taken and the maintainer picked another; its ID is not a secret. It has Contents, Pull requests, Issues and Metadata, and **never the Workflows permission** (below).
- **The secrets are the user's.** The App's private key (`github-app-key`) and the Claude credential (`claude-oauth-token`, from `claude setup-token`, since `agent.auth` is `oauth`) are entered by the user in their own terminal, at the `secrets` stage's hidden prompts or with `fugaro secrets set`. No agent sees a value; `init` refuses to prompt through one.
- **Reviewers.** `git.pr.reviewers` and `labels` in `fugaro.yaml` are empty, with a `TODO(user)` for a GitHub login; changing them is a normal PR to `main`.

## Setup

From a fresh clone of the real remote, as the person who owns the GCP project, in your own terminal (typed confirmations need a real one):

1. **Create the project and link billing**, either by hand or with `--create-project` and `--link-billing` (each its own typed confirmation; neither is covered by `--yes`; not run against a real organization yet, Check 27). Enable the Cloud Billing API on your credentials' quota project (`fugaro doctor` prints the line).
2. **`fugaro init --firebase <project-id> --budget-mode observe`.** The first run asks, once, for the project's name (`fugaro`), the GCP project's ID and the region (`us-east5`). It converges the installation, the Firebase backend, the images, the history job, then the secrets (hidden prompts, in the checkout), the plugin wiring and, once `fugaro.yaml` is on the default branch, the repository. Each stage asks its own confirmation; a stage that needs you stops with one line, and a rerun resumes.
3. **The images.** `fugaro-go` and `fugaro-history` are published only from the first release tag that has them (v0.1.0 predates them), so a development build copies nothing and a release build older than that tag has nothing to copy. Build the base from this checkout and push it as gcp-setup.md step 3 does, then `fugaro init --base-image <tag>`. After a release, `fugaro init --base go` copies it with no Docker (`--expect-digest` pins it).
4. **`fugaro.yaml`.** This repository already has one. In another repository, run `/fugaro:setup` in your coding agent and merge its PR; Fugaro reads `fugaro.yaml` from the default branch.
5. **`fugaro init` again** after the merge: the `repository` stage (its first image build is billable and has its own confirmation, then the job is deployed and the daily check unpaused). It asks once for the App's ID, or take `--github-app-id`.

Then `fugaro run --workflow go "<task>"`, `fugaro ls`, `fugaro watch`, `fugaro logs`, `fugaro diagnose`, or the plugin's `working` skill.

## What a run does, and what it doesn't

A run's verify step runs the workflow's `build` and `test` commands in the container: `go build`, `go vet`, a gofmt check, and `go test` without `-race`. It leaves out four package sets, on purpose:

- **`internal/runner` and `internal/e2e`**: about 12 and 6 minutes, and their tests need a Docker daemon or a git state the container lacks.
- **`internal/image` and `images`**: the base-image builds; their tests need Docker.

It targets about 20 minutes on 4 vCPU and 8 GiB (`timeouts.verify` is 25m). The in-run verify is a smoke check, not the gate.

**CI is the merge gate.** GitHub CI runs `go test -race ./...`, the docker-tests job, the Firebase rules emulator (`rules`) and the Terraform job; `test`, `terraform` and `rules` are required checks on `main`. A green run is not a green CI. The image has no Docker daemon (Cloud Run has none), no Java or Node (the rules emulator) and no tflint, so a run can't prove those: an agent changing `internal/runner`, `internal/image`, `images/` or the rules should say so in the PR, and you read the CI before merging. A run that ends as a failed draft only because the excluded or unrunnable parts failed can still be fine: check the PR's CI, mark it ready and merge (done for two PRs during M11).

## What Fugaro can never change: `.github/workflows/*`

GitHub refuses a push from the App that creates or updates a workflow file unless the App holds the **Workflows** permission, and Fugaro's App never does, deliberately: a run able to edit CI could weaken the merge gates or reach the repository's secrets. A run that changes one anyway ends `infra_error` at finalize with its work saved but not pushed (git-providers.md, "A refused push"). Two parallel runs lost their work this way. **Rule: a task that touches `.github/` is done by a person or a local session, never by a run**, and no task text should ask for it (the agent is told not to). The same goes for `images.yml` and `release.yml`.

## Follow-ups and `followup.allow_public`

`fugaro run --pr N` continues a Fugaro PR and acts on the comments of `followup.trusted` accounts, with the workflow's secrets and the model credential in hand. On a **public** repository anyone can comment, so a follow-up is refused unless `followup.allow_public: true` is set, and even then only trusted accounts' comments are acted on. This repository is public, and its `fugaro.yaml` sets neither key, so follow-ups on its PRs are refused today: continue by starting a new run, or make that decision deliberately in a reviewed PR (a security decision: set `trusted` to the maintainers' numeric IDs and read the design's §6.1 first). The `setup` skill never sets `allow_public` without an explicit yes.

## Parallel runs and `--batch`

Each run has its own worker, branch and PR, so independent tasks run side by side: one `fugaro run` per piece, with a shared `--batch <name>` and a `--run-id` of its own so a repeated launch cannot start a piece twice; `fugaro ls --batch <name>` lists them and `--watch` follows them. What worked:

- **One owner per file.** Two runs that edit the same file give conflicting PRs. Settle shared names and interfaces first and put them in every task's text.
- **Merge in dependency order**, and launch a dependent task only after its base has merged.
- **Check headroom first**: `fugaro budget show`; `max_parallel` (20) refuses beyond that. `oauth` runs have no dollar cap, so the bound is your subscription and the day's headroom.
- **Expect to read each PR's CI**, not just the run's verify.

## What went through runs, and what did not

Through runs: the first dogfood task (a small `diagnose` fix, merged after CI), M11's skills restructure, preflight and backend seam, and other ordinary features, fixes, refactors, tests and docs, plus PR follow-ups where permitted above. The two M11 tasks that touched `.github/workflows` (the release images and the skill lint) lost their runs' work to the refused push and were redone locally.

Through the **subagent review loop in a local session**, with your eyes on it: anything that handles money or security. That is the budget and policy code (`internal/budget`, `internal/policy`, `internal/gateway`, `internal/pricing`), the database rules and token minting, credential and secret handling (the whole `secrets` stage and the mirror), the runner's hardening and the image selftest, IAM and the Terraform that grants it, project creation and billing, the skills' security text, and `fugaro.yaml`, the workflows and `images/` themselves. For M11 the converge loop, mirror, secrets stage, plugin wiring, project creation and the two critical skills each got their own review. A run can open a PR for these, but then the review gate is you and the full CI, not the run's own two review rounds.

## Lessons

- **Time bombs in tests.** Three tests passed for days and then went red on `main` and on every PR, because they hard-coded a date against a wall clock that kept moving: a PR-flow test clock fixed at 2026-10-03 against stage deadlines on the real clock (`context deadline exceeded` after about 15:00 UTC that day, #73), a budget fixture's database clock fixed at 2026-10-02 against a token minted by the wall clock (writes refused about 36 hours later, #78), and a seeded run dated 2026-09-27 that `ls` stopped showing once it was more than 7 days old (#103). Rule: a test starts from the real time (or injects one clock everywhere), and a date that appears in a fixture must follow it.
- **Recommendation, not built: a scheduled CI run with the clock moved forward.** A nightly run of the suite with the wall clock set weeks ahead would have caught all three the day they were written. How to move the clock is open: Go reads the time through the vDSO, so LD_PRELOAD tools such as libfaketime are unreliable for Go binaries (unchecked here), and a container or VM with a shifted clock, or one injected test clock, are the options. A workflow file is a person's change (above).
- **Flaky is a hint.** `TestLedgerNeverExceedsGrantedWithLease` failed "sometimes" under `-race`; it was a real gateway bug (lease top-up starvation, #81), not a flake.
- **Output.** Go emits no JUnit reports, so verify says "0 tests: no JUnit reports"; it reports pass or fail from the exit status only. `gotestsum` would add per-test results and `rerun_failed`; it is a follow-up.
- **Login expiry.** `gcloud` and `gsutil` logins expire (reauth) while you are away; the `fugaro` CLI uses its own credentials and keeps working.
- **The frictions that became M11** (design `m11-setup-and-skills.md` §6): the first-install `init --plan-only` failure, same-project refusal, the taken App name, the Cloud Billing API on the quota project, the default Compute account's `roles/editor`, the unpublished base and history images, wrapped multi-line commands, typed confirmations needing a real terminal, and the many manual rounds.
- **Resize redraw.** Resizing the terminal window during `fugaro watch` can leave a stale duplicate footer line on screen (known cosmetic redraw bug).

## Left out of the `go` image, on purpose

- **Docker.** There is no daemon on Cloud Run, so the docker-tests job can't be a verify step.
- **Node and npm, Java.** Only the Firebase rules emulator job needs them (Node 22, npm 11.19.0, Java 21 and a downloaded emulator jar); its tests skip without the emulator, and CI runs them.
- **tflint.** The Terraform lint is a CI job. Terraform itself is in the image (1.16.4, the CI version) for `go test -tags terraform ./internal/infra/...` and `terraform validate`, which need provider downloads at run time.

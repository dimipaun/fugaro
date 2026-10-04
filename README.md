# Fugaro

**Fleeting cloud workers for coding agents: one task, one container, one PR, then gone.**

*Fugax* is Latin for "fleeting." Fugaro runs long, self-contained coding-agent loops (implement → test → review → fix → compile → open a PR) in ephemeral, throwaway cloud containers instead of on your laptop. Google Cloud Run is the first supported backend; AWS and Azure are wanted next (see [Other clouds](#other-clouds-help-wanted)). Each task gets its own container, runs to completion, pushes its work, and disappears.

> **Status: pre-1.0. Expect breaking changes.** The first release is v0.1.0. It covers the runner, the git providers (GitHub and Bitbucket Cloud), the `web-node` base and derived images, the Cloud Run backend with its CLI, follow-up runs, the shared budget and the plugin skills. The design is [docs/design/v1.md](docs/design/v1.md).

## Getting started

Fugaro needs a Google Cloud project with billing (see [Requirements](#requirements)), `gcloud` signed in with Application Default Credentials, Terraform 1.7 or newer, Docker if you build images locally, and a GitHub or Bitbucket repository. Install the CLI (v0.1.0 is the first release; these channels are live from that tag):

```sh
brew install dimipaun/tap/fugaro
# or
go install github.com/dimipaun/fugaro/cmd/fugaro@latest
```

Or download an archive from [GitHub Releases](https://github.com/dimipaun/fugaro/releases) (darwin and linux, amd64 and arm64); each release has a `checksums.txt` signed with cosign ([how to verify](docs/release.md#verifying-a-release)). Then, in the repository's checkout:

1. Run `fugaro init`.
2. Open your coding agent and run `/fugaro:setup`.

This two-step flow is on `main` and ships with the release after v0.1.0: v0.1.0 does not include the converging `fugaro init` or `/fugaro:setup`, so with v0.1.0 follow [Manual setup](#manual-setup). Parts of the new flow (project creation, image mirroring, the plugin install prompt) are not yet verified against real cloud services.

`fugaro init` converges the installation (a first run names it: `--name`, `--gcp-project`, `--region`; `--create-project` and `--link-billing` create the project and link billing, each behind a confirmation you type), copies the release's base images into your registry, asks for secrets at hidden prompts in your own terminal, and wires the Fugaro plugin into the repository's `.claude/settings.json`, pinned to the release. `/fugaro:setup` reads the repository, writes `fugaro.yaml` (and a Dockerfile only when `image:` can't express the build) with you, and opens a pull request; merge it, then run `fugaro init` again to add the repository's job, first image build and schedule. Details and rollback: [docs/gcp-setup.md](docs/gcp-setup.md).

The plugin gives your agent four skills: `setup`, `working` (run, watch, diagnose, follow up), `routing` (cloud or local) and `parallelism`. Commit the `.claude/settings.json` change; teammates should be offered the plugin when they open the folder in Claude Code and trust it (not yet verified: if you are not offered it, restart Claude Code in the folder or use the commands below). To install it yourself, run `/plugin marketplace add dimipaun/fugaro`, then `/plugin install fugaro@fugaro`. `fugaro doctor` checks the pin and the rest of the setup, and `fugaro update-skills` moves the pin to your binary's release.

## Manual setup

The steps `fugaro init` and `/fugaro:setup` do for you, one by one, and what comes after.

### Stand up the installation (once per project)

```sh
fugaro init --name <project> --gcp-project <gcp-project-id> --region <region>
```

`init` shows its Terraform plan and applies it after you confirm by typing the project's name. `init` is a rerunnable converge: it stops at the first stage that fails or needs you, a rerun resumes, and one with nothing to do says `No changes`. Its `images` stage copies the release's base images (`--base go,web-node`, plus the kinds your `fugaro.yaml` names) and, with `--firebase`, the history image from ghcr.io into your own registry, verified by digest and with no Docker (`--image-source` for a fork, `--expect-digest KIND=sha256:<hex>` to pin a digest, `--replace-image KIND` to name a tag that may be replaced, still typed at a terminal; the trust anchor without a pin is the release tag in ghcr.io, see [docs/gcp-setup.md](docs/gcp-setup.md)). `--non-interactive` never prompts (applying then needs `--yes`, which covers the ordinary steps but never a secret, project creation, billing, an unlisted repository, the Firestore location, a billable first build or replacing an image tag: those are typed at a real terminal, and `init` applies nothing at all under a coding agent's environment, so run it in your own terminal), `--json` prints each stage and what is left for you, and never prompts (the secrets stage is then left to you, as `fugaro secrets set` commands); exit 0 done, 1 refused or left for you, 2 a cloud failure. Details, what it creates and how to roll back: [docs/gcp-setup.md](docs/gcp-setup.md).

### Set up a repository by hand

In the repository's checkout, write `fugaro.yaml` yourself instead of with `/fugaro:setup`, starting from `fugaro config example`, and check it with `fugaro validate` (and `fugaro image build --local` when you have Docker). Merge it, then, from the checkout:

```sh
fugaro init --repo                    # its secrets, registry, job and daily image check
fugaro init                           # in your own terminal: asks, hidden, for the secrets the repository's jobs mount
fugaro secrets set anthropic-api-key  # or by hand: value from stdin or a hidden prompt; also github-app-key, bitbucket-token, claude-oauth-token and the secrets your workflows declare
```

`fugaro init` takes those secrets at hidden prompts only in your own terminal (never with `--yes`; refused when a coding agent's environment variable is set, which stops an accident, not an agent that unsets its own variables: the real controls are the typed confirmations at a real terminal for money and permanent steps, and the skills' lint); otherwise it prints the one-line `fugaro secrets set` commands to run. `init --repo` offers the first image build (billable, confirmed separately). For a GitHub repository, pass `--github-app-id`. See [docs/gcp-setup.md](docs/gcp-setup.md) and [docs/git-providers.md](docs/git-providers.md).

### Run, watch, diagnose

```sh
fugaro run "add a --verbose flag to the export command"
fugaro ls                      # runs, newest first; --watch redraws until they settle
fugaro logs -f <run>           # <repo-slug>/<run-id>, or a bare run ID
fugaro diagnose <run>          # status, failed tests, review findings, last message, cost, PR
fugaro cancel <run>            # the runner finalizes first; the draft PR stays
```

Every run that pushed ends in a pull request, a draft one if it failed. The draft appears at the first verified push and shows the run's progress, and the configured reviewers are requested only when it becomes ready (`git.pr.early_draft`, [docs/git-providers.md](docs/git-providers.md)); unlike earlier versions, a failed, halted or cancelled run no longer notifies reviewers at all, so watch `fugaro ls` for those. To act on review comments, continue it with `fugaro run --pr N` (optionally with extra instructions as TEXT); it reads the comments of the accounts `fugaro.yaml`'s `followup.trusted` lists.

### Optional: the shared budget

With a Firebase project linked to billing, runs share project-wide caps and kill switches ([docs/gcp-setup.md](docs/gcp-setup.md#turning-the-shared-budget-on-m9b)):

```sh
fugaro init --firebase <firebase-project-id>
fugaro budget show             # caps, today's counters, kill switches
fugaro budget set ...          # after a while in observe mode; see --help
fugaro watch                   # live dashboard
fugaro report --by week        # spend history: by day, week, month, year, repo, model or person
```

`fugaro report` reads the spend history that a daily job copies into Firestore (created by `init --firebase`; its `us-east5` location is permanent), with the last days computed live and marked `(partial)`. Model dollars, notional (subscription list-price) and compute are always separate columns. It needs the Firebase project's `roles/datastore.viewer` and `roles/serviceusage.serviceUsageConsumer`; see [docs/gcp-setup.md](docs/gcp-setup.md#spend-history-and-reports-m9d).

---

## Why

Running several coding agents locally, each spawning sub-agents and each compiling and running test suites, saturates even a well-equipped machine: load averages over 100, thousands of threads, thermal throttling, and leaked build daemons piling up in the background. Everything gets slower the more you parallelize.

Fugaro moves that work to the cloud:

- **Real parallelism.** Launch as many tasks as you like; each runs in its own isolated job.
- **Automatic teardown.** Containers exit when the task ends. No stray daemons, no cleanup.
- **Pay per second.** Nothing runs, and nothing bills, between tasks.
- **Never lose work.** Every run ends in a pull request, even when it fails.

## How it works

Each task is a single, ephemeral container job execution (a Cloud Run job on the GCP backend):

1. **Start** from a pre-baked image (toolchains, warm dependency cache, a recent repo checkout).
2. **Authenticate** as a dedicated, least-privilege service account.
3. **Restore caches** (Gradle build cache, node package store) from object storage.
4. **Sync code** with `git fetch` plus a hard reset to the exact target commit, so only the delta is downloaded.
5. **Run the agent loop:** implement, write tests, review, fix, recompile, repeating review rounds as configured.
6. **Always push, always PR.**
   - Success → a PR ready for review.
   - Failure → a **draft PR** with the failure explanation, logs, and the stage reached.
7. **Write caches back**, exit, and let the platform tear everything down.

A warm run should take minutes, not the half hour a cold environment would.

## Design principles

- **Open core.** The engine in this repo is generic. Anything specific to a project lives in that project's own config file, never here.
- **No secrets in the engine or the image.** Credentials live in the cloud's secret store (Secret Manager on GCP) and are injected at runtime, scoped per job.
- **The PR is the debugging surface.** When the environment is gone, the PR still holds the diff, the reasoning, and the logs.
- **Light and simple.** Serverless container jobs, object storage, and a secret store (on GCP: Cloud Run jobs, Cloud Storage, Secret Manager). No cluster to operate and no standing infrastructure cost.
- **Cloud-neutral engine.** The runner, the agent loop, and the git providers don't know which cloud they run on; each cloud is a backend behind one interface (`internal/backend`).

## Per-repo configuration

Each consuming repository carries a small config file describing how to build and test it. The schema is **not final**; this sketch shows the intent:

```yaml
# fugaro.yaml (draft, subject to change; see docs/design/v1.md)
version: 1
project: aurora           # the Fugaro project this repository belongs to
git:
  provider: bitbucket       # or github
  base_branch: main
workflows:
  server:
    base: java-services
    commands:
      build: ./gradlew assemble
      test: ./gradlew test
    secrets:                # logical names; mapped to Secret Manager by the infra layer
      - { name: artifactory-token, env: ARTIFACTORY_TOKEN }
```

A repository can also commit cost and model policy: an optional `budget:` block (`mode`, `per_run_usd`, `allowed_models`) and `agent.max_run_tokens`. It can only tighten the ceiling the project owner sets in the project config (`fugaro init --repo`): the runner reads it from the default branch, a run's own branch can tighten it further but never loosen it, and the tightest value wins. Prices stay with the owner. `allowed_models` bounds the models `fugaro.yaml` may choose. It is enforced on every model call only while the gateway runs (`api-key` or `vertex` with the budget on); with `oauth`, or with the budget off, nothing stops the agent using another model through means a branch controls (a subagent's `model:` frontmatter, `/model`, `.claude/settings.json`), so there it is a configuration check only (accepted risk). See [docs/design/v1.md](docs/design/v1.md) §5.1 and [docs/gcp-setup.md](docs/gcp-setup.md#turning-the-model-budget-on).

With a Firebase project behind the installation (`fugaro init --firebase`), runs also share a **project-wide budget**: daily caps per repository and for the project, kill switches (`fugaro budget kill`), a live list of the runs in flight, and the notional spend of `oauth` runs. It starts in `observe` (counts, refuses nothing), you set caps from what you saw with `fugaro budget set`, and only then switch to `enforce`. The caps live in the database and only budget admins change them; a committed `budget.per_day_usd` can only tighten the repository's own day cap. A run that cannot reach the backend for three minutes halts. `fugaro watch` is the live view of it: the project and each repository against their caps, the burn rate, one row per running agent, and keys to kill or resume a repository (`k`, `r`) or the whole project (`K`, `R`); `--plain`, `--json` and `--once` print instead of drawing a screen. `fugaro ls --watch` stays for "did my runs finish". See [docs/gcp-setup.md](docs/gcp-setup.md#turning-the-shared-budget-on-m9b).

## Other models (experimental)

A run's coder can be a non-Anthropic model, routed through OpenRouter's Anthropic-compatible endpoint, while Claude Code stays the harness and Claude stays the reviewer. It goes through the same gateway, so pinning, the dollar caps and the kill switches hold. It is an opt-in experiment: `agent.models` stays Claude by default, and a cheaper run is never "more ready" than a Claude one.

The **owner** enables it, in the project's local config (`~/.config/fugaro/projects/<project>.yaml`), because it sends a repository's code to a third party. A repository's `fugaro.yaml` cannot add a provider, a URL or a price; it can only name a model the owner has opened to it.

```yaml
# local project config (owner only)
providers:
  openrouter:
    kind: anthropic-compat
    base_url: https://openrouter.ai/api
    auth: bearer
    secret: openrouter-api-key     # the secret's name; the key itself is stored with `fugaro secrets set`
    route_fee_pct: 5.5             # the account's fee, added to every charge
    models: ["deepseek/*"]
    allow_data_to: [edgeappinc/fugarosandbox, dimipaun/fugaro]   # only these repositories may send code here
model_prices:                      # the embedded deepseek row is an unverified placeholder: set the real prices
  deepseek/deepseek-v4-flash: { input_per_m: 0.14, output_per_m: 0.28, cache_read: 0.1 }   # example numbers; cache_read is a multiplier of input_per_m (default 0.1)
budget: { mode: enforce, per_run_usd: 2 }  # a provider model needs enforce
```

```yaml
# fugaro.yaml of an allowed repository
agent:
  auth: api-key                    # required: oauth and vertex are refused for a provider model
  models:
    coder: deepseek/deepseek-v4-flash
    reviewer: claude-sonnet-5-5
    background: claude-haiku-4-5   # optional: with a provider coder it defaults to the coder's model
  first_line_review: auto          # auto | on | off; auto = the coder's model reviews first when a provider serves it
```

With a provider coder, `first_line_review` makes the cheap model review (and fix) its own work before the Claude review, which alone decides readiness. A run on a repository the provider does not list, with a variant (`:free`, `:online`) or alias pin, or without `agent.auth: api-key`, is refused before any model call. The account-side prerequisites (a credit-limited key per project, no fallbacks, the data policy), the key and what `diagnose` shows are in [docs/multi-model.md](docs/multi-model.md); the design is [docs/design/m10-multi-model.md](docs/design/m10-multi-model.md).

## Launching a task

A task is a small hand-off spec: what to do, which repo, which branch, and which workflow. It can be launched directly or by your local coding agent, which builds the spec and starts the job through the `fugaro` CLI. Many tasks can run at once, independently.

Local-agent skills (`plugin/`): `/fugaro:setup` sets a repository up; `working` starts a run, lists running and recent runs, fetches logs, explains a failure and continues a Fugaro PR with a follow-up run; `routing` decides what belongs in the cloud; `parallelism` says how wide to fan out.

## Observability and guardrails

- Live logs in the cloud's logging service (Cloud Logging on GCP)
- A simple status view of running, passed, and failed tasks
- Per-job **timeouts** so runaway tasks can't burn hours
- A **billing budget alert** as a cost backstop (on GCP)

## Requirements

For the GCP backend, the only one today:

- A Google Cloud project with billing enabled and `serviceusage.googleapis.com` on; `fugaro init` enables the other APIs it needs
- `gcloud` installed and authenticated locally, with Application Default Credentials
- Terraform 1.7 or newer on `PATH`, for `fugaro init` ([docs/gcp-setup.md](docs/gcp-setup.md))
- A GitHub or Bitbucket repository to run tasks against

## Other clouds: help wanted

Only GCP is implemented. We want to run Fugaro on **AWS** (for example ECS/Fargate tasks, S3, Secrets Manager, CloudWatch) and **Azure** (for example Container Apps jobs, Blob Storage, Key Vault, Azure Monitor), and we would love help: design feedback, a backend, test accounts, or just telling us what your setup looks like.

A new cloud is a backend behind the interface in `internal/backend` plus its provisioning (today a Terraform module set driven by `fugaro init`), and the base images already run anywhere a container runs. The GCP backend is the reference, and [docs/backends.md](docs/backends.md) describes the seam, the conformance suite, and what is and isn't behind it yet. If you want to take a cloud on, please open an issue first so we can agree the shape together, and say so in the issue even if you can only review or test.

## Roadmap

- [x] Base images for web (Node) workflows (`web-node`), Go workflows (`go`) and Java workflows that need Postgres, Redis and the Firebase emulators (`java-services`)
- [x] Job entrypoint implementing the task lifecycle
- [x] Cache restore and write-back
- [x] Always-PR finish step (ready or draft)
- [x] Per-repo config schema
- [x] Launch CLI and the six local-agent skills
- [x] Status view (`fugaro ls`, `fugaro watch`)
- [x] Follow-up runs, the shared budget, GoReleaser releases and the Homebrew tap
- [x] AWS and Azure help wanted (see [Other clouds](#other-clouds-help-wanted))
- [ ] Server (JVM/Gradle) base image
- [ ] M8: Docker backend, to run a task on your own machine or any Docker host
- [ ] M9d: run history
- [ ] M9e: an early draft PR
- [ ] M10: multi-model support
- [ ] Android workflow (deferred; heavy compiles stay local for now)

Open design questions are tracked in [docs/SPEC.md](docs/SPEC.md#9-open-questions--notes-for-implementer).

## Contributing

Ideas, issues, and PRs are welcome; see [CONTRIBUTING.md](CONTRIBUTING.md). The project is young, so opening an issue to discuss a direction before a large change is appreciated. Please follow the [Code of Conduct](CODE_OF_CONDUCT.md); report vulnerabilities as [SECURITY.md](SECURITY.md) describes.

## License

[MIT](LICENSE)

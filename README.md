# Fugaro

**Fleeting cloud workers for coding agents: one task, one container, one PR, then gone.**

*Fugax* is Latin for "fleeting." Fugaro runs long, self-contained coding-agent loops (implement, test, review, fix, compile, open a pull request) in throwaway cloud containers instead of on your laptop. Each task gets its own Cloud Run job execution in your own Google Cloud project, runs Claude Code headless to completion, pushes its work, opens a pull request and disappears. Google Cloud is the first backend; AWS and Azure are wanted (see [Other clouds](#other-clouds-help-wanted)).

> **Status: pre-1.0.** Minor versions may break things. Releases and their notes are on the [Releases page](https://github.com/dimipaun/fugaro/releases); how a release is cut and verified is in [docs/release.md](docs/release.md).

## Why Fugaro

- **The agent does not run on your machine.** It runs in a throwaway container in your cloud project, so it has no access to your laptop: not your files, your shell history, your SSH keys or your other credentials. What it can reach is limited per repository and listed under [Safety and cost controls](#safety-and-cost-controls).
- **Real parallelism.** Each task is its own job execution. Several agents with sub-agents, compiles and test suites no longer fight over one laptop's CPU, memory and battery.
- **Always a pull request, which a person merges.** Every run that pushed ends in a pull request, a draft one if it failed, with the diff, the report and the logs. Fugaro never merges; a person does.
- **Cost control.** Per-run dollar or token caps, optional project-wide daily caps with kill switches, and a cost line on every run.
- **Your cloud, your model credentials.** It runs in your GCP project with your Claude subscription token, Anthropic API key or Vertex AI. There is no Fugaro service in between, and compute is billed only while a task runs.

## Getting started

You need a Google Cloud project with billing, `gcloud` signed in with Application Default Credentials, Terraform 1.7 or newer, and a GitHub or Bitbucket Cloud repository (details in [Requirements](#requirements)). Install the CLI:

```sh
brew install dimipaun/tap/fugaro                      # Homebrew cask (tested on macOS)
go install github.com/dimipaun/fugaro/cmd/fugaro@latest   # or, a development build (see below)
```

Or download an archive from [Releases](https://github.com/dimipaun/fugaro/releases) (darwin and linux, amd64 and arm64) and [verify it with cosign](docs/release.md#verifying-a-release). A `go install` build reports the version `dev`, so `fugaro init` copies no release images and pins no plugin with it: for setup, use Homebrew or an archive. Then, in the repository's checkout:

1. Run `fugaro init` in your own terminal. It sets up the cloud side behind confirmations you type, asks for secrets at hidden prompts, and wires the Fugaro plugin into `.claude/settings.json`.
2. Open your coding agent and run `/fugaro:setup`. It writes `fugaro.yaml` with you and opens a pull request; merge it, then run `fugaro init` again to add the repository's job and first image build.

Commit the `.claude/settings.json` change: a teammate who opens the folder in Claude Code and trusts it gets the plugin installed by itself, and one who holds the launcher role needs no setup of their own. Then hand it work:

```sh
fugaro run "add a --verbose flag to the export command"
fugaro ls --watch          # your runs until they settle
fugaro diagnose <run>      # what happened: tests, review findings, cost, PR
fugaro run --pr 42         # continue PR 42 from its trusted review comments
```

Or ask your agent: the plugin's `working` skill launches, watches and diagnoses runs, `routing` decides what belongs in the cloud and `parallelism` how wide to fan out. Every flag, the manual setup and the rollback: [docs/gcp-setup.md](docs/gcp-setup.md).

## How it works

```
your machine                        your Google Cloud project
------------                        -------------------------
Claude Code + fugaro plugin         Cloud Run job per repository workflow,
fugaro CLI ---- run ------------->    one execution per task, then gone
           <--- ls, logs, diagnose  Cloud Storage: tasks, run records, caches
                                    Secret Manager: each repository's secrets
                                    Cloud Build: each repository's image
                                    Firebase (optional): the shared budget
```

1. **Start** from the repository's own image: a base (`web-node`, `go` or `java-services`) plus its checkout and dependencies, rebuilt by Cloud Build when a daily check finds it stale.
2. **Sync** to the exact target commit and restore the dependency caches.
3. **Loop:** Claude Code implements, writes tests, runs the build and tests through a recording wrapper, reviews and fixes, for as many review rounds as configured.
4. **Push and open the pull request**: a draft at the first verified push, marked ready only when the run passes. Reviewers are requested only then.
5. **Write caches back and exit.** The run record and the PR remain; the container does not.

The engine is generic: everything specific to a repository lives in its `fugaro.yaml`. The design is [docs/design/v1.md](docs/design/v1.md).

## Safety and cost controls

**What a run's agent cannot reach**
- **Your machine.** Your laptop runs only the `fugaro` CLI and your own agent; the run's agent is in a Cloud Run container.
- **Other repositories.** Each workflow's job runs as its own service account, which can read only the secrets that workflow mounts and only its own repository's run, cache and lock objects. Each repository builds with its own account into its own registry.
- **Your GitHub Actions workflows.** On GitHub the App has exactly four repository permissions, Contents Read & write, Pull requests Read & write, Issues Read and Metadata Read, never Workflows, so GitHub refuses a push that changes `.github/workflows/`; each run's token is for one repository only. Name the App `<yourname>-fugaro`, install it on the repository, and accept any later permission change on the installation ([docs/git-providers.md](docs/git-providers.md)).

**What it can reach, by design**
- The repository, and the secrets its workflow mounts: the repository-scoped git token, the model credential and the workflow's declared secrets. The agent runs with permission prompts bypassed; the container is the boundary.
- **The network, without restriction.** Egress is not limited, so it reaches the model provider and the git host, but also package registries and any other host. A prompt injection in repository content or a dependency could send code or those tokens out.
- The other runs of the same repository, which share its service account.
- The repository's branches and pull requests, with its git token. Fugaro itself never merges, but **protect the base branch** (required reviews): that is what stops a push or a merge into it.

**The controls**
- **One container per task:** a job execution with no retries and a task timeout, plus stage and total timeouts.
- **Secrets are typed only at your own terminal.** `fugaro secrets set` and `fugaro init` read a value from stdin or a hidden prompt, never from an argument, and the plugin's skills never ask for, read or print one (a lint enforces it). Secrets are never baked into images or kept in Terraform state, and run logs and transcripts are redacted, best effort.
- **Typed confirmations for money and permanent steps.** `fugaro init` shows each plan before applying it; creating a project, linking billing, a secret and the first billable build are typed at a real terminal and never covered by `--yes`. It applies nothing when a coding agent's environment variable is set: a mitigation, not a barrier.
- **Spend caps.** Every stage passes Claude Code a dollar limit (`agent.max_budget_usd`, default 25). With the budget on and `api-key`, a gateway in the runner prices every model call and, in `enforce` mode, halts the run at its dollar cap; `oauth` runs can get a token cap. With a Firebase project (`fugaro init --firebase`), runs also share daily caps per repository and project, and kill switches (`fugaro budget kill`, or `k`/`K` in `fugaro watch`) halt running runs within seconds. The caps guard against mistakes and expensive tasks, not against a hostile agent. An optional GCP billing budget alert (`--budget`) is the backstop.
- **Log isolation.** Run logs carry agent output, so by default they go to Fugaro's own log bucket, which only launchers and operators (and the project's owners and editors) can read.
- **Pinned releases.** Release checksums are signed with cosign; `fugaro init` copies base images by verified digest from the release tag (`--expect-digest` pins one), and pins the plugin to the binary's release. Signing the images is planned.

The full model, with its residual risks: [design §6](docs/design/v1.md#6-security-model). Budget setup: [docs/gcp-setup.md](docs/gcp-setup.md#turning-the-model-budget-on).

## Per-repository configuration

Each repository carries a `fugaro.yaml`. `/fugaro:setup` writes it with you; `fugaro config example` prints the annotated template and `fugaro validate` checks it. A trimmed example:

```yaml
version: 1
project: aurora               # the Fugaro project this repository belongs to
gcp_project: my-gcp-project   # written by fugaro init --repo
git:
  provider: github            # github | bitbucket
  base_branch: main
agent:
  auth: oauth                 # oauth | api-key | vertex
  review_rounds: 2            # review and fix rounds before the PR is opened
  max_budget_usd: 25          # per stage
workflows:
  web:
    base: web-node            # go | java-services | web-node
    commands:
      build: npm run build
      test: npm test
    secrets:                  # logical names; values stored with fugaro secrets set
      - { name: npm-token, env: NPM_TOKEN }
    resources: { cpu: 4, memory: 8Gi }
    timeouts: { total: 90m, stage: 40m, verify: 30m, finalize_reserve: 5m }
```

`agent.models` pins a model per role (coder, reviewer, background), and an optional `budget:` block can only tighten the owner's caps ([docs/gcp-setup.md](docs/gcp-setup.md#turning-the-model-budget-on)). **Other models (experimental):** the coder can be a non-Anthropic model through OpenRouter, while Claude Code stays the harness and Claude the reviewer; the project owner opts each repository in. See [docs/multi-model.md](docs/multi-model.md).

## Requirements

- For the GCP backend, the only one today: a Google Cloud project with billing enabled and `serviceusage.googleapis.com` on; `fugaro init` enables the other APIs it needs. Setting up the installation needs the Owner role ([precondition 7](docs/gcp-setup.md#preconditions)).
- `gcloud` signed in with Application Default Credentials, and Terraform 1.7 or newer (before 2.0) on `PATH`.
- A GitHub or Bitbucket Cloud repository, and a GitHub App or a Bitbucket repository access token for it.
- A Claude credential: a subscription token (`claude setup-token`), an Anthropic API key, or Vertex AI.
- macOS or Linux (Windows through WSL2). Docker is optional, for local image builds.

## Other clouds: help wanted

Only GCP is implemented. We want to run Fugaro on **AWS** (for example ECS/Fargate tasks, S3, Secrets Manager, CloudWatch) and **Azure** (for example Container Apps jobs, Blob Storage, Key Vault, Azure Monitor), and would love help: design feedback, a backend, test accounts, or just telling us what your setup looks like.

A new cloud is a backend behind the interface in `internal/backend` plus its provisioning (today a Terraform module set driven by `fugaro init`); the runner, the agent loop and the git providers don't know which cloud they run on, and the base images run anywhere a container runs. [docs/backends.md](docs/backends.md) describes the seam and its conformance suite. Please open an issue first so we can agree on the shape, even if you can only review or test.

## Roadmap

- [x] Runner, git providers (GitHub, Bitbucket Cloud), base images (`web-node`, `go`, `java-services`), the Cloud Run backend and CLI
- [x] `fugaro init` and `/fugaro:setup`, the four plugin skills, teammates with no setup
- [x] Follow-up runs, early draft PRs, the shared budget, `fugaro watch` and `fugaro report`, other models through OpenRouter (experimental)
- [x] Releases with GoReleaser, the Homebrew tap and cosign-signed checksums
- [ ] Signed base images, verified before `fugaro init` copies them
- [ ] An egress-restricted network for runs
- [ ] A Docker-capable backend, for test suites that need Docker
- [ ] One-command offboarding of a repository
- [ ] AWS and Azure backends ([help wanted](#other-clouds-help-wanted))
- [ ] Android workflows (deferred: heavy compiles stay local for now)

Open design questions are tracked in [docs/SPEC.md](docs/SPEC.md#9-open-questions--notes-for-implementer).

## Contributing

Ideas, issues, and PRs are welcome; see [CONTRIBUTING.md](CONTRIBUTING.md). The project is young, so opening an issue to discuss a direction before a large change is appreciated. Please follow the [Code of Conduct](CODE_OF_CONDUCT.md); report vulnerabilities as [SECURITY.md](SECURITY.md) describes.

## License

[MIT](LICENSE)

# Fugaro

**Fleeting cloud workers for coding agents: one task, one container, one PR, then gone.**

*Fugax* is Latin for "fleeting." Fugaro runs long, self-contained coding-agent loops (implement → test → review → fix → compile → open a PR) in ephemeral, throwaway cloud containers instead of on your laptop. Google Cloud Run is the first supported backend; AWS and Azure are wanted next (see [Other clouds](#other-clouds-help-wanted)). Each task gets its own container, runs to completion, pushes its work, and disappears.

> **Status: pre-release, not yet on a tagged version.** The runner, the git providers (GitHub and Bitbucket Cloud), the `web-node` base and derived images, and the Cloud Run backend with its CLI (`run`, `ls`, `logs`, `diagnose`, `cancel`, `secrets`, `image build`, `image check`, `image status`) are implemented, and so are follow-up runs: `fugaro run --pr N` continues a Fugaro pull request, acting on the review comments of the accounts its base branch's `fugaro.yaml` trusts. The GCP resources come from Terraform through `fugaro init` ([docs/gcp-setup.md](docs/gcp-setup.md)), which also adopts what M4's throwaway bootstrap script made; a daily check rebuilds each repository's image when it goes stale. The remaining plugin skills and the first release come next. The design is [docs/design/v1.md](docs/design/v1.md). Expect breaking changes.

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
    base: server-jvm
    commands:
      build: ./gradlew assemble
      test: ./gradlew test
    secrets:                # logical names; mapped to Secret Manager by the infra layer
      - { name: artifactory-token, env: ARTIFACTORY_TOKEN }
```

A repository can also commit cost and model policy: an optional `budget:` block (`mode`, `per_run_usd`, `allowed_models`) and `agent.max_run_tokens`. It can only tighten the ceiling the project owner sets in the project config (`fugaro init --repo`): the runner reads it from the default branch, a run's own branch can tighten it further but never loosen it, and the tightest value wins. Prices stay with the owner. `allowed_models` bounds the models `fugaro.yaml` may choose. It is enforced on every model call only while the gateway runs (`api-key` or `vertex` with the budget on); with `oauth`, or with the budget off, nothing stops the agent using another model through means a branch controls (a subagent's `model:` frontmatter, `/model`, `.claude/settings.json`), so there it is a configuration check only (accepted risk). See [docs/design/v1.md](docs/design/v1.md) §5.1 and [docs/gcp-setup.md](docs/gcp-setup.md#turning-the-model-budget-on).

## Launching a task

A task is a small hand-off spec: what to do, which repo, which branch, and which workflow. It can be launched directly or by your local coding agent, which builds the spec and starts the job through the `fugaro` CLI. Many tasks can run at once, independently.

Local-agent skills (`plugin/`): `fugaro:onboard` sets a repository up, and `fugaro:followup` continues a Fugaro PR with a follow-up run. Planned:

- launch a task
- list running and recent tasks
- fetch logs for a run
- diagnose a failure (surface the draft PR and its logs)

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

A new cloud is a backend behind the interface in `internal/backend` plus its provisioning (today a Terraform module set driven by `fugaro init`), and the base images already run anywhere a container runs. The GCP backend is the reference. If you want to take a cloud on, please open an issue first so we can agree the shape together, and say so in the issue even if you can only review or test.

## Roadmap

- [ ] Base images for server (JVM/Gradle) and web (Node) workflows
- [ ] Job entrypoint implementing the task lifecycle
- [ ] Cache restore and write-back
- [ ] Always-PR finish step (ready or draft)
- [ ] Per-repo config schema
- [ ] Launch CLI and local-agent skills
- [ ] Status view
- [ ] Android workflow (deferred; heavy compiles stay local for now)

Open design questions are tracked in [docs/SPEC.md](docs/SPEC.md#9-open-questions--notes-for-implementer).

## Contributing

Ideas, issues, and PRs are welcome. The project is young, so opening an issue to discuss a direction before a large change is appreciated.

## License

[MIT](LICENSE)

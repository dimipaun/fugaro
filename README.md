# Fugaro

**Fleeting cloud workers for coding agents: one task, one container, one PR, then gone.**

*Fugax* is Latin for "fleeting." Fugaro runs long, self-contained coding-agent loops (implement → test → review → fix → compile → open a PR) in ephemeral Google Cloud Run jobs instead of on your laptop. Each task gets its own container, runs to completion, pushes its work, and disappears.

> **Status: early / spec stage.** The design is written ([docs/SPEC.md](docs/SPEC.md)); implementation is just starting. Expect breaking changes.

---

## Why

Running several coding agents locally, each spawning sub-agents and each compiling and running test suites, saturates even a well-equipped machine: load averages over 100, thousands of threads, thermal throttling, and leaked build daemons piling up in the background. Everything gets slower the more you parallelize.

Fugaro moves that work to the cloud:

- **Real parallelism.** Launch as many tasks as you like; each runs in its own isolated job.
- **Automatic teardown.** Containers exit when the task ends. No stray daemons, no cleanup.
- **Pay per second.** Nothing runs, and nothing bills, between tasks.
- **Never lose work.** Every run ends in a pull request, even when it fails.

## How it works

Each task is a single Cloud Run job execution:

1. **Start** from a pre-baked image (toolchains, warm dependency cache, a recent repo checkout).
2. **Authenticate** as a dedicated, least-privilege service account.
3. **Restore caches** (Gradle build cache, node package store) from Cloud Storage.
4. **Sync code** with `git fetch` plus a hard reset to the exact target commit, so only the delta is downloaded.
5. **Run the agent loop:** implement, write tests, review, fix, recompile, repeating review rounds as configured.
6. **Always push, always PR.**
   - Success → a PR ready for review.
   - Failure → a **draft PR** with the failure explanation, logs, and the stage reached.
7. **Write caches back**, exit, and let Cloud Run tear everything down.

A warm run should take minutes, not the half hour a cold environment would.

## Design principles

- **Open core.** The engine in this repo is generic. Anything specific to a project lives in that project's own config file, never here.
- **No secrets in the engine or the image.** Credentials live in GCP Secret Manager and are injected at runtime, scoped per job.
- **The PR is the debugging surface.** When the environment is gone, the PR still holds the diff, the reasoning, and the logs.
- **Light and simple.** Cloud Run jobs, Cloud Storage, and Secret Manager. No cluster to operate and no standing infrastructure cost.

## Per-repo configuration

Each consuming repository carries a small config file describing how to build and test it. The schema is **not final**; this sketch shows the intent:

```yaml
# fugaro.yaml (draft, subject to change; see docs/design/v1.md)
version: 1
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

## Launching a task

A task is a small hand-off spec: what to do, which repo, which branch, and which workflow. It can be launched directly or by your local coding agent, which builds the spec and executes the Cloud Run job via `gcloud`. Many tasks can run at once, independently.

Planned local-agent skills:

- launch a task
- list running and recent tasks
- fetch logs for a run
- diagnose a failure (surface the draft PR and its logs)

## Observability and guardrails

- Live logs in **Cloud Logging**
- A simple status view of running, passed, and failed tasks
- Per-job **timeouts** so runaway tasks can't burn hours
- A **billing budget alert** as a cost backstop

## Requirements

- A Google Cloud project with Cloud Run, Cloud Storage, and Secret Manager enabled
- `gcloud` installed and authenticated locally
- A GitHub or Bitbucket repository to run tasks against

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

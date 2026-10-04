# Contributing to Fugaro

Ideas, bug reports and PRs are welcome. For anything bigger than a small fix, please open an issue first so we can agree on the direction. By taking part you agree to follow the [Code of Conduct](CODE_OF_CONDUCT.md). Report vulnerabilities as [SECURITY.md](SECURITY.md) says, not in an issue.

## Build and test

Go is the only thing you need for the everyday loop (the version in `go.mod`):

```sh
go build ./...
gofmt -l .            # must print nothing
go vet ./...
go test -race ./...
```

Some tests sit behind build tags and need extra tools. CI type-checks all of them with `go vet -tags <tag> ./...`; run them locally when you touch that area:

| Tag | What it runs | Needs |
| --- | --- | --- |
| `terraform` | the Terraform wrapper against a real binary: `go test -tags terraform ./internal/infra/...` | Terraform 1.7+ |
| `firebase` | the budget database rules against the Realtime Database emulator: `go test -tags firebase ./internal/budget/rules/...` (see `internal/budget/rules/emulator`) | Java 21, Node 22, the pinned firebase-tools |
| `docker` | the image and gateway tests that build or run containers | Docker |
| `live` | tests against real cloud accounts; never run in CI, only type-checked | your own project |

Terraform changes: `terraform fmt -check -recursive deploy/terraform`, and `terraform validate` and `terraform test` in each root under `deploy/terraform/gcp/roots/` (`installation`, `firebase`, `repo`).

## What CI runs

`.github/workflows/ci.yml` has three jobs, and all three are required checks on pull requests:

- `test`: gofmt, `go vet` with every tag above, then `go test -race ./...`
- `terraform`: fmt, validate and `terraform test` of the three roots, tflint, a Trivy scan, and the `terraform`-tagged tests
- `rules`: the database rules against the emulator, which fails (not skips) if the emulator is missing

## Skills

The plugin's skills are in `plugin/skills` (`setup`, `working`, `routing`, `parallelism`). Try a change with `claude --plugin-dir plugin`. A CLI change that touches a documented command changes the skill that names it in the same PR: the tests in `plugin/` check every command and flag a skill gives against the CLI.

## Conventions

- **Strict decoding.** Config and wire formats are decoded strictly (unknown fields are errors), so a typo fails loudly. Keep it that way for new formats and update the JSON Schemas in `schemas/`.
- **Hermetic tests.** Tests must not touch the network, your home directory or the machine's tools beyond what a tag documents. Use the fakes (`internal/gcpfake`, `internal/testutil`) and `t.TempDir()`.
- **No real cloud in tests.** Anything that needs a real account is behind the `live` tag and is never required.
- **No project specifics in the engine.** Anything particular to one repository belongs in that repository's `fugaro.yaml`.
- **Secrets never appear** in logs, PRs, errors or fixtures.
- Update the docs (`README.md`, `docs/`) when behavior or a flag changes.

## Pull requests and commits

- Branch from `main`, keep a PR to one change, and open it against `main`. History is squash-merged, so the PR title becomes the commit subject, followed by the PR number.
- Titles are short and say what changed, often prefixed by the area or milestone, for example `rtdb: conditional PUT must not carry print=silent` or `M9c: fugaro watch, the live terminal dashboard`. Explain the why in the body.
- Fill in the PR template: tests, docs, no secrets.

## Adding a backend for another cloud

A cloud is a backend behind the interface in [`internal/backend`](internal/backend/backend.go); the runner, the agent loop and the git providers must not know which cloud they run on. [`internal/backend/gcp`](internal/backend/gcp) is the reference implementation, and its provisioning is a Terraform module set driven by `fugaro init` (`deploy/terraform/`). AWS and Azure are the most wanted. Please open an issue first (see "Other clouds" in the [README](README.md#other-clouds-help-wanted)), even if you can only review or test.

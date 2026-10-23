# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

Fugaro runs a coding agent's task (implement, test, review, fix, open a PR) as one throwaway Cloud Run job execution in the user's own GCP project. The Go CLI is the product; a Claude Code plugin, an Electron console, an Expo mobile app and an Astro website sit around it. Pre-1.0: reshape directly, no backward compatibility.

## Commands

Go version is pinned in `go.mod`; Terraform 1.9+ for the `terraform` tag.

```sh
go build ./... && gofmt -l . && go vet ./...     # gofmt must print nothing
go test ./internal/cli/ -run TestName -count=1   # one test
go test -race ./...                              # everything (slow)
go test -race -timeout 40m ./internal/runner/... # runner needs more than the 10 min default (~25 min)
go test -tags terraform ./internal/infra/...     # also: firebase, docker, live (live is never run in CI)
terraform fmt -check -recursive deploy/terraform # CI's terraform job starts here
```

Use `GOFLAGS=-p=2` on a loaded machine. Other parts, each in its own directory:

- `console/`: Node 22. `npm ci --legacy-peer-deps=false`, `npm run typecheck`, `npm run test:unit -- --maxWorkers=2`, `npm run test:e2e`.
- `website/`: `npm run check` (build plus every test, ~7 min).
- `mobile/` and `shared/`: `npm test`. Neither runs in CI until `docs/pending-workflows/shared-tests.yml` is copied in.

CI runs on pushes to `main` only, not on PRs (`.github/workflows/ci.yml`; see CONTRIBUTING.md); run it on your branch with `gh workflow run ci --ref <branch>`.

## Architecture

- `cmd/fugaro` is only `main`. The commands live in `internal/cli`, one file per command family; `internal/cli` also holds most of the docs tests (below).
- **A run, end to end.** `fugaro run` (cli) writes the task to the runs bucket and starts the job through `internal/backend` (the only seam to the compute platform; `backend/gcp` is Cloud Run). In the container `internal/runner` drives the loop: bootstrap, implement, review and fix rounds, finalize; `internal/agent` runs `claude -p` per stage; `internal/gateway` is the loopback proxy that holds the model credential and enforces the budget; `internal/gitops` and `internal/gitprov` (github, bitbucket, fake) push and open the PR; `internal/runstore` reads and writes the run record. Design: `docs/design/v1.md`.
- **Config layers.** A repository's `fugaro.yaml` (`internal/config`) is the only place for repo-specific behaviour; the engine stays generic. The project layer (`internal/localcfg` locally, `internal/projectpolicy` published) sets shared defaults, and `internal/policy` merges layers so only tightening wins. Config and wire formats decode strictly (unknown fields are errors) and have JSON Schemas in `schemas/`.
- **Cloud side.** `fugaro setup` and `fugaro update` apply Terraform through `internal/infra` (a wrapper around the `terraform` binary) using the modules and roots in `deploy/terraform/gcp`. The budget lives in a Firebase Realtime Database (`internal/budget`, `internal/rtdb`); the credentials service (`internal/creds`, `internal/credsvc`) is optional and ships dormant.
- **Images.** `images/` holds the pinned base image and its tests; each repository gets a derived image built by Cloud Build.
- **Plugin.** `plugin/skills` (`setup`, `working`, `routing`, `parallelism`) tells the user's own agent how to drive the CLI. Tests in `plugin/` check every command and flag a skill names against the CLI, so a CLI change that touches a documented command changes the skill in the same PR.

## Things that bite

- **Docs are tested.** Many `internal/cli/docs_*_test.go` and `website/test/*.mjs` tests pin sentences, anchors, counts, line citations and file lists in `docs/`, `README.md`, `SECURITY.md` and the website. Change a doc, run `go test ./internal/cli/ -run Docs` (and `npm run check` in `website/` if website content changed); keep the pinned fact, change the wording both places.
- **Never edit `.github/workflows/` from an agent run.** GitHub refuses the push. Stage the file under `docs/pending-workflows/` with its README section; a person copies it in.
- **Hermetic tests.** No network, no home directory, no real cloud: use `internal/gcpfake`, `internal/testutil` and `t.TempDir()`. Anything needing an account is behind the `live` tag.
- **No secret-shaped literals** in tests, docs, logs or fixtures.
- **Never merge `main` into a branch with a cloud follow-up run** if `main` changed `.github/workflows/`: the run's push is refused and its work is lost. Merge locally first.
- **Releases** go through `scripts/release.sh X.Y.Z` from a clean clone ([docs/release.md](docs/release.md)): the notes are `docs/releases/vX.Y.Z.md` plus a line in `website/data/release-summaries.yaml` (no backticks). The CLI, console and mobile app release independently (tags `vX.Y.Z`, `console-vX.Y.Z`, `mobile-vX.Y.Z`).

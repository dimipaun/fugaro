# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

Fugaro runs a coding agent's task (implement, test, review, fix, open a PR) as one throwaway Cloud Run job execution in the user's own GCP project. The Go CLI is the product; a Claude Code plugin, an Electron console, an Expo mobile app and an Astro website sit around it. Pre-1.0: reshape directly, no backward compatibility.

## Working principles

Don't assume, don't hide confusion, surface tradeoffs. State your assumptions; if more than one reading exists, present them instead of picking one silently; if a simpler approach exists, say so and push back; if something is unclear, stop and ask. Keep diffs small and reviewable, follow the surrounding patterns, add no new framework or dependency without asking, and don't reformat code you don't touch. When you ask the user something, use `AskUserQuestion` (structured options) rather than free text.

## Path-scoped rules

Short area rules live under `.claude/rules/` and load automatically when Claude touches matching files:

| Rule | Loads when touching |
| --- | --- |
| `.claude/rules/terraform.md` | `deploy/terraform/**`, `internal/infra/**` |
| `.claude/rules/console.md` | `console/**` |
| `.claude/rules/mobile.md` | `mobile/**`, `shared/**` |
| `.claude/rules/website.md` | `website/**` |
| `.claude/rules/credentials-service.md` | `internal/creds/**`, `internal/credsvc/**`, `SECURITY.md`, `website/content/security.md` |
| `.claude/rules/ee.md` | `ee/**` |

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

**Run targeted tests, not whole packages.** `internal/cli` and `internal/runner` each take about 25 minutes in full: name the test (`-run 'TestX|TestY'`, or the Docs filter `-run Docs` for doc changes) and run the whole package only when the change is broad. Use `GOFLAGS=-p=2` on a loaded machine. Other parts, each in its own directory:

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

- **HARD RULE: `fugaro update` is the whole upgrade, and it works perfectly.** The user is busy and is not expected to do anything more: after `brew upgrade`, the one documented command, `fugaro update`, brings the installation and the repository to the new version with no other step: Terraform, the Firebase rules, the published policy (migrated in place, never "delete and publish again"), the job images (rebuilt behind its own confirmation), the plugin pin and the local config. One missed or broken update loses the user's trust and stops their work, so a release must not ship an operator step that is another command (`fugaro upgrade --local`, `image refresh`, `policy publish --delete`), a deletion or a manual repair: if a change needs one, `update` learns to do it in the same release, and a release's `### For operators` is "run `fugaro update`" plus what it will ask. Any change that breaks an installed version (a policy or rules version, a node shape, an image contract) ships its migration inside `update`, with a test that an installation at the previous version converges. The Fugaro skill notices when an update is due (the CLI newer than the installation, the policy, the images or the plugin pin: what `doctor` already reports) and takes the user to that one command at once, with no list of steps; it never works around the refusal of cloud writes in a coding agent's session.

- **Docs are tested.** Many `internal/cli/docs_*_test.go` and `website/test/*.mjs` tests pin sentences, anchors, counts, line citations and file lists in `docs/`, `README.md`, `SECURITY.md` and the website. Change a doc, run `go test ./internal/cli/ -run Docs` (and `npm run check` in `website/` if website content changed); keep the pinned fact, change the wording both places.
- **Never edit `.github/workflows/` from an agent run.** GitHub refuses the push. Stage the file under `docs/pending-workflows/` with its README section; a person copies it in.
- **Hermetic tests.** No network, no home directory, no real cloud: use `internal/gcpfake`, `internal/testutil` and `t.TempDir()`. Anything needing an account is behind the `live` tag.
- **No secret-shaped literals** in tests, docs, logs or fixtures.
- **Never merge `main` into a branch with a cloud follow-up run** if `main` changed `.github/workflows/`: the run's push is refused and its work is lost. Merge locally first.
- **Steps that must run after a deploy** (a backfill, a config or secret change, a rollout order) go in the release notes' `### For operators` section in the same PR (`docs/releases/README.md`); without an entry the release ships and nobody runs the step.
- **A field never changes meaning.** For any wire or JSON shape a reader outside the same binary consumes (CLI `--json` output, the run record, task, policy and config formats; the console, mobile app, history and plugin read them): adding a field is fine, removing or retyping one bumps that format's version, and a field whose meaning must change gets a new name. The console accepts any newer CLI on this contract, so a reused name would draw a wrong figure with no error.
- **Comments explain why, never what.** No TODO comments, no section-header comments, and no comment that restates a name.
- **Search before you write a helper.** After parallel runs create several files, do a final pass to remove duplicates.
- **A fix round for review findings adds no functionality.** It makes only changes that keep the existing behavior. A finding that needs a new behavior or a product decision is reported, not fixed.
- **Releases** go through `scripts/release.sh X.Y.Z` from a clean clone ([docs/release.md](docs/release.md)): the notes are `docs/releases/vX.Y.Z.md` plus a line in `website/data/release-summaries.yaml` (no backticks). The CLI, console and mobile app release independently (tags `vX.Y.Z`, `console-vX.Y.Z`, `mobile-vX.Y.Z`).

# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

Fugaro runs a coding agent's task (implement, test, review, fix, open a PR) as one throwaway Cloud Run job execution in the user's own GCP project. The Go CLI is the product; a Claude Code plugin, an Electron console, an Expo mobile app and an Astro website sit around it. Pre-1.0: reshape directly, no backward compatibility.

## Working principles

Don't assume, don't hide confusion, surface tradeoffs. State your assumptions; if more than one reading exists, present them instead of picking one silently; if a simpler approach exists, say so and push back; if something is unclear, stop and ask. Keep diffs small and reviewable, follow the surrounding patterns, add no new framework or dependency without asking, and don't reformat code you don't touch. When you ask the user something, use `AskUserQuestion` (structured options) rather than free text.

**Be laconic.** Every word we write is short, direct and to the point: documentation (`SECURITY.md`, `docs/`, the website, the plugin skills), pull-request text, code comments, and above all what a command tells the user in an info line, a warning or an error. One line saying what happened and what to do beats a paragraph; no decision numbers, internal names or history in a message a person reads (they go in the log, `--json` or the doc); cut a sentence that restates another. A warning or error longer than about 140 characters is a defect to shorten, not to ship. Pinned doc sentences stay pinned: change the wording in the doc and its test together.

**Fugaro asks for nothing.** No command waits for interactive input — no y/N, no typing a name back, no "are you sure" — at the owner's terminal or in an agent's session, and no command may hang on a closed stdin. What replaces a prompt is a budget cap, a refusal printed with the fix, or a fence that keeps a raise to the owner's own session — none of them asks a question, and none of them waits. `--ask` is the whole of the exemption (opt-in per invocation, the owner's terminal only, never an agent session, never `--json`); a value only a person can type fails at once with no terminal rather than waiting. The policy and every decision under it are in `docs/no-prompt-policy/` — its spec is the owner's, and its reconciliation, which holds D1540–D1559, wins where the two disagree. **Do not add a confirmation step "for safety".** A change that seems to need one is a design decision to raise first (spec §5), never something to slip into a diff.

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
- **Cloud side.** `fugaro setup` and `fugaro update` apply Terraform through `internal/infra` (a wrapper around the `terraform` binary) using the modules and roots in `deploy/terraform/gcp`. The budget lives in a Firebase Realtime Database (`internal/budget`, `internal/rtdb`); the credentials service (`internal/creds`, `internal/credsvc`) is deployed by `fugaro update` on every installation — it is the credentials half of a `cascade` repository's model access, and a repository reaches it only once its own root is re-applied with the switch on.
- **Images.** `images/` holds the pinned base image and its tests; each repository gets a derived image built by Cloud Build.
- **Plugin.** `plugin/skills` (`setup`, `working`, `routing`, `parallelism`) tells the user's own agent how to drive the CLI. Tests in `plugin/` check every command and flag a skill names against the CLI, so a CLI change that touches a documented command changes the skill in the same PR.

## Things that bite

- **HARD RULE: `fugaro update` is the whole upgrade, and it works perfectly.** The user is busy and is not expected to do anything more: after `brew upgrade`, the one documented command, `fugaro update`, brings the installation and the repository to the new version with no other step: Terraform, the Firebase rules, the published policy (migrated in place, never "delete and publish again"), the job images (rebuilt behind its own confirmation), the plugin (installed in Claude Code at user scope, and a pin an earlier release committed to a checkout is named by `doctor` and `update`, with the line to delete) and the local config. One missed or broken update loses the user's trust and stops their work, so a release must not ship an operator step that is another command (`fugaro update-skills`, `image refresh`, `policy publish --delete`), a deletion or a manual repair: if a change needs one, `update` learns to do it in the same release, and a release's `### For operators` is "run `fugaro update`" plus what it will ask. Any change that breaks an installed version (a policy or rules version, a node shape, an image contract) ships its migration inside `update`, with a test that an installation at the previous version converges. The Fugaro skill notices when an update is due (the CLI newer than the installation, the policy, the images or the plugin pin: what `doctor` already reports) and takes the user to that one command at once, with no list of steps; it never works around the refusal of cloud writes in a coding agent's session. **And it is safe while runs are in flight, always** (the owner's requirement of 2026-11-09): the update asks no run to finish first, never refuses a policy write over one, and never rewrites what one holds — a run that bootstrapped under the release behind keeps its records, its ledger and its counters, and every write it makes still lands (`docs/design/update-in-flight.md`, step by step; `TestUpdateLeavesARunInFlightRunning` pins it). It says so in one line when runs are running — the count, and that the update waits for none of them and changes nothing they hold — and then one short line per step that can still reach one, named rather than denied: the budget mode's raise to `enforce`, which a run meets at its next lease; the policy a cascade project's every model call re-reads; and a retirement, which deletes an orphaned job with the executions on it.

- **Docs are tested.** Many `internal/cli/docs_*_test.go` and `website/test/*.mjs` tests pin sentences, anchors, counts, line citations and file lists in `docs/`, `README.md`, `SECURITY.md` and the website. Change a doc, run `go test ./internal/cli/ -run Docs` (and `npm run check` in `website/` if website content changed); keep the pinned fact, change the wording both places.
- **Never edit `.github/workflows/` from an agent run.** GitHub refuses the push. Stage the file under `docs/pending-workflows/` with its README section; a person copies it in.
- **Hermetic tests.** No network, no home directory, no real cloud: use `internal/gcpfake`, `internal/testutil` and `t.TempDir()`. Anything needing an account is behind the `live` tag.
- **No secret-shaped literals** in tests, docs, logs or fixtures.
- **Never merge `main` into a branch with a cloud follow-up run** if `main` changed `.github/workflows/`: the run's push is refused and its work is lost. Merge locally first.
- **Steps that must run after a deploy** (a backfill, a config or secret change, a rollout order) go in the release notes' `### For operators` section in the same PR (`docs/releases/README.md`); without an entry the release ships and nobody runs the step.
- **A field never changes meaning.** For any wire or JSON shape a reader outside the same binary consumes (CLI `--json` output, the run record, task, policy and config formats; the console, mobile app, history and plugin read them): adding a field is fine, removing or retyping one bumps that format's version, and a field whose meaning must change gets a new name. The console accepts any newer CLI on this contract, so a reused name would draw a wrong figure with no error.
- **Comments explain why, never what.** No TODO comments, no section-header comments, and no comment that restates a name.
- **`docs/status.md` follows the specs.** Whoever merges a plan task, or edits a spec or its plan, moves that spec's row in `docs/status.md` in the same sweep. A task's own pull request does not touch it, so concurrent tasks do not conflict on its rows. The rules for writing it are at the top of docs/status.md.
- **Pull-request words.** An open pull request is in one of four states, and a person, an agent, the skills, this file and `docs/status.md` all name them the same way: **live** (a run is going on it), **ready** (no run is going, the pull request is not a draft, and the last run that opened or followed it ended with the outcome `ready`, which the runner writes only for a pull request it really left ready — an independent review job records the pull request too, and its own outcome `reviewed` changes nothing, so a pull request a review has since read stays ready), **stalled** (it had a run, no run is going and it is not ready: the outcome `draft` — a draft, a pull request on a host with no drafts, an ordinary one it marks `[DRAFT]` in its title that can look ready, or a ready-looking one the run left a blocking finding on — or a ready one a person turned back into a draft; a follow-up run's work, or a merge as-is with the leftovers as follow-ups) and **orphan** (no run ever produced or follows it: a person's pull request, or one whose run records are gone). **Land** is an action, not a fifth state: to land a pull request is to merge it as-is, with the findings it still carries recorded as follow-up tasks (one line each, concrete enough to be a task), instead of running another fix round — never for failing tests on the final commit, an open correctness or security finding that is not a product decision, or a conflict needing real reconciliation, and never on a security path without the person's explicit word (the `landing` skill).
- **Search before you write a helper.** After parallel runs create several files, do a final pass to remove duplicates.
- **A fix round for review findings adds no functionality beyond the task's brief.** It makes only changes that keep the existing behavior beyond the task's brief. A finding that needs a behavior beyond the brief, or a product decision, is reported, not fixed.
- **Releases** go through `scripts/release.sh X.Y.Z` from a clean clone ([docs/release.md](docs/release.md)): the notes are `docs/releases/vX.Y.Z.md` plus a line in `website/data/release-summaries.yaml` (no backticks). The CLI, console and mobile app release independently (tags `vX.Y.Z`, `console-vX.Y.Z`, `mobile-vX.Y.Z`).
- **HARD RULE: every run has a title.** `fugaro watch`, `runs ls` and the console list a run by it, and a `-` there is a bug. A launch with no title is refused (`createTask`), so every stored task has one: `--title`, else the brief's `# Task:` line, else (a follow-up) the chain's own title or brief line, else (a review) `Review of PR #N`; a follow-up with none of these is refused naming `--title`. A new launch path, a daemon or a batch launcher that can end with no title is a defect; its test asserts the stored task has one.
- **A task brief is a file, and its first line is a unique `# Task: <title>`.** `fugaro watch` and the pull request title come from that line. Write the file with an editor or Write tool, never an unquoted shell heredoc: it runs any backticked text.
- **Do not pass `--total-timeout` to `fugaro run` on your own.** The repository's own `timeouts.total` is right, and a shorter flag value ends runs as "time budget exhausted"; the owner may ask for one explicitly.
- **A text-only coder model never reads image files.** The gateway refuses an image block it cannot price and the run fails; check screenshots by name, size or hash and leave re-records as an owner step in the PR body.
- **A model swap is not config-only.** A reviewer whose maximum output is under the review stage's 32000 tokens needs `agent.max_output_tokens.reviewer`, or the gateway refuses every review.
- **Cost caps live in config, not in a brief.** Claude Code's own cost figure misprices non-Anthropic models by roughly 25x, so "$5 hard cap" wording makes a DeepSeek coder stop with no commits; say the gateway enforces the cap and to stop only on its refusal.
- **Read `git status --short` and `git diff --stat` before every commit,** and `git add <paths>` rather than `-A`. On macOS use `perl -pi -e`, never `sed -i -E`: BSD sed writes `*-E` backup files that get committed.
- **After a failed conflict resolution, push nothing.** Resolve per file, then `git grep -q '^<<<<<<< '` must find nothing and the targeted tests must pass before the push; never put `gh pr merge` in the same command line as an unchecked test.
- **Launch cloud runs only while `main`'s CI is green.** CI runs on dispatch only (`gh workflow run ci --ref main`); on a red `main` every run inherits the failing tests and ends a draft, so fix `main` first.
- **Fixing a red CI: run just the broken tests locally first.** Take the failing test names from the CI log, run only those (`go test <pkg> -run 'TestX|TestY' -count=1`, plus build and vet) and see them pass before dispatching a new CI run. A dispatch is slow and costly, and a fix never run locally wastes a whole round.

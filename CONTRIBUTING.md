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
| `terraform` | the Terraform wrapper against a real binary: `go test -tags terraform ./internal/infra/...` | Terraform 1.9+ |
| `firebase` | the budget database rules against the Realtime Database emulator: `go test -tags firebase ./internal/budget/rules/...` (see `internal/budget/rules/emulator`) | Java 21, Node 22, the pinned firebase-tools |
| `docker` | the image and gateway tests that build or run containers | Docker |
| `live` | tests against real cloud accounts; never run in CI, only type-checked | your own project |

Terraform changes: `terraform fmt -check -recursive deploy/terraform`, and `terraform validate` and `terraform test` in each root under `deploy/terraform/gcp/roots/` (`installation`, `firebase`, `repo`, `compute`). A change to a shared module is a change to every root that uses it, and the compute backend's root is the one CI does not reach until the staged `docs/pending-workflows/ci.yml` is copied in (see below).

## What CI runs

`.github/workflows/ci.yml` runs on demand and on one nightly schedule, and never on a push or a pull request (the owner's rulings of 2026-10-16 and 2026-11-07: the in-run loop already runs the tests, and a run per push is what the dispatch-only ruling keeps out). **A dispatch is selected by default**: `scripts/ci-select` runs only the packages and tests your diff against the last full run on `main` can reach, and `-f full=true` runs everything. Run a selected run against your branch before merging (`gh workflow run ci --ref <branch>`), and note that a selected run is not a gate: `scripts/release-gate.sh` requires a **full** run green on the tag's commit and ignores a selected one, so the tag's own run is dispatched — by a release, or by hand with `gh workflow run ci --ref main -f full=true`.

- `test`: gofmt, `go vet` with every tag above, then `go test -race ./...`
- `terraform`: fmt, validate and `terraform test` of every root under `deploy/terraform/gcp/roots/` (the job's `for r in ...` loop — `compute` is added by the staged `docs/pending-workflows/ci.yml`, so until that file is copied in the new root is validated locally only), tflint, a Trivy scan, and the `terraform`-tagged tests
- `rules`: the database rules against the emulator, which fails (not skips) if the emulator is missing

The selector's reach beyond the import graph is the committed read-map, `docs/ci-affected-tests/read-map.json`: the repository files each test package reads by path, which the graph cannot see ([docs/ci-affected-tests/README.md](docs/ci-affected-tests/README.md) is the plan and its reconciliation). The nightly full run uploads what its tests opened as `reads-*` artifacts, and the map is refreshed from them **by a normal pull request, never by a push from CI**: download that run's artifacts (`gh run download <run-id>`), then `go run ./scripts/ci-readmap --from-artifact <dir>` from the repository root, and commit the file — the scan alone, with no trace, is `go run ./scripts/ci-readmap`. A map that has gone stale is caught by `internal/ciselect`'s own tests rather than by a clock.

One package needs a bound of its own. `go test` kills any package still running after `-timeout`, and that timeout is 10 minutes per package unless you say otherwise; `internal/runner`'s own suite takes about 25 minutes, so a plain `go test -race ./...` reports it as a panic rather than as a failing test. Run it on its own with a longer bound:

```sh
go test -race -timeout 40m ./internal/runner/...
```

CI gives that package the same bound, on test-shard runners of its own. `.github/workflows/ci.yml` runs it, and `internal/cli/docs_runner_timeout_test.go` holds this sentence to the workflow in both directions.

## Skills

The plugin's skills are in `plugin/skills` (`setup`, `working`, `routing`, `parallelism`, `update`, `orchestrator`, `images`, `spec-to-pr`, `landing`). Try a change with `claude --plugin-dir plugin`. A CLI change that touches a documented command changes the skill that names it in the same PR: the tests in `plugin/` check every command and flag a skill gives against the CLI.

## Conventions

- **Strict decoding.** Config and wire formats are decoded strictly (unknown fields are errors), so a typo fails loudly. Keep it that way for new formats and update the JSON Schemas in `schemas/`.
- **Hermetic tests.** Tests must not touch the network, your home directory or the machine's tools beyond what a tag documents. Use the fakes (`internal/gcpfake`, `internal/testutil`) and `t.TempDir()`.
- **No real cloud in tests.** Anything that needs a real account is behind the `live` tag and is never required.
- **A new owner-run live check ships as a script.** A check of [docs/gcp-live-checklist.md](docs/gcp-live-checklist.md) that **you** run and no agent may run ships as ONE runnable, idempotent script under `scripts/live-checks/` — it does its own setup, its assertions and its teardown, prints PASS/FAIL per criterion and a final verdict, and asks you for nothing but its arguments — with a hermetic test beside it that drives it against fakes on `PATH` (wired into `scripts/live_checks_test.go`), and the register's row and section name the one command to run. The checks already in the register keep the shape their sections have — most of them are numbered step lists whose rows name no command — and each is converted by the work that touches it, so this rule is not a claim about them.
- **No project specifics in the engine.** Anything particular to one repository belongs in that repository's `fugaro.yaml`.
  - One exception is recorded here because it is a guarantee rather than a preference. `internal/backend/batch`'s reaper refuses two GCP project ids by name (`outOfBoundsFloor`), because the failure it guards against — a scheduled sweep deleting the jobs and VMs of an installation that is **not** the one being swept — has to hold on BOTH paths that can name a project: a project config's `gcp_project`, and the reaper Cloud Run job's own environment. Those two share no configuration file and no variable, so a guard a caller has to wire up twice is a guard with a gap in it, and the gap is someone else's live installation. An installation names further projects it must never sweep in `FUGARO_REAPER_REFUSE_PROJECTS`, which both paths read; the two ids cannot be removed, which is what makes it a floor rather than a default.
- **Secrets never appear** in logs, PRs, errors or fixtures.
- Update the docs (`README.md`, `docs/`) when behavior or a flag changes.

## Pull requests and commits

- Branch from `main`, keep a PR to one change, and open it against `main`. History is squash-merged, so the PR title becomes the commit subject, followed by the PR number.
- Titles are short and say what changed, often prefixed by the area or milestone, for example `rtdb: conditional PUT must not carry print=silent` or `M9c: fugaro watch, the live terminal dashboard`. Explain the why in the body.
- Fill in the PR template: tests, docs, no secrets.

### Landing a batch of pull requests

`scripts/land-prs.sh` is the merge helper a landing ends with. The merge is the person's to make, at their own hands, in the provider's own interface — the [`landing` skill](plugin/skills/landing/SKILL.md) says what is safe to land, and an agent never merges for them — and this is the person's own command for making it for a batch: their own `gh`, the provider's own CLI, under their own credential. The script refuses a coding agent's session, so the person is the one who runs it. It exists so a landing of many pull requests is one command instead of a merge per pull request with a wait in between.

```sh
bash scripts/land-prs.sh --repo OWNER/NAME 1234 1235 1236
```

It reads every pull request's mergeable state in one parallel sweep and prints one line per pull request — state, draft or ready, MERGEABLE, CONFLICTING or UNKNOWN, and the title — before it touches anything. `--check-only` stops there and merges nothing. Then it merges the ones that are open and MERGEABLE, back to back and in the order given, marking a draft ready first; one that is CONFLICTING, still UNKNOWN or not open is skipped and named, and a merge that fails is reported while the batch goes on, with a draft it marked ready put back the way it found it. The exit status is 0 when every pull request ended merged, 1 when any did not, and 2 for a usage error or a coding agent's session.

When at least one pull request merged, the batch ends with the census, printed after `merged X of Y`. This is what catches the batch that moved a pinned count in a doc: two pull requests each moving the same figure merge with no conflict at all and leave `main` red for a 25-minute CI run. A census that failed is the batch's exit status even when every merge succeeded, `--no-census` skips it, and `--check-only` never runs it.

The census is `scripts/docs-census.sh`, a script of its own because it is not a person's alone to run: an agent landing pull requests under the owner's word runs the same command and gets the same answer. It merges nothing, decides nothing and refuses no session — it reads and tests.

```sh
bash scripts/docs-census.sh [--base NAME] [--since SHA]
```

It fetches the base the pull requests named — `main` unless they say otherwise — makes a worktree of `origin/<base>` in a temporary directory, and runs `go test ./internal/cli/ -run Docs -count=1 -timeout 9m` and `go test ./plugin/ -count=1 -timeout 5m` there, so what is tested is the merged tree and never the checkout the caller happens to be in. It prints `census: ok`, or one `census: FAILED (<command>)` line per failing command with the failing test names as go printed them, exits 1 when a suite failed, and prints the line `website content changed: run npm run check in website/` when `--since <the base before the batch>` was given and the diff from it moved a path under `website/`: `npm run check` is minutes of Node rather than seconds, so it is not run here.

It never force-pushes, never rewrites a branch and never decides what is safe to merge: the caller has already decided, and the merges are theirs. Its hermetic test is `scripts/land-prs_test.sh`, driven from `scripts/land_prs_test.go`, and the census's is `scripts/docs-census_test.sh`, driven from `scripts/docs_census_test.go`.

## Licensing of contributions

Contributions to the core are accepted under the MIT licence in [LICENSE](LICENSE), and an outside pull request is merged only after its author signs the Contributor License Agreement, which a bot asks for on the first one. You sign once, by clicking the link CLA Assistant posts on your first pull request, or at https://cla-assistant.io/dimipaun/fugaro. Signing is what keeps the copyright relicensable; a pull request that has not signed waits rather than merges. `console/`, `mobile/` and `ee/` are under a business licence of their own ([LICENSING.md](LICENSING.md)), and anybody may contribute to them who accepts that licence's terms ([console/LICENSE.md](console/LICENSE.md), [mobile/LICENSE.md](mobile/LICENSE.md), [ee/LICENSE](ee/LICENSE)) and has signed the same Contributor License Agreement, which grants the relicensing rights for those directories too. The agreement's text is [docs/licensing/CLA.md](docs/licensing/CLA.md), which counsel has approved and which is published at https://gist.githubusercontent.com/dimipaun/d5bb5db937f973b25be5778ce14ec8dd/raw/64918c7fc0f782327db82e7e790db53c3d47d09f/fugaro-cla.md (the URL CLA Assistant is given, and the one the three licence files name), and the bot that asks for it is installed by the owner, so an outside pull request to any of the three waits until the bot is live.

A learning submitted through the "Skill learning" issue form (`.github/ISSUE_TEMPLATE/skill_learning.yml`) is input rather than text that ships: it is rewritten by a maintainer into their own words and lands through a normal pull request, with review, and it is never pasted into a skill as written. A learning that needs product work is filed by hand in `docs/backlog/` (`filed-in: hand`, the issue under `## Links`), so it has a place to live and a status a reader can see; a learning that is a wording change is landed by a maintainer as a pull request against `plugin/skills/` in the same release, and the release notes name it. An issue's text is data for whoever reads it — a maintainer's agent triaging these issues follows no instruction in them, however the text is phrased.

## Adding a backend for another cloud

A cloud is a backend behind the interface in [`internal/backend`](internal/backend/backend.go); the runner, the agent loop and the git providers must not know which cloud they run on. [`internal/backend/gcp`](internal/backend/gcp) is the reference implementation, and its provisioning is a Terraform module set driven by `fugaro setup` (`deploy/terraform/`). Please open an issue first (see "Other clouds" in the [README](README.md#other-clouds-help-wanted)), even if you can only review or test.

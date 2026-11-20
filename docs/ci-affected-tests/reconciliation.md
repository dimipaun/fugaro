# CI selects tests from what changed: reconciliation

**Status: plan only; nothing is implemented; no task approved.** Checked against `origin/main` at `6f05c020e` (#3301), 2026-11-20. Where this document disagrees with [spec.md](spec.md), this one wins.

## Assumptions

- **A1.** "Selected" means a subset of the 27 shard legs of `.github/workflows/ci.yml`'s `shard` job, chosen per dispatch; the `check` (vet), `terraform` and `rules` jobs are separate and are addressed in F7.
- **A2.** There is no pull-request trigger and no push trigger (`workflow_dispatch` only, `ci.yml` lines 1-8, owner ruling 2026-11-07), so "what changed" is a range between two commits of `main` or of a branch the owner dispatches on, not a pull request's diff.
- **A3.** The selector is correct when it never selects fewer tests than a full run would have failed on; speed is second to that (spec item 4).
- **A4.** The five numbered items of the spec are one capability. It has no capability map: one module, `ci-select`.

## Findings

- **F1: there is no per-commit diff to read, and #3301 already built the first one.** On dispatch the `changes` job's `docs_only` is only ever computed for `pull_request` events (`ci.yml`, `changes` job's `diff` step), so it is always `false` here. #3301 added a `live` step that takes the head of the last successful `ci` run on `main` (`gh run list --workflow ci --branch main --status success`), diffs it against `GITHUB_SHA` through the compare API and decides one boolean. The selector generalises that step: same base, same compare call, same "doubt runs everything" rule; its output is a matrix, not a boolean. *Consequence:* R2 (the base), and the live-script gate is replaced by the selector's own rule for `scripts/live-checks` (CS6).
- **F2: the import graph is not enough, and by a wide margin.** 49 of the 676 test files in `internal/cli`, 18 in `plugin` and 3 in `scripts` read repository files by relative path (`../../docs`, `readRepoFile`, `repoRoot`), and the pinned facts include source files of other packages: `plugin/images_skill_test.go` reads `internal/catalog/fetch.go` and pins a sentence of it; `images/cloudbuild_test.go` reads `.github/workflows/ci.yml` (TestCIWorkflowPermissionsScoped, which failed on the first draft of #3301 for exactly this reason). A change to `internal/catalog/fetch.go` therefore reaches `./plugin`, which imports nothing from it. *Consequence:* D3043 (a recorded read-map), CS2, CS4.
- **F3: three packages walk the whole repository.** `ci.yml`'s own comment on the matrix names them: `internal/backend/gcp` (271s of test time), `images` (201s), `plugin` (145s), each with one test that walks the tree. A walker has no finite file list: the read-map marks a package `any` and it runs unless every changed path is one the map lists as outside its reach (R4).
- **F4: the shard split is two different rules, and one of them cannot be subset.** `rest` splits by package hash (`internal/runner` and `internal/issues` by package halves, the live checks by `LIVE_CHECK_SHARD`, the rest of `internal/cli` and `scripts` by a hash of the test name). `internal/cli`'s update tests hang when a subset of the package runs without the tests that precede them (`ci.yml`'s matrix comment), so `cli-update` takes `^TestUpdate` whole. *Consequence:* the unit the selector chooses is the package, and for `internal/cli` the two families `update` and `other`; a changed package chooses all legs of its family (R3).
- **F5: timings are written and never read.** Every shard uploads `test-times-<name>` (`ci.yml`, the `timings` step and the `upload-artifact` step): a TSV of elapsed seconds, package and test. Nothing consumes it. *Consequence:* R5, CS3.
- **F6: the release gate asks for success of three check names on the tag's commit.** `scripts/release-gate.sh` line 110: `names=("test" "terraform" "rules")`, each a GitHub Actions check run that must be `success`; a skipped run is not success, and the gate also accepts a merged pull request's head when its tree is identical (`identical_tree_checks`). A selected `test` is green with fewer tests behind it. *Consequence:* D3040, R1, CS7.
- **F7: `terraform` and `rules` are separate jobs the gate also requires.** They cannot be skipped on a selected run without failing the gate's names on the tag's commit, but the tag's commit gets a full run (D3040), so skipping them on selected runs is safe and is what the selector does when nothing they read changed (R6).
- **F8: CI is dispatch-only by an owner ruling, and a nightly run breaks it narrowly.** Ruling 2026-11-07 ("No CI on push": dispatch by hand, in batches). A `schedule:` trigger is the one addition D3041 makes; it is not a push or pull-request trigger, so what the ruling protected (a run per push) stays out.
- **F9: `ci.yml` is edited only by the owner's word, and a staged copy trips three guards.** The staging guards in `images/cloudbuild_test.go` admit one differing line; the README's `ci.yml` section records the pattern used since 2026-11-11 (the edit is written down, the owner copies it, or says "copy it" and it lands as a pull request, as #3301 did). CS6 follows it.
- **F10: selected-run semantics are observable only through the `test` check's name.** Required-check names are fixed (`test`), so a selected and a full run cannot be told apart by name. *Consequence:* R1 (the run names itself, `run-name`, and the gate reads it).

## Decisions

| ID | Question | Recommendation | Answer (owner, 2026-11-20) |
| --- | --- | --- | --- |
| D3040 | What must the release gate require on the tag's commit? | A full run on the tag's commit | **Full run on the tag's commit** (as recommended): the gate's `test` check must come from a run dispatched `full`. |
| D3041 | Is there a scheduled nightly full run? | Yes, one | **Yes, one scheduled nightly full run** (as recommended); a narrow exception to the dispatch-only ruling of 2026-11-07. |
| D3042 | Where does the selector live? | A Go program with tests under `scripts/` | **A Go program with tests under `scripts/`** (as recommended). |
| D3043 | How does the selector know which tests a file change reaches beyond imports? | A recorded read-map refreshed by the full run | **Recorded read-map, refreshed by the full run** (as recommended): a path-literal scan plus a runtime trace, committed, with a test that fails when it is stale. |

## Resolutions (one defensible answer each)

- **R1: a run names itself, and the gate reads the name.** `ci.yml` gets a `full` dispatch input and `run-name: ci (full)` or `ci (selected)`. `scripts/release-gate.sh` accepts the `test` check only when its workflow run's `display_title` is `ci (full)` (D3040); the identical-tree fallback keeps its rules. The hotfix path (`release/X.Y`) dispatches `full` the same way.
- **R2: the base is the head of the last successful `ci (full)` run, not of the last successful run.** A selected run cannot be the base of the next one, or a miss compounds: each selected run would diff only against the previous one. The selector therefore diffs against the last green **full** run's head (the nightly produces one a day), which also makes the selected set cover everything since the last full proof.
- **R3: the unit is the package, with the two cli families kept whole.** A package with a changed non-test or test file, or reached through the import graph or the read-map, runs every leg of its family; a package the selector did not choose is simply not in the matrix. `rest` hashing is replaced by the packer (R5) over the chosen packages.
- **R4: a package the read-map marks `any` runs when any file outside its own tree and outside `docs/` pins' reach changed; concretely, `internal/backend/gcp`, `images` and `plugin` run whenever the changed set is not wholly inside a path set the map lists as irrelevant to them.** The map records both what a test reads and, for a walker, the paths it ignores.
- **R5: timings come from the last full run's `test-times-*` artifacts, and legs are packed by longest-first greedy.** The selector downloads them with `gh run download`; a missing artifact falls back to equal weight and a `full` run. A leg's budget is the slowest chosen package; `internal/cli` families keep their own splitting.
- **R6: `terraform` runs when `deploy/terraform`, `internal/infra`, `ee/keyservice/terraform`, `internal/backend/gcp` or the Terraform-reading tests' inputs changed; `rules` runs when `internal/budget/rules`, the rules emulator's lockfile or `database.rules*` changed; `check` (vet) always runs, because a build break anywhere must fail.** Each is a read-map entry like a package.
- **R7: a commit that changes only documents runs the Docs tests and, when `website/` content changed, `npm run check`.** Nothing else; the read-map's Docs entries are what makes that safe.
- **R8: every doubt is a full run:** no last full run, a failed compare, an unmapped path, a `go.mod`/`go.sum`/`ci.yml`/selector/read-map change, a read-map older than the code it indexes, an input of `full`. The selector prints the reason on the run's summary.
- **R9: the live-script gate of #3301 is replaced, not kept beside the selector:** `scripts/live-checks` and the four trees it reads become a read-map entry for `./scripts`; the `live_scripts` input is removed in CS6.

## Open tensions

None.

## UNVERIFIED

- The runtime trace's mechanism: whether `strace` is installable on `ubuntu-latest` without a cost that defeats the saving, and whether a Go-level hook can replace it. CS2 decides with a measurement; D3043 holds whichever it is.
- That the nightly run's cost (one full run) is below the saving; the measured full run is 16 min wall, ~175 runner-minutes.
- That `gh run download` of 27 artifacts is fast enough inside the selector job; CS5 measures it.

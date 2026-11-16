# T11 verification A: the console's tests after #2633

**Status: verification. No console code changed, because no console test failed.** #2633
(`fe0d59d58`, *"cli: --yes and --non-interactive are gone; …; skills, docs and console follow
(T11)"*) merged from run a302, whose implement stage timed out, so its console half never ran. Run
on **2026-11-15**, on `fugaro/20261115-051000-ee96` (base `bb5e5f7f`), and no commit touched
`console/` between `fe0d59d58` and that base (`git log fe0d59d58..HEAD -- console/` is empty), so
what follows ran against exactly what #2633 left.

Node is **not** in this repository's `go` catalog image (it installs only the tools of `mise.toml`),
so the pinned runtime went in first with `mise use --global node@22.23.3` — the version
`console/package.json`'s `engines.node` requires, which `console/.npmrc`'s `engine-strict=true`
enforces (22.23.5 is refused with `EBADENGINE`).

| Command | Last line |
| --- | --- |
| `npm ci --ignore-scripts --no-audit --no-fund --legacy-peer-deps=false` | `added 358 packages in 5s` |
| `npm run typecheck` (`tsc --noEmit`) | no diagnostics |
| `npm run test:unit -- --maxWorkers=2` | `Test Files  115 passed (115)` — `Tests  4137 passed \| 3 skipped (4140)`, `Duration 47.12s` |
| `go test ./internal/cli/ -run Docs -count=1` | `ok  github.com/dimipaun/fugaro/internal/cli  7.994s` |

**The console's tests all pass; nothing needed fixing.** Neither `--yes` nor `--non-interactive`
appears anywhere under `console/` (`grep -rn -- '--yes\|--non-interactive' console/src console/test
console/docs` finds no line), and `SPAWN_FLAGS` and `SETTINGS_EDIT_PHASE_WORDS` in
`console/src/main/spawn.ts` carry exactly what #2633 gave them. The removed flags stay removed and
the console gained no way to confirm what the CLI no longer asks.

**`npm run test:e2e` was not run** — it needs a window and this container has no display — so its
one file #2633 touched, `console/test/e2e/projects.spec.ts`, is unverified here.

**Nine `internal/cli` tests fail on this base, before this task's change** — `fugaro verify test`
reports them, and its Go half is red for that reason alone. They are #2633's CLI-side fallout, each
stopping at the *"typing the confirmation for plugin needs a real terminal"* or *"not confirmed
(--json confirms no step)"* refusal a run with no terminal now meets — and not the console tree's.
All nine reproduce with this note's file moved out of the tree, so they are left unfixed here (the
CLI side's verification is a separate task's): `TestUpdateJSONUnchanged`,
`TestUpdateIsTotalOverItsOwnStageSet`, `TestUpdateJSONDoesNotReachTheCatalogRebuild`, `TestUpdateRebuildsTheStaleCatalogImageOfThisCheckout`, `TestUpdateNamesTheStaleCatalogImageOfAnotherCheckout`, `TestUpdateWithACapAndNoMemberRunsOn`, `TestUpdateSaysNothingAboutACatalogOnThisReleaseBase`, `TestARefusalAfterAFailureStillSaysHowToUnblockIt`, `TestUpdateSkipsAWorkspaceDatabaseThisInstallationDoesNotOwn`.

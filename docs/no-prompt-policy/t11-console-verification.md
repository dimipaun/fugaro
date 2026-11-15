# T11 verification A: the console's tests after #2633

**Status: verification. Docs only: no code changed, because nothing failed.** #2633
(`fe0d59d58`, *"cli: --yes and --non-interactive are gone; --ask stays; skills, docs and
console follow (T11)"*) merged from a run whose implement stage timed out, so its console half
was never executed. Run on **2026-11-15**, on `fugaro/20261115-051000-ee96` (base `bb5e5f7f`).
No commit has touched `console/` between `fe0d59d58` and that base — `git log --oneline
fe0d59d58..HEAD -- console/` is empty — so what follows ran against exactly what #2633 left.

## Commands and their last lines

Node is **not** in this repository's `go` catalog image (`images/catalog/go/Dockerfile` installs
only the tools of `mise.toml`), so the pinned runtime was installed first with
`mise use --global node@22.23.3` — the exact version `console/package.json`'s `engines.node`
requires, which `console/.npmrc`'s `engine-strict=true` enforces (22.23.5 is refused with
`EBADENGINE`).

| Command | Last line |
| --- | --- |
| `npm ci --ignore-scripts --no-audit --no-fund --legacy-peer-deps=false` | `added 358 packages in 5s` |
| `npm run typecheck` (`tsc --noEmit`) | no diagnostics |
| `npm run test:unit -- --maxWorkers=2` | `Test Files  115 passed (115)` — `Tests  4137 passed \| 3 skipped (4140)`, `Duration 47.12s` |
| `go test ./internal/cli/ -run Docs -count=1` | `ok  github.com/dimipaun/fugaro/internal/cli  7.994s` |

## Outcome

**Everything passes; nothing needed fixing.** No test in the tree still hands a stub CLI `--yes`
or `--non-interactive` (`grep -rn -- '--yes\|--non-interactive' console/src console/test
console/docs` finds neither word), the phase tables in `console/src/main/spawn.ts` carry the empty
word lists #2633 gave them, and typecheck accepts the tree as it stands. The removed flags stay
removed and the console gained no way to confirm what the CLI no longer asks.

**`npm run test:e2e` was not run**: it needs a window and this container has no display. Its one
file #2633 touched, `console/test/e2e/projects.spec.ts`, is therefore unverified here.

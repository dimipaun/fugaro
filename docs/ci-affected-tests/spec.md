# CI selects tests from what changed

**Owner:** Dimi Paun. **Date:** 2026-11-20. Written by the coordinator from the owner's direction of 2026-11-20 ("we need to be more precise with what tests we run"); the owner approved the direction and asked for it as a spec and plan.

CI is dispatch-only and runs the whole suite (27 shards, ~175 runner-minutes, 16 min wall at 17 concurrent runners, measured on runs 44738180473 and 44743260788); as the code grows every commit pays for everything, and resharding alone cannot go under ~10 min.

Direction:

1. Affected packages only: a script maps changed files to the packages that import them directly or transitively (reverse `go list -deps`) and runs only those packages' tests, sharded by historical timings (the `test-times-*` artifacts already record them).
2. Non-Go inputs map to explicit test sets because the import graph misses them: `docs/`, `README.md`, `SECURITY.md` and website content to the Docs tests in `internal/cli` (and website `npm run check`), testdata/embedded files/schemas to their owning packages, plugin skills to `./plugin`, `scripts/live-checks` to the live-script runners (what #3301 already does by hand: `scripts/`, `internal/infra`, `internal/procgroup`, `internal/testutil`, `go.mod`/`go.sum`, `ci.yml`).
3. A full run stays as the backstop: the release gate, `go.mod`/`go.sum` or `ci.yml` changes, and a nightly run.
4. When the selector is unsure it runs everything; it never fails open.
5. A test checks every Docs pin and every embed against the map so the map cannot rot.

Context already landed: #3301 (matrix longest-first, scripts-live gated on what it reads, `live_scripts: always` input). Measured result: GitHub does not honour matrix order and scripts-live-1 (11m40) is the long pole.

Constraints: `ci.yml` is only edited by the owner's word (never from an agent run); docs are tested; the release gate (`scripts/release-gate.sh`) requires `test` green on the tag's commit and must keep meaning that.

Open decisions: what the release gate requires (full run on the tag's commit vs selected run plus a recent full run), whether and when the nightly full run is scheduled (CI is dispatch-only by owner ruling 2026-11-07, so a schedule is a ruling to ask), where the selector lives (`scripts/` with Go tests vs inline in `ci.yml`), how timings feed the shard balance, and the behaviour for a commit that changes only docs.

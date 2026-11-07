# Implementation plan: the compatibility matrix page

Builds [spec.md](spec.md) **as amended by [reconciliation.md](reconciliation.md)**. **Nothing is
implemented and no task is approved**: each task needs the owner's explicit go (an answer to a
decision is not that go). The owner's answers that shaped the plan (D1527's Vertex answer is amended after review and awaits the owner's confirmation): D1520 a "Built, not enabled"
status; D1522 the editions are "Core (MIT)" and "Subscription"; D1537 a data file, one page and an
evidence test; D1538 an "as of" stamp refreshed by the release procedure; every row's wording is
the reconciliation's findings F1–F31.

## Conventions every task follows

- `.claude/rules/website.md` and `website/README.md`, "Everything the site would say that the
  product cannot back today is held back": the data file is the one place a claim is written, as
  `website/data/mobile-claims.yaml` is for the phone page; the page renders it verbatim.
- Docs are tested: run `npm run check` in `website/` (about 7 minutes) for any website change and
  `go test ./internal/cli -run Docs` for any change to `docs/`, `README.md` or a skill; keep the
  pinned fact, change the wording in both places.
- No secret-shaped literal; no live action; no `.github/workflows` edit. The page contains no price.
- A website release is part of the release procedure and the owner's go on the release covers it
  (the required reviewer was removed from the GitHub environments on 2026-11-07).

## Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| Statuses are a closed set: `supported`, `coming-soon`, `built-not-enabled`, `roadmap`, `not-planned` | CM1 (a decoder that refuses any other value) |
| Editions are a closed set: `core`, `subscription` (never "CE" or "EE") | CM1 |
| Every `supported` and `built-not-enabled` row names an evidence path that exists in the repository | CM1 |
| The page never claims a feature the reconciliation marked Wrong: no mobile web, no gen 1, no Enterprise SSO, no tracker MCP inside a run, no "any model" | CM2's rendered-page test, which reads the forbidden phrases from the data file's own `never_say` list |
| The "as of" stamp equals the latest `docs/releases/vX.Y.Z.md` | CM3 |
| The Claude terms caveat is the Terms page's own sentence and no other | CM1 (the row's note must equal the sentence read from `website/content/subscription-terms.md`) |

## Slice A: the data and the page (inert until the website promote)

### CM1. The data file and its test
- **Files:** `website/data/compatibility.yaml` (new), `website/test/compatibility.mjs` (new), `website/README.md` (one line naming the data file, as it names `data/mobile-claims.yaml`; its structure is pinned by the site's readme test).
- **Interfaces:** produces the file the page reads. Rows are grouped as the spec groups them
  (local environment, console, mobile, agent harness, model access, cloud, backends, forges, trackers,
  notifications, identity and roles); each row has `name`, `status`, `edition` (where the spec has
  one), `note`, `evidence` (a repository path) and, in the file's header, `as_of` and `never_say`.
  Every row of the reconciliation (F1–F31) maps to one row or one removal.
- **Schema/rules/config change:** none beyond the new file.
- **Tests:** `website/test/compatibility.mjs`: closed statuses and editions; every evidence path
  exists; the Claude row's note is the Terms page's sentence; no row text contains a `never_say` phrase.
- **Command:** `cd website && node --test test/compatibility.mjs` then `npm run check`; `GOFLAGS=-p=2 go test ./internal/cli -run Docs -count=1` if `website/README.md` is pinned from Go.
- **Depends on:** nothing.
- **Red step:** the test fails because the file does not exist, then fails once with a row that
  carries an unknown status.
- **Reviewer tier:** standard.
- **Commit:** `website: the compatibility matrix as data, with an evidence test`.

### CM2. The page and its link
- **Files:** `website/src/pages/compatibility.astro` (new), `website/src/content.ts` (the data loader), `website/src/layouts/Base.astro` (the navigation entry, **added only after the owner approves the nav change**: `.claude/rules/website.md`; until then the page is reachable by URL and from the Docs hub), `website/test/compatibility-page.mjs` (new), `website/test/linkcheck.mjs` only if its list
  of pages is explicit.
- **Interfaces:** consumes CM1's file; produces `/compatibility/`.
- **Tests:** the built page contains one table per group with the legend, every status renders with
  its glyph and text (not colour alone), no `never_say` phrase appears in the built HTML, the page is
  linked from the navigation and passes the site's accessibility and link tests.
- **Command:** `cd website && npm run check`.
- **Depends on:** CM1; the nav entry additionally on the owner's explicit nav approval.
- **Red step:** the page test fails until the page and the link exist.
- **Reviewer tier:** standard.
- **Commit:** `website: the compatibility page`.

## Slice B: keeping it true

### CM3. The stamp and the release procedure
- **Files:** `website/test/compatibility.mjs` (adds the stamp test: CM1 created the file),
  `docs/release.md`, `.claude/skills/new-release/SKILL.md` (force-add if excluded). The console's release notes live under `docs/releases/console/`: the stamp test reads `docs/releases/vX.Y.Z.md` for `as_of.cli` and `docs/releases/console/vX.Y.Z.md` for `as_of.console`.
- **Interfaces:** the release PR must update `as_of.cli` and `as_of.console` in
  `website/data/compatibility.yaml` to the release being cut, the way it already writes the release
  summary; the test fails on a tree whose stamp is behind the newest `docs/releases/vX.Y.Z.md`.
- **Schema/rules/config change:** a skill change: **security-sensitive path** (`.claude/**`).
- **Tests:** the stamp test; `go test ./internal/cli -run Docs` for the release doc's pins.
- **Command:** `cd website && node --test test/compatibility.mjs`; `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs' -count=1`.
- **Depends on:** CM1, and the stale-docs task DOC1 (**in flight, not on main**: it edits the same skill and `docs/release.md` for the website promote step; CM3 launches after DOC1 merges).
- **Red step:** the stamp test fails against a stamp one release behind.
- **Reviewer tier:** **opus + mandatory independent review** (ask for the in-run level 3, never a
  second local review).
- **Commit:** `release: the compatibility stamp is part of the release PR`.

### CM4. Owner read of the copy (OWNER-RUN, no command)
The owner reads the rendered preview's rows against the legend before the release's website promote
(`website-preview` is dispatched by the release procedure). Nothing to type; a wrong row is a change
to `website/data/compatibility.yaml` in a PR.

## Parallel execution

Edges: CM1 → CM2; CM1 → CM3 (and DOC1 → CM3). Levels: **0:** CM1. **1:** CM2, CM3. **Critical path:**
CM1 → CM2 (the page), one website `npm run check` each.

| Level | Task | Files it writes |
| --- | --- | --- |
| 0 | CM1 | `website/data/compatibility.yaml`, `website/test/compatibility.mjs` |
| 1 | CM2 | `website/src/pages/compatibility.astro`, `website/src/content.ts`, `website/src/layouts/Base.astro`, `website/test/compatibility-page.mjs` |
| 1 | CM3 | `website/test/compatibility.mjs` (a level after CM1 wrote it), `docs/release.md`, `.claude/skills/new-release/SKILL.md` |

No level holds two writers of one file: CM1 is the only writer of the data file; CM2 and CM3 write
disjoint files, and `website/test/compatibility.mjs` is written by CM1 at level 0 and CM3 at level 1.

## Checks and release actions

No live check is taken. A release's `### For operators` needs no entry: the page is static and the
release procedure promotes the website.

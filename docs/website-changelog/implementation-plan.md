# Website change log: implementation plan

Builds `spec.md` as amended by [reconciliation.md](reconciliation.md): the change log page shows the
**three newest CLI releases** and a "See all releases on GitHub" link, plus one line each for the latest
console and mobile releases, built from the repository's own release files with **no network** (D1640).

## Status

**Plan only; nothing is implemented; no task is approved.** Each task needs the owner's explicit go; an
answer to a decision is not a go. Owner answers that shaped the plan (2026-11-07): D1640 (files stay the
source, newest three plus the link; the spec's API fetch is overridden), D1641 (CLI three plus a line per
other stream, amended after review), D1642 (the release procedure's preview and promote deploy the change;
no new workflow), D1643 (no date). Open: D1644–D1646 and D1648; the tasks below follow the
recommendations and change with the answers.

## Conventions

- **Website:** `.claude/rules/website.md`. The build needs no network and nothing is written into the tree; no date; no secret-shaped literal; no backticks in a summary line; `npm run check` in `website/` takes about 7 minutes, so run the named tests while developing and the whole gate once (one local test process at a time).
- **Tests are pinned facts:** the page's own words are held to the release files and summaries by `website/test/changelog-page.mjs`. WC1 relaxes that guard by exactly the words D1644 and F5 name, by name, and no more.
- **Docs:** `go test ./internal/cli -run Docs` after any change under `docs/`, `.claude/` or `README.md`.
- **Never edit `.github/workflows/`** from an agent run. This plan needs no workflow change (D1642).
- **Skill/rules edits are security-class paths** (`.claude/**`): opus plus an independent review (WC3).
- **Release notes:** the change is website and docs only, no operator step, so there is no `### For operators` entry.

## Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| The cut is made in the page; `buildChangelog` still returns every release, because `Hero.astro` reads `buildChangelog().find(released)` (F10) | WC1: `TestChangelogGeneratorStillReturnsEveryRelease` (the existing `changelog covers every release file` keeps passing) |
| "Newest" is by version triple (`newestFirst`), never by file time or name order (`v0.9.7` before `v0.10.0` is wrong) | WC1: a test on a fixture directory holding `v0.9.7`, `v0.10.0`, `v0.20.3` |
| The page renders exactly three CLI entries (the fixture and the real tree), plus the link | WC1 |
| The page still carries no date and no sentence that is neither a release file's, a summary's, nor one of the new words listed by name | WC1 |
| A release file with no summary line still stops the build; an older release's summary is still required (F12) | unchanged; `website/test/changelog-page.mjs` existing cases |
| The "not yet released" pill still renders for a file ahead of the plugin's version (D1645) | existing case, adjusted to the three-entry page |
| No fetch, no new dependency, no new workflow | WC1 review focus; the build's own network-free test |

## Slices and tasks

**Slice 1: the page.** WC1 alone ships the visible change. **Slice 2: the words around it.** WC2 and WC3 say what the page now does.

### WC1. The page shows three CLI releases, the GitHub link and the other streams' lines

- **Files:** `website/src/pages/changelog.astro`, `website/src/components/Changelog.astro`, `website/src/content.ts` (a loader for `website/data/mobile-release-summaries.yaml`, which no site code reads today; reads zero entries without error), `website/test/changelog-page.mjs`. No new file; `website/src/lib/changelog.mjs` and `console-release.mjs` are consumed unchanged.
- **Interfaces:** consumes `buildChangelog`, `latestConsoleRelease`, `loadReleaseSummaries`, `loadSite().facts.repository`. Produces: the page renders `entries.slice(0, 3)` (D1640, D1645), then the link `${repository}/releases` with the words "See all releases on GitHub" (D1648), then a console line (`Desktop console vX.Y.Z`, linking `${repository}/releases/tag/console-vX.Y.Z`) and, only when a mobile release exists, a mobile line with its sentence and no link (D1644).
- **Schema/rules/config change:** none. `website/data/mobile-release-summaries.yaml` stays as it is.
- **Tests:** replace "the changelog page lists every release file and no others" with "lists exactly the three newest CLI releases"; add "links the full list on GitHub", "names the latest console release, linking its GitHub release", "names no mobile line while no mobile release exists", "the dropped entries' anchors are gone (D1646)"; extend the allow-list in "adds no sentence of its own" by the link text and the two stream labels, by name; keep "states no date" and "every entry links to its GitHub release".
- **Command:** `cd website && node --test test/changelog-page.mjs test/changelog.mjs`, then `npm run check` once.
- **Depends on:** nothing.
- **Commit:** `website: the changelog shows the three newest releases and links the rest (WC1)`.
- **Red step:** write the "exactly three" test first; it fails on the real tree because the page lists every release file.
- **Reviewer tier:** standard (light review). **OWNER-RUN:** no.

### WC2. The website README says what the page does

- **Files:** `website/README.md` (the sections "The changelog is built, never committed" and its test list; the line that says the page lists every release).
- **Interfaces:** none. **Schema change:** none.
- **Tests:** `node --test test/changelog.mjs` ("README carries no changelog section" must still hold: this edit keeps the section's title and adds no `## Changelog` heading) and `go test ./internal/cli -run Docs`.
- **Command:** as above. **Depends on:** WC1 (the wording follows the page).
- **Commit:** `website: README states the three-entry changelog (WC2)`.
- **Red step:** none (prose); the pinned README tests are the check. **Tier:** standard. **OWNER-RUN:** no.

### WC3. The release procedure's text says the page needs no edit

- **Files:** `docs/release.md` ("After each release, the website" and the Highlights and summary lines), `docs/releases/README.md` ("The end-user summary": the page prints the summary of the three newest releases; every release still has one), `.claude/skills/new-release/SKILL.md` (the "Update the website" step and step 2's summary line), `.claude/rules/website.md` (the two release-file lines, only if their wording changes).
- **Interfaces:** none. **Schema change:** none.
- **Tests:** `go test ./internal/cli -run Docs` (the docs tests pin sentences in these files: keep each pinned fact, change the wording in both places), and the plugin's skill lint is not in play (`.claude/skills` is the repository's own skill).
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli -run Docs -count=1`.
- **Depends on:** WC1, and **DOC1 (PR #1897, open) merged first**: it edits `docs/release.md` and `.claude/skills/new-release/SKILL.md` ("the owner promotes … the approval click"), so a parallel edit conflicts.
- **Commit:** `docs: the release procedure no longer edits the changelog page (WC3)`.
- **Red step:** none; the Docs tests and an independent reading are the check.
- **Reviewer tier:** **opus + mandatory independent review** (a skill and rules path). **OWNER-RUN:** no.

### WC4. Check 430: the preview shows three entries and the link (OWNER-RUN)

- **What it measures:** after the first release that follows WC1, the `website-preview` channel's changelog page shows exactly three CLI entries, newest first, "See all releases on GitHub" linking the repository's releases page, the latest console line, and no mobile line while no mobile release exists. The hermetic form of every assertion is WC1's test; this check is the deployed form, and it is the release's own "update the website" step (the release performer opens the preview URL before promoting).
- **Who runs it:** the release performer, as part of the release; no new command and no live action beyond the release procedure's own dispatch. **Depends on:** WC1 merged and a release cut.

## Parallel execution

Edges: DOC1 (external) → WC3; WC1 → WC2; WC1 → WC3; WC1 → WC4.

| Level | Tasks |
| --- | --- |
| 0 | DOC1 (PR #1897, not this plan's) |
| 1 | WC1 |
| 2 | WC2, WC3 (WC3 only after DOC1 merges) |
| 3 | WC4 (at the next release) |

**Critical path:** WC1 → WC3 (and DOC1 ahead of it) → WC4. WC2 is off the path. The levels are forced by the page (WC1) the other two describe, and by DOC1's edits to the same two docs as WC3.

### Shared-files table

| File | Writer | Level |
| --- | --- | --- |
| `website/src/pages/changelog.astro`, `website/src/components/Changelog.astro`, `website/src/content.ts`, `website/test/changelog-page.mjs` | WC1 | 1 |
| `website/README.md` | WC2 | 2 |
| `docs/release.md`, `docs/releases/README.md`, `.claude/skills/new-release/SKILL.md`, `.claude/rules/website.md` | WC3 | 2 |

No level holds two writers of one file. `docs/release.md` and the skill are also written by DOC1 (level 0, a different plan): WC3 waits for it.

## Checks

| Check | Measures | Who |
| --- | --- | --- |
| 430 | the deployed preview of the changelog page (WC4) | the release performer |

**Checks 431–439 and D1649–D1659 are reserved and unused.**

## Decisions → tasks

D1640 → WC1, WC2, WC3 · D1641/D1644 → WC1 · D1642 → WC3 (no workflow task exists) · D1643 → WC1 (the existing no-date test) · D1645 → WC1 · D1646 → WC1 · D1647 → none (unchanged) · D1648 → WC1.

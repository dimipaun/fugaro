# Reconciliation: the feature matrix

The amended spec. **Where this document disagrees with [spec.md](spec.md), this document wins.**
Checked against `origin/main` at `3e1b02570` on 2026-11-15. Decisions **D2440–D2447** were answered
by the owner on 2026-11-15; D2448–D2459 stay reserved for this folder. No check number is taken.

## Assumptions

- **A1.** "status.md" is `docs/status.md`, and "spec" means a row in one of its four tables. Some
  rows are not specs (F6). The matrix classifies every row.
- **A2.** "Release" means a CLI release (`vX.Y.Z`, `scripts/release.sh`). The console
  (`console-vX.Y.Z`) and the mobile app (`mobile-vX.Y.Z`) release on their own streams
  (`docs/release-streams.yaml`) and do not regenerate the matrix (D2447).
- **A3.** "Tier" means the plans a customer buys under the Subscription Terms (F2), not the
  licence a source file carries.
- **A4.** The public page is on fugaro.dev, the Astro site under `website/`, and follows its rules
  (`.claude/rules/website.md`).
- **A5.** The matrix is product copy, not a contract. It carries no price, no date and no legal
  wording (F9).

## What the repository already has

| Spec concept | What the repository has |
| --- | --- |
| A public matrix rendered from one data file and refreshed on release | The compatibility page: `website/data/compatibility.yaml`, `website/src/pages/compatibility.astro`, `loadCompatibility()` in `website/src/content.ts`, its `as_of` stamp moved by the release notes PR (`docs/release.md`, "Before you tag"; D1538) |
| Statuses | The compatibility file's closed set: `supported`, `coming-soon`, `built-not-enabled`, `roadmap`, `not-planned` |
| Tiers | The plans Community, Team and Enterprise (`docs/licensing/subscription-terms.md`; `TierCommunity`, `TierTeam`, `TierEnterprise` in `ee/licence/key.go`; `docs/subscriptions/reconciliation.md` B1) |
| Reading repository documents into the site at build time | `website/src/lib/changelog.mjs` reads `docs/releases/` |
| A parser of status.md | None |

## Findings

**F1 — Exists: the compatibility matrix is the same shape.** `docs/compatibility-matrix/` built a
public page from one data file (`website/data/compatibility.yaml`). The page writes no claim of its
own and has closed statuses and editions. An `as_of` stamp is moved by the release notes PR and
refused by `website/test/compatibility.mjs` when it falls behind. This design copies that pattern:
one mapping file, one loader, one page, one stamp. The compatibility page covers platforms and
integrations. The feature matrix covers capabilities. They are separate pages, linked both ways
(D2442).

**F2 — Exists: the tiers are already named, and Team and Enterprise carry the same features.** The
Subscription Terms define the Community Plan (no fee: the Core, development and testing use under
§4.2, and the Free Plans), Team (a Paid Plan bought online, Part E) and Enterprise (a Paid Plan
under an Order Form, Part F). `ee/licence/key.go` carries `TierCommunity`, `TierTeam` and
`TierEnterprise`. The key gates no feature by tier: Team and Enterprise differ in how they are
bought and supported (B8), not in what they include. The spec's open question 1 is answered by the
code. The column layout was the owner's (D2440).

**F3 — Rule: the site already has an edition vocabulary.** The compatibility page labels rows
"Core (MIT)" and "Subscription" and never uses "CE" or "EE" (D1522). The feature matrix uses plan
names (D2440), so the site has two vocabularies for one split. Both pages carry a sentence relating
them (T4, I1).

**F4 — Gap: status.md's sections do not map one-to-one onto the spec's states.** The tables are
"Nearly There (NT)", "Work in progress (WIP)", "To do or parked" and "Done". "To do or parked" mixes
not-started specs with specs the owner parked by name. A parked spec can have merged tasks
(`repo-image-removal`, 2/11). "Planned" therefore needs its own rule (D2443).

**F5 — Gap: "Done" is not "shipped".** The status page is read from `main`, not from a release. Many
Done rows have owner live checks still to run (for example `no-prompt-policy`, Checks 380–382). The
mobile stream's `current` is `0.0.0` in `docs/release-streams.yaml`: the app has never shipped. Some
merged code sits behind a switch that is still off (`secrets-brokering` B1.13's flip). The rule
"Available when all specs are Done" alone would claim all of these (D2444).

**F6 — Gap: not every status.md row is a spec, and one row can name several.** Examples: `how-to`
("not a spec"); `voice-module, collateral, backlog, plans` (one cell, "no plan or not a spec"); rows
superseded by another spec (`central-orchestrator`, `windows-cli`) or marked historical
(`in-job-review`); `compute-backend` with no folder under `docs/` (its document is
`docs/backends.md`); `go-to-market` and the specs that are purely internal. The mapping must
classify every name in every row's first cell, splitting on commas. Each name is either a feature's
spec or an excluded entry with a reason.

**F7 — Gap: the spec's merge rule makes shipped features regress.** Bitbucket support is Done
(`bitbucket-credentials`), while `bitbucket-token-types` and `bitbucket-workspace-token`, which
extend it, are WIP. If they shared one row, "Available only when all contributing specs are Done"
would turn a shipped feature back to Coming soon (D2445).

**F8 — Gap: nothing reads status.md as data today.** Its format is the seven prose rules in the
HTML comment at its top, not a schema. The generator is its first machine reader. A format change
the generator does not expect must stop the generator and name the line, never be read as a
guess. Only the merger's sweep edits status.md (CLAUDE.md, "`docs/status.md` follows the specs"),
so no task here edits it, and the generator never writes it.

**F9 — Rule: the site's standing rules bind the page.** From `.claude/rules/website.md`: a nav
change needs the owner's approval (D2442's answer is that approval for `/features/` under
Product); `npm run check` runs before a push; the copy and nav tests are not weakened. From
`docs/website/README.md`: whatever the product cannot back today is held back. From compatibility
D1523: no dates for unbuilt work. The compatibility file carries no price. "Coming soon" and
"Planned" make no promise of a date.

**F10 — Rule: the release procedure already moves a stamp, and `release.sh` checks text by grep.**
`docs/release.md` "Before you tag" moves the compatibility stamp in the same PR as the notes.
`scripts/release.sh` refuses, in its preconditions, a `main` without the version's line in
`website/data/release-summaries.yaml`. That script needs `go`, `gh` and `docker` but not `node`, so
its stamp check is a grep, not a run of the generator (D2447).

**F11 — Gap: a byte-exact freshness test would break every status sweep.** status.md changes
several times a day through sweeps (the last commit on `main` is one). A test that FEATURES.md
equals the generator's output for the current status.md would make every sweep regenerate the
matrix. Freshness is enforced at release, by the stamp (D2447). An unmapped spec is flagged only
when the generator writes, not in `npm run check`.

**F12 — Rule: the website is a second artifact that the Go module never imports.**
`internal/cli/docs_website_boundary_test.go` enforces this. The generator is a Node module under
`website/`, like `changelog.mjs`, run as a script for FEATURES.md and imported by the page (D2446).

**F13 — Open: the spec has no acceptance criteria.** Settled here:
1. Every name in every status.md row is mapped or excluded, or the generator refuses to write and
   lists the names.
2. The page and FEATURES.md show the same rows and statuses.
3. Neither output contains a spec name, a status.md section name ("Nearly there", "Parked", "Work
   in progress"), a task id, a date or a price.
4. FEATURES.md's `as_of` stamp equals the newest CLI release notes file at or below the stream's
   `current`.
5. An Available row never turns back to Coming soon because a spec was added to it.

**F14 — Gap: per-spec progress is in the repository already.** The mapping file names specs and
lives in the repository, as status.md does. The spec's out-of-scope line is about the public page
and FEATURES.md, so neither shows a spec name or a count (F13 criterion 3).

## Decisions

| # | Question | Recommendation | Alternative and its cost | Answer |
| --- | --- | --- | --- | --- |
| **D2440** | The Tier column (spec open question 1; F2, F3) | Two values: "All plans" and "Team and Enterprise" | Three plan columns, which are always identical for Team and Enterprise today; or the Core/Subscription labels | **2026-11-15: not the recommendation. Three plan columns**, one checkmark per plan: Community, Team, Enterprise. Each feature's mapping names the lowest plan that includes it (`community`, `team` or `enterprise`), and every higher plan is checked too. *Why:* the owner's choice of a pricing-table layout. |
| **D2441** | How specs are grouped into features (spec open question 2) | A hand-kept `docs/feature-matrix/mapping.yaml`: features (id, name, explanation, lowest plan, specs, optional hold) and an excluded list with a reason each. The generator refuses to write while a status.md name is unmapped, and lists those names | The agent regroups each release: no upkeep, but names drift | **2026-11-15: as recommended.** *Why:* stable names between releases, as the spec's Stability paragraph asks. |
| **D2442** | Where the page lives (spec open question 3; F9) | `/features/` on fugaro.dev, in the Product menu after "How it works", linked both ways with `/compatibility/` | Under Project; folded into Compatibility; GitHub only | **2026-11-15: as recommended.** This answer is the owner's approval of the nav entry that `.claude/rules/website.md` requires. |
| **D2443** | Which statuses (F4) | Available, Coming soon, Planned. **Planned** when no contributing spec has started (all are in "To do or parked" with nothing merged). A feature whose specs are all parked by the owner is left off the page | Two statuses only; or parked shown as Planned | **2026-11-15: as recommended.** |
| **D2444** | When a feature is Available (F5) | Available when every contributing spec is in Done. Owner live checks do not block. A mapping entry can carry `hold: coming-soon` with a `reason` for built but unshipped features (the mobile app, a switch still off), and that hold wins | Also require every owner live check run; or Done alone | **2026-11-15: as recommended.** |
| **D2445** | A spec that extends an Available feature (F7) | It gets its own row when it is user-visible, and is excluded otherwise. The generator refuses to write when the previous FEATURES.md showed a row Available and the new snapshot shows it Coming soon or Planned, naming the row and the spec that caused it. A `hold` added in the mapping is the deliberate exception | The literal rule: the row drops back until every extension is Done | **2026-11-15: as recommended.** |
| **D2446** | Where the markdown lives and how the page is built (F12) | `FEATURES.md` at the repository root, written by `website/scripts/feature-matrix.mjs`. The page renders the same snapshot model through a loader in `content.ts`, without parsing markdown. The snapshot the page renders is the one frozen at release (D2447), read from FEATURES.md's embedded data block | The page parses the markdown table; or the file inside the spec folder | **2026-11-15: as recommended.** |
| **D2447** | Enforcing refresh on release (F10, F11) | The CLI release notes PR runs the generator, which writes FEATURES.md stamped `as_of: vX.Y.Z`, alongside the compatibility stamp. `website/test/feature-matrix-stamp.mjs` refuses a stamp behind the newest released notes file. `scripts/release.sh` refuses a `main` whose FEATURES.md does not carry the version's stamp | The page reads status.md live at every build: always current, but a website-only deploy shows main's unreleased state | **2026-11-15: as recommended.** |

### Resolutions (one defensible answer, not asked)

- **R1.** Neither output shows a spec name, a count, a task, a date or a price (spec out of scope;
  F9, F13, F14).
- **R2.** `npm run check` validates the mapping's shape and that every name the mapping cites still
  appears in status.md. It does not refuse an unmapped name, so a sweep that adds a new spec row
  stays green. The generator refuses unmapped names when it writes, at release (F11).
- **R3.** The generator reads status.md and never writes it. A row or table it cannot read stops
  it and names the line (F8).
- **R4.** The feature-matrix spec is excluded from its own matrix, as internal.
- **R5.** The frozen snapshot is a fenced JSON block inside FEATURES.md, under the rendered table. The
  page's loader reads that block, so the page and the markdown cannot disagree, and the page never
  reads status.md at build time (D2446, D2447).

## Open tensions

None.

## Open items

- **O1.** The Community column. The Community Plan includes development and testing use of every
  feature (Terms §4.2), so a bare empty Community cell can mislead. Any sentence explaining this is
  legal wording, and the page carries none without the owner's words (A5, compatibility D1539's
  precedent). T4 ships the column with no footnote, and the owner's read (T6) decides whether to
  add one, in the owner's own words.
- **O2.** The rule comment at the top of status.md does not mention that the generator reads it.
  Adding that is a status.md edit, so it belongs to the merger's sweep, not to a task (F8).

## UNVERIFIED

- **U1.** The exact number of features the mapping will hold. The plan estimates 20–30 rows from
  status.md's 79 names. T2 settles it.
- **U2.** Whether the site's per-page byte budget (`website/test/budget.mjs`) has room for a
  30-row table with three checkmark columns. T4's run of `npm run check` settles it.

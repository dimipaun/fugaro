# Implementation plan: the feature matrix

Builds [spec.md](spec.md) **as amended by [reconciliation.md](reconciliation.md)**. **Status:
plan only; nothing is implemented; no task is approved.** Each task needs the owner's explicit go,
and an answer to a decision is not that go.

The owner's answers that shaped the plan (2026-11-15):
- **D2440** (not the recommendation): three plan columns, Community, Team and Enterprise.
- **D2441**: a hand-kept `docs/feature-matrix/mapping.yaml`.
- **D2442**: `/features/` in the Product menu after "How it works". This answer is the nav approval.
- **D2443**: Available, Coming soon and Planned; features whose specs are all parked are left off.
- **D2444**: Done plus a `hold` in the mapping.
- **D2445**: an extension gets its own row, and an Available row never regresses.
- **D2446**: a root `FEATURES.md` and one shared Node library.
- **D2447**: a release stamp, refused by a website test and by `scripts/release.sh`.

## Conventions every task follows

- `.claude/rules/website.md` and `website/README.md` apply to everything under `website/`. Run
  `npm run check` before pushing (about 7 minutes). Tests that pin copy, nav and wording are
  updated, never weakened. The site adds no npm dependency. YAML is read by `parseYAML` from
  `website/src/content.ts`, imported as `website/src/lib/landing.mjs` already does. A second YAML
  reader is a second dialect.
- Docs are tested: a change to `docs/`, `README.md` or `docs/release.md` runs
  `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1`.
- **Always:** follow the compatibility matrix's shape (F1). The page writes no claim of its own,
  and every row comes from the mapping through the snapshot.
- **Ask first:** any nav change beyond D2442's one entry; any sentence about plans, licences or
  terms (O1).
- **Never:** edit `docs/status.md` (only the merger's sweep does, F8); show a spec name, task id,
  count, date or price on the page or in FEATURES.md (R1); deploy the site (the two dispatches stay
  in the owner's terminal, `docs/release.md`); edit `.github/workflows/`; use a secret-shaped
  literal.
- Rigor for this repository resolves to `startup` (`fugaro settings rigor.default --repo
  dimipaun/fugaro`), and no area rule matches these paths. The contract-first pass is on
  (`planning.decoupling` is `true`) and runs at startup's setting: decouple aggressively.

## Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| Statuses are a closed set: `available`, `coming-soon`, `planned` | C1 (`validateMapping`, `validateSnapshot`) |
| Plans are a closed set: `community`, `team`, `enterprise`; a row lists its lowest plan, and every higher plan is checked | C1; T3 and T4a render the three columns |
| Every name in every status.md row is a feature's spec or excluded with a reason, or the generator refuses to write and lists the names; `npm run check` reads no part of status.md, so no sweep turns it red (R2) | T1 |
| The generator stops at a status.md table or row it cannot read and names the line | T1 |
| Available only when every contributing spec is in Done and no `hold` is set; Planned only when no contributing spec has started; all-parked features are omitted | T1 |
| An Available row in the previous FEATURES.md never comes back as Coming soon or Planned, or goes missing, unless a `hold` was added or the id is listed under `retired`; no previous file, no check (R7) | T1 |
| A parked spec counts for nothing beside specs that are not parked (R6) | T1 |
| No spec name, status.md section name, task id, date or price appears in FEATURES.md or the built page. A spec name is matched as a whole hyphenated token; single-word spec names (`mobile`, `website`, `portal`, `plans`, ...) are ordinary words and are not matched | T3, T4a (each output's own test), I1 (the generated FEATURES.md and the built page, ids included: R8) |
| The page and FEATURES.md show the same rows: the page reads FEATURES.md's embedded block | I1 |
| FEATURES.md's `as_of` names an existing notes file not behind the newest one at or below `current` (one release ahead is allowed: F13 criterion 4) | I1 (`feature-matrix-stamp.mjs`), T5 (`release.sh`, ordinary path only) |

## Slice A: the matrix, inert until the next website promotion

### C1. The snapshot contract
- Acceptance: the mapping shape, the snapshot shape, the embedded-block format and a stub
  `buildSnapshot` exist. `validateMapping` and `validateSnapshot` refuse an unknown status, plan
  or key, naming the field.
- **Files:** `docs/feature-matrix/contract.md` (new), `website/src/lib/feature-matrix-model.mjs`
  (new), `website/test/feature-matrix-model.mjs` (new),
  `website/test/fixtures/feature-matrix/snapshot.json` (new), and the stubs
  `website/src/lib/feature-matrix-stub.mjs` (new) and `website/test/feature-matrix-stub.mjs` (new).
  This is six files, all small.
- Interfaces: produces
  - `STATUSES` and `PLANS`.
  - The mapping shape: `version`, a `features` list (`id`, `name`, `explanation`, `plan`,
    `specs`, optional `hold: {status: coming-soon, reason}`; `coming-soon` is the only hold
    status), an `excluded` list (`spec`, `reason`) and a `retired` list of ids (R7).
  - The snapshot shape: `version`, `as_of`, and `rows` of `{id, name, explanation, status,
    plans: {community, team, enterprise}}`.
  - `validateMapping`, `validateSnapshot`.
  - `snapshotBlock(snapshot)` and `readSnapshotBlock(markdown)`, which write and read the fenced
    JSON block FEATURES.md carries (R5).
  - `leakProblems(text, specNames)`, which lists every hyphenated spec name found as a whole
    token, every status.md section name, task id pattern and date other than the stamp (R1, R8).
  - A fixture snapshot, `website/test/fixtures/feature-matrix/snapshot.json`, with its own spec
    list, which the tests of T3 and T4a render. Tests never import the stub.
  - The stub's `buildSnapshot({statusMarkdown, mapping, previous, asOf})`, which returns the fixture
    snapshot, and the stub's own test.
- Tests: `website/test/feature-matrix-model.mjs`: closed statuses and plans, unknown key refused,
  the block round-trips, the fixture passes `validateSnapshot`, and `leakProblems` finds a planted
  hyphenated spec name but not the word "mobile". `website/test/feature-matrix-stub.mjs`: the stub
  returns the fixture.
- Command: `cd website && node --test test/feature-matrix-model.mjs test/feature-matrix-stub.mjs`
- Red step: the test fails until the module exists, then once on a row with status `nearly-there`.
- Depends on: none.
- Commit: `website: the feature matrix's snapshot contract and stub`.
- Reviewer tier: standard.

### T1. Read status.md and build the snapshot
- Acceptance: the builder applies D2443–D2445 and R2–R3. Given status.md and a mapping, it
  returns the snapshot, or it throws listing every unmapped name, every mapped name status.md does
  not carry, every regression against `previous`, or the first unreadable line.
- **Files:** `website/src/lib/feature-matrix.mjs` (new), `website/test/feature-matrix.mjs` (new),
  `website/test/fixtures/feature-matrix/status.md` (new).
- Interfaces: consumes C1's shapes and produces the real `buildSnapshot` with the stub's
  signature. It parses the four tables by heading ("Nearly There (NT)", "Work in progress (WIP)",
  "To do or parked", "Done"), and splits a first cell on commas. A spec is started when it is in
  NT, WIP or Done, or when its To do State cell reports merged tasks; it is parked when that State
  cell says "parked by the owner". The Done table has no Merged column and needs none.
- Tests: Available with all specs Done; Coming soon with one in WIP; Planned with all in To do and
  nothing merged; omitted when all are parked; a parked spec beside a Done one leaves the row
  Available (R6); `hold` wins; an unmapped name is refused with its name; a mapped name status.md
  lacks is refused; a regression is refused with the row and the spec; an Available id missing from
  the new snapshot is refused unless retired (R7); no previous snapshot skips the regression
  check; an unreadable previous block is refused; a renamed table heading is refused with the
  line.
- Command: `cd website && node --test test/feature-matrix.mjs`
- Red step: the Planned and parked cases fail first against a builder that maps only Done.
- Depends on: C1 (the mapping and snapshot shapes).
- Commit: `website: build the feature matrix snapshot from status.md`.
- Reviewer tier: standard.

### T2. The mapping
- Acceptance: every name in every row of `docs/status.md` at the commit the run starts from is a
  spec of exactly one feature or excluded with a reason. Each feature has a user-language name, a
  one- or two-sentence explanation and its lowest plan. A feature whose specs are all Done but
  which has not shipped (the mobile app's Done specs, such as `mobile-auth`, while the mobile stream
  is at `0.0.0`) carries a `hold` with its reason. Extensions follow
  D2445.
- **Files:** `docs/feature-matrix/mapping.yaml` (new), `website/test/feature-matrix-mapping.mjs`
  (new).
- Interfaces: writes the mapping shape this plan states under C1's Interfaces; I1 validates it
  with C1's `validateMapping`. A feature's plan follows F2: `community` for what the
  MIT core does, `team` for what needs the Subscription Edition (`ee/`, the console, the mobile
  app).
- Tests: the file is read literally with `parseYAML` and the closed plan and status sets written in
  the test itself, the way `website/test/compatibility.mjs` reads its file, not through the loader
  it checks. Ids are unique; no spec named twice; no explanation
  longer than two sentences; no name or explanation contains a hyphenated spec name as a whole
  token.
- Command: `cd website && node --test test/feature-matrix-mapping.mjs`
- Red step: the test fails until the file exists.
- Depends on: none. The shape is fixed in this plan, so T2 runs beside C1.
- Commit: `docs(feature-matrix): the mapping from specs to features`.
- Reviewer tier: standard. The owner reads the names in T6.

### T3. FEATURES.md and the generator command
- Acceptance: `npm run features -- --as-of vX.Y.Z` writes `FEATURES.md` at the repository root.
  It contains a lead, a legend, the `as_of` line, one table (Feature, What it does, Status,
  Community, Team, Enterprise) and the snapshot block. A refusal from `buildSnapshot` exits non-zero,
  prints the names, and leaves the file untouched.
- **Files:** `website/src/lib/feature-matrix-markdown.mjs` (new), `website/bin/feature-matrix.mjs`
  (new), `website/package.json` (the `features` script), `website/README.md` (the command, which
  `website/test/readme.mjs` holds to `package.json`'s scripts),
  `website/test/feature-matrix-markdown.mjs` (new).
- Interfaces: consumes C1's snapshot, block format and stub `buildSnapshot`. Produces
  `renderMarkdown(snapshot)` and the command.
- Tests: rendering the fixture snapshot gives a table with three checkmark columns; the block reads
  back equal; `leakProblems` over the output, with the fixture's spec list, is empty; a thrown refusal leaves an existing file byte-identical.
- Command: `cd website && node --test test/feature-matrix-markdown.mjs`
- Red step: the column test fails against a two-column render.
- Depends on: C1 (the snapshot shape and the stub).
- Commit: `website: render FEATURES.md from the snapshot`.
- Reviewer tier: standard.

### T4a. The /features/ page
- Acceptance: `/features/` builds from `loadFeatures()`. It shows one table with status words and
  marks (not colour alone) and three plan columns, it links to `/compatibility/`, and it passes
  every page-wide gate. If it exceeds the site's word or byte budget, the task stops and reports
  it (O5); it does not raise a budget.
- **Files:** `website/src/pages/features.astro` (new), `website/src/content.ts` (`loadFeatures()`,
  reading the stub for now), `website/test/features-page.mjs` (new), `website/test/home.mjs`
  (`plannedRoutes()` gains this plan, whose Files lines are written `**Files:**` so its reader finds
  `features.astro`), `website/test/lib/exempt.mjs` (only if the page is a document page as
  compatibility is).
- Interfaces: consumes C1's snapshot shape through the stub at runtime; its test renders against
  the fixture.
- Tests: the built page has the table, the legend, the three plan columns and the link to
  compatibility; `leakProblems` over the HTML is empty; no price; plus the gates
  `website/test/console-option.mjs` names (a11y, budget, copy, fold, linkcheck, meta, origins,
  os-cards); `home.mjs` admits `/features/`.
- Command: `cd website && npm run check`
- Red step: `features-page.mjs` fails until the page exists.
- Depends on: C1 (the snapshot shape and the stub).
- Commit: `website: the features page`.
- Reviewer tier: standard.

### T4b. The Product menu entry
- Acceptance: the Product menu lists Features directly after "How it works" (D2442), with the
  owner's approval of 2026-11-15 recorded in the comments of `Base.astro` and `base-styles.mjs`, as
  the compatibility entry's was (O3).
- **Files:** `website/src/layouts/Base.astro`, `website/test/base-styles.mjs`.
- Interfaces: none beyond the route.
- Tests: `base-styles.mjs`'s Product group expects `/features/` after `/how-it-works/`.
- Command: `cd website && npm run check`
- Red step: `base-styles.mjs` fails until the entry is added.
- Depends on: T4a (behaviour: the link checks need the page to exist).
- Commit: `website: Features in the Product menu`.
- Reviewer tier: standard.

### T7. The compatibility page links to /features/
- Acceptance: `/compatibility/` carries one link to `/features/`, beside its lead.
- **Files:** `website/src/pages/compatibility.astro`, `website/test/compatibility-page.mjs`.
- Interfaces: none beyond the route.
- Tests: `compatibility-page.mjs` finds the link; `linkcheck.mjs` resolves it.
- Command: `cd website && npm run check`
- Red step: the new assertion fails until the link exists.
- Depends on: T4a (behaviour: the link check needs the `/features/` page to exist).
- Commit: `website: the compatibility page links to the feature matrix`.
- Reviewer tier: standard.

### I1. Wire the real snapshot and write the first FEATURES.md
- Acceptance:
  - The stub and its test are deleted. The command uses T1's `buildSnapshot` with T2's mapping,
    and `loadFeatures()` reads FEATURES.md's block.
  - Names that status.md gained since T2's start commit are mapped here, so the generator writes.
  - FEATURES.md is generated with `as_of` set to the newest `docs/releases/vX.Y.Z.md` on `main`
    (one release ahead of `current` is allowed), so a release that lands between I1 and T5 leaves
    the stamp valid.
  - A stamp test holds the stamp to F13 criterion 4, the built page's rows to the block's, and
    the block to `validateSnapshot`; `leakProblems` over the committed FEATURES.md and the built
    page, with the spec names read from the mapping, is empty (R8). It reads no part of status.md
    (R2).
- **Files:** `website/src/lib/feature-matrix-stub.mjs` and `website/test/feature-matrix-stub.mjs`
  (deleted), `website/bin/feature-matrix.mjs`, `website/src/content.ts`, `FEATURES.md` (new,
  generated), `docs/feature-matrix/mapping.yaml` (only new rows), `website/test/feature-matrix-stamp.mjs`
  (new).
- Interfaces: joins C1, T1, T2, T3 and T4a.
- Tests: `website/test/feature-matrix-stamp.mjs`: the stamp rule, with a fixture one release
  ahead accepted and one behind refused; the built page's rows equal the block's (end to end
  through the real loader); the whole `npm run check`. The generator's agreement with status.md
  is proved once, by the run that writes FEATURES.md here, and is not a standing test (F11).
- Command (from the repository root):
  `v=$(ls docs/releases/v*.md | sed 's|.*/||; s|\.md$||' | sort -V | tail -1) && (cd website && npm run features -- --as-of "$v" && npm run check) && GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1`.
- Red step: the stamp test fails while `loadFeatures()` still reads the stub.
- Depends on: T1, T2, T3, T4a (it wires their real sides together). Merge I1 and T5 back to back
  in one batch, so no release falls between the matrix and the step that refreshes it.
- Commit: `website: the feature matrix, wired end to end, as of the current release`.
- Reviewer tier: standard.

## Slice B: the release keeps it fresh

### T5. The release procedure moves and checks the stamp
- Acceptance:
  - `docs/release.md` "Before you tag" says the notes PR runs `npm run features -- --as-of
    vX.Y.Z` in `website/` and commits FEATURES.md with the notes, the summary line and the
    compatibility stamp. The generator's refusal lists the unmapped specs, and the PR maps them.
  - `scripts/release.sh` refuses in its preconditions when `origin/main`'s FEATURES.md does not
    carry `as_of: vX.Y.Z`, naming the file. This applies to the ordinary release path only. The
    hotfix path does not check it, because `scripts/hotfix-commits.sh` admits no FEATURES.md
    commit on a release branch, and the matrix waits for the next ordinary release. The step says
    so in `docs/release.md`'s "Hotfix releases".
  - The root `README.md` links FEATURES.md.
- **Files:** `docs/release.md`, `scripts/release.sh`, `scripts/release_test.go`,
  `scripts/release_hotfix_test.go`, `README.md`.
- Interfaces: consumes the `as_of` line format of C1's contract.
- Tests: a `TestPreconditionFailures` case refusing a tree without the stamp; every happy-path
  fixture in `scripts/release_test.go` writes a stamped FEATURES.md, as `writeSummaries` writes
  the summaries file, so the existing cases keep passing; the hotfix tests show no stamp is
  demanded; the Docs tests.
- Command: `go test ./scripts/ -count=1 && GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1`
  (the whole `scripts` package, because the fixtures every release test shares change).
- Red step: the new release test fails until the precondition exists.
- Depends on: I1 (data: if `release.sh` demands a FEATURES.md before one is on `main`, the next
  release is refused; a contract cannot supply the file).
- Commit: `release: the notes PR stamps FEATURES.md and release.sh checks it`.
- Reviewer tier: standard.

### T6. The owner's read (owner-run)
- Acceptance: the owner has read the mapping's names and explanations and the `/features/` page
  on the website preview, and has answered O1 (a Community footnote, in the owner's own words, or
  none).
- Owner-run: after I1 merges, the owner dispatches `website-preview` from `main` (`gh workflow run
  website-preview --ref main`), reads `/features/` there, and replies with corrections. Each
  correction is a mapping edit in a follow-up PR. A footnote, if any, is raised as contract change
  CR1. No agent performs a step of it.
- Depends on: I1 (the page with real rows).

## Parallel execution

### Edges kept

| Task | Needs | Reason |
| --- | --- | --- |
| T1, T3, T4a | C1 | each is built against the contract's shapes |
| I1 | T1, T2, T3, T4a | it deletes the stub and wires the real sides (and so follows C1 through them) |
| T5 | I1 | data: `release.sh` must not demand a FEATURES.md that `main` does not have yet |
| T6 | I1 | behaviour: the owner reads the real rows |
| T4b, T7 | T4a | behaviour: the link checks need the page to exist |
| CR1 | T6 | raised only if the owner's read asks for a footnote (O1) |

### Edges broken by C1 (each re-joined by I1)

| Was | Reason it broke |
| --- | --- |
| T1 needs T2 | the builder needs the mapping's shape, not its content; T1 tests on fixtures |
| T3 needs T1 | the renderer needs the snapshot's shape, not the real build |
| T4a needs T3 | the page needs the snapshot and the block format, not a real FEATURES.md |

**The gain**, as `fugaro plan graph` reports it: three edges broken, two kept (T4b and T7 on T4a,
whose links need the page), three tasks added (C1, I1 and the conditional CR1). The weighted critical path drops from 13 to 7.5.

### Levels

| Level | Tasks |
| --- | --- |
| 0 | C1, T2 |
| 1 | T1, T3, T4a |
| 2 | I1, T4b, T7 |
| 3 | T5, T6 |
| 4 | CR1 (only if raised) |

**Critical path:** C1 → T1 → I1 → T6 (→ CR1 if raised). Without CR1: C1 → T1 (or T4a) → I1 → T5.
The sequence after I1 is data (T5) and the owner's read (T6), not an interface.

### Shared files

| File | Writers (level) |
| --- | --- |
| `website/src/content.ts` | T4a (1), I1 (2) |
| `website/bin/feature-matrix.mjs` | T3 (1), I1 (2) |
| `website/src/lib/feature-matrix-stub.mjs`, `website/test/feature-matrix-stub.mjs` | C1 (0), I1 (2, deletes) |
| `docs/feature-matrix/mapping.yaml` | T2 (0), I1 (2, new rows only); T6 (3) is owner-run and is never dispatched: its footprint names the file its follow-up PRs write |
| `docs/feature-matrix/contract.md`, `website/src/lib/feature-matrix-model.mjs` | C1 (0), CR1 (4) |
| `website/package.json` | T3 (1) |
| `website/test/home.mjs`, `website/test/lib/exempt.mjs` | T4a (1) |
| `website/src/layouts/Base.astro`, `website/test/base-styles.mjs` | T4b (2) |
| `website/src/pages/compatibility.astro`, `website/test/compatibility-page.mjs` | T7 (2) |
| `website/README.md` | T3 (1) |

No level holds two writers of one file. C1's own files (`contract.md`,
`feature-matrix-model.mjs`, its test and the fixture) are written by no other task and outlive the
stubs; only the two stub files are deleted, by I1.

### Contract changes anticipated

- **CR1:** a `note` on a snapshot row, if the owner's read (O1) wants a per-row or Community
  footnote. It amends `feature-matrix-model.mjs` and `contract.md`, and reaches T1, T3 and T4a,
  which are merged by then. A follow-up run on each names the contract file by path.

## Considered and declined

- The page parsing FEATURES.md's table (D2446). The page reads the block instead.
- A byte-exact freshness test in `npm run check` (F11). It would break every status sweep.
- A Go generator (F12). The website is a second artifact, and the Go module never imports it.
- Folding the matrix into `/compatibility/` (D2442).

## Checks

None taken. The only owner step is T6's read of the preview. Deploying the live page is the
existing release website step (`docs/release.md`, "After each release, the website").

## After-deploy steps

None. The first public `/features/` page appears with the next release's website promotion, which
`docs/release.md` already makes part of every release.

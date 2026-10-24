# Licensing (open core): implementation plan

## Opening and status

This plan builds [spec.md](spec.md) **as amended by [reconciliation.md](reconciliation.md)**, which
wins where they differ. Base: `origin/main` at `e924481cd`, 2026-10-24.

**Status: plan only. Nothing is implemented, and NO TASK IS APPROVED.** Each task needs the
owner's explicit go; answering a decision approved none of them.

The owner's answers of 2026-10-24 that shape it:
- **D300** the core stays MIT; **D301, amended 2026-10-24 by the owner's O2 answer (not the
  recommendation):** `LICENSE` is not touched at all, and the scope statement goes in a new root
  `LICENSING.md`; **D302** the console, mobile, coordinator and central
  orchestrators stay core, and only net-new organisation features go to `ee/`.
- **D303** ee code reaches users through a separate `main` under `ee/`, a separate artifact; the
  released `fugaro` stays core-only, so `plugin/.claude-plugin/plugin.json` and the Homebrew cask
  keep `MIT` and no task touches them.
- **D304** a CLA through a hosted app, bots allow-listed, before the first outside PR is merged.
- **D305 (not the recommendation)** adopt a published source-available text as is, names filled
  in; **T1 ruled 2026-10-24 as choice (iii):** 0.11.0 ships `ee/` **reserved, with no licence
  file**, and `ee/LICENSE` lands only when counsel's text is approved (task L4.1, after 0.11.0 or
  whenever counsel finishes).
- **D306** the skeleton ships with CLI 0.11.0, with `ee/` reserved (T1); **D307** notices in the CLI archive and the packed console; **D308** the key
  is design only; **D309** a Go-only boundary plus a staged workflow; **D310** a trademark paragraph.

**Re-sliced 2026-10-24 for the T1 and O2 rulings.** What changed: L1.2 is now the reserved `ee/`
(no dependency on counsel); L1.3 writes `LICENSING.md` and leaves `LICENSE` byte for byte as it
is; L1.5 links `LICENSING.md`; Check 130 only verifies that GitHub still reports MIT and that
`LICENSING.md` renders; the licence file moves to a new task, **L4.1**, in a new slice after
counsel. Considered and declined: keeping the licence copy as L1.2 behind a gate (it would sit in
the 0.11.0 slice while being, by ruling, not a 0.11.0 deliverable). No task id was reused for a
different purpose except L1.2, whose earlier content (copying the approved `ee/LICENSE`) moved,
unchanged, to the new L4.1.

Out of scope here (R7): the spec's Task 3 (extension points) and Task 4 (workspaces), and the
D303 `ee/` main, belong to the workspaces plan, which takes D302, D303, D308 and D309 as inputs.

## Conventions every part follows

- `CLAUDE.md`: comments say why; no secret-shaped literal in a test, doc or fixture; hermetic tests
  (`t.TempDir()`, no network); search before writing a helper; targeted test runs, never whole
  packages (`-run` with the test names below; `GOFLAGS=-p=2` on a loaded machine).
- **Docs are tested.** Any task that edits `README.md`, `CONTRIBUTING.md`, `docs/` or website copy
  runs `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1 -timeout 900s`; `CONTRIBUTING.md`
  carries sentences pinned by `internal/cli/docs_runner_timeout_test.go`,
  `docs_compute_backend_test.go`, `docs_coordinator_test.go` and `docs_live_init_census_test.go`,
  so a task adds a section and leaves those sentences alone.
- `.claude/rules/website.md` for L1.5 (`npm run check` in `website/`; update a pinned fact, never
  weaken a test). `.claude/rules/console.md` for L2.2 (exact pins, `npm run typecheck`, unit tests
  with `--maxWorkers=2 --testTimeout=60000`; the console releases on its own `console-vX.Y.Z` tag,
  so L2.2 ships with the next console release, not with CLI 0.11.0).
- **Never edit `.github/workflows/`** from an agent run: L1.7 stages its workflow under
  `docs/pending-workflows/` and the owner copies it in (L1.8).
- **No agent holds or sees a signing key.** The future licence-key private key (D308, R8) is never
  written into the repository, a doc, a test or a run.
- **No real people or organisations** in files, commits or PR bodies beyond what a licence text
  itself must name (the licensor line is the owner's to fill, L0.1).
- Legal text is the owner's and counsel's: no task writes or edits licence terms. L4.1 copies an
  approved text; L1.3 writes a scope statement (`LICENSING.md`) whose words the owner approves in
  review. **`LICENSE` is never edited by any task.**

## Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| `LICENSE` is byte-identical to today's (O2: GitHub keeps reporting MIT; the website's `licenceIdentifier()` keeps reading `MIT License`) | L1.3 `TestDocsLicensingRootLicenceUnchanged`; L1.5 `faq-community.mjs` |
| `LICENSING.md` states the split (`ee/` excluded and reserved, `.agents/` third-party, trademark pointer) and is linked from README and the website card | L1.3 `TestDocsLicensingStatement`; L1.5 |
| No non-`ee/` Go file of any build tag imports `github.com/dimipaun/fugaro/ee`, and no `//go:embed` reaches into `ee/` | L1.1 `TestCoreNeverImportsEE`, `TestCoreNeverEmbedsEE` |
| Every `ee/` source file (any file but `README.md` and `LICENSE`) starts with the two-line notice, and `ee/LICENSE` exists whenever one source file does (R3, amended) | L1.1 `TestEEFilesCarryTheNotice`, `TestEESourceNeedsTheLicence` |
| The core builds and tests with `ee/` deleted | L1.7 staged workflow, run after L1.8 |
| The website card states the split and links `LICENSE` and `LICENSING.md` | L1.5 |
| `ee/README.md` says the folder is reserved and carries no licence yet, until L4.1 | L1.2 creates it, L4.1 switches it |
| The CLI archive carries a notice for every linked third-party module | L2.1 `TestThirdPartyNoticesCoverEveryLinkedModule` |
| The packed console carries `LICENSE` and its notices | L2.2 `packaging-licence.test.ts` |
| Released versions stay MIT, said in `LICENSING.md` and the 0.11.0 notes | L1.3, L1.9 |

**Attacker's view.** The boundary tests are the only guard between commercial and MIT code in this
repository; a build-tagged core file importing `ee/` is the case `go list -deps` misses, so L1.1
parses every file regardless of tags and its self-test plants one. A contributor who removes the
test is caught by review, not by the test: L1.6's rule file names it.

## Slices and tasks

### Slice L0 — owner-run prerequisites (no agent performs any step)

#### L0.1 — Counsel review and the licence decision (OWNER-RUN)
- **What:** settle T1 (reconciliation §Open tensions): choice (i), (ii) or (iii); the licensor line
  (F27 fact 4); the CLA text for the hosted app (D304); F3 (a) and (b); D307's notice format.
- **Produces:** an approved `ee/LICENSE` text with the licensor filled in, handed to L4.1.
- **Depends on:** nothing. **Blocks:** L4.1 and L0.2. **Not on the 0.11.0 critical path** (T1
  ruling, 2026-10-24).

#### L0.2 — Install the hosted CLA app (OWNER-RUN)
- **What:** install a hosted CLA app on the repository, give it the CLA text from L0.1, allow-list
  `fugaro-dev[bot]` and `dependabot[bot]`, and make its status visible on pull requests. No
  workflow file is involved.
- **Verification:** open a test pull request from a second account the owner controls and see the
  app ask for a signature; a bot-authored PR passes without one.
- **Depends on:** L0.1 (the CLA text). **Not on the 0.11.0 critical path** (O3).

### Slice L1 — the 0.11.0 skeleton (D306)

#### L1.1 — The boundary and notice tests
- **Files:** `internal/eeboundary/boundary_test.go` (new; a test-only package), modelled on
  `internal/creds/boundary_test.go`.
- **Interfaces:** consumes the module tree; produces the rule R6 and R3 as tests.
- **Schema/rules/config change:** none.
- **Tests:** `TestCoreNeverImportsEE` (parses imports with `go/parser`, every `.go` outside `ee/`,
  `.agents/`, `node_modules/` and `testdata/`, all build tags); `TestCoreNeverEmbedsEE` (no
  `//go:embed` pattern resolves under `ee/`); `TestEEFilesCarryTheNotice` (every `.go`, `.ts`,
  `.tsx`, `.js`, `.mjs`, `.tf`, `.sh` or other file under `ee/` except `ee/README.md` and
  `ee/LICENSE` starts with `SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise` and a line
  naming `ee/LICENSE`); `TestEESourceNeedsTheLicence` (if any such source file exists,
  `ee/LICENSE` exists); both pass for an absent `ee/` and for a reserved `ee/` holding only
  `README.md` (R3, amended); `TestEEBoundaryScanCatchesPlantedViolations` (a `t.TempDir()` tree
  with a `//go:build ee` core file importing the ee path, an embed into `ee/`, an `ee/` source
  file without the notice, and an `ee/` source file with the notice but no `ee/LICENSE`: the scan
  reports all four; a reserved tree with only `ee/README.md` reports none).
- **Command:** `go test ./internal/eeboundary/ -run 'TestCoreNeverImportsEE|TestCoreNeverEmbedsEE|TestEEFilesCarryTheNotice|TestEESourceNeedsTheLicence|TestEEBoundaryScanCatchesPlantedViolations' -count=1`
- **Red step:** write `TestEEBoundaryScanCatchesPlantedViolations` first against a scan stub that
  returns nothing; it fails on all four plants.
- **Depends on:** nothing. **Reviewer tier:** standard. **OWNER-RUN:** no.
- **Commit:** `eeboundary: core never imports or embeds ee/, and every ee/ file carries its notice`

#### L1.2 — The reserved `ee/` (re-sliced 2026-10-24)
- **Files:** `ee/README.md` (new: the folder is reserved for net-new organisation features under
  their own licence; it carries **no licence yet** and no code; `ee/LICENSE` arrives only when
  counsel's text is approved; core never imports `ee/`; each future file carries the R3 notice);
  `internal/cli/docs_ee_reserved_test.go` (new).
- **Interfaces:** produces the file L1.3's `LICENSING.md` and L1.5's card point at.
- **Tests:** `TestDocsEEReserved` (`ee/README.md` exists, says reserved and that no licence file
  exists yet; `ee/` holds no other file; `ee/LICENSE` is absent). L1.1's tests pass on it.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run 'TestDocsEEReserved|Docs' -count=1 -timeout 900s`
- **Red step:** `TestDocsEEReserved` before `ee/README.md` exists.
- **Depends on:** nothing. **Reviewer tier:** standard. **OWNER-RUN:** no.
- **Commit:** `ee: the folder is reserved and carries no licence yet`

#### L1.3 — `LICENSING.md`, the README Licensing section and the `.agents/` notice (re-sliced 2026-10-24)
- **Files:** `LICENSING.md` (new, root: the core is under the MIT licence in `LICENSE`; `ee/` is
  excluded and reserved, as `ee/README.md` says; `.agents/` is third-party code under its own
  licence in `.agents/LICENSE`; every release before 0.11.0 is MIT (R4); a pointer to the
  README's trademark paragraph); `README.md` (`## License` becomes `## Licensing`: two or three
  sentences, links to `LICENSE` and `LICENSING.md`, the trademark paragraph (D310));
  `.agents/LICENSE` (new: the vendored project's upstream licence text, fetched from its
  repository by the run, the upstream commit named in the commit body);
  `internal/cli/docs_licensing_test.go` (new). **`LICENSE` is not edited (O2).**
- **Tests:** `TestDocsLicensingRootLicenceUnchanged` (`LICENSE`'s SHA-256 equals today's at
  `e924481cd`); `TestDocsLicensingStatement` (`LICENSING.md` names `ee/` as excluded and reserved,
  `.agents/` as third-party, the earlier-releases sentence, and links `LICENSE`, `ee/README.md`,
  `.agents/LICENSE` and the README's trademark paragraph); `TestDocsLicensingReadmeSection` (the
  section links `LICENSE` and `LICENSING.md` and carries the trademark paragraph);
  `TestDocsLicensingVendoredNotice` (`.agents/LICENSE` exists and carries an MIT permission
  notice).
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run 'TestDocsLicensing|Docs' -count=1 -timeout 900s`
- **Red step:** the three new-file tests against today's tree (`TestDocsLicensingRootLicenceUnchanged`
  passes from the start: it is the guard).
- **Depends on:** L1.2 (`LICENSING.md` links `ee/README.md`). **Reviewer tier:** standard, plus
  the owner reads `LICENSING.md`'s wording.
- **Commit:** `licensing: LICENSING.md states the split, the README Licensing section, the vendored skills' licence`

#### L1.4 — Contribution terms
- **Files:** `CONTRIBUTING.md` (a new `## Licensing of contributions` section: outside pull
  requests are merged only after the author signs the Contributor License Agreement, which a bot
  asks for on the first one; `ee/` takes no contributions while it is reserved);
  `.github/pull_request_template.md` (one checklist line about the CLA; not a workflow file);
  `internal/cli/docs_contributing_cla_test.go` (new).
- **Tests:** `TestDocsContributingCLA` (the section exists, names the CLA, and the pinned sentences
  the other docs tests hold are still present byte for byte); `TestDocsPRTemplateCLA`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run 'TestDocsContributingCLA|TestDocsPRTemplateCLA|Docs' -count=1 -timeout 900s`
- **Red step:** both tests before the edit.
- **Depends on:** nothing (the wording holds before L0.2, O3). **Reviewer tier:** standard.
- **Commit:** `contributing: outside contributions need a signed CLA`

#### L1.5 — The website Community card
- **Files:** `website/src/pages/community.astro` (the Licence card: "Fugaro's core is released
  under the {licence} licence; `LICENSING.md` says which parts are not", links `LICENSE` and
  `LICENSING.md`; the "only text that governs" sentence goes; `licenceIdentifier()` unchanged, and
  still reads `MIT License` because `LICENSE` is untouched); `website/test/faq-community.mjs` (the
  pinned facts move with the copy: the identifier test stays, a new assertion that the card links
  `LICENSING.md`, and the link-spacing test still holds).
- **Command:** `cd website && npm run check`, then `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1 -timeout 900s`
- **Red step:** the new `LICENSING.md` link assertion against today's page.
- **Depends on:** L1.3 (`LICENSING.md` exists). **Reviewer tier:** standard.
  Deploying the site is a separate, authorised step (`.claude/rules/website.md`).
- **Commit:** `website: the community card states the core/ee split`

#### L1.6 — The `ee/` path-scoped rule
- **Files:** `.claude/rules/ee.md` (new: loads on `ee/**`; core never imports `ee/`; the R3 notice;
  the test names from L1.1; the private key never enters the repository); `CLAUDE.md` (one row in
  the path-scoped rules table).
- **Tests:** none new; `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1 -timeout 900s`
  (in case a docs test reads the table).
- **Red step:** none (a rule file); the reviewer checks each named test exists.
- **Depends on:** L1.1 (the test names). **Reviewer tier:** standard.
- **Commit:** `rules: ee/ has its own path-scoped rule`

#### L1.7 — Stage the "core without `ee/`" workflow
- **Files:** `docs/pending-workflows/core-without-ee.yml` (new: on push to `main` and
  `workflow_dispatch`, `contents: read`, every action pinned by full commit SHA, Go from `go.mod`;
  deletes `ee/`, then `go build ./...`, `go vet ./...` and `go test ./internal/eeboundary/`);
  `docs/pending-workflows/README.md` (its opening count of pending files and a
  `## core-without-ee.yml` section with the copy-in commands); a test beside the other staged-workflow
  tests, `internal/cli/docs_core_without_ee_workflow_test.go` (new: the file parses, triggers only
  on push to `main` and dispatch, holds no secret, pins every action by SHA, and the README's
  section carries the copy-in).
- **Tests:** the new test plus the existing pending-workflow tests
  (`internal/cli/docs_desktop_console_test.go`'s `pendingWorkflows` derives the set from the
  directory and checks the README's opening names it).
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run 'TestDocsCoreWithoutEE|Pending|Docs' -count=1 -timeout 900s`
- **Red step:** the new test before the file exists.
- **Depends on:** nothing. **Reviewer tier:** standard.
- **Commit:** `pending-workflows: the core builds and tests with ee/ deleted (staged for the owner)`

#### L1.8 — Copy the staged workflow in (OWNER-RUN)
- **What the owner types**, from a clean checkout of `main` after L1.7 merges:
  `cp docs/pending-workflows/core-without-ee.yml .github/workflows/core-without-ee.yml`,
  `git add .github/workflows/core-without-ee.yml`, `git rm docs/pending-workflows/core-without-ee.yml`,
  update the README's opening count as its section says, commit, push, then
  `gh workflow run core-without-ee --ref main` and see it green.
- **Depends on:** L1.7 merged, and L1.1 merged (the workflow runs `internal/eeboundary`).

#### L1.9 — The 0.11.0 release-notes line
- **Files:** none in this plan. The 0.11.0 release PR (`/new-release 0.11.0`) adds one Highlights
  bullet to `docs/releases/v0.11.0.md` and folds the same fact into the version's line in
  `website/data/release-summaries.yaml` (no backticks): *Fugaro's core stays MIT, and the
  repository now reserves an `ee/` directory for future organisation features under their own
  licence, described in LICENSING.md; every earlier release stays MIT.* No `### For operators`
  entry (R5).
- **Depends on:** L1.2, L1.3, L1.5 merged. **Done by:** the release PR.

#### L1.10 — Write Check 130 (amended 2026-10-24: GitHub's MIT reading and `LICENSING.md` only)
- **Files:** `docs/gcp-live-checklist.md` (a `## Check 130` section and its register row: after
  L1.3 merges, the owner opens the repository page and confirms GitHub still reports **MIT**, then
  opens `LICENSING.md` on GitHub and confirms it renders with working links; as amended by O2).
- **Tests:** the checklist's own docs tests.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1 -timeout 900s`
- **Red step:** none beyond the register's tests, which fail if the row and section disagree.
- **Depends on:** nothing. **Reviewer tier:** standard.
- **Commit:** `checklist: Check 130, the licence detectors after the open-core skeleton`

#### L1.11 — Run Check 130 (OWNER-RUN)
- **What:** after L1.3 merges, look at the repository's licence badge and at `LICENSING.md`, and
  write the result into the Check 130 section. A reading other than MIT is a finding against O2's
  premise and goes back to the owner.
- **Depends on:** L1.3 merged, L1.10 merged.

### Slice L2 — third-party notices (D307)

#### L2.1 — Notices in the CLI archive
- **Files:** `scripts/third-party-notices.sh` (new: `go list -deps` over `./cmd/fugaro`, then each
  non-main module's `LICENSE*`/`COPYING*`/`NOTICE*` from its module directory, into one file);
  `scripts/notices_test.go` (new); `.goreleaser.yaml` (a `before.hooks` line running the script and
  the file added to `archives[0].files`); `docs/release.md` (one sentence saying the archive
  carries it).
- **Tests:** `TestThirdPartyNoticesCoverEveryLinkedModule` (runs the script into `t.TempDir()`
  against the already-downloaded module cache; every module `go list -deps ./cmd/fugaro` names
  has a section with its licence text, and `NOTICE` contents appear where a module has one);
  `TestGoreleaserArchivesShipNotices`.
- **Command:** `go test ./scripts/ -run 'TestThirdPartyNotices|TestGoreleaserArchivesShipNotices' -count=1`,
  then `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1 -timeout 900s` (for `docs/release.md`).
- **Red step:** the coverage test against a stub script that writes an empty file.
- **Depends on:** nothing. No new dependency (the Go toolchain and the shell only).
  **Reviewer tier:** standard. Exercised for real only by the 0.11.0 release run.
- **Commit:** `release: the CLI archive carries its third-party notices`

#### L2.2 — `LICENSE` and notices in the packed console
- **Files:** `console/package.json` (`build.extraResources`: the repository's `LICENSE` and a
  console notices file); `console/scripts/notices.mjs` (new: from `console/package-lock.json`'s
  non-dev packages that the bundle ships, today none beyond Electron, whose own licence files the
  packer adds, U4); `console/test/unit/packaging-licence.test.ts` (new).
- **Tests:** `packaging-licence.test.ts` (the build config ships both files; the notices script
  lists every non-dev package with its licence; a planted non-dev package appears).
- **Command:** `cd console && npm run typecheck && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 packaging-licence`
- **Red step:** the test before `extraResources` changes.
- **Depends on:** nothing. **Reviewer tier:** standard. Ships with the next `console-vX.Y.Z`; the
  owner's console release confirms the files inside the packed app (U4).
- **Commit:** `console: the packed app carries LICENSE and its notices`

### Slice L3 — design only, handed to the workspaces plan (not built in 0.11.0)

#### L3.1 — The licence-key design note
- **Files:** `docs/licensing/licence-key.md` (new): D308 as a design (Ed25519 over a JSON claim
  set: licensee, features, expiry, issued; base64url; verified offline in `ee/` code with the
  public key compiled in; a grace period after expiry; no revocation; no phone-home); where the key
  is stored is left to the workspaces plan; minting is OWNER-RUN with a key kept off every machine
  an agent reaches (R8); U5 (secret scans) is a check the first implementing task runs.
- **Tests:** `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1 -timeout 900s`.
- **Depends on:** nothing. **Reviewer tier:** opus + mandatory independent review (a trust
  boundary, even on paper).
- **Commit:** `docs(licensing): the licence-key design (D308), not built`

### Slice L4 — the enterprise licence, after counsel (not in 0.11.0)

#### L4.1 — `ee/LICENSE` from the approved text (new 2026-10-24)
- **Files:** `ee/LICENSE` (new), `ee/README.md` (from "reserved, no licence yet" to "licensed
  under `ee/LICENSE`"), `LICENSING.md` (the `ee/` line names `ee/LICENSE`),
  `internal/cli/docs_ee_reserved_test.go` (its reserved assertions become licensed ones),
  `internal/cli/docs_licensing_test.go` (the statement test follows).
- **Interfaces:** consumes L0.1's approved text verbatim.
- **Tests:** `TestDocsEELicenceIsTheApprovedText` (the file's SHA-256 equals the one the owner's
  approval records in the commit body); `TestDocsEEReserved` renamed `TestDocsEELicensed`;
  `TestDocsLicensingStatement` updated.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run 'TestDocsEE|TestDocsLicensing|Docs' -count=1 -timeout 900s`
- **Red step:** `TestDocsEELicenceIsTheApprovedText` before the file exists.
- **Depends on:** L0.1, L1.2, L1.3. **Reviewer tier:** opus + mandatory independent review (legal
  text; the reviewer diffs it against L0.1's approved copy). **OWNER-RUN:** the approval itself.
- **Commit:** `ee: the enterprise licence, as approved`

## Parallel execution

**Edges** (from the Depends-on lines): L0.1 → L0.2; L1.2 → L1.3; L1.3 → L1.5; L1.1 → L1.6;
L1.1, L1.7 → L1.8; L1.2, L1.3, L1.5 → L1.9; L1.3, L1.10 → L1.11; L0.1, L1.2, L1.3 → L4.1.

| Level | Tasks (run concurrently) |
| --- | --- |
| 0 | L0.1 (owner), L1.1, L1.2, L1.4, L1.7, L1.10, L2.1, L2.2, L3.1 |
| 1 | L0.2 (owner), L1.3, L1.6, L1.8 (owner) |
| 2 | L1.5, L1.11 (owner), L4.1 (after counsel) |
| 3 | L1.9 (the release PR) |

**Critical path to the 0.11.0 tag:** L1.2 → L1.3 → L1.5 → L1.9; the website check (~7 minutes)
is the longest single step. Counsel (L0.1) and the licence file (L4.1) are off it by the T1
ruling: 0.11.0 ships `ee/` reserved.

**What must be sequential and why:** L1.3 after L1.2 (`LICENSING.md` links `ee/README.md`); L1.5
after L1.3 (the card links `LICENSING.md`); L1.6 after L1.1 (it names L1.1's tests); L1.8 after
L1.7 and L1.1 (it copies L1.7's file, which runs L1.1's tests); L4.1 after L1.2 and L1.3 (it
edits both tasks' files) and after counsel.

### Shared-files table (no level has two writers of one file)

| File | Writers (level) |
| --- | --- |
| `LICENSE` | none (O2: never edited) |
| `LICENSING.md` | L1.3 (1), L4.1 (2) |
| `README.md` | L1.3 (1) |
| `.agents/LICENSE` | L1.3 (1) |
| `ee/README.md` | L1.2 (0), L4.1 (2) |
| `ee/LICENSE` | L4.1 (2) |
| `internal/cli/docs_ee_reserved_test.go` | L1.2 (0), L4.1 (2) |
| `internal/cli/docs_licensing_test.go` | L1.3 (1), L4.1 (2) |
| `internal/eeboundary/boundary_test.go` | L1.1 (0) |
| `CONTRIBUTING.md`, `.github/pull_request_template.md`, `internal/cli/docs_contributing_cla_test.go` | L1.4 (0) |
| `website/src/pages/community.astro`, `website/test/faq-community.mjs` | L1.5 (2) |
| `.claude/rules/ee.md`, `CLAUDE.md` | L1.6 (1) |
| `docs/pending-workflows/core-without-ee.yml`, `docs/pending-workflows/README.md`, `internal/cli/docs_core_without_ee_workflow_test.go` | L1.7 (0); the owner's copy-in L1.8 (1) |
| `.github/workflows/core-without-ee.yml` | L1.8 (1, owner) |
| `docs/gcp-live-checklist.md` | L1.10 (0); the owner's result L1.11 (2) |
| `scripts/third-party-notices.sh`, `scripts/notices_test.go`, `.goreleaser.yaml`, `docs/release.md` | L2.1 (0) |
| `console/package.json`, `console/scripts/notices.mjs`, `console/test/unit/packaging-licence.test.ts` | L2.2 (0) |
| `docs/licensing/licence-key.md` | L3.1 (0) |
| `docs/releases/v0.11.0.md`, `website/data/release-summaries.yaml` | the 0.11.0 release PR (3) |

`docs/pending-workflows/README.md` and `docs/gcp-live-checklist.md` are also written by other
plans; whichever PR merges second rebases and keeps both entries (the check register's own rule:
the second claim renumbers, and Check 130 is this plan's reserved number).

## Checks this plan takes

From the reserved block **130–134**: **Check 130** only (L1.10 writes it, L1.11 is the owner's run):
as amended by O2, that GitHub still reports MIT and that `LICENSING.md` renders. **131–134** stay reserved and
unused. No check touches a cloud project.

## After deploy

Nothing runs after a deploy (R5): no backfill, config or secret. The only owner steps are L0.1,
L0.2, L1.8, L1.11 and the approval inside L4.1, none of which is a rollout step.

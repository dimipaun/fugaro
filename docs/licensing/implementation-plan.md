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
- **D303, amended by the owner 2026-10-24 (not the recommendation):** one released binary,
  key-gated: ee code is compiled into the same `fugaro` by an `ee` build tag the release build sets
  and enabled by the licence key; untagged builds are pure MIT. Only `cmd/fugaro/ee_on.go`
  (`//go:build ee`) may import `ee/` (R6). At 0.11.0 `ee/` is empty and the release sets no tag,
  so `plugin/.claude-plugin/plugin.json` and the Homebrew cask keep `MIT`; the release that first
  sets the tag changes every label together (T3, slice L5).
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

**Amended 2026-10-26 by the owner's ruling D311** ([reconciliation.md](reconciliation.md#amendment-2026-10-26-d311)):
`console/LICENSE.md`, `mobile/LICENSE.md` and `ee/LICENSE` land together as a draft counsel has not
read. That brings **L4.1** forward without L0.1: `ee/README.md`, `LICENSING.md`, the ee and
licensing docs tests, the staged `core-without-ee.yml` skip list and `CONTRIBUTING.md` moved as
L4.1 lists, `TestDocsEELicenceIsTheApprovedText` waits for the approved text, and the website's two
licence elements are unchanged because both still read true (`ee/` is still reserved for
organisation features, and `LICENSING.md` still says which parts the MIT licence does not cover).
**L2.2** gains `console/LICENSE.md` in `build.files` and `console/package.json`'s `license` field.

**Amended 2026-10-27: the Subscription Terms draft.** [subscription-terms.md](subscription-terms.md)
is a first draft of the Fugaro Subscription Terms that counsel's licence text names "at [URL TO BE
PUBLISHED]", written for counsel to review and **not in force**. Two tasks follow it, both after
counsel: **L4.3** publishes the approved text on the website (owner-run), and **L4.4** replaces the
placeholder URL in the three licence files. Neither is approved, and this amendment edits no
licence file ([reconciliation.md](reconciliation.md#amendment-2026-10-27-the-subscription-terms-draft)).

Out of scope here (R7): the spec's Task 3 (extension points) and Task 4 (workspaces), the
`cmd/fugaro/ee_on.go` assembly file and the `ee/register` package (D303 as amended) belong to the
workspaces plan, which takes D302, D303, D308 and D309 as inputs.

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
  review. **`LICENSE` is never edited by any task**, with one named exception: if counsel (L0.1)
  names an entity whose name must replace the copyright line (reconciliation F27 fact 4), that edit
  is made only on the owner's explicit go, in its own commit, which updates the hash
  `TestDocsLicensingRootLicenceUnchanged` pins.

## Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| `LICENSE` is byte-identical to today's (O2: GitHub keeps reporting MIT; the website's `licenceIdentifier()` keeps reading `MIT License`) | L1.3 `TestDocsLicensingRootLicenceUnchanged`; L1.5 `faq-community.mjs` |
| `LICENSING.md` states the split (`ee/` excluded and reserved, `.agents/` third-party, trademark pointer) and is linked from README and the website card | L1.3 `TestDocsLicensingStatement`; L1.5 |
| No non-`ee/` Go file of any build tag imports the ee path (matched by path segment) except `cmd/fugaro/ee_on.go`, which must carry `//go:build ee` and blank-import only `…/ee/register`; no symlink, `//go:embed`, `//go:generate`, cgo include, `replace` or nested module reaches into `ee/` | L1.1 `TestCoreNeverImportsEE`, `TestCoreNeverEmbedsEE`, `TestNoModuleRouteIntoEE` |
| Every `ee/` source file (any file but `README.md` and `LICENSE`) starts with the two-line notice, and `ee/LICENSE` exists whenever one source file does (R3, amended) | L1.1 `TestEEFilesCarryTheNotice`, `TestEESourceNeedsTheLicence` |
| The core builds and tests with `ee/` deleted; the only tests skipped there are the ones that read the real `ee/` through the one helper | L1.2 `TestEEReadersAreListed`; L1.7 `TestDocsCoreWithoutEEWorkflow`; run after L1.8 |
| The release build (D303 as amended): every goreleaser build's `main` is `./cmd/fugaro`; its build tags are empty at 0.11.0; a non-empty effective tag set may hold only `ee`, and only when the cask `license`, the plugin manifest `license`, README, `LICENSING.md` and the website elements name the enterprise licence (`LicenseRef-Fugaro-Enterprise` and `ee/LICENSE`); `before.hooks` is an exact list; no archive file glob reaches `ee/` | L2.1 `TestGoreleaserReleaseBuild`, `TestEETagNeedsTheLabels` |
| Both website licence elements (the Licence card and the `#licence` section) state the split and link `LICENSING.md`; "the only text that governs" is gone | L1.5 |
| `ee/README.md` says the folder is reserved and carries no licence yet, until L4.1 | L1.2 creates it, L4.1 switches it |
| The CLI archive carries a notice for every linked third-party module | L2.1 `TestThirdPartyNoticesCoverEveryLinkedModule` |
| The packed console carries `LICENSE` and one notices file, `console/THIRD_PARTY_NOTICES.md`, generated from what the bundler walks **and** copies | L2.2 `packaging-licence.test.ts` |
| Released versions stay MIT, said in the README Licensing section, `LICENSING.md` and the 0.11.0 notes | L1.3 (both tested), L1.9 |

**Attacker's view.** The boundary tests are the only guard between commercial and MIT code in this
repository; a build-tagged core file importing `ee/` is the case `go list -deps` misses, so L1.1
parses every file regardless of tags and its self-test plants one. A contributor who removes the
test is caught by review, not by the test: L1.6's rule file names it. Adding `ee` to the release
build's tags without the label change is refused by L2.1's coupling test, which reads the
**effective** tag set (goreleaser's `tags`, `flags`, `env` and `overrides`, and the release
workflow's environment), not one key; a second assembly file, one whose constraint is anything but
exactly `ee`, or a `//go:linkname` onto the ee path is refused by L1.1. The three published images
(`images/base/Dockerfile`, `images/history/Dockerfile`, `images/credentials/Dockerfile`) build their
own `fugaro` with `go build` and no tag: a second release build, untagged by construction, which
the workspaces plan must decide about (T3 (e)). **Not covered, by D309's
accepted Go-only scope:** the image Dockerfiles (`images/`) and the Terraform sources
(`deploy/terraform/`) could name `ee/` without any test noticing; that scope is extended with the
first non-Go file under `ee/`.

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

**Amended 2026-10-27: L0.1's CLA-text item is drafted.** The agreement's text is
[CLA.md](CLA.md), drafted on the owner's request of 2026-10-26/27 as one Markdown document for a
single click-through signature in CLA Assistant, and **not yet read by counsel**: counsel reviews
it under L0.1 before L0.2 installs it. L0.2 is the owner's CLA Assistant install, in these steps:

- [ ] Create the Gist from `CLA.md` as its top note says (only the agreement text), and copy the
  file's **Raw** URL.
- [ ] Sign in to <https://cla-assistant.io> with GitHub.
- [ ] **Configure CLA**: choose the repository `dimipaun/fugaro` and paste the Gist's raw URL.
- [ ] Allow-list `fugaro-dev[bot]` and `dependabot[bot]` (bots do not sign).
- [ ] Make the CLA status visible on pull requests (the app's commit status and comment).
- [ ] Open a test pull request from a second account the owner controls: the app asks for a
  signature; a bot-authored pull request passes without one.
- [ ] Optional, a repository setting the owner clicks: require the CLA check on `main` in the
  branch protection rules.
- [ ] Replace `[CLA URL TO BE PUBLISHED]` in `console/LICENSE.md`, `mobile/LICENSE.md` and
  `ee/LICENSE` with the Gist's URL (a separate change, after the Gist exists).

### Slice L1 — the 0.11.0 skeleton (D306)

#### L1.1 — The boundary and notice tests
- **Files:** `internal/eeboundary/boundary_test.go` (new; a test-only package), modelled on
  `internal/creds/boundary_test.go`.
- **Interfaces:** consumes the module tree; produces the rule R6 and R3 as tests.
- **Schema/rules/config change:** none.
- **Tests** (tightened after the round-1 review):
  - `TestCoreNeverImportsEE`: parses imports with `go/parser` in **every** `.go` file outside
    `ee/`, at any depth and with every build tag, skipping **no** directory: `node_modules/` at any
    depth, `testdata/`, `.agents/` and `_`/`.` directories are walked like any other, unlike the
    creds walker, so a file the go tool would not build today still cannot carry the import.
    **The one exception (D303 as amended):** `cmd/fugaro/ee_on.go` exactly, whose build
    constraint is `//go:build ee`, whose package is `main`, and whose only import of the ee path
    is the blank import `_ "github.com/dimipaun/fugaro/ee/register"`. Its constraint is parsed
    with `go/build/constraint` from the `//go:build` line above the package clause and must be
    **exactly** the expression `ee` (no `||`, no `!`, no other term; a legacy `// +build` line alone
    does not count). The `ee` tag may appear in **no other** core file's constraint, and no core
    file may carry a `//go:linkname` whose target is on the ee path. The ee path is matched **by path segment**: an
    import equal to `github.com/dimipaun/fugaro/ee` or starting with
    `github.com/dimipaun/fugaro/ee/` (so `…/eevee` is not a match). It also refuses any symlink
    outside `ee/` that resolves into `ee/` (the walker does not follow symlinks, and the tree
    already has symlinks under `.claude/skills/`).
  - `TestCoreNeverEmbedsEE`: no `//go:embed` pattern resolves under `ee/`.
  - `TestNoModuleRouteIntoEE`: no `go.mod` or `go.work` under `ee/`; no `replace` directive in any
    `go.mod` (or `go.work`) whose target is `ee/` or under it; no `//go:generate` line outside
    `ee/` naming an `ee/` path; no cgo `#include` or `#cgo` flag outside `ee/` reaching into
    `ee/`.
  - `TestEEFilesCarryTheNotice`: R3's exact rule (reconciliation R3) over the **tracked** files
    under `ee/` (`git ls-files -s ee/`, so an untracked `.DS_Store` or build output does not fail
    it, and mode `120000` marks a symlink, which is refused); nothing tracked is skipped
    (dot-directories, `_` directories and `testdata/` included), except that a tracked file that
    no longer exists on disk is skipped on `os.IsNotExist`, as
    `internal/cli/docs_merge_markers_test.go` does, so the core-without-ee workflow, which deletes
    `ee/` without committing, does not fail it;
    only the exact paths `ee/README.md` and `ee/LICENSE` are exempt; the comment syntax is chosen
    by file type; a `#!` file carries the notice on lines 2 and 3; a format with no comments
    (`.json`, `go.sum`, binaries, unknown types) needs a sibling `<name>.license` carrying the
    notice, or it is refused.
  - `TestEESourceNeedsTheLicence`: if any non-exempt file exists under `ee/`, `ee/LICENSE` exists.
    Both pass for an absent `ee/` and for a reserved `ee/` holding only `README.md`.
  - `TestEEBoundaryScanCatchesPlantedViolations`: a `t.TempDir()` tree (the notice rule's scan
    takes the file list as input, so the plants hand it theirs) with one plant per rule,
    each reported: a `//go:build ee` core file importing the ee path; an import of `…/ee` exactly;
    a core symlink into `ee/`; a `.go` file under a nested `web/node_modules/x/` importing the ee
    path; an embed into `ee/`; `ee/go.mod`; a root `go.mod` `replace` into
    `ee/`; a `//go:generate` naming `ee/`; a cgo `#include` into `ee/`; an `ee/` source file
    without the notice; an `ee/sub/README.md` without it; a file under `ee/.hidden/`; a file under
    `ee/testdata/`; a shebang script with the notice on line 1 instead of line 2; an `ee/x.json`
    with no sibling `.license`; a noticed file while `ee/LICENSE` is missing. Negative plants that
    must **not** be reported: an import of `…/eevee`, a reserved tree with only `ee/README.md`, a
    shebang script with the notice on lines 2 and 3, and a correct `cmd/fugaro/ee_on.go`. Plants
    for the exception, each reported: a second file `cmd/fugaro/ee_more.go` with the tag importing
    `ee/register`; `cmd/fugaro/ee_on.go` without `//go:build ee`; `ee_on.go` importing an ee
    package other than `ee/register`, or `ee/register` by a non-blank name; an `ee_on.go`-named file
    in another directory; an import of `ee/register` from `internal/cli` under the tag;
    `ee_on.go` with `//go:build ee || linux`; with `//go:build !ee`; with the constraint placed
    after the package clause; with only a legacy `// +build ee` line; another core file whose
    constraint mentions `ee` without importing anything; a core `//go:linkname` onto
    `github.com/dimipaun/fugaro/ee/register.x`.
- **Command:** `go test ./internal/eeboundary/ -run 'TestCoreNeverImportsEE|TestCoreNeverEmbedsEE|TestNoModuleRouteIntoEE|TestEEFilesCarryTheNotice|TestEESourceNeedsTheLicence|TestEEBoundaryScanCatchesPlantedViolations' -count=1`
- **Red step:** write `TestEEBoundaryScanCatchesPlantedViolations` first against a scan stub that
  returns nothing; every positive plant fails.
- **Depends on:** nothing. **Reviewer tier:** standard. **OWNER-RUN:** no.
- **Commit:** `eeboundary: core never imports or embeds ee/, and every ee/ file carries its notice`

#### L1.2 — The reserved `ee/` (re-sliced 2026-10-24)
- **Files:** `ee/README.md` (new: the folder is reserved for net-new organisation features under
  their own licence; it carries **no licence yet** and no code; `ee/LICENSE` arrives only when
  counsel's text is approved; core never imports `ee/`; each future file carries the R3 notice);
  `internal/cli/docs_ee_reserved_test.go` (new); `internal/cli/docs_ee_repo_test.go` (new: the
  **one helper** through which any test reads the real repository's `ee/`, `readRealEE(t, rel)`,
  and the constant `eeReadingTests`, the exact list of the tests that call it).
- **Interfaces:** produces the file L1.3's `LICENSING.md` and L1.5's page point at, and the helper
  and list that L1.3, L1.7 and L4.1 use.
- **Tests:** `TestDocsEEReserved` (through `readRealEE`: `ee/README.md` exists, says reserved and
  that no licence file exists yet; `ee/` holds no other file; `ee/LICENSE` is absent);
  `TestEEReadersAreListed` (both ways: every top-level test in `internal/cli` that calls
  `readRealEE` is in `eeReadingTests`, and every name in `eeReadingTests` is such a test; and no
  `_test.go` file anywhere in the module, outside `internal/eeboundary/` and this helper file,
  passes a path that starts at the repository's `ee` element to a **filesystem call**
  (`os.ReadFile`, `os.Open`, `os.Stat`, `os.Lstat`, `os.ReadDir`, `filepath.Join`,
  `filepath.Walk`/`WalkDir`, any `fs.*` function), so a test cannot read the real `ee/` around the
  helper; a test that only **matches content** naming `ee/` (a link in `LICENSING.md`, the
  workflow's `rm -rf ee`, an archive glob) is not a read and is allowed). L1.1's tests pass on the reserved tree.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run 'TestDocsEEReserved|TestEEReadersAreListed|Docs' -count=1 -timeout 900s`
- **Red step:** `TestDocsEEReserved` before `ee/README.md` exists; `TestEEReadersAreListed` against
  a list missing `TestDocsEEReserved`.
- **Depends on:** nothing. **Reviewer tier:** standard. **OWNER-RUN:** no.
- **Commit:** `ee: the folder is reserved and carries no licence yet`

#### L1.3 — `LICENSING.md`, the README Licensing section and the `.agents/` notice (re-sliced 2026-10-24)
- **Files:** `LICENSING.md` (new, root: the core is under the MIT licence in `LICENSE`; `ee/` is
  excluded and reserved, as `ee/README.md` says; `.agents/` is third-party code under its own
  licence in `.agents/LICENSE`; every release before 0.11.0 is MIT (R4); a pointer to the
  README's trademark paragraph); `README.md` (`## License` becomes `## Licensing`: two or three
  sentences, one of them that every release before 0.11.0 is MIT (R4), links to `LICENSE` and
  `LICENSING.md`, the trademark paragraph (D310));
  `.agents/LICENSE` (new: the vendored project's upstream licence text, fetched from its
  repository by the run, the upstream commit named in the commit body);
  `internal/cli/docs_licensing_test.go` (new); `internal/cli/docs_ee_repo_test.go`
  (`TestDocsLicensingStatement` joins `eeReadingTests`: it checks the `ee/README.md` link through
  `readRealEE`). **`LICENSE` is not edited (O2).**
- **Tests:** `TestDocsLicensingRootLicenceUnchanged` (`LICENSE`'s SHA-256, after normalising CRLF
  to LF so a Windows checkout passes, equals today's at `e924481cd`); `TestDocsLicensingStatement` (`LICENSING.md` names `ee/` as excluded and reserved,
  `.agents/` as third-party, the earlier-releases sentence, and links `LICENSE`, `ee/README.md`,
  `.agents/LICENSE` and the README's trademark paragraph); `TestDocsLicensingReadmeSection` (the
  section links `LICENSE` and `LICENSING.md` and carries the earlier-releases sentence and the
  trademark paragraph);
  `TestDocsLicensingVendoredNotice` (`.agents/LICENSE` exists and carries an MIT permission
  notice).
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run 'TestDocsLicensing|TestEEReadersAreListed|Docs' -count=1 -timeout 900s`
- **Red step:** the three new-file tests against today's tree (`TestDocsLicensingRootLicenceUnchanged`
  passes from the start: it is the guard).
- **Depends on:** L1.2 (`LICENSING.md` links `ee/README.md`; the helper and list). **Reviewer
  tier:** standard, plus the owner reads `LICENSING.md`'s wording. **OWNER-RUN:** no.
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
  **OWNER-RUN:** no.
- **Commit:** `contributing: outside contributions need a signed CLA`

#### L1.5 — The website Community page's two licence elements
- **Files:** `website/src/pages/community.astro`, **both** elements that state the licence:
  (a) the **Licence card** in the community grid (its link to `LICENSE` stays; its note becomes
  "the {licence} licence for the core; `LICENSING.md` says which parts are not", linking
  `LICENSING.md`); (b) the **`#licence` section** ("Fugaro is released under the {licence}
  licence: `LICENSE` is the whole of it, and it is the only text that governs…" becomes "Fugaro's
  core is released under the {licence} licence in `LICENSE`; `LICENSING.md` says which parts of the
  repository are not, and the reserved `ee/` directory is one", both linked).
  `licenceIdentifier()` is unchanged and still reads `MIT License`, because `LICENSE` is untouched.
  `website/test/faq-community.mjs` (the pinned facts move with the copy).
- **Tests:** in `faq-community.mjs`, the identifier test stays; new assertions that **both**
  elements link `LICENSING.md` and that the page no longer says "the only text that governs"; the
  link-spacing test holds for every new link.
- **Command:** `cd website && npm run check`, then `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1 -timeout 900s`
- **Red step:** the new assertions against today's page (no `LICENSING.md` link in either element;
  the "only text that governs" sentence present).
- **Depends on:** L1.3 (`LICENSING.md` exists). **Reviewer tier:** standard. **OWNER-RUN:** no.
  Deploying the site is a separate, authorised step (`.claude/rules/website.md`).
- **Commit:** `website: the community card states the core/ee split`

#### L1.6 — The `ee/` path-scoped rule
- **Files:** `.claude/rules/ee.md` (new: loads on `ee/**`; core never imports `ee/`; the R3 notice;
  the test names from L1.1; the private key never enters the repository); `CLAUDE.md` (one row in
  the path-scoped rules table).
- **Tests:** none new; `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1 -timeout 900s`
  (in case a docs test reads the table).
- **Red step:** none (a rule file); the reviewer checks each named test exists.
- **Depends on:** L1.1 (the test names). **Reviewer tier:** standard. **OWNER-RUN:** no.
- **Commit:** `rules: ee/ has its own path-scoped rule`

#### L1.7 — Stage the "core without `ee/`" workflow (rewritten after the round-2 review)
- **Files:** `docs/pending-workflows/core-without-ee.yml` (new: on push to `main` and
  `workflow_dispatch`, `contents: read`, every action pinned by full commit SHA, Go from `go.mod`,
  a job-level `timeout-minutes: 90`; it deletes `ee/`, then, **with the default build tags** (no
  `ee` tag, so `cmd/fugaro/ee_on.go` is not compiled), runs `go build ./...` and
  `go vet ./...`, then **step A**
  `go test -timeout 30m -skip '^(<eeReadingTests joined by |>)$' $(go list ./... | grep -v internal/runner)`
  and **step B** `go test -race -timeout 40m ./internal/runner/...` with no skip, so the runner
  is not run twice and has the bound `CONTRIBUTING.md` gives it. The spec asks that the core "pass
  its tests" without `ee/`; the cost is a run about as long as `ci.yml`'s test job, on pushes to
  `main` only; its `name:` is `core-without-ee`, so `gh workflow run core-without-ee` finds it);
  `docs/pending-workflows/README.md` (its opening count of pending files and a
  ``## `core-without-ee.yml` `` section, backticks in the heading like every other section there,
  with a status line and the copy-in commands); `website/README.md` (its sentence naming every
  pending workflow, which `TestConsoleLinuxStagingTellsTheOwnerWhatToCopyWhere` and
  `website/test/readme.mjs` hold to the directory);
  `internal/cli/docs_core_without_ee_workflow_test.go` (new).
- **Precedent:** `shared-tests.yml`'s (the staged copy is removed at copy-in, and the test then
  reads `.github/workflows/`). The test reads `docs/pending-workflows/core-without-ee.yml` while
  it exists and `.github/workflows/core-without-ee.yml` otherwise, and fails if neither exists.
- **Tests:** `TestDocsCoreWithoutEEWorkflow` (the file parses; its `name:` is `core-without-ee`;
  triggers only on push to `main` and dispatch; holds no secret; pins every action by SHA; sets `timeout-minutes`; deletes `ee/` before
  building; step A's `-skip` alternation equals `eeReadingTests` exactly and step A excludes
  `internal/runner`; step B runs `./internal/runner/...` at `-timeout 40m` with no `-skip`; no
  step passes `-tags ee` (or any tag list holding `ee`); no
  `internal/eeboundary` test is ever in the skip list, since those plant their own trees under
  `t.TempDir()`; the README section carries the copy-in), plus the existing pending-workflow tests
  (`internal/cli/docs_desktop_console_test.go`'s `pendingWorkflows` derives the set from the
  directory and checks the README's opening names it).
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run 'TestDocsCoreWithoutEE|TestEEReadersAreListed|TestConsoleLinuxStagingTellsTheOwnerWhatToCopyWhere|Pending|Docs' -count=1 -timeout 900s`,
  then `cd website && npm run check`
- **Red step:** the new test before the file exists.
- **Depends on:** L1.1 (the workflow runs its tests), L1.2 and L1.3 (`eeReadingTests` is final for
  0.11.0 only after both). **Reviewer tier:** standard. **OWNER-RUN:** no.
- **Commit:** `pending-workflows: the core builds and tests with ee/ deleted (staged for the owner)`

#### L1.8 — Copy the staged workflow in (OWNER-RUN)
- **What the owner types**, from a clean checkout of `main` after L1.7 merges:
  ```sh
  cp docs/pending-workflows/core-without-ee.yml .github/workflows/core-without-ee.yml
  git rm docs/pending-workflows/core-without-ee.yml
  git add .github/workflows/core-without-ee.yml
  # docs/pending-workflows/README.md: lower the opening count, and set the status line of the
  #   ## `core-without-ee.yml` section to "applied, not pending" (shared-tests.yml's step 3)
  # website/README.md: drop core-without-ee.yml from the sentence naming the pending workflows
  git commit -am "workflows: the core builds and tests with ee/ deleted"
  git push
  gh workflow run core-without-ee --ref main
  ```
  and see it green. `TestDocsCoreWithoutEEWorkflow` then reads the applied copy.
- **Depends on:** L1.7 merged.

#### L1.9 — The 0.11.0 release-notes line
- **Files:** none in this plan. The 0.11.0 release PR (`/new-release 0.11.0`) adds one Highlights
  bullet to `docs/releases/v0.11.0.md` and folds the same fact into the version's line in
  `website/data/release-summaries.yaml` (no backticks): *Fugaro's core stays MIT, and the
  repository now reserves an `ee/` directory for future organisation features under their own
  licence, described in LICENSING.md; every earlier release stays MIT.* No `### For operators`
  entry (R5).
- **Depends on:** L1.2, L1.3, L1.5 merged. **Done by:** the release PR. **Reviewer tier:** the
  release PR's own review. **OWNER-RUN:** no (the release itself is the owner's go).

#### L1.10 — Write Check 130 (amended 2026-10-24: GitHub's MIT reading and `LICENSING.md` only)
- **Files:** `docs/gcp-live-checklist.md` (a `## Check 130` section and its register row: after
  L1.3 merges, the owner opens the repository page and confirms GitHub still reports **MIT**, then
  opens `LICENSING.md` on GitHub and confirms it renders with working links; as amended by O2).
- **Tests:** the checklist's own docs tests.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1 -timeout 900s`
- **Red step:** none beyond the register's tests, which fail if the row and section disagree.
- **Depends on:** nothing. **Reviewer tier:** standard. **OWNER-RUN:** no (L1.11 is).
- **Commit:** `checklist: Check 130, the licence detectors after the open-core skeleton`

#### L1.11 — Run Check 130 (OWNER-RUN)
- **What:** after L1.3 merges, look at the repository's licence badge and at `LICENSING.md`, and
  write the result into the Check 130 section. A reading other than MIT is a finding against O2's
  premise and goes back to the owner.
- **Depends on:** L1.3 merged, L1.10 merged.

### Slice L2 — third-party notices (D307)

#### L2.1 — Notices in the CLI archive
- **Files:** `scripts/third-party-notices.sh` (new: `go list -deps` over `./cmd/fugaro` **once per
  target of the build matrix** (`GOOS` darwin and linux × `GOARCH` amd64 and arm64, as
  `.goreleaser.yaml` builds), the union of the four module sets, then each
  non-main module's `LICENSE*`/`COPYING*`/`NOTICE*` from its module directory, into one file);
  `scripts/notices_test.go` (new); `.goreleaser.yaml` (a `before.hooks` line running the script,
  after `go mod verify`, and the file added to `archives[0].files`); `docs/release.md` (one sentence saying the archive
  carries it).
- **Tests:** `TestThirdPartyNoticesCoverEveryLinkedModule` (runs the script into `t.TempDir()`
  against the already-downloaded module cache; every module `go list -deps ./cmd/fugaro` names for
  each of the four targets has a section with its licence text, and `NOTICE` contents appear where
  a module has one; a module reached only under one target, such as a `golang.org/x/sys` build for
  darwin, is covered);
  `TestGoreleaserArchivesShipNotices`; `TestGoreleaserReleaseBuild` (D303 as amended: every entry
  in `builds` has `main: ./cmd/fugaro`; `before.hooks` equals an exact list (`go mod verify` and
  the notices script); no `archives[].files` entry or glob reaches `ee/`; the **effective tag set**
  is the union of every build's `tags`, any `-tags`/`-tags=` value in its `flags`, any `-tags` in
  `GOFLAGS` in its `env` or in the top-level `env`, the same three in each of its `overrides[]`,
  and `GOFLAGS` in the `env:` of every goreleaser step in `.github/workflows/release.yml`; that set
  is empty at 0.11.0, or holds only `ee`); `TestEETagNeedsTheLabels` (the coupling: if the effective
  tag set holds `ee`, then each label **names the enterprise licence**, defined as: the Homebrew
  cask `license` and `plugin/.claude-plugin/plugin.json`'s `license` are SPDX expressions holding
  `LicenseRef-Fugaro-Enterprise`, and the README Licensing section, `LICENSING.md` and the
  website's two licence elements each contain `LicenseRef-Fugaro-Enterprise` or link `ee/LICENSE`;
  at 0.11.0 the set is empty and the test holds trivially. Plants refused: `tags: [ee]` with MIT
  labels; `flags: [-tags=ee]`; `GOFLAGS=-tags=ee` in a build's `env`, in the top-level `env`, in an
  `overrides[]` entry, and in the release workflow's goreleaser step. Positive plant accepted:
  `tags: [ee]` with every label naming the enterprise licence).
- **Command:** `go test ./scripts/ -run 'TestThirdPartyNotices|TestGoreleaserArchivesShipNotices|TestGoreleaserReleaseBuild|TestEETagNeedsTheLabels' -count=1`,
  then `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1 -timeout 900s` (for `docs/release.md`).
- **Red step:** the coverage test against a stub script that writes an empty file.
- **Depends on:** nothing. No new dependency (the Go toolchain and the shell only).
  **Reviewer tier:** standard. **OWNER-RUN:** no. Exercised for real only by the 0.11.0 release run.
- **Commit:** `release: the CLI archive carries its third-party notices`

#### L2.2 — `LICENSE` and notices in the packed console (rewritten after the round-2 review)
- **Files:** `console/package.json` (`build.extraResources`: the repository's `LICENSE`;
  `build.files`: `THIRD_PARTY_NOTICES.md`, the way the dependency graph's GV2.2 packs it);
  `console/tools/bundle-renderer/index.ts` (records every file it **walks** and every file it
  **copies** verbatim into one inputs list beside `dist/bundle/console.js`; today it walks only
  the console's own sources, because it refuses every bare specifier and never reads
  `node_modules/`, and the files it copies verbatim (`copyFileSync`) are today only the console's
  own HTML documents, none from `node_modules/`); `console/scripts/notices.mjs` (new: from that list, every
  input under `node_modules/` maps to its package and that package's licence file, written to
  `console/THIRD_PARTY_NOTICES.md`); `console/THIRD_PARTY_NOTICES.md` (generated and committed;
  **the one notices file**, the same path GV2.2 names); `console/test/unit/packaging-licence.test.ts`
  (new). The console has no `dependencies`, only `devDependencies`, which is why the source is
  the bundler's inputs and not the lockfile.
- **elkjs.** GV2.2 (`docs/console-dependency-graph/implementation-plan.md`) copies
  `node_modules/elkjs/lib/elk-worker.min.js` verbatim into `dist/bundle/`. Recorded as a copied
  input, it maps to `elkjs` and its EPL-2.0 licence text lands in the notices file: the two plans
  converge on one file and one mechanism.
- **Cross-plan overlap.** L2.2 and GV2.2 both write `console/package.json`,
  `console/tools/bundle-renderer/index.ts`, `console/THIRD_PARTY_NOTICES.md` and the build-files
  test. Whichever merges **second** rebases: if GV2.2 lands first, L2.2 replaces its hand-written
  notices file with the generated one (the elkjs section must come out the same) and records the
  existing copy; if L2.2 lands first, GV2.2 adds its worker to the bundler's copy list and
  regenerates the file instead of writing it by hand.
- **Tests:** `packaging-licence.test.ts` (the build config ships `LICENSE` and the notices file; the
  inputs list holds every walked and every copied file; a planted copied
  `node_modules/elkjs/lib/elk-worker.min.js` with an EPL-2.0 `LICENSE.md` appears in the notices
  with its text; a devDependency that is neither walked nor copied does not; the committed notices
  file equals a fresh generation).
- **Command:** `cd console && npm run typecheck && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 packaging-licence`
- **Red step:** the planted-copy case before the bundler records copies.
- **Depends on:** nothing in this plan (see the cross-plan overlap). **Reviewer tier:** standard
  (the bundler change is reviewed against `.claude/rules/console.md`). **OWNER-RUN:** no. Ships
  with the next `console-vX.Y.Z`; the owner's console release confirms the files inside the
  packed app (U4).
- **Commit:** `console: the packed app carries LICENSE and its notices`

### Slice L3 — design only, handed to the workspaces plan (not built in 0.11.0)

The key check lives in `ee/` and gates at the registration point (`ee/register`); nothing in core
reads, stores or knows about keys (D308 with D303 as amended).

#### L3.1 — The licence-key design note
- **Files:** `docs/licensing/licence-key.md` (new): D308 as a design (Ed25519 over a JSON claim
  set: licensee, features, expiry, issued; base64url; verified offline in `ee/` code with the
  public key compiled in; the signature is verified over the **raw key bytes before** any JSON is
  parsed; the key carries a **key id** so the compiled-in public key can be rotated; a grace period
  after expiry; no revocation; no phone-home; stated as a known limit, an offline expiry can be
  beaten by setting the machine's clock back); where the key
  is stored is left to the workspaces plan; minting is OWNER-RUN with a key kept off every machine
  an agent reaches (R8); U5 (secret scans) is a check the first implementing task runs.
- **Tests:** `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1 -timeout 900s`.
- **Red step:** none (a design note, no code); the reviewer checks that the raw-bytes, key-id and
  clock-set-back rules and the "nothing in core" rule are each stated.
- **Depends on:** nothing. **Reviewer tier:** opus + mandatory independent review (a trust
  boundary, even on paper). **OWNER-RUN:** no.
- **Commit:** `docs(licensing): the licence-key design (D308), not built`

### Slice L4 — the enterprise licence, after counsel (not in 0.11.0)

#### L4.1 — `ee/LICENSE` from the approved text (new 2026-10-24)
- **Files:** `ee/LICENSE` (new), `ee/README.md` (from "reserved, no licence yet" to "licensed
  under `ee/LICENSE`"), `LICENSING.md` (the `ee/` line names `ee/LICENSE`),
  `internal/cli/docs_ee_reserved_test.go` (its reserved assertions become licensed ones),
  `internal/cli/docs_licensing_test.go` (the statement test follows),
  `internal/cli/docs_ee_repo_test.go` (`eeReadingTests` follows the renamed and new tests),
  `docs/pending-workflows/core-without-ee.yml` (the applied workflow, staged again with step A's
  `-skip` matching the new list), `docs/pending-workflows/README.md` (its section, now pending
  again) and `website/README.md` (its pending-workflows sentence names the file again);
  `website/src/pages/community.astro` and `website/test/faq-community.mjs` (the "reserved" wording
  from L1.5 becomes "under `ee/LICENSE`"); `CONTRIBUTING.md` and
  `internal/cli/docs_contributing_cla_test.go` (L1.4's "`ee/` takes no contributions while it is
  reserved" becomes the licensed rule). Without the staged update the applied workflow turns red, because a new test reads the
  `ee/` the workflow deletes, and no agent can change `.github/workflows/`.
- **Interfaces:** consumes L0.1's approved text verbatim.
- **Tests:** `TestDocsEELicenceIsTheApprovedText` (the file's SHA-256 equals a **constant in the
  test**, the hash of the text the owner approved; the commit body only restates it, since a test
  cannot read a commit message); `TestDocsEEReserved` renamed `TestDocsEELicensed`;
  `TestDocsLicensingStatement` updated.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run 'TestDocsEE|TestDocsLicensing|TestEEReadersAreListed|TestDocsCoreWithoutEE|TestDocsContributingCLA|TestConsoleLinuxStagingTellsTheOwnerWhatToCopyWhere|Docs' -count=1 -timeout 900s`,
  then `cd website && npm run check`
- **Red step:** `TestDocsEELicenceIsTheApprovedText` before the file exists.
- **Depends on:** L0.1, L1.2, L1.3, L1.8 (the applied workflow exists to be updated). **Reviewer
  tier:** opus + mandatory independent review (legal text; the reviewer diffs it against L0.1's
  approved copy). **OWNER-RUN:** the approval itself.
- **Commit:** `ee: the enterprise licence, as approved`

#### L4.2 — Copy the updated workflow in (OWNER-RUN)
- **What the owner types**, right after L4.1 merges (the applied workflow is red until then):
  ```sh
  cp docs/pending-workflows/core-without-ee.yml .github/workflows/core-without-ee.yml
  git rm docs/pending-workflows/core-without-ee.yml
  git add .github/workflows/core-without-ee.yml
  # docs/pending-workflows/README.md: lower the opening count, and set the
  #   ## `core-without-ee.yml` section's status line to "applied, not pending" again
  # website/README.md: drop core-without-ee.yml from the pending-workflows sentence again
  git commit -am "workflows: core-without-ee skips the tests that read the licensed ee/"
  git push
  gh workflow run core-without-ee --ref main
  ```
- **Depends on:** L4.1 merged.

#### L4.3 — Publish the Subscription Terms (OWNER-RUN, after counsel)
- **What:** counsel reviews [subscription-terms.md](subscription-terms.md); the owner fills every
  bracketed commercial term, removes the draft status line and the drafting notes, and publishes
  the approved text as a page on the website (`fugaro.dev`, at a stable path chosen then), the
  URL the licence files will name. The two question lists stay in the repository, not on the page.
- **Depends on:** counsel's review; the owner's commercial answers (the draft's "Questions for the
  owner").
- **OWNER-RUN:** yes (the approval and the publication); an agent may prepare the website page
  once the approved text exists.

#### L4.4 — Fill the Subscription Terms URL in the licence files
- **Files:** `ee/LICENSE`, `console/LICENSE.md`, `mobile/LICENSE.md` (`[URL TO BE PUBLISHED]`
  becomes L4.3's URL, the only change), `internal/cli/docs_ee_reserved_test.go` (its pinned
  placeholder follows), `LICENSING.md` and `ee/README.md` (they stop saying the Subscription Terms
  are not published yet; the CLA's placeholder is its own step).
- **Depends on:** L4.3 published. A separate change from the draft on purpose: the licence files
  are counsel's text, and they change only once the document they name exists.
- **Reviewer tier:** mandatory independent review (licence text). **OWNER-RUN:** no, after the
  owner's go.

### Slice L5 — the release that ships the first ee feature (not in 0.11.0; added 2026-10-24 for D303's amendment)

#### L5.1 — Counsel on the mixed-licence binary (OWNER-RUN)
- **What:** with counsel, settle the wording every label carries once the release build sets the
  `ee` tag (T3 (b)), and whether a pure-MIT artifact is also published (T3 (c)).
- **Depends on:** L4.1 (an `ee/LICENSE` exists to point at).

#### L5.2 — Label the mixed-licence release
- **Files:** `.goreleaser.yaml` (`builds[0].tags: [ee]`; the cask's `license` per L5.1),
  `plugin/.claude-plugin/plugin.json` (`license` per L5.1; `plugin/` tests re-run), `README.md`
  (the Licensing section says the released binary carries `ee/` code under `ee/LICENSE`),
  `LICENSING.md`, `website/src/pages/community.astro` and `website/test/faq-community.mjs` (both
  licence elements), `internal/cli/docs_licensing_test.go`.
- **Tests:** `TestEETagNeedsTheLabels` (from L2.1) now runs non-trivially and passes;
  `TestDocsLicensingReadmeSection` and `TestDocsLicensingStatement` updated; the website's pinned
  facts updated; `go test ./plugin/ -run 'TestPlugin' -count=1`.
- **Command:** `go test ./scripts/ -run 'TestEETagNeedsTheLabels|TestGoreleaserReleaseBuild' -count=1`,
  `GOFLAGS=-p=2 go test ./internal/cli/ -run 'TestDocsLicensing|TestEEReadersAreListed|Docs' -count=1 -timeout 900s`,
  `cd website && npm run check`.
- **Red step:** `tags: [ee]` alone, before any label changes: `TestEETagNeedsTheLabels` fails.
- **Depends on:** L5.1, L2.1, and the workspaces plan's first ee feature with its
  `cmd/fugaro/ee_on.go`. **Reviewer tier:** opus + mandatory independent review (licence labels).
  **OWNER-RUN:** no (L5.1 is).
- **Commit:** `release: the binary carries ee/ code, and every label says so`

## Parallel execution

**Edges** (from the Depends-on lines): L0.1 → L0.2; L1.2 → L1.3; L1.3 → L1.5; L1.1 → L1.6;
L1.1, L1.2, L1.3 → L1.7; L1.7 → L1.8; L1.2, L1.3, L1.5 → L1.9; L1.3, L1.10 → L1.11;
L0.1, L1.2, L1.3, L1.8 → L4.1; L4.1 → L4.2; L4.3 → L4.4; L4.1 → L5.1; L5.1, L2.1 → L5.2 (and the workspaces
plan's first ee feature).

| Level | Tasks (run concurrently) |
| --- | --- |
| 0 | L0.1 (owner), L1.1, L1.2, L1.4, L1.10, L2.1, L2.2, L3.1 |
| 1 | L0.2 (owner), L1.3, L1.6 |
| 2 | L1.5, L1.7, L1.11 (owner) |
| 3 | L1.8 (owner), L1.9 (the release PR) |
| 4 | L4.1 (after counsel), L4.3 (owner, after counsel) |
| 5 | L4.2 (owner), L4.4, L5.1 (owner) |
| 6 | L5.2 (with the first ee feature's release) |

**Ready for the 0.11.0 tag** means these are merged: **L1.1, L1.2, L1.3, L1.4, L1.5, L1.6, L1.7,
L2.1 and L1.9** (L1.9 is the release PR itself). **May follow the tag** if the owner says so:
L1.10 (the Check 130 text, an agent's) and the owner-run L1.8, L1.11 and L0.2. L2.2 ships with the console's own release; L3.1, L4.1, L4.2, L5.1 and
L5.2 are not 0.11.0 work. At the 0.11.0 tag the release build sets no `ee` tag (D303 as amended),
which L2.1's test pins.

**Critical path to the 0.11.0 tag:** L1.2 → L1.3 → L1.5 → L1.9, with L1.7 (after L1.3) beside L1.5
in level 2; the website check (~7 minutes) is the longest single step. Counsel (L0.1) and the
licence file (L4.1) are off it by the T1 ruling: 0.11.0 ships `ee/` reserved.

**What must be sequential and why:** L1.3 after L1.2 (`LICENSING.md` links `ee/README.md`, and
L1.3 extends L1.2's reader list); L1.5 after L1.3 (the page links `LICENSING.md`); L1.6 after L1.1
(it names L1.1's tests); L1.7 after L1.1, L1.2 and L1.3 (its workflow runs L1.1's tests and skips
the final reader list); L1.8 after L1.7 (it copies L1.7's file); L4.1 after L1.2, L1.3 and L1.8
(it edits their files and stages an update of the applied workflow) and after counsel; L4.2 after
L4.1; L5.2 after L5.1 and L2.1 (it makes L2.1's coupling test non-trivial).

### Shared-files table (no level has two writers of one file)

| File | Writers (level) |
| --- | --- |
| `LICENSE` | none (O2: never edited; the counsel exception in Conventions is its own owner-approved commit) |
| `LICENSING.md` | L1.3 (1), L4.1 (4), L4.4 (5), L5.2 (6) |
| `README.md` | L1.3 (1), L5.2 (6) |
| `.agents/LICENSE` | L1.3 (1) |
| `ee/README.md` | L1.2 (0), L4.1 (4), L4.4 (5) |
| `ee/LICENSE`, `console/LICENSE.md`, `mobile/LICENSE.md` | L4.1 (4), L4.4 (5) |
| `internal/cli/docs_ee_reserved_test.go` | L1.2 (0), L4.1 (4), L4.4 (5) |
| `internal/cli/docs_ee_repo_test.go` | L1.2 (0), L1.3 (1), L4.1 (4) |
| `internal/cli/docs_licensing_test.go` | L1.3 (1), L4.1 (4), L5.2 (6) |
| `internal/eeboundary/boundary_test.go` | L1.1 (0) |
| `CONTRIBUTING.md`, `internal/cli/docs_contributing_cla_test.go` | L1.4 (0), L4.1 (4) |
| `.github/pull_request_template.md` | L1.4 (0) |
| `website/src/pages/community.astro`, `website/test/faq-community.mjs` | L1.5 (2), L4.1 (4), L5.2 (6) |
| `website/README.md` | L1.7 (2), L1.8 (3, owner), L4.1 (4), L4.2 (5, owner) |
| `plugin/.claude-plugin/plugin.json` | L5.2 (6) |
| `.claude/rules/ee.md`, `CLAUDE.md` | L1.6 (1) |
| `docs/pending-workflows/core-without-ee.yml`, `docs/pending-workflows/README.md` | L1.7 (2), L1.8 (3, owner), L4.1 (4), L4.2 (5, owner) |
| `internal/cli/docs_core_without_ee_workflow_test.go` | L1.7 (2) |
| `.github/workflows/core-without-ee.yml` | L1.8 (3, owner), L4.2 (5, owner) |
| `docs/gcp-live-checklist.md` | L1.10 (0); the owner's result L1.11 (2) |
| `scripts/third-party-notices.sh`, `scripts/notices_test.go`, `docs/release.md` | L2.1 (0) |
| `.goreleaser.yaml` | L2.1 (0), L5.2 (6) |
| `console/package.json`, `console/tools/bundle-renderer/index.ts`, `console/scripts/notices.mjs`, `console/THIRD_PARTY_NOTICES.md`, `console/test/unit/packaging-licence.test.ts` | L2.2 (0); also the dependency graph's GV2.2 (another plan: see L2.2's cross-plan overlap) |
| `docs/licensing/licence-key.md` | L3.1 (0) |
| `docs/licensing/subscription-terms.md` | the 2026-10-27 draft; L4.3 (4, owner) |
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
L0.2, L1.8, L1.11, the approval inside L4.1, L4.2, L4.3 and L5.1, none of which is a rollout step.

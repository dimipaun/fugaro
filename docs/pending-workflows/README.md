# Pending workflow changes

**Five files are pending: `shared-tests.yml`** (the shared package's test job, mobile plan Task P4.3; see `## shared-tests.yml` below), **`console-linux.yml`** (the console's Linux build check, 2026-10-20; see `## console-linux.yml` below), **`console-release-linux.yml`** (the console's signing pipeline with the Linux legs added, 2026-10-20 — it supersedes the staged `console-release.yml`; see `## console-release-linux.yml` below), **`console-windows.yml`** (the console's Windows installer pack, 2026-10-20 — a release-time job, not a pull-request check; see `## console-windows.yml` below) **and `mobile-release.yml`** (the mobile app's build and store submissions, 2026-10-20; see `## mobile-release.yml` below). **Two other files stay staged here by design: `console-tests.yml` and `console-release.yml`.** Both were copied into `.github/workflows/` by the owner, and unlike every other file here their staged copies are kept, because their tests assert the copy under `.github/workflows/` is byte-identical to the staged one (see their sections below). Nothing else is pending: the website's two manual deploy workflows, `website-preview.yml` and `website-promote.yml` (#566), were copied into `.github/workflows/` by the owner (their staged copies are removed); `ci.yml` was copied in on 2026-10-17 (#504, staged copy removed in #508), and `images.yml` was copied in and its staged copy removed in `6b57ba9` (`## images.yml` below is the record of that change, not a pending request). A future workflow change that a run needs is staged here as the real file plus the change, and the owner copies it in.

- **`shared-tests.yml`** — the whole CI job for `shared/`'s tests, mobile plan Task **P4.3**: a `main`-filtered push and pull request under `shared/**`, `contents: read`, `npm ci` and `npm test` on Node 22 and Node 26, and every `shared/*/mutation-proof.sh` in the package, every action pinned by full commit SHA. `shared/` is outside the Go module, so no job in `ci.yml` runs it. Staged by a Fugaro run that cannot push a `.github/workflows/**` change; see `## shared-tests.yml` below, which also records what the job will report on `main` today.
- **`website-preview.yml`** and **`website-promote.yml`** — the website's two new workflows, both `workflow_dispatch`-only: one builds a chosen ref and deploys it to a Firebase Hosting preview channel of the site's own project and prints the preview URL, the other promotes an existing preview channel to live and is the owner's alone (`environment: website-live` plus a guard on `github.actor`). Staged by a Fugaro run that cannot push a `.github/workflows/**` change. **Read `docs/website/preview-promote.md` first**: it carries the owner-only one-time setup (the Workload Identity Federation pool and provider, the deployer service account and its Firebase Hosting roles, the `website-live` environment and the three repository variables) that both files need before they can run at all.
- **`console-linux.yml`** — the console's **Linux build check**: one job on `ubuntu-latest` that packs the AppImage and the `.deb` on every pull request and every push to `main` and uploads them, holding no secret at all. Staged by the 2026-10-20 run that packages the console for Linux (the owner's ruling: Linux for 0.10.0, unsigned). It is its own workflow rather than a second job in `console-tests.yml` because the release gate's required-check list is derived from that file's job set; see `## console-linux.yml` below.
- **`console-release-linux.yml`** — the console's signing, notarisation and publishing pipeline **with the Linux legs added**: a third job that builds and verifies the unsigned AppImage and `.deb`, and a `publish` that merges both legs, signs one merged `console-sha256sums.txt` keyless with cosign and attaches everything to `console-vX.Y.Z`. It supersedes the staged `console-release.yml`, so its copy-in is three commands; see `## console-release-linux.yml` below.
- **`console-windows.yml`** — the console's **Windows pack**, and one of the two files here that are **not** a pull-request check (`mobile-release.yml` below is the other): one job on `windows-latest` that typechecks, runs the unit suite and packs the unsigned NSIS installer **at release time only** — the release tag, or a `workflow_dispatch` before one, and **never on a pull request**, not even path-filtered — and uploads it, holding no secret at all. Staged by the 2026-10-20 run that plans the console's Windows build (the owner's ruling of that date: the console drives the **Linux** `fugaro` inside WSL, unsigned), and re-cut the same day for the owner's answer to that plan's decision D7, whose words were *"a waste of resources; if there are problems we will fix them then"*. **It is red until three things in `console/` land** — the `package:win` script and the `win32` fuse row, the POSIX repairs that let the unit suite run on a Windows runner, and the `win32-x64` Electron checksum pin — which is why it is staged now and copied in later; the file's own header says so first. It is its own workflow rather than a second job in `console-tests.yml` for the reason `console-linux.yml` gives below; see `## console-windows.yml` below.
- **`mobile-release.yml`** — the **mobile app's release**, staged on 2026-10-20 on the owner's rulings of 2026-10-19/20 and reworked on 2026-10-21 after the security review of #777 (the build runs from GitHub Actions on each release tag, and by hand). On every `v[0-9]+.[0-9]+.[0-9]+` tag and on a manual dispatch, **one job per platform** builds on EAS and submits the finished build to TestFlight's internal testers and to Play's internal-testing track, behind the `mobile-release` GitHub environment and its only required reviewer (the owner) — the same click that gates `console-release` and `website-live`. The two jobs are independent, **both are enabled**, and one repository variable (`MOBILE_RELEASE_DISABLED_PLATFORMS`) can turn either off; see `## mobile-release.yml` below, which carries the Android first-release checklist.
- **`images.yml` — applied, not pending.** The signing of the pushed base image by its OCI digest (cosign, keyless) and the nightly-immune `<version>-pinned` tag, plan Task 1 of `docs/plans/2026-10-13-retire-image-mirror.md`, was staged here by a Fugaro run that cannot push a `.github/workflows/**` change, then copied into `.github/workflows/images.yml` and `git rm`'d from here in `6b57ba9`. `## images.yml` below is the record of that change and of the timing it had to meet, not something still to copy.

**`console-tests.yml`** — the desktop console's own tests in CI, staged by Task 1.4 of `docs/plans/2026-10-15-desktop-console.md` and applied by the owner as Task 1.5. **It holds no secret of any kind**, it runs unconditionally on every pull request and every push to `main`, and unlike `images.yml` **its staged copy is kept after the copy-in** (`TestConsoleTestsWorkflowIsStagedAndPinned` asserts the two files are byte-identical from then on): see `## console-tests.yml` below.

**`console-release.yml`** — the desktop console's signing, notarisation and stapling pipeline, staged by Task 4.2 of the same plan and applied by the owner as Task 4.5, together with the protected GitHub environment its secrets live in. **It is a separate file from the tests workflow on purpose, and the split is a security boundary rather than tidiness**: this job reads the Apple signing certificate and the App Store Connect key, and a branch of this repository can open a pull request, so a job holding that material must never sit in a file that runs on every pull request. Its staged copy is kept after the copy-in too: see `## console-release.yml` below.

The two `images.yml` copies that *were* applied — the 0.7.0 hard cut (`838e0b0`, which is why the file below has no legacy per-kind `base` job) and the signing change (`6b57ba9`) — are why that file is not pending today. Each time it was staged again, that section's older text was replaced by the newer one.

Fugaro refuses to push a commit that touches `.github/workflows/**`
(GitHub rejects the push). When a Fugaro run needs to change a workflow
file, it writes the file's full intended new content here instead, under
the same name, for a human to copy in by hand after reviewing it.

**Copying a file in:**

```sh
cp docs/pending-workflows/website-preview.yml .github/workflows/website-preview.yml
cp docs/pending-workflows/website-promote.yml .github/workflows/website-promote.yml
git add .github/workflows/website-preview.yml .github/workflows/website-promote.yml
git rm docs/pending-workflows/website-preview.yml docs/pending-workflows/website-promote.yml
git commit -m "..."
```

The two commands above are the website pair's. The `images.yml` commands that used to follow this block were run in `6b57ba9` and its staged file is gone; **four further files are pending as of 2026-10-20 — `console-linux.yml`, `console-release-linux.yml`, `console-windows.yml` and `mobile-release.yml` — and each section below carries its own copy-in block**, the release one with an extra command, because it also replaces the staged `console-release.yml`.

**The website pair's own copy-in steps, in order.** (1) Do the one-time setup
in `docs/website/preview-promote.md` first — the workflows refuse to run
without the three repository variables, and the promote job needs the
`website-live` environment to exist before GitHub can hold it for approval.
(2) Copy both files in with the two commands above, in **one** commit: they
are a pair (a preview that cannot be promoted is not the feature), and the
`git rm` is load-bearing for the same reason it was for `images.yml` — while a
staged copy exists, `internal/cli/docs_website_deploy_test.go` reads *it*, so
leaving one in place would freeze those checks on a file nobody deploys from.
(3) Start `website-preview` by hand for a ref and a channel, open the URL it
prints in the run summary, and only then start `website-promote`.

**The `git rm` is for `images.yml` and the website pair, and it is not the rule for every file here.**
`console-tests.yml` and `console-release.yml` below are copied in **without** removing the staged copy,
because their tests assert the two files are byte-identical while both exist — see
those sections for the command and the reason.

The `git rm` is not cleanup, it's load-bearing, and `images.yml` is where that
has already been paid: its staged copy is gone (`6b57ba9`), so the static Go
tests that pin that workflow's shape (`images/base_workflow_test.go`'s
`readWorkflow`,
used by `TestImagesWorkflowPublishesAMultiArchBase`,
`TestOnlyPublishJobsHoldPackagesWrite`,
`TestVerifyPublicRunsEvenWhenPublishBaseIsSkipped`,
`TestImagesWorkflowBuildsTheBaseAndHistory`,
`TestPublishBaseSignsByOCIDigestOnPushOnly` and its companion
`TestSigningProblemsFindEveryMutation` — the two that pin the base image's
signing chain — plus
`images/verify_public_test.go`'s `TestImagesWorkflowPublishesAndVerifiesEveryImage`)
read the real `.github/workflows/images.yml` on its own today, as they read the
pending file
**while it existed** and only fell back to the real file once it was gone. If the pending file were
left in place after the copy, those tests would keep reading its frozen
content forever and would never notice a later edit — including an
accidental deletion — to the real file's `fugaro-base`/`publish-base`
jobs. Once the pending file is removed, the real file is read on its own
and *must* contain the base jobs, so deleting them (by hand, by a bad
merge, anything) fails the tests immediately.
`TestReadWorkflowFallsBackOnlyWhilePending` pins this fallback directly,
against temp files, independent of the real repository state.
`images/cloudbuild_test.go`'s `TestCIWorkflowTestsEveryTerraformRoot` reads
`ci.yml` the same way, through the same `readWorkflow`: while the pending
copy exists it is what the root loop is checked against, and once it is gone
the real `ci.yml` must name every root under `deploy/terraform/gcp/roots/`
on its own, so a copy that dropped `compute` (or a later deletion of it)
fails that test rather than leaving the root untested.
`internal/cli/docs_website_deploy_test.go`, the checker for the two website
files below, is written the same way: it reads a staged copy **while it
exists**, falls back to the real `.github/workflows/website-*.yml` once it is
gone, and fails if both copies exist at once.

## `shared-tests.yml`

**Status: `shared-tests.yml` is staged and pending.** The copy has not been made — there is no `.github/workflows/shared-tests.yml` — and this document's opening line says the same. `internal/cli/docs_shared_tests_workflow_test.go`'s **`TestSharedTestsWorkflowStagingStateIsWhatTheReadmeSays`** reads this line, the opening line and the tree together and fails if any two of them disagree, in either direction: while the staged copy exists this line must read `staged and pending`, and once the owner has copied the file in and removed the staged copy it must read `applied, not pending`. Naming that test is checked as well, so the name cannot drift out of the document that depends on it.

**A new file**, so nothing is replaced: it stages `.github/workflows/shared-tests.yml` entire, the CI job for `shared/`'s tests (mobile plan Task **P4.3**). The plan describes that job as a change to `ci.yml`, and it is a file of its own here because the brief for this run asked for the job "as a whole new workflow file"; the job's content is the plan's either way.

**Why `shared/` needs a job of its own.** `shared/` (P0.1-P1.5) is a second artifact outside the Go module, like `console/` and `website/`: `go build ./...`, `go vet ./...`, `gofmt -l .` and every job in `ci.yml` take no notice of it, so until this file exists the package's tests run **only where someone runs them**. That is the gap `docs/mobile/implementation-plan.md` records as open item 4 and `shared/README.md` states under "Two honest gaps". The package has no dependencies at all, so `npm ci` here is the check that the committed `package-lock.json` is installable rather than a download.

What the job is, in one line: on a push or pull request to `main` that touches `shared/**` or this workflow's own path, run `npm ci` then `npm test` in `shared/` on **Node 22 and Node 26**, then run **every `shared/*/mutation-proof.sh` the package carries** — the step globs for them rather than listing them, so a module that adds one is covered without an edit to the job; `contents: read`, one cancelling run per ref, every action pinned by full commit SHA, no secret anywhere. The scripts in the tree today, and the check that keeps this list true, are in the section below.

**The owner's steps, exactly:**

```sh
# 1. Review this file, then copy it in.
cp docs/pending-workflows/shared-tests.yml .github/workflows/shared-tests.yml
git add .github/workflows/shared-tests.yml
# 2. The git rm is load-bearing, not cleanup: while this file exists,
#    internal/cli/docs_shared_tests_workflow_test.go reads IT, so leaving it
#    in place would freeze those assertions on its bytes and hide a later edit
#    or deletion of the real file's job.
git rm docs/pending-workflows/shared-tests.yml
# 3. Say so where the site says what this directory holds -- the last section
#    of website/README.md says "... docs/pending-workflows/ now holds one
#    pending workflow, docs/pending-workflows/shared-tests.yml -- the shared
#    package's test job (mobile plan Task P4.3), which is not this site's: the
#    website's two manual workflows ...". With the staged copy gone, the
#    directory holds none and that sentence is false; website/test/readme.mjs
#    holds it to the directory in both directions, so BOTH the count and the
#    file name have to go: "docs/pending-workflows/ now holds no pending
#    workflow: the website's two manual workflows ...". (A name the README
#    keeps while the directory no longer holds it fails the check even with
#    the word corrected -- and the check scans the WHOLE document, so the last
#    backticked `docs/pending-workflows/shared-tests.yml` anywhere on the page
#    has to go, not only the one in that sentence.)
$EDITOR website/README.md
git add website/README.md
# 4. Update THIS document, which says what is still pending and would
#    otherwise go on describing a staged copy that is gone -- the same thing
#    `images.yml`'s bullet had to do when it was applied:
#      - the opening line: "One file is pending: `shared-tests.yml`" stops
#        being true of this directory;
#      - the `## shared-tests.yml` section: its Status line becomes
#        "**Status: `shared-tests.yml` is applied, not pending.**", with the
#        commit that applied it, and the bullet above the section stops saying
#        it was staged for the owner to copy.
#    TestSharedTestsWorkflowStagingStateIsWhatTheReadmeSays fails while the
#    tree and those two lines say different things, in either direction, so
#    copying the file in without this step leaves `main`'s Go tests red.
$EDITOR docs/pending-workflows/README.md
git add docs/pending-workflows/README.md
git commit -m "shared: run the shared package's tests"
```

**Steps 3 and 4 are not decoration, and they are the two steps someone copying `images.yml` before did not have to take for `images.yml` itself** (`images.yml`'s own bullet in this document was edited when it was applied; that is step 4, and it is the step nobody wrote down until now). `website/test/readme.mjs`'s **last** test — the file's closing `test(...)`, "the README claims about docs/pending-workflows/, firebase.json and the deploy script are literally true" — is what compares the page with `docs/pending-workflows/`, and it does so over the **whole** document rather than over that one sentence: it collapses every run of whitespace in `website/README.md` to a single space and then collects **every backticked `docs/pending-workflows/<name>.yml` the document prints anywhere**, which must be exactly the `*.yml` files the directory holds apart from the console's two kept-by-design copies (in both directions — a staged file nobody named, and a name with nothing staged, both fail), while its count word (`no`/`one`/`two`/`three`/`four`) must be that number; the console's two are excluded from that set and held instead to being byte-identical, each to the file of the same name under `.github/workflows/`. **So every backticked `docs/pending-workflows/<name>.yml` anywhere on the page is a claim that the file is pending, and the sentences that carry the claim are not the only place it can appear**: writing the path for an applied workflow in some other paragraph — the console's two, say, which the page names bare today — fails the check even when this sentence is right, so a cross-reference to a copied-in file has to be written without it. Copying this file in and `git rm`-ing it without step 3 leaves the sentence naming a file that is no longer there, and the site's own `npm run check` goes red on `main`. The test reads the count as a word and compares it as a number, so the sentence is the only thing to update — but it is the sentence, name and count together, that has to match.

**Step 4 is the same idea one level up: this document is itself a claim about the tree, and it goes stale the moment the copy lands.** The `images.yml` bullet above shows what "applied" looks like in it (`**`images.yml` — applied, not pending.**`, with the commit that did it), and `TestSharedTestsWorkflowStagingStateIsWhatTheReadmeSays` is what makes the difference load-bearing rather than a matter of tidiness: it reads this section's `**Status: `shared-tests.yml` is …**` line, the document's opening line and the state of `docs/pending-workflows/shared-tests.yml`, and fails if they disagree — `staged and pending` while the staged file is gone, or `applied, not pending` while it is still there, are both red. It also fails if this document stops naming that test, so the name cannot drift away from the paragraph that points at it.

There is no timing constraint, unlike `images.yml` below: the job is not part of any release path, nothing else reads it, and steps 3 and 4 can land in the same commit as the copy.

**Before copying: check the pins.** Dependabot does not watch this directory, so the two pins here can lag `main`'s. The file uses the SHAs `main` had on 2026-10-18 — `actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1` (v7.0.1, `ci.yml`'s pin) and `actions/setup-node@949feb2413d6458794dcd2491c4babbbce0c15c1` (v7.1.0, `ci.yml`'s pin) — both verified against their tags with `gh api repos/<owner>/<repo>/git/ref/tags/<tag>` when the file was staged. Compare them with `grep -h 'uses:' docs/pending-workflows/shared-tests.yml .github/workflows/*.yml | sort -u` and keep the newer SHA. Once the staged copy is gone, `internal/cli/docs_shared_tests_workflow_test.go`'s `TestSharedTestsWorkflowPinsAgreeWithTheOtherWorkflows` enforces that agreement itself; while it is pending, that test skips and says so.

### What this job will report on `main` today

**This section states no test count, and that is deliberate.** An earlier version of it said the suite was 289 tests and that four mutation proofs would run; both were true when the job was staged and both were wrong by the time anyone read them again (the suite is past 470, and the package carries a proof for every module that declares one). A number in a document is a claim with no test under it, so what follows is **the shape of what the two legs report**, which does not move as the package grows, and a measurement that says when it was taken and how to retake it. The one list that *is* kept here — which scripts the proof step runs — is held to the tree by `TestSharedTestsWorkflowReadmeNamesEveryProofScript` rather than by hand.

**The Node 22 leg will be red, and not because of this job.** One test fails on that line: `shared/url/index.test.js`'s hostile-string sweep expects `https://xn--github-9d0c.com/a` to be refused as `punycode-host`, and Node 22's host parser refuses the string itself, so the module answers `not-a-url` before its own host rule is reached. Node 26 parses it and the case passes. The difference is the runner's line, not the package's:

```console
$ node -e 'try { console.log("ok", new URL("https://xn--github-9d0c.com/a").hostname) } catch (e) { console.log("throws:", e.message) }'
# node 22.23.3:  throws: Invalid URL
# node 26.12.0:  ok xn--github-9d0c.com
```

That failure is **pre-existing and already recorded** — `shared/mailbox/README.md` and `shared/mailbox-sim/README.md` both name it, and it is the stated reason their mutation proofs run their own module's suite rather than the package's. It has a second consequence, one step later in the job: the proofs of `shared/confirm/`, `shared/lease/` and `shared/mailbox-schema/` run the **whole package's** `npm test` as their positive control, so on Node 22 they refuse (exit 2, "the suite does not pass on the pristine …") rather than reporting mutations they never ran. The other four — `shared/command/`, `shared/mailbox/`, `shared/mailbox-sim/` and `shared/prompts/` — run their own module's tests instead, so they are unaffected by it.

**What the proof step runs**, which is the package's own list and not this document's: one script per module that declares one, run by a glob over `shared/*/mutation-proof.sh`. In the tree today that is `shared/command/mutation-proof.sh`, `shared/confirm/mutation-proof.sh`, `shared/lease/mutation-proof.sh`, `shared/mailbox/mutation-proof.sh`, `shared/mailbox-schema/mutation-proof.sh`, `shared/mailbox-sim/mutation-proof.sh` and `shared/prompts/mutation-proof.sh`. Each script breaks one guard in its module and requires the module's tests to catch it, prints its own `N guards: N killed, N survived` line, and exits non-zero unless every mutation it applies is caught, so the step fails on a survivor rather than on a summary nobody reads. `TestSharedTestsWorkflowReadmeNamesEveryProofScript` globs that pattern in the tree and requires this paragraph to name exactly the modules that carry one, in both directions: a module that adds a proof script fails this document until it is named here, and a name here whose script is gone fails too. Because the step globs rather than listing, the **job** never needs an edit for either case — only this list does.

**What the two legs actually report, step by step:** a leg stops at its first failing step and GitHub skips the rest, so on Node 22 the job reports **one red step — "the shared package's tests"** — and its **mutation-proofs step is skipped, not red**: the proofs are never reached on that line, because `npm test` fails before them. On Node 26 both steps run and the leg is green. Everything below is the shape of that; the counts are the measurement dated under it:

| | Node 22.23.3 | Node 26.12.0 |
|---|---|---|
| `npm test` (the step that decides the leg) | **1 test fails**, the punycode case above | every test passes |
| the proofs that run their own module's suite (`command`, `mailbox`, `mailbox-sim`, `prompts`) | pass; every mutation caught | pass; every mutation caught |
| the proofs that run the package's `npm test` as their positive control (`confirm`, `lease`, `mailbox-schema`) | **refuse**, exit 2 | pass; every mutation caught |
| what the job shows | one red step at the test step; the proofs step skipped | green throughout |

Measured on **2026-10-19**, in this container, on the two lines the job pins (Node 22.23.3 and 26.12.0, installed through `mise`), running exactly what the job runs: `cd shared && npm ci --ignore-scripts --no-audit --no-fund`, then `npm test`, then `bash shared/<module>/mutation-proof.sh` for each of the seven. The suite reported **476 tests: 475 pass and 1 fail on Node 22, 476 pass and 0 fail on Node 26**; the seven proofs reported **448 guards, 448 killed, 0 survived, 0 not applied on Node 26**, and on Node 22 the four module-suite proofs reported 29 + 109 + 45 + 93 guards killed — `command`, `mailbox`, `mailbox-sim` and `prompts`, in the order of the table above — with none surviving, while the three package-suite proofs refused. Those two figures are a record of that run, not a standing claim about the tree — re-run the commands rather than trusting them, and note that nothing goes stale in the *job* when they change, only in this sentence.

**Nothing in the workflow hides this**, and it must not be edited to: skipping the failing file, softening the step or dropping the Node 22 leg would delete the evidence rather than the defect. The fix belongs to P0.3's module (`shared/url/`, and the expectation in `index.test.js`), which is a different task with its own review tier; the owner's two live options are to fix `shared/url` first, or to copy the job in with its Node 22 leg red — which is the honest state, and the point of running both lines.

One further honest note: `shared/package.json`'s `engines` is `>=22 <23`, so the Node 26 leg makes npm print an `EBADENGINE` **warning** on `npm ci` (npm 11.20.0) and nothing else — the install and the suite both succeed. Widening `engines` would be a change to P0.1's manifest, not to this file, and is not made here.

### Not verified

**This file has never run in GitHub Actions.** No workflow was dispatched and no job was executed; what is verified is the YAML's shape, through `internal/cli/docs_shared_tests_workflow_test.go` (the triggers, the permissions, the pins and their version comments, both Node lines, the path filter, the two runner commands, the proof step, the absence of a secret and of the dangerous trigger events and permission grants), and the two `run:` steps by hand, in this container, on both Node lines — the same commands, from `shared/`, with Node 22.23.3 and 26.12.0 installed through `mise`, which is what produced the measurement dated above.

What that cannot show: that GitHub's own event filtering and matrix behave as the YAML says, that `actions/setup-node` resolves `"22"` and `"26"` to the lines used here, or that the job's runtime, caching and permissions behave in a hosted runner. Those are CI's to show, once the file is copied in.

## `console-tests.yml`

The desktop console's own tests in CI, staged by Task 1.4 of
`docs/plans/2026-10-15-desktop-console.md` and applied by the owner as Task 1.5.
One job named exactly `console`, on `macos-latest` and on `ubuntu-latest` with
the end-to-end pass under `xvfb-run`: `actions/setup-node` with the version read
from `console/package.json`'s `engines.node`, `npm ci --ignore-scripts
--no-audit --no-fund`, `npm run test:unit` and `npm run test:e2e` (which builds
the app first and passes on a tree with no spec). **It holds no secret at all**,
and its triggers are `pull_request` and `push` to `main` **with no `paths:`
filter and no job-level `if:`** — and that is a correctness requirement rather
than taste. **Task 4.6 (#600) added the job's two matrix legs to
`scripts/release-gate.sh`'s `names` array** — `console (ubuntu-latest)` and
`console (macos-latest)`, the names GitHub gives each leg's check run — so the
gate requires both on a tag's commit and, in its `identical_tree_checks`
fallback, the same names on the pull request head whose tree matches. **A check a filter
skipped is not a success**, so a `console` job filtered to `console/**` would
then refuse **every** release tag in this repository, including the releases that
never touch the console (plan Task 4.6, design section 9.4). The signing,
notarising and stapling job and the Apple material it reads are Task 4.2's
**separate** `console-release.yml`, precisely so that this file — which runs on
every pull request from any branch — never sits next to them.

The owner applies it with one command, from the repository root:

```sh
cp docs/pending-workflows/console-tests.yml .github/workflows/console-tests.yml
git add .github/workflows/console-tests.yml
git commit -m "console: run the console's tests in CI (plan Task 1.5)"
```

**Unlike `images.yml`, the staged copy is not deleted after the copy-in.** While
both files exist, `TestConsoleTestsWorkflowIsStagedAndPinned` (in
`internal/cli/docs_desktop_console_test.go`) reads the staged file and asserts
the one under `.github/workflows/` is **byte-identical** to it: absence would say
only that nobody wrote a file, while identity says the file that exists is the
reviewed one, and the staged copy is what makes that assertable before *and*
after the copy-in. Deleting it would remove the check, so this file's procedure
has no `git rm` in it.

After the copy-in, `console/README.md` gains a line naming the applying commit
(plan Task 1.5), which is the only way a later reader can tell which console
commits had CI and which did not: **Tasks 1.0 to 1.4 merged with no console CI
at all**, and that gap is a fact about this repository's history rather than
something the plan claims away.

## `console-release.yml`

The desktop console's signing, notarisation and stapling pipeline, staged by
Task 4.2 of `docs/plans/2026-10-15-desktop-console.md` and applied by the owner
as Task 4.5. Triggered by a **`push` of a strict `vX.Y.Z` tag and nothing
else** — never a pull request, never a `pull_request_target`, never a
`workflow_run`, never a `workflow_dispatch`. **Two jobs, and the write token is
never in the job that runs the packer:** `release`, on the pinned `macos-15`
runner (never `macos-latest`: the runner image is the signing toolchain), holds
the protected environment, the Apple secrets and `contents: read` +
`checks: read` only; `publish`, on `ubuntu-24.04`, holds `contents: write` and
nothing else — no environment, no Apple secret, no checkout, no packer.
`GH_TOKEN` is in two steps' `env:`: the gate's (read-only) and the publish
step's. The `release` job, in order:

1. checks the tagged commit is reachable from `origin/main`, refuses any tag
   that is not a strict `vX.Y.Z` (its X.Y.Z becomes the app's version), then
   runs `scripts/release-gate.sh`, exactly as `release.yml` and `images.yml` do;
2. with **no secret placed in any step's environment or on disk** (the runner
   process receives the environment's secrets at job start; what the file
   controls is where they are exposed): setup-node (no `cache:`: a restored
   cache is written by other runs), `npm ci --ignore-scripts
   --no-audit --no-fund` from the lockfile (the packer is the pinned
   `electron-builder` devDependency), Electron's own download
   (`node node_modules/electron/install.js`) and an unsigned warm-up
   `npm run package` whose output is deleted (stamped with the tag's version
   like the signed one), so the two Electron archives and
   the packer's DMG and zip toolsets are downloaded here and not later;
3. **one step holds all six secrets** and does everything that needs them:
   the fail-closed check, a temporary keychain, the `.p8` key file (both
   created under `umask 077`), the certificate's team checked against
   `APPLE_TEAM_ID` before anything is signed, the packer (Task 4.1's script:
   `--mac --universal --publish never`, the hardened runtime,
   `console/entitlements.mac.plist`, `-c.extraMetadata.version=X.Y.Z` from the
   tag, and `-c.mac.notarize=false` plus `--dir` on the bundle-only pack:
   electron-builder signs the `.app` and notarises nothing, because its own
   notarise-and-staple is inside `npm run package` and its staple cannot be
   retried on its own — `console/scripts/notarize-and-staple.sh` submits, staples
   and validates the bundle instead, refusing unless `notarytool` exited 0 *and*
   Apple's status is `Accepted`), the second pack building the `.dmg` and the
   `.zip` **from the stapled bundle** as `npm run package:targets --
   --prepackaged "$APP"` — the same pinned packer without the `npm run build`
   the other packs start with, since `--prepackaged` packs a bundle that is
   already built and the rebuild would only be spent again in the step holding
   the unlocked keychain — the
   signature's TeamIdentifier checked, then the `.dmg` signed with
   `codesign --timestamp`, submitted, stapled and validated by the same script;
   its exit trap removes the key material;
4. verifies what ships — the SHA-256 of the `.dmg` and the `.zip`, the bundle
   inside the mounted `.dmg` and the extracted `.zip` (`codesign --verify
   --deep --strict`, `spctl --assess --type execute`, `xcrun stapler
   validate`), and the disk image itself (`spctl --assess --type open`) — into
   `console-verification.txt`, and uploads the `.dmg`, the `.zip`, the record
   and `console-sha256sums.txt` as the workflow artifact
   `console-release-assets` (upload-artifact at the pin `release.yml` and
   `images.yml` use).

**Re-copy required: the staged file changed on 2026-10-19 and the copy under
`.github/workflows/` did not.** Release run 39163351646 was attempted five
times, and the last two of those attempts failed at Apple's ticket-delivery
lookup — `NSURLErrorDomain Code=-1005 "The network connection was lost"`
against `.../com.apple.gk.ticket-delivery/production/public/records/lookup`,
error 68: the **fourth attempt** died in **the packer's own staple, inside
`npm run package`** (`@electron/notarize`, "Failed to staple your application
with code: 68"), and the **fifth** in a `xcrun stapler validate` on the bundle,
which the signing step then refused with "carries no stapled ticket: refusing
to publish artifacts that would carry it unstapled". A network failure to
Apple, not a rejection: the notarisation itself had been accepted. The three
earlier attempts never reached Apple at all — the first failed within about a
second with no step having started (its cause is not recorded, and this
document does not guess one), the second on missing `APPLE_API_*`
secrets, the third on a certificate import with the wrong password. That is why the
packer's notarisation is now off in the workflow and the staple, the validation
**and the submission** are the staged file's own: the packer's staple is one
call inside its process, where nothing can retry it. The staged file retries
the two `xcrun stapler` calls;
**the applied copy is the older text, so until the owner re-copies the staged
file in, `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`'s byte-identity
clause fails** (its last assertion compares the two files and says they
differ). That red test is the intended state of this change: the fix cannot be
delivered by an agent, because GitHub rejects a Fugaro push that touches
`.github/workflows/**`. **So do not merge a staged change before the copy-in.**
While the two files differ, CI's `test` job is red on every push to `main`, and
`scripts/release-gate.sh` requires a check run named `test` — so every tag is
refused, **the CLI's releases included**, until the owner copies the staged
file in. The PR carrying this change is a draft for exactly that reason, and
its own CI is red until then.

**Every `xcrun stapler` call in the pipeline is retried, bounded — and no
other call is.** The signing step does its notarising, stapling and validating
through `console/scripts/notarize-and-staple.sh`, which holds one shell
function, `retry_apple_network` — defined once more, character for character
the same, in the verification step. It wraps every `xcrun stapler staple` and
`xcrun stapler validate` on the way to a release: the bundle's and the disk
image's in the script, and the bundle inside the mounted `.dmg`, the one inside
the extracted `.zip` and the disk image itself in the verification step. It
makes up to **five** attempts with a growing pause between them (**15, 30, 60,
90** seconds), prints each attempt's number and exit code (`apple-network
attempt 2 of 5: … exited 68`), and returns the failure only after the fifth —
so a ticket that is genuinely absent still refuses to publish. In the
verification step each call is inside `record`, so the attempts land in
`console-verification.txt` with the rest of the evidence. The test also runs
the helper: `TestDocsConsoleReleaseRetryHelperIsBounded`.

**The submission is deliberately not retried, and that is not an
oversight.** `xcrun notarytool submit --wait` *is* the submission: a rejection
is final and re-submitting is not this file's business. The script reads
Apple's answer as JSON (`--output-format json`) and refuses at once — with the
answer printed and no staple — unless the status is exactly `Accepted`, so a
rejected notarisation cannot be stapled over. A test runs the script with a
fake `xcrun` and proves that too: a status that is not `Accepted` exits
non-zero with no `stapler` call at all and no pause.

**Nothing else is retried at all**: `codesign`, the TeamIdentifier checks,
`scripts/release-gate.sh`, the fail-closed secret check and the
`spctl`/`hdiutil`/`ditto` assessments all fail at once, exactly as before, and
the helper is called nowhere else in either job. The test reads this both ways
over the script and over every step: every line naming the helper is one of the
retried calls, and **every line that calls `xcrun stapler` names it**, so a
fresh, un-retried `xcrun stapler validate` added anywhere fails the test rather
than quietly missing the retry.

The `publish` job (`needs: release`) downloads that artifact (download-artifact
at the pin `images.yml` uses), re-checks every asset with `sha256sum --strict
--check` and that the `.dmg` and `.zip` are named for the tag's version, and
publishes the console's **own release, `console-vX.Y.Z`**, never touching the
CLI's.

**The version is the tag's.** `console/package.json` says `0.0.0` and nothing
in it is a release version, so every pack is stamped with
`-c.extraMetadata.version=X.Y.Z` (through `env:`, never an inlined `${{ }}`):
the `.dmg` and `.zip` file names carry the real version (`Fugaro
Console-X.Y.Z-universal.dmg`), the app's version is it, the job refuses
artifacts not named for it, and the update feed of plan Tasks 4.3/4.4 depends
on it.

**Its own release, never the CLI's (the owner's ruling of 2026-10-17,
"Separate console release").** GoReleaser publishes the CLI's `vX.Y.Z` release
from the same tag push, and with immutable releases on (`docs/release.md`,
"Before you tag") nothing can be added to a release once it is published. So
the `publish` job creates `console-vX.Y.Z` — X.Y.Z from the CLI tag that triggered it,
the same commit and the same gate (D11) — as a **draft**, uploads the `.dmg`,
the `.zip`, `console-verification.txt` and `console-sha256sums.txt` to it, and
publishes it (`gh release edit --draft=false`) in the same step, with
`--latest=false` so the CLI's release stays the repository's latest. A re-run
never clobbers: an existing `console-vX.Y.Z` release (published, or a draft
left by an earlier attempt, which the owner deletes before re-running) or tag
stops the job with a message. The tag ruleset must let the workflow create
`console-v*` tags. The console's Homebrew cask, when it exists, points at this
release (a later task).

**The real control is the environment's reviewer and a tag ruleset, not the
file's own gate.** A tag push runs this file as the tagged commit holds it, so a
tag on a branch whose copy of the workflow or of `scripts/release-gate.sh` was
edited runs the edited copy. The `console-release` environment's required
reviewer and a ruleset restricting who may create `v*` tags are what stop that;
**the approver confirms the tag's commit is on `main` before approving the
deployment**. The in-job ancestry check catches a mistake, not an attacker.

**It reads its secrets through a job-level `environment: console-release`** —
the protected GitHub environment the owner creates in the same step: required
reviewers = the owner, deployment branches restricted to tags. A staging file
cannot create an environment, so the copy-in and the environment have to be done
together. The environment holds exactly these six, and the workflow names them
and nothing else:

| Secret | What it is |
|---|---|
| `APPLE_CERTIFICATE` | base64 of the Developer ID Application `.p12`, one line (the G2 Sub-CA certificate) |
| `APPLE_CERTIFICATE_PASSWORD` | that `.p12`'s password |
| `APPLE_TEAM_ID` | the team identifier (`ZM8Y2LC7JL`); the workflow checks the certificate names it before packing, and the signature after |
| `APPLE_API_KEY` | the App Store Connect API key's `.p8` contents |
| `APPLE_API_KEY_ID` | the key's id |
| `APPLE_API_ISSUER` | the issuer uuid |

**The notarisation credential is the App Store Connect API key, not an
app-specific password.** D4 settles that signing is required and that the
pipeline is tag-only behind the gate and a protected environment, but it does not
choose between the API key and an app-specific password; the decision recorded
here is the API key, so `APPLE_ID` and `APPLE_APP_PASSWORD` are deliberately
absent, and a row naming either of them is a defect.

**It fails closed.** The signing step's first act is to refuse to continue
unless all six are non-empty, naming the missing one, so an absent secret stops
the job rather than silently skipping the signing — an unsigned console
published under a signed release's name is the worst outcome this file can
produce. No step carries an `if:` except the cleanup's `always()`, nothing
carries `continue-on-error`, and `|| true` is the cleanup's alone. No value of
any secret appears in the file: each is referenced as `${{ secrets.NAME }}` in
the `env:` of **exactly one step**, bound to a variable of the same name — never
a workflow- or job-level `env:` — which is what
`TestDocsConsoleReleaseWorkflowIsStagedAndPinned` parses. The certificate goes
into a keychain the step creates (a random password from `openssl rand`,
`security create-keychain`, `security import -T /usr/bin/codesign` — and not
`-T /usr/bin/security`, which would let any later command export the key —
and `security set-key-partition-list -S apple-tool:,apple:`); the notarisation
key file is handed on as a *path* (`APPLE_API_KEY` is a file to
electron-builder, `--key` is a file to notarytool), never as a value; the
step's exit trap removes both, and an `if: always()` step at the end is the
backstop.

The owner applies it with one command, from the repository root:

```sh
cp docs/pending-workflows/console-release.yml .github/workflows/console-release.yml
git add .github/workflows/console-release.yml
git commit -m "console: sign and notarise the console on a version tag (plan Task 4.5)"
```

and, in Settings, Environments, creates `console-release` with the owner as a
required reviewer and its deployment branches restricted to tags. **Both are
required before the first tag that ships the console**: a tag push runs the
workflow exactly as the tagged commit holds it, and the workflow is only as safe
as the environment its secrets live in.

**Unlike `images.yml`, the staged copy is not deleted after the copy-in.**
`TestDocsConsoleReleaseWorkflowIsStagedAndPinned` reads the staged file and asserts
the one under `.github/workflows/` is byte-identical to it, so a hand-edited or
hand-written workflow there is a red test rather than something nobody reads.

### Not verified

This file has never run, and no signing step under it has ever run either: no
certificate, no App Store Connect key, no notary submission and no Mac were
available or permitted in the run that wrote it, and no agent run in this
repository builds, signs, notarises or verifies a bundle. What *is* verified is
its text,
through `TestDocsConsoleReleaseWorkflowIsStagedAndPinned` and
`TestDocsConsoleReleaseWorkflowHoldsNoPullRequestTrigger`, `TestDocsConsoleReleasePublishRefusesToClobber`,
`TestDocsConsoleReleaseTagStepStampsOnlyStrictVersions` (both run the staged scripts under bash with a faked `gh`),
`TestDocsConsoleReleaseRetryHelperIsBounded` (which runs the Apple-network retry
helper under bash with a fake `sleep` and a command that fails a set number of
times) and `TestDocsConsoleNotarizeAndStapleScript` (which runs
`console/scripts/notarize-and-staple.sh` end to end with fake `xcrun`, `ditto`
and `sleep` binaries on `PATH`: it recovers after lost connections, gives up
after five attempts, and refuses a submission whose status is not `Accepted`
without making a single `stapler` call) in
`internal/cli/docs_desktop_console_test.go` (both picked up by the
`-run 'Docs|Lint'` filter): the trigger surface, the ancestry check and the
gate first, every install and download before the one step that holds the
secrets, the secret names and their bindings, the fail-closed check, no `if:`,
`continue-on-error`, stray `|| true`, inlined `${{ }}` or shell tracing, no
line that prints the key material, the keychain and its trust list, the team
checks, the bundle-only pack with the packer's notarisation off, the script's
submission and its `Accepted` check, its staple and validation of the bundle
and of the disk image (and that the targets are packed from the stapled
bundle), the bounded retry around the staple and validate calls (five
attempts, growing pauses, attempt number and exit code, and nothing else
wrapped), the verification and its checksums, the separate `console-vX.Y.Z`
release and its re-run refusals, the two-job split and each job's token,
the version stamp on every pack, the four action pins, the permissions, and
byte-identity with the copy under `.github/workflows/` once it exists.

Accepted residuals: the keychain password (`security create-keychain -p`,
`unlock-keychain -p`, `set-key-partition-list -k`) and the `.p12` password
(`security import -P`) are on `security`'s argv, which has no stdin form for
them, so another process of the same user can read them in the process list
for the moments those commands run; the `unset` before packing keeps the
certificate, its password and the key's contents out of the packer's
environment but not out of the signing shell's own starting environment, which
the same user can read (`ps -E`) while the step runs. The runner is a
single-tenant, single-job VM.

Unverified offline and left to Check 59a: that electron-builder accepts
`CSC_NAME` as a certificate's SHA-1 together with `CSC_KEYCHAIN`; that
`codesign` signs with only `apple-tool:,apple:` in the partition list; that the
unsigned warm-up pack leaves everything the signed pack needs in the cache; and
that `-c.extraMetadata.version` reaches the bundle's version and the
artifact names as described; that the signed, stapled DMG passes
`spctl --assess --type open --context context:primary-signature`; and, added
2026-10-19, that `npm run package -- --dir -c.mac.notarize=false` builds and
signs the universal `.app` and no targets, and that `npm run package:targets --
--prepackaged "release/mac-universal/Fugaro Console.app" -c.mac.notarize=false`
then packs the `.dmg` and the `.zip` from that bundle with the tag's version
still in their names. `package:targets` is `package` without its `npm run
build`; that the packer really needs no rebuild there (the prepackaged pack
skips `doPack` and the bundle is already on disk) is the same Check 59a reading
of electron-builder 26.17.0, unverifiable offline.

The 2026-10-19 retry leaves one thing for Check 59a to confirm on a real runner,
because it cannot be observed here: that a `xcrun stapler` call which does meet
a transient CloudKit failure recovers inside the five attempts. What is
verified offline is the helper's own behaviour — its bound, its pauses, its
per-attempt reporting and its returning the failure only after the fifth — run
under bash with a fake `sleep`; the real service's timing is not. The retry
wraps only `xcrun stapler`: the two calls in
`console/scripts/notarize-and-staple.sh` (the bundle's and the disk image's)
and the three `xcrun stapler validate` calls in the verification step. The
submission is not among them, and the 2026-10-19 change is why the `.app`'s
staple is a call the pipeline makes itself instead of one inside
`npm run package`.

Two things a reader must not take from those tests: the pinned SHAs are read
against upstream by the person copying the file in (see "Before you copy" below —
Dependabot does not watch this directory), and the first real run is Check 59a's
and 59b's (plan Task 4.3), which are owner-run on a Mac.

## `console-linux.yml`

The desktop console's **Linux build check**, staged on **2026-10-20** by the run
that packaged the console for Linux (the owner's ruling of that date: *"yes,
Linux for 0.10.0, unsigned"*; design D10 and section 9.4, and
`docs/plans/2026-10-15-desktop-console.md`'s phase 4). One job, `console-linux`,
on `ubuntu-latest`: `contents: read`, **no environment and no secret at all**,
the pinned toolchain (`npm ci --ignore-scripts`), Electron's own download
verified against the three checksums `console/package.json` pins, `npm run
build`, then `npm run package:linux` — which is `electron-builder --linux
--publish never`, with no `npx` in front of it — an assertion that exactly one
`.AppImage` and one `.deb` came out, and `upload-artifact`. It runs on every
pull request and every push to `main`, with no path filter: a Linux packaging
path exercised only on a tag is one that breaks silently between releases.

**Why it is its own workflow rather than a second job in `console-tests.yml`.**
The task that wrote it asked for "a new `console-linux` job for the existing CI
workflow", and it is a separate file for one reason, which is a test rather than
a taste: **the release gate's required-check list is derived from
`console-tests.yml`'s job set.** `TestReleaseGateRequiresTheConsoleCheck`
(`internal/cli/docs_desktop_console_test.go`) reads that workflow and asserts
that `scripts/release-gate.sh`'s `names=(...)` array is exactly the check names
it reports plus `test`, `terraform` and `rules` — and `deriveConsoleCheckNames`
**refuses a workflow with more than one job** ("this test cannot tell which of
several jobs that is"). A second job there would therefore not be a packaging
change at all: it would change which checks gate a release tag, and it would
have to land together with a widened derivation and an edited `names=` line.
That is a real decision — arguably the Linux build *should* gate a release — but
it is the owner's, and it is not one to fold silently into a packaging change.
The task asked for a **build-only** job, and build-only is what this file is:
nothing here is in the gate's list, so a red run here is information and not a
blocked release.

**Owner, if you want it to gate releases**, all three edits in one commit, or
the test that holds them together fails: paste the `console-linux` job into
`.github/workflows/console-tests.yml` as a second job; add `console-linux` to
the `#   ` comment block and the `names=(...)` array in
`scripts/release-gate.sh`; and widen `deriveConsoleCheckNames` so it reads a
workflow with several jobs.

**What it proves about the fuses, and what it does not.** `afterPack`
(`console/dist/main/fuses.js`) flips the six and reads the wire back, so a pack
whose flips did not take fails here and produces no artifact at all. It does
**not** re-read the wire out of the packed AppImage or `.deb`: that is
`console-release-linux.yml`'s linux job, on the shipped bytes, because that is
the claim a release makes.

**The version it packs is `0.0.0`.** Nothing stamps `console/package.json` in CI
and nothing should — only a release knows the version, and
`console-release.yml` stamps it from the tag with
`-c.extraMetadata.version` — so the artifacts here are named
`Fugaro.Console-0.0.0-linux-x64.AppImage` and `.deb`. They are a build check,
not a release candidate.

**To copy it in**, from the repository root (and then delete the staged copy, as
`images.yml`'s was: there is no byte-identity test for this file, because it is
a new workflow with nothing to be identical to):

```sh
cp docs/pending-workflows/console-linux.yml .github/workflows/console-linux.yml
git add .github/workflows/console-linux.yml
git rm docs/pending-workflows/console-linux.yml
git commit -m "console: build the Linux artifacts in CI"
```

**Two follow-up edits belong in that commit**, because two sentences in this
repository count the files still pending in this directory and both are held to
the directory: the last section of `website/README.md` (which
`website/test/readme.mjs` reads in both directions, count and names) and **this
README's own opening line** (which `TestConsoleLinuxStagingTellsTheOwnerWhatToCopyWhere`
holds to the same directory). Applying this file leaves
`console-release-linux.yml` and `console-windows.yml` pending beside
`shared-tests.yml`, so the two sentences that commit needs are (each block is
the **exact** text to write in place of the sentence it replaces: the
opening-line block is the whole claim, from its first bold marker to the end of
its last sentence, and the sentence that follows it in this file — "**Two other
files stay staged here by design**, `console-tests.yml` and
`console-release.yml`" — is unchanged, because those two do stay staged):

```
**Three files are pending: `shared-tests.yml`** (the shared package's test job, mobile plan Task
P4.3; see `## shared-tests.yml` below), **`console-release-linux.yml`** (the console's signing
pipeline with the Linux legs added, 2026-10-20 — it supersedes the staged
`console-release.yml`; see `## console-release-linux.yml` below) **and `console-windows.yml`**
(the console's Windows installer pack, 2026-10-20 — a release-time job, not a pull-request check;
see `## console-windows.yml` below).
```

```
`docs/pending-workflows/` now holds three pending workflows,
`docs/pending-workflows/shared-tests.yml` — the shared package's test job (mobile plan Task
P4.3) — `docs/pending-workflows/console-release-linux.yml` — the console's signing pipeline
with the Linux legs added, which supersedes the staged `console-release.yml` and so is copied
over it as well as into `.github/workflows/` — and `docs/pending-workflows/console-windows.yml`
— the console's Windows installer pack, which runs at release time only rather than on a pull
request, and is copied in only after the packer and the unit suite can pass on a Windows runner.
None is this site's:
```

The second block is the head of `website/README.md`'s last paragraph, up to and
including "site's:"; the words that follow it on the page today — "the
website's two manual workflows, `website-preview.yml` and
`website-promote.yml` (#566)…" — are not part of this change and stay where they
are. `TestStagedLinuxFilesSurviveTheCopyIn` splices exactly this text into the
site's README (and the two blocks below into this one) and then runs this suite
in the spliced tree, so a block that is not the text the tests need fails here
rather than in the owner's commit.

The sentences the second commit needs — with both of 2026-10-20's files applied
and their staged copies removed — are in `## console-release-linux.yml` below,
which prints **both** of the pairs that commit can need, each under the label
saying which tree it is for: whether `mobile-release.yml`, the third file staged
that day, has been copied in by then. `website/README.md` is never left saying
"one pending workflow" while this one is still here: `readme.mjs` reads the
directory, not the file's own history.

## `console-release-linux.yml`

**This file supersedes `## console-release.yml` above.** It is that file with
the Linux legs added — a third job and a `publish` that merges both legs — so it
is copied over the staged `console-release.yml` **and** into
`.github/workflows/console-release.yml`, which is why its copy-in has one more
command than usual. Staged on **2026-10-20** by the same run and for the same
ruling (the owner: *"yes, Linux for 0.10.0, unsigned"*).

What it adds, and nothing else in the file moved:

1. **A third job, `linux`**, on the pinned `ubuntu-24.04`, with `contents: read`
   and `checks: read` and **no environment and no secret** — there is nothing to
   sign with, so there is nothing to protect. It runs the same three gate checks
   through the same `scripts/release-gate.sh`, installs the same pinned
   toolchain, downloads Electron against the same
   `electron-v44.7.2-linux-x64.zip` checksum, builds with the same pinned packer
   (`npm run package:linux`, `--publish never`), and then verifies **what
   ships**: the SHA-256 of the `.AppImage` and the `.deb`, the `.deb` opened with
   `dpkg-deb --info`/`--contents`/`--extract`, the AppImage unpacked with its own
   runtime's `--appimage-extract` (no FUSE mount, no display), and the Electron
   fuse wire read back with `@electron/fuses` out of the executable **inside
   each artifact** — the one thing the pack-time hook cannot say about the
   packaged result. It uploads them as `console-linux-assets` with
   `console-linux-verification.txt` and `console-linux-sha256sums.txt`.
2. **`publish` now `needs: [release, linux]`**, downloads both artifacts,
   re-checks each leg against its own sums file, merges the two legs into one
   asset set with **one** `console-sha256sums.txt`, signs that file keyless with
   cosign (`id-token: write`, `cosign sign-blob --yes --bundle`, the same call
   GoReleaser makes for the CLI's archives) into
   `console-sha256sums.txt.sigstore.json`, and attaches everything to
   `console-vX.Y.Z`. The merge refuses a run where a leg is empty or where both
   legs would ship the same file name, and re-runs `sha256sum --strict --check`
   on the merged file **in the directory that ships**.

**The unsigned claim, plainly: an AppImage is not code-signed.** There is no
Linux signing key and none is planned (D4 is macOS; D10's amendment of
2026-10-20 takes Linux **unsigned** and leaves Windows later), so
the AppImage and the `.deb` carry no signature of their own. What proves they
are the bytes this repository built is `console-sha256sums.txt` — which covers
every asset in the release — and the Sigstore bundle beside it, which is signed
by this workflow's own OIDC identity rather than by a key of the owner's. A
release note saying "signed" of the Linux artifacts would be false.

**To copy it in**, from the repository root — the first command is the one that
keeps `console-release.yml` and its staged copy byte-identical, which is what
`TestDocsConsoleReleaseWorkflowIsStagedAndPinned` asserts:

```sh
cp docs/pending-workflows/console-release-linux.yml docs/pending-workflows/console-release.yml
cp docs/pending-workflows/console-release.yml .github/workflows/console-release.yml
git rm docs/pending-workflows/console-release-linux.yml
git add -A
git commit -m "console: build, verify and attach the Linux artifacts on a version tag"
```

`docs/pending-workflows/console-release.yml` and
`.github/workflows/console-release.yml` are **byte-identical today** — the
2026-10-19 change above has been copied in — and this file is that text with the
Linux legs added. So the first command above leaves the two identical again, and
the byte-identity clause
(`TestDocsConsoleReleaseWorkflowIsStagedAndPinned`) is never red in between: this
staging does **not** put `main` in the refused-every-tag state the 2026-10-19
change was merged in. That is the reason it is staged as a file of its own
rather than as an edit to the staged `console-release.yml`, which is what the
2026-10-19 change did.

**Two follow-up edits belong in that commit**, the same pair the
`## console-linux.yml` section above describes — this directory's own opening
line and the last section of `website/README.md`, both of which count and name
the files still pending here, and both of which are held to the directory
(`TestConsoleLinuxStagingTellsTheOwnerWhatToCopyWhere` and
`website/test/readme.mjs`). With both of 2026-10-20's files applied and their
staged copies removed, the two sentences to write depend on one thing:
**`mobile-release.yml`**, the other file staged on 2026-10-20, has a copy-in of
its own (`## mobile-release.yml` below) and the two copy-ins can be made in
either order, so **both pairs are printed here**, each under the sentence that
says when it is the one to write. The opening-line block stops at the end of its
last sentence, and the "**Two other files stay staged here by design**" sentence
that follows it is unchanged — `console-tests.yml` and `console-release.yml` do
stay staged.

**If `mobile-release.yml` is still staged when you make this copy-in** — the
order the copy-in proof applies, and the pair that names it as still pending:

```
**Three files are pending: `shared-tests.yml`** (the shared package's test job, mobile plan Task
P4.3; see `## shared-tests.yml` below), **`console-windows.yml`** (the console's Windows
installer pack, 2026-10-20 — a release-time job, not a pull-request check; see
`## console-windows.yml` below) **and `mobile-release.yml`** (the mobile app's build and store
submissions, 2026-10-20; see `## mobile-release.yml` below).
```

```
`docs/pending-workflows/` now holds three pending workflows,
`docs/pending-workflows/shared-tests.yml` — the shared package's test job (mobile plan Task
P4.3) — `docs/pending-workflows/console-windows.yml` — the console's Windows installer pack,
which runs at release time only rather than on a pull request, and is copied in only after the
packer and the unit suite can pass on a Windows runner — and
`docs/pending-workflows/mobile-release.yml` — the mobile app's build and store submissions
(2026-10-20). None of the three is this site's:
```

**If it has already been copied in**, it is not pending, the directory is left
holding `shared-tests.yml` and `console-windows.yml`, and the pair is the
two-file one — the same pair `## mobile-release.yml` points at for the case
where that copy-in comes second, because that is the same tree:

```
**Two files are pending: `shared-tests.yml`** (the shared package's test job, mobile plan Task
P4.3; see `## shared-tests.yml` below) **and `console-windows.yml`** (the console's Windows
installer pack, 2026-10-20 — a release-time job, not a pull-request check; see
`## console-windows.yml` below).
```

```
`docs/pending-workflows/` now holds two pending workflows,
`docs/pending-workflows/shared-tests.yml` — the shared package's test job (mobile plan Task
P4.3) — and `docs/pending-workflows/console-windows.yml` — the console's Windows installer
pack, which runs at release time only rather than on a pull request, and is copied in only after
the packer and the unit suite can pass on a Windows runner. Neither is this site's:
```

`TestStagedLinuxFilesSurviveTheCopyIn` reads **the pair under the label that
matches the tree it is run in** — it looks for `mobile-release.yml` staged
beside this section's files, and takes the pair that tree's copy-in needs — and
splices that pair into the copy it makes before running this suite there. So
both orders are checked in the tree each one leaves, and a pair that named a
file the directory does not hold fails there rather than in the owner's commit.
That is also why the marker the test finds the opening block by is **`files are
pending`**, plural and count-agnostic: a marker spelled `file is pending` stops
matching the day the sentence says "two files", and the next staging of a
workflow should not have to edit a test to add one.

**What cannot be checked here.** The linux job's steps run on a runner: this
directory's staged files are read by tests on a machine with no
`electron-builder` run, no `dpkg-deb` and no AppImage runtime, so the pack, the
`--appimage-extract`, the `dpkg-deb` reading and the three fuse read-backs are
Check 59a's to confirm on a real run, like the macOS pipeline's own first run.
What *is* asserted offline is the workflow's shape: three jobs, the permissions
each holds, `publish`'s `needs`, the action pins, that every `run:` step is
valid shell, and that the two staged console-release files differ only where
this change says they do.

## `console-windows.yml`

The desktop console's **Windows pack**, staged on **2026-10-20** by the run that
planned the console's Windows build, and re-cut the same day for the owner's
answer to that plan's decision **D7**. The owner's ruling of that date is what it
serves: **0.10.0 gets a Windows build of the Console in which the Fugaro CLI is
the *Linux* binary running inside WSL**, unsigned
([docs/console-windows/](../console-windows/README.md), and the CLI track's own
[docs/windows-cli/](../windows-cli/README.md)). One job, `console-windows`, on
`windows-latest`: `contents: read`, **no environment and no secret at all**, the
pinned toolchain (`npm ci --ignore-scripts`), `npm run typecheck`, `npm run
test:unit`, Electron's own download — which the checksums `console/package.json`
pins will cover once the `win32-x64` hash below lands, and do not today —
`npm run build`, then `npm run package:win` — which is
`electron-builder --win --publish never`, with no `npx` in front of it — an
assertion that exactly one `.exe` came out, and `upload-artifact`.

**It runs at release time only, and that is a ruling rather than a design
choice.** The triggers are `on: push: tags: ["v[0-9]+.[0-9]+.[0-9]+"]` — the same
strict tag `console-release.yml` runs on — and `workflow_dispatch` — and it is
**never on a pull request**, not even path-filtered: there is no `pull_request`
trigger and no `paths:` filter on any trigger. The owner weighed
the recommendation (a pack on every pull request) against this and answered D7
with the release-time option, in these words — *"a waste of resources; if there
are problems we will fix them then"*
([docs/console-windows/reconciliation.md](../console-windows/reconciliation.md)
D7, and the answer table at the head of its §4).

**The risk that accepts, stated rather than implied: a packaging break shows at
release time**, and it is fixed then. Nothing between releases exercises the
Windows pack, so a break in the `win` block, in the fuse table's `win32` row, or
in a lockfile bump, is found by a release rather than by a pull request. **The
mitigation is the manual dispatch**, and it is a step in the release document
rather than a hope: [docs/release.md](../release.md)'s "Before you tag" checklist
tells the owner to open Actions → `console-windows` → **Run workflow** on `main`
before the tag and to look at the artifact, which is the only dry run this design
has. The job's artifact is kept for **90 days** rather than `console-linux.yml`'s
seven, so that it is still there for the release it was dispatched for.

**It is not build-only, and that is why it is worth a runner at all.**
`console-linux.yml` proves the packaging path on every pull request; this job's
subject is that the console has never *run* on Windows, so it typechecks and runs
the unit suite before it packs — **and with the pack moved to release time, this
is the only place that suite ever runs on Windows**, which is what makes the
POSIX repairs below worth doing. The e2e suite is not run here — it drives the
console's real window under its real CSP and needs a display, which is the
owner's manual checklist's job, not a runner's.

**Read this before copying it in: it is red until three things in `console/` have
landed, and they are not this directory's to do.**

1. `console/package.json` has no `package:win` script and no `win` block, and
   `console/src/main/fuses.ts`'s `SUPPORTED_PLATFORMS` is `['darwin','linux']`
   with `fuseTarget` **throwing** for anything else. `afterPack` is the pack's
   only fuse flipper, so `electron-builder --win` produces **no artifact at
   all** today, and the pack step cannot succeed
   ([docs/console-windows/reconciliation.md](../console-windows/reconciliation.md)
   F35, and implementation-plan.md Part B).
2. The unit suite has never run on Windows: `spawn.ts`'s `isExecutableFile`
   reads `stat.mode & 0o111`, which Windows never sets, and the suite's stubs
   are host executables (F4, F33) — so the test step is red on a Windows runner
   until that is platform-aware, along with the `':'` split of the app's own
   `PATH` (F5).
3. `console/package.json`'s `build.electronDownload.checksums` pins three zips —
   `darwin-arm64`, `darwin-x64`, `linux-x64` — and **no `win32-x64` one**, so the
   Electron step downloads its distribution against no pinned hash on this
   runner. That fourth hash is a supply-chain pin and an owner-reviewable edit
   rather than a convenience (F37, Part B), and it lands with the other two.

The order the plan gives is: **stage this file, land those three edits, then copy
it in.** Staging it now is the point of this directory — the file is reviewable
while it is still a proposal, and the header's first paragraph says the same
thing to whoever opens the file rather than this page.

**Why it is its own workflow rather than a second job in `console-tests.yml`.**
Exactly `console-linux.yml`'s reason, which is a test rather than a taste: **the
release gate's required-check list is derived from `console-tests.yml`'s job
set.** `TestReleaseGateRequiresTheConsoleCheck`
(`internal/cli/docs_desktop_console_test.go`) reads that workflow and asserts
that `scripts/release-gate.sh`'s `names=(...)` array is exactly the check names
it reports plus `test`, `terraform` and `rules` — and `deriveConsoleCheckNames`
**refuses a workflow with more than one job** ("this test cannot tell which of
several jobs that is"). A second job there would therefore not be a packaging
change at all: it would change which checks gate a release tag. The release-time
trigger is a second, independent reason: a job that must not run on a pull
request does not belong in the file that runs on every one.

**This job is deliberately NOT in the gate**, and that is a recorded decision
rather than an oversight: a red Windows pack is information, not a blocked
release ([docs/console-windows/reconciliation.md](../console-windows/reconciliation.md)
D8(a), finding F41). **Owner, if you want it to gate releases**, add
`console-windows` to the `#   ` comment block and the `names=(...)` array in
`scripts/release-gate.sh`, and widen `deriveConsoleCheckNames` so it reads a
workflow with several jobs — all of it in one commit, or the test that holds
them together fails.

**The unsigned claim, plainly: the installer is not code-signed.** There is no
Authenticode identity for this project and this change buys none, so the `.exe`
this job uploads is unsigned and **Windows SmartScreen will warn that its
publisher is unknown** — *More info → Run anyway* is the way past it. A release
note saying "signed" of the Windows installer would be false, and
[docs/release.md](../release.md) carries the sentence once this lands
(docs/console-windows/reconciliation.md D10, F39).

**The artifact is this job's own, and it is not attached to any release.** The
installer is uploaded as a build artifact with a 90-day retention; **it is not an
asset of `console-vX.Y.Z`**, and a `console-vX.Y.Z` release carries macOS and
Linux and nothing else. Attaching it is a separate decision nobody has taken, and
it is not a one-line change: that release's `publish` job is the only job holding
`contents: write`, and it would have to fetch this workflow's artifact — a
mechanism the console release does not have today. **The version it packs is
`0.0.0`** for the same reason: nothing stamps `console/package.json` here, and
whether the release-time pack should stamp the tag's `X.Y.Z` with
`-c.extraMetadata.version`, the way `console-release.yml` stamps its own packs,
is part of that same undecided question. So the artifact is named
`Fugaro.Console-0.0.0-win-x64.exe`, and [docs/release.md](../release.md) says all
of this in the release's own words
(docs/console-windows/implementation-plan.md Part H).

**To copy it in**, from the repository root (and then delete the staged copy, as
`console-linux.yml`'s was: there is no byte-identity test for this file, because
it is a new workflow with nothing to be identical to):

```sh
cp docs/pending-workflows/console-windows.yml .github/workflows/console-windows.yml
git add .github/workflows/console-windows.yml
git rm docs/pending-workflows/console-windows.yml
git commit -m "console: pack the Windows installer at release time"
```

**Two follow-up edits belong in that commit** — the last section of
`website/README.md` (which `website/test/readme.mjs` reads in both directions,
count and names) and **this README's own opening line** (which
`TestConsoleLinuxStagingTellsTheOwnerWhatToCopyWhere` holds to the same
directory). **The pair of fenced blocks under `## console-release-linux.yml`
above is *not* this commit's**: those are read by
`TestStagedLinuxFilesSurviveTheCopyIn`, which splices them into a copy of the tree
with the **Linux** copy-in applied, so they describe *that* state — `shared-tests.yml`
and this one pending, plus `mobile-release.yml` when it is still staged. Applying *this* file is a different
state, and it is not the one the `## console-linux.yml` section's blocks
describe either: those name this file, which this commit deletes, and omit
`console-linux.yml`, which it leaves staged.

**Which state this copy-in creates depends on the order the owner applies the
three in**, and this file cannot fix that order: the Linux pair, `mobile-release.yml`
and this file belong to three different runs, and this file's own plan puts it
last in *its* work (Part H, after Parts B and C) without ordering it against
theirs. So the sentences are given for each state the owner can arrive in — the
two the Linux pair leaves, each with `mobile-release.yml` still staged or
already applied. **If the Linux pair has already been copied in and
`mobile-release.yml` has too**, this copy-in leaves one file pending:

```
**One file is pending: `shared-tests.yml`** (the shared package's test job, mobile plan Task
P4.3; see `## shared-tests.yml` below).
```

```
`docs/pending-workflows/` now holds one pending workflow,
`docs/pending-workflows/shared-tests.yml` — the shared package's test job (mobile plan Task
P4.3). None is this site's:
```

**If the Linux pair has been copied in but `mobile-release.yml` is still
staged**, it leaves two:

```
**Two files are pending: `shared-tests.yml`** (the shared package's test job, mobile plan Task
P4.3; see `## shared-tests.yml` below) **and `mobile-release.yml`** (the mobile app's build
and store submissions, 2026-10-20; see `## mobile-release.yml` below).
```

```
`docs/pending-workflows/` now holds two pending workflows,
`docs/pending-workflows/shared-tests.yml` — the shared package's test job (mobile plan Task
P4.3) — and `docs/pending-workflows/mobile-release.yml` — the mobile app's build and store
submissions (2026-10-20), neither of which is this site's:
```

**If the Linux pair has not been copied in and `mobile-release.yml` is still
staged**, it leaves four — `shared-tests.yml`, `console-linux.yml`,
`console-release-linux.yml` and `mobile-release.yml`:

```
**Four files are pending: `shared-tests.yml`** (the shared package's test job, mobile plan Task
P4.3; see `## shared-tests.yml` below), **`console-linux.yml`** (the console's Linux build check,
2026-10-20; see `## console-linux.yml` below), **`console-release-linux.yml`** (the console's
signing pipeline with the Linux legs added, 2026-10-20 — it supersedes the staged
`console-release.yml`; see `## console-release-linux.yml` below) **and `mobile-release.yml`** (the
mobile app's build and store submissions, 2026-10-20; see `## mobile-release.yml` below).
```

```
`docs/pending-workflows/` now holds four pending workflows,
`docs/pending-workflows/shared-tests.yml` — the shared package's test job (mobile plan Task
P4.3) — `docs/pending-workflows/console-linux.yml` — the console's Linux build check, which is
packed on every pull request and holds no secret — `docs/pending-workflows/console-release-linux.yml`
— the console's signing pipeline with the Linux legs added, which supersedes the staged
`console-release.yml` and so is copied over it as well as into `.github/workflows/` — and
`docs/pending-workflows/mobile-release.yml` — the mobile app's build and store submissions
(2026-10-20). None of the four is this site's:
```

**If neither has been copied in**, it leaves three — `shared-tests.yml`,
`console-linux.yml` and `console-release-linux.yml` — and the sentences are:

```
**Three files are pending: `shared-tests.yml`** (the shared package's test job, mobile plan Task
P4.3; see `## shared-tests.yml` below), **`console-linux.yml`** (the console's Linux build check,
2026-10-20; see `## console-linux.yml` below) **and `console-release-linux.yml`** (the console's
signing pipeline with the Linux legs added, 2026-10-20 — it supersedes the staged
`console-release.yml`; see `## console-release-linux.yml` below).
```

```
`docs/pending-workflows/` now holds three pending workflows,
`docs/pending-workflows/shared-tests.yml` — the shared package's test job (mobile plan Task

P4.3) — `docs/pending-workflows/console-linux.yml` — the console's Linux build check, which is
packed on every pull request and holds no secret — and
`docs/pending-workflows/console-release-linux.yml` — the console's signing pipeline with the
Linux legs added, which supersedes the staged `console-release.yml` and so is copied over it as
well as into `.github/workflows/`. None is this site's:
```

**The count word `five` is the one addition these sentences needed to
`website/test/readme.mjs`**: its map already carried `one`, `two`, `three` and
`four`, and the sentence the mobile release's staging leaves behind counts five
files, so the map and the regex beside it grew `five` in the same change.
`TestConsoleLinuxStagingTellsTheOwnerWhatToCopyWhere` reads the names back
against this directory in every state, so a sentence that names the wrong set
fails there whether or not the count word is one this map knows.

**What cannot be checked here, and what nothing checks yet.** This file's steps
run on a runner: the staged copies in this directory are read by tests on a
machine with no `electron-builder` run and no Windows, so the typecheck, the
unit suite, the pack and the fuse read-back are the first real run's to confirm —
and that first real run is now a **manual dispatch**, not a pull request. The
owner's manual checklist in
[docs/console-windows/implementation-plan.md](../console-windows/implementation-plan.md)
§4 is what turns the uploaded installer into evidence, and it is why the dry run
in [docs/release.md](../release.md) matters more here than it would if a pull
request ran this job every day.

**What *is* checked offline, and by which test.** Two guards in
`internal/cli/docs_windows_console_test.go` read this file's **text**, through
the same `consoleWorkflowText` helper the Linux file's guards use (so they pass
before and after the copy-in):
`TestWindowsConsoleWorkflowIsReleaseTimeOnly` — the trigger set is exactly the
release tag and `workflow_dispatch`, there is no `pull_request` trigger and no
`paths:` filter on any trigger, one job named `console-windows` on
`windows-latest` with `contents: read` at both levels and no `secrets.`,
`environment:`, `pull_request_target`, `workflow_run` or `write-all` anywhere in
it, every `uses:` a 40-hex pin with its `# vX.Y.Z`, the six `run:` steps, the
`unset CSC_*` line beside the exactly-one-`.exe` assertion, and an
`upload-artifact` with `if-no-files-found: error` and `path:
console/release/*.exe` — and
`TestWindowsConsoleStagingTellsTheOwnerWhatToCopyWhere`, which holds this section
and `website/README.md`'s sentence to that file. **What is still prose, with a
reviewer as the only thing checking it**: that the three `console/` edits above
have landed, that the pack genuinely produces one installer, and that the fuse
bytes are where the `win32` row says. Those are the first real run's, which is a
manual dispatch before a tag.

## `mobile-release.yml`

**Status: `mobile-release.yml` is staged and pending.** The copy has not been
made — there is no `.github/workflows/mobile-release.yml` — and this document's
opening line says the same. `TestDocsMobileReleaseWorkflowIsStagedAndPinned`
(`internal/cli/docs_mobile_release_workflow_test.go`) reads the staged file, and
`TestDocsMobileReleaseStagingTellsTheOwnerWhatToCopyIn` reads this
section, so the two cannot drift apart silently. (Those names are held to this
document by that second test, so the paragraph cannot go on naming a check that
was renamed or deleted.) **That second test also reads this line against the
tree, in both directions** — the way
`TestSharedTestsWorkflowStagingStateIsWhatTheReadmeSays` reads
`shared-tests.yml`'s — so the copy-in below changes it to
`**Status: `mobile-release.yml` is applied, not pending.**`, and a document that
kept saying "pending" after the file was copied in would be red.

The mobile app's release. `mobile/` is its own artifact outside the Go module
(D125) with its own release path, and this is that path: **on every release tag
(`v[0-9]+.[0-9]+.[0-9]+`, the pattern the sibling release workflows use), and on
a manual dispatch**, one job per platform builds the app on EAS and submits the
finished build to TestFlight's internal testers and to Google Play's
internal-testing track. **Both jobs are enabled** — the owner's ruling of
2026-10-20 is that iOS and Android both ship from here — and one repository
variable can turn either platform off (`MOBILE_RELEASE_DISABLED_PLATFORMS`,
below).

**A new file**, so nothing is replaced: it stages `.github/workflows/mobile-release.yml`
entire. It is a separate file from `ci.yml` for the reason `console-release.yml`
is: **the job holds an Expo access token and publishes to a store**, and a branch
of this repository can open a pull request, so a job holding that material must
never sit in a file that runs on every pull request. This one does not even run
on a pull request: its triggers are a tag push and a manual dispatch, and nothing
else.

**What the jobs are.** Two jobs, `ios` and `android`, each on the pinned
`ubuntu-24.04` — the build itself runs on EAS's own workers, so the runner is
only the client that uploads it — with `contents: read` and the `checks: read`
the release gate below needs, and nothing else, and **neither job needs the
other**, so a platform that fails never blocks the other's release. Each job
checks the repository out with `persist-credentials: false` and
`fetch-depth: 0`, runs the two gate steps (below), takes the Node line from
`mobile/package.json`'s `engines.node` (the same way `console-linux.yml` takes
the console's), installs `eas-cli` **at one exact version**, masks the store
identity, runs `eas build
--platform <platform> --profile internal --non-interactive --wait --json` under
`eas env:exec production`, writes the build's **id** into
`$GITHUB_STEP_SUMMARY`, and then runs `eas submit --profile internal --platform
<platform> --id <that build> --non-interactive`. Every action is pinned by full
commit SHA with its `# vX.Y.Z` beside it. The `concurrency` group is the
workflow's own name and not the ref, so two releases queue rather than submit at
once, and a release that is mid-submission is never cancelled.

**Nothing is published unless it is on main and its checks passed.** On a tag
push the **first step of each job** is `scripts/release-gate.sh "$TAG"` — the
same gate `release.yml`, `images.yml` and `console-release.yml` run first, which
refuses unless the tag is a strict `vX.Y.Z`, its commit is reachable from
`origin/main`, and the required checks (`test`, `terraform`, `rules` and both
console legs) succeeded on it. Without it, a tag pushed on an off-main or red
commit would still build on EAS and submit to TestFlight and to Google Play:
this is a publishing workflow, so it is held to the same rule as every other one
here (`docs/release.md`, "What a tag does"). That step carries
`if: github.event_name == 'push'`, the same condition the sibling workflows
carry, because the gate is a claim about a tag.

**A manual dispatch that would submit passes a gate too.** The environment's
approval is a person deciding to *run* this workflow, not a review of the commit
it would submit, and a dispatch can name a **tag** as well as a branch
(`gh workflow run --ref v1.2.3`): with only the step above, a dispatch on an
off-main branch — or on a `vtest` tag — would build on EAS and submit behind
nothing but that click. So each job carries a **second gate step**, under
`if: github.event_name == 'workflow_dispatch' && github.event.inputs.dry_run != 'true'`:
a dispatched **tag** goes through the whole gate (`scripts/release-gate.sh
"$REF_NAME"`, the same script, so the tag shape and the required checks are
checked as they are on a push), and a dispatched **branch** through the gate's
second half — `git fetch origin main` and `git merge-base --is-ancestor
"$GITHUB_SHA" origin/main` — because a branch has no tag to shape-check and "this
commit is on main" is the claim a submission needs either way. A dispatch with
`dry_run` set submits nothing and is held to neither, which is what keeps a dry
run runnable from a work-in-progress branch.

Because the gate reads the commit's check-runs, each job holds **`checks: read`
beside `contents: read`** — and nothing else — and the checkout is **full-depth
(`fetch-depth: 0`)**, because the gate asks whether that commit is reachable from
`origin/main`, which a shallow clone cannot answer.
`TestDocsMobileReleaseWorkflowIsStagedAndPinned` holds each job to exactly two
gate steps, to those exact conditions, to the tag path and the on-main path
inside the second one, and to both coming before anything is built or
submitted.

**The submission submits the build the run made, and the summary carries no
artifact link.** The build step captures `eas build --json` into
`$RUNNER_TEMP/mobile-build.json`, and the submission reads that build's id out of
it and passes it to `eas submit --id`. `--latest` is not used anywhere: it takes
the latest build of the profile, whoever made it, which is a different claim —
and one a second run of the workflow would silently change. The summary prints
the build's **page id** and nothing else. It deliberately does **not** print
`artifacts.buildUrl`: that link downloads a signed binary whose Info.plist
carries the bundle identifier, and a run summary in a public repository is
public. The id names a page on expo.dev that needs an Expo login, which is what
a reviewer needs and what a stranger cannot use.

**The store identity is not in this file, and cannot be.** The iOS bundle
identifier and the Android package name live in the **EAS project's own
`production` environment** (`mobile/README.md`, "Building for internal testing"),
which is where the owner set them with `eas env:set`; both `eas` commands run
through **`eas env:exec production`**, which is what loads them into the process.
That matters twice over: EAS's build worker resolves `mobile/app.config.js` with
the profile's environment, and the **local** `eas-cli` process that uploads the
build resolves the same file before it uploads anything — the state the owner's
first local submission stopped in, with `Specify ios.bundleIdentifier`. So there
is nothing to add to the repository for the identity: no secret, no variable, no
edit to `mobile/app.json` or `mobile/eas.json`.

**The identity is masked before any `eas` command runs.** `eas build` and
`eas submit` resolve the app's config locally, and a resolved config, a
credential prompt or a submit report can carry the bundle identifier into a job
log — and this repository is public. So the first step of each job that touches
`eas` reads the two values out of the EAS project's `production` environment and
hands each one to **`::add-mask::`**, the only mechanism that hides a value from
a log it would otherwise reach. The values are never echoed: each one lives in a
shell variable for the length of one loop and nowhere else, and the step prints
nothing but the mask commands. **This is the one place in the whole workflow
where the two variable names appear**, and it has to be: a mask cannot hide a
value it is not given. `TestDocsMobileReleaseWorkflowIsStagedAndPinned` holds
each name to exactly one occurrence per job and requires that occurrence to be
the argument of a `mask` call in that job's mask step. A read that fails masks
nothing and **stops the job**, before the build step. `mobile/app.config.js`
requires the identity only on EAS's own build machines, so the build does not
fail on its own when the mask is missing: it resolves the identity and prints it
into this public log. A run that cannot mask a value is therefore a run that must
not build, and the step says which variable it could not read and prints no
value.

**The one thing the owner has to check by eye, on the first dry run.** No test
can prove that a job log carries no identifier, because no test can run the job
and no `eas` command has ever been run from this repository. On the **first dry
run** — a manual dispatch with `dry_run` left checked, which submits nothing —
the owner reads the run's log and confirms that **no bundle identifier or package
name appears anywhere in it**. It is written as an item in the owner's list
below rather than as a number: it is a thing to look at, not a check that has
been made. If an identifier does appear, the mask step is the thing to look at
first, and the fix is in this file, which an agent cannot deliver — so it is
worth doing before the first release rather than after.

**`dry_run` stops before the submission.** The manual dispatch carries one
boolean input, `dry_run`, defaulting to **true**: a person trying the workflow
gets a build, a summary and no store upload, and dispatches again with it
unchecked to submit. A tag push carries no input, so it always submits — which is
what a release means. The step conditions read `github.event.inputs.dry_run` and
not the `inputs` context, because that context is documented for a manual
dispatch and a reusable workflow while this file also runs on a tag push.

**One switch turns either platform off, and both are on by default.** Each job
runs unless the repository variable **`MOBILE_RELEASE_DISABLED_PLATFORMS`** names
its platform:

```
ios      if: ${{ !contains(vars.MOBILE_RELEASE_DISABLED_PLATFORMS, 'ios') }}
android  if: ${{ !contains(vars.MOBILE_RELEASE_DISABLED_PLATFORMS, 'android') }}
```

An **unset** variable names neither platform, so both jobs run — that is what
"both platforms release by design" means, and it is why the switch is a skip list
rather than an enable list. It is one variable rather than two so that the owner
reads one setting instead of a pair that can disagree, and it is a repository
variable rather than a workflow edit because an agent cannot make a workflow edit
at all (GitHub refuses the push). To stop Android releasing while the Play side is
not ready — the state the checklist below describes — **[per repo]**:

```sh
# Not a secret, and it carries no value of anyone's: it names the platforms to
# skip. `ios,android` turns both off.
gh variable set MOBILE_RELEASE_DISABLED_PLATFORMS --repo "$REPO" --body android
```

`TestDocsMobileReleaseWorkflowIsStagedAndPinned` compares each job's `if:` **whole**
against exactly the line above for its platform, so a condition with a second
disjunct — a tag push, say, which would start a submission the owner turned off —
is not green here.

**While the Play side is not ready, set this before the copy-in and not after
it.** The Android job is enabled by default — an unset variable names neither
platform — so a copy-in that lands first is a workflow that can build and submit
to Play on its very first run, and the environment's approval click says a person
ran the workflow, not that the Play side is ready. `MOBILE_RELEASE_DISABLED_PLATFORMS=android`
is the state `### Android first release` below describes, and it is step **3** of
the owner's list under `### Before it can run` for that reason.

### Before it can run: the three things the owner does first

None is in the file, and none can be: a staging file cannot create a GitHub
environment or a secret, and it cannot read a run's log. **Each step below is
labelled with where it is done** — the repository, or the owner's own Expo
account. (The Apple side needs no step of its own here: the App Store Connect key
and the signing certificate are EAS's, server-side, under the owner's own
account — the closing paragraph of this section says so.)

**The environment, the secret and — while the Play side is not ready — the
Android switch have to be done *before* the file is copied in, not after it.**
GitHub creates a missing environment the first time a job names it, and the
environment it creates that way has **no required reviewer at all** — so the
first run of a copied-in workflow against a missing `mobile-release` would go
straight through, holding the Expo token, with nobody's click in between. The
Android switch is the same rule from the other side: the Android job is enabled
by default, so a copy-in made before `MOBILE_RELEASE_DISABLED_PLATFORMS=android`
is set is a file whose first run can submit to Play. The copy-in is deliberately
the last thing in this section: an environment that already exists is not
re-created, and the secret on it is unreadable until the reviewer approves.

**1. [per repo] The `mobile-release` environment, with the owner as its only
required reviewer.** This is the gate: GitHub holds the job until that reviewer
approves it, however the run was started — the same click `console-release` and
`website-live` wait for. Either in the repository's **Settings → Environments**
(create `mobile-release`, add yourself under **Required reviewers**, and under
**Deployment branches and tags** choose **Selected branches** with `main` and the
tag pattern `v*`), or with the API:

```sh
REPO=dimipaun/fugaro   # the repository the workflow runs in

gh api --method PUT "repos/$REPO/environments/mobile-release" --input - <<JSON
{
  "reviewers": [{"type": "User", "id": $(gh api users/dimipaun --jq .id)}],
  "deployment_branch_policy": {"protected_branches": false, "custom_branch_policies": true}
}
JSON

# `main`, so a manual dispatch from the default branch is allowed...
gh api --method POST "repos/$REPO/environments/mobile-release/deployment-branch-policies" \
  -f name=main
# ... and `v*`, so a release tag is. Both are needed: a policy that named only
# `main` would refuse every tag, and one that named only tags would refuse every
# manual dispatch.
gh api --method POST "repos/$REPO/environments/mobile-release/deployment-branch-policies" \
  -f name='v*' -f type=tag
```

The policy's `v*` is deliberately **wider** than the workflow's own tag filter
(`v[0-9]+.[0-9]+.[0-9]+`): the workflow is what decides which tags start a
release, and the environment's policy is a gate that has to let them through.
Tightening this line is the one edit here that can block every release rather
than admit a wrong one, so leave it as it is.

**Leave "Prevent self-review" off.** The owner is both the actor and the only
reviewer, and GitHub would otherwise refuse the owner's own approval of the
owner's own run — the environment would hold every release forever. (The same
note is in `docs/website/preview-promote.md`, step 6, for the same reason.)

**2. [per Expo account] An Expo access token, and [per repo] the secret
`EXPO_TOKEN` holding it.** The token is created in the owner's own Expo account
(expo.dev → **Access tokens** → *Create token*), and the workflow reads it as
`secrets.EXPO_TOKEN`. **Create it as a secret on the `mobile-release`
environment**, not as a repository secret: the environment is what withholds it
until the approval above, and a repository secret of the same name would be
readable by every workflow in the repository, pull-request-triggered ones
included. Either in **Settings → Environments → mobile-release → Environment
secrets → Add secret**, or:

```sh
# Run this where you can paste the token; it is never written into this
# repository and no agent has ever seen a value.
gh secret set EXPO_TOKEN --repo "$REPO" --env mobile-release
```

**3. [per repo] `MOBILE_RELEASE_DISABLED_PLATFORMS=android`, before the copy-in,
while the Play side is not ready.** The command is the `gh variable set` one under
"One switch turns either platform off" above, with why it is a repository variable
rather than a workflow edit. It is a step of its own here rather than a footnote
because of the order: the Android job is enabled by default, and the environment's
approval is not a statement about whether the Play side — the account, the app
record and the forms `### Android first release` below lists — is ready. Set it
**before** the copy-in, and clear it (`gh variable delete
MOBILE_RELEASE_DISABLED_PLATFORMS --repo "$REPO"`, or set it to `ios`) only when
that checklist is done.

**4. [per repo, after the first dry run] Read the first dry run's log and
confirm it carries no identifier.** Dispatch the workflow once with `dry_run`
left checked — it builds, writes its summary and submits nothing — and then read
that run's log for the bundle identifier and the package name. **No test can make
this check for you**: nothing here runs GitHub Actions, and no `eas` command has
ever been run from this repository, so whether `eas build` or `eas submit` prints
an identity into a job log is the kind of thing that can only be known by
looking. It is an item to do rather than a numbered check, because a number would
claim a check that has not been made. If an identifier does appear, the mask step
is what to look at first — and its fix is an edit to this workflow, which an
agent cannot deliver (GitHub refuses the push), so it is worth doing before the
first release rather than after it.

**That is the whole list.** `EXPO_TOKEN` is the only secret this workflow needs,
and it is the only `secrets.` reference in the file: the App Store Connect API
key and the signing certificate are EAS's (server-side, under the owner's
account), which is why the submission needs no second credential here.
`TestDocsMobileReleaseWorkflowIsStagedAndPinned` asserts that the file names
exactly that one secret and carries no literal that could be one.

**Nothing is added to the repository for the store identity**, because the
identity is not here: `FUGARO_IOS_BUNDLE_ID` and `FUGARO_ANDROID_PACKAGE` stay in
the EAS project's `production` environment, where the owner set them
(`mobile/README.md`, "Building for internal testing"), and the file reads them
only through `eas env:exec production` and the mask step above. **The two names
appear in the workflow exactly once per job — as the mask step's arguments — and
nowhere else**: a value can only be masked by naming the variable it is read
from, and that is the whole of the exception. The test above holds each name to
that count, refuses a name bound in any step's `env:` or `with:`, and refuses the
file outright if a reverse-DNS-shaped value appears anywhere in it — **in either
case**, so a pasted `com.Fugaro.App` is caught exactly as `com.fugaro.app` is
(the identity's labels may be either case, `mobile/README.md`).

### To copy it in

```sh
cp docs/pending-workflows/mobile-release.yml .github/workflows/mobile-release.yml
git add .github/workflows/mobile-release.yml
git rm docs/pending-workflows/mobile-release.yml
git commit -m "mobile: build and submit the app on a release tag"
```

The `git rm` is not cleanup, it's load-bearing, for the reason it was for
`images.yml`: while a staged copy exists the tests above read **it**, so leaving
it in place would freeze them on its bytes and hide a later edit to the file that
runs. This file is new, so there is no byte-identity clause to keep it — unlike
`console-tests.yml` and `console-release.yml`, whose staged copies stay on
purpose.

**Three follow-up edits belong in that same commit.** Two are the sentences that
count and name the files still pending in this directory, and both are held to
the directory: the last section of `website/README.md` (which
`website/test/readme.mjs` reads in both directions, count and names) and **this
README's own opening line** (which
`TestConsoleLinuxStagingTellsTheOwnerWhatToCopyWhere` holds to the same
directory). The third is **this section's own Status line** — it reads
`**Status: `mobile-release.yml` is staged and pending.**` while the staged copy
exists and has to become
`**Status: `mobile-release.yml` is applied, not pending.**` with it gone, because
`TestDocsMobileReleaseStagingTellsTheOwnerWhatToCopyIn` reads that line against
the tree in both directions the way
`TestSharedTestsWorkflowStagingStateIsWhatTheReadmeSays` reads
`shared-tests.yml`'s, so copying the file in without this edit leaves the suite
red. Applying this file leaves the directory holding `shared-tests.yml`,
`console-linux.yml`, `console-release-linux.yml` and `console-windows.yml`, so the two sentences that
commit needs are (each block is the **exact** text to write in place of the
sentence it replaces; the opening-line block is the whole claim, from its first
bold marker to the end of its last sentence, and the sentence that follows it in
this file — "**Two other files stay staged here by design**, `console-tests.yml`
and `console-release.yml`" — is unchanged, because those two do stay staged):

```
**Four files are pending: `shared-tests.yml`** (the shared package's test job, mobile plan Task
P4.3; see `## shared-tests.yml` below), **`console-linux.yml`** (the console's Linux build check,
2026-10-20; see `## console-linux.yml` below), **`console-release-linux.yml`** (the console's
signing pipeline with the Linux legs added, 2026-10-20 — it supersedes the staged
`console-release.yml`; see `## console-release-linux.yml` below) **and `console-windows.yml`** (the
console's Windows installer pack, 2026-10-20 — a release-time job, not a pull-request check; see
`## console-windows.yml` below).
```

```
`docs/pending-workflows/` now holds four pending workflows,
`docs/pending-workflows/shared-tests.yml` — the shared package's test job (mobile plan Task
P4.3) — `docs/pending-workflows/console-linux.yml` — the desktop console's Linux build check
(2026-10-20: it packs the AppImage and the `.deb` on `ubuntu-latest` and uploads them, and
holds no secret) — `docs/pending-workflows/console-release-linux.yml` — the console's
signing pipeline with the Linux legs added, which supersedes the staged `console-release.yml`
and so is copied over it as well as into `.github/workflows/` — and
`docs/pending-workflows/console-windows.yml` — the console's Windows installer pack, which runs
at release time only rather than on a pull request, and is copied in only after the packer and
the unit suite can pass on a Windows runner. None of the four is this site's:
```
The second block is the head of `website/README.md`'s last paragraph, up to and
including the words "the website's two manual" — the sentence continues as it
does today, and `website/test/readme.mjs` reads the whole page rather than this
sentence, so the rest of it needs no edit. It also reads the count as a word and
compares it as a number, so `four` here is what the directory holds after this
copy-in; the word `five` is the one this document's opening line carries while
the file is pending.

**The two copy-ins of 2026-10-20 can be made in either order, and the pair
depends on which one this is.** The two blocks above are the pair for making
**this** copy-in **first** — the order this tree is in (`mobile-release.yml`
applied, the console's two files still staged), and the pair
`TestDocsMobileReleaseStagingTellsTheOwnerWhatToCopyIn` reads out of this section
and holds to the directory **while the console's two files are still staged
beside it, which is the tree the pair describes**. If the console's two files
have been copied in first, this copy-in leaves **two** pending files rather than
four, and the pair to write is the two-file pair printed under the matching
label in `## console-release-linux.yml` above — the same pair that section
prescribes for its own copy-in when this file has already been applied, because
it is the same tree either way: the directory is left holding `shared-tests.yml`
and `console-windows.yml`. (Making both copy-ins in one commit leaves that tree too, so that pair is
the one to write then as well.) Read the count and the names off the directory,
which is what both sentences are held to.

**The Status line is the one thing in this section that does not survive the
copy-in**: it is read against the tree, so the same commit has to change it (the
third edit above). The two sentences do survive it, when this copy-in is the one
made first — the directory is left holding the four files the block above names
— and `website/README.md` is never left naming a set this file is not in while it
is still here: `readme.mjs` reads the directory, not the file's own history.

### Android first release

**The Android job is enabled; the Play side is what is not ready.** Nothing below
is a workflow change, and nothing below is a secret in this repository. **Each
step is labelled with the account or the machine it is done on, and no agent runs
any of it** — the rule every store step in this document is under (mobile plan
D120, Global Constraint 9). The order is the order: the account and the app record
come first, because the first upload cannot be made by CI at all.

**1. [per Google account] A Google Play Console developer account, and the app
created in it.** The developer account is registered by the owner in the Play
Console, and the app record is created there too — **by hand, in the console, not
by a workflow and not by `eas`**. That is the prerequisite the whole platform
hangs on: `eas submit` publishes an app's *later* releases, and a brand-new Play
app's **first** upload has to be made once through the Play Console's own UI,
because neither the Play API nor `eas submit` can create an app's first release
(`mobile/README.md`, "Building for internal testing" — the sentence that also
records how much of the Play side is unverified).

**2. [per Play Console] The forms the internal-testing track needs, by name.** A
new Play app cannot publish to the internal-testing track until the console has
what it asks for. The forms are: **Store listing** (the app name, the short and
full description, the app icon and the feature graphic, and at least two
screenshots), the **Data safety** form, the **Content rating** questionnaire, the
**Target audience and content** declarations, and — on the internal-testing track
itself — the **"any other information required for access"** field. Names only:
the answers are the owner's to give, and the one already recorded in this
repository is in `docs/mobile/play-review-access.md`, which holds the access
paragraph to paste into that last field (what "Try the demo" does, with no account
and nothing to set up first). What the other forms ask for is Google's to say and
the owner's to answer, and **nothing here has verified any of it**.

**3. [per Google Cloud, then per Play Console] A service account for `eas submit`,
with its key stored only in EAS.** In Google Cloud: create a service account,
enable the **Google Play Android Developer API**, and create a **JSON key** for
it. In the Play Console: **Users and permissions → API access**, invite that
service account's address, and give it the **Release to testing tracks**
permission. The JSON key is then stored **by the owner in the EAS project's
credentials for the Android submission and nowhere else** — not in this
repository, not in a GitHub secret, not in a file under `mobile/`, and **never
given to an agent**: `eas submit` reads it from EAS, which is why this workflow
carries no Play credential of its own. (`mobile/README.md`, "Building for internal
testing", is where the Play credential's home is recorded.)

**4. [per owner's machine] The quickest path to something installable, before any
of that.** An APK from the `preview` profile needs no Play account, no track, no
form and no service account: `preview` is an internal-distribution build that
reaches no store (`mobile/README.md`, "Building for internal testing"), and it
installs straight onto a device. The build is `eas build --platform android
--profile preview`, wrapped in `eas env:exec preview` so that the profile's own
EAS environment — where the package name lives — is loaded into that one command:

```sh
# --- as the owner, at your own terminal, in mobile/ ---
eas env:exec preview 'eas build --platform android --profile preview'
```

That command is the owner's, like every other `eas` command here: no agent has run
it and none may. It is worth doing **first**, because it answers the question the
rest of this list cannot — whether the app builds and installs on a device at all
— without touching the Play side, and it is the fastest way to have something in
hand while the console work is still outstanding.

**Until those are done, leave Android off.** The switch above is
`MOBILE_RELEASE_DISABLED_PLATFORMS=android`, and with it set the Android job is
skipped and the iOS release is unaffected — which is what the switch is for.

### Not verified

**The workflow has never run, and no `eas` command in it has ever been run from
this repository.** No Expo account, no Apple account and no store credential were
available or permitted in the run that wrote it, and no agent runs `eas build`,
`eas submit` or touches a signing key or a store credential (the mobile plan's
Global Constraint 9, D120). Check 89 (`docs/gcp-live-checklist.md`) is the
owner-run check for the store path, and it is **NOT RUN**.

What *is* asserted offline, by `TestDocsMobileReleaseWorkflowIsStagedAndPinned`,
is the file's text: its two triggers and the absence of any third, on the tag
pattern `v[0-9]+.[0-9]+.[0-9]+`; `contents: read` at the top and
`{contents: read, checks: read}` on each job — the second being what the release
gate's check-runs read needs — with no other scope and no job-level
`env:`; the **release gate** as each job's first step, carrying exactly
`if: github.event_name == 'push'`, and the **second gate step** a manual
dispatch that would submit runs, carrying exactly
`if: github.event_name == 'workflow_dispatch' && github.event.inputs.dry_run != 'true'`,
routing a dispatched tag through `scripts/release-gate.sh` and a dispatched
branch through `git merge-base --is-ancestor "$GITHUB_SHA" origin/main`, and
stopping the job when that check fails — with `fetch-depth: 0` on the checkout
they depend on; the `mobile-release` environment
on both jobs; the single
`secrets.EXPO_TOKEN` reference — bound in the `env:` of the steps that run an
`eas` command and on no other step, with no job-level `env:` at all — and no
secret-shaped literal; every `uses:` a full 40-hex SHA with its `# vX.Y.Z`, and
the one install, `eas-cli@25.3.0`, pinned by version rather than floating; the
`concurrency` group and `cancel-in-progress: false`; the `dry_run` input and the
step conditions that read it, **each compared whole** (`${{ github.event.inputs.dry_run != 'true' }}`
on both submissions, its exact complement on the notice, and the dispatch gate's
`github.event_name == 'workflow_dispatch' && github.event.inputs.dry_run != 'true'`), so a condition with
a second disjunct — a tag push, say, which would submit on a dispatch that asked
for a dry run — is not green here; each job's `if:` compared whole against
`${{ !contains(vars.MOBILE_RELEASE_DISABLED_PLATFORMS, '<platform>') }}`, so the
switch is that one variable by name and no second disjunct can start a submission
the owner turned off; both platforms' `eas` commands under `eas env:exec
production` and carrying `--non-interactive`; the build step's `--wait` and
`--json`; the summary step writing to `$GITHUB_STEP_SUMMARY` and carrying the
build's **id**; that no step runs `--latest` or `eas build:list` or reads
`artifacts.buildUrl`; the mask step in each job, before the first `eas` command
that resolves the app's config, naming both identity variables — each of which
appears **exactly once per job and nowhere else** in the file — reading each one
non-interactively into a shell variable and stopping the job on a read that fails
or comes back empty rather than masking nothing and carrying on; that the
submission checks the build's id is **UUID-shaped** before pasting it into the
submit command; and that no
reverse-DNS-shaped value appears anywhere in it, **in either case** — the scan
is case-insensitive on purpose, because a lowercase-only one would pass a pasted
`com.Fugaro.App`, which is the accident it exists to catch. (A handful of
dotted names the file is allowed to carry are listed explicitly — `engines.node`,
`submit.internal.android.track`, `android.buildType`, `artifacts.buildUrl`,
`Info.plist` — plus the `github.` event-payload namespace the conditions read.
Each is a field or file name, never a value.)

**What none of that can tell the owner is what a log looks like.** The mask step
is asserted to exist, to run before the first `eas` command and to name both
variables; that `::add-mask::` actually keeps an identity out of `eas`'s output
is a fact about `eas-cli` and GitHub Actions, and the only instrument for it is a
person reading the first dry run's log (item 4 above). That gap is what item 4 is
for, and it is why the item is on the owner's list rather than in this paragraph.

**`eas-cli` is pinned to one exact version, and it is the only install in the
file.** `npm install --global eas-cli@25.3.0` — **25.3.0** was the version the
npm registry's `latest` tag pointed at on 2026-10-21, checked with
`npm view eas-cli version` against the registry itself rather than a page that
quotes it. The earlier version of this file installed it unpinned, and the reason
written here for that was wrong: `eas-cli` is not a dependency of the app and
does not have to become one (`mobile/package.json` declares none, and this is a
global install on the runner), so a lockfile entry was never the price of the
pin. The price is that a pin is a version someone has to raise — raising it is a
deliberate edit to this file, checked the same way (read the registry's `latest`,
write the version, and the test names both if they disagree). Every other pin in
the file is a commit SHA with its version beside it; this one is a version, and
it is the only pin that is not a commit.

## `website-preview.yml` and `website-promote.yml`

Two **new** workflows, both `workflow_dispatch`-only, staged by the run that
wrote the website's deploy path (website plan Task W2.1, D91, D109, F14). They
replace nothing: `.github/workflows/` holds `ci.yml`, `images.yml` and
`release.yml` today, and neither staged file touches the release train.

- **`website-preview.yml`** builds a chosen ref (`ref`, default `main`) and
  runs the site's own gate (`npm run check` in `website/`: the Astro build plus
  every `website/test/*.mjs`) in a `build` job that holds **no** `id-token`
  scope, then a separate `deploy` job takes the build as an artifact, refuses a
  `firebase.json` with a lifecycle hook or a `public` dir other than
  `website/dist`, authenticates, and deploys the result to a **preview channel**
  of the website's own Firebase project with `firebase hosting:channel:deploy
  --expires 7d --json`. It writes the channel, the URL, the ref and the commit
  into the job summary, because the URL is the run's whole output. The live
  channel is never touched. **A preview is public**: anyone with the URL can
  read it.
- **`website-promote.yml`** copies an existing preview channel's current
  version onto the live channel with `firebase hosting:clone` — the bytes the
  owner already looked at, rebuilt nowhere — and it is the owner's alone, twice
  over: the job carries `environment: website-live` (whose only required
  reviewer is the owner, so GitHub holds the job for approval) and its `if:`
  refuses any actor but the owner, so a dispatch by anyone else never even
  queues an approval request. A second step checks the actor again, so a later
  edit that lost the `if:` still stops rather than asking for approval.

**Neither file holds a credential, and neither reads a secret.** Authentication
to Google is Workload Identity Federation (`google-github-actions/auth`, pinned
by SHA): the run's own OIDC token is exchanged for a short-lived access token
for a deployer service account that holds only Firebase Hosting roles on the
site's project. No service account key, no key file, no `FIREBASE_TOKEN`, no
`secrets.` reference at all — the three values both files read
(`FIREBASE_PROJECT_ID`, `GCP_WORKLOAD_IDENTITY_PROVIDER`,
`GCP_DEPLOYER_SERVICE_ACCOUNT`) are repository **variables**, which are
identifiers rather than credentials. **No Firebase project id is committed by
either file**: the project is named on the firebase CLI's command line from the
variable, exactly as `scripts/deploy-website.sh` names it from its own
`--project` argument, and there is no `.firebaserc`.

**The channel-name pattern is the script's, character for character.**
`^[A-Za-z0-9][A-Za-z0-9_-]{0,62}$`, the pattern
`scripts/deploy-website.sh` validates `--channel` against, is written into each
workflow's validation step and read back out of both files by
`internal/cli/docs_website_deploy_test.go`, which fails if the two ever drift.

`internal/cli/docs_website_deploy_test.go` is the static check these two files
have: it parses each staged YAML (the fields GitHub executes, not strings in
comments) and requires `workflow_dispatch` to be the **only** trigger, no
`pull_request_target` anywhere, every `uses:` pinned to a full commit SHA with
its version comment, an explicit least-privilege `permissions:` block on the
workflow and on every job, `id-token: write` only on the job that
authenticates (never on the preview's `build` job, which also may not share a
job with the auth step), no `${{ }}` expression inside any `run:` script, the
environment and the owner guard on the promote
job, the channel pattern above, the preview URL written to
`$GITHUB_STEP_SUMMARY`, and **no key-file credential** — no `credentials_json`,
no token flag, and no `secrets.` reference — in any of them. A companion test
feeds that checker a planted weakening per rule and requires each to be
reported, so no check can rot unnoticed. A second test holds
`docs/website/preview-promote.md`'s WIF provider condition to this repository
(name and id), `refs/heads/main` and `workflow_dispatch`, and refuses a key.

**The owner's one-time setup is `docs/website/preview-promote.md`**, and it is
the owner's alone: the WIF pool and provider, the deployer service account and
its roles, the `website-live` environment and the three repository variables.
No agent creates any of it and no agent runs any of its commands. Until it is
done, both workflows stop at their first validation step with a message naming
the missing variable.

### Not verified

Neither file has ever run in GitHub Actions. No deploy, no `firebase` command
and no Google API call of any kind were made by the run that staged them: what
is verified is their YAML shape, through the Go test named above, and that
`actionlint` (if present) parses them.

Two things the run that staged them *did* check, short of running anything:
the two global CLI options both workflows rely on (`-P, --project` and
`-j, --json`) exist in the pinned firebase-tools v15.19.0, and the shapes read
back out of that version's own source — `hosting:channel:deploy --json` prints
`{"status": "success", "result": …}` with `result` keyed by the site id and
carrying `url` (the summary step's `to_entries[0].value.url`), and
`hosting:clone` refuses a source channel the project has not got, and one with
no version on it, with its own message. What remains unexercised is everything
that needs a live project: that firebase-tools authenticates from the
credential file google-github-actions/auth writes (a WIF `external_account`,
not a key) — and that `roles/firebasehosting.admin` alone is enough for a
deploy, which is the narrowest predefined Firebase Hosting role (the role list
has exactly two: `firebasehosting.admin` and `firebasehosting.viewer`); a
permission error naming another API points at the two add-on roles
`docs/website/preview-promote.md` lists. The copy-in is the owner's, and the
first real run is what settles those.

## `images.yml` — applied in `6b57ba9`; what follows is the record

**Not pending: this change landed.** It was copied into
`.github/workflows/images.yml` and the staged file removed in `6b57ba9`
("Update workflow rules", #385), so what this section describes is what the
repository runs today, and the Go tests named below read the real file on its
own. The section is kept as the record of *what* that change was and *why* —
its tense corrected only where the change has since landed, and left as written
where it describes the file's shape, which has not changed. A future change to
`images.yml` would replace it with a new one, the way it replaced the sections
before it.

It replaced `.github/workflows/images.yml` with the same file plus Task 1 of
`docs/plans/2026-10-13-retire-image-mirror.md`: `publish-base` also began to
publish a nightly-immune `<version>-pinned` tag and to **sign the pushed
base image by its OCI digest with cosign (keyless)**. The committed file
already carried the real `fugaro-base`/`publish-base` jobs (commit
`356f7b1`) and the 0.7.0 cut that deleted the legacy per-kind `base` job
(commit `838e0b0`), so that diff was small and touched `publish-base` alone.
Everything else in the file — the triggers, `version`, `history`,
`fugaro-base`, `publish`, `verify-public`, `docker-tests` — was carried over
verbatim, comments included. Diff summary against the committed file:

- **`publish-base`'s `permissions` gain `id-token: write`,** alongside the
  existing `contents: read` and `packages: write`. It is needed for exactly
  one thing: the keyless signing step's OIDC request to Fulcio (the
  certificate GitHub's own OIDC issuer vouches for). The `-pinned` tag push
  itself is an ordinary registry write and needs only the `packages: write`
  that was already there. `id-token: write` is on **no other job** in this
  workflow.
- **The existing "Push per architecture and publish the index" step gains an
  `id: index` and a `--metadata-file index.json`** on its `docker buildx
  imagetools create` call (the one that already makes `:$VERSION` and, when
  it moves, `:$MAJOR`). The digest of the index that call just published is
  read out of that file (`containerimage.descriptor.digest` — the key
  docker's docs document for `imagetools create --metadata-file`; a missing
  or malformed value fails the job rather than signing something unknown),
  the step fails if the published index's children are not exactly the two
  child digests this job pushed, and the digest is handed to the steps below
  through `$GITHUB_OUTPUT`. **Nothing in the job learns a digest by asking a
  tag what it resolves to.** A tag is a second, movable registry answer: an
  actor who can write this repository's tags could move it between a create
  and a re-read, and the release's own identity would then sign *that*
  actor's image.
- **Four new steps after it**, all gated `if: github.event_name == 'push'`
  (so the nightly and `workflow_dispatch` runs — which resolve the *same*
  `$VERSION`, "the latest strict release tag" — can neither sign nor touch
  the pinned tag), each with its own shell, so they share no shell variables
  with the long script above:
  - `sigstore/cosign-installer@6f9f17788090df1f26f669e9d70d6ae9567deba6 # v4.1.2`
    — the exact pin `release.yml:95` already uses, not a placeholder;
  - `Sign the published index` (`id: sign`): `cosign sign --yes
    "$repo@$DIGEST"` for the digest the index step reported. It signs the
    **digest**, not a tag: a later move of any tag cannot change what the
    signature covers, and no tag is read to find it.
  - `Verify the signature just attached` (`id: verify`): `cosign verify`
    against that same digest, with `--certificate-identity
    https://github.com/dimipaun/fugaro/.github/workflows/images.yml@refs/tags/v$VERSION`,
    `--certificate-oidc-issuer
    https://token.actions.githubusercontent.com`, plus
    `--certificate-github-workflow-trigger push` and
    `--certificate-github-workflow-repository dimipaun/fugaro`. A signature
    made by any other identity or workflow fails the job here. **The last two
    are not decoration:** the identity alone does *not* prove a tag push — a
    `workflow_dispatch` run started against a tag ref is issued the same
    `...images.yml@refs/tags/vX.Y.Z` subject — so the trigger claim
    (Fulcio extension `1.3.6.1.4.1.57264.1.2`, `event_name`) and the
    repository claim (`.1.5`) are what make the other two mean "a release,
    in this repository", and all four are required. No mode that skips the
    transparency log or the artifact binding is used.
  - `Publish the -pinned tag at the signed digest` (`id: pinned`): **only
    now**, with the signature made and verified, does anything name
    `$VERSION-pinned`: `docker buildx imagetools create --tag
    "$repo:$VERSION-pinned" "$repo@$DIGEST"` — one source that is an index
    is copied as-is (docker's docs), so the tag names the very digest that
    was signed — followed by a read-back that fails unless
    `$VERSION-pinned` resolves to that same digest. So `-pinned` never
    exists unsigned, and `fugaro-base:<v>-pinned` names the release-day
    signed bytes until the next release's own tag push; a nightly rebuild of
    the plain `:<v>` tag can neither move it nor invalidate its signature.

**Every existing guarantee is kept, and the trigger surface is unchanged.**
The `on:` block is byte-identical: a strict `vX.Y.Z` tag push, the
`pull_request` path filter, the nightly cron and `workflow_dispatch`. The
base still builds natively for amd64 and arm64 (`fugaro-base`'s matrix) and
is still verified as a multi-platform index (`verify-public` still calls
`images/verify-public.sh --arm64`). `verify-public` is untouched: still
`always()`, still gated on `publish` having succeeded and on `publish-base`
having succeeded when the kinds include base, still with no `packages`
permission and no login, so it keeps running even when `publish-base` is
skipped (a backport tree with no `images/base`). `publish` and
`publish-base` remain the only two jobs holding `packages: write`.
`images/base_workflow_test.go`'s `TestPublishBaseSignsByOCIDigestOnPushOnly`
pins all of the above, from the parsed YAML (the fields GitHub executes, not
strings in comments) — the digest source (`--metadata-file`), the two
children assertion, signing by the digest handed to the step, the identity
the verification demands, the `-pinned` tag written last and at that digest,
the step order, the cosign-installer pin, the push-only gates, and
`id-token: write` on `publish-base` alone. Its companion
`TestSigningProblemsFindEveryMutation` feeds that checker forty-three
weakenings — a tag instead of the digest, `|| true` and `|| :`,
`continue-on-error: true` on a step or on the whole job, publish-base's own
`if:` widened to `always()`, the sign step before the push or after the
verification, `-pinned` unconditional with the gate left in a comment,
`-pinned` added back to the index step's create call, the `--metadata-file`
flag or the `containerimage.descriptor` read surviving only in a comment, a
verify step without this workflow's identity, GitHub's issuer, the `push`
trigger or the repository claim, workflow-level `id-token: write`, a widened
condition, a floating installer pin, each chain step dropped — and requires
every one to be reported **by the check that names it**, not merely to
produce some problem, so no check can rot unnoticed — and
`TestOnlyPublishJobsHoldPackagesWrite`,
`TestVerifyPublicRunsEvenWhenPublishBaseIsSkipped`,
`TestImagesWorkflowPublishesAMultiArchBase`,
`TestImagesWorkflowBuildsTheBaseAndHistory` and
`images/verify_public_test.go`'s
`TestImagesWorkflowPublishesAndVerifiesEveryImage` kept passing unmodified
against this file (`readImagesWorkflow` read the pending copy first; it reads
the real `.github/workflows/images.yml` now that the copy has landed).

### Why it was staged here, and the timing it had to meet

Fugaro cannot push a change under `.github/workflows/` from an agent run
(GitHub rejects it), so this diff was staged here for a human to copy in by
hand. **The copy had to land on `main` before that release's version-bump
PR's merge commit was created and tagged**, and this is the whole of the
authority for that requirement (design `docs/design/retire-image-mirror.md`
§5, §8; plan Task 1's rollout precondition): GitHub Actions runs a tag
push's workflow *exactly as that file exists in the tagged commit*, and
`scripts/release.sh` always tags whatever commit is currently on `main`.
Once the copy is in, that release's own tag push runs the updated
`images.yml` and signs its own `<that-version>-pinned` image as part of the
very same push — there is no earlier, separate tag push to wait for, and a
signed image from some *other* release could never help, because the
copy-time resolve (`basepin.Resolve`) only ever reads the **installed CLI's
own version's** tag. The release process cannot enforce this by itself. The
same constraint applies to any future change to `images.yml`, staged here
again: it must be copied in before that release's version-bump merge commit
is tagged.

The steps the copy used, exactly (run in `6b57ba9`):

```sh
# 1. Review this file, then copy it in BEFORE the version-bump merge commit
#    is created (i.e. before the tagged commit exists).
cp docs/pending-workflows/images.yml .github/workflows/images.yml
git add .github/workflows/images.yml
# 2. The git rm is load-bearing, not cleanup: while this file exists,
#    images/base_workflow_test.go's readWorkflow reads IT, so leaving it in
#    place would freeze these tests on this content and hide a later edit
#    or deletion of the real file's publish-base job.
git rm docs/pending-workflows/images.yml
git commit -m "images workflow: sign the base image by OCI digest"
```

What the first tag push after the copy signs: `fugaro-base:<v>-pinned` at
the index digest the push itself just created, with a Fulcio certificate
whose identity is `https://github.com/<owner>/fugaro/.github/workflows/images.yml@refs/tags/v<v>`
issued by `https://token.actions.githubusercontent.com` — the identity a
copy-time verification (`basepin.OCIVerifier`) checks against. The plain
`:<v>` and `:<major>` tags are pushed exactly as they are today.

**A real, if ordinarily brief, window exists regardless of the copy's
timing:** `release.yml` (the CLI) and `images.yml` (`publish-base`) are
independent workflows on the same tag push with no guaranteed ordering, so
for the minutes between the CLI being published and `publish-base` finishing
its sign, a copy with the newly-published CLI has nothing to resolve or
verify. That is expected; it closes when `publish-base` finishes. If the
sign step fails outright, the job fails (`set -eu`, no `|| true`), and a
maintainer can re-run the failed job: **a re-run reuses the build artifacts
that push uploaded** (`fugaro-base-<arch>-image`, kept for one day —
`retention-days: 1`), and re-runs it through create → sign → verify →
`-pinned` from there. It does **not** rebuild the image from the tag's
source; a re-run after the artifacts have expired has nothing to sign, so
the tag cannot be signed after that. GitHub also only offers re-runs for a
limited window (about 30 days for a workflow run); past that, a release's
`-pinned` image stays unsigned and the copy-time resolve fails loudly rather
than accepting anything else. A re-run re-creates `-pinned` again —
**normally at the same digest**, since it consumes the same uploaded
artifacts, so nothing a reader resolved changes. It is not guaranteed to be
the same one (an artifact could be re-uploaded, or the job re-run from a
different commit's tree), and that is **safe either way**: the re-run signs
whatever digest it creates, verifies it, and only then points `-pinned` at
it, so the tag and the signature always move together and a copy either reads
the release-day bytes or a re-signed equivalent — never an unsigned digest.
The nightly never touches it: that is the property the tag exists for, and
what Check 39e verifies.

### Not verified *at the time it was staged*

This is the staged run's own note, kept as part of the record. The file has
since been copied in, so the signing chain it describes is CI's to show on a
tag push from now on — but **none of it was exercised by the run that wrote
it**: in that run there was no Docker daemon, no network access and no live
action of any kind
available or permitted. Nothing about a real `publish-base` run
(the `-pinned` tag actually appearing in ghcr.io, the signature being
attached, an anonymous `docker pull` of `-pinned`, or the nightly genuinely
leaving it alone) was exercised. What that run verified is the YAML shape
through the Go tests named above, read from the pending copy then; the real
workflow run is CI's, and that copy has since landed.

Two honest limits of this diff, both deliberate:

- **Nothing here is checked against a real signature.** The verification side
  (`internal/basepin`, plan Task 2) does not exist yet; this file only
  produces the signed `<v>-pinned` image it will read.
- **`verify-public` does not yet check the `-pinned` tag's anonymous
  readability.** The plan marks that as a cheap, recommended addendum, not
  part of Task 1; it is not done here, so this diff changes nothing in
  `images/verify-public.sh` or in `verify-public`'s own matrix-free base
  check. A package is private until the owner makes it public once
  (`docs/release.md`), so if `-pinned` were ever private, the copy-time
  resolve would fail loudly rather than silently — but not at release time.

## `ci.yml`

Copied in on 2026-10-17 (#504): `.github/workflows/ci.yml`'s `terraform` job now loops over `installation firebase repo compute`. The staged copy is removed, so `images/cloudbuild_test.go` reads the real file on its own: `TestCIWorkflowTestsEveryTerraformRoot` requires it to name every root under `deploy/terraform/gcp/roots/`, and `TestStagedCIWorkflowIsTheRealOnePlusTheComputeRoot` skips while the staged copy is absent. A future change to `ci.yml` that a run needs is staged here again, as the real file plus the change.

## Before you copy: check the action pins

Dependabot does not watch this directory, and `next` can lag `main`'s Dependabot bumps of the workflow files, so a staged file's pins can differ from the ones `main` has moved to. `TestActionPinsAreFullSHAs` requires every `uses:` **here** to be a full commit SHA; once a staged file is copied in and gone from here, the same test also requires its pins to agree with the other workflows' — `images.yml` is that test's subject, so that comparison is live for it. `TestSharedTestsWorkflowIsStagedAndPinned` requires the same of the pending `shared-tests.yml` (full SHAs, each with its `# vX.Y.Z` beside it). Before copying, compare `grep -h 'uses:' .github/workflows/*.yml docs/pending-workflows/*.yml | sort -u` and keep the NEWER SHA for each action (verify it against the upstream tag with `gh api repos/<owner>/<repo>/git/ref/tags/<tag>`), so the copy never downgrades a pin.

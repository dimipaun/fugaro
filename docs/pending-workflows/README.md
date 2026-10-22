# Pending workflow changes

**Two files are pending: `shared-tests.yml`** (the shared package's test job, mobile plan Task P4.3; see `## shared-tests.yml` below) **and `images.yml`** (the credentials service's image leg, credentials-service plan Task C1.14; see `## images.yml` below). **Two other files stay staged here by design: `console-tests.yml` and `console-release.yml`.** Both were copied into `.github/workflows/` by the owner, and unlike every other file here their staged copies are kept, because their tests assert the copy under `.github/workflows/` is byte-identical to the staged one (see their sections below). Nothing else is pending: the website's two manual deploy workflows, `website-preview.yml` and `website-promote.yml` (#566), were copied into `.github/workflows/` by the owner (their staged copies are removed); `ci.yml` was copied in on 2026-10-17 (#504, staged copy removed in #508); and `images.yml` was copied in and its staged copy removed in `6b57ba9`, then staged again by C1.14 for the change its section below describes. A future workflow change that a run needs is staged here as the real file plus the change, and the owner copies it in.

- **`shared-tests.yml`** — the whole CI job for `shared/`'s tests, mobile plan Task **P4.3**: a `main`-filtered push and pull request under `shared/**`, `contents: read`, `npm ci` and `npm test` on Node 22 and Node 26, and every `shared/*/mutation-proof.sh` in the package, every action pinned by full commit SHA. `shared/` is outside the Go module, so no job in `ci.yml` runs it. Staged by a Fugaro run that cannot push a `.github/workflows/**` change; see `## shared-tests.yml` below, which also records what the job will report on `main` today.
- **`website-preview.yml`** and **`website-promote.yml`** — the website's two new workflows, both `workflow_dispatch`-only: one builds a chosen ref and deploys it to a Firebase Hosting preview channel of the site's own project and prints the preview URL, the other promotes an existing preview channel to live and is the owner's alone (`environment: website-live` plus a guard on `github.actor`). Staged by a Fugaro run that cannot push a `.github/workflows/**` change. **Read `docs/website/preview-promote.md` first**: it carries the owner-only one-time setup (the Workload Identity Federation pool and provider, the deployer service account and its Firebase Hosting roles, the `website-live` environment and the three repository variables) that both files need before they can run at all.
- **`images.yml` — staged again, and pending.** The staged copy is the running `.github/workflows/images.yml` plus the credentials service's image leg (credentials-service plan, task **C1.14**): the `credentials` job that builds `images/credentials/Dockerfile`, smoke-tests it, holds it to a size budget and secret-scans it on every run, the `credentials` kind in the `version` job's list, the `publish-credentials` job that publishes it and gives it the same signed `<version>-pinned` tag the base has (which `fugaro init`'s credentials stage resolves and verifies), and the `credentials` image in `verify-public`'s pull-back loop. Staged by a Fugaro run that cannot push a `.github/workflows/**` change; see `## images.yml` below for the diff, the one command that applies it and the steps that have to go with it. **The two earlier `images.yml` changes are records, not requests:** the signing of the pushed base image by its OCI digest (cosign, keyless) and the nightly-immune `<version>-pinned` tag (plan Task 1 of `docs/plans/2026-10-13-retire-image-mirror.md`) was copied in and `git rm`'d from here in `6b57ba9`, and the 0.7.0 hard cut (`838e0b0`) before it.

**`console-tests.yml`** — the desktop console's own tests in CI, staged by Task 1.4 of `docs/plans/2026-10-15-desktop-console.md` and applied by the owner as Task 1.5. **It holds no secret of any kind**, it runs unconditionally on every pull request and every push to `main`, and unlike `images.yml` **its staged copy is kept after the copy-in** (`TestConsoleTestsWorkflowIsStagedAndPinned` asserts the two files are byte-identical from then on): see `## console-tests.yml` below.

**`console-release.yml`** — the desktop console's signing, notarisation and stapling pipeline, staged by Task 4.2 of the same plan and applied by the owner as Task 4.5, together with the protected GitHub environment its secrets live in. **It is a separate file from the tests workflow on purpose, and the split is a security boundary rather than tidiness**: this job reads the Apple signing certificate and the App Store Connect key, and a branch of this repository can open a pull request, so a job holding that material must never sit in a file that runs on every pull request. Its staged copy is kept after the copy-in too: see `## console-release.yml` below.

The two `images.yml` changes that *were* applied — the 0.7.0 hard cut (`838e0b0`, which is why the file has no legacy per-kind `base` job) and the signing change (`6b57ba9`) — are the file's history, and the third staging (C1.14's credentials leg, below) is what is pending today. Each time it is staged again, the `## images.yml` section's older text is replaced by the newer one.

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

**`images.yml`'s own copy-in steps, in order.** (1) Copy the file in with

```sh
cp docs/pending-workflows/images.yml .github/workflows/images.yml
git rm docs/pending-workflows/images.yml
git commit -m "..."
```

(2) The `git rm` is load-bearing here for the reason it is for the website
pair and `shared-tests.yml`: while a staged copy exists, `images/base_workflow_test.go`'s
`readWorkflow` reads *it*, so leaving it in place would freeze those checks on
its bytes and hide a later edit or deletion of the real file's jobs.
(3) Update the sentence at the end of `website/README.md` that says what
`docs/pending-workflows/` holds: it says "now holds **two** pending workflows"
and names `docs/pending-workflows/images.yml` and
`docs/pending-workflows/shared-tests.yml`; after this copy it holds one, and
`website/test/readme.mjs` checks the count, the names and the directory in both
directions (it reads the whole flattened page, so the name has to go from
every backticked mention on it, not only from that sentence). (4) Update **this
document**, which would otherwise go on describing a staged copy that is gone:
the opening line's count and this section's status line. (5) Update
**`docs/release.md`**, whose `## What a tag does` section enumerates the images
`images.yml` builds, smoke-tests, scans and publishes and which of them carries
the signed `<version>-pinned` tag: once this copy is in and the credentials
image ships, that sentence is stale in the way `.goreleaser.yaml`'s release
notes and `website/README.md` were already updated for, so it has to name
`fugaro-credentials` too — built and scanned on every run, published with the
others, and **signed and pinned the way `fugaro-base` is** (the `-pinned` tag
`fugaro init`'s credentials stage resolves), with `verify-public` pulling it
back. Nothing holds that sentence to the workflow — unlike `website/README.md`'s
count, which `website/test/readme.mjs` checks in both directions — so this step
is for the reader rather than for a red test. (6) Compare the action
pins with the other workflows' first — Dependabot does not watch
`docs/pending-workflows/`, so nothing else will (see `## Before you copy: check
the action pins` at the end).

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
has been paid twice and is being paid a third time: its staged copy is back
(C1.14), so the static Go
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
read the pending file **while it exists** — which it does again now, so what
they judge today is the staged copy — and fall back to the real
`.github/workflows/images.yml` once it is gone, as they did between `6b57ba9`
and this staging. If the pending file were
left in place after the copy, those tests would keep reading its frozen
content forever and would never notice a later edit — including an
accidental deletion — to the real file's `fugaro-base`/`publish-base`/
`credentials` jobs. Once the pending file is removed, the real file is read on
its own and *must* contain the base jobs, so deleting them (by hand, by a bad
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

**Steps 3 and 4 are not decoration, and they are the two steps someone copying `images.yml` before did not have to take for `images.yml` itself** (`images.yml`'s own bullet in this document was edited when it was applied; that is step 4, and it is the step nobody wrote down until now). `website/test/readme.mjs`'s **last** test — the file's closing `test(...)`, "the README claims about docs/pending-workflows/, firebase.json and the deploy script are literally true" — is what compares the page with `docs/pending-workflows/`, and it does so over the **whole** document rather than over that one sentence: it collapses every run of whitespace in `website/README.md` to a single space and then collects **every backticked `docs/pending-workflows/<name>.yml` the document prints anywhere**, which must be exactly the `*.yml` files the directory holds apart from the console's two kept-by-design copies (in both directions — a staged file nobody named, and a name with nothing staged, both fail), while its count word (`no`/`one`/`two`/`three`) must be that number; the console's two are excluded from that set and held instead to being byte-identical, each to the file of the same name under `.github/workflows/`. **So every backticked `docs/pending-workflows/<name>.yml` anywhere on the page is a claim that the file is pending, and the sentences that carry the claim are not the only place it can appear**: writing the path for an applied workflow in some other paragraph — the console's two, say, which the page names bare today — fails the check even when this sentence is right, so a cross-reference to a copied-in file has to be written without it. Copying this file in and `git rm`-ing it without step 3 leaves the sentence naming a file that is no longer there, and the site's own `npm run check` goes red on `main`. The test reads the count as a word and compares it as a number, so the sentence is the only thing to update — but it is the sentence, name and count together, that has to match.

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

## `images.yml`

**Status: `images.yml` is staged and pending.** `.github/workflows/images.yml`
holds the two earlier changes (the 0.7.0 hard cut `838e0b0` and the signing
change `6b57ba9`, whose own record follows this section) and does **not** hold
this one: the staged file here is that file plus the credentials service's
image leg, and the `## `images.yml` — applied in `6b57ba9`` section below is the
record of the change before this one, not of this one. The copy-in is the
owner's — a Fugaro run may not write `.github/workflows/**` (GitHub rejects the
push) — and the one command is in **`images.yml`'s own copy-in steps** above.

**What the change is.** The credentials service's image (credentials-service
plan, task **C1.14**, `images/credentials/Dockerfile`) is built and checked by
CI the way the history job's image is, published with the others on a release
tag, and — unlike history — **signed and pinned the way the base is**, because
something verifies it: `fugaro init`'s credentials stage resolves
`fugaro-credentials:<version>-pinned`, verifies the cosign signature and only
then writes the digest it names into the service's Terraform variable
(`internal/cli/init_credentials.go`, task C1.20; `internal/infra/tf` refuses a
tag at plan time), and its own rig publishes exactly that `-pinned` tag.
Nothing outside the hunks below changes: the triggers, the `version` job's
version-picking, the `fugaro-base` and `docker-tests` jobs, every *step* of
`publish` and `publish-base`, and `publish`'s own `needs: [version, history]`
and `matrix.base: [history]` lines are all carried over byte for byte. Two
*comments* are not, and each is a hunk of its own below: a comment naming the
jobs that hold `packages: write` and `id-token: write` is part of the trust
boundary it describes, so both copies of it would otherwise be wrong. The diff
against the committed file is six things:

- **`version`'s "Which images this tree builds" list gains `credentials`:**
  `for k in history base credentials;`, plus six added comment lines above the
  loop (a bare `#` and five lines of prose) recording why a tag whose tree
  predates the image skips that leg. The list is
  the one place an image is registered, and every step of every build leg is
  gated on it, so a tag whose tree predates `images/credentials/Dockerfile`
  (any release before this one) skips that leg instead of failing every nightly.
- **A `credentials` job**, on the `history` job's shape: checkout, the resolved
  tag on a nightly or dispatch run, `images/build-base.sh credentials
  fugaro-credentials:ci`, `images/smoke-credentials.sh`, `images/size-budget.sh`,
  `images/scan.sh --secrets` and (off pull requests) `images/scan.sh`, then the
  image saved and uploaded as the `fugaro-credentials-image` artifact. It is
  the `fugaro-base` job's shape for the scans rather than `history`'s: **the
  secret scan runs on every run**, not only off pull requests — this image is
  the one process that holds model credentials, so a secret that leaked into a
  layer has to fail the pull request that added it, not the release that ships
  it — and the vulnerability scan is off pull requests, exactly as it is for
  the base. There is no multi-arch leg:
  the service's revision is linux/amd64, which is what `build-base.sh` builds
  by default. Nothing in the job passes the service's three variables
  (`FUGARO_PROJECT`, `FUGARO_GCP_PROJECT`, `FUGARO_REGION`): the Cloud Run
  service gets them from the installation module (`credentials.tf`) and the
  image's `credentials serve` refuses to start without them.
  `images/smoke-credentials.sh` asserts exactly that in the built image, and
  that the image carries no credential-shaped environment of its own.
- **`publish-base`'s header comment names three jobs, not two:** its closing
  two lines said "this job and publish are the only ones with packages: write;
  id-token: write is here alone"; they now say that `publish-base`, `publish`
  and `publish-credentials` are the only ones with `packages: write` and that
  `id-token: write` is on `publish-base` and `publish-credentials` alone. The
  sentence is the written form of what `TestOnlyPublishJobsHoldPackagesWrite`
  and `signingProblems`' id-token check assert, so leaving it stale would have
  made the file disagree with its own guard.
- **A `publish-credentials` job**, `publish-base`'s chain for a
  single-architecture image: load the artifact, push `<version>` (and
  `<major>` when that tag moves), take the digest **from the push's own
  answer** rather than from a tag, sign exactly that digest with this
  workflow's keyless identity on a tag push, verify the signature against
  this workflow on a `push` event and this repository, and only then write
  `<version>-pinned` at the signed digest — by re-pushing the manifest the job
  already signed, then reading the tag back and failing if it resolves
  anywhere else. It is a separate job rather than a leg of `publish` because
  signing needs `id-token: write`, and that belongs on the jobs that sign and
  on no other: `publish` keeps only `packages: write`.
  **Why this image is signed when history's is not:** the `-pinned` tag is
  what the credentials stage resolves and the digest it names is what the
  service's Terraform variable is set to, so a tag-only publish would leave
  that stage with nothing it may pin. The service runs as the account that
  reads every model credential in the bundle, while every operator of the
  project holds `roles/artifactregistry.writer` on the registry the image
  comes from, so a mutable tag would let an operator choose the code that
  account runs. The chain itself is `publish-base`'s, from the review that
  produced it (`docs/plans/2026-10-13-retire-image-mirror.md`, Task 1, PR
  #377); what is new is that a second job carries it.
- **`publish`'s header comment says why it is not the one publishing this
  image:** it said "this job and publish-base are the only ones that need
  packages: write, and neither ever runs on pull_request"; it now names
  `publish-credentials` too and says the base and the credentials service
  publish through the two jobs above *because those two are signed*. Its
  `needs:` and `matrix.base:` lines are **not** part of the change: they
  already read `[version, history]` and `[history]` in the committed file,
  because the 0.7.0 cut (`838e0b0`) had already removed the base from this
  job's matrix — the credentials image is a job that never joins it rather
  than an image leaving it.
- **`verify-public` pull-checks it:** `for img in history credentials; do`, so
  the published image is pulled back with no login, and the job now waits on
  `publish-credentials`, gating on it exactly as it gates on `publish-base` —
  a skipped `publish-credentials` is fine only when the tree holds no
  credentials image, so a failed or skipped one cannot leave the image
  unverified. A brand-new ghcr.io package is private until its owner makes it
  public once by hand (`docs/release.md`), and this job is what fails the
  release instead of shipping an image nobody can pull. **The owner's one-time
  step for a new package applies here:** after the first release that
  publishes it, `fugaro-credentials` has to be made public by hand, or this
  job fails the next release and every one after it.

**What the copy-in has to leave true.** After the copy and the `git rm`, the
real `.github/workflows/images.yml` is what `images/base_workflow_test.go`'s
`readWorkflow` reads, and it must hold the base jobs *and* the credentials
leg: `TestImagesWorkflowBuildsTheBaseAndHistory` (the `credentials` and
`publish-credentials` jobs exist),
`TestImagesWorkflowPublishesAndVerifiesEveryImage` (publish's matrix is
`[history]`, `publish-credentials` waits for the credentials build and is
gated on the publish/kinds conditions, both images are built and
smoke-tested, the size budget and the secret scan are run, `verify-public`
names both and waits on both publish jobs),
`TestOnlyPublishJobsHoldPackagesWrite` (the `credentials` job holds no
`packages: write`, and `publish-credentials` does),
`TestPublishBaseSignsByOCIDigestOnPushOnly` and
`TestCredentialsPublishSignsByDigestOnPushOnly` (`id-token: write` is on
those two jobs and no others, and both chains sign by digest on a tag push
and write their `-pinned` tag last), and their mutation tests are the checks
that fail if a copy dropped the change. They are the same checks that read
the staged file today, so a copy that is faithful changes nothing about them.

**One thing this staging does not do, and the owner should know before
releasing.** The workflow publishes the image; it does not make the service
run it. Turning the service on is `fugaro init`'s opt-in
(`credentials_service`, installation plan task C1.20), and the digest it pins
is read back through the ghcr proxy from the published image — so the first
release that carries this leg is the release a service can be pointed at. The
Terraform side of that (C1.18) is a separate, also owner-run, change.

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

# Pending workflow changes

**Two files are pending: `console-tests.yml` and `console-release.yml`.** (`ci.yml` was copied in on 2026-10-17, #504, and its staged copy removed in #508; `images.yml` was copied in earlier and is no longer staged. The `## images.yml` section below is kept as the record of that change.) A future workflow change that a run needs is staged here as the real file plus the change, and the owner copies it in.

**`console-tests.yml`** — the desktop console's own tests in CI, staged by Task 1.4 of `docs/plans/2026-10-15-desktop-console.md` and applied by the owner as Task 1.5. **It holds no secret of any kind**, it runs unconditionally on every pull request and every push to `main`, and unlike `images.yml` **its staged copy is kept after the copy-in** (`TestConsoleTestsWorkflowIsStagedAndPinned` asserts the two files are byte-identical from then on): see `## console-tests.yml` below.

**`console-release.yml`** — the desktop console's signing, notarisation and stapling pipeline, staged by Task 4.2 of the same plan and applied by the owner as Task 4.5, together with the protected GitHub environment its secrets live in. **It is a separate file from the tests workflow on purpose, and the split is a security boundary rather than tidiness**: this job reads the Apple signing certificate and the App Store Connect key, and a branch of this repository can open a pull request, so a job holding that material must never sit in a file that runs on every pull request. Its staged copy is kept after the copy-in too: see `## console-release.yml` below.

The most recent copy that *was* applied (`images.yml` for the 0.7.0 hard cut, commit `838e0b0`) is the reason the file below has no legacy per-kind `base` job; that section's older text was replaced by this one when the file became pending again.

Fugaro refuses to push a commit that touches `.github/workflows/**`
(GitHub rejects the push). When a Fugaro run needs to change a workflow
file, it writes the file's full intended new content here instead, under
the same name, for a human to copy in by hand after reviewing it.

**Copying a file in:**

```sh
cp docs/pending-workflows/images.yml .github/workflows/images.yml
git add .github/workflows/images.yml
git rm docs/pending-workflows/images.yml
git commit -m "..."
```

**The `git rm` is for `images.yml`, and it is not the rule for every file here.**
`console-tests.yml` below is copied in **without** removing the staged copy,
because its test asserts the two files are byte-identical while both exist — see
that section for the command and the reason.

The `git rm` is not cleanup, it's load-bearing: the static Go tests that pin
these workflows' shape (`images/base_workflow_test.go`'s `readWorkflow`,
used by `TestImagesWorkflowPublishesAMultiArchBase`,
`TestOnlyPublishJobsHoldPackagesWrite`,
`TestVerifyPublicRunsEvenWhenPublishBaseIsSkipped`,
`TestImagesWorkflowBuildsTheBaseAndHistory`,
`TestPublishBaseSignsByOCIDigestOnPushOnly` and its companion
`TestSigningProblemsFindEveryMutation` — the two that pin the base image's
signing chain — plus
`images/verify_public_test.go`'s `TestImagesWorkflowPublishesAndVerifiesEveryImage`)
read the pending file
**while it exists** and only fall back to the real
`.github/workflows/images.yml` once it is gone. If the pending file were
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
than taste. **Task 4.6 is what adds `console` to `scripts/release-gate.sh`'s
`names` list** (that line reads `names="test terraform rules"` today, so this
check is not yet required), and from that edit on the gate requires a check named
`console` on a tag's commit and, in its `identical_tree_checks` fallback, the
same names on the pull request head whose tree matches. **A check a filter
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
as Task 4.5. One job keyed exactly `release`, on `macos-latest`, triggered by a
**`push` of a strict `vX.Y.Z` tag and nothing else** — never a pull request,
never a `pull_request_target`, never a `workflow_run`, never a
`workflow_dispatch`. It runs `scripts/release-gate.sh` first, exactly as
`release.yml` and `images.yml` do, then builds with Task 4.1's own scripts
(`npm ci --ignore-scripts --no-audit --no-fund` and `npm run package`, which
carry the hardened runtime and `console/entitlements.mac.plist`), notarises and
staples the `.app` through that packaging step — electron-builder does it as it
packs, **before** it builds the `.dmg` and the `.zip`, so both artifacts are
made from the stapled bundle — then notarises the disk image with
`xcrun notarytool submit --wait` and staples it, verifies the bundle *inside*
the mounted `.dmg` and the extracted `.zip` with `codesign --verify --deep
--strict`, `spctl --assess --type execute` and `xcrun stapler validate`, and
uploads the `.dmg`, the `.zip` and the recorded verification output to the
release `release.yml` creates for the same tag.

**The order is the part a reader should check first, after the trigger.** The
packaging step is the one holding the App Store Connect triple, exactly as the
owner's documented local flow does, and it is the only order in which the
artifacts contain a stapled bundle: packing first and notarising afterwards
ships a `.dmg` and a `.zip` whose app Gatekeeper has to ask the notary service
about, online, at every launch. The workflow validates the packed bundle's
ticket before it publishes anything, so a packer that did not staple stops the
job instead.

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
| `APPLE_TEAM_ID` | the team identifier (`ZM8Y2LC7JL`); the workflow checks that the signature it built names it |
| `APPLE_API_KEY` | the App Store Connect API key's `.p8` contents |
| `APPLE_API_KEY_ID` | the key's id |
| `APPLE_API_ISSUER` | the issuer uuid |

**The notarisation credential is the App Store Connect API key, not an
app-specific password.** D4 settles that signing is required and that the
pipeline is tag-only behind the gate and a protected environment, but it does not
choose between the API key and an app-specific password; the decision recorded
here is the API key, so `APPLE_ID` and `APPLE_APP_PASSWORD` are deliberately
absent, and a row naming either of them is a defect.

**It fails closed.** The first step that reads a secret refuses to continue
unless all six are non-empty, naming the missing one, so an absent secret stops
the job rather than silently skipping the signing step — an unsigned console
published under a signed release's name is the worst outcome this file can
produce. No value of any secret appears in the file: each is referenced as
`${{ secrets.NAME }}` in the `env:` of the single step that needs it — bound to
a variable of the same name, which is what `TestConsoleReleaseWorkflowIsStagedAndPinned`
compares — and the certificate is imported into a keychain the job creates (a
random password from `openssl rand`, `security create-keychain`,
`security import`, `security set-key-partition-list`) and deletes in an
`if: always()` step, along with the notarisation key file, which is written with
mode `0600` and handed on as a *path* (`APPLE_API_KEY` is a file to
electron-builder, `--key` is a file to notarytool), never as a value.

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
`TestConsoleReleaseWorkflowIsStagedAndPinned` reads the staged file and asserts
the one under `.github/workflows/` is byte-identical to it, so a hand-edited or
hand-written workflow there is a red test rather than something nobody reads.

### Not verified

This file has never run, and no signing step under it has ever run either: no
certificate, no App Store Connect key, no notary submission and no Mac were
available or permitted in the run that wrote it, and no agent run in this
repository builds, signs, notarises or verifies a bundle. What *is* verified is
its text,
through `TestConsoleReleaseWorkflowIsStagedAndPinned` and
`TestConsoleReleaseWorkflowHoldsNoPullRequestTrigger` in
`internal/cli/docs_desktop_console_test.go`: the trigger surface, the environment
name, the secret names and the variables they are bound to, the fail-closed step,
the temporary keychain and its deletion, the App Store Connect triple being in
the packaging step's environment and the key file it names, the step that
validates the packed bundle's staple, the notarytool submission of the disk
image, the verification being about the bundle inside the `.dmg` and the `.zip`,
the two action pins, the permissions, and byte-identity with the copy under
`.github/workflows/` once it exists.

Two things a reader must not take from those tests: the pinned SHAs are read
against upstream by the person copying the file in (see "Before you copy" below —
Dependabot does not watch this directory), and the first real run is Check 59a's
and 59b's (plan Task 4.3), which are owner-run on a Mac.

## `images.yml`

Replaces `.github/workflows/images.yml` with the same file plus Task 1 of
`docs/plans/2026-10-13-retire-image-mirror.md`: `publish-base` now also
publishes a nightly-immune `<version>-pinned` tag and **signs the pushed
base image by its OCI digest with cosign (keyless)**. The committed file
already carries the real `fugaro-base`/`publish-base` jobs (commit
`356f7b1`) and the 0.7.0 cut that deleted the legacy per-kind `base` job
(commit `838e0b0`), so this diff is small and touches `publish-base` alone.
Everything else in the file — the triggers, `version`, `history`,
`fugaro-base`, `publish`, `verify-public`, `docker-tests` — is carried over
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
`TestImagesWorkflowPublishesAndVerifiesEveryImage` keep passing unmodified
against this file (`readImagesWorkflow` reads this pending copy first).

### Why this is pending here, and the timing that matters

Fugaro cannot push a change under `.github/workflows/` from an agent run
(GitHub rejects it), so this diff is staged here for a human to copy in by
hand. **The copy must land on `main` before that release's version-bump
PR's merge commit is created and tagged**, and this is the whole of the
authority for that requirement (design `docs/design/retire-image-mirror.md`
§5, §8; plan Task 1's rollout precondition): GitHub Actions runs a tag
push's workflow *exactly as that file exists in the tagged commit*, and
`scripts/release.sh` always tags whatever commit is currently on `main`.
Once the copy is in, that release's own tag push runs the updated
`images.yml` and signs its own `<that-version>-pinned` image as part of the
very same push — there is no earlier, separate tag push to wait for, and a
signed image from some *other* release could never help, because the
copy-time resolve (`basepin.Resolve`) only ever reads the **installed CLI's
own version's** tag. The release process cannot enforce this by itself.

The owner steps, exactly:

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

### Not verified

This file has never run in GitHub Actions, and neither has cosign under it:
no Docker daemon, no network access and no live action of any kind were
available or permitted in this run. Nothing about a real `publish-base` run
(the `-pinned` tag actually appearing in ghcr.io, the signature being
attached, an anonymous `docker pull` of `-pinned`, or the nightly genuinely
leaving it alone) was exercised. What is verified is the YAML shape through
the Go tests named above, read from this pending copy; the real workflow run
is CI's, once this file is copied into `.github/workflows/images.yml`
(removing it from here) and a tag push or a PR touching `images/**` runs it.

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

Dependabot does not watch this directory, and `next` can lag `main`'s Dependabot bumps of the workflow files, so the pins in `images.yml` here can differ from the ones `main` has moved to. `TestActionPinsAreFullSHAs` requires every `uses:` here to be a full commit SHA; once the pending file is gone it also requires the real file's pins to agree with the other workflows. Before copying, compare `grep -h 'uses:' .github/workflows/*.yml docs/pending-workflows/images.yml | sort -u` and keep the NEWER SHA for each action (verify it against the upstream tag with `gh api repos/<owner>/<repo>/git/ref/tags/<tag>`), so the copy never downgrades a pin.

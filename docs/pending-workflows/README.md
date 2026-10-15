# Pending workflow changes

**One file is pending: `images.yml`** — the signing of the pushed base image by its OCI digest (cosign, keyless) and the nightly-immune `<version>-pinned` tag, plan Task 1 of `docs/plans/2026-10-13-retire-image-mirror.md`. It was staged by a Fugaro run that cannot push a `.github/workflows/**` change. **Copy it in before the release's version-bump merge commit is created** (the timing is load-bearing: see `## images.yml` below), then `git rm` the pending file, as the procedure below describes.

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

The `git rm` is not cleanup, it's load-bearing: the static Go tests that pin
these workflows' shape (`images/base_workflow_test.go`'s `readWorkflow`,
used by `TestImagesWorkflowPublishesAMultiArchBase`,
`TestOnlyPublishJobsHoldPackagesWrite`,
`TestVerifyPublicRunsEvenWhenPublishBaseIsSkipped` and
`TestImagesWorkflowBuildsTheBaseAndHistory`, plus
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
- **The tag-creation block inside the existing "Push per architecture and
  publish the index" step adds `--tag $repo:$VERSION-pinned`** to the same
  `docker buildx imagetools create` call that already makes `:$VERSION` and
  (when it moves) `:$MAJOR`. That addition is guarded inside the script by
  `if [ "$GITHUB_EVENT_NAME" = push ]`, because this same script also runs
  on the nightly and on `workflow_dispatch` — and on those events the
  `version` job's own logic picks "the latest strict release tag", i.e. the
  identical `$VERSION` — so an unguarded tag would be created and **moved**
  by every nightly, which is precisely what a verified copy must not read.
  `fugaro-base:<v>-pinned` therefore names the release-day, signed bytes
  until the next release's own tag push, and a nightly rebuild of the plain
  `:<v>` tag can neither move it nor invalidate the signature on it.
- **Two new steps in `publish-base`, both `if: github.event_name == 'push'`,**
  added after the tag step (so the registry login from the earlier step is
  already in place, and each has its own shell — they share no shell
  variables with the long script above):
  - `sigstore/cosign-installer@6f9f17788090df1f26f669e9d70d6ae9567deba6 # v4.1.2`
    — the exact pin `release.yml:95` already uses, not a placeholder;
  - `Sign the base image`: resolves the digest the tag it just wrote now
    names (`docker buildx imagetools inspect "$repo:$VERSION-pinned"
    --format '{{.Manifest.Digest}}'`, since `imagetools create` does not
    print it) and runs `cosign sign --yes "$repo@$digest"`. It signs the
    **digest**, not the tag: the signature is attached as its own OCI
    artifact in the same ghcr.io repository, and a later move of any tag
    cannot change what it covers. The resolve is safe here and only here
    because it runs in the same job, immediately after that job's own
    create call, under the job's existing `images-publish-base` concurrency
    group (`cancel-in-progress: false`).

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
pins all of the above — the signing step, the `-pinned` tag, its in-script
event guard, the cosign-installer pin, and `id-token: write` on
`publish-base` alone — and `TestOnlyPublishJobsHoldPackagesWrite`,
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
maintainer's re-run rebuilds the image, re-creates and moves `-pinned`, and
re-signs it — a re-run of the **tag-push** workflow does move `-pinned`
again, which is harmless (the nightly never does; that is the property the
tag exists for, and what Check 39e verifies).

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

## Before you copy: check the action pins

Dependabot does not watch this directory, and `next` can lag `main`'s Dependabot bumps of the workflow files, so the pins in `images.yml` here can differ from the ones `main` has moved to. `TestActionPinsAreFullSHAs` requires every `uses:` here to be a full commit SHA; once the pending file is gone it also requires the real file's pins to agree with the other workflows. Before copying, compare `grep -h 'uses:' .github/workflows/*.yml docs/pending-workflows/images.yml | sort -u` and keep the NEWER SHA for each action (verify it against the upstream tag with `gh api repos/<owner>/<repo>/git/ref/tags/<tag>`), so the copy never downgrades a pin.

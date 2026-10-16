# Pending workflow changes

**Two files are pending: `images.yml` and `ci.yml`.**

- **`images.yml`** — the signing of the pushed base image by its OCI digest (cosign, keyless) and the nightly-immune `<version>-pinned` tag, plan Task 1 of `docs/plans/2026-10-13-retire-image-mirror.md`. It was staged by a Fugaro run that cannot push a `.github/workflows/**` change. **Copy it in before the release's version-bump merge commit is created** (the timing is load-bearing: see `## images.yml` below), then `git rm` the pending file, as the procedure below describes.
- **`ci.yml`** — the `terraform` job's root loop grows the `compute` root of the Batch backend (design `docs/design/compute-backend.md` §8.1 task 6). It is one word on one line; nothing schedules it and nothing is released by it, but until it is copied in, CI neither validates nor terraform-tests that root. See `## ci.yml` below.

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

(`ci.yml` is copied in the same way — `cp` it over `.github/workflows/ci.yml`,
`git rm` the pending copy, commit.)

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

Replaces `.github/workflows/ci.yml` with the same file plus **one word on one
line**: the `terraform` job's "validate and test the roots" step loops
`for r in installation firebase repo compute; do` instead of
`for r in installation firebase repo; do`. Everything else — the triggers, the
`changes`, `test`, `rules` and `terraform` jobs, every step, every comment,
every action pin — is carried over verbatim.

That loop is what `deploy/terraform/gcp/roots/compute` needs: Task 6 of
`docs/design/compute-backend.md` §8.1 added it (the Batch backend's
installation-level provisioning), and `images/cloudbuild_test.go`'s
`TestCIWorkflowTestsEveryTerraformRoot` holds every directory under
`deploy/terraform/gcp/roots/` to being named there, precisely so a new root
cannot be added and silently go untested. The step's own guard
(`[ -d ... ] || { echo "no Terraform root $r" >&2; exit 1; }`) fails the job
on a name that has no directory, so the two must move together — which is why
this is a workflow change at all rather than something the run could settle in
Go: naming the root in the workflow is exactly what a Fugaro run is not
allowed to push.

**Until this copy is in, CI does not validate or terraform-test the `compute`
root.** It is the only thing lost: the staged file is otherwise identical, so
every check the real `ci.yml` performs today still runs, and the root's own
files are still covered by `terraform fmt -check -recursive deploy/terraform`,
by `tflint --recursive`, by `sh deploy/terraform/scan.sh` and by
`deploy/terraform/terraform_test.go`'s Go-side pins (which run under the
`test` job's `go test -race ./...`). What waits for the copy is
`terraform validate` on the root and its ten mock-provider runs in
`roots/compute/tests/compute.tftest.hcl` — the reaper's six-permission role,
the conditioned launcher binding, the run accounts' roles, the NAT's network
and subnetwork, the two APIs and nothing else, the preload build's actAs, and
the two variable refusals.

**Verified in the run that staged this** (linux_amd64, terraform v1.16.4 —
the version the job pins — and provider `hashicorp/google` 8.4.0 from the
committed lock file): `terraform -chdir=deploy/terraform/gcp/roots/compute
init -backend=false -lockfile=readonly -input=false`, `validate` and `test`
against the mock provider, with `TF_DATA_DIR` outside the tree as the job's
step sets it — `Success! 10 passed, 0 failed` — plus `terraform fmt -check
-recursive deploy/terraform` and `tflint --chdir=deploy/terraform/gcp
--recursive` clean. **What is not verified is the workflow run itself**: no
GitHub Actions run of this file has happened, and none can from here. Copying
it in is what turns the above into CI's own result.

## Before you copy: check the action pins

Dependabot does not watch this directory, and `next` can lag `main`'s Dependabot bumps of the workflow files, so the pins in `images.yml` here can differ from the ones `main` has moved to. (The same is true of `ci.yml`, which Dependabot does watch in its real location: a pending copy is a snapshot, and a bump that lands on `main` after it was staged is not in it.) `TestActionPinsAreFullSHAs` requires every `uses:` here to be a full commit SHA; once the pending file is gone it also requires the real file's pins to agree with the other workflows. Before copying, compare `grep -h 'uses:' .github/workflows/*.yml docs/pending-workflows/images.yml | sort -u` (and the same with `docs/pending-workflows/ci.yml`) and keep the NEWER SHA for each action (verify it against the upstream tag with `gh api repos/<owner>/<repo>/git/ref/tags/<tag>`), so the copy never downgrades a pin.

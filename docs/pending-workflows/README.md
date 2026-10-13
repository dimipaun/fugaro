# Pending workflow changes

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

Replaces `.github/workflows/images.yml` (base image task 20, design
`docs/design/base-image.md`, the 0.7.0 hard cut). The committed file already
carries task 6/18's `fugaro-base`/`publish-base` addition (that earlier
pending copy was applied). This diff is task 20's own: it deletes the
legacy per-kind build job, now that `images/go/`, `images/web-node/` and
`images/java-services/` are gone from the tree. Diff summary against the
committed file:

- Job **`base`** (the `matrix: base: [web-node, go, java-services]` build,
  smoke-test, scan and save) is deleted outright: there is nothing left
  under `images/go/`, `images/web-node/` or `images/java-services/` for it
  to build.
- **`publish`**'s matrix shrinks from `[web-node, go, java-services,
  history]` to `[history]` alone, and its `needs` drops `base` (`[version,
  base, history]` becomes `[version, history]`); the base publishes through
  `publish-base` instead, unchanged by this diff.
- **`verify-public`**'s pull-check loop shrinks from `for img in web-node go
  java-services history; do` to `for img in history; do`.
- The `version` job's "Which images this tree builds" step is **unchanged**:
  it still lists `web-node go java-services history base` so a backport
  build off an old (pre-cut) release tag still detects those directories in
  that old tree and still skips their legs there, rather than failing. No
  current tree holds them any more, so on `main` those kinds are always
  absent from `kinds` going forward; this is deliberate, not a leftover.
- Every action pin (`actions/checkout`, `actions/upload-artifact`,
  `actions/download-artifact`, `actions/setup-go`) is carried over unchanged
  from the committed file — see "Before you copy" below regardless, since
  Dependabot may have moved `main` on since this was written.

**Nothing changes about permissions or the base/history jobs.** `publish`
still holds `packages: write` (it is the only one of the two
`packages: write` jobs this diff touches), `publish-base`, `fugaro-base` and
`history` are untouched, and `TestOnlyPublishJobsHoldPackagesWrite` and
`TestVerifyPublicRunsEvenWhenPublishBaseIsSkipped` keep passing unmodified
against this file (`readImagesWorkflow` reads this pending copy first).

### Why this is still pending here, not in the real file

Fugaro cannot push a change under `.github/workflows/` from an agent run
(GitHub rejects it), so this diff — like task 6/18's before it — is staged
here for a human to copy in by hand. Until that copy happens, the real
`.github/workflows/images.yml` still has the `base` job and still builds,
smoke-tests and publishes `fugaro-web-node`, `fugaro-go` and
`fugaro-java-services` on every tag: this repository's own `docs/release.md`
and `docs/gcp-live-checklist.md` describe that still-current behavior
deliberately, not as a bug. Copying this file in is what actually removes
those legs from CI; update `docs/release.md`'s "today builds, smoke tests,
scans and publishes ghcr.io/dimipaun/fugaro-web-node, fugaro-go,
fugaro-java-services and fugaro-history" line (and the `docker pull` list
under "Verifying a release") at the same time as the copy, not before.

### Not verified

This file has never run in GitHub Actions: no Docker daemon and no network
access were available in this run, so nothing about the deleted `base`
job's absence (or the trimmed `publish`/`verify-public` matrices) was
exercised against a real `images` run. What's verified is the YAML shape
through the Go tests above, read from this pending copy; the real workflow
run is CI's, once this file is copied into `.github/workflows/images.yml`
(removing it from here, see above) and a PR touching `images/**` runs it.

## Before you copy: check the action pins

Dependabot does not watch this directory, and `next` can lag `main`'s Dependabot bumps of the workflow files, so the pins in `images.yml` here can differ from the ones `main` has moved to. `TestActionPinsAreFullSHAs` requires every `uses:` here to be a full commit SHA; once the pending file is gone it also requires the real file's pins to agree with the other workflows. Before copying, compare `grep -h 'uses:' .github/workflows/*.yml docs/pending-workflows/images.yml | sort -u` and keep the NEWER SHA for each action (verify it against the upstream tag with `gh api repos/<owner>/<repo>/git/ref/tags/<tag>`), so the copy never downgrades a pin.

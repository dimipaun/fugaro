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
`TestOnlyPublishJobsHoldPackagesWrite` and
`TestVerifyPublicRunsEvenWhenPublishBaseIsSkipped`) read the pending file
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

Replaces `.github/workflows/images.yml` (base image task 6, design
`docs/design/base-image.md`). Diff summary against the committed file:

- The `version` job's "Which images this tree builds" step now checks for
  `images/base/Dockerfile` too (`for k in web-node go java-services history
  base; do`), so `kinds` can contain `base`.
- New job **`fugaro-base`**: builds the one base image (`images/base/`)
  natively per architecture (`matrix.include` with `amd64`/`ubuntu-latest`
  and `arm64`/`ubuntu-24.04-arm`), smoke-tests it, checks its size budget,
  scans it for secrets on every run and for vulnerabilities off pull
  requests, then saves the per-architecture image for `publish-base`.
- New job **`publish-base`**: loads both architectures' saved images,
  pushes each under `<version>-<arch>`, and builds the multi-platform
  index `<version>` (and `<major>` when it moves) from the two pushed
  digests with `docker buildx imagetools create`.
- `verify-public`'s script now also checks `ghcr.io/dimipaun/fugaro-base`
  with `images/verify-public.sh --arm64`, which fails unless the published
  reference is a multi-platform index holding *both* `linux/amd64` and
  `linux/arm64`. Its `needs` grows to `[version, publish, publish-base]`,
  and its `if` changes (see "publish-base can be skipped" below).
- `actions/upload-artifact` and `actions/download-artifact` are pinned to
  `v7.0.2` (`cf430e030ddbb5b0abf93d22962f4752f3646cd9`) and `v8.0.2`
  (`9000827ccba6bdab643e8b6fd33ac0654aef8333`) respectively, matching
  `origin/main`'s pins as of this writing (`main` bumped both since this
  branch diverged — PRs #278/#279 — so using main's SHAs here avoids this
  branch downgrading or conflicting with them when `next` is later merged
  into `main`).

**Permissions (the security-relevant part):** only two jobs in this file
hold `packages: write` — the pre-existing **`publish`** job (pushes
`web-node`/`go`/`java-services`/`history`) and the new **`publish-base`**
job (pushes the base's per-architecture tags and multi-platform index).
Every other job, including the new **`fugaro-base`** build/smoke/scan job,
holds `contents: read` only and never logs in to `ghcr.io`. The comment
above `publish` that used to say "Only this job ever needs packages:write"
was stale as soon as `publish-base` existed too (a prior review of an
earlier run of this task flagged exactly this); the version here says "This
job and publish-base are the only ones that need packages: write" instead.
`TestOnlyPublishJobsHoldPackagesWrite` (images/base_workflow_test.go) pins
this: it fails if any job other than `publish`/`publish-base` ever gains
`packages: write`, or if either of those two loses it.

**`publish-base` can be skipped without skipping `verify-public`:**
`publish-base`'s `if` already requires `base` in `kinds`, so on a tag tree
with no `images/base` (a backport off an old release line) it is skipped,
not failed. GitHub's default job gate treats "a needed job was skipped" the
same as "not satisfied" and would then skip `verify-public` too — silently
dropping the anonymous-pull check for the legacy images that `publish` did
still push. `verify-public`'s `if` therefore opens with `always()` and
checks `needs.publish.result == 'success'` (unchanged behavior: still
requires the legacy publish to have succeeded) together with
`needs['publish-base'].result != 'failure'` and
`!= 'cancelled'` (an actual publish-base failure or cancellation still
blocks verify-public; a skip does not).
`TestVerifyPublicRunsEvenWhenPublishBaseIsSkipped` pins the exact
substrings of that `if` expression.

### Operational note: the first copy publishes a new package

The first nightly/release run after this file is copied in publishes
`ghcr.io/dimipaun/fugaro-base:<version>` and `:<major>` as a **brand-new**
ghcr.io package (it has never been pushed before). Per `docs/release.md`,
a package a workflow publishes from this public repository is public from
the start — but `verify-public` is exactly the proof of that, not an
assumption: if the new `fugaro-base` package is not public yet for any
reason, `verify-public --arm64` will fail against it with "cannot be read
without a login". If that happens: check that the package is public (a
passing `verify-public` run is the proof it is); if it isn't, make it
public once by hand in the package's GitHub settings (an owner action,
same one-time step `docs/release.md` describes for the existing
`fugaro-*` packages) and re-run the workflow.

### Not verified

This file has never run in GitHub Actions: no Docker daemon and no network
access were available in this run, so `images/build-base.sh`,
`images/smoke.sh`, `images/size-budget.sh`, `images/scan.sh` and the actual
`docker buildx imagetools create` / `docker push` calls were never
exercised end-to-end. What's verified is the YAML shape and the shell
scripts' plain logic (`images/verify-public.sh`'s `--arm64` flag, tested
with a fake `docker`) — not a real build, a real multi-arch push, or a real
anonymous pull from `ghcr.io`. The `always()`/`needs[...].result` gate on
`verify-public` is likewise checked only as a pinned string in the YAML,
never exercised against a real skipped/failed `publish-base` job (GitHub
Actions' job-gating semantics are not something a local test can execute).
The real workflow run is CI's, once this file is copied into
`.github/workflows/images.yml` (removing it from here, see above) and a PR
touching `images/**` runs it.

## Before you copy: check the action pins

Dependabot does not watch this directory, and `next` can lag `main`'s Dependabot bumps of the workflow files, so the pins in `images.yml` here can differ from the ones `main` has moved to. `TestActionPinsAreFullSHAs` requires every `uses:` here to be a full commit SHA; once the pending file is gone it also requires the real file's pins to agree with the other workflows. Before copying, compare `grep -h 'uses:' .github/workflows/*.yml docs/pending-workflows/images.yml | sort -u` and keep the NEWER SHA for each action (verify it against the upstream tag with `gh api repos/<owner>/<repo>/git/ref/tags/<tag>`), so the copy never downgrades a pin.

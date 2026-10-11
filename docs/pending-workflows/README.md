# Pending workflow changes

Fugaro refuses to push a commit that touches `.github/workflows/**`
(GitHub rejects the push). When a Fugaro run needs to change a workflow
file, it writes the file's full intended new content here instead, under
the same name, and the owner copies it into `.github/workflows/` by hand
after reviewing it.

The static Go tests that pin these workflows' shape (e.g.
`images/base_workflow_test.go`'s `TestImagesWorkflowPublishesAMultiArchBase`
and `TestOnlyPublishJobsHoldPackagesWrite`) read the real workflow file and
fall back to the matching file here when the real file doesn't hold the
change yet (see `readImagesWorkflow` / `imagesWorkflowPath` /
`pendingImagesWorkflowPath` in that file). That means they're green before
the copy and keep guarding the same jobs after it, with no test edit
needed at copy time.

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
  `linux/arm64`. Its `needs` grows to `[version, publish, publish-base]`.

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

### Not verified

This file has never run in GitHub Actions: no Docker daemon and no network
access were available in this run, so `images/build-base.sh`,
`images/smoke.sh`, `images/size-budget.sh`, `images/scan.sh` and the actual
`docker buildx imagetools create` / `docker push` calls were never
exercised end-to-end. What's verified is the YAML shape and the shell
scripts' plain logic (`images/verify-public.sh`'s `--arm64` flag, tested
with a fake `docker`) — not a real build, a real multi-arch push, or a real
anonymous pull from `ghcr.io`. The real workflow run is CI's, once this
file is copied into `.github/workflows/images.yml` and a PR touching
`images/**` runs it.

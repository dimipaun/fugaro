# images

The Dockerfiles, the shell checks and the tests of the images Fugaro builds and
ships. Every script here runs on its own against an image with plain docker,
because CI can be unavailable; the tests that need a container run the same
scripts with the `docker` build tag.

## The scripts

- `build-base.sh BASE [TAG...]` builds `images/BASE` from this checkout
  (default tag `fugaro-BASE:dev`), the way CI and the docker tests build it.
- `smoke.sh IMAGE BASE` checks a built base image (design §13): the tools, the
  pins its Dockerfile's ARGs hold, and the image's own selftest.
- `smoke-credentials.sh IMAGE` and `smoke-history.sh IMAGE` check the two small
  images.
- `scan.sh [--secrets] IMAGE` scans a built image with the Trivy it pins by
  digest. `--secrets` is the leak route a catalog build runs before it
  promotes: every layer and the config for secrets, plus the checks Trivy's own
  rules cannot state (images/trivy-secret.yaml, and `leaks` inside the script).
- `size-budget.sh IMAGE MB [WARN]` bounds a published image's size and
  `verify-public.sh [--arm64] REF...` checks a pushed one.

## The tests

`go test ./images/` needs nothing but Go: the scan's own tests run it against a
fake docker and fixture tars (`scan_test.go`), so they are part of the default
suite and of CI's `rest-images` shard.

A test that builds or runs a real container carries `//go:build docker` and
needs a Docker daemon and network (it pulls the images its Dockerfile builds
from, and the Trivy image `scan.sh` runs). Run them all with

```sh
go test -tags docker -count=1 ./images/
```

`TestScanSecretsOnACatalogShapedImage` runs the real `scan.sh --secrets` on a
small real image shaped like a catalog build (`images/catalog/go/Dockerfile`):
a tiny pinned base, the two paths `trivy-secret.yaml` allows a build's false
positives at holding throwaway placeholders, two RUNs that change nothing, and
a private key in the checkout. It is the assertion a fixture cannot make -- a
daemon really does hand the image over with a repeated empty layer, and the
pinned Trivy really does flag and clear what the config says. Run it alone
with

```sh
go test -tags docker -count=1 -run TestScanSecretsOnACatalogShapedImage ./images/
```

A test skips itself where no daemon answers (`testutil.RequireDocker`), and the
undecodable-layer case skips where the daemon's image store hands over no layer
the worker cannot read. CI runs the docker-tagged set in
`.github/workflows/images.yml`'s `docker-tests` job, on every event but a push.

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
- `size-budget.sh IMAGE [MAX_MB [WARN_MB]]` bounds a published image's size and
  `verify-public.sh [--arm64] REF...` checks a pushed one.

## The tests

`go test ./images/` needs nothing but Go: the scan's own tests run it against a
fake docker and fixture tars (`scan_test.go`), so they are part of the default
suite and of CI's `rest-images` shard. That includes
`TestScanReadsTheCatalogFixture`, which reads the fixture the docker-tagged test
loads -- the same tar, as the image's save -- so a fixture that a load would
reshape fails on a machine with no daemon rather than only on the nightly job.

A test that loads or runs a real container carries `//go:build docker` and needs
a Docker daemon and network (the Trivy image `scan.sh` runs). Run them all with

```sh
go test -tags docker -count=1 ./images/
```

`TestScanSecretsOnACatalogShapedImage` loads an image shaped like a catalog
build (`images/catalog/go/Dockerfile`) into a real daemon and runs the real
`scan.sh --secrets` on it: the two paths `trivy-secret.yaml` allows a build's
false positives at holding throwaway placeholders, an empty layer the image's
history lists twice, and -- in a second image -- a private key in the checkout.

The image is written as an OCI layout tar by `scan_catalog_test.go` and loaded
with `docker load`, rather than built from a Dockerfile: what a no-op step
leaves is the builder's to shape, and the repeat this test pins is only the
fixture's to promise. Everything past the load is real -- the daemon's inspect,
save and export, and the pinned Trivy over them -- which is the assertion a
fixture cannot make. Run it alone with

```sh
go test -tags docker -count=1 -run TestScanSecretsOnACatalogShapedImage ./images/
```

Its subtests are the four things a catalog build's scan has to get right: the
clean image passes; the two placeholders are findings of their own, so they are
what the allowances clear; a key left in the checkout is refused by name; and a
layer no decompressor can read is refused. The last one is built only where the
daemon keeps its layers as written: a store that re-exports each layer's plain
diff tar (the classic store) hands over nothing this worker cannot read, and
that subtest says so when it skips. A test skips itself where no daemon answers
(`testutil.RequireDocker`). CI runs the docker-tagged set in
`.github/workflows/images.yml`'s `docker-tests` job, on every event but a push.

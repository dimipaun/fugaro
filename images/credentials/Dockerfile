# syntax=docker/dockerfile:1.10@sha256:865e5dd094beca432e8c0a1d5e1c465db5f998dca4e439981029b3b81fb39ed5
# Fugaro credentials image (credentials-service plan, task C1.14): the fugaro
# binary alone in a distroless static image, for the credentials Cloud Run
# service. It is the one process that holds model credentials -- it reads the
# bundle secret (Q6: one secret container, one accessor grant) as its own
# service account and issues nothing else -- so it carries no shell, no
# package manager, no git, no model CLI and no credential of its own: its
# identity is the service's account, and the bundle is read at run time, never
# baked in.
#
# Unlike the history job's image, this one is not started with a command: a
# Cloud Run *service*'s container sets neither command nor args, so the
# image's own entrypoint is the service and the CMD below is what runs
# (internal/cli/credentials_serve.go, `fugaro credentials serve`).
# internal/infra/tf's cover case for google_cloud_run_v2_service is written
# against exactly that: it refuses a plan whose container sets a command or
# args. Nothing builds or publishes it for the project until the images
# workflow's staged credentials leg is copied in (docs/pending-workflows/
# images.yml, Convention 8): build it by hand from the repository root with
# images/build-base.sh credentials <registry>/fugaro-credentials:latest and
# docker push it. The syntax pin above, like the one in images/base/Dockerfile
# and images/history/Dockerfile, is bumped by hand monthly.
#
# Both of this image's bases are pinned BY DIGEST, the way images/base/Dockerfile
# pins Debian (ARG DEBIAN_DIGEST) and unlike the history image, which still
# carries the "pin it by digest when the digest is known" note. The two are the
# builder that compiles the binary and the distroless layer the service runs,
# and neither is a floating tag here: a tag is a name whose bytes the registry
# may point somewhere else between two builds of the same commit, and this
# image is the one process that holds every model credential the installation
# has -- the digest the release signs and `fugaro update` writes into the
# service's Terraform variable is a digest of what the build produced, and a
# base that moved under it would make that signature describe something else
# than what a rebuild from the same commit produces. The digests are read from
# the registries' own manifests (docker buildx imagetools inspect) and bumped
# by hand with the syntax pin; images/credentials_test.go asserts both FROMs
# carry one, so a tag cannot come back by accident.

FROM --platform=$BUILDPLATFORM golang:1.27-bookworm@sha256:972b4110ba0f47539a975c31e52c64765e1ff19f2d74469ae4a595c6676e80a5 AS fugaro
ARG TARGETOS
ARG TARGETARCH
ARG FUGARO_VERSION=dev
ARG FUGARO_COMMIT=unknown
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY images ./images
COPY deploy/terraform ./deploy/terraform
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      -ldflags "-s -w -X github.com/dimipaun/fugaro/internal/cli.Version=${FUGARO_VERSION} -X github.com/dimipaun/fugaro/internal/cli.Commit=${FUGARO_COMMIT}" \
      -o /out/fugaro ./cmd/fugaro

# static-debian12:nonroot has CA certificates, tzdata and uid 65532 and
# nothing else. The digest is the multi-platform index read on 2026-10-22
# (docker buildx imagetools inspect gcr.io/distroless/static-debian12:nonroot),
# so the build resolves the same bytes on either build platform; it moves by
# hand with the syntax pin above.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=fugaro /out/fugaro /usr/local/bin/fugaro
USER 65532:65532
# The service's entrypoint: Cloud Run sets no command and no args, so this CMD
# is what the revision runs. It reads the three names the installation module
# sets on the service (FUGARO_PROJECT, FUGARO_GCP_PROJECT, FUGARO_REGION) and
# refuses to start, naming what is missing, rather than serving anything a run
# would act on.
ENTRYPOINT ["/usr/local/bin/fugaro"]
CMD ["credentials", "serve"]

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

FROM --platform=$BUILDPLATFORM golang:1.27-bookworm AS fugaro
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
# nothing else. Pinned by tag only: pin it by digest when the digest is known
# (docker buildx imagetools inspect gcr.io/distroless/static-debian12:nonroot).
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=fugaro /out/fugaro /usr/local/bin/fugaro
USER 65532:65532
# The service's entrypoint: Cloud Run sets no command and no args, so this CMD
# is what the revision runs. It reads the three names the installation module
# sets on the service (FUGARO_PROJECT, FUGARO_GCP_PROJECT, FUGARO_REGION) and
# refuses to start, naming what is missing, rather than serving anything a run
# would act on.
ENTRYPOINT ["/usr/local/bin/fugaro"]
CMD ["credentials", "serve"]

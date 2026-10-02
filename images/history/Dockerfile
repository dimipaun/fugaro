# syntax=docker/dockerfile:1.10@sha256:865e5dd094beca432e8c0a1d5e1c465db5f998dca4e439981029b3b81fb39ed5
# Fugaro history image (design D11, §6.5): the fugaro binary alone in a
# distroless static image, for the history Cloud Run job (fugaro budget
# history --sweep). It holds firebasedatabase.admin and firebaseauth.admin on
# the Firebase project, so it carries no shell, no package manager, no git, no
# model CLI and no credential: its identity is the job's service account.
# Nothing builds or publishes it for the project until M7 publishes images
# (D11): build it by hand from the repository root with
# images/build-base.sh history <registry>/fugaro-base/history:latest and
# docker push it (fugaro init --firebase prints the commands). The syntax pin above, like the one in
# images/web-node/Dockerfile, is bumped by hand monthly.

FROM --platform=$BUILDPLATFORM golang:1.27-bookworm AS fugaro
ARG TARGETOS
ARG TARGETARCH
ARG FUGARO_VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY images ./images
COPY deploy/terraform ./deploy/terraform
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      -ldflags "-s -w -X github.com/dimipaun/fugaro/internal/cli.Version=${FUGARO_VERSION}" \
      -o /out/fugaro ./cmd/fugaro

# static-debian12:nonroot has CA certificates, tzdata and uid 65532 and
# nothing else. Pinned by tag only: pin it by digest when the digest is known
# (docker buildx imagetools inspect gcr.io/distroless/static-debian12:nonroot).
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=fugaro /out/fugaro /usr/local/bin/fugaro
USER 65532:65532
# The job sets command and args (fugaro budget history --sweep); the default
# is a harmless usage message.
ENTRYPOINT ["/usr/local/bin/fugaro"]
CMD ["version"]

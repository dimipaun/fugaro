# SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
# See ee/LICENSE for the terms.
#
# The portal API's image (portal plan P1.7, design
# docs/key-signing-service/spec.md S3.5, S3.7): the program alone in a
# distroless static image, for the `fugaro-portal-api` Cloud Run service.
#
# It holds the deployment's public half — the document store, the mail key, the
# merchant's secret — so it carries no shell, no package manager, no git and no
# credential: the Secret Manager and Firestore clients reach for the service's
# own account, and nothing is baked in. Its other half is the minter's image
# beside it, and the signer is there and not here (Check 174).
#
# Cloud Run sets no command and no args for a service, so this image's own
# entrypoint is what a revision runs: the program serves. Its one subcommand,
# `seed-operations <address>`, is what the deployment's seed job runs: the
# services root declares that job (`google_cloud_run_v2_job.seed`, the same image
# and the API's own environment) and the deploy executes it, because the seed
# builds the API's own service before it writes anything and so needs the
# twenty-three variables a revision's environment carries — which no shell on
# anybody's machine has, and none of which the project's credentials stand in for
# (P0.5, P9.2).
#
# **This file's first two lines are the ee/ notice**, which is why there is no
# `# syntax=` directive: BuildKit reads that directive only above every other
# line, and TestEEFilesCarryTheNotice requires the notice there. So this Dockerfile
# uses no BuildKit-only instruction — no cache mount, no heredoc — and builds
# the same way under any builder. The bases are pinned by tag with the digest
# the registry answered written beside them where one is known, as
# images/history/Dockerfile does; bump both by hand with the repository's other
# pins.
FROM --platform=$BUILDPLATFORM golang:1.27-bookworm AS builder
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
# ee/ is the program's own tree; internal/ and images/ are what it reaches
# through the coordinator's own packages (go list -deps), and a missing one is
# a build that fails rather than an image that starts and panics.
COPY ee ./ee
COPY internal ./internal
COPY images ./images
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      -ldflags "-s -w" -o /out/portal-api ./ee/keyservice/cmd/portal-api

# static-debian12:nonroot has CA certificates, tzdata and uid 65532 and nothing
# else, which is what the two Google clients and the mail path need.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/portal-api /usr/local/bin/portal-api
USER 65532:65532
LABEL org.opencontainers.image.licenses="LicenseRef-Fugaro-Subscription"
ENTRYPOINT ["/usr/local/bin/portal-api"]

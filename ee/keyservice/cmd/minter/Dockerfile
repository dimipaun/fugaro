# SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
# See ee/LICENSE for the terms.
#
# The minter's image (portal plan P1.7, design
# docs/key-signing-service/spec.md S3.2, S3.3): the program alone in a
# distroless static image, for the `fugaro-key-minter` Cloud Run service.
#
# It is the one process in the deployment that can sign, and the whole of what
# it holds is the Cloud KMS client: no store, no mail key, no merchant secret
# and no secret mounted into it at all. Whoever deploys it grants the API's own
# account alone the invoker role (P1.5, Check 174), and the container's own
# account holds `cloudkms.signerVerifier` on one key version — nothing this file
# can express, which is why the deploy's IAM is what holds it.
#
# Cloud Run sets no command and no args, so the entrypoint below is
# what a revision runs; the program reads three variables
# (FUGARO_ENVIRONMENT, FUGARO_KEYS, FUGARO_SIGNER_KEY_VERSION) and refuses any
# other FUGARO_ variable, which is how the API's environment cannot end up in
# this process by a mistake in a deploy script (config.go).
#
# **This file's first two lines are the ee/ notice**, so there is no
# `# syntax=` directive and this Dockerfile uses no BuildKit-only instruction
# (see the portal API's Dockerfile beside it).
FROM --platform=$BUILDPLATFORM golang:1.27-bookworm AS builder
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY ee ./ee
COPY internal ./internal
COPY images ./images
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      -ldflags "-s -w" -o /out/minter ./ee/keyservice/cmd/minter

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/minter /usr/local/bin/minter
USER 65532:65532
LABEL org.opencontainers.image.licenses="LicenseRef-Fugaro-Subscription"
ENTRYPOINT ["/usr/local/bin/minter"]

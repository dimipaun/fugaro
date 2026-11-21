# SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
# See ee/LICENSE for the terms.
#
# The identity service's image (mobile identity plan task A1): the program alone
# in a distroless static image, for the signer's Cloud Run service (A3).
#
# It is the process that turns one verified sign-in into one project-bound
# assertion, and the whole of what it holds is the Cloud KMS client: no store, no
# secret and nothing mounted into it at all. What it reads that is not its own is
# Google's published Firebase key set (public, fetched over HTTPS) and the
# served, root-signed key set — a document of public keys, which the owner writes
# and this service reads.
#
# **The grants the deploy gives it**, none of which this file can express: the
# container's own service account holds `cloudkms.signerVerifier` on the ONE
# asymmetric key version FUGARO_IDENTITY_KEY_VERSION names (never
# `cloudkms.admin`, and never a second key), and a read on the served key set's
# object (A3's bucket, O7). The route is public, because the phones that call it
# hold no Google identity — what admits a caller is the Firebase ID token in the
# request and nothing else, which is why this service's own account is the only
# credential in the container.
#
# Cloud Run sets no command and no args, so the entrypoint below is what a
# revision runs. The program reads four variables and no others —
# FUGARO_IDENTITY_PROJECT (the identity project's own Firebase project id),
# FUGARO_IDENTITY_KEY_ID (the `kid` the served key set must publish),
# FUGARO_IDENTITY_KEY_VERSION (the KMS key version above), FUGARO_IDENTITY_KEYSET
# (a bucket URI, gs://bucket/object, holding the served key set) — and it refuses
# every other FUGARO_ variable, which is how another service's environment cannot
# end up in this process by a mistake in a deploy script (main.go's loadConfig).
#
# **This file's first two lines are the ee/ notice**, so there is no
# `# syntax=` directive and this Dockerfile uses no BuildKit-only instruction
# (see the minter's Dockerfile beside it, in ee/keyservice/cmd/minter).
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
      -ldflags "-s -w" -o /out/identitysvc ./ee/identitysvc/cmd/identitysvc

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/identitysvc /usr/local/bin/identitysvc
USER 65532:65532
LABEL org.opencontainers.image.licenses="LicenseRef-Fugaro-Subscription"
ENTRYPOINT ["/usr/local/bin/identitysvc"]

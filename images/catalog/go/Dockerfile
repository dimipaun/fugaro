# syntax=docker/dockerfile:1.10@sha256:865e5dd094beca432e8c0a1d5e1c465db5f998dca4e439981029b3b81fb39ed5
# The catalog entry this repository runs: the image of its own `go` workflow,
# built by the project image catalog (docs/project-image-catalog) instead of by
# Fugaro's per-repository renderer (docs/repo-image-removal, R0).
#
# It is the definition images/derived/Dockerfile.tmpl used to render for this
# workflow, written out by hand because the renderer is what the removal takes
# away (D1208): the entry's own Dockerfile does the clone, the dependency
# warm-up and the finalize-checkout call, and the engine gains no bake mode.
#
# Build arguments and secrets, exactly what the derived build handed the
# rendered Dockerfile (images/derived/cloudbuild.yaml, the `build` step):
#   FUGARO_BASE    the Fugaro base image; Cloud Build passes it by digest
#   REPO_URL       this repository's https clone URL, without credentials
#   BASE_BRANCH    the branch to clone
#   FUGARO_COMMIT  the commit the catalog copy was taken from, which is the
#                  commit the checkout is reset to
#   git-credentials  a git credential-store file for REPO_URL, mounted as a
#                  BuildKit file secret and never written into a layer
ARG FUGARO_BASE
FROM ${FUGARO_BASE}
ARG REPO_URL
ARG BASE_BRANCH
ARG FUGARO_COMMIT=""

WORKDIR /work/repo
# The clone is reset to FUGARO_COMMIT by itself, so a push to the branch after
# the definition was copied cannot change what this image bakes. A commit no
# longer on the branch (a force-push) is fetched by its own ID; when it cannot
# be fetched either, the build fails rather than baking the branch head.
RUN --mount=type=secret,id=git-credentials,uid=1000,required=false \
    case "$FUGARO_COMMIT" in *[!0-9a-f]*) echo "FUGARO_COMMIT is not a commit ID" >&2; exit 1 ;; esac \
 && GIT_TERMINAL_PROMPT=0 git -c credential.helper= \
      -c credential.helper='store --file=/run/secrets/git-credentials' \
      clone --quiet --branch "$BASE_BRANCH" --single-branch -- "$REPO_URL" /work/repo \
 && commit="${FUGARO_COMMIT:-HEAD}" \
 && { git -C /work/repo cat-file -e "$commit^{commit}" 2>/dev/null \
      || GIT_TERMINAL_PROMPT=0 git -C /work/repo -c credential.helper= \
           -c credential.helper='store --file=/run/secrets/git-credentials' \
           fetch --quiet origin "$commit" \
      || { echo "commit $commit is no longer reachable: the branch was force-pushed; re-run the build" >&2; exit 1; }; } \
 && git -C /work/repo reset --quiet --hard "$commit"

# This repository's own tools, from its mise.toml (Go, Terraform, tflint,
# Trivy): the base image installs no toolchain of its own. The download cache
# survives local rebuilds, and nothing may be left missing.
RUN --mount=type=cache,id=fugaro-mise,target=/home/fugaro/.cache/mise,uid=1000,gid=1000 mise install \
 && missing="$(mise ls --current --missing)" \
 && if [ -n "$missing" ]; then printf 'mise left these tools missing:\n%s\n' "$missing" >&2; exit 1; fi

# tflint's google plugin, fetched here where the build has network, so the
# `tflint --chdir=deploy/terraform/gcp --init` a run of .claude/rules/
# terraform.md asks for usually finds it installed already. Best effort on
# purpose: --init asks the GitHub API for the plugin's release, an anonymous
# caller gets 60 requests an hour from an address Cloud Build's shared egress
# can already be past (the limit ci.yml's own step passes a token to avoid),
# and a refused call must not fail the whole catalog build -- fugaro update's
# rebuild of it included -- over a plugin cache. That build bakes no plugin,
# and a run's own --init downloads it then.
#
# The directory is under fugaro's home and TFLINT_PLUGIN_DIR names it: a run
# reads a catalog image's ENV (docs/base-image.md), and tflint reads the
# variable, so the two agree without a run having to set anything.
ENV TFLINT_PLUGIN_DIR=/home/fugaro/.tflint.d/plugins
RUN tflint --chdir=deploy/terraform/gcp --init \
 || echo "tflint --init fetched no plugin; the image carries none and a run's own --init will" >&2

# The workflow's image.setup verbatim (fugaro.yaml): bake the module cache and
# a warm build cache into the image, so a run's verify step does not pay for
# them.
RUN go mod download && go build ./...

# The base ships sudo and su setuid-root and granting nothing; a rendered image
# disarms both here, after image.setup (D1). fugaro needs neither, and su's
# setuid bit is the empty-password route a setup step's `passwd -d root` would
# otherwise open. The catalog build's own smoke step requires it as well: the
# candidate's root selftest expects Debian's standard setuid binaries and
# neither sudo nor su (internal/image/selftest.go), so an entry that kept them
# would fail its build rather than run a weaker image.
USER root
RUN { [ ! -e /usr/bin/sudo ] || chmod u-s /usr/bin/sudo; } \
 && { [ ! -e /usr/bin/su ] || chmod u-s /usr/bin/su; }
USER fugaro

RUN /usr/local/lib/fugaro/finalize-checkout /work/repo

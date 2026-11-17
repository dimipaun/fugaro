# syntax=docker/dockerfile:1.10@sha256:865e5dd094beca432e8c0a1d5e1c465db5f998dca4e439981029b3b81fb39ed5
# The catalog entry this repository runs: the image of its own `go` workflow,
# built by the project image catalog (docs/project-image-catalog) instead of by
# Fugaro's per-repository renderer (docs/repo-image-removal, R0).
#
# It is the definition images/derived/Dockerfile.tmpl used to render for this
# workflow, written out by hand because the renderer is what the removal takes
# away (D1208): the entry's own Dockerfile finishes the checkout and warms what
# the job would otherwise pay for, and the engine gains no bake mode.
#
# The checkout arrives under the name `fugaro-checkout`: the catalog build's
# clone step clones this repository in a build of Fugaro's own, from the pinned
# base digest, and tags the checkout with that name (B8), so this definition
# never clones with a credential of its own — a definition that mounts one is
# refused by name, with the line it is written on (B9, D2311). The step resets
# the checkout to the commit the copied definition was taken from, so a push to
# the branch after the copy cannot change what this image bakes.
ARG FUGARO_BASE
FROM ${FUGARO_BASE}

# The whole checkout, at that commit. The step that used to stand here cloned it
# with the git credential mounted as a BuildKit file secret; that is the
# definition B9 refuses today, by name and by line.
WORKDIR /work/repo
COPY --from=fugaro-checkout --chown=1000:1000 / /work/repo

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

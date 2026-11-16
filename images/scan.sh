#!/bin/sh
# scan.sh IMAGE scans a built image for vulnerabilities with Trivy, run
# through plain docker. It prints HIGH and CRITICAL findings and fails only
# on CRITICAL ones that have a fix available.
#
# scan.sh --secrets IMAGE scans every layer (each one separately, so a file a
# later layer deletes is still seen) and the image config (its history and
# environment, where a leaked build argument would show) for secrets, fails on
# any finding not allowed by images/trivy-secret.yaml, and then runs the checks
# Trivy's own rules cannot state (leaks, below): a variable named like a
# credential in the environment the config sets, a credential file in the
# filesystem the image ends with, and the git configs a build's own git call
# writes a credential to, which Trivy's walk skips where one sits inside a
# directory named .git: those are read out of every layer of the image as well
# as out of the filesystem it ends with, since a config a later layer deleted
# is still a credential the image carries, and an image holding a layer this
# cannot decode is refused rather than cleared in part. A catalog build runs
# this route before it promotes (internal/backend/gcp's catalog-scan step, B3,
# R2), so an image that carries a credential of the build's is refused rather
# than promoted.
set -eu
mode=vuln
if [ "${1:-}" = --secrets ]; then
  mode=secrets
  shift
fi
image=${1:?usage: scan.sh [--secrets] IMAGE}
cache=${TRIVY_CACHE_DIR:-$HOME/.cache/trivy}
mkdir -p "$cache"
here=$(cd "$(dirname "$0")" && pwd)
# Pinned by digest (not just the 0.76.2 tag, which is mutable): checked on
# 2026-11-08 through the registry's manifest API, the read `docker inspect
# aquasec/trivy:0.76.2 --format '{{index .RepoDigests 0}}'` makes -- the same
# one that produced the 0.65.0 pin this replaced. 0.65.0 moved because GitHub
# no longer serves a release for it, and mise installs Trivy from the release,
# so the version mise.toml pins (the Trivy a run's own PATH carries) cannot be
# it; images/repo_pins_test.go holds this tag's version and that pin equal.
# Dependabot doesn't cover this pin (it's a shell variable, not a Dockerfile
# FROM): bump it by hand monthly, alongside the base refreshes, to the latest
# Trivy release's digest, and move mise.toml's trivy pin with it.
trivy_image=aquasec/trivy@sha256:8f6bb947dbc528902e11f2f5be52f7b519f6a39d5d148f41166f2040499768fb
trivy() {
  docker run --rm -v /var/run/docker.sock:/var/run/docker.sock -v "$cache:/root/.cache/trivy" \
    -v "$here/trivy-secret.yaml:/trivy-secret.yaml:ro" "$trivy_image" image "$@" "$image"
}
# env_credential_names matches a variable *named* like a credential. Trivy
# reads the image config's environment itself, but only with its built-in
# rules -- a config's own rules reach the filesystem and not the config, so the
# name is what this check reads. It is matched case-insensitively, anchored on
# a separator at each end so that a name which merely contains one of these
# words -- the base image's COREPACK_ENABLE_DOWNLOAD_PROMPT, say, or a
# TOKENIZER_PATH -- is not a finding.
env_credential_names='(^|_)(TOKEN|SECRET|PASSWORD|PASSWD|CREDENTIAL|CREDENTIALS|API_KEY|ACCESS_KEY|PRIVATE_KEY)($|_)'
# git_config_credential is images.GitConfigCredentialText verbatim (a test
# pins it there), the same text images/trivy-secret.yaml's
# fugaro-git-config-credential rule greps for: one definition of what a
# credential in a git config is, for the two readers that have no git to ask.
git_config_credential='\[credential|^[[:space:]]*helper[[:space:]]*=|extraheader|(https?|git)://[^/[:space:]]*@'
# git_config_path is images.GitConfigPathText verbatim (a test pins it), the
# same text images/trivy-secret.yaml's fugaro-git-config-credential rule reads
# its path with: every file git reads a config from, because a build's own git
# call takes the credential to whichever of them is configured -- a checkout's
# .git/config, a bare or mirror repository's <name>.git/config, the global and
# system configs. A fence that knew one spelling would pass an image whose
# build used another.
git_config_path='(^|/)(\.gitconfig|gitconfig|[^/]*\.git/config|(\.config|xdg)/git/config)$'
# leaks checks the image as it ends up, for the three things a layer scan
# cannot state:
#
#   - the environment its config sets. Trivy's image-config scan applies its
#     built-in rules to it and no custom rule reaches it, so a provider's own
#     token -- an app password, a workspace token, neither of which has a
#     built-in format -- would pass there. A variable whose value is a path is
#     no finding: the check reads the value, below.
#   - a credential *file*, by name: a .netrc and a credential-helper store are
#     findings whatever they hold, and Trivy writes one only where the file's
#     lines match its rule.
#   - a git config carrying a credential, wherever git_config_path finds one:
#     Trivy skips the ones under a directory named .git -- a directory named
#     .git is left out of its filesystem analysis -- so a checkout baked with
#     `git clone https://user:token@host` in it is a credential its layer scan
#     does not report. Each config git_config_path names is read here, out of
#     the filesystem the image ends with and out of every layer of it.
#
# The two fences are complements, not duplicates: where Trivy's rules see the
# contents of every layer, this one sees the filesystem the image ends with, so
# a `.netrc` a later layer deleted is a finding there and no file here could
# show it. A git config under a directory named .git is the one thing neither
# fence reads whole on its own, which is why it is read in both: the layers
# through docker save (below), the image as it ends through docker export.
leaks() {
  work=$(mktemp -d)
  cid=$(docker create "$image")
  trap 'docker rm -f "$cid" >/dev/null 2>&1 || true; rm -rf "$work"' EXIT INT TERM
  docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$image" > "$work/env"
  # A variable *named* like a credential is a finding unless the value it holds
  # is a path. GOOGLE_APPLICATION_CREDENTIALS, AWS_SHARED_CREDENTIALS_FILE,
  # API_KEY_PATH and SSH_PRIVATE_KEY_PATH are the platform's own names for a
  # *file*, and the path an author writes for one carries no secret: refusing
  # that shape tells an author to put a path in a secret mount, which a name the
  # platform reserves and drops at run time cannot do. The definition guard lets
  # the same shape through for the same reason
  # (internal/catalog/fetch.go's pathValue, whose test this is: whitespace
  # trimmed, then a leading /, ./, ../ or ~/ and nothing else -- a value
  # written through a variable is not a path literal and stays a finding).
  : > "$work/named"
  while IFS= read -r setting; do
    [ -n "$setting" ] || continue
    name=${setting%%=*}
    value=${setting#*=}
    value=${value#"${value%%[![:space:]]*}"}
    case "$value" in
      # The tilde is quoted: a shell expands an unquoted one in a case pattern
      # to the home directory, and then no value beginning with ~ would match
      # and every such value would be refused.
      /*|./*|../*|"~/"*) continue ;;
    esac
    printf '%s\n' "$name" | grep -Eiq "$env_credential_names" || continue
    printf '%s\n' "$name" >> "$work/named"
  done < "$work/env"
  if named=$(sort -u "$work/named") && [ -n "$named" ]; then
    echo "scan.sh: $image sets a credential in its environment: $(printf '%s' "$named" | tr '\n' ' ')" >&2
    echo "scan.sh: a value a build needs belongs in a BuildKit secret file mount, never in an ENV" >&2
    exit 1
  fi
  # The export is read for its member names and not for its contents: it is a
  # whole filesystem, and the one file whose text is wanted is copied out of
  # the container on its own, by docker, rather than unpacked from here.
  docker export "$cid" | tar -tf - > "$work/names"
  [ -s "$work/names" ] || {
    echo "scan.sh: $image exported no filesystem to read" >&2
    exit 1
  }
  if found=$(grep -E '(^|/)(\.netrc|_netrc|\.git-credentials)$' "$work/names") && [ -n "$found" ]; then
    echo "scan.sh: $image bakes a credential file: $(printf '%s' "$found" | tr '\n' ' ')" >&2
    exit 1
  fi
  grep -E "$git_config_path" "$work/names" > "$work/configs" || true
  while IFS= read -r config; do
    [ -n "$config" ] || continue
    docker cp "$cid:/$config" "$work/config" 2>/dev/null || continue
    if grep -Eiq "$git_config_credential" "$work/config"; then
      echo "scan.sh: $image bakes a git config that carries a credential: $config" >&2
      echo "scan.sh: a git config baked into an image carries no credential" >&2
      exit 1
    fi
  done < "$work/configs"
  # And the same configs in every layer of the image, which the export cannot
  # show: an image whose checkout a later layer deleted -- its config handed to
  # a build that copied the tree out of it, say -- carries the credential in a
  # layer that the filesystem the image ends with no longer has. docker save is
  # what hands over the layers: an uncompressed tar whose members are the
  # layers themselves, beside the manifests and the image config, which are
  # JSON.
  #
  # A member is written out before it is read, rather than piped into the
  # reader: a layer is stored as the daemon holds it -- an uncompressed tar for
  # what docker built, a compressed one (gzip, or whatever codec an image was
  # pushed with, kept as it is under the containerd image store) -- and tar
  # finds a compression by its magic in a file while it refuses a compressed
  # stream on stdin ("Archive is compressed. Use -z option"). Piping would
  # leave every layer unread and the check passing over an image it never
  # looked into, with nothing said.
  #
  # Every layer is read or the image is refused: a layer this worker cannot
  # decode -- a codec it has no decompressor for -- is not a layer that carries
  # no credential, and a skip would promote an image nobody cleared. The count
  # is the daemon's own, so the number of layers is not taken from the save's
  # JSON here.
  docker save -o "$work/image.tar" "$image"
  layers=$(docker inspect --format '{{len .RootFS.Layers}}' "$image")
  tar -tf "$work/image.tar" > "$work/outer"
  read_layers=0
  while IFS= read -r entry; do
    case "$entry" in */) continue ;; esac
    tar -xOf "$work/image.tar" "$entry" > "$work/layer" 2>/dev/null || continue
    if ! tar -tf "$work/layer" > "$work/members" 2>/dev/null; then
      # Not a tar: a manifest or the image's config, which a save carries
      # beside its layers and which this is not the fence for.
      continue
    fi
    read_layers=$((read_layers + 1))
    grep -E "$git_config_path" "$work/members" > "$work/configs" || true
    while IFS= read -r member; do
      [ -n "$member" ] || continue
      tar -xOf "$work/layer" "$member" 2>/dev/null > "$work/config" || {
        echo "scan.sh: $image holds a git config this cannot read: $member" >&2
        exit 1
      }
      if grep -Eiq "$git_config_credential" "$work/config"; then
        echo "scan.sh: $image bakes a git config that carries a credential: $member (in a layer of the image)" >&2
        echo "scan.sh: a git config baked into an image carries no credential" >&2
        exit 1
      fi
    done < "$work/configs"
  done < "$work/outer"
  if [ "$read_layers" -ne "$layers" ]; then
    echo "scan.sh: $image holds $layers layers and this read $read_layers of them: a layer it cannot decode is a layer it cannot clear" >&2
    echo "scan.sh: the image is refused rather than promoted with a layer nobody read" >&2
    exit 1
  fi
}
if [ "$mode" = secrets ]; then
  trivy --scanners secret --image-config-scanners secret --secret-config /trivy-secret.yaml --exit-code 1
  leaks
  exit 0
fi
trivy --scanners vuln --ignore-unfixed --severity HIGH,CRITICAL --exit-code 0
trivy --scanners vuln --ignore-unfixed --severity CRITICAL --exit-code 1 --quiet

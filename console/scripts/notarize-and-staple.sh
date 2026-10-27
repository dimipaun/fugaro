#!/usr/bin/env bash
# SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
# See console/LICENSE.md for the terms.
#
# Notarise one built artifact with Apple, staple the ticket to it, and validate
# the staple.
#
#   console/scripts/notarize-and-staple.sh app "release/mac-universal/Fugaro Console.app"
#   console/scripts/notarize-and-staple.sh dmg "release/Fugaro Console-0.9.0-universal.dmg"
#
# Why this is a script and not four lines in the workflow: the signing workflow
# may not let the packer notarise. electron-builder submits *and staples* the
# .app inside `npm run package` (its `@electron/notarize` step), and that staple
# is one call in the middle of the packer's own process, so a lost connection to
# Apple's ticket-delivery service there cannot be retried on its own -- it fails
# the pack. That is exactly what the October 2026 release runs hit. So the
# workflow packs with the packer's notarisation off (`-c.mac.notarize=false`)
# and submits, staples and validates here instead, where the staple and the
# validate are retried and the submission is not.
#
# **Only `xcrun stapler` is retried here.** The submission is retried never: a
# rejected submission is final, and re-submitting is not this script's business.
# The credential check below, the `ditto` that zips a bundle, the submission's
# result -- `notarytool`'s exit status *and* Apple's own status, both required
# to be good -- and the refusals all fail at once.
#
# Credentials, from the environment; the workflow's signing step exports them,
# and they are the same triple the packer and `notarytool` already read:
#   APPLE_API_KEY      the PATH to the App Store Connect .p8 -- not its contents.
#                      The signing step writes the secret to a private file and
#                      exports the path, which is what `notarytool --key` and
#                      electron-builder both want.
#   APPLE_API_KEY_ID   the key's id
#   APPLE_API_ISSUER   the issuer id
#
# bash 3.2 is what runs this on a macOS runner (/bin/bash), and the workflow
# step that calls it: no `local -n`, no `${var,,}`, no `mapfile`, no associative
# arrays. `retry_apple_network` below is character for character the copy in the
# verification step of docs/pending-workflows/console-release.yml, and
# TestDocsConsoleReleaseWorkflowIsStagedAndPinned holds the two together.
set -euo pipefail

usage() {
  echo "usage: $0 app|dmg <artifact>" >&2
  exit 2
}

if [ "$#" -ne 2 ]; then
  usage
fi
kind="$1"
artifact="$2"

# What is being notarised decides how it is submitted, and a path of the wrong
# kind is a mistake in the caller rather than something to notarise anyway.
case "$kind" in
  app)
    if [ ! -d "$artifact" ]; then
      echo "::error::$artifact is not a bundle: refusing to notarise it"
      exit 1
    fi
    ;;
  dmg)
    if [ ! -f "$artifact" ]; then
      echo "::error::$artifact is not a disk image: refusing to notarise it"
      exit 1
    fi
    ;;
  *)
    usage
    ;;
esac

# Fail closed, before anything reaches Apple: a submission with no credential
# is a submission that silently does not happen. (Named one by one rather than
# through the signing step's `${!name}` loop: this file is not the step, and
# `${!name}` on an unset variable is an unbound-variable error under
# `set -u` in the bash the tests run.)
if [ -z "${APPLE_API_KEY:-}" ] || [ -z "${APPLE_API_KEY_ID:-}" ] || [ -z "${APPLE_API_ISSUER:-}" ]; then
  echo "::error::the App Store Connect key triple (APPLE_API_KEY, APPLE_API_KEY_ID, APPLE_API_ISSUER) is not all in the environment -- refusing to notarise $artifact"
  exit 1
fi

# Apple's ticket delivery is reached over the network by both the staple and its
# validation, and a lost connection there is not a rejection: `xcrun stapler`
# prints a CloudKit/NSURLErrorDomain `-1005 "The network connection was lost"`
# against .../com.apple.gk.ticket-delivery/... and exits non-zero (68), which is
# what the October 2026 release runs hit -- twice, in different processes. So
# each attempt is numbered and its exit code printed, the pause before the next
# grows (15, 30, 60, 90 seconds), and only the fifth failure is returned to the
# refusal below -- a ticket that is genuinely absent still refuses to publish.
# **It wraps the staple and the validate and nothing else**: the submission is
# not retried, and neither is anything around it.
retry_apple_network() {
  local attempt=1 status=0
  while [ "$attempt" -le 5 ]; do
    if "$@"; then
      status=0
    else
      status=$?
    fi
    echo "apple-network attempt $attempt of 5: $* exited $status"
    if [ "$status" -eq 0 ]; then
      return 0
    fi
    if [ "$attempt" -eq 5 ]; then
      return "$status"
    fi
    case "$attempt" in
      1) sleep 15 ;;
      2) sleep 30 ;;
      3) sleep 60 ;;
      4) sleep 90 ;;
    esac
    attempt=$((attempt + 1))
  done
}

work="$(mktemp -d "${TMPDIR:-/tmp}/console-notarise.XXXXXX")"
trap 'rm -rf "$work"' EXIT

# A bundle goes to the notary service zipped -- `ditto`, so the signature and
# the extended attributes survive the round trip -- and a disk image goes as
# itself. `--keepParent` keeps the .app as the archive's single top-level entry.
if [ "$kind" = "app" ]; then
  submission="$work/$(basename "$artifact").zip"
  ditto -c -k --keepParent "$artifact" "$submission"
else
  submission="$artifact"
fi

# The submission, once, waited for, with Apple's answer read as JSON so the
# status can be checked rather than inferred from the exit code -- and with
# **both** checked, because neither alone is the whole answer: `notarytool` can
# exit non-zero having printed an answer (a truncated run, a failure after the
# body was written), and it can exit 0 having printed a status that is not
# "Accepted". Both are required to be good. Anything else is final: there is no
# retry, and no ticket to staple.
submit_status=0
answer="$(xcrun notarytool submit "$submission" --key "$APPLE_API_KEY" --key-id "$APPLE_API_KEY_ID" --issuer "$APPLE_API_ISSUER" --wait --output-format json)" || submit_status=$?
status="$(printf '%s\n' "$answer" | grep -o '"status"[[:space:]]*:[[:space:]]*"[^"]*"' | sed 's/.*"\([^"]*\)"$/\1/')" || status=""
# Apple's answer, on the record whether it was Accepted or not (it names the
# submission's id and status; it holds no credential).
printf '%s\n' "$answer"
if [ "$submit_status" -ne 0 ] || [ "$status" != "Accepted" ]; then
  echo "::error::notarytool exited $submit_status with the notary service answering '${status:-nothing}': not 'Accepted' and a clean exit, and that is final and is not retried -- refusing to staple $artifact"
  exit 1
fi
echo "notarised: $artifact is Accepted by the notary service"

# The ticket, onto the artifact that ships, and then the validation the
# verification step also runs: the staple can succeed while the ticket is
# missing, and a validation is what proves it is there. Both reach Apple, so
# both are retried; a fifth failure of either is the refusal, with today's
# message.
if ! retry_apple_network xcrun stapler staple "$artifact"; then
  echo "::error::$artifact carries no stapled ticket: refusing to publish artifacts that would carry it unstapled"
  exit 1
fi
if ! retry_apple_network xcrun stapler validate "$artifact"; then
  echo "::error::$artifact carries no stapled ticket: refusing to publish artifacts that would carry it unstapled"
  exit 1
fi
echo "stapled and validated: $artifact"

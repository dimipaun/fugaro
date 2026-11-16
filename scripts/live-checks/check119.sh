#!/usr/bin/env bash
# Check 119 — the installer, read as a file: its name, its size, its SHA-256 and
# whether it really is a 64-bit Windows executable.
#
# Check 119 is the owner's manual checklist for the console on Windows
# (docs/console-windows/implementation-plan.md §4, written out in
# docs/gcp-live-checklist.md). Every step of it but this one happens on a real
# Windows machine: a distribution, the console's window, its first-run pages,
# a cancelled `watch`. This is the step that does not — the installer a manual
# dispatch of `console-windows.yml` uploaded is a file, and a file can be read
# anywhere. What it records is what step 1's evidence rests on: which artifact
# the run produced, and that it is the Windows executable the pack claims
# rather than an empty upload, an archive or another platform's build.
#
# It reads and prints. It writes nothing, installs nothing, runs nothing and
# reaches no cloud, no project and no installation; the one file it opens is
# the one you name.
#
# Usage:      bash scripts/live-checks/check119.sh --installer <path>
#             (the `.exe` from the dispatch's `console-windows` artifact; on a
#             Windows machine it is wherever the download landed, which a
#             distribution reaches as /mnt/c/Users/<you>/Downloads/…)
#             bash scripts/live-checks/check119.sh --help
# Needs:      bash, od, sha256sum and head on PATH. Nothing is installed and
#             nothing is fetched; a missing tool stops the script with one line
#             naming it.
# Where:      anywhere. This is the one part of Check 119 with no Windows
#             requirement, no cloud project and no installation: it reads the
#             bytes of one file you point it at.
# Creates:    nothing. No scratch file, no directory, no state.
# Costs:      nothing: one read of one file.
# Runs:       by YOU, and it is the only step of Check 119 that can be run
#             outside Windows — which is why it refuses to start in a coding
#             agent's session (CLAUDECODE, CLAUDE_CODE_ENTRYPOINT,
#             CLAUDE_CODE_REMOTE, CURSOR_AGENT, AI_AGENT): no agent runs any
#             step of Check 119, and this is the step whose output the rest of
#             the check is recorded against.
set -euo pipefail

stop() {
  printf 'Check 119 stopped: %s\n' "$1" >&2
  exit 2
}

usage() {
  cat <<'USAGE'
Check 119 — read the installer a manual dispatch of `console-windows.yml`
produced, as a file.

Usage:
  bash scripts/live-checks/check119.sh --installer <path>
  bash scripts/live-checks/check119.sh --help

It prints, one per line:
  installer-name=<the file's own name>
  installer-bytes=<its size>
  installer-sha256=<its SHA-256>
  installer-arch=x64

and refuses, with one line and exit 2, when the file is missing, empty, not a
regular file, or not a 64-bit Windows executable — a `.exe` the pack produced
is a PE image, and anything else is the artifact a reader would otherwise take
on trust. Nothing is written and nothing is installed.
USAGE
}

need() {
  command -v "$1" >/dev/null 2>&1 || stop "$1 is not on PATH, and this reads the file with it"
}

guard_agent() {
  local marker found=""
  for marker in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "$(eval "printf '%s' \"\${$marker:-}\"")" ]; then
      found="${found}${found:+, }$marker"
    fi
  done
  [ -z "$found" ] ||
    stop "this shell carries $found, so it is a coding agent's session, and no agent runs any step of Check 119. Run it yourself: bash scripts/live-checks/check119.sh --installer <the .exe the dispatch uploaded>"
}

# byte_at reads $3 bytes from $2 of $1, one decimal number per line, so a field
# is read as bytes rather than through the host's own byte order: the header's
# fields are little-endian, and `od`'s multi-byte forms are not.
byte_at() {
  od -An -tu1 -j "$2" -N "$3" "$1" | tr -s ' ' '\n' | grep -v '^$'
}

installer=""
while [ $# -gt 0 ]; do
  case "$1" in
  --help | -h)
    usage
    exit 0
    ;;
  --installer)
    [ $# -ge 2 ] || stop "--installer needs the path of the .exe the dispatch uploaded"
    installer=$2
    shift 2
    ;;
  *)
    stop "unknown argument $1 (usage: check119.sh --installer <path>, or --help)"
    ;;
  esac
done

guard_agent
[ -n "$installer" ] || stop "no --installer was given, and the installer is what this reads (usage: check119.sh --installer <path>, or --help)"
need od
need sha256sum
need head

[ -e "$installer" ] || stop "$installer does not exist: point --installer at the .exe the dispatch's artifact carried"
[ -f "$installer" ] || stop "$installer is not a regular file: the artifact is one .exe, not a directory or a device"
[ -r "$installer" ] || stop "$installer cannot be read"

size=$(wc -c <"$installer" | tr -d ' ')
[ "$size" -gt 0 ] || stop "$installer is empty: an artifact that carries no bytes is an upload that carried nothing"
# The DOS header is 64 bytes and its last field is where the PE header is, so a
# file shorter than that cannot be one whatever else it is.
[ "$size" -ge 64 ] || stop "$installer is $size bytes, which is smaller than a DOS header: it is not a Windows executable"

read -r mz0 mz1 <<<"$(byte_at "$installer" 0 2 | tr '\n' ' ')"
[ "$mz0" = 77 ] && [ "$mz1" = 90 ] ||
  stop "$installer does not begin with MZ, so it is not a Windows executable (the pack's artifact is a PE image)"

read -r b0 b1 b2 b3 <<<"$(byte_at "$installer" 60 4 | tr '\n' ' ')"
pe=$((b0 + b1 * 256 + b2 * 65536 + b3 * 16777216))
[ $((pe + 6)) -le "$size" ] ||
  stop "$installer says its PE header is at byte $pe and the file is $size bytes, so there is no header to read: it is a DOS stub or a truncated download"

read -r s0 s1 z0 z1 m0 m1 <<<"$(byte_at "$installer" "$pe" 6 | tr '\n' ' ')"
[ "$s0" = 80 ] && [ "$s1" = 69 ] && [ "$z0" = 0 ] && [ "$z1" = 0 ] ||
  stop "$installer has no PE signature at byte $pe, so it is a DOS executable or not an executable at all"
machine=$((m0 + m1 * 256))
# 0x8664 is IMAGE_FILE_MACHINE_AMD64, which is what the job's `build.win` block
# asks electron-builder for (`arch: ['x64']`); any other machine is a pack
# nobody asked for, and reading it as this one would be the mistake this line
# exists to prevent.
[ "$machine" = 34404 ] ||
  stop "$installer's PE header names machine $machine, not 34404 (x64), which is the arch the pack's own target names"

printf 'installer-name=%s\n' "$(basename "$installer")"
printf 'installer-bytes=%s\n' "$size"
printf 'installer-sha256=%s\n' "$(sha256sum "$installer" | cut -d' ' -f1)"
printf 'installer-arch=x64\n'

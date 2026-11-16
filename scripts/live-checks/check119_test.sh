#!/usr/bin/env bash
# Tests for scripts/live-checks/check119.sh. NOTHING here is a Windows machine,
# a cloud, a project or an installation: every case builds the bytes it reads in
# a temporary directory and asserts what the script printed and what it refused.
#
# The bytes are the point. Check 119's first step rests on the installer a
# manual dispatch of `console-windows.yml` uploaded, and the one thing that can
# be read about it anywhere is its file: a name, a size, a SHA-256, and a PE
# header that says x64. So the cases that matter are the ones where those bytes
# are bent — a text file, a DOS stub with no PE signature, a 32-bit image, an
# empty upload, a download that stopped half-way — because each of them is an
# artifact a reader would otherwise take on trust.
#
# Usage: bash scripts/live-checks/check119_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check119.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK119_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
}
trap cleanup_all EXIT

failures=0
fail() {
  echo "FAIL: $*" >&2
  failures=$((failures + 1))
}
expect_contains() { # haystack needle what
  case "$1" in
  *"$2"*) ;;
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK119_TEST_HEAD:-900}")" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1"
}

# run119 runs the script with the agent markers out of the environment: this
# suite is itself run inside an agent's session, and the check's own first
# refusal is exactly those markers — the case below sets one back to read it.
# Sets $status, $out and $work/err.
run119() {
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT \
    ${RUN119_ENV[@]+"${RUN119_ENV[@]}"} \
    bash "$script" "$@" 2>"$work/err") || status=$?
  err=$(cat "$work/err")
  RUN119_ENV=()
}

# zeros writes $1 zero bytes to stdout, which is how every case below builds the
# padding between the header fields it cares about.
zeros() { head -c "$1" /dev/zero; }

# make_pe writes a minimal PE image: the DOS header, the offset of the PE header
# it carries at byte 60, the signature, and the machine field. It is not a
# runnable program and does not need to be — what the check reads is the header.
make_pe() { # path machine-le-lo
  local path=$1 lo=$2 hi=$3
  {
    printf 'MZ'
    zeros 58
    printf '\200\000\000\000' # 0x80, where the PE header is
    zeros 64
    printf 'PE\000\000'
    printf "\\$(printf '%03o' "$lo")\\$(printf '%03o' "$hi")"
    zeros 122
  } >"$path"
}

x64_lo=100 x64_hi=134 # 0x8664, little-endian
i386_lo=76 i386_hi=1  # 0x014c

test_help() {
  run119 --help
  expect_status "$status" 0 "check119.sh --help"
  expect_contains "$out" "Usage:" "--help prints the usage"
  expect_contains "$out" "--installer" "--help names the one flag"
}

test_a_real_installer_is_read() {
  make_pe "$work/Fugaro.Console-0.0.0-win-x64.exe" "$x64_lo" "$x64_hi"
  run119 --installer "$work/Fugaro.Console-0.0.0-win-x64.exe"
  expect_status "$status" 0 "a PE x64 image is the artifact the check expects"
  expect_contains "$out" "installer-name=Fugaro.Console-0.0.0-win-x64.exe" "the name is the file's own"
  expect_contains "$out" "installer-bytes=256" "the size is the file's own"
  expect_contains "$out" "installer-arch=x64" "the machine field reads as x64"
  want=$(sha256sum "$work/Fugaro.Console-0.0.0-win-x64.exe" | cut -d' ' -f1)
  expect_contains "$out" "installer-sha256=$want" "the digest is the file's own"
}

test_a_text_file_is_refused() {
  # Longer than a DOS header, so the case reads the magic rather than the size.
  { printf 'this is not an installer'; zeros 100; } >"$work/notes.txt"
  run119 --installer "$work/notes.txt"
  expect_status "$status" 2 "a text file is not a Windows executable"
  expect_contains "$err" "MZ" "the refusal names what is missing"
  expect_contains "$err" "$work/notes.txt" "the refusal names the file"
}

test_a_dos_stub_is_refused() {
  { printf 'MZ'; zeros 254; } >"$work/dos-only.bin"
  run119 --installer "$work/dos-only.bin"
  expect_status "$status" 2 "a DOS stub with no PE header is not the pack's artifact"
  expect_contains "$err" "no PE signature at byte" "the refusal names the missing signature"
}

test_a_truncated_download_is_refused() {
  # The offset says the header is at 0x80 and the file stops before it: this is
  # a download that ended part-way, which is the failure an owner meets when
  # the artifact is fetched rather than carried.
  {
    printf 'MZ'
    zeros 58
    printf '\200\000\000\000'
    zeros 10
  } >"$work/short-download.bin"
  run119 --installer "$work/short-download.bin"
  expect_status "$status" 2 "a file that stops before its own PE header is not an installer"
  expect_contains "$err" "there is no header to read" "the refusal says the download stopped before the header it names"
}

test_a_32_bit_image_is_refused() {
  make_pe "$work/Fugaro.Console-0.0.0-win-ia32.exe" "$i386_lo" "$i386_hi"
  run119 --installer "$work/Fugaro.Console-0.0.0-win-ia32.exe"
  expect_status "$status" 2 "the pack asks for x64, so a 32-bit image is not its artifact"
  expect_contains "$err" "34404" "the refusal names the machine the check wants"
  expect_contains "$err" "332" "the refusal names the machine the file carries"
}

test_an_empty_upload_is_refused() {
  : >"$work/zero-length.bin"
  run119 --installer "$work/zero-length.bin"
  expect_status "$status" 2 "an empty artifact is an upload that carried nothing"
  expect_contains "$err" "carries no bytes" "the refusal says the file is empty"
}

test_a_file_too_short_to_hold_a_dos_header_is_refused() {
  # Long enough to carry the MZ magic and nothing else: the size is the only
  # thing that can say this is not an executable.
  { printf 'MZ'; zeros 8; } >"$work/tiny.bin"
  run119 --installer "$work/tiny.bin"
  expect_status "$status" 2 "a file smaller than a DOS header is not a Windows executable"
  expect_contains "$err" "smaller than a DOS header" "the refusal says the file is too short to carry the header it would be read for"
}

test_a_missing_file_is_refused() {
  run119 --installer "$work/not-there.exe"
  expect_status "$status" 2 "a path with no file is refused"
  expect_contains "$err" "does not exist" "the refusal says the file is missing"
}

test_a_directory_is_refused() {
  mkdir -p "$work/a-directory.exe"
  run119 --installer "$work/a-directory.exe"
  expect_status "$status" 2 "the artifact is one .exe, not a directory"
  expect_contains "$err" "regular file" "the refusal says what the path is"
}

test_no_installer_is_refused() {
  run119
  expect_status "$status" 2 "the installer is the one thing this reads"
  expect_contains "$err" "no --installer" "the refusal says which flag is missing"
  expect_contains "$err" "--help" "the refusal says how to read the usage"
}

test_an_empty_installer_value_is_refused() {
  run119 --installer ""
  expect_status "$status" 2 "an empty path is not a file to read"
}

test_an_unknown_argument_is_refused() {
  run119 --installer "$work/x.exe" --dry-run
  expect_status "$status" 2 "this check has no dry run: it calls nothing and changes nothing"
  expect_contains "$err" "unknown argument --dry-run" "the refusal names the argument"
}

test_an_agent_session_is_refused() {
  make_pe "$work/Fugaro.Console-0.0.0-win-x64.exe" "$x64_lo" "$x64_hi"
  RUN119_ENV=(CLAUDECODE=1)
  run119 --installer "$work/Fugaro.Console-0.0.0-win-x64.exe"
  expect_status "$status" 2 "no agent runs any step of Check 119"
  expect_contains "$err" "CLAUDECODE" "the refusal names the marker it found"
  expect_contains "$err" "coding agent" "the refusal says what the session is"
}

# Sharding, so a Go test could run these side by side: CHECK_TEST_SHARD="i/n"
# runs every n-th test starting at i (each case works in its own files under
# $work), CHECK119_TEST_ONLY=name runs that one alone.
test_index=0
t() {
  local i=$test_index
  test_index=$((test_index + 1))
  if [ -n "${CHECK119_TEST_ONLY:-}" ]; then
    [ "$1" = "$CHECK119_TEST_ONLY" ] || return 0
  elif [ -n "${CHECK_TEST_SHARD:-}" ]; then
    [ $((i % ${CHECK_TEST_SHARD#*/})) -eq "${CHECK_TEST_SHARD%/*}" ] || return 0
  fi
  "$1"
}

t test_help
t test_a_real_installer_is_read
t test_a_text_file_is_refused
t test_a_dos_stub_is_refused
t test_a_truncated_download_is_refused
t test_a_32_bit_image_is_refused
t test_an_empty_upload_is_refused
t test_a_file_too_short_to_hold_a_dos_header_is_refused
t test_a_missing_file_is_refused
t test_a_directory_is_refused
t test_no_installer_is_refused
t test_an_empty_installer_value_is_refused
t test_an_unknown_argument_is_refused
t test_an_agent_session_is_refused

if [ "$failures" -ne 0 ]; then
  echo "check119_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check119_test.sh: ok"

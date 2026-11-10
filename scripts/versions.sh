# shellcheck shell=bash
# Sourced by scripts/release.sh and scripts/verify-release.sh: ordering strict
# X.Y.Z versions without `sort -V`. GNU sort -V is not everywhere (busybox and
# the BSD sorts), and a `sort -V` that refuses its flag does not fail the
# pipeline it sits in -- `git tag -l ... | sort -V | tail -1` still exits 0, with
# nothing on stdout -- so a comparison built that way answers "no version" and
# the caller acts on it: the release gate would read a maintenance branch as an
# older line, and the verifier would skip the moving `<major>` image ref and
# still print that the release is verified. The key below is each component
# zero-padded, so a plain `sort` orders the lines.

# highest_version reads strict X.Y.Z versions, one per line, and prints the
# highest, or nothing when it was given none. A line that is not a strict
# version -- a `-rc` tag, a four-part one, one with a leading zero -- is dropped
# rather than compared, which is what `sort -V | tail -1` would not do: it would
# answer with the line it could not order.
highest_version() {
  awk -F. 'NF == 3 && $1 ~ /^(0|[1-9][0-9]*)$/ && $2 ~ /^(0|[1-9][0-9]*)$/ && $3 ~ /^(0|[1-9][0-9]*)$/ {printf "%020d%020d%020d %s\n", $1, $2, $3, $0}' | sort | tail -1 | awk '{print $2}'
}

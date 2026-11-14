#!/usr/bin/env bash
# Check 460's seed — three `submitted` shared records, one per GitHub issue the
# check reads.
#
# Check 460 is the milestone read on real GitHub (docs/issues-resolved-on-update/
# implementation-plan.md, "Checks"): the owner picks one issue closed as
# completed WITH a milestone, one closed as completed WITHOUT one, and one
# closed as not planned, then types `fugaro update` on a CLI at or above the
# milestone and confirms the first is listed and marked `resolved` with
# `resolved_in` equal to the milestone, the second is untouched, and the third
# becomes `dismissed` with `closed_as`.
#
# The resolver only ever looks at issues the shared store holds as `submitted`,
# and `fugaro issue sync` uploads only this machine's `pending` ones — so there
# is no command that puts a record linked to somebody else's GitHub issue into
# the store, and this script writes those three records itself, in the store's
# own format: `issues/records/<NNNN>` with the record, `issues/fingerprints/
# <sha256>` naming it, and the `issues/counter` the three numbers came from, so
# no later upload can take one of them.
#
# Usage:      bash scripts/check-460-seed.sh --bucket gs://<runs bucket> \
#                 --milestone <issue N: closed as completed, with a milestone> \
#                 --no-milestone <issue N: closed as completed, no milestone> \
#                 --not-planned <issue N: closed as not planned>
#             bash scripts/check-460-seed.sh --help
# Needs:      bash, gcloud (authenticated as an OPERATOR: `issues/` is a
#             launcher's prefix and `fugaro/` is the operator's, and this writes
#             both — the counter beside the records), python3 (building the
#             record's envelope), sha256sum, mktemp, date.
# Where:      your own machine, against the runs bucket of the installation you
#             are about to measure. Nothing is read or written anywhere else.
# Creates:    three records under `issues/` on that bucket, and the `issues/
#             counter` that accounts for their numbers. Nothing else, ever: the
#             script refuses to overwrite a number the store already holds.
# Costs:      nothing. No model call, no run, no GitHub request.
# Runs:       by YOU. No agent runs it, and no test drives it.
# Cleanup:    its scratch directory, removed from the exit trap.
# After it:   `fugaro update` at your own terminal, then `fugaro issue list` to
#             read the three records back; `gcloud storage rm` the three
#             `issues/records/<NNNN>` objects and their fingerprint entries when
#             the check is done, or leave them: they are ordinary issues of the
#             project and nothing but the update reads them.

set -euo pipefail

BUCKET=""
WITH_MILESTONE=""
NO_MILESTONE=""
NOT_PLANNED=""

usage() {
	cat <<'USAGE'
Check 460's seed — three `submitted` shared records, one per GitHub issue the
check reads.

Usage:
  bash scripts/check-460-seed.sh --bucket gs://<runs bucket> \
      --milestone <issue N: closed as completed, with a milestone> \
      --no-milestone <issue N: closed as completed, no milestone> \
      --not-planned <issue N: closed as not planned>

Writes three records under `issues/` on that bucket, in the store's own format,
and the counter that accounts for their numbers. It writes nothing anywhere
else, refuses to overwrite a number the store already holds, and spends nothing:
no model call, no run, no GitHub request. Read this file's header for what Check
460 then measures.
USAGE
}

die() {
	printf 'check 460 seed: %s\n' "$*" >&2
	exit 1
}

while [ $# -gt 0 ]; do
	case "$1" in
	--bucket) BUCKET="${2:-}"; shift 2 ;;
	--bucket=*) BUCKET="${1#*=}"; shift ;;
	--milestone) WITH_MILESTONE="${2:-}"; shift 2 ;;
	--milestone=*) WITH_MILESTONE="${1#*=}"; shift ;;
	--no-milestone) NO_MILESTONE="${2:-}"; shift 2 ;;
	--no-milestone=*) NO_MILESTONE="${1#*=}"; shift ;;
	--not-planned) NOT_PLANNED="${2:-}"; shift 2 ;;
	--not-planned=*) NOT_PLANNED="${1#*=}"; shift ;;
	-h | --help) usage; exit 0 ;;
	*) die "unknown argument $1 (--help prints the usage)" ;;
	esac
done

for want in BUCKET WITH_MILESTONE NO_MILESTONE NOT_PLANNED; do
	[ -n "${!want}" ] || die "--$(printf '%s' "$want" | tr 'A-Z_' 'a-z-') is required (--help prints the usage)"
done
for n in "$WITH_MILESTONE" "$NO_MILESTONE" "$NOT_PLANNED"; do
	case "$n" in
	'' | *[!0-9]*) die "'$n' is not a GitHub issue number" ;;
	esac
done
case "$BUCKET" in
gs://*) ;;
*) die "--bucket wants a gs:// URL, got '$BUCKET'" ;;
esac

for tool in gcloud python3 sha256sum mktemp date; do
	command -v "$tool" >/dev/null 2>&1 || die "$tool is not on PATH"
done

scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT

# The last number the store handed out, 0 when it has handed out none: the
# three seed issues take the numbers after it, so an upload that follows takes
# its own and never one of these.
counter_json=$(gcloud storage cat "$BUCKET/issues/counter" 2>/dev/null || printf '')
last=0
if [ -n "$counter_json" ]; then
	last=$(printf '%s' "$counter_json" | python3 -c 'import json,sys; print(int(json.load(sys.stdin)["number"]))' 2>/dev/null) ||
		die "$BUCKET/issues/counter is not a counter this store wrote ('$counter_json'): fix or remove it, then run this again"
fi

stamp=$(date -u +%Y%m%d%H%M%S)
project="dimipaun/fugaro"

# seed <number the GitHub issue> <summary> writes one record and its two
# companions, and leaves the project number it took in seed_number. It is not
# run in a subshell: the number it takes is the number the next one starts
# after.
seed_number=0
seed() {
	github="$1"
	summary="$2"
	number=$((last + 1))
	last="$number"
	seed_number="$number"
	key=$(printf '%s/issues/records/%04d' "$BUCKET" "$number")

	if gcloud storage ls "$key" >/dev/null 2>&1; then
		die "$key exists: this store already holds issue $number, so the counter ($last) and the records disagree — fix that by hand rather than by overwriting an issue"
	fi

	fingerprint=$(printf 'check-460:%s' "$github" | sha256sum | cut -d' ' -f1)
	run="check460-${stamp}-$$"

	cat >"$scratch/record" <<EOF
---
version: 1
number: $number
local_id: local-$run
run: $run
reporter: check-460-seed
created: $(date -u +%Y-%m-%dT%H:%M:%SZ)
failed_command: fugaro run "check 460"
class: fugaro
fugaro_version: check-460
os: linux
installation: ""
status: submitted
closed_as: ""
url: https://github.com/$project/issues/$github
uploaded: true
fingerprint: $fingerprint
resolved_in: ""
regression: false
original_url: ""
notified: false
submitted_at: $(date -u +%Y-%m-%dT%H:%M:%SZ)
---

## Summary

\`\`\`
$summary
\`\`\`

## Diagnosis

\`\`\`
Seeded by scripts/check-460-seed.sh for Check 460. It is not a diagnosis: the
record exists so the resolve pass has a \`submitted\` issue whose GitHub issue is
$project#$github.
\`\`\`

## Steps

\`\`\`
None.
\`\`\`

## Log

\`\`\`
(no log: seeded, not diagnosed)
\`\`\`
EOF

	python3 - "$scratch/record" >"$scratch/envelope" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as fh:
    record = fh.read()
print(json.dumps({"format": 1, "record": record}))
PY

	printf '{"format":1,"number":%d}' "$number" >"$scratch/fingerprint"

	# The record first, then the entry that names it: a reader that meets the
	# record before its entry cannot see it, while an entry with no record is
	# a number the store has handed out and nothing else.
	gcloud storage cp "$scratch/envelope" "$key" >/dev/null
	gcloud storage cp "$scratch/fingerprint" "$BUCKET/issues/fingerprints/$fingerprint" >/dev/null
}

seed "$WITH_MILESTONE" "Check 460: closed as completed, with a milestone"
first="$seed_number"
seed "$NO_MILESTONE" "Check 460: closed as completed, with no milestone"
second="$seed_number"
seed "$NOT_PLANNED" "Check 460: closed as not planned"
third="$seed_number"

printf '{"format":1,"number":%d}\n' "$last" >"$scratch/counter"
gcloud storage cp "$scratch/counter" "$BUCKET/issues/counter" >/dev/null

printf '\nseeded three submitted issues on %s:\n' "$BUCKET"
printf '  #%04d -> https://github.com/%s/issues/%s  (closed as completed, with a milestone)\n' "$first" "$project" "$WITH_MILESTONE"
printf '  #%04d -> https://github.com/%s/issues/%s  (closed as completed, no milestone)\n' "$second" "$project" "$NO_MILESTONE"
printf '  #%04d -> https://github.com/%s/issues/%s  (closed as not planned)\n' "$third" "$project" "$NOT_PLANNED"
cat <<EOF

Now, at your own terminal, on a CLI at or above the first issue's milestone:

  fugaro update

and confirm: the first is listed under "This update fixes" and comes back
\`resolved\` with \`resolved_in\` equal to its milestone, the second is untouched,
and the third is \`dismissed\` with \`closed_as\` set. \`fugaro issue list\` prints
all three afterwards, and the update's --json result carries the first as
\`resolved_issues\`.
EOF

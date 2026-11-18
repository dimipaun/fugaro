#!/usr/bin/env bash
# scripts/release-rehearsal.sh — the pre-tag rehearsal of this repository's own
# catalog entry, and the gate that reads the record it writes.
#
# 0.26.0's catalog build failed for the owner on three things no hermetic test
# sees: a stale pinned branch, the leak scan's false positives on a real image,
# and a layer-count refusal. The tag was pushed by then, so the build that found
# them was `fugaro update`'s, on the owner's own installation, and the release
# that shipped was one nobody had built. This is that same build, for real, at a
# point where a red verdict still stops the release.
#
#   scripts/release-rehearsal.sh run <sha>
#     builds this repository's own `go` catalog entry (images/catalog/go/
#     Dockerfile, built by the project image catalog — docs/project-image-catalog)
#     with the CLI built from <sha>, in the SANDBOX project only, and writes the
#     record: the commit, the Cloud Build id, the verdict, the build's log. The
#     build is the CLI's own `fugaro image build go`, so it runs every step a
#     release depends on — the clone at the commit, the build, the leak scan, the
#     smoke, the promotion and the record — and nothing here repeats any of them.
#     Exit 0 when it passed, 1 when it did not (or could not run), naming the
#     Cloud Build to read.
#
#   scripts/release-rehearsal.sh check <sha>
#     reads the record and refuses a missing one, a red one, or one written for
#     another commit. This is the release gate: scripts/release.sh runs it before
#     the tag, so the tag is cut on a build that really happened for the commit
#     being tagged. It reads a file and touches nothing, so it runs in any
#     session, a coding agent's included.
#
# The record is one file for the whole machine, replaced by every rehearsal:
# $XDG_STATE_HOME/fugaro/release-rehearsal/record (RELEASE_REHEARSAL_DIR moves
# it). One record and not one per commit on purpose — a record that is not this
# commit's is a REHEARSAL THAT HAS NOT RUN for it, and the refusal says so
# rather than reading as a green one.
#
# Where, and why it is the sandbox: FUGARO_LIVE_GCP_PROJECT names the live-test
# project the register's owner-run checks use (docs/gcp-live-checklist.md, and
# scripts/live-checks/*.sh the same way): a name that carries `belong` is refused
# before anything is run, and it is passed on as the CLI's own --gcp-project, so
# a config in force that names another project is refused by the CLI as well and
# no project but that one can be built in. The checkout must be THIS repository,
# at the commit being rehearsed: the entry's Dockerfile and the CLI are both read
# off the tree.
#
# Costs: one billable Cloud Build (the catalog build of one entry: the clone, the
# build, the smoke, the scan and the promotion), and the CLI's own budget lease
# judges it against the project's caps before it is submitted, as it does for
# every `fugaro image build`. Nothing else is spent: no run, no model call.
# Runs: by YOU, at your own terminal. The build of a catalog entry is a project
# admin's, so the CLI's managed gate refuses a coding agent's session outright —
# and the rehearsal is where that matters, so this file never works around it.
# Needs: bash, git, mktemp, sed, grep, cut, tr, head, go (the CLI is built from
# the commit), and a checkout of this repository at that commit.
# Env: FUGARO_LIVE_GCP_PROJECT the sandbox GCP project (required for `run`),
# RELEASE_REHEARSAL_DIR the record's directory, RELEASE_REHEARSAL_FUGARO the CLI
# to run instead of building one (the tests, whose fixture checkout holds no go
# module — the same seam RELEASE_SH_STREAMS_CMD is).
set -eu

prog=${0##*/}
# This repository's own entry, and the only one this rehearses: the point is that
# THIS image is the one a release ships, so a name taken from an argument could
# rehearse somebody else's.
entry=go
record_dir=${RELEASE_REHEARSAL_DIR:-${XDG_STATE_HOME:-$HOME/.local/state}/fugaro/release-rehearsal}
record=$record_dir/record

die() {
  printf '%s: %s\n' "$prog" "$*" >&2
  exit 1
}

usage() {
  cat >&2 <<EOF
usage: $prog run <sha>     # build this repository's own $entry catalog entry for <sha>, write the record
       $prog check <sha>   # refuse a missing, red, or other-commit record for <sha>
EOF
}

# field NAME prints the record's value for NAME, or nothing.
field() {
  sed -n "s/^$1=//p" "$record" 2>/dev/null | head -n1
}

# write_record VERDICT BUILD LOG replaces the record: written beside its own
# path and moved into place, so a reader never sees half of one.
write_record() {
  mkdir -p "$record_dir"
  tmp=$record.$$
  {
    printf 'commit=%s\n' "$sha"
    printf 'build=%s\n' "$2"
    printf 'verdict=%s\n' "$1"
    printf 'log=%s\n' "$3"
  } >"$tmp"
  mv "$tmp" "$record"
}

# guard_project refuses unless FUGARO_LIVE_GCP_PROJECT names the sandbox, and
# sets $project to it. The rule is the register's own (docs/gcp-live-checklist.md,
# "Guardrails the tests enforce"): the checks name the live-test project and
# never decide for themselves which project is one, and the owner's own project
# is never it.
guard_project() {
  project=${FUGARO_LIVE_GCP_PROJECT:-}
  if [ -z "$project" ]; then
    die "FUGARO_LIVE_GCP_PROJECT is not set, and this rehearses in the sandbox project only: it names the live-test project the owner-run checks build in (docs/gcp-live-checklist.md, Preconditions). Export it (export FUGARO_LIVE_GCP_PROJECT=<the sandbox GCP project>) and run again"
  fi
  case "$(printf '%s' "$project" | tr '[:upper:]' '[:lower:]')" in
  *belong*)
    die "FUGARO_LIVE_GCP_PROJECT names '$project', which carries 'belong': the owner's own installation is never the sandbox a rehearsal runs in"
    ;;
  esac
}

# check_sha SHA refuses a record this commit's release may not proceed on.
check_sha() {
  sha=$1
  if [ ! -f "$record" ]; then
    die "there is no rehearsal record at $record: the tag waits on a real build of this repository's $entry catalog entry for $sha, and no build of it has been recorded. Run $prog run $sha"
  fi
  got=$(field commit)
  if [ "$got" != "$sha" ]; then
    die "the rehearsal record at $record is for $got, not $sha: a build of $got vouches for nothing about the commit being tagged. Run $prog run $sha"
  fi
  verdict=$(field verdict)
  if [ "$verdict" != pass ]; then
    die "the rehearsal of $sha failed: the build did not produce the $entry catalog entry (Cloud Build build $(field build); log: $(field log)). Fix what the log shows, then run $prog run $sha again — the tag is not cut on a red rehearsal"
  fi
  echo "the rehearsal of $sha passed: the $entry catalog entry built for real (Cloud Build build $(field build), $(field log))"
}

# run_sha SHA builds the entry for SHA and records what happened.
run_sha() {
  sha=$1
  guard_project
  command -v git >/dev/null 2>&1 || die "git is not on PATH; this rehearses the checkout's commit and reads nothing else"
  root=$(git rev-parse --show-toplevel 2>/dev/null) \
    || die "this is not a git checkout, and the entry and the CLI are both read from the tree of the commit being rehearsed: run it from a checkout of this repository"
  head=$(git -C "$root" rev-parse HEAD)
  if [ "$head" != "$sha" ]; then
    die "$root is at $head, not $sha: the rehearsal builds the $entry catalog entry and the CLI from the tree it runs in, so it has to run on the commit it records. Check the commit out first (git -C $root checkout $sha) and run again"
  fi
  if [ -n "$(git -C "$root" status --porcelain)" ]; then
    die "$root has uncommitted changes, so the CLI this builds is not $sha: commit or stash them, then run again"
  fi
  if [ ! -f "$root/images/catalog/$entry/Dockerfile" ]; then
    die "$root has no images/catalog/$entry/Dockerfile: this rehearses THIS repository's own catalog entry, and a checkout without it is not this repository's"
  fi
  # A green record for this very commit is the whole of what the tag needs, so a
  # re-run (release.sh resumed after an interruption) rebuilds nothing: the
  # build is billable and it already happened for this commit.
  if [ -f "$record" ] && [ "$(field commit)" = "$sha" ] && [ "$(field verdict)" = pass ]; then
    printf '%s: the rehearsal of %s already passed (Cloud Build build %s, %s); not building it again\n' "$prog" "$sha" "$(field build)" "$(field log)"
    return 0
  fi

  scratch=$(mktemp -d)
  trap 'rm -rf "$scratch"' EXIT
  if [ -n "${RELEASE_REHEARSAL_FUGARO:-}" ]; then
    fugaro=$RELEASE_REHEARSAL_FUGARO
  else
    fugaro=$scratch/fugaro
    ( cd "$root" && go build -o "$fugaro" ./cmd/fugaro ) \
      || die "building the CLI from $sha failed (go build ./cmd/fugaro in $root): the build has to run the CLI of the commit being rehearsed, and one built from another tree vouches for another commit"
  fi

  # The build runs in $root with this session's stdin: the CLI reads a terminal
  # there to know the owner is the one building, and it refuses a coding agent's
  # session with the sentence that says so, which is the refusal this rehearsal
  # has to keep rather than route around. --gcp-project is the sandbox name:
  # a config in force that names another GCP project is refused by the CLI.
  status=0
  "$fugaro" image build "$entry" --gcp-project "$project" >"$scratch/out" 2>&1 || status=$?
  cat "$scratch/out"

  build=$(sed -n 's/.*Cloud Build build \([^ ;]*\).*/\1/p' "$scratch/out" | head -n1)
  log=$(sed -n 's/.*log: \(http[^ ]*\).*/\1/p' "$scratch/out" | head -n1)
  if [ -z "$build" ]; then
    # Nothing was submitted — a refusal (the caps, the gate, a config that names
    # another project) says so above — so there is nothing to record: a record is
    # about a build that ran, and a missing one is what the gate reads.
    die "the rehearsal of $sha built nothing: the CLI above refused before it submitted a Cloud Build, so no record was written and the release refuses until a build of $sha has run. Fix what it printed, then run $prog run $sha"
  fi
  [ -n "$log" ] || log="https://console.cloud.google.com/cloud-build/builds/$build?project=$project"
  if [ "$status" != 0 ]; then
    write_record fail "$build" "$log"
    die "FAIL: Cloud Build build $build did not build the $entry catalog entry for $sha. Its log is $log (record: $record); the tag is not cut on a red rehearsal"
  fi
  write_record pass "$build" "$log"
  printf '%s: PASS: the %s catalog entry built for %s (Cloud Build build %s, %s)\n' "$prog" "$entry" "$sha" "$build" "$log"
  printf 'record: %s\n' "$record"
}

command=${1:-}
sha=${2:-}
case $command in
run | check)
  [ -n "$sha" ] || { usage; die "$command needs the commit sha being rehearsed (e.g. $prog $command \$(git rev-parse HEAD))"; }
  [ "$#" -le 2 ] || { usage; die "$command takes the commit sha and nothing else (it was given $# arguments)"; }
  ;;
*)
  usage
  [ -n "$command" ] || die "no command: it is run or check"
  die "unknown command '$command': it is run or check"
  ;;
esac

case $command in
run) run_sha "$sha" ;;
check) check_sha "$sha" ;;
esac

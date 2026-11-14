#!/usr/bin/env bash
# Check C0 — the console-coordinator page's reads, timed and sized, as ONE
# script YOU run.
#
# It is the profiling task **C0** of
# docs/console-coordinator-v2/implementation-plan.md. That page's project screen
# is built from the reads spec.md §2.2 lists, and every size and time the spec
# prints for them is a READING rather than an estimate: where the design has
# none, the cell says **`(unmeasured)`**, and this script is what fills it. It
# runs each read against an installation YOU name, prints a table of bytes and
# seconds per read, and prints one `CELL` line per read in the wording those
# cells need — so the numbers are copied rather than worked out, and the spec's
# cells are edited by you and never by a script.
#
# What it times and sizes, each exactly as the console spawns it:
#
#   * `fugaro runs ls --json --since 24h`, `--since 7d` and `--since 0` — the
#     same listing at three windows, which is what the page's cost is a function
#     of;
#   * `fugaro runs ls --json --since 24h --limit 50` AND `... --limit 20` — the
#     same window with a page asked for: `--limit` shortens the listing and not
#     the read, and the pair of numbers is what says so. **Both limits are
#     measured because the plan and the spec do not name the same one**: C0's
#     own row says `--limit 50` (D818's first page, spec §2.6), while the
#     `(unmeasured)` size cell in spec §2.2 — the halted-or-failed-job item —
#     reads `--limit 20` (D810's bounded read). Whichever of the two the owner
#     settles on, the number for it is in the table, and the drift itself is
#     reported rather than decided here;
#   * `fugaro coordinator status --from-store --all --json` — ONCE PER
#     INSTALLATION YOU NAME, because that document's size is a function of the
#     repository count (the spec's `~724 KB` was read on a four-repository
#     installation). Name two installations whose configs list different
#     numbers of repositories and the table carries two points on that curve;
#   * `fugaro plan status --json --all` — once per checkout you name, run FROM
#     that checkout, because the read walks `docs/` there;
#   * `fugaro budget show --json`.
#
# The installation is selected the way every other command selects one:
# `FUGARO_PROJECT=<name>` in the read's own environment, so the argv stays the
# console's own words. The runs, coordinator and budget reads are made from this
# check's scratch directory, which is outside every checkout — the console's own
# working directory is the app's, empty — so no checkout's `fugaro.yaml` changes
# which installation answers. The plan read is the one that has to run inside a
# checkout.
#
# READ-ONLY, and it cannot be anything else. Every command below is a read: a
# listing of the runs bucket, the stored view out of the project's database, the
# plan walk over a checkout, and the budget document. It creates nothing, writes
# nothing, launches no run, mints no credential and changes no cloud state, so
# there is nothing for a teardown to put back — the `restore` row says exactly
# that, and the only thing removed is this check's own scratch directory.
# Because no write can happen, refusing the installation named `belong` is NOT a
# write guard: the refusal is kept because every check script in this directory
# refuses that name, and the numbers this check takes are meant to come from an
# installation you name for the purpose.
#
# Usage:      bash scripts/live-checks/check-c0.sh --project <name> [--project <name>...]
#             [--checkout <path>]... [--dry-run]
#             --project names a Fugaro installation the runs, coordinator and
#             budget reads are made against. It may be repeated, and the
#             coordinator read is made once per name: that is how a
#             four-repository installation's size and a smaller one's are read
#             in one run. It is required, and it is never picked for you.
#             --checkout names a repository checkout whose `plan status` is
#             timed; it may be repeated. Without it the current directory is
#             used when it is a checkout (it has a `docs/`), and the plan row
#             says so when it is not.
#             `--dry-run` prints every command a real run would make and runs
#             nothing at all: no fugaro command, no cloud call and no verdict.
# Needs:      bash, mktemp, head, sed, python3 (the reader that counts what a
#             document holds and prints no document) and fugaro on PATH — the
#             build you have installed, whose numbers these are. Nothing is
#             installed and nothing is fetched; if one of those is missing the
#             script stops with one line naming it.
# Where:      your own machine, on an installation you name. A name carrying
#             `belong` is refused before any read is made, in `--project` and in
#             `--checkout` alike.
# Creates:    nothing in the cloud and nothing on disk that outlives the run:
#             one scratch directory, mode 0700, removed at the end. The runs
#             listing may refresh the CLI's own local run cache, which is the
#             read the console makes too.
# Costs:      nothing billable and no model call. `plan status` is the one read
#             that is not a single call: it walks the checkout and asks the
#             forge about the pull requests its tasks carry — one `gh` per
#             joined pull request, at most 32 (`planMaxLookups`).
# Runs:       by YOU. No agent runs any step of it, and its own test
#             (check-c0_test.sh) drives it against a fake `fugaro` on PATH only:
#             no network, no cloud project, no credential and no live step.
# Cleanup:    the scratch directory, removed from the exit trap even on a failed
#             read or a Ctrl-C. Nothing else was created, so there is nothing
#             else to undo.
# Output:     progress on stderr, ONE block on stdout headed CHECK C0 RESULT:
#             a FACT row per read (PASS when it answered with the shape this
#             check reads, FAIL when it did not), the readings table — bytes,
#             human size, seconds, and what the read held — and one CELL line
#             per read to paste into spec.md's cells. Exit 0 when every read
#             answered; 1 when every read answered and one answered with a
#             document that is not the shape this check reads — a finding about
#             the build, with every number in place; and 2 when a read did not
#             answer or a guard stopped the run, which is the run saying it
#             could not tell, not a number. A run that could not measure a read
#             ends 2 even if another read also came back the wrong shape: the
#             table has a hole in it, and a hole is settled before a finding is
#             read.
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: the documents this
# check reads carry pull request titles and branch names other people wrote, and
# a trace of the reads would print them in full into the log you would then
# paste back. The trace is turned off here, BASH_XTRACEFD is unset so a trace
# redirected to a file this script knows nothing about cannot be reopened, and
# guard_xtrace refuses a shell that arrived tracing. Check 149 spells the three
# parts out; this is the same guard.
XTRACE_AT_START=no
# Each source on its own: BASHOPTS carries names like extquote, and a single
# pattern over the three concatenated would match that 'x' and refuse every run.
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
case "${SHELLOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
case "${BASHOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null
unset BASH_XTRACEFD 2>/dev/null || true

# The owner's own stdout and stderr, kept on 3 and 4: a trap runs with whatever
# redirection is in effect when the signal arrives, so the RESULT block is
# written to the saved descriptors rather than to whatever fd 1 is doing then.
exec 3>&1 4>&2

PROG=${0##*/}
USAGE="check-c0.sh --project <name> [--project <name>...] [--checkout <path>...] [--dry-run]"
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
owner() { printf '%s: %s\n' "$PROG" "$*" >&4; }
die() { say "$*"; exit 2; }

# stop REASON: records a FAILed fact and ends the run. The EXIT trap prints the
# block and cleans up, so a stop still gives you something to paste back. Every
# stop here is a guard that could not tell — a shell that is tracing, an
# installation named `belong`, a name that is not one — and never a finding
# about the build, so it ends 2.
stop() {
  fact aborted FAIL "$1"
  owner "stopped: $1"
  exit 2
}

FACTS_FILE=""
fact() { # fact KEY PASS|FAIL|INFO "detail"
  [ -n "$FACTS_FILE" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS_FILE"
}

# unmeasured: a row this run could NOT measure, for a reason that is not a
# finding about the build — a read that did not answer, a checkout that is not
# there. The row reads FAIL in the block (it is not a pass) and the run ends as
# one that could not tell (exit 2), because a table with a hole in it is not the
# table the spec's cells are filled from.
UNMEASURED_KEYS=""
unmeasured() { # unmeasured KEY "detail"
  fact "$1" FAIL "$2"
  UNMEASURED_KEYS="${UNMEASURED_KEYS:+$UNMEASURED_KEYS }$1"
}

on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  say "an internal error ended this check at line $line"
  exit 2
}

need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check C0 needs bash"
  local t
  for t in mktemp head sed python3; do
    command -v "$t" >/dev/null 2>&1 || die "Check C0 needs '$t' on PATH and it is not there"
  done
  command -v fugaro >/dev/null 2>&1 || die "Check C0 needs 'fugaro' on PATH: the numbers are the build you have installed, and it is not there"
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk-c0-mine"
SCRATCH_REMOVED=no
NEUTRAL_CWD=""
FACTS_CAPTURED=""
READINGS_CAPTURED=""
READINGS_FILE=""
KEEP_SCRATCH=${CHECK_C0_KEEP_SCRATCH:-}

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk-c0.XXXXXXXX") || die "Check C0 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  READINGS_FILE="$SCRATCH/readings"
  : >"$FACTS_FILE"
  : >"$READINGS_FILE"
  # The reads that do not need a checkout are made from here: it is outside
  # every git work tree (the check below), so the project selection is the one
  # FUGARO_PROJECT names rather than whatever fugaro.yaml the caller stood in.
  NEUTRAL_CWD="$SCRATCH/run"
  mkdir -p "$NEUTRAL_CWD"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check C0 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident — set TMPDIR outside every repository and run again"
  fi
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

remove_scratch() {
  case "$SCRATCH" in
  "") return 0 ;;
  / | "${HOME:-}" | "${HOME:-}"/)
    say "refusing to remove a scratch directory of '$SCRATCH'"
    return 0
    ;;
  esac
  if [ ! -f "$SCRATCH/$SCRATCH_MARKER" ]; then
    say "refusing to remove $SCRATCH: it carries no marker from this check"
    return 0
  fi
  if [ -n "$KEEP_SCRATCH" ]; then
    say "CHECK_C0_KEEP_SCRATCH is set: $SCRATCH is kept"
    return 0
  fi
  # Guarded, and its outcome recorded rather than assumed: this runs from the
  # exit trap, where a failing `rm` under `set -e` would end the shell before
  # print_block and lose the whole block.
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# cleanup runs IN THIS SHELL, never in a subshell: the captured facts and
# readings are what print_block is written from, and a `$(cleanup)` would throw
# them away with the subshell.
cleanup() {
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  READINGS_CAPTURED=$(cat "$READINGS_FILE" 2>/dev/null || true)
  remove_scratch
}

finish() {
  local status=$?
  trap - EXIT ERR
  cleanup
  local facts=$FACTS_CAPTURED
  if [ -z "$facts" ]; then facts="aborted|FAIL|the run stopped before it recorded anything"; fi
  local restore=PASS
  if [ -z "$SCRATCH" ]; then
    facts="${facts}"$'\n'"restore|PASS|nothing was created: the run stopped before it made a scratch directory, so there is nothing to remove and no cloud state of this check's to undo"
  else
    [ "$SCRATCH_REMOVED" = yes ] || restore=FAIL
    facts="${facts}"$'\n'"restore|$restore|this check changed no cloud state and could not: every command it ran is a read (the runs listing, the stored view, the plan walk and the budget document), and it wrote nothing, launched nothing and minted no credential. The scratch directory $([ "$SCRATCH_REMOVED" = yes ] && echo "is gone" || echo "is STILL THERE, at $SCRATCH — remove it yourself")"
  fi
  local key verdict failed=no
  while IFS='|' read -r key verdict _; do
    [ "$verdict" = FAIL ] || continue
    case " ${UNMEASURED_KEYS:-} " in *" $key "*) continue ;; esac
    failed=yes
  done <<<"$facts"
  # A run that could not measure a row ends 2 whatever else it found: the table
  # it printed is short a read, so it is not the table the spec's cells are
  # filled from, and a hole is settled before a finding is read. 1 is for the
  # run that measured every read and found one that answered with a document
  # this check does not read — a finding about the build, with every number in
  # place.
  if [ -n "$UNMEASURED_KEYS" ]; then
    if [ "$status" = 0 ] || [ "$status" = 1 ]; then status=2; fi
    say "this run could NOT measure ${UNMEASURED_KEYS// /, }: read the FAIL row(s) above, settle it, and run the check again"
  elif [ "$failed" = yes ] && [ "$status" = 0 ]; then
    status=1
  fi
  print_block "$facts" "$status"
  exit "$status"
}

# ------------------------------------------------------------------- the reads
# read_do is the timed half: it runs the read in CWD with its own output kept in
# the scratch directory. It is a function so that the `time` keyword in read_run
# can measure it while the command's own stderr goes to its own file — the
# timing output and the read's stderr are then never mixed.
read_do() { # read_do CWD KEY CMD...
  local cwd=$1 key=$2
  shift 2
  # The subshell is what keeps the cd to itself: read_do is a function, so a
  # plain cd would move the script itself and a relative --checkout would then
  # resolve against the wrong directory. The redirections are the subshell's own
  # command line, so the `time` in read_run still writes its number where
  # read_run points it.
  (cd "$cwd" && exec "$@") >"$SCRATCH/$key.out" 2>"$SCRATCH/$key.err"
}

# read_run KEY DISPLAY CWD CMD... — runs one read, and leaves its outcome in
# READ_STATUS, READ_BYTES, READ_SECONDS and READ_DISPLAY. The seconds are bash's
# own `time` (TIMEFORMAT %R), not GNU date: this check runs on your machine, and
# macOS's `date` has no sub-second format. A read that hangs is ended with a
# Ctrl-C, which the signal trap turns into the same block and the same cleanup
# as any other ending.
read_run() { # read_run KEY DISPLAY CWD CMD...
  local key=$1 display=$2 cwd=$3
  shift 3
  READ_STATUS=0
  TIMEFORMAT='%R'
  { time read_do "$cwd" "$key" "$@"; } 2>"$SCRATCH/$key.time" || READ_STATUS=$?
  READ_SECONDS=$(head -n 1 "$SCRATCH/$key.time" 2>/dev/null || true)
  # A cwd that is not there answers with bash's own message instead of a number:
  # that is not a reading, so it is dropped rather than printed as seconds.
  case "$READ_SECONDS" in '' | *[!0-9.]*) READ_SECONDS="" ;; esac
  READ_BYTES=0
  if [ -f "$SCRATCH/$key.out" ]; then
    READ_BYTES=$(wc -c <"$SCRATCH/$key.out" | tr -d ' ')
  fi
  READ_DISPLAY=$display
  return 0
}

# reader SUBCOMMAND ARGS — the python3 reader. It prints a fixed vocabulary and
# never a document: counts, and a key path with a type when a document is not
# the shape this check reads.
reader() { python3 "$SCRATCH/reader.py" "$@"; }

field_of() { # field_of NAME TEXT — the value of a NAME= line the reader printed
  printf '%s\n' "$2" | sed -n "s/^$1=//p" | head -n 1
}

# scan_kinds FILES... — the KINDS of credential-shaped text the reader found, or
# `none`. The text itself is never printed: a match can come from a pull request
# title somebody else wrote.
scan_kinds() {
  local verdict
  verdict=$(reader scan "$@" 2>/dev/null || printf 'creds=unreadable\n')
  field_of creds "$verdict"
}

# stderr_excerpt KEY — a failed read's own message, bounded and shown only when
# it carries nothing credential-shaped. It is never parsed: a line of fugaro's
# own progress is not a fact about the read.
stderr_excerpt() { # stderr_excerpt KEY
  local key=$1 kinds
  [ -f "$SCRATCH/$key.err" ] || return 0
  kinds=$(scan_kinds "$SCRATCH/$key.out" "$SCRATCH/$key.err")
  case "$kinds" in
  none) head -n 3 "$SCRATCH/$key.err" | tr '\n' ' ' || true ;;
  *)
    printf 'NOT PRINTED HERE: what it said carries something credential-shaped (%s), and this check never echoes one — run the read yourself and read what it says' "${kinds:-unreadable}"
    ;;
  esac
}

record_reading() { # record_reading KEY DISPLAY WHAT
  printf '%s|%s|%s|%s|%s\n' "$1" "$2" "$READ_BYTES" "${READ_SECONDS:-0}" "$3" >>"$READINGS_FILE"
}

# finish_read KEY LABEL KIND — turns the read read_run just made into a row and a
# readings line. A read that did not answer is `unmeasured` (the run could not
# tell); a document that answered and is not the shape this check reads is a
# FAIL, and its size and time are recorded anyway, because the bytes are the
# bytes whatever they hold.
finish_read() { # finish_read KEY LABEL KIND
  local key=$1 label=$2 kind=$3
  if [ "$READ_STATUS" -ne 0 ]; then
    unmeasured "$key" "$label did not answer (fugaro exited $READ_STATUS), so this run has no number for it — the table below is short one read. What it said: $(stderr_excerpt "$key")"
    return 0
  fi
  local answer why problem what
  if ! answer=$(reader "$kind" "$SCRATCH/$key.out" 2>"$SCRATCH/$key.readerr"); then
    why=$(head -n 1 "$SCRATCH/$key.readerr" 2>/dev/null || true)
    record_reading "$key" "$READ_DISPLAY" "not parsed (${why:-no reason given})"
    fact "$key" FAIL "$label answered, and what it printed is not a document this check can read: ${why:-the reader gave no reason}. The output is never echoed here — it may be a refusal of the command rather than a document, and the reader says which by the line it could not read"
    return 0
  fi
  problem=$(field_of problem "$answer")
  what=$(field_of what "$answer")
  record_reading "$key" "$READ_DISPLAY" "${what:-not counted}${problem:+ — not the shape this check reads: $problem}"
  if [ -n "$problem" ]; then
    fact "$key" FAIL "$label answered with a document that is not the shape this check reads: $problem — named by a key path and a type, never by a value out of the document. Its size and time are still in the table, and a number read off a refusal is not the page's cost"
    return 0
  fi
  fact "$key" PASS "$label — $READ_BYTES bytes in ${READ_SECONDS:-?} s, holding $what"
}

# ------------------------------------------------------------------ the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    fact xtrace FAIL "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS), which would print the documents this check reads — pull request titles and branch names other people wrote — into the log you would then paste"
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check-c0.sh --project <name> — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run carries a document it read"
}

# guard_projects: the installation named `belong` is the owner's own, and every
# check script here refuses it. This one is READ-ONLY, so the refusal is not a
# write guard — it is the family's rule, and the numbers are meant to come from
# an installation you name for the purpose. The test is the lowercased
# substring, so `fugaro-belong` and `belong-dev` are refused with `belong`
# itself, and the name is refused before any read is made. The name is also held
# to the characters a project name is made of, because it becomes a fact key.
guard_projects() {
  local name lower
  for name in "${PROJECTS[@]}"; do
    lower=$(printf '%s' "$name" | tr '[:upper:]' '[:lower:]')
    case "$lower" in
    *belong*)
      fact belong FAIL "the installation named is $name, which names the owner's own one: no check script here runs against it, and this check never decides for itself that a name is safe"
      stop "Check C0 never runs against 'belong'. Name an installation you mean to profile: bash scripts/live-checks/check-c0.sh --project <name>"
      ;;
    esac
    case "$name" in
    *[!A-Za-z0-9._-]*)
      stop "'$name' is not a project name this check can carry: a Fugaro project name is letters, digits, dot, dash and underscore, and the name is printed as a fact key"
      ;;
    esac
  done
  fact belong PASS "no installation named carries 'belong': the owner's own one is refused before any read is made, and the numbers this check takes come from the name(s) you chose"
  fact project PASS "$(printf '%s ' "${PROJECTS[@]}")— the installation(s) the runs, coordinator and budget reads are made against, selected with FUGARO_PROJECT=<name> so the argv stays the console's own words"
}

# guard_checkouts: a checkout named is the directory the plan read walks, so it
# has to be one — a directory with a `docs/` in it, which is what `plan status`
# reads. A path that is not one is recorded rather than stopping the run: the
# other reads still measure, and the plan row says the run could not tell.
CHECKOUT_BAD=()
guard_checkouts() {
  local i path bad="" good=""
  # `"${!CHECKOUTS[@]}"` on an array that is still empty is an unbound variable
  # under `set -u` before bash 4.4, so the walk is behind the count.
  if [ "${#CHECKOUTS[@]}" -eq 0 ]; then
    unmeasured checkout "no checkout was named and the current directory is not one: the plan read walks \`docs/\` in the directory it is run from, so there is nothing to time. Pass --checkout <path> to the checkout whose plans the console shows"
    return 0
  fi
  for i in "${!CHECKOUTS[@]}"; do
    path=${CHECKOUTS[$i]}
    case "$(printf '%s' "$path" | tr '[:upper:]' '[:lower:]')" in
    *belong*)
      fact belong FAIL "the checkout named is $path, which names the owner's own installation's tree: no check script here reads it"
      stop "Check C0 never reads a checkout under 'belong'. Name a checkout you mean to profile"
      ;;
    esac
    if [ -d "$path/docs" ]; then
      CHECKOUT_BAD[$i]=""
      good="${good}${good:+, }$path"
    else
      CHECKOUT_BAD[$i]=not-a-checkout
      bad="${bad}${bad:+, }$path"
    fi
  done
  if [ -n "$bad" ]; then
    unmeasured checkout "$bad is not a checkout this check can walk: the plan read walks \`docs/\` in the directory it is run from, and there is none there. Its plan row reads FAIL and the run ends 2; the other reads are still measured"
    return 0
  fi
  fact checkout PASS "$good — the checkout(s) the plan read is run in, which is what makes it the console's own read (the console runs it in the repository's checkout)"
}

# guard_fugaro: the build is the subject, so it has to answer at all. A `fugaro`
# that cannot print its version cannot answer any of the reads below, and every
# row would be a FAIL for a reason that says nothing about the page.
guard_fugaro() {
  local v status=0
  v=$("$FUGARO_BIN" version 2>"$SCRATCH/version.err" | head -n 1) || status=$?
  if [ "$status" -ne 0 ] || [ -z "$v" ]; then
    stop "fugaro version did not answer (exit $status), so this run cannot tell which build it would be profiling: $(head -n 2 "$SCRATCH/version.err" 2>/dev/null | tr '\n' ' ')"
  fi
  fact fugaro-version PASS "$v — the installed build whose reads these numbers are. A number measured on another build is another build's"
}

# ------------------------------------------------------------------- the steps
# step_runs: the listing at the three windows the spec's row is about, and the
# same 24 h argv at the two page sizes the plan and the spec each name. All five
# are made against the FIRST installation named: the window's cost is a function
# of the bucket, not of the installation the console happens to have selected.
step_runs() {
  local project=${PROJECTS[0]}
  read_runs runs-24h "$project" 24h
  read_runs runs-7d "$project" 7d
  read_runs runs-all "$project" 0
  read_runs runs-limit50 "$project" 24h --limit 50
  read_runs runs-limit20 "$project" 24h --limit 20
}

read_runs() { # read_runs KEY PROJECT SINCE [EXTRA...]
  local key=$1 project=$2 since=$3
  shift 3
  local display="fugaro runs ls --json --since $since${1:+ $*}"
  read_run "$key" "$display" "$NEUTRAL_CWD" env FUGARO_PROJECT="$project" \
    "$FUGARO_BIN" runs ls --json --since "$since" "$@"
  finish_read "$key" "$display (FUGARO_PROJECT=$project)" runs
}

# step_coordinator: one read per installation named, because the document's size
# is a function of the repository count and that is the curve the spec's `~724
# KB` is one point of. The row names the count the document itself carried.
step_coordinator() {
  local name
  for name in "${PROJECTS[@]}"; do
    read_coordinator "$name"
  done
}

read_coordinator() { # read_coordinator PROJECT
  local name=$1 key="coordinator-$name"
  local display="fugaro coordinator status --from-store --all --json"
  read_run "$key" "$display" "$NEUTRAL_CWD" env FUGARO_PROJECT="$name" \
    "$FUGARO_BIN" coordinator status --from-store --all --json
  finish_read "$key" "$display (FUGARO_PROJECT=$name, the installation whose repositories it lists)" view
}

# step_plan: the one read that has to run inside a checkout. Its row is per
# checkout, and a checkout that is not there is recorded rather than skipped.
step_plan() {
  local i key
  if [ "${#CHECKOUTS[@]}" -eq 0 ]; then
    unmeasured plan-status "the plan read was not made: no checkout was named and the current directory is not one, so there is nothing for it to walk. Pass --checkout <path>"
    return 0
  fi
  for i in "${!CHECKOUTS[@]}"; do
    key="plan-status-$((i + 1))"
    if [ -n "${CHECKOUT_BAD[$i]:-}" ]; then
      unmeasured "$key" "the plan read was not made in ${CHECKOUTS[$i]}: that path is not a checkout (it has no docs/), so the walk would read nothing and its number would be a number about an empty directory"
      continue
    fi
    read_plan "$key" "${CHECKOUTS[$i]}"
  done
}

read_plan() { # read_plan KEY PATH
  local key=$1 path=$2
  local display="fugaro plan status --json --all"
  read_run "$key" "$display" "$path" env FUGARO_PROJECT="${PROJECTS[0]}" \
    "$FUGARO_BIN" plan status --json --all
  finish_read "$key" "$display (run in $path, which is what the console does)" plan
}

# step_budget: the fleet's budget document, as the console's budget alert reads
# it. One read: `--all` is the fleet, and `--repo` per repository is the same
# document narrowed, which is not a second measurement of anything.
step_budget() {
  local display="fugaro budget show --json"
  read_run budget-show "$display" "$NEUTRAL_CWD" env FUGARO_PROJECT="${PROJECTS[0]}" \
    "$FUGARO_BIN" budget show --json
  finish_read budget-show "$display (FUGARO_PROJECT=${PROJECTS[0]})" budget
}

step_readings() {
  local n
  n=$(grep -c . "$READINGS_FILE" 2>/dev/null || true)
  if [ "${n:-0}" -eq 0 ]; then
    fact readings FAIL "no read was timed, so there is nothing to paste into the spec's cells"
    return 0
  fi
  fact readings PASS "$n read(s) timed and sized — the table below is what spec.md §2.2's cells are filled from: the bytes are the document's own length on stdout and the seconds are the whole spawn's wall clock, which is the read the console makes"
}

# -------------------------------------------------------------------- the plan
# print_plan: every command a real run would make, in order, and what its
# Restore would do. Printed by --dry-run INSTEAD of running anything: this
# check's own test pins that a dry run makes no fugaro call at all.
print_plan() {
  local name path key
  say "CHECK C0 plan (dry run: nothing below is run, and no row of the block was measured)"
  say "  [local] fugaro version — the installed build whose reads these numbers are"
  for name in "${PROJECTS[@]}"; do
    say "  [network, read] FUGARO_PROJECT=$name fugaro coordinator status --from-store --all --json — the stored view of every repository that installation's config lists, out of the project's database. One read per --project, which is how the size/repository-count curve is read"
  done
  say "  [network, read] FUGARO_PROJECT=${PROJECTS[0]} fugaro runs ls --json --since 24h — the window the page's action queue reads, and the one the spec's ~5.8 s was measured for"
  say "  [network, read] FUGARO_PROJECT=${PROJECTS[0]} fugaro runs ls --json --since 7d — the same listing at a week"
  say "  [network, read] FUGARO_PROJECT=${PROJECTS[0]} fugaro runs ls --json --since 0 — the same listing with no window at all (every run the bucket holds)"
  say "  [network, read] FUGARO_PROJECT=${PROJECTS[0]} fugaro runs ls --json --since 24h --limit 50 — the same 24 h window with a page asked for: --limit shortens the listing, not the read, and the pair of numbers is what says what a page saves. This is D818's first page, the one the plan's C0 row names"
  say "  [network, read] FUGARO_PROJECT=${PROJECTS[0]} fugaro runs ls --json --since 24h --limit 20 — the same window at D810's page size, which is what spec.md §2.2's halted-or-failed-job cell reads where its size says (unmeasured). The plan and the spec name different limits and this check settles neither: it measures both"
  say "  [network, read] FUGARO_PROJECT=${PROJECTS[0]} fugaro budget show --json — the fleet's budget document, the read behind the page's budget alert"
  if [ "${#CHECKOUTS[@]}" -eq 0 ]; then
    say "  [not run] the plan read: no --checkout was given and the current directory is not a checkout, so there is nothing to walk. A real run records that row as FAIL and ends 2"
  else
    for path in "${CHECKOUTS[@]}"; do
      say "  [network, read, in $path] fugaro plan status --json --all — the plan walk over that checkout's docs/, joining the runs bucket and asking the forge about the pull requests its tasks carry (one gh per joined pull request, at most 32). The one read that has to run inside a checkout"
    done
  fi
  say "  [local] size and time each answer: the bytes are the document's own length on stdout, and the seconds are bash's own TIMEFORMAT around the spawn (no GNU date is needed). Each document is counted by one python3 reader that prints counts and never a document, and scanned for credential-shaped text before any of its own words is echoed"
  say "  [nothing created] no bucket write, no database write, no job, no credential: every command above is a read, and this check has no write to undo. Its Restore removes the scratch directory"
  fact dry-run PASS "the plan was printed and nothing was run: no fugaro command, no read of any bucket or database, no number and no verdict"
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# `FAIL — not reached`, and any other recorded key follows in the order the run
# recorded it.
BLOCK_KEYS=()
READ_KEYS=(runs-24h runs-7d runs-all runs-limit50 runs-limit20)
build_block_keys() {
  local name i
  BLOCK_KEYS=(fugaro-version xtrace project checkout belong "${READ_KEYS[@]}")
  for name in "${PROJECTS[@]}"; do BLOCK_KEYS+=("coordinator-$name"); done
  if [ "${#CHECKOUTS[@]}" -eq 0 ]; then
    BLOCK_KEYS+=(plan-status)
  else
    for i in "${!CHECKOUTS[@]}"; do BLOCK_KEYS+=("plan-status-$((i + 1))"); done
  fi
  BLOCK_KEYS+=(budget-show readings restore)
}

# human_size BYTES — the reading in the form the spec's cells carry it
# (`~724 KB`), which is what makes the table copyable rather than arithmetic.
human_size() { # human_size BYTES
  local b=$1
  case "$b" in '' | *[!0-9]*) printf '?'; return 0 ;; esac
  if [ "$b" -ge 1048576 ]; then
    printf '%d.%d MB' "$((b / 1048576))" "$(((b % 1048576) * 10 / 1048576))"
  elif [ "$b" -ge 1024 ]; then
    printf '%d.%d KB' "$((b / 1024))" "$(((b % 1024) * 10 / 1024))"
  else
    printf '%d B' "$b"
  fi
}

# about_secs SECONDS — one decimal, the way a spec cell reads it (`about 5.8 s`).
about_secs() { # about_secs SECONDS
  case "$1" in '' | *[!0-9.]*) printf '?'; return 0 ;; esac
  printf '%.1f' "$1"
}

# print_readings: the table the owner pastes, and one CELL line per read in the
# wording spec.md's cells need. Written from the captured readings, because the
# scratch directory is gone by the time the block is printed.
print_readings() {
  local captured=$1
  [ -n "$captured" ] || return 0
  printf '\nC0 READINGS — bytes and seconds per read\n' >&3
  printf '%-52s %10s %9s %8s  %s\n' READ BYTES SIZE SECONDS 'WHAT IT READ' >&3
  local key display bytes secs what
  while IFS='|' read -r key display bytes secs what; do
    [ -n "$key" ] || continue
    printf '%-52s %10s %9s %8s  %s\n' "$display" "$bytes" "$(human_size "$bytes")" "${secs:-0}" "$what" >&3
  done <<<"$captured"
  printf '\nCELLS — one line per read, in the form spec.md §2.2 needs (paste the numbers; edit the cells yourself)\n' >&3
  while IFS='|' read -r key display bytes secs what; do
    [ -n "$key" ] || continue
    printf '  `%s` — size about %s, time about %s s (%s)\n' "$display" "$(human_size "$bytes")" "$(about_secs "$secs")" "$what" >&3
  done <<<"$captured"
}

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK C0 RESULT\n' >&3
  for key in "${BLOCK_KEYS[@]}"; do
    local line
    line=$(printf '%s\n' "$facts" | grep -m1 "^$key|" || true)
    if [ -n "$line" ]; then
      printf 'FACT %s: %s — %s\n' "$key" \
        "$(printf '%s' "$line" | cut -d'|' -f2)" \
        "$(printf '%s' "$line" | cut -d'|' -f3-)" >&3
    elif [ -n "$DRY_RUN" ]; then
      printf 'FACT %s: INFO — not run: --dry-run prints the commands and measures nothing\n' "$key" >&3
    else
      printf 'FACT %s: FAIL — not reached (the run stopped before this step)\n' "$key" >&3
    fi
    printed="$printed$key"$'\n'
  done
  printf '%s\n' "$facts" | while IFS='|' read -r key verdict detail; do
    [ -n "$key" ] || continue
    case $'\n'"$printed" in *$'\n'"$key"$'\n'*) continue ;; esac
    printf 'FACT %s: %s — %s\n' "$key" "$verdict" "$detail" >&3
  done
  print_readings "$READINGS_CAPTURED"
  printf 'CHECK C0 RESULT END (exit %s)\n' "$status" >&3
}

on_signal() {
  owner "interrupted: printing what was measured and cleaning up"
  exit 2
}

# ---------------------------------------------------------------------- inputs
PROJECTS=()
CHECKOUTS=()
DRY_RUN=${CHECK_C0_DRY_RUN:-}

while [ $# -gt 0 ]; do
  case "$1" in
  --project)
    # An empty name is refused here rather than measured: FUGARO_PROJECT="" is
    # the default installation, which is not one this check was told to read.
    [ $# -ge 2 ] && [ -n "$2" ] || die "--project needs a Fugaro project name. usage: $USAGE"
    PROJECTS+=("$2")
    shift 2
    ;;
  --checkout)
    [ $# -ge 2 ] && [ -n "$2" ] || die "--checkout needs a path to a repository checkout. usage: $USAGE"
    CHECKOUTS+=("$2")
    shift 2
    ;;
  --dry-run)
    DRY_RUN=1
    shift
    ;;
  -h | --help)
    sed -n '2,/^set -Eeuo/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&3
    exit 0
    ;;
  *)
    die "unknown argument '$1'. usage: $USAGE"
    ;;
  esac
done
if [ "${#PROJECTS[@]}" -eq 0 ]; then
  die "--project is required: it names the installation whose reads are timed, and this check never picks one for itself. usage: $USAGE"
fi
# The checkout the plan read runs in: the one(s) named, else the current
# directory when it is a checkout. Decided here, before the scratch directory
# exists, so the block's keys and the plan are the same shape either way.
if [ "${#CHECKOUTS[@]}" -eq 0 ] && [ -d "${PWD:-}/docs" ]; then
  CHECKOUTS=("$PWD")
fi

FUGARO_BIN=$(command -v fugaro || true)

main() {
  need_tools
  scratch_up
  trap on_error ERR
  write_files
  build_block_keys
  guard_xtrace
  guard_projects
  guard_checkouts
  if [ -n "$DRY_RUN" ]; then
    print_plan
    return 0
  fi
  guard_fugaro
  step_runs
  step_coordinator
  step_plan
  step_budget
  step_readings
}

write_files() {
  cat >"$SCRATCH/reader.py" <<'READER_EOF'
#!/usr/bin/env python3
"""Check C0's reader: it counts what a document holds and prints no document.

One subcommand per read this check makes — `runs`, `view`, `plan` and `budget` —
each printing `json=ok`, then `what=` (the counts, as one phrase the readings
table carries), then the individual counts, and `problem=<code>` when the
document is not the shape this check reads. The code names a KEY PATH and a TYPE
and never a value out of the document.

`scan` reads the files it is given for credential-shaped text and prints the
KINDS it found and how many, never the text: a pull request's title is text
somebody else wrote, and a match there must not reach the log you paste back.

The one value printed out of a document is a COUNT. Nothing else is printed, so
nothing that reaches the block can be a document's own words.
"""
import json
import re
import sys


def fail(message):
    print("error=%s" % message, file=sys.stderr)
    sys.exit(1)


def load(path):
    try:
        raw = open(path, encoding="utf-8").read()
    except OSError as e:
        fail("cannot read the read's output: %s" % e.strerror)
    try:
        return json.loads(raw)
    except ValueError as e:
        # The position alone: json's message quotes nothing of the document,
        # and this keeps it that way whatever a later Python does.
        at = re.search(r"line \d+ column \d+", str(e))
        fail("not JSON%s" % (" at " + at.group(0) if at else ""))


def kind_of(v):
    if isinstance(v, bool):
        return "bool"
    if isinstance(v, dict):
        return "object"
    if isinstance(v, list):
        return "list"
    if isinstance(v, str):
        return "string"
    if isinstance(v, int):
        return "number"
    if v is None:
        return "null"
    return "value"


def field(doc, key):
    """A key the shape needs, or the code that says it is not the one read."""
    if key not in doc:
        return None, key + "-missing"
    if not isinstance(doc[key], list):
        return None, key + "-not-a-" + kind_of(doc[key])
    return doc[key], None


def count(v):
    return v if isinstance(v, int) and not isinstance(v, bool) else 0


def cmd_runs(path):
    doc = load(path)
    if not isinstance(doc, dict):
        return "document-not-an-object", None, []
    runs, problem = field(doc, "runs")
    if problem:
        return problem, None, []
    return None, "runs %d" % len(runs), ["runs=%d" % len(runs)]


def cmd_view(path):
    doc = load(path)
    if not isinstance(doc, dict):
        return "document-not-an-object", None, []
    repos, problem = field(doc, "repos")
    if problem:
        return problem, None, []
    records = runs = prs = stored = stale = 0
    for entry in repos:
        if not isinstance(entry, dict):
            return "entry-not-an-object", None, []
        if entry.get("stored") is True:
            stored += 1
        if entry.get("stale") is not None:
            stale += 1
        view = entry.get("view")
        if not isinstance(view, dict):
            return "entry-view-not-an-object", None, []
        recs, problem = field(view, "records")
        if problem:
            return "view-" + problem, None, []
        records += len(recs)
        for rec in recs:
            if not isinstance(rec, dict):
                return "record-not-an-object", None, []
            r = rec.get("runs")
            runs += len(r) if isinstance(r, list) else 0
            if rec.get("pr") is not None:
                prs += 1
    what = "repos %d, records %d, runs %d, pull requests %d, stored %d, stale %d" % (
        len(repos), records, runs, prs, stored, stale)
    return None, what, [
        "repos=%d" % len(repos), "records=%d" % records, "runs=%d" % runs,
        "prs=%d" % prs, "stored=%d" % stored, "stale=%d" % stale,
    ]


def cmd_plan(path):
    doc = load(path)
    if not isinstance(doc, dict):
        return "document-not-an-object", None, []
    plans, problem = field(doc, "plans")
    if problem:
        return problem, None, []
    totals = doc.get("totals")
    if not isinstance(totals, dict):
        return "totals-not-an-object", None, []
    tasks, opened, closed = count(totals.get("tasks")), count(totals.get("open")), count(totals.get("closed"))
    what = "plans %d, tasks %d, open %d, closed %d" % (len(plans), tasks, opened, closed)
    return None, what, [
        "plans=%d" % len(plans), "tasks=%d" % tasks, "open=%d" % opened, "closed=%d" % closed,
    ]


def cmd_budget(path):
    doc = load(path)
    if not isinstance(doc, dict):
        return "document-not-an-object", None, []
    repos, problem = field(doc, "repos")
    if problem:
        return problem, None, []
    running, problem = field(doc, "running")
    if problem:
        return problem, None, []
    what = "repos %d, running %d" % (len(repos), len(running))
    return None, what, ["repos=%d" % len(repos), "running=%d" % len(running)]


# The credential-shaped text this check refuses to see in a read's own output.
# The kinds are named and the text is never printed.
CREDENTIALS = (
    ("bearer", re.compile(r"(?i)\bbearer\s+[A-Za-z0-9._~+/-]{8,}")),
    ("pem-block", re.compile(r"-----BEGIN [A-Z ]*PRIVATE KEY-----")),
    ("google-access-token", re.compile(r"\bya29\.[A-Za-z0-9._-]{10,}")),
    ("google-api-key", re.compile(r"\bAIza[0-9A-Za-z_-]{30,}")),
    ("github-token", re.compile(r"\b(ghp|gho|ghs|ghr|github_pat)_[A-Za-z0-9_]{20,}")),
    ("slack-token", re.compile(r"\bxox[abprs]-[A-Za-z0-9-]{10,}")),
    ("jwt", re.compile(r"\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{4,}")),
    ("key-assignment", re.compile(
        r"(?i)\b(secret|token|password|passwd|api[_-]?key|private[_-]?key|client[_-]?secret)\b\s*[:=]\s*[\"']?[A-Za-z0-9._~+/-]{16,}")),
    ("opaque-run", re.compile(r"\b[A-Za-z0-9+/]{60,}={0,2}\b")),
    ("hex-run", re.compile(r"\b[0-9a-fA-F]{64,}\b")),
)


def cmd_scan(paths):
    text = ""
    for path in paths:
        try:
            text += open(path, encoding="utf-8", errors="replace").read()
        except OSError:
            # A read that answered nothing has no output to scan; that is the
            # row's own story, not a reason to fail the scan.
            continue
    kinds = []
    found = 0
    for kind, rx in CREDENTIALS:
        matches = rx.findall(text)
        if matches:
            kinds.append(kind)
            found += len(matches)
    print("creds=%s" % ("+".join(kinds) if kinds else "none"))
    print("count=%d" % found)


COMMANDS = {"runs": cmd_runs, "view": cmd_view, "plan": cmd_plan, "budget": cmd_budget}


def main(argv):
    if not argv:
        fail("no subcommand")
    if argv[0] == "scan":
        cmd_scan(argv[1:])
        return
    if argv[0] not in COMMANDS or len(argv) != 2:
        fail("usage: reader.py {runs|view|plan|budget} FILE | scan FILE...")
    problem, what, counts = COMMANDS[argv[0]](argv[1])
    print("json=ok")
    for line in counts:
        print(line)
    if problem:
        print("problem=%s" % problem)
    if what:
        print("what=%s" % what)


main(sys.argv[1:])
READER_EOF
}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
